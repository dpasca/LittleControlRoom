package agentcontrol

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

func TestCancelWakeupOnlyBySchedulingSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wakeup-cancel.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	project := t.TempDir()

	scheduler, err := NewExecutor(Options{Store: st, OriginProjectPath: project, Scope: control.AuthorityScopeProject, Source: "codex", Provider: "codex", SessionKey: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.EngineerSendPromptInput{
		ProjectPath: project, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew,
		TargetSessionID: "session-1", Prompt: "Verify the repost.", DeliverAt: time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	report, err := scheduler.Propose(ctx, string(control.CapabilityEngineerSendPrompt), args, "schedule")
	if err != nil {
		t.Fatal(err)
	}
	operationID := proposedOperationID(t, report)
	deliverAt := time.Now().Add(time.Hour)
	message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
		OperationID: operationID, ProjectPath: project, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew,
		TargetSessionID: "session-1", Prompt: "Verify the repost.", DeliverAt: deliverAt, ExpiresAt: deliverAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelArgs := json.RawMessage(`{"wakeup_id":"` + message.ID + `"}`)

	other, err := NewExecutor(Options{Store: st, OriginProjectPath: project, Scope: control.AuthorityScopeProject, Source: "codex", Provider: "codex", SessionKey: "someone-else"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Propose(ctx, string(control.CapabilityEngineerCancelWakeup), cancelArgs, "cancel-other"); err == nil || !strings.Contains(err.Error(), "/wakeups") {
		t.Fatalf("another session canceled the wakeup: err=%v", err)
	}
	result, err := scheduler.Propose(ctx, string(control.CapabilityEngineerCancelWakeup), cancelArgs, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if result["operator_confirmation"] != false || result["state"] != control.EngineerMessageFailed {
		t.Fatalf("unexpected cancel receipt: %+v", result)
	}
	if _, err := scheduler.Propose(ctx, string(control.CapabilityEngineerCancelWakeup), cancelArgs, "cancel-again"); err == nil {
		t.Fatal("canceled a wakeup twice")
	}
}

func proposedOperationID(t *testing.T, report map[string]any) string {
	t.Helper()
	encoded, _ := json.Marshal(report)
	var decoded struct {
		Operation struct {
			ID string `json:"id"`
		} `json:"operation"`
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(encoded, &decoded)
	if id := firstNonEmpty(decoded.Operation.ID, decoded.OperationID); id != "" {
		return id
	}
	t.Fatalf("no operation id in report: %s", encoded)
	return ""
}
