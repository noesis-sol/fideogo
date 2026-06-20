package fideogo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// startFile creates the per-file cancel context, registers it in m.cancels so
// the UI can abort the encode, and returns the launch cmd. Registering here
// keeps the invariant "an in-flight file is always in m.cancels" structural —
// the cancellation paths (handleDone/Error/Cancel, cancelAll) all rely on it.
func (m model) startFile(idx int) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[idx] = cancel
	return m.processFile(idx, ctx, cancel)
}

// fillSlots walks selected+unstarted files (skipping skipIdx, pass -1 for none),
// queuing processFile cmds up to remaining capacity. The overwrite prompt is
// modal: as soon as a conflict is captured we stop queuing new work and return,
// so the user resolves the prompt before anything else starts (the next
// fillSlots pass resumes after they choose). Pass in any pre-queued cmds (e.g. a
// just-confirmed overwrite) so the capacity check stays accurate.
func (m model) fillSlots(skipIdx int, cmds []tea.Cmd) (model, []tea.Cmd) {
	for i := range m.files {
		if i == skipIdx || !m.files[i].selected || m.files[i].status != statusPending {
			continue
		}
		// startFile registers a cancel in m.cancels and launches the worker
		// immediately, but the file's status flips to statusProcessing (and
		// processingCount increments) only later, when the async processingStartMsg
		// is handled. In that gap the file is still statusPending, so without this
		// guard a sibling's terminal message (e.g. a fast probe failure that
		// re-enters fillSlots) could pick the file again and launch a SECOND ffmpeg
		// for it — two encodes racing on one output, with the first worker's cancel
		// orphaned. m.cancels is the authoritative "in flight" set, so skip anything
		// already in it.
		if _, inFlight := m.cancels[i]; inFlight {
			continue
		}
		// Gate capacity on the true in-flight count (m.cancels), which includes
		// launched-but-unconfirmed files, rather than processingCount (which lags
		// until processingStartMsg) — otherwise concurrent encodes can exceed
		// maxConcurrent during the launch window.
		if len(m.cancels) >= m.config.maxConcurrent {
			break
		}
		// Resolve and store the output path once, disambiguating against outputs
		// already claimed by other files in this batch, so two inputs that share
		// a stem (e.g. a.mp4 and a.mov -> out_a.mp4) never write the same file
		// concurrently or silently clobber each other.
		if m.files[i].outPath == "" {
			m.files[i].outPath = m.resolveOutputPath(i)
		}
		// Prompt before clobbering an existing, distinct file. Plain in-place
		// (destination == source) is the consented case and needs no prompt, but an
		// in-place --format conversion whose destination is a *different*,
		// pre-existing file must prompt — as must every non-in-place out_ file.
		// Gate on "destination exists and is not the source"; EqualFold so a
		// case-only spelling difference on a case-insensitive filesystem still
		// counts as the source (not a clobber).
		if pathExists(m.files[i].outPath) && !strings.EqualFold(m.files[i].outPath, m.files[i].path) {
			if !m.showOverwritePrompt {
				m.showOverwritePrompt = true
				m.overwriteCursor = 0
				m.pendingOutputFile = m.files[i].outPath
				m.currentIdx = i
			}
			// Don't start later files while a prompt is pending — otherwise a
			// trailing file can steal the slot the just-confirmed file needs
			// (silently dropping the user's Overwrite choice), and unrelated work
			// would run beneath a modal dialog.
			break
		}
		cmds = append(cmds, m.startFile(i))
	}
	return m, cmds
}

func (m model) tryStartNextFile() (model, tea.Cmd) {
	if m.showOverwritePrompt || m.userCancelled {
		return m, nil
	}
	var cmds []tea.Cmd
	m, cmds = m.fillSlots(-1, cmds)
	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m model) hasUnstartedFiles() bool {
	for _, f := range m.files {
		if f.selected && f.status == statusPending {
			return true
		}
	}
	return false
}

// batchSettled reports that no encode is in flight and there's nothing left to
// start — either the queue drained or the user cancelled. The terminal-state
// handlers (done/error/cancel) share it to decide when to leave the processing
// state.
func (m model) batchSettled() bool {
	// len(m.cancels) is the true in-flight count: a file sits in the map from the
	// instant startFile launches it until its terminal message is handled. That is
	// stricter than processingCount (which only counts files past
	// processingStartMsg), so settling on it can't declare the batch done while a
	// just-launched worker is still spinning up.
	return len(m.cancels) == 0 &&
		(m.userCancelled || (!m.showOverwritePrompt && !m.hasUnstartedFiles()))
}

