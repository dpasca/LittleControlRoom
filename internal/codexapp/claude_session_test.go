package codexapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexcli"
)

type recordingWriteCloser struct {
	writes []string
	closed bool
}

func (r *recordingWriteCloser) Write(p []byte) (int, error) {
	r.writes = append(r.writes, string(p))
	return len(p), nil
}

func (r *recordingWriteCloser) Close() error {
	r.closed = true
	return nil
}

func TestClaudePermissionModeForPreset(t *testing.T) {
	tests := []struct {
		preset     codexcli.Preset
		wantMode   string
		wantNotice string
	}{
		{preset: codexcli.PresetYolo, wantMode: "bypassPermissions", wantNotice: claudeYoloPresetMappingNotice},
		{preset: codexcli.PresetFullAuto, wantMode: "acceptEdits", wantNotice: claudeSafePresetMappingNotice},
		{preset: codexcli.PresetSafe, wantMode: "acceptEdits", wantNotice: claudeSafePresetMappingNotice},
	}

	for _, tt := range tests {
		gotMode, gotNotice := claudePermissionModeForPreset(tt.preset)
		if gotMode != tt.wantMode {
			t.Fatalf("claudePermissionModeForPreset(%q) mode = %q, want %q", tt.preset, gotMode, tt.wantMode)
		}
		if gotNotice != tt.wantNotice {
			t.Fatalf("claudePermissionModeForPreset(%q) notice = %q, want %q", tt.preset, gotNotice, tt.wantNotice)
		}
	}
}

