package fideogo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// videoMetadata holds the ffprobe-derived fields we display and use for progress.
type videoMetadata struct {
	codec    string
	width    string
	height   string
	bitrate  string // raw bps as reported by ffprobe; may be "N/A" or empty
	duration float64
}

// probeMetadata fetches codec, dimensions, bitrate, and duration in a single
// ffprobe invocation — spawn cost dominates ffprobe latency, so one call beats
// three parallel ones.
func (vs *videoService) probeMetadata(path string) (videoMetadata, error) {
	if path == "" {
		return videoMetadata{}, fmt.Errorf("path cannot be empty")
	}
	// Ask for duration on both the stream and the format: Matroska/WebM often
	// leave format=duration as "N/A", in which case the per-stream value is the
	// only one available. We take the first that parses (see the loop below).
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,duration:format=bit_rate,duration",
		"-of", "default=noprint_wrappers=1",
		path,
	}
	out, err := exec.Command("ffprobe", args...).Output()
	if err != nil {
		return videoMetadata{}, fmt.Errorf("ffprobe failed: %w", err)
	}

	var meta videoMetadata
	for _, line := range strings.Split(string(out), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "codec_name":
			meta.codec = val
		case "width":
			meta.width = val
		case "height":
			meta.height = val
		case "bit_rate":
			meta.bitrate = val
		case "duration":
			// Both the stream and format sections emit a duration line; keep the
			// first that parses to a positive value so a valid stream duration
			// isn't clobbered by a later "N/A" from the format section.
			if meta.duration == 0 {
				if d, err := strconv.ParseFloat(val, 64); err == nil && d > 0 {
					meta.duration = d
				}
			}
		}
	}
	return meta, nil
}

// formatVideoInfo renders the user-facing one-line summary from probed metadata.
func (vs *videoService) formatVideoInfo(path string, meta videoMetadata) string {
	bitrate := meta.bitrate
	if br, err := strconv.ParseFloat(bitrate, 64); err == nil {
		bitrate = fmt.Sprintf("%.1f Mbps", br/1000000)
	}
	format := strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))
	size := "unknown size"
	if fi, err := os.Stat(path); err == nil {
		size = formatFileSize(fi.Size())
	}
	return fmt.Sprintf("%sx%s | %s (%s) | %s | %s", meta.width, meta.height, format, meta.codec, bitrate, size)
}

func (vs *videoService) getVideoInfo(path string) string {
	meta, err := vs.probeMetadata(path)
	if err != nil || meta.width == "" || meta.height == "" {
		return "unable to read video info"
	}
	return vs.formatVideoInfo(path, meta)
}

func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
