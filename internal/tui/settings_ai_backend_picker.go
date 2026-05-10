package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingsAIBackendOption struct {
	Value       config.AIBackend
	Label       string
	Summary     string
	Description string
}

func settingsAIBackendOptions() []settingsAIBackendOption {
	return []settingsAIBackendOption{
		{
			Value:       config.AIBackendCodex,
			Label:       "Codex",
			Summary:     "Use your local Codex CLI installation for project analysis.",
			Description: "Requires Codex to be installed and authenticated. No API key is stored by Little Control Room.",
		},
		{
			Value:       config.AIBackendOpenCode,
			Label:       "OpenCode",
			Summary:     "Use your local OpenCode installation for project analysis.",
			Description: "Requires OpenCode to be installed and authenticated. No API key is stored by Little Control Room.",
		},
		{
			Value:       config.AIBackendClaude,
			Label:       "Claude Code",
			Summary:     "Use your local Claude Code installation for project analysis.",
			Description: "Requires Claude Code to be installed and authenticated. Background tasks default to Haiku to keep usage lighter.",
		},
		{
			Value:       config.AIBackendMLX,
			Label:       "MLX",
			Summary:     "Use a local MLX OpenAI-compatible endpoint for project analysis.",
			Description: "Requires a local MLX server running at the configured endpoint. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOllama,
			Label:       "Ollama",
			Summary:     "Use a local Ollama OpenAI-compatible endpoint for project analysis.",
			Description: "Requires a local Ollama server running at the configured endpoint. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Use a direct OpenAI API key for project analysis.",
			Description: "Requires an OpenAI API key to be saved. This is the most predictable setup if you do not have Codex, OpenCode, or Claude Code installed.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Disabled",
			Summary:     "Turn off AI-powered project analysis.",
			Description: "Little Control Room keeps working, but summaries, classifications, and commit help stay off.",
		},
	}
}

func (m Model) openSettingsAIBackendPicker() (tea.Model, tea.Cmd) {
	options := settingsAIBackendOptions()
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

func (m Model) settingsAIBackendPickerSelection(options []settingsAIBackendOption) int {
	current := config.AIBackend(strings.TrimSpace(m.settingsFieldValue(settingsFieldAIBackend)))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsAIBackendPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := settingsAIBackendOptions()
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

func (m Model) applySettingsAIBackendPickerSelection(option settingsAIBackendOption) (tea.Model, tea.Cmd) {
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
	options := settingsAIBackendOptions()
	lines := []string{
		commandPaletteTitleStyle.Render("AI Backend"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentLabel := settingsAIBackendOptionLabel(m.settingsFieldValue(settingsFieldAIBackend))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)))
	lines = append(lines, "")

	for i, option := range options {
		lines = append(lines, m.renderSettingsAIBackendPickerRow(option, i == m.settingsAIBackendPickerSelected, option.Label == currentLabel, width))
	}

	selected := options[m.settingsAIBackendPickerSelected]
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("About"))
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(20, width-2), selected.Summary)...)
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(20, width-2), selected.Description)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsAIBackendPickerRow(option settingsAIBackendOption, selected, current bool, width int) string {
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

func settingsAIBackendOptionLabel(raw string) string {
	current := config.AIBackend(strings.TrimSpace(raw))
	for _, option := range settingsAIBackendOptions() {
		if option.Value == current {
			return option.Label
		}
	}
	return "Not configured"
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
