package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openSettingsAIBackendPicker() (tea.Model, tea.Cmd) {
	options := m.settingsAIBackendOptions()
	m.settingsAIBackendPickerVisible = true
	m.settingsAIBackendPickerSelected = m.settingsAIBackendPickerSelection(options)
	m.status = "Choose the AI backend for project analysis."
	return m, nil
}

func (m *Model) closeSettingsAIBackendPicker(status string) {
	m.settingsAIBackendPickerVisible = false
	m.settingsAIBackendPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsAIBackendOptions() []providerChoice {
	return m.providerChoices(providerChoiceRoleProjectReports, m.settingsDraftForInferenceStatus())
}

func (m Model) settingsAIBackendPickerSelection(options []providerChoice) int {
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldAIBackend)))
	return providerChoiceSelection(options, current)
}

func (m Model) updateSettingsAIBackendPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := m.settingsAIBackendOptions()
	if len(options) == 0 {
		m.closeSettingsAIBackendPicker("No AI backend options are available right now.")
		return m, nil
	}

	if m.settingsAIBackendPickerSelected < 0 {
		m.settingsAIBackendPickerSelected = 0
	}
	if m.settingsAIBackendPickerSelected >= len(options) {
		m.settingsAIBackendPickerSelected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsAIBackendPicker("AI backend chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsAIBackendPickerSelected = wrapIndex(m.settingsAIBackendPickerSelected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		m.settingsAIBackendPickerSelected = wrapIndex(m.settingsAIBackendPickerSelected+1, len(options))
		return m, nil
	case "enter":
		return m.applySettingsAIBackendPickerSelection(options[m.settingsAIBackendPickerSelected])
	}
	return m, nil
}

func (m Model) applySettingsAIBackendPickerSelection(option providerChoice) (tea.Model, tea.Cmd) {
	if len(m.settingsFields) > settingsFieldAIBackend {
		m.settingsFields[settingsFieldAIBackend].input.SetValue(string(option.Value))
	}
	m.closeSettingsAIBackendPicker(fmt.Sprintf("AI backend set to %s. Press ctrl+s to save.", option.Label))
	return m, nil
}

func (m Model) renderSettingsAIBackendPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsAIBackendPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsAIBackendPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-18), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsAIBackendPickerContent(panelInnerWidth))
}

func (m Model) renderSettingsAIBackendPickerContent(width int) string {
	options := m.settingsAIBackendOptions()
	lines := []string{
		commandPaletteTitleStyle.Render("AI Backend"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentLabel := settingsAIBackendOptionLabel(m.settingsFieldValue(settingsFieldAIBackend))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)))
	lines = append(lines, "")

	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldAIBackend)))
	for i, option := range options {
		lines = append(lines, renderProviderChoiceRow(option, i == m.settingsAIBackendPickerSelected, option.Value == current, width))
	}

	selected := options[m.settingsAIBackendPickerSelected]
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("About"))
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(20, width-2), selected.Summary)...)
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(20, width-2), selected.Description)...)
	lines = append(lines, detailField("Status", renderProviderChoiceStatus(selected)))
	lines = append(lines, renderWrappedDetailField("Next", detailValueStyle, width, selected.NextStep))
	return strings.Join(lines, "\n")
}

func settingsAIBackendOptionLabel(raw string) string {
	current := config.AIBackend(strings.TrimSpace(raw))
	return providerChoiceLabel(Model{}.providerChoices(providerChoiceRoleProjectReports, config.EditableSettings{}), current, "Not configured")
}

func (m Model) renderSettingsAIBackendValue(selected bool, inputWidth int) string {
	label := settingsAIBackendOptionLabel(m.settingsFieldValue(settingsFieldAIBackend))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
