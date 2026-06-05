# Fideogo - Video Compressor TUI

## Project Description

Fideogo is a terminal user interface (TUI) application for compressing video files using ffmpeg. It provides an interactive interface for selecting videos — from the current directory, explicit paths, or shell globs — and compressing them with sensible defaults (H.264, 1080p, CRF 28), plus optional format, size, and hardware-encoder overrides.

## Language

**Go 1.24.0**

## Libraries Used

### Charm Ecosystem
- **github.com/charmbracelet/bubbletea** v1.3.10 - Elm-inspired TUI framework (main architecture)
- **github.com/charmbracelet/bubbles** v0.21.0 - Reusable TUI components (progress bar)
- **github.com/charmbracelet/lipgloss** v1.1.0 - Style definitions and terminal styling
- **github.com/charmbracelet/harmonica** v0.2.0 - Spring-based animations
- **github.com/charmbracelet/colorprofile** v0.2.3 - Color profile detection
- **github.com/charmbracelet/x/ansi** v0.10.1 - ANSI escape code utilities
- **github.com/charmbracelet/x/cellbuf** v0.0.13 - Terminal cell buffer
- **github.com/charmbracelet/x/term** v0.2.1 - Terminal utilities

### Other Dependencies
- **github.com/lucasb-eyer/go-colorful** v1.2.0 - Color manipulation and interpolation

## Key Features

- Interactive file selection with checkbox interface
- Real-time progress tracking with color-coded percentage display
- Gradient color progress indicator (cyan → green → orange → yellow)
- Video metadata display (resolution, codec, bitrate)
- Cancel rendering mid-process (press 'c' or ctrl+c)
- Auto-compression with optimized ffmpeg settings
- Batch compression of multiple files with bounded concurrency
- Output format conversion (mp4/mov/mkv/webm) and size presets (`--size`)
- Optional hardware-accelerated encoding (`--hw`: VideoToolbox/NVENC/QSV/AMF)
- Overwrite prompt (overwrite / skip / cancel) with collision-safe output naming

## IMPORTANT: Build & Deploy Instructions for AI Agents

When making changes to this project, follow these steps:

### 1. Find the Binary Location

**ALWAYS** run this command first to find where the binary is installed:

```bash
which fideogo
```

This will return the full path (e.g., `/Users/<username>/.local/bin/fideogo`)

### 2. After Completing Any Task

**ALWAYS** compile and replace the binary at the location found in step 1. The
entry point lives under `./cmd/fideogo`, so build that package (not `.`):

```bash
go build -o fideogo ./cmd/fideogo && mv fideogo <FULL_PATH_FROM_WHICH_COMMAND>
```

For example:
```bash
go build -o fideogo ./cmd/fideogo && mv fideogo /Users/michailmichailidis/.local/bin/fideogo
```

### 3. Never Skip This Step

The user expects the binary to be updated after every code change. Do not ask permission - just do it as part of completing the task.

## Project Structure

