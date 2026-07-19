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
	inner    tea.Model
	recorder ViewRecorder
	view     string
	width    int
	height   int
}

func WrapModel(inner tea.Model, recorder ViewRecorder) *RecordingModel {
	if inner == nil {
		return nil
	}
	return &RecordingModel{
		inner:    inner,
		recorder: recorder,
		view:     inner.View(),
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
	changed := nextView != m.view ||
		m.width != previousWidth ||
		m.height != previousHeight
	m.inner = next
	m.view = nextView
	if changed && m.recorder != nil && m.width > 0 && m.height > 0 {
		m.recorder.Capture(m.width, m.height, nextView)
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
