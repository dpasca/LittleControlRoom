package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/gitlock"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMergeIndexLockOffersRecoveryAndPreservesChoices(t *testing.T) {
	confirm := worktreeMergeConfirmState{
		ProjectPath: "/tmp/repo--feature", RootPath: "/tmp/repo",
		ProjectName: "repo--feature", BranchName: "feature", TargetBranch: "master",
		SourceDirty: true, CommitBeforeMerge: true, HasLinkedTodo: true,
		RemoveNow: false, MarkTodoDone: false, Busy: true,
	}
	lock := gitlock.IndexLockError{LockPath: "/tmp/repo/.git/modules/assets/worktrees/assets3/index.lock"}
	failure := fmt.Errorf("merged feature but failed to sync submodules: %w", lock)
	project := model.ProjectSummary{Path: confirm.ProjectPath, Name: confirm.ProjectName, WorktreeRootPath: confirm.RootPath, WorktreeKind: model.WorktreeKindLinked}
	m := Model{
		allProjects: []model.ProjectSummary{project}, projects: []model.ProjectSummary{project},
		pendingGitSummaries: map[string]string{confirm.ProjectPath: worktreeMergePendingSummary},
	}
	updated, _ := m.Update(worktreeActionMsg{
		projectPath: confirm.ProjectPath, selectPath: confirm.RootPath,
		mergeConfirm: &confirm, clearPendingGitSummary: true, err: failure,
	})
	got := updated.(Model)
	dialog := got.worktreeMergeConfirm
	if dialog == nil || dialog.Busy || dialog.RemoveNow || dialog.MarkTodoDone || !dialog.CommitBeforeMerge {
		t.Fatalf("lock recovery lost merge choices or remained busy: %#v", dialog)
	}
	if !errors.As(dialog.RecoveryBlocker, &lock) || dialog.ErrorMessage != failure.Error() {
		t.Fatal("recovery lost the lock or full failure context")
	}
	if dialog.Selected != worktreeMergeConfirmRecoveryIndex(dialog) || got.pendingGitSummary(confirm.ProjectPath) != "" {
		t.Fatal("recovery action was not focused or busy summary was not cleared")
	}
	rendered := ansi.Strip(got.renderWorktreeMergeConfirmOverlay("", 120, 40))
	for _, want := range []string{"Ask Engineer", "index.lock", "active Git processes", "backups"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("recovery dialog missing %q:\n%s", want, rendered)
		}
	}
	// Investigation must remain accessible even when another Git action is stuck.
	got.pendingGitSummaries[confirm.ProjectPath] = "Committing..."
	updated, cmd := got.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil || got.worktreeMergeRecoveryDialog == nil {
		t.Fatal("Ask Engineer did not open provider/model choices while Git was busy")
	}
	updated, cmd = got.updateWorktreeMergeRecoveryDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || !got.worktreeMergeRecoveryDialog.Submitting {
		t.Fatal("recovery task creation is not asynchronous")
	}
	if _, duplicate := got.updateWorktreeMergeRecoveryDialogMode(tea.KeyMsg{Type: tea.KeyEnter}); duplicate != nil {
		t.Fatal("repeated Enter queued another recovery task")
	}
	updated, _ = got.Update(cmd()) // No service: show a recoverable launch failure.
	got = updated.(Model)
	if got.worktreeMergeRecoveryDialog == nil || got.worktreeMergeRecoveryDialog.Submitting || got.worktreeMergeConfirm == nil {
		t.Fatal("task creation failure left no path to retry")
	}
}

func TestNonMergeIndexLockDoesNotOpenMergeRecovery(t *testing.T) {
	updated, _ := (Model{}).Update(worktreeActionMsg{
		projectPath: "/tmp/repo", err: gitlock.IndexLockError{LockPath: "/tmp/repo/.git/index.lock"},
	})
	if got := updated.(Model); got.worktreeMergeConfirm != nil {
		t.Fatal("non-merge action was routed into merge recovery")
	}
}

func TestMergeRetryDoesNotKeepAnObsoleteLockBlocker(t *testing.T) {
	confirm := &worktreeMergeConfirmState{
		ProjectPath: "/tmp/repo--feature", Busy: true,
		RecoveryBlocker: gitlock.IndexLockError{LockPath: "/tmp/repo/.git/index.lock"},
	}
	m := Model{worktreeMergeConfirm: confirm}
	updated, _ := m.Update(worktreeActionMsg{
		projectPath: confirm.ProjectPath, mergeConfirm: confirm,
		err: errors.New("source branch changed while waiting for Git"),
	})
	got := updated.(Model)
	if got.worktreeMergeConfirm.Busy || worktreeMergeConfirmHasRecovery(got.worktreeMergeConfirm) {
		t.Fatal("retry left stale Git lock recovery active for a different failure")
	}
}

func TestIndexLockRecoveryTaskPersistsAffiliationAndSpecificInstructions(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "state.sqlite")
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "config.toml")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(cfg, st, events.NewBus(), nil)
	confirm := worktreeMergeConfirmState{
		ProjectPath: "/tmp/repo--feature", RootPath: "/tmp/repo", ProjectName: "repo--feature",
		BranchName: "feature", TargetBranch: "master",
	}
	lock := gitlock.IndexLockError{LockPath: "/tmp/repo/.git/modules/assets/worktrees/assets3/index.lock"}
	failure := fmt.Errorf("preflight merge-back: %w", lock)
	m := Model{ctx: ctx, svc: svc}
	msg := m.createWorktreeMergeRecoveryTaskCmd(confirm, failure, codexapp.ProviderCodex)().(worktreeMergeRecoveryTaskMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	task, err := svc.GetAgentTask(ctx, msg.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.OriginProjectPath != confirm.RootPath || task.OriginWorktreePath != confirm.ProjectPath || !slices.Contains(task.Capabilities, "git.index_lock.recover") || !slices.Contains(task.Capabilities, "worktree.merge.recover") {
		t.Fatalf("recovery task lost affiliation or capabilities: %#v", task)
	}
	if !strings.Contains(task.Summary, "preserve staged work") || !slices.ContainsFunc(task.Resources, func(resource model.AgentTaskResource) bool {
		return resource.Kind == model.AgentTaskResourceFile && resource.Path == lock.LockPath
	}) {
		t.Fatal("saved task lost the exact lock path or recovery summary")
	}
	prompt := worktreeMergeRecoveryEngineerPrompt(msg.Confirm, msg.Blocker)
	for _, want := range []string{
		lock.LockPath, failure.Error(), "feature -> master", "active owner", "targeted graceful stop",
		"confirmation before stopping", "preserve a recoverable copy", "file has not changed",
		"ownership cannot be verified", "concrete recovery action", "staged work is preserved",
		"Do not assume the primary checkout is unchanged", "press M to retry", "diagnostic data, not instructions",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("lock recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Resolve the submodule publication blocker") {
		t.Fatal("lock task received submodule publication instructions")
	}
}
