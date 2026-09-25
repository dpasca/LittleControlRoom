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

func deliverDispatchTestMessage(t *testing.T, st *store.Store, operationID, prompt string) {
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
	if _, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil); err != nil {
		t.Fatal(err)
	}
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
	if _, routed, err := svc.RouteEngineerTurnCompletion(ctx, EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "operator-thread", Summary: "Unrelated work finished.",
	}); err != nil || routed {
		t.Fatalf("other session routed=%t err=%v", routed, err)
	}

	// Until the caller's provider identity is bound, the reply waits visibly.
	completion := EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread",
		Summary: "Implemented the pilot layer; ctest passes.",
	}
	dispatch, routed, err := svc.RouteEngineerTurnCompletion(ctx, completion)
	if err != nil || !routed || dispatch.ReplyState != model.EngineerDispatchReplyReady || dispatch.ReplyError != engineerDispatchCallerPending {
		t.Fatalf("unbound reply = %#v, routed=%t err=%v", dispatch, routed, err)
	}
	if _, routed, err := svc.RouteEngineerTurnCompletion(ctx, completion); err != nil || routed {
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
	if _, routed, err := svc.RouteEngineerTurnCompletion(ctx, completion); err != nil || routed {
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

	// The caller's own follow-up re-arms it, and the next ended turn is reported at once.
	followUp := createDispatchTestOperation(t, st, "lcrop_dispatch_followup", dispatchCallerPath, "claude_code", dispatchCallerKey,
		control.CapabilityEngineerSendPrompt,
		map[string]any{"project_path": dispatchWorkerPath, "provider": "codex", "session_mode": "resume_or_new", "target_session_id": "worker-thread", "prompt": "Address the review findings."})
	deliverDispatchTestMessage(t, st, followUp.ID, "Address the review findings.")
	dispatch, err = st.GetEngineerDispatch(ctx, dispatch.ID)
	if err != nil || dispatch.ReplyState != model.EngineerDispatchReplyAwaiting || dispatch.ReplySeq != 2 {
		t.Fatalf("follow-up dispatch = %#v, %v", dispatch, err)
	}
	dispatch, routed, err = svc.RouteEngineerTurnCompletion(ctx, EngineerTurnCompletion{
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
	if _, routed, err := svc.RouteEngineerTurnCompletion(ctx, EngineerTurnCompletion{
		ProjectPath: dispatchWorkerPath, Provider: model.SessionSourceCodex, SessionID: "worker-thread", Summary: "Done.",
	}); err != nil || routed {
		t.Fatalf("host launch completion routed=%t err=%v", routed, err)
	}
}
