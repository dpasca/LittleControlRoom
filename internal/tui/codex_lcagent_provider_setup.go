package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexLCAgentProviderSetupState struct {
	ProjectPath  string
	Provider     string
	Model        codexapp.ModelOption
	Reasoning    string
	FieldIndexes []int
	Fields       []settingsField
	Selected     int
	Saving       bool
	Error        string
}

func (m Model) lcagentModelProviderReady(provider string) bool {
	state, _, _ := lcagentCredentialSmokeCheckForProvider(m.currentSettingsBaseline(), provider)
	return state == "ready"
}

func lcagentCredentialSmokeCheckForProvider(settings config.EditableSettings, provider string) (string, lipgloss.Style, string) {
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = strings.ToLower(strings.TrimSpace(provider))
	return lcagentCredentialSmokeCheck(settings)
}

func (m Model) openCodexLCAgentProviderSetup(option codexapp.ModelOption, reasoning string) (tea.Model, tea.Cmd) {
	provider := strings.ToLower(strings.TrimSpace(option.ModelProvider))
	if provider == "" {
		m.status = "That LCAgent model has no provider to set up."
		return m, nil
	}
	settings := m.currentSettingsBaseline()
	fieldIndexes := appendSettingsLCAgentConnectionFields(nil, provider)
	if len(fieldIndexes) == 0 {
		m.status = "No setup fields are available for " + settingsLCAgentProviderOptionLabel(provider) + "."
		return m, nil
	}
	allFields := newSettingsFields(settings)
	fields := make([]settingsField, 0, len(fieldIndexes))
	for _, fieldIndex := range fieldIndexes {
		if fieldIndex < 0 || fieldIndex >= len(allFields) {
			continue
		}
		fields = append(fields, allFields[fieldIndex])
	}
	if len(fields) == 0 {
		m.status = "No setup fields are available for " + settingsLCAgentProviderOptionLabel(provider) + "."
		return m, nil
	}
	selected := codexLCAgentProviderSetupInitialSelection(provider, fieldIndexes, settings)
	if selected < 0 || selected >= len(fields) {
		selected = 0
	}
	state := &codexLCAgentProviderSetupState{
		ProjectPath:  strings.TrimSpace(m.codexVisibleProject),
		Provider:     provider,
		Model:        option,
		Reasoning:    strings.TrimSpace(reasoning),
		FieldIndexes: fieldIndexes,
		Fields:       fields,
		Selected:     selected,
	}
	state.focusSelected()
	m.codexLCAgentProviderSetup = state
	m.status = "Set up " + settingsLCAgentProviderOptionLabel(provider) + " for LCAgent."
	return m, nil
}

func codexLCAgentProviderSetupInitialSelection(provider string, fieldIndexes []int, settings config.EditableSettings) int {
	if strings.EqualFold(provider, "xiaomi") &&
		config.LooksLikeXiaomiTokenPlanAPIKey(settings.XiaomiAPIKey) &&
		config.LooksLikeRegularXiaomiBaseURL(settings.XiaomiBaseURL) {
		for i, fieldIndex := range fieldIndexes {
			if fieldIndex == settingsFieldXiaomiBaseURL {
				return i
			}
		}
	}
	credentialField := settingsLCAgentCredentialFieldForProvider(provider)
	for i, fieldIndex := range fieldIndexes {
		if fieldIndex == credentialField {
			return i
		}
	}
	return 0
}

func (s *codexLCAgentProviderSetupState) focusSelected() {
	if s == nil {
		return
	}
	for i := range s.Fields {
		if i == s.Selected {
			s.Fields[i].input.Focus()
			s.Fields[i].input.CursorEnd()
		} else {
			s.Fields[i].input.Blur()
		}
	}
}

