package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
)

func TestControlOperationLifecycleAndIdempotency(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	projectPath := t.TempDir()
	first := createTodoControlOperation(t, st, "lcrop_first", "client-1", projectPath)
	replayed := createTodoControlOperation(t, st, "lcrop_retry", "client-1", projectPath)
	if replayed.ID != first.ID {
		t.Fatalf("idempotent replay id = %q, want %q", replayed.ID, first.ID)
	}
	conflictingArgs, err := json.Marshal(control.TodoAddInput{
		RequestID:   "lcrop_conflict",
		ProjectPath: projectPath,
		Text:        "A different request",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateControlOperation(ctx, control.Operation{
		ID:              "lcrop_conflict",
		ClientRequestID: "client-1",
		Capability:      control.CapabilityTodoAdd,
		Status:          control.OperationProposed,
		Invocation: control.Invocation{
			RequestID:  "lcrop_conflict",
			Capability: control.CapabilityTodoAdd,
			Args:       conflictingArgs,
		},
		Source:     "test",
		SessionKey: "session",
	})
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("conflicting idempotent create error = %v", err)
	}

	claimed, found, err := st.ClaimNextControlOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !found || claimed.ID != first.ID || claimed.Status != control.OperationWaitingForConfirmation {
		t.Fatalf("claimed operation = %#v, found %t", claimed, found)
	}
	running, err := st.UpdateControlOperationStatus(ctx, first.ID, control.OperationRunning, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !running.Confirmed || running.StartedAt.IsZero() {
		t.Fatalf("running operation = %#v, want confirmation and start time", running)
	}
	result := json.RawMessage(`{"status":"done"}`)
	completed, err := st.UpdateControlOperationStatus(ctx, first.ID, control.OperationCompleted, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != control.OperationCompleted || !completed.Status.Terminal() || completed.CompletedAt.IsZero() {
		t.Fatalf("completed operation = %#v", completed)
	}
}

func TestRequeueWaitingControlOperations(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	created := createTodoControlOperation(t, st, "lcrop_requeue", "", t.TempDir())
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatalf("claim operation: found %t err %v", found, err)
	}
	if err := st.RequeueWaitingControlOperations(ctx); err != nil {
		t.Fatal(err)
	}
	requeued, err := st.GetControlOperation(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Status != control.OperationProposed {
		t.Fatalf("requeued status = %q, want proposed", requeued.Status)
	}
}

func createTodoControlOperation(t *testing.T, st *Store, id, clientRequestID, projectPath string) control.Operation {
	t.Helper()
	args, err := json.Marshal(control.TodoAddInput{
		RequestID:   id,
		ProjectPath: projectPath,
		Text:        "Document the control surface",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := st.CreateControlOperation(context.Background(), control.Operation{
		ID:              id,
		ClientRequestID: clientRequestID,
		Capability:      control.CapabilityTodoAdd,
		Status:          control.OperationProposed,
		Invocation: control.Invocation{
			RequestID:  id,
			Capability: control.CapabilityTodoAdd,
			Args:       args,
		},
		Source:      "test",
		SessionKey:  "session",
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
