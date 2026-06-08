package lcagent

import (
	"crypto/sha256"
	"encoding/hex"
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
	hasThreadState := stateStore != nil && strings.TrimSpace(stateStore.ThreadID) != ""
	if hasThreadState {
		if err := stateStore.SaveCheckpoint(source, snapshot, compacted); err != nil {
			return err
		}
	}
	event := session.Event{
		"type":            modelContextSnapshotType,
		"session_id":      sessionID,
		"source":          strings.TrimSpace(source),
		"message_count":   len(snapshot),
		"approx_chars":    messagesApproxChars(snapshot),
		"content_sha256":  modelMessagesHash(snapshot),
		"context_mode":    threadContextModeForCompacted(compacted),
		"snapshot_format": "inline_messages",
	}
	if hasThreadState {
		event["thread_id"] = strings.TrimSpace(stateStore.ThreadID)
		event["snapshot_format"] = "thread_state_ref"
		event["messages_included"] = false
		if path, err := threadStatePath(stateStore.DataDir, stateStore.ThreadID); err == nil && strings.TrimSpace(path) != "" {
			event["state_path"] = path
		}
	} else {
		event["messages"] = snapshot
		event["messages_included"] = true
	}
	return writer.WritePrivate(event)
}

func modelMessagesHash(messages []modeladapter.Message) string {
	body, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func threadContextModeForCompacted(compacted bool) string {
	if compacted {
		return threadContextModeCompacted
	}
	return threadContextModeExact
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
