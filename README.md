# 🎬 FideoGo - Video Compressor with Charm TUI

<div align="center">

![Gemini_Generated_Image_97rzu397rzu397rz](https://github.com/user-attachments/assets/f1a5f222-ae95-4653-ad2a-63efae30858b)


**A beautiful terminal interface for compressing video files with ffmpeg**

[![Go Version](https://img.shields.io/badge/Go-1.24.0-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![Built with Charm](https://img.shields.io/badge/Built%20with-Charm-FF69B4?style=for-the-badge)](https://charm.sh/)

</div>

---

## ✨ Features

- 🎯 **Interactive Selection** - Browse and select multiple videos with an intuitive checkbox interface
- 📊 **Real-time Progress** - Watch compression progress with a gradient color indicator (cyan → green → orange → yellow)
- 📹 **Video Metadata** - View resolution, codec, and bitrate for input and output files
- ⚠️ **Smart Overwrite Protection** - Beautiful prompt when output files already exist
- 🎨 **Color-coded Interface** - Easy-to-read status indicators and navigation hints
- ⚡ **Optimized Settings** - Pre-configured ffmpeg settings for best quality/size ratio
- 🖥️ **Hardware Acceleration, Everywhere** - One `--hw` flag taps your GPU's video engine on macOS, Linux, and Windows — VideoToolbox, NVENC, QuickSync, or AMF — and quietly falls back to software when there's nothing to accelerate
- 🧵 **Tuned to Your Machine** - Batch parallelism and per-job thread budgets scale to your CPU automatically, so encodes run fast without thrashing
- 🚫 **Cancel Anytime** - Press `c` or `Ctrl+C` to stop rendering mid-process

## 🖼️ Preview

```
🎬 Video Compressor

  ● MathPro-D Module 3_el.mp4
    ██████████████████████░░░░░░░░░░░░░░░░░░ 54%
    In:  1920x1080 | MP4 (h264) | 1.4 Mbps

Processing... (c or ctrl+c to cancel)
```

## 🚀 Quick Start

### Installation

```bash
# Clone the repository
git clone <your-repo-url>
cd compress-tui

# Build the binary (dependencies download automatically)
go build -o fideogo main.go

# Move to your PATH (optional)
mv fideogo ~/.local/bin/fideogo
# or
sudo mv fideogo /usr/local/bin/fideogo
```

### Usage

```bash
# Compress videos in current directory
fideogo

# Compress videos in a specific directory
fideogo /path/to/videos

# Compress a specific video file
fideogo video.mp4

# Use wildcards to select multiple files (always quote the pattern)
fideogo '*.mp4'
fideogo '/path/to/videos/*.mov'

# Convert to a specific format
fideogo --format mkv video.mp4
fideogo /path/to/videos --format mov

# Compress to a specific size
fideogo --size sm video.mp4
fideogo --size large /path/to/videos

# Let the GPU do the heavy lifting (auto-detects the right encoder)
fideogo --hw video.mov
fideogo --hw --size lg /path/to/videos
```

### Wildcard Patterns

Pass a glob pattern instead of a single path to select files across one or more
folders. The following metacharacters are supported:

| Pattern | Matches |
|---------|---------|
| `*` | Any sequence of characters within a single path segment (does **not** cross `/`) |
| `?` | Any single character |
| `[abc]` / `[a-z]` | One character from the set or range |

> **Always quote your pattern** (`'...'`) so your shell passes it to fideogo
> literally instead of expanding it first. fideogo accepts a single path/pattern
> argument — an unquoted glob that your shell expands into several filenames will
> be rejected with `only one path allowed`.

```bash
# Every .mov in the current directory
fideogo '*.mov'

# Files like clip1.mp4, clip2.mp4, clipX.mp4 (single-character wildcard)
fideogo 'clip?.mp4'

# Files starting with a, b, or c
fideogo '[abc]*.mp4'
```

#### Finding the same file across subfolders

Glob matching is **not recursive** (there is no `**`), so you match one directory
level per `*/`. To compress a file that has the same name inside every subfolder
of a parent directory, put a `*` where the subfolder name goes:

```bash
# 'intro.mp4' inside every immediate subfolder of ./courses
#   courses/python/intro.mp4
#   courses/golang/intro.mp4
#   courses/rust/intro.mp4
fideogo 'courses/*/intro.mp4'

# One level deeper (e.g. courses/python/week1/intro.mp4)
fideogo 'courses/*/*/intro.mp4'
```

You can also point a wildcard at the subfolders themselves — each matched
directory is scanned (non-recursively) for supported videos:

```bash
# Compress every video sitting directly inside each subfolder of ./courses
fideogo 'courses/*'
```

### Output Size

Control the output resolution height with `--size`:

| Size | Aliases | Resolution |
|------|---------|------------|
| Small | `sm`, `small` | 540p |
| Medium | `md`, `medium` | 1080p (default) |
| Large | `lg`, `large` | 2160p |

```bash
fideogo --size sm video.mp4
# Compresses to 540p height

fideogo --size large --format mkv video.mp4
# Compresses to 2160p height as MKV
```

The width is automatically calculated to preserve the original aspect ratio.

### Output Format

By default, output files keep the same format as the input. Use `--format` to convert to a different container:

| Format | Description |
|--------|-------------|
| `mp4`  | MPEG-4 Part 14 — most universal, great for sharing and web |
| `mov`  | QuickTime — ideal for Apple ecosystem and editing workflows |
| `mkv`  | Matroska — flexible container, supports virtually any codec |

```bash
fideogo --format mp4 video.mov
# Produces: out_video.mp4
```

The flag works in any position and can be combined with directories, file paths, or wildcards.

## ⌨️ Controls

| Key | Action |
|-----|--------|
| `↑` / `↓` or `k` / `j` | Navigate through video list |
| `Space` | Toggle selection of current video |
| `a` | Select all videos and start processing |
| `Enter` | Start processing selected videos |
| `c` or `Ctrl+C` | Cancel current processing |
| `q` | Quit (when not processing) |

## 🎨 Built With

This project is built with the amazing [Charm](https://charm.sh/) ecosystem:

### Core Libraries

- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** `v1.3.10` - The Elm-inspired TUI framework that powers the entire application architecture
- **[Bubbles](https://github.com/charmbracelet/bubbles)** `v0.21.0` - Reusable TUI components (progress bars, spinners, etc.)
- **[Lip Gloss](https://github.com/charmbracelet/lipgloss)** `v1.1.0` - Style definitions and terminal styling for beautiful text rendering

### Supporting Libraries

- **[Harmonica](https://github.com/charmbracelet/harmonica)** `v0.2.0` - Spring-based animations for smooth transitions
- **[Color Profile](https://github.com/charmbracelet/colorprofile)** `v0.2.3` - Automatic terminal color profile detection
- **[Charm X - ANSI](https://github.com/charmbracelet/x/tree/main/ansi)** `v0.10.1` - ANSI escape code utilities
- **[Charm X - Cell Buffer](https://github.com/charmbracelet/x/tree/main/cellbuf)** `v0.0.13` - Terminal cell buffer management
- **[Charm X - Term](https://github.com/charmbracelet/x/tree/main/term)** `v0.2.1` - Terminal utilities and helpers

### Other Dependencies

- **[go-colorful](https://github.com/lucasb-eyer/go-colorful)** `v1.2.0` - Color manipulation and smooth gradient interpolation for the progress indicator

## 🎛️ Compression Settings

Fideogo uses optimized ffmpeg settings for the best balance between quality and file size:

| Setting | Value | Description |
|---------|-------|-------------|
| **Codec** | H.264 (libx264) | Universal compatibility |
| **Preset** | medium | Balanced compression speed and efficiency |
| **CRF** | 28 | Quality level (lower = better quality) |
| **Resolution** | 1080p | Scaled height (`--size` flag: 540p / 1080p / 2160p) |
| **Audio Codec** | AAC | High compatibility |
| **Audio Bitrate** | 96k | Optimized for voice/music |
| **Output** | `out_{filename}` | Prefixed in same directory |

## ⚡ Hardware Acceleration & Cross-Platform Performance

Fideogo runs the same everywhere — and gets faster the better your hardware is.
Add `--hw` and let your GPU's dedicated media engine carry the encode, typically
several times quicker than software x264:

```bash
fideogo --hw video.mov
```

### 🎯 The right encoder, picked for you

No flags to memorize, no per-GPU setup. Fideogo detects the best available
encoder for your platform and hardware:

| Platform | Encoders tried (in order) | Backend |
|----------|---------------------------|---------|
| 🍎 macOS (Intel & Apple Silicon) | `h264_videotoolbox` | Apple VideoToolbox |
| 🐧 Linux / 🪟 Windows | `h264_nvenc` → `h264_qsv` → `h264_amf` | NVIDIA NVENC · Intel QuickSync · AMD AMF |

And it's careful about it. Fideogo only offers an encoder that your `ffmpeg`
build actually ships, prefers the one whose GPU is physically present (on Linux
it even inspects `/dev/nvidia*` and DRM render nodes by vendor), and runs a quick
probe-encode to confirm it really initializes. If nothing pans out, it **falls
back to software automatically** — a wrong guess can never make your batch fail.

### 🚀 Smart hardware decoding

Heavy inputs (1440p and up, or AV1 / HEVC / VP9) get GPU-accelerated decoding
too — VideoToolbox on macOS, auto-selected elsewhere. Lighter files stay on
software decode to skip the setup overhead, and the whole path is best-effort, so
a stream the GPU can't handle simply rolls back to software.

### 🧵 Concurrency that fits your CPU

Batch parallelism scales to your core count (~1 job per 4 cores, kept sensible at
2–4), and every ffmpeg job gets its own thread budget so simultaneous encodes
don't fight for the CPU. Hardware runs cap at 2 jobs at once — GPU media engines
are a small, fixed resource, and piling on only adds overhead.

### 🌍 Built to run anywhere

- Reads both modern (`out_time_us`) and older (`out_time_ms`) ffmpeg progress
  output, so the progress bar stays accurate even on the ffmpeg builds that
  older Linux distributions ship.
- Output-name collision handling is case-insensitive, matching how macOS
  (APFS/HFS+) and Windows actually treat filenames.

## 📋 Requirements

- **Go** 1.24.0 or higher
- **ffmpeg** installed and available in PATH
- **ffprobe** (usually comes with ffmpeg)

### Installing ffmpeg

```bash
# macOS
brew install ffmpeg

# Ubuntu/Debian
sudo apt install ffmpeg

# Fedora
sudo dnf install ffmpeg

# Windows (with Chocolatey)
choco install ffmpeg
```

## 🔧 Development

### Project Structure

```
compress-tui/
├── main.go          # Main application code
├── go.mod           # Go module dependencies
├── go.sum           # Dependency checksums
├── README.md        # This file
└── CLAUDE.md        # Development instructions
```

### Building from Source

```bash
# Build the binary (dependencies are automatically downloaded)
go build -o fideogo main.go

# Or manually download dependencies first (optional)
go mod download
go build -o fideogo main.go

# Run directly without installing
go run main.go

# Build and install in one command
go build -o fideogo main.go && mv fideogo ~/.local/bin/fideogo
```

> **Note:** Go automatically downloads and caches dependencies during the build process. You don't need to run `go mod download` explicitly unless you want to pre-fetch dependencies.

## 🎯 Supported Video Formats

- `.mp4` - MPEG-4 Part 14
- `.mov` - QuickTime File Format
- `.avi` - Audio Video Interleave
- `.mkv` - Matroska Video
- `.m4v` - MPEG-4 Video

## 💡 Tips

- Output files are automatically prefixed with `out_` to avoid overwriting originals
- If an output file already exists, you'll be prompted with options to overwrite, skip, or cancel
- The progress bar uses a color gradient that transitions from cyan (start) to yellow (complete)
- Processing can be cancelled at any time - partial output files are automatically cleaned up
- Select multiple files with `Space` and process them in batch with `Enter`

## 📝 License

This project is open source and available under your chosen license.

## 🙏 Acknowledgments

Special thanks to the [Charm](https://charm.sh/) team for creating such beautiful and powerful TUI tools!

---

<div align="center">

Made with 💖 and [Charm](https://charm.sh/)

</div>
