package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestHydrateResumedThreadBuildsTranscript(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.hydrateResumedThread(resumedThread{
		ID: "thread_123",
		Status: resumedThreadStatus{
			Type: "idle",
		},
		Turns: []resumedTurn{
			{
				ID:     "turn_1",
				Status: "completed",
				Items: []map[string]json.RawMessage{
					{
						"id":      json.RawMessage(`"item_user"`),
						"type":    json.RawMessage(`"userMessage"`),
						"content": json.RawMessage(`[{"type":"text","text":"summarize this repo"}]`),
					},
					{
						"id":   json.RawMessage(`"item_agent"`),
						"type": json.RawMessage(`"agentMessage"`),
						"text": json.RawMessage(`"Here is a quick summary."`),
					},
					{
						"id":               json.RawMessage(`"item_cmd"`),
						"type":             json.RawMessage(`"commandExecution"`),
						"command":          json.RawMessage(`"git status --short"`),
						"cwd":              json.RawMessage(`"/tmp/demo"`),
						"aggregatedOutput": json.RawMessage(`" M README.md"`),
						"status":           json.RawMessage(`"completed"`),
						"exitCode":         json.RawMessage(`0`),
						"commandActions":   json.RawMessage(`[]`),
					},
				},
			},
		},
	})

	snapshot := s.Snapshot()
	if snapshot.ThreadID != "thread_123" {
		t.Fatalf("thread id = %q, want thread_123", snapshot.ThreadID)
	}
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if !strings.Contains(snapshot.Transcript, "You: summarize this repo") {
		t.Fatalf("transcript missing resumed user message: %q", snapshot.Transcript)
	}
	if !strings.Contains(snapshot.Transcript, "Codex: Here is a quick summary.") {
		t.Fatalf("transcript missing resumed agent message: %q", snapshot.Transcript)
	}
	if !strings.Contains(snapshot.Transcript, "$ git status --short") {
		t.Fatalf("transcript missing resumed command: %q", snapshot.Transcript)
	}
	if !strings.Contains(snapshot.Transcript, "[command completed, exit 0]") {
		t.Fatalf("transcript missing resumed command status: %q", snapshot.Transcript)
	}
}

func TestHydrateResumedThreadMarksActiveTurnBusy(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.hydrateResumedThread(resumedThread{
		ID: "thread_456",
		Status: resumedThreadStatus{
			Type: "active",
		},
		Turns: []resumedTurn{
			{
				ID:     "turn_live",
				Status: "inProgress",
				Items:  []map[string]json.RawMessage{},
			},
		},
	})

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if !snapshot.BusyExternal {
		t.Fatalf("busy external = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_live" {
		t.Fatalf("active turn id = %q, want turn_live", snapshot.ActiveTurnID)
	}
	if !snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = %v, want zero because resumed busy turn has no known start time", snapshot.BusySince)
	}
}

func TestTurnStartedSetsBusySince(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	before := time.Now()
	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))
	after := time.Now()

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = zero, want turn start timestamp")
	}
	if snapshot.BusySince.Before(before) || snapshot.BusySince.After(after) {
		t.Fatalf("busy since = %v, want between %v and %v", snapshot.BusySince, before, after)
	}
}

func TestTurnCompletedClearsBusyExternal(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.hydrateResumedThread(resumedThread{
		ID: "thread_456",
		Status: resumedThreadStatus{
			Type: "active",
		},
		Turns: []resumedTurn{
			{
				ID:     "turn_live",
				Status: "inProgress",
				Items:  []map[string]json.RawMessage{},
			},
		},
	})

	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false")
	}
	if !snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = %v, want zero after turn completion", snapshot.BusySince)
	}
	if snapshot.Status != "Turn completed" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Turn completed")
	}
}

