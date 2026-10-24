package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func openSuccessionStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "successions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func queueExactMessage(t *testing.T, st *Store, operationID, projectPath string, provider control.Provider, target string) control.EngineerMessage {
	t.Helper()
	message, err := st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		OperationID:     operationID,
		ProjectPath:     projectPath,
		Provider:        provider,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: target,
		Prompt:          "Overnight phone queue finished.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func queuedIDs(t *testing.T, st *Store) map[string]control.EngineerMessage {
	t.Helper()
	messages, err := st.ListQueuedEngineerMessages(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]control.EngineerMessage, len(messages))
	for _, message := range messages {
		out[message.ID] = message
	}
	return out
}

func TestHandoffSuccessionForwardsQueuedMessagesOnceSuccessorAnnouncesIdentity(t *testing.T) {
	st := openSuccessionStore(t)
	ctx := context.Background()
	organizer := filepath.Clean(t.TempDir())
	early := queueExactMessage(t, st, "dispatch-reply:1", organizer, control.ProviderClaudeCode, "old-organizer")

	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: organizer, Provider: model.SessionSourceClaudeCode, SessionID: "old-organizer", SessionKey: "old-key",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch-key", Reason: "handoff",
	}); err != nil {
		t.Fatal(err)
	}
	// While the successor is starting, nothing addressed to the old session
	// may be delivered: resuming it is exactly what the handoff replaced.
	late := queueExactMessage(t, st, "dispatch-reply:2", organizer, control.ProviderClaudeCode, "old-organizer")
	if queued := queuedIDs(t, st); len(queued) != 0 {
		t.Fatalf("pending succession delivered messages to the replaced session: %#v", queued)
	}
	if held, _ := st.GetEngineerMessage(ctx, early.ID); !strings.Contains(held.LastError, "replaced old-organizer") {
		t.Fatalf("held message status = %q", held.LastError)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, late.ID, "old-organizer"); err != nil || claimed {
		t.Fatalf("claim to replaced session: claimed=%t err=%v", claimed, err)
	}

	completed, err := st.CompleteEngineerSessionSuccessions(ctx, organizer, model.SessionSourceCodex, "launch-key", "new-organizer")
	if err != nil || len(completed) != 1 {
		t.Fatalf("complete: %#v err=%v", completed, err)
	}
	queued := queuedIDs(t, st)
	for _, id := range []string{early.ID, late.ID} {
		message, ok := queued[id]
		if !ok {
			t.Fatalf("message %s was not released after completion: %#v", id, queued)
		}
		if message.TargetSessionID != "new-organizer" || message.Provider != control.ProviderCodex || message.RequestedTargetSessionID != "old-organizer" {
			t.Fatalf("forwarded message = %#v", message)
		}
	}

	// Later reports to the old ID, including exact retries, resolve on creation.
	again := queueExactMessage(t, st, "dispatch-reply:2", organizer, control.ProviderClaudeCode, "old-organizer")
	if again.ID != late.ID {
		t.Fatalf("idempotent retry created %s, want %s", again.ID, late.ID)
	}
	fresh := queueExactMessage(t, st, "dispatch-reply:3", organizer, control.ProviderClaudeCode, "old-organizer")
	if fresh.TargetSessionID != "new-organizer" || fresh.Provider != control.ProviderCodex || !strings.Contains(fresh.LastError, "Forwarded from session old-organizer") {
		t.Fatalf("new message to replaced session = %#v", fresh)
	}
}

func TestHandoffSuccessionChainsAndRefusesRewrite(t *testing.T) {
	st := openSuccessionStore(t)
	ctx := context.Background()
	path := filepath.Clean(t.TempDir())
	for _, step := range []struct{ from, to string }{{"a", "b"}, {"b", "c"}} {
		if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
			ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: step.from,
			SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "key-" + step.to, SuccessorSessionID: step.to,
		}); err != nil {
			t.Fatal(err)
		}
	}
	current, pending, err := st.ResolveEngineerSessionSuccessor(ctx, EngineerSessionRef{ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "a"})
	if err != nil || pending || current.SessionID != "c" {
		t.Fatalf("resolve a = %#v pending=%t err=%v", current, pending, err)
	}
	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "a",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "key-d",
	}); err == nil {
		t.Fatal("rewrote a completed succession")
	}
	// Completing a succession into an earlier session reinstates it, so the
	// stored graph never contains a cycle.
	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "c",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "key-a", SuccessorSessionID: "a",
	}); err != nil {
		t.Fatal(err)
	}
	current, _, err = st.ResolveEngineerSessionSuccessor(ctx, EngineerSessionRef{ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "b"})
	if err != nil || current.SessionID != "a" {
		t.Fatalf("resolve b after reinstating a = %#v err=%v", current, err)
	}
}

