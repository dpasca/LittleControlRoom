//go:build !darwin && !linux

package codexapp

import (
	"errors"
	"os"
	"os/exec"
)

func configureAppServerCommand(cmd *exec.Cmd) {
}

func terminateAppServerCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
