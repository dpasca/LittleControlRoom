package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

const (
	dispatchCallerPath = "/projects/game--producer"
	dispatchCallerKey  = "producer-control-key"
	dispatchWorkerPath = "/projects/game--todo-fly-1"
)

func newEngineerDispatchTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "state.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(cfg, st, events.NewBus(), nil), st
}

func createDispatchTestOperation(t *testing.T, st *store.Store, id, projectPath, provider, key string, capability control.CapabilityName, args any) control.Operation {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateControlOperation(t.Context(), control.Operation{
		ID: id, Provider: provider, SessionKey: key, ProjectPath: projectPath, Source: "mcp",
		Invocation: control.Invocation{RequestID: id, Capability: capability, Args: payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func deliverDispatchTestMessage(t *testing.T, st *store.Store, operationID, prompt string) control.EngineerMessage {
	t.Helper()
	ctx := t.Context()
	message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
		OperationID: operationID, ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex,
		SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "worker-thread", Prompt: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "worker-thread"); err != nil || !claimed {
		t.Fatalf("claim follow-up: claimed=%t err=%v", claimed, err)
	}
	message, err = st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestEngineerDispatchReportsRequestedTurnsToExactCaller(t *testing.T) {
	ctx := t.Context()
	svc, st := newEngineerDispatchTestService(t)
	launch := createDispatchTestOperation(t, st, "lcrop_dispatch_launch", dispatchCallerPath, "claude_code", dispatchCallerKey,
		control.CapabilityTodoCreateWorktreeAndStartEngineer,
		map[string]any{"project_path": "/projects/game", "todo_text": "FLY-1 human pilot layer", "provider": "codex"})
	dispatch, recorded, err := svc.RecordEngineerDispatch(ctx, EngineerDispatchLaunch{
		OperationID: launch.ID, TodoID: 7, TodoText: "FLY-1 human pilot layer",
		WorkerProjectPath: dispatchWorkerPath, WorkerProvider: model.SessionSourceCodex, WorkerSessionID: "worker-thread",
	})
	if err != nil || !recorded || dispatch.ReplyState != model.EngineerDispatchReplyAwaiting || dispatch.CallerSessionID != "" {
		t.Fatalf("recorded dispatch = %#v, recorded=%t err=%v", dispatch, recorded, err)
	}
	replayed, _, err := svc.RecordEngineerDispatch(ctx, EngineerDispatchLaunch{
		OperationID: launch.ID, WorkerProjectPath: dispatchWorkerPath, WorkerProvider: model.SessionSourceCodex,
	})
	if err != nil || replayed.ID != dispatch.ID {
		t.Fatalf("replayed launch = %#v, %v; want dispatch %d", replayed, err, dispatch.ID)
	}

	// Another session in the same worktree does not answer the request.
	if _, routed, err := routeSingleEngineerDispatch(t, svc, EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "operator-thread", Summary: "Unrelated work finished.",
	}); err != nil || routed {
		t.Fatalf("other session routed=%t err=%v", routed, err)
	}

	// Until the caller's provider identity is bound, the reply waits visibly.
	completion := EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread",
		Summary: "Implemented the pilot layer; ctest passes.",
	}
	dispatch, routed, err := routeSingleEngineerDispatch(t, svc, completion)
	if err != nil || !routed || dispatch.ReplyState != model.EngineerDispatchReplyReady || dispatch.ReplyError != engineerDispatchCallerPending {
		t.Fatalf("unbound reply = %#v, routed=%t err=%v", dispatch, routed, err)
	}
	if _, routed, err := routeSingleEngineerDispatch(t, svc, completion); err != nil || routed {
		t.Fatalf("repeat completion routed=%t err=%v", routed, err)
	}
	if messages, err := st.ListQueuedEngineerMessages(ctx, 10); err != nil || len(messages) != 0 {
		t.Fatalf("queued before caller identity = %#v, %v", messages, err)
	}

	// The caller's identity announcement delivers the stored reply to that exact session.
	if err := svc.RecordEmbeddedSessionIdentity(ctx, EmbeddedSessionActivity{
		ProjectPath: dispatchCallerPath, Source: model.SessionSourceClaudeCode, ControlSessionKey: dispatchCallerKey, SessionID: "producer-thread",
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("queued replies = %#v, %v", messages, err)
	}
	reply := messages[0]
	if reply.ProjectPath != dispatchCallerPath || reply.Provider != control.ProviderClaudeCode || reply.TargetSessionID != "producer-thread" || reply.TodoID != 0 {
		t.Fatalf("reply target = %#v", reply)
	}
	for _, want := range []string{"TODO #7: FLY-1 human pilot layer", "Worktree: " + dispatchWorkerPath, "Implemented the pilot layer; ctest passes.", "target_session_id worker-thread", "does not authorize merges"} {
		if !strings.Contains(reply.Prompt, want) {
			t.Fatalf("reply prompt missing %q:\n%s", want, reply.Prompt)
		}
	}
	dispatch, err = st.GetEngineerDispatch(ctx, dispatch.ID)
	if err != nil || dispatch.ReplyState != model.EngineerDispatchReplySent || dispatch.ReplyMessageID != reply.ID || dispatch.ReplyError != "" {
		t.Fatalf("sent dispatch = %#v, %v", dispatch, err)
	}

	// Turns the operator starts in the worker are not reported.
	if _, routed, err := routeSingleEngineerDispatch(t, svc, completion); err != nil || routed {
		t.Fatalf("unrequested turn routed=%t err=%v", routed, err)
	}

	// A follow-up from someone else does not re-arm the dispatch.
	other := createDispatchTestOperation(t, st, "lcrop_dispatch_other", "/projects/other", "claude_code", "other-key",
		control.CapabilityEngineerSendPrompt,
		map[string]any{"project_path": dispatchWorkerPath, "provider": "codex", "session_mode": "resume_or_new", "target_session_id": "worker-thread", "prompt": "Unrelated request."})
	deliverDispatchTestMessage(t, st, other.ID, "Unrelated request.")
	if dispatch, err := st.GetEngineerDispatch(ctx, dispatch.ID); err != nil || dispatch.ReplyState != model.EngineerDispatchReplySent {
		t.Fatalf("foreign follow-up re-armed dispatch: %#v, %v", dispatch, err)
	}
	otherDispatch, routed, err := routeSingleEngineerDispatch(t, svc, completion)
	if err != nil || !routed || otherDispatch.CallerSessionKey != "other-key" || otherDispatch.ReplyState != model.EngineerDispatchReplyReady {
		t.Fatalf("other caller's independent report = %#v, routed=%t err=%v", otherDispatch, routed, err)
	}

	// The caller's own follow-up re-arms it, and the next ended turn is reported at once.
	followUp := createDispatchTestOperation(t, st, "lcrop_dispatch_followup", dispatchCallerPath, "claude_code", dispatchCallerKey,
		control.CapabilityEngineerSendPrompt,
		map[string]any{"project_path": dispatchWorkerPath, "provider": "codex", "session_mode": "resume_or_new", "target_session_id": "worker-thread", "prompt": "Address the review findings."})
	deliverDispatchTestMessage(t, st, followUp.ID, "Address the review findings.")
	dispatch, err = st.GetEngineerDispatch(ctx, dispatch.ID)
	if err != nil || dispatch.ReplyState != model.EngineerDispatchReplyAwaiting || dispatch.ReplySeq != 2 {
		t.Fatalf("follow-up dispatch = %#v, %v", dispatch, err)
	}
	dispatch, routed, err = routeSingleEngineerDispatch(t, svc, EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread", Problem: "model stream disconnected",
	})
	if err != nil || !routed || dispatch.ReplyState != model.EngineerDispatchReplySent {
		t.Fatalf("follow-up reply = %#v, routed=%t err=%v", dispatch, routed, err)
	}
	second, err := st.GetEngineerMessage(ctx, dispatch.ReplyMessageID)
	if err != nil || second.OperationID != fmt.Sprintf("engineer-dispatch-reply:%d:2", dispatch.ID) || second.TargetSessionID != "producer-thread" ||
		!strings.Contains(second.Prompt, "without a final message") || !strings.Contains(second.Prompt, "model stream disconnected") {
		t.Fatalf("follow-up reply message = %#v, %v", second, err)
	}
}

func TestEngineerDispatchIgnoresHostLaunches(t *testing.T) {
	ctx := t.Context()
	svc, st := newEngineerDispatchTestService(t)
	anonymous := createDispatchTestOperation(t, st, "lcrop_dispatch_anonymous", dispatchCallerPath, "claude_code", "",
		control.CapabilityTodoCreateWorktreeAndStartEngineer,
		map[string]any{"project_path": "/projects/game", "todo_text": "Chat launch", "provider": "codex"})
	for _, operationID := range []string{"", "chat-request-1", anonymous.ID} {
		dispatch, recorded, err := svc.RecordEngineerDispatch(ctx, EngineerDispatchLaunch{
			OperationID: operationID, WorkerProjectPath: dispatchWorkerPath, WorkerProvider: model.SessionSourceCodex,
		})
		if err != nil || recorded || dispatch.ID != 0 {
			t.Fatalf("operation %q recorded dispatch %#v, recorded=%t err=%v", operationID, dispatch, recorded, err)
		}
	}
	if _, routed, err := routeSingleEngineerDispatch(t, svc, EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread", Summary: "Done.",
	}); err != nil || routed {
		t.Fatalf("host launch completion routed=%t err=%v", routed, err)
	}
}

func TestEngineerMessageToExistingSessionRecordsReturnAddress(t *testing.T) {
	providers := []control.Provider{control.ProviderCodex, control.ProviderClaudeCode, control.ProviderOpenCode, control.ProviderLCAgent}
	for _, caller := range providers {
		for _, worker := range providers {
			t.Run(string(caller)+"_to_"+string(worker), func(t *testing.T) {
				ctx := t.Context()
				svc, st := newEngineerDispatchTestService(t)
				if err := st.BindEngineerSession(ctx, dispatchCallerPath, sessionSourceFromControlProvider(caller), dispatchCallerKey, "producer-thread"); err != nil {
					t.Fatal(err)
				}
				op := createDispatchTestOperation(t, st, "lcrop_existing_worker", dispatchCallerPath, string(caller), dispatchCallerKey,
					control.CapabilityEngineerSendPrompt,
					map[string]any{"project_path": dispatchWorkerPath, "provider": worker, "session_mode": "resume_or_new", "target_session_id": "worker-thread", "prompt": "Render the next rehearsal."})
				message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
					OperationID: op.ID, ProjectPath: dispatchWorkerPath, Provider: worker,
					SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "worker-thread", Prompt: "Render the next rehearsal.",
				})
				if err != nil {
					t.Fatal(err)
				}
				completion := EngineerTurnCompletion{ProjectPath: dispatchWorkerPath, Provider: sessionSourceFromControlProvider(worker), SessionID: "worker-thread", Summary: "The rehearsal is ready."}
				if _, routed, err := routeSingleEngineerDispatch(t, svc, completion); err != nil || routed {
					t.Fatalf("undelivered request routed=%t err=%v", routed, err)
				}
				if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "worker-thread"); err != nil || !claimed {
					t.Fatalf("claim request: claimed=%t err=%v", claimed, err)
				}
				if _, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil); err != nil {
					t.Fatal(err)
				}
				for _, wrongID := range []string{"", "replacement-worker"} {
					wrong := completion
					wrong.SessionID = wrongID
					if _, routed, err := routeSingleEngineerDispatch(t, svc, wrong); err != nil || routed {
						t.Fatalf("wrong worker %q routed=%t err=%v", wrongID, routed, err)
					}
				}
				dispatch, routed, err := routeSingleEngineerDispatch(t, svc, completion)
				if err != nil || !routed || dispatch.ReplyState != model.EngineerDispatchReplySent {
					t.Fatalf("existing worker report = %#v, routed=%t err=%v", dispatch, routed, err)
				}
				reply, err := st.GetEngineerMessage(ctx, dispatch.ReplyMessageID)
				if err != nil || reply.ProjectPath != dispatchCallerPath || reply.TargetSessionID != "producer-thread" || reply.Provider != caller || !strings.Contains(reply.Prompt, "The rehearsal is ready.") {
					t.Fatalf("caller report = %#v, %v", reply, err)
				}
				// Retrying the successful receipt cannot re-arm a completed exchange.
				if _, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil); err != nil {
					t.Fatal(err)
				}
				if _, routed, err := routeSingleEngineerDispatch(t, svc, completion); err != nil || routed {
					t.Fatalf("repeated receipt/completion routed=%t err=%v", routed, err)
				}
				// Host-generated reports must not start a reply-to-reply loop.
				if _, claimed, err := st.ClaimEngineerMessage(ctx, reply.ID, "producer-thread"); err != nil || !claimed {
					t.Fatalf("claim callback: claimed=%t err=%v", claimed, err)
				}
				if _, err := st.RecordEngineerMessageState(ctx, reply.ID, control.EngineerMessageDelivered, "producer-thread", "delivered", nil); err != nil {
					t.Fatal(err)
				}
				if _, routed, err := routeSingleEngineerDispatch(t, svc, EngineerTurnCompletion{
					ProjectPath: dispatchCallerPath, Provider: sessionSourceFromControlProvider(caller), SessionID: "producer-thread", Summary: "Reviewed the rehearsal.",
				}); err != nil || routed {
					t.Fatalf("callback created another report: routed=%t err=%v", routed, err)
				}
			})
		}
	}
}