func TestCancelledHandoffReinstatesPredecessor(t *testing.T) {
	st := openSuccessionStore(t)
	ctx := context.Background()
	path := filepath.Clean(t.TempDir())
	message := queueExactMessage(t, st, "dispatch-reply:1", path, control.ProviderCodex, "old")
	predecessor := EngineerSessionRef{ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "old"}
	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "old",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelPendingEngineerSessionSuccession(ctx, predecessor, "launch"); err != nil {
		t.Fatal(err)
	}
	queued := queuedIDs(t, st)
	if got, ok := queued[message.ID]; !ok || got.TargetSessionID != "old" || got.LastError != "" {
		t.Fatalf("message after cancel = %#v ok=%t", got, ok)
	}
	if _, replaced, err := st.EngineerSessionSupersededByKey(ctx, path, model.SessionSourceCodex, "old"); err != nil || replaced {
		t.Fatalf("cancelled predecessor still replaced=%t err=%v", replaced, err)
	}
}

func TestHandoffSuccessionMovesTaskCallerAuthorityAndDispatchReports(t *testing.T) {
	st := openSuccessionStore(t)
	ctx := context.Background()
	organizer := filepath.Clean(t.TempDir())
	provider := model.SessionSourceClaudeCode
	if err := st.BindEngineerSession(ctx, organizer, provider, "old-key", "old-organizer"); err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID: "agt_owned", Title: "perf follow-up", Provider: model.SessionSourceCodex, WorkspacePath: "/worker",
		OriginProjectPath: organizer, OriginProvider: provider, OriginSessionID: "old-organizer", OriginSessionKey: "old-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	pendingCaller, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID: "agt_pending_caller", Title: "caller identity not yet observed", Provider: model.SessionSourceCodex, WorkspacePath: "/worker2",
		OriginProjectPath: organizer, OriginProvider: provider, OriginSessionKey: "old-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateEngineerDispatch(ctx, model.EngineerDispatch{
		OriginOperationID: "op-perf", WorkerProjectPath: "/perf", WorkerProvider: provider,
		CallerProjectPath: organizer, CallerProvider: provider, CallerSessionKey: "old-key", CallerSessionID: "old-organizer",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: organizer, Provider: provider, SessionID: "old-organizer", SessionKey: "old-key",
		SuccessorProvider: provider, SuccessorSessionKey: "new-key", SuccessorSessionID: "new-organizer",
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{task.ID, pendingCaller.ID} {
		moved, err := st.GetAgentTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if moved.OriginSessionID != "new-organizer" || moved.OriginSessionKey != "new-key" {
			t.Fatalf("task %s caller = %q/%q", id, moved.OriginSessionID, moved.OriginSessionKey)
		}
		if !st.taskCallerAuthorized(ctx, moved, model.AgentTaskActor{ProjectPath: organizer, Provider: provider, SessionKey: "new-key"}) {
			t.Fatalf("successor cannot review task %s", id)
		}
		if st.taskCallerAuthorized(ctx, moved, model.AgentTaskActor{ProjectPath: organizer, Provider: provider, SessionKey: "old-key"}) {
			t.Fatalf("replaced session can still review task %s", id)
		}
	}
	awaiting, err := st.ListAwaitingEngineerDispatches(ctx, "/perf", provider, "")
	if err != nil || len(awaiting) != 1 || awaiting[0].CallerSessionID != "new-organizer" || awaiting[0].CallerSessionKey != "new-key" {
		t.Fatalf("awaiting dispatch = %#v err=%v", awaiting, err)
	}

	if _, replaced, err := st.EngineerSessionSupersededByKey(ctx, organizer, provider, "old-key"); err != nil || !replaced {
		t.Fatalf("old control key replaced=%t err=%v", replaced, err)
	}
	if _, replaced, err := st.EngineerSessionSupersededByKey(ctx, organizer, provider, "new-key"); err != nil || replaced {
		t.Fatalf("successor control key replaced=%t err=%v", replaced, err)
	}
}

func TestPossiblySubmittedMessageIsNotForwarded(t *testing.T) {
	st := openSuccessionStore(t)
	ctx := context.Background()
	path := filepath.Clean(t.TempDir())
	message := queueExactMessage(t, st, "dispatch-reply:1", path, control.ProviderCodex, "old")
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "old"); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	if err := st.RequeueDeliveringEngineerMessages(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordEngineerSessionSuccession(ctx, EngineerSessionSuccession{
		ProjectPath: path, Provider: model.SessionSourceCodex, SessionID: "old",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "launch", SuccessorSessionID: "new",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEngineerMessage(ctx, message.ID)
	if err != nil || got.TargetSessionID != "old" || !got.SubmissionUncertain {
		t.Fatalf("uncertain message = %#v err=%v", got, err)
	}
}
