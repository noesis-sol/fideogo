package fideogo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInPlaceDest(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		format string
		want   string
	}{
		{"no format keeps source path", "dir/clip.mov", "", "dir/clip.mov"},
		{"no format keeps webm", "a.webm", "", "a.webm"},
		{"explicit format swaps extension", "dir/clip.mov", "mp4", "dir/clip.mp4"},
		{"same format keeps name", "dir/clip.mp4", "mp4", "dir/clip.mp4"},
		{"absolute path swapped", "/v/clip.mkv", "mp4", "/v/clip.mp4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inPlaceDest(tt.input, tt.format); got != tt.want {
				t.Errorf("inPlaceDest(%q, %q) = %q, want %q", tt.input, tt.format, got, tt.want)
			}
		})
	}
}

func TestTempOutputPath(t *testing.T) {
	dest := filepath.Join("dir", "clip.mp4")
	got := tempOutputPath(dest, "dir/clip.mov")

	if filepath.Dir(got) != "dir" {
		t.Errorf("temp must sit next to dest; got dir %q", filepath.Dir(got))
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, ".") {
		t.Errorf("temp must be hidden; got %q", base)
	}
	if filepath.Ext(got) != ".mp4" {
		t.Errorf("temp must keep dest extension for muxer inference; got %q", got)
	}
	if !strings.Contains(base, tempOutputMarker) {
		t.Errorf("temp must carry %q so findVideos can skip it; got %q", tempOutputMarker, base)
	}
	if !strings.Contains(base, "mov") {
		t.Errorf("temp must fold in source container to stay batch-unique; got %q", base)
	}
}

// TestTempOutputPathUnique guards the convergent case: two sources mapping to the
// same in-place destination (a.mov and a.mp4 both under --format mp4) must not
// share one scratch temp, or concurrent workers would write the same file.
func TestTempOutputPathUnique(t *testing.T) {
	dest := "a.mp4"
	fromMov := tempOutputPath(dest, "a.mov")
	fromMp4 := tempOutputPath(dest, "a.mp4")
	if fromMov == fromMp4 {
		t.Errorf("temps for distinct sources collided: %q", fromMov)
	}
}

func TestResolveOutputPathInPlace(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		format string
		want   string
	}{
		{"keeps source when no format", "dir/clip.mov", "", "dir/clip.mov"},
		{"swaps extension under format", "dir/clip.mov", "mp4", "dir/clip.mp4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{
				files:  []videoFile{{path: tt.path}},
				config: compressionConfig{inPlace: true, outputFormat: tt.format},
			}
			got := m.resolveOutputPath(0)
			if got != tt.want {
				t.Errorf("resolveOutputPath = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, outputPrefix) {
				t.Errorf("in-place output must not carry the out_ prefix; got %q", got)
			}
		})
	}
}

func TestParseArgsOverwrite(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		overwrite bool
		paths     []string
	}{
		{"absent by default", []string{"video.mp4"}, false, []string{"video.mp4"}},
		{"long flag", []string{"--overwrite", "video.mp4"}, true, []string{"video.mp4"}},
		{"single dash alias", []string{"-overwrite", "video.mp4"}, true, []string{"video.mp4"}},
		{"order independent", []string{"video.mp4", "--overwrite"}, true, []string{"video.mp4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, paths, _, overwrite := parseArgs(tt.args)
			if overwrite != tt.overwrite {
				t.Errorf("overwrite = %v, want %v", overwrite, tt.overwrite)
			}
			if len(paths) != len(tt.paths) || (len(paths) > 0 && paths[0] != tt.paths[0]) {
				t.Errorf("paths = %v, want %v", paths, tt.paths)
			}
		})
	}
}

// TestFindVideosSkipsScratchTemps ensures a temp left behind by a crashed
// in-place encode is not offered up as a source video.
func TestFindVideosSkipsScratchTemps(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"clip.mp4", ".clip.mov" + tempOutputMarker + ".mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := findVideos(dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 video, got %d: %v", len(files), files)
	}
	if files[0].name != "clip.mp4" {
		t.Errorf("found %q, want clip.mp4 (scratch temp must be skipped)", files[0].name)
	}
}
