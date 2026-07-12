package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// settingsLCAgentPickerRow represents a single display row in the model picker.
// A row is either a provider section header or a model option.
type settingsLCAgentPickerRow struct {
	IsHeader   bool
	Provider   string // display label for header rows
	ModelIndex int    // index into FilteredModels; -1 for headers
}

type settingsLCAgentModelPickerStep int

const (
	settingsLCAgentModelPickerStepProvider settingsLCAgentModelPickerStep = iota
	settingsLCAgentModelPickerStepAPIKey
	settingsLCAgentModelPickerStepModel
	settingsLCAgentModelPickerStepReasoning
)

type settingsLCAgentModelPickerState struct {
	FieldIndex         int
	EmbeddedApply      bool
	EmbeddedProject    string
	Step               settingsLCAgentModelPickerStep
	Provider           string
	CurrentProvider    string
	ProviderOptions    []settingsLCAgentProviderOption
	ProviderSelected   int
	APIKeyInput        textinput.Model
	BaseURLInput       textinput.Model
	ConnectionSelected int
	APIKeyProvider     string
	Current            string
	Models             []codexapp.ModelOption
	FilteredModels     []codexapp.ModelOption
	Rows               []settingsLCAgentPickerRow
	FilterInput        textinput.Model
	Selected           int
	PendingModel       string
	PendingModelAuto   bool
	PendingModelOption codexapp.ModelOption
	CurrentReasoning   string
	PendingReasoning   string
	ReasoningSelected  int
	Loading            bool
	Err                string
}

type settingsLCAgentModelListMsg struct {
	fieldIndex int
	provider   string
	current    string
	models     []codexapp.ModelOption
	err        error
}

func settingsFieldUsesLCAgentModelPicker(index int) bool {
	return index == settingsFieldLCAgentModel ||
		index == settingsFieldLCAgentUtilityModel ||
		index == settingsFieldLCAgentVisionModel
}

func settingsFieldUsesProjectCloudModelPicker(index int) bool {
	return index == settingsFieldOpenRouterModel ||
		index == settingsFieldDeepSeekModel ||
		index == settingsFieldMoonshotModel ||
		index == settingsFieldXiaomiModel
}

func settingsFieldUsesBossCloudModelPicker(index int) bool {
	return index == settingsFieldBossChatModel ||
		index == settingsFieldBossUtilityModel
}

func settingsFieldUsesAnyCloudModelPicker(index int) bool {
	return settingsFieldUsesLCAgentModelPicker(index) ||
		settingsFieldUsesProjectCloudModelPicker(index) ||
		settingsFieldUsesBossCloudModelPicker(index)
}

func settingsFieldUsesUnifiedCloudModelPicker(index int) bool {
	return settingsFieldUsesLCAgentModelPicker(index) ||
		settingsFieldUsesProjectCloudModelPicker(index)
}

func (m Model) settingsFieldUsesUnifiedCloudModelPicker(index int) bool {
	if settingsFieldUsesUnifiedCloudModelPicker(index) {
		return true
	}
	if !settingsFieldUsesBossCloudModelPicker(index) {
		return false
	}
	settings := m.settingsDraftForInferenceStatus()
	return settingsCloudModelProviderForBackend(settings.BossChatBackend) != "" ||
		(settings.BossChatBackend == config.AIBackendUnset && strings.TrimSpace(settings.OpenAIAPIKey) != "")
}

func newSettingsLCAgentModelPickerFilterInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "type to filter"
	input.CharLimit = 256
	input.Focus()
	return input
}

func newSettingsLCAgentModelPickerAPIKeyInput(provider, value string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Paste " + settingsLCAgentModelPickerProviderLabel(provider) + " API key"
	input.CharLimit = 512
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '*'
	input.SetValue(strings.TrimSpace(value))
	input.Focus()
	input.CursorEnd()
	return input
}

func newSettingsLCAgentModelPickerBaseURLInput(provider, value string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = settingsModelPickerBaseURLPlaceholder(provider)
	input.CharLimit = 512
	input.SetValue(strings.TrimSpace(value))
	input.CursorEnd()
	return input
}

func (m Model) openSettingsLCAgentModelPicker() (tea.Model, tea.Cmd) {
	fieldIndex := m.settingsSelected
	if m.setupMode && m.setupConfigMode {
		fieldIndex = m.setupSelectedConfigFieldIndex()
	}
	if !m.settingsFieldUsesUnifiedCloudModelPicker(fieldIndex) {
		return m, nil
	}
	settings := m.settingsDraftForInferenceStatus()
	provider := settingsLCAgentModelPickerProvider(settings, fieldIndex)
	current := settingsLCAgentModelPickerRawModel(settings, fieldIndex)
	providerOptions := settingsLCAgentModelPickerProviderOptions(fieldIndex)
	m.settingsLCAgentModelPicker = &settingsLCAgentModelPickerState{
		FieldIndex:       fieldIndex,
		Step:             settingsLCAgentModelPickerStepProvider,
		Provider:         provider,
		CurrentProvider:  provider,
		ProviderOptions:  providerOptions,
		ProviderSelected: settingsLCAgentModelPickerProviderSelection(providerOptions, provider),
		Current:          current,
		FilterInput:      newSettingsLCAgentModelPickerFilterInput(),
		CurrentReasoning: settingsLCAgentModelPickerRawReasoning(settings, fieldIndex),
	}
	m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(fieldIndex)) + " provider."
	return m, nil
}

func (m Model) openEmbeddedLCAgentModelPicker() (tea.Model, tea.Cmd) {
	m.settingsFields = newSettingsFields(m.currentSettingsBaseline())
	m.settingsSelected = settingsFieldLCAgentModel
	next, cmd := m.openSettingsLCAgentModelPicker()
	updated, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	m = updated
	if state := m.settingsLCAgentModelPicker; state != nil {
		state.EmbeddedApply = true
		state.EmbeddedProject = strings.TrimSpace(m.codexVisibleProject)
	}
	m.status = "Choose the LCAgent provider and model."
	return m, cmd
}

func settingsLCAgentModelListCmd(fieldIndex int, provider, current string, cfg codexapp.LCAgentModelListConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		models, err := codexapp.LCAgentModelOptions(ctx, cfg)
		return settingsLCAgentModelListMsg{
			fieldIndex: fieldIndex,
			provider:   provider,
			current:    current,
			models:     models,
			err:        err,
		}
	}
}

func settingsLCAgentModelListConfig(settings config.EditableSettings, fieldIndex int) (codexapp.LCAgentModelListConfig, string, string, bool) {
	return settingsLCAgentModelListConfigForProvider(settings, fieldIndex, "")
}

func settingsLCAgentModelListConfigForProvider(settings config.EditableSettings, fieldIndex int, providerOverride string) (codexapp.LCAgentModelListConfig, string, string, bool) {
	providerOverride = strings.ToLower(strings.TrimSpace(providerOverride))
	provider := settingsLCAgentMainProvider(settings)
	current := strings.TrimSpace(settings.EmbeddedLCAgentModel)
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		utilityProvider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
		if utilityProvider == "off" && providerOverride == "" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if utilityProvider != "main" {
			provider = utilityProvider
		}
		current = strings.TrimSpace(settings.LCAgentUtilityModel)
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		visionProvider := settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
		if (visionProvider == "off" || visionProvider == "auto") && providerOverride == "" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if visionProvider != "main" {
			provider = visionProvider
		}
		current = strings.TrimSpace(settings.LCAgentVisionModel)
	}
	if backend := settingsProjectCloudModelFieldBackend(fieldIndex); backend != config.AIBackendUnset {
		provider = settingsCloudModelProviderForBackend(backend)
		current = settingsProjectCloudModelRawValue(settings, backend)
	}
	if settingsFieldUsesBossCloudModelPicker(fieldIndex) {
		provider = settingsCloudModelProviderForBackend(settings.BossChatBackend)
		if provider == "" {
			provider = "openai"
		}
		current = strings.TrimSpace(settings.BossHelmModel)
		if fieldIndex == settingsFieldBossUtilityModel {
			current = strings.TrimSpace(settings.BossUtilityModel)
		}
	}
	if providerOverride != "" {
		if providerOverride == "off" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if providerOverride == "main" {
			provider = settingsLCAgentMainProvider(settings)
		} else {
			provider = providerOverride
		}
		if !strings.EqualFold(providerOverride, settingsLCAgentModelPickerProvider(settings, fieldIndex)) {
			current = ""
		}
	}
	if strings.EqualFold(provider, "ollama") && strings.TrimSpace(current) == "" {
		current = strings.TrimSpace(settings.OllamaModel)
	}
	cfg := codexapp.LCAgentModelListConfig{
		Provider:         provider,
		Model:            current,
		IncludeAvailable: fieldIndex == settingsFieldLCAgentModel && strings.TrimSpace(providerOverride) == "",
		EnvFile:          settingsModelPickerEnvFile(settings, fieldIndex),
		OpenAIAPIKey:     settings.OpenAIAPIKey,
		OpenRouterAPIKey: settings.OpenRouterAPIKey,
		DeepSeekAPIKey:   settings.DeepSeekAPIKey,
		MoonshotAPIKey:   settings.MoonshotAPIKey,
		XiaomiAPIKey:     settings.XiaomiAPIKey,
		XiaomiBaseURL:    settings.XiaomiBaseURL,
		OllamaAPIKey:     settings.OllamaAPIKey,
		OllamaBaseURL:    settings.OllamaBaseURL,
		OllamaModel:      settings.OllamaModel,
		RequestTimeout:   settings.LCAgentRequestTimeout,
	}
	return cfg, provider, current, true
}

