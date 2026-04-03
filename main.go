package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// compressionConfig holds configuration for video compression
type compressionConfig struct {
	maxConcurrent int
	channelBuffer int
	codec         string
	preset        string
	crf           string
	audioBitrate  string
	resolution    string
	outputFormat  string // target container format (mp4, mov, mkv)
}

// validFormats lists the supported output container formats
var validFormats = map[string]bool{
	"mp4": true,
	"mov": true,
	"mkv": true,
}

const (
	outputPrefix = "out_"
)

var (
	defaultConfig = compressionConfig{
		maxConcurrent: 3,
		channelBuffer: 100,
		codec:         "libx264",
		preset:        "slow",
		crf:           "28",
		audioBitrate:  "96k",
		resolution:    "1080",
	}

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
	err      error // Error for this specific file
}

// videoService encapsulates video processing operations
type videoService struct {
	config compressionConfig
}

func newVideoService(config compressionConfig) *videoService {
	return &videoService{config: config}
}

// getOutputPath returns the output path for a given input file.
// If outputFormat is non-empty, the extension is replaced with the target format.
func getOutputPath(inputPath, outputFormat string) string {
	dir := filepath.Dir(inputPath)
	base := filepath.Base(inputPath)
	if outputFormat != "" {
		ext := filepath.Ext(base)
		base = strings.TrimSuffix(base, ext) + "." + outputFormat
	}
	return filepath.Join(dir, outputPrefix+base)
}

// outputFileExists checks if the output file already exists
func outputFileExists(inputPath, outputFormat string) bool {
	output := getOutputPath(inputPath, outputFormat)
	_, err := os.Stat(output)
	return err == nil
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
type cancelMsg struct{ idx int }
type overwriteConfirmMsg struct{}
type overwriteSkipMsg struct{}

func findVideos(dir string) []videoFile {
	var files []videoFile

	if dir == "" {
		return files
	}

	// Validate directory exists and is accessible
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return files
	}

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

		// Skip output files (those with the output prefix)
		if strings.HasPrefix(name, outputPrefix) {
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

func (vs *videoService) getVideoInfo(path string) string {
	codec, _ := vs.runProbe(path, "stream=codec_name", "-select_streams", "v:0")
	res, resErr := vs.runProbe(path, "stream=width,height", "-select_streams", "v:0")
	if resErr != nil {
		return "unable to read video info"
	}
	res = strings.Replace(res, "\n", "x", 1)
	bitrate, _ := vs.runProbe(path, "format=bit_rate")
	if br, err := strconv.ParseFloat(strings.TrimSpace(bitrate), 64); err == nil {
		bitrate = fmt.Sprintf("%.1f Mbps", br/1000000)
	}
	format := strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))
	return fmt.Sprintf("%s | %s (%s) | %s", strings.TrimSpace(res), format, strings.TrimSpace(codec), bitrate)
}

func (vs *videoService) runProbe(path, entries string, extra ...string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	if entries == "" {
		return "", fmt.Errorf("entries cannot be empty")
	}
	args := append([]string{"-v", "error", "-show_entries", entries, "-of", "default=noprint_wrappers=1:nokey=1"}, extra...)
	args = append(args, path)
	out, err := exec.Command("ffprobe", args...).Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	return string(out), nil
}

func (vs *videoService) buildFFmpegCommand(inputPath, outputPath string) *exec.Cmd {
	args := []string{
		"-i", inputPath,
		"-c:v", vs.config.codec, "-preset", vs.config.preset, "-crf", vs.config.crf,
		"-vf", "scale=-2:" + vs.config.resolution,
		"-c:a", "aac", "-b:a", vs.config.audioBitrate,
	}
	// movflags is only valid for MP4/MOV containers
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(outputPath), "."))
	if ext == "mp4" || ext == "mov" || ext == "m4v" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-progress", "pipe:1", "-loglevel", "error", "-y", outputPath)
	return exec.Command("ffmpeg", args...)
}

// Gradient stops for the progress bar: cyan -> green -> orange -> yellow
var progressColorStops = []colorful.Color{
	{R: 0.3, G: 0.8, B: 1.0}, // Cyan (0%)
	{R: 0.2, G: 0.9, B: 0.2}, // Green (33%)
	{R: 1.0, G: 0.5, B: 0.0}, // Orange (66%)
	{R: 1.0, G: 1.0, B: 0.0}, // Yellow (100%)
}

