package codexapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
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

func TestCloseExitChKeepsCodexHomeOverlayForResumedSkillPaths(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "lcroom-codex-home-test")
	if err := os.MkdirAll(filepath.Join(overlay, "skills", "playwright"), 0o700); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "skills", "playwright", "SKILL.md"), []byte("shadow skill"), 0o644); err != nil {
		t.Fatalf("write shadow skill: %v", err)
	}

	s := &appServerSession{
		codexHomeOverlay: overlay,
		exitCh:           make(chan struct{}),
	}

	s.closeExitCh()

	if _, err := os.Stat(filepath.Join(overlay, "skills", "playwright", "SKILL.md")); err != nil {
		t.Fatalf("expected overlay skill to remain after session exit: %v", err)
	}
}

func TestHydrateResumedThreadMaterializesGeneratedImages(t *testing.T) {
	dataDir := t.TempDir()
	imageBytes := mustGeneratedImageTestPNG(t)
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	s := &appServerSession{
		projectPath: "/tmp/demo",
		dataDir:     dataDir,
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.hydrateResumedThread(resumedThread{
		ID: "thread_demo",
		Status: resumedThreadStatus{
			Type: "idle",
		},
		Turns: []resumedTurn{
			{
				ID:     "turn_1",
				Status: "completed",
				Items: []map[string]json.RawMessage{
					{
						"id":         json.RawMessage(`"ig_demo"`),
						"type":       json.RawMessage(`"imageGeneration"`),
						"status":     json.RawMessage(`"generating"`),
						"result":     jsonStringRaw(t, encoded),
						"saved_path": json.RawMessage(`"/tmp/lcroom-codex-home-deleted/generated_images/ig_demo.png"`),
					},
				},
			},
		},
	})

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snapshot.Entries))
	}
	entry := snapshot.Entries[0]
	if entry.GeneratedImage == nil {
		t.Fatalf("generated image metadata missing from transcript entry: %#v", entry)
	}
	wantPath := filepath.Join(dataDir, "artifacts", generatedImageArtifactRootName, "thread_demo", "ig_demo.png")
	if entry.GeneratedImage.Path != wantPath {
		t.Fatalf("generated image path = %q, want %q", entry.GeneratedImage.Path, wantPath)
	}
	if entry.GeneratedImage.Width != 2 || entry.GeneratedImage.Height != 1 {
		t.Fatalf("generated image dimensions = %dx%d, want 2x1", entry.GeneratedImage.Width, entry.GeneratedImage.Height)
	}
	written, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("expected durable generated image file: %v", err)
	}
	if !bytes.Equal(written, imageBytes) {
		t.Fatalf("durable generated image bytes differ from result bytes")
	}
	if strings.Contains(entry.Text, encoded) || strings.Contains(snapshot.Transcript, encoded) {
		t.Fatalf("transcript should not expose generated image base64")
	}
	if strings.Contains(entry.Text, "[generating]") {
		t.Fatalf("completed image result should not render as still generating: %q", entry.Text)
	}
}

func TestHandleImageGenerationCompletionRefreshesMissingLiveArtifact(t *testing.T) {
	oldDelay := generatedImageArtifactRefreshDelay
	generatedImageArtifactRefreshDelay = 0
	t.Cleanup(func() { generatedImageArtifactRefreshDelay = oldDelay })

	dataDir := t.TempDir()
	imageBytes := mustGeneratedImageTestPNG(t)
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	encodedRaw := jsonStringRaw(t, encoded)
	threadReadCalled := make(chan struct{}, 1)

	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread_demo",
		dataDir:      dataDir,
		activeTurnID: "turn_live",
		busy:         true,
		entryIndex:   make(map[string]int),
		notify:       func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "thread/read" {
				return nil, fmt.Errorf("method = %q, want thread/read", method)
			}
			request, ok := params.(threadReadParams)
			if !ok {
				return nil, fmt.Errorf("params = %#v, want threadReadParams", params)
			}
			if request.ThreadID != "thread_demo" || !request.IncludeTurns {
				return nil, fmt.Errorf("thread/read params = %#v, want thread_demo with turns", request)
			}
			select {
			case threadReadCalled <- struct{}{}:
			default:
			}
			return json.RawMessage(`{"thread":{"id":"thread_demo","status":{"type":"idle"},"turns":[{"id":"turn_live","status":"completed","items":[{"id":"ig_live","type":"imageGeneration","status":"completed","result":` + string(encodedRaw) + `,"saved_path":"/tmp/codex/generated_images/ig_live.png"}]}]}}`), nil
		},
	}

	s.handleNotification("item/started", json.RawMessage(`{
		"threadId":"thread_demo",
		"turnId":"turn_live",
		"item":{"id":"ig_live","type":"imageGeneration","status":"generating"}
	}`))
	s.handleNotification("item/completed", json.RawMessage(`{
		"threadId":"thread_demo",
		"turnId":"turn_live",
		"item":{"id":"ig_live","type":"imageGeneration","status":"completed"}
	}`))

	select {
	case <-threadReadCalled:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for generated image artifact refresh")
	}

	var entry TranscriptEntry
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := s.Snapshot()
		if len(snapshot.Entries) == 1 && snapshot.Entries[0].GeneratedImage != nil {
			entry = snapshot.Entries[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if entry.GeneratedImage == nil {
		t.Fatalf("generated image metadata missing after live refresh: %#v", s.Snapshot().Entries)
	}
	wantPath := filepath.Join(dataDir, "artifacts", generatedImageArtifactRootName, "thread_demo", "ig_live.png")
	if entry.GeneratedImage.Path != wantPath {
		t.Fatalf("generated image path = %q, want %q", entry.GeneratedImage.Path, wantPath)
	}
	if entry.GeneratedImage.Width != 2 || entry.GeneratedImage.Height != 1 {
		t.Fatalf("generated image dimensions = %dx%d, want 2x1", entry.GeneratedImage.Width, entry.GeneratedImage.Height)
	}
	if strings.Contains(entry.Text, encoded) {
		t.Fatalf("transcript should not expose generated image base64")
	}
}

func TestHydrateResumedThreadTracksCurrentPlaywrightPageURL(t *testing.T) {
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
						"id":     json.RawMessage(`"item_browser"`),
						"type":   json.RawMessage(`"mcpToolCall"`),
						"server": json.RawMessage(`"playwright"`),
						"tool":   json.RawMessage(`"browser_click"`),
						"status": json.RawMessage(`"completed"`),
						"result": json.RawMessage(`{
							"content": [
								{
									"type": "text",
									"text": "### Page state\n- Page URL: https://chartboost.us.auth0.com/u/login?state=demo\n"
								}
							]
						}`),
					},
				},
			},
		},
	})

	snapshot := s.Snapshot()
	if got, want := snapshot.CurrentBrowserPageURL, "https://chartboost.us.auth0.com/u/login?state=demo"; got != want {
		t.Fatalf("current browser page URL = %q, want %q", got, want)
	}
	if !snapshot.CurrentBrowserPageStale {
		t.Fatalf("current browser page should be marked stale after resume")
	}
	playwright := requireMCPUsage(t, snapshot, "playwright")
	if playwright.ToolCalls != 1 || playwright.LastTool != "browser_click" || mcpToolCalls(playwright, "browser_click") != 1 {
		t.Fatalf("resumed Playwright MCP usage = %#v, want one browser_click call", playwright)
	}
}

func mustGeneratedImageTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	img.SetRGBA(1, 0, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func jsonStringRaw(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal json string: %v", err)
	}
	return raw
}

func requireMCPUsage(t *testing.T, snapshot Snapshot, serverName string) MCPUsageSnapshot {
	t.Helper()
	for _, usage := range snapshot.MCPUsage {
		if usage.ServerName == serverName {
			return usage
		}
	}
	t.Fatalf("MCP usage for %q not found in %#v", serverName, snapshot.MCPUsage)
	return MCPUsageSnapshot{}
}

