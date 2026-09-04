package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestUnknownOrphanedWorktreeHotkeyOpensPersistentInspectionDialog(t *testing.T) {
	rootPath := "/tmp/repo"
	orphanPath := "/tmp/lcroom-agent-task-retained"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{{
			Name:             "repo",
			Path:             rootPath,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindMain,
		}},
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{
			rootPath: {{
				Name:                 "lcroom-agent-task-retained",
				Path:                 orphanPath,
				PresentOnDisk:        true,
				Forgotten:            true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "asset/retained-workspace",
			}},
		},
		orphanedCleanupKindByPath: map[string]service.ResidualWorktreeCleanupKind{
			orphanPath: service.ResidualWorktreeCleanupUnknown,
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(orphanPath)

	if footer := ansi.Strip(m.renderFooter(160)); !strings.Contains(footer, "x inspect") {
		t.Fatalf("unknown orphan footer should offer inspection: %q", footer)
	}
	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("x on an unknown orphan should schedule a read-only inspection")
	}
	if got.orphanedWorktreeInspection == nil || !got.orphanedWorktreeInspection.Busy {
		t.Fatalf("inspection dialog = %#v, want visible and busy", got.orphanedWorktreeInspection)
	}
	rendered := ansi.Strip(got.renderOrphanedWorktreeInspectionContent(76))
	for _, want := range []string{
		"Inspecting orphaned worktree",
		"determining what owns this folder",
		"inspection is read-only",
		orphanPath,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("busy inspection dialog missing %q in %q", want, rendered)
		}
	}

	rawMsg := cmd()
	updated, _ = got.Update(rawMsg)
	got = updated.(Model)
	if got.orphanedWorktreeInspection == nil || got.orphanedWorktreeInspection.Busy {
		t.Fatalf("failed inspection should remain visible: %#v", got.orphanedWorktreeInspection)
	}
	rendered = ansi.Strip(got.renderOrphanedWorktreeInspectionContent(76))
	for _, want := range []string{"Inspection needs attention", "service unavailable", "Inspect Again", "Keep"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("failed inspection dialog missing %q in %q", want, rendered)
		}
	}
}

func TestRetainedTaskInspectionOffersDeleteNowAndKeepsDialogOnFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 18, 0, 0, 0, time.Local)
	expiresAt := now.Add(72 * time.Hour)
	projectPath := "/tmp/lcroom-agent-task-retained"
	rootPath := "/tmp/repo"
	m := Model{
		nowFn: func() time.Time { return now },
		orphanedWorktreeInspection: &orphanedWorktreeInspectionState{
			ProjectPath: projectPath,
			RootPath:    rootPath,
			ProjectName: "retained workspace",
			BranchName:  "asset/retained-workspace",
			Busy:        true,
		},
	}
	inspection := service.OrphanedWorktreeInspection{
		ProjectPath:  projectPath,
		RootPath:     rootPath,
		BranchName:   "asset/retained-workspace",
		TargetBranch: "master",
		Resolution:   service.OrphanedWorktreeResolutionDeleteArchivedTaskNow,
		Reason:       "This checkout belongs to the task in Trash.",
		AgentTask: model.AgentTask{
			ID:            "agt_retained",
			Title:         "Textured packed-PBR asset",
			Status:        model.AgentTaskStatusArchived,
			WorkspacePath: projectPath,
			ExpiresAt:     expiresAt,
		},
	}
	updated, _ := m.Update(orphanedWorktreeInspectionMsg{ProjectPath: projectPath, Inspection: inspection})
	got := updated.(Model)
	state := got.orphanedWorktreeInspection
	if state == nil || !state.HaveInspection || state.Busy {
		t.Fatalf("applied retained-task inspection = %#v", state)
	}
	if state.Selected != orphanedWorktreeInspectionKeepIndex(state) {
		t.Fatalf("retained-task default selection = %d, want Keep", state.Selected)
	}
	rendered := ansi.Strip(got.renderOrphanedWorktreeInspectionContent(78))
	for _, want := range []string{
		"Retained task workspace",
		"not abandoned residue",
		"Textured packed-PBR asset",
		"scheduled for automatic deletion",
		"Delete Now permanently deletes the task record",
		"branch remains available",
		"[Delete Now]",
		"Keep",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("retained-task dialog missing %q in %q", want, rendered)
		}
	}

	state.Selected = orphanedWorktreeInspectionActionIndex(state)
	updated, cmd := got.updateOrphanedWorktreeInspectionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || got.orphanedWorktreeInspection == nil || !got.orphanedWorktreeInspection.Busy {
		t.Fatalf("Delete Now should keep a busy dialog while work runs: state=%#v cmd=%v", got.orphanedWorktreeInspection, cmd)
	}
	rawMsg := cmd()
	updated, _ = got.Update(rawMsg)
	got = updated.(Model)
	if got.orphanedWorktreeInspection == nil || got.orphanedWorktreeInspection.Busy {
		t.Fatalf("failed Delete Now should keep the dialog open: %#v", got.orphanedWorktreeInspection)
	}
	rendered = ansi.Strip(got.renderOrphanedWorktreeInspectionContent(78))
	if !strings.Contains(rendered, "service unavailable") || !strings.Contains(rendered, "Inspect Again") {
		t.Fatalf("failed resolution should remain actionable in dialog: %q", rendered)
	}
}
