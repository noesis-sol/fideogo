package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
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

func (m model) View() string {
	var s strings.Builder

	s.WriteString(titleStyle.Render("🎬 FideoGo Video Compressor"))
	s.WriteString("\n")

	if m.processing {
		completed := 0
		for _, f := range m.files {
			if f.status == "done" {
				completed++
			}
		}
		statusLine := fmt.Sprintf("Processing %d of %d files (%d completed)",
			m.processingCount, m.totalToProcess, completed)
		s.WriteString(infoStyle.Render(statusLine))
	}
	s.WriteString("\n")

	if len(m.files) == 0 {
		s.WriteString(dimStyle.Render("No video files found in current directory."))
		s.WriteString("\n\n")
		s.WriteString(helpTextStyle.Render("Press ") + keyStyle.Render("esc") + helpTextStyle.Render(" or ") + keyStyle.Render("q") + helpTextStyle.Render(" to exit."))
		return s.String()
	}

	for i, f := range m.files {
		cursor := "  "
		if i == m.cursor && !m.processing {
			cursor = "▸ "
		}

		marker := "○"
		markerStyle := dimStyle
		if f.selected {
			marker = "●"
			markerStyle = normalStyle
		}

		// Differentiate in-progress vs waiting while a batch is running.
		useSpinner := false
		if m.processing && f.selected {
			switch f.status {
			case "processing":
				useSpinner = true
			case "":
				marker = "○"
				markerStyle = dimStyle
			}
		}

		style := normalStyle
		if i == m.cursor && !m.processing {
			style = selectedStyle
		}

		s.WriteString(style.Render(cursor))
		if useSpinner {
			s.WriteString(m.spinner.View())
		} else {
			s.WriteString(markerStyle.Render(marker))
		}
		s.WriteString(style.Render(" " + f.name))

		if f.status == "processing" {
			s.WriteString("\n")
			s.WriteString("    ")
			s.WriteString(m.progressBar.ViewAs(f.progress))
			s.WriteString(" ")
			percentColor := getProgressColor(f.progress)
			s.WriteString(lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("%.0f%%", f.progress*100)))
			if f.info != "" {
				s.WriteString("\n    ")
				s.WriteString(infoStyle.Render("In:  " + f.info))
			}
		} else if f.status == "done" {
			s.WriteString(successStyle.Render(" ✓"))
			if f.info != "" {
				s.WriteString("\n    ")
				s.WriteString(normalStyle.Render("In:  " + f.info))
			}
			if f.outInfo != "" {
				s.WriteString("\n    ")
				s.WriteString(successStyle.Render("Out: " + f.outInfo))
			}
		} else if f.status == "error" {
			s.WriteString(errorStyle.Render(" ✗"))
			if f.err != nil {
				s.WriteString("\n    ")
				s.WriteString(errorStyle.Render("Error: " + f.err.Error()))
			}
		}

		s.WriteString("\n")
	}

	s.WriteString("\n")
	if m.done {
		doneMsg := helpTextStyle.Render("All done! Press ") + keyStyle.Render("q") + helpTextStyle.Render(" to quit.")
		s.WriteString(doneMsg)
	} else if m.processing {
		s.WriteString(dimStyle.Render("Processing... (") + keyStyle.Render("c") + dimStyle.Render(" or ") + keyStyle.Render("ctrl+c") + dimStyle.Render(" to cancel)"))
	} else {
		help := keyStyle.Render("↑/↓") + helpTextStyle.Render(" navigate • ") +
			keyStyle.Render("space") + helpTextStyle.Render(" select • ") +
			keyStyle.Render("a") + helpTextStyle.Render(" all • ") +
			keyStyle.Render("enter") + helpTextStyle.Render(" start • ") +
			keyStyle.Render("q") + helpTextStyle.Render(" quit")
		s.WriteString(help)
	}

	if m.showOverwritePrompt {
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
			cursor := "  "
			style := dialogOptionStyle
			if i == m.overwriteCursor {
				cursor = "▸ "
				style = dialogOptionSelectedStyle
			}
			dialog.WriteString(style.Render(cursor + opt))
			dialog.WriteString("\n")
		}

		dialog.WriteString("\n")
		dialog.WriteString(helpTextStyle.Render(keyStyle.Render("↑/↓") + " navigate • " + keyStyle.Render("enter") + " select"))

		s.WriteString(dialogBoxStyle.Render(dialog.String()))
	}

	return s.String()
}
