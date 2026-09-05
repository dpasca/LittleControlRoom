package integrations

import "os/exec"

func configureProbeProcess(cmd *exec.Cmd) {}
func stopProbeProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
