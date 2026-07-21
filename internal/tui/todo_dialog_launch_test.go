package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedModelPreferencePersistsAcrossFutureSessionsPerProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
	})
	m = updated.(Model)

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(codex) should return an open command")
	}
	msg := cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("codex open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	updated, _ = m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to openai/gpt-5.4 with medium reasoning for the next prompt",
		provider:    codexapp.ProviderOpenCode,
		model:       "openai/gpt-5.4",
		reasoning:   "medium",
	})
	m = updated.(Model)

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(opencode) should return an open command")
	}
	msg = cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("opencode open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	updated, _ = m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to sonnet with max reasoning for the next prompt",
		provider:    codexapp.ProviderClaudeCode,
		model:       "sonnet",
		reasoning:   "max",
	})
	m = updated.(Model)

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(claude) should return an open command")
	}
	msg = cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("claude open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderCodex || requests[0].PendingModel != "gpt-5.4" || requests[0].PendingReasoning != "high" {
		t.Fatalf("codex request = %#v, want persisted Codex model preference", requests[0])
	}
	if requests[1].Provider != codexapp.ProviderOpenCode || requests[1].PendingModel != "openai/gpt-5.4" || requests[1].PendingReasoning != "medium" {
		t.Fatalf("opencode request = %#v, want persisted OpenCode model preference", requests[1])
	}
	if requests[2].Provider != codexapp.ProviderClaudeCode || requests[2].PendingModel != "sonnet" || requests[2].PendingReasoning != "max" {
		t.Fatalf("claude request = %#v, want persisted Claude model preference", requests[2])
	}
}

func TestTodoDialogEnterStartsFreshPreferredProviderWithDraft(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-todo",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	todoText := "Change font color to red when there's an error\n\nStack:\n- check logger context\n"
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        todoText,
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoDialog == nil {
		t.Fatalf("todo dialog should still be open after Enter (shows copy dialog)")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should close when starting the selected TODO")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}
	if got.codexDrafts["/tmp/demo"].Text != todoText {
		t.Fatalf("draft text = %q, want selected TODO text", got.codexDrafts["/tmp/demo"].Text)
	}
	if cmd == nil {
		t.Fatalf("starting a TODO should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode || !requests[0].ForceNew || strings.TrimSpace(requests[0].Prompt) != "" {
		t.Fatalf("launch request = %#v, want fresh OpenCode launch without auto-sent prompt", requests[0])
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexInput.Value() != todoText {
		t.Fatalf("composer text = %q, want selected TODO draft", got.codexInput.Value())
	}
	if got.status != "Fresh OpenCode session ready with TODO draft. Edit and press Enter to send." {
		t.Fatalf("status = %q, want todo draft ready status", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening the embedded session should return a focus command")
	}
}

func TestTodoDialogStartsFreshSessionWithImageDraft(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-todo-image",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	todoText := "Match the UI state shown in the screenshot"
	imagePath := "/tmp/todo-reference.png"
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          8,
				ProjectPath: "/tmp/demo",
				Text:        todoText,
				Attachments: []model.TodoAttachment{{
					Kind: model.TodoAttachmentLocalImage,
					Path: imagePath,
				}},
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil || got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("todo copy dialog = %#v, want dedicated worktree by default", got.todoCopyDialog)
	}
	if rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24)); !strings.Contains(rendered, "1 image") {
		t.Fatalf("copy dialog should show image summary:\n%s", rendered)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("starting a TODO with an image should return an open command")
	}
	draft := got.codexDrafts["/tmp/demo"]
	if draft.Text != todoText+"\n\n[Image #1] " {
		t.Fatalf("draft text = %q, want image marker after TODO text", draft.Text)
	}
	if len(draft.Attachments) != 1 || draft.Attachments[0].Path != imagePath {
		t.Fatalf("draft attachments = %#v, want image path", draft.Attachments)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 || requests[0].Prompt != "" || !requests[0].InitialInput.Empty() {
		t.Fatalf("launch request = %#v, want draft-only fresh session", requests)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexInput.Value() != todoText+"\n\n[Image #1] " {
		t.Fatalf("composer text = %q, want TODO draft with image marker", got.codexInput.Value())
	}
	if got.currentCodexDraft().Submission().Text != todoText {
		t.Fatalf("submission text = %q, want TODO text without image marker", got.currentCodexDraft().Submission().Text)
	}
	if len(got.currentCodexDraft().Submission().Attachments) != 1 {
		t.Fatalf("submission attachments = %#v, want one image", got.currentCodexDraft().Submission().Attachments)
	}
}

func TestTodoDialogBlocksImageAttachmentsForUnsupportedProvider(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          9,
				ProjectPath: "/tmp/demo",
				Text:        "Use the attached screenshot",
				Attachments: []model.TodoAttachment{{
					Kind: model.TodoAttachmentLocalImage,
					Path: "/tmp/reference.png",
				}},
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open")
	}
	got.todoCopyDialog.Provider = codexapp.ProviderClaudeCode
	got.todoCopyDialog.RunMode = todoCopyModeHere
	rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "no images") || !strings.Contains(rendered, "does not support TODO image attachments") {
		t.Fatalf("copy dialog should warn about unsupported image provider:\n%s", rendered)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("unsupported image launch should not return a command")
	}
	if got.status != todoAttachmentUnsupportedStatus(codexapp.ProviderClaudeCode) {
		t.Fatalf("status = %q, want unsupported-provider message", got.status)
	}
}

func TestTodoDialogHereBlocksFreshSessionWhenProviderLaneBusy(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:     req.Provider.Normalized(),
				ThreadID:     firstNonEmptyTrimmed(req.ResumeID, "ses-busy"),
				Started:      true,
				Busy:         true,
				ActiveTurnID: "turn-busy",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-busy",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Do not replace the active engineer turn",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("busy engineer lane should block fresh TODO launch")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should stay open after the blocked launch")
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the seeded busy session", len(requests))
	}
	if got.status != "TODO #7 launch blocked" {
		t.Fatalf("status = %q, want concise blocked-launch status", got.status)
	}
	if got.attentionDialog == nil {
		t.Fatalf("busy engineer lane should show a blocking attention dialog")
	}
	if got.attentionDialog.Title != "TODO launch blocked" {
		t.Fatalf("attention dialog title = %q, want TODO launch blocked", got.attentionDialog.Title)
	}
	rendered := ansi.Strip(got.renderAttentionDialogContent(72))
	for _, want := range []string{
		"embedded OpenCode engineer session is already running",
		"TODO #7",
		"Choose Dedicated worktree",
		"OK",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("attention dialog missing %q:\n%s", want, rendered)
		}
	}
	if severity := topStatusSeverityForMessage(got.status, nil); severity != topStatusSeverityWarning {
		t.Fatalf("top status severity = %d, want warning", severity)
	}
}

