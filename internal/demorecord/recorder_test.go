package demorecord

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func TestRecorderRoundTripUsesIndependentCompressedChunks(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "session.lcrdemo")
	recorder, err := NewRecorder(path, RecorderOptions{
		Now:           clock.Now,
		ChunkDuration: time.Second,
		CaptureBuffer: 16,
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	recorder.Capture(80, 24, "first\nsecond\nthird")
	clock.Advance(400 * time.Millisecond)
	recorder.Capture(80, 24, "first\nchanged\nthird")
	clock.Advance(200 * time.Millisecond)
	recorder.Capture(80, 24, "first\nchanged\nthird") // exact duplicate
	clock.Advance(900 * time.Millisecond)
	recorder.Capture(100, 30, "shorter\nview")
	clock.Advance(500 * time.Millisecond)
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	manifest := reader.Manifest()
	if got, want := len(manifest.Chunks), 2; got != want {
		t.Fatalf("chunks = %d, want %d: %#v", got, want, manifest.Chunks)
	}
	if got, want := manifest.FrameCount, int64(3); got != want {
		t.Fatalf("frame count = %d, want %d", got, want)
	}
	if got, want := manifest.DurationMS, int64(2000); got != want {
		t.Fatalf("duration = %dms, want %dms", got, want)
	}
	if manifest.CompletedAt == nil {
		t.Fatal("completed_at is nil")
	}

	firstChunk, err := reader.LoadChunk(0)
	if err != nil {
		t.Fatalf("LoadChunk(0): %v", err)
	}
	if got, want := len(firstChunk), 2; got != want {
		t.Fatalf("first chunk frames = %d, want %d", got, want)
	}
	if got, want := firstChunk[1].View, "first\nchanged\nthird"; got != want {
		t.Fatalf("delta view = %q, want %q", got, want)
	}

	secondChunk, err := reader.LoadChunk(1)
	if err != nil {
		t.Fatalf("LoadChunk(1): %v", err)
	}
	if got, want := secondChunk[0].View, "shorter\nview"; got != want {
		t.Fatalf("truncated view = %q, want %q", got, want)
	}
	if secondChunk[0].Width != 100 || secondChunk[0].Height != 30 {
		t.Fatalf("second chunk dimensions = %dx%d", secondChunk[0].Width, secondChunk[0].Height)
	}

	frame, err := reader.FrameAt(1200)
	if err != nil {
		t.Fatalf("FrameAt: %v", err)
	}
	if got, want := frame.View, "first\nchanged\nthird"; got != want {
		t.Fatalf("frame at gap = %q, want %q", got, want)
	}
}

func TestRecorderRefusesToOverwriteExistingPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "existing.lcrdemo")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := NewRecorder(path, RecorderOptions{})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("NewRecorder error = %v, want existing-path refusal", err)
	}
}

