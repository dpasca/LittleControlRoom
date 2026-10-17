package boss

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/todocontrol/ownertest"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpChatPlainTodoAddShowsRepositoryOwnerAndEffectsOnShortScreen(t *testing.T) {
	f := ownertest.New(t)
	args, _ := json.Marshal(control.TodoAddInput{ProjectPath: f.Caller, Text: strings.Repeat("task ", 100)})
	inv, err := control.BuildProposedInvocation("", control.CapabilityTodoAdd, args)
	if err != nil {
		t.Fatal(err)
	}
	a := &Assistant{controlStore: f.Store}
	response, err := a.inspectTodoProposal(t.Context(), AssistantResponse{ControlInvocation: &inv, Content: "Original operator scope"}, nil)
	if err != nil || response.ControlInvocation == nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var input control.TodoAddInput
	if err := json.Unmarshal(response.ControlInvocation.Args, &input); err != nil {
		t.Fatal(err)
	}
	if input.ProjectPath != f.Caller || input.TodoOwnerPath != f.Root {
		t.Fatalf("input=%+v", input)
	}
	if !strings.Contains(response.Content, "Original operator scope") {
		t.Fatal("original scope lost")
	}
	var reviewed strings.Builder
	op := control.Operation{Source: "chat", Invocation: *response.ControlInvocation}
	for offset := 0; ; offset++ {
		panel, maxScroll, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{ScrollOffset: offset, Preview: response.Content}, 64, 18)
		if err != nil {
			t.Fatal(err)
		}
		plain := ansi.Strip(panel)
		if lipgloss.Width(panel) > 64 || lipgloss.Height(panel) > 18 || !strings.Contains(plain, "Enter") || !strings.Contains(plain, "Esc") {
			t.Fatalf("clipped confirmation: %s", plain)
		}
		reviewed.WriteString(plain)
		if offset >= maxScroll {
			break
		}
	}
	text := reviewed.String()
	if strings.Contains(text, "resume_on_success") {
		t.Fatal("confirmation exposes API argument wording")
	}
	for _, want := range []string{"Repository TODO owner:", "Requested checkout:", "no worktree or engineer", "add"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

func TestTodoLaunchConfirmationExposesOwnerDestinationEffectsAndContinuationOnShortScreen(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, existing := range []int64{0, 1618} {
			input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "dialog", ProjectPath: "/repos/root--release", TodoOwnerPath: "/repos/root", ProjectName: strings.Repeat("long-name-", 15), TodoID: existing, TodoText: strings.Repeat("task ", 100), Prompt: strings.Repeat("implement ", 100), Provider: control.ProviderCodex}
			args, _ := json.Marshal(input)
			op := control.Operation{ProjectPath: "/repos/root--caller", Provider: "codex", SessionKey: "caller", ResumeOnSuccess: resume, Invocation: control.Invocation{RequestID: "dialog", Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args}}
			var reviewed strings.Builder
			for offset := 0; ; offset++ {
				panel, maxScroll, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{ScrollOffset: offset}, 64, 18)
				if err != nil {
					t.Fatal(err)
				}
				if lipgloss.Width(panel) > 64 || lipgloss.Height(panel) > 18 {
					t.Fatalf("panel dimensions=%dx%d", lipgloss.Width(panel), lipgloss.Height(panel))
				}
				plain := ansi.Strip(panel)
				if !strings.Contains(plain, "Enter") || !strings.Contains(plain, "Esc") {
					t.Fatalf("actions clipped: %s", plain)
				}
				reviewed.WriteString(plain)
				if offset >= maxScroll {
					break
				}
			}
			text := reviewed.String()
			for _, want := range []string{"Repository TODO owner:", "/repos/root", "Worker destination:", "not yet fixed", "separate worktree", "start one engineer"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			continuation := "disabled"
			if resume {
				continuation = "enabled after success"
			}
			if !strings.Contains(text, continuation) {
				t.Fatalf("continuation missing: %s", text)
			}
			if existing > 0 && (!strings.Contains(text, "Use existing TODO #1618") || !strings.Contains(text, "no new TODO")) {
				t.Fatalf("existing TODO semantics missing: %s", text)
			}
			if existing == 0 && !strings.Contains(text, "Create one repository TODO") {
				t.Fatalf("new TODO effect missing: %s", text)
			}
			busy, _, err := RenderPermissionConfirmationDialog(op, PermissionConfirmationOptions{Busy: true}, 64, 18)
			if err != nil || !strings.Contains(ansi.Strip(busy), "Saving approval") {
				t.Fatalf("busy state=%s err=%v", busy, err)
			}
		}
	}
}

func TestHelpChatRepairPreflightBindsHostIdentityAndReviewedRevision(t *testing.T) {
	f := ownertest.New(t)
	todo, err := f.Store.AddTodo(t.Context(), f.Caller, "incorrect owner")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.TodoRepairOwnerInput{TodoID: todo.ID, ExpectedOwnerPath: f.Caller, RepositoryPath: f.Root})
	inv, err := control.BuildProposedInvocation("", control.CapabilityTodoRepairOwner, args)
	if err != nil {
		t.Fatal(err)
	}
	a := &Assistant{controlStore: f.Store}
	response, err := a.inspectTodoProposal(t.Context(), AssistantResponse{ControlInvocation: &inv, Content: "Original operator scope"}, nil)
	if err != nil || response.ControlInvocation == nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var bound control.TodoRepairOwnerInput
	json.Unmarshal(response.ControlInvocation.Args, &bound)
	if bound.RequestID == "" || control.IsExternalOperationID(bound.RequestID) || bound.ExpectedRevision == "" {
		t.Fatalf("unusable host repair=%+v", bound)
	}
	if !strings.Contains(response.Content, "Original operator scope") || !strings.Contains(response.Content, f.Root) {
		t.Fatalf("lost preview scope: %s", response.Content)
	}
	if err := f.Store.UpdateTodo(t.Context(), todo.ID, "changed during review"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.inspectTodoProposal(t.Context(), response, nil); err == nil {
		t.Fatal("stale repair review accepted")
	}
}
