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

func writeModelContextSnapshot(writer *session.Writer, stateStore *threadStateStore, sessionID, source string, messages []modeladapter.Message, compacted bool) error {
	if writer == nil || len(messages) == 0 {
		return nil
	}
	snapshot := cloneModelMessages(messages)
	event := session.Event{
		"type":          modelContextSnapshotType,
		"session_id":    sessionID,
		"source":        strings.TrimSpace(source),
		"message_count": len(snapshot),
		"approx_chars":  messagesApproxChars(snapshot),
		"messages":      snapshot,
	}
	if stateStore != nil && strings.TrimSpace(stateStore.ThreadID) != "" {
		event["thread_id"] = strings.TrimSpace(stateStore.ThreadID)
	}
	if err := writer.WritePrivate(event); err != nil {
		return err
	}
	if stateStore != nil {
		return stateStore.SaveCheckpoint(source, snapshot, compacted)
	}
	return nil
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

func lastAssistantMessageText(messages []modeladapter.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			if text := strings.TrimSpace(messages[i].Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func withCurrentSystemMessage(messages []modeladapter.Message, systemPrompt string) []modeladapter.Message {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return cloneModelMessages(messages)
	}
	out := cloneModelMessages(messages)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = systemPrompt
			return out
		}
	}
	return append([]modeladapter.Message{{Role: "system", Content: systemPrompt}}, out...)
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
