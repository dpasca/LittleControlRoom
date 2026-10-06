package boss

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/control"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestCollaborationConfirmationShowsPromptOnStandardTerminal(t *testing.T) {
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: "/projects/crypto", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "session", Prompt: "Fix the build incompatibility."})
	view, err := RenderCollaborationConfirmationDialog(control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}, "/projects/crypto_desk", false, "", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"always allow this pair", "Fix the build incompatibility.", "crypto_desk", "/collab", "Enter sends only this message.", "a also allows future messages"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %s", want, view)
		}
	}
}

func TestCollaborationConfirmationKeepsActionsVisible(t *testing.T) {
	inv := validatedControlInvocationForTest(t, control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{
		ProjectPath:     "/projects/crypto",
		Provider:        control.ProviderCodex,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: "session",
		Prompt:          strings.Repeat("Review the build and continue the authorized work.\n", 30),
	})
	for _, size := range [][2]int{{110, 40}, {80, 24}, {60, 18}, {40, 14}} {
		for _, state := range []struct {
			name, errorText string
			busy            bool
		}{
			{name: "ready"},
			{name: "saving", busy: true},
			{name: "error", errorText: "Approval failed: disk failure"},
		} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], state.name), func(t *testing.T) {
				view, err := RenderCollaborationConfirmationDialog(inv, "/projects/crypto_desk", state.busy, state.errorText, size[0], size[1])
				if err != nil {
					t.Fatal(err)
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
					t.Fatalf("dialog exceeds terminal bounds: %s", view)
				}
				plain := ansi.Strip(view)
				separator := strings.Index(plain, "│ ────")
				if separator < 0 {
					t.Fatalf("missing action bar separator: %s", plain)
				}
				footer := strings.Join(strings.Fields(plain[separator:]), " ")
				for _, want := range []string{"Enter send once", "a always allow this pair", "Esc cancel"} {
					if !strings.Contains(footer, want) {
						t.Fatalf("action bar missing %q: %s", want, plain)
					}
				}
				if state.busy && !strings.Contains(footer, "Saving collaboration approval...") {
					t.Fatalf("missing saving status: %s", plain)
				}
				if state.errorText != "" && !strings.Contains(footer, state.errorText) {
					t.Fatalf("missing failure status: %s", plain)
				}
			})
		}
	}
}

func TestWorktreeRemoveConfirmationShowsDestructiveScope(t *testing.T) {
	inv := validatedControlInvocationForTest(t, control.CapabilityWorktreeRemove, control.WorktreeRemoveInput{WorktreePath: "/projects/demo--task"})
	view, err := RenderControlConfirmationDialog(inv, "An agent asked for cleanup", 100, 32)
	if err != nil {
		t.Fatal(err)
	}
	view = strings.Join(strings.Fields(ansi.Strip(view)), " ")
	for _, want := range []string{"Delete Worktree", "/projects/demo--task", "Permanently delete", "uncommitted files", "no recovery archive", "conversation history", "not stopped", "Enter", "delete", "Esc"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q: %s", want, view)
		}
	}
	content, err := controlConfirmationContent(inv)
	if err != nil || !strings.Contains(content, "Permanently delete") || !strings.Contains(content, "/projects/demo--task") {
		t.Fatalf("Chat confirmation lost removal scope: %s, %v", content, err)
	}
}

func TestWorktreeRemoveConfirmationNeverElidesLongTarget(t *testing.T) {
	path := "/projects/" + strings.Repeat("long-parent-", 9) + "/repo--unique-target"
	input := control.WorktreeRemoveInput{WorktreePath: path}
	content := ansi.Strip(renderWorktreeRemoveConfirmation(input, 76))
	if !strings.Contains(strings.ReplaceAll(content, "\n", ""), path) {
		t.Fatalf("target was abbreviated: %s", content)
	}
	inv := validatedControlInvocationForTest(t, control.CapabilityWorktreeRemove, input)
	view, err := RenderControlConfirmationDialog(inv, "", 80, 24)
	joined := strings.NewReplacer(" ", "", "\n", "", "│", "").Replace(ansi.Strip(view))
	if err != nil || !strings.Contains(joined, path) || !strings.Contains(view, "no recovery archive") || !strings.Contains(view, "delete") {
		t.Fatalf("long target confirmation clipped critical details: %s, %v", view, err)
	}
}
