package tui

import (
	"strings"
	"testing"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestRepositoryIntegrityAddsFamilyAttention(t *testing.T) {
	root := model.ProjectSummary{Path: "/repos/demo", Name: "demo", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: "/repos/demo"}
	linked := model.ProjectSummary{Path: "/repos/demo--feature", Name: "demo--feature", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/repos/demo"}
	state := model.RepositoryIntegrityState{
		RootPath:       root.Path,
		RootName:       root.Name,
		ExpectedBranch: "master",
		ActualBranch:   "feature/root",
		Mode:           model.RepositoryIntegrityModeWarn,
		Displaced:      true,
		Members: []model.RepositoryIntegrityMember{
			{Path: root.Path, WorktreeKind: model.WorktreeKindMain},
			{Path: linked.Path, WorktreeKind: model.WorktreeKindLinked},
		},
	}
	m := Model{
		allProjects:               []model.ProjectSummary{root, linked},
		projects:                  []model.ProjectSummary{root, linked},
		repositoryIntegrityByRoot: map[string]model.RepositoryIntegrityState{root.Path: state},
	}
	if got := m.projectAttentionScore(linked); got != repositoryIntegrityAttentionWeight {
		t.Fatalf("projectAttentionScore() = %d, want %d", got, repositoryIntegrityAttentionWeight)
	}
	reason := m.projectRepositoryIntegrityAttentionReason(linked.Path)
	if reason == nil || reason.Code != "repository_root_displaced" || !strings.Contains(reason.Text, "expected master") {
		t.Fatalf("attention reason = %#v", reason)
	}
}

func TestRepositoryIntegrityDialogDefaultsToKeep(t *testing.T) {
	project := model.ProjectSummary{Path: "/repos/demo", Name: "demo", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: "/repos/demo"}
	state := model.RepositoryIntegrityState{
		RootPath:              project.Path,
		RootName:              project.Name,
		ExpectedBranch:        "master",
		ActualBranch:          "feature/root",
		ExpectedBranchSource:  "worktree_creation",
		Mode:                  model.RepositoryIntegrityModeWarn,
		Displaced:             true,
		CanRepair:             true,
		SuggestedWorktreePath: "/repos/demo--feature-root",
		Fingerprint:           "incident-1",
		Members:               []model.RepositoryIntegrityMember{{Path: project.Path, Name: project.Name, WorktreeKind: model.WorktreeKindMain}},
	}
	m := Model{
		allProjects:               []model.ProjectSummary{project},
		projects:                  []model.ProjectSummary{project},
		repositoryIntegrityByRoot: map[string]model.RepositoryIntegrityState{project.Path: state},
		selected:                  0,
	}
	if cmd := m.openRepositoryIntegrityDialogForSelection(); cmd != nil {
		t.Fatal("open dialog should not schedule work")
	}
	if m.repositoryIntegrityDialog == nil || m.repositoryIntegrityDialog.Selected != repositoryIntegrityKeep {
		t.Fatalf("dialog = %#v, want Keep selected", m.repositoryIntegrityDialog)
	}
	rendered := ansi.Strip(m.renderRepositoryIntegrityDialogContent(*m.repositoryIntegrityDialog, 90))
	for _, text := range []string{"Repository Root Integrity", "Expected:", "master", "Repair Safely", "Ask Engineer", "Use Current", "Keep"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("dialog missing %q: %q", text, rendered)
		}
	}
	narrow := ansi.Strip(m.renderRepositoryIntegrityDialogContent(*m.repositoryIntegrityDialog, 64))
	if got := strings.Count(narrow, "\n") + 1; got > 20 {
		t.Fatalf("narrow dialog height = %d, want <= 20 lines:\n%s", got, narrow)
	}

	updated, cmd := m.updateRepositoryIntegrityDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := normalizeUpdateModel(updated)
	if cmd == nil || got.repositoryIntegrityDialog == nil || !got.repositoryIntegrityDialog.Busy {
		t.Fatalf("Enter on Keep = dialog %#v cmd=%v, want explicit async acknowledgement", got.repositoryIntegrityDialog, cmd != nil)
	}
}

func TestRepositoryIntegrityEngineerPromptRequiresConfirmation(t *testing.T) {
	state := model.RepositoryIntegrityState{
		RootPath:              "/repos/demo",
		ExpectedBranch:        "master",
		ActualBranch:          "feature/root",
		SuggestedWorktreePath: "/repos/demo--feature-root",
		RecentExcursions: []model.RepositoryWorkspaceExcursion{{
			CWD:     "/repos/demo",
			Command: "git status",
		}},
	}
	prompt := repositoryIntegrityEngineerPrompt(state)
	for _, required := range []string{
		"investigation-only mode",
		"do not mutate",
		"explicit confirmation",
		"Expected root branch: master",
		"Current root branch: feature/root",
		"git status",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("engineer prompt missing %q:\n%s", required, prompt)
		}
	}
}
