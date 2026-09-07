package fideogo

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestProcessingStartUnderCancelKeepsSlot guards the stale-cancel fix. When
// 'c' is pressed while a worker's processingStartMsg is still in flight, the
// handler must NOT release the worker's m.cancels slot or declare the batch
// drained: only the worker's own terminal cancelMsg may do that. Releasing it
// early let the user restart, register a new worker under the same index, and
// then had the late cancelMsg delete the new worker's cancel — orphaning it.
func TestProcessingStartUnderCancelKeepsSlot(t *testing.T) {
	cancelled := 0
	m := model{
		files:      []videoFile{{path: "a.mp4", name: "a.mp4", selected: true}},
		cancels:    map[int]context.CancelFunc{0: func() { cancelled++ }},
		config:     compressionConfig{maxConcurrent: 2},
		processing: true,
	}

	m, _ = m.handleKeyPress(key("c"))
	if cancelled == 0 {
		t.Fatal("'c' must cancel the in-flight worker")
	}

	// The worker's processingStartMsg lands after the cancel.
	m, _ = m.handleProcessingStart(processingStartMsg{idx: 0})
	if _, ok := m.cancels[0]; !ok {
		t.Fatal("slot released before the worker's terminal cancelMsg; a restart could now be orphaned")
	}
	if !m.processing {
		t.Fatal("batch declared drained while a worker is still winding down")
	}
	if m.batchSettled() {
		t.Fatal("batch must not settle while the worker is still registered")
	}
	// fillSlots must not launch a second worker for the still-registered file.
	if _, cmds := m.fillSlots(-1, nil); len(cmds) != 0 {
		t.Fatalf("fillSlots relaunched a file that is still in flight (%d cmds)", len(cmds))
	}

	// Only the worker's own terminal message settles the batch.
	m, _ = m.handleCancel(cancelMsg{idx: 0})
	if len(m.cancels) != 0 {
		t.Errorf("cancelMsg should release the slot; %d still registered", len(m.cancels))
	}
	if m.processing {
		t.Error("batch should be settled after the last worker's cancelMsg")
	}
	if m.files[0].status != statusPending || m.files[0].outPath != "" {
		t.Errorf("cancelled file should be reset to pending with no output claim; got status %v outPath %q", m.files[0].status, m.files[0].outPath)
	}
}

// TestBatchJobs pins the thread-budget divisor: in-flight plus queued files,
// capped at maxConcurrent, never below one.
func TestBatchJobs(t *testing.T) {
	tests := []struct {
		name                            string
		maxConcurrent, inFlight, queued int
		want                            int
	}{
		{"single file", 4, 0, 1, 1},
		{"batch below the cap", 4, 0, 2, 2},
		{"batch at the cap", 4, 0, 4, 4},
		{"batch above the cap", 4, 0, 10, 4},
		{"in-flight files count too", 4, 3, 1, 4},
		{"last file while others run below the cap", 4, 1, 1, 2},
		{"nothing queued still one", 4, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{cancels: map[int]context.CancelFunc{}, config: compressionConfig{maxConcurrent: tt.maxConcurrent}}
			for i := 0; i < tt.inFlight; i++ {
				m.files = append(m.files, videoFile{selected: true})
				m.cancels[i] = func() {}
			}
			for i := 0; i < tt.queued; i++ {
				m.files = append(m.files, videoFile{selected: true})
			}
			m.files = append(m.files, videoFile{selected: false}, videoFile{selected: true, status: statusDone})
			if got := m.batchJobs(); got != tt.want {
				t.Errorf("batchJobs() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestNavigationWhileProcessing covers the tall-list fix: the cursor can be
// moved (and so the list scrolled) in every state, including mid-batch, while
// selection stays locked until the batch is over.
func TestNavigationWhileProcessing(t *testing.T) {
	m := model{
		files:      make([]videoFile, 30),
		cancels:    map[int]context.CancelFunc{},
		config:     compressionConfig{maxConcurrent: 2},
		processing: true,
		height:     12,
	}
	press := func(k string) {
		t.Helper()
		m, _ = m.handleKeyPress(key(k))
	}

	press("down")
	if m.cursor != 1 {
		t.Fatalf("down while processing: cursor = %d, want 1", m.cursor)
	}
	press("space")
	if m.files[1].selected {
		t.Error("space must not toggle selection while processing")
	}
	press("end")
	if m.cursor != 29 {
		t.Errorf("end: cursor = %d, want 29", m.cursor)
	}
	press("home")
	if m.cursor != 0 {
		t.Errorf("home: cursor = %d, want 0", m.cursor)
	}
	press("pgdown")
	after := m.cursor
	if after <= 0 || after >= 29 {
		t.Errorf("pgdown from the top should land inside the list; cursor = %d", after)
	}
	press("pgup")
	if m.cursor >= after {
		t.Errorf("pgup should move up from %d; cursor = %d", after, m.cursor)
	}
	press("pgup")
	if m.cursor != 0 {
		t.Errorf("pgup near the top must clamp to 0; cursor = %d", m.cursor)
	}
	press("G")
	if m.cursor != 29 {
		t.Errorf("G: cursor = %d, want 29", m.cursor)
	}
	press("g")
	if m.cursor != 0 {
		t.Errorf("g: cursor = %d, want 0", m.cursor)
	}
	press("up")
	if m.cursor != 0 {
		t.Errorf("up at the top must clamp; cursor = %d", m.cursor)
	}
}
