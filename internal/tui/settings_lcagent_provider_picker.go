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
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use the direct Xiaomi MiMo route for LCAgent.",
			Description: "Uses the saved Xiaomi API key, with env file or process environment as an advanced fallback.",
		},
	}
}

func settingsLCAgentUtilityProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "main",
			Label:       "Same as Main",
			Summary:     "Use the Main Model provider and model for utility work.",
			Description: "Default utility route. Search-result refinement and other helper work use the same provider and model selected for the Main Model unless you override the Utility Model field.",
		},
		{
			Value:       "off",
			Label:       "Off",
			Summary:     "Disable utility-model search refinement.",
			Description: "LCAgent can still ask for compact deterministic search results, but oversized search output will not be condensed by a secondary model.",
		},
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter for utility work.",
			Description: "Uses the saved OpenRouter API key. Leave Utility Model blank to use the standard OpenRouter LCAgent model default.",
		},
		{
			Value:       "openai",
			Label:       "OpenAI",
			Summary:     "Use the direct OpenAI route for utility work.",
			Description: "Useful if you prefer direct OpenAI billing and behavior for small structured helper calls.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use the direct DeepSeek route for utility work.",
			Description: "Uses the saved DeepSeek API key. Leave Utility Model blank to use the standard DeepSeek LCAgent model default.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use the direct Moonshot/Kimi route for utility work.",
			Description: "Uses the saved Moonshot API key. Leave Utility Model blank to use the standard Moonshot LCAgent model default.",
		},
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use the direct Xiaomi MiMo route for utility work.",
			Description: "Uses the saved Xiaomi API key. Leave Utility Model blank to use the standard Xiaomi LCAgent model default.",
		},
	}
}

func settingsLCAgentCriticProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "off",
			Label:       "Off",
			Summary:     "Disable the post-turn critic.",
			Description: "Default for normal use. LCAgent completes turns without an extra trace-only model review.",
		},
		{
			Value:       "main",
			Label:       "Same as Main",
			Summary:     "Use the Main Model provider and model for critic review.",
			Description: "The critic runs after the lead turn completes, reviews only the captured trace packet, and can draft a follow-up for you to send.",
		},
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter for post-turn critic review.",
			Description: "Uses the saved OpenRouter API key. Leave Critic Model blank to use the standard OpenRouter LCAgent model default.",
		},
		{
			Value:       "openai",
			Label:       "OpenAI",
			Summary:     "Use direct OpenAI for post-turn critic review.",
			Description: "Useful for a high-grade no-tool review model that looks only at the captured turn packet.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use direct DeepSeek for post-turn critic review.",
			Description: "Uses the saved DeepSeek API key. Leave Critic Model blank to use the standard DeepSeek LCAgent model default.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use direct Moonshot/Kimi for post-turn critic review.",
			Description: "Uses the saved Moonshot API key. The critic receives no tools and cannot change files.",
		},
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use direct Xiaomi MiMo for post-turn critic review.",
			Description: "Uses the saved Xiaomi API key. The critic receives no tools and cannot change files.",
		},
	}
}

func settingsLCAgentVisionProviderOptions() []settingsLCAgentProviderOption {
	return []settingsLCAgentProviderOption{
		{
			Value:       "off",
			Label:       "Off",
			Summary:     "Disable image analysis.",
			Description: "Default for normal use. LCAgent still captures screenshot paths, but it will not call a vision model to inspect pixels.",
		},
		{
			Value:       "main",
			Label:       "Same as Main",
			Summary:     "Use the Main Model provider and model for image analysis.",
			Description: "Only choose this when the Main Model supports image input. The analyze_image tool will send the selected screenshot or image file to that model.",
		},
		{
			Value:       "openrouter",
			Label:       "OpenRouter",
			Summary:     "Use OpenRouter for image analysis.",
			Description: "Uses the saved OpenRouter API key. Choose a model that supports image input.",
		},
		{
			Value:       "openai",
			Label:       "OpenAI",
			Summary:     "Use direct OpenAI for image analysis.",
			Description: "Useful for screenshot review and visual QA with OpenAI vision-capable models.",
		},
		{
			Value:       "deepseek",
			Label:       "DeepSeek",
			Summary:     "Use direct DeepSeek for image analysis.",
			Description: "Uses the saved DeepSeek API key. Choose a model that supports image input.",
		},
		{
			Value:       "moonshot",
			Label:       "Moonshot",
			Summary:     "Use direct Moonshot/Kimi for image analysis.",
			Description: "Uses the saved Moonshot API key. Choose a model that supports image input.",
		},
		{
			Value:       "xiaomi",
			Label:       "Xiaomi",
			Summary:     "Use direct Xiaomi MiMo for image analysis.",
			Description: "Uses the saved Xiaomi API key. Choose a model that supports image input.",
		},
	}
}

func settingsLCAgentProviderOptionsForField(fieldIndex int) []settingsLCAgentProviderOption {
	if fieldIndex == settingsFieldLCAgentVisionProvider {
		return settingsLCAgentVisionProviderOptions()
	}
	if fieldIndex == settingsFieldLCAgentCriticProvider {
		return settingsLCAgentCriticProviderOptions()
	}
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		return settingsLCAgentUtilityProviderOptions()
	}
	return settingsLCAgentProviderOptions()
}

