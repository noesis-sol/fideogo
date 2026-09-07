package fideogo

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// program is set in Run() so worker goroutines can push messages via
// program.Send instead of routing through per-file channels.
var program *tea.Program

func checkDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return fmt.Errorf("ffprobe not found")
	}
	return nil
}

const usageText = `Usage: fideogo [options] [path|pattern ...]

Options:
  --format <fmt>   Output format: mp4, mov, mkv, webm (default: mp4)
  --size <size>    Target size: sm/small (540p), md/medium (1080p), lg/large (2160p)
  --hw             Use hardware encoder (VideoToolbox/NVENC/QSV/AMF) — much faster
  --overwrite      Replace each source file with its compressed result (no out_ prefix)
  --               Treat every following argument as a path (for names starting with -)
  --help, -h       Show this help message

Examples:
  fideogo                       Compress videos in current directory
  fideogo video.mp4             Compress a single file
  fideogo a.mp4 b.mov clip.mkv  Compress several files
  fideogo */videos/*.mp4        Compress shell-expanded matches
  fideogo /path/to/dir          Compress videos in a directory
  fideogo '*.mov'               Compress matching files
  fideogo --format mkv .        Convert to MKV format
  fideogo --size sm video.mp4   Compress to 540p
  fideogo --hw video.mov        Use hardware encoder
  fideogo --overwrite video.mp4 Replace the original with the compressed file
  fideogo -- -clip.mp4          Compress a file whose name starts with a dash
`

// cliOptions is the parsed command line.
type cliOptions struct {
	format    string
	size      string
	paths     []string
	hw        bool
	overwrite bool
	help      bool
}

// parseArgs extracts the --format, --size, --hw, --overwrite and --help flags
// and the positional paths from args, in any order, so shell-expanded wildcards
// (e.g. */videos/*.mp4) just work without quoting. Anything else that starts
// with "-" is an unknown option and is rejected — silently trying it as a file
// name used to surface as a baffling "stat --verbose: no such file". A "--"
// ends option parsing so a file whose name begins with "-" can still be named.
// Errors are returned rather than exiting so the parser is unit-testable; Run
// owns the exit codes.
func parseArgs(args []string) (cliOptions, error) {
	var o cliOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.paths = append(o.paths, args[i+1:]...)
			break
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		switch name {
		case "--format", "-format":
			val, skip, err := flagValue(args, i, "--format", inline, hasInline, "Supported formats: mp4, mov, mkv, webm")
			if err != nil {
				return o, err
			}
			o.format, i = val, i+skip
		case "--size", "-size":
			val, skip, err := flagValue(args, i, "--size", inline, hasInline, "Supported sizes: sm, small, md, medium, lg, large")
			if err != nil {
				return o, err
			}
			o.size, i = val, i+skip
		case "--hw", "-hw", "--overwrite", "-overwrite", "--help", "-h":
			if hasInline {
				return o, fmt.Errorf("option %s takes no value", name)
			}
			switch name {
			case "--hw", "-hw":
				o.hw = true
			case "--overwrite", "-overwrite":
				o.overwrite = true
			default:
				o.help = true
			}
		default:
			if len(arg) > 1 && strings.HasPrefix(arg, "-") {
				return o, fmt.Errorf("unknown option %q (put -- before file names that start with a dash)", arg)
			}
			o.paths = append(o.paths, arg)
		}
	}
	return o, nil
}

// flagValue returns the value of a --flag=value or --flag value option and how
// many extra args it consumed (0 for the inline form, 1 for the separate one).
func flagValue(args []string, i int, name, inline string, hasInline bool, hint string) (string, int, error) {
	if hasInline {
		if inline == "" {
			return "", 0, fmt.Errorf("%s requires a value\n%s", name, hint)
		}
		return inline, 0, nil
	}
	if i+1 >= len(args) {
		return "", 0, fmt.Errorf("%s requires a value\n%s", name, hint)
	}
	return args[i+1], 1, nil
}

