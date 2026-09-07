package fideogo

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParseProbeOutput covers ffprobe parsing, including the audio-only fix: a
// source with no video stream is refused with a clear error instead of being
// handed to ffmpeg to fail on the scale filter.
func TestParseProbeOutput(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    videoMetadata
		wantErr bool
	}{
		{
			"stream then format; first positive duration wins",
			"codec_name=h264\nwidth=1920\nheight=1080\nduration=12.5\nbit_rate=4000000\nduration=12.6\n",
			videoMetadata{codec: "h264", width: "1920", height: "1080", bitrate: "4000000", duration: 12.5}, false,
		},
		{
			"N/A stream duration falls through to the format duration",
			"codec_name=vp9\nwidth=1280\nheight=720\nduration=N/A\nbit_rate=N/A\nduration=30.0\n",
			videoMetadata{codec: "vp9", width: "1280", height: "720", bitrate: "N/A", duration: 30}, false,
		},
		{"audio-only source has no video stream", "bit_rate=128000\nduration=200.0\n", videoMetadata{}, true},
		{"empty output", "", videoMetadata{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProbeOutput([]byte(tt.out))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProbeOutput(%q) = %+v, want an error", tt.out, got)
				}
				if !strings.Contains(err.Error(), "no video stream") {
					t.Errorf("error %q should say there is no video stream", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProbeOutput(%q) unexpected error: %v", tt.out, err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(videoMetadata{})); diff != "" {
				t.Errorf("parseProbeOutput mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWithStderr verifies a failed command's captured stderr is folded into its
// error (last line only), and that other errors pass through untouched.
func TestWithStderr(t *testing.T) {
	plain := errors.New("boom")
	if got := withStderr(plain); got != plain {
		t.Errorf("withStderr(plain) = %v, want the same error back", got)
	}

	ee := &exec.ExitError{Stderr: []byte("[mov] moov atom not found\nclip.mp4: Invalid data found when processing input\n")}
	got := withStderr(ee).Error()
	if !strings.HasSuffix(got, "clip.mp4: Invalid data found when processing input") {
		t.Errorf("withStderr should append ffprobe's last stderr line; got %q", got)
	}
	if strings.Contains(got, "moov atom") {
		t.Errorf("withStderr should keep only the last line; got %q", got)
	}
	if !errors.As(withStderr(ee), new(*exec.ExitError)) {
		t.Error("withStderr must wrap, not replace, the ExitError")
	}
}

func TestLastLine(t *testing.T) {
	tests := map[string]string{
		"":                     "",
		"single":               "single",
		"a\nb\n":               "b",
		"a\n  b with space \n": "b with space",
		"\n\n":                 "",
	}
	for in, want := range tests {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}
