package tui

import (
	"encoding/json"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func TestControlModelPickerPausesBeforeWorkAndCancels(t *testing.T) {
	for _, capability := range []control.CapabilityName{control.CapabilityEngineerSendPrompt, control.CapabilityTodoCreateWorktreeAndStartEngineer, control.CapabilityProjectCreateAndStartEngineer} {
		args := map[string]any{"provider": "codex", "project_path": "/repo", "project_name": "repo", "prompt": "work", "reveal": true, "select_model": true}
		if capability == control.CapabilityEngineerSendPrompt {
			args["session_mode"] = "new"
		} else {
			args["todo_text"] = "work"
		}
		if capability == control.CapabilityProjectCreateAndStartEngineer {
			args["parent_path"] = "/"
		}
		raw, _ := json.Marshal(args)
		inv := control.Invocation{Capability: capability, Args: raw}
		m := Model{}
		outcome := m.executeControlInvocationWithOutcome(inv)
		if outcome.err != nil || !outcome.deferBossResult || outcome.model.codexModelPicker == nil || outcome.cmd == nil {
			t.Fatalf("picker did not pause work: %+v", outcome)
		}
		// No service or project exists: reaching a launch would have failed.
		updated, cmd := outcome.model.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEsc})
		if updated.(Model).codexModelPicker != nil || cmd == nil {
			t.Fatal("cancel did not finish picker")
		}
		result, ok := cmd().(bossui.ControlInvocationResultMsg)
		if !ok || result.Err == nil {
			t.Fatalf("cancel lacked failure receipt: %#v", result)
		}
	}
}

func TestControlPickerChoiceReachesMailboxAndLaunch(t *testing.T) {
	projectPath := "/repo"
	var request codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		request = req
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: req.Provider, ThreadID: "new-thread", Started: true}}, nil
	})
	m := Model{svc: newControlTestService(t), codexManager: manager, allProjects: []model.ProjectSummary{{Path: projectPath, Name: "repo", PresentOnDisk: true}}, embeddedModelPrefs: map[codexapp.Provider]embeddedModelPreference{codexapp.ProviderCodex: {Model: "wrong-default", Reasoning: "low"}}}
	inv := controlInvocationForTest(t, control.EngineerSendPromptInput{EngineerModelSelection: control.EngineerModelSelection{SelectModel: true}, ProjectPath: projectPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeNew, Prompt: "work"})
	outcome := m.executeControlInvocationWithOutcome(inv)
	m = outcome.model
	option := codexapp.ModelOption{ID: "exact-id", Model: "exact-id", SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{{ReasoningEffort: "medium"}}}
	m.openLoadedCodexModelPicker([]codexapp.ModelOption{option})
	if m.codexModelPicker.ControlInvocation == nil {
		t.Fatal("loading lost pending operation")
	}
	updated, cmd := m.applyControlModelPickerSelection(option, "medium")
	_, _ = runEngineerMailboxForTest(t, updated.(Model), cmd)
	if request.PendingModel != "exact-id" || request.PendingReasoning != "medium" {
		t.Fatalf("launch lost selection: %+v", request)
	}
}

func TestControlModelValidationRejectsBeforeMutation(t *testing.T) {
	m := Model{}
	inv := controlInvocationForTest(t, control.EngineerSendPromptInput{EngineerModelSelection: control.EngineerModelSelection{Model: "unknown-id"}, ProjectPath: "/repo", Provider: control.ProviderCodex, SessionMode: control.SessionModeNew, Prompt: "work"})
	outcome := m.executeControlInvocationWithOutcome(inv)
	if outcome.cmd == nil || !outcome.deferBossResult {
		t.Fatal("validation must be asynchronous")
	}
	msg := outcome.cmd().(controlEngineerModelValidatedMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "unknown-id") {
		t.Fatalf("invalid model accepted: %v", msg.err)
	}
	_, cmd := m.applyControlEngineerModelValidated(msg)
	if cmd == nil || cmd().(bossui.ControlInvocationResultMsg).Err == nil {
		t.Fatal("validation needs an explicit failed receipt")
	}
}

func TestModelChoiceWaitsForActiveCodexTurn(t *testing.T) {
	projectPath := "/repo"
	session := &fakeCodexSession{projectPath: projectPath, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "thread", Started: true, Busy: true, ActiveTurnID: "turn", Phase: codexapp.SessionPhaseRunning}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return session, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	m := Model{codexManager: manager, allProjects: []model.ProjectSummary{{Path: projectPath, PresentOnDisk: true}}}
	message := control.EngineerMessage{EngineerModelSelection: control.EngineerModelSelection{Model: "exact-id", ReasoningEffort: "medium"}, ProjectPath: projectPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "thread"}
	disposition := m.engineerMessageDisposition(message)
	if !disposition.wait || disposition.deliver {
		t.Fatalf("model-changing prompt would steer old model: %+v", disposition)
	}
}

func TestControlModelPickerLoadFailureFinishesOperation(t *testing.T) {
	inv := controlInvocationForTest(t, control.EngineerSendPromptInput{EngineerModelSelection: control.EngineerModelSelection{SelectModel: true}, ProjectPath: "/repo", Provider: control.ProviderCodex, SessionMode: control.SessionModeNew, Prompt: "work"})
	m := Model{}
	outcome := m.executeControlInvocationWithOutcome(inv)
	m = outcome.model
	updated, cmd := m.applyCodexModelListMsg(codexModelListMsg{target: codexModelPickerTargetControl, provider: codexapp.ProviderCodex, err: errControlModelPickerCanceled})
	if updated.(Model).codexModelPicker != nil || cmd == nil || cmd().(bossui.ControlInvocationResultMsg).Err == nil {
		t.Fatal("failed discovery left an unfinished operation")
	}
}
