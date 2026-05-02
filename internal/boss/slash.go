package boss

import (
	"fmt"
	"strings"

	"lcroom/internal/bossslash"
	"lcroom/internal/slashcmd"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) SlashActive() bool {
	return m.bossSlashActive()
}

func (m Model) bossSlashActive() bool {
	return strings.HasPrefix(m.bossSlashInput(), "/")
}

func (m Model) bossSlashInput() string {
	return strings.TrimSpace(m.input.Value())
}

func (m Model) bossSlashSuggestions() []bossslash.Suggestion {
	return bossslash.Suggestions(m.bossSlashInput())
}

func (m *Model) syncBossSlashSelection() {
	suggestions := m.bossSlashSuggestions()
	if len(suggestions) == 0 {
		m.bossSlashSelected = 0
		return
	}
	if m.bossSlashSelected < 0 {
		m.bossSlashSelected = 0
	}
	if m.bossSlashSelected >= len(suggestions) {
		m.bossSlashSelected = len(suggestions) - 1
	}
}

func (m Model) selectedBossSlashSuggestion() (bossslash.Suggestion, bool) {
	suggestions := m.bossSlashSuggestions()
	if len(suggestions) == 0 {
		return bossslash.Suggestion{}, false
	}
	index := m.bossSlashSelected
	if index < 0 {
		index = 0
	}
	if index >= len(suggestions) {
		index = len(suggestions) - 1
	}
	return suggestions[index], true
}

func (m *Model) cycleAndApplyBossSlashSuggestion(delta int) bool {
	if !m.bossSlashActive() {
		return false
	}
	current := strings.TrimSpace(m.input.Value())
	suggestions := m.bossSlashSuggestions()
	suggestion, selectedIndex, ok := slashcmd.CycleSuggestion(current, m.bossSlashSelected, suggestions, bossslash.Suggestions("/"), delta)
	if !ok {
		return false
	}
	m.bossSlashSelected = selectedIndex
	m.input.SetValue(suggestion.Insert)
	m.input.CursorEnd()
	m.syncBossSlashSelection()
	m.syncLayout(false)
	return true
}

func (m Model) resolvedBossSlashInput() string {
	raw := strings.TrimSpace(m.bossSlashInput())
	if raw == "" {
		return raw
	}
	suggestion, ok := m.selectedBossSlashSuggestion()
	return slashcmd.ResolveInput(raw, suggestion, ok, func(input string) bool {
		_, err := bossslash.Parse(input)
		return err == nil
	})
}

func (m Model) runBossSlashCommand(raw string) (tea.Model, tea.Cmd) {
	inv, err := bossslash.Parse(m.resolvedBossSlashInput())
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.input.Reset()
	m.bossSlashSelected = 0
	switch inv.Kind {
	case bossslash.KindNew:
		m.messages = nil
		m.sessionTitle = ""
		m.syncLayout(true)
		if !m.hasPersistentSessions() {
			m.status = "Started a fresh boss chat session"
			if strings.TrimSpace(inv.Prompt) != "" {
				return m.submitChatMessage(inv.Prompt)
			}
			return m, nil
		}
		m.sessionLoaded = false
		m.status = "Starting a fresh boss chat session..."
		return m, m.newBossSessionCmd(inv.Prompt)
	case bossslash.KindSessions:
		if !m.hasPersistentSessions() {
			m.status = "Boss chat sessions are unavailable without an app data directory"
			return m, nil
		}
		if strings.TrimSpace(inv.SessionID) == "" {
			return m.openBossSessionPicker()
		}
		m.status = "Opening boss chat session " + shortBossSessionID(inv.SessionID) + "..."
		return m, m.loadBossSessionCmd(inv.SessionID)
	case bossslash.KindHelp:
		message := ChatMessage{Role: "assistant", Content: formatBossSlashHelp(), At: m.now()}
		m.messages = append(m.messages, message)
		m.status = "Boss chat slash commands"
		m.syncLayout(true)
		return m, nil
	case bossslash.KindSkills:
		m.status = "Loading Codex skills..."
		return m, m.loadSkillsInventoryCmd()
	case bossslash.KindClose:
		return m, m.exitCmd()
	default:
		m.status = "Unsupported boss slash command"
		return m, nil
	}
}

func (m Model) bossSlashSuggestionWindow(total int) (int, int) {
	return slashcmd.SuggestionWindow(m.bossSlashSelected, total, minInt(4, total))
}

func (m Model) bossSlashBlockHeight() int {
	if !m.bossSlashActive() {
		return 0
	}
	height := 2
	suggestions := m.bossSlashSuggestions()
	if len(suggestions) == 0 {
		height++
	} else {
		start, end := m.bossSlashSuggestionWindow(len(suggestions))
		if start > 0 {
			height++
		}
		height += end - start
		if end < len(suggestions) {
			height++
		}
		if selected, ok := m.selectedBossSlashSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
			height++
		}
	}
	return height
}

func (m Model) renderBossSlashBlock(width, height int) string {
	if !m.bossSlashActive() || height <= 0 {
		return ""
	}
	width = maxInt(24, width)
	lines := []string{
		bossSlashTitleStyle.Render(fitLine("Boss Slash Commands", width)),
		bossMutedStyle.Render(fitLine("Enter runs locally. Tab completes or cycles. Shift+Tab moves back.", width)),
	}
	suggestions := m.bossSlashSuggestions()
	if len(suggestions) == 0 {
		lines = append(lines, bossMutedStyle.Render(fitLine("No supported boss slash commands match. Try /new, /sessions, /skills, or /help.", width)))
	} else {
		start, end := m.bossSlashSuggestionWindow(len(suggestions))
		if start > 0 {
			lines = append(lines, bossMutedStyle.Render(fitLine("up more", width)))
		}
		for i := start; i < end; i++ {
			lines = append(lines, renderBossSlashSuggestionRow(suggestions[i], i == m.bossSlashSelected, width))
		}
		if end < len(suggestions) {
			lines = append(lines, bossMutedStyle.Render(fitLine("down more", width)))
		}
	}
	if selected, ok := m.selectedBossSlashSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, bossMutedStyle.Render(fitLine(selected.Summary, width)))
	}
	return fitRenderedBlock(strings.Join(lines, "\n"), width, height)
}

func renderBossSlashSuggestionRow(s bossslash.Suggestion, selected bool, width int) string {
	displayWidth := minInt(28, maxInt(12, width/3))
	marker := " "
	style := bossSlashRowStyle
	if selected {
		marker = ">"
		style = bossSlashSelectedRowStyle
	}
	line := fmt.Sprintf("%s %-*s %s", marker, displayWidth, s.Display, s.Summary)
	return style.Render(fitLine(line, width))
}

func formatBossSlashHelp() string {
	lines := []string{"Boss chat slash commands:"}
	for _, spec := range bossslash.Specs() {
		if spec.Hidden {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s` - %s", spec.Usage, spec.Summary))
	}
	return strings.Join(lines, "\n")
}

func shortBossSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) <= 14 {
		return sessionID
	}
	return sessionID[:14]
}

var (
	bossSlashTitleStyle = lipgloss.NewStyle().
				Foreground(bossPanelAccent).
				Bold(true).
				Background(bossPanelBackground)
	bossSlashRowStyle = lipgloss.NewStyle().
				Foreground(bossPanelText).
				Background(bossPanelBackground)
	bossSlashSelectedRowStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("16")).
					Background(bossPanelAccent).
					Bold(true)
)
