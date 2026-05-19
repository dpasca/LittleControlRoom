package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func settingsLCAgentProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter as the LCAgent model gateway.",
			Description: "Good default for model-routing experiments. Uses the saved OpenRouter API key, with env file or process environment as an advanced fallback.",
		},
		{
			Value:       "openai",
			Label:       "OpenAI",
			Summary:     "Use the direct OpenAI route for LCAgent.",
			Description: "Best fit when you want direct OpenAI Responses API behavior. Reuses the shared OpenAI API key when saved.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use the direct DeepSeek route for LCAgent.",
			Description: "Uses the saved DeepSeek API key, with env file or process environment as an advanced fallback.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use the direct Moonshot/Kimi route for LCAgent.",
			Description: "Uses the saved Moonshot API key, with env file or process environment as an advanced fallback.",
		},
	}
}

func (m Model) openSettingsLCAgentProviderPicker() (tea.Model, tea.Cmd) {
	options := settingsLCAgentProviderOptions()
	m.settingsLCAgentProviderVisible = true
	m.settingsLCAgentProviderSelected = m.settingsLCAgentProviderPickerSelection(options)
	m.status = "Choose the provider for LCAgent."
	return m, nil
}

func (m *Model) closeSettingsLCAgentProviderPicker(status string) {
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsLCAgentProviderPickerSelection(options []settingsLCAgentProviderOption) int {
	current := normalizeSettingsChoice(m.settingsFieldValue(settingsFieldLCAgentProvider))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsLCAgentProviderPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := settingsLCAgentProviderOptions()
	if len(options) == 0 {
		m.closeSettingsLCAgentProviderPicker("No LCAgent provider options are available right now.")
		return m, nil
	}
	if m.settingsLCAgentProviderSelected < 0 {
		m.settingsLCAgentProviderSelected = 0
	}
	if m.settingsLCAgentProviderSelected >= len(options) {
		m.settingsLCAgentProviderSelected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentProviderPicker("LCAgent provider chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsLCAgentProviderSelected = wrapIndex(m.settingsLCAgentProviderSelected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		m.settingsLCAgentProviderSelected = wrapIndex(m.settingsLCAgentProviderSelected+1, len(options))
		return m, nil
	case "enter":
		return m.applySettingsLCAgentProviderPickerSelection(options[m.settingsLCAgentProviderSelected])
	}
	return m, nil
}

func (m Model) applySettingsLCAgentProviderPickerSelection(option settingsLCAgentProviderOption) (tea.Model, tea.Cmd) {
	if len(m.settingsFields) > settingsFieldLCAgentProvider {
		m.settingsFields[settingsFieldLCAgentProvider].input.SetValue(option.Value)
	}
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	m.closeSettingsLCAgentProviderPicker(fmt.Sprintf("LCAgent provider set to %s. %s", option.Label, hint))
	return m, nil
}

func (m Model) renderSettingsLCAgentProviderPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsLCAgentProviderPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsLCAgentProviderPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-18), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsLCAgentProviderPickerContent(panelInnerWidth, max(12, bodyH-2)))
}

func (m Model) renderSettingsLCAgentProviderPickerContent(width, bodyH int) string {
	options := settingsLCAgentProviderOptions()
	lines := []string{
		commandPaletteTitleStyle.Render("LCAgent Provider"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentLabel := settingsLCAgentProviderOptionLabel(m.settingsFieldValue(settingsFieldLCAgentProvider))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)), "")
	for i, option := range options {
		lines = append(lines, renderSettingsLCAgentProviderPickerRow(option, i == m.settingsLCAgentProviderSelected, option.Label == currentLabel, width))
	}
	selected := options[m.settingsLCAgentProviderSelected]
	lines = append(lines, "", detailSectionStyle.Render("About"))
	if strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(18, width), selected.Summary)...)
	}
	if strings.TrimSpace(selected.Description) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), selected.Description)...)
	}
	lines = append(lines, detailField("Selected", detailValueStyle.Render(selected.Label)))
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

func renderSettingsLCAgentProviderPickerRow(option settingsLCAgentProviderOption, selected, current bool, width int) string {
	labelStyle := detailValueStyle
	if selected {
		labelStyle = commandPalettePickStyle
	}
	markerStyle := commandPaletteHintStyle
	if selected {
		markerStyle = commandPalettePickStyle
	}
	marker := markerStyle.Render(" ")
	if selected {
		marker = markerStyle.Render(">")
	}
	label := truncateText(option.Label, max(10, width-4))
	row := marker + " " + labelStyle.Render(label)
	if current {
		row += "  " + detailMutedStyle.Render("(current)")
	}
	row = fitFooterWidth(row, width)
	if selected {
		return projectListSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}

func settingsLCAgentProviderOptionLabel(raw string) string {
	normalized := normalizeSettingsChoice(raw)
	for _, option := range settingsLCAgentProviderOptions() {
		if option.Value == normalized {
			return option.Label
		}
	}
	return "OpenRouter"
}

func (m Model) renderSettingsLCAgentProviderValue(selected bool, inputWidth int) string {
	label := settingsLCAgentProviderOptionLabel(m.settingsFieldValue(settingsFieldLCAgentProvider))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
