package fideogo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Installation-dialog styles (static; built once rather than rebuilt on every
// View() call, matching the package-level style vars in view.go).
var (
	errBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(1, 2).
			Width(70)
	errTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))
	errCommandBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("82")).
				Padding(0, 1).
				Foreground(lipgloss.Color("82")).
				Bold(true)
	errCopyButtonStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("117")).
				Background(lipgloss.Color("236")).
				Padding(0, 2)
)

func getInstallCommand() (osName, command string) {
	switch runtime.GOOS {
	case "darwin":
		return "macOS", "brew install ffmpeg"
	case "linux":
		if _, err := exec.LookPath("apt"); err == nil {
			return "Ubuntu/Debian", "sudo apt install ffmpeg"
		} else if _, err := exec.LookPath("dnf"); err == nil {
			return "Fedora", "sudo dnf install ffmpeg"
		}
		return "Linux", "sudo apt install ffmpeg  # or use your package manager"
	case "windows":
		return "Windows", "choco install ffmpeg"
	default:
		return "Your system", "Please visit https://ffmpeg.org/download.html"
	}
}

func copyToClipboard(text string) error {
	// Bound the helper so a wedged clipboard tool (e.g. a stuck wl-copy/xclip)
	// can't hang forever; copyCmd already runs this off the UI goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "pbcopy")
	case "linux":
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.CommandContext(ctx, "xsel", "--clipboard", "--input")
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.CommandContext(ctx, "wl-copy")
		} else {
			return fmt.Errorf("no clipboard utility found (install xclip, xsel, or wl-copy)")
		}
	case "windows":
		cmd = exec.CommandContext(ctx, "clip")
	default:
		return fmt.Errorf("clipboard not supported on this platform")
	}

	pipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Always reap the child, even if the Write/Close below fails (e.g. the helper
	// exits early and the pipe write returns EPIPE) — otherwise the started
	// process is never Wait()ed and leaks as a zombie.
	defer func() { _ = cmd.Wait() }()
	if _, err := pipe.Write([]byte(text)); err != nil {
		return err
	}
	return pipe.Close()
}

type errorModel struct {
	osName  string
	command string
	copied  bool
	err     string
}

func newErrorModel() errorModel {
	osName, command := getInstallCommand()
	return errorModel{osName: osName, command: command}
}

func (m errorModel) Init() tea.Cmd { return nil }

// clipboardResultMsg carries the outcome of an async clipboard copy back to the
// Update loop.
type clipboardResultMsg struct{ err error }

// copyCmd runs the (potentially slow, process-spawning) clipboard copy as a
// tea.Cmd so it never blocks the Update goroutine, per Bubble Tea's contract.
func copyCmd(command string) tea.Cmd {
	return func() tea.Msg {
		return clipboardResultMsg{err: copyToClipboard(command)}
	}
}

func (m errorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clipboardResultMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.copied = true
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "c", "enter":
			return m, copyCmd(m.command)
		}
	}
	return m, nil
}

func (m errorModel) View() string {
	var content strings.Builder
	content.WriteString(errTitleStyle.Render("⚠️  Missing Dependency"))
	content.WriteString("\n\n")
	content.WriteString(normalStyle.Render("ffmpeg is not installed on your system."))
	content.WriteString("\n")
	content.WriteString(dimStyle.Render("This tool requires ffmpeg for video compression."))
	content.WriteString("\n\n")
	content.WriteString(infoStyle.Render("Installation Instructions for " + m.osName + ":"))
	content.WriteString("\n\n")
	content.WriteString(errCommandBoxStyle.Render(m.command))
	content.WriteString("\n\n")

	if m.copied {
		content.WriteString(successStyle.Render("✓ Copied to clipboard!"))
		content.WriteString("\n")
		content.WriteString(helpTextStyle.Render("Paste it in your terminal to install ffmpeg."))
	} else if m.err != "" {
		content.WriteString(errorStyle.Render("✗ " + m.err))
		content.WriteString("\n")
		content.WriteString(helpTextStyle.Render("Please select and copy the command manually."))
	} else {
		content.WriteString(errCopyButtonStyle.Render("Press 'c' or 'enter' to copy"))
		content.WriteString("\n")
		content.WriteString(helpTextStyle.Render("Or select the command above with your mouse"))
	}

	content.WriteString("\n\n")
	content.WriteString(dimStyle.Render("Press ") + keyStyle.Render("q") + dimStyle.Render(" to exit"))

	return "\n" + errBoxStyle.Render(content.String()) + "\n"
}

func displayInstallationHelp() {
	m := newErrorModel()
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
}
