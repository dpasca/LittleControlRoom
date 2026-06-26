package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func settingsLCAgentWebSearchOptions() []settingsLCAgentWebSearchOption {
	return []settingsLCAgentWebSearchOption{
		{
			Value:       "off",
			Label:       "Off",
			Summary:     "Do not expose web_search to LCAgent.",
			Description: "Use this when you want LCAgent to stay purely local. New sessions will show a setup warning so it is clear that web search is unavailable.",
		},
		{
			Value:       "exa",
			Label:       "Exa",
			Summary:     "Use Exa's search API for agent-oriented web search.",
			Description: "Best fit when you already have an Exa subscription or API key. It gives LCAgent a normal web_search tool without depending on the model provider.",
		},
		{
			Value:       "google",
			Label:       "Google",
			Summary:     "Use Google Programmable Search.",
			Description: "Good when you specifically want Google ranking. Requires a Google Programmable Search API key and search engine ID.",
		},
		{
			Value:       "searxng",
			Label:       "SearXNG",
			Summary:     "Use a self-hosted or trusted SearXNG endpoint.",
			Description: "Useful for local or private setups. Public instances can be uneven, so this is best for users who already run or trust a SearXNG instance.",
		},
		{
			Value:       "browser",
			Label:       "Browser",
			Summary:     "Use managed browser automation to search Google without a search API key.",
			Description: "Good as a cheap fallback when managed browser automation is enabled. Results can be less reliable if Google asks for consent, CAPTCHA, or unusual-traffic checks.",
		},
	}
}

func (m Model) openSettingsLCAgentWebSearchPicker() (tea.Model, tea.Cmd) {
	options := settingsLCAgentWebSearchOptions()
	m.settingsLCAgentSearchPickerVisible = true
	m.settingsLCAgentSearchPickerSelected = m.settingsLCAgentWebSearchPickerSelection(options)
	m.status = "Choose the web search backend for LCAgent."
	return m, nil
}

func (m *Model) closeSettingsLCAgentWebSearchPicker(status string) {
	m.settingsLCAgentSearchPickerVisible = false
	m.settingsLCAgentSearchPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsLCAgentWebSearchPickerSelection(options []settingsLCAgentWebSearchOption) int {
	current := normalizeSettingsChoice(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsLCAgentWebSearchPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := settingsLCAgentWebSearchOptions()
	if len(options) == 0 {
		m.closeSettingsLCAgentWebSearchPicker("No LCAgent web search options are available right now.")
		return m, nil
	}
	if m.settingsLCAgentSearchPickerSelected < 0 {
		m.settingsLCAgentSearchPickerSelected = 0
	}
	if m.settingsLCAgentSearchPickerSelected >= len(options) {
		m.settingsLCAgentSearchPickerSelected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsLCAgentWebSearchPicker("LCAgent web search chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsLCAgentSearchPickerSelected = wrapIndex(m.settingsLCAgentSearchPickerSelected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		m.settingsLCAgentSearchPickerSelected = wrapIndex(m.settingsLCAgentSearchPickerSelected+1, len(options))
		return m, nil
	case "enter":
		return m.applySettingsLCAgentWebSearchPickerSelection(options[m.settingsLCAgentSearchPickerSelected])
	}
	return m, nil
}

func (m Model) applySettingsLCAgentWebSearchPickerSelection(option settingsLCAgentWebSearchOption) (tea.Model, tea.Cmd) {
	if len(m.settingsFields) > settingsFieldLCAgentWebSearchBackend {
		m.settingsFields[settingsFieldLCAgentWebSearchBackend].input.SetValue(option.Value)
	}
	m.closeSettingsLCAgentWebSearchPicker(fmt.Sprintf("LCAgent web search set to %s. Press ctrl+s to save.", option.Label))
	return m, nil
}

func (m Model) renderSettingsLCAgentWebSearchPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsLCAgentWebSearchPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsLCAgentWebSearchPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-18), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsLCAgentWebSearchPickerContent(panelInnerWidth, max(12, bodyH-2)))
}

func (m Model) renderSettingsLCAgentWebSearchPickerContent(width, bodyH int) string {
	options := settingsLCAgentWebSearchOptions()
	lines := []string{
		commandPaletteTitleStyle.Render("LCAgent Web Search"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentLabel := settingsLCAgentWebSearchOptionLabel(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)), "")
	for i, option := range options {
		lines = append(lines, m.renderSettingsLCAgentWebSearchPickerRow(option, i == m.settingsLCAgentSearchPickerSelected, option.Label == currentLabel, width))
	}
	selected := options[m.settingsLCAgentSearchPickerSelected]
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

func (m Model) renderSettingsLCAgentWebSearchPickerRow(option settingsLCAgentWebSearchOption, selected, current bool, width int) string {
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

func settingsLCAgentWebSearchOptionLabel(raw string) string {
	normalized := normalizeSettingsChoice(raw)
	for _, option := range settingsLCAgentWebSearchOptions() {
		if option.Value == normalized {
			return option.Label
		}
	}
	return "Off"
}

func (m Model) renderSettingsLCAgentWebSearchValue(selected bool, inputWidth int) string {
	label := settingsLCAgentWebSearchOptionLabel(m.settingsFieldValue(settingsFieldLCAgentWebSearchBackend))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