func TestTodoDialogHereShowsModalWhenProviderLaneIsIdleButOpen(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "thread-open",
				Started:  true,
				Phase:    codexapp.SessionPhaseIdle,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		ResumeID:    "thread-open",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "codex",
		}},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			TodoID:      792,
			RunMode:     todoCopyModeHere,
			Provider:    codexapp.ProviderCodex,
		},
	}

	updated, cmd := m.startTodoInProjectPath(
		"/tmp/demo",
		792,
		"Do not replace an open Codex session",
		nil,
		codexapp.ProviderCodex,
		false,
	)
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("open engineer lane should block fresh TODO launch")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("TODO launcher should remain open beneath the warning")
	}
	if got.status != "TODO #792 launch blocked" {
		t.Fatalf("status = %q, want concise blocked-launch status", got.status)
	}
	if got.attentionDialog == nil {
		t.Fatalf("open engineer lane should show a blocking attention dialog")
	}
	for _, want := range []string{
		"embedded Codex engineer session is still open for TODO #792",
		"An idle turn does not show that its task is finished",
		"dedicated worktree",
	} {
		if !strings.Contains(got.attentionDialog.Message, want) {
			t.Fatalf("attention dialog message missing %q: %q", want, got.attentionDialog.Message)
		}
	}
	rendered := ansi.Strip(got.renderAttentionDialogContent(72))
	for _, want := range []string{
		"TODO launch blocked",
		"Choose Dedicated worktree",
		"OK",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("attention dialog missing %q:\n%s", want, rendered)
		}
	}
	if severity := topStatusSeverityForMessage(got.status, nil); severity != topStatusSeverityWarning {
		t.Fatalf("top status severity = %d, want warning", severity)
	}
	withANSI256DarkBackground(t)
	if colored := got.renderAttentionDialogPanel(80); !strings.Contains(colored, "38;5;178") {
		t.Fatalf("blocked-launch dialog should use the warning color, got %q", colored)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the existing Codex session", len(requests))
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("acknowledging the warning should not start another action")
	}
	if got.attentionDialog != nil {
		t.Fatalf("Enter should acknowledge and close the warning")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("TODO launcher should remain available after acknowledgement")
	}
}

