package service

import (
	"path/filepath"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestAgentTaskCallbackBindsExactCallerAcrossRestart(t *testing.T) {
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceClaudeCode, model.SessionSourceOpenCode, model.SessionSourceLCAgent} {
		for _, resumed := range []bool{false, true} {
			name := string(provider) + "/fresh"
			if resumed {
				name = string(provider) + "/resumed"
			}
			t.Run(name, func(t *testing.T) {
				ctx := t.Context()
				cfg := config.Default()
				cfg.DataDir = t.TempDir()
				cfg.DBPath = filepath.Join(cfg.DataDir, "state.sqlite")
				st, err := store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { st.Close() }()
				svc := New(cfg, st, events.NewBus(), nil)
				const path, sessionID = "/projects/caller--worktree", "provider-original"
				key := "host-control-channel"
				if resumed || provider == model.SessionSourceLCAgent {
					key = sessionID
				}
				task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
					Title: "Bounded worker", OriginProjectPath: "/projects/caller", OriginWorktreePath: path,
					OriginProvider: provider, OriginSessionKey: key,
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.MarkAgentTaskReadyForReview(ctx, task.ID, "Changes and tests ready"); err != nil {
					t.Fatal(err)
				}
				task, err = svc.QueueAgentTaskResultCallback(ctx, task.ID)
				if err != nil || task.ResultMessageID != "" || task.OriginSessionID != "" || task.ResultDeliveryError == "" {
					t.Fatalf("unbound result = %#v, %v", task, err)
				}
				// Another provider, worktree, or launch must not resolve this caller.
				for _, activity := range []EmbeddedSessionActivity{
					{ProjectPath: path, Source: provider, ControlSessionKey: "replacement-key", SessionID: "replacement-thread"},
					{ProjectPath: "/projects/caller", Source: provider, ControlSessionKey: key, SessionID: "wrong-worktree-thread"},
				} {
					if err := svc.RecordEmbeddedSessionIdentity(ctx, activity); err != nil {
						t.Fatal(err)
					}
				}
				task, err = svc.QueueAgentTaskResultCallback(ctx, task.ID)
				if err != nil || task.ResultMessageID != "" {
					t.Fatalf("retargeted to replacement: %#v, %v", task, err)
				}
				// A restart while still unbound retains both the task and its control key.
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				svc = New(cfg, st, events.NewBus(), nil)
				// The late announcement itself queues delivery, with no UI list refresh.
				if err := svc.RecordEmbeddedSessionIdentity(ctx, EmbeddedSessionActivity{ProjectPath: path, Source: provider, ControlSessionKey: key, SessionID: sessionID}); err != nil {
					t.Fatal(err)
				}
				if err := st.BindEngineerSession(ctx, path, provider, key, "replacement-thread"); err == nil {
					t.Fatal("rebound original control channel")
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				svc = New(cfg, st, events.NewBus(), nil)
				tasks, err := svc.ListOpenAgentTasks(ctx, 10)
				if err != nil || len(tasks) != 1 || tasks[0].OriginSessionID != sessionID || tasks[0].ResultDeliveryError != "" {
					t.Fatalf("recovered task = %#v, %v", tasks, err)
				}
				messages, err := st.ListQueuedEngineerMessages(ctx, 10)
				if err != nil || len(messages) != 1 {
					t.Fatalf("callbacks = %#v, %v", messages, err)
				}
				// A stale unbound lookup cannot overwrite successful delivery state.
				latest, err := st.RecordAgentTaskCallerPending(ctx, task, "stale lookup")
				if err != nil || latest.ResultDeliveryError != "" {
					t.Fatalf("stale error won binding race: %#v, %v", latest, err)
				}
				msg := messages[0]
				if msg.TargetSessionID != sessionID || msg.RequestedTargetSessionID != sessionID || msg.ProjectPath != path || string(msg.Provider) != string(provider) {
					t.Fatalf("wrong callback target: %#v", msg)
				}
				if _, err := svc.QueueAgentTaskResultCallback(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
				messages, err = st.ListQueuedEngineerMessages(ctx, 10)
				if err != nil || len(messages) != 1 || messages[0].ID != msg.ID {
					t.Fatalf("duplicate callback: %#v, %v", messages, err)
				}
			})
		}
	}
}