func mcpToolCalls(usage MCPUsageSnapshot, toolName string) int {
	for _, tool := range usage.Tools {
		if tool.Name == toolName {
			return tool.Calls
		}
	}
	return 0
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

func TestTurnStartedWithoutFollowupActivityStaysIdle(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false until meaningful activity arrives")
	}
	if !snapshot.BusySince.IsZero() {
		t.Fatalf("busy since = %v, want zero while the turn is still provisional", snapshot.BusySince)
	}
	if snapshot.ActiveTurnID != "turn_live" {
		t.Fatalf("active turn id = %q, want turn_live", snapshot.ActiveTurnID)
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

func TestStageModelOverrideKeepsTranscriptRevisionStable(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		model:              "gpt-5",
		reasoningEffort:    "medium",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastActivityAt:     time.Now(),
		transcriptRevision: 1,
		entries: []transcriptEntry{{
			Kind: TranscriptAgent,
			Text: "Existing reply",
		}},
	}

	first := s.Snapshot()
	if err := s.StageModelOverride("gpt-5-codex", "high"); err != nil {
		t.Fatalf("StageModelOverride() error = %v", err)
	}
	second := s.Snapshot()

	if second.TranscriptRevision != first.TranscriptRevision {
		t.Fatalf("transcript revision changed from %d to %d after model stage", first.TranscriptRevision, second.TranscriptRevision)
	}
	if second.Transcript != first.Transcript {
		t.Fatalf("transcript changed after model stage: %q -> %q", first.Transcript, second.Transcript)
	}
	if len(second.Entries) != len(first.Entries) || second.Entries[0].Text != first.Entries[0].Text {
		t.Fatalf("entries changed after model stage: %#v -> %#v", first.Entries, second.Entries)
	}
}

func TestSnapshotBoundsLargeTranscriptExport(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		transcriptRevision: 1,
	}
	for i := 0; i < 900; i++ {
		s.entries = append(s.entries, transcriptEntry{
			ItemID: fmt.Sprintf("old_%03d", i),
			Kind:   TranscriptCommand,
			Text:   fmt.Sprintf("historical command %03d\n%s", i, strings.Repeat("output ", 700)),
		})
	}
	s.entries = append(s.entries, transcriptEntry{
		ItemID: "latest",
		Kind:   TranscriptAgent,
		Text:   strings.Repeat("latest reply body ", 12000) + "FINAL_REPLY_STAYS",
	})

	snapshot := s.Snapshot()
	if len(snapshot.Entries) == 0 {
		t.Fatalf("snapshot entries empty, want bounded transcript")
	}
	if len(snapshot.Entries) > maxExportedTranscriptEntries+1 {
		t.Fatalf("snapshot entries = %d, want at most %d", len(snapshot.Entries), maxExportedTranscriptEntries+1)
	}
	if snapshot.Entries[0].Kind != TranscriptSystem || !strings.Contains(snapshot.Entries[0].Text, "older transcript entries omitted") {
		t.Fatalf("first entry = %#v, want omission marker", snapshot.Entries[0])
	}
	if !strings.Contains(snapshot.Transcript, "FINAL_REPLY_STAYS") {
		t.Fatalf("snapshot transcript dropped the latest reply tail")
	}
	if len(snapshot.Transcript) > maxExportedTranscriptBytes+64*1024 {
		t.Fatalf("snapshot transcript length = %d, want bounded near %d", len(snapshot.Transcript), maxExportedTranscriptBytes)
	}
}

func TestSnapshotKeepsDeepTextHistory(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		transcriptRevision: 1,
	}
	for i := 0; i < 700; i++ {
		s.entries = append(s.entries, transcriptEntry{
			ItemID: fmt.Sprintf("entry_%03d", i),
			Kind:   TranscriptAgent,
			Text:   fmt.Sprintf("reply %03d", i),
		})
	}

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 700 {
		t.Fatalf("snapshot entries = %d, want full 700-entry history", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Kind == TranscriptSystem && strings.Contains(snapshot.Entries[0].Text, "older transcript entries omitted") {
		t.Fatalf("snapshot unexpectedly clipped modest deep history: %#v", snapshot.Entries[0])
	}
	if snapshot.Entries[0].Text != "reply 000" || snapshot.Entries[len(snapshot.Entries)-1].Text != "reply 699" {
		t.Fatalf("snapshot history boundaries = %q .. %q, want reply 000 .. reply 699",
			snapshot.Entries[0].Text, snapshot.Entries[len(snapshot.Entries)-1].Text)
	}
}

func TestSnapshotGeneratedImagePreviewDoesNotEvictTextHistory(t *testing.T) {
	preview := bytes.Repeat([]byte("p"), maxExportedGeneratedImagePreviewBytes-1024)
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		transcriptRevision: 1,
	}
	for i := 0; i < 640; i++ {
		s.entries = append(s.entries, transcriptEntry{
			ItemID: fmt.Sprintf("entry_%03d", i),
			Kind:   TranscriptAgent,
			Text:   fmt.Sprintf("reply %03d", i),
		})
	}
	s.entries = append(s.entries, transcriptEntry{
		ItemID: "image_latest",
		Kind:   TranscriptTool,
		Text:   "Generated image",
		GeneratedImage: &GeneratedImageArtifact{
			ID:          "image_latest",
			Path:        "/tmp/image-latest.png",
			ByteSize:    int64(len(preview)),
			PreviewData: preview,
		},
	})

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 641 {
		t.Fatalf("snapshot entries = %d, want text history plus image", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Text != "reply 000" {
		t.Fatalf("snapshot first entry = %q, want oldest text retained", snapshot.Entries[0].Text)
	}
	image := snapshot.Entries[len(snapshot.Entries)-1].GeneratedImage
	if image == nil || len(image.PreviewData) != len(preview) {
		t.Fatalf("latest generated image preview length = %d, want %d", len(image.PreviewData), len(preview))
	}
}

func TestSnapshotKeepsOnlyNewestGeneratedImagePreview(t *testing.T) {
	oldPreview := []byte("old preview")
	newPreview := []byte("new preview")
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		transcriptRevision: 1,
	}
	s.entries = append(s.entries,
		transcriptEntry{
			ItemID: "image_old",
			Kind:   TranscriptTool,
			Text:   "Generated image",
			GeneratedImage: &GeneratedImageArtifact{
				ID:          "image_old",
				Path:        "/tmp/image-old.png",
				PreviewData: oldPreview,
			},
		},
		transcriptEntry{
			ItemID: "image_new",
			Kind:   TranscriptTool,
			Text:   "Generated image",
			GeneratedImage: &GeneratedImageArtifact{
				ID:          "image_new",
				Path:        "/tmp/image-new.png",
				PreviewData: newPreview,
			},
		},
	)

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot entries = %d, want 2", len(snapshot.Entries))
	}
	if snapshot.Entries[0].GeneratedImage == nil || len(snapshot.Entries[0].GeneratedImage.PreviewData) != 0 {
		t.Fatalf("older image preview should be stripped, got %#v", snapshot.Entries[0].GeneratedImage)
	}
	if snapshot.Entries[1].GeneratedImage == nil || !bytes.Equal(snapshot.Entries[1].GeneratedImage.PreviewData, newPreview) {
		t.Fatalf("newest image preview not retained: %#v", snapshot.Entries[1].GeneratedImage)
	}
}

func TestStagedModelOverride(t *testing.T) {
	model, reasoning := stagedModelOverride("gpt-5", "medium", "gpt-5-codex", "high")
	if model != "gpt-5-codex" || reasoning != "high" {
		t.Fatalf("stagedModelOverride(change) = (%q, %q), want (gpt-5-codex, high)", model, reasoning)
	}

	model, reasoning = stagedModelOverride("gpt-5", "medium", "gpt-5", "medium")
	if model != "" || reasoning != "" {
		t.Fatalf("stagedModelOverride(same) = (%q, %q), want empty", model, reasoning)
	}

	model, reasoning = stagedModelOverride("gpt-5", "medium", "", "")
	if model != "" || reasoning != "" {
		t.Fatalf("stagedModelOverride(empty) = (%q, %q), want empty", model, reasoning)
	}

	model, reasoning = stagedModelOverride("gpt-5", "medium", "gpt-5-codex", "")
	if model != "gpt-5-codex" || reasoning != "medium" {
		t.Fatalf("stagedModelOverride(fill reasoning) = (%q, %q), want (gpt-5-codex, medium)", model, reasoning)
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

func TestHydrateResumedThreadDoesNotStayBusyWithoutInProgressTurn(t *testing.T) {
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
				ID:     "turn_done",
				Status: "completed",
			},
		},
	})

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false when no turn is actually in progress")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false when no turn is actually in progress")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
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

