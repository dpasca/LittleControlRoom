package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

// activeWakeupGrant is the pre-authorization for one delivered wakeup. It is
// kept in memory only: after an LCR restart a running wakeup falls back to
// waiting for the operator, which is the safe direction.
type activeWakeupGrant struct {
	messageID  string
	sessionID  string
	grant      control.UnattendedGrant
	validUntil time.Time
	sawBusy    bool
	answered   map[string]struct{}
	reported   map[string]struct{}
}

func (m *Model) registerWakeupGrant(message control.EngineerMessage) {
	if m == nil || !message.Scheduled() || message.Unattended == nil || message.Unattended.IsZero() {
		return
	}
	if m.wakeupGrants == nil {
		m.wakeupGrants = make(map[string]*activeWakeupGrant)
	}
	m.wakeupGrants[strings.TrimSpace(message.ProjectPath)] = &activeWakeupGrant{
		messageID:  message.ID,
		sessionID:  strings.TrimSpace(message.TargetSessionID),
		grant:      *message.Unattended,
		validUntil: m.currentTime().Add(message.Unattended.ValidFor()),
		answered:   make(map[string]struct{}),
		reported:   make(map[string]struct{}),
	}
}

// reviewWakeupGrant answers an approval the wakeup's grant covers and reports
// anything it does not. It only reads the cached snapshot; the answer itself
// is sent from a command so the UI thread never blocks on the session.
func (m *Model) reviewWakeupGrant(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	active := m.wakeupGrants[projectPath]
	if active == nil {
		return nil
	}
	if snapshot.Closed || !m.currentTime().Before(active.validUntil) {
		delete(m.wakeupGrants, projectPath)
		return nil
	}
	if !m.wakeupGrantMatchesSession(projectPath, active, snapshot) {
		return nil // Fail closed: a different or unverified session gets no pre-approval.
	}
	pending := snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil
	if snapshot.Busy {
		active.sawBusy = true
	} else if active.sawBusy && !pending {
		delete(m.wakeupGrants, projectPath) // The woken turn ended; later prompts are the operator's.
		return nil
	}
	switch {
	case snapshot.PendingApproval != nil:
		return m.reviewWakeupApproval(projectPath, active, snapshot)
	case snapshot.PendingToolInput != nil:
		m.reportWakeupParked(projectPath, active, snapshot, "tool:"+snapshot.PendingToolInput.ID, snapshot.PendingToolInput.Summary())
	case snapshot.PendingElicitation != nil:
		m.reportWakeupParked(projectPath, active, snapshot, "elicitation:"+snapshot.PendingElicitation.ID, snapshot.PendingElicitation.Summary())
	}
	return nil
}

func (m *Model) wakeupGrantMatchesSession(projectPath string, active *activeWakeupGrant, snapshot codexapp.Snapshot) bool {
	if active.sessionID == "" {
		return false
	}
	return strings.TrimSpace(snapshot.ThreadID) == active.sessionID || m.embeddedSessionMatchesResumeID(projectPath, active.sessionID)
}

func (m *Model) reviewWakeupApproval(projectPath string, active *activeWakeupGrant, snapshot codexapp.Snapshot) tea.Cmd {
	approval := snapshot.PendingApproval
	key := "approval:" + approval.ID
	if _, done := active.answered[key]; done {
		return nil
	}
	if !wakeupGrantCoversApproval(active.grant, projectPath, *approval) {
		m.reportWakeupParked(projectPath, active, snapshot, key, approval.Summary())
		return nil
	}
	active.answered[key] = struct{}{}
	label := embeddedProvider(snapshot).Label()
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		// One-shot accept: never a session-wide allowance the grant did not name.
		if err := session.RespondApproval(codexapp.DecisionAccept); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Wakeup grant approved: " + approval.Summary() + " (" + label + ")"}
	})
}

// wakeupGrantCoversApproval keeps file-change grants inside the project and
// otherwise defers to the grant's own matching rules.
func wakeupGrantCoversApproval(grant control.UnattendedGrant, projectPath string, approval codexapp.ApprovalRequest) bool {
	switch approval.Kind {
	case codexapp.ApprovalCommandExecution:
		return grant.Allows(control.ApprovalKindCommand, approval.Command)
	case codexapp.ApprovalFileChange:
		if root := strings.TrimSpace(approval.GrantRoot); root != "" && !pathWithin(projectPath, root) {
			return false
		}
		return grant.Allows(control.ApprovalKindFileChange, "")
	case codexapp.ApprovalToolUse:
		return grant.Allows(control.ApprovalKindToolUse, approval.ToolName)
	default:
		return false
	}
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// reportWakeupParked leaves the request pending for the operator and says so
// once, so a blocked wakeup is never mistaken for one that is still working.
func (m *Model) reportWakeupParked(projectPath string, active *activeWakeupGrant, snapshot codexapp.Snapshot, key, summary string) {
	if _, done := active.reported[key]; done {
		return
	}
	active.reported[key] = struct{}{}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "a request outside the grant"
	}
	notice := "Scheduled wakeup is waiting for you: " + summary
	m.appendErrorLogEntry("Scheduled wakeup needs approval", errors.New(notice+" (outside its unattended grant)"), projectPath)
	m.status = notice
	if !(m.codexVisible() && m.codexVisibleProject == projectPath) {
		m.questionNotify = &questionNotification{
			ProjectPath: projectPath,
			ProjectName: projectTitle(projectPath, ""),
			Provider:    embeddedProvider(snapshot),
			Summary:     notice,
		}
	}
}
