package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexModelPickerFocus string

const (
	codexModelPickerFocusFilter  codexModelPickerFocus = "filter"
	codexModelPickerFocusRecent  codexModelPickerFocus = "recent"
	codexModelPickerFocusModels  codexModelPickerFocus = "models"
	codexModelPickerFocusEfforts codexModelPickerFocus = "efforts"
)

type codexModelPickerState struct {
	Models         []codexapp.ModelOption
	FilteredModels []codexapp.ModelOption
	RecentModels   []codexapp.ModelOption
	FilterText     string
	SelectedModel  string
	ModelIndex     int
	RecentIndex    int
	EffortIndex    int
	Focus          codexModelPickerFocus
	Loading        bool
}

func (m Model) codexModelPickerVisible() bool {
	return m.codexModelPicker != nil
}

func (m *Model) openCodexModelPickerCmd() tea.Cmd {
	session, ok := m.currentCodexSession()
	if !ok {
		return nil
	}
	projectPath := m.codexVisibleProject
	perfOpID := m.beginAILatencyOp("Model list", projectPath, m.currentEmbeddedSessionLabel())
	return func() tea.Msg {
		startedAt := time.Now()
		models, err := session.ListModels()
		return codexModelListMsg{
			projectPath:  projectPath,
			models:       models,
			perfOpID:     perfOpID,
			perfDuration: time.Since(startedAt),
			err:          err,
		}
	}
}

func (m Model) currentEmbeddedSessionLabel() string {
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		return embeddedProvider(snapshot).Label()
	}
	return "Codex"
}

func (m Model) currentEmbeddedSessionProvider() codexapp.Provider {
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		return embeddedProvider(snapshot)
	}
	return codexapp.ProviderCodex
}

func (m *Model) openCodexModelPickerLoading() {
	m.codexModelPicker = &codexModelPickerState{
		Loading: true,
		Focus:   codexModelPickerFocusFilter,
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
		Models:         append([]codexapp.ModelOption(nil), models...),
		FilteredModels: append([]codexapp.ModelOption(nil), models...),
		Focus:          codexModelPickerFocusFilter,
	}

	recentModelIDs := m.recentCodexModels
	switch m.currentEmbeddedSessionProvider() {
	case codexapp.ProviderClaudeCode:
		recentModelIDs = m.recentClaudeModels
	case codexapp.ProviderOpenCode:
		recentModelIDs = m.recentOpenCodeModels
	}
	state.RecentModels = codexBuildRecentModels(models, recentModelIDs, 5)

	desiredModel := ""
	desiredReasoning := ""
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		desiredModel = firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
		desiredReasoning = firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
	}

	state.ModelIndex = codexModelOptionIndex(state.FilteredModels, desiredModel)
	if state.ModelIndex < 0 {
		state.ModelIndex = codexDefaultModelOptionIndex(state.FilteredModels)
	}

	if len(state.FilteredModels) > 0 && state.ModelIndex >= 0 && state.ModelIndex < len(state.FilteredModels) {
		state.SelectedModel = codexModelOptionKey(state.FilteredModels[state.ModelIndex])
	}
	if state.RecentIndex < 0 {
		state.RecentIndex = codexModelOptionIndex(state.RecentModels, state.SelectedModel)
		if state.RecentIndex < 0 {
			state.RecentIndex = 0
		}
	}

	m.codexModelPicker = state
	m.syncCodexModelPickerSelectionWithReasoning(desiredReasoning)
	m.status = "Embedded " + label + " model picker open"
}

func (m *Model) closeCodexModelPicker(status string) {
	m.codexModelPicker = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) closeCodexModelPickerAndReturnToTodo(status string) {
	m.closeCodexModelPicker(status)
	m.returnToTodoFromModelPicker()
}

func (m *Model) syncCodexModelPickerSelection() {
	m.syncCodexModelPickerSelectionWithReasoning("")
}

