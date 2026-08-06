package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openSettingsBossChatBackendPicker() (tea.Model, tea.Cmd) {
	options := m.settingsBossChatBackendOptions()
	m.settingsBossChatPickerVisible = true
	m.settingsBossChatPickerSelected = m.settingsBossChatBackendPickerSelection(options)
	m.status = "Choose the provider for Chat."
	return m, nil
}

func (m *Model) closeSettingsBossChatBackendPicker(status string) {
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsBossChatBackendOptions() []providerChoice {
	return m.providerChoices(providerChoiceRoleBossChat, m.settingsDraftForInferenceStatus())
}

func (m Model) settingsBossChatBackendPickerSelection(options []providerChoice) int {
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldBossChatBackend)))
	return providerChoiceSelection(options, current)
}

func (m Model) updateSettingsBossChatBackendPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := m.settingsBossChatBackendOptions()
	if len(options) == 0 {
		m.closeSettingsBossChatBackendPicker("No Chat options are available right now.")
		return m, nil
	}

	if m.settingsBossChatPickerSelected < 0 {
		m.settingsBossChatPickerSelected = 0
	}
	if m.settingsBossChatPickerSelected >= len(options) {
		m.settingsBossChatPickerSelected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsBossChatBackendPicker("Chat chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsBossChatPickerSelected = wrapIndex(m.settingsBossChatPickerSelected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		m.settingsBossChatPickerSelected = wrapIndex(m.settingsBossChatPickerSelected+1, len(options))
		return m, nil
	case "enter":
		return m.applySettingsBossChatBackendPickerSelection(options[m.settingsBossChatPickerSelected])
	}
	return m, nil
}

func (m Model) applySettingsBossChatBackendPickerSelection(option providerChoice) (tea.Model, tea.Cmd) {
	modelsReset := m.applySettingsBossChatBackend(option.Value)
	status := fmt.Sprintf("Chat provider set to %s. Press ctrl+s to save.", option.Label)
	if modelsReset {
		status = fmt.Sprintf("Chat provider set to %s; Chat models reset to its defaults. Press ctrl+s to save.", option.Label)
	}
	m.closeSettingsBossChatBackendPicker(status)
	return m.focusSettingsProviderDetail(option.Value)
}

// applySettingsBossChatBackend keeps the single Chat provider selection and
// its model overrides coherent. A provider change invalidates both overrides;
// reselecting the current provider repairs any known cross-provider model left
// behind by older settings UIs.
func (m *Model) applySettingsBossChatBackend(backend config.AIBackend) bool {
	if len(m.settingsFields) <= settingsFieldBossChatBackend {
		return false
	}
	previous := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldBossChatBackend)))
	m.settingsFields[settingsFieldBossChatBackend].input.SetValue(string(backend))
	if previous != backend {
		return m.resetSettingsBossChatModels()
	}

	provider := settingsCloudModelProviderForBackend(backend)
	if provider == "" {
		return false
	}
	reset := false
	for _, fieldIndex := range []int{settingsFieldBossChatModel, settingsFieldBossUtilityModel} {
		if fieldIndex >= len(m.settingsFields) {
			continue
		}
		model := strings.TrimSpace(m.settingsFieldValue(fieldIndex))
		if _, mismatch := codexapp.LCAgentKnownModelProviderMismatch(provider, model); !mismatch {
			continue
		}
		m.settingsFields[fieldIndex].input.SetValue("")
		m.settingsFields[fieldIndex].input.CursorEnd()
		reset = true
	}
	return reset
}

func (m *Model) resetSettingsBossChatModels() bool {
	reset := false
	for _, fieldIndex := range []int{settingsFieldBossChatModel, settingsFieldBossUtilityModel} {
		if fieldIndex >= len(m.settingsFields) {
			continue
		}
		if strings.TrimSpace(m.settingsFieldValue(fieldIndex)) != "" {
			reset = true
		}
		m.settingsFields[fieldIndex].input.SetValue("")
		m.settingsFields[fieldIndex].input.CursorEnd()
	}
	return reset
}

func (m Model) renderSettingsBossChatBackendPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsBossChatBackendPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsBossChatBackendPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-18), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsBossChatBackendPickerContent(panelInnerWidth))
}

func (m Model) renderSettingsBossChatBackendPickerContent(width int) string {
	options := m.settingsBossChatBackendOptions()
	currentLabel := settingsBossChatBackendOptionLabel(m.settingsFieldValue(settingsFieldBossChatBackend))
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldBossChatBackend)))
	return renderProviderChoicePickerContent("Chat Provider", currentLabel, options, m.settingsBossChatPickerSelected, current, width)
}

func renderSettingsBossChatBackendLabel(raw string) string {
	return settingsBossChatBackendOptionLabel(raw)
}

func settingsBossChatBackendOptionLabel(raw string) string {
	current := config.AIBackend(strings.TrimSpace(raw))
	return providerChoiceLabel(Model{}.providerChoices(providerChoiceRoleBossChat, config.EditableSettings{}), current, providerChoiceRoleFallbackLabel(providerChoiceRoleBossChat))
}

func (m Model) renderSettingsBossChatBackendValue(selected bool, inputWidth int) string {
	label := renderSettingsBossChatBackendLabel(m.settingsFieldValue(settingsFieldBossChatBackend))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
