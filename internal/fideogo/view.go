package fideogo

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// Row glyphs and the detail-line indent, named so the View helpers read clearly.
const (
	cursorActive     = "▸ "
	cursorInactive   = "  "
	markerSelected   = "●"
	markerUnselected = "○"
	doneMark         = " ✓"
	errorMark        = " ✗"
	detailIndent     = "    " // leading pad for In:/Out:/Error: detail lines
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	normalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	keyStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")) // Soft cyan, bold
	helpTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))            // Light gray for descriptions

	dialogBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 1).
			Width(60)
	dialogTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("220"))
	dialogOptionStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dialogOptionSelectedStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("212")).
					Background(lipgloss.Color("236"))
)

// Gradient stops for the progress bar: cyan -> green -> orange -> yellow.
var progressColorStops = []colorful.Color{
	{R: 0.3, G: 0.8, B: 1.0}, // Cyan (0%)
	{R: 0.2, G: 0.9, B: 0.2}, // Green (33%)
	{R: 1.0, G: 0.5, B: 0.0}, // Orange (66%)
	{R: 1.0, G: 1.0, B: 0.0}, // Yellow (100%)
}

func getProgressColor(progress float64) lipgloss.Color {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	numSegments := float64(len(progressColorStops) - 1)
	segment := progress * numSegments
	segmentIndex := int(segment)

	if segmentIndex >= len(progressColorStops)-1 {
		c := progressColorStops[len(progressColorStops)-1]
		return lipgloss.Color(c.Hex())
	}

	t := segment - float64(segmentIndex)
	c1 := progressColorStops[segmentIndex]
	c2 := progressColorStops[segmentIndex+1]
	interpolated := c1.BlendRgb(c2, t)
	return lipgloss.Color(interpolated.Hex())
}

// percentStyles and percentLabels precompute the gradient-colored percentage
// readout for every whole percent (0–100). The progress display only ever shows
// an integer percent, so the hot render path indexes these tables instead of
// blending a color, formatting a string, and allocating a fresh lipgloss.Style
// on every frame for every in-flight file.
var percentStyles, percentLabels = buildPercentTables()

func buildPercentTables() ([101]lipgloss.Style, [101]string) {
	var styles [101]lipgloss.Style
	var labels [101]string
	for p := 0; p <= 100; p++ {
		styles[p] = lipgloss.NewStyle().Foreground(getProgressColor(float64(p) / 100))
		labels[p] = strconv.Itoa(p) + "%"
	}
	return styles, labels
}

func (m model) View() string {
	var s strings.Builder

	s.WriteString(titleStyle.Render("🎬 FideoGo Video Compressor"))
	s.WriteString("\n")

	m.renderStatusHeader(&s)
	s.WriteString("\n")

	if len(m.files) == 0 {
		s.WriteString(dimStyle.Render("No video files found in current directory."))
		s.WriteString("\n\n")
		s.WriteString(helpTextStyle.Render("Press ") + keyStyle.Render("esc") + helpTextStyle.Render(" or ") + keyStyle.Render("q") + helpTextStyle.Render(" to exit."))
		return s.String()
	}

	for i, f := range m.files {
		m.renderFileRow(&s, i, f)
	}

	s.WriteString("\n")
	m.renderFooter(&s)

	if m.showOverwritePrompt {
		m.renderOverwriteDialog(&s)
	}

	return s.String()
}

// renderStatusHeader writes the "Processing X of Y" summary while a batch runs.
func (m model) renderStatusHeader(s *strings.Builder) {
	if !m.processing {
		return
	}
	statusLine := fmt.Sprintf("Processing %d of %d files (%d completed)",
		m.processingCount, m.totalToProcess, m.completedCount)
	s.WriteString(infoStyle.Render(statusLine))
}

// fileGlyph picks a row's leading indicator. While a batch runs, a selected file
// shows the spinner if it's encoding and a dim hollow circle if it's still
// waiting its turn; otherwise the glyph just reflects selection (filled when
// selected, dim hollow when not). When spin is true, glyph and style are unused.
func (m model) fileGlyph(f videoFile) (glyph string, style lipgloss.Style, spin bool) {
	if m.processing && f.selected {
		switch f.status {
		case statusProcessing:
			return markerUnselected, dimStyle, true
		case statusPending:
			return markerUnselected, dimStyle, false
		}
	}
	if f.selected {
		return markerSelected, normalStyle, false
	}
	return markerUnselected, dimStyle, false
}

