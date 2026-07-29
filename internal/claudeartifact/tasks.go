package claudeartifact

import (
	"encoding/json"
	"strings"
	"time"
)

type AsyncTaskEventKind string

const (
	AsyncTaskLaunched AsyncTaskEventKind = "launched"
	AsyncTaskUpdated  AsyncTaskEventKind = "updated"
)

const (
	AsyncTaskSourceBackgroundShell = "background_shell"
	AsyncTaskSourceAgent           = "agent"
)

type AsyncTaskEvent struct {
	Kind       AsyncTaskEventKind
	TaskID     string
	ToolUseID  string
	Source     string
	Status     string
	OutputPath string
	Summary    string
	At         time.Time
}

type asyncTaskToolUseResult struct {
	BackgroundTaskID string `json:"backgroundTaskId"`
	IsAsync          bool   `json:"isAsync"`
	Status           string `json:"status"`
	AgentID          string `json:"agentId"`
}

// ParseAsyncTaskEvents reads Claude Code's structured background-shell and
// async-agent records. It deliberately ignores natural-language tool output:
// task identity and lifecycle must come from provider fields and task
// notifications.
func ParseAsyncTaskEvents(line []byte) []AsyncTaskEvent {
	var raw struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Content   string `json:"content"`
		Origin    struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		ToolUseResult       asyncTaskToolUseResult `json:"toolUseResult"`
		StreamToolUseResult asyncTaskToolUseResult `json:"tool_use_result"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	at := time.Time{}
	if raw.Timestamp != "" {
		at, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
	}
	result := mergeAsyncTaskToolUseResult(raw.ToolUseResult, raw.StreamToolUseResult)
	events := make([]AsyncTaskEvent, 0, 2)
	if taskID := strings.TrimSpace(result.BackgroundTaskID); taskID != "" {
		events = append(events, AsyncTaskEvent{
			Kind:      AsyncTaskLaunched,
			TaskID:    taskID,
			ToolUseID: firstToolResultUseID(raw.Message.Content),
			Source:    AsyncTaskSourceBackgroundShell,
			Status:    "running",
			At:        at,
		})
	}
	if result.IsAsync && strings.EqualFold(strings.TrimSpace(result.Status), "async_launched") {
		if taskID := strings.TrimSpace(result.AgentID); taskID != "" {
			events = append(events, AsyncTaskEvent{
				Kind:      AsyncTaskLaunched,
				TaskID:    taskID,
				ToolUseID: firstToolResultUseID(raw.Message.Content),
				Source:    AsyncTaskSourceAgent,
				Status:    "running",
				At:        at,
			})
		}
	}

	notification := ""
	switch {
	case strings.EqualFold(strings.TrimSpace(raw.Origin.Kind), "task-notification"):
		notification = asyncTaskMessageText(raw.Message.Content)
	case raw.Type == "queue-operation":
		notification = strings.TrimSpace(raw.Content)
	}
	if notification == "" {
		return events
	}
	taskID := taggedValue(notification, "task-id")
	status := strings.ToLower(strings.TrimSpace(taggedValue(notification, "status")))
	if taskID == "" || status == "" {
		return events
	}
	events = append(events, AsyncTaskEvent{
		Kind:       AsyncTaskUpdated,
		TaskID:     taskID,
		Status:     status,
		OutputPath: taggedValue(notification, "output-file"),
		Summary:    taggedValue(notification, "summary"),
		At:         at,
	})
	return events
}

func IsTerminalTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "error", "errored", "cancelled", "canceled", "interrupted", "stopped":
		return true
	default:
		return false
	}
}

func mergeAsyncTaskToolUseResult(primary, fallback asyncTaskToolUseResult) asyncTaskToolUseResult {
	if strings.TrimSpace(primary.BackgroundTaskID) == "" {
		primary.BackgroundTaskID = fallback.BackgroundTaskID
	}
	if !primary.IsAsync {
		primary.IsAsync = fallback.IsAsync
	}
	if strings.TrimSpace(primary.Status) == "" {
		primary.Status = fallback.Status
	}
	if strings.TrimSpace(primary.AgentID) == "" {
		primary.AgentID = fallback.AgentID
	}
	return primary
}

func firstToolResultUseID(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "tool_result" {
			if toolUseID := strings.TrimSpace(block.ToolUseID); toolUseID != "" {
				return toolUseID
			}
		}
	}
	return ""
}

func asyncTaskMessageText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func taggedValue(raw, tag string) string {
	raw = strings.TrimSpace(raw)
	tag = strings.TrimSpace(tag)
	if raw == "" || tag == "" {
		return ""
	}
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(raw, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(raw[start:], close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(raw[start : start+end])
}
