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
	"time"

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

// pathExists reports whether a file or directory exists at p.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolveOutputPath returns the output path for file idx, disambiguated so that
// no two inputs in the batch map to the same output. Distinct sources that share
// a stem (e.g. a.mp4 and a.mov both -> out_a.mp4 under --format mp4) would
// otherwise have concurrent ffmpeg workers write — and on cancel delete — the
// same file. The first claimant keeps the natural name; later ones fold in the
// source extension (out_a_mov.mp4), then a numeric suffix if still taken.
func (m model) resolveOutputPath(idx int) string {
	base := getOutputPath(m.files[idx].path, m.config.outputFormat)
	// Compare case-insensitively: macOS (APFS/HFS+) and Windows treat paths that
	// differ only in case as the SAME file, so two inputs whose stems differ only
	// in case (A.mp4 / a.mov) would otherwise each "claim" what is really one
	// physical output. Lowercasing the keys is also correct on case-sensitive
	// Linux (it only over-disambiguates the rare case-only-difference pair).
	claimed := make(map[string]bool)
	for j := range m.files {
		if j != idx && m.files[j].outPath != "" {
			claimed[strings.ToLower(m.files[j].outPath)] = true
		}
	}
	taken := func(p string) bool { return claimed[strings.ToLower(p)] }
	if !taken(base) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if srcExt := strings.TrimPrefix(strings.ToLower(filepath.Ext(m.files[idx].path)), "."); srcExt != "" {
		if cand := stem + "_" + srcExt + ext; !taken(cand) {
			return cand
		}
	}
	for n := 1; ; n++ {
		if cand := fmt.Sprintf("%s-%d%s", stem, n, ext); !taken(cand) {
			return cand
		}
	}
}

// containerOf returns the lowercased container extension of a path (e.g. "mp4").
func containerOf(outputPath string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(outputPath), "."))
}

// ffmpegArgs assembles the full ffmpeg argument list for one encode. It is a
// pure function of its inputs (no exec, no process), so the command can be
// unit-tested in isolation; buildFFmpegCommand only wraps it in an *exec.Cmd.
func (vs *videoService) ffmpegArgs(inputPath, outputPath string, meta videoMetadata) []string {
	p := profileFor(containerOf(outputPath))

	// Hardware-accelerated decode flags must precede -i to apply to the input.
	args := decodeArgs(meta)
	args = append(args, "-i", inputPath)
	args = append(args, p.video(vs.config)...)
	args = append(args, "-vf", "scale=-2:'min("+vs.config.resolution+",ih)'")
	args = append(args, p.audio(vs.config)...)
	args = append(args, p.muxFlags...)
	args = append(args, "-progress", "pipe:1", "-loglevel", "error", "-y", outputPath)
	return args
}

func (vs *videoService) buildFFmpegCommand(ctx context.Context, inputPath, outputPath string, meta videoMetadata) *exec.Cmd {
	return exec.CommandContext(ctx, "ffmpeg", vs.ffmpegArgs(inputPath, outputPath, meta)...)
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

	// A compiled-in encoder plus a matching GPU node is necessary but NOT
	// sufficient: h264_amf is listed and the AMD vendor ID matches even on a
	// Mesa-only system with no AMF runtime, and an Intel render node doesn't
	// imply a working QSV stack. So device presence is only a preference hint for
	// ordering; the gate is a best-effort probe-encode that tries to initialize
	// the encoder. We return the first candidate that encodes a few frames
	// without error, so a wrong guess can't make every real file fail.
	ordered := orderByDevicePresence(compiled)
	for _, enc := range ordered {
		switch probeEncode(enc) {
		case probeOK:
			return enc, nil
		case probeTimedOut:
			// Inconclusive: a cold/slow GPU init and a wedged driver both hit the
			// deadline, and we can't tell them apart. Don't reject (that would
			// wrongly downgrade a working-but-slow encoder to software) and don't
			// keep probing (that would let several wedged candidates stack up
			// timeouts and freeze startup). Use this device-present candidate and
			// let any genuine failure surface per file.
			return enc, nil
		}
		// probeFailed: encoder cleanly unavailable (fast error) — try the next.
	}
	// Every candidate cleanly failed. If the probe harness itself is unusable (a
	// stripped ffmpeg without the lavfi color source), the probes false-negative
	// every encoder — so don't force software. Fall back to the device-presence
	// heuristic (ordered[0]) and let any real per-file error surface.
	if !lavfiProbeUsable() {
		return ordered[0], nil
	}
	return "", fmt.Errorf("hardware encoder(s) %s are in this ffmpeg build but none could be initialized on this system", strings.Join(compiled, ", "))
}

// orderByDevicePresence puts encoders whose backing GPU node is detectable
// first, preserving relative order, so on a multi-GPU box we probe the encoder
// matching the GPU that's actually present before the rest.
func orderByDevicePresence(encoders []string) []string {
	var present, absent []string
	for _, enc := range encoders {
		if hwDeviceAvailable(enc) {
			present = append(present, enc)
		} else {
			absent = append(absent, enc)
		}
	}
	return append(present, absent...)
}

