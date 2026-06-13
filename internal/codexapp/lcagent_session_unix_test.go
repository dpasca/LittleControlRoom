//go:build darwin || linux

package codexapp

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLCAgentSessionInterruptTerminatesProcessGroup(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	sessionID := "lca_interrupt_session"
	started := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	tracePath := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatalf("create fake trace dir: %v", err)
	}
	trace := `{"type":"session_meta","id":"` + sessionID + `","started_at":"` + started.Format(time.RFC3339Nano) + `","cwd":"` + filepath.ToSlash(root) + `","auto":"low","model":"fake"}
{"type":"user_message","timestamp":"` + started.Add(time.Second).Format(time.RFC3339Nano) + `","session_id":"` + sessionID + `","message":"wait until interrupted"}
`
	if err := os.WriteFile(tracePath, []byte(trace), 0o600); err != nil {
		t.Fatalf("write fake trace: %v", err)
	}

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
printf '%s\n' '{"type":"session_meta","id":"lca_interrupt_session","cwd":"/tmp/demo"}'
sleep 30 &
echo $! > "$LCAGENT_CHILD_PID_FILE"
wait
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_CHILD_PID_FILE", pidFile)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           root,
		AppDataDir:            dataDir,
		LCAgentPath:           exe,
		LCAgentProvider:       "openai",
		LCAgentRequestTimeout: time.Minute,
		Prompt:                "wait until interrupted",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() {
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})
	waitForLCAgentThreadID(t, session, notify, sessionID)

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	_ = waitForLCAgentIdleSnapshot(t, session, notify)
	if err := waitForProcessExit(childPID, 5*time.Second); err != nil {
		t.Fatalf("background child still alive after LCAgent interrupt: %v", err)
	}
	childPID = 0

	parsed, err := ParseLCAgentTraceFile(tracePath)
	if err != nil {
		t.Fatalf("ParseLCAgentTraceFile() error = %v", err)
	}
	if !parsed.Aborted || len(parsed.Errors) == 0 || parsed.Errors[len(parsed.Errors)-1] != "interrupted" {
		t.Fatalf("trace abort state = aborted:%v errors:%#v, want appended interrupted marker", parsed.Aborted, parsed.Errors)
	}
}

func waitForLCAgentThreadID(t *testing.T, session Session, notify <-chan struct{}, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if got := session.Snapshot().ThreadID; got == want {
			return
		}
		select {
		case <-notify:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for LCAgent thread %q, snapshot=%#v", want, session.Snapshot())
		}
	}
}
