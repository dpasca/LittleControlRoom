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
	// AsyncTasksOrphaned reports that a resumed Claude Code process finished
	// every earlier task of Source that the previous process left without a
	// completion record, except LiveTaskIDs. TaskID is empty.
	AsyncTasksOrphaned AsyncTaskEventKind = "orphaned"
)

const (
	AsyncTaskSourceBackgroundShell = "background_shell"
	AsyncTaskSourceAgent           = "agent"
	AsyncTaskSourceWorkflow        = "local_workflow"
)

// Claude Code's orphan summary task ids are scan markers, not tasks.
const (
	orphanSummaryPrefix     = "__orphan_summary"
	orphanSummaryKindPrefix = "__orphan_summary__:"
	orphanSummaryLivePrefix = "__orphan_summary_live__:"
)

type AsyncTaskEvent struct {
	Kind       AsyncTaskEventKind
	NewRun     bool // An explicit task_started frame, including a resumed agent.
	TaskID     string
	ToolUseID  string
	Source     string
	Status     string
	OutputPath string
	Summary    string
	At         time.Time
	// LiveTaskIDs are excluded from an AsyncTasksOrphaned event.
	LiveTaskIDs []string
}

// FinishesOrphan reports whether e is a terminal orphan summary covering the
// task with taskID and source.
func (e AsyncTaskEvent) FinishesOrphan(taskID, source string) bool {
	if e.Kind != AsyncTasksOrphaned || !IsTerminalTaskStatus(e.Status) || e.Source != source {
		return false
	}
	for _, live := range e.LiveTaskIDs {
		if live == taskID {
			return false
		}
	}
	return true
}

type asyncTaskToolUseResult struct {
	BackgroundTaskID string `json:"backgroundTaskId"`
	IsAsync          bool   `json:"isAsync"`
	Status           string `json:"status"`
	AgentID          string `json:"agentId"`
	StoppedTaskID    string `json:"task_id"`
	StoppedTaskType  string `json:"task_type"`
}

