package fideogo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestDecodeArgsPerOS exercises every GOOS branch of decodeArgs on a single host
// by injecting goos, which runtime.GOOS would otherwise pin to the build target.
func TestDecodeArgsPerOS(t *testing.T) {
	heavy := videoMetadata{codec: "hevc", height: "1080"}
	light := videoMetadata{codec: "h264", height: "1080"}

	tests := []struct {
		name string
		meta videoMetadata
		goos string
		want []string
	}{
		{"darwin heavy -> videotoolbox", heavy, "darwin", []string{"-hwaccel", "videotoolbox"}},
		{"linux heavy -> auto", heavy, "linux", []string{"-hwaccel", "auto"}},
		{"windows heavy -> auto", heavy, "windows", []string{"-hwaccel", "auto"}},
		{"freebsd heavy -> auto", heavy, "freebsd", []string{"-hwaccel", "auto"}},
		{"darwin light -> none", light, "darwin", nil},
		{"linux light -> none", light, "linux", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, decodeArgs(tt.meta, tt.goos)); diff != "" {
				t.Errorf("decodeArgs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestHWEncoderCandidatesPerOS pins the per-platform encoder preference order.
func TestHWEncoderCandidatesPerOS(t *testing.T) {
	tests := []struct {
		goos string
		want []string
	}{
		{"darwin", []string{"h264_videotoolbox"}},
		{"linux", []string{"h264_nvenc", "h264_qsv", "h264_amf"}},
		{"windows", []string{"h264_nvenc", "h264_qsv", "h264_amf"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, hwEncoderCandidates(tt.goos)); diff != "" {
				t.Errorf("hwEncoderCandidates(%q) mismatch (-want +got):\n%s", tt.goos, diff)
			}
		})
	}
}

// writeTestFile creates path (and any parent dirs) with the given contents,
// used to lay out a synthetic /dev + /sys device tree under a temp root.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDeviceProbeAvailable drives the Linux GPU-node detection against a fake
// filesystem root, so the per-vendor logic is covered without a real GPU or a
// Linux host. The non-linux short-circuit is checked too.
func TestDeviceProbeAvailable(t *testing.T) {
	allEncoders := []string{"h264_nvenc", "h264_qsv", "h264_amf"}

	t.Run("non-linux is always available", func(t *testing.T) {
		d := deviceProbe{goos: "darwin", root: t.TempDir()} // empty tree, never read
		for _, enc := range allEncoders {
			if !d.available(enc) {
				t.Errorf("darwin: %s should be reported available", enc)
			}
		}
	})

	t.Run("linux empty tree -> none available", func(t *testing.T) {
		d := deviceProbe{goos: "linux", root: t.TempDir()}
		for _, enc := range allEncoders {
			if d.available(enc) {
				t.Errorf("linux empty: %s should be unavailable", enc)
			}
		}
	})

	t.Run("nvidia node present -> only nvenc available", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "dev/nvidia0"), "")
		d := deviceProbe{goos: "linux", root: root}
		if !d.available("h264_nvenc") {
			t.Error("nvenc should be available with /dev/nvidia0 present")
		}
		if d.available("h264_qsv") {
			t.Error("qsv should be unavailable without an Intel render node")
		}
	})

	t.Run("intel render node -> qsv yes, amf no", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "dev/dri/renderD128"), "")
		writeTestFile(t, filepath.Join(root, "sys/class/drm/renderD128/device/vendor"), "0x8086\n")
		d := deviceProbe{goos: "linux", root: root}
		if !d.available("h264_qsv") {
			t.Error("qsv should be available with an Intel (0x8086) render node")
		}
		if d.available("h264_amf") {
			t.Error("amf should be unavailable when the only node is Intel")
		}
	})

	t.Run("amd render node -> amf available", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "dev/dri/renderD129"), "")
		writeTestFile(t, filepath.Join(root, "sys/class/drm/renderD129/device/vendor"), "0x1002")
		d := deviceProbe{goos: "linux", root: root}
		if !d.available("h264_amf") {
			t.Error("amf should be available with an AMD (0x1002) render node")
		}
	})
}

// TestOrderByDevicePresence verifies the encoder whose GPU node is present is
// floated to the front while the rest keep their relative order.
func TestOrderByDevicePresence(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "dev/dri/renderD128"), "")
	writeTestFile(t, filepath.Join(root, "sys/class/drm/renderD128/device/vendor"), "0x1002") // AMD
	probe := deviceProbe{goos: "linux", root: root}

	got := orderByDevicePresence([]string{"h264_nvenc", "h264_qsv", "h264_amf"}, probe)
	want := []string{"h264_amf", "h264_nvenc", "h264_qsv"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("orderByDevicePresence mismatch (-want +got):\n%s", diff)
	}
}