func (m *Model) syncCodexModelPickerSelectionWithReasoning(preferredReasoning string) {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	if len(state.FilteredModels) == 0 && len(state.RecentModels) == 0 && len(state.Models) == 0 {
		state.ModelIndex = 0
		state.EffortIndex = 0
		state.SelectedModel = ""
		return
	}
	if len(state.FilteredModels) > 0 {
		if state.ModelIndex < 0 {
			state.ModelIndex = 0
		}
		if state.ModelIndex >= len(state.FilteredModels) {
			state.ModelIndex = len(state.FilteredModels) - 1
		}
	} else {
		state.ModelIndex = 0
	}
	if len(state.RecentModels) > 0 {
		if state.RecentIndex < 0 {
			state.RecentIndex = 0
		}
		if state.RecentIndex >= len(state.RecentModels) {
			state.RecentIndex = len(state.RecentModels) - 1
		}
	} else {
		state.RecentIndex = 0
	}

	if strings.TrimSpace(preferredReasoning) == "" {
		if selectedEffort, ok := m.currentCodexReasoningOption(); ok {
			preferredReasoning = strings.TrimSpace(selectedEffort.ReasoningEffort)
		}
	}

	selectedModel, ok := m.selectedCodexModelOption()
	if !ok {
		selectedModel, ok = m.codexFallbackModelOption()
	}
	if !ok {
		state.SelectedModel = ""
		state.EffortIndex = 0
		return
	}
	m.setCodexModelPickerModel(selectedModel, preferredReasoning)
}

func (m Model) currentCodexModelOption() (codexapp.ModelOption, bool) {
	if selected, ok := m.selectedCodexModelOption(); ok {
		return selected, true
	}
	return m.codexFallbackModelOption()
}

func (m Model) selectedCodexModelOption() (codexapp.ModelOption, bool) {
	state := m.codexModelPicker
	if state == nil {
		return codexapp.ModelOption{}, false
	}
	selectedModel := strings.TrimSpace(state.SelectedModel)
	if selectedModel == "" {
		return codexapp.ModelOption{}, false
	}
	if index := codexModelOptionIndex(state.Models, selectedModel); index >= 0 {
		return state.Models[index], true
	}
	return codexapp.ModelOption{}, false
}

func (m Model) codexFallbackModelOption() (codexapp.ModelOption, bool) {
	state := m.codexModelPicker
	if state == nil {
		return codexapp.ModelOption{}, false
	}
	if state.Focus == codexModelPickerFocusRecent && len(state.RecentModels) > 0 {
		index := state.RecentIndex
		if index < 0 {
			index = 0
		}
		if index >= len(state.RecentModels) {
			index = len(state.RecentModels) - 1
		}
		return state.RecentModels[index], true
	}
	if len(state.FilteredModels) == 0 {
		return codexapp.ModelOption{}, false
	}
	index := state.ModelIndex
	if index < 0 {
		index = 0
	}
	if index >= len(state.FilteredModels) {
		index = len(state.FilteredModels) - 1
	}
	return state.FilteredModels[index], true
}