func TestAppServerSessionTracksMCPUsage(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_navigate",
			"status": "inProgress"
		}
	}`))
	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_navigate",
			"status": "completed"
		}
	}`))
	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_runtime",
			"type": "mcpToolCall",
			"server": "lcr_runtime",
			"tool": "process_list",
			"status": "completed"
		}
	}`))

	snapshot := s.Snapshot()
	playwright := requireMCPUsage(t, snapshot, "playwright")
	if playwright.ToolCalls != 1 || playwright.LastTool != "browser_navigate" || mcpToolCalls(playwright, "browser_navigate") != 1 {
		t.Fatalf("playwright MCP usage = %#v, want one browser_navigate call", playwright)
	}
	runtime := requireMCPUsage(t, snapshot, "lcr_runtime")
	if runtime.ToolCalls != 1 || runtime.LastTool != "process_list" || mcpToolCalls(runtime, "process_list") != 1 {
		t.Fatalf("runtime MCP usage = %#v, want one process_list call", runtime)
	}
}

func TestHandleItemStartedTracksPlaywrightBrowserActivity(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	s := &appServerSession{
		projectPath:             "/tmp/demo",
		entryIndex:              make(map[string]int),
		notify:                  func() {},
		playwrightPolicy:        policy,
		browserActivity:         browserctl.DefaultSessionActivity(policy),
		currentBrowserPageStale: true,
	}

	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_navigate",
			"status": "inProgress"
		}
	}`))

	snapshot := s.Snapshot()
	if got, want := snapshot.BrowserActivity.State, browserctl.SessionActivityStateActive; got != want {
		t.Fatalf("browser activity state = %q, want %q", got, want)
	}
	if got, want := snapshot.BrowserActivity.ServerName, "playwright"; got != want {
		t.Fatalf("browser activity server = %q, want %q", got, want)
	}
	if got, want := snapshot.BrowserActivity.ToolName, "browser_navigate"; got != want {
		t.Fatalf("browser activity tool = %q, want %q", got, want)
	}
}

func TestHandleItemCompletedTracksCurrentPlaywrightPageURL(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	s := &appServerSession{
		projectPath:      "/tmp/demo",
		entryIndex:       make(map[string]int),
		notify:           func() {},
		playwrightPolicy: policy,
		browserActivity:  browserctl.DefaultSessionActivity(policy),
	}

	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_click",
			"status": "completed",
			"result": {
				"content": [
					{
						"type": "text",
						"text": "### Page state\n- Page URL: https://chartboost.us.auth0.com/u/login?state=demo\n- Page Title: Log in | Chartboost\n"
					}
				]
			}
		}
	}`))

	snapshot := s.Snapshot()
	if got, want := snapshot.CurrentBrowserPageURL, "https://chartboost.us.auth0.com/u/login?state=demo"; got != want {
		t.Fatalf("current browser page URL = %q, want %q", got, want)
	}
	if snapshot.CurrentBrowserPageStale {
		t.Fatalf("current browser page should no longer be stale after live Playwright result")
	}
}

func TestPlaywrightElicitationUpdatesBrowserActivity(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}
	s := &appServerSession{
		projectPath:      "/tmp/demo",
		entryIndex:       make(map[string]int),
		notify:           func() {},
		playwrightPolicy: policy,
		browserActivity:  browserctl.DefaultSessionActivity(policy),
	}

	s.handleItemStarted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_navigate",
			"status": "inProgress"
		}
	}`))
	s.handleServerRequest(rpcEnvelope{
		Method: "mcpServer/elicitation/request",
		ID:     json.RawMessage(`1`),
		Params: json.RawMessage(`{
			"serverName": "playwright",
			"threadId": "thread_1",
			"turnId": "turn_1",
			"mode": "form",
			"message": "Log in to continue",
			"elicitationId": "elicitation_1",
			"requestedSchema": {"type":"object"}
		}`),
	})

	snapshot := s.Snapshot()
	if got, want := snapshot.BrowserActivity.State, browserctl.SessionActivityStateWaitingForUser; got != want {
		t.Fatalf("browser activity state = %q, want %q", got, want)
	}
	if got, want := snapshot.Status, "Browser needs attention"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := snapshot.LastSystemNotice, "Playwright requested browser input"; got != want {
		t.Fatalf("last system notice = %q, want %q", got, want)
	}
	if snapshot.PendingElicitation == nil {
		t.Fatalf("pending elicitation = nil, want request")
	}

	s.handleNotification("serverRequest/resolved", json.RawMessage(`{
		"threadId": "thread_1",
		"requestId": 1
	}`))
	snapshot = s.Snapshot()
	if got, want := snapshot.BrowserActivity.State, browserctl.SessionActivityStateActive; got != want {
		t.Fatalf("browser activity state after resolve = %q, want %q", got, want)
	}

	s.handleItemCompleted(json.RawMessage(`{
		"item": {
			"id": "item_browser",
			"type": "mcpToolCall",
			"server": "playwright",
			"tool": "browser_navigate",
			"status": "completed"
		}
	}`))
	snapshot = s.Snapshot()
	if got, want := snapshot.BrowserActivity.State, browserctl.SessionActivityStateIdle; got != want {
		t.Fatalf("browser activity state after completion = %q, want %q", got, want)
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
	if got := len(snapshot.Entries[0].Text); got > maxExportedTranscriptEntryBytes {
		t.Fatalf("snapshot entry text length = %d, want at most %d", got, maxExportedTranscriptEntryBytes)
	}
	if !strings.Contains(snapshot.Entries[0].Text, "embedded transcript entry shortened") {
		t.Fatalf("snapshot entry text missing truncation marker")
	}
	if !strings.HasSuffix(snapshot.Entries[0].Text, strings.Repeat("x", 64)) {
		t.Fatalf("snapshot entry text should preserve the latest delta tail")
	}
	s.mu.Lock()
	rawEntryText := s.entries[0].Text
	s.mu.Unlock()
	if got := len(rawEntryText); got != len(delta) {
		t.Fatalf("raw entry text length = %d, want %d", got, len(delta))
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

func TestReadStderrCompactsServiceUnavailable503Status(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.readStderr(strings.NewReader("2026-03-20T05:08:05.951003Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: HTTP error: 503 Service Unavailable, url: wss://chatgpt.com/backend-api/codex/responses\n"))

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Kind != TranscriptSystem || !strings.Contains(snapshot.Entries[0].Text, "codex stderr:") {
		t.Fatalf("first entry = %#v, want raw stderr notice", snapshot.Entries[0])
	}
	if snapshot.Status != codexServiceUnavailable503StatusLabel() {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexServiceUnavailable503StatusLabel())
	}
	if !strings.Contains(snapshot.LastSystemNotice, "503 Service Unavailable") {
		t.Fatalf("last system notice = %q, want raw 503 stderr notice retained", snapshot.LastSystemNotice)
	}
}

func TestReadStderrUsesGenericCompactStatusForUnknownStderr(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.readStderr(strings.NewReader("2026-03-20T05:08:05.951003Z WARN codex_transport::stream: reconnecting after unexpected EOF\n"))

	snapshot := s.Snapshot()
	if snapshot.Status != "Codex reported stderr" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Codex reported stderr")
	}
	if !strings.Contains(snapshot.LastSystemNotice, "unexpected EOF") {
		t.Fatalf("last system notice = %q, want raw stderr notice retained", snapshot.LastSystemNotice)
	}
}

func TestAppendSystemErrorCompactsRateLimitedStatus(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.appendSystemError(errors.New("unexpected status 429 Too Many Requests: rate limited, url: https://chatgpt.com/backend-api/codex/responses"))

	snapshot := s.Snapshot()
	if snapshot.Status != codexRateLimited429StatusLabel() {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexRateLimited429StatusLabel())
	}
	if !strings.Contains(snapshot.LastSystemNotice, "429 Too Many Requests") {
		t.Fatalf("last system notice = %q, want raw error retained", snapshot.LastSystemNotice)
	}
}

func TestAppendCodexHomeCleanupWarning(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.appendCodexHomeCleanupWarning("/tmp/.codex", errors.New("database is locked"))

	snapshot := s.Snapshot()
	if snapshot.Status != codexHomeCleanupWarning {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexHomeCleanupWarning)
	}
	if snapshot.LastSystemNotice != codexHomeCleanupWarning {
		t.Fatalf("last system notice = %q, want %q", snapshot.LastSystemNotice, codexHomeCleanupWarning)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != TranscriptSystem || snapshot.Entries[0].Text != codexHomeCleanupWarning {
		t.Fatalf("entries = %#v, want one cleanup warning entry", snapshot.Entries)
	}
}

func TestCompactCodexStatusLabel(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "502 service unavailable",
			message: "codex stderr: failed to connect to websocket: HTTP error: 502 Bad Gateway, url: wss://chatgpt.com/backend-api/codex/responses",
			want:    "Codex service unavailable (HTTP 502)",
		},
		{
			name:    "timeout",
			message: "codex stderr: request failed with context deadline exceeded while calling https://chatgpt.com/backend-api/codex/responses",
			want:    codexTimeoutStatusLabel(),
		},
		{
			name:    "connection failure",
			message: "codex stderr: dial tcp 1.2.3.4:443: connect: connection refused while reaching https://chatgpt.com/backend-api/codex/responses",
			want:    codexConnectionFailedStatusLabel(),
		},
		{
			name:    "stderr stream",
			message: "codex stderr stream error: read |0: file already closed",
			want:    "Codex stderr stream failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactCodexStatusLabel(tt.message); got != tt.want {
				t.Fatalf("compactCodexStatusLabel() = %q, want %q", got, tt.want)
			}
		})
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

