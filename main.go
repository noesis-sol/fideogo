package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
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
	helpTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))             // Light gray for descriptions

	// Dialog styles
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

	// Compile regex once at package level
	timeRegex = regexp.MustCompile(`out_time_ms=(\d+)`)

	// Video file extensions
	videoExtensions = map[string]bool{
		".mp4": true,
		".mov": true,
		".avi": true,
		".mkv": true,
		".m4v": true,
	}
)

type videoFile struct {
	path     string
	name     string
	selected bool
	status   string // "", "processing", "done", "error"
	progress float64
	info     string
	outInfo  string
}

type model struct {
	files               []videoFile
	cursor              int
	processing          bool
	currentIdx          int
	progressBar         progress.Model
	done                bool
	err                 error
	msgChans            map[int]chan tea.Msg
	runningCmds         map[int]*exec.Cmd
	showOverwritePrompt bool
	overwriteCursor     int
	pendingOutputFile   string
	maxConcurrent       int
	processingCount     int
	totalToProcess      int
}

type progressMsg struct {
	idx      int
	progress float64
}
type processingStartMsg struct {
	idx int
	cmd *exec.Cmd
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
type cancelMsg struct{}
type overwriteConfirmMsg struct{}
type overwriteSkipMsg struct{}

func findVideos(dir string) []videoFile {
	var files []videoFile

	// Read only the current directory (non-recursive)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}

	for _, entry := range entries {
		// Skip directories
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip out_ prefixed files
		if strings.HasPrefix(name, "out_") {
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		if videoExtensions[ext] {
			files = append(files, videoFile{
				path: filepath.Join(dir, name),
				name: name,
			})
		}
	}
	return files
}

func getVideoInfo(path string) string {
	codec := runProbe(path, "stream=codec_name", "-select_streams", "v:0")
	res := runProbe(path, "stream=width,height", "-select_streams", "v:0")
	res = strings.Replace(res, "\n", "x", 1)
	bitrate := runProbe(path, "format=bit_rate")
	if br, err := strconv.ParseFloat(strings.TrimSpace(bitrate), 64); err == nil {
		bitrate = fmt.Sprintf("%.1f Mbps", br/1000000)
	}
	return fmt.Sprintf("%s | %s | %s", strings.TrimSpace(res), strings.TrimSpace(codec), bitrate)
}

func runProbe(path, entries string, extra ...string) string {
	args := append([]string{"-v", "error", "-show_entries", entries, "-of", "default=noprint_wrappers=1:nokey=1"}, extra...)
	args = append(args, path)
	out, _ := exec.Command("ffprobe", args...).Output()
	return string(out)
}

func getProgressColor(progress float64) lipgloss.Color {
	// Define gradient stops: cyan -> green -> orange -> yellow (cold to warm)
	colorStops := []colorful.Color{
		colorful.Color{R: 0.3, G: 0.8, B: 1.0},   // Cyan (0%)
		colorful.Color{R: 0.2, G: 0.9, B: 0.2},   // Green (33%)
		colorful.Color{R: 1.0, G: 0.5, B: 0.0},   // Orange (66%)
		colorful.Color{R: 1.0, G: 1.0, B: 0.0},   // Yellow (100%)
	}

	// Clamp progress to [0, 1]
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	// Calculate which segment we're in
	numSegments := float64(len(colorStops) - 1)
	segment := progress * numSegments
	segmentIndex := int(segment)

	// Handle edge case for 100%
	if segmentIndex >= len(colorStops)-1 {
		c := colorStops[len(colorStops)-1]
		return lipgloss.Color(c.Hex())
	}

	// Interpolate between the two colors in this segment
	t := segment - float64(segmentIndex)
	c1 := colorStops[segmentIndex]
	c2 := colorStops[segmentIndex+1]
	interpolated := c1.BlendRgb(c2, t)

	return lipgloss.Color(interpolated.Hex())
}

func initialModel(path string) model {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(),
	)
	files := findVideos(path)

	// Auto-select if there's only one file
	if len(files) == 1 {
		files[0].selected = true
	}

	return model{
		files:         files,
		progressBar:   p,
		msgChans:      make(map[int]chan tea.Msg),
		runningCmds:   make(map[int]*exec.Cmd),
		maxConcurrent: 3,
	}
}

func (m model) Init() tea.Cmd {
	// Auto-start if there's only one video file and it's selected
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
		m.showOverwritePrompt = false
		// Continue with processing
		m.processing = true
		return m, m.processFile(m.currentIdx)

	case overwriteSkipMsg:
		m.showOverwritePrompt = false
		// Skip this file and move to next
		for i := m.currentIdx + 1; i < len(m.files); i++ {
			if m.files[i].selected {
				m.currentIdx = i
				return m, m.startProcessing()
			}
		}
		// No more files to process
		m.done = true
		return m, nil

	case tea.KeyMsg:
		// Handle overwrite prompt first
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
					m.processing = false
					// Unselect all remaining files
					for i := m.currentIdx; i < len(m.files); i++ {
						m.files[i].selected = false
					}
					return m, nil
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "c":
			if m.processing {
				// Cancel all running processes
				for _, cmd := range m.runningCmds {
					if cmd != nil && cmd.Process != nil {
						cmd.Process.Kill()
					}
				}
				return m, nil
			}
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		case "q":
			if !m.processing {
				return m, tea.Quit
			}
		}
		if m.processing {
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
			// Select all files and start processing
			for i := range m.files {
				m.files[i].selected = true
			}
			return m, m.startProcessing()
		case "enter":
			return m, m.startProcessing()
		}

	case processingStartMsg:
		m.files[msg.idx].status = "processing"
		m.runningCmds[msg.idx] = msg.cmd
		m.processingCount++
		return m, m.continueListening()

	case progressMsg:
		m.files[msg.idx].progress = msg.progress
		return m, m.continueListening()

	case videoInfoMsg:
		m.files[msg.idx].info = msg.info
		return m, m.continueListening()

	case outputInfoMsg:
		m.files[msg.idx].outInfo = msg.info
		return m, m.continueListening()

	case doneMsg:
		m.files[msg.idx].status = "done"
		m.files[msg.idx].progress = 1.0
		delete(m.runningCmds, msg.idx)
		m.processingCount--

		// Find next unprocessed selected file
		for i := 0; i < len(m.files); i++ {
			if m.files[i].selected && m.files[i].status == "" {
				// Found an unprocessed file, start it
				return m, m.processFile(i)
			}
		}

		// No more files to process
		if m.processingCount == 0 {
			m.processing = false
			m.done = true
		}
		return m, m.continueListening()

	case errorMsg:
		m.files[msg.idx].status = "error"
		m.err = msg.err
		delete(m.runningCmds, msg.idx)
		m.processingCount--

		// Continue processing other files
		if m.processingCount == 0 {
			m.processing = false
		}
		return m, m.continueListening()

	case cancelMsg:
		m.processingCount--
		if m.processingCount == 0 {
			m.processing = false
		}
		return m, m.continueListening()
	}

	return m, nil
}