// markInPlaceCollisions flags, as errors, every selected-and-pending in-place
// file whose destination is shared by another selected-and-pending file. Two
// distinct sources cannot both be replaced in place into one path without losing
// data, and the in-place contract forbids out_-style disambiguation, so the only
// safe action is to refuse the colliding files while letting the rest proceed.
// Destinations are compared case-folded so clip.MP4/clip.mp4 collide on a
// case-insensitive filesystem too. A no-op outside in-place mode, and when
// outputFormat is empty (plain --overwrite) every destination equals its own
// unique source, so nothing collides.
func (m *model) markInPlaceCollisions() {
	if !m.config.inPlace {
		return
	}
	dests := make([]string, len(m.files))
	counts := make(map[string]int)
	for i := range m.files {
		if !m.files[i].selected || m.files[i].status != statusPending {
			continue
		}
		d := strings.ToLower(inPlaceDest(m.files[i].path, m.config.outputFormat))
		dests[i] = d
		counts[d]++
	}
	for i := range m.files {
		if dests[i] == "" || counts[dests[i]] <= 1 {
			continue
		}
		m.files[i].status = statusError
		m.files[i].err = fmt.Errorf(
			"skipped: multiple selected inputs map to the same in-place destination %q under --format %s; rename one or drop --format",
			filepath.Base(inPlaceDest(m.files[i].path, m.config.outputFormat)), m.config.outputFormat)
		m.files[i].outPath = ""
		m.err = m.files[i].err
	}
}

func (m *model) startProcessing() tea.Cmd {
	m.userCancelled = false
	// done and completedCount are per-batch: clear them so a batch started after an
	// earlier one finished doesn't render a stale "All done!" footer or carry the
	// previous batch's count into the "(N completed)" header.
	m.done = false
	m.completedCount = 0

	// In-place mode replaces each source with its own re-encode, so two selected
	// sources that resolve to the same destination (e.g. clip.mov and clip.mp4
	// under --format mp4) would race on one file and delete a source. There is no
	// safe in-place disambiguation, so mark every member of a convergent group as
	// an error and skip it; unaffected files still process normally.
	m.markInPlaceCollisions()

	m.totalToProcess = 0
	for _, f := range m.files {
		if f.selected && f.status == statusPending {
			m.totalToProcess++
		}
	}

	next, cmds := m.fillSlots(-1, nil)
	*m = next

	if len(cmds) > 0 {
		m.processing = true
		return tea.Batch(cmds...)
	}
	return nil
}

func (m model) handleOverwriteConfirm() (model, tea.Cmd) {
	m.showOverwritePrompt = false
	m.processing = true

	confirmed := m.currentIdx
	var cmds []tea.Cmd
	// Capacity is the in-flight count (m.cancels), and the confirmed file must not
	// already be launched, mirroring fillSlots' guards.
	_, alreadyInFlight := m.cancels[confirmed]
	if len(m.cancels) < m.config.maxConcurrent && !alreadyInFlight &&
		m.files[confirmed].selected && m.files[confirmed].status == statusPending {
		cmds = append(cmds, m.startFile(confirmed))
	}

	m, cmds = m.fillSlots(confirmed, cmds)

	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m model) handleOverwriteSkip() (model, tea.Cmd) {
	m.showOverwritePrompt = false
	m.files[m.currentIdx].selected = false
	m.files[m.currentIdx].outPath = "" // release the claim; file won't be written
	if m.totalToProcess > 0 {
		m.totalToProcess--
	}

	var cmds []tea.Cmd
	m, cmds = m.fillSlots(-1, cmds)
	if len(cmds) > 0 {
		m.processing = true
		return m, tea.Batch(cmds...)
	}

	if m.batchSettled() {
		m.processing = false
		m.done = true
	}
	return m, nil
}

func (m model) handleProcessingStart(msg processingStartMsg) (model, tea.Cmd) {
	if m.userCancelled {
		if cancel, ok := m.cancels[msg.idx]; ok {
			cancel()
			delete(m.cancels, msg.idx)
		}
		m.files[msg.idx].status = statusPending
		m.files[msg.idx].progress = 0
		m.files[msg.idx].outPath = "" // unstarted again; recompute next batch
		// cancels[msg.idx] was just deleted above; if nothing else is in flight the
		// cancellation has fully drained.
		if len(m.cancels) == 0 {
			m.processing = false
		}
		return m, nil
	}

	wasIdle := m.processingCount == 0
	m.files[msg.idx].status = statusProcessing
	m.processingCount++

	// Start the spinner tick loop on 0→1 transition; subsequent ticks
	// self-perpetuate via spinner.Update.
	if wasIdle {
		return m, m.spinner.Tick
	}
	return m, nil
}

func (m model) handleProgress(msg progressMsg) (model, tea.Cmd) {
	m.files[msg.idx].progress = msg.progress
	return m, nil
}

func (m model) handleVideoInfo(msg videoInfoMsg) (model, tea.Cmd) {
	m.files[msg.idx].info = msg.info
	return m, nil
}

func (m model) handleOutputInfo(msg outputInfoMsg) (model, tea.Cmd) {
	m.files[msg.idx].outInfo = msg.info
	return m, nil
}