func getProgressColor(progress float64) lipgloss.Color {

	// Clamp progress to [0, 1]
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	// Calculate which segment we're in
	numSegments := float64(len(progressColorStops) - 1)
	segment := progress * numSegments
	segmentIndex := int(segment)

	// Handle edge case for 100%
	if segmentIndex >= len(progressColorStops)-1 {
		c := progressColorStops[len(progressColorStops)-1]
		return lipgloss.Color(c.Hex())
	}

	// Interpolate between the two colors in this segment
	t := segment - float64(segmentIndex)
	c1 := progressColorStops[segmentIndex]
	c2 := progressColorStops[segmentIndex+1]
	interpolated := c1.BlendRgb(c2, t)

	return lipgloss.Color(interpolated.Hex())
}

func initialModel(path string) model {
	files := findVideos(path)
	return newModel(files)
}

func newModel(files []videoFile) model {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithoutPercentage(),
	)

	// Auto-select if there's only one file
	if len(files) == 1 {
		files[0].selected = true
	}

	config := defaultConfig
	return model{
		files:        files,
		progressBar:  p,
		msgChans:     make(map[int]chan tea.Msg),
		runningCmds:  make(map[int]*exec.Cmd),
		config:       config,
		videoService: newVideoService(config),
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

func (m model) handleOverwriteConfirm() (model, tea.Cmd) {
	m.showOverwritePrompt = false
	m.processing = true
	// User is confirming to continue, reset cancellation
	m.userCancelled = false

	var cmds []tea.Cmd

	// Only start the confirmed file if we have capacity
	if m.processingCount < m.config.maxConcurrent {
		// Verify file is still in valid state before processing
		if m.files[m.currentIdx].selected && m.files[m.currentIdx].status == "" {
			cmds = append(cmds, m.processFile(m.currentIdx))
		}
	}

	// Try to start more files up to maxConcurrent
	for i := 0; i < len(m.files); i++ {
		if m.processingCount+len(cmds) >= m.config.maxConcurrent {
			break
		}

		if m.files[i].selected && m.files[i].status == "" && i != m.currentIdx {
			if outputFileExists(m.files[i].path, m.config.outputFormat) {
				// Has overwrite conflict, skip for now
				continue
			}

			// No conflict, start processing
			cmds = append(cmds, m.processFile(i))
		}
	}

	// If we still have capacity and there are unprocessed files, check for next conflict
	if m.processingCount+len(cmds) < m.config.maxConcurrent {
		for i := 0; i < len(m.files); i++ {
			if m.files[i].selected && m.files[i].status == "" && i != m.currentIdx {
				if outputFileExists(m.files[i].path, m.config.outputFormat) {
					// Found next conflict, show prompt
					m.showOverwritePrompt = true
					m.overwriteCursor = 0
					m.pendingOutputFile = getOutputPath(m.files[i].path, m.config.outputFormat)
					m.currentIdx = i
					break
				}
			}
		}
	}

	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m model) handleProcessingStart(msg processingStartMsg) (model, tea.Cmd) {
	// If user cancelled, don't register this file as processing
	if m.userCancelled {
		// Clean up and reset file to allow retry
		m.files[msg.idx].status = ""
		m.files[msg.idx].progress = 0
		delete(m.msgChans, msg.idx)
		return m, nil
	}

	m.files[msg.idx].status = "processing"
	m.runningCmds[msg.idx] = msg.cmd
	m.processingCount++
	// Return listener only for this message's channel
	if ch, ok := m.msgChans[msg.idx]; ok {
		return m, listenToChannel(ch)
	}
	return m, nil
}

func (m model) handleProgress(msg progressMsg) (model, tea.Cmd) {
	m.files[msg.idx].progress = msg.progress
	// Return listener only for this message's channel
	if ch, ok := m.msgChans[msg.idx]; ok {
		return m, listenToChannel(ch)
	}
	return m, nil
}

func (m model) handleVideoInfo(msg videoInfoMsg) (model, tea.Cmd) {
	m.files[msg.idx].info = msg.info
	// Return listener only for this message's channel
	if ch, ok := m.msgChans[msg.idx]; ok {
		return m, listenToChannel(ch)
	}
	return m, nil
}

func (m model) handleOutputInfo(msg outputInfoMsg) (model, tea.Cmd) {
	m.files[msg.idx].outInfo = msg.info
	// Return listener only for this message's channel
	if ch, ok := m.msgChans[msg.idx]; ok {
		return m, listenToChannel(ch)
	}
	return m, nil
}

func (m model) tryStartNextFile() (model, tea.Cmd) {
	// Find next unprocessed selected file only if we have capacity and no prompt is showing
	if m.processingCount < m.config.maxConcurrent && !m.showOverwritePrompt {
		for i := 0; i < len(m.files); i++ {
			if m.files[i].selected && m.files[i].status == "" {
				// Check if file has overwrite conflict
				if outputFileExists(m.files[i].path, m.config.outputFormat) {
					// Has overwrite conflict, show prompt
					m.showOverwritePrompt = true
					m.overwriteCursor = 0
					m.pendingOutputFile = getOutputPath(m.files[i].path, m.config.outputFormat)
					m.currentIdx = i
					return m, nil
				}

				// No conflict, start processing
				return m, m.processFile(i)
			}
		}
	}
	return m, nil
}

func (m model) handleDone(msg doneMsg) (model, tea.Cmd) {
	m.files[msg.idx].status = "done"
	m.files[msg.idx].progress = 1.0
	delete(m.runningCmds, msg.idx)
	delete(m.msgChans, msg.idx)
	if m.processingCount > 0 {
		m.processingCount--
	}

	// Try to start next file
	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	// No more files to process
	if m.processingCount == 0 {
		m.processing = false
		m.done = true
	}
	return m, nil
}

func (m model) handleError(msg errorMsg) (model, tea.Cmd) {
	// Only decrement processingCount if file was actually processing
	wasProcessing := m.files[msg.idx].status == "processing"

	m.files[msg.idx].status = "error"
	m.files[msg.idx].err = msg.err
	m.err = msg.err
	delete(m.runningCmds, msg.idx)
	delete(m.msgChans, msg.idx)

	if wasProcessing && m.processingCount > 0 {
		m.processingCount--
	}

	// Try to start next file
	m, cmd := m.tryStartNextFile()
	if cmd != nil {
		return m, cmd
	}

	// No more files to process
	if m.processingCount == 0 {
		m.processing = false
	}
	return m, nil
}

func (m model) handleCancel(msg cancelMsg) (model, tea.Cmd) {
	// Reset file status so it can be retried
	m.files[msg.idx].status = ""
	m.files[msg.idx].progress = 0
	m.files[msg.idx].err = nil
	delete(m.runningCmds, msg.idx)
	delete(m.msgChans, msg.idx)
	if m.processingCount > 0 {
		m.processingCount--
	}

	if m.processingCount == 0 {
		m.processing = false
	}
	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyMsg) (model, tea.Cmd) {
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
				// Mark as cancelled to prevent new files from starting
				m.userCancelled = true
				// Kill all running processes
				for _, cmd := range m.runningCmds {
					if cmd != nil && cmd.Process != nil {
						cmd.Process.Kill()
					}
				}
				// Unselect all remaining unprocessed files
				for i := 0; i < len(m.files); i++ {
					if m.files[i].selected && m.files[i].status == "" {
						m.files[i].selected = false
						if m.totalToProcess > 0 {
							m.totalToProcess--
						}
					}
				}
				// Mark as done if no files processing
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
			// Mark as cancelled to prevent new files from starting
			m.userCancelled = true
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
	// Don't handle navigation/selection if no files
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
		// Select all unprocessed files and start processing
		for i := range m.files {
			// Only select files that haven't been processed yet
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

func (m model) handleOverwriteSkip() (model, tea.Cmd) {
	m.showOverwritePrompt = false
	// Unselect the skipped file
	m.files[m.currentIdx].selected = false
	if m.totalToProcess > 0 {
		m.totalToProcess--
	}

	// Check if we should start processing or look for next overwrite
	if m.processingCount < m.config.maxConcurrent {
		// Try to start more files
		for i := 0; i < len(m.files); i++ {
			if m.files[i].selected && m.files[i].status == "" {
				if outputFileExists(m.files[i].path, m.config.outputFormat) {
					// Another overwrite conflict, show prompt
					m.showOverwritePrompt = true
					m.overwriteCursor = 0
					m.pendingOutputFile = getOutputPath(m.files[i].path, m.config.outputFormat)
					m.currentIdx = i
					return m, nil
				}

				// No conflict, start processing
				m.processing = true
				return m, m.processFile(i)
			}
		}
	}

	// Check if all done
	if m.processingCount == 0 && m.totalToProcess == 0 {
		m.done = true
	}
	return m, nil
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
	}

	return m, nil
}


func (m *model) startProcessing() tea.Cmd {
	// Reset cancellation flag for new processing session
	m.userCancelled = false

	// Count total files to process (only unprocessed files)
	m.totalToProcess = 0
	for _, f := range m.files {
		if f.selected && f.status == "" {
			m.totalToProcess++
		}
	}

	// Start up to maxConcurrent files
	var cmds []tea.Cmd
	started := 0
	foundOverwrite := false

	for i, f := range m.files {
		if !f.selected || f.status != "" {
			continue
		}

		if started >= m.config.maxConcurrent {
			break
		}

		// Check if output file already exists
		if outputFileExists(f.path, m.config.outputFormat) {
			// File exists, show prompt for first conflict only
			if !foundOverwrite {
				m.showOverwritePrompt = true
				m.overwriteCursor = 0
				m.pendingOutputFile = getOutputPath(f.path, m.config.outputFormat)
				m.currentIdx = i
				foundOverwrite = true
			}
			// Skip this file for now, continue with others
			continue
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
	// Validate index bounds
	if idx < 0 || idx >= len(m.files) {
		return func() tea.Msg {
			return errorMsg{idx: idx, err: fmt.Errorf("invalid file index: %d", idx)}
		}
	}

	msgChan := make(chan tea.Msg, m.config.channelBuffer)
	m.msgChans[idx] = msgChan

	go func() {
		defer close(msgChan)

		f := m.files[idx]

		// Get input info
		info := m.videoService.getVideoInfo(f.path)
		if info != "" {
			msgChan <- videoInfoMsg{idx: idx, info: info}
		}

		output := getOutputPath(f.path, m.config.outputFormat)

		// Get duration for progress calculation
		durStr, err := m.videoService.runProbe(f.path, "format=duration")
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to probe video: %w", err)}
			return
		}
		duration, err := strconv.ParseFloat(strings.TrimSpace(durStr), 64)
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to get video duration: %w", err)}
			return
		}

		cmd := m.videoService.buildFFmpegCommand(f.path, output)

		// Send processing start message with the command
		msgChan <- processingStartMsg{idx: idx, cmd: cmd}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to create stdout pipe: %w", err)}
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to create stderr pipe: %w", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("failed to start ffmpeg: %w", err)}
			return
		}

		scanner := bufio.NewScanner(stdout)
		var stderrBuf strings.Builder

		// Parse progress in a separate goroutine
		progressDone := make(chan struct{})
		go func() {
			defer close(progressDone)
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

		// Capture stderr in case of errors
		stderrDone := make(chan struct{})
		go func() {
			defer close(stderrDone)
			stderrScanner := bufio.NewScanner(stderr)
			for stderrScanner.Scan() {
				stderrBuf.WriteString(stderrScanner.Text())
				stderrBuf.WriteString("\n")
			}
		}()

		waitErr := cmd.Wait()

		// Wait for goroutines to finish reading pipes
		<-progressDone
		<-stderrDone

		if waitErr != nil {
			// Check if process was killed (cancelled)
			if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == -1 {
				// Process was killed, send cancel message and clean up partial output
				if removeErr := os.Remove(output); removeErr != nil && !os.IsNotExist(removeErr) {
					// Log but don't fail on cleanup error
					msgChan <- errorMsg{idx: idx, err: fmt.Errorf("cleanup failed: %w", removeErr)}
					return
				}
				msgChan <- cancelMsg{idx: idx}
				return
			}

			// Include stderr output in error message if available
			errMsg := fmt.Sprintf("ffmpeg failed: %v", waitErr)
			if stderrOutput := stderrBuf.String(); stderrOutput != "" {
				errMsg = fmt.Sprintf("%s\nDetails: %s", errMsg, strings.TrimSpace(stderrOutput))
			}
			msgChan <- errorMsg{idx: idx, err: fmt.Errorf("%s", errMsg)}
			return
		}

		// Get output info
		outInfo := m.videoService.getVideoInfo(output)
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

func collectVideosFromPattern(pattern string) ([]videoFile, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("error expanding pattern: %w", err)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no files or directories match pattern: %s", pattern)
	}

	var allFiles []videoFile
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}

		if info.IsDir() {
			dirFiles := findVideos(match)
			allFiles = append(allFiles, dirFiles...)
		} else {
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
		return nil, fmt.Errorf("no video files found matching pattern: %s", pattern)
	}

	return allFiles, nil
}

func createModelFromPath(targetPath string) (model, error) {
	// Check for wildcard pattern
	if strings.Contains(targetPath, "*") {
		files, err := collectVideosFromPattern(targetPath)
		if err != nil {
			return model{}, err
		}
		return newModel(files), nil
	}

	// Get file info to determine if it's a file or directory
	info, err := os.Stat(targetPath)
	if err != nil {
		return model{}, err
	}

	if info.IsDir() {
		return initialModel(targetPath), nil
	}

	// It's a file - validate it's a video
	ext := strings.ToLower(filepath.Ext(targetPath))
	if !videoExtensions[ext] {
		return model{}, fmt.Errorf("%s is not a supported video file\nSupported extensions: .mp4, .mov, .avi, .mkv, .m4v", targetPath)
	}

	// Create model with single file
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return model{}, fmt.Errorf("error resolving path: %w", err)
	}

	return newModel([]videoFile{
		{
			path:     absPath,
			name:     filepath.Base(targetPath),
			selected: true,
		},
	}), nil
}

func getInstallCommand() (osName, command string) {
	switch runtime.GOOS {
	case "darwin":
		return "macOS", "brew install ffmpeg"
	case "linux":
		// Try to detect the Linux distribution
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
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try different clipboard utilities
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			return fmt.Errorf("no clipboard utility found (install xclip, xsel, or wl-copy)")
		}
	case "windows":
		cmd = exec.Command("clip")
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

	if _, err := pipe.Write([]byte(text)); err != nil {
		return err
	}

	if err := pipe.Close(); err != nil {
		return err
	}

	return cmd.Wait()
}

type errorModel struct {
	osName  string
	command string
	copied  bool
	err     string
}

func newErrorModel() errorModel {
	osName, command := getInstallCommand()
	return errorModel{
		osName:  osName,
		command: command,
	}
}

func (m errorModel) Init() tea.Cmd {
	return nil
}

func (m errorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "c", "enter":
			if err := copyToClipboard(m.command); err != nil {
				m.err = err.Error()
			} else {
				m.copied = true
			}
			return m, nil
		}
	}
	return m, nil
}

