//go:build darwin || linux

package codexapp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfigureAppServerCommandSetsProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")

	configureAppServerCommand(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("configureAppServerCommand should put codex app-server in its own process group")
	}
}

func TestTerminateAppServerCommandKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $! > \"$1\"; wait", "sh", pidFile)
	configureAppServerCommand(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() {
		if childPID <= 0 {
			return
		}
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	if err := terminateAppServerCommand(cmd); err != nil {
		t.Fatalf("terminateAppServerCommand() error = %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("cmd.Wait() timed out after process-group kill")
	}

	if err := waitForProcessExit(childPID, 5*time.Second); err != nil {
		t.Fatalf("background child still alive after process-group kill: %v", err)
	}
	childPID = 0
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for child pid file %s", path)
	return 0
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}

	return errors.New("process did not exit before timeout")
}