func TestClaudeStdoutLineBuildsToolAndCommandEntries(t *testing.T) {
	session := &claudeCodeSession{
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"assistant","session_id":"ses-demo","effort":"high","message":{"id":"msg_1","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"npm test"}}]}}`)
	if len(session.entries) != 1 {
		t.Fatalf("entry count after tool_use = %d, want 1", len(session.entries))
	}
	if session.entries[0].Kind != TranscriptTool {
		t.Fatalf("tool_use entry kind = %q, want %q", session.entries[0].Kind, TranscriptTool)
	}
	if session.entries[0].Text != "Bash: npm test" {
		t.Fatalf("tool_use entry text = %q, want Bash summary", session.entries[0].Text)
	}
	if session.entries[0].ToolName != "Bash" || session.entries[0].ToolPath != "" {
		t.Fatalf("tool_use entry metadata = %#v, want Bash without a file path", session.entries[0])
	}
	if got := session.Snapshot().ReasoningEffort; got != "high" {
		t.Fatalf("reasoning effort after assistant event = %q, want high", got)
	}

	session.handleClaudeStdoutLine(`{"type":"user","session_id":"ses-demo","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"tests passed"}]}}`)
	if len(session.entries) != 2 {
		t.Fatalf("entry count after tool_result = %d, want 2", len(session.entries))
	}
	if session.entries[1].Kind != TranscriptCommand {
		t.Fatalf("tool_result entry kind = %q, want %q", session.entries[1].Kind, TranscriptCommand)
	}
	if !strings.Contains(session.entries[1].Text, "$ npm test") || !strings.Contains(session.entries[1].Text, "tests passed") {
		t.Fatalf("tool_result entry text = %q, want command output", session.entries[1].Text)
	}
	if session.entries[1].CommandText != "npm test" {
		t.Fatalf("tool_result command metadata = %q, want npm test", session.entries[1].CommandText)
	}
}

func TestClaudeAssistantBlocksDeduplicateRepeatedEvents(t *testing.T) {
	session := &claudeCodeSession{
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	line := `{"type":"assistant","session_id":"ses-demo","message":{"id":"msg_1","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"text","text":"Working on it."}]}}`
	session.handleClaudeStdoutLine(line)
	session.handleClaudeStdoutLine(line)

	if len(session.entries) != 1 {
		t.Fatalf("duplicate assistant events should not duplicate transcript entries, got %d", len(session.entries))
	}
	if session.entries[0].Text != "Working on it." {
		t.Fatalf("assistant text = %q, want original text", session.entries[0].Text)
	}
}

func TestClaudeStreamUsagePopulatesContextSnapshot(t *testing.T) {
	session := &claudeCodeSession{
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"ses-demo","model":"claude-opus-5"}`)
	session.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"msg_1","model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":914,"cache_creation_input_tokens":500000,"cache_read_input_tokens":150000,"output_tokens":42}}}`)
	session.handleClaudeStdoutLine(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","modelUsage":{"claude-opus-5":{"inputTokens":914,"cacheCreationInputTokens":500000,"cacheReadInputTokens":150000,"outputTokens":42,"contextWindow":1000000}}}`)

	snapshot := session.Snapshot()
	if snapshot.TokenUsage == nil {
		t.Fatal("TokenUsage = nil, want Claude usage snapshot")
	}
	if got, want := snapshot.TokenUsage.ContextTokens, int64(650914); got != want {
		t.Fatalf("ContextTokens = %d, want %d", got, want)
	}
	if got, want := snapshot.TokenUsage.ModelContextWindow, int64(1000000); got != want {
		t.Fatalf("ModelContextWindow = %d, want %d", got, want)
	}
	if got, want := snapshot.TokenUsage.ContextLeftPercent(), 35; got != want {
		t.Fatalf("ContextLeftPercent() = %d, want %d", got, want)
	}
	if got, want := snapshot.Model, "claude-opus-5"; got != want {
		t.Fatalf("Model = %q, want %q", got, want)
	}
	if err := session.ShowStatus(); err != nil {
		t.Fatalf("ShowStatus() error = %v", err)
	}
	if got := session.Snapshot().Transcript; !strings.Contains(got, "Context: 650914 / 1000000 tokens used (65% used, 35% left)") {
		t.Fatalf("status transcript = %q, want Claude context report", got)
	}
}

func TestClaudeUsageTotalsDeduplicateMessagesAndSurviveCompaction(t *testing.T) {
	session := &claudeCodeSession{
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	first := `{"type":"assistant","uuid":"outer-1","message":{"id":"msg_1","model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"First block."}],"usage":{"input_tokens":14,"cache_creation_input_tokens":500,"cache_read_input_tokens":1500,"output_tokens":42}}}`
	session.handleClaudeStdoutLine(first)
	// Claude persists one AssistantMessage per content block. Those records share
	// an API message ID and repeat the same per-message usage.
	session.handleClaudeStdoutLine(`{"type":"assistant","uuid":"outer-2","message":{"id":"msg_1","model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"Second block."}],"usage":{"input_tokens":14,"cache_creation_input_tokens":500,"cache_read_input_tokens":1500,"output_tokens":42}}}`)

	snapshot := session.Snapshot()
	if got, want := snapshot.TokenUsage.Total.InputTokens, int64(2014); got != want {
		t.Fatalf("deduplicated total input = %d, want %d", got, want)
	}
	if got, want := snapshot.TokenUsage.Total.OutputTokens, int64(42); got != want {
		t.Fatalf("deduplicated total output = %d, want %d", got, want)
	}

	session.handleClaudeStdoutLine(`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":2014}}`)
	if session.Snapshot().TokenUsage != nil {
		t.Fatal("current context usage should be unavailable immediately after compaction")
	}
	session.handleClaudeStdoutLine(`{"type":"assistant","uuid":"outer-3","message":{"id":"msg_2","model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"After compact."}],"usage":{"input_tokens":12,"cache_creation_input_tokens":30,"cache_read_input_tokens":1000,"output_tokens":10}}}`)

	snapshot = session.Snapshot()
	if got, want := snapshot.TokenUsage.ContextTokens, int64(1042); got != want {
		t.Fatalf("post-compact context = %d, want %d", got, want)
	}
	if got, want := snapshot.TokenUsage.Total.InputTokens, int64(3056); got != want {
		t.Fatalf("post-compact total input = %d, want %d", got, want)
	}
	if got, want := snapshot.TokenUsage.Total.OutputTokens, int64(52); got != want {
		t.Fatalf("post-compact total output = %d, want %d", got, want)
	}
}

func TestClaudeCompactBoundaryClearsStaleUsage(t *testing.T) {
	command := &claudeCompactCommand{done: make(chan claudeCompactCompletion, 1)}
	session := &claudeCodeSession{
		compacting:     true,
		compactCommand: command,
		tokenUsage: &TokenUsageSnapshot{
			ContextTokens:      650914,
			ModelContextWindow: 1000000,
		},
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":650914}}`)

	if session.tokenUsage != nil {
		t.Fatalf("tokenUsage = %#v, want stale pre-compaction usage cleared", session.tokenUsage)
	}
	if !command.boundarySeen {
		t.Fatal("compact boundary was not recorded")
	}
	if command.metadata.PreTokens != 650914 || command.metadata.Trigger != "manual" {
		t.Fatalf("compact metadata = %#v", command.metadata)
	}
	if got := session.entries[len(session.entries)-1].Text; !strings.Contains(got, "650914 tokens before compaction") {
		t.Fatalf("compact notice = %q, want pre-compaction count", got)
	}
}

func TestClaudeSyntheticAssistantKeepsLastRealModel(t *testing.T) {
	session := &claudeCodeSession{
		model:           "claude-fable-5",
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"assistant","is_api_error_message":true,"message":{"id":"msg_limit","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"You've hit your session limit."}]}}`)

	snapshot := session.Snapshot()
	if snapshot.Model != "claude-fable-5" {
		t.Fatalf("model after synthetic limit message = %q, want last real model", snapshot.Model)
	}
	if !strings.Contains(snapshot.Transcript, "You've hit your session limit.") {
		t.Fatalf("transcript = %q, want limit message preserved", snapshot.Transcript)
	}
	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if claudeModelOptionExists(models, "<synthetic>") {
		t.Fatalf("model options = %#v, want internal synthetic marker omitted", models)
	}
}

func TestClaudeLoadTranscriptRestoresUsageAndHidesCompactSummary(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"before","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Older reply."}],"usage":{"input_tokens":10,"cache_creation_input_tokens":600000,"cache_read_input_tokens":50000,"output_tokens":20}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","pre_tokens":650010}}`,
		`{"type":"user","uuid":"summary","isCompactSummary":true,"message":{"role":"user","content":"This is Claude's generated compact summary, not a human prompt."}}`,
		`{"type":"user","uuid":"after","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"Continue."}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}
	session.loadTranscriptLocked()

	if session.tokenUsage != nil {
		t.Fatalf("tokenUsage = %#v, want pre-compaction usage cleared", session.tokenUsage)
	}
	if got, want := len(session.entries), 3; got != want {
		t.Fatalf("entry count = %d, want %d: %#v", got, want, session.entries)
	}
	if session.entries[1].Kind != TranscriptSystem || !strings.Contains(session.entries[1].Text, "compacted conversation history") {
		t.Fatalf("compact boundary entry = %#v", session.entries[1])
	}
	if session.entries[2].Kind != TranscriptUser || session.entries[2].Text != "Continue." {
		t.Fatalf("post-compact user entry = %#v", session.entries[2])
	}
	for _, entry := range session.entries {
		if strings.Contains(entry.Text, "generated compact summary") {
			t.Fatalf("compact summary leaked into transcript: %#v", entry)
		}
	}
}

func TestClaudeLoadTranscriptAggregatesUniqueMessagesAcrossCompaction(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"before-a","message":{"id":"msg_before","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Part one."}],"usage":{"input_tokens":10,"cache_creation_input_tokens":600,"cache_read_input_tokens":50,"output_tokens":20}}}`,
		`{"type":"assistant","uuid":"before-b","message":{"id":"msg_before","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Part two."}],"usage":{"input_tokens":10,"cache_creation_input_tokens":600,"cache_read_input_tokens":50,"output_tokens":20}}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","pre_tokens":660}}`,
		`{"type":"assistant","uuid":"after","message":{"id":"msg_after","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"New context."}],"usage":{"input_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":200,"output_tokens":7}}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}
	if err := session.loadTranscriptLocked(); err != nil {
		t.Fatalf("loadTranscriptLocked() error = %v", err)
	}
	if session.tokenUsage == nil {
		t.Fatal("tokenUsage = nil, want post-compact usage")
	}
	if got, want := session.tokenUsage.ContextTokens, int64(305); got != want {
		t.Fatalf("current context = %d, want %d", got, want)
	}
	if got, want := session.tokenUsage.Total.InputTokens, int64(965); got != want {
		t.Fatalf("cumulative input = %d, want %d", got, want)
	}
	if got, want := session.tokenUsage.Total.OutputTokens, int64(27); got != want {
		t.Fatalf("cumulative output = %d, want %d", got, want)
	}
}

func TestClaudeRateLimitEventsPopulateUsageWindows(t *testing.T) {
	session := &claudeCodeSession{}
	fiveHourReset := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC).Unix()
	weeklyReset := time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC).Unix()
	session.handleClaudeStdoutLine(fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","utilization":0.17,"resetsAt":%d}}`, fiveHourReset))
	session.handleClaudeStdoutLine(fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"seven_day","utilization":0.03,"resetsAt":%d}}`, weeklyReset))

	windows := session.Snapshot().UsageWindows
	if len(windows) != 2 {
		t.Fatalf("UsageWindows = %#v, want five-hour and weekly windows", windows)
	}
	if windows[0].Window != "5h" || windows[0].LeftPercent != 83 || !windows[0].ResetsAt.Equal(time.Unix(fiveHourReset, 0)) {
		t.Fatalf("five-hour window = %#v", windows[0])
	}
	if windows[1].Window != "weekly" || windows[1].LeftPercent != 97 || !windows[1].ResetsAt.Equal(time.Unix(weeklyReset, 0)) {
		t.Fatalf("weekly window = %#v", windows[1])
	}
}

func TestClaudeSessionFilePathUsesClaudeProjectDirectorySanitization(t *testing.T) {
	t.Parallel()

	got := claudeSessionFilePath(
		"/tmp/claude-home",
		"/Users/davide/dev/repos/demo_tviking--improve-techno-viking-model",
		"session-123",
	)
	want := filepath.Join(
		"/tmp/claude-home",
		"projects",
		"-Users-davide-dev-repos-demo-tviking--improve-techno-viking-model",
		"session-123.jsonl",
	)
	if got != want {
		t.Fatalf("claudeSessionFilePath() = %q, want %q", got, want)
	}
}

func TestClaudeLoadTranscriptReportsMissingSessionFile(t *testing.T) {
	t.Parallel()

	session := &claudeCodeSession{
		sessionFile: filepath.Join(t.TempDir(), "missing-session.jsonl"),
	}
	err := session.loadTranscriptLocked()
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loadTranscriptLocked() error = %v, want missing-file error", err)
	}
}

func TestParseCCLineEntriesRebuildsStructuredToolEntries(t *testing.T) {
	toolCalls := make(map[string]claudeToolCall)
	toolResults := make(map[string]struct{})
	var conversationTracker claudeartifact.ConversationTracker

	assistantEntries, entryType, reasoningEffort, _ := parseCCLineEntries(`{"type":"assistant","uuid":"msg_1","effort":"xhigh","message":{"role":"assistant","content":[{"type":"text","text":"Checking logs."},{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"refresh"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"make test"}}]}}`, toolCalls, toolResults, &conversationTracker)
	if entryType != "assistant" {
		t.Fatalf("assistant entry type = %q, want assistant", entryType)
	}
	if reasoningEffort != "xhigh" {
		t.Fatalf("assistant reasoning effort = %q, want xhigh", reasoningEffort)
	}
	if len(assistantEntries) != 3 {
		t.Fatalf("assistant entry count = %d, want 3", len(assistantEntries))
	}
	if assistantEntries[0].Kind != TranscriptAgent || assistantEntries[0].Text != "Checking logs." {
		t.Fatalf("assistant text entry = %#v, want agent text", assistantEntries[0])
	}
	if assistantEntries[1].Kind != TranscriptTool || assistantEntries[1].Text != "Grep: refresh" {
		t.Fatalf("grep tool entry = %#v, want structured grep tool", assistantEntries[1])
	}
	if assistantEntries[2].Kind != TranscriptTool || assistantEntries[2].Text != "Bash: make test" {
		t.Fatalf("bash tool entry = %#v, want structured bash tool", assistantEntries[2])
	}

	userEntries, entryType, reasoningEffort, _ := parseCCLineEntries(`{"type":"user","uuid":"msg_2","parentUuid":"msg_1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"tests passed"}]}}`, toolCalls, toolResults, &conversationTracker)
	if entryType != "user" {
		t.Fatalf("user entry type = %q, want user", entryType)
	}
	if reasoningEffort != "" {
		t.Fatalf("user reasoning effort = %q, want empty", reasoningEffort)
	}
	if len(userEntries) != 1 {
		t.Fatalf("user entry count = %d, want 1", len(userEntries))
	}
	if userEntries[0].Kind != TranscriptCommand {
		t.Fatalf("user result kind = %q, want %q", userEntries[0].Kind, TranscriptCommand)
	}
	if !strings.Contains(userEntries[0].Text, "$ make test") || !strings.Contains(userEntries[0].Text, "tests passed") {
		t.Fatalf("user result text = %q, want reconstructed command output", userEntries[0].Text)
	}
}

func TestParseCCLineEntriesPreservesFileToolAndCommandMetadata(t *testing.T) {
	toolCalls := make(map[string]claudeToolCall)
	toolResults := make(map[string]struct{})
	var conversationTracker claudeartifact.ConversationTracker

	assistantEntries, _, _, _ := parseCCLineEntries(
		`{"type":"assistant","uuid":"msg_1","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_read","name":"Read","input":{"file_path":"/tmp/tv_shots/v1_head_f200.png"}},{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"magick /tmp/tv_shots/source.ppm /tmp/tv_shots/v1_zoom.png"}}]}}`,
		toolCalls,
		toolResults,
		&conversationTracker,
	)
	if len(assistantEntries) != 2 {
		t.Fatalf("assistant entry count = %d, want 2", len(assistantEntries))
	}
	if got := assistantEntries[0]; got.Kind != TranscriptTool ||
		got.ToolName != "Read" ||
		got.ToolPath != "/tmp/tv_shots/v1_head_f200.png" {
		t.Fatalf("read entry metadata = %#v", got)
	}
	if got := assistantEntries[1]; got.Kind != TranscriptTool ||
		got.ToolName != "Bash" ||
		got.ToolPath != "" {
		t.Fatalf("bash entry metadata = %#v", got)
	}

	userEntries, _, _, _ := parseCCLineEntries(
		`{"type":"user","uuid":"msg_2","parentUuid":"msg_1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"/tmp/tv_shots/v1_zoom.png"}]}}`,
		toolCalls,
		toolResults,
		&conversationTracker,
	)
	if len(userEntries) != 1 {
		t.Fatalf("user entry count = %d, want 1", len(userEntries))
	}
	if got, want := userEntries[0].CommandText, "magick /tmp/tv_shots/source.ppm /tmp/tv_shots/v1_zoom.png"; got != want {
		t.Fatalf("command metadata = %q, want %q", got, want)
	}
}

func TestClaudeLoadTranscriptHidesLocalCommandRecordsAndKeepsRepeatedPrompt(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"previous-answer","message":{"role":"assistant","content":[{"type":"text","text":"Choose one of the open items."}]}}`,
		`{"type":"user","isMeta":true,"uuid":"model-caveat","parentUuid":"previous-answer","promptId":"model-command","message":{"role":"user","content":"<local-command-caveat>generated local command records follow</local-command-caveat>"}}`,
		`{"type":"user","uuid":"model-command","parentUuid":"model-caveat","promptId":"model-command","message":{"role":"user","content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"}}`,
		`{"type":"user","uuid":"model-output","parentUuid":"model-command","promptId":"model-command","message":{"role":"user","content":"<local-command-stdout>Set model to Opus 5</local-command-stdout>"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","isMeta":true,"uuid":"effort-caveat","parentUuid":"model-output","promptId":"effort-command","message":{"role":"user","content":"<local-command-caveat>generated local command records follow</local-command-caveat>"}}`,
		`{"type":"user","uuid":"effort-command","parentUuid":"effort-caveat","promptId":"effort-command","message":{"role":"user","content":"<command-name>/effort</command-name>\n<command-message>effort</command-message>\n<command-args></command-args>"}}`,
		`{"type":"user","uuid":"effort-output","parentUuid":"effort-command","promptId":"effort-command","message":{"role":"user","content":"<local-command-stdout>Set effort level to xhigh</local-command-stdout>"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"escaped-prompt","parentUuid":"effort-output","promptId":"first-prompt","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"continued-prompt","parentUuid":"effort-output","promptId":"second-prompt","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"assistant","uuid":"answer","parentUuid":"continued-prompt","message":{"role":"assistant","content":[{"type":"text","text":"Config plumbing is the most important."}]}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}
	session.loadTranscriptLocked()

	if got, want := len(session.entries), 4; got != want {
		t.Fatalf("entry count = %d, want %d: %#v", got, want, session.entries)
	}
	if session.entries[0].Kind != TranscriptAgent || session.entries[0].Text != "Choose one of the open items." {
		t.Fatalf("previous assistant entry = %#v", session.entries[0])
	}
	for i := 1; i <= 2; i++ {
		if session.entries[i].Kind != TranscriptUser || session.entries[i].Text != "which one you think is more improtant at this point" {
			t.Fatalf("repeated prompt entry %d = %#v", i, session.entries[i])
		}
	}
	if session.entries[3].Kind != TranscriptAgent || session.entries[3].Text != "Config plumbing is the most important." {
		t.Fatalf("answer entry = %#v", session.entries[3])
	}
	for _, entry := range session.entries {
		if strings.Contains(entry.Text, "<command-") || strings.Contains(entry.Text, "<local-command-") {
			t.Fatalf("local command XML leaked into transcript entry: %#v", entry)
		}
	}
}

func TestClaudeLoadTranscriptPreservesSubmittedXMLLookingText(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"user","isMeta":true,"uuid":"command-caveat","message":{"role":"user","content":"internal metadata"}}`,
		`{"type":"user","uuid":"command-output","parentUuid":"command-caveat","message":{"role":"user","content":"internal command output"}}`,
		`{"type":"user","uuid":"real-prompt","parentUuid":"command-output","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"Please explain <command-name>/model</command-name>."}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}
	session.loadTranscriptLocked()

	if got, want := len(session.entries), 1; got != want {
		t.Fatalf("entry count = %d, want %d: %#v", got, want, session.entries)
	}
	if got, want := session.entries[0].Text, "Please explain <command-name>/model</command-name>."; got != want {
		t.Fatalf("submitted XML-looking text = %q, want %q", got, want)
	}
}

func TestClaudeLoadTranscriptKeepsToolEntriesStructuredOnRefresh(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"msg_1","message":{"role":"assistant","content":[{"type":"text","text":"Checking logs."},{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"refresh"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"make test"}}]}}`,
		`{"type":"user","uuid":"msg_2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"tests passed"}]}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}

	session.loadTranscriptLocked()

	if len(session.entries) != 4 {
		t.Fatalf("entry count after refresh = %d, want 4", len(session.entries))
	}
	kinds := []TranscriptKind{
		session.entries[0].Kind,
		session.entries[1].Kind,
		session.entries[2].Kind,
		session.entries[3].Kind,
	}
	wantKinds := []TranscriptKind{TranscriptAgent, TranscriptTool, TranscriptTool, TranscriptCommand}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("entry kinds = %#v, want %#v", kinds, wantKinds)
	}
	if session.entries[1].Text != "Grep: refresh" {
		t.Fatalf("grep entry text = %q, want structured grep tool", session.entries[1].Text)
	}
	if session.entries[2].Text != "Bash: make test" {
		t.Fatalf("bash entry text = %q, want structured bash tool", session.entries[2].Text)
	}
	if strings.Contains(session.entries[0].Text, "[Grep]") || strings.Contains(session.entries[0].Text, "[Bash]") {
		t.Fatalf("assistant text should no longer inline bracketed tool labels: %q", session.entries[0].Text)
	}
	if !strings.Contains(session.entries[3].Text, "$ make test") {
		t.Fatalf("command entry text = %q, want reconstructed bash command", session.entries[3].Text)
	}
}

func TestClaudeLoadTranscriptPresentsServerAPIErrorAsRecoverableInterruption(t *testing.T) {
	sessionFile := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"user","uuid":"msg_user","message":{"role":"user","content":[{"type":"text","text":"let's pause for a few"}]}}`,
		`{"type":"assistant","uuid":"msg_error","error":"server_error","isApiErrorMessage":true,"message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: Unable to connect to API (ENOTFOUND)"}]}}`,
		`{"type":"last-prompt","lastPrompt":"let's pause for a few"}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile: sessionFile,
		toolCalls:   make(map[string]claudeToolCall),
		toolResults: make(map[string]struct{}),
	}
	if err := session.loadTranscriptLocked(); err != nil {
		t.Fatalf("loadTranscriptLocked() error = %v", err)
	}

	snapshot := session.Snapshot()
	if len(snapshot.Entries) != 2 {
		t.Fatalf("entries = %#v, want saved user prompt and provider interruption", snapshot.Entries)
	}
	got := snapshot.Entries[1]
	if got.Kind != TranscriptStatus {
		t.Fatalf("API error kind = %q, want %q", got.Kind, TranscriptStatus)
	}
	if got.Text != "API Error: Unable to connect to API (ENOTFOUND)" {
		t.Fatalf("raw API error = %q, want diagnostic preserved", got.Text)
	}
	if got.DisplayText != claudeRecoverableAPIErrorNotice {
		t.Fatalf("API error display text = %q, want %q", got.DisplayText, claudeRecoverableAPIErrorNotice)
	}
	if !strings.Contains(snapshot.Transcript, claudeRecoverableAPIErrorNotice) {
		t.Fatalf("transcript = %q, want recoverable interruption notice", snapshot.Transcript)
	}
	if strings.Contains(snapshot.Transcript, "ENOTFOUND") {
		t.Fatalf("transcript = %q, raw provider failure should not be the user-facing text", snapshot.Transcript)
	}
}

func TestClaudeAPIErrorClassificationKeepsActionableFailuresAsErrors(t *testing.T) {
	var conversationTracker claudeartifact.ConversationTracker
	entries, _, _, _ := parseCCLineEntries(
		`{"type":"assistant","uuid":"msg_limit","error":"rate_limit","isApiErrorMessage":true,"message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"You've hit your session limit"}]}}`,
		make(map[string]claudeToolCall),
		make(map[string]struct{}),
		&conversationTracker,
	)
	if len(entries) != 1 || entries[0].Kind != TranscriptError {
		t.Fatalf("entries = %#v, want actionable API failure rendered as an error", entries)
	}
	if entries[0].Text != "You've hit your session limit" || entries[0].DisplayText != "" {
		t.Fatalf("rate-limit entry = %#v, want provider guidance unchanged", entries[0])
	}
}

func TestClaudeLoadTranscriptRestoresLatestReasoningEffort(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"msg_1","effort":"high","message":{"role":"assistant","content":[{"type":"text","text":"First reply."}]}}`,
		`{"type":"user","uuid":"msg_2","message":{"role":"user","content":[{"type":"text","text":"Continue."}]}}`,
		`{"type":"assistant","uuid":"msg_3","effort":"xhigh","message":{"role":"assistant","content":[{"type":"text","text":"Latest reply."}]}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := &claudeCodeSession{
		sessionFile:      sessionFile,
		pendingReasoning: "max",
		toolCalls:        make(map[string]claudeToolCall),
		toolResults:      make(map[string]struct{}),
	}

	session.loadTranscriptLocked()
	snapshot := session.Snapshot()

	if snapshot.ReasoningEffort != "xhigh" {
		t.Fatalf("restored reasoning effort = %q, want latest xhigh", snapshot.ReasoningEffort)
	}
	if snapshot.PendingReasoning != "max" {
		t.Fatalf("pending reasoning effort = %q, want staged max preserved", snapshot.PendingReasoning)
	}
}

func TestClaudeListModelsIncludesAliasesAndCurrentModel(t *testing.T) {
	session := &claudeCodeSession{
		model:        "claude-sonnet-4-6",
		pendingModel: "claude-opus-4-6",
	}

	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) < 4 {
		t.Fatalf("ListModels() returned %d models, want curated aliases", len(models))
	}
	if got := models[0].Model; got != "claude-opus-4-6" {
		t.Fatalf("first model = %q, want pending model first", got)
	}
	if got := models[1].Model; got != "claude-sonnet-4-6" {
		t.Fatalf("second model = %q, want current model next", got)
	}
	if !claudeModelOptionExists(models, "sonnet") {
		t.Fatalf("expected sonnet alias in model list")
	}
	if !claudeModelOptionExists(models, "fable") {
		t.Fatalf("expected fable alias in model list")
	}
	if !claudeModelOptionExists(models, "opus") {
		t.Fatalf("expected opus alias in model list")
	}
	if !claudeModelOptionExists(models, "haiku") {
		t.Fatalf("expected haiku alias in model list")
	}
	if got := models[2].DefaultReasoningEffort; got != claudeDefaultReasoningEffort {
		t.Fatalf("default reasoning = %q, want %q", got, claudeDefaultReasoningEffort)
	}
}

func TestClaudeReasoningEffortsIncludeXHigh(t *testing.T) {
	efforts := claudeReasoningEffortOptions()
	for _, effort := range efforts {
		if effort.ReasoningEffort == "xhigh" {
			return
		}
	}
	t.Fatalf("reasoning efforts = %#v, want xhigh", efforts)
}

func TestClaudeSubmitReportsMissingAuthenticationBeforeStartingTurn(t *testing.T) {
	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ] && [ "$3" = "--json" ]; then
	printf '%s\n' '{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}'
	exit 1
fi
exit 99
`
	if err := os.WriteFile(claudePath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Claude CLI: %v", err)
	}
	t.Setenv("PATH", binDir)

	notified := false
	session := &claudeCodeSession{
		projectPath:     t.TempDir(),
		claudeHome:      t.TempDir(),
		preset:          codexcli.PresetSafe,
		status:          claudeFreshReadyStatus,
		closedCh:        make(chan struct{}),
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
		notify:          func() { notified = true },
	}

	err := session.SubmitInput(Submission{Text: "please fix the bug"})
	if !errors.Is(err, ErrClaudeCodeAuthenticationRequired) {
		t.Fatalf("SubmitInput() error = %v, want ErrClaudeCodeAuthenticationRequired", err)
	}
	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatal("Snapshot().Busy = true, want turn not started")
	}
	if snapshot.LastError != ErrClaudeCodeAuthenticationRequired.Error() {
		t.Fatalf("Snapshot().LastError = %q, want actionable auth message", snapshot.LastError)
	}
	if !strings.Contains(snapshot.Transcript, "claude auth login") {
		t.Fatalf("Snapshot().Transcript = %q, want login instructions", snapshot.Transcript)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != TranscriptError {
		t.Fatalf("Snapshot().Entries = %#v, want one auth error and no submitted user turn", snapshot.Entries)
	}
	if !notified {
		t.Fatal("session did not notify after recording the authentication error")
	}
}

func TestClaudeCompactForwardsInstructionsAndRequiresBoundary(t *testing.T) {
	binDir := t.TempDir()
	inputPath := filepath.Join(t.TempDir(), "input.json")
	claudePath := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ] && [ "$3" = "--json" ]; then
	printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'
	exit 0
fi
IFS= read -r payload
printf '%s\n' "$payload" > "$CLAUDE_TEST_INPUT"
printf '%s\n' '{"type":"system","subtype":"init","session_id":"ses-demo","model":"claude-opus-5","permissionMode":"acceptEdits"}'
printf '%s\n' '{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":650914}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"Compacted","modelUsage":{"claude-opus-5":{"contextWindow":1000000}}}'
`
	if err := os.WriteFile(claudePath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Claude CLI: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CLAUDE_TEST_INPUT", inputPath)

	session := &claudeCodeSession{
		projectPath:     t.TempDir(),
		claudeHome:      t.TempDir(),
		sessionID:       "ses-demo",
		started:         true,
		preset:          codexcli.PresetSafe,
		safetySettings:  `{"hooks":{"PreToolUse":[]}}`,
		status:          claudeReadyStatus,
		closedCh:        make(chan struct{}),
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	result, err := session.CompactWithInstructions("preserve the renderer decisions")
	if err != nil {
		t.Fatalf("CompactWithInstructions() error = %v", err)
	}
	if !result.Compacted || result.PreTokens != 650914 || result.Trigger != "manual" {
		t.Fatalf("compaction result = %#v", result)
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read captured Claude input: %v", err)
	}
	var payload struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode captured Claude input: %v", err)
	}
	if got, want := payload.Message.Content[0].Text, "/compact preserve the renderer decisions"; got != want {
		t.Fatalf("Claude compact prompt = %q, want %q", got, want)
	}

	snapshot := session.Snapshot()
	if snapshot.Busy || snapshot.Phase != SessionPhaseIdle {
		t.Fatalf("snapshot busy=%t phase=%q, want settled session", snapshot.Busy, snapshot.Phase)
	}
	for _, entry := range snapshot.Entries {
		if entry.Kind == TranscriptUser && strings.Contains(entry.Text, "/compact") {
			t.Fatalf("host compact command leaked in as a conversational prompt: %#v", entry)
		}
	}
	if snapshot.TokenUsage != nil {
		t.Fatalf("TokenUsage = %#v, want stale usage unavailable after compact", snapshot.TokenUsage)
	}

	if err := session.ShowStatus(); err != nil {
		t.Fatalf("ShowStatus() error = %v", err)
	}
	if got := session.Snapshot().Transcript; !strings.Contains(got, "model window is 1000000 tokens") {
		t.Fatalf("status transcript = %q, want retained model context window", got)
	}
}

func TestClaudeCompactNoBoundaryReportsNoOp(t *testing.T) {
	result, err := claudeCompactionCompletion(&claudeCompactCommand{
		resultText: "Conversation is too short to compact.",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("claudeCompactionCompletion() error = %v", err)
	}
	if result.Compacted {
		t.Fatalf("result.Compacted = true, want no-op without compact boundary")
	}
	if result.Message != "Conversation is too short to compact." {
		t.Fatalf("result.Message = %q", result.Message)
	}
}

func TestClaudeCompactTranscriptBoundaryDoesNotDuplicateNotice(t *testing.T) {
	sessionFile := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":738407}}`
	if err := os.WriteFile(sessionFile, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write Claude transcript: %v", err)
	}

	command := &claudeCompactCommand{done: make(chan claudeCompactCompletion, 1)}
	session := &claudeCodeSession{
		sessionFile:     sessionFile,
		busy:            true,
		compacting:      true,
		compactCommand:  command,
		status:          claudeCompactingStatus,
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.finishClaudeTurn(nil, nil, nil)

	completion := <-command.done
	if completion.err != nil {
		t.Fatalf("compaction completion error = %v", completion.err)
	}
	if !completion.result.Compacted {
		t.Fatalf("compaction result = %#v, want compacted boundary", completion.result)
	}
	notice := claudeCompactionNotice(claudeCompactMetadata{PreTokens: 738407, Trigger: "manual"})
	count := 0
	for _, entry := range session.Snapshot().Entries {
		if entry.Kind == TranscriptSystem && entry.Text == notice {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("compaction notice count = %d, want 1: %#v", count, session.Snapshot().Entries)
	}
	if session.Snapshot().LastSystemNotice != notice {
		t.Fatalf("LastSystemNotice = %q, want %q", session.Snapshot().LastSystemNotice, notice)
	}
}

func TestClaudeCompactExitReportsUnresolvedBackgroundTask(t *testing.T) {
	command := &claudeCompactCommand{done: make(chan claudeCompactCompletion, 1)}
	session := &claudeCodeSession{
		started:        true,
		busy:           true,
		compacting:     true,
		compactCommand: command,
		backgroundTasks: map[string]BackgroundTaskSnapshot{
			"task-bg-1": {
				ID:     "task-bg-1",
				Status: "running",
			},
		},
		backgroundTaskOrder: []string{"task-bg-1"},
		assistantBlocks:     make(map[string]map[string]struct{}),
		toolCalls:           make(map[string]claudeToolCall),
		toolResults:         make(map[string]struct{}),
	}

	session.finishClaudeTurn(nil, nil, nil)

	completion := <-command.done
	if completion.err == nil || completion.err.Error() != claudeBackgroundTaskUnresolved {
		t.Fatalf("compaction error = %v, want unresolved background-task error", completion.err)
	}
	snapshot := session.Snapshot()
	if snapshot.LastError != claudeBackgroundTaskUnresolved {
		t.Fatalf("LastError = %q, want unresolved-task error", snapshot.LastError)
	}
	if len(snapshot.BackgroundTasks) != 1 || snapshot.BackgroundTasks[0].Status != "unresolved" {
		t.Fatalf("BackgroundTasks = %#v, want visible unresolved task", snapshot.BackgroundTasks)
	}
}

func TestClaudeTurnAuthenticationFailureAvoidsGenericExitError(t *testing.T) {
	session := &claudeCodeSession{
		status:          claudeThinkingStatus,
		busy:            true,
		lastError:       "claude stderr: Failed to authenticate",
		entries:         []TranscriptEntry{{Kind: TranscriptError, Text: "claude stderr: Failed to authenticate"}},
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.finishClaudeTurn(ErrClaudeCodeAuthenticationRequired, nil, nil)

	snapshot := session.Snapshot()
	if snapshot.LastError != ErrClaudeCodeAuthenticationRequired.Error() {
		t.Fatalf("Snapshot().LastError = %q, want actionable auth message", snapshot.LastError)
	}
	if !strings.Contains(snapshot.Transcript, "claude auth login") {
		t.Fatalf("Snapshot().Transcript = %q, want login instructions", snapshot.Transcript)
	}
	if strings.Contains(snapshot.Transcript, "Claude Code exited with error") {
		t.Fatalf("Snapshot().Transcript = %q, want no generic exit-status error", snapshot.Transcript)
	}
}

func TestClaudeStageModelOverrideKeepsTranscriptRevisionStable(t *testing.T) {
	session := &claudeCodeSession{
		model:              "sonnet",
		reasoningEffort:    "medium",
		lastActivityAt:     time.Now(),
		transcriptRevision: 1,
		entries: []TranscriptEntry{{
			Kind: TranscriptAgent,
			Text: "Existing reply",
		}},
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	first := session.Snapshot()
	if err := session.StageModelOverride("opus", "high"); err != nil {
		t.Fatalf("StageModelOverride() error = %v", err)
	}
	second := session.Snapshot()

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

func TestClaudeTurnArgsIncludeVerboseForStreamJSON(t *testing.T) {
	got := claudeTurnArgs("ses-demo", "sonnet", "high", "bypassPermissions")
	want := []string{
		"-p",
		"--verbose",
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--permission-mode", "bypassPermissions",
		"--resume", "ses-demo",
		"--model", "sonnet",
		"--effort", "high",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgs() = %#v, want %#v", got, want)
	}
}

func TestClaudeTurnArgsOmitSyntheticModelPlaceholder(t *testing.T) {
	got := claudeTurnArgs("ses-demo", "<synthetic>", "high", "bypassPermissions")
	want := []string{
		"-p",
		"--verbose",
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--permission-mode", "bypassPermissions",
		"--resume", "ses-demo",
		"--effort", "high",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgs() = %#v, want synthetic model marker omitted: %#v", got, want)
	}
}

func TestClaudeTurnArgsAddRuntimeMCPWithoutReplacingUserServers(t *testing.T) {
	const (
		config = `{"mcpServers":{"lcr_runtime":{"type":"stdio","command":"/tmp/lcroom","args":["runtime-mcp"]}}}`
		prompt = "Follow the shared LCR TODO capture policy."
	)
	const safetySettings = `{"hooks":{"PreToolUse":[]}}`
	got := claudeTurnArgsWithRuntimeMCP("ses-demo", "sonnet", "high", "acceptEdits", config, prompt, safetySettings)
	want := []string{
		"-p",
		"--verbose",
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--permission-mode", "acceptEdits",
		"--resume", "ses-demo",
		"--model", "sonnet",
		"--effort", "high",
		"--settings", safetySettings,
		"--mcp-config", config,
		"--append-system-prompt", prompt,
		"--allowedTools", strings.Join([]string{
			claudeRuntimeMCPListControlsTool,
			claudeRuntimeMCPDescribeControlTool,
			claudeRuntimeMCPProposeControlTool,
			claudeRuntimeMCPGetControlTool,
			claudeRuntimeMCPListTODOsTool,
			claudeRuntimeMCPAddTODOTool,
		}, ","),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithRuntimeMCP() = %#v, want %#v", got, want)
	}
	for _, arg := range got {
		if arg == "--strict-mcp-config" {
			t.Fatal("runtime MCP args must preserve user-configured MCP servers")
		}
	}
}

func TestClaudeTurnArgsOmitRuntimeMCPFlagsWithoutConfig(t *testing.T) {
	const safetySettings = `{"hooks":{"PreToolUse":[]}}`
	got := claudeTurnArgsWithRuntimeMCP("", "", "", "acceptEdits", "  ", "ignored instructions", safetySettings)
	want := append(claudeTurnArgs("", "", "", "acceptEdits"), "--settings", safetySettings)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithRuntimeMCP() = %#v, want %#v", got, want)
	}
}

func TestClaudeTurnArgsKeepRuntimeMCPWithoutTODOPreapproval(t *testing.T) {
	const config = `{"mcpServers":{"lcr_runtime":{"type":"stdio","command":"/tmp/lcroom"}}}`
	const safetySettings = `{"hooks":{"PreToolUse":[]}}`
	got := claudeTurnArgsWithRuntimeMCP("", "", "", "acceptEdits", config, "", safetySettings)
	want := append(
		append(claudeTurnArgs("", "", "", "acceptEdits"), "--settings", safetySettings),
		"--mcp-config", config,
		"--allowedTools", strings.Join([]string{
			claudeRuntimeMCPListControlsTool,
			claudeRuntimeMCPDescribeControlTool,
			claudeRuntimeMCPProposeControlTool,
			claudeRuntimeMCPGetControlTool,
		}, ","),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithRuntimeMCP() = %#v, want %#v", got, want)
	}
}

func TestStartClaudeTurnFailsClosedWithoutSafetySettings(t *testing.T) {
	_, _, _, _, err := startClaudeTurnWithRuntimeMCP(
		context.Background(),
		t.TempDir(),
		"",
		"sonnet",
		"medium",
		"bypassPermissions",
		browserctl.Policy{},
		"",
		"",
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "safety-hook settings are required") {
		t.Fatalf("startClaudeTurnWithRuntimeMCP() error = %v, want missing safety-hook rejection", err)
	}
}

func TestApplyEmbeddedClaudeProcessEnvironmentDisablesNativeBackgroundTasks(t *testing.T) {
	cmd := &exec.Cmd{
		Env: []string{
			"KEEP=value",
			claudeDisableBackgroundTasksEnv + "=0",
			claudeDisableBackgroundTasksEnv + "=false",
		},
	}

	applyEmbeddedClaudeProcessEnvironment(cmd)

	var backgroundSettings []string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, claudeDisableBackgroundTasksEnv+"=") {
			backgroundSettings = append(backgroundSettings, entry)
		}
	}
	want := []string{claudeDisableBackgroundTasksEnv + "=1"}
	if !reflect.DeepEqual(backgroundSettings, want) {
		t.Fatalf("background task settings = %#v, want %#v", backgroundSettings, want)
	}
	if !containsString(cmd.Env, "KEEP=value") {
		t.Fatalf("environment = %#v, want unrelated values preserved", cmd.Env)
	}
}

func TestClaudeSnapshotIncludesBusySinceForInternalTurn(t *testing.T) {
	startedAt := time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC)
	session := &claudeCodeSession{
		projectPath:        "/tmp/demo",
		started:            true,
		busy:               true,
		busySince:          startedAt,
		pendingSubmissions: 1,
		status:             claudeThinkingStatus,
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
	}

	snapshot := session.Snapshot()
	if !snapshot.BusySince.Equal(startedAt) {
		t.Fatalf("snapshot.BusySince = %v, want %v", snapshot.BusySince, startedAt)
	}
	if snapshot.Phase != SessionPhaseRunning {
		t.Fatalf("snapshot.Phase = %q, want %q", snapshot.Phase, SessionPhaseRunning)
	}
}

func TestClaudeSnapshotUsesFinishingPhaseWhenBatchIsDraining(t *testing.T) {
	session := &claudeCodeSession{
		projectPath:     "/tmp/demo",
		started:         true,
		busy:            true,
		status:          claudeFinishingStatus,
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	snapshot := session.Snapshot()
	if snapshot.Phase != SessionPhaseFinishing {
		t.Fatalf("snapshot.Phase = %q, want %q", snapshot.Phase, SessionPhaseFinishing)
	}
}

func TestClaudeSubmitInputSteersActiveStream(t *testing.T) {
	stdin := &recordingWriteCloser{}
	session := &claudeCodeSession{
		projectPath:        "/tmp/demo",
		busy:               true,
		pendingSubmissions: 1,
		cmd:                &exec.Cmd{},
		stdin:              stdin,
		status:             claudeThinkingStatus,
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
	}

	if err := session.SubmitInput(Submission{Text: "keep going"}); err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}

	if session.pendingSubmissions != 2 {
		t.Fatalf("pendingSubmissions = %d, want 2", session.pendingSubmissions)
	}
	if !session.interruptPending {
		t.Fatalf("interruptPending = false, want true while steer is in flight")
	}
	if len(stdin.writes) != 2 {
		t.Fatalf("stdin writes = %d, want 2", len(stdin.writes))
	}
	if !strings.Contains(stdin.writes[0], `"type":"control_request"`) || !strings.Contains(stdin.writes[0], `"subtype":"interrupt"`) {
		t.Fatalf("first stdin payload = %q, want interrupt control request", stdin.writes[0])
	}
	if !strings.Contains(stdin.writes[1], `"type":"user"`) || !strings.Contains(stdin.writes[1], `"text":"keep going"`) {
		t.Fatalf("second stdin payload = %q, want steered user message", stdin.writes[1])
	}
	if got := session.entries[len(session.entries)-1]; got.Kind != TranscriptUser || got.Text != "keep going" {
		t.Fatalf("last entry = %#v, want steered user transcript entry", got)
	}
}

func TestBuildClaudeStreamInputIncludesImageAttachment(t *testing.T) {
	imageData := mustGeneratedImageTestPNG(t)
	imagePath := filepath.Join(t.TempDir(), "replacement.png")
	if err := os.WriteFile(imagePath, imageData, 0o600); err != nil {
		t.Fatalf("write image attachment: %v", err)
	}

	raw, err := buildClaudeStreamInput(Submission{
		Text: "Review the replacement image",
		Attachments: []Attachment{{
			Kind: AttachmentLocalImage,
			Path: imagePath,
		}},
	})
	if err != nil {
		t.Fatalf("buildClaudeStreamInput() error = %v", err)
	}

	var payload struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal Claude stream input: %v", err)
	}
	if payload.Type != "user" || payload.Message.Role != "user" {
		t.Fatalf("payload envelope = %#v, want user message", payload)
	}
	if len(payload.Message.Content) != 2 {
		t.Fatalf("content blocks = %#v, want text plus image", payload.Message.Content)
	}
	if payload.Message.Content[0].Type != "text" || payload.Message.Content[0].Text != "Review the replacement image" {
		t.Fatalf("text block = %#v", payload.Message.Content[0])
	}
	imageBlock := payload.Message.Content[1]
	if imageBlock.Type != "image" || imageBlock.Source.Type != "base64" || imageBlock.Source.MediaType != "image/png" {
		t.Fatalf("image block = %#v", imageBlock)
	}
	decoded, err := base64.StdEncoding.DecodeString(imageBlock.Source.Data)
	if err != nil {
		t.Fatalf("decode image attachment: %v", err)
	}
	if !reflect.DeepEqual(decoded, imageData) {
		t.Fatalf("decoded image attachment differs from source")
	}
}

func TestClaudeSubmitInputRejectsMissingImageBeforeChangingSessionState(t *testing.T) {
	session := &claudeCodeSession{
		projectPath:     "/tmp/demo",
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	err := session.SubmitInput(Submission{
		Text: "Review this",
		Attachments: []Attachment{{
			Kind: AttachmentLocalImage,
			Path: filepath.Join(t.TempDir(), "missing.png"),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "read image attachment") {
		t.Fatalf("SubmitInput() error = %v, want missing image error", err)
	}
	if session.busy || session.pendingSubmissions != 0 || len(session.entries) != 0 {
		t.Fatalf("failed image submission changed session state: busy=%t pending=%d entries=%#v", session.busy, session.pendingSubmissions, session.entries)
	}
}

func TestClaudeInterruptedResultKeepsRunningWhileSteeredFollowUpRemains(t *testing.T) {
	stdin := &recordingWriteCloser{}
	session := &claudeCodeSession{
		projectPath:        "/tmp/demo",
		busy:               true,
		pendingSubmissions: 2,
		interruptPending:   true,
		stdin:              stdin,
		status:             claudeThinkingStatus,
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`)

	if session.pendingSubmissions != 1 {
		t.Fatalf("pendingSubmissions = %d, want 1", session.pendingSubmissions)
	}
	if session.interruptPending {
		t.Fatalf("interruptPending = true, want false after interrupted result")
	}
	if stdin.closed {
		t.Fatalf("stdin should remain open while steered follow-up remains")
	}
	if session.status != claudeThinkingStatus {
		t.Fatalf("status = %q, want %q", session.status, claudeThinkingStatus)
	}
	if session.lastError != "" {
		t.Fatalf("lastError = %q, want empty after interrupted result", session.lastError)
	}
	if snapshot := session.Snapshot(); snapshot.Phase != SessionPhaseRunning {
		t.Fatalf("snapshot.Phase = %q, want %q", snapshot.Phase, SessionPhaseRunning)
	}
}

func TestClaudeResultMovesSessionToFinishingWhenTurnDrains(t *testing.T) {
	stdin := &recordingWriteCloser{}
	session := &claudeCodeSession{
		projectPath:        "/tmp/demo",
		busy:               true,
		pendingSubmissions: 1,
		stdin:              stdin,
		status:             claudeThinkingStatus,
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
	}

	session.handleClaudeStdoutLine(`{"type":"result","subtype":"success","is_error":false,"result":"done"}`)

	if session.pendingSubmissions != 0 {
		t.Fatalf("pendingSubmissions = %d, want 0", session.pendingSubmissions)
	}
	if !stdin.closed {
		t.Fatalf("stdin should close once the active Claude turn drains")
	}
	if session.stdin != nil {
		t.Fatalf("stdin = %#v, want nil after closing the batch", session.stdin)
	}
	if session.status != claudeFinishingStatus {
		t.Fatalf("status = %q, want %q", session.status, claudeFinishingStatus)
	}
	if snapshot := session.Snapshot(); snapshot.Phase != SessionPhaseFinishing {
		t.Fatalf("snapshot.Phase = %q, want %q", snapshot.Phase, SessionPhaseFinishing)
	}
}

func TestClaudeResultKeepsStreamOpenUntilBackgroundTaskSettles(t *testing.T) {
	stdin := &recordingWriteCloser{}
	session := &claudeCodeSession{
		projectPath:        "/tmp/demo",
		busy:               true,
		pendingSubmissions: 1,
		stdin:              stdin,
		status:             claudeThinkingStatus,
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
		backgroundTasks:    make(map[string]BackgroundTaskSnapshot),
	}

	session.handleClaudeStdoutLine(`{"type":"assistant","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"toolu_bg","name":"Bash","input":{"command":"./telemetry --frames 2900","run_in_background":true}}]}}`)
	session.handleClaudeStdoutLine(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg","content":"background task launched"}]},"tool_use_result":{"backgroundTaskId":"task-bg-1"}}`)
	session.handleClaudeStdoutLine(`{"type":"result","subtype":"success","is_error":false,"result":"waiting for telemetry"}`)

	if session.pendingSubmissions != 0 {
		t.Fatalf("pendingSubmissions = %d, want drained model turn", session.pendingSubmissions)
	}
	if stdin.closed {
		t.Fatal("stdin closed while the provider-declared background task was still running")
	}
	if session.stdin != stdin {
		t.Fatalf("session.stdin = %#v, want stream retained", session.stdin)
	}
	snapshot := session.Snapshot()
	if !snapshot.Busy || snapshot.Phase != SessionPhaseRunning {
		t.Fatalf("snapshot busy=%t phase=%q, want running background work", snapshot.Busy, snapshot.Phase)
	}
	if len(snapshot.BackgroundTasks) != 1 {
		t.Fatalf("background tasks = %#v, want one active task", snapshot.BackgroundTasks)
	}
	task := snapshot.BackgroundTasks[0]
	if task.ID != "task-bg-1" || task.ToolUseID != "toolu_bg" || task.Tool != "Bash" || task.Command != "./telemetry --frames 2900" {
		t.Fatalf("background task = %#v, want structured Bash task metadata", task)
	}
	if !strings.Contains(snapshot.Status, "1 background task") {
		t.Fatalf("snapshot.Status = %q, want visible background-task status", snapshot.Status)
	}

	session.handleClaudeStdoutLine(`{"type":"queue-operation","content":"<task-notification>\n<task-id>task-bg-1</task-id>\n<status>completed</status>\n<summary>Telemetry completed (exit code 0)</summary>\n</task-notification>"}`)

	snapshot = session.Snapshot()
	if len(snapshot.BackgroundTasks) != 0 {
		t.Fatalf("background tasks after completion = %#v, want settled", snapshot.BackgroundTasks)
	}
	if snapshot.Phase != SessionPhaseFinishing {
		t.Fatalf("phase after task completion = %q, want final provider result", snapshot.Phase)
	}
	if stdin.closed {
		t.Fatal("stdin closed before Claude could consume the task notification")
	}

	session.handleClaudeStdoutLine(`{"type":"result","subtype":"success","is_error":false,"result":"telemetry validated"}`)

	if !stdin.closed {
		t.Fatal("stdin remained open after the background task and follow-up model turn settled")
	}
	if session.stdin != nil {
		t.Fatalf("session.stdin = %#v, want nil after final result", session.stdin)
	}
}

func TestClaudeUnexpectedExitReportsUnresolvedBackgroundTask(t *testing.T) {
	session := &claudeCodeSession{
		projectPath: "/tmp/demo",
		started:     true,
		busy:        true,
		status:      claudeThinkingStatus,
		backgroundTasks: map[string]BackgroundTaskSnapshot{
			"task-bg-1": {
				ID:      "task-bg-1",
				Status:  "running",
				Command: "./telemetry --frames 2900",
			},
		},
		backgroundTaskOrder: []string{"task-bg-1"},
		assistantBlocks:     make(map[string]map[string]struct{}),
		toolCalls:           make(map[string]claudeToolCall),
		toolResults:         make(map[string]struct{}),
	}

	session.finishClaudeTurn(nil, nil, nil)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatal("snapshot.Busy = true after provider process exited")
	}
	if snapshot.LastError != claudeBackgroundTaskUnresolved {
		t.Fatalf("LastError = %q, want unresolved-task error", snapshot.LastError)
	}
	if len(snapshot.BackgroundTasks) != 1 || snapshot.BackgroundTasks[0].Status != "unresolved" {
		t.Fatalf("BackgroundTasks = %#v, want visible unresolved task", snapshot.BackgroundTasks)
	}
}

func TestClaudeRefreshMarksUnownedBackgroundTaskUnresolved(t *testing.T) {
	session := &claudeCodeSession{
		claudeHome: t.TempDir(),
		started:    true,
		backgroundTasks: map[string]BackgroundTaskSnapshot{
			"task-bg-1": {
				ID:     "task-bg-1",
				Status: "running",
			},
		},
		backgroundTaskOrder: []string{"task-bg-1"},
		assistantBlocks:     make(map[string]map[string]struct{}),
		toolCalls:           make(map[string]claudeToolCall),
		toolResults:         make(map[string]struct{}),
	}

	if err := session.RefreshBusyElsewhere(); err != nil {
		t.Fatalf("RefreshBusyElsewhere() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatal("snapshot.Busy = true without a live task owner")
	}
	if snapshot.Status != claudeBackgroundTaskUnresolved {
		t.Fatalf("Status = %q, want unresolved-task status", snapshot.Status)
	}
	if len(snapshot.BackgroundTasks) != 1 || snapshot.BackgroundTasks[0].Status != "unresolved" {
		t.Fatalf("BackgroundTasks = %#v, want visible unresolved task", snapshot.BackgroundTasks)
	}
}

func TestClaudeRefreshActiveSetsBusySinceFromPIDSession(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, ".claude")
	sessionsDir := filepath.Join(claudeHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}

	startedAt := time.Date(2026, 3, 31, 9, 12, 0, 0, time.UTC)
	data, err := json.Marshal(map[string]any{
		"pid":       os.Getpid(),
		"sessionId": "ses-demo",
		"startedAt": startedAt.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("marshal pid session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "active.json"), data, 0o644); err != nil {
		t.Fatalf("write pid session: %v", err)
	}

	session := &claudeCodeSession{
		claudeHome:      claudeHome,
		sessionID:       "ses-demo",
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.refreshActiveLocked()

	if !session.busyExternal {
		t.Fatalf("busyExternal = false, want true")
	}
	if !session.busySince.Equal(startedAt) {
		t.Fatalf("busySince = %v, want %v", session.busySince, startedAt)
	}
}

func TestClaudeRefreshActiveUsesStatusUpdateForExternalTurnStart(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, ".claude")
	sessionsDir := filepath.Join(claudeHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}

	processStartedAt := time.Date(2026, 7, 27, 6, 20, 29, 0, time.UTC)
	turnStartedAt := processStartedAt.Add(26 * time.Minute)
	data, err := json.Marshal(map[string]any{
		"pid":             os.Getpid(),
		"sessionId":       "ses-busy",
		"startedAt":       processStartedAt.UnixMilli(),
		"status":          claudePIDStatusBusy,
		"statusUpdatedAt": turnStartedAt.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("marshal pid session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "busy.json"), data, 0o644); err != nil {
		t.Fatalf("write pid session: %v", err)
	}

	session := &claudeCodeSession{
		claudeHome:      claudeHome,
		sessionID:       "ses-busy",
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	session.refreshActiveLocked()
	session.updateStatusLocked()

	if !session.busyExternal {
		t.Fatal("busyExternal = false, want read-only external ownership")
	}
	if !session.externalTurnActive {
		t.Fatal("externalTurnActive = false, want active turn for status=busy")
	}
	if !session.busySince.Equal(turnStartedAt) {
		t.Fatalf("busySince = %v, want status update %v instead of process start %v", session.busySince, turnStartedAt, processStartedAt)
	}
	snapshot := session.Snapshot()
	if !snapshot.Busy || snapshot.Phase != SessionPhaseExternal {
		t.Fatalf("snapshot busy=%t phase=%q, want active external turn", snapshot.Busy, snapshot.Phase)
	}
}

func TestClaudeRefreshActiveKeepsIdleExternalSessionReadOnlyWithoutBusyTimer(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, ".claude")
	sessionsDir := filepath.Join(claudeHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}

	processStartedAt := time.Date(2026, 7, 27, 6, 20, 29, 0, time.UTC)
	turnCompletedAt := processStartedAt.Add(51 * time.Minute)
	data, err := json.Marshal(map[string]any{
		"pid":             os.Getpid(),
		"sessionId":       "ses-idle",
		"startedAt":       processStartedAt.UnixMilli(),
		"status":          claudePIDStatusIdle,
		"statusUpdatedAt": turnCompletedAt.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("marshal pid session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "idle.json"), data, 0o644); err != nil {
		t.Fatalf("write pid session: %v", err)
	}

	session := &claudeCodeSession{
		claudeHome:         claudeHome,
		sessionID:          "ses-idle",
		started:            true,
		busyExternal:       true,
		externalTurnActive: true,
		busySince:          processStartedAt,
		status:             "Claude Code session active in another terminal",
		assistantBlocks:    make(map[string]map[string]struct{}),
		toolCalls:          make(map[string]claudeToolCall),
		toolResults:        make(map[string]struct{}),
	}

	session.refreshActiveLocked()
	session.updateStatusLocked()

	if !session.busyExternal {
		t.Fatal("busyExternal = false, want the live external CLI to remain read-only")
	}
	if session.externalTurnActive {
		t.Fatal("externalTurnActive = true, want status=idle to settle the turn")
	}
	if !session.busySince.IsZero() {
		t.Fatalf("busySince = %v, want cleared after the external turn becomes idle", session.busySince)
	}
	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatal("snapshot.Busy = true, want idle external CLI excluded from running work")
	}
	if !snapshot.BusyExternal {
		t.Fatal("snapshot.BusyExternal = false, want read-only ownership preserved")
	}
	if snapshot.Phase != SessionPhaseIdle {
		t.Fatalf("snapshot.Phase = %q, want %q", snapshot.Phase, SessionPhaseIdle)
	}
	if snapshot.Status != claudeOpenElsewhereStatus {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, claudeOpenElsewhereStatus)
	}
	if err := session.Submit("do not race the external shell"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("Submit() error = %v, want read-only ownership error", err)
	}
}