func TestSubmitInputReactivatesBlockedGoalBeforeStartingTurn(t *testing.T) {
	calls := []string{}

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		goal: &ThreadGoal{
			ThreadID:  "thread_456",
			Objective: "finish the branch",
			Status:    ThreadGoalStatusBlocked,
		},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch len(calls) {
			case 1:
				if method != "thread/goal/set" {
					t.Fatalf("call 1 method = %q, want thread/goal/set", method)
				}
				request, ok := params.(threadGoalSetParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalSetParams", params)
				}
				if request.Objective != "finish the branch" {
					t.Fatalf("objective = %q, want finish the branch", request.Objective)
				}
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"finish the branch","status":"blocked","tokensUsed":10,"timeUsedSeconds":2,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
			case 2:
				if method != "thread/goal/clear" {
					t.Fatalf("call 2 method = %q, want thread/goal/clear", method)
				}
				return json.RawMessage(`{"cleared":true}`), nil
			case 3:
				if method != "thread/goal/set" {
					t.Fatalf("call 3 method = %q, want thread/goal/set", method)
				}
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"finish the branch","status":"active","tokensUsed":0,"timeUsedSeconds":0,"createdAt":1773027020,"updatedAt":1773027020}}`), nil
			case 4:
				if method != "turn/start" {
					t.Fatalf("call 4 method = %q, want turn/start", method)
				}
				request, ok := params.(turnStartParams)
				if !ok {
					t.Fatalf("params = %#v, want turnStartParams", params)
				}
				if request.ThreadID != "thread_456" {
					t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
				}
				return json.RawMessage(`{"turn":{"id":"turn_resumed"}}`), nil
			default:
				t.Fatalf("unexpected rpc call %d: %s", len(calls), method)
				return nil, nil
			}
		},
	}

	if err := s.Submit("please keep going"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if strings.Join(calls, ",") != "thread/goal/set,thread/goal/clear,thread/goal/set,turn/start" {
		t.Fatalf("calls = %#v, want goal reset before turn start", calls)
	}

	snapshot := s.Snapshot()
	if snapshot.Goal == nil {
		t.Fatalf("snapshot goal = nil, want resumed goal")
	}
	if snapshot.Goal.Status != ThreadGoalStatusActive {
		t.Fatalf("goal status = %q, want active", snapshot.Goal.Status)
	}
	if snapshot.ActiveTurnID != "turn_resumed" {
		t.Fatalf("active turn id = %q, want turn_resumed", snapshot.ActiveTurnID)
	}
}

func TestSubmitInputStartsNewTurnWhenSteerFindsNoActiveTurn(t *testing.T) {
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
				return nil, errors.New("no active turn to steer")
			case 2:
				if method != "thread/read" {
					t.Fatalf("call 2 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[]}}`), nil
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

