//go:build darwin || linux

package tui

import (
	"errors"
	"fmt"
	"syscall"
)

func terminateExternalProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
