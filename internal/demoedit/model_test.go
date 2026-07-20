package demoedit

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/demorecord"

	tea "github.com/charmbracelet/bubbletea"
)

type editorClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *editorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *editorClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func createEditorRecording(t *testing.T) *demorecord.Reader {
	t.Helper()
	clock := &editorClock{now: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "editor.lcrdemo")
	recorder, err := demorecord.NewRecorder(path, demorecord.RecorderOptions{
		Now:           clock.Now,
		ChunkDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	recorder.Capture(80, 24, "frame zero\nrow")
	clock.Advance(time.Second)
	recorder.Capture(80, 24, "frame one\nrow")
	clock.Advance(time.Second)
	recorder.Capture(80, 24, "frame two\nrow")
	clock.Advance(time.Second)
	recorder.Capture(80, 24, "frame three\nrow")
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reader, err := demorecord.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return reader
}

func loadInitialEditorModel(t *testing.T) Model {
	t.Helper()
	reader := createEditorRecording(t)
	model, err := New(reader, demorecord.DefaultEditProject())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	model = next.(Model)
	msg := model.Init()()
	next, _ = model.Update(msg)
	return next.(Model)
}

func TestEditorMarksAndPersistsAClipWithoutBlockingUpdate(t *testing.T) {
	t.Parallel()

	model := loadInitialEditorModel(t)
	if model.loading {
		t.Fatal("editor remained loading")
	}
	if !model.smartTiming {
		t.Fatal("new editor selection did not default to smart timing")
	}

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = next.(Model)
	if cmd != nil {
		t.Fatal("same-chunk seek unexpectedly scheduled disk work")
	}
	if got, want := model.positionMS, int64(1000); got != want {
		t.Fatalf("position = %d, want %d", got, want)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = next.(Model)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = next.(Model)
	if model.inMS != 1000 || model.outMS != 2000 {
		t.Fatalf("selection = %d..%d, want 1000..2000", model.inMS, model.outMS)
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = next.(Model)
	if !model.busy || cmd == nil {
		t.Fatalf("save state busy=%v cmd=%v", model.busy, cmd)
	}
	next, _ = model.Update(cmd())
	model = next.(Model)
	if model.busy || len(model.edits.Clips) != 1 {
		t.Fatalf("saved state busy=%v clips=%#v", model.busy, model.edits.Clips)
	}
	if got := model.edits.Clips[0]; got.InMS != 1000 || got.OutMS != 2000 || !got.SmartTiming {
		t.Fatalf("saved clip = %#v", got)
	}

	loaded, err := demorecord.LoadEdits(model.reader.Path())
	if err != nil {
		t.Fatalf("LoadEdits: %v", err)
	}
	if got, want := len(loaded.Clips), 1; got != want {
		t.Fatalf("persisted clips = %d, want %d", got, want)
	}
	if !loaded.Clips[0].SmartTiming {
		t.Fatalf("persisted clip lost smart timing: %#v", loaded.Clips[0])
	}
}

func TestEditorCanToggleSmartTimingBeforeSaving(t *testing.T) {
	t.Parallel()

	model := loadInitialEditorModel(t)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = next.(Model)
	if model.smartTiming {
		t.Fatal("smart timing remained enabled after toggle")
	}
	if !strings.Contains(model.status, "off") {
		t.Fatalf("toggle status = %q", model.status)
	}

	clip := model.currentSelectionClip()
	if clip.SmartTiming {
		t.Fatalf("selection clip retained smart timing: %#v", clip)
	}
}

func TestEditorNewSelectionRestoresSmartTimingDefault(t *testing.T) {
	t.Parallel()

	model := loadInitialEditorModel(t)
	model.smartTiming = false
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = next.(Model)
	if !model.smartTiming {
		t.Fatal("new selection inherited disabled smart timing")
	}
}

func TestEditorViewCombinesRecordedFrameAndTimeline(t *testing.T) {
	t.Parallel()

	model := loadInitialEditorModel(t)
	view := model.View()
	for _, want := range []string{"frame zero", "LCR DEMO", "IN ", "OUT ", "space play"} {
		if !strings.Contains(view, want) {
			t.Fatalf("editor view missing %q:\n%s", want, view)
		}
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	full := next.(Model).View()
	if !strings.Contains(full, "frame zero") {
		t.Fatalf("full-frame view missing recording:\n%s", full)
	}
	if strings.Contains(full, "LCR DEMO") {
		t.Fatalf("full-frame view retained editor chrome:\n%s", full)
	}
}

func TestFrameIndexAtUsesLastFrameAtOrBeforePosition(t *testing.T) {
	t.Parallel()

	frames := []demorecord.Frame{{AtMS: 0}, {AtMS: 1000}, {AtMS: 2500}}
	cases := []struct {
		at   int64
		want int
	}{
		{at: 0, want: 0},
		{at: 999, want: 0},
		{at: 1000, want: 1},
		{at: 2000, want: 1},
		{at: 9999, want: 2},
	}
	for _, test := range cases {
		if got := frameIndexAt(frames, test.at); got != test.want {
			t.Fatalf("frameIndexAt(%d) = %d, want %d", test.at, got, test.want)
		}
	}
}

func TestEditorJumpsBetweenInteractionMarkers(t *testing.T) {
	t.Parallel()

	model := loadInitialEditorModel(t)
	model.manifest.InteractionMS = []int64{500, 1500, 2500}
	model.positionMS = 1000

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = next.(Model)
	if cmd != nil {
		t.Fatal("same-chunk interaction jump unexpectedly scheduled disk work")
	}
	if got, want := model.positionMS, int64(1500); got != want {
		t.Fatalf("next interaction position = %d, want %d", got, want)
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = next.(Model)
	if cmd != nil {
		t.Fatal("same-chunk interaction jump unexpectedly scheduled disk work")
	}
	if got, want := model.positionMS, int64(500); got != want {
		t.Fatalf("previous interaction position = %d, want %d", got, want)
	}
}

func TestPlayerStartsAtClipInPointWithCleanFullFrame(t *testing.T) {
	t.Parallel()

	reader := createEditorRecording(t)
	clip := demorecord.Clip{
		ID:              "clip-1",
		Name:            "Clip 1",
		InMS:            1000,
		OutMS:           3000,
		IdleTimeLimitMS: 500,
		SmartTiming:     true,
	}
	model, err := NewPlayer(reader, clip)
	if err != nil {
		t.Fatalf("NewPlayer: %v", err)
	}
	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = next.(Model)
	next, tick := model.Update(model.Init()())
	model = next.(Model)
	if !model.playing || tick == nil {
		t.Fatalf("player state playing=%v tick=%v", model.playing, tick)
	}
	if !model.smartTiming {
		t.Fatal("player did not restore the clip's smart timing")
	}
	if got, want := model.positionMS, int64(1000); got != want {
		t.Fatalf("player position = %d, want %d", got, want)
	}
	view := model.View()
	if !strings.Contains(view, "frame one") || strings.Contains(view, "LCR DEMO") {
		t.Fatalf("player did not render a clean clip frame:\n%s", view)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = next.(Model)
	if got, want := model.positionMS, int64(1000); got != want {
		t.Fatalf("player sought before clip: %d, want %d", got, want)
	}
}
