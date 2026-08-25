package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
)

func TestEngineerMessageLifecyclePersistsAndCompletesControlOperation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "engineer-messages.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	projectPath := t.TempDir()
	operation := createEngineerControlOperation(t, st, "lcrop_message", projectPath)
	if _, err := st.UpdateControlOperationStatus(ctx, operation.ID, control.OperationRunning, nil, nil); err != nil {
		t.Fatal(err)
	}

	request := control.EngineerMessage{
		OperationID: operation.ID,
		ProjectPath: projectPath,
		Provider:    control.ProviderLCAgent,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "Read the handoff and continue the exact thread.",
		TodoID:      42,
		TodoLabel:   "session handoff",
		State:       control.EngineerMessageQueued,
	}
	created, err := st.CreateEngineerMessage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.State != control.EngineerMessageQueued || created.AttemptCount != 0 {
		t.Fatalf("created message = %#v", created)
	}

	replay := request
	replay.ID = "ignored-retry-id"
	replayed, err := st.CreateEngineerMessage(ctx, replay)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("idempotent replay = %#v, err=%v", replayed, err)
	}
	conflict := request
	conflict.Prompt = "A different handoff"
	if _, err := st.CreateEngineerMessage(ctx, conflict); err == nil || !strings.Contains(err.Error(), "different engineer message") {
		t.Fatalf("conflicting operation binding error = %v", err)
	}

	claimed, ok, err := st.ClaimEngineerMessage(ctx, created.ID, "lca-thread-1")
	if err != nil || !ok {
		t.Fatalf("first claim = %#v, ok=%t err=%v", claimed, ok, err)
	}
	if claimed.State != control.EngineerMessageDelivering || claimed.TargetSessionID != "lca-thread-1" || claimed.AttemptCount != 1 {
		t.Fatalf("first claimed message = %#v", claimed)
	}
	if err := st.RequeueDeliveringEngineerMessages(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := st.GetEngineerMessage(ctx, created.ID)
	if err != nil || queued.State != control.EngineerMessageQueued || !strings.Contains(queued.LastError, "LCR restart") {
		t.Fatalf("recovered message = %#v, err=%v", queued, err)
	}
	operation, err = st.GetControlOperation(ctx, operation.ID)
	if err != nil || operation.Status != control.OperationRunning {
		t.Fatalf("operation while delivery is queued = %#v, err=%v", operation, err)
	}
	claimed, ok, err = st.ClaimEngineerMessage(ctx, created.ID, "different-thread")
	if err != nil || !ok || claimed.AttemptCount != 2 || claimed.TargetSessionID != "lca-thread-1" {
		t.Fatalf("second claim = %#v, ok=%t err=%v", claimed, ok, err)
	}

	delivered, err := st.RecordEngineerMessageState(
		ctx,
		created.ID,
		control.EngineerMessageDelivered,
		"lca-thread-1",
		"Message delivered to the LCAgent engineer.",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.State != control.EngineerMessageDelivered || delivered.DeliveredAt.IsZero() || delivered.LastError != "" {
		t.Fatalf("delivered message = %#v", delivered)
	}
	operation, err = st.GetControlOperation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != control.OperationCompleted || operation.CompletedAt.IsZero() ||
		!strings.Contains(string(operation.Result), `"state":"delivered"`) ||
		!strings.Contains(string(operation.Result), created.ID) {
		t.Fatalf("completed operation = %#v", operation)
	}
	terminalReplay, err := st.CreateEngineerMessage(ctx, request)
	if err != nil || terminalReplay.ID != created.ID || terminalReplay.State != control.EngineerMessageDelivered {
		t.Fatalf("terminal idempotent replay = %#v, err=%v", terminalReplay, err)
	}
	queuedMessages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(queuedMessages) != 0 {
		t.Fatalf("queued messages after delivery = %#v, err=%v", queuedMessages, err)
	}
}

func TestEngineerMessageClaimRecoversAcrossStoreRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "engineer-message-restart.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		ProjectPath:     t.TempDir(),
		Provider:        control.ProviderOpenCode,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: "opencode-target",
		Prompt:          "Continue after LCR restarts.",
		State:           control.EngineerMessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := st.ClaimEngineerMessage(context.Background(), created.ID, ""); err != nil || !claimed {
		t.Fatalf("claim before restart: claimed=%t err=%v", claimed, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RequeueDeliveringEngineerMessages(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.GetEngineerMessage(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != control.EngineerMessageQueued || recovered.TargetSessionID != "opencode-target" || recovered.AttemptCount != 1 {
		t.Fatalf("recovered message = %#v", recovered)
	}
}

func TestEngineerMessageFailureIsTerminal(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "engineer-message-failure.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	created, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
		ProjectPath:     t.TempDir(),
		Provider:        control.ProviderClaudeCode,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: "claude-exact",
		Prompt:          "Continue the handoff.",
		State:           control.EngineerMessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, created.ID, ""); err != nil || !claimed {
		t.Fatalf("claim failed message: claimed=%t err=%v", claimed, err)
	}
	wantErr := errors.New("target session changed")
	failed, err := st.RecordEngineerMessageState(ctx, created.ID, control.EngineerMessageFailed, "", "Delivery failed", wantErr)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != control.EngineerMessageFailed || failed.LastError != wantErr.Error() {
		t.Fatalf("failed message = %#v", failed)
	}
	afterTerminal, err := st.RecordEngineerMessageState(ctx, created.ID, control.EngineerMessageQueued, "", "retry", nil)
	if err != nil || afterTerminal.State != control.EngineerMessageFailed {
		t.Fatalf("terminal message changed = %#v, err=%v", afterTerminal, err)
	}
}

func TestListQueuedEngineerMessagesAfterPagesPastWaitingBatch(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "engineer-message-pages.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	createdAt := time.Unix(1_800_000_000, 0)
	for i := 0; i < 35; i++ {
		_, err := st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
			ID:          fmt.Sprintf("lcrmsg_page_%02d", i),
			ProjectPath: "/tmp/page-target",
			Provider:    control.ProviderCodex,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      fmt.Sprintf("Message %d", i),
			State:       control.EngineerMessageQueued,
			CreatedAt:   createdAt,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := st.ListQueuedEngineerMessages(context.Background(), 32)
	if err != nil || len(first) != 32 {
		t.Fatalf("first page len=%d err=%v", len(first), err)
	}
	second, err := st.ListQueuedEngineerMessagesAfter(context.Background(), first[31].CreatedAt, first[31].ID, 32)
	if err != nil || len(second) != 3 {
		t.Fatalf("second page = %#v, err=%v", second, err)
	}
	if second[0].ID != "lcrmsg_page_32" || second[2].ID != "lcrmsg_page_34" {
		t.Fatalf("second page ids = %s ... %s", second[0].ID, second[2].ID)
	}
}

func createEngineerControlOperation(t *testing.T, st *Store, id, projectPath string) control.Operation {
	t.Helper()
	args, err := json.Marshal(control.EngineerSendPromptInput{
		RequestID:   id,
		ProjectPath: projectPath,
		Provider:    control.ProviderLCAgent,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "Read the handoff and continue the exact thread.",
		TodoID:      42,
		TodoLabel:   "session handoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := st.CreateControlOperation(context.Background(), control.Operation{
		ID:         id,
		Capability: control.CapabilityEngineerSendPrompt,
		Status:     control.OperationProposed,
		Invocation: control.Invocation{
			RequestID:  id,
			Capability: control.CapabilityEngineerSendPrompt,
			Args:       args,
		},
		Source:      "lcagent",
		Provider:    "lcagent",
		SessionKey:  "lca-thread-sender",
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
