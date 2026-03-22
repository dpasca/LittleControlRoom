package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexModelPickerFocus string

const (
	codexModelPickerFocusModels  codexModelPickerFocus = "models"
	codexModelPickerFocusEfforts codexModelPickerFocus = "efforts"
)

type codexModelPickerState struct {
	Models      []codexapp.ModelOption
	ModelIndex  int
	EffortIndex int
	Focus       codexModelPickerFocus
	Loading     bool
}

func (m Model) codexModelPickerVisible() bool {
	return m.codexModelPicker != nil
}

func (m Model) openCodexModelPickerCmd() tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		models, err := session.ListModels()
		return codexModelListMsg{
			projectPath: projectPath,
			models:      models,
			err:         err,
		}
	}
}

func (m Model) currentEmbeddedSessionLabel() string {
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		return embeddedProvider(snapshot).Label()
	}
	return "Codex"
}

func (m *Model) openCodexModelPickerLoading() {
	m.codexModelPicker = &codexModelPickerState{
		Loading: true,
		Focus:   codexModelPickerFocusModels,
	}
}

func (m *Model) openLoadedCodexModelPicker(models []codexapp.ModelOption) {
	label := m.currentEmbeddedSessionLabel()
	if len(models) == 0 {
		m.codexModelPicker = nil
		m.status = "No embedded " + label + " models are available"
		return
	}
	state := &codexModelPickerState{
		Models: append([]codexapp.ModelOption(nil), models...),
		Focus:  codexModelPickerFocusModels,
	}

	desiredModel := ""
	desiredReasoning := ""
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		desiredModel = firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
		desiredReasoning = firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
	}

	state.ModelIndex = codexModelOptionIndex(state.Models, desiredModel)
	if state.ModelIndex < 0 {
		state.ModelIndex = codexDefaultModelOptionIndex(state.Models)
	}

	efforts := codexReasoningOptionsFor(state.Models[state.ModelIndex])
	state.EffortIndex = codexReasoningOptionIndex(efforts, desiredReasoning)
	if state.EffortIndex < 0 {
		state.EffortIndex = codexDefaultReasoningOptionIndex(state.Models[state.ModelIndex], efforts)
	}

	m.codexModelPicker = state
	m.status = "Embedded " + label + " model picker open"
}

func (m *Model) closeCodexModelPicker(status string) {
	m.codexModelPicker = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) syncCodexModelPickerSelection() {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	if len(state.Models) == 0 {
		state.ModelIndex = 0
		state.EffortIndex = 0
		return
	}
	if state.ModelIndex < 0 {
		state.ModelIndex = 0
	}
	if state.ModelIndex >= len(state.Models) {
		state.ModelIndex = len(state.Models) - 1
	}

	efforts := codexReasoningOptionsFor(state.Models[state.ModelIndex])
	if len(efforts) == 0 {
		state.EffortIndex = 0
		state.Focus = codexModelPickerFocusModels
		return
	}
	if state.EffortIndex < 0 {
		state.EffortIndex = 0
	}
	if state.EffortIndex >= len(efforts) {
		state.EffortIndex = len(efforts) - 1
	}
}

func (m Model) currentCodexModelOption() (codexapp.ModelOption, bool) {
	state := m.codexModelPicker
	if state == nil || len(state.Models) == 0 {
		return codexapp.ModelOption{}, false
	}
	index := state.ModelIndex
	if index < 0 {
		index = 0
	}
	if index >= len(state.Models) {
		index = len(state.Models) - 1
	}
	return state.Models[index], true
}

func (m Model) currentCodexReasoningOptions() []codexapp.ReasoningEffortOption {
	modelOption, ok := m.currentCodexModelOption()
	if !ok {
		return nil
	}
	return codexReasoningOptionsFor(modelOption)
}

