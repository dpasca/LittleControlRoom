package tui

import (
	"strings"

	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/slashcmd"

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
	return strings.TrimSpace(draft.Text)
}

func (m Model) codexSlashSuggestions() []codexslash.Suggestion {
	return codexslash.Suggestions(m.codexSlashInput())
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
	suggestion, selectedIndex, ok := slashcmd.CycleSuggestion(current, m.codexSlashSelected, suggestions, codexslash.Suggestions("/"), delta)
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
	suggestion, ok := m.selectedCodexSlashSuggestion()
	return slashcmd.ResolveInput(raw, suggestion, ok, func(input string) bool {
		_, err := codexslash.Parse(input)
		return err == nil
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
		lines = append(lines, commandPaletteHintStyle.Render("No supported embedded slash commands match. Try /new, /resume, /model, /status, /review, /skills, or /reconnect."))
	} else {
		start, end := m.codexSlashSuggestionWindow(len(suggestions))
		if start > 0 {
			lines = append(lines, commandPaletteHintStyle.Render("↑ more"))
		}
		for i := start; i < end; i++ {
			lines = append(lines, m.renderCodexSlashSuggestionRow(suggestions[i], i == m.codexSlashSelected, contentWidth))
		}
		if end < len(suggestions) {
			lines = append(lines, commandPaletteHintStyle.Render("↓ more"))
		}
	}

	if selected, ok := m.selectedCodexSlashSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, commandPaletteHintStyle.Render(selected.Summary))
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
	return m.renderCommandSuggestionRow(commands.Suggestion{
		Insert:  s.Insert,
		Display: s.Display,
		Summary: s.Summary,
	}, selected, width)
}

func codexSlashSuggestionIndex(suggestions []codexslash.Suggestion, raw string) int {
	return slashcmd.SuggestionIndex(suggestions, raw)
}
