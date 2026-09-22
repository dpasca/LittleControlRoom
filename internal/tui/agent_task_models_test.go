package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestTaskModelChoiceSurvivesLaunchAndRestart(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := t.Context()
			svc := newControlTestService(t)
			selection := control.EngineerModelSelection{Model: "worker-cheap", ReasoningEffort: "low"}
			if provider == codexapp.ProviderLCAgent {
				selection.ModelProvider = "zai"
			}
			catalog := control.EngineerModelCatalog{Provider: controlProviderFromCodexProvider(provider), Source: "test", ObservedAt: time.Now(), Models: []control.EngineerModel{{Model: selection.Model, ModelProvider: selection.ModelProvider, ReasoningEfforts: []string{"low"}}}}
			if err := svc.Store().SaveEngineerModelCatalog(ctx, catalog); err != nil {
				t.Fatal(err)
			}
			var requests []codexapp.LaunchRequest
			factory := func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
				tasks, err := svc.Store().ListAgentTasks(ctx, model.AgentTaskFilter{})
				if err != nil || len(tasks) != 1 || tasks[0].ModelSelection.Model != selection.Model {
					t.Fatalf("choice not durable before launch: %#v, %v", tasks, err)
				}
				requests = append(requests, req)
				return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: provider, ThreadID: "worker-thread", Started: true, LastActivityAt: time.Now(), Model: "reported-worker-model", ReasoningEffort: "low", ModelProvider: selection.ModelProvider}}, nil
			}
			manager := codexapp.NewManagerWithFactory(factory)
			repo := t.TempDir()
			m := Model{ctx: ctx, svc: svc, codexManager: manager, embeddedModelPrefs: map[codexapp.Provider]embeddedModelPreference{provider: {Model: "expensive-global", Reasoning: "high"}}}
			input := control.AgentTaskCreateInput{EngineerModelSelection: selection, Title: "Visible cheap worker", Kind: control.AgentTaskKindAgent, Provider: controlProviderFromCodexProvider(provider), Prompt: "Implement the bounded change", Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: repo}}}
			inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskCreate, input)
			outcome := m.executeControlInvocationWithOutcome(inv)
			if outcome.err != nil || outcome.cmd == nil {
				t.Fatalf("selection entry: %#v", outcome)
			}
			validated, ok := outcome.cmd().(controlEngineerModelValidatedMsg)
			if !ok || validated.err != nil {
				t.Fatalf("validation: %#v", validated)
			}
			updated, cmd := outcome.model.applyControlEngineerModelValidated(validated)
			m = updated.(Model)
			created, ok := cmd().(bossAgentTaskCreatedMsg)
			if !ok || created.err != nil {
				t.Fatalf("create: %#v", created)
			}
			updated, cmd = m.applyBossAgentTaskCreated(created)
			m = updated.(Model)
			assertTaskLaunchSucceeded(t, cmd)
			if len(requests) != 1 {
				t.Fatalf("launches = %d", len(requests))
			}
			req := requests[0]
			if req.PendingModel != selection.Model || req.PendingReasoning != "low" {
				t.Fatalf("selection lost: %#v", req)
			}
			if provider == codexapp.ProviderLCAgent && (req.LCAgentProvider != "zai" || req.LCAgentRoutePreset != "" || len(req.LCAgentWritableRoots) != 1 || req.LCAgentWritableRoots[0] != repo) {
				t.Fatalf("LCAgent route/root lost: %#v", req)
			}
			task, err := svc.GetAgentTask(ctx, created.task.ID)
			if err != nil || task.ModelSelection.Model != selection.Model || task.ObservedModel.Model != "reported-worker-model" {
				t.Fatalf("requested/reported choice = %#v, %v", task, err)
			}
			manager.CloseProject(task.WorkspacePath)
			cfg := svc.Config()
			svc.Store().Close()
			reopened, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			svc = service.New(cfg, reopened, events.NewBus(), nil)
			task, err = svc.GetAgentTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			manager = codexapp.NewManagerWithFactory(factory)
			m = Model{ctx: ctx, svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task}, embeddedModelPrefs: map[codexapp.Provider]embeddedModelPreference{provider: {Model: "even-more-expensive", Reasoning: "high"}}}
			continuation := control.AgentTaskContinueInput{TaskID: task.ID, Provider: control.ProviderAuto, SessionMode: control.SessionModeResumeOrNew, Prompt: "Fix the review finding"}
			inv = controlInvocationRawForTest(t, control.CapabilityAgentTaskContinue, continuation)
			outcome = m.executeControlInvocationWithOutcome(inv)
			loaded, ok := outcome.cmd().(bossAgentTaskContinueLoadedMsg)
			if !ok || loaded.err != nil {
				t.Fatalf("load inherited choice: %#v", loaded)
			}
			if loaded.input.Model != selection.Model {
				t.Fatalf("did not inherit: %#v", loaded.input)
			}
			updated, cmd = outcome.model.applyBossAgentTaskContinueLoaded(loaded)
			m = updated.(Model)
			assertTaskLaunchSucceeded(t, cmd)
			if len(requests) != 2 || requests[1].PendingModel != selection.Model || requests[1].PendingReasoning != "low" || requests[1].ResumeID != "worker-thread" || requests[1].ForceNew {
				t.Fatalf("resume did not retain selection: %#v", requests)
			}
			if provider == codexapp.ProviderLCAgent && (requests[1].LCAgentProvider != "zai" || len(requests[1].LCAgentWritableRoots) != 1 || requests[1].LCAgentWritableRoots[0] != repo) {
				t.Fatal("resume lost scoped LCAgent access")
			}
			if m.embeddedModelPrefs[provider].Model != "even-more-expensive" {
				t.Fatal("task choice changed global preferences")
			}
			// Reopening the row must also prefer the durable task choice.
			req = m.enrichEmbeddedLaunchRequest(codexapp.LaunchRequest{Provider: provider, ProjectPath: task.WorkspacePath, ResumeID: "worker-thread"})
			if req.PendingModel != selection.Model || req.PendingReasoning != "low" {
				t.Fatal("reopen inherited global defaults")
			}
			manager.CloseProject(task.WorkspacePath)
		})
	}
}

