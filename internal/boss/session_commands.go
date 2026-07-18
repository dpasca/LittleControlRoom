package boss

import (
	"context"
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
		m.status = m.chatSurfaceLabel() + " session is still loading..."
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
	m.assistantStreamID++
	streamID := m.assistantStreamID
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)
	m.assistantCancel = cancel
	m.streamingAssistantText = ""
	m.streamingToolCalls = nil
	m.assistantStartedAt = m.now()
	m.haveLastAssistantTime = false
	m.lastAssistantTime = 0
	m.haveLastContextReport = false
	m.status = m.chatSurfaceLabel() + " is thinking..."
	m.syncLayout(true)
	return m, tea.Batch(
		m.saveBossChatMessageCmd(userMessage),
		m.askAssistantStreamCmd(runCtx, streamID, modelVisibleChatMessages(m.messages), m.snapshot, m.assistantViewContext()),
	)
}

func (m Model) loadLatestBossSessionCmd() tea.Cmd {
	store := m.sessionStore
	parent := m.ctx
	now := m.now()
	return func() tea.Msg {
		if store == nil {
			return bossSessionLoadedMsg{err: fmt.Errorf("Chat session store is not available")}
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
			return bossSessionLoadedMsg{err: fmt.Errorf("Chat session store is not available")}
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
			return bossSessionLoadedMsg{err: fmt.Errorf("Chat session store is not available")}
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
			return bossSessionsListedMsg{err: fmt.Errorf("Chat session store is not available")}
		}
		ctx, cancel := childContext(parent, 20*time.Second)
		defer cancel()
		sessions, err := store.listSessions(ctx, 40)
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

func (m *Model) appendAssistantChatMessage(content string, handoffs ...*HandoffHighlight) (ChatMessage, bool) {
	return m.appendAssistantMessage(content, ChatMessageKindChat, handoffs...)
}

func (m *Model) appendAssistantNoticeMessage(content string, handoffs ...*HandoffHighlight) (ChatMessage, bool) {
	if m.helpChat {
		return m.appendAssistantMessage(content, ChatMessageKindChat, handoffs...)
	}
	return m.appendAssistantMessage(content, ChatMessageKindFlow, handoffs...)
}

func (m *Model) appendAssistantEventMessage(content string, handoffs ...*HandoffHighlight) (ChatMessage, bool) {
	if m.helpChat {
		return m.appendAssistantMessage(content, ChatMessageKindLog, handoffs...)
	}
	return m.appendAssistantMessage(content, ChatMessageKindFlow, handoffs...)
}

func (m *Model) appendAssistantMessage(content string, kind string, handoffs ...*HandoffHighlight) (ChatMessage, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return ChatMessage{}, false
	}
	kind = normalizeChatMessageKind(kind)
	if kind == ChatMessageKindLog {
		for _, existing := range m.messages {
			if normalizeChatRole(existing.Role) == "assistant" &&
				normalizeChatMessageKind(existing.Kind) == kind &&
				strings.TrimSpace(existing.Content) == content {
				return ChatMessage{}, false
			}
		}
	}
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if normalizeChatRole(last.Role) == "assistant" && strings.TrimSpace(last.Content) == content && normalizeChatMessageKind(last.Kind) == kind {
			return ChatMessage{}, false
		}
	}
	message := ChatMessage{
		Role:    "assistant",
		Content: content,
		At:      m.now(),
		Kind:    kind,
	}
	if len(handoffs) > 0 {
		if handoff, ok := normalizedHandoffHighlight(handoffs[0]); ok {
			message.Handoff = &handoff
		}
	}
	m.messages = append(m.messages, message)
	return message, true
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
			Kind:    normalizeChatMessageKind(message.Kind),
		})
	}
	return out
}
