package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
)

func TestExecuteControlEngineerSendPromptRoutesOpenCodeHidden(t *testing.T) {
	projectPath := "/tmp/control-opencode"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-1",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-opencode",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderOpenCode,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "please run the next step",
		Reveal:      false,
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	if got.codexPendingOpen == nil {
		t.Fatalf("codexPendingOpen = nil, want hidden pending open")
	}
	if got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("pending provider = %q, want opencode", got.codexPendingOpen.provider)
	}
	if got.codexPendingOpen.showWhilePending {
		t.Fatalf("showWhilePending = true, want hidden background open")
	}
	if !got.codexPendingOpen.hideOnOpen {
		t.Fatalf("hideOnOpen = false, want background result to stay hidden")
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.projectPath == "" {
		t.Fatalf("command messages = %#v, want codexSessionOpenedMsg", msgs)
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("request provider = %q, want opencode", requests[0].Provider)
	}
	if requests[0].ForceNew {
		t.Fatalf("request ForceNew = true, want resume_or_new")
	}
	if requests[0].Prompt != "please run the next step" {
		t.Fatalf("request Prompt = %q, want control prompt", requests[0].Prompt)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != projectPath {
		t.Fatalf("codexHiddenProject = %q, want %q", got.codexHiddenProject, projectPath)
	}
}

func TestExecuteControlEngineerSendPromptAutoProviderUsesProjectPreference(t *testing.T) {
	projectPath := "/tmp/control-auto"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-2",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:                projectPath,
			Name:                "Control Auto",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectName: "control auto",
		Provider:    control.ProviderAuto,
		SessionMode: control.SessionModeNew,
		Prompt:      "start fresh please",
		Reveal:      true,
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("executeControlInvocation() cmd = nil, want embedded open command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("request provider = %q, want opencode from project preference", requests[0].Provider)
	}
	if !requests[0].ForceNew {
		t.Fatalf("request ForceNew = false, want new session")
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != projectPath {
		t.Fatalf("codexVisibleProject = %q, want %q", got.codexVisibleProject, projectPath)
	}
}

func TestExecuteControlEngineerSendPromptRejectsDisabledClaudeCode(t *testing.T) {
	projectPath := "/tmp/control-claude"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-claude",
			PresentOnDisk: true,
		}},
	}

	updated, cmd := m.executeControlInvocation(controlInvocationForTest(t, control.EngineerSendPromptInput{
		ProjectPath: projectPath,
		Provider:    control.ProviderClaudeCode,
		SessionMode: control.SessionModeResumeOrNew,
		Prompt:      "try claude",
		Reveal:      true,
	}))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("executeControlInvocation() cmd = %#v, want nil for disabled provider", cmd)
	}
	if !strings.Contains(got.status, "Claude Code") || !strings.Contains(got.status, "disabled") {
		t.Fatalf("status = %q, want disabled Claude Code message", got.status)
	}
}

func TestExecuteBossControlInvocationBatchesOpenAndBossResult(t *testing.T) {
	projectPath := "/tmp/control-boss-result"
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       "oc-session-result",
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "control-boss-result",
			PresentOnDisk: true,
		}},
		codexManager: manager,
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderOpenCode,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "please run the next step",
			Reveal:      false,
		}),
	})
	_ = updated.(Model)
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want wrapped open command")
	}
	msgs := collectCmdMsgs(cmd)
	var sawOpen bool
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case codexSessionOpenedMsg:
			sawOpen = true
		case bossui.ControlInvocationResultMsg:
			result = typed
		}
	}
	if !sawOpen {
		t.Fatalf("wrapped command messages = %#v, want codexSessionOpenedMsg", msgs)
	}
	if result.Invocation.Capability != control.CapabilityEngineerSendPrompt {
		t.Fatalf("result invocation = %#v", result.Invocation)
	}
	if result.Err != nil {
		t.Fatalf("result err = %v", result.Err)
	}
	if !strings.Contains(result.Status, "Prompt sent to embedded OpenCode") {
		t.Fatalf("result status = %q", result.Status)
	}
}

func TestExecuteBossControlInvocationReportsBlockedLaunch(t *testing.T) {
	projectPath := "/tmp/control-boss-blocked"
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:                     projectPath,
			Name:                     "control-boss-blocked",
			PresentOnDisk:            true,
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: time.Now(),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
	}

	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{
		Invocation: controlInvocationForTest(t, control.EngineerSendPromptInput{
			ProjectPath: projectPath,
			Provider:    control.ProviderOpenCode,
			SessionMode: control.SessionModeResumeOrNew,
			Prompt:      "please run the next step",
			Reveal:      false,
		}),
	})
	got := updated.(Model)
	if got.attentionDialog != nil {
		t.Fatalf("attentionDialog = %#v, want boss control failure to stay in boss transcript", got.attentionDialog)
	}
	if cmd == nil {
		t.Fatalf("executeBossControlInvocation() cmd = nil, want immediate result")
	}
	msgs := collectCmdMsgs(cmd)
	var result bossui.ControlInvocationResultMsg
	for _, msg := range msgs {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			result = typed
			break
		}
	}
	if result.Err == nil {
		t.Fatalf("result err = nil, want blocked launch error")
	}
	if !strings.Contains(result.Status, "unfinished Codex session") {
		t.Fatalf("result status = %q, want unfinished Codex session", result.Status)
	}
}

func controlInvocationForTest(t *testing.T, input control.EngineerSendPromptInput) control.Invocation {
	t.Helper()
	if input.Provider == "" {
		input.Provider = control.ProviderAuto
	}
	if input.SessionMode == "" {
		input.SessionMode = control.SessionModeResumeOrNew
	}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal control input: %v", err)
	}
	return control.Invocation{
		RequestID:  "test-request",
		Capability: control.CapabilityEngineerSendPrompt,
		Args:       args,
	}
}
