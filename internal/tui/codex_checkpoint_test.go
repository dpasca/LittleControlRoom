package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/codexslash"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type checkpointTestSession struct {
	fakeCodexSession
	notes []string
}

func (s *checkpointTestSession) RequestCheckpoint(ctx context.Context, note string) error {
	s.notes = append(s.notes, note)
	return ctx.Err()
}

func newCheckpointTestModel(t *testing.T) (Model, *checkpointTestSession) {
	t.Helper()
	s := &checkpointTestSession{fakeCodexSession: fakeCodexSession{
		projectPath: "/tmp/checkpoint",
		snapshot: codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Started: true,
			Busy: true, ThreadID: "claude-original", ControlSessionKey: "original-key", Phase: codexapp.SessionPhaseRunning},
	}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return s, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: s.projectPath, Provider: codexapp.ProviderClaudeCode, Preset: codexcli.PresetYolo}); err != nil {
		t.Fatal(err)
	}
	return Model{codexManager: manager, codexVisibleProject: s.projectPath, codexHiddenProject: s.projectPath,
		codexInput: newCodexTextarea(), codexViewport: viewport.New(0, 0), width: 100, height: 24}, s
}

func TestVisibleClaudeCheckpointRunsAsyncAndCoalesces(t *testing.T) {
	m, session := newCheckpointTestModel(t)
	m.codexInput.SetValue("/checkpoint preserve current evidence")
	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || len(session.notes) != 0 || !m.codexCheckpointPending[session.projectPath] {
		t.Fatal("checkpoint must be deferred with immediate busy state")
	}
	m.codexInput.SetValue("/checkpoint duplicate")
	updated, duplicate := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if duplicate != nil || !strings.Contains(m.status, "already in progress") {
		t.Fatal("repeat activation was not ignored")
	}
	action := cmd().(codexActionMsg)
	if action.err != nil || len(session.notes) != 1 || session.notes[0] != "preserve current evidence" {
		t.Fatalf("checkpoint failed: %+v %v", action, session.notes)
	}
	if len(session.submitted) != 0 {
		t.Fatal("checkpoint was queued as an ordinary follow-up")
	}
	updated, _ = m.applyCodexActionMsg(action)
	m = updated.(Model)
	if m.codexCheckpointPending[session.projectPath] {
		t.Fatal("checkpoint UI stayed busy after submission")
	}
}

func TestVisibleClaudeCheckpointRefusesReplacementSession(t *testing.T) {
	m, session := newCheckpointTestModel(t)
	_, cmd := m.requestVisibleClaudeCheckpoint(session.snapshot, "report")
	session.snapshot.ThreadID = "replacement"
	action := cmd().(codexActionMsg)
	if !errors.Is(action.err, codexapp.ErrSessionChanged) || len(session.notes) != 0 {
		t.Fatalf("checkpoint crossed session identity: %+v", action)
	}
}

func TestClaudeCheckpointPreservesDraftWhileSettling(t *testing.T) {
	m, session := newCheckpointTestModel(t)
	m.codexCheckpointPending = map[string]bool{session.projectPath: true}
	m.codexInput.SetValue("another instruction")
	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.codexInput.Value() != "another instruction" || len(session.submitted) != 0 {
		t.Fatal("checkpoint lost the draft or accepted competing input")
	}
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode} {
		snapshot := session.snapshot
		snapshot.Provider, snapshot.BusyExternal = provider, true
		if _, cmd := m.requestVisibleClaudeCheckpoint(snapshot, "report"); cmd != nil {
			t.Fatal("checkpoint accepted external or unsupported session")
		}
	}
}

func TestClaudeSilenceOffersExplicitCheckpoint(t *testing.T) {
	m, session := newCheckpointTestModel(t)
	snapshot := session.snapshot
	snapshot.ParentStatus, snapshot.ParentTurnActive = "Parent working", true
	snapshot.ParentActivityAt = time.Now().Add(-10 * time.Minute)
	if text := strings.Join(m.renderCodexMessageDelivery(snapshot, 100), "\n"); !strings.Contains(text, "/checkpoint interrupts active tools") {
		t.Fatal("silent Claude turn did not offer checkpoint")
	}
	inv, err := codexslash.ParseClaude("/checkpoint preserve job IDs")
	if err != nil || inv.Kind != codexslash.KindCheckpoint || inv.Prompt != "preserve job IDs" {
		t.Fatalf("checkpoint note lost: %+v %v", inv, err)
	}
}
