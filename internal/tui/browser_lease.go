package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) ensureBrowserController() *browserctl.Controller {
	if m.browserController == nil {
		m.browserController = browserctl.NewController()
	}
	return m.browserController
}

func (m *Model) refreshBrowserLeaseSnapshot() {
	controller := m.ensureBrowserController()
	if controller == nil {
		m.browserLeaseSnapshot = browserctl.ControllerSnapshot{}
		return
	}
	m.browserLeaseSnapshot = controller.Snapshot()
}

func (m *Model) observeManagedBrowserLease(projectPath string, snapshot codexapp.Snapshot) {
	controller := m.ensureBrowserController()
	if controller == nil {
		return
	}
	projectPath = normalizeProjectPath(firstNonEmptyString(projectPath, snapshot.ProjectPath))
	ref := browserctl.SessionRef{
		Provider:    string(embeddedProvider(snapshot).Normalized()),
		ProjectPath: projectPath,
		SessionID:   strings.TrimSpace(snapshot.ThreadID),
	}
	if !ref.Valid() {
		m.browserLeaseSnapshot = controller.Snapshot()
		return
	}
	loginURL := managedBrowserAttentionURL(snapshot)
	m.browserLeaseSnapshot = controller.Observe(browserctl.Observation{
		Ref:       ref,
		Policy:    snapshot.BrowserActivity.Policy.Normalize(),
		Activity:  snapshot.BrowserActivity.Normalize(),
		LoginURL:  loginURL,
		UpdatedAt: embeddedSnapshotActivityAt(snapshot),
	})
}

func (m *Model) removeManagedBrowserLease(projectPath string, snapshot codexapp.Snapshot) {
	controller := m.ensureBrowserController()
	if controller == nil {
		return
	}
	ref := browserctl.SessionRef{
		Provider:    string(embeddedProvider(snapshot).Normalized()),
		ProjectPath: normalizeProjectPath(firstNonEmptyString(projectPath, snapshot.ProjectPath)),
		SessionID:   strings.TrimSpace(snapshot.ThreadID),
	}
	if !ref.Valid() {
		m.browserLeaseSnapshot = controller.Snapshot()
		return
	}
	m.browserLeaseSnapshot = controller.Remove(ref)
}

func (m *Model) acquireManagedBrowserLease(ref browserctl.SessionRef) browserctl.InteractiveAcquireResult {
	controller := m.ensureBrowserController()
	if controller == nil {
		return browserctl.InteractiveAcquireResult{}
	}
	result := controller.AcquireInteractive(ref)
	m.browserLeaseSnapshot = result.Snapshot
	return result
}

func (m *Model) releaseManagedBrowserLease(ref browserctl.SessionRef) browserctl.ControllerSnapshot {
	controller := m.ensureBrowserController()
	if controller == nil {
		return browserctl.ControllerSnapshot{}
	}
	snapshot := controller.ReleaseInteractive(ref)
	m.browserLeaseSnapshot = snapshot
	return snapshot
}

func managedBrowserLeaseRef(provider codexapp.Provider, projectPath, threadID string) browserctl.SessionRef {
	return browserctl.SessionRef{
		Provider:    string(provider.Normalized()),
		ProjectPath: normalizeProjectPath(projectPath),
		SessionID:   strings.TrimSpace(threadID),
	}
}

func (m Model) openManagedBrowserLogin(projectPath string, provider codexapp.Provider, threadID, managedSessionKey string, activity browserctl.SessionActivity, loginURL, openingStatus, successStatus string) (tea.Model, tea.Cmd) {
	ref := managedBrowserLeaseRef(provider, projectPath, threadID)
	if !ref.Valid() {
		m.status = "Managed browser control is unavailable for this session."
		return m, nil
	}
	controller := m.ensureBrowserController()
	if controller != nil {
		m.browserLeaseSnapshot = controller.Observe(browserctl.Observation{
			Ref:       ref,
			Policy:    activity.Policy.Normalize(),
			Activity:  activity.Normalize(),
			LoginURL:  loginURL,
			UpdatedAt: activity.LastEventAt,
		})
	}
	result := m.acquireManagedBrowserLease(ref)
	if !result.Granted {
		m.status = m.managedBrowserLeaseBlockedStatus(result.Owner)
		return m, nil
	}
	m.status = openingStatus
	return m, m.revealManagedBrowserCmd(managedSessionKey, ref, successStatus)
}

func (m Model) revealManagedBrowserCmd(managedSessionKey string, ref browserctl.SessionRef, successStatus string) tea.Cmd {
	controller := m.ensureBrowserController()
	dataDir := m.appDataDir()
	return func() tea.Msg {
		if err := revealManagedBrowserSession(dataDir, managedSessionKey); err != nil {
			msg := browserOpenMsg{err: err}
			if controller != nil {
				msg.browserLeaseSnapshot = controller.ReleaseInteractive(ref)
				msg.browserLeaseSnapshotSet = true
			}
			return msg
		}
		return browserOpenMsg{status: successStatus}
	}
}

func (m Model) currentInteractiveBrowserLeaseOwner() *browserctl.InteractiveLease {
	if m.browserLeaseSnapshot.Interactive == nil {
		return nil
	}
	owner := m.browserLeaseSnapshot.Interactive.Normalize()
	return &owner
}

func (m Model) managedBrowserLeaseBlockedBy(ref browserctl.SessionRef) *browserctl.InteractiveLease {
	ref = ref.Normalize()
	if !ref.Valid() || m.browserLeaseSnapshot.Interactive == nil {
		return nil
	}
	owner := m.browserLeaseSnapshot.Interactive.Normalize()
	if owner.Ref.Normalize() == ref {
		return nil
	}
	return &owner
}

func (m Model) describeManagedBrowserLease(lease browserctl.InteractiveLease) string {
	normalized := lease.Normalize()
	providerLabel := codexapp.Provider(normalized.Ref.Provider).Label()
	projectLabel := strings.TrimSpace(filepath.Base(normalized.Ref.ProjectPath))
	if projectLabel == "" || projectLabel == "." || projectLabel == string(filepath.Separator) {
		projectLabel = normalized.Ref.ProjectPath
	}
	if projectLabel == "" {
		projectLabel = "unknown project"
	}
	return providerLabel + " / " + projectLabel
}

func (m Model) managedBrowserLeaseBlockedStatus(owner *browserctl.InteractiveLease) string {
	if owner == nil {
		return "Interactive browser is busy. Finish that login flow first."
	}
	return fmt.Sprintf("Interactive browser is already reserved by %s. Finish that login flow first.", m.describeManagedBrowserLease(*owner))
}