// collectVideosFromArg resolves a single CLI argument — a glob pattern, a
// directory, or a file — into the video files it refers to. An explicitly
// named file is pre-selected; directories and patterns are left unselected so
// the user picks them in the TUI (newModel auto-selects when only one results).
func collectVideosFromArg(arg string) ([]videoFile, error) {
	// A real file or directory by this exact name takes precedence over glob
	// interpretation, so inputs whose names literally contain glob metacharacters
	// ([ ] * ?) — common from duplicate-download renaming like "clip[1].mov" —
	// stay reachable instead of being mis-parsed as a (usually non-matching)
	// pattern and rejected.
	if info, err := os.Stat(arg); err == nil {
		if info.IsDir() {
			return findVideos(arg), nil
		}
		ext := strings.ToLower(filepath.Ext(arg))
		if !videoExtensions[ext] {
			return nil, fmt.Errorf("%s is not a supported video file\nSupported extensions: .mp4, .mov, .avi, .mkv, .m4v, .webm", arg)
		}
		absPath, err := filepath.Abs(arg)
		if err != nil {
			return nil, fmt.Errorf("error resolving path: %w", err)
		}
		return []videoFile{{
			path:     absPath,
			name:     filepath.Base(arg),
			selected: true,
		}}, nil
	}

	// No literal match: an arg with metacharacters is treated as a glob pattern.
	if strings.ContainsAny(arg, "*?[") {
		return collectVideosFromPattern(arg)
	}

	// Neither a literal path nor a pattern: surface the stat error (e.g. ENOENT).
	_, err := os.Stat(arg)
	return nil, err
}

