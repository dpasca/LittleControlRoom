package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
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
