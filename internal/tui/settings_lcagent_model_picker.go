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

type settingsLCAgentModelPickerState struct {
	FieldIndex     int
	Provider       string
	Current        string
	Models         []codexapp.ModelOption
	FilteredModels []codexapp.ModelOption
	Rows           []settingsLCAgentPickerRow
	FilterInput    textinput.Model
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
	cfg, provider, current, ok := settingsLCAgentModelListConfig(settings, fieldIndex)
	if !ok {
		m.status = "No LCAgent model list is available for that field."
		return m, nil
	}
	m.settingsLCAgentModelPicker = &settingsLCAgentModelPickerState{
		FieldIndex:  fieldIndex,
		Provider:    provider,
		Current:     current,
		FilterInput: newSettingsLCAgentModelPickerFilterInput(),
		Loading:     true,
	}
	if fieldIndex == settingsFieldLCAgentModel {
		m.status = "Checking LCAgent provider/model options..."
	} else {
		m.status = "Checking " + settingsLCAgentModelPickerProviderLabel(provider) + " models..."
	}
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
	state.Rows = buildSettingsLCAgentPickerRows(state.FilteredModels, state.Provider)
	state.Err = ""
	if msg.err != nil {
		state.Err = msg.err.Error()
	}
	state.Selected = settingsLCAgentModelPickerSelection(state.FilteredModels, state.Rows, state.Current)
	if state.Err != "" {
		m.status = "Showing curated LCAgent models; provider list check did not complete."
	} else if state.FieldIndex == settingsFieldLCAgentModel {
		m.status = fmt.Sprintf("Loaded %d LCAgent provider/model options.", len(state.Models))
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
	if state.Loading {
		switch msg.String() {
		case "esc":
			m.closeSettingsLCAgentModelPicker("LCAgent model check canceled")
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
	case "up", "shift+tab":
		state.Selected = settingsLCAgentPickerPrevSelectable(state.Rows, state.Selected)
		return m, nil
	case "down", "tab":
		state.Selected = settingsLCAgentPickerNextSelectable(state.Rows, state.Selected)
		return m, nil
	case "enter":
		if state.Selected == 0 {
			return m.applySettingsLCAgentModelPickerSelection(codexapp.ModelOption{})
		}
		if state.Selected > 0 && state.Selected <= len(state.Rows) {
			row := state.Rows[state.Selected-1]
			if !row.IsHeader && row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
				return m.applySettingsLCAgentModelPickerSelection(state.FilteredModels[row.ModelIndex])
			}
		}
		return m, nil
	default:
		// Everything else — letters, digits, backspace, home/end,
		// ctrl+u, etc. — goes to the filter input for typing.
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
	providerLabel := strings.TrimSpace(option.ModelProvider)
	if providerLabel != "" {
		providerLabel = settingsLCAgentModelPickerProviderLabel(providerLabel) + " / "
	}
	if strings.TrimSpace(model) == "" {
		m.closeSettingsLCAgentModelPicker(label + " reset to provider default. Press ctrl+s to save.")
		return m, nil
	}
	m.closeSettingsLCAgentModelPicker(label + " set to " + providerLabel + strings.TrimSpace(model) + ". Press ctrl+s to save.")
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
	if state.FieldIndex == settingsFieldLCAgentModel {
		lines = append(lines, detailMutedStyle.Render("Current: "+truncateText(settingsLCAgentModelValueLabel(m.settingsDraftForInferenceStatus(), state.FieldIndex), max(18, width-9))))
	} else {
		lines = append(lines, detailMutedStyle.Render("Provider: "+settingsLCAgentModelPickerProviderLabel(state.Provider)+"   Current: "+truncateText(current, max(18, width-22))))
	}
	if state.Err != "" {
		lines = append(lines, detailMutedStyle.Render("Provider check: "+truncateText(state.Err, max(18, width-16))))
	}
	lines = append(lines, commandPaletteRowStyle.Render("Filter: "+state.FilterInput.View()), "")

	about := ""
	if state.Selected > 0 && state.Selected-1 < len(state.Rows) {
		row := state.Rows[state.Selected-1]
		if !row.IsHeader && row.ModelIndex >= 0 && row.ModelIndex < len(state.FilteredModels) {
			about = strings.TrimSpace(state.FilteredModels[row.ModelIndex].Description)
		}
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

func (m Model) renderSettingsLCAgentModelPickerRow(index int, state *settingsLCAgentModelPickerState, selected bool, width int) string {
	label := "Auto"
	right := settingsLCAgentModelPickerAutoLabel(m.settingsDraftForInferenceStatus(), state.FieldIndex)
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
	if fieldIndex == settingsFieldLCAgentUtilityModel {
		return settingsLCAgentUtilityDefaultLabel(settings)
	}
	return settingsLCAgentMainModel(settings)
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
	provider := settingsLCAgentMainProvider(settings)
	return settingsLCAgentProviderOptionLabel(provider) + " / " + settingsLCAgentMainModel(settings)
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

func settingsLCAgentPickerLastSelectable(rows []settingsLCAgentPickerRow) int {
	for i := len(rows) - 1; i >= 0; i-- {
		if !rows[i].IsHeader {
			return i + 1
		}
	}
	return 0
}
