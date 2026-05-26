package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// videoService encapsulates video processing operations.
type videoService struct {
	config compressionConfig
}

func newVideoService(config compressionConfig) *videoService {
	return &videoService{config: config}
}

// timeRegex extracts the microsecond timestamp from ffmpeg's -progress output.
var timeRegex = regexp.MustCompile(`out_time_ms=(\d+)`)

// getOutputPath returns the output path for a given input file.
// If outputFormat is non-empty, the extension is replaced with the target format.
func getOutputPath(inputPath, outputFormat string) string {
	dir := filepath.Dir(inputPath)
	base := filepath.Base(inputPath)
	if outputFormat != "" {
		ext := filepath.Ext(base)
		base = strings.TrimSuffix(base, ext) + "." + outputFormat
	}
	return filepath.Join(dir, outputPrefix+base)
}

func outputFileExists(inputPath, outputFormat string) bool {
	output := getOutputPath(inputPath, outputFormat)
	_, err := os.Stat(output)
	return err == nil
}

func (vs *videoService) buildFFmpegCommand(ctx context.Context, inputPath, outputPath string) *exec.Cmd {
	args := []string{"-i", inputPath}

	if vs.config.hwAccel {
		// VideoToolbox: hardware-accelerated, ignores -preset/-crf, uses -q:v (0-100, higher = better)
		args = append(args, "-c:v", "h264_videotoolbox", "-q:v", vs.config.hwQuality)
	} else {
		args = append(args, "-c:v", vs.config.codec, "-preset", vs.config.preset, "-crf", vs.config.crf)
		// Cap CPU threads per job so concurrent ffmpegs don't thrash. HW encoders
		// don't benefit from this since they offload to the media engine.
		args = append(args, "-threads", strconv.Itoa(autoThreadsPerJob(vs.config.maxConcurrent)))
	}

	args = append(args,
		"-vf", "scale=-2:'min("+vs.config.resolution+",ih)'",
		"-c:a", "aac", "-b:a", vs.config.audioBitrate,
	)

	// movflags is only valid for MP4/MOV containers.
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(outputPath), "."))
	if ext == "mp4" || ext == "mov" || ext == "m4v" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-progress", "pipe:1", "-loglevel", "error", "-y", outputPath)
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

// processFile spawns a worker goroutine that probes the input, runs ffmpeg, and
// streams progress/done/error/cancel messages back to the Bubble Tea program.
// The worker holds no reference to the model — only the snapshotted path,
// outputFormat, videoService, and cancel context.
func (m *model) processFile(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.files) {
		return func() tea.Msg {
			return errorMsg{idx: idx, err: fmt.Errorf("invalid file index: %d", idx)}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[idx] = cancel

	path := m.files[idx].path
	outputFormat := m.config.outputFormat
	vs := m.videoService

	go func() {
		defer cancel()

		meta, err := vs.probeMetadata(path)
		if err != nil {
			program.Send(errorMsg{idx: idx, err: fmt.Errorf("failed to probe video: %w", err)})
			return
		}
		if meta.width != "" && meta.height != "" {
			program.Send(videoInfoMsg{idx: idx, info: vs.formatVideoInfo(path, meta)})
		}

		output := getOutputPath(path, outputFormat)
		duration := meta.duration

		cmd := vs.buildFFmpegCommand(ctx, path, output)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			program.Send(errorMsg{idx: idx, err: fmt.Errorf("failed to create stdout pipe: %w", err)})
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			program.Send(errorMsg{idx: idx, err: fmt.Errorf("failed to create stderr pipe: %w", err)})
			return
		}

		if err := cmd.Start(); err != nil {
			program.Send(errorMsg{idx: idx, err: fmt.Errorf("failed to start ffmpeg: %w", err)})
			return
		}

		program.Send(processingStartMsg{idx: idx})

		var stderrBuf strings.Builder

		progressDone := make(chan struct{})
		go func() {
			defer close(progressDone)
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				matches := timeRegex.FindStringSubmatch(line)
				if len(matches) <= 1 {
					continue
				}
				timeMs, err := strconv.ParseInt(matches[1], 10, 64)
				if err != nil {
					continue
				}
				timeSec := float64(timeMs) / 1000000
				prog := 0.5
				if duration > 0 {
					prog = timeSec / duration
					if prog > 1 {
						prog = 1
					}
				}
				program.Send(progressMsg{idx: idx, progress: prog})
			}
		}()

		stderrDone := make(chan struct{})
		go func() {
			defer close(stderrDone)
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				stderrBuf.WriteString(sc.Text())
				stderrBuf.WriteString("\n")
			}
		}()

		waitErr := cmd.Wait()
		<-progressDone
		<-stderrDone

		if waitErr != nil {
			// Context cancellation = user cancel. Clean up partial output.
			if ctx.Err() != nil {
				if removeErr := os.Remove(output); removeErr != nil && !os.IsNotExist(removeErr) {
					program.Send(errorMsg{idx: idx, err: fmt.Errorf("cleanup failed: %w", removeErr)})
					return
				}
				program.Send(cancelMsg{idx: idx})
				return
			}

			errMsg := fmt.Sprintf("ffmpeg failed: %v", waitErr)
			if so := stderrBuf.String(); so != "" {
				errMsg = fmt.Sprintf("%s\nDetails: %s", errMsg, strings.TrimSpace(so))
			}
			program.Send(errorMsg{idx: idx, err: fmt.Errorf("%s", errMsg)})
			return
		}

		if outInfo := vs.getVideoInfo(output); outInfo != "" {
			program.Send(outputInfoMsg{idx: idx, info: outInfo})
		}
		program.Send(doneMsg{idx: idx})
	}()

	return nil
}
