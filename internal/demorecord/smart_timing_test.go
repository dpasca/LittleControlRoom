package demorecord

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSmartTimingCompressesLongPauseInsideTextEntry(t *testing.T) {
	t.Parallel()

	state := SmartTimingState{}
	current := smartTestFrame(0, "│ > hello                         │")
	next := smartTestFrame(20_000, "│ > hello w                       │")
	delay := state.SmartDelay(current, next, 20*time.Second, 2*time.Second)
	if delay < 40*time.Millisecond || delay > 150*time.Millisecond {
		t.Fatalf("text-entry delay = %s, want a short steady cadence", delay)
	}
}

func TestSmartTimingHoldsFinishedTextBeforeLargeScreenChange(t *testing.T) {
	t.Parallel()

	state := SmartTimingState{}
	before := smartTestFrame(0, "│ > hello                         │")
	finished := smartTestFrame(100, "│ > hello world                   │")
	state.ObserveInteraction(finished.AtMS)
	_ = state.SmartDelay(before, finished, 100*time.Millisecond, 2*time.Second)

	large := smartTestFrame(110, strings.Join([]string{
		"dashboard row one",
		"dashboard row two",
		"dashboard row three",
		"dashboard row four",
		"dashboard row five",
		"dashboard row six",
		"dashboard row seven",
		"dashboard row eight",
	}, "\n"))
	delay := state.SmartDelay(finished, large, 10*time.Millisecond, 2*time.Second)
	if delay != smartFinalTextHold {
		t.Fatalf("post-input screen-transition delay = %s, want %s", delay, smartFinalTextHold)
	}
}

func TestSmartTimingAcceleratesQuietCounters(t *testing.T) {
	t.Parallel()

	state := SmartTimingState{}
	current := smartTestFrame(0, "Working 00:01")
	next := smartTestFrame(500, "Working 00:02")
	delay := state.SmartDelay(current, next, 500*time.Millisecond, 2*time.Second)
	if delay != 62_500*time.Microsecond {
		t.Fatalf("quiet counter delay = %s, want 62.5ms", delay)
	}
}

func TestSmartTimingExpiresOldTextBeforeLaterTransition(t *testing.T) {
	t.Parallel()

	state := SmartTimingState{}
	before := smartTestFrame(0, "│ > hello                         │")
	finished := smartTestFrame(100, "│ > hello world                   │")
	state.ObserveInteraction(finished.AtMS)
	_ = state.SmartDelay(before, finished, 100*time.Millisecond, 2*time.Second)
	large := smartTestFrame(61_100, strings.Repeat("changed row\n", 12))
	delay := state.SmartDelay(finished, large, 61*time.Second, 2*time.Second)
	if delay != smartLargeChangeDelayCap {
		t.Fatalf("expired post-input transition delay = %s, want %s", delay, smartLargeChangeDelayCap)
	}
}

func TestExportAsciicastSmartTimingWritesExplicitEditedDelays(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)}
	recordingPath := filepath.Join(t.TempDir(), "smart-source.lcrdemo")
	recorder, err := NewRecorder(recordingPath, RecorderOptions{Now: clock.Now, ChunkDuration: time.Minute})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	recorder.Capture(80, 24, "│ >                              │")
	clock.Advance(20 * time.Second)
	recorder.MarkInteraction()
	recorder.Capture(80, 24, "│ > h                            │")
	clock.Advance(20 * time.Second)
	recorder.MarkInteraction()
	recorder.Capture(80, 24, "│ > hi                           │")
	clock.Advance(10 * time.Millisecond)
	recorder.MarkInteraction()
	recorder.Capture(80, 24, strings.Repeat("new dashboard row\n", 10))
	clock.Advance(990 * time.Millisecond)
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(recordingPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "smart.cast")
	clip := Clip{
		ID:              "smart",
		Name:            "Smart",
		InMS:            0,
		OutMS:           reader.Manifest().DurationMS,
		IdleTimeLimitMS: 2000,
		SmartTiming:     true,
	}
	if err := ExportAsciicast(reader, clip, outputPath); err != nil {
		t.Fatalf("ExportAsciicast: %v", err)
	}

	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("missing asciicast header")
	}
	var header map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		t.Fatal(err)
	}
	if _, exists := header["idle_time_limit"]; exists {
		t.Fatalf("smart export should use explicit delays, got header %#v", header)
	}

	var intervals []float64
	for scanner.Scan() {
		var event []json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		var interval float64
		if err := json.Unmarshal(event[0], &interval); err != nil {
			t.Fatal(err)
		}
		intervals = append(intervals, interval)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 0.045, 0.045, 0.7, 0.35}
	if len(intervals) != len(want) {
		t.Fatalf("smart event intervals = %#v, want %#v", intervals, want)
	}
	for index := range want {
		if difference := intervals[index] - want[index]; difference < -0.000_001 || difference > 0.000_001 {
			t.Fatalf("smart event interval %d = %.6f, want %.6f", index, intervals[index], want[index])
		}
	}
}

func smartTestFrame(atMS int64, view string) Frame {
	return Frame{AtMS: atMS, Width: 80, Height: 24, View: view}
}
