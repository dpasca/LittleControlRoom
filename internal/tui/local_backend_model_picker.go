package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openLocalBackendModelPicker() (tea.Model, tea.Cmd) {
	backend := m.setupSelectedLocalModelBackend()
	status := m.setupSnapshot.StatusFor(backend)
	models := localBackendPickerModels(status.Models)
	if !isLocalBackendModelPickerBackend(backend) || len(models) == 0 {
		m.status = "No discovered local models yet. Press r to refresh first."
		return m, nil
	}

	m.localModelPickerVisible = true
	m.localModelPickerBackend = backend
	m.localModelPickerSelected = m.localBackendModelPickerSelection(backend, models)
	m.status = "Choose the " + backend.Label() + " model to use for " + m.setupFocusedRoleModelPickerLabel() + "."
	return m, nil
}

func (m Model) setupFocusedRoleModelPickerLabel() string {
	if m.setupFocusedRole == setupRoleBossChat {
		return "boss chat"
	}
	return "background AI tasks"
}

func (m *Model) closeLocalBackendModelPicker(status string) {
	m.localModelPickerVisible = false
	m.localModelPickerBackend = config.AIBackendUnset
	m.localModelPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) updateLocalBackendModelPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	backend := m.localModelPickerBackend
	models := localBackendPickerModels(m.setupSnapshot.StatusFor(backend).Models)
	if !isLocalBackendModelPickerBackend(backend) || len(models) == 0 {
		m.closeLocalBackendModelPicker("No discovered local models yet. Press r to refresh first.")
		return m, nil
	}

	maxIndex := len(models)
	if m.localModelPickerSelected < 0 {
		m.localModelPickerSelected = 0
	}
	if m.localModelPickerSelected > maxIndex {
		m.localModelPickerSelected = maxIndex
	}

	switch msg.String() {
	case "esc":
		m.closeLocalBackendModelPicker("Local model picker closed")
		return m, nil
	case "up", "k":
		m.localModelPickerSelected--
		if m.localModelPickerSelected < 0 {
			m.localModelPickerSelected = maxIndex
		}
		return m, nil
	case "down", "j", "tab":
		m.localModelPickerSelected++
		if m.localModelPickerSelected > maxIndex {
			m.localModelPickerSelected = 0
		}
		return m, nil
	case "shift+tab":
		m.localModelPickerSelected--
		if m.localModelPickerSelected < 0 {
			m.localModelPickerSelected = maxIndex
		}
		return m, nil
	case "a", "backspace", "delete":
		return m.applyLocalBackendModelSelection("")
	case "enter":
		if m.localModelPickerSelected == 0 {
			return m.applyLocalBackendModelSelection("")
		}
		return m.applyLocalBackendModelSelection(models[m.localModelPickerSelected-1])
	}
	return m, nil
}

func (m Model) applyLocalBackendModelSelection(model string) (tea.Model, tea.Cmd) {
	backend := m.localModelPickerBackend
	settings := cloneEditableSettings(m.currentSettingsBaseline())
	settings.SetOpenAICompatibleModel(backend, model)
	m.settingsBaseline = &settings
	if len(m.settingsFields) > 0 {
		switch backend {
		case config.AIBackendMLX:
			m.settingsFields[settingsFieldMLXModel].input.SetValue(strings.TrimSpace(model))
		case config.AIBackendOllama:
			m.settingsFields[settingsFieldOllamaModel].input.SetValue(strings.TrimSpace(model))
		}
	}
	if strings.TrimSpace(model) == "" {
		active := firstNonEmptyString(m.setupSnapshot.StatusFor(backend).ActiveModel, firstString(localBackendPickerModels(m.setupSnapshot.StatusFor(backend).Models)))
		m.closeLocalBackendModelPicker(fmt.Sprintf("%s model reset to auto selection%s", backend.Label(), formatAutoModelSuffix(active)))
		return m, nil
	}
	m.closeLocalBackendModelPicker(fmt.Sprintf("%s model set to %s. Press Enter in /setup to save.", backend.Label(), model))
	return m, nil
}

func (m Model) localBackendModelPickerSelection(backend config.AIBackend, models []string) int {
	selected := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	if selected == "" {
		return 0
	}
	for i, model := range models {
		if strings.EqualFold(model, selected) {
			return i + 1
		}
	}
	return 0
}

func (m Model) renderLocalBackendModelPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderLocalBackendModelPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderLocalBackendModelPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(62, bodyW-10), 108))
	panelInnerWidth := max(30, panelWidth-4)
	maxContentHeight := max(12, bodyH-2)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderLocalBackendModelPickerContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderLocalBackendModelPickerContent(width, bodyH int) string {
	backend := m.localModelPickerBackend
	status := m.setupSnapshot.StatusFor(backend)
	models := localBackendPickerModels(status.Models)
	lines := []string{
		commandPaletteTitleStyle.Render(backend.Label() + " Models"),
		renderDialogAction("Up/Down", "move", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("a", "auto", pushActionKeyStyle, pushActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	if len(models) == 0 {
		lines = append(lines, "", detailMutedStyle.Render("No models discovered."))
		return strings.Join(lines, "\n")
	}

	configuredModel := strings.TrimSpace(m.currentSettingsBaseline().OpenAICompatibleModel(backend))
	autoModel := firstNonEmptyString(status.ActiveModel, firstString(models))
	if configuredModel == "" {
		lines = append(lines, detailMutedStyle.Render("Current mode: auto"+formatAutoModelSuffix(autoModel)))
	} else {
		lines = append(lines, detailMutedStyle.Render("Current override: "+configuredModel))
	}
	if endpoint := strings.TrimSpace(status.Endpoint); endpoint != "" {
		lines = append(lines, detailMutedStyle.Render("Endpoint: "+endpoint))
	}
	lines = append(lines, "")

	limit := max(4, min(len(models)+1, bodyH-10))
	start := 0
	if m.localModelPickerSelected >= limit {
		start = m.localModelPickerSelected - limit + 1
	}
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for i := start; i < min(len(models)+1, start+limit); i++ {
		lines = append(lines, m.renderLocalBackendModelPickerRow(i, models, i == m.localModelPickerSelected, width))
	}
	if end := min(len(models)+1, start+limit); end < len(models)+1 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(models)+1-end)))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderLocalBackendModelPickerRow(index int, models []string, selected bool, width int) string {
	label := ""
	right := ""
	if index == 0 {
		autoModel := firstNonEmptyString(m.setupSnapshot.StatusFor(m.localModelPickerBackend).ActiveModel, firstString(models))
		label = "Auto"
		if autoModel != "" {
			right = autoModel
		} else {
			right = "first discovered model"
		}
	} else {
		label = models[index-1]
	}

	leftWidth := max(12, width-lipgloss.Width(right)-2)
	row := truncateText(label, leftWidth)
	if right != "" {
		row = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth).Render(row), "  ", detailMutedStyle.Render(truncateText(right, max(10, width-leftWidth-2))))
	}
	if selected {
		return projectListSelectedRowStyle.Render(row)
	}
	return row
}

func isLocalBackendModelPickerBackend(backend config.AIBackend) bool {
	return backend == config.AIBackendMLX || backend == config.AIBackendOllama
}

func localBackendPickerModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(model)]; ok {
			continue
		}
		seen[strings.ToLower(model)] = struct{}{}
		out = append(out, model)
	}
	return out
}

func firstString(items []string) string {
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func formatAutoModelSuffix(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	return " (" + model + ")"
}
