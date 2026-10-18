package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/claudecli"
)

func newClaudePermissionModeTestSession(t *testing.T) *claudeCodeSession {
	t.Helper()
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())
	s.requestedPermissionMode, s.permissionMode = claudecli.PermissionModeAuto, claudecli.PermissionModeAuto
	s.mcpOptions.PermissionPromptTool = claudeRuntimeMCPApprovalTool
	return s
}

func TestClaudePermissionModeIdleSession(t *testing.T) {
	s := newClaudePermissionModeTestSession(t)
	for _, mode := range []claudecli.PermissionMode{claudecli.PermissionModeManual, claudecli.PermissionModeAcceptEdits, claudecli.PermissionModeDontAsk, claudecli.PermissionModePlan, claudecli.PermissionModeBypassPermissions, claudecli.PermissionModeAuto} {
		if err := s.SetPermissionMode(string(mode)); err != nil {
			t.Fatal(err)
		}
		if got := s.StateSnapshot().PermissionLevel; got != string(mode) || s.requestedPermissionMode != mode {
			t.Fatalf("mode=%s snapshot=%s requested=%s", mode, got, s.requestedPermissionMode)
		}
		if !strings.Contains(s.lastSystemNotice, "this session") {
			t.Fatal(s.lastSystemNotice)
		}
	}
	if _, err := os.Stat(filepath.Join(s.projectPath, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("mode command wrote local settings: %v", err)
	}
}

func TestClaudePermissionModeRejectsUnavailableChanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*claudeCodeSession)
	}{
		{"closed", func(s *claudeCodeSession) { s.closed = true }},
		{"read-only worker", func(s *claudeCodeSession) { s.readOnlySubagent = true }},
		{"external work", func(s *claudeCodeSession) { s.busyExternal = true }},
		{"external turn", func(s *claudeCodeSession) { s.externalTurnActive = true }},
		{"approval", func(s *claudeCodeSession) { s.pendingApproval = &ApprovalRequest{} }},
		{"question", func(s *claudeCodeSession) { s.pendingToolInput = &ToolInputRequest{} }},
		{"no approval bridge", func(s *claudeCodeSession) { s.mcpOptions.PermissionPromptTool = "" }},
		{"finishing", func(s *claudeCodeSession) { s.cmd = &exec.Cmd{} }},
		{"legacy stream", func(s *claudeCodeSession) { s.cmd, s.stdin = &exec.Cmd{}, &recordingWriteCloser{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newClaudePermissionModeTestSession(t)
			tc.setup(s)
			if err := s.SetPermissionMode("manual"); err == nil {
				t.Fatal("unavailable change succeeded")
			}
			if s.requestedPermissionMode != claudecli.PermissionModeAuto || s.permissionMode != claudecli.PermissionModeAuto {
				t.Fatal("rejected change altered mode")
			}
		})
	}
	for _, mode := range []string{"", "unknown"} {
		if err := newClaudePermissionModeTestSession(t).SetPermissionMode(mode); err == nil {
			t.Fatalf("accepted mode %q", mode)
		}
	}
}

func startClaudePermissionModeTestChange(t *testing.T, ctx context.Context, s *claudeCodeSession) (string, <-chan error) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	s.cmd, s.stdin, s.managedStream = &exec.Cmd{}, writer, true
	s.streamState, s.busy, s.pendingSubmissions = "running", true, 1
	result := make(chan error, 1)
	go func() {
		result <- s.setPermissionMode(ctx, "manual")
		_ = writer.Close()
	}()
	var request struct {
		Type    string `json:"type"`
		ID      string `json:"request_id"`
		Request struct {
			Subtype string `json:"subtype"`
			Mode    string `json:"mode"`
		} `json:"request"`
	}
	if err := json.NewDecoder(reader).Decode(&request); err != nil {
		t.Fatal(err)
	}
	if request.Type != "control_request" || request.Request.Subtype != "set_permission_mode" || request.Request.Mode != "default" || request.ID == "" {
		t.Fatalf("bad control request: %#v", request)
	}
	return request.ID, result
}

func awaitClaudePermissionModeTestResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("permission mode command did not finish")
		return nil
	}
}