func TestSubmitInputStartsNewTurnWhenBusyStateIsStaleAndThreadReadShowsNoActiveTurn(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_old",
		busy:               true,
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: time.Now().Add(-2 * time.Minute),
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "thread/read" {
					t.Fatalf("call 1 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[]}}`), nil
			case 2:
				if method != "turn/start" {
					t.Fatalf("call 2 method = %q, want turn/start", method)
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
	if callCount != 2 {
		t.Fatalf("rpc call count = %d, want 2", callCount)
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

func TestSubmitInputPausesActiveGoalBeforeStartingManualTurn(t *testing.T) {
	calls := []string{}

	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread_456",
		activeTurnID: "turn_goal",
		busy:         true,
		entryIndex:   make(map[string]int),
		notify:       func() {},
		goal: &ThreadGoal{
			ThreadID:  "thread_456",
			Objective: "finish the branch",
			Status:    ThreadGoalStatusActive,
		},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch len(calls) {
			case 1:
				if method != "turn/interrupt" {
					t.Fatalf("call 1 method = %q, want turn/interrupt", method)
				}
				request, ok := params.(turnInterruptParams)
				if !ok {
					t.Fatalf("params = %#v, want turnInterruptParams", params)
				}
				if request.ThreadID != "thread_456" || request.TurnID != "turn_goal" {
					t.Fatalf("interrupt request = %#v, want thread_456/turn_goal", request)
				}
				return json.RawMessage(`{}`), nil
			case 2:
				if method != "thread/goal/set" {
					t.Fatalf("call 2 method = %q, want thread/goal/set", method)
				}
				request, ok := params.(threadGoalSetParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalSetParams", params)
				}
				if request.Status != ThreadGoalStatusPaused {
					t.Fatalf("goal status = %q, want paused", request.Status)
				}
				if request.Objective != "finish the branch" {
					t.Fatalf("goal objective = %q, want finish the branch", request.Objective)
				}
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"finish the branch","status":"paused","tokensUsed":20,"timeUsedSeconds":5,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
			case 3:
				if method != "thread/read" {
					t.Fatalf("call 3 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"},"turns":[]}}`), nil
			case 4:
				if method != "turn/start" {
					t.Fatalf("call 4 method = %q, want turn/start", method)
				}
				request, ok := params.(turnStartParams)
				if !ok {
					t.Fatalf("params = %#v, want turnStartParams", params)
				}
				if request.ThreadID != "thread_456" {
					t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
				}
				return json.RawMessage(`{"turn":{"id":"turn_manual"}}`), nil
			default:
				t.Fatalf("unexpected rpc call %d: %s", len(calls), method)
				return nil, nil
			}
		},
	}

	if err := s.Submit("answer this side question"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if strings.Join(calls, ",") != "turn/interrupt,thread/goal/set,thread/read,turn/start" {
		t.Fatalf("calls = %#v, want pause then manual turn start", calls)
	}

	snapshot := s.Snapshot()
	if snapshot.Goal == nil || snapshot.Goal.Status != ThreadGoalStatusPaused {
		t.Fatalf("goal = %#v, want paused goal", snapshot.Goal)
	}
	if snapshot.ActiveTurnID != "turn_manual" {
		t.Fatalf("active turn id = %q, want turn_manual", snapshot.ActiveTurnID)
	}
}

func TestSubmitWaitsForPlaywrightMCPToolsBeforeTurnStart(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath:           "/tmp/demo",
		threadID:              "thread_456",
		entryIndex:            make(map[string]int),
		notify:                func() {},
		playwrightMCPExpected: true,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "mcpServerStatus/list" {
					t.Fatalf("call 1 method = %q, want mcpServerStatus/list", method)
				}
				request, ok := params.(mcpServerStatusListParams)
				if !ok {
					t.Fatalf("call 1 params = %#v, want mcpServerStatusListParams", params)
				}
				if request.Detail != "toolsAndAuthOnly" {
					t.Fatalf("call 1 detail = %q, want toolsAndAuthOnly", request.Detail)
				}
				return json.RawMessage(`{"data":[{"name":"playwright","tools":{}}]}`), nil
			case 2:
				if method != "mcpServerStatus/list" {
					t.Fatalf("call 2 method = %q, want mcpServerStatus/list", method)
				}
				return json.RawMessage(`{"data":[{"name":"playwright","tools":{"browser_navigate":{"name":"browser_navigate","inputSchema":{}}}}]}`), nil
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

	if err := s.Submit("browse to example.com"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("rpc call count = %d, want 3", callCount)
	}
	if !s.playwrightMCPReady {
		t.Fatalf("playwrightMCPReady = false, want true")
	}

	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_fresh" {
		t.Fatalf("active turn id = %q, want turn_fresh", snapshot.ActiveTurnID)
	}
}

func TestEnsurePlaywrightMCPReadyTimesOutWithNotice(t *testing.T) {
	s := &appServerSession{
		projectPath:           "/tmp/demo",
		entryIndex:            make(map[string]int),
		notify:                func() {},
		playwrightMCPExpected: true,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "mcpServerStatus/list" {
				t.Fatalf("method = %q, want mcpServerStatus/list", method)
			}
			return json.RawMessage(`{"data":[]}`), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := s.ensurePlaywrightMCPReady(ctx); err != nil {
		t.Fatalf("ensurePlaywrightMCPReady() error = %v, want nil", err)
	}
	snapshot := s.Snapshot()
	if !strings.Contains(snapshot.LastSystemNotice, "Playwright tools are still starting") {
		t.Fatalf("last system notice = %q, want timeout notice", snapshot.LastSystemNotice)
	}
	if s.playwrightMCPReady {
		t.Fatalf("playwrightMCPReady = true, want false")
	}
}

func TestSubmitWaitsForRuntimeMCPToolsBeforeTurnStart(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		runtimeMCPExpected: true,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "mcpServerStatus/list" {
					t.Fatalf("call 1 method = %q, want mcpServerStatus/list", method)
				}
				request, ok := params.(mcpServerStatusListParams)
				if !ok {
					t.Fatalf("call 1 params = %#v, want mcpServerStatusListParams", params)
				}
				if request.Detail != "toolsAndAuthOnly" {
					t.Fatalf("call 1 detail = %q, want toolsAndAuthOnly", request.Detail)
				}
				return json.RawMessage(`{"data":[{"name":"lcr_runtime","tools":{}}]}`), nil
			case 2:
				if method != "mcpServerStatus/list" {
					t.Fatalf("call 2 method = %q, want mcpServerStatus/list", method)
				}
				return json.RawMessage(`{"data":[{"name":"lcr_runtime","tools":{"start_process":{"name":"start_process","inputSchema":{}}}}]}`), nil
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

	if err := s.Submit("start the app"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("rpc call count = %d, want 3", callCount)
	}
	if !s.runtimeMCPReady {
		t.Fatalf("runtimeMCPReady = false, want true")
	}
	snapshot := s.Snapshot()
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.ActiveTurnID != "turn_fresh" {
		t.Fatalf("active turn id = %q, want turn_fresh", snapshot.ActiveTurnID)
	}
	if strings.Contains(snapshot.Transcript, "Little Control Room runtime context") {
		t.Fatalf("transcript should not include injected runtime context:\n%s", snapshot.Transcript)
	}
}

func TestEnsureRuntimeMCPReadyTimesOutWithNotice(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		runtimeMCPExpected: true,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "mcpServerStatus/list" {
				t.Fatalf("method = %q, want mcpServerStatus/list", method)
			}
			return json.RawMessage(`{"data":[]}`), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := s.ensureRuntimeMCPReady(ctx); err != nil {
		t.Fatalf("ensureRuntimeMCPReady() error = %v, want nil", err)
	}
	snapshot := s.Snapshot()
	if !strings.Contains(snapshot.LastSystemNotice, "Runtime tools are still starting") {
		t.Fatalf("last system notice = %q, want timeout notice", snapshot.LastSystemNotice)
	}
	if s.runtimeMCPReady {
		t.Fatalf("runtimeMCPReady = true, want false")
	}
}

func TestManagedPlaywrightMCPReadyInTrustedProject(t *testing.T) {
	if os.Getenv("LCROOM_EMBEDDED_CX_BROWSER_SMOKE") == "" {
		t.Skip("set LCROOM_EMBEDDED_CX_BROWSER_SMOKE=1 to run the real embedded Codex Playwright smoke test")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not available")
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	projectPath, err := os.MkdirTemp(repoRoot, "tmp-cx-browser-smoke-*")
	if err != nil {
		t.Fatalf("mkdir temp trusted project: %v", err)
	}
	defer os.RemoveAll(projectPath)
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("smoke\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	helperBinary := filepath.Join(t.TempDir(), "lcroom")
	buildCmd := exec.Command("go", "build", "-o", helperBinary, "./cmd/lcroom")
	buildCmd.Dir = repoRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build lcroom helper binary: %v\n%s", err, output)
	}

	dataDir := t.TempDir()
	sessionAny, err := newAppServerSession(LaunchRequest{
		Provider:          ProviderCodex,
		ProjectPath:       projectPath,
		ForceNew:          true,
		PlaywrightPolicy:  browserctl.DefaultPolicy(),
		AppDataDir:        dataDir,
		CodexHome:         filepath.Join(os.Getenv("HOME"), ".codex"),
		CLIExecutablePath: helperBinary,
	}, func() {})
	if err != nil {
		t.Fatalf("newAppServerSession() error = %v", err)
	}
	defer sessionAny.Close()

	session, ok := sessionAny.(*appServerSession)
	if !ok {
		t.Fatalf("session type = %T, want *appServerSession", sessionAny)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := session.ensurePlaywrightMCPReady(ctx); err != nil {
		t.Fatalf("ensurePlaywrightMCPReady() error = %v", err)
	}
	if !session.playwrightMCPReady {
		snapshot := session.Snapshot()
		t.Fatalf("playwrightMCPReady = false, want true (status=%q notice=%q startup=%#v)", snapshot.Status, snapshot.LastSystemNotice, session.mcpServerStartup)
	}
}

func TestSubmitInputRejectsPromptWhileCompacting(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		compacting:  true,
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			t.Fatalf("unexpected rpc call during compaction: %s (%#v)", method, params)
			return nil, nil
		},
	}

	err := s.Submit("follow up")
	if err == nil {
		t.Fatalf("Submit() error = nil, want compaction guard")
	}
	if !strings.Contains(err.Error(), "compaction is in progress") {
		t.Fatalf("Submit() error = %v, want compaction guidance", err)
	}
	if callCount != 0 {
		t.Fatalf("rpc call count = %d, want 0", callCount)
	}

	snapshot := s.Snapshot()
	if len(snapshot.Entries) != 0 {
		t.Fatalf("entries = %#v, want no optimistic transcript append", snapshot.Entries)
	}
}

func TestCompactWaitsForCompletionAndHydratesCompactionEntry(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "thread/compact/start" {
					t.Fatalf("call 1 method = %q, want thread/compact/start", method)
				}
				request, ok := params.(threadCompactStartParams)
				if !ok {
					t.Fatalf("call 1 params = %#v, want threadCompactStartParams", params)
				}
				if request.ThreadID != "thread_456" {
					t.Fatalf("call 1 thread id = %q, want thread_456", request.ThreadID)
				}
				return json.RawMessage(`{}`), nil
			case 2:
				if method != "thread/read" {
					t.Fatalf("call 2 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[{"id":"turn_compact","status":"inProgress","items":[{"id":"item_compact","type":"contextCompaction"}]}]}}`), nil
			case 3:
				if method != "thread/read" {
					t.Fatalf("call 3 method = %q, want thread/read", method)
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"},"turns":[{"id":"turn_compact","status":"completed","items":[{"id":"item_compact","type":"contextCompaction"}]}]}}`), nil
			default:
				t.Fatalf("unexpected rpc call %d: %s", callCount, method)
				return nil, nil
			}
		},
	}

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if callCount != 3 {
		t.Fatalf("rpc call count = %d, want 3", callCount)
	}

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseIdle {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseIdle)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Conversation history compacted" {
		t.Fatalf("status = %q, want conversation compacted", snapshot.Status)
	}
	if snapshot.LastSystemNotice != "Conversation history compacted" {
		t.Fatalf("last system notice = %q, want conversation compacted", snapshot.LastSystemNotice)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %#v, want one compaction entry", snapshot.Entries)
	}
	if snapshot.Entries[0].Text != "Conversation history compacted" {
		t.Fatalf("entry text = %q, want compaction transcript", snapshot.Entries[0].Text)
	}
}

func TestCompactClearsCompactingStateWhenThreadReadFails(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			callCount++
			switch callCount {
			case 1:
				if method != "thread/compact/start" {
					t.Fatalf("call 1 method = %q, want thread/compact/start", method)
				}
				return json.RawMessage(`{}`), nil
			case 2:
				if method != "thread/read" {
					t.Fatalf("call 2 method = %q, want thread/read", method)
				}
				return nil, context.DeadlineExceeded
			default:
				t.Fatalf("unexpected rpc call %d: %s", callCount, method)
				return nil, nil
			}
		},
	}

	err := s.Compact()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Compact() error = %v, want context deadline exceeded", err)
	}

	snapshot := s.Snapshot()
	if snapshot.Phase == SessionPhaseReconciling {
		t.Fatalf("phase = %q, want compaction state cleared after read failure", snapshot.Phase)
	}
	if snapshot.Status != "Codex error" {
		t.Fatalf("status = %q, want Codex error", snapshot.Status)
	}
	if !strings.Contains(snapshot.LastError, "context deadline exceeded") {
		t.Fatalf("last error = %q, want deadline error", snapshot.LastError)
	}
}

func TestReviewStartsUncommittedChangesReview(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "review/start" {
				t.Fatalf("method = %q, want review/start", method)
			}
			request, ok := params.(reviewStartParams)
			if !ok {
				t.Fatalf("params = %#v, want reviewStartParams", params)
			}
			if request.ThreadID != "thread_456" {
				t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
			}
			if request.Delivery != "inline" {
				t.Fatalf("delivery = %q, want inline", request.Delivery)
			}
			if request.Target.Type != "uncommittedChanges" {
				t.Fatalf("target type = %q, want uncommittedChanges", request.Target.Type)
			}
			return json.RawMessage(`{"turn":{"id":"turn_review","status":"inProgress","items":[]},"reviewThreadId":"thread_456"}`), nil
		},
	}

	if err := s.Review(); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseRunning {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseRunning)
	}
	if snapshot.ActiveTurnID != "turn_review" {
		t.Fatalf("active turn id = %q, want turn_review", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Codex is reviewing uncommitted changes..." {
		t.Fatalf("status = %q, want review progress", snapshot.Status)
	}
}

