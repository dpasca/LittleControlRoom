//go:build windows

package browserctl

import "os/exec"

func configureIsolatedProcessGroup(*exec.Cmd) {}

func bestEffortKillProcessGroup(*exec.Cmd) {}
