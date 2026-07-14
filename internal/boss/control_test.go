package boss

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lcroom/internal/control"
)

func TestControlProposalFromBossActionBuildsNewRepositoryInvocation(t *testing.T) {
	action := bossAction{
		Kind:              bossActionProposeControl,
		ControlCapability: string(control.CapabilityProjectCreateAndStartEngineer),
		RequestID:         " new-project-1 ",
		ProjectParentPath: " /tmp/repos ",
		ProjectName:       " KeyMaster ",
		TodoText:          " Build the initial KeyMaster repository. ",
		Prompt:            " Create the project structure and verify it. ",
		EngineerProvider:  string(control.ProviderCodex),
		Reveal:            true,
	}

	inv, preview, err := controlProposalFromBossAction(action)
	if err != nil {
		t.Fatalf("controlProposalFromBossAction() error = %v", err)
	}
	if inv.Capability != control.CapabilityProjectCreateAndStartEngineer || inv.RequestID != "new-project-1" {
		t.Fatalf("invocation = %#v", inv)
	}
	var input control.ProjectCreateAndStartEngineerInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode invocation: %v", err)
	}
	if input.ParentPath != "/tmp/repos" || input.ProjectName != "KeyMaster" || input.ProjectPath != "/tmp/repos/KeyMaster" {
		t.Fatalf("new repository target = %#v", input)
	}
	if input.TodoText != "Build the initial KeyMaster repository." || input.Prompt != "Create the project structure and verify it." || !input.Reveal {
		t.Fatalf("new repository work = %#v", input)
	}
	for _, want := range []string{"Set up Git repository /tmp/repos/KeyMaster", "register an existing Git repository", "create one if the path is unused", "dedicated worktree", "fresh engineer session"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
}

func TestValidateControlProposalAgainstSnapshotDistinguishesLoadedAndNewProjects(t *testing.T) {
	snapshot := StateSnapshot{
		LoadedProjectRefsKnown: true,
		LoadedProjects: []ProjectRef{{
			Name: "Alpha",
			Path: "/tmp/repos/alpha",
		}},
	}

	loadedWork := validatedControlInvocationForTest(t, control.CapabilityTodoCreateWorktreeAndStartEngineer, control.TodoCreateWorktreeAndStartEngineerInput{
		ProjectName: "Alpha",
		TodoText:    "Continue Alpha.",
		Prompt:      "Continue Alpha.",
		Provider:    control.ProviderCodex,
	})
	if err := validateControlProposalAgainstSnapshot(loadedWork, snapshot); err != nil {
		t.Fatalf("loaded-project proposal rejected: %v", err)
	}

	missingWork := validatedControlInvocationForTest(t, control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{
		ProjectPath: "/tmp/repos/KeyMaster",
		Prompt:      "Build KeyMaster.",
		Provider:    control.ProviderCodex,
		SessionMode: control.SessionModeNew,
	})
	err := validateControlProposalAgainstSnapshot(missingWork, snapshot)
	if err == nil || !strings.Contains(err.Error(), "project is not loaded") || !strings.Contains(err.Error(), string(control.CapabilityProjectCreateAndStartEngineer)) {
		t.Fatalf("missing-project error = %v, want new-repository guidance", err)
	}

	newProject := validatedControlInvocationForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  "/tmp/repos",
		ProjectName: "KeyMaster",
		TodoText:    "Build KeyMaster.",
		Prompt:      "Build KeyMaster.",
		Provider:    control.ProviderCodex,
	})
	if err := validateControlProposalAgainstSnapshot(newProject, snapshot); err != nil {
		t.Fatalf("brand-new project proposal rejected: %v", err)
	}

	existingProject := validatedControlInvocationForTest(t, control.CapabilityProjectCreateAndStartEngineer, control.ProjectCreateAndStartEngineerInput{
		ParentPath:  "/tmp/repos",
		ProjectName: "alpha",
		TodoText:    "Continue Alpha.",
		Prompt:      "Continue Alpha.",
		Provider:    control.ProviderCodex,
	})
	err = validateControlProposalAgainstSnapshot(existingProject, snapshot)
	if err == nil || !strings.Contains(err.Error(), "project is already loaded") || !strings.Contains(err.Error(), string(control.CapabilityTodoCreateWorktreeAndStartEngineer)) {
		t.Fatalf("existing-project create error = %v, want loaded-project guidance", err)
	}
}