func (m *Model) setCodexModelPickerModel(option codexapp.ModelOption, preferredReasoning string) {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	key := codexModelOptionKey(option)
	if key == "" {
		return
	}
	state.SelectedModel = key
	if index := codexModelOptionIndex(state.FilteredModels, key); index >= 0 {
		state.ModelIndex = index
	}
	if index := codexModelOptionIndex(state.RecentModels, key); index >= 0 {
		state.RecentIndex = index
	}
	efforts := codexReasoningOptionsFor(option)
	if len(efforts) == 0 {
		state.EffortIndex = 0
		return
	}
	if index := codexReasoningOptionIndex(efforts, preferredReasoning); index >= 0 {
		state.EffortIndex = index
		return
	}
	state.EffortIndex = codexDefaultReasoningOptionIndex(option, efforts)
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
			m.closeCodexModelPickerAndReturnToTodo("Embedded model picker canceled")
		}
		return m, nil
	}
	if len(state.Models) == 0 {
		m.closeCodexModelPickerAndReturnToTodo("No embedded " + m.currentEmbeddedSessionLabel() + " models are available")
		return m, nil
	}

	if state.Focus == codexModelPickerFocusFilter {
		return m.updateCodexModelPickerFilterMode(msg)
	}

	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			if state.Focus == codexModelPickerFocusEfforts {
				state.EffortIndex = 0
			} else if state.Focus == codexModelPickerFocusRecent {
				state.RecentIndex = 0
			} else {
				state.ModelIndex = 0
			}
			m.syncCodexModelPickerSelection()
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		m.closeCodexModelPickerAndReturnToTodo("Embedded model picker canceled")
		return m, nil
	case "tab":
		m.codexModelPickerFocusNext()
		return m, nil
	case "shift+tab":
		m.codexModelPickerFocusPrev()
		return m, nil
	case "right", "l":
		if state.Focus == codexModelPickerFocusModels && len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
		}
		return m, nil
	case "left", "h":
		if state.Focus == codexModelPickerFocusEfforts {
			state.Focus = codexModelPickerFocusModels
			if len(state.FilteredModels) > 0 {
				m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			}
			m.status = "Choosing embedded model"
		}
		return m, nil
	case "up", "k":
		m.moveCodexModelPickerSelection(-1)
		return m, nil
	case "down", "j":
		m.moveCodexModelPickerSelection(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.moveCodexModelPickerSelection(-5)
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveCodexModelPickerSelection(5)
		return m, nil
	case "home":
		if state.Focus == codexModelPickerFocusEfforts {
			state.EffortIndex = 0
		} else if state.Focus == codexModelPickerFocusRecent {
			state.RecentIndex = 0
		} else {
			state.ModelIndex = 0
		}
		m.syncCodexModelPickerSelection()
		return m, nil
	case "end", "G":
		if state.Focus == codexModelPickerFocusEfforts {
			options := m.currentCodexReasoningOptions()
			state.EffortIndex = max(0, len(options)-1)
		} else if state.Focus == codexModelPickerFocusRecent {
			state.RecentIndex = max(0, len(state.RecentModels)-1)
		} else {
			state.ModelIndex = max(0, len(state.FilteredModels)-1)
		}
		m.syncCodexModelPickerSelection()
		return m, nil
	case "g":
		m.pendingG = true
		return m, nil
	case "enter":
		if state.Focus == codexModelPickerFocusRecent && len(state.RecentModels) > 0 {
			if len(m.currentCodexReasoningOptions()) > 0 {
				state.Focus = codexModelPickerFocusEfforts
				m.status = "Choose reasoning effort, then press Enter to apply"
				return m, nil
			}
			return m.applyCodexModelPickerSelection()
		}
		if state.Focus == codexModelPickerFocusModels && len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choose reasoning effort, then press Enter to apply"
			return m, nil
		}
		return m.applyCodexModelPickerSelection()
	case "backspace":
		state.Focus = codexModelPickerFocusFilter
		m.status = "Filter models"
		return m, nil
	}

	return m, nil
}

func (m Model) updateCodexModelPickerFilterMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.codexModelPicker
	if state == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeCodexModelPickerAndReturnToTodo("Embedded model picker canceled")
		return m, nil
	case "enter":
		if len(state.RecentModels) > 0 {
			state.Focus = codexModelPickerFocusRecent
			m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], "")
			m.status = "Choosing from recent models"
		} else if len(state.FilteredModels) > 0 {
			state.Focus = codexModelPickerFocusModels
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			m.status = "Choosing embedded model"
		}
		return m, nil
	case "tab":
		if len(state.RecentModels) > 0 {
			state.Focus = codexModelPickerFocusRecent
			m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], "")
			m.status = "Choosing from recent models"
		} else if len(state.FilteredModels) > 0 {
			state.Focus = codexModelPickerFocusModels
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			m.status = "Choosing embedded model"
		}
		return m, nil
	case "backspace":
		if len(state.FilterText) > 0 {
			state.FilterText = state.FilterText[:len(state.FilterText)-1]
			m.applyCodexModelPickerFilter()
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			state.FilterText += msg.String()
			m.applyCodexModelPickerFilter()
			return m, nil
		}
	}

	return m, nil
}

func (m *Model) applyCodexModelPickerFilter() {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	preferredReasoning := ""
	if selectedEffort, ok := m.currentCodexReasoningOption(); ok {
		preferredReasoning = strings.TrimSpace(selectedEffort.ReasoningEffort)
	}
	state.FilteredModels = codexFilterModels(state.Models, state.FilterText)
	state.ModelIndex = 0
	if len(state.FilteredModels) > 0 {
		m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], preferredReasoning)
	} else {
		state.EffortIndex = 0
	}
}