func TestEngineerMessagePreservesReportsUntilCallerIdentityArrives(t *testing.T) {
	ctx := t.Context()
	svc, st := newEngineerDispatchTestService(t)
	var dispatches []model.EngineerDispatch
	for i := range 2 {
		prompt := fmt.Sprintf("Request %d", i)
		op := createDispatchTestOperation(t, st, fmt.Sprintf("lcrop_late_identity_%d", i), dispatchCallerPath, "claude_code", dispatchCallerKey,
			control.CapabilityEngineerSendPrompt,
			map[string]any{"project_path": dispatchWorkerPath, "provider": "codex", "session_mode": "resume_or_new", "target_session_id": "worker-thread", "prompt": prompt})
		deliverDispatchTestMessage(t, st, op.ID, prompt)
		dispatch, routed, err := routeSingleEngineerDispatch(t, svc, EngineerTurnCompletion{
			ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread", Summary: fmt.Sprintf("Result %d", i),
		})
		if err != nil || !routed || dispatch.ReplyState != model.EngineerDispatchReplyReady {
			t.Fatalf("pending report = %#v, routed=%t err=%v", dispatch, routed, err)
		}
		dispatches = append(dispatches, dispatch)
	}
	if dispatches[0].ID == dispatches[1].ID {
		t.Fatal("new request overwrote the stored earlier report")
	}
	if err := svc.RecordEmbeddedSessionIdentity(ctx, EmbeddedSessionActivity{
		ProjectPath: dispatchCallerPath, Source: model.SessionSourceClaudeCode, ControlSessionKey: dispatchCallerKey, SessionID: "producer-thread",
	}); err != nil {
		t.Fatal(err)
	}
	for i, original := range dispatches {
		dispatch, err := st.GetEngineerDispatch(ctx, original.ID)
		if err != nil || dispatch.ReplyState != model.EngineerDispatchReplySent || dispatch.ReplyPrompt != original.ReplyPrompt || dispatch.CallerSessionID != "producer-thread" {
			t.Fatalf("late-bound report %d = %#v, %v", i, dispatch, err)
		}
	}
}

// Existing single-caller scenarios also assert that routing cannot silently
// create extra reports. Multi-caller fan-out is exercised by the TUI tests.
func routeSingleEngineerDispatch(t *testing.T, svc *Service, completion EngineerTurnCompletion) (model.EngineerDispatch, bool, error) {
	t.Helper()
	replies, err := svc.RouteEngineerTurnCompletion(t.Context(), completion)
	if len(replies) > 1 {
		t.Fatalf("wanted at most one report, got %#v", replies)
	}
	if len(replies) == 0 {
		return model.EngineerDispatch{}, false, err
	}
	return replies[0], true, err
}