func (m Model) updateCodexLCAgentProviderSetupMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.codexLCAgentProviderSetup
	if state == nil {
		return m, nil
	}
	if state.Saving {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.codexLCAgentProviderSetup = nil
		m.status = "LCAgent provider setup canceled"
		return m, nil
	case "tab", "down":
		state.Selected = wrapIndex(state.Selected+1, len(state.Fields))
		state.focusSelected()
		return m, nil
	case "shift+tab", "up":
		state.Selected = wrapIndex(state.Selected-1, len(state.Fields))
		state.focusSelected()
		return m, nil
	case "enter", "ctrl+s":
		return m.saveCodexLCAgentProviderSetup()
	default:
		if state.Selected >= 0 && state.Selected < len(state.Fields) {
			field, cmd := state.Fields[state.Selected].input.Update(msg)
			state.Fields[state.Selected].input = field
			state.Error = ""
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) saveCodexLCAgentProviderSetup() (tea.Model, tea.Cmd) {
	state := m.codexLCAgentProviderSetup
	if state == nil {
		return m, nil
	}
	if fieldIndex, ok := state.missingRequiredCredentialField(); ok {
		state.Selected = max(0, fieldIndex)
		state.focusSelected()
		state.Error = "Paste " + lcagentProviderSavedKeyLabel(state.Provider) + " before saving."
		m.status = state.Error
		return m, nil
	}
	settings := m.settingsFromCodexLCAgentProviderSetup(state)
	if strings.EqualFold(state.Provider, "xiaomi") &&
		config.LooksLikeXiaomiTokenPlanAPIKey(settings.XiaomiAPIKey) &&
		config.LooksLikeRegularXiaomiBaseURL(settings.XiaomiBaseURL) {
		for i, fieldIndex := range state.FieldIndexes {
			if fieldIndex == settingsFieldXiaomiBaseURL {
				state.Selected = i
				state.focusSelected()
				break
			}
		}
		state.Error = "Token Plan key needs the regional Xiaomi token-plan base URL."
		m.status = state.Error
		return m, nil
	}
	state.Saving = true
	state.Error = ""
	m.status = "Saving " + settingsLCAgentProviderOptionLabel(state.Provider) + " setup..."
	path := m.currentWritableConfigPath()
	projectPath := strings.TrimSpace(state.ProjectPath)
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

func (s *codexLCAgentProviderSetupState) missingRequiredCredentialField() (int, bool) {
	if s == nil {
		return 0, false
	}
	credentialField := settingsLCAgentCredentialFieldForProvider(s.Provider)
	if credentialField < 0 {
		return 0, false
	}
	for i, fieldIndex := range s.FieldIndexes {
		if fieldIndex == credentialField && strings.TrimSpace(s.Fields[i].input.Value()) == "" {
			return i, true
		}
	}
	return 0, false
}

func (m Model) settingsFromCodexLCAgentProviderSetup(state *codexLCAgentProviderSetupState) config.EditableSettings {
	settings := m.currentSettingsBaseline()
	if state == nil {
		return settings
	}
	for i, fieldIndex := range state.FieldIndexes {
		if i < 0 || i >= len(state.Fields) {
			continue
		}
		value := strings.TrimSpace(state.Fields[i].input.Value())
		switch fieldIndex {
		case settingsFieldOpenAIAPIKey:
			settings.OpenAIAPIKey = value
		case settingsFieldOpenRouterAPIKey:
			settings.OpenRouterAPIKey = value
		case settingsFieldDeepSeekAPIKey:
			settings.DeepSeekAPIKey = value
		case settingsFieldMoonshotAPIKey:
			settings.MoonshotAPIKey = value
		case settingsFieldXiaomiBaseURL:
			settings.XiaomiBaseURL = value
		case settingsFieldXiaomiAPIKey:
			settings.XiaomiAPIKey = value
		}
	}
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = strings.ToLower(strings.TrimSpace(state.Provider))
	settings.EmbeddedLCAgentModel = strings.TrimSpace(state.Model.Model)
	settings.EmbeddedLCAgentReasoning = strings.TrimSpace(state.Reasoning)
	settings.RecentLCAgentModels = appendRecentString(settings.RecentLCAgentModels, formatLCAgentRecentModelID(state.Provider, state.Model.Model), 5)
	return config.NormalizeEditableSettings(settings)
}

func appendRecentString(values []string, value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return append([]string(nil), values...)
	}
	out := make([]string, 0, len(values)+1)
	out = append(out, value)
	for _, existing := range values {
		existing = strings.TrimSpace(existing)
		if existing == "" || strings.EqualFold(existing, value) {
			continue
		}
		out = append(out, existing)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (m Model) applyCodexLCAgentProviderSetupSavedMsg(msg codexLCAgentProviderSetupSavedMsg) (tea.Model, tea.Cmd) {
	if state := m.codexLCAgentProviderSetup; state != nil {
		state.Saving = false
	}
	if msg.err != nil {
		if state := m.codexLCAgentProviderSetup; state != nil {
			state.Error = msg.err.Error()
		}
		m.reportError("LCAgent provider setup save failed", msg.err, msg.projectPath)
		return m, nil
	}
	saved := cloneEditableSettings(msg.settings)
	m.settingsBaseline = &saved
	m.settingsConfigPath = strings.TrimSpace(msg.path)
	m.embeddedModelPrefs = embeddedModelPreferencesFromSettings(saved)
	m.recentLCAgentModels = append([]string(nil), saved.RecentLCAgentModels...)
	m.codexLCAgentProviderSetup = nil
	m.codexModelPicker = nil
	projectPath := strings.TrimSpace(msg.projectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(m.codexVisibleProject)
	}
	m.status = fmt.Sprintf("Saved %s setup. Restarting LCAgent with %s.", msg.path, saved.EmbeddedLCAgentModel)
	cmds := []tea.Cmd{m.applyEditableSettingsCmd(saved)}
	if projectPath != "" {
		cmds = append(cmds, m.reloadEmbeddedLCAgentAfterSettingsCmd(projectPath, saved))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) renderCodexLCAgentProviderSetupOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexLCAgentProviderSetupPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexLCAgentProviderSetupPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-14), 92))
	panelInnerWidth := max(32, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCodexLCAgentProviderSetupContent(panelInnerWidth, max(12, bodyH-2)))
}

