package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"strings"
	"testing"
	"time"
)

func TestVisibleCodexAltEnterInsertsNewline(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	input := newCodexTextarea()
	input.SetValue("line 1")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+enter should not send a command")
	}
	if got.codexInput.Value() != "line 1\n" {
		t.Fatalf("codex input = %q, want trailing newline", got.codexInput.Value())
	}
}

func TestVisibleCodexRecordsComposerInputTelemetry(t *testing.T) {
	input := newCodexTextarea()
	input.Focus()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started: true,
				Status:  "Codex session ready",
			},
		},
		codexInput:    input,
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		nowFn:         func() time.Time { return now },
		width:         100,
		height:        24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got := updated.(Model)

	if got.codexInput.Value() != "h" {
		t.Fatalf("composer value = %q, want typed rune", got.codexInput.Value())
	}
	if got.codexComposerKeyCount != 1 || !got.codexComposerLastKeyAt.Equal(now) {
		t.Fatalf("key telemetry = (%d, %v), want one key at %v", got.codexComposerKeyCount, got.codexComposerLastKeyAt, now)
	}
	if got.codexComposerChangeCount != 1 || !got.codexComposerLastChangeAt.Equal(now) {
		t.Fatalf("change telemetry = (%d, %v), want one change at %v", got.codexComposerChangeCount, got.codexComposerLastChangeAt, now)
	}
	rendered := ansi.Strip(got.renderPerfContent(80))
	for _, want := range []string{"Composer", "Input", "focused", "1 routed", "1 changed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderPerfContent() missing %q for composer telemetry: %q", want, rendered)
		}
	}
}

func TestStoreCodexSnapshotIgnoresLCAgentSuggestedDraftWithoutSource(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
	}

	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:              codexapp.ProviderLCAgent,
		ProjectPath:           projectPath,
		SuggestedInputDraftID: "draft-1",
		SuggestedInputDraft:   "Please verify the failing test before continuing.",
	})
	if got := m.codexInput.Value(); got != "" {
		t.Fatalf("composer draft = %q", got)
	}
}

func TestStoreCodexSnapshotAppliesNonLCAgentSuggestedDraftOnce(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
	}

	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:              codexapp.ProviderCodex,
		ProjectPath:           projectPath,
		SuggestedInputDraftID: "draft-1",
		SuggestedInputDraft:   "Please inspect the changed files.",
	})
	if got := m.codexInput.Value(); got != "Please inspect the changed files." {
		t.Fatalf("composer draft = %q", got)
	}
	if got := m.status; got != "Suggested follow-up draft ready for review." {
		t.Fatalf("status = %q", got)
	}

	m.codexInput.SetValue("")
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:              codexapp.ProviderCodex,
		ProjectPath:           projectPath,
		SuggestedInputDraftID: "draft-1",
		SuggestedInputDraft:   "Please inspect the changed files.",
	})
	if got := m.codexInput.Value(); got != "" {
		t.Fatalf("same draft reapplied after clearing = %q", got)
	}

	m.codexInput.SetValue("human draft")
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:              codexapp.ProviderCodex,
		ProjectPath:           projectPath,
		SuggestedInputDraftID: "draft-2",
		SuggestedInputDraft:   "Please inspect the changed files again.",
	})
	if got := m.codexInput.Value(); got != "human draft" {
		t.Fatalf("suggested draft overwrote human draft = %q", got)
	}
}

func TestVisibleCodexCtrlVAttachesClipboardImage(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "/tmp/clipboard.png", nil
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd == nil || !got.codexClipboardPasteInFlight {
		t.Fatalf("ctrl+v image attach should queue background clipboard work")
	}
	got = completeCodexClipboardPaste(t, got, cmd)
	attachments := got.currentCodexAttachments()
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if attachments[0].Path != "/tmp/clipboard.png" {
		t.Fatalf("attachment path = %q, want /tmp/clipboard.png", attachments[0].Path)
	}
	if got.codexInput.Value() != "[Image #1] " {
		t.Fatalf("composer = %q, want inline image marker", got.codexInput.Value())
	}
	if got.status != "Attached [Image #1]" {
		t.Fatalf("status = %q, want attachment notice", got.status)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "[Image #1]") {
		t.Fatalf("rendered view should show an inline image marker: %q", rendered)
	}
}

