package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/slashcmd"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) codexSlashActive() bool {
	return strings.HasPrefix(m.codexSlashInput(), "/")
}

func (m Model) codexSlashInput() string {
	draft := m.currentCodexDraft()
	if len(draft.Attachments) > 0 {
		return ""
	}
	return strings.TrimLeft(draft.Text, " \t\r\n")
}

func (m Model) codexSlashSuggestions() []codexslash.Suggestion {
	return codexSlashSuggestionsForInput(m.codexSlashInput())
}

func codexSlashSuggestionsForInput(input string) []codexslash.Suggestion {
	suggestions := append([]codexslash.Suggestion(nil), codexslash.Suggestions(input)...)
	seen := make(map[string]struct{}, len(suggestions))
	for _, suggestion := range suggestions {
		seen[strings.ToLower(strings.TrimSpace(suggestion.Insert))] = struct{}{}
	}
	for _, suggestion := range codexHostSlashSuggestions(input) {
		key := strings.ToLower(strings.TrimSpace(suggestion.Insert))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions
}

func codexHostSlashCommand(input string) (commands.Invocation, bool) {
	inv, err := commands.Parse(input)
	if err != nil {
		return commands.Invocation{}, false
	}
	switch inv.Kind {
	case commands.KindTaskActions,
		commands.KindClean,
		commands.KindCodexGC,
		commands.KindResolve,
		commands.KindRepairTerminal,
		commands.KindRun,
		commands.KindRestart,
		commands.KindRunEdit,
		commands.KindRuntime,
		commands.KindStop,
		commands.KindCommit:
		return inv, true
	default:
		return commands.Invocation{}, false
	}
}

func codexHostSlashSuggestions(input string) []codexslash.Suggestion {
	suggestions := commands.Suggestions(input)
	out := make([]codexslash.Suggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if _, ok := codexHostSlashCommand(suggestion.Insert); !ok {
			continue
		}
		out = append(out, codexslash.Suggestion{
			Insert:  suggestion.Insert,
			Display: suggestion.Display,
			Summary: suggestion.Summary,
		})
	}
	return out
}

func (m Model) dispatchCodexHostSlashCommand(inv commands.Invocation) (tea.Model, tea.Cmd) {
	if !codexHostSlashTargetsEmbeddedProject(inv.Kind) {
		return m.dispatchCommand(inv)
	}

	projectPath := normalizeProjectPath(m.codexVisibleProject)
	if projectPath == "" {
		m.status = "Embedded project unavailable"
		return m, nil
	}

	targetIndex := -1
	for i, project := range m.projects {
		if normalizeProjectPath(project.Path) == projectPath {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		m.status = "Embedded project is not available in the current project view"
		return m, nil
	}

	var focusCmd tea.Cmd
	if m.selected != targetIndex {
		focusCmd = m.focusProjectPath(m.projects[targetIndex].Path)
	}
	if inv.Kind == commands.KindRuntime {
		updated, hideCmd := m.hideCodexSession()
		hidden := normalizeUpdateModel(updated)
		dispatched, commandCmd := hidden.dispatchCommand(inv)
		return dispatched, batchCmds(focusCmd, hideCmd, commandCmd)
	}

	dispatched, commandCmd := m.dispatchCommand(inv)
	return dispatched, batchCmds(focusCmd, commandCmd)
}

func codexHostSlashTargetsEmbeddedProject(kind commands.Kind) bool {
	switch kind {
	case commands.KindRun,
		commands.KindRestart,
		commands.KindRunEdit,
		commands.KindRuntime,
		commands.KindStop,
		commands.KindCommit:
		return true
	default:
		return false
	}
}

func (m *Model) syncCodexSlashSelection() {
	suggestions := m.codexSlashSuggestions()
	if len(suggestions) == 0 {
		m.codexSlashSelected = 0
		return
	}
	if m.codexSlashSelected < 0 {
		m.codexSlashSelected = 0
	}
	if m.codexSlashSelected >= len(suggestions) {
		m.codexSlashSelected = len(suggestions) - 1
	}
}

func (m Model) selectedCodexSlashSuggestion() (codexslash.Suggestion, bool) {
	suggestions := m.codexSlashSuggestions()
	if len(suggestions) == 0 {
		return codexslash.Suggestion{}, false
	}
	index := m.codexSlashSelected
	if index < 0 {
		index = 0
	}
	if index >= len(suggestions) {
		index = len(suggestions) - 1
	}
	return suggestions[index], true
}

func (m *Model) moveCodexSlashSelection(delta int) bool {
	suggestions := m.codexSlashSuggestions()
	if len(suggestions) == 0 || delta == 0 {
		return false
	}
	m.codexSlashSelected += delta
	if m.codexSlashSelected < 0 {
		m.codexSlashSelected = len(suggestions) - 1
	}
	if m.codexSlashSelected >= len(suggestions) {
		m.codexSlashSelected = 0
	}
	return true
}

func (m *Model) applySelectedCodexSlashSuggestion() bool {
	suggestion, ok := m.selectedCodexSlashSuggestion()
	if !ok {
		return false
	}
	m.codexInput.SetValue(suggestion.Insert)
	m.codexInput.CursorEnd()
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return true
}

func (m *Model) cycleAndApplyCodexSlashSuggestion(delta int) bool {
	if !m.codexSlashActive() {
		return false
	}
	current := strings.TrimSpace(m.codexInput.Value())
	suggestions := m.codexSlashSuggestions()
	suggestion, selectedIndex, ok := slashcmd.CycleSuggestion(current, m.codexSlashSelected, suggestions, codexSlashSuggestionsForInput("/"), delta)
	if !ok {
		return false
	}
	m.codexSlashSelected = selectedIndex
	m.codexInput.SetValue(suggestion.Insert)
	m.codexInput.CursorEnd()
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return true
}

func (m Model) resolvedCodexSlashInput() string {
	raw := strings.TrimSpace(m.codexSlashInput())
	if raw == "" {
		return raw
	}

	if _, err := codexslash.Parse(raw); err == nil {
		return raw
	}
	if _, ok := codexHostSlashCommand(raw); ok {
		return raw
	}

	suggestion, ok := m.selectedCodexSlashSuggestion()
	return slashcmd.ResolveInput(raw, suggestion, ok, func(input string) bool {
		if _, err := codexslash.Parse(input); err == nil {
			return true
		}
		_, ok := codexHostSlashCommand(input)
		return ok
	})
}

func (m Model) codexSlashSuggestionWindow(total int) (int, int) {
	return slashcmd.SuggestionWindow(m.codexSlashSelected, total, min(4, total))
}

func (m Model) renderCodexSlashBlocks(width int) []string {
	if !m.codexSlashActive() {
		return nil
	}
	contentWidth := max(24, width-4)
	lines := []string{
		commandPaletteTitleStyle.Render("Embedded Slash Commands"),
		commandPaletteHintStyle.Render("Enter runs locally. Tab completes or cycles. Shift+Tab moves back."),
	}

	suggestions := m.codexSlashSuggestions()
	if len(suggestions) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No supported embedded or host slash commands match. Try /new, /handoff, /lcagent-handoff, /sessions, /model, /terminal, /skills, or /reconnect."))
	} else {
		start, end := m.codexSlashSuggestionWindow(len(suggestions))
		if start > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
		}
		for i := start; i < end; i++ {
			lines = append(lines, m.renderCodexSlashSuggestionRow(suggestions[i], i == m.codexSlashSelected, contentWidth))
		}
		if end < len(suggestions) {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(suggestions)-end)))
		}
	}

	if selected, ok := m.selectedCodexSlashSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
		summary := renderWrappedDialogTextLines(commandPaletteHintStyle, contentWidth, strings.TrimSpace(selected.Summary))
		if len(summary) == 0 {
			lines = append(lines, commandPaletteHintStyle.Render(selected.Summary))
		} else {
			lines = append(lines, summary...)
		}
	}

	return []string{
		lipgloss.NewStyle().
			BorderLeft(true).
			BorderForeground(lipgloss.Color("153")).
			PaddingLeft(1).
			Render(strings.Join(lines, "\n")),
	}
}

func (m Model) renderCodexSlashSuggestionRow(s codexslash.Suggestion, selected bool, width int) string {
	_ = width
	left := s.Display
	if left == "" {
		left = s.Insert
	}
	right := strings.TrimSpace(s.Summary)
	marker := " "
	if selected {
		marker = ">"
	}
	row := marker + " " + left
	if right != "" {
		row += "  " + right
	}
	if selected {
		return commandPaletteSelectStyle.Render(row)
	}
	return commandPaletteRowStyle.Render(row)
}

func codexSlashSuggestionIndex(suggestions []codexslash.Suggestion, raw string) int {
	return slashcmd.SuggestionIndex(suggestions, raw)
}
