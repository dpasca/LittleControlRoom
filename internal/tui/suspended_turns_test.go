package tui

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildSuspendedTurnResumeChoicesFiltersAndSorts(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	projects := []model.ProjectSummary{
		{
			Path:                     "/tmp/completed",
			Name:                     "completed",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "codex:done",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now.Add(-time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      true,
		},
		{
			Path:                     "/tmp/missing",
			Name:                     "missing",
			InScope:                  true,
			LatestSessionID:          "codex:missing",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now,
			LatestTurnStateKnown:     true,
		},
		{
			Path:                     "/tmp/old",
			Name:                     "old",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "codex:old",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now.Add(-2 * time.Hour),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		},
		{
			Path:                     "/tmp/new",
			Name:                     "new",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "opencode:new",
			LatestSessionFormat:      "opencode_db",
			LatestSessionLastEventAt: now.Add(-5 * time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		},
	}

	choices := buildSuspendedTurnResumeChoices(projects, 10)
	if len(choices) != 2 {
		t.Fatalf("choices len = %d, want 2: %#v", len(choices), choices)
	}
	if choices[0].ProjectPath != "/tmp/new" || choices[0].Provider != codexapp.ProviderOpenCode || choices[0].SessionID != "new" {
		t.Fatalf("first choice = %#v, want newest OpenCode raw session", choices[0])
	}
	if choices[1].ProjectPath != "/tmp/old" || choices[1].Provider != codexapp.ProviderCodex || choices[1].SessionID != "old" {
		t.Fatalf("second choice = %#v, want older Codex raw session", choices[1])
	}
}

func TestSuspendedTurnResumeDialogEnterOpensChoicesInBackground(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider.Normalized(),
				ProjectPath:    req.ProjectPath,
				ThreadID:       req.ResumeID,
				Started:        true,
				LastActivityAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
			},
		}, nil
	})
	m := Model{
		codexManager: manager,
		width:        100,
		height:       24,
	}

	updated, cmd := m.Update(suspendedTurnResumeChoicesMsg{choices: []suspendedTurnResumeChoice{
		{ProjectPath: "/tmp/a", ProjectName: "a", Provider: codexapp.ProviderCodex, SessionID: "cx-a"},
		{ProjectPath: "/tmp/b", ProjectName: "b", Provider: codexapp.ProviderClaudeCode, SessionID: "cc-b"},
	}})
	if cmd != nil {
		t.Fatalf("choices msg should not launch until the user confirms")
	}
	got := updated.(Model)
	if got.suspendedTurnDialog == nil {
		t.Fatalf("suspended turn dialog not shown")
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.suspendedTurnDialog != nil {
		t.Fatalf("dialog should close after confirm")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) != 2 {
		t.Fatalf("resume command messages = %d, want 2: %#v", len(msgs), msgs)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].ProjectPath != "/tmp/a" || requests[0].Provider != codexapp.ProviderCodex || requests[0].ResumeID != "cx-a" {
		t.Fatalf("first request = %#v", requests[0])
	}
	if requests[1].ProjectPath != "/tmp/b" || requests[1].Provider != codexapp.ProviderClaudeCode || requests[1].ResumeID != "cc-b" {
		t.Fatalf("second request = %#v", requests[1])
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("confirmed startup resumes should stay hidden, visible project = %q", got.codexVisibleProject)
	}
}
