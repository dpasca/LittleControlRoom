//go:build !darwin && !linux

package projectrun

import (
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
	return cmd.Process.Kill()
}

func currentProcessGroups() (map[int]int, error) {
	return map[int]int{}, nil
}

func currentListeningPorts() (map[int][]int, error) {
	return map[int][]int{}, nil
}
