package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func TestExternalControlProposalEnterCannotConfirmBeforeExplicitReview(t *testing.T) {
	operationID := "lcrop_tui_safe_enter"
	args, err := json.Marshal(control.TodoAddInput{
		RequestID:   operationID,
		ProjectPath: t.TempDir(),
		Text:        "Do not confirm from an in-flight Enter key",
	})
	if err != nil {
		t.Fatal(err)
	}
	m := Model{
		externalControlConfirmation: &externalControlConfirmationState{
			operation: control.Operation{
				ID:     operationID,
				Status: control.OperationWaitingForConfirmation,
				Invocation: control.Invocation{
					RequestID:  operationID,
					Capability: control.CapabilityTodoAdd,
					Args:       args,
				},
			},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := normalizeUpdateModel(updated)
	if cmd != nil {
		t.Fatal("Enter before explicit review queued a command")
	}
	if !got.externalControlReviewWaiting() {
		t.Fatal("Enter before explicit review resolved the pending proposal")
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	got = normalizeUpdateModel(updated)
	if cmd != nil || !got.externalControlReviewActive() {
		t.Fatalf("Ctrl+G review transition = active %t, cmd %v", got.externalControlReviewActive(), cmd)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = normalizeUpdateModel(updated)
	if cmd == nil {
		t.Fatal("Enter after explicit review did not queue confirmation")
	}
	if got.externalControlConfirmation != nil {
		t.Fatal("confirmed external proposal remained pending")
	}
	if _, ok := cmd().(bossui.ControlInvocationConfirmedMsg); !ok {
		t.Fatal("confirmation command returned the wrong message type")
	}
}

func TestExternalControlProposalWaitsForExplicitReviewAndRecordsCancellation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	ctx := context.Background()
	m := New(ctx, svc)
	m.width = 120
	m.height = 40
	operationID := "lcrop_tui_cancel"
	args, err := json.Marshal(control.TodoAddInput{
		RequestID:   operationID,
		ProjectPath: t.TempDir(),
		Text:        "Confirm the external proposal path",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateControlOperation(ctx, control.Operation{
		ID:         operationID,
		Capability: control.CapabilityTodoAdd,
		Invocation: control.Invocation{
			RequestID:  operationID,
			Capability: control.CapabilityTodoAdd,
			Args:       args,
		},
		Status:      control.OperationProposed,
		Source:      "test",
		Provider:    "codex",
		SessionKey:  "session",
		ProjectPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := st.UpdateControlOperationStatus(ctx, created.ID, control.OperationWaitingForConfirmation, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.applyExternalControlProposalLoaded(externalControlProposalLoadedMsg{operation: waiting})
	got := normalizeUpdateModel(updated)
	if got.helpChatMode || got.helpChatModelActive {
		t.Fatalf("external proposal opened Help Chat: mode=%t active=%t", got.helpChatMode, got.helpChatModelActive)
	}
	if got.externalControlConfirmation == nil {
		t.Fatal("external proposal did not create pending review state")
	}
	if !got.externalControlReviewWaiting() {
		t.Fatal("external proposal should wait without taking keyboard focus")
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Agent request waiting") || !strings.Contains(rendered, "ctrl+g") {
		t.Fatalf("rendered frame does not show the pending review notice: %q", rendered)
	}
	if strings.Contains(rendered, "Confirm Control Action") || strings.Contains(rendered, "Confirm the external proposal path") {
		t.Fatalf("external proposal took focus before explicit review: %q", rendered)
	}

	updated, ordinaryCmd := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = normalizeUpdateModel(updated)
	if ordinaryCmd != nil {
		t.Fatal("Esc before explicit review should stay with the underlying surface")
	}
	if !got.externalControlReviewWaiting() {
		t.Fatal("Esc before explicit review canceled or opened the pending proposal")
	}
	stored, err := st.GetControlOperation(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != control.OperationWaitingForConfirmation {
		t.Fatalf("stored status before review = %q, want waiting_for_confirmation", stored.Status)
	}

	updated, reviewCmd := got.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	got = normalizeUpdateModel(updated)
	if reviewCmd != nil {
		t.Fatal("opening external proposal review should not run a command")
	}
	if !got.externalControlReviewActive() {
		t.Fatal("Ctrl+G did not deliberately open external proposal review")
	}
	rendered = got.View()
	if !strings.Contains(rendered, "Confirm Control Action") ||
		!strings.Contains(rendered, "Confirm the external proposal path") {
		t.Fatalf("rendered frame does not show explicitly opened confirmation: %q", rendered)
	}

	updated, cancelCmd := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = normalizeUpdateModel(updated)
	if cancelCmd == nil {
		t.Fatal("canceling external proposal did not queue operation update")
	}
	canceledRequest := cancelCmd()
	updated, recordCmd := got.Update(canceledRequest)
	got = normalizeUpdateModel(updated)
	if recordCmd == nil {
		t.Fatal("external cancellation did not queue durable status update")
	}
	recorded := recordCmd()
	updated, _ = got.Update(recorded)
	got = normalizeUpdateModel(updated)
	stored, err = st.GetControlOperation(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != control.OperationCanceled {
		t.Fatalf("stored status = %q, want canceled", stored.Status)
	}
	if got.status != "Agent control proposal canceled; no action was run" {
		t.Fatalf("status = %q, want explicit canceled receipt", got.status)
	}
}

func TestExternalControlProposalDoesNotHideAlreadyCanceledState(t *testing.T) {
	m := Model{}
	updated, cmd := m.applyExternalControlProposalLoaded(externalControlProposalLoadedMsg{
		operation: control.Operation{
			ID:     "lcrop_already_canceled",
			Status: control.OperationCanceled,
		},
	})
	got := normalizeUpdateModel(updated)
	if cmd != nil {
		t.Fatal("already canceled proposal should not open a confirmation command")
	}
	if got.helpChatMode || got.helpChatModelActive {
		t.Fatal("already canceled proposal should not open Chat")
	}
	if got.status != "Agent control proposal was already canceled; no confirmation is pending" {
		t.Fatalf("status = %q, want explicit no-pending receipt", got.status)
	}
}

func TestExternalControlProposalOverlaysVisibleEmbeddedSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	ctx := context.Background()
	projectPath := t.TempDir()
	m := New(ctx, svc)
	m.codexVisibleProject = projectPath
	m.codexSnapshots[projectPath] = codexapp.Snapshot{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderClaudeCode,
		Started:     true,
		Status:      "Claude Code session ready",
	}
	m.codexInput.Focus()
	m.width = 120
	m.height = 40

	operationID := "lcrop_tui_visible_codex"
	args, err := json.Marshal(control.TodoAddInput{
		RequestID:   operationID,
		ProjectPath: projectPath,
		Text:        "Confirm above the embedded session",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateControlOperation(ctx, control.Operation{
		ID:         operationID,
		Capability: control.CapabilityTodoAdd,
		Invocation: control.Invocation{
			RequestID:  operationID,
			Capability: control.CapabilityTodoAdd,
			Args:       args,
		},
		Status:      control.OperationProposed,
		Source:      "test",
		Provider:    "codex",
		SessionKey:  "session",
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := st.UpdateControlOperationStatus(ctx, created.ID, control.OperationWaitingForConfirmation, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := m.applyExternalControlProposalLoaded(externalControlProposalLoadedMsg{operation: waiting})
	got := normalizeUpdateModel(updated)
	if !got.codexVisible() || got.codexVisibleProject != projectPath {
		t.Fatalf("external confirmation displaced embedded session: visible=%t project=%q", got.codexVisible(), got.codexVisibleProject)
	}
	if got.codexHiddenProject != "" {
		t.Fatalf("external confirmation hid embedded project: %q", got.codexHiddenProject)
	}
	if got.helpChatMode || got.helpChatModelActive {
		t.Fatalf("external proposal opened Help Chat over embedded session: mode=%t active=%t", got.helpChatMode, got.helpChatModelActive)
	}
	if got.externalControlConfirmation == nil {
		t.Fatal("external proposal did not create pending review state")
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Agent request waiting") || strings.Contains(rendered, "Confirm Control Action") {
		t.Fatalf("pending proposal should notify without overlaying the embedded session: %q", rendered)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got = normalizeUpdateModel(updated)
	if got.codexInput.Value() != "x" {
		t.Fatalf("typed key was intercepted by delayed confirmation, composer = %q", got.codexInput.Value())
	}
	if !got.externalControlReviewWaiting() {
		t.Fatal("ordinary composer input resolved the pending external proposal")
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	got = normalizeUpdateModel(updated)
	rendered = got.View()
	if !strings.Contains(rendered, "Confirm Control Action") ||
		!strings.Contains(rendered, "Confirm above the embedded session") {
		t.Fatalf("rendered frame does not show explicitly opened external confirmation: %q", rendered)
	}
}

func TestCommitPreviewOverlaysAndReceivesInputWhileEmbeddedSessionVisible(t *testing.T) {
	projectPath := t.TempDir()
	m := New(context.Background(), newControlTestService(t))
	m.codexVisibleProject = projectPath
	m.width = 120
	m.height = 40
	m.commitPreview = &service.CommitPreview{
		ProjectPath: projectPath,
		ProjectName: "embedded-project",
		Message:     "Keep preview over Codex",
		Intent:      service.GitActionFinish,
		CanPush:     true,
	}

	rendered := m.View()
	if !strings.Contains(rendered, "Commit Preview") ||
		!strings.Contains(rendered, "Keep preview over Codex") {
		t.Fatalf("embedded frame does not show commit preview: %q", rendered)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := normalizeUpdateModel(updated)
	if got.commitPreview != nil {
		t.Fatal("Esc was routed to Codex instead of closing the commit preview")
	}
	if !got.codexVisible() || got.codexVisibleProject != projectPath {
		t.Fatalf("closing commit preview displaced embedded session: visible=%t project=%q", got.codexVisible(), got.codexVisibleProject)
	}
}

func TestProjectCollaborationApprovalUIAndAutomaticFollowup(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	origin, target := "/projects/crypto_desk", "/projects/crypto"
	create := func(id, from, to string) control.Operation {
		args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: to, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "target-session", Prompt: "Review the existing task and continue within its authorized scope."})
		op, err := st.CreateControlOperation(ctx, control.Operation{ID: id, ProjectPath: from, Provider: "codex", Source: "codex", SessionKey: "sender-session", Invocation: control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	first := create("lcrop_collab_ui", origin, target)
	claimed, found, err := st.ClaimNextControlOperation(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	m := New(ctx, service.New(config.Default(), st, events.NewBus(), nil))
	m.width = 110
	m.height = 40
	updated, _ := m.applyExternalControlProposalLoaded(externalControlProposalLoadedMsg{operation: claimed})
	m = normalizeUpdateModel(updated)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = normalizeUpdateModel(updated)
	pairs, _ := st.ListProjectCollaborations(ctx, origin)
	if len(pairs) != 0 {
		t.Fatal("A granted trust without review")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = normalizeUpdateModel(updated)
	view := m.View()
	for _, want := range []string{"Project Collaboration", "always allow this pair", origin, target, "/collab"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q from %s", want, view)
		}
	}
	updated, save := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = normalizeUpdateModel(updated)
	if save == nil || !m.externalControlConfirmation.submitting {
		t.Fatal("approval not submitting")
	}
	_, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if duplicate != nil {
		t.Fatal("repeat approval submitted twice")
	}
	updated, deliver := m.Update(save())
	m = normalizeUpdateModel(updated)
	if m.externalControlConfirmation != nil || deliver == nil {
		t.Fatal("saved approval did not release the handoff")
	}
	confirmed := deliver().(bossui.ControlInvocationConfirmedMsg)
	if !confirmed.OperationRecorded || confirmed.Invocation.RequestID != first.ID {
		t.Fatal(confirmed)
	}
	pairs, err = st.ListProjectCollaborations(ctx, origin)
	if err != nil || len(pairs) != 1 {
		t.Fatal(pairs, err)
	}
	// Automatic handoffs use the same durable mailbox and stable retry payload.
	var input control.EngineerSendPromptInput
	if err := json.Unmarshal(first.Invocation.Args, &input); err != nil {
		t.Fatal(err)
	}
	queue := m.createEngineerMessageCmd(first.Invocation, input, model.ProjectSummary{Path: target}, codexapp.ProviderCodex, input.Prompt)
	queued := queue().(engineerMessageQueuedMsg)
	replayed := queue().(engineerMessageQueuedMsg)
	if queued.err != nil || replayed.err != nil || queued.message.ID != replayed.message.ID {
		t.Fatal("collaboration mailbox retry failed", queued, replayed)
	}
	if strings.Count(queued.message.Prompt, "LCR project collaboration is approved") != 1 {
		t.Fatal("missing or repeated collaboration context", queued.message.Prompt)
	}
	reverse := create("lcrop_collab_reply", target, origin)
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	// An unrelated open dialog must remain intact while the reply proceeds.
	m.externalControlConfirmation = &externalControlConfirmationState{operation: control.Operation{ID: "unrelated"}}
	loaded := m.loadExternalControlProposalCmd(reverse.ID)()
	updated, deliver = m.Update(loaded)
	m = normalizeUpdateModel(updated)
	if deliver == nil || m.externalControlConfirmation.operation.ID != "unrelated" {
		t.Fatal("trusted reply disturbed unrelated approval")
	}
	if msg := deliver().(bossui.ControlInvocationConfirmedMsg); !msg.OperationRecorded || msg.Invocation.RequestID != reverse.ID {
		t.Fatal(msg)
	}
	// Revocation from the project screen is immediate and ignores repeated keys.
	m.externalControlConfirmation = nil
	load := m.openProjectCollaborations(origin)
	updated, _ = m.Update(load())
	m = normalizeUpdateModel(updated)
	if !strings.Contains(m.View(), target) {
		t.Fatal("trusted peer missing from management screen")
	}
	updated, revoke := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = normalizeUpdateModel(updated)
	if revoke == nil {
		t.Fatal("no revoke command")
	}
	if _, again := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); again != nil {
		t.Fatal("revoke repeated while busy")
	}
	updated, _ = m.Update(revoke())
	m = normalizeUpdateModel(updated)
	if len(m.projectCollaborationDialog.pairs) != 0 || m.projectCollaborationDialog.busy {
		t.Fatal("revocation not reflected")
	}
	if allowed, err := st.CollaborationAllowsOperation(ctx, reverse); err != nil || allowed {
		t.Fatal(allowed, err)
	}
}

func TestProjectCollaborationApprovalFailureKeepsReviewUsable(t *testing.T) {
	m := Model{externalControlConfirmation: &externalControlConfirmationState{operation: control.Operation{ID: "op"}, reviewing: true, submitting: true}}
	updated, cmd := m.applyProjectCollaborationApproved(projectCollaborationApprovedMsg{id: "op", err: fmt.Errorf("disk failure")})
	m = normalizeUpdateModel(updated)
	if cmd != nil || m.externalControlConfirmation.submitting || !strings.Contains(m.externalControlConfirmation.errorText, "disk failure") {
		t.Fatal("approval failure was hidden")
	}
}
