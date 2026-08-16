//go:build !darwin && !linux

package projectrun

import (
	"errors"
	"os"
	"os/exec"
)

func configureManagedCommand(cmd *exec.Cmd) {
}

func managedProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func terminateManagedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return ErrNotRunning
	}
	// The process can exit after Manager snapshots it as running but before
	// shutdown reaches Kill. That race is already a successful stop.
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func currentProcessGroups() (map[int]int, error) {
	return map[int]int{}, nil
}

func currentListeningPorts() (map[int][]int, error) {
	return map[int][]int{}, nil
}
