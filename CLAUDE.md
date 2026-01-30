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
