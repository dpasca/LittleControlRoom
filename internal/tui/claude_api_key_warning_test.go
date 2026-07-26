package tui

import (
	"errors"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestAnthropicAPIKeyPresentInEnvironment(t *testing.T) {
	t.Setenv(anthropicAPIKeyEnvironmentVariable, "test-key")
	if !anthropicAPIKeyPresentInEnvironment() {
		t.Fatal("non-empty ANTHROPIC_API_KEY was not detected")
	}
	t.Setenv(anthropicAPIKeyEnvironmentVariable, "   ")
	if anthropicAPIKeyPresentInEnvironment() {
		t.Fatal("blank ANTHROPIC_API_KEY should not trigger the billing warning")
	}
}

func TestClaudeAPIKeyWarningDefersEmbeddedLaunchUntilAcknowledged(t *testing.T) {
	launches := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		launches++
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:    codexapp.ProviderClaudeCode,
				ProjectPath: req.ProjectPath,
				ThreadID:    "claude-thread",
				Started:     true,
				Status:      "Claude Code session ready",
			},
		}, nil
	})
	m := Model{
		codexManager: manager,
		anthropicAPIKeyPresentFn: func() bool {
			return true
		},
	}

	cmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: "/tmp/billing-warning",
	})
	msg := cmd()
	request, ok := msg.(claudeAPIKeyWarningRequestedMsg)
	if !ok {
		t.Fatalf("launch result = %T, want claudeAPIKeyWarningRequestedMsg", msg)
	}
	if launches != 0 {
		t.Fatalf("Claude factory calls = %d before acknowledgement, want 0", launches)
	}

	updated, followup := m.update(request)
	m = updated.(Model)
	if followup != nil {
		t.Fatal("warning request should not launch Claude before acknowledgement")
	}
	if m.claudeAPIKeyWarning == nil {
		t.Fatal("Claude API-key warning was not opened")
	}
	if m.claudeAPIKeyWarning.Selected != claudeAPIKeyWarningFocusCancel {
		t.Fatalf("default selection = %d, want cancel", m.claudeAPIKeyWarning.Selected)
	}
	rendered := ansi.Strip(m.renderClaudeAPIKeyWarningPanel(100))
	for _, want := range []string{
		"ANTHROPIC_API_KEY",
		"Pay-as-you-go charges may apply",
		"subscription limits",
		"until LCR restarts",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("warning panel missing %q:\n%s", want, rendered)
		}
	}

	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	updated, launchCmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if launchCmd == nil {
		t.Fatal("acknowledging warning returned no Claude launch command")
	}
	if m.claudeAPIKeyWarning != nil {
		t.Fatal("warning remained open after acknowledgement")
	}
	if !m.claudeAPIKeyWarningAcknowledged {
		t.Fatal("warning acknowledgement was not retained for the current LCR run")
	}
	msg = launchCmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("acknowledged launch result = %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("acknowledged launch error = %v", opened.err)
	}
	if launches != 1 {
		t.Fatalf("Claude factory calls after acknowledgement = %d, want 1", launches)
	}

	secondCmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: "/tmp/billing-warning-second",
	})
	if _, warnedAgain := secondCmd().(claudeAPIKeyWarningRequestedMsg); warnedAgain {
		t.Fatal("warning reappeared after acknowledgement in the same LCR run")
	}
	if launches != 2 {
		t.Fatalf("Claude factory calls after second launch = %d, want 2", launches)
	}
}

func TestClaudeAPIKeyWarningCancelDoesNotAcknowledgeOrReportFailure(t *testing.T) {
	launches := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		launches++
		return &fakeCodexSession{projectPath: req.ProjectPath}, nil
	})
	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		anthropicAPIKeyPresentFn: func() bool {
			return true
		},
	}
	projectPath := "/tmp/billing-warning-cancel"
	m.beginCodexPendingOpen(projectPath, codexapp.ProviderClaudeCode)
	m.storeTodoLaunchDraft(todoLaunchDraftState{
		projectPath: projectPath,
		provider:    codexapp.ProviderClaudeCode,
	})

	cmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: projectPath,
	})
	msg := cmd()
	request, ok := msg.(claudeAPIKeyWarningRequestedMsg)
	if !ok {
		t.Fatalf("launch result = %T, want warning request", msg)
	}
	updated, _ := m.applyClaudeAPIKeyWarningRequested(request)
	m = updated.(Model)
	updated, cancelCmd := m.updateClaudeAPIKeyWarningMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cancelCmd == nil {
		t.Fatal("canceling warning returned no pending-launch cleanup command")
	}
	msg = cancelCmd()
	canceled, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cancel result = %T, want codexSessionOpenedMsg", msg)
	}
	if !errors.Is(canceled.err, errClaudeAPIKeyLaunchCanceled) {
		t.Fatalf("cancel error = %v, want API-key warning cancellation", canceled.err)
	}
	if launches != 0 {
		t.Fatalf("Claude factory calls after cancel = %d, want 0", launches)
	}
	if m.claudeAPIKeyWarningAcknowledged {
		t.Fatal("canceling the warning incorrectly acknowledged it")
	}

	applied, _ := m.applyCodexSessionOpenedMsg(canceled)
	m = applied.(Model)
	if m.codexPendingOpen != nil {
		t.Fatalf("pending Claude open remained after cancel: %#v", m.codexPendingOpen)
	}
	if len(m.todoLaunchDrafts) != 0 {
		t.Fatalf("TODO launch draft remained after cancel: %#v", m.todoLaunchDrafts)
	}
	if m.err != nil || len(m.errorLogEntries) != 0 || m.actionNoticeDialog != nil {
		t.Fatalf("user cancellation was surfaced as a failure: err=%v log=%#v dialog=%#v", m.err, m.errorLogEntries, m.actionNoticeDialog)
	}

	nextCmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: "/tmp/billing-warning-next",
	})
	if _, ok := nextCmd().(claudeAPIKeyWarningRequestedMsg); !ok {
		t.Fatal("warning did not reappear after a canceled launch")
	}
}

