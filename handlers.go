package main

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// fillSlots walks selected+unstarted files (skipping skipIdx, pass -1 for none),
// queuing processFile cmds up to remaining capacity. The first overwrite
// conflict encountered while still within capacity is captured into the prompt
// fields; scanning stops once capacity is exhausted. Pass in any pre-queued
// cmds (e.g. a just-confirmed overwrite) so the capacity check stays accurate.
func (m model) fillSlots(skipIdx int, cmds []tea.Cmd) (model, []tea.Cmd) {
	for i := range m.files {
		if i == skipIdx || !m.files[i].selected || m.files[i].status != "" {
			continue
		}
		if m.processingCount+len(cmds) >= m.config.maxConcurrent {
			break
		}
		if outputFileExists(m.files[i].path, m.config.outputFormat) {
			if !m.showOverwritePrompt {
				m.showOverwritePrompt = true
				m.overwriteCursor = 0
				m.pendingOutputFile = getOutputPath(m.files[i].path, m.config.outputFormat)
				m.currentIdx = i
			}
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancels[i] = cancel
		cmds = append(cmds, m.processFile(i, ctx, cancel))
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
		if f.selected && f.status == "" {
			return true
		}
	}
	return false
}

func (m *model) startProcessing() tea.Cmd {
	m.userCancelled = false

	m.totalToProcess = 0
	for _, f := range m.files {
		if f.selected && f.status == "" {
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
	m.userCancelled = false

	confirmed := m.currentIdx
	var cmds []tea.Cmd
	if m.processingCount < m.config.maxConcurrent &&
		m.files[confirmed].selected && m.files[confirmed].status == "" {
		ctx, cancel := context.WithCancel(context.Background())
		m.cancels[confirmed] = cancel
		cmds = append(cmds, m.processFile(confirmed, ctx, cancel))
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
	if m.totalToProcess > 0 {
		m.totalToProcess--
	}

	var cmds []tea.Cmd
	m, cmds = m.fillSlots(-1, cmds)
	if len(cmds) > 0 {
		m.processing = true
		return m, tea.Batch(cmds...)
	}

	if m.processingCount == 0 && !m.hasUnstartedFiles() {
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
		m.files[msg.idx].status = ""
		m.files[msg.idx].progress = 0
		if m.processingCount == 0 {
			m.processing = false
		}
		return m, nil
	}

	wasIdle := m.processingCount == 0
	m.files[msg.idx].status = "processing"
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
	m.files[msg.idx].status = "done"
	m.files[msg.idx].progress = 1.0
	delete(m.cancels, msg.idx)
	if m.processingCount > 0 {
		m.processingCount--
	}

	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	if m.processingCount == 0 && (m.userCancelled || (!m.showOverwritePrompt && !m.hasUnstartedFiles())) {
		m.processing = false
		if !m.userCancelled {
			m.done = true
		}
	}
	return m, nil
}

func (m model) handleError(msg errorMsg) (model, tea.Cmd) {
	wasProcessing := m.files[msg.idx].status == "processing"

	m.files[msg.idx].status = "error"
	m.files[msg.idx].err = msg.err
	m.err = msg.err
	delete(m.cancels, msg.idx)

	if wasProcessing && m.processingCount > 0 {
		m.processingCount--
	}

	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	if m.processingCount == 0 && (m.userCancelled || (!m.showOverwritePrompt && !m.hasUnstartedFiles())) {
		m.processing = false
	}
	return m, nil
}

func (m model) handleCancel(msg cancelMsg) (model, tea.Cmd) {
	// Only decrement if this file was actually marked processing — otherwise we
	// could steal a slot from another file (e.g. cancel arriving for a file
	// whose processingStartMsg was discarded under userCancelled).
	wasProcessing := m.files[msg.idx].status == "processing"

	m.files[msg.idx].status = ""
	m.files[msg.idx].progress = 0
	m.files[msg.idx].err = nil
	delete(m.cancels, msg.idx)
	if wasProcessing && m.processingCount > 0 {
		m.processingCount--
	}

	if m.processingCount == 0 && (m.userCancelled || (!m.showOverwritePrompt && !m.hasUnstartedFiles())) {
		m.processing = false
	}
	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyMsg) (model, tea.Cmd) {
	// Handle overwrite prompt first.
	if m.showOverwritePrompt {
		switch msg.String() {
		case "ctrl+c":
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
				m.showOverwritePrompt = false
				m.userCancelled = true
				for _, cancel := range m.cancels {
					cancel()
				}
				for i := 0; i < len(m.files); i++ {
					if m.files[i].selected && m.files[i].status == "" {
						m.files[i].selected = false
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
				return m, nil
			}
		}
		return m, nil
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
	case "a":
		for i := range m.files {
			if m.files[i].status == "" {
				m.files[i].selected = true
			}
		}
		return m, m.startProcessing()
	case "enter":
		return m, m.startProcessing()
	}
	return m, nil
}
