//go:build integration

package fideogo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestEncodeIntegration generates a tiny clip with ffmpeg, runs the real encode
// pipeline (probe -> buildFFmpegCommand -> ffmpeg), and verifies the output is a
// valid, resolution-capped H.264 file. Gated behind the `integration` build tag
// and skipped when ffmpeg/ffprobe are absent, so plain `go test` stays hermetic.
//
//	go test -tags=integration ./...
func TestEncodeIntegration(t *testing.T) {
	requireBinaries(t, "ffmpeg", "ffprobe")

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")

	// Synthesize a 1s 320x240 test pattern + tone so probe/encode have real input.
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generating source clip failed: %v\n%s", err, out)
	}

	cfg := compressionConfig{
		maxConcurrent: 1,
		codec:         "libx264",
		preset:        "ultrafast", // speed over ratio; this is a correctness test
		crf:           "28",
		audioBitrate:  "96k",
		resolution:    "240",
		hwQuality:     "65",
		outputFormat:  "mp4",
	}
	vs := newVideoService(cfg)

	meta, err := vs.probeMetadata(src)
	if err != nil {
		t.Fatalf("probing source failed: %v", err)
	}
	if meta.codec != "h264" {
		t.Fatalf("source codec = %q, want h264", meta.codec)
	}

	out := filepath.Join(dir, "out.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if b, err := vs.buildFFmpegCommand(ctx, src, out, meta).CombinedOutput(); err != nil {
		t.Fatalf("encode failed: %v\n%s", err, b)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}

	outMeta, err := vs.probeMetadata(out)
	if err != nil {
		t.Fatalf("probing output failed: %v", err)
	}
	if outMeta.codec != "h264" {
		t.Errorf("output codec = %q, want h264", outMeta.codec)
	}
	if outMeta.height != "240" {
		t.Errorf("output height = %q, want 240 (capped, not upscaled)", outMeta.height)
	}
}

// requireBinaries skips the test unless every named executable is on PATH.
func requireBinaries(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not found on PATH; skipping integration test", n)
		}
	}
}