func (m errorModel) View() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(1, 2).
		Width(70)

	errorTitleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("196"))

	commandBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("82")).
		Padding(0, 1).
		Foreground(lipgloss.Color("82")).
		Bold(true)

	copyButtonStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("117")).
		Background(lipgloss.Color("236")).
		Padding(0, 2)

	var content strings.Builder
	content.WriteString(errorTitleStyle.Render("⚠️  Missing Dependency"))
	content.WriteString("\n\n")
	content.WriteString(normalStyle.Render("ffmpeg is not installed on your system."))
	content.WriteString("\n")
	content.WriteString(dimStyle.Render("This tool requires ffmpeg for video compression."))
	content.WriteString("\n\n")
	content.WriteString(infoStyle.Render("Installation Instructions for " + m.osName + ":"))
	content.WriteString("\n\n")
	content.WriteString(commandBoxStyle.Render(m.command))
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
		content.WriteString(copyButtonStyle.Render("Press 'c' or 'enter' to copy"))
		content.WriteString("\n")
		content.WriteString(helpTextStyle.Render("Or select the command above with your mouse"))
	}

	content.WriteString("\n\n")
	content.WriteString(dimStyle.Render("Press ") + keyStyle.Render("q") + dimStyle.Render(" to exit"))

	return "\n" + boxStyle.Render(content.String()) + "\n"
}

func displayInstallationHelp() {
	m := newErrorModel()
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
}

func checkDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return fmt.Errorf("ffprobe not found")
	}
	return nil
}

// parseArgs extracts the --format flag and positional path from args in any order.
func parseArgs(args []string) (format, path string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--format" || args[i] == "-format" {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --format requires a value\nSupported formats: mp4, mov, mkv\n")
				os.Exit(1)
			}
			format = args[i+1]
			i++ // skip the value
		} else {
			path = args[i]
		}
	}
	return
}

func main() {
	// Check dependencies before starting
	if err := checkDependencies(); err != nil {
		displayInstallationHelp()
		os.Exit(1)
	}

	format, path := parseArgs(os.Args[1:])

	if format != "" && !validFormats[format] {
		fmt.Fprintf(os.Stderr, "Error: unsupported format %q\nSupported formats: mp4, mov, mkv\n", format)
		os.Exit(1)
	}

	var m model
	var err error

	if path != "" {
		m, err = createModelFromPath(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		m = initialModel(".")
	}

	m.config.outputFormat = format
	m.videoService = newVideoService(m.config)

	p := tea.NewProgram(m, tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