func (m *Model) codexModelPickerFocusNext() {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	switch state.Focus {
	case codexModelPickerFocusFilter:
		if len(state.RecentModels) > 0 {
			state.Focus = codexModelPickerFocusRecent
			m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], "")
			m.status = "Choosing from recent models"
		} else if len(state.FilteredModels) > 0 {
			state.Focus = codexModelPickerFocusModels
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			m.status = "Choosing embedded model"
		}
	case codexModelPickerFocusRecent:
		if len(state.FilteredModels) > 0 {
			state.Focus = codexModelPickerFocusModels
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			m.status = "Choosing embedded model"
		} else if len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
		}
	case codexModelPickerFocusModels:
		if len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
		}
	case codexModelPickerFocusEfforts:
		state.Focus = codexModelPickerFocusFilter
		m.status = "Filter models"
	}
}

func (m *Model) codexModelPickerFocusPrev() {
	state := m.codexModelPicker
	if state == nil {
		return
	}
	switch state.Focus {
	case codexModelPickerFocusFilter:
		if len(m.currentCodexReasoningOptions()) > 0 {
			state.Focus = codexModelPickerFocusEfforts
			m.status = "Choosing embedded reasoning effort"
		} else if len(state.FilteredModels) > 0 {
			state.Focus = codexModelPickerFocusModels
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
			m.status = "Choosing embedded model"
		} else if len(state.RecentModels) > 0 {
			state.Focus = codexModelPickerFocusRecent
			m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], "")
			m.status = "Choosing from recent models"
		}
	case codexModelPickerFocusRecent:
		state.Focus = codexModelPickerFocusFilter
		m.status = "Filter models"
	case codexModelPickerFocusModels:
		if len(state.RecentModels) > 0 {
			state.Focus = codexModelPickerFocusRecent
			m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], "")
			m.status = "Choosing from recent models"
		} else {
			state.Focus = codexModelPickerFocusFilter
			m.status = "Filter models"
		}
	case codexModelPickerFocusEfforts:
		state.Focus = codexModelPickerFocusModels
		if len(state.FilteredModels) > 0 {
			m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], "")
		}
		m.status = "Choosing embedded model"
	}
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
	case codexModelPickerFocusRecent:
		if len(state.RecentModels) == 0 {
			return
		}
		preferredReasoning := ""
		if selectedEffort, ok := m.currentCodexReasoningOption(); ok {
			preferredReasoning = strings.TrimSpace(selectedEffort.ReasoningEffort)
		}
		state.RecentIndex += delta
		if state.RecentIndex < 0 {
			state.RecentIndex = 0
		}
		if state.RecentIndex >= len(state.RecentModels) {
			state.RecentIndex = len(state.RecentModels) - 1
		}
		m.setCodexModelPickerModel(state.RecentModels[state.RecentIndex], preferredReasoning)
	default:
		if len(state.FilteredModels) == 0 {
			return
		}
		preferredReasoning := ""
		if selectedEffort, ok := m.currentCodexReasoningOption(); ok {
			preferredReasoning = strings.TrimSpace(selectedEffort.ReasoningEffort)
		}
		state.ModelIndex += delta
		if state.ModelIndex < 0 {
			state.ModelIndex = 0
		}
		if state.ModelIndex >= len(state.FilteredModels) {
			state.ModelIndex = len(state.FilteredModels) - 1
		}
		m.setCodexModelPickerModel(state.FilteredModels[state.ModelIndex], preferredReasoning)
	}
	m.syncCodexModelPickerSelection()
}

