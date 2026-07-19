package demorecord

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type recordingModelFixture struct {
	value     string
	viewCalls *int
}

func (m recordingModelFixture) Init() tea.Cmd {
	return nil
}

func (m recordingModelFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if value, ok := msg.(string); ok {
		m.value = value
	}
	return m, nil
}

func (m recordingModelFixture) View() string {
	*m.viewCalls++
	return m.value
}

type capturedView struct {
	width  int
	height int
	view   string
}

type captureFixture struct {
	frames []capturedView
}

func (c *captureFixture) Capture(width, height int, view string) {
	c.frames = append(c.frames, capturedView{width: width, height: height, view: view})
}

type statusCaptureFixture struct {
	captureFixture
	err     error
	dropped uint64
}

func (c *statusCaptureFixture) Err() error {
	return c.err
}

func (c *statusCaptureFixture) DroppedFrames() uint64 {
	return c.dropped
}

func TestRecordingModelCapturesChangedViewsAfterTerminalSize(t *testing.T) {
	t.Parallel()

	viewCalls := 0
	capture := &captureFixture{}
	wrapped := WrapModel(recordingModelFixture{value: "initial", viewCalls: &viewCalls}, capture)
	if wrapped == nil {
		t.Fatal("WrapModel returned nil")
	}
	if got, want := wrapped.View(), "initial"; got != want {
		t.Fatalf("initial view = %q, want %q", got, want)
	}

	next, _ := wrapped.Update("before-size")
	wrapped = next.(*RecordingModel)
	if len(capture.frames) != 0 {
		t.Fatalf("captured before terminal size: %#v", capture.frames)
	}

	next, _ = wrapped.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	wrapped = next.(*RecordingModel)
	if got, want := len(capture.frames), 1; got != want {
		t.Fatalf("captures = %d, want %d", got, want)
	}
	if got := capture.frames[0]; got.width != 100 || got.height != 30 || got.view != "before-size" {
		t.Fatalf("capture = %#v", got)
	}

	previousCalls := viewCalls
	next, _ = wrapped.Update("changed")
	wrapped = next.(*RecordingModel)
	if got, want := viewCalls, previousCalls+1; got != want {
		t.Fatalf("underlying View calls for update = %d, want %d", got-previousCalls, 1)
	}
	if got, want := wrapped.View(), "changed"; got != want {
		t.Fatalf("cached view = %q, want %q", got, want)
	}
	if got, want := len(capture.frames), 2; got != want {
		t.Fatalf("captures = %d, want %d", got, want)
	}
}

func TestRecordingModelMakesRecorderFailureVisibleWithoutCapturingTheWarning(t *testing.T) {
	t.Parallel()

	viewCalls := 0
	capture := &statusCaptureFixture{err: errors.New("disk full")}
	wrapped := WrapModel(
		recordingModelFixture{value: "original first row\nsecond row", viewCalls: &viewCalls},
		capture,
	)
	next, _ := wrapped.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	wrapped = next.(*RecordingModel)

	view := wrapped.View()
	if !strings.Contains(view, "DEMO RECORDING FAILED") || !strings.Contains(view, "disk full") {
		t.Fatalf("visible view missing recording failure:\n%s", view)
	}
	if len(capture.frames) != 1 || strings.Contains(capture.frames[0].view, "DEMO RECORDING FAILED") {
		t.Fatalf("captured frame unexpectedly contains warning: %#v", capture.frames)
	}
}
