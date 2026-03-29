package codexapp

import (
	"reflect"
	"strings"
	"testing"

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
