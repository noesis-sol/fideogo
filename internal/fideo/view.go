package fideo

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

	// rowReserved is the width a file row spends around its name: the cursor
	// (2), the glyph (1) and a space before it, and the ✓/✗ mark (2) after it.
	rowReserved = 6

	// chromeLines are the fixed lines around the file list: the title, the
	// status header, the blank line before the footer, and the footer.
	chromeLines = 4
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

	s.WriteString(titleStyle.Render("🎬 Fideo Video Compressor"))
	s.WriteString("\n")

	m.renderStatusHeader(&s)
	s.WriteString("\n")

	if len(m.files) == 0 {
		s.WriteString(dimStyle.Render("No video files found in current directory."))
		s.WriteString("\n\n")
		s.WriteString(helpTextStyle.Render("Press ") + keyStyle.Render("esc") + helpTextStyle.Render(" or ") + keyStyle.Render("q") + helpTextStyle.Render(" to exit."))
		return s.String()
	}

	m.renderFileList(&s)

	s.WriteString("\n")
	m.renderFooter(&s)

	if m.showOverwritePrompt {
		s.WriteString("\n\n")
		s.WriteString(m.renderOverwriteDialog())
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

// renderFileList writes the rows that fit the terminal — a window around the
// cursor (see viewport) — with "N more" markers where rows are clipped above or
// below, so a long directory stays navigable instead of having Bubble Tea's
// inline renderer silently drop the top of the frame.
func (m model) renderFileList(s *strings.Builder) {
	start, end := m.viewport()
	if start > 0 {
		s.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
		s.WriteString("\n")
	}
	for i := start; i < end; i++ {
		m.renderFileRow(s, i, m.files[i])
	}
	if end < len(m.files) {
		s.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(m.files)-end)))
		s.WriteString("\n")
	}
}

// viewport is the [start, end) range of file rows View draws.
func (m model) viewport() (start, end int) {
	return viewport(m.rowLineCounts(), m.cursor, m.offset, m.rowBudget())
}

// scrollOffset is the first visible row once the cursor has been kept in view;
// Update stores it back into m.offset after every message so scrolling is
// incremental rather than recomputed from scratch.
func (m model) scrollOffset() int {
	start, _ := m.viewport()
	return start
}

// pageRows is how many rows a PgUp/PgDn keystroke moves: the rows currently on
// screen, or a modest default before the terminal size is known.
func (m model) pageRows() int {
	if m.height <= 0 {
		return 10
	}
	start, end := m.viewport()
	return max(1, end-start)
}

// rowBudget is how many terminal lines the file list may occupy, or 0 when the
// terminal size is unknown (nothing is clipped then). The overwrite dialog,
// when shown, sits below the footer and takes its lines out of the same budget.
func (m model) rowBudget() int {
	if m.height <= 0 {
		return 0
	}
	extra := 0
	if m.showOverwritePrompt {
		extra = 1 + lipgloss.Height(m.renderOverwriteDialog()) // blank line + box
	}
	return max(1, m.height-chromeLines-extra)
}

// rowLineCounts returns the number of terminal lines each file row renders to.
func (m model) rowLineCounts() []int {
	lines := make([]int, len(m.files))
	for i, f := range m.files {
		lines[i] = rowLines(f)
	}
	return lines
}

// rowLines is the number of lines renderFileRow emits for f. It must mirror
// renderFileRow exactly — detail lines are truncated rather than wrapped, so
// the count is reliable — because the viewport uses it to decide what fits
// without rendering every row.
func rowLines(f videoFile) int {
	n := 1
	switch f.status {
	case statusProcessing:
		n++ // progress bar
		if f.info != "" {
			n++
		}
	case statusDone:
		if f.info != "" {
			n++
		}
		if f.outInfo != "" {
			n++
		}
	case statusError:
		if f.err != nil {
			n++
		}
	}
	return n
}