func (m Model) currentCodexReasoningOption() (codexapp.ReasoningEffortOption, bool) {
	options := m.currentCodexReasoningOptions()
	if len(options) == 0 {
		return codexapp.ReasoningEffortOption{}, false
	}
	index := 0
	if state := m.codexModelPicker; state != nil {
		index = state.EffortIndex
	}
	if index < 0 {
		index = 0
	}
	if index >= len(options) {
		index = len(options) - 1
	}
	return options[index], true
}

func (m Model) updateCodexModelPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.codexModelPicker
	if state == nil {
		return m, nil
	}
	if state.Loading {
		switch msg.String() {
		case "esc":
			m.closeCodexModelPicker("Embedded model picker canceled")
		}
		return m, nil
	}
	if len(state.Models) == 0 {
		m.closeCodexModelPicker("No embedded " + m.currentEmbeddedSessionLabel() + " models are available")
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeCodexModelPicker("Embedded model picker canceled")
		return m, nil
	case "tab":
		if state.Focus == codexModelPickerFocusEfforts || len(m.currentCodexReasoningOptions()) == 0 {
			state.Focus = codexModelPickerFocusModels
			m.status = "Choosing embedded model"
			return m, nil
		}
		state.Focus = codexModelPickerFocusEfforts
		m.status = "Choosing embedded reasoning effort"
		return m, nil
	case "shift+tab":
		if state.Focus == codexModelPickerFocusModels && len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
			return m, nil
		}
		state.Focus = codexModelPickerFocusModels
		m.status = "Choosing embedded model"
		return m, nil
	case "right", "l":
		if len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
		}
		return m, nil
	case "left", "h":
		state.Focus = codexModelPickerFocusModels
		m.status = "Choosing embedded model"
		return m, nil
	case "up", "k":
		m.moveCodexModelPickerSelection(-1)
		return m, nil
	case "down", "j":
		m.moveCodexModelPickerSelection(1)
		return m, nil
	case "pgup":
		m.moveCodexModelPickerSelection(-5)
		return m, nil
	case "pgdown":
		m.moveCodexModelPickerSelection(5)
		return m, nil
	case "home":
		if state.Focus == codexModelPickerFocusEfforts {
			state.EffortIndex = 0
		} else {
			state.ModelIndex = 0
			state.EffortIndex = codexDefaultReasoningOptionIndex(state.Models[state.ModelIndex], m.currentCodexReasoningOptions())
		}
		m.syncCodexModelPickerSelection()
		return m, nil
	case "end":
		if state.Focus == codexModelPickerFocusEfforts {
			options := m.currentCodexReasoningOptions()
			state.EffortIndex = max(0, len(options)-1)
		} else {
			state.ModelIndex = len(state.Models) - 1
			state.EffortIndex = codexDefaultReasoningOptionIndex(state.Models[state.ModelIndex], m.currentCodexReasoningOptions())
		}
		m.syncCodexModelPickerSelection()
		return m, nil
	case "enter":
		if state.Focus == codexModelPickerFocusModels && len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choose reasoning effort, then press Enter to apply"
			return m, nil
		}
		return m.applyCodexModelPickerSelection()
	}

	return m, nil
}

func (m *Model) moveCodexModelPickerSelection(delta int) {
	state := m.codexModelPicker
	if state == nil || delta == 0 {
		return
	}
	switch state.Focus {
	case codexModelPickerFocusEfforts:
		options := m.currentCodexReasoningOptions()
		if len(options) == 0 {
			return
		}
		state.EffortIndex += delta
		if state.EffortIndex < 0 {
			state.EffortIndex = 0
		}
		if state.EffortIndex >= len(options) {
			state.EffortIndex = len(options) - 1
		}
	default:
		state.ModelIndex += delta
		if state.ModelIndex < 0 {
			state.ModelIndex = 0
		}
		if state.ModelIndex >= len(state.Models) {
			state.ModelIndex = len(state.Models) - 1
		}
		options := m.currentCodexReasoningOptions()
		state.EffortIndex = codexDefaultReasoningOptionIndex(state.Models[state.ModelIndex], options)
	}
	m.syncCodexModelPickerSelection()
}