// probeResult is the outcome of a synthetic probe-encode. A clean failure
// (encoder genuinely unavailable) is treated very differently from a timeout
// (slow cold init or a wedged driver — indistinguishable within the deadline),
// so the caller can reject the former but optimistically use the latter.
type probeResult int

const (
	probeOK probeResult = iota
	probeFailed
	probeTimedOut
)

// hwProbeTimeout bounds each synthetic probe-encode. resolveHWEncoder runs
// before the TUI is drawn, so without a deadline a GPU driver that wedges during
// encoder init would hang startup with a blank terminal. A healthy encoder
// passes this 64x64/0.1s probe in well under a second; a genuinely unavailable
// one errors out fast — so only a slow/wedged init reaches the deadline.
const hwProbeTimeout = 6 * time.Second

// probeEncode runs a tiny synthetic encode with the given video codec and
// reports whether ffmpeg exited cleanly, failed fast, or hit the deadline.
func probeEncode(codec string) probeResult {
	ctx, cancel := context.WithTimeout(context.Background(), hwProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:d=0.1",
		"-c:v", codec, "-f", "null", "-")
	err := cmd.Run()
	if err == nil {
		return probeOK
	}
	if ctx.Err() == context.DeadlineExceeded {
		return probeTimedOut
	}
	return probeFailed
}

// lavfiProbeUsable reports whether the probe harness itself works — i.e. ffmpeg
// has the lavfi input + color source. rawvideo is always compiled in, so a
// failure here means lavfi/color is missing rather than any encoder being
// broken; callers use this to avoid false-negating every hardware encoder.
func lavfiProbeUsable() bool {
	return probeEncode("rawvideo") == probeOK
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

// containerProfile captures everything container-specific about an encode in one
// place: which video/audio codec arguments the container accepts, any muxer
// flags it needs, and whether a hardware H.264 encoder is usable for it.
// Consolidating these here keeps per-container decisions from scattering across
// the codec helpers, the command builder, and CLI validation.
type containerProfile struct {
	video    func(c compressionConfig) []string
	audio    func(c compressionConfig) []string
	muxFlags []string
	allowsHW bool
}

// profileFor returns the encoding profile for a container extension. Unknown
// containers fall back to the H.264/AAC family (same as mkv), the
// broadest-compatibility default.
func profileFor(container string) containerProfile {
	switch container {
	case "webm":
		// WebM only accepts VP8/VP9/AV1 video + Vorbis/Opus audio, so H.264
		// (including the h264_videotoolbox HW encoder) is never usable here.
		return containerProfile{video: vp9Video, audio: opusAudio, allowsHW: false}
	case "mp4", "mov", "m4v":
		// +faststart moves the moov atom to the front for progressive playback;
		// it is only valid for the ISO-BMFF (MP4/MOV) family.
		return containerProfile{
			video: h264Video, audio: aacAudio,
			muxFlags: []string{"-movflags", "+faststart"}, allowsHW: true,
		}
	default: // mkv and any unknown container
		return containerProfile{video: h264Video, audio: aacAudio, allowsHW: true}
	}
}

// h264Video returns the H.264 video arguments, preferring the resolved hardware
// encoder when hardware acceleration is enabled. Software encoding caps CPU
// threads per job so concurrent ffmpegs don't thrash; HW encoders skip the cap
// since they offload to the media engine.
func h264Video(c compressionConfig) []string {
	if c.hwAccel {
		return hwEncoderArgs(c)
	}
	return []string{
		"-c:v", c.codec, "-preset", c.preset, "-crf", c.crf,
		"-threads", strconv.Itoa(autoThreadsPerJob(c.maxConcurrent)),
	}
}

// vp9Video returns libvpx-vp9 arguments in constant-quality mode (-b:v 0 hands
// rate control to -crf). row-mt plus a per-job thread cap keep concurrent
// software encodes from thrashing.
func vp9Video(c compressionConfig) []string {
	return []string{
		"-c:v", "libvpx-vp9", "-crf", c.crf, "-b:v", "0", "-row-mt", "1",
		"-threads", strconv.Itoa(autoThreadsPerJob(c.maxConcurrent)),
	}
}

func aacAudio(c compressionConfig) []string {
	return []string{"-c:a", "aac", "-b:a", c.audioBitrate}
}

func opusAudio(c compressionConfig) []string {
	return []string{"-c:a", "libopus", "-b:a", c.audioBitrate}
}

// processFile spawns a worker goroutine that probes the input, runs ffmpeg, and
// streams progress/done/error/cancel messages back to the Bubble Tea program.
// The worker holds no reference to the model — only the snapshotted path,
// resolved output path, videoService, and the caller-owned cancel context.
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
	output := m.files[idx].outPath
	if output == "" {
		// Fallback: resolve directly if the slot filler didn't pre-assign one.
		output = getOutputPath(path, m.config.outputFormat)
	}
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