func TestTurnCompletedWaitsForActiveAgentMessageToFinish(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))

	s.mu.Lock()
	s.busySince = time.Time{}
	s.mu.Unlock()

	s.handleNotification("item/started", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_agent",
			"type": "agentMessage"
		}
	}`))
	s.handleNotification("item/agentMessage/delta", json.RawMessage(`{
		"threadId":"thread_456",
		"turnId":"turn_live",
		"itemId":"item_agent",
		"delta":"still streaming"
	}`))
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true while agent output is still active")
	}
	if snapshot.Status != "Codex is working..." {
		t.Fatalf("status = %q, want working status until the item completes", snapshot.Status)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Text != "still streaming" {
		t.Fatalf("entries = %#v, want the in-flight agent text", snapshot.Entries)
	}

	s.handleNotification("item/completed", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_agent",
			"type": "agentMessage",
			"text": "still streaming"
		}
	}`))

	snapshot = s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false once the agent item finishes")
	}
	if snapshot.Status != "Turn completed" && !strings.HasPrefix(snapshot.Status, "Completed in ") {
		t.Fatalf("status = %q, want a completed-turn status", snapshot.Status)
	}
}

func TestSnapshotIncludesStructuredMetadata(t *testing.T) {
	resetAt := time.Now().Add(90 * time.Minute).Unix()
	windowMins := int64(300)
	contextWindow := int64(200000)
	s := &appServerSession{
		projectPath:      "/tmp/demo",
		currentCWD:       "/tmp/demo/subdir",
		model:            "gpt-5-codex",
		modelProvider:    "openai",
		reasoningEffort:  "high",
		pendingModel:     "gpt-5",
		pendingReasoning: "medium",
		tokenUsage: &threadTokenUsage{
			Total: tokenUsageBreakdown{
				TotalTokens: 12345,
			},
			ModelContextWindow: &contextWindow,
		},
		rateLimitsByID: map[string]rateLimitSnapshot{
			"codex": {
				LimitName: stringPtr("Codex"),
				PlanType:  stringPtr("Pro"),
				Primary: &rateLimitWindow{
					UsedPercent:        15,
					ResetsAt:           &resetAt,
					WindowDurationMins: &windowMins,
				},
			},
		},
		entryIndex: make(map[string]int),
		notify:     func() {},
	}

	snapshot := s.Snapshot()
	if snapshot.Model != "gpt-5-codex" {
		t.Fatalf("snapshot.Model = %q, want gpt-5-codex", snapshot.Model)
	}
	if snapshot.ReasoningEffort != "high" {
		t.Fatalf("snapshot.ReasoningEffort = %q, want high", snapshot.ReasoningEffort)
	}
	if snapshot.PendingModel != "gpt-5" || snapshot.PendingReasoning != "medium" {
		t.Fatalf("pending model snapshot = %#v, want gpt-5/medium", snapshot)
	}
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.ModelContextWindow != 200000 || snapshot.TokenUsage.Total.TotalTokens != 12345 {
		t.Fatalf("snapshot.TokenUsage = %#v, want context window + total tokens", snapshot.TokenUsage)
	}
	if len(snapshot.UsageWindows) != 1 {
		t.Fatalf("snapshot.UsageWindows = %#v, want 1 entry", snapshot.UsageWindows)
	}
	if snapshot.UsageWindows[0].Limit != "Codex" || snapshot.UsageWindows[0].LeftPercent != 85 {
		t.Fatalf("snapshot.UsageWindows[0] = %#v, want Codex 85%% left", snapshot.UsageWindows[0])
	}
}

func TestTokenUsageUpdateDoesNotRefreshBusyActivityTimestamp(t *testing.T) {
	staleBusy := time.Now().Add(-2 * time.Hour).Round(0)
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		busy:               true,
		activeTurnID:       "turn_live",
		lastActivityAt:     staleBusy,
		lastBusyActivityAt: staleBusy,
	}

	s.handleNotification("thread/tokenUsage/updated", json.RawMessage(`{
		"threadId":"thread_456",
		"turnId":"turn_live",
		"tokenUsage":{"total":{"totalTokens":42}}
	}`))

	snapshot := s.Snapshot()
	if !snapshot.LastBusyActivityAt.Equal(staleBusy) {
		t.Fatalf("last busy activity = %v, want %v", snapshot.LastBusyActivityAt, staleBusy)
	}
	if snapshot.LastActivityAt.Equal(staleBusy) {
		t.Fatalf("last activity = %v, want a refreshed timestamp", snapshot.LastActivityAt)
	}
}

