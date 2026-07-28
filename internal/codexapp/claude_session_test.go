package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	var conversationTracker claudeartifact.ConversationTracker

	assistantEntries, entryType, reasoningEffort := parseCCLineEntries(`{"type":"assistant","uuid":"msg_1","effort":"xhigh","message":{"role":"assistant","content":[{"type":"text","text":"Checking logs."},{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"refresh"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"make test"}}]}}`, toolCalls, toolResults, &conversationTracker)
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

	userEntries, entryType, reasoningEffort := parseCCLineEntries(`{"type":"user","uuid":"msg_2","parentUuid":"msg_1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"tests passed"}]}}`, toolCalls, toolResults, &conversationTracker)
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

func TestClaudeTurnArgsAddRuntimeMCPWithoutReplacingUserServers(t *testing.T) {
	const (
		config = `{"mcpServers":{"lcr_runtime":{"type":"stdio","command":"/tmp/lcroom","args":["runtime-mcp"]}}}`
		prompt = "Follow the shared LCR TODO capture policy."
	)
	got := claudeTurnArgsWithRuntimeMCP("ses-demo", "sonnet", "high", "acceptEdits", config, prompt)
	want := []string{
		"-p",
		"--verbose",
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--permission-mode", "acceptEdits",
		"--resume", "ses-demo",
		"--model", "sonnet",
		"--effort", "high",
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
	got := claudeTurnArgsWithRuntimeMCP("", "", "", "acceptEdits", "  ", "ignored instructions")
	want := claudeTurnArgs("", "", "", "acceptEdits")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithRuntimeMCP() = %#v, want %#v", got, want)
	}
}

func TestClaudeTurnArgsKeepRuntimeMCPWithoutTODOPreapproval(t *testing.T) {
	const config = `{"mcpServers":{"lcr_runtime":{"type":"stdio","command":"/tmp/lcroom"}}}`
	got := claudeTurnArgsWithRuntimeMCP("", "", "", "acceptEdits", config, "")
	want := append(
		claudeTurnArgs("", "", "", "acceptEdits"),
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
