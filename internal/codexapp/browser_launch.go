package codexapp

import (
	"os"
	"os/exec"

	"lcroom/internal/browserctl"
)

func applyPlaywrightPolicyEnvironment(cmd *exec.Cmd, provider Provider, policy browserctl.Policy) {
	if cmd == nil {
		return
	}
	cmd.Env = browserctl.AppendEnv(os.Environ(), string(provider.Normalized()), policy)
}