func TestSetGoalCallsThreadGoalSetAndStoresGoal(t *testing.T) {
	callCount := 0
	tokenBudget := int64(5000)

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/goal/set" {
				t.Fatalf("method = %q, want thread/goal/set", method)
			}
			request, ok := params.(threadGoalSetParams)
			if !ok {
				t.Fatalf("params = %#v, want threadGoalSetParams", params)
			}
			if request.ThreadID != "thread_456" {
				t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
			}
			if request.Objective != "ship the goal command" {
				t.Fatalf("objective = %q, want ship the goal command", request.Objective)
			}
			if request.TokenBudget == nil || *request.TokenBudget != 5000 {
				t.Fatalf("token budget = %v, want 5000", request.TokenBudget)
			}
			return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"ship the goal command","status":"active","tokenBudget":5000,"tokensUsed":12,"timeUsedSeconds":3,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
		},
	}

	if err := s.SetGoal(" ship the goal command ", &tokenBudget); err != nil {
		t.Fatalf("SetGoal() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}

	snapshot := s.Snapshot()
	if snapshot.Goal == nil {
		t.Fatalf("snapshot goal = nil, want stored goal")
	}
	if snapshot.Goal.Objective != "ship the goal command" {
		t.Fatalf("goal objective = %q, want ship the goal command", snapshot.Goal.Objective)
	}
	if snapshot.Goal.TokenBudget == nil || *snapshot.Goal.TokenBudget != 5000 {
		t.Fatalf("goal token budget = %v, want 5000", snapshot.Goal.TokenBudget)
	}
	if snapshot.Goal.TokensUsed != 12 {
		t.Fatalf("goal tokens used = %d, want 12", snapshot.Goal.TokensUsed)
	}
	if snapshot.Status != "Set embedded Codex goal" {
		t.Fatalf("status = %q, want Set embedded Codex goal", snapshot.Status)
	}
	if len(snapshot.Entries) != 1 || !strings.Contains(snapshot.Entries[0].Text, "objective: ship the goal command") {
		t.Fatalf("entries = %#v, want goal transcript entry", snapshot.Entries)
	}
}

func TestResumeGoalCallsThreadGoalSetWithActiveStatus(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		goal: &ThreadGoal{
			ThreadID:  "thread_456",
			Objective: "finish the branch",
			Status:    ThreadGoalStatusPaused,
		},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/goal/set" {
				t.Fatalf("method = %q, want thread/goal/set", method)
			}
			request, ok := params.(threadGoalSetParams)
			if !ok {
				t.Fatalf("params = %#v, want threadGoalSetParams", params)
			}
			if request.ThreadID != "thread_456" {
				t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
			}
			if request.Status != ThreadGoalStatusActive {
				t.Fatalf("status = %q, want active", request.Status)
			}
			if request.Objective != "finish the branch" {
				t.Fatalf("objective = %q, want finish the branch", request.Objective)
			}
			return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"finish the branch","status":"active","tokensUsed":20,"timeUsedSeconds":5,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
		},
	}

	if err := s.ResumeGoal(); err != nil {
		t.Fatalf("ResumeGoal() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}

	snapshot := s.Snapshot()
	if snapshot.Goal == nil || snapshot.Goal.Status != ThreadGoalStatusActive {
		t.Fatalf("goal = %#v, want active goal", snapshot.Goal)
	}
	if snapshot.Status != "Embedded Codex goal resumed" {
		t.Fatalf("status = %q, want Embedded Codex goal resumed", snapshot.Status)
	}
}

func TestSetGoalClearsCompletedGoalAndRetriesWhenCodexReturnsStaleGoal(t *testing.T) {
	calls := []string{}

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch len(calls) {
			case 1:
				if method != "thread/goal/set" {
					t.Fatalf("first method = %q, want thread/goal/set", method)
				}
				request, ok := params.(threadGoalSetParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalSetParams", params)
				}
				if request.Objective != "ship the next goal" {
					t.Fatalf("objective = %q, want ship the next goal", request.Objective)
				}
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"old goal","status":"complete","tokensUsed":324582,"timeUsedSeconds":733,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
			case 2:
				if method != "thread/goal/clear" {
					t.Fatalf("second method = %q, want thread/goal/clear", method)
				}
				request, ok := params.(threadGoalClearParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalClearParams", params)
				}
				if request.ThreadID != "thread_456" {
					t.Fatalf("clear thread id = %q, want thread_456", request.ThreadID)
				}
				return json.RawMessage(`{"cleared":true}`), nil
			case 3:
				if method != "thread/goal/set" {
					t.Fatalf("third method = %q, want thread/goal/set", method)
				}
				request, ok := params.(threadGoalSetParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalSetParams", params)
				}
				if request.Objective != "ship the next goal" {
					t.Fatalf("retry objective = %q, want ship the next goal", request.Objective)
				}
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"ship the next goal","status":"active","tokensUsed":0,"timeUsedSeconds":0,"createdAt":1773027020,"updatedAt":1773027020}}`), nil
			default:
				t.Fatalf("unexpected RPC call %d: %s", len(calls), method)
				return nil, nil
			}
		},
	}

	if err := s.SetGoal("ship the next goal", nil); err != nil {
		t.Fatalf("SetGoal() error = %v", err)
	}
	if strings.Join(calls, ",") != "thread/goal/set,thread/goal/clear,thread/goal/set" {
		t.Fatalf("calls = %#v, want set, clear, set", calls)
	}
	snapshot := s.Snapshot()
	if snapshot.Goal == nil {
		t.Fatalf("snapshot goal = nil, want stored goal")
	}
	if snapshot.Goal.Objective != "ship the next goal" {
		t.Fatalf("goal objective = %q, want ship the next goal", snapshot.Goal.Objective)
	}
	if snapshot.Goal.Status != ThreadGoalStatusActive {
		t.Fatalf("goal status = %q, want active", snapshot.Goal.Status)
	}
	if len(snapshot.Entries) != 1 || !strings.Contains(snapshot.Entries[0].Text, "objective: ship the next goal") || strings.Contains(snapshot.Entries[0].Text, "old goal") {
		t.Fatalf("entries = %#v, want only new goal transcript entry", snapshot.Entries)
	}
}

func TestSetGoalErrorsWhenCodexKeepsReturningStaleGoal(t *testing.T) {
	callCount := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			callCount++
			switch method {
			case "thread/goal/set":
				return json.RawMessage(`{"goal":{"threadId":"thread_456","objective":"old goal","status":"complete","tokensUsed":324582,"timeUsedSeconds":733,"createdAt":1773027000,"updatedAt":1773027010}}`), nil
			case "thread/goal/clear":
				return json.RawMessage(`{"cleared":true}`), nil
			default:
				t.Fatalf("unexpected method = %q", method)
				return nil, nil
			}
		},
	}

	err := s.SetGoal("ship the next goal", nil)
	if err == nil {
		t.Fatalf("SetGoal() error = nil, want stale-goal error")
	}
	if !strings.Contains(err.Error(), "embedded Codex goal did not update") {
		t.Fatalf("SetGoal() error = %v, want stale-goal message", err)
	}
	if callCount != 3 {
		t.Fatalf("call count = %d, want 3", callCount)
	}
	snapshot := s.Snapshot()
	if snapshot.Goal != nil {
		t.Fatalf("snapshot goal = %#v, want nil after failed stale set", snapshot.Goal)
	}
}

func TestClearGoalClearsBeforeInterruptingActiveTurn(t *testing.T) {
	calls := []string{}

	s := &appServerSession{
		projectPath:  "/tmp/demo",
		threadID:     "thread_456",
		activeTurnID: "turn_789",
		busy:         true,
		entryIndex:   make(map[string]int),
		notify:       func() {},
		goal:         &ThreadGoal{ThreadID: "thread_456", Objective: "stay paused", Status: ThreadGoalStatusActive},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch method {
			case "turn/interrupt":
				request, ok := params.(turnInterruptParams)
				if !ok {
					t.Fatalf("params = %#v, want turnInterruptParams", params)
				}
				if request.ThreadID != "thread_456" || request.TurnID != "turn_789" {
					t.Fatalf("interrupt request = %#v, want thread_456/turn_789", request)
				}
				return json.RawMessage(`{}`), nil
			case "thread/read":
				return json.RawMessage(`{"thread":{"id":"thread_456","turns":[{"id":"turn_789","status":"completed"}]}}`), nil
			case "thread/goal/clear":
				request, ok := params.(threadGoalClearParams)
				if !ok {
					t.Fatalf("params = %#v, want threadGoalClearParams", params)
				}
				if request.ThreadID != "thread_456" {
					t.Fatalf("clear thread id = %q, want thread_456", request.ThreadID)
				}
				return json.RawMessage(`{"cleared":true}`), nil
			default:
				t.Fatalf("unexpected method = %q", method)
				return nil, nil
			}
		},
	}

	if err := s.ClearGoal(); err != nil {
		t.Fatalf("ClearGoal() error = %v", err)
	}
	if strings.Join(calls, ",") != "thread/goal/clear,turn/interrupt,thread/read" {
		t.Fatalf("calls = %#v, want clear, interrupt, then wait", calls)
	}
	snapshot := s.Snapshot()
	if snapshot.Goal != nil {
		t.Fatalf("snapshot goal = %#v, want nil", snapshot.Goal)
	}
	if snapshot.Status != "Embedded Codex goal stopped" {
		t.Fatalf("status = %q, want goal stopped", snapshot.Status)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Text != "Embedded Codex goal stopped" {
		t.Fatalf("entries = %#v, want stopped transcript entry", snapshot.Entries)
	}
}

func TestClearGoalRecoversActiveTurnBeforeClearing(t *testing.T) {
	calls := []string{}
	readCalls := 0

	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		busy:        true,
		entryIndex:  make(map[string]int),
		notify:      func() {},
		goal:        &ThreadGoal{ThreadID: "thread_456", Objective: "stay paused", Status: ThreadGoalStatusActive},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch method {
			case "thread/read":
				readCalls++
				if readCalls == 1 {
					return json.RawMessage(`{"thread":{"id":"thread_456","turns":[{"id":"turn_recovered","status":"inProgress"}]}}`), nil
				}
				return json.RawMessage(`{"thread":{"id":"thread_456","turns":[{"id":"turn_recovered","status":"completed"}]}}`), nil
			case "turn/interrupt":
				request, ok := params.(turnInterruptParams)
				if !ok {
					t.Fatalf("params = %#v, want turnInterruptParams", params)
				}
				if request.ThreadID != "thread_456" || request.TurnID != "turn_recovered" {
					t.Fatalf("interrupt request = %#v, want thread_456/turn_recovered", request)
				}
				return json.RawMessage(`{}`), nil
			case "thread/goal/clear":
				return json.RawMessage(`{"cleared":true}`), nil
			default:
				t.Fatalf("unexpected method = %q", method)
				return nil, nil
			}
		},
	}

	if err := s.ClearGoal(); err != nil {
		t.Fatalf("ClearGoal() error = %v", err)
	}
	if strings.Join(calls, ",") != "thread/read,thread/goal/clear,turn/interrupt,thread/read" {
		t.Fatalf("calls = %#v, want recover, clear, interrupt, wait", calls)
	}
	snapshot := s.Snapshot()
	if snapshot.Goal != nil {
		t.Fatalf("snapshot goal = %#v, want nil", snapshot.Goal)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty after interrupt wait", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Embedded Codex goal stopped" {
		t.Fatalf("status = %q, want goal stopped", snapshot.Status)
	}
}

func TestHandleGoalNotificationsUpdateSnapshot(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("thread/goal/updated", json.RawMessage(`{"threadId":"thread_456","goal":{"threadId":"thread_456","objective":"finish the branch","status":"budgetLimited","tokenBudget":100,"tokensUsed":101,"timeUsedSeconds":9,"createdAt":1773027000,"updatedAt":1773027010}}`))
	snapshot := s.Snapshot()
	if snapshot.Goal == nil {
		t.Fatalf("snapshot goal = nil, want updated goal")
	}
	if snapshot.Goal.Status != ThreadGoalStatusBudgetLimited {
		t.Fatalf("goal status = %q, want %q", snapshot.Goal.Status, ThreadGoalStatusBudgetLimited)
	}
	if snapshot.LastSystemNotice != "Embedded Codex goal reached its token budget" {
		t.Fatalf("last system notice = %q, want budget notice", snapshot.LastSystemNotice)
	}

	s.handleNotification("thread/goal/cleared", json.RawMessage(`{"threadId":"thread_456"}`))
	snapshot = s.Snapshot()
	if snapshot.Goal != nil {
		t.Fatalf("snapshot goal = %#v, want nil after clear", snapshot.Goal)
	}
	if snapshot.LastSystemNotice != "Embedded Codex goal cleared" {
		t.Fatalf("last system notice = %q, want clear notice", snapshot.LastSystemNotice)
	}
}

func TestHydrateCompactingThreadKeepsSessionWritableAndShowsProgress(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		threadID:    "thread_456",
		compacting:  true,
		status:      "Compacting conversation history...",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	thread := resumedThread{
		ID: "thread_456",
		Status: resumedThreadStatus{
			Type: "active",
		},
		Turns: []resumedTurn{{
			ID:     "turn_compact",
			Status: "inProgress",
			Items: []map[string]json.RawMessage{{
				"id":   json.RawMessage(`"item_compact"`),
				"type": json.RawMessage(`"contextCompaction"`),
			}},
		}},
	}

	s.mu.Lock()
	s.hydrateResumedThreadLocked(thread)
	s.syncThreadStatusLocked("thread_456", effectiveThreadStatus(thread), true)
	s.mu.Unlock()

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseReconciling {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseReconciling)
	}
	if !snapshot.Busy {
		t.Fatalf("busy = false, want true")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false during compaction")
	}
	if snapshot.Status != "Compacting conversation history..." {
		t.Fatalf("status = %q, want compaction progress", snapshot.Status)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("entries = %#v, want one compaction progress entry", snapshot.Entries)
	}
	if snapshot.Entries[0].Text != "Compacting conversation history..." {
		t.Fatalf("entry text = %q, want compaction progress transcript", snapshot.Entries[0].Text)
	}
}

func TestReconcileBusyStateClearsBusyWithLightweightIdleStatus(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_old",
		busy:               true,
		status:             "Codex is working...",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: time.Now().Add(-2 * time.Minute),
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			request, ok := params.(threadReadParams)
			if !ok {
				t.Fatalf("params = %#v, want threadReadParams", params)
			}
			if request.IncludeTurns {
				t.Fatalf("includeTurns = true, want false for lightweight busy reconciliation")
			}
			return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"}}}`), nil
		},
	}

	if err := s.ReconcileBusyState(); err != nil {
		t.Fatalf("ReconcileBusyState() error = %v", err)
	}

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false after reconcile sees no active turn")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Recovered idle after status check" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Recovered idle after status check")
	}
}