func (m Model) applySettingsLCAgentModelListMsg(msg settingsLCAgentModelListMsg) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil ||
		state.Step != settingsLCAgentModelPickerStepModel ||
		state.FieldIndex != msg.fieldIndex ||
		strings.TrimSpace(state.Provider) != strings.TrimSpace(msg.provider) {
		return m, nil
	}
	if len(msg.models) == 0 {
		m.settingsLCAgentModelPicker = nil
		if msg.err != nil {
			m.reportError("Model list failed", msg.err, "")
			return m, nil
		}
		m.status = "No models are available for " + settingsLCAgentModelPickerProviderLabel(msg.provider) + "."
		return m, nil
	}
	state.Loading = false
	state.Current = strings.TrimSpace(msg.current)
	state.Models = append([]codexapp.ModelOption(nil), msg.models...)
	state.FilteredModels = append([]codexapp.ModelOption(nil), msg.models...)
	state.Rows = buildSettingsLCAgentPickerRows(state.FilteredModels, state.Provider)
	state.Err = ""
	if msg.err != nil {
		state.Err = msg.err.Error()
	}
	state.Selected = settingsLCAgentModelPickerSelection(state.FilteredModels, state.Rows, state.Current)
	if state.Err != "" {
		m.status = "Provider model list unavailable; showing curated fallback models only."
	} else {
		m.status = fmt.Sprintf("Loaded %d %s models.", len(state.Models), settingsLCAgentModelPickerProviderLabel(state.Provider))
	}
	return m, nil
}

func settingsLCAgentModelPickerSelection(models []codexapp.ModelOption, rows []settingsLCAgentPickerRow, current string) int {
	current = strings.TrimSpace(current)
	if current == "" {
		return 0
	}
	for i, row := range rows {
		if row.IsHeader || row.ModelIndex < 0 || row.ModelIndex >= len(models) {
			continue
		}
		option := models[row.ModelIndex]
		if strings.EqualFold(strings.TrimSpace(option.Model), current) || strings.EqualFold(strings.TrimSpace(option.DisplayName), current) {
			return i + 1
		}
	}
	return 0
}

func (m *Model) closeSettingsLCAgentModelPicker(status string) {
	m.settingsLCAgentModelPicker = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateSettingsLCAgentModelPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	if state.Step == settingsLCAgentModelPickerStepProvider {
		return m.updateSettingsLCAgentModelPickerProviderStep(msg)
	}
	if state.Step == settingsLCAgentModelPickerStepAPIKey {
		return m.updateSettingsLCAgentModelPickerAPIKeyStep(msg)
	}
	if state.Step == settingsLCAgentModelPickerStepReasoning {
		return m.updateSettingsLCAgentModelPickerReasoningStep(msg)
	}
	if state.Loading {
		switch msg.String() {
		case "esc":
			m.closeSettingsLCAgentModelPicker("LCAgent model check canceled")
		case "left", "h", "backspace":
			if strings.TrimSpace(state.APIKeyProvider) != "" {
				m.settingsLCAgentModelPicker.Step = settingsLCAgentModelPickerStepAPIKey
				m.status = "Confirm the shared " + settingsLCAgentModelPickerProviderLabel(state.APIKeyProvider) + " connection."
			} else {
				m.settingsLCAgentModelPicker.Step = settingsLCAgentModelPickerStepProvider
				m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " provider."
			}
			m.settingsLCAgentModelPicker.Loading = false
		}
		return m, nil
	}
	maxIndex := len(state.Rows)
	if state.Selected < 0 {
		state.Selected = 0
	}
	if state.Selected > maxIndex {
		state.Selected = maxIndex
	}
	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentModelPicker("LCAgent model picker closed")
		return m, nil
	case "left", "h":
		if strings.TrimSpace(state.APIKeyProvider) != "" {
			state.Step = settingsLCAgentModelPickerStepAPIKey
			m.status = "Confirm the shared " + settingsLCAgentModelPickerProviderLabel(state.APIKeyProvider) + " connection."
		} else {
			state.Step = settingsLCAgentModelPickerStepProvider
			m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " provider."
		}
		return m, nil
	case "up", "shift+tab":
		state.Selected = settingsLCAgentPickerPrevSelectable(state.Rows, state.Selected)
		return m, nil
	case "down", "tab":
		state.Selected = settingsLCAgentPickerNextSelectable(state.Rows, state.Selected)
		return m, nil
	case "pgup", "pageup", "ctrl+u":
		state.Selected = settingsLCAgentPickerMoveSelectable(state.Rows, state.Selected, -5)
		return m, nil
	case "pgdown", "pagedown", "ctrl+d":
		state.Selected = settingsLCAgentPickerMoveSelectable(state.Rows, state.Selected, 5)
		return m, nil
	case "enter":
		if state.Selected == 0 {
			return m.chooseSettingsLCAgentModelPickerModel(codexapp.ModelOption{}, true)
		}
		if state.Selected > 0 && state.Selected <= len(state.Rows) {
			row := state.Rows[state.Selected-1]
			if !row.IsHeader && row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
				return m.chooseSettingsLCAgentModelPickerModel(state.FilteredModels[row.ModelIndex], false)
			}
		}
		return m, nil
	default:
		// Everything else — letters, digits, backspace, home/end,
		// etc. — goes to the filter input for typing.
		// j/k are NOT used for vim-style list navigation here.
		previous := strings.TrimSpace(state.FilterInput.Value())
		input, cmd := state.FilterInput.Update(msg)
		state.FilterInput = input
		current := strings.TrimSpace(state.FilterInput.Value())
		if current != previous {
			m.applySettingsLCAgentModelPickerFilter()
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) updateSettingsLCAgentModelPickerProviderStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	options := state.ProviderOptions
	if len(options) == 0 {
		m.closeSettingsLCAgentModelPicker("No LCAgent providers are available right now.")
		return m, nil
	}
	if state.ProviderSelected < 0 {
		state.ProviderSelected = 0
	}
	if state.ProviderSelected >= len(options) {
		state.ProviderSelected = len(options) - 1
	}
	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentModelPicker("LCAgent model picker closed")
		return m, nil
	case "up", "k", "shift+tab":
		state.ProviderSelected = wrapIndex(state.ProviderSelected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		state.ProviderSelected = wrapIndex(state.ProviderSelected+1, len(options))
		return m, nil
	case "enter":
		return m.chooseSettingsLCAgentModelPickerProvider(options[state.ProviderSelected])
	}
	return m, nil
}

func (m Model) chooseSettingsLCAgentModelPickerProvider(option settingsLCAgentProviderOption) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	provider := strings.ToLower(strings.TrimSpace(option.Value))
	state.Provider = provider
	state.PendingModel = ""
	state.PendingModelAuto = true
	state.PendingModelOption = codexapp.ModelOption{}
	state.PendingReasoning = strings.TrimSpace(state.CurrentReasoning)
	if settingsLCAgentModelPickerProviderSkipsModel(provider) {
		return m.applySettingsLCAgentModelPickerSelection()
	}
	state.Step = settingsLCAgentModelPickerStepAPIKey
	state.APIKeyProvider = provider
	state.APIKeyInput = newSettingsLCAgentModelPickerAPIKeyInput(provider, settingsModelPickerSavedAPIKey(m.settingsDraftForInferenceStatus(), provider))
	state.BaseURLInput = newSettingsLCAgentModelPickerBaseURLInput(provider, settingsModelPickerSavedBaseURL(m.settingsDraftForInferenceStatus(), provider))
	state.ConnectionSelected = 0
	state.focusConnectionInput()
	state.Loading = false
	state.Err = ""
	m.status = "Confirm the shared " + settingsLCAgentModelPickerProviderLabel(provider) + " connection."
	return m, nil
}

