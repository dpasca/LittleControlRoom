package demorecord

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type ViewRecorder interface {
	Capture(width, height int, view string)
}

// RecordingModel decorates a Bubble Tea model with non-blocking view capture.
// It caches each updated view so Bubble Tea does not have to render the
// underlying model a second time for the same message.
type RecordingModel struct {
	inner       tea.Model
	recorder    ViewRecorder
	view        string
	captureView string
	width       int
	height      int
}

func WrapModel(inner tea.Model, recorder ViewRecorder) *RecordingModel {
	if inner == nil {
		return nil
	}
	view := inner.View()
	return &RecordingModel{
		inner:       inner,
		recorder:    recorder,
		view:        view,
		captureView: demoRecordingCaptureView(inner, 0, 0, view),
	}
}

func (m *RecordingModel) Init() tea.Cmd {
	if m == nil || m.inner == nil {
		return nil
	}
	return m.inner.Init()
}

func (m *RecordingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m == nil || m.inner == nil {
		return m, nil
	}
	previousWidth := m.width
	previousHeight := m.height
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		if marker, ok := m.recorder.(interface{ MarkInteraction() }); ok {
			marker.MarkInteraction()
		}
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
	}

	next, cmd := m.inner.Update(msg)
	if next == nil {
		next = m.inner
	}
	nextView := next.View()
	nextCaptureView := demoRecordingCaptureView(next, m.width, m.height, nextView)
	changed := nextCaptureView != m.captureView ||
		m.width != previousWidth ||
		m.height != previousHeight
	m.inner = next
	m.view = nextView
	m.captureView = nextCaptureView
	if changed && m.recorder != nil && m.width > 0 && m.height > 0 {
		m.recorder.Capture(m.width, m.height, nextCaptureView)
	}
	return m, cmd
}

func (m *RecordingModel) View() string {
	if m == nil {
		return ""
	}
	if status, ok := m.recorder.(interface {
		Err() error
		DroppedFrames() uint64
	}); ok {
		if err := status.Err(); err != nil {
			return recordingWarningView(m.view, m.width, "DEMO RECORDING FAILED — "+err.Error())
		}
		if dropped := status.DroppedFrames(); dropped > 0 {
			return recordingWarningView(
				m.view,
				m.width,
				fmt.Sprintf("DEMO RECORDING WARNING — %d frames dropped", dropped),
			)
		}
	}
	return m.view
}

func (m *RecordingModel) Unwrap() tea.Model {
	if m == nil {
		return nil
	}
	return m.inner
}

func recordingWarningView(view string, width int, warning string) string {
	line := "\x1b[1;31m" + warning + "\x1b[0m"
	if width > 0 {
		line = ansi.Truncate(line, width, "")
	}
	if _, rest, ok := strings.Cut(view, "\n"); ok {
		return line + "\n" + rest
	}
	return line
}

// demoRecordingPrivacyProvider is intentionally structural so a hosted TUI can
// expose capture-only privacy state without depending on this package. The
// actual terminal view remains visible to its operator; only the saved frame is
// replaced.
type demoRecordingPrivacyProvider interface {
	DemoRecordingPrivate() bool
}

func demoRecordingCaptureView(inner tea.Model, width, height int, view string) string {
	privacy, ok := inner.(demoRecordingPrivacyProvider)
	if !ok || !privacy.DemoRecordingPrivate() {
		return view
	}
	return privateDemoRecordingView(width, height)
}

func privateDemoRecordingView(width, height int) string {
	const message = "PRIVATE VIEW — NOT RECORDED"
	if width <= 0 || height <= 0 {
		return message
	}
	line := ansi.Truncate(message, width, "")
	leftPadding := max(0, (width-ansi.StringWidth(line))/2)
	lines := make([]string, height)
	lines[(height-1)/2] = strings.Repeat(" ", leftPadding) + line
	return strings.Join(lines, "\n")
}
