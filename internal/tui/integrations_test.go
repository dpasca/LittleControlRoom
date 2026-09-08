package tui

import (
	"strings"
	"testing"

	"lcroom/internal/integrations"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func integrationDialogModel() Model {
	target := integrations.Target{Provider: "codex", Scope: "user"}
	return Model{width: 100, height: 30, skillsDialog: &skillsDialogState{Target: target, Kind: "mcp", Inventory: integrations.Inventory{Target: target, Revision: strings.Repeat("a", 64), Entries: []integrations.Entry{{ID: "test-id", Name: "example", Kind: "mcp", Enabled: true, Actions: []string{"set_enabled", "remove", "check_mcp"}}}}}}
}

func TestIntegrationDialogPreviewConfirmationAndBusyGuard(t *testing.T) {
	m := integrationDialogModel()
	updated, cmd := m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(Model)
	if cmd != nil || m.skillsDialog.Pending == nil || m.skillsDialog.Busy {
		t.Fatal("toggle bypassed preview")
	}
	if !strings.Contains(m.skillsDialog.Preview, "example enabled=false") {
		t.Fatal("preview omitted concrete toggle target")
	}
	updated, cmd = m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || !m.skillsDialog.Busy || !m.integrationDialogBusy || m.skillsDialog.Pending != nil {
		t.Fatal("confirmation did not queue a single worker")
	}
	_, repeat := m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	if repeat != nil {
		t.Fatal("busy dialog submitted duplicate work")
	}
	updated, _ = m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.skillsDialog != nil || !m.integrationDialogBusy {
		t.Fatal("hiding forgot the active operation")
	}
	_ = m.openSkillsDialog()
	if !m.skillsDialog.Busy {
		t.Fatal("reopening enabled duplicate submission")
	}
	updated, _ = m.applyIntegrationResult(integrationAppliedMsg{dialog: true, result: integrations.Result{Status: "Saved", Activation: integrations.ActivationNotice}})
	m = updated.(Model)
	if m.integrationDialogBusy || m.skillsDialog.Busy || !m.skillsDialog.ShowingResult {
		t.Fatal("completion did not release busy state/show receipt")
	}
}

func TestIntegrationDialogCancelAndStaleRefresh(t *testing.T) {
	m := integrationDialogModel()
	updated, _ := m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	updated, cmd := m.updateSkillsDialogMode(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if cmd != nil || m.skillsDialog.Pending != nil || m.skillsDialog.Busy {
		t.Fatal("cancel queued a mutation")
	}
	m.skillsDialog.RequestID = 10
	m.skillsDialog.Loading = true
	updated, _ = m.applySkillsInventoryMsg(skillsInventoryMsg{requestID: 9, inventory: integrations.Inventory{Revision: "wrong"}})
	m = updated.(Model)
	if !m.skillsDialog.Loading || m.skillsDialog.Inventory.Revision == "wrong" {
		t.Fatal("stale inventory replaced current dialog")
	}
}

func TestIntegrationDialogRendersBoundedRowsAndRecovery(t *testing.T) {
	m := integrationDialogModel()
	for _, width := range []int{64, 100} {
		view := m.renderSkillsDialog(width, 30)
		if lipgloss.Width(view) > width || lipgloss.Height(view) > 30 {
			t.Fatalf("dialog exceeds %dx30: %dx%d", width, lipgloss.Width(view), lipgloss.Height(view))
		}
	}
	m.skillsDialog.ShowingResult = true
	m.skillsDialog.Notice = "Saved"
	m.skillsDialog.LastResult = &integrations.Result{Activation: integrations.ActivationNotice, BackupPaths: []string{"/recovery/config.bak"}}
	if view := m.renderSkillsDialog(100, 30); !strings.Contains(view, "Recovery: /recovery/config.bak") {
		t.Fatalf("recovery path not inspectable: %s", view)
	}
}

func TestIntegrationDialogTableKeepsColumnsAndShortcutsVisible(t *testing.T) {
	m := integrationDialogModel()
	m.skillsDialog.Target.ProjectPath = "/project"
	for i := 0; i < 20; i++ {
		m.skillsDialog.Inventory.Entries = append(m.skillsDialog.Inventory.Entries, integrations.Entry{
			Name: strings.Repeat("長い名前", 20), Kind: "mcp", State: "configured",
			Scope: "project", Source: "native", Description: "A description",
		})
	}
	m.skillsDialog.Selected = 1
	for _, width := range []int{64, 80, 100, 140} {
		m.width = width
		view := m.renderSkillsDialog(width, m.height)
		if lipgloss.Width(view) > width || lipgloss.Height(view) > m.height {
			t.Fatalf("dialog exceeds %dx%d: %dx%d", width, m.height, lipgloss.Width(view), lipgloss.Height(view))
		}
		for _, label := range []string{"Name", "State", "Scope", "Source", "configured", "Tab", "Space", "Esc", "refresh", "Plugins"} {
			if !strings.Contains(view, label) {
				t.Fatalf("width %d omitted %q", width, label)
			}
		}
	}
}