func TestTodoSessionOpenFailureShowsErrorModal(t *testing.T) {
	m := Model{
		todoLaunchDrafts: map[string]todoLaunchDraftState{
			"/tmp/demo": {
				projectPath: "/tmp/demo",
				todoID:      792,
				provider:    codexapp.ProviderCodex,
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.applyCodexSessionOpenedMsg(codexSessionOpenedMsg{
		projectPath: "/tmp/demo",
		provider:    codexapp.ProviderCodex,
		err:         fmt.Errorf("codex startup failed"),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("failed TODO session open should not return a follow-up command")
	}
	if got.status != "TODO launch failed (use /errors)" {
		t.Fatalf("status = %q, want TODO launch failure", got.status)
	}
	if got.attentionDialog == nil {
		t.Fatalf("failed TODO session open should show an error dialog")
	}
	if got.attentionDialog.Severity != attentionDialogSeverityError {
		t.Fatalf("attention dialog severity = %d, want error", got.attentionDialog.Severity)
	}
	if got.attentionDialog.Message != "codex startup failed" {
		t.Fatalf("attention dialog message = %q, want startup error", got.attentionDialog.Message)
	}
	if _, ok := got.todoLaunchDraftFor("/tmp/demo"); ok {
		t.Fatalf("failed TODO session open should clear its launch draft")
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "codex startup failed" {
		t.Fatalf("latest error log entry = %#v, want startup error", got.errorLogEntries)
	}
}

func TestTodoDialogModelToggleOpensPickerBeforeDraft(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-model-toggle",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
			models: []codexapp.ModelOption{{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				IsDefault:   true,
			}},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "codex",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          11,
				ProjectPath: "/tmp/demo",
				Text:        "Check model toggle behavior",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}
	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if !got.todoCopyDialog.OpenModelFirst {
		t.Fatalf("copy dialog should enable model toggle after m")
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("starting a TODO should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexModelPicker == nil || !got.codexModelPicker.Loading {
		t.Fatalf("model picker should enter loading state when m is enabled")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker guidance", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening the embedded session should return a model picker command")
	}
}

func TestTodoModelPickerApplyRefocusesComposerAndCapturesSettleLatencyImmediately(t *testing.T) {
	now := time.Date(2026, time.April, 2, 17, 30, 0, 0, time.UTC)
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:        codexapp.ProviderOpenCode,
			ThreadID:        "ses-model-focus",
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "OpenCode ready",
			Model:           "openai/gpt-4.1",
			ReasoningEffort: "",
		},
		models: []codexapp.ModelOption{{
			ID:          "openai/gpt-5.4",
			Model:       "openai/gpt-5.4",
			DisplayName: "GPT-5.4",
			Description: "Fast enough",
			IsDefault:   true,
		}},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {Text: "Line one\nLine two"},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        28,
		nowFn:         func() time.Time { return now },
		todoLaunchDrafts: map[string]todoLaunchDraftState{
			"/tmp/demo": {
				projectPath:    "/tmp/demo",
				provider:       codexapp.ProviderOpenCode,
				openModelFirst: true,
			},
		},
	}

	updated, cmd := m.update(codexSessionOpenedMsg{
		projectPath: "/tmp/demo",
		status:      "OpenCode session ready",
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("session open should return the model picker command")
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused while the model picker is taking over")
	}
	if got.codexInput.Value() != "Line one\nLine two" {
		t.Fatalf("composer text = %q, want preserved multiline TODO draft", got.codexInput.Value())
	}

	msgs := collectCmdMsgs(cmd)
	var listMsg codexModelListMsg
	ok := false
	for _, msg := range msgs {
		if typed, isList := msg.(codexModelListMsg); isList {
			listMsg = typed
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("cmd messages = %#v, want codexModelListMsg", msgs)
	}
	updated, _ = got.update(listMsg)
	got = updated.(Model)
	if got.codexModelPicker == nil || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be loaded")
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus = %q, want models", got.codexModelPicker.Focus)
	}
	session.trySnapshotCalls = 0
	session.snapshotCalls = 0

	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected model")
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.awaitSettle {
		t.Fatalf("model apply should wait for a snapshot settle event")
	}

	updated, cmd = got.update(action)
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("successful model apply should save preferences and refocus the composer")
	}
	if !got.codexInput.Focused() {
		t.Fatalf("composer should regain focus after applying the model picker selection")
	}
	if session.trySnapshotCalls != 0 {
		t.Fatalf("model apply should not probe the live session snapshot on the UI thread; TrySnapshot calls = %d", session.trySnapshotCalls)
	}
	if _, ok := got.modelSettlePending["/tmp/demo"]; ok {
		t.Fatalf("model settle should complete immediately after the staged snapshot refresh")
	}
	if len(got.aiLatencyInFlightSnapshot()) != 0 {
		t.Fatalf("in-flight latency ops = %#v, want none after the immediate settle refresh", got.aiLatencyInFlightSnapshot())
	}

	found := false
	for _, sample := range got.aiLatencyRecent {
		if sample.Name != "Model settle" {
			continue
		}
		found = true
		if sample.ProjectPath != "/tmp/demo" {
			t.Fatalf("model settle project = %q, want /tmp/demo", sample.ProjectPath)
		}
		if sample.Duration != 0 {
			t.Fatalf("model settle duration = %v, want immediate completion", sample.Duration)
		}
	}
	if !found {
		t.Fatalf("recent latency samples = %#v, want a completed model settle sample", got.aiLatencyRecent)
	}
}

func TestCodexUpdateAfterModelApplyDefersLiveSnapshotRefresh(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:        codexapp.ProviderCodex,
			ThreadID:        "thread-demo",
			Started:         true,
			Status:          "Codex ready",
			Model:           "gpt-5",
			ReasoningEffort: "medium",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true},
		},
		width:  100,
		height: 24,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	session.trySnapshotCalls = 0
	session.snapshotCalls = 0

	updated, _ := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
		awaitSettle: true,
	})
	got := updated.(Model)
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("model apply should avoid live snapshot reads on the UI thread; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}

	updated, _ = got.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got = updated.(Model)
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("the first update after model apply should defer live snapshot refresh off the UI thread; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}
	if _, ok := got.codexSkipNextLiveRefresh["/tmp/demo"]; ok {
		t.Fatalf("codexUpdateMsg should consume the deferred-refresh hint")
	}
}

func TestCodexUpdateBusyToIdleSettlesTurnRefreshesProjectStatusAndSidebarDiff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionEvidence := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceCodex,
		SessionID:            "codex:thread-demo",
		RawSessionID:         "thread-demo",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		Format:               "modern",
		StartedAt:            now.Add(-10 * time.Minute),
		LastEventAt:          now.Add(-time.Minute),
		LatestTurnStartedAt:  now.Add(-2 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		LastActivity:  now.Add(-time.Minute),
		Status:        model.StatusActive,
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     false,
		Sessions:      []model.SessionEvidence{sessionEvidence},
		CreatedAt:     now.Add(-2 * time.Hour),
		UpdatedAt:     now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    projectPath,
			ThreadID:       "thread-demo",
			Started:        true,
			Busy:           false,
			BusySince:      now.Add(-2 * time.Minute),
			LastActivityAt: now,
			Status:         "Codex ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		selected:            0,
		codexVisibleProject: projectPath,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:          projectPath,
				Name:          "repo",
				PresentOnDisk: true,
				RepoBranch:    "master",
			},
		},
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:           codexapp.ProviderCodex,
				ProjectPath:        projectPath,
				ThreadID:           "thread-demo",
				Started:            true,
				Busy:               true,
				BusySince:          now.Add(-2 * time.Minute),
				LastBusyActivityAt: now.Add(-30 * time.Second),
				LastActivityAt:     now.Add(-30 * time.Second),
			},
		},
		codexInput:      newCodexTextarea(),
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           118,
		height:          24,
	}

	updated, cmd := m.Update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("busy-to-idle update should queue follow-up work")
	}
	raw := cmd()
	if raw == nil {
		t.Fatal("busy-to-idle update should return follow-up messages")
	}
	if batch, ok := raw.(tea.BatchMsg); ok {
		foundProjectRefresh := false
		foundSidebarDiffRefresh := false
		firstFollowUp := max(0, len(batch)-4)
		for i := len(batch) - 1; i >= firstFollowUp && (!foundProjectRefresh || !foundSidebarDiffRefresh); i-- {
			if batch[i] == nil {
				continue
			}
			msg := batch[i]()
			switch typed := msg.(type) {
			case projectStatusRefreshedMsg:
				updated, followUp := got.Update(typed)
				got = updated.(Model)
				got = drainCmdMsgs(got, followUp)
				foundProjectRefresh = true
			case embeddedSidebarDiffPreviewMsg:
				updated, followUp := got.Update(typed)
				got = updated.(Model)
				got = drainCmdMsgs(got, followUp)
				foundSidebarDiffRefresh = true
			}
		}
		if !foundProjectRefresh {
			t.Fatalf("busy-to-idle batch should include projectStatusRefreshedMsg, got %#v", raw)
		}
		if !foundSidebarDiffRefresh {
			t.Fatalf("busy-to-idle batch should include embeddedSidebarDiffPreviewMsg, got %#v", raw)
		}
	} else {
		updated, followUp := got.Update(raw)
		got = updated.(Model)
		got = drainCmdMsgs(got, followUp)
	}

	if !got.detail.Summary.RepoDirty {
		t.Fatalf("detail repo dirty = %t, want refreshed dirty state", got.detail.Summary.RepoDirty)
	}
	if !got.detail.Summary.LatestTurnStateKnown || !got.detail.Summary.LatestTurnCompleted {
		t.Fatalf("detail turn state = known:%t completed:%t, want settled turn", got.detail.Summary.LatestTurnStateKnown, got.detail.Summary.LatestTurnCompleted)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after settle refresh: %v", err)
	}
	if !detail.Summary.RepoDirty {
		t.Fatalf("store detail should reflect dirty repo after settle refresh, got %#v", detail.Summary)
	}
	if len(detail.Sessions) == 0 || !detail.Sessions[0].LatestTurnStateKnown || !detail.Sessions[0].LatestTurnCompleted {
		t.Fatalf("stored session turn state = %#v, want settled turn", detail.Sessions)
	}
	diffState, ok := got.embeddedSidebarDiffState(projectPath)
	if !ok || diffState.Preview == nil || len(diffState.Preview.Files) == 0 {
		t.Fatalf("sidebar diff state = %#v, want refreshed diff preview", diffState)
	}
	if got := diffState.Preview.Files[0].Path; got != "README.md" {
		t.Fatalf("sidebar diff first file = %q, want README.md", got)
	}
}

