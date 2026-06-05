package fideogo

import "runtime"

// compressionConfig holds configuration for video compression.
type compressionConfig struct {
	maxConcurrent int
	codec         string
	preset        string
	crf           string
	audioBitrate  string
	resolution    string
	outputFormat  string // target container format (mp4, mov, mkv, webm)
	hwAccel       bool   // use hardware encoder
	hwEncoder     string // resolved hw encoder (h264_videotoolbox/nvenc/qsv/amf)
	hwQuality     string // -q:v value for h264_videotoolbox (0-100, higher = better)
}

const outputPrefix = "out_"

// defaultOutputFormat is the container used when --format is not given. We pick
// mp4 (H.264/AAC) rather than mirroring the source container so inputs like
// WebM aren't locked into the slow software VP9 encoder, and so --hw hardware
// encoding is available by default. Override per-run with --format.
const defaultOutputFormat = "mp4"

// autoMaxConcurrent picks a sensible parallelism for software x264 encodes:
// 1 job per ~4 cores, clamped to [2, 4]. Each ffmpeg job still gets a thread
// budget via autoThreadsPerJob so 2 well-budgeted jobs beat 4 thrashing ones.
func autoMaxConcurrent() int {
	n := runtime.NumCPU() / 4
	if n < 2 {
		n = 2
	}
	if n > 4 {
		n = 4
	}
	return n
}

// autoThreadsPerJob divides available cores across concurrent ffmpeg jobs.
func autoThreadsPerJob(maxConcurrent int) int {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	t := runtime.NumCPU() / maxConcurrent
	if t < 1 {
		t = 1
	}
	return t
}

var validFormats = map[string]bool{
	"mp4":  true,
	"mov":  true,
	"mkv":  true,
	"webm": true,
}

var validSizes = map[string]string{
	"sm":     "540",
	"small":  "540",
	"md":     "1080",
	"medium": "1080",
	"lg":     "2160",
	"large":  "2160",
}

var defaultConfig = compressionConfig{
	maxConcurrent: autoMaxConcurrent(),
	codec:         "libx264",
	preset:        "medium",
	crf:           "28",
	audioBitrate:  "96k",
	resolution:    "1080",
	hwQuality:     "65",
}