func (m Model) updateSettingsLCAgentModelPickerAPIKeyStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentModelPicker("LCAgent model picker closed")
		return m, nil
	case "shift+tab", "ctrl+p":
		state.Step = settingsLCAgentModelPickerStepProvider
		m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " provider."
		return m, nil
	case "tab", "down", "up":
		if settingsModelPickerBaseURLField(state.APIKeyProvider) >= 0 {
			if state.ConnectionSelected == 0 {
				state.ConnectionSelected = 1
			} else {
				state.ConnectionSelected = 0
			}
			state.focusConnectionInput()
		}
		return m, nil
	case "enter":
		return m.startSettingsLCAgentModelPickerModelList()
	default:
		if state.ConnectionSelected == 1 && settingsModelPickerBaseURLField(state.APIKeyProvider) >= 0 {
			input, cmd := state.BaseURLInput.Update(msg)
			state.BaseURLInput = input
			return m, cmd
		}
		input, cmd := state.APIKeyInput.Update(msg)
		state.APIKeyInput = input
		return m, cmd
	}
}

func (s *settingsLCAgentModelPickerState) focusConnectionInput() {
	if s == nil {
		return
	}
	if s.ConnectionSelected == 1 && settingsModelPickerBaseURLField(s.APIKeyProvider) >= 0 {
		s.APIKeyInput.Blur()
		s.BaseURLInput.Focus()
		s.BaseURLInput.CursorEnd()
		return
	}
	s.ConnectionSelected = 0
	s.BaseURLInput.Blur()
	s.APIKeyInput.Focus()
	s.APIKeyInput.CursorEnd()
}

func (m Model) startSettingsLCAgentModelPickerModelList() (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	provider := strings.ToLower(strings.TrimSpace(state.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(state.APIKeyProvider))
	}
	if provider == "" {
		m.status = "Choose a provider before checking models."
		return m, nil
	}
	m.setSettingsModelPickerAPIKey(provider, state.APIKeyInput.Value())
	m.setSettingsModelPickerBaseURL(provider, state.BaseURLInput.Value())
	settings := m.settingsDraftForInferenceStatus()
	cfg, resolvedProvider, current, ok := settingsLCAgentModelListConfigForProvider(settings, state.FieldIndex, provider)
	if !ok {
		m.status = "No model list is available for " + settingsLCAgentModelPickerProviderLabel(provider) + "."
		return m, nil
	}
	state.Step = settingsLCAgentModelPickerStepModel
	state.Provider = resolvedProvider
	state.APIKeyProvider = resolvedProvider
	state.Current = current
	state.FilterInput = newSettingsLCAgentModelPickerFilterInput()
	state.Models = nil
	state.FilteredModels = nil
	state.Rows = nil
	state.Selected = 0
	state.Loading = true
	state.Err = ""
	m.status = "Checking " + settingsLCAgentModelPickerProviderLabel(resolvedProvider) + " models..."
	return m, settingsLCAgentModelListCmd(state.FieldIndex, resolvedProvider, current, cfg)
}

func settingsLCAgentModelPickerProviderSkipsModel(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "off", "main":
		return true
	default:
		return false
	}
}

func (m *Model) applySettingsLCAgentModelPickerFilter() {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return
	}
	state.FilteredModels = codexFilterModels(state.Models, state.FilterInput.Value())
	if custom, ok := settingsLCAgentModelPickerCustomOption(state); ok {
		state.FilteredModels = append(state.FilteredModels, custom)
	}
	state.Rows = buildSettingsLCAgentPickerRows(state.FilteredModels, state.Provider)

	// Preserve the current selection when the selected model is still
	// visible after filtering.  Only recalculate when the selection is no
	// longer valid or was Auto (0).
	if state.Selected > 0 && state.Selected <= len(state.Rows) {
		row := state.Rows[state.Selected-1]
		if !row.IsHeader && row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
			// Same model is still present — keep the selection.
			return
		}
	}
	// Selection is no longer valid; recalculate.
	state.Selected = 0
	if state.Current != "" {
		state.Selected = settingsLCAgentModelPickerSelection(state.FilteredModels, state.Rows, state.Current)
	}
	if state.Selected == 0 && strings.TrimSpace(state.FilterInput.Value()) != "" && len(state.FilteredModels) > 0 {
		state.Selected = settingsLCAgentPickerNextSelectable(state.Rows, 0)
	}
	if state.Selected > len(state.Rows) {
		state.Selected = len(state.Rows)
	}
}

func settingsLCAgentModelPickerCustomOption(state *settingsLCAgentModelPickerState) (codexapp.ModelOption, bool) {
	if state == nil {
		return codexapp.ModelOption{}, false
	}
	model := strings.TrimSpace(state.FilterInput.Value())
	if model == "" || settingsLCAgentModelPickerHasModel(state.Models, model) {
		return codexapp.ModelOption{}, false
	}
	provider := strings.ToLower(strings.TrimSpace(state.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(state.CurrentProvider))
	}
	return codexapp.ModelOption{
		ID:                        model,
		Model:                     model,
		ModelProvider:             provider,
		DisplayName:               "Custom: " + model,
		Description:               "Use this provider model ID exactly as typed.",
		SupportedReasoningEfforts: codexapp.LCAgentReasoningEffortOptionsForProvider(provider),
		DefaultReasoningEffort:    settingsLCAgentModelPickerDefaultReasoning(provider, model),
	}, true
}

func settingsLCAgentModelPickerHasModel(models []codexapp.ModelOption, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, option := range models {
		if strings.EqualFold(strings.TrimSpace(option.Model), model) ||
			strings.EqualFold(strings.TrimSpace(option.ID), model) ||
			strings.EqualFold(strings.TrimSpace(option.DisplayName), model) {
			return true
		}
	}
	return false
}

func settingsLCAgentModelPickerDefaultReasoning(provider, model string) string {
	options := codexapp.LCAgentReasoningEffortOptionsForProvider(provider)
	if len(options) > 0 {
		return strings.TrimSpace(options[0].ReasoningEffort)
	}
	return ""
}

func (m Model) chooseSettingsLCAgentModelPickerModel(option codexapp.ModelOption, auto bool) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	state.PendingModel = strings.TrimSpace(option.Model)
	state.PendingModelAuto = auto
	state.PendingModelOption = option
	if settingsLCAgentModelPickerUsesReasoning(state.FieldIndex) {
		state.Step = settingsLCAgentModelPickerStepReasoning
		options := settingsLCAgentModelPickerReasoningOptions(state)
		state.ReasoningSelected = settingsLCAgentModelPickerReasoningSelection(options, state)
		if state.ReasoningSelected >= 0 && state.ReasoningSelected < len(options) {
			state.PendingReasoning = strings.TrimSpace(options[state.ReasoningSelected].Value)
		}
		m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " reasoning effort."
		return m, nil
	}
	return m.applySettingsLCAgentModelPickerSelection()
}

func (m Model) updateSettingsLCAgentModelPickerReasoningStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	options := settingsLCAgentModelPickerReasoningOptions(state)
	if len(options) == 0 {
		return m.applySettingsLCAgentModelPickerSelection()
	}
	if state.ReasoningSelected < 0 {
		state.ReasoningSelected = 0
	}
	if state.ReasoningSelected >= len(options) {
		state.ReasoningSelected = len(options) - 1
	}
	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentModelPicker("LCAgent model picker closed")
		return m, nil
	case "left", "h", "backspace":
		state.Step = settingsLCAgentModelPickerStepModel
		m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " model."
		return m, nil
	case "up", "k", "shift+tab":
		state.ReasoningSelected = wrapIndex(state.ReasoningSelected-1, len(options))
		state.PendingReasoning = strings.TrimSpace(options[state.ReasoningSelected].Value)
		return m, nil
	case "down", "j", "tab":
		state.ReasoningSelected = wrapIndex(state.ReasoningSelected+1, len(options))
		state.PendingReasoning = strings.TrimSpace(options[state.ReasoningSelected].Value)
		return m, nil
	case "enter":
		state.PendingReasoning = strings.TrimSpace(options[state.ReasoningSelected].Value)
		return m.applySettingsLCAgentModelPickerSelection()
	}
	return m, nil
}

