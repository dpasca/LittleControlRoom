package boss

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) hasPersistentSessions() bool {
	return m.sessionStore != nil
}

func (m Model) submitChatMessage(text string) (tea.Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	if m.hasPersistentSessions() && !m.sessionLoaded {
		m.status = "Boss chat session is still loading..."
		return m, nil
	}
	userMessage := ChatMessage{
		Role:    "user",
		Content: text,
		At:      m.now(),
	}
	m.messages = append(m.messages, userMessage)
	m.input.Reset()
	m.bossSlashSelected = 0
	m.sending = true
	m.status = "Boss chat is thinking..."
	m.syncLayout(true)
	return m, tea.Batch(
		m.saveBossChatMessageCmd(userMessage),
		m.askAssistantCmd(append([]ChatMessage(nil), m.messages...), m.snapshot, m.viewContext),
	)
}

func (m Model) loadLatestBossSessionCmd() tea.Cmd {
	store := m.sessionStore
	parent := m.ctx
	now := m.now()
	return func() tea.Msg {
		if store == nil {
			return bossSessionLoadedMsg{err: fmt.Errorf("boss chat session store is not available")}
		}
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		session, messages, created, err := store.loadLatestOrCreate(ctx, now)
		return bossSessionLoadedMsg{session: session, messages: messages, created: created, err: err}
	}
}

func (m Model) newBossSessionCmd(prompt string) tea.Cmd {
	store := m.sessionStore
	parent := m.ctx
	now := m.now()
	return func() tea.Msg {
		if store == nil {
			return bossSessionLoadedMsg{err: fmt.Errorf("boss chat session store is not available")}
		}
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		session, err := store.createSession(ctx, now)
		return bossSessionLoadedMsg{session: session, created: true, prompt: strings.TrimSpace(prompt), err: err}
	}
}

func (m Model) loadBossSessionCmd(sessionID string) tea.Cmd {
	store := m.sessionStore
	parent := m.ctx
	return func() tea.Msg {
		if store == nil {
			return bossSessionLoadedMsg{err: fmt.Errorf("boss chat session store is not available")}
		}
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		session, messages, err := store.loadSession(ctx, sessionID)
		return bossSessionLoadedMsg{session: session, messages: messages, err: err}
	}
}

func (m Model) listBossSessionsCmd() tea.Cmd {
	store := m.sessionStore
	parent := m.ctx
	return func() tea.Msg {
		if store == nil {
			return bossSessionsListedMsg{err: fmt.Errorf("boss chat session store is not available")}
		}
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		sessions, err := store.listSessions(ctx, 12)
		return bossSessionsListedMsg{sessions: sessions, err: err}
	}
}

func (m Model) saveBossChatMessageCmd(message ChatMessage) tea.Cmd {
	if !m.hasPersistentSessions() || strings.TrimSpace(m.sessionID) == "" || strings.TrimSpace(message.Content) == "" {
		return nil
	}
	store := m.sessionStore
	sessionID := m.sessionID
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		return bossSessionSavedMsg{err: store.appendMessage(ctx, sessionID, message)}
	}
}

func chatMessagesFromBossMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		out = append(out, ChatMessage{
			Role:    normalizeChatRole(message.Role),
			Content: content,
			At:      message.At,
		})
	}
	return out
}
