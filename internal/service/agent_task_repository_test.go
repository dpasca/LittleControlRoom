package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
)

func repositoryTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repositoryTestFixture(t *testing.T) (*Service, model.AgentTask, string) {
	t.Helper()
	root := t.TempDir()
	repositoryTestGit(t, root, "init", "-b", "fixture")
	repositoryTestGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "baseline")
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "check.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(cfg, st, events.NewBus(), nil)
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Writer", Kind: model.AgentTaskKindAgent, OriginProjectPath: root, Repository: model.AgentTaskRepository{Write: true}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, task, root
}

func startRepositoryTurn(t *testing.T, svc *Service, task model.AgentTask, key string) {
	t.Helper()
	unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, key)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestAgentTaskRepositoryExcludesOtherWritersAndCapturesHandoff(t *testing.T) {
	svc, task, root := repositoryTestFixture(t)
	startRepositoryTurn(t, svc, task, "worker")
	current, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || current.Repository.State != "held" || current.Repository.BaseHEAD == "" {
		t.Fatalf("ownership not durable: %+v %v", current.Repository, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, alias, filepath.Join(root, "src")} {
		if unlock, err := svc.BeginRepositoryTurn(path, "caller"); err == nil {
			unlock()
			t.Fatalf("allowed competing writer %s", path)
		}
	}
	if unlock, err := svc.BeginRepositoryProcess(root, root); err == nil {
		unlock()
		t.Fatal("allowed competing process")
	}
	if unlock, err := svc.BeginRepositoryProcess(task.WorkspacePath, root); err != nil {
		t.Fatal(err)
	} else {
		unlock()
	}
	if err := os.WriteFile(filepath.Join(root, "worker.txt"), []byte("review me"), 0600); err != nil {
		t.Fatal(err)
	}
	returned, err := svc.MarkAgentTaskReadyForReview(t.Context(), task.ID, "Ready")
	if err != nil {
		t.Fatal(err)
	}
	if returned.Repository.State != "released" || !strings.Contains(returned.Repository.Changes, "worker.txt") || returned.Repository.HandoffFingerprint == "" || returned.ResultReadyAt.IsZero() {
		t.Fatalf("bad handoff: %+v", returned)
	}
	unlock, err := svc.BeginRepositoryTurn(root, "caller")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	// Dirty correction baselines require a later explicit ownership-boundary API.
	if unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("silently adopted dirty baseline")
	}
	data, _ := os.ReadFile(filepath.Join(root, "worker.txt"))
	if string(data) != "review me" {
		t.Fatal("changed user edits")
	}
}

func TestAgentTaskRepositoryBlocksDirtyBusyAndRunningProcess(t *testing.T) {
	for _, condition := range []string{"dirty", "busy", "goal", "process"} {
		t.Run(condition, func(t *testing.T) {
			svc, task, root := repositoryTestFixture(t)
			switch condition {
			case "dirty":
				os.WriteFile(filepath.Join(root, "existing.txt"), []byte("preserve"), 0600)
			case "busy":
				svc.repositoryEngineers = func() []codexapp.Snapshot { return []codexapp.Snapshot{{ProjectPath: root, Busy: true}} }
			case "goal":
				svc.repositoryEngineers = func() []codexapp.Snapshot {
					return []codexapp.Snapshot{{ProjectPath: root, Goal: &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusActive}}}
				}
			case "process":
				svc.repositoryProcesses = func() []projectrun.Snapshot {
					return []projectrun.Snapshot{{ProjectPath: root, Running: true, PID: 123, ID: "writer"}}
				}
			}
			if unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "worker"); err == nil {
				unlock()
				t.Fatal("unsafe preflight succeeded")
			}
			current, _ := svc.GetAgentTask(t.Context(), task.ID)
			if current.Repository.State != "blocked" || current.Repository.Error == "" {
				t.Fatalf("failure not visible: %+v", current.Repository)
			}
			leases, err := svc.Store().AgentTaskRepositoryLeases(t.Context())
			if err != nil || len(leases) != 0 {
				t.Fatalf("unexpected leases: %+v %v", leases, err)
			}
		})
	}
}