func (m Model) applySettingsLCAgentModelPickerSelection() (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	model := strings.TrimSpace(state.PendingModel)
	fieldIndex := state.FieldIndex
	provider := strings.ToLower(strings.TrimSpace(state.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(state.CurrentProvider))
	}
	if provider == "main" || provider == "off" {
		model = ""
	}
	if strings.TrimSpace(state.APIKeyProvider) != "" {
		m.setSettingsModelPickerAPIKey(provider, state.APIKeyInput.Value())
	}
	if settingsFieldUsesProjectCloudModelPicker(fieldIndex) {
		return m.applySettingsProjectCloudModelPickerSelection(provider, model)
	}
	if settingsFieldUsesBossCloudModelPicker(fieldIndex) {
		return m.applySettingsBossCloudModelPickerSelection(provider, model)
	}
	if fieldIndex >= 0 && fieldIndex < len(m.settingsFields) {
		m.settingsFields[fieldIndex].input.SetValue(model)
		m.settingsFields[fieldIndex].input.CursorEnd()
	}
	switch fieldIndex {
	case settingsFieldLCAgentModel:
		if len(m.settingsFields) > settingsFieldLCAgentRoutePreset {
			m.settingsFields[settingsFieldLCAgentRoutePreset].input.SetValue("")
		}
		if len(m.settingsFields) > settingsFieldLCAgentProvider && provider != "" {
			m.settingsFields[settingsFieldLCAgentProvider].input.SetValue(provider)
		}
		if len(m.settingsFields) > settingsFieldLCAgentReasoning {
			m.settingsFields[settingsFieldLCAgentReasoning].input.SetValue(strings.TrimSpace(state.PendingReasoning))
		}
	case settingsFieldLCAgentUtilityModel:
		if len(m.settingsFields) > settingsFieldLCAgentUtilityProvider && provider != "" {
			m.settingsFields[settingsFieldLCAgentUtilityProvider].input.SetValue(provider)
		}
	case settingsFieldLCAgentVisionModel:
		if len(m.settingsFields) > settingsFieldLCAgentVisionProvider && provider != "" {
			m.settingsFields[settingsFieldLCAgentVisionProvider].input.SetValue(provider)
		}
	}
	if state.EmbeddedApply {
		settings := config.NormalizeEditableSettings(m.settingsDraftForInferenceStatus())
		settings.RecentLCAgentModels = appendRecentString(settings.RecentLCAgentModels, formatLCAgentRecentModelID(provider, model), 5)
		path := m.currentWritableConfigPath()
		projectPath := strings.TrimSpace(state.EmbeddedProject)
		if projectPath == "" {
			projectPath = strings.TrimSpace(m.codexVisibleProject)
		}
		m.settingsLCAgentModelPicker = nil
		if strings.TrimSpace(model) == "" {
			m.status = "Saving LCAgent " + settingsLCAgentModelPickerProviderLabel(provider) + " auto model..."
		} else {
			m.status = "Saving LCAgent " + settingsLCAgentModelPickerProviderLabel(provider) + " model..."
		}
		return m, func() tea.Msg {
			err := config.SaveEditableSettings(path, settings)
			return codexLCAgentProviderSetupSavedMsg{
				projectPath: projectPath,
				settings:    settings,
				path:        path,
				err:         err,
			}
		}
	}
	label := settingsLCAgentModelPickerRoleLabel(fieldIndex)
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	providerLabel := provider
	if providerLabel != "" {
		providerLabel = settingsLCAgentModelPickerProviderLabel(providerLabel) + " / "
	}
	if provider == "off" {
		m.closeSettingsLCAgentModelPicker(label + " turned off. " + hint)
		return m, nil
	}
	if provider == "main" {
		m.closeSettingsLCAgentModelPicker(label + " set to Same as Main. " + hint)
		return m, nil
	}
	if strings.TrimSpace(model) == "" {
		m.closeSettingsLCAgentModelPicker(label + " reset to " + providerLabel + "default. " + hint)
		return m, nil
	}
	if settingsLCAgentModelPickerUsesReasoning(fieldIndex) && strings.TrimSpace(state.PendingReasoning) != "" {
		m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + strings.TrimSpace(model) + " with " + strings.TrimSpace(state.PendingReasoning) + " reasoning. " + hint)
		return m, nil
	}
	m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + strings.TrimSpace(model) + ". " + hint)
	return m, nil
}

func (m Model) applySettingsProjectCloudModelPickerSelection(provider, model string) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	backend := settingsCloudModelBackendForProvider(provider)
	if backend == config.AIBackendUnset || backend == config.AIBackendOpenAIAPI {
		m.closeSettingsLCAgentModelPicker("Project reports do not have a direct model field for " + settingsLCAgentModelPickerProviderLabel(provider) + ".")
		return m, nil
	}
	if len(m.settingsFields) > settingsFieldAIBackend {
		m.settingsFields[settingsFieldAIBackend].input.SetValue(string(backend))
	}
	if fieldIndex := settingsProjectCloudModelFieldForBackend(backend); fieldIndex >= 0 && fieldIndex < len(m.settingsFields) {
		m.settingsFields[fieldIndex].input.SetValue(strings.TrimSpace(model))
		m.settingsFields[fieldIndex].input.CursorEnd()
	}
	if state != nil && len(m.settingsFields) > settingsFieldProjectReasoning {
		m.settingsFields[settingsFieldProjectReasoning].input.SetValue(strings.TrimSpace(state.PendingReasoning))
		m.settingsFields[settingsFieldProjectReasoning].input.CursorEnd()
	}
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	label := "Project reports model"
	providerLabel := backend.Label()
	if strings.TrimSpace(model) == "" {
		m.closeSettingsLCAgentModelPicker(label + " reset to " + providerLabel + " default. " + hint)
		return m, nil
	}
	if state != nil && strings.TrimSpace(state.PendingReasoning) != "" {
		m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + " / " + strings.TrimSpace(model) + " with " + strings.TrimSpace(state.PendingReasoning) + " reasoning. " + hint)
		return m, nil
	}
	m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + " / " + strings.TrimSpace(model) + ". " + hint)
	return m, nil
}

func (m Model) applySettingsBossCloudModelPickerSelection(provider, model string) (tea.Model, tea.Cmd) {
	backend := settingsCloudModelBackendForProvider(provider)
	if backend == config.AIBackendUnset {
		m.closeSettingsLCAgentModelPicker("Help Chat does not have a model list for " + settingsLCAgentModelPickerProviderLabel(provider) + ".")
		return m, nil
	}
	if len(m.settingsFields) > settingsFieldBossChatBackend {
		m.settingsFields[settingsFieldBossChatBackend].input.SetValue(string(backend))
	}
	fieldIndex := settingsFieldBossChatModel
	label := "Help Chat main model"
	if m.settingsLCAgentModelPicker != nil && m.settingsLCAgentModelPicker.FieldIndex == settingsFieldBossUtilityModel {
		fieldIndex = settingsFieldBossUtilityModel
		label = "Help Chat utility model"
	}
	if fieldIndex >= 0 && fieldIndex < len(m.settingsFields) {
		m.settingsFields[fieldIndex].input.SetValue(strings.TrimSpace(model))
		m.settingsFields[fieldIndex].input.CursorEnd()
	}
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	providerLabel := backend.Label()
	if strings.TrimSpace(model) == "" {
		m.closeSettingsLCAgentModelPicker(label + " reset to " + providerLabel + " default. " + hint)
		return m, nil
	}
	m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + " / " + strings.TrimSpace(model) + ". " + hint)
	return m, nil
}

func (m Model) renderSettingsLCAgentModelPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsLCAgentModelPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsLCAgentModelPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(68, bodyW-10), 108))
	panelInnerWidth := max(32, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsLCAgentModelPickerContent(panelInnerWidth, max(14, bodyH-2)))
}

