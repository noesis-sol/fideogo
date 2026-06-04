package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// program is set in main() so worker goroutines can push messages via
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

// parseFlagValue extracts the value from --flag=value or --flag value syntax.
// Returns the value and how many args were consumed (0 for =value, 1 for space-separated).
func parseFlagValue(args []string, i int, name, hint string) (string, int) {
	arg := args[i]
	if eqIdx := strings.Index(arg, "="); eqIdx != -1 {
		val := arg[eqIdx+1:]
		if val == "" {
			fmt.Fprintf(os.Stderr, "Error: %s requires a value\n%s\n", name, hint)
			os.Exit(1)
		}
		return val, 0
	}
	if i+1 >= len(args) {
		fmt.Fprintf(os.Stderr, "Error: %s requires a value\n%s\n", name, hint)
		os.Exit(1)
	}
	return args[i+1], 1
}

// parseArgs extracts --format, --size, --hw flags and positional path from args in any order.
func parseArgs(args []string) (format, size, path string, hw bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flagName := strings.SplitN(arg, "=", 2)[0]

		if flagName == "--format" || flagName == "-format" {
			val, skip := parseFlagValue(args, i, "--format", "Supported formats: mp4, mov, mkv, webm")
			format = val
			i += skip
		} else if flagName == "--size" || flagName == "-size" {
			val, skip := parseFlagValue(args, i, "--size", "Supported sizes: sm, small, md, medium, lg, large")
			size = val
			i += skip
		} else if arg == "--hw" || arg == "-hw" {
			hw = true
		} else if args[i] == "--help" || args[i] == "-h" {
			fmt.Print("Usage: fideogo [options] [path|pattern]\n\n")
			fmt.Print("Options:\n")
			fmt.Print("  --format <fmt>   Output format: mp4, mov, mkv, webm (default: mp4)\n")
			fmt.Print("  --size <size>    Target size: sm/small (540p), md/medium (1080p), lg/large (2160p)\n")
			fmt.Print("  --hw             Use hardware encoder (VideoToolbox/NVENC/QSV/AMF) — much faster\n")
			fmt.Print("  --help, -h       Show this help message\n\n")
			fmt.Print("Examples:\n")
			fmt.Print("  fideogo                     Compress videos in current directory\n")
			fmt.Print("  fideogo video.mp4            Compress a single file\n")
			fmt.Print("  fideogo /path/to/dir         Compress videos in a directory\n")
			fmt.Print("  fideogo '*.mov'              Compress matching files\n")
			fmt.Print("  fideogo --format mkv .       Convert to MKV format\n")
			fmt.Print("  fideogo --size sm video.mp4  Compress to 540p\n")
			fmt.Print("  fideogo --hw video.mov       Use hardware encoder\n")
			os.Exit(0)
		} else if path != "" {
			fmt.Fprintf(os.Stderr, "Error: unexpected argument %q (only one path allowed)\n", args[i])
			fmt.Fprint(os.Stderr, "Hint: if you used a wildcard, quote it so the shell doesn't expand it: fideogo '*.mov'\n")
			os.Exit(1)
		} else {
			path = args[i]
		}
	}
	return
}

func createModelFromPath(targetPath string) (model, error) {
	if strings.ContainsAny(targetPath, "*?[") {
		files, err := collectVideosFromPattern(targetPath)
		if err != nil {
			return model{}, err
		}
		return newModel(files), nil
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return model{}, err
	}

	if info.IsDir() {
		return initialModel(targetPath), nil
	}

	ext := strings.ToLower(filepath.Ext(targetPath))
	if !videoExtensions[ext] {
		return model{}, fmt.Errorf("%s is not a supported video file\nSupported extensions: .mp4, .mov, .avi, .mkv, .m4v, .webm", targetPath)
	}

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

func main() {
	if err := checkDependencies(); err != nil {
		displayInstallationHelp()
		os.Exit(1)
	}

	format, size, path, hw := parseArgs(os.Args[1:])

	// Default to mp4 rather than preserving the source container, so a WebM
	// (or any) input isn't forced through the slow software VP9 encoder.
	if format == "" {
		format = defaultOutputFormat
	}

	if format != "" && !validFormats[format] {
		fmt.Fprintf(os.Stderr, "Error: unsupported format %q\nSupported formats: mp4, mov, mkv, webm\n", format)
		os.Exit(1)
	}

	if hw && format == "webm" {
		// h264_videotoolbox emits H.264, which a WebM container can't hold;
		// WebM always goes through the software VP9 encoder instead.
		fmt.Fprintf(os.Stderr, "Error: --hw is not compatible with --format webm (WebM requires VP9)\n")
		os.Exit(1)
	}

	var resolution string
	if size != "" {
		res, ok := validSizes[strings.ToLower(size)]
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unsupported size %q\nSupported sizes: sm, small, md, medium, lg, large\n", size)
			os.Exit(1)
		}
		resolution = res
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

	if resolution != "" {
		m.config.resolution = resolution
	}
	m.config.outputFormat = format
	m.config.hwAccel = hw
	if hw {
		encoder, err := resolveHWEncoder()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		m.config.hwEncoder = encoder

		// Hardware encoders offload to a small fixed number of dedicated engines
		// (Apple Silicon: 1 on base/Pro, 2 on Max, 4 on Ultra; NVENC/QSV/AMF
		// likewise cap concurrent sessions) — unrelated to CPU count.
		// Oversubscribing just serializes at the driver while adding per-process
		// memory and scheduling overhead, so we cap low. A little parallelism
		// still overlaps each file's probe, audio encode, and I/O with another
		// file's video encode.
		const hwMaxConcurrent = 2
		if hwMaxConcurrent > m.config.maxConcurrent {
			m.config.maxConcurrent = hwMaxConcurrent
		}
	}
	m.videoService = newVideoService(m.config)

	program = tea.NewProgram(m, tea.WithoutSignalHandler())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