The repo follows the `/cmd` + `/internal` split from the community
[golang-standards/project-layout](https://github.com/golang-standards/project-layout):
a thin executable under `cmd/`, all application code in a single private package
under `internal/` (so it can't be imported by other modules and tests keep
white-box access to unexported helpers).

```
fideogo/
├── cmd/
│   └── fideogo/
│       └── main.go      # Thin entry point: calls fideogo.Run()
├── internal/
│   └── fideogo/         # package fideogo — all application code
│       ├── app.go       # Run() entry: CLI parsing, dep check, program bootstrap
│       ├── config.go    # compressionConfig, defaults, autoMaxConcurrent, validators
│       ├── discover.go  # videoFile + fileStatus enum, findVideos, collectVideosFromPattern
│       ├── probe.go     # videoMetadata + ffprobe (single-call) + formatVideoInfo
│       ├── encode.go    # videoService, buildFFmpegCommand, processFile worker, streamProgress/drainStderr
│       ├── encode_test.go # ffmpegArgs unit + golden tests (go-cmp)
│       ├── model.go     # Bubble Tea model, msg types, Init, Update dispatch
│       ├── handlers.go  # handleX methods, fillSlots state machine, batchSettled predicate
│       ├── view.go      # View() + renderX helpers, lipgloss styles, gradient + precomputed percent tables
│       └── errorui.go   # Missing-ffmpeg installation help dialog
├── go.mod               # Go module dependencies
├── go.sum               # Dependency checksums
└── CLAUDE.md            # This file
```

Note: file references elsewhere in this doc (e.g. "encode.go", "config.go") now
live under `internal/fideogo/`.

## Testing

Tests live beside the code in `internal/fideogo` (white-box, `package fideogo`).

- **Unit tests** — `go test ./...`. Pure and hermetic: they never exec ffmpeg.
  OS-specific logic is *injectable* rather than reading `runtime.GOOS` / the real
  filesystem inline, so every platform branch is covered on a single host:
  - `decodeArgs(meta, goos)` and `hwEncoderCandidates(goos)` take the target OS as
    a parameter (production callers pass `runtime.GOOS`); tests pass
    darwin/linux/windows.
  - `deviceProbe{goos, root}` points the Linux GPU-node probe at a synthetic
    `/dev` + `/sys` tree under a temp dir, so NVENC/QSV/AMF detection is tested
    without a real GPU or a Linux host.

  When adding OS- or filesystem-dependent behavior, thread the dependency in the
  same way instead of calling `runtime.GOOS` / `os` directly — keep it
  unit-testable on one machine.

- **Integration tests** — `go test -tags=integration ./...`. Behind the
  `integration` build tag: they generate a tiny clip, run a real ffmpeg encode end
  to end, then ffprobe the result. Skipped automatically when ffmpeg/ffprobe are
  not on PATH, so the default `go test` stays hermetic.

- **Local Linux reproduction** — `docker build -f Dockerfile.test -t fideogo-test .`
  then `docker run --rm fideogo-test`. Runs vet + unit + integration on Linux with
  a real ffmpeg, exercising the `GOOS=linux` paths. (Hardware encoders still need a
  real GPU; macOS/VideoToolbox can't run in a Linux container.)

- **CI** — `.github/workflows/ci.yml` runs the suite (including integration) on a
  matrix of `ubuntu-latest` and `macos-latest`, installing ffmpeg on each, so the
  darwin and linux paths run on their actual platforms.

## ffmpeg Settings Used

Defaults below; most are overridable via CLI flags (run with `--help`). The values
live in `defaultConfig` (config.go) and are assembled into ffmpeg arguments in
encode.go (`ffmpegArgs` / `profileFor`).

- Container: `mp4` (`--format mp4|mov|mkv|webm`)
- Video: H.264 / libx264, `-preset medium`, `-crf 28`
  - `--hw` substitutes a hardware H.264 encoder (VideoToolbox / NVENC / QSV / AMF)
    when one initializes successfully, falling back to software otherwise
  - `webm` output uses libvpx-vp9 (`-crf 28 -b:v 0`) since the container can't carry H.264
- Resolution: scaled to 1080p height and never upscaled (`scale=-2:'min(1080,ih)'`);
  `--size sm|md|lg` selects 540 / 1080 / 2160
- Audio: AAC at 96k (Opus at 96k for webm)
- Concurrency: software encodes are thread-capped per job so parallel jobs don't
  thrash; hardware runs cap at 2 concurrent jobs
- Output: written next to the source with an `out_` prefix, with collision-safe
  naming within a batch

## Rendering & Performance Notes

Bubble Tea calls `model.View()` after *every* message; the terminal write is
separately throttled to ~60fps and skipped when the frame is unchanged. Two rules
follow from that and are easy to regress:

- **Keep `View()` cheap.** It is split into `renderX` helpers, and the per-frame
  percentage readout indexes the precomputed `percentStyles` / `percentLabels`
  tables (one entry per whole percent) instead of blending a gradient color and
  allocating a `lipgloss.Style` on every frame. Don't move color math or style
  allocation back into the render path.
- **Coalesce progress at the source.** `streamProgress` (encode.go) forwards at
  most one `progressMsg` per whole percent. ffmpeg prints progress blocks many
  times a second, and every forwarded message rebuilds the entire View across all
  concurrent files — so gate on the displayed granularity, not raw ffmpeg output.

Per-file lifecycle state is the typed `fileStatus` enum (`statusPending` /
`statusProcessing` / `statusDone` / `statusError`), not strings; `statusPending`
is the zero value, so a freshly discovered file is unprocessed by default.

## Go Design Patterns

- **Prefer composition over inheritance—embed structs rather than building deep hierarchies**
```go
  // Good: composition via embedding
  type VideoCompressor struct {
      *FFmpegEncoder
      logger Logger
  }

  // Avoid: simulating inheritance through deep struct chains
```

- **Use interfaces at consumption sites, not declaration sites; accept interfaces, return concrete types**
```go
  // Good: interface defined where it's used
  type Encoder interface {
      Encode(input []byte) ([]byte, error)
  }

  func Compress(e Encoder, data []byte) ([]byte, error) {
      return e.Encode(data)
  }

  // The concrete type doesn't declare "implements Encoder"—it just does
```

- **Apply the functional options pattern for configurable constructors**
```go
  type Option func(*Compressor)

  func WithBitrate(b int) Option {
      return func(c *Compressor) { c.bitrate = b }
  }

  func WithCodec(codec string) Option {
      return func(c *Compressor) { c.codec = codec }
  }

  func NewCompressor(opts ...Option) *Compressor {
      c := &Compressor{bitrate: 1000, codec: "h264"} // defaults
      for _, opt := range opts {
          opt(c)
      }
      return c
  }

  // Usage: NewCompressor(WithBitrate(2000), WithCodec("hevc"))
```

- **Keep functions short and single-purpose; if a function exceeds 30 lines, it likely needs decomposition**
```go
  // Instead of one 100-line function:
  func ProcessVideo(path string) error {
      meta, err := extractMetadata(path)
      if err != nil {
          return err
      }
      normalized, err := normalizeAudio(path, meta)
      if err != nil {
          return err
      }
      return compress(normalized, meta)
  }
```

- **Use table-driven tests**
```go
  func TestBitrateCalculation(t *testing.T) {
      tests := []struct {
          name     string
          width    int
          height   int
          expected int
      }{
          {"SD", 640, 480, 1000},
          {"HD", 1920, 1080, 4000},
          {"4K", 3840, 2160, 12000},
      }

      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) {
              got := CalculateBitrate(tt.width, tt.height)
              if got != tt.expected {
                  t.Errorf("got %d, want %d", got, tt.expected)
              }
          })
      }
  }
```

- **Avoid package-level state and `init()` functions; pass dependencies explicitly**
```go
  // Avoid
  var globalEncoder *Encoder

  func init() {
      globalEncoder = NewEncoder()
  }

  // Good: explicit dependency injection
  func NewService(encoder *Encoder, logger Logger) *Service {
      return &Service{encoder: encoder, logger: logger}
  }
```

- **When error handling becomes repetitive, extract a helper or use a scanner-style pattern**
```go
  type Pipeline struct {
      err error
  }

  func (p *Pipeline) Run(step func() error) {
      if p.err != nil {
          return // skip if already failed
      }
      p.err = step()
  }

  // Usage
  p := &Pipeline{}
  p.Run(func() error { return validateInput(path) })
  p.Run(func() error { return extractAudio(path) })
  p.Run(func() error { return compress(path) })
  if p.err != nil {
      return p.err
  }
```

- **Prefer channels for coordination and mutexes for state protection—don't mix metaphors**
```go
  // Channels for signaling/coordination
  done := make(chan struct{})
  go func() {
      processVideo()
      close(done)
  }()
  <-done

  // Mutex for protecting shared state
  type Stats struct {
      mu    sync.Mutex
      count int
  }

  func (s *Stats) Increment() {
      s.mu.Lock()
      s.count++
      s.mu.Unlock()
  }
```

- **Name interfaces by what they do with an -er suffix, not what they are**
```go
  // Good
  type Compressor interface {
      Compress(data []byte) ([]byte, error)
  }

  type ProgressReporter interface {
      Report(percent float64)
  }

  // Avoid
  type VideoInterface interface { ... }
  type CompressionManager interface { ... }
```

- **Keep the happy path unindented; handle errors and edge cases first with early returns**
```go
  // Good: happy path at left margin
  func Compress(path string) (*Result, error) {
      if path == "" {
          return nil, errors.New("empty path")
      }
      if !fileExists(path) {
          return nil, errors.New("file not found")
      }

      data, err := os.ReadFile(path)
      if err != nil {
          return nil, fmt.Errorf("reading file: %w", err)
      }

      return process(data), nil
  }

  // Avoid: deeply nested happy path
  func Compress(path string) (*Result, error) {
      if path != "" {
          if fileExists(path) {
              data, err := os.ReadFile(path)
              if err == nil {
                  return process(data), nil
              }
          }
      }
      return nil, errors.New("failed")
  }
```