func (m Model) renderSettingsLCAgentModelPickerContent(width, bodyH int) string {
	state := m.settingsLCAgentModelPicker
	title := "Model Selector"
	if state != nil {
		title = settingsLCAgentModelPickerDialogTitle(state.FieldIndex)
	}
	if state != nil && state.Step == settingsLCAgentModelPickerStepProvider {
		return m.renderSettingsLCAgentModelPickerProviderContent(width, bodyH, title)
	}
	if state != nil && state.Step == settingsLCAgentModelPickerStepAPIKey {
		return m.renderSettingsLCAgentModelPickerAPIKeyContent(width, bodyH, title)
	}
	if state != nil && state.Step == settingsLCAgentModelPickerStepReasoning {
		return m.renderSettingsLCAgentModelPickerReasoningContent(width, bodyH, title)
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Type", "filter", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("PgUp/PgDn", "page", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Left", "key", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if state == nil || state.Loading {
		lines = append(lines, "", commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Checking provider model list..."))
		return strings.Join(lines, "\n")
	}
	current := strings.TrimSpace(state.Current)
	if current == "" {
		current = "Auto (" + settingsLCAgentModelPickerAutoLabelForProvider(m.settingsDraftForInferenceStatus(), state.FieldIndex, state.Provider) + ")"
	}
	lines = append(lines, detailMutedStyle.Render("Provider: "+settingsLCAgentModelPickerProviderLabel(state.Provider)+"   Current: "+truncateText(current, max(18, width-22))))
	if state.Err != "" {
		lines = append(lines, detailWarningStyle.Render("Warning: full provider model list unavailable."))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, max(18, width), "Showing curated fallback models only. Check the shared API key, base URL, env file, or process environment before choosing.")...)
		lines = append(lines, detailMutedStyle.Render("Provider check: "+truncateText(state.Err, max(18, width-16))))
	}
	lines = append(lines, commandPaletteRowStyle.Render("Filter: "+state.FilterInput.View()), "")

	about := ""
	selectedStatus := ""
	if state.Selected > 0 && state.Selected-1 < len(state.Rows) {
		row := state.Rows[state.Selected-1]
		if !row.IsHeader && row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
			option := state.FilteredModels[row.ModelIndex]
			about = strings.TrimSpace(option.Description)
			selectedStatus = codexModelPickerSelectedStatus(option)
		}
	}
	if selectedStatus != "" {
		lines = append(lines, detailMutedStyle.Render("Selected: "+truncateText(selectedStatus, max(18, width-10))))
	}
	totalDisplayRows := len(state.Rows) + 1 // +1 for Auto row
	listLimit := max(4, min(totalDisplayRows, bodyH-len(lines)-5))
	start := 0
	if state.Selected >= listLimit {
		start = state.Selected - listLimit + 1
	}
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	end := min(totalDisplayRows, start+listLimit)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderSettingsLCAgentModelPickerRow(i, state, i == state.Selected, width))
	}
	if end < totalDisplayRows {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", totalDisplayRows-end)))
	}
	if about != "" {
		lines = append(lines, "", detailSectionStyle.Render("About"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), about)...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsLCAgentModelPickerProviderContent(width, bodyH int, title string) string {
	state := m.settingsLCAgentModelPicker
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Up/Down", "provider", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "continue", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if state == nil || len(state.ProviderOptions) == 0 {
		lines = append(lines, "", commandPaletteHintStyle.Render("No provider options are available."))
		return strings.Join(lines, "\n")
	}
	currentLabel := settingsLCAgentModelValueLabel(m.settingsDraftForInferenceStatus(), state.FieldIndex)
	lines = append(lines, detailMutedStyle.Render("Current: "+truncateText(currentLabel, max(18, width-9))), "")
	for i, option := range state.ProviderOptions {
		lines = append(lines, renderSettingsLCAgentProviderPickerRow(option, i == state.ProviderSelected, option.Value == state.CurrentProvider, width))
	}
	selected := state.ProviderOptions[state.ProviderSelected]
	lines = append(lines, "", detailField("Selected", detailValueStyle.Render(selected.Label)))
	if strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(18, width), selected.Summary)...)
	}
	if strings.TrimSpace(selected.Description) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), selected.Description)...)
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsLCAgentModelPickerAPIKeyContent(width, bodyH int, title string) string {
	state := m.settingsLCAgentModelPicker
	action := renderDialogAction("Type", "API key", navigateActionKeyStyle, navigateActionTextStyle)
	if state != nil && settingsModelPickerBaseURLField(firstNonEmptyTrimmed(state.APIKeyProvider, state.Provider)) >= 0 {
		action = renderDialogAction("Type", "connection", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Tab", "field", navigateActionKeyStyle, navigateActionTextStyle)
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		action + "   " +
			renderDialogAction("Enter", "load models", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Shift+Tab", "provider", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if state == nil {
		return strings.Join(lines, "\n")
	}
	provider := firstNonEmptyTrimmed(state.APIKeyProvider, state.Provider)
	input := state.APIKeyInput
	input.Width = max(18, width)
	rowStyle := commandPaletteRowStyle
	if state.ConnectionSelected == 0 {
		rowStyle = dialogSelectedRowStyle
	}
	lines = append(lines, detailMutedStyle.Render("Provider: "+settingsLCAgentModelPickerProviderLabel(provider)))
	if settingsModelPickerBaseURLField(provider) >= 0 {
		baseInput := state.BaseURLInput
		baseInput.Width = max(18, width)
		baseStyle := commandPaletteRowStyle
		if state.ConnectionSelected == 1 {
			baseStyle = dialogSelectedRowStyle
		}
		lines = append(lines, baseStyle.Render(settingsModelPickerBaseURLLabel(provider)+": "+baseInput.View()))
	}
	lines = append(lines, rowStyle.Render(settingsModelPickerAPIKeyLabel(provider)+": "+input.View()))
	if strings.TrimSpace(input.Value()) != "" {
		lines = append(lines, detailMutedStyle.Render("Shared key will be used for this provider and saved with settings. It remains hidden while editing."))
	} else {
		fallback := settingsModelPickerAPIKeyFallbackText(m.settingsDraftForInferenceStatus(), state.FieldIndex, provider)
		lines = append(lines, detailMutedStyle.Render(fallback))
	}
	lines = append(lines, "", detailMutedStyle.Render("Leave blank to try provider defaults or env values. If discovery cannot authenticate, the next list is a clearly marked curated fallback."))
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsLCAgentModelPickerReasoningContent(width, bodyH int, title string) string {
	state := m.settingsLCAgentModelPicker
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Up/Down", "reasoning", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Left", "model", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if state == nil {
		return strings.Join(lines, "\n")
	}
	modelLabel := "Auto (" + settingsLCAgentModelPickerAutoLabelForProvider(m.settingsDraftForInferenceStatus(), state.FieldIndex, state.Provider) + ")"
	if strings.TrimSpace(state.PendingModel) != "" {
		modelLabel = strings.TrimSpace(state.PendingModel)
	}
	lines = append(lines,
		detailMutedStyle.Render("Provider: "+settingsLCAgentModelPickerProviderLabel(state.Provider)+"   Model: "+truncateText(modelLabel, max(18, width-20))),
		"",
	)
	options := settingsLCAgentModelPickerReasoningOptions(state)
	for i, option := range options {
		lines = append(lines, renderSettingsChoicePickerRow(option, i == state.ReasoningSelected, option.Value == state.CurrentReasoning, width))
	}
	if len(options) > 0 && state.ReasoningSelected >= 0 && state.ReasoningSelected < len(options) {
		selected := options[state.ReasoningSelected]
		lines = append(lines, "", detailField("Selected", detailValueStyle.Render(selected.Label)))
		if strings.TrimSpace(selected.Summary) != "" {
			lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(18, width), selected.Summary)...)
		}
		if strings.TrimSpace(selected.Description) != "" {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), selected.Description)...)
		}
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsLCAgentModelPickerRow(index int, state *settingsLCAgentModelPickerState, selected bool, width int) string {
	label := "Auto"
	right := settingsLCAgentModelPickerAutoLabelForProvider(m.settingsDraftForInferenceStatus(), state.FieldIndex, state.Provider)
	if index > 0 {
		row := state.Rows[index-1]
		if row.IsHeader {
			header := settingsLCAgentModelPickerProviderLabel(row.Provider)
			return commandPaletteTitleStyle.Width(width).Render("── " + header + " ──")
		}
		if row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
			option := state.FilteredModels[row.ModelIndex]
			label = firstNonEmptyTrimmed(option.DisplayName, option.Model)
			right = strings.TrimSpace(option.Model)
			if strings.EqualFold(label, right) {
				right = ""
			}
			if option.IsDefault && right == "" {
				right = "default"
			} else if option.IsDefault {
				right += "  default"
			}
		}
	}
	leftWidth := width
	if right != "" {
		leftWidth = max(14, width-lipgloss.Width(right)-2)
	}
	row := truncateText(label, max(8, leftWidth))
	if right != "" {
		row = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth).Render(row), "  ", detailMutedStyle.Render(truncateText(right, max(8, width-leftWidth-2))))
	}
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func settingsLCAgentModelPickerAutoLabel(settings config.EditableSettings, fieldIndex int) string {
	return settingsLCAgentModelPickerAutoLabelForProvider(settings, fieldIndex, settingsLCAgentModelPickerProvider(settings, fieldIndex))
}

