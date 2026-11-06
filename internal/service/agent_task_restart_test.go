package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
)

func TestReleasedTaskRecoversExactHostIdentityAfterRestart(t *testing.T) {
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceClaudeCode, model.SessionSourceOpenCode, model.SessionSourceLCAgent} {
		for _, state := range []string{"released", "blocked", "changed checkout"} {
			t.Run(string(provider)+"/"+state, func(t *testing.T) {
				ctx := t.Context()
				f := newCorrectionFixture(t)
				startRepositoryTurn(t, f.svc, f.task, "original-host-key")
				task, err := f.svc.AttachAgentTaskEngineerSession(ctx, f.task.ID, provider, "exact-worker-thread")
				if err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(task.Repository.Root, "worker.txt")
				if err := os.WriteFile(file, []byte("keep these edits"), 0600); err != nil {
					t.Fatal(err)
				}
				f.snapshot = codexapp.Snapshot{ProjectPath: task.WorkspacePath, Provider: codexapp.Provider(provider), ThreadID: task.SessionID, ControlSessionKey: "original-host-key", Started: true, Phase: codexapp.SessionPhaseIdle}
				if err := f.svc.store.StopStructuredTask(ctx, task, false); err != nil {
					t.Fatal(err)
				}
				task, err = f.svc.GetAgentTask(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				task, err = f.svc.settleStructuredTask(ctx, task)
				if err != nil {
					t.Fatal(err)
				}
				if task.Repository.State != "released" || !task.Workflow.Handoff {
					t.Fatalf("missing handoff: %+v", task)
				}
				if state == "blocked" {
					f.svc.recordRepositoryProblem(ctx, task, errors.New("repository write preflight requires a clean checkout"))
				}
				if state == "changed checkout" {
					if err := os.WriteFile(file, []byte("later edits"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				cfg := f.svc.Config()
				if err := f.svc.Store().Close(); err != nil {
					t.Fatal(err)
				}
				st, err := store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				recovered := New(cfg, st, events.NewBus(), nil)
				recovered.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
				for _, other := range []struct {
					path     string
					provider model.SessionSource
					id       string
				}{
					{task.WorkspacePath, provider, "another-thread"},
					{task.WorkspacePath, model.SessionSourceUnknown, task.SessionID},
					{f.root, provider, task.SessionID},
				} {
					key, err := recovered.TaskResumeControlKey(ctx, other.path, other.provider, other.id)
					if err != nil || key != "" {
						t.Fatalf("unrelated session received worker key: %q %v", key, err)
					}
				}
				key, err := recovered.TaskResumeControlKey(ctx, task.WorkspacePath, provider, task.SessionID)
				if err != nil || key != "original-host-key" {
					t.Fatalf("lost recorded identity after restart: %q %v", key, err)
				}
				unlock, err := recovered.BeginRepositoryTurn(task.WorkspacePath, key)
				if state == "changed checkout" {
					if err == nil {
						unlock()
						t.Fatal("recovered identity bypassed fingerprint verification")
					}
					if !strings.Contains(err.Error(), "checkout changed since the unfinished task stopped") {
						t.Fatal(err)
					}
					data, err := os.ReadFile(file)
					if err != nil || string(data) != "later edits" {
						t.Fatalf("changed checkout not preserved: %q %v", data, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				unlock()
				current, err := recovered.GetAgentTask(ctx, task.ID)
				if err != nil || current.Repository.State != "held" || current.Repository.SessionKey != key || current.Workflow.WorkerKey != key || current.Workflow.RunID != task.Workflow.RunID+1 {
					t.Fatalf("incorrect resumed ownership: %+v %v", current, err)
				}
				data, err := os.ReadFile(file)
				if err != nil || string(data) != "keep these edits" {
					t.Fatalf("resume changed edits: %q %v", data, err)
				}
			})
		}
	}
}

func TestResumeKeyDoesNotBypassHandoffOrReviewBoundaries(t *testing.T) {
	for _, boundary := range []string{"no handoff", "no fingerprint", "submitted result", "reviewed result", "archived"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := t.Context()
			var f *correctionFixture
			var task model.AgentTask
			switch boundary {
			case "no handoff":
				f = newCorrectionFixture(t)
				startRepositoryTurn(t, f.svc, f.task, "original-host-key")
				var err error
				task, err = f.svc.AttachAgentTaskEngineerSession(ctx, f.task.ID, model.SessionSourceCodex, "exact-worker")
				if err != nil {
					t.Fatal(err)
				}
				if err := f.svc.ReleaseTaskRepository(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
			case "submitted result", "reviewed result":
				f = newCorrectionFixture(t)
				f.runWorker(t, 1, func() {
					if err := os.WriteFile(filepath.Join(f.task.Repository.Root, "worker.txt"), []byte("submitted"), 0600); err != nil {
						t.Fatal(err)
					}
				}, ready("ready"))
				if boundary == "reviewed result" {
					f.review(t, "accept")
				}
				task = f.task
			default:
				f, task = stoppedLCAgentHandoff(t)
				if boundary == "no fingerprint" {
					repository := task.Repository
					repository.HandoffFingerprint = ""
					if _, err := f.svc.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Repository: &repository}); err != nil {
						t.Fatal(err)
					}
				} else {
					status := model.AgentTaskStatusArchived
					if _, err := f.svc.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Status: &status}); err != nil {
						t.Fatal(err)
					}
				}
			}
			key, err := f.svc.TaskResumeControlKey(ctx, task.WorkspacePath, task.Provider, task.SessionID)
			if err != nil || key != "" {
				t.Fatalf("bypassed %s: %q %v", boundary, key, err)
			}
		})
	}
}
