package codexapp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/codexstate"
)

const maxCodexRolloutReplayLineBytes = 32 * 1024 * 1024

// loadCodexInterruptedTurnTranscript rebuilds only the latest unfinished or
// aborted turn. Codex thread/resume can retain that turn's user and assistant
// messages while omitting its tool items, even though the rollout JSONL still
// has the complete structured event sequence.
func loadCodexInterruptedTurnTranscript(req LaunchRequest) ([]TranscriptEntry, error) {
	threadID := strings.TrimSpace(req.ResumeID)
	if threadID == "" {
		return nil, nil
	}
	codexHome, err := effectiveCodexHome(req.CodexHome)
	if err != nil {
		return nil, err
	}
	rolloutPath, lookupErr := codexstate.ThreadRolloutPath(codexHome, threadID)
	if strings.TrimSpace(rolloutPath) != "" {
		if _, err := os.Stat(rolloutPath); err != nil {
			rolloutPath = ""
		}
	}
	if strings.TrimSpace(rolloutPath) == "" {
		rolloutPath = findCodexRolloutPath(codexHome, threadID)
	}
	if strings.TrimSpace(rolloutPath) == "" {
		if lookupErr != nil {
			return nil, lookupErr
		}
		return nil, nil
	}
	return readCodexInterruptedTurnTranscript(rolloutPath, threadID, req.ProjectPath)
}

func findCodexRolloutPath(codexHome, threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || filepath.Base(threadID) != threadID || strings.ContainsAny(threadID, `/\\`) {
		return ""
	}
	patterns := []string{
		filepath.Join(codexHome, "sessions", "*", "*", "*", "*"+threadID+"*.jsonl"),
		filepath.Join(codexHome, "archived_sessions", "*"+threadID+"*.jsonl"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		return matches[0]
	}
	return ""
}

func readCodexInterruptedTurnTranscript(path, expectedThreadID, expectedProjectPath string) ([]TranscriptEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open codex rollout replay: %w", err)
	}
	defer file.Close()

	var replay codexInterruptedTurnReplay
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxCodexRolloutReplayLineBytes)
	for scanner.Scan() {
		if err := replay.consume(scanner.Bytes(), expectedThreadID, expectedProjectPath); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan codex rollout replay: %w", err)
	}
	if !replay.recoverable || len(replay.entries) == 0 {
		return nil, nil
	}
	return cloneTranscriptEntries(replay.entries), nil
}

type codexInterruptedTurnReplay struct {
	seenMeta    bool
	turnID      string
	active      bool
	recoverable bool
	entries     []TranscriptEntry
	callIndex   map[string]int
	callNames   map[string]string
}

func (r *codexInterruptedTurnReplay) consume(line []byte, expectedThreadID, expectedProjectPath string) error {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil
	}

	switch envelope.Type {
	case "session_meta":
		if r.seenMeta {
			return nil
		}
		r.seenMeta = true
		var meta struct {
			ID  string `json:"id"`
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(envelope.Payload, &meta); err != nil {
			return nil
		}
		if expected := strings.TrimSpace(expectedThreadID); expected != "" && strings.TrimSpace(meta.ID) != expected {
			return fmt.Errorf("codex rollout thread id %q does not match resume id %q", strings.TrimSpace(meta.ID), expected)
		}
		if expected := cleanComparablePath(expectedProjectPath); expected != "" {
			if actual := cleanComparablePath(meta.CWD); actual != "" && actual != expected {
				return fmt.Errorf("codex rollout cwd %q does not match resumed project %q", meta.CWD, expectedProjectPath)
			}
		}
	case "event_msg":
		return r.consumeEvent(envelope.Payload)
	case "response_item":
		r.consumeResponseItem(envelope.Payload)
	}
	return nil
}

func cleanComparablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func (r *codexInterruptedTurnReplay) consumeEvent(raw json.RawMessage) error {
	var event struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		TurnID  string `json:"turn_id"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil
	}
	switch event.Type {
	case "task_started":
		r.turnID = strings.TrimSpace(event.TurnID)
		r.active = true
		r.recoverable = true
		r.entries = nil
		r.callIndex = make(map[string]int)
		r.callNames = make(map[string]string)
	case "task_complete":
		if r.matchesTurn(event.TurnID) {
			r.active = false
			r.recoverable = false
			r.entries = nil
			r.callIndex = nil
			r.callNames = nil
		}
	case "turn_aborted":
		if r.matchesTurn(event.TurnID) {
			r.active = false
			r.recoverable = true
			r.markPendingCalls("interrupted")
		}
	case "user_message":
		if r.active {
			r.appendMessage(TranscriptUser, event.Message)
		}
	case "agent_message":
		if r.active {
			r.appendMessage(TranscriptAgent, event.Message)
		}
	}
	return nil
}

func (r *codexInterruptedTurnReplay) matchesTurn(turnID string) bool {
	turnID = strings.TrimSpace(turnID)
	return turnID == "" || r.turnID == "" || turnID == r.turnID
}

func (r *codexInterruptedTurnReplay) appendMessage(kind TranscriptKind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.entries = append(r.entries, TranscriptEntry{Kind: kind, Text: text})
}

func (r *codexInterruptedTurnReplay) consumeResponseItem(raw json.RawMessage) {
	if !r.active {
		return
	}
	var item struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}
	switch item.Type {
	case "custom_tool_call", "function_call":
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return
		}
		callID := strings.TrimSpace(item.CallID)
		status := normalizedReplayToolStatus(item.Status)
		if status == "" {
			status = "in progress"
		}
		entry := TranscriptEntry{
			ItemID: firstNonEmpty(strings.TrimSpace(item.ID), callID),
			Kind:   TranscriptTool,
			Text:   replayToolText(name, status),
		}
		r.entries = append(r.entries, entry)
		if callID != "" {
			r.callIndex[callID] = len(r.entries) - 1
			r.callNames[callID] = name
		}
	case "custom_tool_call_output", "function_call_output":
		callID := strings.TrimSpace(item.CallID)
		index, ok := r.callIndex[callID]
		if !ok || index < 0 || index >= len(r.entries) {
			return
		}
		name := strings.TrimSpace(r.callNames[callID])
		if name != "" {
			r.entries[index].Text = replayToolText(name, "completed")
		}
	}
}

func normalizedReplayToolStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "inProgress", "in_progress":
		return "in progress"
	default:
		return status
	}
}

func replayToolText(name, status string) string {
	text := "Tool " + strings.TrimSpace(name)
	if status := strings.TrimSpace(status); status != "" {
		text += " [" + status + "]"
	}
	return text
}

func (r *codexInterruptedTurnReplay) markPendingCalls(status string) {
	for callID, index := range r.callIndex {
		if index < 0 || index >= len(r.entries) {
			continue
		}
		if !strings.Contains(r.entries[index].Text, "[in progress]") {
			continue
		}
		if name := strings.TrimSpace(r.callNames[callID]); name != "" {
			r.entries[index].Text = replayToolText(name, status)
		}
	}
}