func TestStageModelOverrideUpdatesSnapshot(t *testing.T) {
	s := &appServerSession{
		projectPath:     "/tmp/demo",
		model:           "gpt-5",
		reasoningEffort: "medium",
		entryIndex:      make(map[string]int),
		notify:          func() {},
		lastActivityAt:  time.Now(),
	}

	if err := s.StageModelOverride("gpt-5-codex", "high"); err != nil {
		t.Fatalf("StageModelOverride() error = %v", err)
	}
	snapshot := s.Snapshot()
	if snapshot.PendingModel != "gpt-5-codex" || snapshot.PendingReasoning != "high" {
		t.Fatalf("staged snapshot = %#v, want gpt-5-codex/high", snapshot)
	}

	if err := s.StageModelOverride("gpt-5", "medium"); err != nil {
		t.Fatalf("StageModelOverride(reset) error = %v", err)
	}
	snapshot = s.Snapshot()
	if snapshot.PendingModel != "" || snapshot.PendingReasoning != "" {
		t.Fatalf("reset staged snapshot = %#v, want cleared pending model state", snapshot)
	}
}

func stringPtr(value string) *string {
	return &value
}

func TestTurnCompletedDoesNotWaitForUserMessageStart(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))
	s.handleNotification("item/started", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_user",
			"type": "userMessage",
			"content": [{"type":"input_text","text":"summarize this repo"}]
		}
	}`))
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false after turn completion when only a user message started")
	}
	if snapshot.Status != "Turn completed" && !strings.HasPrefix(snapshot.Status, "Completed in ") {
		t.Fatalf("status = %q, want a completed-turn status", snapshot.Status)
	}
}

func TestTurnCompletedWaitsForCommandOutputDeltaToFinish(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))

	s.mu.Lock()
	s.busySince = time.Time{}
	s.mu.Unlock()

	s.handleNotification("item/commandExecution/outputDelta", json.RawMessage(`{
		"threadId":"thread_456",
		"turnId":"turn_live",
		"itemId":"item_cmd",
		"delta":" M README.md"
	}`))
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true while command output is still active")
	}
	if got := len(snapshot.Entries); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
	if snapshot.Entries[0].Kind != TranscriptCommand {
		t.Fatalf("entry kind = %q, want %q", snapshot.Entries[0].Kind, TranscriptCommand)
	}
	if snapshot.Entries[0].Text != "M README.md" {
		t.Fatalf("command delta text = %q, want trimmed output delta", snapshot.Entries[0].Text)
	}

	s.handleNotification("item/completed", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_cmd",
			"type": "commandExecution",
			"command": "git status --short",
			"cwd": "/tmp/demo",
			"aggregatedOutput": " M README.md",
			"status": "completed",
			"exitCode": 0
		}
	}`))

	snapshot = s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false once the command item finishes")
	}
	if snapshot.Status != "Turn completed" && !strings.HasPrefix(snapshot.Status, "Completed in ") {
		t.Fatalf("status = %q, want a completed-turn status", snapshot.Status)
	}
	if !strings.Contains(snapshot.Entries[0].Text, "[command completed, exit 0]") {
		t.Fatalf("command entry missing completion summary: %q", snapshot.Entries[0].Text)
	}
}

func TestTurnCompletedWaitsForPlanDeltaToFinish(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))
	s.handleNotification("item/started", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_plan",
			"type": "plan"
		}
	}`))
	s.handleNotification("item/plan/delta", json.RawMessage(`{
		"threadId":"thread_456",
		"turnId":"turn_live",
		"itemId":"item_plan",
		"delta":"1. Investigate the stuck completion state"
	}`))
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true while plan output is still active")
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Kind != TranscriptPlan {
		t.Fatalf("entry kind = %q, want %q", snapshot.Entries[0].Kind, TranscriptPlan)
	}
	if snapshot.Entries[0].Text != "1. Investigate the stuck completion state" {
		t.Fatalf("plan delta text = %q, want streamed plan text", snapshot.Entries[0].Text)
	}

	s.handleNotification("item/completed", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_plan",
			"type": "plan",
			"text": "1. Investigate the stuck completion state\n2. Reconcile idle notifications"
		}
	}`))

	snapshot = s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false once the plan item finishes")
	}
	if snapshot.Status != "Turn completed" && !strings.HasPrefix(snapshot.Status, "Completed in ") {
		t.Fatalf("status = %q, want a completed-turn status", snapshot.Status)
	}
	if !strings.Contains(snapshot.Entries[0].Text, "Reconcile idle notifications") {
		t.Fatalf("completed plan text missing final content: %q", snapshot.Entries[0].Text)
	}
}

