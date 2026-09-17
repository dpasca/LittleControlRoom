package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
)

func createCollaborationMessage(t *testing.T, st *Store, id, from, to string) control.Operation {
	t.Helper()
	args, err := json.Marshal(control.EngineerSendPromptInput{ProjectPath: to, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "target", Prompt: "Continue the authorized task"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateControlOperation(context.Background(), control.Operation{ID: id, Source: "codex", Provider: "codex", SessionKey: "sender", ProjectPath: from, Invocation: control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestProjectCollaborationPersistsBothDirectionsAndBypassesUnrelatedApproval(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	a, b := t.TempDir(), t.TempDir()
	first := createCollaborationMessage(t, st, "lcrop_first", a, b)
	if allowed, err := st.CollaborationAllowsOperation(ctx, first); err != nil || allowed {
		t.Fatalf("unapproved = %v, %v", allowed, err)
	}
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	approved, err := st.ApproveProjectCollaboration(ctx, first.ID)
	if err != nil || approved.ConfirmationBy != control.ConfirmationProjectCollaboration {
		t.Fatalf("approval = %+v, %v", approved, err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Leave an unrelated request waiting indefinitely.
	manual := createTodoControlOperation(t, st, "lcrop_manual", "", a)
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	createCollaborationMessage(t, st, "lcrop_other", a, t.TempDir())
	reverse := createCollaborationMessage(t, st, "lcrop_reverse", b, a)
	if allowed, err := st.CollaborationAllowsOperation(ctx, reverse); err != nil || !allowed {
		t.Fatalf("reverse after reopen = %v, %v", allowed, err)
	}
	claimed, found, err := st.ClaimNextControlOperation(ctx)
	if err != nil || !found || claimed.ID != reverse.ID {
		t.Fatalf("claim through manual wait = %+v, %v, %v", claimed, found, err)
	}
	running, automatic, err := st.ConfirmProjectCollaboration(ctx, claimed.ID)
	if err != nil || !automatic || running.Status != control.OperationRunning || !running.Confirmed {
		t.Fatalf("automatic = %+v, %v, %v", running, automatic, err)
	}
	if _, again, err := st.ConfirmProjectCollaboration(ctx, claimed.ID); err != nil || again {
		t.Fatalf("duplicate delivery = %v, %v", again, err)
	}
	preserved, err := st.UpdateControlOperationStatus(ctx, claimed.ID, control.OperationRunning, nil, nil)
	if err != nil || preserved.ConfirmationBy != control.ConfirmationProjectCollaboration {
		t.Fatal("lost audit attribution", preserved, err)
	}
	waiting, _ := st.GetControlOperation(ctx, manual.ID)
	if waiting.Status != control.OperationWaitingForConfirmation {
		t.Fatal("unrelated request was approved", waiting)
	}
	pairs, err := st.ListProjectCollaborations(ctx, b)
	if err != nil || len(pairs) != 1 {
		t.Fatal(pairs, err)
	}
	// Revocation between claim and dispatch must fall back to confirmation.
	next := createCollaborationMessage(t, st, "lcrop_next", a, b)
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	if err := st.RevokeProjectCollaboration(ctx, pairs[0]); err != nil {
		t.Fatal(err)
	}
	op, automatic, err := st.ConfirmProjectCollaboration(ctx, next.ID)
	if err != nil || automatic || op.Status != control.OperationWaitingForConfirmation {
		t.Fatal("revocation did not stop dispatch", op, automatic, err)
	}
	if allowed, err := st.CollaborationAllowsOperation(ctx, reverse); err != nil || allowed {
		t.Fatal("revocation did not affect reverse direction", allowed, err)
	}
}

func TestProjectCollaborationCannotBeGrantedFromCanceledRequest(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	op := createCollaborationMessage(t, st, "lcrop_canceled", t.TempDir(), t.TempDir())
	if _, err := st.UpdateControlOperationStatus(ctx, op.ID, control.OperationCanceled, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveProjectCollaboration(ctx, op.ID); err == nil {
		t.Fatal("canceled request granted trust")
	}
	pairs, err := st.ListProjectCollaborations(ctx, op.ProjectPath)
	if err != nil || len(pairs) != 0 {
		t.Fatal(pairs, err)
	}
}
