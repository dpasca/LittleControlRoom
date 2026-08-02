package codexapp

import (
	"encoding/json"
	"testing"

	"lcroom/internal/claudehook"
)

func TestClaudeSafetyHookSettingsUsesLaunchExecutable(t *testing.T) {
	const executable = "/Applications/Little Control Room/lcroom"
	raw, err := claudeSafetyHookSettings(LaunchRequest{CLIExecutablePath: executable})
	if err != nil {
		t.Fatalf("claudeSafetyHookSettings() error = %v", err)
	}

	var settings struct {
		Attribution *struct {
			Commit string `json:"commit"`
			PR     string `json:"pr"`
		} `json:"attribution"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("unmarshal Claude safety settings: %v", err)
	}
	if settings.Attribution == nil || settings.Attribution.Commit != "" || settings.Attribution.PR != "" {
		t.Fatalf("attribution = %#v, want commit and PR attribution disabled", settings.Attribution)
	}
	groups := settings.Hooks["PreToolUse"]
	if len(groups) != 1 || groups[0].Matcher != "Bash" || len(groups[0].Hooks) != 1 {
		t.Fatalf("PreToolUse settings = %#v, want one Bash hook", groups)
	}
	hook := groups[0].Hooks[0]
	if hook.Command != executable {
		t.Fatalf("hook command = %q, want %q", hook.Command, executable)
	}
	if len(hook.Args) != 1 || hook.Args[0] != claudehook.Subcommand {
		t.Fatalf("hook args = %#v, want [%q]", hook.Args, claudehook.Subcommand)
	}
}
