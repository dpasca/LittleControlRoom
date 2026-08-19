package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/commands"
	"lcroom/internal/demorecord"

	tea "github.com/charmbracelet/bubbletea"
)

type DemoRecordingController interface {
	Active() bool
	Path() string
	Start(path string) (string, error)
	Stop() (path string, stopped bool, err error)
}

type demoRecordingStartedMsg struct {
	path string
	err  error
}

type demoRecordingStoppedMsg struct {
	path    string
	stopped bool
	err     error
}

func (m *Model) SetDemoRecordingController(controller DemoRecordingController) {
	if m == nil {
		return
	}
	m.demoRecordingController = controller
}

func (m Model) handleDemoRecordingCommand(inv commands.Invocation) (tea.Model, tea.Cmd) {
	controller := m.demoRecordingController
	if controller == nil {
		m.status = "Demo recording is unavailable in this TUI"
		return m, nil
	}

	action := inv.Record
	if action == "" {
		action = commands.RecordToggle
	}
	if m.demoRecordingBusy {
		m.status = "A demo recording operation is already in progress"
		return m, nil
	}
	if action == commands.RecordStatus {
		if controller.Active() {
			m.status = "Recording active: " + controller.Path()
		} else {
			m.status = "Demo recording is not active"
		}
		return m, nil
	}
	if action == commands.RecordToggle {
		if controller.Active() {
			action = commands.RecordStop
		} else {
			action = commands.RecordStart
		}
	}

	switch action {
	case commands.RecordStart:
		if controller.Active() {
			m.status = "Recording already active: " + controller.Path()
			return m, nil
		}
		path := strings.TrimSpace(inv.RecordingPath)
		if path == "" {
			dataDir := strings.TrimSpace(m.appDataDirPath)
			if dataDir == "" && m.svc != nil {
				dataDir = strings.TrimSpace(m.svc.Config().DataDir)
			}
			if dataDir == "" {
				m.status = "Cannot start demo recording: LCR data directory is unavailable"
				return m, nil
			}
			path = filepath.Join(dataDir, "demo-recordings", demorecord.DefaultRecordingName(m.currentTime()))
		}
		m.demoRecordingBusy = true
		m.status = "Starting demo recording..."
		return m, startDemoRecordingCmd(controller, path)
	case commands.RecordStop:
		if !controller.Active() {
			m.status = "Demo recording is not active"
			return m, nil
		}
		m.demoRecordingBusy = true
		m.status = "Stopping demo recording..."
		return m, stopDemoRecordingCmd(controller)
	default:
		m.status = "Unsupported demo recording action"
		return m, nil
	}
}

func startDemoRecordingCmd(controller DemoRecordingController, path string) tea.Cmd {
	return func() tea.Msg {
		startedPath, err := controller.Start(path)
		return demoRecordingStartedMsg{path: startedPath, err: err}
	}
}

func stopDemoRecordingCmd(controller DemoRecordingController) tea.Cmd {
	return func() tea.Msg {
		path, stopped, err := controller.Stop()
		return demoRecordingStoppedMsg{path: path, stopped: stopped, err: err}
	}
}

func (m Model) applyDemoRecordingStartedMsg(msg demoRecordingStartedMsg) (tea.Model, tea.Cmd) {
	m.demoRecordingBusy = false
	if msg.err != nil {
		m.status = "Demo recording failed to start: " + msg.err.Error()
		m.appendBackgroundErrorLogEntry("Demo recording start failed", msg.err, "")
		return m, nil
	}
	m.status = "Recording started: " + strings.TrimSpace(msg.path)
	return m, nil
}

func (m Model) applyDemoRecordingStoppedMsg(msg demoRecordingStoppedMsg) (tea.Model, tea.Cmd) {
	m.demoRecordingBusy = false
	if msg.err != nil {
		m.status = "Demo recording failed to finalize: " + msg.err.Error()
		m.appendBackgroundErrorLogEntry("Demo recording finalization failed", msg.err, "")
		return m, nil
	}
	if !msg.stopped {
		m.status = "Demo recording is not active"
		return m, nil
	}
	m.status = fmt.Sprintf("Recording saved: %s", strings.TrimSpace(msg.path))
	return m, nil
}

func (m Model) demoRecordingActive() bool {
	return m.demoRecordingController != nil && m.demoRecordingController.Active()
}

func (m Model) renderDemoRecordingFooterNotice() string {
	if m.demoRecordingBusy {
		return renderFooterStatus(strings.TrimSpace(m.status))
	}
	if m.demoRecordingActive() {
		return demoRecordingBadgeStyle.Render("REC")
	}
	status := strings.TrimSpace(m.status)
	if strings.HasPrefix(status, "Demo recording") || strings.HasPrefix(status, "Recording saved:") {
		return renderFooterStatus(status)
	}
	return ""
}