func assertTaskLaunchSucceeded(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	found := false
	for _, msg := range collectCmdMsgs(cmd) {
		if result, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			found = true
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		}
	}
	if !found {
		t.Fatal("launch did not produce a control receipt")
	}
}

func TestTaskModelValidationFailsBeforeWorkspaceCreation(t *testing.T) {
	svc := newControlTestService(t)
	m := Model{ctx: t.Context(), svc: svc}
	input := control.AgentTaskCreateInput{EngineerModelSelection: control.EngineerModelSelection{Model: "nonexistent-model"}, Title: "Rejected worker", Kind: control.AgentTaskKindAgent, Provider: control.ProviderCodex, Prompt: "work"}
	outcome := m.executeControlInvocationWithOutcome(controlInvocationRawForTest(t, control.CapabilityAgentTaskCreate, input))
	validated, ok := outcome.cmd().(controlEngineerModelValidatedMsg)
	if !ok || validated.err == nil {
		t.Fatalf("unknown model accepted: %#v", validated)
	}
	tasks, err := svc.Store().ListAgentTasks(t.Context(), model.AgentTaskFilter{})
	if err != nil || len(tasks) != 0 {
		t.Fatalf("invalid choice created tasks: %#v,%v", tasks, err)
	}
}

func TestTaskProviderSwitchNeedsNewModelAndRejectsStaleReports(t *testing.T) {
	svc := newControlTestService(t)
	ctx := t.Context()
	saved := model.AgentTaskModelSelection{Provider: model.SessionSourceCodex, Model: "cheap-original", ReasoningEffort: "low"}
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "Worker", ModelSelection: saved, Provider: model.SessionSourceCodex, SessionID: "old-thread"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inheritedTaskModelChoice(task, codexapp.ProviderClaudeCode, control.EngineerModelSelection{}); err == nil {
		t.Fatal("provider switch reused foreign model/defaults")
	}
	explicit := control.EngineerModelSelection{Model: "new-cheap", ReasoningEffort: "low"}
	if got, err := inheritedTaskModelChoice(task, codexapp.ProviderClaudeCode, explicit); err != nil || got != explicit {
		t.Fatalf("explicit switch: %#v,%v", got, err)
	}
	if _, err := svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceClaudeCode, "new-thread"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store().RecordAgentTaskObservedModel(ctx, task.ID, "old-thread", saved); err != nil {
		t.Fatal(err)
	}
	task, err = svc.GetAgentTask(ctx, task.ID)
	if err != nil || task.ObservedModel.Model != "" {
		t.Fatalf("stale report won: %#v,%v", task, err)
	}
}

