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
	m.status = "Choose the helper for project reports."
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
	return m.focusSettingsProviderDetail(option.Value)
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
	currentLabel := settingsAIBackendOptionLabel(m.settingsFieldValue(settingsFieldAIBackend))
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldAIBackend)))
	return renderProviderChoicePickerContent(providerChoiceRoleTitle(providerChoiceRoleProjectReports), currentLabel, options, m.settingsAIBackendPickerSelected, current, width)
}

func settingsAIBackendOptionLabel(raw string) string {
	current := config.AIBackend(strings.TrimSpace(raw))
	return providerChoiceLabel(Model{}.providerChoices(providerChoiceRoleProjectReports, config.EditableSettings{}), current, providerChoiceRoleFallbackLabel(providerChoiceRoleProjectReports))
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