func settingsLCAgentModelPickerAutoLabelForProvider(settings config.EditableSettings, fieldIndex int, provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if backend := settingsProjectCloudModelFieldBackend(fieldIndex); backend != config.AIBackendUnset {
		if override := settingsCloudModelBackendForProvider(provider); override != config.AIBackendUnset {
			backend = override
		}
		return backend.DefaultProjectModel()
	}
	if fieldIndex == settingsFieldBossChatModel || fieldIndex == settingsFieldBossUtilityModel {
		backend := settingsCloudModelBackendForProvider(provider)
		if backend == config.AIBackendUnset {
			backend = settings.BossChatBackend
		}
		bossSettings := settings
		bossSettings.BossChatBackend = backend
		if fieldIndex == settingsFieldBossUtilityModel {
			return settingsBossUtilityDefaultLabel(bossSettings)
		}
		return settingsBossHelmDefaultLabel(bossSettings)
	}
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		return settingsLCAgentUtilityDefaultLabelForProvider(settings, provider)
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		return settingsLCAgentVisionDefaultLabelForProvider(settings, provider)
	}
	if provider != "" {
		if strings.EqualFold(provider, "ollama") {
			if model := strings.TrimSpace(settings.OllamaModel); model != "" {
				return model
			}
			return "first local Ollama model"
		}
		return lcagentDefaultModelForProvider(provider)
	}
	return settingsLCAgentMainModel(settings)
}

func settingsLCAgentModelPickerProviderOptions(fieldIndex int) []settingsLCAgentProviderOption {
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return settingsLCAgentUtilityProviderOptions()
	case settingsFieldLCAgentVisionModel:
		return settingsLCAgentVisionProviderOptions()
	case settingsFieldOpenRouterModel, settingsFieldDeepSeekModel, settingsFieldMoonshotModel, settingsFieldXiaomiModel:
		return settingsProjectCloudModelProviderOptions()
	case settingsFieldBossChatModel, settingsFieldBossUtilityModel:
		return settingsBossCloudModelProviderOptions()
	default:
		return settingsLCAgentProviderOptions()
	}
}

func settingsProjectCloudModelProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter for project reports and background summaries.",
			Description: "Uses the shared OpenRouter API key and model list. Leave the model on Auto for the built-in project-report default.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use direct DeepSeek for project reports and background summaries.",
			Description: "Uses the shared DeepSeek API key and direct DeepSeek model IDs.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use direct Moonshot/Kimi for project reports and background summaries.",
			Description: "Uses the shared Moonshot API key and Kimi model IDs.",
		},
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use direct Xiaomi MiMo for project reports and background summaries.",
			Description: "Uses the shared Xiaomi API key and MiMo model IDs.",
		},
	}
}

func settingsBossCloudModelProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "openai",
			Label:       "OpenAI",
			Summary:     "Use direct OpenAI API inference for Help Chat.",
			Description: "Uses the shared OpenAI API key and direct OpenAI model IDs.",
		},
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter for Help Chat.",
			Description: "Uses the shared OpenRouter API key and model list.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use direct DeepSeek for Help Chat.",
			Description: "Uses the shared DeepSeek API key and direct DeepSeek model IDs.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use direct Moonshot/Kimi for Help Chat.",
			Description: "Uses the shared Moonshot API key and Kimi model IDs.",
		},
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use direct Xiaomi MiMo for Help Chat.",
			Description: "Uses the shared Xiaomi API key and MiMo model IDs.",
		},
	}
}

func settingsLCAgentModelPickerProviderSelection(options []settingsLCAgentProviderOption, provider string) int {
	provider = settingsLCAgentProviderOptionValueForModelField(provider)
	for i, option := range options {
		if option.Value == provider {
			return i
		}
	}
	return 0
}

func settingsLCAgentProviderOptionValueForModelField(raw string) string {
	normalized := normalizeSettingsChoice(raw)
	switch normalized {
	case "", "same", "same-as-main":
		return "main"
	default:
		return normalized
	}
}

func settingsLCAgentModelPickerProvider(settings config.EditableSettings, fieldIndex int) string {
	if backend := settingsProjectCloudModelFieldBackend(fieldIndex); backend != config.AIBackendUnset {
		return settingsCloudModelProviderForBackend(backend)
	}
	if settingsFieldUsesBossCloudModelPicker(fieldIndex) {
		if provider := settingsCloudModelProviderForBackend(settings.BossChatBackend); provider != "" {
			return provider
		}
		return "openai"
	}
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
	case settingsFieldLCAgentVisionModel:
		return settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
	default:
		return settingsLCAgentMainProvider(settings)
	}
}

func settingsLCAgentModelPickerRawModel(settings config.EditableSettings, fieldIndex int) string {
	if backend := settingsProjectCloudModelFieldBackend(fieldIndex); backend != config.AIBackendUnset {
		return settingsProjectCloudModelRawValue(settings, backend)
	}
	if fieldIndex == settingsFieldBossChatModel {
		return strings.TrimSpace(settings.BossHelmModel)
	}
	if fieldIndex == settingsFieldBossUtilityModel {
		return strings.TrimSpace(settings.BossUtilityModel)
	}
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return strings.TrimSpace(settings.LCAgentUtilityModel)
	case settingsFieldLCAgentVisionModel:
		return strings.TrimSpace(settings.LCAgentVisionModel)
	default:
		return strings.TrimSpace(settings.EmbeddedLCAgentModel)
	}
}

func settingsLCAgentModelPickerRawReasoning(settings config.EditableSettings, fieldIndex int) string {
	if fieldIndex == settingsFieldLCAgentModel {
		return strings.TrimSpace(settings.EmbeddedLCAgentReasoning)
	}
	if settingsFieldUsesProjectCloudModelPicker(fieldIndex) {
		return strings.TrimSpace(settings.ProjectReasoningEffort)
	}
	return ""
}

func settingsLCAgentModelPickerUsesReasoning(fieldIndex int) bool {
	return settingsFieldUsesAnyCloudModelPicker(fieldIndex)
}

func settingsLCAgentModelPickerRoleLabel(fieldIndex int) string {
	switch fieldIndex {
	case settingsFieldOpenRouterModel, settingsFieldDeepSeekModel, settingsFieldMoonshotModel, settingsFieldXiaomiModel:
		return "Project reports model"
	case settingsFieldBossChatModel:
		return "Help Chat main model"
	case settingsFieldBossUtilityModel:
		return "Help Chat utility model"
	case settingsFieldLCAgentUtilityModel:
		return "Utility model"
	case settingsFieldLCAgentVisionModel:
		return "Vision model"
	default:
		return "Main model"
	}
}

func settingsLCAgentModelPickerDialogTitle(fieldIndex int) string {
	if settingsFieldUsesLCAgentModelPicker(fieldIndex) {
		return "LCAgent " + settingsLCAgentModelPickerRoleLabel(fieldIndex)
	}
	return settingsLCAgentModelPickerRoleLabel(fieldIndex)
}

