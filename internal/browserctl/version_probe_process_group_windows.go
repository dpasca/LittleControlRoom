//go:build windows

package browserctl

import "os/exec"

func configureVersionProbeProcessGroup(*exec.Cmd) {}

func bestEffortKillVersionProbeProcessGroup(*exec.Cmd) {}