func (m Model) renderCodexLCAgentProviderSetupContent(width, bodyH int) string {
	state := m.codexLCAgentProviderSetup
	if state == nil {
		return ""
	}
	providerLabel := settingsLCAgentProviderOptionLabel(state.Provider)
	model := strings.TrimSpace(state.Model.Model)
	lines := []string{
		commandPaletteTitleStyle.Render("Set Up " + providerLabel),
		renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Tab", "field", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		"",
		renderWrappedDetailField("Model", detailValueStyle, width, model),
	}
	stateName, style, detail := lcagentCredentialSmokeCheckForProvider(m.settingsFromCodexLCAgentProviderSetup(state), state.Provider)
	if detail != "" {
		lines = append(lines, renderWrappedDetailField("Connection", style, width, stateName+" - "+detail))
	}
	if strings.TrimSpace(state.Error) != "" {
		lines = append(lines, renderWrappedDetailField("Error", detailDangerStyle, width, state.Error))
	}
	lines = append(lines, "")
	for i, field := range state.Fields {
		field.input.Width = max(24, width-24)
		lines = append(lines, renderCodexLCAgentProviderSetupFieldRow(state.FieldIndexes[i], field, i == state.Selected, width))
	}
	if state.Saving {
		lines = append(lines, "", commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Saving setup..."))
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

func renderCodexLCAgentProviderSetupFieldRow(fieldIndex int, field settingsField, selected bool, width int) string {
	labelWidth := min(22, max(12, width/3))
	inputWidth := max(18, width-labelWidth-1)
	label := "  " + field.label
	labelStyle := detailLabelStyle
	if selected {
		label = "> " + field.label
		labelStyle = commandPalettePickStyle
	}
	field.input.Width = inputWidth
	row := labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) + " " + field.input.View()
	_ = fieldIndex
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(fitFooterWidth(row, width))
	}
	return lipgloss.NewStyle().Width(width).Render(fitFooterWidth(row, width))
}
