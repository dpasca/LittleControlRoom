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

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	_ = waitForLCAgentIdleSnapshot(t, session, notify)
	if err := waitForProcessExit(childPID, 5*time.Second); err != nil {
		t.Fatalf("background child still alive after LCAgent interrupt: %v", err)
	}
	childPID = 0
}
