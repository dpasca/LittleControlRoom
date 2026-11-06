package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

type restartRepositorySession struct{ *fakeCodexSession }

func (s *restartRepositorySession) Interrupt() error {
	s.snapshot.Busy = false
	s.snapshot.ActiveTurnID = ""
	s.snapshot.Phase = codexapp.SessionPhaseIdle
	s.snapshot.LatestTurnStateKnown = true
	s.snapshot.LatestTurnCompleted = true
	return s.fakeCodexSession.Interrupt()
}

func TestGracefulQuitRestoresReleasedRepositoryTaskForEveryProvider(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := t.Context()
			svc := newControlTestService(t)
			svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
			root := t.TempDir()
			git := func(path string, args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return string(out)
			}
			git(root, "init", "-b", "fixture")
			if err := os.WriteFile(filepath.Join(root, "worker.txt"), []byte("baseline"), 0600); err != nil {
				t.Fatal(err)
			}
			git(root, "add", "worker.txt")
			git(root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "baseline")
			task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
				Title: "Restart worker", Provider: model.SessionSource(provider), OriginProjectPath: root,
				OriginProvider: model.SessionSourceCodex, OriginSessionKey: "caller", OriginSessionID: "caller-thread",
				Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "original-host-key")
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, task.Provider, "exact-worker-thread")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(task.WorkspacePath, "worker.txt")
			if err := os.WriteFile(file, []byte("unfinished worker edits"), 0600); err != nil {
				t.Fatal(err)
			}
			before := git(task.WorkspacePath, "diff", "HEAD", "--binary")
			source := &restartRepositorySession{&fakeCodexSession{projectPath: task.WorkspacePath, snapshot: codexapp.Snapshot{
				Provider: provider, ThreadID: task.SessionID, ControlSessionKey: "original-host-key", Started: true, Busy: true,
				Phase: codexapp.SessionPhaseRunning, ActiveTurnID: "interrupted-turn", LatestTurnStartedAt: time.Now().Add(-time.Minute),
			}}}
			manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return source, nil })
			if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: provider}); err != nil {
				t.Fatal(err)
			}
			svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
			m := Model{ctx: ctx, svc: svc, codexManager: manager, appDataDirPath: svc.Config().DataDir}
			quitting, cmd := m.beginGracefulQuit()
			finished := cmd().(gracefulQuitFinishedMsg)
			if finished.err != nil || finished.captured != 1 {
				t.Fatalf("quit failed: %+v", finished)
			}
			_, quitCmd := quitting.(Model).applyGracefulQuitFinishedMsg(finished)
			if _, ok := quitCmd().(tea.QuitMsg); !ok {
				t.Fatal("one confirmed quit did not exit")
			}
			// A completed provider update can settle ownership while shutdown
			// is running. Restore must work whether ownership stayed held or
			// reached this recorded, released handoff before the process exited.
			task, err = svc.SettleAgentTaskEngineerTurn(ctx, task, source.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			if task.Repository.State != "released" || !task.Workflow.Handoff {
				t.Fatalf("handoff missing: %+v", task)
			}
			cfg := svc.Config()
			if err := svc.Store().Close(); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			svc = service.New(cfg, st, events.NewBus(), nil)
			var request codexapp.LaunchRequest
			manager = codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
				request = req
				return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{
					Provider: provider, ThreadID: req.ResumeID, ControlSessionKey: req.TodoCaptureSessionKey, Started: true,
				}}, nil
			})
			defer manager.CloseAll()
			manager.SetTurnAdmission(svc.BeginRepositoryTurn)
			svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
			m = Model{ctx: ctx, svc: svc, codexManager: manager, appDataDirPath: cfg.DataDir, openAgentTasks: []model.AgentTask{task}}
			saved := m.loadSuspendedTurnResumeChoicesCmd()().(suspendedTurnResumeChoicesMsg)
			if saved.err != nil || len(saved.choices) != 1 {
				t.Fatalf("lost quit journal: %+v", saved)
			}
			_, cmd = m.resumeSuspendedTurnChoices(saved.choices)
			opened := false
			for _, msg := range collectCmdMsgs(cmd) {
				if result, ok := msg.(codexSessionOpenedMsg); ok {
					if result.err != nil {
						t.Fatal(result.err)
					}
					opened = true
				}
			}
			if !opened || request.ResumeID != task.SessionID || request.TodoCaptureSessionKey != "original-host-key" || !request.RequireResumeID || !request.ContinueInterruptedTurn {
				t.Fatalf("restore lost exact worker: %+v", request)
			}
			current, err := svc.GetAgentTask(ctx, task.ID)
			if err != nil || current.Workflow.RunID != task.Workflow.RunID+1 || current.Repository.SessionKey != "original-host-key" {
				t.Fatalf("restore did not reacquire handoff: %+v %v", current, err)
			}
			if git(task.WorkspacePath, "diff", "HEAD", "--binary") != before {
				t.Fatal("restart modified worker edits")
			}
		})
	}
}