func TestReconcileBusyStateMarksSessionStalledAfterRepeatedHealthCheckFailures(t *testing.T) {
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_old",
		busy:               true,
		status:             "Codex is working...",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: time.Now().Add(-2 * time.Minute),
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			request, ok := params.(threadReadParams)
			if !ok {
				t.Fatalf("params = %#v, want threadReadParams", params)
			}
			if request.IncludeTurns {
				t.Fatalf("includeTurns = true, want false for lightweight busy reconciliation")
			}
			return nil, errors.New("context deadline exceeded")
		},
	}

	if err := s.ReconcileBusyState(); err == nil {
		t.Fatalf("first ReconcileBusyState() error = nil, want timeout")
	}
	first := s.Snapshot()
	if first.Phase != SessionPhaseRunning {
		t.Fatalf("phase after first failure = %q, want %q before stall threshold", first.Phase, SessionPhaseRunning)
	}
	if first.Status == codexReconnectSuggestion {
		t.Fatalf("status after first failure = %q, should not promote reconnect suggestion yet", first.Status)
	}

	if err := s.ReconcileBusyState(); err == nil {
		t.Fatalf("second ReconcileBusyState() error = nil, want timeout")
	}
	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseStalled {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseStalled)
	}
	if snapshot.Status != codexReconnectSuggestion {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexReconnectSuggestion)
	}
	if snapshot.LastSystemNotice != codexReconnectSuggestion {
		t.Fatalf("last system notice = %q, want reconnect suggestion", snapshot.LastSystemNotice)
	}
	if !strings.Contains(snapshot.LastError, "context deadline exceeded") {
		t.Fatalf("last error = %q, want timeout", snapshot.LastError)
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[len(snapshot.Entries)-1].Text != codexReconnectSuggestion {
		t.Fatalf("entries = %#v, want reconnect guidance entry appended once", snapshot.Entries)
	}
}

