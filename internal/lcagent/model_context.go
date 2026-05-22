package lcagent

import (
	"encoding/json"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

const modelContextSnapshotType = "model_context_snapshot"

func writeModelContextSnapshot(writer *session.Writer, sessionID, source string, messages []modeladapter.Message) error {
	if writer == nil || len(messages) == 0 {
		return nil
	}
	snapshot := cloneModelMessages(messages)
	return writer.WritePrivate(session.Event{
		"type":          modelContextSnapshotType,
		"session_id":    sessionID,
		"source":        strings.TrimSpace(source),
		"message_count": len(snapshot),
		"approx_chars":  messagesApproxChars(snapshot),
		"messages":      snapshot,
	})
}

func appendFinalResponseForContextSnapshot(messages []modeladapter.Message, callID string, final script.Action) []modeladapter.Message {
	out := cloneModelMessages(messages)
	summary := strings.TrimSpace(final.Summary)
	callID = strings.TrimSpace(callID)
	if callID != "" {
		for i := range out {
			if out[i].Role != "assistant" {
				continue
			}
			for _, call := range out[i].ToolCalls {
				if call.ID == callID && call.Function.Name == "final_response" {
					out[i].Content = ""
				}
			}
		}
		result := tools.ToolResult{Success: true, Output: summary}
		if resultJSON, err := json.Marshal(result); err == nil {
			out = append(out, modeladapter.Message{
				Role:       "tool",
				ToolCallID: callID,
				Content:    string(resultJSON),
			})
		}
	}
	if summary != "" {
		out = append(out, modeladapter.Message{Role: "assistant", Content: summary})
	}
	return out
}

func appendAssistantContentForContextSnapshot(messages []modeladapter.Message, content string) []modeladapter.Message {
	out := cloneModelMessages(messages)
	if content = strings.TrimSpace(content); content != "" {
		out = append(out, modeladapter.Message{Role: "assistant", Content: content})
	}
	return out
}

func appendSystemContextToModelMessages(messages []modeladapter.Message, context string) []modeladapter.Message {
	context = strings.TrimSpace(context)
	out := cloneModelMessages(messages)
	if context == "" {
		return out
	}
	for i := range out {
		if out[i].Role != "system" {
			continue
		}
		if strings.TrimSpace(out[i].Content) == "" {
			out[i].Content = context
		} else {
			out[i].Content = strings.TrimSpace(out[i].Content) + "\n\n" + context
		}
		return out
	}
	return append([]modeladapter.Message{{Role: "system", Content: context}}, out...)
}

func modelMessagesHaveSystem(messages []modeladapter.Message) bool {
	for _, msg := range messages {
		if msg.Role == "system" && strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

func observeReadLedgerMessages(ledger *readLedger, messages []modeladapter.Message) {
	if ledger == nil {
		return
	}
	for _, msg := range messages {
		if msg.Role != "tool" || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		var result tools.ToolResult
		if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
			continue
		}
		ledger.ObserveReadResult(result)
	}
}