func settingsLCAgentModelPickerReasoningOptions(state *settingsLCAgentModelPickerState) []settingsChoiceOption {
	options := []settingsChoiceOption{{
		Value:       "",
		Label:       "Provider Default",
		Summary:     "Omit explicit reasoning effort.",
		Description: "Lets the selected provider or model decide the reasoning behavior.",
	}}
	if state == nil {
		return options
	}
	if settingsFieldUsesProjectCloudModelPicker(state.FieldIndex) {
		options[0] = settingsProjectReasoningDefaultChoiceOption()
	}
	if state.FieldIndex != settingsFieldLCAgentModel && !settingsFieldUsesProjectCloudModelPicker(state.FieldIndex) {
		options[0].Summary = "Use provider default reasoning."
		if settingsFieldUsesLCAgentModelPicker(state.FieldIndex) {
			options[0].Description = "This role currently follows provider defaults; role-specific reasoning effort can be wired here when the LCAgent runtime supports it."
		} else {
			options[0].Description = "This model setting does not store a separate reasoning effort yet, so the selected provider or model decides."
		}
		return options
	}
	modelOption := state.PendingModelOption
	if state.PendingModelAuto || strings.TrimSpace(modelOption.Model) == "" {
		modelOption = settingsLCAgentModelPickerDefaultOption(state.Models)
	}
	efforts := modelOption.SupportedReasoningEfforts
	if len(efforts) == 0 {
		provider := firstNonEmptyTrimmed(modelOption.ModelProvider, state.Provider)
		efforts = codexapp.LCAgentReasoningEffortOptionsForProvider(provider)
	}
	for _, effort := range efforts {
		value := strings.TrimSpace(effort.ReasoningEffort)
		if value == "" {
			continue
		}
		options = append(options, settingsChoiceOption{
			Value:       value,
			Label:       strings.ToUpper(value[:1]) + value[1:],
			Summary:     strings.TrimSpace(effort.Description),
			Description: strings.TrimSpace(effort.Description),
		})
	}
	return options
}

func settingsLCAgentModelPickerDefaultOption(models []codexapp.ModelOption) codexapp.ModelOption {
	for _, option := range models {
		if option.IsDefault {
			return option
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return codexapp.ModelOption{}
}

func settingsLCAgentModelPickerReasoningSelection(options []settingsChoiceOption, state *settingsLCAgentModelPickerState) int {
	desired := ""
	if state != nil {
		if state.FieldIndex != settingsFieldLCAgentModel && !settingsFieldUsesProjectCloudModelPicker(state.FieldIndex) {
			return 0
		}
		desired = strings.TrimSpace(state.CurrentReasoning)
		if settingsFieldUsesProjectCloudModelPicker(state.FieldIndex) {
			for i, option := range options {
				if strings.EqualFold(strings.TrimSpace(option.Value), desired) {
					return i
				}
			}
			return 0
		}
		if desired == "" {
			if state.PendingModelAuto || strings.TrimSpace(state.PendingModelOption.Model) == "" {
				desired = strings.TrimSpace(settingsLCAgentModelPickerDefaultOption(state.Models).DefaultReasoningEffort)
			} else {
				desired = strings.TrimSpace(state.PendingModelOption.DefaultReasoningEffort)
			}
		}
	}
	for i, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Value), desired) {
			return i
		}
	}
	return 0
}

func (m Model) renderSettingsLCAgentModelValue(fieldIndex int, selected bool, inputWidth int) string {
	label := settingsLCAgentModelValueLabel(m.settingsDraftForInferenceStatus(), fieldIndex)
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}

func settingsLCAgentModelValueLabel(settings config.EditableSettings, fieldIndex int) string {
	if backend := settingsProjectCloudModelFieldBackend(fieldIndex); backend != config.AIBackendUnset {
		provider := settingsCloudModelProviderForBackend(backend)
		model := settingsProjectCloudModelRawValue(settings, backend)
		if model == "" {
			model = "Default: " + backend.DefaultProjectModel()
		}
		return settingsModelPickerAppendReasoning(settingsLCAgentModelPickerProviderLabel(provider)+" / "+model, settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
	}
	if fieldIndex == settingsFieldBossChatModel || fieldIndex == settingsFieldBossUtilityModel {
		provider := settingsCloudModelProviderForBackend(settings.BossChatBackend)
		if provider == "" {
			provider = "openai"
		}
		model := strings.TrimSpace(settings.BossHelmModel)
		if fieldIndex == settingsFieldBossUtilityModel {
			model = strings.TrimSpace(settings.BossUtilityModel)
		}
		if model == "" {
			model = "Default: " + settingsLCAgentModelPickerAutoLabelForProvider(settings, fieldIndex, provider)
		}
		return settingsModelPickerAppendReasoning(settingsLCAgentModelPickerProviderLabel(provider)+" / "+model, settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
	}
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		provider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
		switch provider {
		case "off":
			return "Off"
		case "main":
			return settingsModelPickerAppendReasoning("Same as Main / "+settingsLCAgentMainModel(settings), settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
		default:
			model := strings.TrimSpace(settings.LCAgentUtilityModel)
			if model == "" {
				model = "Default: " + lcagentDefaultUtilityModelForProvider(provider)
			}
			return settingsModelPickerAppendReasoning(settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentUtilityProvider, provider)+" / "+model, settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
		}
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		provider := settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
		switch provider {
		case "auto":
			return settingsLCAgentVisionAutoLabel(settings)
		case "off":
			return "Off"
		case "main":
			return settingsModelPickerAppendReasoning("Same as Main / "+settingsLCAgentMainModel(settings), settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
		default:
			model := strings.TrimSpace(settings.LCAgentVisionModel)
			if model == "" {
				model = "Default: " + lcagentDefaultModelForProvider(provider)
			}
			return settingsModelPickerAppendReasoning(settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentVisionProvider, provider)+" / "+model, settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
		}
	}
	provider := settingsLCAgentMainProvider(settings)
	model := settingsLCAgentMainModel(settings)
	if strings.TrimSpace(settings.LCAgentRoutePreset) == "" && strings.TrimSpace(settings.EmbeddedLCAgentModel) == "" {
		model = "Default: " + model
	}
	return settingsModelPickerAppendReasoning(settingsLCAgentProviderOptionLabel(provider)+" / "+model, settingsModelPickerReasoningDisplay(settings, fieldIndex, provider))
}

func settingsModelPickerAppendReasoning(label, reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return label
	}
	return label + " / reasoning: " + reasoning
}

func settingsModelPickerReasoningDisplay(settings config.EditableSettings, fieldIndex int, provider string) string {
	if settingsFieldUsesProjectCloudModelPicker(fieldIndex) {
		if effort := strings.TrimSpace(settings.ProjectReasoningEffort); effort != "" {
			return effort
		}
		return "LCR Default"
	}
	if fieldIndex == settingsFieldLCAgentModel || strings.EqualFold(strings.TrimSpace(provider), "main") {
		if effort := strings.TrimSpace(settings.EmbeddedLCAgentReasoning); effort != "" {
			return effort
		}
	}
	return "Provider Default"
}

func settingsLCAgentModelPickerProviderLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "main":
		return "Same as Main"
	case "off":
		return "Off"
	case "openai":
		return "OpenAI"
	case "deepseek":
		return "DeepSeek"
	case "moonshot":
		return "Moonshot"
	case "xiaomi":
		return "Xiaomi"
	case "ollama":
		return "Ollama"
	default:
		return "OpenRouter"
	}
}

func settingsProjectCloudModelFieldBackend(fieldIndex int) config.AIBackend {
	switch fieldIndex {
	case settingsFieldOpenRouterModel:
		return config.AIBackendOpenRouter
	case settingsFieldDeepSeekModel:
		return config.AIBackendDeepSeek
	case settingsFieldMoonshotModel:
		return config.AIBackendMoonshot
	case settingsFieldXiaomiModel:
		return config.AIBackendXiaomi
	default:
		return config.AIBackendUnset
	}
}

func settingsProjectCloudModelFieldForBackend(backend config.AIBackend) int {
	switch backend {
	case config.AIBackendOpenRouter:
		return settingsFieldOpenRouterModel
	case config.AIBackendDeepSeek:
		return settingsFieldDeepSeekModel
	case config.AIBackendMoonshot:
		return settingsFieldMoonshotModel
	case config.AIBackendXiaomi:
		return settingsFieldXiaomiModel
	default:
		return -1
	}
}

func settingsProjectCloudModelRawValue(settings config.EditableSettings, backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenRouter:
		return strings.TrimSpace(settings.OpenRouterModel)
	case config.AIBackendDeepSeek:
		return strings.TrimSpace(settings.DeepSeekModel)
	case config.AIBackendMoonshot:
		return strings.TrimSpace(settings.MoonshotModel)
	case config.AIBackendXiaomi:
		return strings.TrimSpace(settings.XiaomiModel)
	default:
		return ""
	}
}

