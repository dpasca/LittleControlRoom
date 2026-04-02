package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexcli"
)

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

	session.handleClaudeStdoutLine(`{"type":"assistant","session_id":"ses-demo","message":{"id":"msg_1","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"npm test"}}]}}`)
	if len(session.entries) != 1 {
		t.Fatalf("entry count after tool_use = %d, want 1", len(session.entries))
	}
	if session.entries[0].Kind != TranscriptTool {
		t.Fatalf("tool_use entry kind = %q, want %q", session.entries[0].Kind, TranscriptTool)
	}
	if session.entries[0].Text != "Bash: npm test" {
		t.Fatalf("tool_use entry text = %q, want Bash summary", session.entries[0].Text)
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

func TestParseCCLineEntriesRebuildsStructuredToolEntries(t *testing.T) {
	toolCalls := make(map[string]claudeToolCall)
	toolResults := make(map[string]struct{})

	assistantEntries, entryType := parseCCLineEntries(`{"type":"assistant","uuid":"msg_1","message":{"role":"assistant","content":[{"type":"text","text":"Checking logs."},{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"refresh"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"make test"}}]}}`, toolCalls, toolResults)
	if entryType != "assistant" {
		t.Fatalf("assistant entry type = %q, want assistant", entryType)
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

	userEntries, entryType := parseCCLineEntries(`{"type":"user","uuid":"msg_2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"tests passed"}]}}`, toolCalls, toolResults)
	if entryType != "user" {
		t.Fatalf("user entry type = %q, want user", entryType)
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

func TestClaudeListModelsIncludesAliasesAndCurrentModel(t *testing.T) {
	session := &claudeCodeSession{
		model:        "claude-sonnet-4-6",
		pendingModel: "claude-opus-4-6",
	}

	models, err := session.ListModels()
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) < 3 {
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

func TestClaudeSnapshotIncludesBusySinceForInternalTurn(t *testing.T) {
	startedAt := time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC)
	session := &claudeCodeSession{
		projectPath:     "/tmp/demo",
		started:         true,
		busy:            true,
		busySince:       startedAt,
		status:          claudeThinkingStatus,
		assistantBlocks: make(map[string]map[string]struct{}),
		toolCalls:       make(map[string]claudeToolCall),
		toolResults:     make(map[string]struct{}),
	}

	snapshot := session.Snapshot()
	if !snapshot.BusySince.Equal(startedAt) {
		t.Fatalf("snapshot.BusySince = %v, want %v", snapshot.BusySince, startedAt)
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