func (m Model) applyCodexModelPickerSelection() (tea.Model, tea.Cmd) {
	session, ok := m.currentCodexSession()
	if !ok {
		m.closeCodexModelPicker("Embedded session unavailable")
		return m, nil
	}
	modelOption, ok := m.currentCodexModelOption()
	if !ok {
		m.closeCodexModelPicker("No embedded " + m.currentEmbeddedSessionLabel() + " models are available")
		return m, nil
	}
	effort := strings.TrimSpace(modelOption.DefaultReasoningEffort)
	if selectedEffort, ok := m.currentCodexReasoningOption(); ok {
		effort = strings.TrimSpace(selectedEffort.ReasoningEffort)
	}
	modelName := strings.TrimSpace(modelOption.Model)
	projectPath := m.codexVisibleProject
	snapshot, _ := m.currentCodexSnapshot()
	provider := embeddedProvider(snapshot)
	if provider.Normalized() == "" {
		provider = codexapp.ProviderCodex
	}
	m.closeCodexModelPicker("")
	m.status = fmt.Sprintf("Staging %s (%s)...", modelName, effort)
	return m, func() tea.Msg {
		if err := session.StageModelOverride(modelName, effort); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		status := fmt.Sprintf("Embedded model set to %s with %s reasoning for the next prompt", modelName, effort)
		if snapshot.Busy {
			status = fmt.Sprintf("Embedded model change to %s (%s) is staged for the next fresh prompt", modelName, effort)
		}
		if strings.EqualFold(strings.TrimSpace(snapshot.Model), modelName) &&
			strings.EqualFold(strings.TrimSpace(snapshot.ReasoningEffort), effort) &&
			strings.TrimSpace(snapshot.PendingModel) == "" &&
			strings.TrimSpace(snapshot.PendingReasoning) == "" {
			status = fmt.Sprintf("Embedded model remains %s with %s reasoning", modelName, effort)
		}
		return codexActionMsg{
			projectPath: projectPath,
			status:      status,
			provider:    provider,
			model:       modelName,
			reasoning:   effort,
		}
	}
}

func (m Model) renderCodexModelPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexModelPicker(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/5)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexModelPicker(bodyW int) string {
	panelWidth := min(bodyW, min(max(72, bodyW-10), 108))
	panelInnerWidth := max(36, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCodexModelPickerContent(panelInnerWidth))
}

