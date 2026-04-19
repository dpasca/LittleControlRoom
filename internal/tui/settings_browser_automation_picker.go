package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openSettingsBrowserAutomationPicker() (tea.Model, tea.Cmd) {
	options := settingsBrowserAutomationOptions(m.currentSettingsBaseline().PlaywrightPolicy)
	if len(options) == 0 {
		m.status = "No browser automation options are available right now."
		return m, nil
	}

	m.settingsBrowserPickerVisible = true
	m.settingsBrowserPickerSelected = m.settingsBrowserAutomationPickerSelection(options)
	m.status = "Choose when Little Control Room should show browser windows."
	return m, nil
}

func (m *Model) closeSettingsBrowserAutomationPicker(status string) {
	m.settingsBrowserPickerVisible = false
	m.settingsBrowserPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsBrowserAutomationPickerSelection(options []settingsBrowserAutomationOption) int {
	current := normalizeSettingsChoice(m.settingsFieldValue(settingsFieldBrowserAutomation))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsBrowserAutomationPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := settingsBrowserAutomationOptions(m.currentSettingsBaseline().PlaywrightPolicy)
	if len(options) == 0 {
		m.closeSettingsBrowserAutomationPicker("No browser automation options are available right now.")
		return m, nil
	}

	if m.settingsBrowserPickerSelected < 0 {
		m.settingsBrowserPickerSelected = 0
	}
	if m.settingsBrowserPickerSelected >= len(options) {
		m.settingsBrowserPickerSelected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsBrowserAutomationPicker("Browser windows chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsBrowserPickerSelected--
		if m.settingsBrowserPickerSelected < 0 {
			m.settingsBrowserPickerSelected = len(options) - 1
		}
		return m, nil
	case "down", "j", "tab":
		m.settingsBrowserPickerSelected++
		if m.settingsBrowserPickerSelected >= len(options) {
			m.settingsBrowserPickerSelected = 0
		}
		return m, nil
	case "enter":
		return m.applySettingsBrowserAutomationPickerSelection(options[m.settingsBrowserPickerSelected])
	}
	return m, nil
}

func (m Model) applySettingsBrowserAutomationPickerSelection(option settingsBrowserAutomationOption) (tea.Model, tea.Cmd) {
	if len(m.settingsFields) > settingsFieldBrowserAutomation {
		m.settingsFields[settingsFieldBrowserAutomation].input.SetValue(option.Value)
	}
	m.closeSettingsBrowserAutomationPicker(fmt.Sprintf("Browser windows set to %s. Press Ctrl+S to save.", option.Label))
	return m, nil
}

func (m Model) renderSettingsBrowserAutomationPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsBrowserAutomationPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsBrowserAutomationPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-18), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsBrowserAutomationPickerContent(panelInnerWidth, max(12, bodyH-2)))
}

func (m Model) renderSettingsBrowserAutomationPickerContent(width, bodyH int) string {
	options := settingsBrowserAutomationOptions(m.currentSettingsBaseline().PlaywrightPolicy)
	lines := []string{
		commandPaletteTitleStyle.Render("Browser Windows"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if len(options) == 0 {
		lines = append(lines, "", detailMutedStyle.Render("No browser automation options are available."))
		return strings.Join(lines, "\n")
	}

	currentLabel := settingsBrowserAutomationOptionLabel(m.settingsFieldValue(settingsFieldBrowserAutomation), m.currentSettingsBaseline().PlaywrightPolicy)
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)))
	lines = append(lines, "")

	for i, option := range options {
		lines = append(lines, m.renderSettingsBrowserAutomationPickerRow(option, i == m.settingsBrowserPickerSelected, option.Label == currentLabel, width))
	}

	selected := options[m.settingsBrowserPickerSelected]
	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("About"))
	if strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(20, width-2), selected.Summary)...)
	}
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(20, width-2), selected.Description)...)
	if strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, "", detailField("Selected", detailValueStyle.Render(selected.Label)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsBrowserAutomationPickerRow(option settingsBrowserAutomationOption, selected, current bool, width int) string {
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

func (m Model) renderSettingsBrowserAutomationValue(selected bool, inputWidth int) string {
	label := settingsBrowserAutomationOptionLabel(m.settingsFieldValue(settingsFieldBrowserAutomation), m.currentSettingsBaseline().PlaywrightPolicy)
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
