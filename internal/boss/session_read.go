package boss

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/lcagent"
)

// Indices refer to the saved transcript, including events. Continuations use
// rune offsets so a long message can be read without dropping text.
type chatSessionReadRequest struct {
	Source        string `json:"source"`
	SessionID     string `json:"session_id"`
	StartTurn     int    `json:"start_turn"`
	StartChar     int    `json:"start_char"`
	Limit         int    `json:"limit"`
	MaxChars      int    `json:"max_chars"`
	IncludeEvents bool   `json:"include_events"`
}

type chatSessionReadTurn struct {
	Index      int       `json:"index"`
	Role       string    `json:"role"`
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	Content    string    `json:"content"`
	CharOffset int       `json:"char_offset"`
	Truncated  bool      `json:"truncated"`
}

type chatSessionReadResult struct {
	Source       string                  `json:"source"`
	SessionID    string                  `json:"session_id"`
	Title        string                  `json:"title"`
	Path         string                  `json:"path"`
	CreatedAt    time.Time               `json:"created_at"`
	MessageCount int                     `json:"message_count"`
	Turns        []chatSessionReadTurn   `json:"turns"`
	Next         *chatSessionReadRequest `json:"next,omitempty"`
	Note         string                  `json:"note"`
}

func (e *QueryExecutor) readChatSession(ctx context.Context, req chatSessionReadRequest, view ViewContext) (chatSessionReadResult, string, error) {
	if view.PrivacyMode {
		return chatSessionReadResult{}, "", fmt.Errorf("saved Chat history is unavailable in privacy mode because transcripts can contain private projects")
	}
	if req.StartTurn == 0 {
		req.StartTurn = 1
	}
	if req.Limit == 0 {
		req.Limit = 6
	}
	if req.MaxChars == 0 {
		req.MaxChars = 8000
	}
	if req.StartTurn < 1 || req.StartChar < 0 || req.Limit < 1 || req.Limit > 12 || req.MaxChars < 256 || req.MaxChars > 12000 {
		return chatSessionReadResult{}, "", fmt.Errorf("invalid Chat read bounds: start_turn >= 1, start_char >= 0, limit 1–12, max_chars 256–12000")
	}
	if req.Source != helpChatSessionsDirName && req.Source != bossSessionsDirName {
		return chatSessionReadResult{}, "", fmt.Errorf("source must be help-chat-sessions or boss-sessions, as returned by search_chat_sessions")
	}
	var store *bossSessionStore
	if e != nil {
		for _, candidate := range e.bossSessions {
			if filepath.Base(candidate.dir) == req.Source {
				store = candidate
				break
			}
		}
	}
	if store == nil {
		return chatSessionReadResult{}, "", fmt.Errorf("Chat history source %q is not connected", req.Source)
	}
	session, messages, err := store.loadSession(ctx, req.SessionID)
	if err != nil {
		return chatSessionReadResult{}, "", err
	}
	if req.StartTurn > len(messages) {
		return chatSessionReadResult{}, "", fmt.Errorf("start_turn %d exceeds this transcript's %d messages", req.StartTurn, len(messages))
	}
	result := chatSessionReadResult{
		Source: req.Source, SessionID: session.SessionID, Title: session.Title,
		Path: session.Path, CreatedAt: session.CreatedAt, MessageCount: len(messages),
		Turns: make([]chatSessionReadTurn, 0, req.Limit),
		Note:  "Saved historical evidence, not current project state. Log/flow entries are operational receipts, not user or assistant conversation. Follow next to read omitted text; a missing current project does not establish that the work was Chat-only.",
	}
	remaining := req.MaxChars
	for i := req.StartTurn - 1; i < len(messages); i++ {
		if err := ctx.Err(); err != nil {
			return chatSessionReadResult{}, "", err
		}
		message := messages[i]
		kind := normalizeChatMessageKind(message.Kind)
		if !req.IncludeEvents && kind != ChatMessageKindChat {
			continue
		}
		offset := 0
		if i == req.StartTurn-1 {
			offset = req.StartChar
		}
		content := []rune(message.Content)
		if offset > len(content) {
			return chatSessionReadResult{}, "", fmt.Errorf("start_char exceeds the length of message %d", i+1)
		}
		if len(result.Turns) == req.Limit || remaining == 0 {
			next := req
			next.StartTurn, next.StartChar = i+1, offset
			result.Next = &next
			break
		}
		end := minInt(len(content), offset+remaining)
		result.Turns = append(result.Turns, chatSessionReadTurn{
			Index: i + 1, Role: normalizeChatRole(message.Role), Kind: kind, At: message.At,
			Content: string(content[offset:end]), CharOffset: offset, Truncated: end < len(content),
		})
		remaining -= end - offset
		if end < len(content) {
			next := req
			next.StartTurn, next.StartChar = i+1, end
			result.Next = &next
			break
		}
	}
	receipt := ""
	if len(result.Turns) > 0 {
		first, last := result.Turns[0], result.Turns[len(result.Turns)-1]
		receipt = fmt.Sprintf("History source: [Chat %s, messages %d–%d](<%s>) (`%s/%s`).",
			first.At.Format("2006-01-02"), first.Index, last.Index, session.Path, req.Source, session.SessionID)
		if result.Next != nil {
			receipt += " Excerpt; more history is available."
		}
	}
	return result, receipt, nil
}

func (a *Assistant) helpChatReadSessionTool(req AssistantRequest) lcagent.AgentRuntimeTool {
	return lcagent.AgentRuntimeTool{
		Definition: helpChatToolDefinition("read_chat_session", "Read a saved Chat exchange using a search result or an earlier History source citation. Include nearby turns to resolve project references. Explicitly include_events to inspect work receipts. Follow next for more text. This does not switch the user's current Chat session.", map[string]any{
			"source":         map[string]any{"type": "string", "enum": []string{helpChatSessionsDirName, bossSessionsDirName}},
			"session_id":     map[string]any{"type": "string", "minLength": 1},
			"start_turn":     map[string]any{"type": "integer", "minimum": 1, "description": "1-based saved message index; start before the matching turn for surrounding context. Default 1."},
			"start_char":     map[string]any{"type": "integer", "minimum": 0, "description": "Rune offset in start_turn, from a previous next continuation. Default 0."},
			"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
			"max_chars":      map[string]any{"type": "integer", "minimum": 256, "maximum": 12000},
			"include_events": map[string]any{"type": "boolean", "description": "Include saved log/flow receipts in this explicit lookup. Default false."},
		}, []string{"source", "session_id"}),
		Run: func(ctx context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
			var args chatSessionReadRequest
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return lcagent.AgentRuntimeToolOutcome{}, err
			}
			result, receipt, err := a.query.readChatSession(ctx, args, req.View)
			if err != nil {
				return lcagent.AgentRuntimeToolOutcome{}, err
			}
			return lcagent.AgentRuntimeToolOutcome{Result: result, Receipt: strings.TrimSpace(receipt)}, nil
		},
	}
}
