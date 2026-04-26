package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingsBossChatBackendOption struct {
	Value       config.AIBackend
	Label       string
	Summary     string
	Description string
}

func settingsBossChatBackendOptions() []settingsBossChatBackendOption {
	return []settingsBossChatBackendOption{
		{
			Value:       config.AIBackendUnset,
			Label:       "Auto",
			Summary:     "Use OpenAI API automatically when a saved API key exists.",
			Description: "This keeps boss chat low-friction without forcing a separate backend choice. If no OpenAI API key is saved, boss chat stays unconfigured.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Use direct OpenAI API inference for the high-level /boss conversation.",
			Description: "Project reports can still use Codex, OpenCode, Claude Code, MLX, Ollama, or another backend. Boss chat only shares the saved API key.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Off",
			Summary:     "Turn off boss chat inference.",
			Description: "The classic TUI and project-report inference keep working. This only disables the high-level chat assistant.",
		},
	}
}

func (m Model) openSettingsBossChatBackendPicker() (tea.Model, tea.Cmd) {
	options := settingsBossChatBackendOptions()
	m.settingsBossChatPickerVisible = true
	m.settingsBossChatPickerSelected = m.settingsBossChatBackendPickerSelection(options)
	m.status = "Choose boss chat inference."
	return m, nil
}

func (m *Model) closeSettingsBossChatBackendPicker(status string) {
	m.settingsBossChatPickerVisible = false
	m.settingsBossChatPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsBossChatBackendPickerSelection(options []settingsBossChatBackendOption) int {
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldBossChatBackend)))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsBossChatBackendPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := settingsBossChatBackendOptions()
	if len(options) == 0 {
		m.closeSettingsBossChatBackendPicker("No boss chat options are available right now.")
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
		m.closeSettingsBossChatBackendPicker("Boss chat chooser closed")
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

func (m Model) applySettingsBossChatBackendPickerSelection(option settingsBossChatBackendOption) (tea.Model, tea.Cmd) {
	if len(m.settingsFields) > settingsFieldBossChatBackend {
		m.settingsFields[settingsFieldBossChatBackend].input.SetValue(string(option.Value))
	}
	m.closeSettingsBossChatBackendPicker(fmt.Sprintf("Boss chat set to %s. Press Ctrl+S to save.", option.Label))
	return m, nil
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
	options := settingsBossChatBackendOptions()
	lines := []string{
		commandPaletteTitleStyle.Render("Boss Chat"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentLabel := settingsBossChatBackendOptionLabel(m.settingsFieldValue(settingsFieldBossChatBackend))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)))
	lines = append(lines, "")

	for i, option := range options {
		lines = append(lines, m.renderSettingsBossChatBackendPickerRow(option, i == m.settingsBossChatPickerSelected, option.Label == currentLabel, width))
	}

	selected := options[m.settingsBossChatPickerSelected]
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("About"))
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(20, width-2), selected.Summary)...)
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(20, width-2), selected.Description)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsBossChatBackendPickerRow(option settingsBossChatBackendOption, selected, current bool, width int) string {
	labelStyle := detailValueStyle.Bold(true)
	if selected {
		labelStyle = labelStyle.Foreground(lipgloss.Color("230"))
	}
	markerStyle := commandPaletteHintStyle
	if selected {
		markerStyle = commandPalettePickStyle
	}
	marker := markerStyle.Render(" ")
	if selected {
		marker = markerStyle.Render("›")
	}
	row := marker + " " + labelStyle.Render(truncateText(option.Label, max(10, width-4)))
	if current {
		row += "  " + detailMutedStyle.Render("(current)")
	}
	row = fitFooterWidth(row, width)
	if selected {
		return projectListSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func renderSettingsBossChatBackendLabel(raw string) string {
	return settingsBossChatBackendOptionLabel(raw)
}

func settingsBossChatBackendOptionLabel(raw string) string {
	current := config.AIBackend(strings.TrimSpace(raw))
	for _, option := range settingsBossChatBackendOptions() {
		if option.Value == current {
			return option.Label
		}
	}
	return "Auto"
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