func TestVisibleCodexBackspaceRemovesInlineImageMarker(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "/tmp/clipboard.png", nil
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+v image attach should queue background clipboard work")
	}
	got = completeCodexClipboardPaste(t, got, cmd)

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace marker removal should not queue a command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer = %q, want marker removed", got.codexInput.Value())
	}
	attachments := got.currentCodexAttachments()
	if len(attachments) != 0 {
		t.Fatalf("attachments = %d, want 0", len(attachments))
	}
	if got.status != "Removed [Image #1]" {
		t.Fatalf("status = %q, want inline marker removal notice", got.status)
	}
}

func TestVisibleCodexCtrlVPastesLargeTextAsPlaceholder(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "", errClipboardHasNoImage
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	previousReader := clipboardTextReader
	clipboardTextReader = func() (string, error) {
		return strings.Repeat("a", 1200), nil
	}
	t.Cleanup(func() {
		clipboardTextReader = previousReader
	})

	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd == nil || !got.codexClipboardPasteInFlight {
		t.Fatalf("ctrl+v large text paste should queue background clipboard work")
	}
	got = completeCodexClipboardPaste(t, got, cmd)
	if got.codexInput.Value() != "[Paste #1: 1 line] " {
		t.Fatalf("composer = %q, want large paste placeholder", got.codexInput.Value())
	}
	pastedTexts := got.currentCodexPastedTexts()
	if len(pastedTexts) != 1 {
		t.Fatalf("pasted texts = %d, want 1", len(pastedTexts))
	}
	if pastedTexts[0].Text != strings.Repeat("a", 1200) {
		t.Fatalf("stored pasted text length = %d, want 1200", len([]rune(pastedTexts[0].Text)))
	}
	if got.status != "Pasted [1 line pasted] as a placeholder" {
		t.Fatalf("status = %q, want placeholder notice", got.status)
	}
}

func TestVisibleCodexBracketedPasteUsesLargeTextPlaceholder(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	longText := strings.Repeat("b", 800)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longText), Paste: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("bracketed large paste should not queue a command")
	}
	if got.codexInput.Value() != "[Paste #1: 1 line] " {
		t.Fatalf("composer = %q, want bracketed paste placeholder", got.codexInput.Value())
	}
}

func TestVisibleCodexBracketedPasteAfterExistingInputUsesPlaceholder(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	input := newCodexTextarea()
	input.SetValue("short dictation")
	input.CursorEnd()
	longText := strings.Repeat("c", 800)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longText), Paste: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("bracketed large paste should not queue a command")
	}
	if got.codexInput.Value() != "short dictation [Paste #1: 1 line] " {
		t.Fatalf("composer = %q, want existing text plus paste placeholder", got.codexInput.Value())
	}
	submission := got.currentCodexDraft().Submission()
	if submission.Text != "short dictation "+longText {
		t.Fatalf("submission text = %q, want existing text plus expanded paste", submission.Text)
	}
	if submission.DisplayText != "short dictation [1 line pasted]" {
		t.Fatalf("submission display text = %q, want collapsed display text", submission.DisplayText)
	}
}

func TestVisibleCodexBulkRuneInputUsesLargeTextPlaceholder(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	longText := strings.Repeat("d", 900)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longText)})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("bulk text input should not queue a command")
	}
	if got.codexInput.Value() != "[Paste #1: 1 line] " {
		t.Fatalf("composer = %q, want bulk text placeholder", got.codexInput.Value())
	}
	pastedTexts := got.currentCodexPastedTexts()
	if len(pastedTexts) != 1 || pastedTexts[0].Text != longText {
		t.Fatalf("pasted texts = %#v, want stored bulk text", pastedTexts)
	}
}