func (m Model) openSettingsLCAgentProviderPicker() (tea.Model, tea.Cmd) {
	fieldIndex := m.settingsLCAgentProviderPickerField()
	options := settingsLCAgentProviderOptionsForField(fieldIndex)
	m.settingsLCAgentProviderVisible = true
	m.settingsLCAgentProviderSelected = m.settingsLCAgentProviderPickerSelection(options, fieldIndex)
	m.status = "Choose the Main Model provider for LCAgent."
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		m.status = "Choose the Utility Model provider for LCAgent."
	} else if fieldIndex == settingsFieldLCAgentCriticProvider {
		m.status = "Choose the Critic Model provider for LCAgent."
	} else if fieldIndex == settingsFieldLCAgentVisionProvider {
		m.status = "Choose the Vision Model provider for LCAgent."
	}
	return m, nil
}

func (m *Model) closeSettingsLCAgentProviderPicker(status string) {
	m.settingsLCAgentProviderVisible = false
	m.settingsLCAgentProviderSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) settingsLCAgentProviderPickerField() int {
	if m.settingsSelected == settingsFieldLCAgentUtilityProvider {
		return settingsFieldLCAgentUtilityProvider
	}
	if m.settingsSelected == settingsFieldLCAgentCriticProvider {
		return settingsFieldLCAgentCriticProvider
	}
	if m.settingsSelected == settingsFieldLCAgentVisionProvider {
		return settingsFieldLCAgentVisionProvider
	}
	return settingsFieldLCAgentProvider
}

func (m Model) settingsLCAgentProviderPickerSelection(options []settingsLCAgentProviderOption, fieldIndex int) int {
	current := settingsLCAgentProviderOptionValueForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func (m Model) updateSettingsLCAgentProviderPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fieldIndex := m.settingsLCAgentProviderPickerField()
	options := settingsLCAgentProviderOptionsForField(fieldIndex)
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
	fieldIndex := m.settingsLCAgentProviderPickerField()
	if len(m.settingsFields) > fieldIndex {
		m.settingsFields[fieldIndex].input.SetValue(option.Value)
	}
	hint := "Press ctrl+s to save."
	if m.setupMode {
		hint = "Press ctrl+s to continue."
	}
	target := "LCAgent provider"
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		target = "Utility Model provider"
	} else if fieldIndex == settingsFieldLCAgentCriticProvider {
		target = "Critic Model provider"
	} else if fieldIndex == settingsFieldLCAgentVisionProvider {
		target = "Vision Model provider"
	} else {
		target = "Main Model provider"
	}
	m.closeSettingsLCAgentProviderPicker(fmt.Sprintf("%s set to %s. %s", target, option.Label, hint))
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
	fieldIndex := m.settingsLCAgentProviderPickerField()
	options := settingsLCAgentProviderOptionsForField(fieldIndex)
	title := "LCAgent Provider"
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		title = "Utility Model Provider"
	} else if fieldIndex == settingsFieldLCAgentCriticProvider {
		title = "Critic Model Provider"
	} else if fieldIndex == settingsFieldLCAgentVisionProvider {
		title = "Vision Model Provider"
	} else {
		title = "Main Model Provider"
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	currentValue := settingsLCAgentProviderOptionValueForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	currentLabel := settingsLCAgentProviderOptionLabelForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	lines = append(lines, detailField("Current", detailValueStyle.Render(currentLabel)), "")
	for i, option := range options {
		lines = append(lines, renderSettingsLCAgentProviderPickerRow(option, i == m.settingsLCAgentProviderSelected, option.Value == currentValue, width))
	}
	selected := options[m.settingsLCAgentProviderSelected]
	lines = append(lines, "", detailField("Selected", detailValueStyle.Render(selected.Label)), "", detailSectionStyle.Render("About"))
	if strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(18, width), selected.Summary)...)
	}
	if strings.TrimSpace(selected.Description) != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(18, width), selected.Description)...)
	}
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
	return settingsLCAgentProviderOptionLabelForField(settingsFieldLCAgentProvider, raw)
}

func settingsLCAgentProviderOptionValueForField(fieldIndex int, raw string) string {
	normalized := normalizeSettingsChoice(raw)
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		switch normalized {
		case "", "main", "same", "same-as-main":
			return "main"
		case "off":
			return "off"
		}
		return normalized
	}
	if fieldIndex == settingsFieldLCAgentCriticProvider {
		switch normalized {
		case "", "off":
			return "off"
		case "main", "same", "same-as-main":
			return "main"
		default:
			return normalized
		}
	}
	if fieldIndex == settingsFieldLCAgentVisionProvider {
		switch normalized {
		case "", "off":
			return "off"
		case "main", "same", "same-as-main":
			return "main"
		default:
			return normalized
		}
	}
	if normalized == "" {
		return "openrouter"
	}
	return normalized
}

func settingsLCAgentProviderOptionLabelForField(fieldIndex int, raw string) string {
	normalized := settingsLCAgentProviderOptionValueForField(fieldIndex, raw)
	for _, option := range settingsLCAgentProviderOptionsForField(fieldIndex) {
		if option.Value == normalized {
			return option.Label
		}
	}
	if fieldIndex == settingsFieldLCAgentUtilityProvider {
		return "Same as Main"
	}
	if fieldIndex == settingsFieldLCAgentCriticProvider {
		return "Off"
	}
	if fieldIndex == settingsFieldLCAgentVisionProvider {
		return "Off"
	}
	return "OpenRouter"
}

func (m Model) renderSettingsLCAgentProviderValue(fieldIndex int, selected bool, inputWidth int) string {
	label := settingsLCAgentProviderOptionLabelForField(fieldIndex, m.settingsFieldValue(fieldIndex))
	value := detailValueStyle.Bold(true).Render(label + " ▼")
	if selected {
		value = projectListSelectedRowStyle.Render(label + " ▼")
		prompt := commandPaletteHintStyle.Render("Enter to choose")
		return fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, value, "  ", prompt), inputWidth)
	}
	return fitFooterWidth(value, inputWidth)
}