func TestAgentTaskRepositoryDoesNotReleaseBeforeWorkerAndProcessesStop(t *testing.T) {
	svc, task, root := repositoryTestFixture(t)
	startRepositoryTurn(t, svc, task, "worker")
	svc.repositoryEngineers = func() []codexapp.Snapshot { return []codexapp.Snapshot{{ProjectPath: task.WorkspacePath, Busy: true}} }
	if _, err := svc.MarkAgentTaskReadyForReview(t.Context(), task.ID, "premature"); err == nil {
		t.Fatal("released a busy worker")
	}
	svc.repositoryEngineers = func() []codexapp.Snapshot { return nil }
	svc.repositoryProcesses = func() []projectrun.Snapshot {
		return []projectrun.Snapshot{{ProjectPath: task.WorkspacePath, CWD: root, Running: true, ID: "leftover"}}
	}
	if _, err := svc.CompleteAgentTask(t.Context(), task.ID, "accepted"); err == nil {
		t.Fatal("released with a live process")
	}
	current, _ := svc.GetAgentTask(t.Context(), task.ID)
	if current.Repository.State != "held" || !current.ResultReadyAt.IsZero() {
		t.Fatalf("unsafe review transition: %+v", current)
	}
	svc.repositoryProcesses = func() []projectrun.Snapshot { return nil }
	if _, err := svc.MarkAgentTaskReadyForReview(t.Context(), task.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
}

func TestAgentTaskRepositoryRestartRequiresExplicitRelease(t *testing.T) {
	svc, task, _ := repositoryTestFixture(t)
	startRepositoryTurn(t, svc, task, "old-owner")
	cfg := svc.Config()
	svc.Store().Close()
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	recovered := New(cfg, st, events.NewBus(), nil)
	recovered.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
	if unlock, err := recovered.BeginRepositoryTurn(task.WorkspacePath, "new-owner"); err == nil {
		unlock()
		t.Fatal("stole lease after restart")
	}
	if _, err := recovered.CompleteAgentTask(t.Context(), task.ID, "Explicitly stopped old owner"); err != nil {
		t.Fatal(err)
	}
	startRepositoryTurn(t, recovered, task, "new-owner")
}

func TestAgentTaskRepositoryConcurrentAcquisitionHasOneOwner(t *testing.T) {
	svc, first, root := repositoryTestFixture(t)
	second, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "second", Kind: model.AgentTaskKindAgent, OriginProjectPath: root, Repository: model.AgentTaskRepository{Write: true}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, task := range []model.AgentTask{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, task.ID)
			if err == nil {
				unlock()
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d write owners", successes)
	}
}

func TestAgentTaskRepositoryDetectsRevisionChangeWithoutOverwriting(t *testing.T) {
	svc, task, root := repositoryTestFixture(t)
	startRepositoryTurn(t, svc, task, "worker")
	repositoryTestGit(t, root, "switch", "-c", "external-change")
	returned, err := svc.MarkAgentTaskReadyForReview(context.Background(), task.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(returned.Repository.Error, "HEAD or branch changed") || returned.Repository.State != "released" {
		t.Fatalf("missing conflict notice: %+v", returned.Repository)
	}
}

func TestAgentTaskRepositoryGitLockBlocksLaunchAndMissingCheckoutCanBeReleased(t *testing.T) {
	svc, task, root := repositoryTestFixture(t)
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("ignored Git operation lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	startRepositoryTurn(t, svc, task, "worker")
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReleaseTaskRepository(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || current.Repository.State != "released" || !strings.Contains(current.Repository.Error, "evidence unavailable") {
		t.Fatalf("stopped owner stranded: %+v %v", current.Repository, err)
	}
}

func TestAgentTaskRepositoryLeaseProtectsWorkspaceFromRetention(t *testing.T) {
	svc, task, _ := repositoryTestFixture(t)
	startRepositoryTurn(t, svc, task, "worker")
	archived := model.AgentTaskStatusArchived
	old := time.Now().Add(-time.Hour)
	if _, err := svc.Store().UpdateAgentTask(t.Context(), model.UpdateAgentTaskInput{ID: task.ID, Status: &archived, ExpiresAt: &old}); err != nil {
		t.Fatal(err)
	}
	purged, err := svc.PurgeExpiredAgentTasks(t.Context(), time.Now())
	if err != nil || purged != 0 {
		t.Fatalf("purged lease owner: %d %v", purged, err)
	}
	if _, err := os.Stat(task.WorkspacePath); err != nil {
		t.Fatal("removed owner's workspace")
	}
}