func TestReconcileBusyStateMarksSessionStalledWhenActiveTurnStaysUnresponsive(t *testing.T) {
	staleBusy := time.Now().Add(-(busyStateUnresponsiveFor + time.Minute)).Round(0)
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_old",
		busy:               true,
		status:             "Codex is working...",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: staleBusy,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			request, ok := params.(threadReadParams)
			if !ok {
				t.Fatalf("params = %#v, want threadReadParams", params)
			}
			if request.IncludeTurns {
				t.Fatalf("includeTurns = true, want false for lightweight busy reconciliation")
			}
			return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[{"id":"turn_old","status":"inProgress"}]}}`), nil
		},
	}

	if err := s.ReconcileBusyState(); err != nil {
		t.Fatalf("first ReconcileBusyState() error = %v, want nil", err)
	}
	first := s.Snapshot()
	if first.Phase != SessionPhaseRunning {
		t.Fatalf("phase after first stale-active check = %q, want %q before stall threshold", first.Phase, SessionPhaseRunning)
	}
	if first.Status == codexReconnectSuggestion {
		t.Fatalf("status after first stale-active check = %q, should not promote reconnect suggestion yet", first.Status)
	}
	if !first.LastBusyActivityAt.Equal(staleBusy) {
		t.Fatalf("last busy activity after first stale-active check = %v, want %v", first.LastBusyActivityAt, staleBusy)
	}

	if err := s.ReconcileBusyState(); err != nil {
		t.Fatalf("second ReconcileBusyState() error = %v, want nil", err)
	}
	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseStalled {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseStalled)
	}
	if snapshot.Status != codexReconnectSuggestion {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexReconnectSuggestion)
	}
	if snapshot.LastSystemNotice != codexReconnectSuggestion {
		t.Fatalf("last system notice = %q, want reconnect suggestion", snapshot.LastSystemNotice)
	}
	if !snapshot.LastBusyActivityAt.Equal(staleBusy) {
		t.Fatalf("last busy activity = %v, want %v", snapshot.LastBusyActivityAt, staleBusy)
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[len(snapshot.Entries)-1].Text != codexReconnectSuggestion {
		t.Fatalf("entries = %#v, want reconnect guidance entry appended once", snapshot.Entries)
	}
}

func TestSnapshotMarksLongSilentBusySessionStalled(t *testing.T) {
	staleBusy := time.Now().Add(-(busyStateHardStallAfter + time.Minute)).Round(0)
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_old",
		busy:               true,
		reconciling:        true,
		status:             "Codex is working...",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: staleBusy,
	}

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseStalled {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseStalled)
	}
	if snapshot.Status != codexReconnectSuggestion {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexReconnectSuggestion)
	}
	if snapshot.LastSystemNotice != codexReconnectSuggestion {
		t.Fatalf("last system notice = %q, want reconnect suggestion", snapshot.LastSystemNotice)
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[len(snapshot.Entries)-1].Text != codexReconnectSuggestion {
		t.Fatalf("entries = %#v, want reconnect guidance appended after hard stall", snapshot.Entries)
	}
}

func TestSnapshotMarksSilentCompactionStalledBeforeGenericBusyTimeout(t *testing.T) {
	staleBusy := time.Now().Add(-(compactionWaitTimeout + time.Second)).Round(0)
	s := &appServerSession{
		projectPath:             "/tmp/demo",
		threadID:                "thread_456",
		activeTurnID:            "turn_compact",
		busy:                    true,
		compacting:              true,
		contextCompactionActive: true,
		status:                  "Compacting conversation history...",
		entryIndex:              make(map[string]int),
		notify:                  func() {},
		lastBusyActivityAt:      staleBusy,
	}

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseStalled {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseStalled)
	}
	if snapshot.Status != codexCompactionStuckSuggestion {
		t.Fatalf("status = %q, want compaction stuck suggestion", snapshot.Status)
	}
	if snapshot.LastSystemNotice != codexCompactionStuckSuggestion {
		t.Fatalf("last system notice = %q, want compaction stuck suggestion", snapshot.LastSystemNotice)
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[len(snapshot.Entries)-1].Text != codexCompactionStuckSuggestion {
		t.Fatalf("entries = %#v, want compaction stuck guidance appended", snapshot.Entries)
	}
}

func TestThreadCompactedNotificationClearsAutomaticCompactionBusyState(t *testing.T) {
	s := &appServerSession{
		projectPath:             "/tmp/demo",
		threadID:                "thread_456",
		activeTurnID:            "turn_compact",
		activeItems:             map[string]struct{}{"item_compact": {}},
		activeCompactionItems:   map[string]struct{}{"item_compact": {}},
		busy:                    true,
		contextCompactionActive: true,
		status:                  "Compacting conversation history...",
		entryIndex:              make(map[string]int),
		notify:                  func() {},
		lastBusyActivityAt:      time.Now().Add(-time.Minute),
	}

	s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"thread_456"}`))

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseIdle {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseIdle)
	}
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Conversation history compacted" {
		t.Fatalf("status = %q, want conversation compacted", snapshot.Status)
	}
	if snapshot.LastSystemNotice != "Conversation history compacted" {
		t.Fatalf("last system notice = %q, want conversation compacted", snapshot.LastSystemNotice)
	}
}

func TestSubmitInputRejectsSteerWhenRecoveredTurnLooksStuck(t *testing.T) {
	staleBusy := time.Now().Add(-(busyStateUnresponsiveFor + time.Minute)).Round(0)
	callCount := 0
	s := &appServerSession{
		projectPath:        "/tmp/demo",
		threadID:           "thread_456",
		activeTurnID:       "turn_live",
		busy:               true,
		status:             "Codex is working...",
		entryIndex:         make(map[string]int),
		notify:             func() {},
		lastBusyActivityAt: staleBusy,
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"active"},"turns":[{"id":"turn_live","status":"inProgress"}]}}`), nil
		},
	}

	err := s.Submit("follow up")
	if !isBusyTurnLikelyStuckError(err) {
		t.Fatalf("Submit() error = %v, want busy-turn-stuck guidance", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want only thread/read when steer is rejected", callCount)
	}

	snapshot := s.Snapshot()
	if snapshot.Phase != SessionPhaseStalled {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, SessionPhaseStalled)
	}
	if snapshot.Status != codexReconnectSuggestion {
		t.Fatalf("status = %q, want %q", snapshot.Status, codexReconnectSuggestion)
	}
	if !snapshot.LastBusyActivityAt.Equal(staleBusy) {
		t.Fatalf("last busy activity = %v, want %v", snapshot.LastBusyActivityAt, staleBusy)
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[len(snapshot.Entries)-1].Text != codexReconnectSuggestion {
		t.Fatalf("entries = %#v, want reconnect guidance appended after the user tries to steer a stuck turn", snapshot.Entries)
	}
}

func TestEnsureFreshThreadRejectsRetainedHistory(t *testing.T) {
	callCount := 0
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			request, ok := params.(threadReadParams)
			if !ok {
				t.Fatalf("params = %#v, want threadReadParams", params)
			}
			if request.ThreadID != "thread_456" {
				t.Fatalf("thread id = %q, want thread_456", request.ThreadID)
			}
			return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"},"turns":[{"id":"turn_old","status":"completed","items":[{"id":"item_user","type":"userMessage"}]}]}}`), nil
		},
	}

	err := s.ensureFreshThread(context.Background(), "thread_456")
	if err == nil {
		t.Fatalf("ensureFreshThread() error = nil, want ForceNewSessionReusedError")
	}
	var reusedErr *ForceNewSessionReusedError
	if !errors.As(err, &reusedErr) {
		t.Fatalf("ensureFreshThread() error = %v, want ForceNewSessionReusedError", err)
	}
	if reusedErr.ThreadID != "thread_456" {
		t.Fatalf("reused thread id = %q, want thread_456", reusedErr.ThreadID)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}
}

func TestEnsureFreshThreadAcceptsEmptyThread(t *testing.T) {
	callCount := 0
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			return json.RawMessage(`{"thread":{"id":"thread_456","status":{"type":"idle"},"turns":[]}}`), nil
		},
	}

	if err := s.ensureFreshThread(context.Background(), "thread_456"); err != nil {
		t.Fatalf("ensureFreshThread() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}
}

func TestEnsureFreshThreadAcceptsUnmaterializedFreshThread(t *testing.T) {
	callCount := 0
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		rpcCallHook: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			callCount++
			if method != "thread/read" {
				t.Fatalf("method = %q, want thread/read", method)
			}
			return nil, errors.New("thread thread_456 is not materialized yet; includeTurns is unavailable before first user message")
		},
	}

	if err := s.ensureFreshThread(context.Background(), "thread_456"); err != nil {
		t.Fatalf("ensureFreshThread() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("rpc call count = %d, want 1", callCount)
	}
}

func TestTurnAbortedClearsBusyLikeInterruptedCompletion(t *testing.T) {
	s := &appServerSession{
		projectPath: "/tmp/demo",
		entryIndex:  make(map[string]int),
		notify:      func() {},
	}

	s.handleNotification("turn/started", json.RawMessage(`{"threadId":"thread_456","turn":{"id":"turn_live","status":"inProgress"}}`))
	s.handleNotification("turn/aborted", json.RawMessage(`{"threadId":"thread_456","turnId":"turn_live","reason":"interrupted"}`))

	snapshot := s.Snapshot()
	if snapshot.Busy {
		t.Fatalf("busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("busy external = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("active turn id = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Turn interrupted" {
		t.Fatalf("status = %q, want %q", snapshot.Status, "Turn interrupted")
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
