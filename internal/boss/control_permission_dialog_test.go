package boss

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/control"
)

func TestPermissionChoicesRemainVisibleWithLongPrompts(t *testing.T) {
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: "/target", Provider: control.ProviderCodex, TargetSessionID: "worker", SessionMode: control.SessionModeResumeOrNew, Prompt: strings.Repeat("Review and continue. ", 300)})
	op := control.Operation{Capability: control.CapabilityEngineerSendPrompt, ProjectPath: "/caller", Provider: "codex", SessionKey: "key", ResumeOnSuccess: true, Invocation: control.Invocation{RequestID: "op", Capability: control.CapabilityEngineerSendPrompt, Args: args}}
	for _, height := range []int{18, 24, 40} {
		view, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{}, 80, height)
		if err != nil {
			t.Fatal(err)
		}
		plain := strings.Join(strings.Fields(ansi.Strip(view)), " ")
		for _, want := range []string{"Enter allow once", "s allow for session", "p save permission", "a always allow this pair", "Esc cancel"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("height %d missing %q: %s", height, want, plain)
			}
		}
	}
}

func TestPermissionDetailsCanBeReadOnShortScreens(t *testing.T) {
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: "/target", Provider: control.ProviderCodex, TargetSessionID: "worker", SessionMode: control.SessionModeResumeOrNew, Prompt: strings.Repeat("Review and continue.\n", 60) + "Last prompt line"})
	op := control.Operation{Capability: control.CapabilityEngineerSendPrompt, ProjectPath: "/caller", Provider: "codex", SessionKey: "key", Invocation: control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}}
	summary, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{}, 80, 60)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, "Full request details") || strings.Contains(summary, `"prompt":`) {
		t.Fatal("raw JSON is visible by default")
	}
	expanded, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{ShowDetails: true}, 80, 60)
	if err != nil || lipgloss.Height(summary) >= lipgloss.Height(expanded) {
		t.Fatal("hiding JSON did not shrink the dialog", err)
	}
	_, limit, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{ShowDetails: true}, 80, 18)
	if err != nil || limit == 0 {
		t.Fatalf("missing scrollable content: %d %v", limit, err)
	}
	readable := ""
	for offset := 0; offset <= limit; offset++ {
		view, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{ScrollOffset: offset, ShowDetails: true}, 80, 18)
		if err != nil {
			t.Fatal(err)
		}
		plain := strings.Join(strings.Fields(ansi.Strip(view)), " ")
		readable += plain + "\n"
		if !strings.Contains(plain, "p save permission") {
			t.Fatal("scrolling hid permission choices")
		}
	}
	for _, want := range []string{"Current defaults", "worker", "Last prompt line"} {
		if !strings.Contains(readable, want) {
			t.Fatalf("cannot review %q on a short screen", want)
		}
	}
}
