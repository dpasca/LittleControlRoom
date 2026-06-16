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
	settingsLCAgentModelPickerStepModel
	settingsLCAgentModelPickerStepReasoning
)

type settingsLCAgentModelPickerState struct {
	FieldIndex         int
	Step               settingsLCAgentModelPickerStep
	Provider           string
	CurrentProvider    string
	ProviderOptions    []settingsLCAgentProviderOption
	ProviderSelected   int
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
		index == settingsFieldLCAgentCriticModel ||
		index == settingsFieldLCAgentVisionModel
}

func newSettingsLCAgentModelPickerFilterInput() textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "type to filter"
	input.CharLimit = 256
	input.Focus()
	return input
}

func (m Model) openSettingsLCAgentModelPicker() (tea.Model, tea.Cmd) {
	fieldIndex := m.settingsSelected
	if m.setupMode && m.setupConfigMode {
		fieldIndex = m.setupSelectedConfigFieldIndex()
	}
	if !settingsFieldUsesLCAgentModelPicker(fieldIndex) {
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
	provider := settingsLCAgentMainProvider(settings)
	current := strings.TrimSpace(settings.EmbeddedLCAgentModel)
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		utilityProvider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
		if utilityProvider == "off" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if utilityProvider != "main" {
			provider = utilityProvider
		}
		current = strings.TrimSpace(settings.LCAgentUtilityModel)
	}
	if fieldIndex == settingsFieldLCAgentCriticModel {
		criticProvider := settingsLCAgentCriticProviderValue(settings.LCAgentCriticProvider)
		if criticProvider == "off" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if criticProvider != "main" {
			provider = criticProvider
		}
		current = strings.TrimSpace(settings.LCAgentCriticModel)
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		visionProvider := settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
		if visionProvider == "off" {
			return codexapp.LCAgentModelListConfig{}, "", "", false
		}
		if visionProvider != "main" {
			provider = visionProvider
		}
		current = strings.TrimSpace(settings.LCAgentVisionModel)
	}
	if providerOverride = strings.ToLower(strings.TrimSpace(providerOverride)); providerOverride != "" {
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
	cfg := codexapp.LCAgentModelListConfig{
		Provider:         provider,
		Model:            current,
		IncludeAvailable: fieldIndex == settingsFieldLCAgentModel && strings.TrimSpace(providerOverride) == "",
		EnvFile:          settings.LCAgentEnvFile,
		OpenAIAPIKey:     settings.OpenAIAPIKey,
		OpenRouterAPIKey: settings.OpenRouterAPIKey,
		DeepSeekAPIKey:   settings.DeepSeekAPIKey,
		MoonshotAPIKey:   settings.MoonshotAPIKey,
		XiaomiAPIKey:     settings.XiaomiAPIKey,
		XiaomiBaseURL:    settings.XiaomiBaseURL,
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
			m.reportError("LCAgent model list failed", msg.err, "")
			return m, nil
		}
		m.status = "No LCAgent models are available for " + settingsLCAgentModelPickerProviderLabel(msg.provider) + "."
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
		m.status = "Showing curated LCAgent models; provider list check did not complete."
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
	if state.Step == settingsLCAgentModelPickerStepReasoning {
		return m.updateSettingsLCAgentModelPickerReasoningStep(msg)
	}
	if state.Loading {
		switch msg.String() {
		case "esc":
			m.closeSettingsLCAgentModelPicker("LCAgent model check canceled")
		case "left", "h", "backspace":
			m.settingsLCAgentModelPicker.Step = settingsLCAgentModelPickerStepProvider
			m.settingsLCAgentModelPicker.Loading = false
			m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " provider."
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
	case "left", "h", "backspace":
		state.Step = settingsLCAgentModelPickerStepProvider
		m.status = "Choose the " + strings.ToLower(settingsLCAgentModelPickerRoleLabel(state.FieldIndex)) + " provider."
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
	settings := m.settingsDraftForInferenceStatus()
	cfg, resolvedProvider, current, ok := settingsLCAgentModelListConfigForProvider(settings, state.FieldIndex, provider)
	if !ok {
		m.status = "No LCAgent model list is available for " + option.Label + "."
		return m, nil
	}
	state.Step = settingsLCAgentModelPickerStepModel
	state.Provider = resolvedProvider
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
		return m, nil
	case "down", "j", "tab":
		state.ReasoningSelected = wrapIndex(state.ReasoningSelected+1, len(options))
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
	case settingsFieldLCAgentCriticModel:
		if len(m.settingsFields) > settingsFieldLCAgentCriticProvider && provider != "" {
			m.settingsFields[settingsFieldLCAgentCriticProvider].input.SetValue(provider)
		}
	case settingsFieldLCAgentVisionModel:
		if len(m.settingsFields) > settingsFieldLCAgentVisionProvider && provider != "" {
			m.settingsFields[settingsFieldLCAgentVisionProvider].input.SetValue(provider)
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
	title := "LCAgent Model"
	if state != nil {
		title = "LCAgent " + settingsLCAgentModelPickerRoleLabel(state.FieldIndex)
	}
	if state != nil && state.Step == settingsLCAgentModelPickerStepProvider {
		return m.renderSettingsLCAgentModelPickerProviderContent(width, bodyH, title)
	}
	if state != nil && state.Step == settingsLCAgentModelPickerStepReasoning {
		return m.renderSettingsLCAgentModelPickerReasoningContent(width, bodyH, title)
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Type", "filter", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("PgUp/PgDn", "page", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Left", "provider", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
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
		return projectListSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func settingsLCAgentModelPickerAutoLabel(settings config.EditableSettings, fieldIndex int) string {
	return settingsLCAgentModelPickerAutoLabelForProvider(settings, fieldIndex, settingsLCAgentModelPickerProvider(settings, fieldIndex))
}

func settingsLCAgentModelPickerAutoLabelForProvider(settings config.EditableSettings, fieldIndex int, provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		return settingsLCAgentUtilityDefaultLabelForProvider(settings, provider)
	}
	if fieldIndex == settingsFieldLCAgentCriticModel {
		return settingsLCAgentCriticDefaultLabelForProvider(settings, provider)
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		return settingsLCAgentVisionDefaultLabelForProvider(settings, provider)
	}
	if provider != "" {
		return lcagentDefaultModelForProvider(provider)
	}
	return settingsLCAgentMainModel(settings)
}

func settingsLCAgentModelPickerProviderOptions(fieldIndex int) []settingsLCAgentProviderOption {
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return settingsLCAgentUtilityProviderOptions()
	case settingsFieldLCAgentCriticModel:
		return settingsLCAgentCriticProviderOptions()
	case settingsFieldLCAgentVisionModel:
		return settingsLCAgentVisionProviderOptions()
	default:
		return settingsLCAgentProviderOptions()
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
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
	case settingsFieldLCAgentCriticModel:
		return settingsLCAgentCriticProviderValue(settings.LCAgentCriticProvider)
	case settingsFieldLCAgentVisionModel:
		return settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
	default:
		return settingsLCAgentMainProvider(settings)
	}
}

func settingsLCAgentModelPickerRawModel(settings config.EditableSettings, fieldIndex int) string {
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return strings.TrimSpace(settings.LCAgentUtilityModel)
	case settingsFieldLCAgentCriticModel:
		return strings.TrimSpace(settings.LCAgentCriticModel)
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
	return ""
}

func settingsLCAgentModelPickerUsesReasoning(fieldIndex int) bool {
	return settingsFieldUsesLCAgentModelPicker(fieldIndex)
}

func settingsLCAgentModelPickerRoleLabel(fieldIndex int) string {
	switch fieldIndex {
	case settingsFieldLCAgentUtilityModel:
		return "Utility model"
	case settingsFieldLCAgentCriticModel:
		return "Critic model"
	case settingsFieldLCAgentVisionModel:
		return "Vision model"
	default:
		return "Main model"
	}
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
	if state.FieldIndex != settingsFieldLCAgentModel {
		options[0].Summary = "Use provider default reasoning."
		options[0].Description = "This role currently follows provider defaults; role-specific reasoning effort can be wired here when the LCAgent runtime supports it."
		return options
	}
	modelOption := state.PendingModelOption
	if state.PendingModelAuto || strings.TrimSpace(modelOption.Model) == "" {
		modelOption = settingsLCAgentModelPickerDefaultOption(state.Models)
	}
	for _, effort := range modelOption.SupportedReasoningEfforts {
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
		if state.FieldIndex != settingsFieldLCAgentModel {
			return 0
		}
		desired = strings.TrimSpace(state.CurrentReasoning)
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
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		provider := settingsLCAgentUtilityProviderValue(settings.LCAgentUtilityProvider)
		switch provider {
		case "off":
			return "Off"
		case "main":
			return "Same as Main / " + settingsLCAgentMainModel(settings)
		default:
			model := strings.TrimSpace(settings.LCAgentUtilityModel)
			if model == "" {
				model = lcagentDefaultUtilityModelForProvider(provider)
			}
			return settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentUtilityProvider, provider) + " / " + model
		}
	}
	if fieldIndex == settingsFieldLCAgentCriticModel {
		provider := settingsLCAgentCriticProviderValue(settings.LCAgentCriticProvider)
		switch provider {
		case "off":
			return "Off"
		case "main":
			return "Same as Main / " + settingsLCAgentMainModel(settings)
		default:
			model := strings.TrimSpace(settings.LCAgentCriticModel)
			if model == "" {
				model = lcagentDefaultModelForProvider(provider)
			}
			return settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentCriticProvider, provider) + " / " + model
		}
	}
	if fieldIndex == settingsFieldLCAgentVisionModel {
		provider := settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
		switch provider {
		case "off":
			return "Off"
		case "main":
			return "Same as Main / " + settingsLCAgentMainModel(settings)
		default:
			model := strings.TrimSpace(settings.LCAgentVisionModel)
			if model == "" {
				model = lcagentDefaultModelForProvider(provider)
			}
			return settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentVisionProvider, provider) + " / " + model
		}
	}
	provider := settingsLCAgentMainProvider(settings)
	return settingsLCAgentProviderOptionLabel(provider) + " / " + settingsLCAgentMainModel(settings)
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
	default:
		return "OpenRouter"
	}
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
