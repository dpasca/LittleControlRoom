package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestEnterOpensClaudeSubagentInsteadOfEmptyCodexSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider, Started: true, ThreadID: req.ResumeID,
				EmptyConversation: req.Provider == codexapp.ProviderCodex,
				BusyExternal:      req.Provider == codexapp.ProviderClaudeCode,
				LastActivityAt:    time.Now(),
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/worktree", Provider: codexapp.ProviderCodex, ResumeID: "empty-codex"}); err != nil {
		t.Fatal(err)
	}
	project := model.ProjectSummary{
		Path: "/tmp/worktree", Name: "worktree", PresentOnDisk: true,
		LatestSessionID: "claude_code:parent/agent-worker", LatestSessionFormat: "claude_code",
		LatestTurnStateKnown: true, LatestSessionLastEventAt: time.Now(), LatestTurnStartedAt: time.Now().Add(-time.Minute),
	}
	m := Model{
		codexManager: manager, projects: []model.ProjectSummary{project},
		focusedPane: focusProjects, startupScanCompleted: true,
		codexInput: newCodexTextarea(), codexDrafts: make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0), width: 120, height: 30,
		anthropicAPIKeyPresentFn: func() bool { return true },
	}
	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil || got.attentionDialog != nil || got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("Enter failed to select Claude child: %#v", got.codexPendingOpen)
	}
	opened, ok := cmd().(codexSessionOpenedMsg)
	if !ok || opened.err != nil || !strings.Contains(opened.status, "read-only Claude Code subagent") {
		t.Fatalf("read-only open should bypass API billing prompts: %#v", opened)
	}
	if len(requests) != 2 || requests[1].Provider != codexapp.ProviderClaudeCode || requests[1].ResumeID != "parent/agent-worker" || requests[1].ForceNew {
		t.Fatalf("wrong launch request: %#v", requests)
	}
	label, tag, _ := (Model{startupScanCompleted: true}).projectAgentDisplay(project, time.Now())
	if tag != "CC" || !strings.HasPrefix(label, "CC ") {
		t.Fatalf("missing Claude badge or activity timer: %q, %q", label, tag)
	}
	// Closing or losing the local observer is not an external task completion.
	if _, ok := embeddedSessionSettledActivityFromSnapshot(project.Path, opened.snapshot); ok {
		t.Fatal("closing the observer must not persist completion")
	}
	m.markClosedEmbeddedSessionSettled(project.Path, opened.snapshot)
	if m.projects[0].LatestTurnCompleted {
		t.Fatal("closing the observer must not mark the worktree done")
	}
	completed := opened.snapshot
	completed.LatestTurnStateKnown = true
	completed.LatestTurnCompleted = true
	if _, ok := embeddedSessionSettledActivityFromSnapshot(project.Path, completed); !ok {
		t.Fatal("a recorded terminal turn should persist completion")
	}
	if got.codexBrowserPolicyMismatch(opened.snapshot) || embeddedSidebarBrowserRelevant(opened.snapshot) {
		t.Fatal("read-only child must not offer to reconnect managed browser controls")
	}
}
