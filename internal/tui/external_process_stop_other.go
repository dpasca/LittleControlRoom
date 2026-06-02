//go:build !darwin && !linux

package tui

import (
	"fmt"
	"os"
)

func terminateExternalProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
