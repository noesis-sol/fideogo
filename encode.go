package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
// Modern ffmpeg emits out_time_us; older builds emit only out_time_ms, which —
// despite its name — is a historical mislabel that also carries microseconds.
// Matching either keeps the progress bar working on the older ffmpeg that many
// Linux distros ship; both keys are interpreted as microseconds.
var timeRegex = regexp.MustCompile(`out_time_(?:us|ms)=(\d+)`)

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

func (vs *videoService) buildFFmpegCommand(ctx context.Context, inputPath, outputPath string, meta videoMetadata) *exec.Cmd {
	container := strings.ToLower(strings.TrimPrefix(filepath.Ext(outputPath), "."))

	// Hardware-accelerated decode flags must precede -i to apply to the input.
	args := decodeArgs(meta)
	args = append(args, "-i", inputPath)
	args = append(args, vs.videoArgs(container)...)
	args = append(args, "-vf", "scale=-2:'min("+vs.config.resolution+",ih)'")
	args = append(args, vs.audioArgs(container)...)

	// movflags is only valid for MP4/MOV containers.
	if container == "mp4" || container == "mov" || container == "m4v" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-progress", "pipe:1", "-loglevel", "error", "-y", outputPath)
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

// heavyDecodeCodecs are modern codecs whose software decode is CPU-expensive
// enough that offloading to a hardware decoder is worthwhile. Lightweight
// sources (e.g. H.264 at 1080p) decode cheaply, so we leave them on software
// to avoid the setup cost and quirks of the hardware path.
var heavyDecodeCodecs = map[string]bool{
	"av1":  true,
	"hevc": true,
	"h265": true,
	"vp9":  true,
}

// decodeArgs returns hardware-accelerated decode flags to place before -i.
//
// Added selectively in two senses: (1) only for heavy sources — 1440p+ or a
// modern codec — where software decode actually dominates CPU time; and (2) the
// accelerator is chosen per platform so the tool still builds and runs on any
// POSIX system. macOS uses VideoToolbox; elsewhere we let ffmpeg auto-select an
// available accelerator. Hardware decode is best-effort: if the device can't
// decode this codec (or none exists), ffmpeg falls back to software on its own,
// so this never turns a working encode into a failing one.
func decodeArgs(meta videoMetadata) []string {
	height, _ := strconv.Atoi(meta.height)
	heavy := height >= 1440 || heavyDecodeCodecs[strings.ToLower(meta.codec)]
	if !heavy {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		return []string{"-hwaccel", "videotoolbox"}
	default:
		return []string{"-hwaccel", "auto"}
	}
}

// hwEncoderCandidates lists the H.264 hardware encoders we try, per platform, in
// preference order. VAAPI is intentionally omitted: it requires uploading frames
// to the GPU (format=nv12,hwupload + scale_vaapi), which is incompatible with our
// software scale filter and would need a separate filter pipeline.
func hwEncoderCandidates() []string {
	if runtime.GOOS == "darwin" {
		return []string{"h264_videotoolbox"}
	}
	// NVIDIA, then Intel QuickSync, then AMD AMF.
	return []string{"h264_nvenc", "h264_qsv", "h264_amf"}
}

// resolveHWEncoder picks a platform-appropriate hardware encoder. It first
// narrows the candidates to those ffmpeg was compiled with, then prefers one
// whose matching GPU device is actually present — so a build that bundles e.g.
// h264_nvenc on a non-NVIDIA machine doesn't shadow the GPU that's really there.
// If the device probe finds no match (or can't see device nodes, e.g. inside a
// container), it falls back to the first compiled-in encoder and lets ffmpeg
// surface any device error per file rather than guessing wrong here.
func resolveHWEncoder() (string, error) {
	candidates := hwEncoderCandidates()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return "", fmt.Errorf("could not query ffmpeg encoders: %w", err)
	}
	available := string(out)

	var compiled []string
	for _, enc := range candidates {
		if strings.Contains(available, enc) {
			compiled = append(compiled, enc)
		}
	}
	if len(compiled) == 0 {
		return "", fmt.Errorf("no hardware H.264 encoder available in this ffmpeg build (looked for: %s)", strings.Join(candidates, ", "))
	}

	for _, enc := range compiled {
		if hwDeviceAvailable(enc) {
			return enc, nil
		}
	}
	return compiled[0], nil
}

// hwDeviceAvailable reports whether the GPU backing the given encoder is present.
// Device nodes are only probeable on Linux; elsewhere (Windows, macOS) we can't
// cheaply tell, so we assume available and rely on ffmpeg to error if not.
func hwDeviceAvailable(encoder string) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	switch encoder {
	case "h264_nvenc":
		// NVIDIA driver exposes /dev/nvidia0, /dev/nvidia1, …
		matches, _ := filepath.Glob("/dev/nvidia[0-9]*")
		return len(matches) > 0
	case "h264_qsv":
		return drmRenderVendorPresent("0x8086") // Intel
	case "h264_amf":
		return drmRenderVendorPresent("0x1002") // AMD
	}
	return true
}

