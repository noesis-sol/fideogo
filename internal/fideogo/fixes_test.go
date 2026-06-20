package fideogo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noopCancel is a stand-in cancel func for populating m.cancels in tests that
// exercise the in-flight bookkeeping without launching real workers.
func noopCancel() {}

// TestFillSlotsSkipsLaunchedFile guards the double-start fix: a file already
// registered in m.cancels (launched, but its processingStartMsg not yet handled,
// so still statusPending) must NOT be picked again by fillSlots — otherwise a
// second ffmpeg worker would start on the same input.
func TestFillSlotsSkipsLaunchedFile(t *testing.T) {
	m := model{
		files:   []videoFile{{path: "a.mp4", name: "a.mp4", selected: true, status: statusPending}},
		cancels: map[int]context.CancelFunc{0: noopCancel}, // file 0 already launched
		config:  compressionConfig{maxConcurrent: 2},
	}
	_, cmds := m.fillSlots(-1, nil)
	if len(cmds) != 0 {
		t.Fatalf("launched-but-unconfirmed file was re-selected: got %d cmds, want 0", len(cmds))
	}
}

// TestFillSlotsCapacityCountsInFlight verifies the capacity gate uses the true
// in-flight count (len(m.cancels)), so a launched-but-unconfirmed file still
// occupies a slot and maxConcurrent is never exceeded during the launch window.
func TestFillSlotsCapacityCountsInFlight(t *testing.T) {
	m := model{
		files: []videoFile{
			{path: "a.mp4", selected: true, status: statusPending},
			{path: "b.mp4", selected: true, status: statusPending},
			{path: "c.mp4", selected: true, status: statusPending},
		},
		// Two slots already occupied by launched files; maxConcurrent is 2.
		cancels: map[int]context.CancelFunc{0: noopCancel, 1: noopCancel},
		config:  compressionConfig{maxConcurrent: 2},
	}
	_, cmds := m.fillSlots(-1, nil)
	if len(cmds) != 0 {
		t.Fatalf("fillSlots exceeded capacity: got %d cmds, want 0 (2/2 slots already in flight)", len(cmds))
	}
}

// TestFillSlotsPromptsOnDistinctInPlaceDest covers the --overwrite --format
// data-loss fix: when an in-place conversion's destination is a different,
// pre-existing file, fillSlots must raise the overwrite prompt instead of
// silently clobbering it (and must not launch the encode while prompting).
func TestFillSlotsPromptsOnDistinctInPlaceDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mov")
	dst := filepath.Join(dir, "clip.mp4") // pre-existing, unrelated file
	for _, p := range []string{src, dst} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := model{
		files:   []videoFile{{path: src, name: "clip.mov", selected: true, status: statusPending}},
		cancels: map[int]context.CancelFunc{},
		config:  compressionConfig{maxConcurrent: 2, inPlace: true, outputFormat: "mp4"},
	}
	m, cmds := m.fillSlots(-1, nil)
	if !m.showOverwritePrompt {
		t.Error("expected overwrite prompt for in-place conversion onto a pre-existing distinct file")
	}
	if m.pendingOutputFile != dst {
		t.Errorf("pendingOutputFile = %q, want %q", m.pendingOutputFile, dst)
	}
	if len(cmds) != 0 {
		t.Errorf("no encode should start while the overwrite prompt is pending; got %d cmds", len(cmds))
	}
}

// TestMarkInPlaceCollisions covers the convergent-destination fix: two selected
// sources that map to the same in-place destination are both flagged as errors,
// while an unrelated file is left to process.
func TestMarkInPlaceCollisions(t *testing.T) {
	m := model{
		files: []videoFile{
			{path: "clip.mov", name: "clip.mov", selected: true, status: statusPending, outPath: "claimed"},
			{path: "clip.mp4", name: "clip.mp4", selected: true, status: statusPending},
			{path: "other.mkv", name: "other.mkv", selected: true, status: statusPending},
		},
		cancels: map[int]context.CancelFunc{},
		config:  compressionConfig{inPlace: true, outputFormat: "mp4"},
	}
	m.markInPlaceCollisions()

	if m.files[0].status != statusError || m.files[1].status != statusError {
		t.Errorf("both converging sources should be errored; got %v and %v", m.files[0].status, m.files[1].status)
	}
	if m.files[0].err == nil || m.files[1].err == nil {
		t.Error("colliding files should carry an explanatory error")
	}
	if m.files[0].outPath != "" {
		t.Errorf("colliding file should release its output claim; got %q", m.files[0].outPath)
	}
	if m.files[2].status != statusPending {
		t.Errorf("non-colliding file should stay pending; got %v", m.files[2].status)
	}
}