func TestClaudeAPIKeyWarningSkipsUnsetKeyAndOtherProviders(t *testing.T) {
	tests := []struct {
		name       string
		provider   codexapp.Provider
		keyPresent bool
	}{
		{name: "Claude without API key", provider: codexapp.ProviderClaudeCode, keyPresent: false},
		{name: "Codex with Anthropic API key", provider: codexapp.ProviderCodex, keyPresent: true},
		{name: "OpenCode with Anthropic API key", provider: codexapp.ProviderOpenCode, keyPresent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			launches := 0
			m := Model{
				anthropicAPIKeyPresentFn: func() bool {
					return tc.keyPresent
				},
			}
			cmd := m.deferClaudeLaunchForAPIKeyWarning(
				tc.provider,
				"/tmp/no-warning",
				func() tea.Msg {
					launches++
					return "launched"
				},
				nil,
			)
			if got := cmd(); got != "launched" {
				t.Fatalf("launch result = %#v, want direct launch", got)
			}
			if launches != 1 {
				t.Fatalf("launch count = %d, want 1", launches)
			}
		})
	}
}

func TestClaudeAPIKeyWarningPreservesDeferredCommandDecorators(t *testing.T) {
	m := Model{
		anthropicAPIKeyPresentFn: func() bool {
			return true
		},
	}
	cmd := m.deferClaudeLaunchForAPIKeyWarning(
		codexapp.ProviderClaudeCode,
		"/tmp/decorated-warning",
		func() tea.Msg { return "launched" },
		func() tea.Msg { return "canceled" },
	)
	cmd = mapDeferredClaudeLaunchCommand(cmd, func(msg tea.Msg) tea.Msg {
		return "inner:" + msg.(string)
	})
	cmd = mapDeferredClaudeLaunchCommand(cmd, func(msg tea.Msg) tea.Msg {
		return "outer:" + msg.(string)
	})

	request, ok := cmd().(claudeAPIKeyWarningRequestedMsg)
	if !ok {
		t.Fatalf("decorated command did not preserve warning request")
	}
	updated, _ := m.applyClaudeAPIKeyWarningRequested(request)
	m = updated.(Model)
	updated, _ = m.updateClaudeAPIKeyWarningMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	_, launchCmd := m.updateClaudeAPIKeyWarningMode(tea.KeyMsg{Type: tea.KeyEnter})
	if got := launchCmd(); got != "outer:inner:launched" {
		t.Fatalf("decorated launch result = %#v, want nested decorators", got)
	}
}

func TestClaudeAPIKeyWarningDefersParallelConflictResolver(t *testing.T) {
	launches := 0
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		launches++
		return &fakeCodexSession{projectPath: req.ProjectPath}, nil
	})
	settings := config.EditableSettingsFromAppConfig(config.Default())
	project := model.ProjectSummary{
		Name:          "conflicted",
		Path:          "/tmp/claude-resolver-warning",
		PresentOnDisk: true,
		RepoConflict:  true,
	}
	m := Model{
		codexManager:           manager,
		settingsBaseline:       &settings,
		mergeConflictResolvers: make(map[string]mergeConflictResolverState),
		anthropicAPIKeyPresentFn: func() bool {
			return true
		},
	}

	updated, cmd := m.launchParallelMergeConflictResolverWithOptions(
		project.Path,
		project,
		codexapp.ProviderClaudeCode,
		embeddedLaunchOptions{forceNew: true},
		"",
	)
	m = updated.(Model)
	msg := cmd()
	request, ok := msg.(claudeAPIKeyWarningRequestedMsg)
	if !ok {
		t.Fatalf("parallel launch result = %T, want warning request", msg)
	}
	if launches != 0 {
		t.Fatalf("parallel Claude factory calls before warning = %d, want 0", launches)
	}
	updated, _ = m.applyClaudeAPIKeyWarningRequested(request)
	m = updated.(Model)
	updated, cancelCmd := m.updateClaudeAPIKeyWarningMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	msg = cancelCmd()
	canceled, ok := msg.(mergeConflictResolverOpenedMsg)
	if !ok {
		t.Fatalf("parallel cancel result = %T, want mergeConflictResolverOpenedMsg", msg)
	}
	applied, _ := m.applyMergeConflictResolverOpenedMsg(canceled)
	m = applied.(Model)
	if _, exists := m.mergeConflictResolverForProject(project.Path); exists {
		t.Fatal("canceled parallel resolver remained in starting state")
	}
	if launches != 0 {
		t.Fatalf("parallel Claude factory calls after cancel = %d, want 0", launches)
	}
	if len(m.errorLogEntries) != 0 {
		t.Fatalf("parallel user cancellation was logged as a failure: %#v", m.errorLogEntries)
	}
}
