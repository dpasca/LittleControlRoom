package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDispatchCommandWorktreeRestoreOpensLoadingPicker(t *testing.T) {
	rootPath := "/tmp/demo"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:             "demo",
			Path:             rootPath,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindMain,
		}},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeRestore})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("dispatchCommand(/wt restore) should load recovery candidates asynchronously")
	}
	if got.worktreeRestore == nil || !got.worktreeRestore.Loading || got.worktreeRestore.RootPath != rootPath {
		t.Fatalf("restore dialog = %#v", got.worktreeRestore)
	}
}

func TestWorktreeRestorePickerRendersCandidatesAndStartsReadySelection(t *testing.T) {
	rootPath := "/tmp/demo"
	ready := service.RestorableWorktreeSession{
		RootProjectPath: rootPath,
		WorktreePath:    "/tmp/demo--water",
		WorktreeName:    "demo--water",
		SessionID:       "thread-water-session",
		BranchName:      "feature/water",
		Title:           "Fix coastal water boundaries",
		Summary:         "Continue separating the terrain coastline from the water mesh.",
		LastActivity:    time.Now().Add(-time.Hour),
		BranchExists:    true,
		Ready:           true,
	}
	blocked := service.RestorableWorktreeSession{
		RootProjectPath: rootPath,
		WorktreePath:    "/tmp/demo--blocked",
		SessionID:       "thread-blocked",
		BranchName:      "feature/blocked",
		Title:           "Blocked old session",
		LastActivity:    time.Now(),
		BlockReason:     "branch is checked out elsewhere",
	}
	m := Model{worktreeRestore: &worktreeRestoreDialogState{RootPath: rootPath, Loading: true}}
	updated, cmd := m.applyWorktreeRestoreCandidates(worktreeRestoreCandidatesMsg{
		rootPath:   rootPath,
		candidates: []service.RestorableWorktreeSession{blocked, ready},
	})
	if cmd != nil {
		t.Fatal("applying recovery candidates should not schedule work")
	}
	got := updated.(Model)
	if got.worktreeRestore == nil || got.worktreeRestore.Loading || got.worktreeRestore.Selected != 1 {
		t.Fatalf("loaded restore dialog = %#v", got.worktreeRestore)
	}
	rendered := ansi.Strip(got.renderWorktreeRestoreOverlay("", 110, 34))
	for _, want := range []string{"Restore deleted worktree session", "Fix coastal water boundaries", "feature/water", "thread-w", "retained local branch"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("restore dialog missing %q: %q", want, rendered)
		}
	}

	updated, cmd = got.updateWorktreeRestoreMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || got.worktreeRestore == nil || !got.worktreeRestore.Busy {
		t.Fatalf("Enter on ready recovery should start one busy action: dialog=%#v cmd=%v", got.worktreeRestore, cmd)
	}
	updated, escCmd := got.updateWorktreeRestoreMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if escCmd != nil || got.worktreeRestore == nil || !got.worktreeRestore.Busy {
		t.Fatal("busy restore dialog should ignore Esc until the mutation finishes")
	}
}

func TestWorktreeRestorePickerDoesNotStartBlockedSelection(t *testing.T) {
	m := Model{worktreeRestore: &worktreeRestoreDialogState{
		RootPath: "/tmp/demo",
		Candidates: []service.RestorableWorktreeSession{{
			SessionID:    "thread-blocked",
			WorktreePath: "/tmp/demo--blocked",
			BlockReason:  "recorded commit is unavailable",
		}},
	}}
	updated, cmd := m.updateWorktreeRestoreMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil || got.worktreeRestore.Busy {
		t.Fatal("blocked recovery should not schedule a restore")
	}
	if !strings.Contains(got.status, "recorded commit is unavailable") {
		t.Fatalf("status = %q, want block reason", got.status)
	}
}

func TestWorktreeRestoreEmptyResultClosesPicker(t *testing.T) {
	m := Model{worktreeRestore: &worktreeRestoreDialogState{RootPath: "/tmp/demo", Loading: true}}
	updated, cmd := m.applyWorktreeRestoreCandidates(worktreeRestoreCandidatesMsg{rootPath: "/tmp/demo"})
	got := updated.(Model)
	if cmd != nil || got.worktreeRestore != nil {
		t.Fatalf("empty recovery result should close dialog: %#v", got.worktreeRestore)
	}
	if !strings.Contains(got.status, "No deleted-worktree Codex sessions") {
		t.Fatalf("status = %q", got.status)
	}
}
