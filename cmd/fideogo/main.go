// Command fideogo is a terminal UI for compressing video files with ffmpeg.
// This is a thin entry point; all behavior lives in internal/fideogo so the
// package can be unit-tested with white-box access to its unexported helpers.
package main

import "fideogo/internal/fideogo"

func main() {
	fideogo.Run()
}