func TestThreadStatusIdleClearsBusyWhenItemCompletionIsMissing(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		threadID:    "thread_456",
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))
	s.handleNotification("item/started", json.RawMessage(`{
		"turnId":"turn_live",
		"item": {
			"id": "item_agent",
			"type": "agentMessage"
		}
	}`))
	s.handleNotification("item/agentMessage/delta", json.RawMessage(`{
		"threadId":"thread_456",
		"turnId":"turn_live",
		"itemId":"item_agent",
		"delta":"still streaming"
	}`))
	s.handleNotification("turn/completed", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"completed"}}`))

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true before thread idle fallback")
	}

	s.handleNotification("thread/status/changed", json.RawMessage(`{
		"threadId":"thread_456",
		"status":{"type":"idle"}
	}`))

	snapshot = s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false once the thread reports idle")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false once the thread reports idle")
	}
	if snapshot.Status != "Turn completed" && !strings.HasPrefix(snapshot.Status, "Completed in ") {
		t.Fatalf("status = %q, want a completed-turn status after idle fallback", snapshot.Status)
	}
}

func TestThreadStatusIdleClearsResumedBusyExternalTurn(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.hydrateResumedThread(resumedThread{
		ID: "thread_456",
		Status: resumedThreadStatus{
			Type: "active",
		},
		Turns: []resumedTurn{
			{
				ID:     "turn_live",
				Status: "inProgress",
			},
		},
	})

	s.handleNotification("thread/status/changed", json.RawMessage(`{
		"threadId":"thread_456",
		"status":{"type":"idle"}
	}`))

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false once the resumed thread reports idle")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false once the resumed thread reports idle")
	}
	if snapshot.Status != "Turn finished" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Turn finished")
	}
}

func TestRecoveredIdleStatusMarksRecoveredTurn(t *testing.T) {
	s := &appServerSession{
		projectPath:  "/tmp/demo",
		entryIndex:   make(map[string]int),
		notify:       func() {},
		threadID:     "thread_456",
		busy:         true,
		activeTurnID: "turn_live",
	}

	s.syncThreadStatusLocked("thread_456", resumedThreadStatus{Type: "idle"}, true)

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false after recovered idle")
	}
	if snapshot.Status != "Recovered idle after status check" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Recovered idle after status check")
	}
	if snapshot.Phase != SessionPhaseIdle {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseIdle)
	}
}

func TestFormatTurnCompletionStatusIncludesDuration(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	busySince := now.Add(-(3*time.Minute + 7*time.Second))

	if got := formatTurnCompletionStatus("completed", busySince, now); got != "Completed in 03:07" {
		t.Fatalf("formatTurnCompletionStatus() = %q, want %q", got, "Completed in 03:07")
	}
}

func TestHandleItemStartedBindsOptimisticUserPromptToServerItem(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.appendEntryLocked("", TranscriptUser, "summarize this repo")
	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_user",
			"type": "userMessage",
			"content": [{"type":"input_text","text":"summarize this repo"}]
		}
	}`))
	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_user",
			"type": "userMessage",
			"content": [{"type":"input_text","text":"summarize this repo"}]
		}
	}`))

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1: %#v", len(snapshot.Entries), snapshot.Entries)
	}
	entry := snapshot.Entries[0]
	if entry.ItemID != "item_user" {
		t.Fatalf("item id = %q, want item_user", entry.ItemID)
	}
	if entry.Kind != TranscriptUser {
		t.Fatalf("kind = %q, want %q", entry.Kind, TranscriptUser)
	}
	if entry.Text != "summarize this repo" {
		t.Fatalf("text = %q, want prompt text", entry.Text)
	}
}

func TestHandleItemCompletedReplacesInProgressCommandStatus(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_cmd",
			"type": "commandExecution",
			"command": "git status --short",
			"cwd": "/tmp/demo",
			"status": "inProgress"
		}
	}`))
	s.handleNotification("item/commandExecution/outputDelta", json.RawMessage(`{
		"itemId": "item_cmd",
		"delta": " M README.md"
	}`))
	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_cmd",
			"type": "commandExecution",
			"command": "git status --short",
			"cwd": "/tmp/demo",
			"aggregatedOutput": " M README.md",
			"status": "completed",
			"exitCode": 0
		}
	}`))

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1: %#v", len(snapshot.Entries), snapshot.Entries)
	}
	text := snapshot.Entries[0].Text
	if strings.Contains(text, "[command inProgress]") {
		t.Fatalf("command entry should replace stale in-progress marker: %q", text)
	}
	if strings.Count(text, "[command completed, exit 0]") != 1 {
		t.Fatalf("command completion summary count = %d, want 1: %q", strings.Count(text, "[command completed, exit 0]"), text)
	}
	if !strings.Contains(text, " M README.md") {
		t.Fatalf("command output missing from merged text: %q", text)
	}
}

func TestHandleItemCompletedUpdatesToolStatus(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_tool",
			"type": "dynamicToolCall",
			"tool": "request_user_input",
			"status": "inProgress"
		}
	}`))
	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_tool",
			"type": "dynamicToolCall",
			"tool": "request_user_input",
			"status": "completed"
		}
	}`))

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1: %#v", len(snapshot.Entries), snapshot.Entries)
	}
	text := snapshot.Entries[0].Text
	if strings.Contains(text, "[inProgress]") {
		t.Fatalf("tool entry should replace stale in-progress marker: %q", text)
	}
	if text != "Tool request_user_input [completed]" {
		t.Fatalf("tool text = %q, want completed status", text)
	}
}

func TestReadStdoutHandlesLargeJSONLines(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	delta := strings.Repeat("x", 2*1024*1024)
	payload := fmt.Sprintf(`{"method":"item/agentMessage/delta","params":{"itemId":"item_big","delta":%q}}`+"\n", delta)

	s.readStdout(strings.NewReader(payload))

	snapshot := s.Snapshot()
	if snapshot.Closed {
		t.Fatalf("closed = true, want false")
	}
	if snapshot.LastError != "" {
		t.Fatalf("last error = %q, want empty", snapshot.LastError)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snapshot.Entries))
	}
	if got := len(snapshot.Entries[0].Text); got != len(delta) {
		t.Fatalf("entry text length = %d, want %d", got, len(delta))
	}
}

func TestReadStdoutStreamErrorClosesBusySession(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		busy:               true,
		busyExternal:       true,
		busySince:          time.Now(),
		activeTurnID:       "turn_live",
		pendingApproval:    &ApprovalRequest{ID: "approval_1"},
		pendingToolInput:   &ToolInputRequest{ID: "tool_1"},
		pendingElicitation: &ElicitationRequest{ID: "elicitation_1"},
		status:             "Codex is working...",
	}

	s.readStdout(failingReader{err: errors.New("boom")})

	snapshot := s.Snapshot()
	if !snapshot.Closed {
		t.Fatalf("closed = false, want true")
	}
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false")
	}
	if !snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = %v, want zero", snapshot.BusySince)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		t.Fatalf("pending interactive state should be cleared: %#v %#v %#v", snapshot.PendingApproval, snapshot.PendingToolInput, snapshot.PendingElicitation)
	}
	if snapshot.Status != "Codex transport failed; session closed" {
		t.Fatalf("status = %q, want transport failure status", snapshot.Status)
	}
	if !strings.Contains(snapshot.LastError, "app-server stream error: boom") {
		t.Fatalf("last error = %q, want stream error", snapshot.LastError)
	}
	if len(snapshot.Entries) == 0 {
		t.Fatalf("entries = 0, want error transcript entry")
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Kind != TranscriptError {
		t.Fatalf("last entry kind = %q, want %q", last.Kind, TranscriptError)
	}
	if !strings.Contains(last.Text, "app-server stream error: boom") {
		t.Fatalf("last entry text = %q, want stream error", last.Text)
	}
}

func TestCloseClearsBusyState(t *testing.T) {
	s := &appServerSession{
		projectPath:  "/tmp/demo",
		entryIndex:   make(map[string]int),
		notify:       func() {},
		busy:         true,
		busyExternal: true,
		busySince:    time.Now(),
		activeTurnID: "turn_live",
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	snapshot := s.Snapshot()
	if !snapshot.Closed {
		t.Fatalf("closed = false, want true")
	}
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false")
	}
	if !snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = %v, want zero", snapshot.BusySince)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
}

func TestReadStderrAppendsAuth403Diagnosis(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.readStderr(strings.NewReader("2026-03-17T15:31:02.728473Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: HTTP error: 403 Forbidden, url: wss://chatgpt.com/backend-api/codex/responses\n"))

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Kind != TranscriptSystem || !strings.Contains(snapshot.Entries[0].Text, "codex stderr:") {
		t.Fatalf("first entry = %#v, want raw stderr notice", snapshot.Entries[0])
	}
	if snapshot.Entries[1].Kind != TranscriptSystem || !strings.Contains(snapshot.Entries[1].Text, "Codex rejected the request with HTTP 403.") {
		t.Fatalf("second entry = %#v, want auth diagnosis notice", snapshot.Entries[1])
	}
	if snapshot.Status != codexAuth403StatusLabel() {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexAuth403StatusLabel())
	}
}

func TestAuth403DiagnosisIsOnlyAppendedOnce(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.readStderr(strings.NewReader("2026-03-17T15:31:02.728473Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: HTTP error: 403 Forbidden, url: wss://chatgpt.com/backend-api/codex/responses\n"))
	s.appendSystemError(errors.New("unexpected status 403 Forbidden: Unknown error, url: https://chatgpt.com/backend-api/codex/responses"))

	snapshot := s.Snapshot()
	count := 0
	for _, entry := range snapshot.Entries {
		if strings.Contains(entry.Text, "Codex rejected the request with HTTP 403.") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("auth diagnosis count = %d, want 1; entries = %#v", count, snapshot.Entries)
	}
}

func TestHydrateResumedThreadKeepsBusySinceForSameActiveTurn(t *testing.T) {
	startedAt := time.Date(2026, 3, 9, 17, 0, 0, 0, time.UTC)
	s := &appServerSession{
		projectPath:  "/tmp/demo",
		entryIndex:   make(map[string]int),
		notify:       func() {},
		busy:         true,
		busyExternal: true,
		busySince:    startedAt,
		activeTurnID: "turn_live",
	}

	s.hydrateResumedThreadLocked(resumedThread{
		ID: "thread_demo",
		Status: resumedThreadStatus{
			Type: "active",
		},
		Turns: []resumedTurn{{
			ID:     "turn_live",
			Status: "inProgress",
		}},
	})

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if !snapshot.BusyExternal {
		t.Fatalf("busy external = false, want true")
	}
	if !snapshot.BusySince.Equal(startedAt) {
		t.Fatalf("busy since = %v, want %v", snapshot.BusySince, startedAt)
	}
	if snapshot.ActiveTurnID != "turn_live" {
		t.Fatalf("active turn id = %q, want %q", snapshot.ActiveTurnID, "turn_live")
	}
}

func TestSubmitInputRetriesSteerAfterActiveTurnMismatch(t *testing.T) {
	startedAt := time.Date(2026, 3, 9, 17, 0, 0, 0, time.UTC)
	callCount := 0

	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread_456",
		activeTurnID: "turn_old",
		busy:         true,
		busySince:    startedAt,
		entryIndex:   make(map[string]int),
		notify:       func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "turn/steer" {
					t.Fatalf("call 1 method = %q, want turn/steer", method)
				}
				request, ok := params.(turnSteerParams)
				if !ok {
					t.Fatalf("call 1 params = %#v, want turnSteerParams", params)
				}
				if request.ExpectedTurnID != "turn_old" {
					t.Fatalf("call 1 expected turn id = %q, want turn_old", request.ExpectedTurnID)
				}
				return nil, errors.New("expected active turn id `turn_old` but found `turn_new`")
			case 2:
				if method != "thread/read" {
					t.Fatalf("call 2 method = %q, want thread/read", method)
				}
				request, ok := params.(threadReadParams)
				if !ok {
					t.Fatalf("call 2 params = %#v, want threadReadParams", params)
				}
				if !request.IncludeTurns {
					t.Fatalf("call 2 includeTurns = false, want true")
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[{"id":"turn_new","status":"inProgress"}]}}`), nil
			case 3:
				if method != "turn/steer" {
					t.Fatalf("call 3 method = %q, want turn/steer", method)
				}
				request, ok := params.(turnSteerParams)
				if !ok {
					t.Fatalf("call 3 params = %#v, want turnSteerParams", params)
				}
				if request.ExpectedTurnID != "turn_new" {
					t.Fatalf("call 3 expected turn id = %q, want turn_new", request.ExpectedTurnID)
				}
				return json.RawMessage(`{"turnId":"turn_new"}`), nil
			default:
				t.Fatalf("unexpected rpc call %d: %s", callCount, method)
				return nil, nil
			}
		},
	}

	if err := s.Submit("follow up"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("rpc call count = %d, want 3", callCount)
	}

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_new" {
		t.Fatalf("active turn id = %q, want turn_new", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Sent follow-up to Codex" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Sent follow-up to Codex")
	}
	if !snapshot.BusySince.After(startedAt) {
		t.Fatalf("busy since = %v, want reset after switching to the new turn", snapshot.BusySince)
	}
}

