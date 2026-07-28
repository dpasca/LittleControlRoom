package codexapp

import (
	"fmt"
	"strings"

	"lcroom/internal/claudehook"
)

func claudeSafetyHookSettings(req LaunchRequest) (string, error) {
	executablePath, err := lcrCLIExecutablePath(req)
	if err != nil {
		return "", fmt.Errorf("resolve Little Control Room executable: %w", err)
	}
	if strings.TrimSpace(executablePath) == "" {
		return "", fmt.Errorf("Little Control Room executable path is empty")
	}
	return claudehook.SettingsJSON(executablePath)
}
