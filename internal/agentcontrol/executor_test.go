package agentcontrol

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
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

func TestApprovedCollaborationReportDoesNotAskAgain(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	origin, target := t.TempDir(), t.TempDir()
	executor, err := NewExecutor(Options{Store: st, OriginProjectPath: origin, Scope: control.AuthorityScopePortfolio, Source: "codex", Provider: "codex", SessionKey: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: target, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "target", Prompt: "Review and continue the authorized task"})
	first, err := executor.Propose(ctx, string(control.CapabilityEngineerSendPrompt), args, "first")
	if err != nil || first["requires_new_user_turn"] != true {
		t.Fatal(first, err)
	}
	op := first["operation"].(control.Operation)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveProjectCollaboration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	next, err := executor.Propose(ctx, string(control.CapabilityEngineerSendPrompt), args, "next")
	if err != nil || next["automatic_delivery"] != true || next["requires_new_user_turn"] != false || next["operator_confirmation"] != false {
		t.Fatal(next, err)
	}
	nextOp := next["operation"].(control.Operation)
	if nextOp.Confirmed || nextOp.Status != control.OperationProposed {
		t.Fatal("proposal falsely claimed delivery", nextOp)
	}
	pairs, _ := st.ListProjectCollaborations(ctx, origin)
	if err := st.RevokeProjectCollaboration(ctx, pairs[0]); err != nil {
		t.Fatal(err)
	}
	report, err := executor.Get(ctx, nextOp.ID)
	if err != nil || report["requires_new_user_turn"] != true {
		t.Fatal("revoked proposal does not ask", report, err)
	}
}

func TestReplacedSessionCannotProposeControls(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	organizer, worker := t.TempDir(), t.TempDir()
	if _, err := st.RecordEngineerSessionSuccession(ctx, store.EngineerSessionSuccession{
		ProjectPath: organizer, Provider: model.SessionSourceCodex, SessionID: "old-thread", SessionKey: "old-key",
		SuccessorProvider: model.SessionSourceCodex, SuccessorSessionKey: "new-key", SuccessorSessionID: "new-thread",
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: worker, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "worker", Prompt: "Start the overnight queue"})
	for _, tc := range []struct {
		key     string
		refused bool
	}{{"old-key", true}, {"new-key", false}} {
		executor, err := NewExecutor(Options{Store: st, OriginProjectPath: organizer, Scope: control.AuthorityScopePortfolio, Source: "codex", Provider: "codex", SessionKey: tc.key})
		if err != nil {
			t.Fatal(err)
		}
		_, err = executor.Propose(ctx, string(control.CapabilityEngineerSendPrompt), args, tc.key)
		if refused := err != nil && strings.Contains(err.Error(), "replaced through /handoff by new-thread"); refused != tc.refused {
			t.Fatalf("key %s: refused=%t err=%v", tc.key, refused, err)
		}
	}
}

func TestWorkerSessionPermissionReportDoesNotAskAgain(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, origin := t.Context(), t.TempDir()
	executor, err := NewExecutor(Options{Store: st, OriginProjectPath: origin, Scope: control.AuthorityScopePortfolio,
		Source: "claude_code", Provider: "claude_code", SessionKey: "organizer"})
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"title":"Worker","prompt":"Implement the task"}`)
	first, err := executor.Propose(ctx, string(control.CapabilityAgentTaskCreate), args, "first")
	if err != nil || first["requires_new_user_turn"] != true {
		t.Fatal(first, err)
	}
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveWorkerDelegation(ctx, first["operation"].(control.Operation).ID); err != nil {
		t.Fatal(err)
	}
	next, err := executor.Propose(ctx, string(control.CapabilityAgentTaskCreate), json.RawMessage(`{"title":"Another worker","prompt":"Implement another task","repository_write":true}`), "next")
	if err != nil || next["automatic_delivery"] != true || next["requires_new_user_turn"] != false || next["operator_confirmation"] != false {
		t.Fatal(next, err)
	}
	op := next["operation"].(control.Operation)
	if op.Confirmed || op.Status != control.OperationProposed {
		t.Fatal("proposal falsely claimed execution", op)
	}
	permissions, _ := st.ListControlPermissions(ctx, origin)
	if err := st.RevokeControlPermission(ctx, origin, permissions[0].ID); err != nil {
		t.Fatal(err)
	}
	report, err := executor.Get(ctx, op.ID)
	if err != nil || report["requires_new_user_turn"] != true || report["automatic_delivery"] == true {
		t.Fatal("revoked worker proposal does not ask", report, err)
	}
}
