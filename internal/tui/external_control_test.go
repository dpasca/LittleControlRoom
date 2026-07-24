package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func TestExternalControlProposalOpensTUIConfirmationAndRecordsCancellation(t *testing.T) {
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
		t.Fatal("external proposal did not open the TUI-owned confirmation")
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Confirm Control Action") ||
		!strings.Contains(rendered, "Confirm the external proposal path") {
		t.Fatalf("rendered frame does not show TUI confirmation: %q", rendered)
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
	stored, err := st.GetControlOperation(ctx, operationID)
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
		t.Fatal("external proposal did not create TUI-owned confirmation")
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Confirm Control Action") ||
		!strings.Contains(rendered, "Confirm above the embedded session") {
		t.Fatalf("rendered frame does not show external confirmation: %q", rendered)
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
