package codexapp

import (
	"fmt"
	"strings"

	"lcroom/internal/claudehook"
)

// claudeSafetyHookExecutable resolves the LCR binary that Claude Code's
// PreToolUse hook invokes. It is resolved once per session so later turns can
// rebuild the settings payload without re-deriving the path.
func claudeSafetyHookExecutable(req LaunchRequest) (string, error) {
	executablePath, err := lcrCLIExecutablePath(req)
	if err != nil {
		return "", fmt.Errorf("resolve Little Control Room executable: %w", err)
	}
	if strings.TrimSpace(executablePath) == "" {
		return "", fmt.Errorf("Little Control Room executable path is empty")
	}
	return executablePath, nil
}

// claudeSafetyHookSettings renders the per-turn `--settings` payload. The
// output style travels here rather than as a CLI flag because Claude Code has
// no --output-style flag; settings JSON is its supported non-interactive path.
func claudeSafetyHookSettings(executablePath, outputStyle string) (string, error) {
	return claudehook.SettingsJSON(executablePath, outputStyle)
}
