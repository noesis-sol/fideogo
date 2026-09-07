package fideo

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func ones(n int) []int {
	xs := make([]int, n)
	for i := range xs {
		xs[i] = 1
	}
	return xs
}

// TestViewport pins the row-window arithmetic the tall-list fix relies on.
func TestViewport(t *testing.T) {
	tests := []struct {
		name               string
		lines              []int
		cursor, offset     int
		budget             int
		wantStart, wantEnd int
	}{
		{"unlimited budget shows all", ones(5), 0, 0, 0, 0, 5},
		{"everything fits shows all", ones(5), 4, 0, 5, 0, 5},
		{"top window reserves a bottom marker line", ones(10), 0, 0, 5, 0, 4},
		{"cursor below the window pins the bottom edge", ones(10), 7, 0, 5, 5, 8},
		{"scrolling one row past the window moves by one", ones(10), 8, 5, 5, 6, 9},
		{"window at the end backfills spare lines", ones(10), 9, 8, 5, 6, 10},
		{"tall rows are counted", []int{3, 3, 3, 3}, 0, 0, 5, 0, 1},
		{"a cursor row taller than the budget is still shown", []int{1, 9, 1}, 1, 0, 4, 1, 2},
		{"offset never starts below the cursor", ones(10), 2, 7, 5, 2, 5},
		{"empty list", nil, 0, 0, 5, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := viewport(tt.lines, tt.cursor, tt.offset, tt.budget)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("viewport = [%d, %d), want [%d, %d)", start, end, tt.wantStart, tt.wantEnd)
			}
			if len(tt.lines) > 0 && !(start <= tt.cursor && tt.cursor < end) {
				t.Errorf("cursor %d not inside [%d, %d)", tt.cursor, start, end)
			}
		})
	}
}

// TestViewFitsTerminalHeight renders a 40-file list into a 12-line terminal and
// checks the frame never exceeds the height (Bubble Tea's inline renderer would
// otherwise drop the top of it), that the cursor row is always visible, and
// that clipped edges are announced.
func TestViewFitsTerminalHeight(t *testing.T) {
	files := make([]videoFile, 40)
	for i := range files {
		files[i] = videoFile{name: fmt.Sprintf("clip-%02d.mp4", i)}
	}
	files[0] = videoFile{name: "done-00.mp4", status: statusDone, info: "1920x1080 | MP4 (h264)", outInfo: "1920x1080 | MP4 (h264)"}
	files[3] = videoFile{name: "err-03.mp4", status: statusError, err: errors.New("ffmpeg failed: exit status 1: boom")}
	m := newModel(files)
	m.width, m.height = 80, 12

	for _, cursor := range []int{0, 5, 20, 39} {
		m.cursor = cursor
		m.offset = m.scrollOffset()
		view := m.View()
		if lines := strings.Count(view, "\n") + 1; lines > m.height {
			t.Errorf("cursor %d: view is %d lines, terminal has %d:\n%s", cursor, lines, m.height, view)
		}
		if !strings.Contains(view, files[cursor].name) {
			t.Errorf("cursor %d: row %q is not visible:\n%s", cursor, files[cursor].name, view)
		}
	}

	m.cursor = 20
	m.offset = m.scrollOffset()
	view := m.View()
	if !strings.Contains(view, "↑") || !strings.Contains(view, "↓") {
		t.Errorf("clipped edges should be marked with ↑/↓ counts:\n%s", view)
	}

	// Unknown terminal size: nothing is clipped.
	m.width, m.height = 0, 0
	m.offset = m.scrollOffset()
	view = m.View()
	for _, f := range files {
		if !strings.Contains(view, f.name) {
			t.Fatalf("with an unknown height every row must be drawn; %q missing", f.name)
		}
	}
}

// TestRowLinesMatchesRender keeps the viewport's line accounting honest: for
// every row shape, rowLines must equal what renderFileRow actually emits — a
// multi-line error included, which is flattened onto one detail line.
func TestRowLinesMatchesRender(t *testing.T) {
	m := newModel(nil)
	m.width = 80
	cases := []videoFile{
		{name: "pending.mp4"},
		{name: "processing.mp4", status: statusProcessing, progress: 0.5},
		{name: "processing-info.mp4", status: statusProcessing, info: "1920x1080"},
		{name: "done.mp4", status: statusDone},
		{name: "done-full.mp4", status: statusDone, info: "in", outInfo: "out"},
		{name: "error.mp4", status: statusError, err: errors.New("ffmpeg failed: exit status 1\nDetails: multi\nline")},
	}
	for _, f := range cases {
		var s strings.Builder
		m.renderFileRow(&s, 0, f)
		if got, want := strings.Count(s.String(), "\n"), rowLines(f); got != want {
			t.Errorf("%s: renderFileRow emitted %d lines, rowLines says %d:\n%s", f.name, got, want, s.String())
		}
	}
}

// TestFitTruncates checks names and detail lines are cut to the terminal width
// (so rows never wrap) and left alone while the width is unknown.
func TestFitTruncates(t *testing.T) {
	long := strings.Repeat("a", 100) + ".mp4"
	m := model{width: 30}
	got := m.fit(long, rowReserved)
	if w := lipgloss.Width(got); w > 30-rowReserved {
		t.Errorf("fit produced %d cells, want at most %d", w, 30-rowReserved)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated text should end with an ellipsis; got %q", got)
	}
	if short := m.fit("short.mp4", rowReserved); short != "short.mp4" {
		t.Errorf("text that fits must be untouched; got %q", short)
	}
	if (model{}).fit(long, rowReserved) != long {
		t.Error("with an unknown width fit must be a no-op")
	}
}

// TestUpdateTracksScrollOffset drives the real Update loop: a resize followed
// by cursor moves must keep the cursor inside the drawn window and persist the
// scroll offset so the next move is incremental.
func TestUpdateTracksScrollOffset(t *testing.T) {
	files := make([]videoFile, 30)
	for i := range files {
		files[i] = videoFile{name: fmt.Sprintf("v%02d.mp4", i)}
	}
	var m tea.Model = newModel(files)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	for i := 0; i < 20; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	mm := m.(model)
	if mm.cursor != 20 {
		t.Fatalf("cursor = %d, want 20", mm.cursor)
	}
	start, end := mm.viewport()
	if !(start <= 20 && 20 < end) {
		t.Errorf("cursor 20 outside the drawn window [%d, %d)", start, end)
	}
	if mm.offset != start {
		t.Errorf("Update stored offset %d, viewport starts at %d", mm.offset, start)
	}
	if mm.progressBar.Width != 40 {
		t.Errorf("progress bar width = %d, want 40 on an 80-column terminal", mm.progressBar.Width)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	if w := m.(model).progressBar.Width; w != 20 {
		t.Errorf("progress bar width = %d on a 30-column terminal, want 20", w)
	}
}
