package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestControlContinuationOnlyWakesTheIntentionallyWaitingCaller(t *testing.T) {
	for _, scenario := range []string{"success", "no intent", "still running", "new input", "stop", "replacement", "closed", "failed", "canceled", "restart", "uncertain delivery", "caller error"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			path := t.TempDir()
			started := time.Now().Add(-time.Second)
			session := &fakeCodexSession{projectPath: path, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: path, ThreadID: "caller-thread", ControlSessionKey: "caller-key", Started: true, Busy: true, ControlInput: codexapp.ControlInputState{Revision: 1, SubmittedAt: time.Now().Add(-time.Millisecond)}}}
			manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return session, nil })
			defer manager.CloseAll()
			if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path}); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(control.TodoAddInput{ProjectPath: path, Text: "Record work"})
			op, err := st.CreateControlOperation(ctx, control.Operation{ID: "lcrop_wait", Source: "mcp", Provider: "codex", SessionKey: "caller-key", ProjectPath: path, ResumeOnSuccess: scenario != "no intent", Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args}})
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ProcessControlContinuations(ctx, st, manager, started); err != nil {
				t.Fatal(err)
			}
			if len(session.submitted) != 0 {
				t.Fatal("woke before execution")
			}
			status := control.OperationCompleted
			session.snapshot.Busy = false
			switch scenario {
			case "still running":
				status = control.OperationRunning
			case "new input":
				session.snapshot.ControlInput.Revision++
				session.snapshot.ControlInput.SubmittedAt = time.Now()
			case "stop":
				session.snapshot.ControlInput.Stopped = true
			case "replacement":
				session.snapshot.ThreadID = "replacement"
			case "closed":
				session.snapshot.Closed = true
			case "failed":
				status = control.OperationFailed
			case "canceled":
				status = control.OperationCanceled
			case "restart":
				started = time.Now()
			case "uncertain delivery":
				list, _ := st.ListPendingControlContinuations(ctx)
				if _, err := st.TransitionControlContinuation(ctx, list[0], "dispatching", ""); err != nil {
					t.Fatal(err)
				}
			case "caller error":
				session.snapshot.LastError = "interrupted"
			}
			if _, err := st.UpdateControlOperationStatus(ctx, op.ID, status, json.RawMessage(`{"status":"done"}`), nil); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := service.ProcessControlContinuations(ctx, st, manager, started); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if scenario == "success" {
				want = 1
			}
			if len(session.submitted) != want {
				t.Fatalf("submitted=%v want %d", session.submitted, want)
			}
			if want == 1 {
				input := session.submissions[0]
				if !input.RequireIdle || input.ExpectedControlInput == nil || input.ExpectedControlInput.Revision != 1 || !strings.Contains(input.Text, op.ID) || !strings.Contains(input.Text, `"status":"done"`) {
					t.Fatalf("unguarded or missing result: %+v", input)
				}
			}
			final, err := st.GetControlOperation(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "no intent" && scenario != "still running" && scenario != "success" && final.ContinuationState != "suppressed" {
				t.Fatal(final.ContinuationState)
			}
		})
	}
}

func TestLateHostObservationCannotArmAWaitAfterNewUserInput(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := t.TempDir()
	started := time.Now().Add(-time.Second)
	args, _ := json.Marshal(control.TodoAddInput{ProjectPath: path, Text: "Record work"})
	_, err = st.CreateControlOperation(ctx, control.Operation{ID: "old", Source: "mcp", Provider: "codex", SessionKey: "key", ProjectPath: path, ResumeOnSuccess: true, Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	session := &fakeCodexSession{projectPath: path, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: path, ThreadID: "thread", ControlSessionKey: "key", Started: true, ControlInput: codexapp.ControlInputState{Revision: 2, SubmittedAt: time.Now()}}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return session, nil })
	defer manager.CloseAll()
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessControlContinuations(ctx, st, manager, started); err != nil {
		t.Fatal(err)
	}
	op, err := st.GetControlOperation(ctx, "old")
	if err != nil || op.ContinuationState != "suppressed" || len(session.submitted) != 0 {
		t.Fatal(op, err)
	}
}