func (m Model) renderCodexModelPickerContent(width int) string {
	state := m.codexModelPicker
	label := m.currentEmbeddedSessionLabel()
	lines := []string{
		commandPaletteTitleStyle.Render("Embedded Model Picker"),
		commandPaletteHintStyle.Render("Choose a model and reasoning effort for upcoming embedded " + label + " prompts."),
		"",
		renderDialogAction("Enter", "apply", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Tab", "focus", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}

	if state == nil || state.Loading {
		lines = append(lines, commandPaletteHintStyle.Render("Loading available embedded "+label+" models..."))
		return strings.Join(lines, "\n")
	}
	if len(state.Models) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No embedded "+label+" models are available."))
		return strings.Join(lines, "\n")
	}

	if snapshot, ok := m.currentCodexSnapshot(); ok {
		current := firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
		currentReasoning := firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
		if current != "" {
			lines = append(lines, detailValueStyle.Render("Current: "+current+"  Reasoning: "+currentReasoning))
			lines = append(lines, "")
		}
	}

	lines = append(lines, commandPaletteTitleStyle.Render("Models"))
	start, end := codexPickerWindowFor(state.ModelIndex, len(state.Models), 6)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderCodexModelPickerRow(state.Models[i], i == state.ModelIndex, width))
	}
	if end < len(state.Models) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(state.Models)-end)))
	}

	selectedModel, _ := m.currentCodexModelOption()
	if description := strings.TrimSpace(selectedModel.Description); description != "" {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("About"))
		lines = append(lines, commandPaletteHintStyle.Render(description))
	}

	options := m.currentCodexReasoningOptions()
	lines = append(lines, "")
	lines = append(lines, commandPaletteTitleStyle.Render("Reasoning"))
	if len(options) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("This model does not advertise separate reasoning controls."))
	} else {
		for i, option := range options {
			lines = append(lines, m.renderCodexReasoningPickerRow(option, i == state.EffortIndex, width))
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderCodexModelPickerRow(option codexapp.ModelOption, selected bool, width int) string {
	parts := []string{}
	label := strings.TrimSpace(option.DisplayName)
	if label == "" {
		label = strings.TrimSpace(option.Model)
	}
	parts = append(parts, label)
	if modelName := strings.TrimSpace(option.Model); modelName != "" && !strings.EqualFold(modelName, label) {
		parts = append(parts, modelName)
	}
	if option.IsDefault {
		parts = append(parts, "default")
	}
	row := strings.Join(parts, "  ")
	if selected {
		prefix := "> "
		if m.codexModelPicker != nil && m.codexModelPicker.Focus == codexModelPickerFocusModels {
			prefix = "> "
		} else {
			prefix = "* "
		}
		return commandPaletteSelectStyle.Width(width).Render(prefix + truncateText(row, max(12, width-2)))
	}
	return commandPaletteRowStyle.Width(width).Render("  " + truncateText(row, max(12, width-2)))
}

func (m Model) renderCodexReasoningPickerRow(option codexapp.ReasoningEffortOption, selected bool, width int) string {
	row := strings.TrimSpace(option.ReasoningEffort)
	if desc := strings.TrimSpace(option.Description); desc != "" {
		row += "  " + desc
	}
	if selected {
		prefix := "> "
		if m.codexModelPicker != nil && m.codexModelPicker.Focus == codexModelPickerFocusEfforts {
			prefix = "> "
		} else {
			prefix = "* "
		}
		return commandPaletteSelectStyle.Width(width).Render(prefix + truncateText(row, max(12, width-2)))
	}
	return commandPaletteRowStyle.Width(width).Render("  " + truncateText(row, max(12, width-2)))
}

func codexModelOptionIndex(models []codexapp.ModelOption, desired string) int {
	desired = strings.TrimSpace(desired)
	if desired == "" {
		return -1
	}
	for i, option := range models {
		if strings.EqualFold(strings.TrimSpace(option.Model), desired) || strings.EqualFold(strings.TrimSpace(option.DisplayName), desired) {
			return i
		}
	}
	return -1
}

func codexDefaultModelOptionIndex(models []codexapp.ModelOption) int {
	for i, option := range models {
		if option.IsDefault {
			return i
		}
	}
	return 0
}

func codexReasoningOptionsFor(option codexapp.ModelOption) []codexapp.ReasoningEffortOption {
	if len(option.SupportedReasoningEfforts) == 0 {
		if effort := strings.TrimSpace(option.DefaultReasoningEffort); effort != "" {
			return []codexapp.ReasoningEffortOption{{
				ReasoningEffort: effort,
				Description:     "Default reasoning effort",
			}}
		}
		return nil
	}
	return append([]codexapp.ReasoningEffortOption(nil), option.SupportedReasoningEfforts...)
}

func codexReasoningOptionIndex(options []codexapp.ReasoningEffortOption, desired string) int {
	desired = strings.TrimSpace(desired)
	if desired == "" {
		return -1
	}
	for i, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.ReasoningEffort), desired) {
			return i
		}
	}
	return -1
}

func codexDefaultReasoningOptionIndex(modelOption codexapp.ModelOption, options []codexapp.ReasoningEffortOption) int {
	if len(options) == 0 {
		return 0
	}
	if index := codexReasoningOptionIndex(options, modelOption.DefaultReasoningEffort); index >= 0 {
		return index
	}
	return 0
}

func codexPickerWindowFor(selected, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || limit > total {
		limit = total
	}
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
