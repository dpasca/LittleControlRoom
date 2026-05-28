package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingsLCAgentModelPickerState struct {
	FieldIndex     int
	Provider       string
	Current        string
	Models         []codexapp.ModelOption
	FilteredModels []codexapp.ModelOption
	FilterText     string
	Selected       int
	Loading        bool
	Err            string
}

type settingsLCAgentModelListMsg struct {
	fieldIndex int
	provider   string
	current    string
	models     []codexapp.ModelOption
	err        error
}

func settingsFieldUsesLCAgentModelPicker(index int) bool {
	return index == settingsFieldLCAgentModel || index == settingsFieldLCAgentUtilityModel
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
	cfg, provider, current, ok := settingsLCAgentModelListConfig(settings, fieldIndex)
	if !ok {
		m.status = "No LCAgent model list is available for that field."
		return m, nil
	}
	m.settingsLCAgentModelPicker = &settingsLCAgentModelPickerState{
		FieldIndex: fieldIndex,
		Provider:   provider,
		Current:    current,
		Loading:    true,
	}
	m.status = "Checking " + settingsLCAgentModelPickerProviderLabel(provider) + " models..."
	return m, settingsLCAgentModelListCmd(fieldIndex, provider, current, cfg)
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
	cfg := codexapp.LCAgentModelListConfig{
		Provider:         provider,
		Model:            current,
		IncludeAvailable: fieldIndex == settingsFieldLCAgentModel,
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
	if state == nil || state.FieldIndex != msg.fieldIndex || strings.TrimSpace(state.Provider) != strings.TrimSpace(msg.provider) {
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
	state.Err = ""
	if msg.err != nil {
		state.Err = msg.err.Error()
	}
	state.Selected = settingsLCAgentModelPickerSelection(state.FilteredModels, state.Current)
	if state.Err != "" {
		m.status = "Showing curated LCAgent models; provider list check did not complete."
	} else {
		m.status = fmt.Sprintf("Loaded %d %s models.", len(state.Models), settingsLCAgentModelPickerProviderLabel(state.Provider))
	}
	return m, nil
}

func settingsLCAgentModelPickerSelection(models []codexapp.ModelOption, current string) int {
	current = strings.TrimSpace(current)
	if current == "" {
		return 0
	}
	for i, option := range models {
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
	if state.Loading {
		switch msg.String() {
		case "esc":
			m.closeSettingsLCAgentModelPicker("LCAgent model check canceled")
		}
		return m, nil
	}
	maxIndex := len(state.FilteredModels)
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
	case "up", "shift+tab":
		state.Selected--
		if state.Selected < 0 {
			state.Selected = maxIndex
		}
		return m, nil
	case "down", "tab":
		state.Selected++
		if state.Selected > maxIndex {
			state.Selected = 0
		}
		return m, nil
	case "home":
		state.Selected = 0
		return m, nil
	case "end":
		state.Selected = maxIndex
		return m, nil
	case "backspace":
		if state.FilterText != "" {
			state.FilterText = state.FilterText[:len(state.FilterText)-1]
			m.applySettingsLCAgentModelPickerFilter()
		}
		return m, nil
	case "ctrl+u":
		state.FilterText = ""
		m.applySettingsLCAgentModelPickerFilter()
		return m, nil
	case "enter":
		if state.Selected == 0 {
			return m.applySettingsLCAgentModelPickerSelection(codexapp.ModelOption{})
		}
		index := state.Selected - 1
		if index >= 0 && index < len(state.FilteredModels) {
			return m.applySettingsLCAgentModelPickerSelection(state.FilteredModels[index])
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			state.FilterText += msg.String()
			m.applySettingsLCAgentModelPickerFilter()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) applySettingsLCAgentModelPickerFilter() {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return
	}
	state.FilteredModels = codexFilterModels(state.Models, state.FilterText)
	state.Selected = 0
	if state.Current != "" {
		state.Selected = settingsLCAgentModelPickerSelection(state.FilteredModels, state.Current)
	}
	if state.Selected == 0 && strings.TrimSpace(state.FilterText) != "" && len(state.FilteredModels) > 0 {
		state.Selected = 1
	}
	if state.Selected > len(state.FilteredModels) {
		state.Selected = len(state.FilteredModels)
	}
}

func (m Model) applySettingsLCAgentModelPickerSelection(option codexapp.ModelOption) (tea.Model, tea.Cmd) {
	state := m.settingsLCAgentModelPicker
	if state == nil {
		return m, nil
	}
	model := strings.TrimSpace(option.Model)
	fieldIndex := state.FieldIndex
	if fieldIndex >= 0 && fieldIndex < len(m.settingsFields) {
		m.settingsFields[fieldIndex].input.SetValue(model)
		m.settingsFields[fieldIndex].input.CursorEnd()
	}
	if model != "" && strings.TrimSpace(option.ModelProvider) != "" {
		switch fieldIndex {
		case settingsFieldLCAgentModel:
			if len(m.settingsFields) > settingsFieldLCAgentRoutePreset {
				m.settingsFields[settingsFieldLCAgentRoutePreset].input.SetValue("")
			}
			if len(m.settingsFields) > settingsFieldLCAgentProvider {
				m.settingsFields[settingsFieldLCAgentProvider].input.SetValue(strings.TrimSpace(option.ModelProvider))
			}
		case settingsFieldLCAgentUtilityModel:
			if len(m.settingsFields) > settingsFieldLCAgentUtilityProvider {
				m.settingsFields[settingsFieldLCAgentUtilityProvider].input.SetValue(strings.TrimSpace(option.ModelProvider))
			}
		}
	}
	label := "Main model"
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		label = "Utility model"
	}
	if strings.TrimSpace(model) == "" {
		m.closeSettingsLCAgentModelPicker(label + " reset to provider default. Press ctrl+s to save.")
		return m, nil
	}
	m.closeSettingsLCAgentModelPicker(label + " set to " + strings.TrimSpace(model) + ". Press ctrl+s to save.")
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
	if state != nil && state.FieldIndex == settingsFieldLCAgentUtilityModel {
		title = "LCAgent Utility Model"
	} else if state != nil {
		title = "LCAgent Main Model"
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Type", "filter", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if state == nil || state.Loading {
		lines = append(lines, "", commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Checking provider model list..."))
		return strings.Join(lines, "\n")
	}
	current := strings.TrimSpace(state.Current)
	if current == "" {
		current = "Auto (" + settingsLCAgentModelPickerAutoLabel(m.settingsDraftForInferenceStatus(), state.FieldIndex) + ")"
	}
	lines = append(lines, detailMutedStyle.Render("Provider: "+settingsLCAgentModelPickerProviderLabel(state.Provider)+"   Current: "+truncateText(current, max(18, width-22))))
	if state.Err != "" {
		lines = append(lines, detailMutedStyle.Render("Provider check: "+truncateText(state.Err, max(18, width-16))))
	}
	filter := state.FilterText
	if filter == "" {
		filter = "type to filter"
	}
	lines = append(lines, commandPaletteRowStyle.Render("Filter: "+filter), "")

	about := ""
	if state.Selected > 0 && state.Selected-1 < len(state.FilteredModels) {
		about = strings.TrimSpace(state.FilteredModels[state.Selected-1].Description)
	}
	listLimit := max(4, min(len(state.FilteredModels)+1, bodyH-len(lines)-5))
	start := 0
	if state.Selected >= listLimit {
		start = state.Selected - listLimit + 1
	}
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	end := min(len(state.FilteredModels)+1, start+listLimit)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderSettingsLCAgentModelPickerRow(i, state, i == state.Selected, width))
	}
	if end < len(state.FilteredModels)+1 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(state.FilteredModels)+1-end)))
	}
	if about != "" {
		lines = append(lines, "", detailSectionStyle.Render("About"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), about)...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsLCAgentModelPickerRow(index int, state *settingsLCAgentModelPickerState, selected bool, width int) string {
	label := "Auto"
	right := settingsLCAgentModelPickerAutoLabel(m.settingsDraftForInferenceStatus(), state.FieldIndex)
	if index > 0 {
		option := state.FilteredModels[index-1]
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
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		return settingsLCAgentUtilityDefaultLabel(settings)
	}
	return settingsLCAgentMainModel(settings)
}

func settingsLCAgentModelPickerProviderLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
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
