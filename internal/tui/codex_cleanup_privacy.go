package tui

import (
	"fmt"
	"path/filepath"

	"lcroom/internal/service"
)

func (m Model) codexCleanupGroupPrivate(group service.CodexCleanupWorktreeGroup) bool {
	return m.privacyMode && (m.demoRecordingProjectPrivate(group.WorktreePath) || m.demoRecordingProjectPrivate(group.RootProjectPath))
}

func (m Model) codexCleanupGroupLabel(group service.CodexCleanupWorktreeGroup) string {
	if m.codexCleanupGroupPrivate(group) {
		return "private project"
	}
	return filepath.Base(group.WorktreePath)
}

// Filter the selectable model, so select-all and confirmation cannot include
// hidden projects. The global storage inventory remains a whole-home total.
func (m Model) filterCodexCleanupForPrivacy() {
	d := m.codexCleanup
	if !m.privacyMode || d == nil {
		return
	}
	groups := make([]service.CodexCleanupWorktreeGroup, 0, len(d.Audit.Groups))
	for _, group := range d.Audit.Groups {
		if m.codexCleanupGroupPrivate(group) {
			delete(d.Chosen, group.WorktreePath)
			continue
		}
		groups = append(groups, group)
	}
	d.Audit.Groups = groups
	d.Audit.RecoverableBytes, d.Audit.EligibleRootThreads, d.Audit.EligibleDescendants = codexCleanupGroupTotals(groups)
	retained := make([]service.CodexCleanupRetainedGroup, 0, len(d.Audit.Retained))
	for _, group := range d.Audit.Retained {
		if m.demoRecordingProjectPrivate(group.Path) || m.demoRecordingProjectPrivate(group.ProjectPath) {
			continue
		}
		retained = append(retained, group)
	}
	d.Audit.Retained = retained
	d.Selected = max(0, min(d.Selected, len(groups)-1))
	d.RetainedIndex = max(0, min(d.RetainedIndex, len(retained)-1))
}

// A previously authorized background delete keeps its real queue; only its
// presentation is masked if privacy is enabled while it runs.
func (m Model) codexCleanupPrivacyView() *codexCleanupDialogState {
	d := m.codexCleanup
	if !m.privacyMode || d == nil {
		return d
	}
	view := *d
	view.Queue = append([]service.CodexCleanupWorktreeGroup(nil), d.Queue...)
	for i, group := range view.Queue {
		if m.codexCleanupGroupPrivate(group) {
			view.Queue[i] = service.CodexCleanupWorktreeGroup{WorktreePath: "private project"}
		}
	}
	view.Results = append([]codexCleanupDeleteResult(nil), d.Results...)
	for i, result := range view.Results {
		if m.codexCleanupGroupPrivate(result.Group) {
			view.Results[i].Group = service.CodexCleanupWorktreeGroup{WorktreePath: "private project"}
			if result.Err != nil {
				view.Results[i].Err = fmt.Errorf("cleanup failed for a private project")
			}
		}
	}
	if view.ErrorMessage != "" {
		view.ErrorMessage = "Codex cleanup encountered an error"
	}
	return &view
}