func TestSyncCodexViewportRecordsSharedStageLatencies(t *testing.T) {
	projectPath := "/tmp/demo"
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Closed:   true,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "hello",
		}},
	}

	now := time.Date(2026, time.April, 3, 11, 0, 0, 0, time.UTC)
	ticks := 0
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
		nowFn: func() time.Time {
			value := now.Add(time.Duration(ticks) * 200 * time.Millisecond)
			ticks++
			return value
		},
	}
	m.storeCodexSnapshot(projectPath, snapshot)

	m.syncCodexViewport(true)

	gotNames := map[string]bool{}
	for _, sample := range m.aiLatencyRecent {
		gotNames[sample.Name] = true
	}
	for _, want := range []string{
		"Embedded lower blocks",
		"Embedded transcript render",
		"Embedded viewport content",
		"Embedded viewport sync",
	} {
		if !gotNames[want] {
			t.Fatalf("syncCodexViewport() missing latency sample %q, got %#v", want, m.aiLatencyRecent)
		}
	}
}

func TestCodexUpdateStatusOnlyPreservesViewportOffset(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: strings.Join([]string{
					"line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08",
					"line 09", "line 10", "line 11", "line 12", "line 13", "line 14", "line 15", "line 16",
					"line 17", "line 18", "line 19", "line 20", "line 21", "line 22", "line 23", "line 24",
					"line 25", "line 26", "line 27", "line 28", "line 29", "line 30", "line 31", "line 32",
				}, "\n"),
			}},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              18,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)
	m.codexViewport.SetYOffset(1)

	session.snapshot.Status = "Codex status changed only"

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if got.codexViewport.YOffset != 1 {
		t.Fatalf("status-only codex update should preserve viewport offset, got %d", got.codexViewport.YOffset)
	}
}

func TestCodexPageKeysScrollTranscriptByEightyPercent(t *testing.T) {
	vp := viewport.New(80, 10)
	vp.SetContent(testViewportLines(40))
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {Provider: codexapp.ProviderCodex, ProjectPath: "/tmp/demo", Started: true},
		},
		codexViewport: vp,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Fatalf("Page Down should not return a command")
	}
	got := updated.(Model)
	if got.codexViewport.YOffset != 8 {
		t.Fatalf("Page Down offset = %d, want 8", got.codexViewport.YOffset)
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyPgUp})
	if cmd != nil {
		t.Fatalf("Page Up should not return a command")
	}
	got = updated.(Model)
	if got.codexViewport.YOffset != 0 {
		t.Fatalf("Page Up offset = %d, want 0", got.codexViewport.YOffset)
	}
}
