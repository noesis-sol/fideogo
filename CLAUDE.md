# Fideogo - Video Compressor TUI

## Project Description

Fideogo is a terminal user interface (TUI) application for compressing video files using ffmpeg. It provides an interactive interface for selecting videos in the current directory and compressing them with optimized settings (H.264, 1080p, reduced bitrate).

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

## IMPORTANT: Build & Deploy Instructions for AI Agents

When making changes to this project, follow these steps:

### 1. Find the Binary Location

**ALWAYS** run this command first to find where the binary is installed:

```bash
which fideogo
```

This will return the full path (e.g., `/Users/michailmichailidis/.local/bin/fideogo`)

### 2. After Completing Any Task

**ALWAYS** compile and replace the binary at the location found in step 1:

```bash
go build -o fideogo main.go && mv fideogo <FULL_PATH_FROM_WHICH_COMMAND>
```

For example:
```bash
go build -o fideogo main.go && mv fideogo /Users/michailmichailidis/.local/bin/fideogo
```

### 3. Never Skip This Step

The user expects the binary to be updated after every code change. Do not ask permission - just do it as part of completing the task.

## Project Structure

```
compress-tui/
├── main.go          # Main application code
├── go.mod           # Go module dependencies
├── go.sum           # Dependency checksums
└── CLAUDE.md        # This file
```

## ffmpeg Settings Used

The application compresses videos with these settings:
- Codec: H.264 (libx264)
- Preset: slow (better compression)
- CRF: 28 (quality level)
- Resolution: Scaled to 1080p height
- Audio: AAC at 96k bitrate
- Output: Prefixed with `out_` in the same directory

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