// drmRenderVendorPresent reports whether any DRM render node (/dev/dri/renderD*)
// is backed by a GPU with the given PCI vendor ID (read from sysfs). Used to tell
// Intel (QSV) and AMD (AMF) render nodes apart, since the node path alone doesn't
// identify the vendor.
func drmRenderVendorPresent(vendorID string) bool {
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	for _, node := range nodes {
		name := filepath.Base(node) // e.g. renderD128
		data, err := os.ReadFile("/sys/class/drm/" + name + "/device/vendor")
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(data)), vendorID) {
			return true
		}
	}
	return false
}

// hwEncoderArgs returns the video-codec arguments for the resolved hardware
// encoder. Each encoder exposes a different quality knob: VideoToolbox uses -q:v
// (0-100, higher = better); NVENC/QSV/AMF use a CRF-like quantizer (0-51, lower =
// better), for which we reuse the software CRF value.
func hwEncoderArgs(c compressionConfig) []string {
	switch c.hwEncoder {
	case "h264_nvenc":
		return []string{"-c:v", "h264_nvenc", "-cq", c.crf}
	case "h264_qsv":
		return []string{"-c:v", "h264_qsv", "-global_quality", c.crf}
	case "h264_amf":
		return []string{"-c:v", "h264_amf", "-rc", "cqp", "-qp_i", c.crf, "-qp_p", c.crf, "-qp_b", c.crf}
	default: // h264_videotoolbox
		return []string{"-c:v", "h264_videotoolbox", "-q:v", c.hwQuality}
	}
}

// videoArgs returns the video-codec ffmpeg arguments for the target container.
func (vs *videoService) videoArgs(container string) []string {
	// WebM only accepts VP8/VP9/AV1 video, so H.264 (including the
	// h264_videotoolbox HW encoder) is invalid here — always use VP9.
	// -b:v 0 puts libvpx-vp9 in constant-quality mode driven by -crf.
	if container == "webm" {
		return []string{
			"-c:v", "libvpx-vp9", "-crf", vs.config.crf, "-b:v", "0", "-row-mt", "1",
			"-threads", strconv.Itoa(autoThreadsPerJob(vs.config.maxConcurrent)),
		}
	}
	if vs.config.hwAccel {
		return hwEncoderArgs(vs.config)
	}
	// Cap CPU threads per job so concurrent ffmpegs don't thrash. HW encoders
	// don't benefit from this since they offload to the media engine.
	return []string{
		"-c:v", vs.config.codec, "-preset", vs.config.preset, "-crf", vs.config.crf,
		"-threads", strconv.Itoa(autoThreadsPerJob(vs.config.maxConcurrent)),
	}
}

// audioArgs returns the audio-codec ffmpeg arguments for the target container.
// WebM requires Vorbis/Opus rather than AAC.
func (vs *videoService) audioArgs(container string) []string {
	if container == "webm" {
		return []string{"-c:a", "libopus", "-b:a", vs.config.audioBitrate}
	}
	return []string{"-c:a", "aac", "-b:a", vs.config.audioBitrate}
}

// processFile spawns a worker goroutine that probes the input, runs ffmpeg, and
// streams progress/done/error/cancel messages back to the Bubble Tea program.
// The worker holds no reference to the model — only the snapshotted path,
// outputFormat, videoService, and the caller-owned cancel context.
//
// processFile is a read-only value-receiver method: the caller is responsible
// for creating the cancel context and registering it in m.cancels before
// invocation. This keeps state mutation out of a Cmd-returning method.
func (m model) processFile(idx int, ctx context.Context, cancel context.CancelFunc) tea.Cmd {
	if idx < 0 || idx >= len(m.files) {
		cancel()
		return func() tea.Msg {
			return errorMsg{idx: idx, err: fmt.Errorf("invalid file index: %d", idx)}
		}
	}

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

		cmd := vs.buildFFmpegCommand(ctx, path, output, meta)

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
			// Always drain stdout — leaving the pipe full would block ffmpeg.
			// Without a known duration we can't compute a percentage, so we
			// drain silently and let the spinner indicate activity rather
			// than pinning a misleading static value.
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if duration <= 0 {
					continue
				}
				matches := timeRegex.FindStringSubmatch(scanner.Text())
				if len(matches) <= 1 {
					continue
				}
				timeUs, err := strconv.ParseInt(matches[1], 10, 64)
				if err != nil {
					continue
				}
				prog := float64(timeUs) / 1_000_000 / duration
				if prog > 1 {
					prog = 1
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
			// Context cancellation = user cancel. Best-effort cleanup of the
			// partial output; a cleanup failure must not be reported as a
			// processing error, since the user explicitly asked to cancel.
			if ctx.Err() != nil {
				_ = os.Remove(output)
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
