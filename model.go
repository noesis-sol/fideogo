package main

import (
	"context"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	files               []videoFile
	cursor              int
	processing          bool
	currentIdx          int
	progressBar         progress.Model
	spinner             spinner.Model
	done                bool
	err                 error
	cancels             map[int]context.CancelFunc
	showOverwritePrompt bool
	overwriteCursor     int
	pendingOutputFile   string
	config              compressionConfig
	videoService        *videoService
	processingCount     int
	totalToProcess      int
	userCancelled       bool
}

type progressMsg struct {
	idx      int
	progress float64
}
type processingStartMsg struct {
	idx int
}
type doneMsg struct{ idx int }
type errorMsg struct {
	idx int
	err error
}
type videoInfoMsg struct {
	idx  int
	info string
}
type outputInfoMsg struct {
	idx  int
	info string
}
type autoStartMsg struct{}
type cancelMsg struct{ idx int }
type overwriteConfirmMsg struct{}
type overwriteSkipMsg struct{}

func initialModel(path string) model {
	files := findVideos(path)
	return newModel(files)
}

func newModel(files []videoFile) model {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(),
	)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	// Auto-select if there's only one file.
	if len(files) == 1 {
		files[0].selected = true
	}

	config := defaultConfig
	return model{
		files:        files,
		progressBar:  p,
		spinner:      sp,
		cancels:      make(map[int]context.CancelFunc),
		config:       config,
		videoService: newVideoService(config),
	}
}

func (m model) Init() tea.Cmd {
	// Auto-start if there's only one video file and it's selected.
	if len(m.files) == 1 && m.files[0].selected {
		return func() tea.Msg {
			return autoStartMsg{}
		}
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case autoStartMsg:
		return m, m.startProcessing()

	case overwriteConfirmMsg:
		return m.handleOverwriteConfirm()

	case overwriteSkipMsg:
		return m.handleOverwriteSkip()

	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case processingStartMsg:
		return m.handleProcessingStart(msg)

	case progressMsg:
		return m.handleProgress(msg)

	case videoInfoMsg:
		return m.handleVideoInfo(msg)

	case outputInfoMsg:
		return m.handleOutputInfo(msg)

	case doneMsg:
		return m.handleDone(msg)

	case errorMsg:
		return m.handleError(msg)

	case cancelMsg:
		return m.handleCancel(msg)

	case spinner.TickMsg:
		if m.processingCount == 0 {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}
