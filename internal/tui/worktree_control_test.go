package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func newWorktreeControlFixture(t *testing.T) (Model, string, string) {
	t.Helper()
	base := t.TempDir()
	root, path := filepath.Join(base, "repo"), filepath.Join(base, "repo--task")
	runTUITestGit(t, base, "init", "-b", "master", root)
	runTUITestGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Initial commit")
	runTUITestGit(t, root, "worktree", "add", "-b", "task", path)
	svc := newControlTestService(t)
	for _, entry := range []struct {
		path string
		kind model.WorktreeKind
	}{{root, model.WorktreeKindMain}, {path, model.WorktreeKindLinked}} {
		if err := svc.Store().UpsertProjectState(t.Context(), model.ProjectState{
			Path: entry.path, Name: filepath.Base(entry.path), PresentOnDisk: true, InScope: true,
			Status: model.StatusIdle, WorktreeRootPath: root, WorktreeKind: entry.kind, UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := svc.Store().ListProjects(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: t.Context(), svc: svc, allProjects: projects, visibility: visibilityAllFolders, sortMode: sortByAttention}
	m.rebuildProjectList(path)
	return m, root, path
}

func TestWorktreeRemoveControlCleansGitStoreAndTUI(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "present", true: "missing"}[missing], func(t *testing.T) {
			m, root, path := newWorktreeControlFixture(t)
			st := m.svc.Store()
			ctx := t.Context()
			todo, err := st.AddTodo(ctx, root, "A task whose checkout can be removed")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AttachTodoWorkSession(ctx, todo.ID, path, model.SessionSourceClaudeCode, "thread", model.TodoWorkStateWaiting, time.Now()); err != nil {
				t.Fatal(err)
			}
			if missing {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := st.SetForgotten(ctx, path, true); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(path, "build-output"), []byte("discard after confirmation"), 0600); err != nil {
				t.Fatal(err)
			}
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
				return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{
					Provider: req.Provider, ThreadID: "thread", Started: true, Phase: codexapp.SessionPhaseIdle,
				}}, nil
			})
			t.Cleanup(func() { _ = manager.CloseAll() })
			if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: codexapp.ProviderClaudeCode}); err != nil {
				t.Fatal(err)
			}
			m.codexManager = manager
			m.codexHiddenProject = path
			args, _ := json.Marshal(control.WorktreeRemoveInput{WorktreePath: path})
			inv, err := control.BuildProposedInvocation("lcrop_remove_test", control.CapabilityWorktreeRemove, args)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateControlOperation(ctx, control.Operation{ID: inv.RequestID, Capability: inv.Capability, Invocation: inv, Source: "test", SessionKey: "thread"}); err != nil {
				t.Fatal(err)
			}
			confirmed := bossui.ControlInvocationConfirmedMsg{Invocation: inv}
			recorded := m.recordExternalControlConfirmationCmd(confirmed)().(externalControlConfirmationRecordedMsg)
			if recorded.err != nil {
				t.Fatal(recorded.err)
			}
			updated, cmd := m.executeBossControlInvocation(recorded.confirmed)
			m = updated.(Model)
			if !missing {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("deletion ran on the UI thread")
				}
			}
			// Repeat activation must not schedule another deletion.
			duplicate := m.executeWorktreeRemoveControl(control.WorktreeRemoveInput{WorktreePath: path})
			if duplicate.err == nil || duplicate.cmd != nil {
				t.Fatal("duplicate removal scheduled")
			}
			var action worktreeActionMsg
			var receipt bossui.ControlInvocationResultMsg
			for _, msg := range collectCmdMsgs(cmd) {
				switch msg := msg.(type) {
				case worktreeActionMsg:
					action = msg
				case bossui.ControlInvocationResultMsg:
					receipt = msg
				}
			}
			if action.err != nil || receipt.Err != nil || receipt.WorktreeResult == nil || !receipt.WorktreeResult.WorktreeRemoved || !receipt.WorktreeResult.IdleSessionClosed {
				t.Fatalf("removal action=%#v receipt=%#v", action, receipt)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("target remains: %v", err)
			}
			if listing := runTUITestGit(t, root, "worktree", "list", "--porcelain"); strings.Contains(listing, path) {
				t.Fatalf("Git registration remains: %s", listing)
			}
			runTUITestGit(t, root, "rev-parse", "--verify", "refs/heads/task")
			project, err := st.GetTrackedProjectSummary(ctx, path)
			if err != nil || project.PresentOnDisk || !project.Forgotten {
				t.Fatalf("LCR record=%#v err=%v", project, err)
			}
			todo, err = st.GetTodo(ctx, todo.ID)
			if err != nil || todo.WorkProjectPath != "" || todo.WorkSessionID != "" || todo.Done {
				t.Fatalf("TODO state=%#v err=%v", todo, err)
			}
			updated, _ = m.Update(action)
			m = updated.(Model)
			if _, ok := m.projectSummaryByPath(path); ok || m.codexHiddenProject != "" {
				t.Fatal("removed row/session remained in TUI until a later scan")
			}
			if _, ok := m.projectSummaryByPath(root); !ok {
				t.Fatal("root disappeared from TUI")
			}
			saved := m.recordExternalControlResultCmd(receipt)().(externalControlResultRecordedMsg)
			if saved.err != nil {
				t.Fatal(saved.err)
			}
			operation, err := st.GetControlOperation(ctx, inv.RequestID)
			if err != nil || operation.Status != control.OperationCompleted || !strings.Contains(string(operation.Result), `"worktree_removed":true`) {
				t.Fatalf("terminal receipt=%#v err=%v", operation, err)
			}
		})
	}
}

func TestWorktreeRemoveControlRejectsUnsafeTargetsAndReportsFailure(t *testing.T) {
	for _, target := range []string{"primary", "untracked", "symlink"} {
		t.Run(target, func(t *testing.T) {
			m, root, path := newWorktreeControlFixture(t)
			switch target {
			case "primary":
				path = root
			case "untracked":
				path = t.TempDir()
			case "symlink":
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, path); err != nil {
					t.Fatal(err)
				}
			}
			inv := controlInvocationRawForTest(t, control.CapabilityWorktreeRemove, control.WorktreeRemoveInput{WorktreePath: path})
			updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
			m = updated.(Model)
			var receipt bossui.ControlInvocationResultMsg
			for _, msg := range collectCmdMsgs(cmd) {
				switch msg := msg.(type) {
				case worktreeActionMsg:
					updated, _ = m.Update(msg)
					m = updated.(Model)
				case bossui.ControlInvocationResultMsg:
					receipt = msg
				}
			}
			if receipt.Err == nil || receipt.WorktreeResult == nil || receipt.WorktreeResult.WorktreeRemoved || !strings.Contains(receipt.Status, "failed") {
				t.Fatalf("false success: %#v", receipt)
			}
			if m.pendingGitSummary(path) != "" {
				t.Fatal("failure left loading state")
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("root was changed: %v", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("rejected target was changed: %v", err)
			}
		})
	}
}

func TestWorktreeRemoveControlCancellationDeletesNothing(t *testing.T) {
	m, _, path := newWorktreeControlFixture(t)
	inv := controlInvocationRawForTest(t, control.CapabilityWorktreeRemove, control.WorktreeRemoveInput{WorktreePath: path})
	m.externalControlConfirmation = &externalControlConfirmationState{
		operation: control.Operation{Invocation: inv}, reviewing: true,
	}
	_, cmd := m.updateExternalControlConfirmationMode(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := cmd().(bossui.ControlInvocationCanceledMsg); !ok {
		t.Fatal("Esc did not cancel removal")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancellation changed checkout")
	}
}