func (m Model) applyCodexModelPickerSelection() (tea.Model, tea.Cmd) {
	session, ok := m.currentCodexSession()
	if !ok {
		m.closeCodexModelPickerAndReturnToTodo("Embedded session unavailable")
		return m, nil
	}
	modelOption, ok := m.currentCodexModelOption()
	if !ok {
		m.closeCodexModelPickerAndReturnToTodo("No embedded " + m.currentEmbeddedSessionLabel() + " models are available")
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
	perfOpID := m.beginAILatencyOp("Model apply", projectPath, strings.TrimSpace(provider.Label()+" "+modelName+" "+effort))
	m.closeCodexModelPicker("")
	m.status = fmt.Sprintf("Staging %s (%s)...", modelName, effort)
	return m, func() tea.Msg {
		startedAt := time.Now()
		if err := session.StageModelOverride(modelName, effort); err != nil {
			return codexActionMsg{
				projectPath:  projectPath,
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          err,
			}
		}
		status := fmt.Sprintf("Embedded model set to %s with %s reasoning for the next prompt", modelName, effort)
		awaitSettle := true
		if snapshot.Busy {
			status = fmt.Sprintf("Embedded model change to %s (%s) is staged for the next fresh prompt", modelName, effort)
		}
		if strings.EqualFold(strings.TrimSpace(snapshot.Model), modelName) &&
			strings.EqualFold(strings.TrimSpace(snapshot.ReasoningEffort), effort) &&
			strings.TrimSpace(snapshot.PendingModel) == "" &&
			strings.TrimSpace(snapshot.PendingReasoning) == "" {
			status = fmt.Sprintf("Embedded model remains %s with %s reasoning", modelName, effort)
			awaitSettle = false
		}
		return codexActionMsg{
			projectPath:  projectPath,
			status:       status,
			provider:     provider,
			model:        modelName,
			reasoning:    effort,
			awaitSettle:  awaitSettle,
			perfOpID:     perfOpID,
			perfDuration: time.Since(startedAt),
		}
	}
}

func (m Model) renderCodexModelPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexModelPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexModelPicker(bodyW, bodyH int) string {
	maxHeight := bodyH - 8
	if maxHeight < 20 {
		maxHeight = 20
	}
	panelWidth := min(bodyW, min(max(72, bodyW-10), 108))
	panelInnerWidth := max(36, panelWidth-4)
	content := m.renderCodexModelPickerContent(panelInnerWidth, maxHeight)
	return renderDialogPanel(panelWidth, panelInnerWidth, content)
}

func (m Model) renderCodexModelPickerContent(width, maxHeight int) string {
	state := m.codexModelPicker
	label := m.currentEmbeddedSessionLabel()
	header := []string{
		commandPaletteTitleStyle.Render("Embedded Model Picker (" + label + ")"),
		renderDialogAction("Enter", "apply", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Tab", "focus", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}

	if state == nil || state.Loading {
		header = append(header, commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Loading available embedded "+label+" models..."))
		return strings.Join(header, "\n")
	}
	if len(state.Models) == 0 {
		header = append(header, commandPaletteHintStyle.Render("No embedded "+label+" models are available."))
		return strings.Join(header, "\n")
	}

	if snapshot, ok := m.currentCodexSnapshot(); ok {
		current := firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
		currentReasoning := firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
		if current != "" {
			header = append(header, detailValueStyle.Render("Current: "+current+"  Reasoning: "+currentReasoning))
			header = append(header, "")
		}
	}

	// Column widths: left gets 60%, right gets the rest, with a separator gap.
	const sepWidth = 3 // " │ "
	leftWidth := max(24, (width*60)/100)
	rightWidth := max(20, width-leftWidth-sepWidth)

	// ── Left column: filter + recent + models ──
	leftLines := []string{}

	filterLabel := "Filter: "
	filterValue := state.FilterText
	if state.Focus == codexModelPickerFocusFilter {
		filterValue += "█"
	}
	filterLine := filterLabel + filterValue
	if len(filterLine) > leftWidth {
		filterLine = filterLine[:leftWidth]
	}
	leftLines = append(leftLines, commandPaletteRowStyle.Render(filterLine))
	leftLines = append(leftLines, "")

	if len(state.RecentModels) > 0 {
		leftLines = append(leftLines, commandPaletteTitleStyle.Render("Recent"))
		start, end := codexPickerWindowFor(state.RecentIndex, len(state.RecentModels), 5)
		selectedModel, _ := m.currentCodexModelOption()
		for i := start; i < end; i++ {
			leftLines = append(leftLines, m.renderCodexModelPickerRow(state.RecentModels[i], codexSameModelOption(state.RecentModels[i], selectedModel), leftWidth))
		}
		leftLines = append(leftLines, "")
	}

	leftLines = append(leftLines, commandPaletteTitleStyle.Render("Models"))
	if len(state.FilteredModels) == 0 {
		leftLines = append(leftLines, commandPaletteHintStyle.Render("No models match filter."))
	} else {
		headerRows := len(header) + len(leftLines)
		modelLimit := maxHeight - headerRows - 4
		if modelLimit < 5 {
			modelLimit = 5
		}
		if modelLimit > 12 {
			modelLimit = 12
		}
		start, end := codexPickerWindowFor(state.ModelIndex, len(state.FilteredModels), modelLimit)
		if start > 0 {
			leftLines = append(leftLines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
		}
		selectedModel, _ := m.currentCodexModelOption()
		for i := start; i < end; i++ {
			leftLines = append(leftLines, m.renderCodexModelPickerRow(state.FilteredModels[i], codexSameModelOption(state.FilteredModels[i], selectedModel), leftWidth))
		}
		if end < len(state.FilteredModels) {
			leftLines = append(leftLines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(state.FilteredModels)-end)))
		}
	}

	// ── Right column: reasoning + about ──
	rightLines := []string{}

	options := m.currentCodexReasoningOptions()
	rightLines = append(rightLines, commandPaletteTitleStyle.Render("Reasoning"))
	if len(options) == 0 {
		rightLines = append(rightLines, commandPaletteHintStyle.Render("No reasoning controls."))
	} else {
		for i, option := range options {
			rightLines = append(rightLines, m.renderCodexReasoningPickerRow(option, i == state.EffortIndex, rightWidth))
		}
	}

	selectedModel, _ := m.currentCodexModelOption()
	if description := strings.TrimSpace(selectedModel.Description); description != "" {
		rightLines = append(rightLines, "")
		rightLines = append(rightLines, commandPaletteTitleStyle.Render("About"))
		rightLines = append(rightLines, commandPaletteHintStyle.Render(description))
	}

	// Pad columns to equal height, then join side-by-side.
	leftCol := strings.Join(leftLines, "\n")
	rightCol := strings.Join(rightLines, "\n")
	leftH := lipgloss.Height(leftCol)
	rightH := lipgloss.Height(rightCol)
	targetH := max(leftH, rightH)
	if leftH < targetH {
		leftCol += strings.Repeat("\n", targetH-leftH)
	}
	if rightH < targetH {
		rightCol += strings.Repeat("\n", targetH-rightH)
	}

	leftRendered := lipgloss.NewStyle().Width(leftWidth).Render(leftCol)
	rightRendered := lipgloss.NewStyle().Width(rightWidth).Render(rightCol)
	sep := lipgloss.NewStyle().
		Foreground(dialogPanelBorderColor).
		Render(strings.Repeat("│\n", targetH-1) + "│")
	columns := lipgloss.JoinHorizontal(lipgloss.Top, leftRendered, " ", sep, " ", rightRendered)

	header = append(header, columns)
	return strings.Join(header, "\n")
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

func codexModelOptionKey(option codexapp.ModelOption) string {
	return firstNonEmptyTrimmed(option.Model, option.DisplayName, option.ID)
}

func codexSameModelOption(left, right codexapp.ModelOption) bool {
	leftKey := codexModelOptionKey(left)
	rightKey := codexModelOptionKey(right)
	return leftKey != "" && rightKey != "" && strings.EqualFold(leftKey, rightKey)
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

func codexBuildRecentModels(models []codexapp.ModelOption, recentIDs []string, maxRecent int) []codexapp.ModelOption {
	if len(recentIDs) == 0 || len(models) == 0 || maxRecent <= 0 {
		return nil
	}
	modelMap := make(map[string]codexapp.ModelOption)
	for _, model := range models {
		modelMap[strings.ToLower(strings.TrimSpace(model.Model))] = model
	}
	var recent []codexapp.ModelOption
	for _, id := range recentIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if model, ok := modelMap[id]; ok {
			recent = append(recent, model)
			if len(recent) >= maxRecent {
				break
			}
		}
	}
	return recent
}

func codexFilterModels(models []codexapp.ModelOption, filter string) []codexapp.ModelOption {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return models
	}
	var filtered []codexapp.ModelOption
	for _, model := range models {
		name := strings.ToLower(model.DisplayName)
		id := strings.ToLower(model.Model)
		if strings.Contains(name, filter) || strings.Contains(id, filter) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}