func (m *model) continueListening() tea.Cmd {
	if len(m.msgChans) == 0 {
		return nil
	}

	// Listen to all channels simultaneously
	var cmds []tea.Cmd
	for _, ch := range m.msgChans {
		cmds = append(cmds, listenToChannel(ch))
	}
	return tea.Batch(cmds...)
}

func (m *model) startProcessing() tea.Cmd {
	// Count total files to process
	m.totalToProcess = 0
	for _, f := range m.files {
		if f.selected {
			m.totalToProcess++
		}
	}

	// Start up to maxConcurrent files
	var cmds []tea.Cmd
	started := 0

	for i, f := range m.files {
		if !f.selected || f.status != "" {
			continue
		}

		if started >= m.maxConcurrent {
			break
		}

		// Check if output file already exists
		dir := filepath.Dir(f.path)
		base := filepath.Base(f.path)
		output := filepath.Join(dir, "out_"+base)

		if _, err := os.Stat(output); err == nil {
			// File exists, show prompt
			m.showOverwritePrompt = true
			m.overwriteCursor = 0
			m.pendingOutputFile = output
			m.currentIdx = i
			return nil
		}

		m.processing = true
		cmds = append(cmds, m.processFile(i))
		started++
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func listenToChannel(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) processFile(idx int) tea.Cmd {
	msgChan := make(chan tea.Msg, 100)
	m.msgChans[idx] = msgChan

	go func() {
		defer func() {
			close(msgChan)
			delete(m.msgChans, idx)
		}()

		f := m.files[idx]

		// Get input info
		info := getVideoInfo(f.path)
		if info != "" {
			msgChan <- videoInfoMsg{idx: idx, info: info}
		}

		dir := filepath.Dir(f.path)
		base := filepath.Base(f.path)
		output := filepath.Join(dir, "out_"+base)

		// Get duration for progress calculation
		durStr := runProbe(f.path, "format=duration")
		duration, err := strconv.ParseFloat(strings.TrimSpace(durStr), 64)
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to get video duration: %w", err)}
			return
		}

		cmd := exec.Command("ffmpeg", "-i", f.path,
			"-c:v", "libx264", "-preset", "slow", "-crf", "28",
			"-vf", "scale=-2:1080",
			"-c:a", "aac", "-b:a", "96k",
			"-movflags", "+faststart",
			"-progress", "pipe:1",
			"-loglevel", "error",
			"-y", output)

		// Send processing start message with the command
		msgChan <- processingStartMsg{idx: idx, cmd: cmd}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to create stdout pipe: %w", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to start ffmpeg: %w", err)}
			return
		}

		scanner := bufio.NewScanner(stdout)

		// Parse progress in a separate goroutine
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				if matches := timeRegex.FindStringSubmatch(line); len(matches) > 1 {
					timeMs, err := strconv.ParseInt(matches[1], 10, 64)
					if err != nil {
						continue
					}
					timeSec := float64(timeMs) / 1000000
					if duration > 0 {
						prog := timeSec / duration
						if prog > 1 {
							prog = 1
						}
						// Send progress through channel
						msgChan <- progressMsg{idx: idx, progress: prog}
					}
				}
			}
		}()

		if err := cmd.Wait(); err != nil {
			// Check if process was killed (cancelled)
			if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == -1 {
				// Process was killed, send cancel message and clean up partial output
				os.Remove(output)
				msgChan <- cancelMsg{}
				return
			}
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("ffmpeg failed: %w", err)}
			return
		}

		// Get output info
		outInfo := getVideoInfo(output)
		if outInfo != "" {
			msgChan <- outputInfoMsg{idx: idx, info: outInfo}
		}

		msgChan <- doneMsg{idx: idx}
	}()

	return listenToChannel(msgChan)
}

