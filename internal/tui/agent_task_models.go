package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func taskModelChoice(provider codexapp.Provider, selection control.EngineerModelSelection) model.AgentTaskModelSelection {
	return model.AgentTaskModelSelection{Provider: modelSessionSourceFromCodexProvider(provider), Model: selection.Model, ModelProvider: selection.ModelProvider, ReasoningEffort: selection.ReasoningEffort}
}

func taskControlModelChoice(choice model.AgentTaskModelSelection) control.EngineerModelSelection {
	return control.EngineerModelSelection{Model: choice.Model, ModelProvider: choice.ModelProvider, ReasoningEffort: choice.ReasoningEffort}
}

func inheritedTaskModelChoice(task model.AgentTask, provider codexapp.Provider, explicit control.EngineerModelSelection) (control.EngineerModelSelection, error) {
	if explicit.Model != "" {
		return explicit, nil
	}
	saved := task.ModelSelection
	if saved.Model == "" {
		return explicit, nil
	}
	if saved.Provider != modelSessionSourceFromCodexProvider(provider) {
		return control.EngineerModelSelection{}, fmt.Errorf("switching this task's engineer provider requires an explicit model choice; query engineer.models")
	}
	return taskControlModelChoice(saved), nil
}

func taskObservedModel(snapshot codexapp.Snapshot) model.AgentTaskModelSelection {
	// A staged choice has not yet been used; do not report the old model as the
	// effective choice for the next worker turn.
	if snapshot.Model == "" || snapshot.PendingModel != "" || snapshot.PendingReasoning != "" {
		return model.AgentTaskModelSelection{}
	}
	return model.AgentTaskModelSelection{Provider: embeddedSessionSource(snapshot.Provider), Model: firstNonEmptyTrimmed(snapshot.ReportedModel, snapshot.Model), ModelProvider: snapshot.ModelProvider, ReasoningEffort: snapshot.ReasoningEffort}
}

func (m Model) prepareAgentTaskModelCmd(task model.AgentTask, cmd tea.Cmd) tea.Cmd {
	if m.svc == nil || cmd == nil {
		return cmd
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		empty := model.AgentTaskModelSelection{}
		if _, err := m.svc.Store().UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, ModelSelection: &task.ModelSelection, ObservedModel: &empty}); err != nil {
			return codexSessionOpenedMsg{projectPath: task.WorkspacePath, err: err}
		}
		return cmd()
	}
}

func applyTaskModelToLaunch(req codexapp.LaunchRequest, task model.AgentTask) codexapp.LaunchRequest {
	choice := task.ModelSelection
	if req.PendingModel != "" || choice.Model == "" || choice.Provider != modelSessionSourceFromCodexProvider(req.Provider) {
		return req
	}
	req.PendingModel, req.PendingReasoning = choice.Model, choice.ReasoningEffort
	if req.Provider == codexapp.ProviderLCAgent {
		req.LCAgentRoutePreset = ""
		if choice.ModelProvider != "" {
			req.LCAgentProvider = choice.ModelProvider
		}
	}
	return req
}

func taskModelLabel(choice model.AgentTaskModelSelection) string {
	if choice.Model == "" {
		return ""
	}
	label := codexapp.ModelDisplayName(codexProviderFromSessionSource(choice.Provider), choice.Model)
	parts := []string{label}
	if label != choice.Model {
		parts = append(parts, choice.Model)
	}
	if choice.ModelProvider != "" {
		parts = append(parts, "via "+choice.ModelProvider)
	}
	if choice.ReasoningEffort != "" {
		parts = append(parts, choice.ReasoningEffort+" reasoning")
	}
	return strings.Join(parts, " · ")
}

func (m Model) persistAgentTaskModelChoiceCmd(task model.AgentTask) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		_, err := m.svc.Store().UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, ModelSelection: &task.ModelSelection, ObservedModel: &task.ObservedModel})
		return projectStatusRefreshedMsg{projectPath: task.WorkspacePath, err: err}
	}
}