func TestTaskModelPickerCancellationDoesNotStartWork(t *testing.T) {
	for _, capability := range []control.CapabilityName{control.CapabilityAgentTaskCreate, control.CapabilityAgentTaskContinue} {
		var payload any
		if capability == control.CapabilityAgentTaskCreate {
			payload = control.AgentTaskCreateInput{EngineerModelSelection: control.EngineerModelSelection{SelectModel: true}, Title: "Pick worker", Kind: control.AgentTaskKindAgent, Provider: control.ProviderCodex, Prompt: "work"}
		} else {
			payload = control.AgentTaskContinueInput{EngineerModelSelection: control.EngineerModelSelection{SelectModel: true}, TaskID: "agt_existing", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "work"}
		}
		m := Model{}
		outcome := m.executeControlInvocationWithOutcome(controlInvocationRawForTest(t, capability, payload))
		if outcome.err != nil || outcome.model.codexModelPicker == nil || !outcome.deferBossResult {
			t.Fatalf("picker didn't pause: %#v", outcome)
		}
		updated, cmd := outcome.model.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEsc})
		if updated.(Model).codexModelPicker != nil || cmd == nil {
			t.Fatal("picker remained pending")
		}
		if result, ok := cmd().(bossui.ControlInvocationResultMsg); !ok || result.Err == nil {
			t.Fatalf("cancel missing receipt: %#v", result)
		}
	}
}

func TestTaskModelContinuationFailsWhenSavedChoiceDisappears(t *testing.T) {
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Worker", Provider: model.SessionSourceCodex, ModelSelection: model.AgentTaskModelSelection{Provider: model.SessionSourceCodex, Model: "removed-cheap-model", ReasoningEffort: "low"}})
	if err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: t.Context(), svc: svc}
	input := control.AgentTaskContinueInput{TaskID: task.ID, Provider: control.ProviderAuto, SessionMode: control.SessionModeResumeOrNew, Prompt: "continue"}
	msg := m.loadBossAgentTaskContinueCmd(control.Invocation{}, input)().(bossAgentTaskContinueLoadedMsg)
	if msg.err == nil {
		t.Fatal("unavailable saved model silently fell back")
	}
	retained, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || retained.ModelSelection != task.ModelSelection {
		t.Fatal("failed validation replaced saved selection")
	}
}

func TestTaskModelChoiceDoesNotSteerAnActiveTurn(t *testing.T) {
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Busy worker", Provider: model.SessionSourceCodex, SessionID: "worker-thread"})
	if err != nil {
		t.Fatal(err)
	}
	session := &fakeCodexSession{projectPath: task.WorkspacePath, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "worker-thread", Started: true, Busy: true, ActiveTurnID: "active-turn"}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return session, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: t.Context(), svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task}}
	_, cmd := m.applyBossAgentTaskContinueLoaded(bossAgentTaskContinueLoadedMsg{task: task, input: control.AgentTaskContinueInput{TaskID: task.ID, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "next", EngineerModelSelection: control.EngineerModelSelection{Model: "cheap", ReasoningEffort: "low"}}})
	result, ok := cmd().(bossui.ControlInvocationResultMsg)
	if !ok || result.Err == nil || len(session.submitted) != 0 || len(session.modelStages) != 0 {
		t.Fatalf("model choice steered active turn: %#v", result)
	}
}

func TestTaskModelLabelNamesClaudeVersionAndKeepsID(t *testing.T) {
	label := taskModelLabel(model.AgentTaskModelSelection{Provider: model.SessionSourceClaudeCode, Model: "claude-opus-5-5[1m]", ReasoningEffort: "high"})
	if label != "Opus 5.5 (1M) · claude-opus-5-5[1m] · high reasoning" {
		t.Fatalf("Claude task model label = %q", label)
	}
	label = taskModelLabel(model.AgentTaskModelSelection{Provider: model.SessionSourceCodex, Model: "gpt-5.5"})
	if label != "gpt-5.5" {
		t.Fatalf("Codex task model label = %q, want raw ID", label)
	}
}
