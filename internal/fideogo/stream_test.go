package fideogo

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// countingReader records how many bytes were consumed from the wrapped reader.
type countingReader struct {
	io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += n
	return n, err
}

// TestThreadBudgetScalesWithBatch guards the thread-budget fix: the -threads
// cap is derived from how many encodes actually run side by side, so a
// single-file batch gets every core instead of NumCPU/maxConcurrent.
func TestThreadBudgetScalesWithBatch(t *testing.T) {
	cfg := testConfig() // maxConcurrent 2
	vs := newVideoService(cfg)
	meta := videoMetadata{codec: "h264", height: "1080"}
	threadsOf := func(jobs int) string {
		t.Helper()
		args := vs.ffmpegArgs("in.mov", "out.mp4", meta, jobs)
		i := indexOf(args, "-threads")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("no -threads in %v", args)
		}
		return args[i+1]
	}

	if got, want := threadsOf(1), strconv.Itoa(runtime.NumCPU()); got != want {
		t.Errorf("single-file batch: -threads %s, want every core (%s)", got, want)
	}
	if got, want := threadsOf(2), strconv.Itoa(autoThreadsPerJob(2)); got != want {
		t.Errorf("batch at the cap: -threads %s, want %s", got, want)
	}
	if got, want := threadsOf(50), strconv.Itoa(autoThreadsPerJob(2)); got != want {
		t.Errorf("batch above the cap must still be budgeted for maxConcurrent jobs: -threads %s, want %s", got, want)
	}
	if got, want := threadsOf(0), threadsOf(1); got != want {
		t.Errorf("zero jobs should be treated as one: -threads %s, want %s", got, want)
	}
}

func TestConcurrentJobs(t *testing.T) {
	tests := []struct{ maxConcurrent, queued, want int }{
		{4, 1, 1}, {4, 4, 4}, {4, 9, 4}, {4, 0, 1}, {2, -3, 1}, {1, 5, 1},
	}
	for _, tt := range tests {
		if got := concurrentJobs(tt.maxConcurrent, tt.queued); got != tt.want {
			t.Errorf("concurrentJobs(%d, %d) = %d, want %d", tt.maxConcurrent, tt.queued, got, tt.want)
		}
	}
}

// TestStreamProgressCoalescesAndDrains covers two properties of the progress
// reader: it forwards at most one message per whole percent, and it keeps
// reading past an over-long line — a reader that gave up there (as
// bufio.Scanner does at its token limit) would leave ffmpeg blocked on a full
// pipe and the encode hung.
func TestStreamProgressCoalescesAndDrains(t *testing.T) {
	var in strings.Builder
	for _, us := range []int64{1_000_000, 1_040_000, 2_000_000} {
		fmt.Fprintf(&in, "frame=1\nout_time_us=%d\nprogress=continue\n", us)
	}
	in.WriteString(strings.Repeat("x", 200<<10) + "\n") // 200 KiB junk line
	in.WriteString("out_time_us=3000000\nprogress=end\n")
	r := &countingReader{Reader: strings.NewReader(in.String())}

	var got []int
	streamProgress(r, 7, 10, func(msg tea.Msg) {
		pm := msg.(progressMsg)
		if pm.idx != 7 {
			t.Errorf("progressMsg for idx %d, want 7", pm.idx)
		}
		got = append(got, int(pm.progress*100+0.5))
	})

	if r.n != in.Len() {
		t.Errorf("streamProgress stopped after %d of %d bytes; the pipe would block ffmpeg", r.n, in.Len())
	}
	want := []int{10, 20, 30}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("forwarded percents = %v, want %v (one per whole percent, none lost after the long line)", got, want)
	}
}

// TestStreamProgressNoDurationDrains: without a duration no percent can be
// computed, but stdout must still be read to EOF.
func TestStreamProgressNoDurationDrains(t *testing.T) {
	in := "out_time_us=1000000\n" + strings.Repeat("y", 100<<10)
	r := &countingReader{Reader: strings.NewReader(in)}
	sent := 0
	streamProgress(r, 0, 0, func(tea.Msg) { sent++ })
	if r.n != len(in) {
		t.Errorf("drained %d of %d bytes", r.n, len(in))
	}
	if sent != 0 {
		t.Errorf("sent %d messages without a duration, want 0", sent)
	}
}

// TestDrainStderrKeepsTailPastLongLines guards the scanner-stall fix on the
// stderr side: a line beyond bufio.Scanner's 64 KiB limit must not stop the
// drain, and the decisive last line must survive in the retained tail.
func TestDrainStderrKeepsTailPastLongLines(t *testing.T) {
	in := strings.Repeat("x", 70<<10) + "\nfinal error line\n"
	r := &countingReader{Reader: strings.NewReader(in)}
	got := drainStderr(r)
	if r.n != len(in) {
		t.Errorf("drainStderr consumed %d of %d bytes; the pipe would block ffmpeg", r.n, len(in))
	}
	if len(got) > stderrTailBytes {
		t.Errorf("retained %d bytes, cap is %d", len(got), stderrTailBytes)
	}
	if lastLine(got) != "final error line" {
		t.Errorf("last line = %q, want the final stderr line", lastLine(got))
	}
}

func TestTailWriterRetainsLastBytes(t *testing.T) {
	w := &tailWriter{keep: 8}
	for _, chunk := range []string{"abc", "defgh", "ij"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(w.buf); got != "cdefghij" {
		t.Errorf("after small writes buf = %q, want %q", got, "cdefghij")
	}
	if _, err := w.Write([]byte("klmnopqrstuvwxyz")); err != nil {
		t.Fatal(err)
	}
	if got := string(w.buf); got != "stuvwxyz" {
		t.Errorf("after an oversized write buf = %q, want %q", got, "stuvwxyz")
	}
}

// TestWaitWorkers checks the bounded drain Run() performs after the UI closes.
func TestWaitWorkers(t *testing.T) {
	vs := newVideoService(testConfig())
	release := make(chan struct{})
	vs.workers.Add(1)
	go func() {
		defer vs.workers.Done()
		<-release
	}()
	if vs.waitWorkers(20 * time.Millisecond) {
		t.Fatal("waitWorkers returned true while a worker was still running")
	}
	close(release)
	if !vs.waitWorkers(5 * time.Second) {
		t.Fatal("waitWorkers timed out after the worker finished")
	}
	if !vs.waitWorkers(time.Millisecond) {
		t.Fatal("waitWorkers with no workers should return immediately")
	}
}