// viewport picks the half-open row range [start, end) to draw so that the rows
// fit in budget lines (budget <= 0 means unlimited) and the cursor is visible.
// offset is the preferred first row — the current scroll position — and is
// moved only as far as needed to bring the cursor into view, so the list
// scrolls a row at a time instead of jumping. One line is reserved at each
// clipped edge for its "more" marker, so the whole frame still fits.
func viewport(lines []int, cursor, offset, budget int) (start, end int) {
	n := len(lines)
	if n == 0 {
		return 0, 0
	}
	if budget <= 0 || sumInts(lines) <= budget {
		return 0, n
	}
	cursor = max(0, min(cursor, n-1))
	offset = max(0, min(offset, cursor)) // never start below the cursor

	end = extendDown(lines, offset, budget)
	if cursor >= end {
		// The cursor fell below the window: pin the bottom edge to it.
		end = cursor + 1
		return extendUp(lines, end, budget), end
	}
	if end == n {
		// The window reaches the last row: pull the top edge up to use any
		// spare lines rather than leave them blank.
		offset = min(offset, extendUp(lines, n, budget))
	}
	return offset, end
}

// extendDown returns the largest end such that rows [start, end) plus their
// edge markers fit in budget; always at least one row.
func extendDown(lines []int, start, budget int) int {
	n := len(lines)
	used, end := 0, start
	for end < n {
		reserve := btoi(start > 0) + btoi(end+1 < n)
		if used+lines[end]+reserve > budget {
			break
		}
		used += lines[end]
		end++
	}
	return max(end, start+1)
}

// extendUp returns the smallest start such that rows [start, end) plus their
// edge markers fit in budget; always at least one row.
func extendUp(lines []int, end, budget int) int {
	n := len(lines)
	used, start := 0, end
	for start > 0 {
		reserve := btoi(end < n) + btoi(start-1 > 0)
		if used+lines[start-1]+reserve > budget {
			break
		}
		used += lines[start-1]
		start--
	}
	return min(start, end-1)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sumInts(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
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
// The name and every detail line are truncated to the terminal width so a row
// never wraps; rowLines depends on that.
func (m model) renderFileRow(s *strings.Builder, i int, f videoFile) {
	cursor := cursorInactive
	rowStyle := normalStyle
	if i == m.cursor {
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
	s.WriteString(rowStyle.Render(" " + m.fit(f.name, rowReserved)))

	switch f.status {
	case statusProcessing:
		m.renderProgress(s, f)
	case statusDone:
		s.WriteString(successStyle.Render(doneMark))
		if f.info != "" {
			m.writeDetail(s, normalStyle, "In:  "+f.info)
		}
		if f.outInfo != "" {
			m.writeDetail(s, successStyle, "Out: "+f.outInfo)
		}
	case statusError:
		s.WriteString(errorStyle.Render(errorMark))
		if f.err != nil {
			m.writeDetail(s, errorStyle, "Error: "+oneLine(f.err.Error()))
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
		m.writeDetail(s, infoStyle, "In:  "+f.info)
	}
}

// writeDetail writes one indented detail line beneath a row, truncated to the
// terminal width so it can never wrap — a wrapped line would break the
// viewport's row accounting and corrupt the frame.
func (m model) writeDetail(s *strings.Builder, style lipgloss.Style, text string) {
	s.WriteString("\n" + detailIndent)
	s.WriteString(style.Render(m.fit(text, len(detailIndent))))
}

// fit truncates text with an ellipsis to the terminal width minus reserved
// cells; a no-op until the terminal size is known.
func (m model) fit(text string, reserved int) string {
	if m.width <= 0 {
		return text
	}
	return ansi.Truncate(text, max(1, m.width-reserved), "…")
}

// oneLine collapses an error's whitespace (including embedded newlines) so it
// renders on a single detail line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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

// renderOverwriteDialog returns the modal shown when an output file already
// exists. It is returned rather than written so rowBudget can measure it.
func (m model) renderOverwriteDialog() string {
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

	return dialogBoxStyle.Render(dialog.String())
}