// renderFileRow writes one file's line: cursor, status glyph/spinner, name, and
// the status-specific detail beneath it (progress, input/output info, or error).
func (m model) renderFileRow(s *strings.Builder, i int, f videoFile) {
	cursor := cursorInactive
	rowStyle := normalStyle
	if i == m.cursor && !m.processing {
		cursor = cursorActive
		rowStyle = selectedStyle
	}

	glyph, glyphStyle, spin := m.fileGlyph(f)
	s.WriteString(rowStyle.Render(cursor))
	if spin {
		s.WriteString(m.spinner.View())
	} else {
		s.WriteString(glyphStyle.Render(glyph))
	}
	s.WriteString(rowStyle.Render(" " + f.name))

	switch f.status {
	case statusProcessing:
		m.renderProgress(s, f)
	case statusDone:
		s.WriteString(successStyle.Render(doneMark))
		if f.info != "" {
			s.WriteString("\n" + detailIndent)
			s.WriteString(normalStyle.Render("In:  " + f.info))
		}
		if f.outInfo != "" {
			s.WriteString("\n" + detailIndent)
			s.WriteString(successStyle.Render("Out: " + f.outInfo))
		}
	case statusError:
		s.WriteString(errorStyle.Render(errorMark))
		if f.err != nil {
			s.WriteString("\n" + detailIndent)
			s.WriteString(errorStyle.Render("Error: " + f.err.Error()))
		}
	}

	s.WriteString("\n")
}

// renderProgress writes the progress bar, gradient percentage, and input info
// for a file that is currently encoding.
func (m model) renderProgress(s *strings.Builder, f videoFile) {
	s.WriteString("\n" + detailIndent)
	s.WriteString(m.progressBar.ViewAs(f.progress))
	s.WriteString(" ")

	pct := int(f.progress*100 + 0.5)
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	s.WriteString(percentStyles[pct].Render(percentLabels[pct]))

	if f.info != "" {
		s.WriteString("\n" + detailIndent)
		s.WriteString(infoStyle.Render("In:  " + f.info))
	}
}

// anyErrors reports whether any file in the batch ended in the error state. Used
// by the footer to distinguish a clean completion from one with failures. Cheap
// (a bounded scan, no allocation), so it stays in the render path.
func (m model) anyErrors() bool {
	for _, f := range m.files {
		if f.status == statusError {
			return true
		}
	}
	return false
}

// renderFooter writes the contextual help/status line at the bottom of the view.
func (m model) renderFooter(s *strings.Builder) {
	switch {
	case m.done && m.anyErrors():
		s.WriteString(helpTextStyle.Render("Done — some files had errors (see above). Press ") + keyStyle.Render("q") + helpTextStyle.Render(" to quit."))
	case m.done:
		s.WriteString(helpTextStyle.Render("All done! Press ") + keyStyle.Render("q") + helpTextStyle.Render(" to quit."))
	case m.processing:
		s.WriteString(dimStyle.Render("Processing... (") + keyStyle.Render("c") + dimStyle.Render(" or ") + keyStyle.Render("ctrl+c") + dimStyle.Render(" to cancel)"))
	default:
		s.WriteString(keyStyle.Render("↑/↓") + helpTextStyle.Render(" navigate • ") +
			keyStyle.Render("space") + helpTextStyle.Render(" select • ") +
			keyStyle.Render("a") + helpTextStyle.Render(" all • ") +
			keyStyle.Render("enter") + helpTextStyle.Render(" start • ") +
			keyStyle.Render("q") + helpTextStyle.Render(" quit"))
	}
}

// renderOverwriteDialog writes the modal shown when an output file already exists.
func (m model) renderOverwriteDialog(s *strings.Builder) {
	s.WriteString("\n\n")
	var dialog strings.Builder

	dialog.WriteString(dialogTitleStyle.Render("⚠️  File Already Exists"))
	dialog.WriteString("\n")
	dialog.WriteString(normalStyle.Render("The output file already exists:"))
	dialog.WriteString("\n")
	dialog.WriteString(infoStyle.Render(filepath.Base(m.pendingOutputFile)))
	dialog.WriteString("\n")
	dialog.WriteString(helpTextStyle.Render("What would you like to do?"))
	dialog.WriteString("\n")

	options := []string{"Overwrite existing file", "Skip this file", "Cancel all"}
	for i, opt := range options {
		cursor := cursorInactive
		style := dialogOptionStyle
		if i == m.overwriteCursor {
			cursor = cursorActive
			style = dialogOptionSelectedStyle
		}
		dialog.WriteString(style.Render(cursor + opt))
		dialog.WriteString("\n")
	}

	dialog.WriteString("\n")
	dialog.WriteString(helpTextStyle.Render(keyStyle.Render("↑/↓") + " navigate • " + keyStyle.Render("enter") + " select"))

	s.WriteString(dialogBoxStyle.Render(dialog.String()))
}
