package codexapp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/browserctl"
	"lcroom/internal/claudecli"
)

func TestClaudeAuthenticationErrorsOfferConnectionRecovery(t *testing.T) {
	t.Parallel()
	const line = `{"type":"assistant","uuid":"msg-auth-error","error":"authentication_failed","isApiErrorMessage":true,"message":{"id":"msg-auth-error","role":"assistant","content":[{"type":"text","text":"Failed to authenticate: OAuth token revoked"}]}}`
	for _, persisted := range []bool{false, true} {
		session := &claudeCodeSession{
			assistantBlocks: make(map[string]map[string]struct{}),
			toolCalls:       make(map[string]claudeToolCall),
			toolResults:     make(map[string]struct{}),
		}
		if persisted {
			session.sessionFile = filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(session.sessionFile, []byte(line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := session.loadTranscriptLocked(); err != nil {
				t.Fatal(err)
			}
		} else {
			session.handleClaudeStdoutLine(line)
		}
		entries := session.Snapshot().Entries
		if len(entries) != 1 || entries[0].Kind != TranscriptError || entries[0].Text != "Failed to authenticate: OAuth token revoked" {
			t.Fatalf("provider auth failure was not preserved (persisted=%v)", persisted)
		}
		if !strings.Contains(entries[0].DisplayText, entries[0].Text) || !strings.Contains(entries[0].DisplayText, "/claude-auth") {
			t.Fatalf("auth failure has no connection recovery action (persisted=%v)", persisted)
		}
	}
}

func TestClaudeLaunchRetainsSavedTokenAfterEnvironmentDecorators(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nprintf '%s' \"$CLAUDE_CODE_OAUTH_TOKEN\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv(claudecli.SubscriptionTokenEnv, "")
	previous := applyClaudeSubscriptionToken
	t.Cleanup(func() { applyClaudeSubscriptionToken = previous })
	const token = "sk-ant-oat01-saved-fixture"
	applyClaudeSubscriptionToken = func(cmd *exec.Cmd) error {
		// Simulate a saved credential, rather than an inherited variable that
		// would survive an accidental os.Environ() reset in a later decorator.
		cmd.Env = append(cmd.Env, claudecli.SubscriptionTokenEnv+"="+token)
		return nil
	}
	for _, managed := range []bool{false, true} {
		cmd, stdin, stdout, stderr, err := startClaudeTurnWithMCP(context.Background(), t.TempDir(), "", "", "", "default", browserctl.Policy{}, claudeMCPOptions{}, `{}`, managed)
		if err != nil {
			t.Fatal(err)
		}
		_ = stdin.Close()
		output, readErr := io.ReadAll(stdout)
		_, _ = io.Copy(io.Discard, stderr)
		_ = stdout.Close()
		_ = stderr.Close()
		if err := cmd.Wait(); err != nil || readErr != nil || string(output) != token {
			t.Fatalf("saved token lost during launch (managed=%v): wait=%v read=%v", managed, err, readErr)
		}
	}
	cmd := newClaudeModelCatalogCommand(context.Background())
	output, err := cmd.Output()
	if err != nil || string(output) != token {
		t.Fatalf("saved token lost during model discovery: %v", err)
	}
}
