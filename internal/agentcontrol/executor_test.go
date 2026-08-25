package agentcontrol

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

func TestExecutorProgressiveControlProposalAndThreadOwnership(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	origin := t.TempDir()
	executor, err := NewExecutor(Options{
		Store:             st,
		OriginProjectPath: origin,
		Scope:             control.AuthorityScopePortfolio,
		Source:            "lcagent",
		Provider:          "lcagent",
		SessionKey:        "lca-thread-sender",
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := executor.List("engineer")
	if err != nil || !strings.Contains(mustJSON(t, listed), string(control.CapabilityEngineerSendPrompt)) {
		t.Fatalf("list report = %#v, err=%v", listed, err)
	}
	described, err := executor.Describe(string(control.CapabilityEngineerSendPrompt))
	if err != nil || !strings.Contains(mustJSON(t, described), `"input_schema"`) {
		t.Fatalf("describe report = %#v, err=%v", described, err)
	}
	if _, err := executor.Describe(string(control.CapabilitySettingsUpdate)); err == nil || !strings.Contains(err.Error(), "requires host scope") {
		t.Fatalf("host-scope describe error = %v", err)
	}

	arguments := json.RawMessage(`{
		"project_path":"/repos/recipient",
		"provider":"lcagent",
		"session_mode":"resume_or_new",
		"target_session_id":"lca-thread-recipient",
		"prompt":"Read docs/handoff.md and continue.",
		"reveal":false
	}`)
	proposed, err := executor.Propose(context.Background(), string(control.CapabilityEngineerSendPrompt), arguments, "handoff-1")
	if err != nil {
		t.Fatal(err)
	}
	operation, ok := proposed["operation"].(control.Operation)
	if !ok || !control.IsExternalOperationID(operation.ID) || operation.Status != control.OperationProposed {
		t.Fatalf("proposal = %#v", proposed)
	}
	if operation.Source != "lcagent" || operation.Provider != "lcagent" || operation.SessionKey != "lca-thread-sender" {
		t.Fatalf("operation ownership = %#v", operation)
	}
	replayed, err := executor.Propose(context.Background(), string(control.CapabilityEngineerSendPrompt), arguments, "handoff-1")
	if err != nil {
		t.Fatal(err)
	}
	replayedOperation, _ := replayed["operation"].(control.Operation)
	if replayedOperation.ID != operation.ID || replayed["idempotent_replay"] != true {
		t.Fatalf("idempotent replay = %#v", replayed)
	}
	conflict := json.RawMessage(strings.Replace(string(arguments), "continue.", "do something else.", 1))
	if _, err := executor.Propose(context.Background(), string(control.CapabilityEngineerSendPrompt), conflict, "handoff-1"); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("conflicting retry error = %v", err)
	}

	if _, err := executor.Get(context.Background(), operation.ID); err != nil {
		t.Fatalf("owner get error = %v", err)
	}
	other, err := NewExecutor(Options{
		Store:             st,
		OriginProjectPath: origin,
		Scope:             control.AuthorityScopePortfolio,
		Source:            "lcagent",
		Provider:          "lcagent",
		SessionKey:        "different-thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(context.Background(), operation.ID); err == nil || !strings.Contains(err.Error(), "different embedded session") {
		t.Fatalf("cross-thread get error = %v", err)
	}

	if _, err := st.UpdateControlOperationStatus(context.Background(), operation.ID, control.OperationRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	running, err := executor.Get(context.Background(), operation.ID)
	if err != nil || !strings.Contains(mustJSON(t, running), `"status":"running"`) || running["terminal"] != false {
		t.Fatalf("running report = %#v, err=%v", running, err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