func TestVisibleCodexInputDoesNotFreezeAtPreviousCharLimit(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	input := newCodexTextarea()
	input.SetValue(strings.Repeat("x", 10000))
	input.CursorEnd()
	input.Focus()
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got := updated.(Model)
	if got.codexInput.Value() != strings.Repeat("x", 10000)+"z" {
		t.Fatalf("composer length = %d, want previous 10000-rune draft plus new text", len([]rune(got.codexInput.Value())))
	}
}

func TestVisibleCodexBackspaceRemovesLargePastePlaceholder(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "", errClipboardHasNoImage
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	previousReader := clipboardTextReader
	clipboardTextReader = func() (string, error) {
		return strings.Repeat("x", 900), nil
	}
	t.Cleanup(func() {
		clipboardTextReader = previousReader
	})

	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, pasteCmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	got = completeCodexClipboardPaste(t, got, pasteCmd)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace large placeholder removal should not queue a command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer = %q, want placeholder removed", got.codexInput.Value())
	}
	if len(got.currentCodexPastedTexts()) != 0 {
		t.Fatalf("pasted texts = %d, want 0", len(got.currentCodexPastedTexts()))
	}
	if got.status != "Removed [1 line pasted] placeholder" {
		t.Fatalf("status = %q, want placeholder removal notice", got.status)
	}
}

func completeCodexClipboardPaste(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("clipboard paste command is nil")
	}
	raw := cmd()
	msg, ok := raw.(codexClipboardPasteMsg)
	if !ok {
		t.Fatalf("clipboard paste command returned %T", raw)
	}
	updated, followup := m.applyCodexClipboardPasteMsg(msg)
	if followup != nil {
		t.Fatal("applying clipboard paste should not queue follow-up work")
	}
	return updated.(Model)
}

func TestVisibleCodexSubmissionStripsInlineImageMarker(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	input := newCodexTextarea()
	input.SetValue("[Image #1] describe this")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {
				Attachments: []codexapp.Attachment{
					{Kind: codexapp.AttachmentLocalImage, Path: "/tmp/one.png"},
				},
			},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue a submission command")
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submission returned error = %v", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Text != "describe this" {
		t.Fatalf("submission text = %q, want stripped prompt", submission.Text)
	}
	if len(submission.Attachments) != 1 || submission.Attachments[0].Path != "/tmp/one.png" {
		t.Fatalf("submission attachments = %#v, want one image", submission.Attachments)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer should clear after submit, got %q", got.codexInput.Value())
	}
}

func TestVisibleCodexSubmissionExpandsLargePastePlaceholder(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	hidden := strings.Repeat("p", 700)
	token := "[Paste #1: 1 line]"
	input := newCodexTextarea()
	input.SetValue(token + " summarize this")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {
				PastedTexts: []codexPastedText{{
					Token: token,
					Text:  hidden,
				}},
			},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue a submission command")
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submission returned error = %v", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Text != hidden+" summarize this" {
		t.Fatalf("submission text length = %d, want expanded hidden paste", len([]rune(submission.Text)))
	}
	if submission.DisplayText != "[1 line pasted] summarize this" {
		t.Fatalf("submission display text = %q, want collapsed paste placeholder", submission.DisplayText)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer should clear after submit, got %q", got.codexInput.Value())
	}
}

func TestRenderCodexTranscriptShowsFullTypedText(t *testing.T) {
	longText := strings.Repeat("z", 650)
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: longText},
			},
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

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "[650 characters]") {
		t.Fatalf("rendered transcript should NOT collapse typed text: %q", rendered)
	}
	if !strings.Contains(rendered, strings.Repeat("z", 80)) {
		t.Fatalf("rendered transcript should include the full typed text")
	}
}
