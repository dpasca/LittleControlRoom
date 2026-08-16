//go:build darwin || linux

package projectrun

import (
	"os"
	"os/exec"
	"testing"
)

func TestTerminateManagedCommandIgnoresAlreadyExitedProcessGroup(t *testing.T) {
	cmd := &exec.Cmd{Process: &os.Process{Pid: 1 << 30}}
	configureManagedCommand(cmd)

	if err := terminateManagedCommand(cmd); err != nil {
		t.Fatalf("terminate exited command: %v", err)
	}
}
