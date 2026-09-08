package tui

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

type controlEngineerModelValidatedMsg struct {
	inv control.Invocation
	err error
}

func controlEngineerSelection(inv control.Invocation) (control.EngineerModelSelection, codexapp.Provider, bool) {
	switch inv.Capability {
	case control.CapabilityEngineerSendPrompt, control.CapabilityProjectCreateAndStartEngineer, control.CapabilityTodoCreateWorktreeAndStartEngineer:
		var args struct {
			control.EngineerModelSelection
			Provider control.Provider `json:"provider"`
		}
		if json.Unmarshal(inv.Args, &args) == nil {
			return args.EngineerModelSelection, codexProviderFromControlProvider(args.Provider), true
		}
	}
	return control.EngineerModelSelection{}, "", false
}

func engineerCatalog(provider codexapp.Provider, models []codexapp.ModelOption, source string) control.EngineerModelCatalog {
	catalog := control.EngineerModelCatalog{Provider: controlProviderFromCodexProvider(provider), Source: source, ObservedAt: time.Now(), Models: []control.EngineerModel{}}
	for _, option := range models {
		if option.Model == "" || option.Hidden {
			continue
		}
		item := control.EngineerModel{Model: option.Model, ModelProvider: option.ModelProvider, DisplayName: option.DisplayName, DefaultReasoningEffort: option.DefaultReasoningEffort, IsDefault: option.IsDefault, ReasoningEfforts: []string{}}
		for _, effort := range option.SupportedReasoningEfforts {
			item.ReasoningEfforts = append(item.ReasoningEfforts, effort.ReasoningEffort)
		}
		catalog.Models = append(catalog.Models, item)
	}
	return catalog
}

func (m Model) saveEngineerCatalog(ctx context.Context, provider codexapp.Provider, models []codexapp.ModelOption, source string) {
	if m.svc == nil || m.svc.Store() == nil || len(models) == 0 {
		return
	}
	// Catalog persistence is best effort; never make opening a session depend on it.
	_ = m.svc.Store().SaveEngineerModelCatalog(ctx, engineerCatalog(provider, models, source))
}

func engineerCatalogSource(provider codexapp.Provider) string {
	switch provider {
	case codexapp.ProviderClaudeCode:
		return "built_in_aliases_and_session"
	case codexapp.ProviderLCAgent:
		return "provider_listing_and_curated_routes"
	default:
		return "provider_listing"
	}
}

func (m *Model) refreshEngineerCatalogCmd(projectPath string, provider codexapp.Provider) tea.Cmd {
	if m.engineerModelCatalogRefresh == nil {
		m.engineerModelCatalogRefresh = make(map[codexapp.Provider]time.Time)
	}
	if last := m.engineerModelCatalogRefresh[provider]; !last.IsZero() && time.Since(last) < time.Minute {
		return nil
	}
	m.engineerModelCatalogRefresh[provider] = time.Now()

	manager := m.codexManager
	modelSnapshot := *m
	return func() tea.Msg {
		if manager == nil {
			return nil
		}
		session, ok := manager.Session(projectPath)
		if !ok {
			return nil
		}
		models, err := session.ListModels()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			modelSnapshot.saveEngineerCatalog(ctx, provider, models, engineerCatalogSource(provider))
		}
		return nil
	}
}

func (m Model) validateControlEngineerModelCmd(inv control.Invocation, provider codexapp.Provider, selection control.EngineerModelSelection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := m.prelaunchEmbeddedModelOptions(ctx, provider)
		if err == nil {
			err = engineerCatalog(provider, models, "prelaunch").Validate(selection)
		}
		return controlEngineerModelValidatedMsg{inv: inv, err: err}
	}
}

func (m Model) applyControlEngineerModelValidated(msg controlEngineerModelValidatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "Model selection failed: " + msg.err.Error()
		return m, bossControlResultCmd(msg.inv, m.status, msg.err)
	}
	outcome := m.executeValidatedControlInvocation(msg.inv)
	m = outcome.model
	inv := msg.inv
	if outcome.inv.Capability != "" {
		inv = outcome.inv
	}
	if outcome.cmd == nil {
		return m, bossControlResultCmd(inv, m.status, outcome.err)
	}
	if outcome.deferBossResult || inv.Capability == control.CapabilityProjectCreateAndStartEngineer || inv.Capability == control.CapabilityTodoCreateWorktreeAndStartEngineer {
		return m, outcome.cmd
	}
	return m, bossControlExecutionCmd(inv, outcome.cmd)
}

func (m Model) applyControlModelPickerSelection(option codexapp.ModelOption, effort string) (tea.Model, tea.Cmd) {
	inv := *m.codexModelPicker.ControlInvocation
	var args map[string]any
	if err := json.Unmarshal(inv.Args, &args); err != nil {
		return m.cancelControlModelPicker(err)
	}
	args["select_model"] = false
	args["model"] = option.Model
	args["model_provider"] = option.ModelProvider
	args["reasoning_effort"] = effort
	inv.Args, _ = json.Marshal(args)
	m.closeCodexModelPicker("")
	// Choices came directly from this picker; do not refetch a different catalog.
	normalized, err := control.ValidateInvocation(inv)
	if err != nil {
		return m, bossControlResultCmd(inv, err.Error(), err)
	}
	return m.applyControlEngineerModelValidated(controlEngineerModelValidatedMsg{inv: normalized})
}

func (m Model) cancelControlModelPicker(err error) (tea.Model, tea.Cmd) {
	inv := *m.codexModelPicker.ControlInvocation
	m.closeCodexModelPicker("Engineer launch canceled: " + err.Error())
	return m, bossControlResultCmd(inv, m.status, err)
}

var errControlModelPickerCanceled = errors.New("model selection canceled")