// TestMarkInPlaceCollisionsPlainOverwrite verifies that plain --overwrite (no
// --format) never reports a collision, since each destination equals its own
// unique source.
func TestMarkInPlaceCollisionsPlainOverwrite(t *testing.T) {
	m := model{
		files: []videoFile{
			{path: "a.mov", selected: true, status: statusPending},
			{path: "a.mp4", selected: true, status: statusPending},
		},
		cancels: map[int]context.CancelFunc{},
		config:  compressionConfig{inPlace: true, outputFormat: ""},
	}
	m.markInPlaceCollisions()
	for i, f := range m.files {
		if f.status != statusPending {
			t.Errorf("file %d should stay pending under plain --overwrite; got %v", i, f.status)
		}
	}
}

// TestStartProcessingResetsBatchState covers the stale-footer fix: starting a new
// batch clears m.done and m.completedCount left over from a prior batch.
func TestStartProcessingResetsBatchState(t *testing.T) {
	m := model{
		files:          []videoFile{{path: "a.mp4", selected: false, status: statusDone}},
		cancels:        map[int]context.CancelFunc{},
		config:         compressionConfig{maxConcurrent: 2},
		done:           true,
		completedCount: 5,
	}
	m.startProcessing() // nothing selected: resets state, launches nothing
	if m.done {
		t.Error("startProcessing should clear m.done for the new batch")
	}
	if m.completedCount != 0 {
		t.Errorf("startProcessing should reset completedCount; got %d", m.completedCount)
	}
}

// TestCollectVideosFromArgLiteralMetachar covers the glob-metacharacter fix: a
// real file whose name literally contains glob metacharacters is reachable as an
// explicit argument (and pre-selected, matching plain explicit-file behavior).
func TestCollectVideosFromArgLiteralMetachar(t *testing.T) {
	dir := t.TempDir()
	name := "clip[1].mp4"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := collectVideosFromArg(path)
	if err != nil {
		t.Fatalf("collectVideosFromArg(%q) errored: %v", path, err)
	}
	if len(files) != 1 || files[0].name != name {
		t.Fatalf("expected the literal bracketed file, got %v", files)
	}
	if !files[0].selected {
		t.Error("an explicitly named file should be pre-selected")
	}
}

// TestCollectVideosFromArgGlobStillWorks verifies the literal-first change does
// not break real glob patterns (no literal file is named "*.mov").
func TestCollectVideosFromArgGlobStillWorks(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.mov", "b.mov", "note.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := collectVideosFromArg(filepath.Join(dir, "*.mov"))
	if err != nil {
		t.Fatalf("glob arg errored: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 .mov matches, got %d: %v", len(files), files)
	}
}

// TestEvenHeightScaleFilter pins the parity-safe scale expression so an odd
// source height can't reach libx264 and abort the encode.
func TestEvenHeightScaleFilter(t *testing.T) {
	vs := newVideoService(testConfig())
	args := vs.ffmpegArgs("in.mov", "out.mp4", videoMetadata{codec: "h264", height: "405"})
	i := indexOf(args, "-vf")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("no -vf filter in %v", args)
	}
	got := args[i+1]
	want := "scale=-2:'2*trunc(min(1080,ih)/2)'"
	if got != want {
		t.Errorf("scale filter = %q, want %q (must force an even height)", got, want)
	}
	if strings.Contains(got, "min(1080,ih)'") && !strings.Contains(got, "trunc") {
		t.Error("scale filter passes odd height through; libx264 would abort")
	}
}
