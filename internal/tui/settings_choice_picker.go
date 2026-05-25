package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingsChoiceOption struct {
	Value       string
	Label       string
	Summary     string
	Description string
}

type settingsChoicePickerState struct {
	FieldIndex int
	Selected   int
}

func settingsFieldUsesChoicePicker(fieldIndex int) bool {
	switch fieldIndex {
	case settingsFieldLCAgentRoutePreset,
		settingsFieldLCAgentReasoning,
		settingsFieldLCAgentAuto,
		settingsFieldLCAgentAdminWrite,
		settingsFieldLCAgentToolProfile,
		settingsFieldLCAgentContextProfile:
		return true
	default:
		return false
	}
}

func settingsChoiceOptionsForField(fieldIndex int) []settingsChoiceOption {
	switch fieldIndex {
	case settingsFieldLCAgentRoutePreset:
		return []settingsChoiceOption{
			{
				Value:       "",
				Label:       "Individual Fields",
				Summary:     "Use the provider, model, autonomy, and profile fields below.",
				Description: "Choose this when you want to tune LCAgent by field instead of applying a bundled coding lane.",
			},
			{
				Value:       "balanced",
				Label:       "Balanced Coding",
				Summary:     "Default coding lane with conservative tool and context budgets.",
				Description: "Good everyday choice for normal LCAgent coding work.",
			},
			{
				Value:       "quality",
				Label:       "Quality Coding",
				Summary:     "Higher-quality route with larger retained context.",
				Description: "Use this for important edits, reviews, or work where model quality matters more than cost.",
			},
			{
				Value:       "cheap-scout",
				Label:       "Cheap Scout",
				Summary:     "Lower-cost read-first lane for bounded exploration.",
				Description: "Useful for quick orientation, small follow-up tasks, and low-risk summaries.",
			},
		}
	case settingsFieldLCAgentReasoning:
		return []settingsChoiceOption{
			{Value: "", Label: "Provider Default", Summary: "Omit explicit reasoning effort.", Description: "Lets the selected provider or route preset decide the reasoning behavior."},
			{Value: "low", Label: "Low", Summary: "Use light reasoning.", Description: "Good for ordinary coding turns where responsiveness matters."},
			{Value: "medium", Label: "Medium", Summary: "Use moderate reasoning.", Description: "A middle setting for more involved tasks."},
			{Value: "high", Label: "High", Summary: "Use deeper reasoning.", Description: "Best for difficult reviews, refactors, or debugging sessions."},
		}
	case settingsFieldLCAgentAuto:
		return []settingsChoiceOption{
			{Value: "off", Label: "Off", Summary: "Deny write tools and allow only read-only commands.", Description: "Good when you want LCAgent to inspect and explain before changing files."},
			{Value: "low", Label: "Low", Summary: "Allow project-local edits plus read-only and approved verification commands.", Description: "The default coding mode. It can edit files in the workspace, run checks such as tests, lint, typecheck, or build through approved argv forms, and asks before broader commands."},
			{Value: "medium", Label: "Medium", Summary: "Allow command execution without the Low allowlist.", Description: "Use for trusted local tasks that need setup, custom build commands, managed processes, or fewer repeated approvals. Write tools still stay inside the workspace unless admin write is on."},
		}
	case settingsFieldLCAgentAdminWrite:
		return []settingsChoiceOption{
			{Value: "false", Label: "Off", Summary: "Keep write tools scoped to the workspace.", Description: "Recommended for normal project work."},
			{Value: "true", Label: "On", Summary: "Allow explicit absolute-path admin edits.", Description: "Use only for system or cross-workspace maintenance where you expect LCAgent to write outside the project."},
		}
	case settingsFieldLCAgentToolProfile:
		return []settingsChoiceOption{
			{Value: "balanced", Label: "Balanced", Summary: "Use conservative file-read budgets.", Description: "Good default for most projects and model sizes."},
			{Value: "generous", Label: "Generous", Summary: "Allow larger read budgets.", Description: "Useful with large-context models or broad refactors that need more surrounding code."},
		}
	case settingsFieldLCAgentContextProfile:
		return []settingsChoiceOption{
			{Value: "balanced", Label: "Balanced", Summary: "Compact provider-loop context earlier.", Description: "Good default for stable cost and latency."},
			{Value: "large", Label: "Large", Summary: "Retain more loop context before compaction.", Description: "Useful when the selected model can handle larger context windows."},
		}
	default:
		return nil
	}
}

func (m Model) openSettingsChoicePicker(fieldIndex int) (tea.Model, tea.Cmd) {
	options := settingsChoiceOptionsForField(fieldIndex)
	if len(options) == 0 {
		return m, nil
	}
	m.settingsChoicePicker = &settingsChoicePickerState{
		FieldIndex: fieldIndex,
		Selected:   settingsChoicePickerSelection(options, fieldIndex, m.settingsFieldValue(fieldIndex)),
	}
	m.status = "Choose " + strings.ToLower(settingsChoiceTitle(fieldIndex)) + "."
	return m, nil
}

func (m *Model) closeSettingsChoicePicker(status string) {
	m.settingsChoicePicker = nil
	if status != "" {
		m.status = status
	}
}

