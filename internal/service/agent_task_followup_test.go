package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfirmedAcceptedFollowUpPreservesHistoryAndEdits(t *testing.T) {
	for _, mode := range []string{"explicit followup", "ordinary launch", "changed checkout"} {
		t.Run(mode, func(t *testing.T) {
			f := newCorrectionFixture(t)
			path := filepath.Join(f.task.Repository.Root, "accepted.txt")
			f.runWorker(t, 1, func() {
				if err := os.WriteFile(path, []byte("accepted work"), 0600); err != nil {
					t.Fatal(err)
				}
			}, ready("accepted"))
			f.review(t, "accept")
			expected := f.task
			before, err := f.svc.GetAgentTaskResult(t.Context(), expected.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed checkout" {
				if err := os.WriteFile(path, []byte("intervening edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			unlock, err := f.svc.BeginConfirmedTaskTurn(expected, "next-host-key", mode != "ordinary launch")
			if mode != "explicit followup" {
				if err == nil {
					unlock()
					t.Fatal("crossed an unauthorized result boundary")
				}
				if mode == "changed checkout" && !strings.Contains(err.Error(), "checkout changed since accepted revision") {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			current := f.reload(t)
			if current.Workflow.RunID != 2 || current.Workflow.Result != nil || current.Workflow.Review != nil || !current.Workflow.SupervisionRevoked || current.Repository.SessionKey != "next-host-key" {
				t.Fatalf("incorrect followup: %+v", current)
			}
			after, err := f.svc.GetAgentTaskResult(t.Context(), expected.ID, 1)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("accepted history changed: before %+v after %+v %v", before, after, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "accepted work" {
				t.Fatalf("accepted edits changed: %q %v", data, err)
			}
		})
	}
}

func TestExplicitStopInvalidatesQueuedTaskLaunch(t *testing.T) {
	f := newCorrectionFixture(t)
	queued := f.task
	stopped, err := f.svc.CancelAgentTaskRun(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Workflow.Phase != "canceled" || stopped.Status != "waiting" {
		t.Fatalf("stop not durable: %+v", stopped)
	}
	if unlock, err := f.svc.BeginConfirmedTaskTurn(queued, "stale-launch", false); err == nil {
		unlock()
		t.Fatal("queued launch revived a stopped task")
	}
	current := f.reload(t)
	if current.Repository.State == "held" || current.Workflow.RunID != queued.Workflow.RunID {
		t.Fatalf("stale launch changed ownership: %+v", current)
	}
}

// A caller merges an accepted worker result by committing exactly the accepted
// edits in the worker checkout, including a commit in a linked submodule
// worktree, while unrelated untracked files stay behind. Explicit continuation
// must still resume the same worker; any unreviewed content must not.
func TestAcceptedFollowUpAfterCallerCommitsAcceptedEdits(t *testing.T) {
	commit := func(t *testing.T, root string, args ...string) {
		t.Helper()
		repositoryTestGit(t, root, append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "protocol.file.allow=always"}, args...)...)
	}
	write := func(t *testing.T, path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"exact commit", "unreviewed submodule edit", "unreviewed committed file", "other branch"} {
		t.Run(mode, func(t *testing.T) {
			source := t.TempDir()
			repositoryTestGit(t, source, "init", "-b", "main")
			write(t, filepath.Join(source, "cfg.txt"), "heading 90\n")
			commit(t, source, "add", "cfg.txt")
			commit(t, source, "commit", "-m", "assets")
			f := newCorrectionFixtureWith(t, func(root string) {
				write(t, filepath.Join(root, "hud.txt"), "hud v1\n")
				commit(t, root, "submodule", "add", source, "Assets")
				commit(t, root, "add", "hud.txt")
				commit(t, root, "commit", "-m", "add assets")
			})
			checkout := f.task.Repository.Root
			// worktreeprep holds the submodule as a linked worktree of the shared
			// module store, as in LCR-managed task checkouts.
			sub := filepath.Join(checkout, "Assets")
			if gitDir := repositoryGitOutput(t, sub, "rev-parse", "--git-dir"); !strings.Contains(gitDir, "worktrees") {
				t.Fatalf("submodule is not a linked worktree: %s", gitDir)
			}
			f.runWorker(t, 1, func() {
				write(t, filepath.Join(checkout, "hud.txt"), "hud v2 surface marks\n")
				write(t, filepath.Join(checkout, "marks.txt"), "a1 a2\n")
				write(t, filepath.Join(sub, "cfg.txt"), "heading 80\n")
				write(t, filepath.Join(sub, "ui_language_en.png"), "untracked artifact")
			}, ready("surface marks"))
			f.review(t, "accept")
			if f.task.Repository.HandoffContent == "" {
				t.Fatalf("handoff recorded no content digest: %+v", f.task.Repository)
			}
			commit(t, sub, "checkout", "-b", "tfa/l166")
			commit(t, sub, "add", "cfg.txt")
			commit(t, sub, "commit", "-m", "heading 80")
			if mode == "other branch" {
				commit(t, checkout, "checkout", "-b", "merge-elsewhere")
			}
			commit(t, checkout, "add", "hud.txt", "marks.txt", "Assets")
			if mode == "unreviewed committed file" {
				write(t, filepath.Join(checkout, "extra.txt"), "never reviewed")
				commit(t, checkout, "add", "extra.txt")
			}
			commit(t, checkout, "commit", "-m", "merge accepted result")
			if mode == "unreviewed submodule edit" {
				write(t, filepath.Join(sub, "cfg.txt"), "heading 70\n")
			}
			unlock, err := f.svc.BeginConfirmedTaskTurn(f.task, "next-host-key", true)
			if mode != "exact commit" {
				if err == nil {
					unlock()
					t.Fatal("continued over unreviewed changes")
				}
				want := map[string]string{"other branch": "branch changed", "unreviewed submodule edit": "content differs", "unreviewed committed file": "content differs"}[mode]
				if !strings.Contains(err.Error(), "checkout changed since accepted revision 1") || !strings.Contains(err.Error(), want) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			current := f.reload(t)
			head := strings.TrimSpace(repositoryGitOutput(t, checkout, "rev-parse", "HEAD"))
			if current.Workflow.RunID != 2 || current.Repository.State != "held" || current.Repository.SessionKey != "next-host-key" || current.Repository.BaseHEAD != head || current.Repository.HandoffContent != "" {
				t.Fatalf("incorrect followup: %+v", current)
			}
			if data, err := os.ReadFile(filepath.Join(sub, "ui_language_en.png")); err != nil || string(data) != "untracked artifact" {
				t.Fatalf("untracked artifact changed: %q %v", data, err)
			}
		})
	}
}

func repositoryGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := repositoryGit(t.Context(), root, args...)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