// ParseAsyncTaskEvents reads Claude Code's structured background-shell and
// async-agent records. It deliberately ignores natural-language tool output:
// task identity and lifecycle must come from provider fields and task
// notifications.
func ParseAsyncTaskEvents(line []byte) []AsyncTaskEvent {
	var raw struct {
		Type        string `json:"type"`
		Subtype     string `json:"subtype"`
		TaskID      string `json:"task_id"`
		TaskType    string `json:"task_type"`
		ToolUseID   string `json:"tool_use_id"`
		Status      string `json:"status"`
		Description string `json:"description"`
		Summary     string `json:"summary"`
		OutputFile  string `json:"output_file"`
		Ambient     bool   `json:"ambient"`
		Patch       struct {
			Status string `json:"status"`
		} `json:"patch"`
		Tasks []struct {
			TaskID      string `json:"task_id"`
			TaskType    string `json:"task_type"`
			Description string `json:"description"`
			Ambient     bool   `json:"ambient"`
		} `json:"tasks"`
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
	if raw.Type == "system" {
		if raw.Ambient {
			return nil
		}
		if raw.Subtype == "background_tasks_changed" {
			// This set contains background tasks only. Add positive ownership
			// evidence, but never finish a foreground child just because it is
			// absent here. Terminal task events release ownership.
			for _, task := range raw.Tasks {
				if task.TaskID != "" && !task.Ambient {
					events = append(events, AsyncTaskEvent{Kind: AsyncTaskLaunched, TaskID: task.TaskID,
						Source: streamTaskSource(task.TaskType), Status: "running", Summary: task.Description, At: at})
				}
			}
			return events
		}
		if raw.TaskID == "" {
			return nil
		}
		event := AsyncTaskEvent{TaskID: raw.TaskID, ToolUseID: raw.ToolUseID,
			Source: streamTaskSource(raw.TaskType), Status: raw.Status, Summary: raw.Summary,
			OutputPath: raw.OutputFile, At: at}
		switch raw.Subtype {
		case "task_started":
			event.Kind, event.Status, event.Summary = AsyncTaskLaunched, "running", raw.Description
			event.NewRun = true
		case "task_progress":
			event.Kind, event.Status = AsyncTaskUpdated, "running"
		case "task_updated":
			event.Kind, event.Status = AsyncTaskUpdated, raw.Patch.Status
			if event.Status == "" {
				return nil
			}
		case "task_notification":
			event.Kind = AsyncTaskUpdated
		default:
			return nil
		}
		return append(events, event)
	}
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
	// A TaskStop result is the only persisted record of a stop: Claude streams
	// task_updated and task_notification frames for it but writes neither.
	if taskID := strings.TrimSpace(result.StoppedTaskID); taskID != "" && strings.TrimSpace(result.StoppedTaskType) != "" {
		events = append(events, AsyncTaskEvent{
			Kind:   AsyncTaskUpdated,
			TaskID: taskID,
			Source: streamTaskSource(result.StoppedTaskType),
			Status: "stopped",
			At:     at,
		})
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
	// A resumed process reports the tasks the previous process left without a
	// completion record in one notification: repeated task-id tags capped at
	// 20 ids, plus a per-kind summary marker that covers the uncapped set.
	taskIDs := taggedValues(notification, "task-id")
	status := strings.ToLower(strings.TrimSpace(taggedValue(notification, "status")))
	if len(taskIDs) == 0 || status == "" {
		return events
	}
	outputPath := taggedValue(notification, "output-file")
	summary := taggedValue(notification, "summary")
	var orphanSources, liveTaskIDs []string
	for _, taskID := range taskIDs {
		switch {
		case strings.HasPrefix(taskID, orphanSummaryLivePrefix):
			liveTaskIDs = append(liveTaskIDs, strings.TrimPrefix(taskID, orphanSummaryLivePrefix))
		case strings.HasPrefix(taskID, orphanSummaryKindPrefix):
			if source := orphanSummarySource(strings.TrimPrefix(taskID, orphanSummaryKindPrefix)); source != "" {
				orphanSources = append(orphanSources, source)
			}
		case strings.HasPrefix(taskID, orphanSummaryPrefix):
		default:
			events = append(events, AsyncTaskEvent{
				Kind:       AsyncTaskUpdated,
				TaskID:     taskID,
				Status:     status,
				OutputPath: outputPath,
				Summary:    summary,
				At:         at,
			})
		}
	}
	for _, source := range orphanSources {
		events = append(events, AsyncTaskEvent{
			Kind:        AsyncTasksOrphaned,
			Source:      source,
			Status:      status,
			Summary:     summary,
			At:          at,
			LiveTaskIDs: liveTaskIDs,
		})
	}
	return events
}

func orphanSummarySource(kind string) string {
	switch kind {
	case "shell":
		return AsyncTaskSourceBackgroundShell
	case "agent":
		return AsyncTaskSourceAgent
	case "workflow":
		return AsyncTaskSourceWorkflow
	default:
		return ""
	}
}

func IsTerminalTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "error", "errored", "cancelled", "canceled", "interrupted", "stopped", "killed":
		return true
	default:
		return false
	}
}

func streamTaskSource(taskType string) string {
	switch taskType {
	case "local_agent", "remote_agent":
		return AsyncTaskSourceAgent
	case "local_bash":
		return AsyncTaskSourceBackgroundShell
	default:
		return taskType
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
	if strings.TrimSpace(primary.StoppedTaskID) == "" {
		primary.StoppedTaskID = fallback.StoppedTaskID
		primary.StoppedTaskType = fallback.StoppedTaskType
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

func taggedValues(raw, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	var values []string
	for {
		start := strings.Index(raw, open)
		if start < 0 {
			return values
		}
		raw = raw[start+len(open):]
		end := strings.Index(raw, close)
		if end < 0 {
			return values
		}
		if value := strings.TrimSpace(raw[:end]); value != "" {
			values = append(values, value)
		}
		raw = raw[end+len(close):]
	}
}