func settingsChoicePickerSelection(options []settingsChoiceOption, fieldIndex int, raw string) int {
	current := settingsChoiceOptionValueForField(fieldIndex, raw)
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsChoicePickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsChoicePicker == nil {
		return m, nil
	}
	fieldIndex := m.settingsChoicePicker.FieldIndex
	options := settingsChoiceOptionsForField(fieldIndex)
	if len(options) == 0 {
		m.closeSettingsChoicePicker("No choices are available right now.")
		return m, nil
	}
	if m.settingsChoicePicker.Selected < 0 {
		m.settingsChoicePicker.Selected = 0
	}
	if m.settingsChoicePicker.Selected >= len(options) {
		m.settingsChoicePicker.Selected = len(options) - 1
	}

	switch msg.String() {
	case "esc":
		m.closeSettingsChoicePicker(settingsChoiceTitle(fieldIndex) + " chooser closed")
		return m, nil
	case "up", "k", "shift+tab":
		m.settingsChoicePicker.Selected = wrapIndex(m.settingsChoicePicker.Selected-1, len(options))
		return m, nil
	case "down", "j", "tab":
		m.settingsChoicePicker.Selected = wrapIndex(m.settingsChoicePicker.Selected+1, len(options))
		return m, nil
	case "enter":
		return m.applySettingsChoicePickerSelection(options[m.settingsChoicePicker.Selected])
	}
	return m, nil
}

func (m Model) applySettingsChoicePickerSelection(option settingsChoiceOption) (tea.Model, tea.Cmd) {
	if m.settingsChoicePicker == nil {
		return m, nil
	}
	fieldIndex := m.settingsChoicePicker.FieldIndex
	if len(m.settingsFields) > fieldIndex {
		m.settingsFields[fieldIndex].input.SetValue(option.Value)
	}
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	title := settingsChoiceTitle(fieldIndex)
	m.closeSettingsChoicePicker(fmt.Sprintf("%s set to %s. %s", title, option.Label, hint))
	return m, nil
}

func (m Model) renderSettingsChoicePickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSettingsChoicePickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSettingsChoicePickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(52, bodyW-18), 76))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderSettingsChoicePickerContent(panelInnerWidth, max(12, bodyH-2)))
}

func (m Model) renderSettingsChoicePickerContent(width, bodyH int) string {
	if m.settingsChoicePicker == nil {
		return ""
	}
	fieldIndex := m.settingsChoicePicker.FieldIndex
	options := settingsChoiceOptionsForField(fieldIndex)
	if len(options) == 0 {
		return ""
	}
	selectedIndex := m.settingsChoicePicker.Selected
	if selectedIndex < 0 || selectedIndex >= len(options) {
		selectedIndex = 0
	}
	title := settingsChoiceTitle(fieldIndex)
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentValue := settingsChoiceOptionValueForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	currentLabel := settingsChoiceOptionLabelForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)), "")
	for i, option := range options {
		lines = append(lines, renderSettingsChoicePickerRow(option, i == selectedIndex, option.Value == currentValue, width))
	}
	selected := options[selectedIndex]
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

func renderSettingsChoicePickerRow(option settingsChoiceOption, selected, current bool, width int) string {
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

func settingsChoiceOptionValueForField(fieldIndex int, raw string) string {
	normalized := normalizeSettingsChoice(raw)
	switch fieldIndex {
	case settingsFieldLCAgentRoutePreset:
		switch normalized {
		case "scout", "cheap", "cheapscout":
			return "cheap-scout"
		}
		return normalized
	case settingsFieldLCAgentAdminWrite:
		if normalized == "true" || normalized == "yes" || normalized == "on" || normalized == "1" {
			return "true"
		}
		return "false"
	case settingsFieldLCAgentAuto:
		if normalized == "" {
			return "low"
		}
		return normalized
	case settingsFieldLCAgentToolProfile, settingsFieldLCAgentContextProfile:
		if normalized == "" {
			return "balanced"
		}
		return normalized
	default:
		return normalized
	}
}

func settingsChoiceOptionLabelForField(fieldIndex int, raw string) string {
	value := settingsChoiceOptionValueForField(fieldIndex, raw)
	for _, option := range settingsChoiceOptionsForField(fieldIndex) {
		if option.Value == value {
			return option.Label
		}
	}
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return "Default"
}

func settingsChoiceTitle(fieldIndex int) string {
	switch fieldIndex {
	case settingsFieldLCAgentRoutePreset:
		return "LCAgent Route Preset"
	case settingsFieldLCAgentReasoning:
		return "LCAgent Reasoning"
	case settingsFieldLCAgentAuto:
		return "LCAgent Autonomy"
	case settingsFieldLCAgentAdminWrite:
		return "LCAgent Admin Write"
	case settingsFieldLCAgentToolProfile:
		return "LCAgent Tool Profile"
	case settingsFieldLCAgentContextProfile:
		return "LCAgent Context Profile"
	default:
		return "Setting"
	}
}

func (m Model) renderSettingsChoiceValue(fieldIndex int, selected bool, inputWidth int) string {
	label := settingsChoiceOptionLabelForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
