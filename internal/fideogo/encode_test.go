package fideogo

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// testConfig is a representative software-encode config used across tests.
func testConfig() compressionConfig {
	return compressionConfig{
		maxConcurrent: 2,
		codec:         "libx264",
		preset:        "medium",
		crf:           "28",
		audioBitrate:  "96k",
		resolution:    "1080",
		hwQuality:     "65",
	}
}

// containsSeq reports whether sub appears as a contiguous subsequence of args,
// so adjacency between a flag and its value (e.g. -c:v libx264) is asserted.
func containsSeq(args []string, sub ...string) bool {
	for i := 0; i+len(sub) <= len(args); i++ {
		match := true
		for j, s := range sub {
			if args[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestFFmpegArgs(t *testing.T) {
	hwConfig := testConfig()
	hwConfig.hwAccel = true
	hwConfig.hwEncoder = "h264_videotoolbox"

	tests := []struct {
		name   string
		config compressionConfig
		out    string
		want   [][]string // contiguous sequences that must be present
		absent []string   // tokens that must NOT be present
	}{
		{
			name:   "mp4 software h264/aac with faststart",
			config: testConfig(),
			out:    "out_video.mp4",
			want: [][]string{
				{"-c:v", "libx264"},
				{"-preset", "medium"},
				{"-crf", "28"},
				{"-c:a", "aac"},
				{"-b:a", "96k"},
				{"-movflags", "+faststart"},
			},
			absent: []string{"libvpx-vp9", "libopus"},
		},
		{
			name:   "webm forces vp9/opus and no faststart",
			config: testConfig(),
			out:    "out_video.webm",
			want: [][]string{
				{"-c:v", "libvpx-vp9"},
				{"-b:v", "0"},
				{"-c:a", "libopus"},
				{"-b:a", "96k"},
			},
			absent: []string{"libx264", "aac", "+faststart", "h264_videotoolbox"},
		},
		{
			name:   "mkv h264/aac without faststart",
			config: testConfig(),
			out:    "out_video.mkv",
			want:   [][]string{{"-c:v", "libx264"}, {"-c:a", "aac"}},
			absent: []string{"+faststart", "libvpx-vp9"},
		},
		{
			name:   "mov gets faststart",
			config: testConfig(),
			out:    "out_video.mov",
			want:   [][]string{{"-movflags", "+faststart"}, {"-c:v", "libx264"}},
		},
		{
			name:   "hw videotoolbox mp4 uses q:v and skips thread cap",
			config: hwConfig,
			out:    "out_video.mp4",
			want:   [][]string{{"-c:v", "h264_videotoolbox"}, {"-q:v", "65"}},
			absent: []string{"-threads", "-preset", "libx264"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := newVideoService(tt.config)
			// Light source (h264/1080p) so decode hwaccel is not added here.
			args := vs.ffmpegArgs("input.mov", tt.out, videoMetadata{codec: "h264", height: "1080"})

			if len(args) == 0 || args[len(args)-1] != tt.out {
				t.Fatalf("output path must be last arg; got %v", args)
			}
			if !contains(args, "-i") {
				t.Errorf("missing -i in %v", args)
			}
			if !contains(args, "-vf") {
				t.Errorf("missing -vf scale filter in %v", args)
			}
			for _, seq := range tt.want {
				if !containsSeq(args, seq...) {
					t.Errorf("missing sequence %v in %v", seq, args)
				}
			}
			for _, tok := range tt.absent {
				if contains(args, tok) {
					t.Errorf("unexpected token %q in %v", tok, args)
				}
			}
		})
	}
}

// TestFFmpegArgsGolden pins the full argument list for each container and the
// hardware path. Where TestFFmpegArgs asserts that key flag sequences are present,
// these golden cases assert the complete list — order, adjacency, and absence of
// stray flags — via cmp.Diff, which prints a readable diff when ffmpegArgs drifts.
// The -threads value is computed (not hard-coded) so software expectations stay
// exact yet portable across machines with different core counts. All cases use a
// light source (h264/1080p) so no hwaccel decode flags are prepended here; the
// decode prefix is covered exactly by TestFFmpegArgsHeavySourceExact.
func TestFFmpegArgsGolden(t *testing.T) {
	threads := strconv.Itoa(autoThreadsPerJob(testConfig().maxConcurrent))

	hwConfig := testConfig()
	hwConfig.hwAccel = true
	hwConfig.hwEncoder = "h264_videotoolbox"

	tests := []struct {
		name   string
		config compressionConfig
		out    string
		want   []string
	}{
		{
			name:   "mp4 software h264/aac",
			config: testConfig(),
			out:    "out_video.mp4",
			want: []string{
				"-i", "input.mov",
				"-c:v", "libx264", "-preset", "medium", "-crf", "28", "-threads", threads,
				"-vf", "scale=-2:'min(1080,ih)'",
				"-c:a", "aac", "-b:a", "96k",
				"-movflags", "+faststart",
				"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.mp4",
			},
		},
		{
			name:   "mov software gets faststart",
			config: testConfig(),
			out:    "out_video.mov",
			want: []string{
				"-i", "input.mov",
				"-c:v", "libx264", "-preset", "medium", "-crf", "28", "-threads", threads,
				"-vf", "scale=-2:'min(1080,ih)'",
				"-c:a", "aac", "-b:a", "96k",
				"-movflags", "+faststart",
				"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.mov",
			},
		},
		{
			name:   "mkv software no faststart",
			config: testConfig(),
			out:    "out_video.mkv",
			want: []string{
				"-i", "input.mov",
				"-c:v", "libx264", "-preset", "medium", "-crf", "28", "-threads", threads,
				"-vf", "scale=-2:'min(1080,ih)'",
				"-c:a", "aac", "-b:a", "96k",
				"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.mkv",
			},
		},
		{
			name:   "webm vp9/opus no faststart",
			config: testConfig(),
			out:    "out_video.webm",
			want: []string{
				"-i", "input.mov",
				"-c:v", "libvpx-vp9", "-crf", "28", "-b:v", "0", "-row-mt", "1", "-threads", threads,
				"-vf", "scale=-2:'min(1080,ih)'",
				"-c:a", "libopus", "-b:a", "96k",
				"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.webm",
			},
		},
		{
			name:   "mp4 hardware videotoolbox skips thread cap",
			config: hwConfig,
			out:    "out_video.mp4",
			want: []string{
				"-i", "input.mov",
				"-c:v", "h264_videotoolbox", "-q:v", "65",
				"-vf", "scale=-2:'min(1080,ih)'",
				"-c:a", "aac", "-b:a", "96k",
				"-movflags", "+faststart",
				"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.mp4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := newVideoService(tt.config)
			args := vs.ffmpegArgs("input.mov", tt.out, videoMetadata{codec: "h264", height: "1080"})
			if diff := cmp.Diff(tt.want, args); diff != "" {
				t.Errorf("ffmpegArgs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFFmpegArgsHeavySourceExact pins the complete argument list for a heavy
// source, including the hardware decode prefix that must precede -i. The expected
// prefix is derived from runtime.GOOS (VideoToolbox on macOS, auto elsewhere) so
// the assertion is exact on every platform the test runs on.
func TestFFmpegArgsHeavySourceExact(t *testing.T) {
	cfg := testConfig()
	vs := newVideoService(cfg)
	args := vs.ffmpegArgs("input.mkv", "out_video.mp4", videoMetadata{codec: "hevc", height: "2160"})

	threads := strconv.Itoa(autoThreadsPerJob(cfg.maxConcurrent))
	decodePrefix := []string{"-hwaccel", "auto"}
	if runtime.GOOS == "darwin" {
		decodePrefix = []string{"-hwaccel", "videotoolbox"}
	}

	want := append(append([]string{}, decodePrefix...),
		"-i", "input.mkv",
		"-c:v", "libx264", "-preset", "medium", "-crf", "28", "-threads", threads,
		"-vf", "scale=-2:'min(1080,ih)'",
		"-c:a", "aac", "-b:a", "96k",
		"-movflags", "+faststart",
		"-progress", "pipe:1", "-loglevel", "error", "-y", "out_video.mp4",
	)

	if diff := cmp.Diff(want, args); diff != "" {
		t.Errorf("ffmpegArgs mismatch (-want +got):\n%s", diff)
	}
}

// TestBuildFFmpegCommandString asserts the final command line — the literal
// string that gets executed — names the ffmpeg binary as argv[0] and carries
// exactly the ffmpegArgs vector after it. cmd.Args (not cmd.Path) is checked so
// the assertion is independent of where ffmpeg is installed on the host.
func TestBuildFFmpegCommandString(t *testing.T) {
	cfg := testConfig()
	vs := newVideoService(cfg)
	meta := videoMetadata{codec: "h264", height: "1080"}

	cmd := vs.buildFFmpegCommand(context.Background(), "input.mov", "out_video.mp4", meta)

	if cmd.Args[0] != "ffmpeg" {
		t.Errorf("argv[0] = %q, want \"ffmpeg\"", cmd.Args[0])
	}

	wantArgs := vs.ffmpegArgs("input.mov", "out_video.mp4", meta)
	wantLine := "ffmpeg " + strings.Join(wantArgs, " ")
	if got := strings.Join(cmd.Args, " "); got != wantLine {
		t.Errorf("command line mismatch:\n got: %s\nwant: %s", got, wantLine)
	}
}

func TestFFmpegArgsHardwareDecodePrecedesInput(t *testing.T) {
	vs := newVideoService(testConfig())
	// Heavy source (hevc) must trigger hwaccel decode flags, placed before -i.
	args := vs.ffmpegArgs("input.mkv", "out.mp4", videoMetadata{codec: "hevc", height: "2160"})

	hwIdx := indexOf(args, "-hwaccel")
	iIdx := indexOf(args, "-i")
	if hwIdx < 0 {
		t.Fatalf("expected -hwaccel decode flag for heavy source, got %v", args)
	}
	if iIdx < 0 || hwIdx > iIdx {
		t.Errorf("-hwaccel must precede -i; got hwaccel@%d i@%d in %v", hwIdx, iIdx, args)
	}
}

func TestDecodeArgs(t *testing.T) {
	tests := []struct {
		name  string
		meta  videoMetadata
		heavy bool
	}{
		{"light h264 1080p", videoMetadata{codec: "h264", height: "1080"}, false},
		{"heavy hevc 1080p", videoMetadata{codec: "hevc", height: "1080"}, true},
		{"heavy by resolution 1440p h264", videoMetadata{codec: "h264", height: "1440"}, true},
		{"av1 720p", videoMetadata{codec: "av1", height: "720"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeArgs(tt.meta)
			if tt.heavy && len(got) == 0 {
				t.Errorf("expected hwaccel decode args, got none")
			}
			if !tt.heavy && len(got) != 0 {
				t.Errorf("expected no decode args, got %v", got)
			}
		})
	}
}

func TestProfileAllowsHW(t *testing.T) {
	tests := []struct {
		container string
		allowsHW  bool
	}{
		{"mp4", true},
		{"mov", true},
		{"m4v", true},
		{"mkv", true},
		{"webm", false},
	}
	for _, tt := range tests {
		t.Run(tt.container, func(t *testing.T) {
			if got := profileFor(tt.container).allowsHW; got != tt.allowsHW {
				t.Errorf("profileFor(%q).allowsHW = %v, want %v", tt.container, got, tt.allowsHW)
			}
		})
	}
}

func TestHWEncoderArgs(t *testing.T) {
	tests := []struct {
		encoder string
		want    []string
	}{
		{"h264_nvenc", []string{"-c:v", "h264_nvenc", "-cq", "28"}},
		{"h264_qsv", []string{"-c:v", "h264_qsv", "-global_quality", "28"}},
		{"h264_amf", []string{"-c:v", "h264_amf", "-rc", "cqp"}},
		{"h264_videotoolbox", []string{"-c:v", "h264_videotoolbox", "-q:v", "65"}},
	}
	for _, tt := range tests {
		t.Run(tt.encoder, func(t *testing.T) {
			c := compressionConfig{hwEncoder: tt.encoder, crf: "28", hwQuality: "65"}
			got := hwEncoderArgs(c)
			if !containsSeq(got, tt.want...) {
				t.Errorf("hwEncoderArgs(%q) = %v, want seq %v", tt.encoder, got, tt.want)
			}
		})
	}
}

func TestContainerOf(t *testing.T) {
	tests := map[string]string{
		"out_video.mp4":   "mp4",
		"/path/to/X.WEBM": "webm",
		"a.MKV":           "mkv",
	}
	for in, want := range tests {
		if got := containerOf(in); got != want {
			t.Errorf("containerOf(%q) = %q, want %q", in, got, want)
		}
	}
}