func settingsCloudModelProviderForBackend(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return "openai"
	case config.AIBackendOpenRouter:
		return "openrouter"
	case config.AIBackendDeepSeek:
		return "deepseek"
	case config.AIBackendMoonshot:
		return "moonshot"
	case config.AIBackendXiaomi:
		return "xiaomi"
	default:
		return ""
	}
}

func settingsCloudModelBackendForProvider(provider string) config.AIBackend {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return config.AIBackendOpenAIAPI
	case "", "openrouter":
		return config.AIBackendOpenRouter
	case "deepseek":
		return config.AIBackendDeepSeek
	case "moonshot":
		return config.AIBackendMoonshot
	case "xiaomi":
		return config.AIBackendXiaomi
	default:
		return config.AIBackendUnset
	}
}

func settingsModelPickerEnvFile(settings config.EditableSettings, fieldIndex int) string {
	if settingsFieldUsesLCAgentModelPicker(fieldIndex) {
		return strings.TrimSpace(settings.LCAgentEnvFile)
	}
	return ""
}

func settingsModelPickerSavedAPIKey(settings config.EditableSettings, provider string) string {
	return lcagentProviderSavedAPIKey(settings, provider)
}

func settingsModelPickerAPIKeyLabel(provider string) string {
	return lcagentProviderSavedKeyLabel(provider)
}

func settingsModelPickerAPIKeyField(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return settingsFieldOpenAIAPIKey
	case "", "openrouter":
		return settingsFieldOpenRouterAPIKey
	case "deepseek":
		return settingsFieldDeepSeekAPIKey
	case "moonshot":
		return settingsFieldMoonshotAPIKey
	case "xiaomi":
		return settingsFieldXiaomiAPIKey
	case "ollama":
		return settingsFieldOllamaAPIKey
	default:
		return -1
	}
}

func settingsModelPickerBaseURLField(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "xiaomi":
		return settingsFieldXiaomiBaseURL
	case "ollama":
		return settingsFieldOllamaBaseURL
	default:
		return -1
	}
}

func settingsModelPickerSavedBaseURL(settings config.EditableSettings, provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "xiaomi":
		return strings.TrimSpace(settings.XiaomiBaseURL)
	case "ollama":
		return strings.TrimSpace(settings.OllamaBaseURL)
	default:
		return ""
	}
}

func settingsModelPickerBaseURLLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "xiaomi":
		return "Xiaomi base URL"
	case "ollama":
		return "Ollama base URL"
	default:
		return "Base URL"
	}
}

func settingsModelPickerBaseURLPlaceholder(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "xiaomi":
		return config.AIBackendXiaomi.DefaultOpenAICompatibleBaseURL()
	case "ollama":
		return config.AIBackendOllama.DefaultOpenAICompatibleBaseURL()
	default:
		return "https://provider.example/v1"
	}
}

func (m *Model) setSettingsModelPickerAPIKey(provider, apiKey string) {
	fieldIndex := settingsModelPickerAPIKeyField(provider)
	if fieldIndex < 0 || fieldIndex >= len(m.settingsFields) {
		return
	}
	m.settingsFields[fieldIndex].input.SetValue(strings.TrimSpace(apiKey))
	m.settingsFields[fieldIndex].input.CursorEnd()
}

func (m *Model) setSettingsModelPickerBaseURL(provider, baseURL string) {
	fieldIndex := settingsModelPickerBaseURLField(provider)
	if fieldIndex < 0 || fieldIndex >= len(m.settingsFields) {
		return
	}
	m.settingsFields[fieldIndex].input.SetValue(strings.TrimSpace(baseURL))
	m.settingsFields[fieldIndex].input.CursorEnd()
}

func settingsModelPickerAPIKeyFallbackText(settings config.EditableSettings, fieldIndex int, provider string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "ollama") {
		endpoint := strings.TrimSpace(settings.OllamaBaseURL)
		if endpoint == "" {
			endpoint = config.AIBackendOllama.DefaultOpenAICompatibleBaseURL()
		}
		return "Blank uses the local Ollama endpoint at " + endpoint + "; the API key is optional for the default server."
	}
	keyName := lcagentProviderAPIKeyName(provider)
	if settingsFieldUsesLCAgentModelPicker(fieldIndex) {
		if envFile := strings.TrimSpace(settings.LCAgentEnvFile); envFile != "" {
			return "Blank checks " + keyName + " in the LCAgent env file, then the process environment. If discovery fails, only curated fallback models are shown."
		}
	}
	if keyName != "" {
		return "Blank checks " + keyName + " in the process environment. If discovery fails, only curated fallback models are shown."
	}
	return "Blank shows only curated fallback models when provider discovery cannot authenticate."
}

func buildSettingsLCAgentPickerRows(models []codexapp.ModelOption, selectedProvider string) []settingsLCAgentPickerRow {
	groups := map[string][]int{}
	for i, m := range models {
		p := strings.ToLower(strings.TrimSpace(m.ModelProvider))
		if p == "" {
			p = "other"
		}
		groups[p] = append(groups[p], i)
	}
	order := settingsLCAgentPickerSortedProviders(groups, selectedProvider)
	var rows []settingsLCAgentPickerRow
	for _, provider := range order {
		indices := groups[provider]
		if len(indices) == 0 {
			continue
		}
		rows = append(rows, settingsLCAgentPickerRow{IsHeader: true, Provider: provider, ModelIndex: -1})
		for _, idx := range indices {
			rows = append(rows, settingsLCAgentPickerRow{IsHeader: false, Provider: provider, ModelIndex: idx})
		}
	}
	return rows
}

func settingsLCAgentPickerSortedProviders(groups map[string][]int, selectedProvider string) []string {
	selectedProvider = strings.ToLower(strings.TrimSpace(selectedProvider))
	providers := make([]string, 0, len(groups))
	for p := range groups {
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool {
		pi, pj := providers[i], providers[j]
		if pi == selectedProvider {
			return true
		}
		if pj == selectedProvider {
			return false
		}
		return settingsLCAgentPickerProviderSortOrder(pi) < settingsLCAgentPickerProviderSortOrder(pj)
	})
	return providers
}

func settingsLCAgentPickerProviderSortOrder(provider string) int {
	switch provider {
	case "openrouter":
		return 0
	case "openai":
		return 1
	case "deepseek":
		return 2
	case "moonshot":
		return 3
	case "xiaomi":
		return 4
	case "ollama":
		return 5
	default:
		return 100
	}
}

func settingsLCAgentPickerNextSelectable(rows []settingsLCAgentPickerRow, current int) int {
	maxIdx := len(rows)
	if maxIdx == 0 {
		return 0
	}
	next := current
	for i := 0; i <= maxIdx+1; i++ {
		next++
		if next > maxIdx {
			next = 0
		}
		if next == 0 || !rows[next-1].IsHeader {
			return next
		}
	}
	return 0
}

func settingsLCAgentPickerPrevSelectable(rows []settingsLCAgentPickerRow, current int) int {
	maxIdx := len(rows)
	if maxIdx == 0 {
		return 0
	}
	prev := current
	for i := 0; i <= maxIdx+1; i++ {
		prev--
		if prev < 0 {
			prev = maxIdx
		}
		if prev == 0 || (prev > 0 && prev <= maxIdx && !rows[prev-1].IsHeader) {
			return prev
		}
	}
	return 0
}

func settingsLCAgentPickerMoveSelectable(rows []settingsLCAgentPickerRow, current, delta int) int {
	selectable := []int{0}
	for i, row := range rows {
		if !row.IsHeader {
			selectable = append(selectable, i+1)
		}
	}
	if len(selectable) == 0 || delta == 0 {
		return current
	}
	pos := -1
	for i, idx := range selectable {
		if idx == current {
			pos = i
			break
		}
	}
	if pos < 0 {
		pos = 0
		for i, idx := range selectable {
			if idx >= current {
				pos = i
				break
			}
			pos = i
		}
	}
	pos += delta
	if pos < 0 {
		pos = 0
	}
	if pos >= len(selectable) {
		pos = len(selectable) - 1
	}
	return selectable[pos]
}

func settingsLCAgentPickerLastSelectable(rows []settingsLCAgentPickerRow) int {
	for i := len(rows) - 1; i >= 0; i-- {
		if !rows[i].IsHeader {
			return i + 1
		}
	}
	return 0
}