func TestClaudePermissionModeLiveAcknowledgment(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "refused"}[success], func(t *testing.T) {
			s := newClaudePermissionModeTestSession(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			id, result := startClaudePermissionModeTestChange(t, ctx, s)
			if got := s.StateSnapshot().PermissionLevel; got != "auto" {
				t.Fatalf("unacknowledged badge=%s", got)
			}
			s.handleClaudeStdoutLine(`{"type":"control_response","response":{"request_id":"unrelated","subtype":"success"}}`)
			select {
			case err := <-result:
				t.Fatalf("unrelated response completed change: %v", err)
			default:
			}
			subtype := "error"
			if success {
				subtype = "success"
			}
			s.handleClaudeStdoutLine(`{"type":"control_response","response":{"request_id":"` + id + `","subtype":"` + subtype + `","error":"managed policy"}}`)
			err := awaitClaudePermissionModeTestResult(t, result)
			if (err == nil) != success {
				t.Fatalf("success=%v err=%v", success, err)
			}
			want := "auto"
			if success {
				want = "manual"
			}
			if got := s.StateSnapshot().PermissionLevel; got != want || string(s.requestedPermissionMode) != want {
				t.Fatalf("badge=%s requested=%s want=%s", got, s.requestedPermissionMode, want)
			}
			if !success && !strings.Contains(err.Error(), "managed policy") {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudePermissionModeTimeoutAndLateAcknowledgment(t *testing.T) {
	s := newClaudePermissionModeTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id, result := startClaudePermissionModeTestChange(t, ctx, s)
	cancel()
	if err := awaitClaudePermissionModeTestResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.SetPermissionMode("auto"); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("repeated change=%v", err)
	}
	s.mu.Lock()
	err := s.submissionStateErrorLocked(claudeSubmissionNormal, nil)
	s.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("submission with unknown mode=%v", err)
	}
	s.handleClaudeStdoutLine(`{"type":"control_response","response":{"request_id":"` + id + `","subtype":"success"}}`)
	if got := s.StateSnapshot().PermissionLevel; got != "manual" {
		t.Fatalf("late acknowledgment badge=%s", got)
	}
}

func TestClaudePermissionModeExitUnblocksWaiter(t *testing.T) {
	s := newClaudePermissionModeTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, result := startClaudePermissionModeTestChange(t, ctx, s)
	s.finishClaudeTurn(nil, nil, nil)
	if err := awaitClaudePermissionModeTestResult(t, result); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("exit result=%v", err)
	}
}

func TestClaudePermissionModeKeepsIdleStreamUntilAcknowledgment(t *testing.T) {
	s, stdin := managedClaudeTestSession()
	change := &claudePermissionModeChange{requestID: "change-mode", mode: claudecli.PermissionModeManual, result: make(chan error, 1)}
	s.permissionModeChange, s.pendingSubmissions = change, 0
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	if stdin.closed || s.stdin == nil {
		t.Fatal("idle stream closed before acknowledging the mode change")
	}
	s.handleClaudeStdoutLine(`{"type":"control_response","response":{"request_id":"change-mode","subtype":"success"}}`)
	if err := awaitClaudePermissionModeTestResult(t, change.result); err != nil {
		t.Fatal(err)
	}
	if !stdin.closed || s.stdin != nil || s.StateSnapshot().PermissionLevel != "manual" {
		t.Fatal("acknowledgment did not release the idle stream in Manual mode")
	}
}

func TestClaudePermissionModeWriteFailure(t *testing.T) {
	s := newClaudePermissionModeTestSession(t)
	reader, writer := io.Pipe()
	_ = reader.Close()
	defer writer.Close()
	s.cmd, s.stdin, s.managedStream = &exec.Cmd{}, writer, true
	if err := s.SetPermissionMode("manual"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write failure=%v", err)
	}
	if s.StateSnapshot().PermissionLevel != "auto" || s.permissionModeChange != nil {
		t.Fatal("failed write changed the mode or left the request pending")
	}
}

func TestClaudePermissionModeTracksNativeFallback(t *testing.T) {
	s := newClaudePermissionModeTestSession(t)
	s.handleClaudeStdoutLine(`{"type":"system","subtype":"status","permissionMode":"default"}`)
	if got := s.StateSnapshot().PermissionLevel; got != "manual" {
		t.Fatalf("fallback badge=%s", got)
	}
}

func TestClaudePermissionRulesReportAndEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	project, home := t.TempDir(), t.TempDir()
	s := newOutputStyleSession(t, home, project)
	s.permissionMode = claudecli.PermissionModeAuto
	if err := s.EditPermissionRule("allow", "Bash(huggingface-cli upload:*)", false); err != nil {
		t.Fatal(err)
	}
	if err := s.ShowPermissions(); err != nil {
		t.Fatal(err)
	}
	text := s.entries[len(s.entries)-1].Text
	for _, want := range []string{"Current mode: Auto", "Bash(huggingface-cli upload:*)", "/reconnect", filepath.Join(project, ".claude", "settings.local.json")} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	s.busy = true
	if err := s.EditPermissionRule("deny", "Bash", false); err == nil {
		t.Fatal("busy edit should fail")
	}
	rules, err := claudecli.ReadPermissionRules(filepath.Join(project, ".claude", "settings.local.json"))
	if err != nil || len(rules["deny"]) != 0 {
		t.Fatalf("busy changed rules: %#v %v", rules, err)
	}
	s.busy = false
	s.closed = true
	if err := s.EditPermissionRule("deny", "Bash", false); err == nil {
		t.Fatal("closed edit should fail")
	}
}

func TestClaudePermissionsUsesConfiguredUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"permissions":{"deny":["Read(.env)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())
	if err := s.ShowPermissions(); err != nil {
		t.Fatal(err)
	}
	if text := s.entries[len(s.entries)-1].Text; !strings.Contains(text, "Read(.env)") {
		t.Fatal(text)
	}
}
