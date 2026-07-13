package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
)

func TestSupersededPendingOpenCompletionDoesNotReplaceNewerVisibleSession(t *testing.T) {
	m := Model{
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}
	m.beginNewCodexPendingOpen("/tmp/session-a", codexapp.ProviderCodex)
	requestA := m.codexPendingOpen.requestID

	updated, _ := m.hidePendingCodexOpen("/tmp/session-a")
	m = updated.(Model)
	m.beginNewCodexPendingOpen("/tmp/session-b", codexapp.ProviderOpenCode)
	requestB := m.codexPendingOpen.requestID

	updated, _ = m.applyCodexSessionOpenedMsg(codexSessionOpenedMsg{
		projectPath:   "/tmp/session-b",
		provider:      codexapp.ProviderOpenCode,
		openRequestID: requestB,
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderOpenCode,
			ProjectPath: "/tmp/session-b",
			ThreadID:    "session-b",
			Started:     true,
		},
		visibleStatus:    "Session B ready",
		backgroundStatus: "Session B ready in background",
	})
	m = updated.(Model)
	if m.codexVisibleProject != "/tmp/session-b" {
		t.Fatalf("visible project after session B opens = %q, want session B", m.codexVisibleProject)
	}

	updated, _ = m.applyCodexSessionOpenedMsg(codexSessionOpenedMsg{
		projectPath:   "/tmp/session-a",
		provider:      codexapp.ProviderCodex,
		openRequestID: requestA,
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderCodex,
			ProjectPath: "/tmp/session-a",
			ThreadID:    "session-a",
			Started:     true,
		},
		visibleStatus:    "Session A ready",
		backgroundStatus: "Session A ready in background",
	})
	m = updated.(Model)
	if m.codexVisibleProject != "/tmp/session-b" {
		t.Fatalf("late session A completion replaced the visible session: got %q", m.codexVisibleProject)
	}
	if m.codexHiddenProject != "/tmp/session-a" {
		t.Fatalf("late session A should remain available in the background, hidden project = %q", m.codexHiddenProject)
	}
}

func TestPendingOpenAcceptsAndPreservesDraftUntilSessionIsReady(t *testing.T) {
	m := Model{
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}
	projectPath := "/tmp/session-loading"
	m.beginNewCodexPendingOpen(projectPath, codexapp.ProviderCodex)
	requestID := m.codexPendingOpen.requestID

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Start investigating now")})
	m = updated.(Model)
	if got := m.codexDrafts[projectPath].Text; got != "Start investigating now" {
		t.Fatalf("pending draft = %q, want typed text", got)
	}
	if rendered := m.renderCodexView(); !strings.Contains(rendered, "Start investigating now") || !strings.Contains(rendered, "Type your draft now") {
		t.Fatalf("pending view should render the editable draft and guidance, got %q", rendered)
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if got := m.codexDrafts[projectPath].Text; got != "Start investigating now" {
		t.Fatalf("early Enter discarded pending draft: got %q", got)
	}
	if !strings.Contains(m.status, "Draft saved") {
		t.Fatalf("early Enter status = %q, want saved-draft guidance", m.status)
	}

	updated, _ = m.applyCodexSessionOpenedMsg(codexSessionOpenedMsg{
		projectPath:   projectPath,
		provider:      codexapp.ProviderCodex,
		openRequestID: requestID,
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderCodex,
			ProjectPath: projectPath,
			ThreadID:    "thread-ready",
			Started:     true,
		},
		visibleStatus: "Session ready",
	})
	m = updated.(Model)
	if m.codexVisibleProject != projectPath {
		t.Fatalf("visible project = %q, want ready session", m.codexVisibleProject)
	}
	if got := m.codexInput.Value(); got != "Start investigating now" {
		t.Fatalf("ready composer draft = %q, want pending text", got)
	}
}
