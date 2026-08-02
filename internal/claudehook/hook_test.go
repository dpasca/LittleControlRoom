package claudehook

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/commandguard"
)

func TestRunBlocksDirectRMInClaudeBashTool(t *testing.T) {
	tests := []string{
		`rm -rf build/dev && cmake --preset dev`,
		`sudo -n env MODE=clean zsh -c 'rm -fr "$TARGET"'`,
		`echo "$(rm -rf build)"`,
	}
	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			var stderr strings.Builder
			code := Run(strings.NewReader(hookPayload(t, "PreToolUse", "Bash", command)), &stderr)
			if code != 2 {
				t.Fatalf("Run() code = %d, want Claude blocking code 2", code)
			}
			if !strings.Contains(stderr.String(), commandguard.DirectRMDenialReason) {
				t.Fatalf("Run() stderr = %q, want direct-rm denial", stderr.String())
			}
		})
	}
}

func TestRunAllowsNonRMCommandAndQuotedExample(t *testing.T) {
	for _, command := range []string{
		`cmake --build --preset dev`,
		`printf '%s\n' 'rm -rf /'`,
		`rg 'rm -rf' README.md`,
	} {
		t.Run(command, func(t *testing.T) {
			var stderr strings.Builder
			code := Run(strings.NewReader(hookPayload(t, "PreToolUse", "Bash", command)), &stderr)
			if code != 0 {
				t.Fatalf("Run() code = %d, want 0; stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestRunFailsClosedForMalformedOrUnexpectedInput(t *testing.T) {
	tests := []string{
		`not-json`,
		hookPayload(t, "PostToolUse", "Bash", "go test ./..."),
		hookPayload(t, "PreToolUse", "Write", "go test ./..."),
		hookPayload(t, "PreToolUse", "Bash", ""),
	}
	for _, input := range tests {
		var stderr strings.Builder
		if code := Run(strings.NewReader(input), &stderr); code != 2 {
			t.Fatalf("Run(%q) code = %d, want 2", input, code)
		}
		if !strings.Contains(stderr.String(), "Blocked by Little Control Room") {
			t.Fatalf("Run(%q) stderr = %q, want blocking reason", input, stderr.String())
		}
	}
}

func TestSettingsJSONRegistersExecFormBashPreToolUseHook(t *testing.T) {
	const executable = "/Applications/Little Control Room/lcroom"
	raw, err := SettingsJSON(executable)
	if err != nil {
		t.Fatalf("SettingsJSON() error = %v", err)
	}

	var got settings
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("unmarshal settings document: %v", err)
	}
	if _, ok := document["attribution"]; !ok {
		t.Fatal("settings omitted attribution policy")
	}
	if got.Attribution.Commit != "" || got.Attribution.PR != "" {
		t.Fatalf("attribution = %#v, want commit and PR attribution disabled", got.Attribution)
	}
	groups := got.Hooks["PreToolUse"]
	if len(groups) != 1 || groups[0].Matcher != "Bash" || len(groups[0].Hooks) != 1 {
		t.Fatalf("PreToolUse settings = %#v, want one Bash hook", groups)
	}
	hook := groups[0].Hooks[0]
	if hook.Type != "command" || hook.Command != executable {
		t.Fatalf("hook executable = %#v, want exec-form %q", hook, executable)
	}
	if len(hook.Args) != 1 || hook.Args[0] != Subcommand {
		t.Fatalf("hook args = %#v, want [%q]", hook.Args, Subcommand)
	}
	if hook.Timeout <= 0 {
		t.Fatalf("hook timeout = %d, want positive timeout", hook.Timeout)
	}
}

func TestSettingsJSONRequiresExecutable(t *testing.T) {
	if _, err := SettingsJSON("  "); err == nil {
		t.Fatal("SettingsJSON() error = nil, want empty executable rejection")
	}
}

func hookPayload(t *testing.T, event, tool, command string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"hook_event_name": event,
		"tool_name":       tool,
		"tool_input": map[string]any{
			"command": command,
		},
	})
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}
	return string(data)
}
