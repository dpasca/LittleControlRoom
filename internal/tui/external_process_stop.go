package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	externalProcessStopConfirmFocusStop = iota
	externalProcessStopConfirmFocusKeep
)

var externalProcessTerminator = terminateExternalProcess

type externalProcessStopConfirmState struct {
	ProjectPath string
	ProjectName string
	PID         int
	PGID        int
	Command     string
	CWD         string
	Ports       []int
	Selected    int
	Submitting  bool
}

func (m Model) handleStopRuntime(project model.ProjectSummary) (tea.Model, tea.Cmd) {
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		return m, m.stopProjectRuntimeCmd(project.Path)
	}
	if snapshot, ok := m.projectPrimaryLocalInstanceSnapshot(project.Path); ok && snapshot.Running {
		return m, m.openExternalProcessStopConfirm(project, snapshot)
	}
	m.status = "Runtime is not running"
	return m, nil
}

func (m *Model) openExternalProcessStopConfirm(project model.ProjectSummary, snapshot projectrun.Snapshot) tea.Cmd {
	if !snapshot.External {
		m.status = "Runtime is not external"
		return nil
	}
	if snapshot.PID <= 0 {
		m.status = "External process PID is unknown"
		return nil
	}
	m.externalStopConfirm = &externalProcessStopConfirmState{
		ProjectPath: project.Path,
		ProjectName: projectTitle(project.Path, project.Name),
		PID:         snapshot.PID,
		PGID:        snapshot.PGID,
		Command:     strings.TrimSpace(snapshot.Command),
		CWD:         strings.TrimSpace(snapshot.CWD),
		Ports:       append([]int(nil), snapshot.Ports...),
		Selected:    externalProcessStopConfirmFocusKeep,
	}
	m.commandMode = false
	m.err = nil
	m.status = "Confirm external process stop"
	return nil
}

func (m *Model) closeExternalProcessStopConfirm(status string) {
	m.externalStopConfirm = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) cycleExternalProcessStopConfirmSelection(delta int) {
	confirm := m.externalStopConfirm
	if confirm == nil || delta == 0 {
		return
	}
	count := 2
	confirm.Selected = (confirm.Selected + delta + count) % count
}

func (m Model) updateExternalProcessStopConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.externalStopConfirm
	if confirm == nil {
		return m, nil
	}
	if confirm.Submitting {
		if msg.String() == "esc" {
			m.status = "External process stop already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeExternalProcessStopConfirm("External process stop canceled")
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		m.cycleExternalProcessStopConfirmSelection(-1)
		return m, nil
	case "right", "l", "down", "j", "tab":
		m.cycleExternalProcessStopConfirmSelection(1)
		return m, nil
	case "enter":
		if confirm.Selected == externalProcessStopConfirmFocusKeep {
			m.closeExternalProcessStopConfirm("External process stop canceled")
			return m, nil
		}
		confirm.Submitting = true
		m.status = fmt.Sprintf("Stopping external process PID %d...", confirm.PID)
		return m, m.stopExternalProcessCmd(*confirm)
	}
	return m, nil
}

func (m Model) stopExternalProcessCmd(confirm externalProcessStopConfirmState) tea.Cmd {
	return func() tea.Msg {
		if confirm.PID <= 0 {
			return externalProcessStopMsg{
				projectPath: confirm.ProjectPath,
				pid:         confirm.PID,
				err:         fmt.Errorf("external process PID is unknown"),
			}
		}
		if externalProcessTerminator == nil {
			return externalProcessStopMsg{
				projectPath: confirm.ProjectPath,
				pid:         confirm.PID,
				err:         fmt.Errorf("external process terminator unavailable"),
			}
		}
		if err := externalProcessTerminator(confirm.PID); err != nil {
			return externalProcessStopMsg{
				projectPath: confirm.ProjectPath,
				pid:         confirm.PID,
				err:         fmt.Errorf("stop external process PID %d: %w", confirm.PID, err),
			}
		}
		return externalProcessStopMsg{
			projectPath: confirm.ProjectPath,
			pid:         confirm.PID,
			status:      fmt.Sprintf("Requested stop for external process PID %d", confirm.PID),
		}
	}
}

func (m Model) renderExternalProcessStopConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.externalStopConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(56, bodyW-24), 84)
	panelInnerW := max(32, panelW-4)
	lines := []string{
		detailSectionStyle.Render("Stop External Process"),
		"",
		detailValueStyle.Render(truncateText(confirm.ProjectName, panelInnerW)),
		detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(confirm.ProjectPath), panelInnerW)),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, "This process is project-local, but it was not launched as a Little Control Room managed runtime. Stop only if it is stale or unintended.")...)
	lines = append(lines, "",
		detailField("PID", detailValueStyle.Render(fmt.Sprintf("%d", confirm.PID))),
	)
	if confirm.PGID > 0 {
		lines = append(lines, detailField("PGID", detailMutedStyle.Render(fmt.Sprintf("%d", confirm.PGID))))
	}
	if len(confirm.Ports) > 0 {
		lines = append(lines, detailField("Ports", detailValueStyle.Render(joinPorts(confirm.Ports))))
	}
	if strings.TrimSpace(confirm.Command) != "" {
		lines = append(lines, renderWrappedRuntimeField("Command", panelInnerW, detailMutedStyle, confirm.Command)...)
	}
	if strings.TrimSpace(confirm.CWD) != "" {
		lines = append(lines, renderWrappedRuntimeField("CWD", panelInnerW, detailMutedStyle, m.displayPathWithHomeTilde(confirm.CWD))...)
	}
	if confirm.Submitting {
		lines = append(lines, "", detailValueStyle.Render("Stop request in progress"))
	}
	lines = append(lines, "", renderExternalProcessStopConfirmButtons(confirm, m.spinnerFrame))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func renderExternalProcessStopConfirmButtons(confirm *externalProcessStopConfirmState, spinnerFrame int) string {
	if confirm == nil {
		return ""
	}
	if confirm.Submitting {
		return disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(spinnerFrame) + "]")
	}
	return strings.Join([]string{
		renderDialogButton("Stop", confirm.Selected == externalProcessStopConfirmFocusStop),
		renderDialogButton("Keep", confirm.Selected == externalProcessStopConfirmFocusKeep),
	}, " ")
}