func (m model) View() string {
	var s strings.Builder

	s.WriteString(titleStyle.Render("🎬 Video Compressor"))
	s.WriteString("\n")

	// Show processing status if processing
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
		return s.String()
	}

	for i, f := range m.files {
		cursor := "  "
		if i == m.cursor && !m.processing {
			cursor = "▸ "
		}

		checkbox := "○"
		if f.selected {
			checkbox = "●"
		}

		style := normalStyle
		if i == m.cursor && !m.processing {
			style = selectedStyle
		}

		line := fmt.Sprintf("%s%s %s", cursor, checkbox, f.name)
		s.WriteString(style.Render(line))

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
			if m.err != nil {
				s.WriteString("\n    ")
				s.WriteString(errorStyle.Render("Error: " + m.err.Error()))
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
		// Build help line with prominent key bindings
		help := keyStyle.Render("↑/↓") + helpTextStyle.Render(" navigate • ") +
			keyStyle.Render("space") + helpTextStyle.Render(" select • ") +
			keyStyle.Render("a") + helpTextStyle.Render(" all • ") +
			keyStyle.Render("enter") + helpTextStyle.Render(" start • ") +
			keyStyle.Render("q") + helpTextStyle.Render(" quit")
		s.WriteString(help)
	}

	// Show overwrite prompt if needed
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

func main() {
	var m model

	// Check for positional argument
	if len(os.Args) > 1 {
		targetPath := os.Args[1]

		// Check if path contains wildcard
		if strings.Contains(targetPath, "*") {
			// Expand glob pattern
			matches, err := filepath.Glob(targetPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error expanding pattern: %v\n", err)
				os.Exit(1)
			}

			if len(matches) == 0 {
				fmt.Fprintf(os.Stderr, "Error: no files or directories match pattern: %s\n", targetPath)
				os.Exit(1)
			}

			// Collect all video files from matches
			var allFiles []videoFile
			for _, match := range matches {
				info, err := os.Stat(match)
				if err != nil {
					// Skip files that can't be accessed
					continue
				}

				if info.IsDir() {
					// It's a directory - find videos in it
					dirFiles := findVideos(match)
					allFiles = append(allFiles, dirFiles...)
				} else {
					// It's a file - check if it's a video
					ext := strings.ToLower(filepath.Ext(match))
					if videoExtensions[ext] {
						absPath, err := filepath.Abs(match)
						if err != nil {
							continue
						}
						allFiles = append(allFiles, videoFile{
							path: absPath,
							name: filepath.Base(match),
						})
					}
				}
			}

			if len(allFiles) == 0 {
				fmt.Fprintf(os.Stderr, "Error: no video files found matching pattern: %s\n", targetPath)
				os.Exit(1)
			}

			// Create model with collected files
			p := progress.New(
				progress.WithDefaultGradient(),
				progress.WithoutPercentage(),
			)

			// Auto-select if there's only one file
			if len(allFiles) == 1 {
				allFiles[0].selected = true
			}

			m = model{
				files:         allFiles,
				progressBar:   p,
				msgChans:      make(map[int]chan tea.Msg),
				runningCmds:   make(map[int]*exec.Cmd),
				maxConcurrent: 3,
			}
		} else {
			// No wildcard - process as before
			// Get file info to determine if it's a file or directory
			info, err := os.Stat(targetPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if info.IsDir() {
				// It's a directory - initialize model with this directory
				m = initialModel(targetPath)
			} else {
				// It's a file - check if it's a video file
				ext := strings.ToLower(filepath.Ext(targetPath))
				if !videoExtensions[ext] {
					fmt.Fprintf(os.Stderr, "Error: %s is not a supported video file\n", targetPath)
					fmt.Fprintf(os.Stderr, "Supported extensions: .mp4, .mov, .avi, .mkv, .m4v\n")
					os.Exit(1)
				}

				// Create model with single file, auto-selected
				p := progress.New(
					progress.WithDefaultGradient(),
					progress.WithoutPercentage(),
				)

				absPath, err := filepath.Abs(targetPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
					os.Exit(1)
				}

				m = model{
					files: []videoFile{
						{
							path:     absPath,
							name:     filepath.Base(targetPath),
							selected: true,
						},
					},
					progressBar:   p,
					msgChans:      make(map[int]chan tea.Msg),
					runningCmds:   make(map[int]*exec.Cmd),
					maxConcurrent: 3,
				}
			}
		}
	} else {
		// No arguments - use current directory
		m = initialModel(".")
	}

	p := tea.NewProgram(m, tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