func TestValidateControlProposalAgainstSnapshotAllowsUnknownFixtureInventory(t *testing.T) {
	inv := validatedControlInvocationForTest(t, control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{
		ProjectPath: "/tmp/repos/KeyMaster",
		Prompt:      "Build KeyMaster.",
		Provider:    control.ProviderCodex,
		SessionMode: control.SessionModeNew,
	})
	if err := validateControlProposalAgainstSnapshot(inv, StateSnapshot{}); err != nil {
		t.Fatalf("unknown fixture inventory should preserve legacy behavior: %v", err)
	}
}

func TestValidateControlProposalAgainstSnapshotRejectsAmbiguousProjectName(t *testing.T) {
	inv := validatedControlInvocationForTest(t, control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{
		ProjectName: "Alpha",
		Prompt:      "Continue Alpha.",
		Provider:    control.ProviderCodex,
		SessionMode: control.SessionModeNew,
	})
	err := validateControlProposalAgainstSnapshot(inv, StateSnapshot{
		LoadedProjectRefsKnown: true,
		LoadedProjects: []ProjectRef{
			{Name: "Alpha", Path: "/tmp/team-one/alpha"},
			{Name: "Alpha", Path: "/tmp/team-two/alpha"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "project name is ambiguous") {
		t.Fatalf("ambiguous-name error = %v", err)
	}
}

func TestAssistantReplyRejectsUnloadedProjectBeforeConfirmation(t *testing.T) {
	inv := validatedControlInvocationForTest(t, control.CapabilityTodoCreateWorktreeAndStartEngineer, control.TodoCreateWorktreeAndStartEngineerInput{
		ProjectPath: "/tmp/repos/KeyMaster",
		TodoText:    "Build KeyMaster.",
		Prompt:      "Build KeyMaster.",
		Provider:    control.ProviderCodex,
	})
	m := NewEmbeddedHelp(context.Background(), nil)
	updated, _ := m.Update(AssistantReplyMsg{
		response: AssistantResponse{Content: "Start KeyMaster?", ControlInvocation: &inv},
		snapshot: StateSnapshot{
			LoadedProjectRefsKnown: true,
			LoadedProjects:         []ProjectRef{{Name: "Alpha", Path: "/tmp/repos/alpha"}},
		},
	})
	got := updated.(Model)
	if got.pendingControl != nil || got.ControlConfirmationActive() {
		t.Fatalf("unloaded-project proposal should not reach confirmation: %#v", got.pendingControl)
	}
	if len(got.messages) == 0 || !strings.Contains(got.messages[len(got.messages)-1].Content, "project is not loaded") {
		t.Fatalf("assistant receipt = %#v, want unloaded-project explanation", got.messages)
	}
}

func TestControlResultContentDoesNotRepeatEmbeddedError(t *testing.T) {
	content := controlResultContent(ControlInvocationResultMsg{
		Status: "Control request failed: project is not loaded: KeyMaster",
		Err:    errors.New("project is not loaded: KeyMaster"),
	})
	if count := strings.Count(content, "project is not loaded: KeyMaster"); count != 1 {
		t.Fatalf("control result repeats error %d times:\n%s", count, content)
	}
}

func validatedControlInvocationForTest(t *testing.T, capability control.CapabilityName, input any) control.Invocation {
	t.Helper()
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", capability, err)
	}
	inv, err := control.ValidateInvocation(control.Invocation{Capability: capability, Args: args})
	if err != nil {
		t.Fatalf("validate %s invocation: %v", capability, err)
	}
	return inv
}
