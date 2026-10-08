package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	bossui "lcroom/internal/boss"
	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type projectCollaborationApprovedMsg struct {
	id        string
	operation control.Operation
	scoped    bool
	err       error
}

func (m Model) approveProjectCollaborationCmd(id string) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return projectCollaborationApprovedMsg{id: id, err: fmt.Errorf("store unavailable")}
		}
		op, err := svc.Store().ApproveProjectCollaboration(ctx, id)
		return projectCollaborationApprovedMsg{id: id, operation: op, err: err}
	}
}

func (m Model) applyProjectCollaborationApproved(msg projectCollaborationApprovedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if m.externalControlConfirmation != nil && m.externalControlConfirmation.operation.ID == msg.id {
			state := *m.externalControlConfirmation
			state.submitting = false
			state.errorText = "Approval failed: " + msg.err.Error()
			m.externalControlConfirmation = &state
		}
		m.status = "Project collaboration approval failed: " + msg.err.Error()
		return m, nil
	}
	if m.externalControlConfirmation != nil && m.externalControlConfirmation.operation.ID == msg.id {
		m.externalControlConfirmation = nil
	}
	m.status = "Project collaboration saved; future messages are automatic in both directions"
	if msg.scoped {
		m.status = "LCR permission saved; matching operations within its limits are automatic"
	}
	return m, func() tea.Msg {
		return bossui.ControlInvocationConfirmedMsg{Invocation: msg.operation.Invocation, OperationRecorded: true}
	}
}

type projectCollaborationDialog struct {
	project     string
	pairs       []control.ProjectCollaboration
	permissions []control.ControlPermission
	selected    int
	busy        bool
	errorText   string
}

type projectCollaborationsLoadedMsg struct {
	project     string
	pairs       []control.ProjectCollaboration
	permissions []control.ControlPermission
	err         error
}

func (m *Model) openProjectCollaborations(project string) tea.Cmd {
	if strings.TrimSpace(project) == "" {
		m.status = "Select a project to manage collaboration"
		return nil
	}
	m.projectCollaborationDialog = &projectCollaborationDialog{project: project, busy: true}
	return m.projectCollaborationsCmd(project, nil)
}

func (m Model) projectCollaborationsCmd(project string, revoke *control.ProjectCollaboration, permissionID ...int64) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		msg := projectCollaborationsLoadedMsg{project: project}
		if svc == nil || svc.Store() == nil {
			msg.err = fmt.Errorf("store unavailable")
			return msg
		}
		if revoke != nil {
			if msg.err = svc.Store().RevokeProjectCollaboration(ctx, *revoke); msg.err != nil {
				return msg
			}
		}
		if len(permissionID) > 0 {
			if msg.err = svc.Store().RevokeControlPermission(ctx, project, permissionID[0]); msg.err != nil {
				return msg
			}
		}
		msg.permissions, msg.err = svc.Store().ListControlPermissions(ctx, project)
		if msg.err != nil {
			return msg
		}
		msg.pairs, msg.err = svc.Store().ListProjectCollaborations(ctx, project)
		return msg
	}
}

func (m Model) updateProjectCollaborations(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := *m.projectCollaborationDialog
	if d.busy {
		return m, nil
	}
	switch key.String() {
	case "esc", "q":
		m.projectCollaborationDialog = nil
		return m, nil
	case "up", "k":
		d.selected = max(0, d.selected-1)
	case "down", "j":
		d.selected = min(max(0, len(d.pairs)+len(d.permissions)-1), d.selected+1)
	case "r":
		if d.selected >= len(d.pairs) && d.selected < len(d.pairs)+len(d.permissions) {
			p := d.permissions[d.selected-len(d.pairs)]
			d.busy = true
			m.projectCollaborationDialog = &d
			return m, m.projectCollaborationsCmd(d.project, nil, p.ID)
		}
		if len(d.pairs) > 0 {
			pair := d.pairs[d.selected]
			d.busy = true
			m.projectCollaborationDialog = &d
			return m, m.projectCollaborationsCmd(d.project, &pair)
		}
	}
	m.projectCollaborationDialog = &d
	return m, nil
}

func (m Model) renderProjectCollaborations(body string, width, height int) string {
	d := m.projectCollaborationDialog
	lines := []string{"LCR Permissions", "", filepath.Base(d.project) + " (" + d.project + ")", "", "Saved permissions apply only to their displayed scope.", "Message pairs allow both directions; action grants are directional.", "r: revoke selected permission   Esc: close", ""}
	if d.busy {
		lines = append(lines, "Loading...")
	} else if d.errorText != "" {
		lines = append(lines, d.errorText)
	} else if len(d.pairs)+len(d.permissions) == 0 {
		lines = append(lines, "No saved permissions. Choose s or p at an eligible action review.")
	}
	// Window the list so selection remains visible on short terminals.
	visible := max(1, height-15)
	start := max(0, d.selected-visible+1)
	for i := start; i < min(len(d.pairs)+len(d.permissions), start+visible); i++ {
		if i >= len(d.pairs) {
			p := d.permissions[i-len(d.pairs)]
			scope := "saved"
			if p.SessionKey != "" {
				scope = "session " + shortID(p.SessionKey)
			}
			if p.Limit > 0 {
				scope += fmt.Sprintf(" · %d/%d used", p.Used, p.Limit)
			}
			prefix := "  "
			if i == d.selected {
				prefix = "> "
			}
			lines = append(lines, prefix+string(p.Capability)+" → "+p.Target+" ("+scope+")")
			continue
		}
		other := d.pairs[i].ProjectA
		if other == d.project {
			other = d.pairs[i].ProjectB
		}
		prefix := "  "
		if i == d.selected {
			prefix = "> "
		}
		lines = append(lines, prefix+filepath.Base(other)+" ("+other+")")
	}
	panelW := max(20, min(width-4, 96))
	for i, line := range lines {
		lines[i] = fitPaneContent(line, panelW-4, 1)
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1).Width(panelW - 2).Render(strings.Join(lines, "\n"))
	return overlayBlock(body, panel, width, height, max(0, (width-lipgloss.Width(panel))/2), max(0, (height-lipgloss.Height(panel))/3))
}

func (m Model) approveControlPermissionCmd(id string, sessionOnly bool) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return projectCollaborationApprovedMsg{id: id, scoped: true, err: fmt.Errorf("store unavailable")}
		}
		op, err := svc.Store().ApproveControlPermission(ctx, id, sessionOnly)
		return projectCollaborationApprovedMsg{id: id, scoped: true, operation: op, err: err}
	}
}
