# 🎬 Fideogo - Video Compressor TUI

<div align="center">

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
- 🚫 **Cancel Anytime** - Press `c` or `Ctrl+C` to stop rendering mid-process

## 🖼️ Preview

```
🎬 Video Compressor

  ● MathPro-D Module 3_el.mp4
    ██████████████████████░░░░░░░░░░░░░░░░░░ 54%
    In:  1920x1080 | h264 | 1.4 Mbps

Processing... (c or ctrl+c to cancel)
```

## 🚀 Quick Start

### Installation

```bash
# Clone the repository
git clone <your-repo-url>
cd compress-tui

# Build the binary
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

# Use wildcards to select multiple files
fideogo *.mp4
fideogo /path/to/videos/*.mov
```

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

- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** - The Elm-inspired TUI framework
- **[Bubbles](https://github.com/charmbracelet/bubbles)** - Reusable TUI components
- **[Lip Gloss](https://github.com/charmbracelet/lipgloss)** - Style definitions and terminal styling
- **[go-colorful](https://github.com/lucasb-eyer/go-colorful)** - Color manipulation and interpolation

## 🎛️ Compression Settings

Fideogo uses optimized ffmpeg settings for the best balance between quality and file size:

| Setting | Value | Description |
|---------|-------|-------------|
| **Codec** | H.264 (libx264) | Universal compatibility |
| **Preset** | slow | Better compression efficiency |
| **CRF** | 28 | Quality level (lower = better quality) |
| **Resolution** | 1080p | Scaled to max 1080p height |
| **Audio Codec** | AAC | High compatibility |
| **Audio Bitrate** | 96k | Optimized for voice/music |
| **Output** | `out_{filename}` | Prefixed in same directory |

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
# Install dependencies
go mod download

# Build the binary
go build -o fideogo main.go

# Run directly without installing
go run main.go

# Build and install in one command
go build -o fideogo main.go && mv fideogo ~/.local/bin/fideogo
```

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