func (m model) handleDone(msg doneMsg) (model, tea.Cmd) {
	m.files[msg.idx].status = statusDone
	m.files[msg.idx].progress = 1.0
	m.completedCount++
	delete(m.cancels, msg.idx)
	if m.processingCount > 0 {
		m.processingCount--
	}

	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	if m.batchSettled() {
		m.processing = false
		if !m.userCancelled {
			m.done = true
		}
	}
	return m, nil
}

func (m model) handleError(msg errorMsg) (model, tea.Cmd) {
	wasProcessing := m.files[msg.idx].status == statusProcessing

	m.files[msg.idx].status = statusError
	m.files[msg.idx].err = msg.err
	m.err = msg.err
	m.files[msg.idx].outPath = "" // release the claim; this file produced no result
	delete(m.cancels, msg.idx)

	if wasProcessing && m.processingCount > 0 {
		m.processingCount--
	}

	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	if m.batchSettled() {
		m.processing = false
		if !m.userCancelled {
			// Reach the same terminal state as a success-ending batch instead of
			// silently dropping back into the editable picker when the last event
			// of the batch happens to be an error (the footer reports the errors).
			m.done = true
		}
	}
	return m, nil
}

func (m model) handleCancel(msg cancelMsg) (model, tea.Cmd) {
	// Only decrement if this file was actually marked processing — otherwise we
	// could steal a slot from another file (e.g. cancel arriving for a file
	// whose processingStartMsg was discarded under userCancelled).
	wasProcessing := m.files[msg.idx].status == statusProcessing

	m.files[msg.idx].status = statusPending
	m.files[msg.idx].progress = 0
	m.files[msg.idx].err = nil
	m.files[msg.idx].outPath = "" // unstarted again; recompute fresh next batch
	delete(m.cancels, msg.idx)
	if wasProcessing && m.processingCount > 0 {
		m.processingCount--
	}

	if m.batchSettled() {
		m.processing = false
	}
	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyMsg) (model, tea.Cmd) {
	// The overwrite prompt is modal — it owns all input while shown.
	if m.showOverwritePrompt {
		return m.handleOverwritePromptKey(msg)
	}

	switch msg.String() {
	case "ctrl+c", "c":
		if m.processing {
			m.userCancelled = true
			for _, cancel := range m.cancels {
				cancel()
			}
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case "q", "esc":
		if !m.processing {
			return m, tea.Quit
		}
	}
	if m.processing {
		return m, nil
	}
	if len(m.files) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case " ":
		m.files[m.cursor].selected = !m.files[m.cursor].selected
		if m.files[m.cursor].status == statusPending {
			// Drop any stale claim so the output name is recomputed for the
			// actual next batch (a deselected sibling shouldn't keep reserving a
			// disambiguated name).
			m.files[m.cursor].outPath = ""
		}
	case "a":
		for i := range m.files {
			if m.files[i].status == statusPending {
				m.files[i].selected = true
			}
		}
		return m, m.startProcessing()
	case "enter":
		return m, m.startProcessing()
	}
	return m, nil
}

// handleOverwritePromptKey handles input while the overwrite confirmation dialog
// is shown; it owns all keys until the user resolves the conflict.
func (m model) handleOverwritePromptKey(msg tea.KeyMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		// Cancel every in-flight encode before quitting. The overwrite modal often
		// appears while sibling files are still encoding; tea.Quit alone would tear
		// down the TUI but leave those ffmpeg children running (they'd finish and
		// write their outputs after we exit). Run() also cancels any survivors after
		// program.Run() returns, but cancelling here kills them promptly.
		m.userCancelled = true
		for _, cancel := range m.cancels {
			cancel()
		}
		return m, tea.Quit
	case "up", "k":
		if m.overwriteCursor > 0 {
			m.overwriteCursor--
		}
	case "down", "j":
		if m.overwriteCursor < 2 {
			m.overwriteCursor++
		}
	case "enter":
		switch m.overwriteCursor {
		case 0: // Overwrite
			return m, func() tea.Msg { return overwriteConfirmMsg{} }
		case 1: // Skip
			return m, func() tea.Msg { return overwriteSkipMsg{} }
		case 2: // Cancel all
			return m.cancelAll(), nil
		}
	}
	return m, nil
}

// cancelAll aborts every in-flight encode and drops the rest of the queued
// batch. It backs the overwrite prompt's "Cancel all" choice.
func (m model) cancelAll() model {
	m.showOverwritePrompt = false
	m.userCancelled = true
	for _, cancel := range m.cancels {
		cancel()
	}
	for i := range m.files {
		if m.files[i].selected && m.files[i].status == statusPending {
			m.files[i].selected = false
			m.files[i].outPath = "" // release the claim; not being written
			if m.totalToProcess > 0 {
				m.totalToProcess--
			}
		}
	}
	if m.processingCount == 0 {
		m.processing = false
		if m.totalToProcess == 0 {
			m.done = true
		}
	}
	return m
}