// createModelFromPaths merges the videos referenced by every positional
// argument into one model, deduping by absolute path. A single argument keeps
// the previous behavior (directory discovery, single-file auto-start).
func createModelFromPaths(paths []string) (model, error) {
	var allFiles []videoFile
	seen := make(map[string]bool)

	for _, p := range paths {
		files, err := collectVideosFromArg(p)
		if err != nil {
			return model{}, err
		}
		for _, f := range files {
			// findVideos (the directory branch of collectVideosFromArg) returns
			// relative paths, so re-resolve to absolute before deduping — the same
			// file reached as a directory entry and as an explicit file arg would
			// otherwise get two different keys and slip past the dedup.
			key := f.path
			if abs, err := filepath.Abs(f.path); err == nil {
				key = abs
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			allFiles = append(allFiles, f)
		}
	}

	if len(allFiles) == 0 {
		return model{}, fmt.Errorf("no video files found in the given paths")
	}
	return newModel(allFiles), nil
}

// Run is the application entry point: it parses CLI flags and positional
// paths, checks dependencies, builds the initial model, and starts the Bubble
// Tea program. It owns process exit codes (via os.Exit) so cmd/fideogo stays a
// thin shell around this package.
func Run() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'fideogo --help' for usage.\n", err)
		os.Exit(1)
	}
	if opts.help {
		fmt.Print(usageText)
		os.Exit(0)
	}

	// Dependencies are checked only once the command line is valid, so --help
	// and usage errors work on a machine that has no ffmpeg yet.
	if err := checkDependencies(); err != nil {
		displayInstallationHelp()
		os.Exit(1)
	}

	// Container names are case-insensitive on the command line (--format MP4),
	// like the --size presets.
	format := strings.ToLower(opts.format)
	if format != "" && !validFormats[format] {
		fmt.Fprintf(os.Stderr, "Error: unsupported format %q\nSupported formats: mp4, mov, mkv, webm\n", opts.format)
		os.Exit(1)
	}

	// Default to mp4 rather than preserving the source container, so a WebM (or
	// any) input isn't forced through the slow software VP9 encoder. In overwrite
	// mode an unspecified --format instead means "keep each file's own container",
	// so a plain --overwrite recompresses in place without converting the file (and
	// deleting the original under a new extension).
	if format == "" && !opts.overwrite {
		format = defaultOutputFormat
	}

	if opts.hw && !profileFor(format).allowsHW {
		// h264_videotoolbox emits H.264, which a WebM container can't hold;
		// WebM always goes through the software VP9 encoder instead.
		fmt.Fprintf(os.Stderr, "Error: --hw is not compatible with --format %s (it requires a software codec)\n", format)
		os.Exit(1)
	}

	var resolution string
	if opts.size != "" {
		res, ok := validSizes[strings.ToLower(opts.size)]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unsupported size %q\nSupported sizes: sm, small, md, medium, lg, large\n", opts.size)
			os.Exit(1)
		}
		resolution = res
	}

	var m model
	if len(opts.paths) > 0 {
		m, err = createModelFromPaths(opts.paths)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		m = initialModel(".")
	}

	if resolution != "" {
		m.config.resolution = resolution
	}
	m.config.outputFormat = format
	m.config.hwAccel = opts.hw
	m.config.inPlace = opts.overwrite
	if opts.hw {
		encoder, err := resolveHWEncoder()
		if err != nil {
			// No usable hardware encoder — fall back to software instead of
			// failing every file. resolveHWEncoder only errors after a real
			// probe-encode failed, so software (libx264) is the correct path.
			fmt.Fprintf(os.Stderr, "Warning: %v\nFalling back to software (libx264) encoding.\n", err)
			m.config.hwAccel = false
		} else {
			m.config.hwEncoder = encoder

			// Hardware encoders offload to a small fixed number of dedicated
			// engines (Apple Silicon: 1 on base/Pro, 2 on Max, 4 on Ultra;
			// NVENC/QSV/AMF likewise cap concurrent sessions) — unrelated to CPU
			// count. Oversubscribing just serializes at the driver while adding
			// per-process memory and scheduling overhead, so we cap low. A little
			// parallelism still overlaps each file's probe, audio encode, and I/O
			// with another file's video encode.
			const hwMaxConcurrent = 2
			if hwMaxConcurrent < m.config.maxConcurrent {
				m.config.maxConcurrent = hwMaxConcurrent
			}
		}
	}
	m.videoService = newVideoService(m.config)

	// In overwrite mode without --format (format == ""), each file keeps its own
	// container. A WebM source can't carry H.264, so --hw silently falls back to
	// software VP9 for it — warn rather than let the user believe hardware encoding
	// applied to every file.
	if m.config.hwAccel && format == "" {
		webm := 0
		for _, f := range m.files {
			if !profileFor(containerOf(f.path)).allowsHW {
				webm++
			}
		}
		if webm > 0 {
			fmt.Fprintf(os.Stderr, "Warning: --hw cannot produce WebM (VP9) output; %d source(s) will be encoded with software VP9 instead.\n", webm)
		}
	}

	program = tea.NewProgram(m, tea.WithoutSignalHandler())

	// Bubble Tea consumes Ctrl+C as a key while the terminal is in raw mode, but an
	// external SIGINT/SIGTERM/SIGHUP (kill, closed terminal) would otherwise
	// terminate the process abruptly — skipping the terminal restore and leaving
	// in-flight ffmpeg children running. Translate those signals into a graceful
	// Quit so Run() returns and the cleanup below executes.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		program.Quit()
	}()

	finalModel, runErr := program.Run()
	// However the program ended (normal quit, signal, or error), cancel every
	// still-registered encode so no ffmpeg child is orphaned past our exit…
	if fm, ok := finalModel.(model); ok {
		for _, cancel := range fm.cancels {
			cancel()
		}
	}
	// …then let the workers finish their cleanup: each removes its partial output
	// only after ffmpeg has died, and exiting first would leave a truncated out_
	// file or scratch temp behind. Bounded, so a wedged worker can't hold the quit.
	m.videoService.waitWorkers(workerDrainTimeout)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		os.Exit(1)
	}
}
