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
		for _, want := range []string{"1 Just this once", "2 For this Codex session", "3 Always, messages both ways: caller ↔ target", "Enter confirm", "Esc deny"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("height %d missing %q: %s", height, want, plain)
			}
		}
		if strings.Contains(plain, "Always, from any") {
			t.Fatalf("height %d offers two kinds of always: %s", height, plain)
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
		if !strings.Contains(plain, "1 Just this once") {
			t.Fatal("scrolling hid permission choices")
		}
	}
	for _, want := range []string{"Current defaults", "worker", "Last prompt line"} {
		if !strings.Contains(readable, want) {
			t.Fatalf("cannot review %q on a short screen", want)
		}
	}
}

func TestPermissionChoicesExplainTheHighlightedScope(t *testing.T) {
	args, _ := json.Marshal(control.TodoAddInput{ProjectPath: "/projects/webshop", Text: "Track progress"})
	op := control.Operation{Capability: control.CapabilityTodoAdd, ProjectPath: "/projects/webshop", Provider: "codex", SessionKey: "key", Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args}}
	if got := PermissionChoices(op, ""); len(got) != 3 || got[2] != PermissionAllowAlways {
		t.Fatalf("choices = %v", got)
	}
	if got := PermissionChoices(op, "needs a fetch"); len(got) != 1 {
		t.Fatalf("unavailable standing permission still offered: %v", got)
	}
	for selected, want := range []string{"asks again next time", "from this Codex session run without asking", "future Codex sessions in this project"} {
		view, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{Selected: selected}, 100, 40)
		if err != nil {
			t.Fatal(err)
		}
		plain := strings.Join(strings.Fields(ansi.Strip(view)), " ")
		if !strings.Contains(plain, "3 Always, from any Codex session in webshop") || !strings.Contains(plain, want) {
			t.Fatalf("selection %d missing %q: %s", selected, want, plain)
		}
	}
}