func TestSubmitInputStartsNewTurnWhenSteerMismatchFindsIdleThread(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread_456",
		activeTurnID: "turn_old",
		busy:         true,
		entryIndex:   make(map[string]int),
		notify:       func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "turn/steer" {
					t.Fatalf("call 1 method = %q, want turn/steer", method)
				}
				return nil, errors.New("expected active turn id `turn_old` but found `turn_new`")
			case 2:
				if method != "thread/read" {
					t.Fatalf("call 2 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"},"turns":[]}}`), nil
			case 3:
				if method != "turn/start" {
					t.Fatalf("call 3 method = %q, want turn/start", method)
				}
				return json.RawMessage(`{"turn":{"id":"turn_fresh"}}`), nil
			default:
				t.Fatalf("unexpected rpc call %d: %s", callCount, method)
				return nil, nil
			}
		},
	}

	if err := s.Submit("follow up"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("rpc call count = %d, want 3", callCount)
	}

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_fresh" {
		t.Fatalf("active turn id = %q, want turn_fresh", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Codex is working..." {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Codex is working...")
	}
}

func TestRecordSteerSubmissionClearsPriorTurnState(t *testing.T) {
	startedAt := time.Date(2026, 3, 9, 17, 0, 0, 0, time.UTC)
	s := &appServerSession{
		projectPath:       "/tmp/demo",
		entryIndex:        make(map[string]int),
		notify:            func() {},
		busy:              true,
		busySince:         startedAt,
		activeTurnID:      "turn_old",
		activeItems:       map[string]struct{}{"item_old": {}},
		pendingCompletion: &turnCompletionState{TurnID: "turn_old", Status: "Completed in 00:04"},
	}

	s.recordSteerSubmission("turn_new")

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_new" {
		t.Fatalf("active turn id = %q, want turn_new", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Sent follow-up to Codex" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Sent follow-up to Codex")
	}
	if s.pendingCompletion != nil {
		t.Fatalf("pending completion = %#v, want nil", s.pendingCompletion)
	}
	if len(s.activeItems) != 0 {
		t.Fatalf("active items = %#v, want cleared", s.activeItems)
	}
	if !snapshot.BusySince.After(startedAt) {
		t.Fatalf("busy since = %v, want reset for the new turn", snapshot.BusySince)
	}
}

type failingReader struct {
	err error
}

func (r failingReader) Read(_ []byte) (int, error) {
	if r.err == nil {
		return 0, io.EOF
	}
	return 0, r.err
}
