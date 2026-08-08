package claudehook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"lcroom/internal/commandguard"
)

const (
	// Subcommand is intentionally handled before normal CLI configuration so a
	// Claude Code hook can invoke the running Little Control Room executable.
	Subcommand = "claude-pretooluse-hook"

	maxHookInputBytes = 1 << 20
)

type settings struct {
	Attribution attribution            `json:"attribution"`
	Hooks       map[string][]hookGroup `json:"hooks"`
}

type attribution struct {
	Commit string `json:"commit"`
	PR     string `json:"pr"`
}

type hookGroup struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookHandler `json:"hooks"`
}

type hookHandler struct {
	Type          string   `json:"type"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	Timeout       int      `json:"timeout"`
	StatusMessage string   `json:"statusMessage"`
}

type preToolUseInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// SettingsJSON returns additive Claude Code settings that disable automatic
// commit and pull-request attribution and register an LCR-owned Bash PreToolUse
// hook. Exec-form args avoid passing the executable path through a shell.
func SettingsJSON(executablePath string) (string, error) {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return "", fmt.Errorf("Little Control Room executable path is empty")
	}
	data, err := json.Marshal(settings{
		Attribution: attribution{},
		Hooks: map[string][]hookGroup{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []hookHandler{
						{
							Type:          "command",
							Command:       executablePath,
							Args:          []string{Subcommand},
							Timeout:       5,
							StatusMessage: "Checking Little Control Room command safety...",
						},
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode Claude Code hook settings: %w", err)
	}
	return string(data), nil
}

// Run evaluates one Claude Code PreToolUse hook payload. Exit code 2 is
// Claude's blocking result for PreToolUse hooks; malformed or unexpected input
// also fails closed so a broken integration cannot silently disable the guard.
func Run(input io.Reader, errorOutput io.Writer) int {
	var event preToolUseInput
	decoder := json.NewDecoder(io.LimitReader(input, maxHookInputBytes))
	if err := decoder.Decode(&event); err != nil {
		return deny(errorOutput, fmt.Sprintf("invalid Claude Code safety-hook input: %v", err))
	}
	if strings.TrimSpace(event.HookEventName) != "PreToolUse" ||
		!strings.EqualFold(strings.TrimSpace(event.ToolName), "Bash") {
		return deny(errorOutput, "unexpected Claude Code safety-hook event")
	}
	command := strings.TrimSpace(event.ToolInput.Command)
	if command == "" {
		return deny(errorOutput, "Claude Code Bash command was empty")
	}
	if commandguard.ContainsRecursiveRM(command) {
		return deny(errorOutput, commandguard.RecursiveRMDenialReason)
	}
	return 0
}

func deny(output io.Writer, reason string) int {
	if output != nil {
		_, _ = fmt.Fprintf(output, "Blocked by Little Control Room: %s\n", reason)
	}
	return 2
}