func TestRecorderCompressesLineLevelAnimation(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.July, 19, 11, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "animation.lcrdemo")
	recorder, err := NewRecorder(path, RecorderOptions{
		Now:           clock.Now,
		ChunkDuration: time.Minute,
		CaptureBuffer: 700,
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	staticRows := strings.Repeat("A stable dashboard row with ANSI-free text\n", 22)
	rawBytes := 0
	for i := 0; i < 600; i++ {
		view := fmt.Sprintf("Little Control Room\n%sanimation frame %04d", staticRows, i)
		rawBytes += len(view)
		recorder.Capture(100, 24, view)
		clock.Advance(200 * time.Millisecond)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	compressedBytes := int64(0)
	for _, chunk := range reader.Manifest().Chunks {
		compressedBytes += chunk.Bytes
	}
	if compressedBytes >= int64(rawBytes/4) {
		t.Fatalf(
			"compressed recording = %d bytes, raw full frames = %d bytes; want at least 4:1 reduction",
			compressedBytes,
			rawBytes,
		)
	}
}

func TestRecorderStoresCoarseInteractionTimesWithoutKeyValues(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.July, 19, 11, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "interactions.lcrdemo")
	recorder, err := NewRecorder(path, RecorderOptions{Now: clock.Now})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	recorder.Capture(80, 24, "frame")
	recorder.MarkInteraction()
	clock.Advance(time.Second)
	recorder.MarkInteraction()
	clock.Advance(time.Second)
	recorder.MarkInteraction()
	clock.Advance(time.Second)
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := reader.Manifest().InteractionMS
	if len(got) != 2 || got[0] != 0 || got[1] != 2000 {
		t.Fatalf("interaction markers = %#v, want [0 2000]", got)
	}
	raw, err := os.ReadFile(filepath.Join(path, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "key") {
		t.Fatalf("manifest unexpectedly contains key data:\n%s", raw)
	}
}

func TestSaveEditsValidatesAndRoundTrips(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "edits.lcrdemo")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	project := EditProject{
		Version: FormatVersion,
		Clips: []Clip{{
			ID:              "clip-1",
			Name:            "Overview",
			InMS:            1000,
			OutMS:           5000,
			IdleTimeLimitMS: 1500,
			SmartTiming:     true,
		}},
	}
	if err := SaveEdits(path, project, 10_000); err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	loaded, err := LoadEdits(path)
	if err != nil {
		t.Fatalf("LoadEdits: %v", err)
	}
	if got, want := loaded.Clips[0], project.Clips[0]; got != want {
		t.Fatalf("loaded clip = %#v, want %#v", got, want)
	}

	project.Clips[0].OutMS = 11_000
	if err := SaveEdits(path, project, 10_000); err == nil {
		t.Fatal("SaveEdits accepted a clip beyond the recording")
	}
}

func TestExportAsciicastStartsWithSelectedScreenState(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "source.lcrdemo")
	recorder, err := NewRecorder(path, RecorderOptions{Now: clock.Now, ChunkDuration: time.Minute})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	recorder.Capture(80, 24, "zero")
	clock.Advance(time.Second)
	recorder.Capture(80, 24, "one")
	clock.Advance(time.Second)
	recorder.Capture(100, 30, "two")
	clock.Advance(time.Second)
	recorder.Capture(100, 30, "three")
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	output := filepath.Join(t.TempDir(), "clip.cast")
	clip := Clip{
		ID:              "clip-1",
		Name:            "Selected",
		InMS:            1500,
		OutMS:           3000,
		IdleTimeLimitMS: 1000,
	}
	if err := ExportAsciicast(reader, clip, output); err != nil {
		t.Fatalf("ExportAsciicast: %v", err)
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("missing asciicast header")
	}
	var header asciicastHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if header.Version != 3 || header.Term.Cols != 80 || header.Term.Rows != 24 {
		t.Fatalf("header = %#v", header)
	}
	if header.IdleTimeLimit != 1 {
		t.Fatalf("idle limit = %v, want 1", header.IdleTimeLimit)
	}

	if !scanner.Scan() {
		t.Fatal("missing initial output event")
	}
	var initial []json.RawMessage
	if err := json.Unmarshal(scanner.Bytes(), &initial); err != nil {
		t.Fatalf("parse initial event: %v", err)
	}
	var code, data string
	if err := json.Unmarshal(initial[1], &code); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(initial[2], &data); err != nil {
		t.Fatal(err)
	}
	if code != "o" || !strings.Contains(data, "one") {
		t.Fatalf("initial event code=%q data=%q, want screen state at in-point", code, data)
	}

	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"r","100x30"`) {
		t.Fatalf("asciicast did not preserve resize event:\n%s", raw)
	}

	noIdleOutput := filepath.Join(t.TempDir(), "clip-no-idle-limit.cast")
	clip.IdleTimeLimitMS = -1
	if err := ExportAsciicast(reader, clip, noIdleOutput); err != nil {
		t.Fatalf("ExportAsciicast without idle limit: %v", err)
	}
	noIdleFile, err := os.Open(noIdleOutput)
	if err != nil {
		t.Fatal(err)
	}
	defer noIdleFile.Close()
	noIdleScanner := bufio.NewScanner(noIdleFile)
	if !noIdleScanner.Scan() {
		t.Fatal("missing no-idle asciicast header")
	}
	var noIdleHeader map[string]any
	if err := json.Unmarshal(noIdleScanner.Bytes(), &noIdleHeader); err != nil {
		t.Fatal(err)
	}
	if _, exists := noIdleHeader["idle_time_limit"]; exists {
		t.Fatalf("idle_time_limit present when compression is off: %#v", noIdleHeader)
	}
}
