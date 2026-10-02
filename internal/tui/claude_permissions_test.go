package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexslash"
)

type fakeClaudePermissionSession struct {
	*fakeCodexSession
	action, rule string
	remove       bool
	edits        int
}

func (s *fakeClaudePermissionSession) EditPermissionRule(action, rule string, remove bool) error {
	s.action, s.rule, s.remove = action, rule, remove
	s.edits++
	return nil
}

func TestClaudePermissionsDispatchPreservesRuleAndRunsLocally(t *testing.T) {
	for _, input := range []string{"/permissions", "/permissions allow Bash(huggingface-cli upload:*)", "/permissions remove deny Bash(Git Push:*)"} {
		t.Run(input, func(t *testing.T) {
			session := &fakeClaudePermissionSession{fakeCodexSession: &fakeCodexSession{projectPath: "/tmp/demo", snapshot: codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Started: true, ThreadID: "claude-demo"}}}
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) { return session, nil })
			if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo", Provider: codexapp.ProviderClaudeCode}); err != nil {
				t.Fatal(err)
			}
			inputBox := newCodexTextarea()
			inputBox.SetValue(input)
			m := Model{codexManager: manager, codexVisibleProject: "/tmp/demo", codexHiddenProject: "/tmp/demo", codexInput: inputBox, codexViewport: viewport.New(0, 0), width: 100, height: 24}
			updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
			got := updated.(Model)
			if cmd == nil {
				t.Fatalf("no command: %s", got.status)
			}
			if !got.claudePermissionsBusy {
				t.Fatal("missing busy state")
			}
			message := cmd()
			result, ok := message.(claudePermissionsResultMsg)
			if !ok || result.err != nil {
				t.Fatalf("result=%#v", message)
			}
			if input == "/permissions" {
				if session.permissionCalls != 1 {
					t.Fatal("did not show permissions")
				}
			} else {
				if session.edits != 1 {
					t.Fatal("did not edit permission rule")
				}
				if session.action == "allow" && session.rule != "Bash(huggingface-cli upload:*)" {
					t.Fatalf("rule=%q", session.rule)
				}
				if session.action == "deny" && (!session.remove || session.rule != "Bash(Git Push:*)") {
					t.Fatalf("remove=%v rule=%q", session.remove, session.rule)
				}
			}
			if len(session.submissions) != 0 {
				t.Fatal("permissions sent to Claude as a prompt")
			}
			applied, _ := got.Update(message)
			if applied.(Model).claudePermissionsBusy {
				t.Fatal("busy state not cleared")
			}
		})
	}
}

func TestClaudePermissionCompletionUsesCachedProviderAndCyclesActions(t *testing.T) {
	input := newCodexTextarea()
	input.SetValue("/permissions allow")
	m := Model{codexInput: input, codexVisibleProject: "/tmp/demo", codexSnapshots: map[string]codexapp.Snapshot{"/tmp/demo": {ProjectPath: "/tmp/demo", Provider: codexapp.ProviderClaudeCode}}}
	if !m.cycleAndApplyCodexSlashSuggestion(1) || m.codexInput.Value() != "/permissions ask" {
		t.Fatalf("Tab failed to cycle Claude actions: %q", m.codexInput.Value())
	}
	m.codexInput.SetValue("/permissions allow Bash(Git Push:*)")
	if got := m.resolvedCodexSlashInput(); got != "/permissions allow Bash(Git Push:*)" {
		t.Fatalf("rule changed: %q", got)
	}
	m.claudePermissionsBusy = true
	_, cmd := m.runClaudePermissions(codexslash.Invocation{Kind: codexslash.KindPermissions})
	if cmd != nil {
		t.Fatal("repeated activation while busy")
	}
}
