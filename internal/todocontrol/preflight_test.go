package todocontrol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/todocontrol"
	"lcroom/internal/todocontrol/ownertest"
)

func TestRepositoryTodoRoutingRetainsCheckoutAndExactSession(t *testing.T) {
	f := ownertest.New(t)
	todo, err := f.Store.AddTodo(t.Context(), f.Root, "tracked work")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.AttachTodoWorkSession(t.Context(), todo.ID, f.Worker, model.SessionSourceCodex, "codex:worker-session", model.TodoWorkStateIdle, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.Root, f.Worker} {
		input := control.EngineerSendPromptInput{ProjectPath: path, TodoID: todo.ID, Provider: control.ProviderCodex, TargetSessionID: "worker-session", SessionMode: control.SessionModeResumeOrNew, Prompt: "continue"}
		raw, _ := json.Marshal(input)
		inv, plan, err := todocontrol.Inspect(t.Context(), f.Store, control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: raw})
		if err != nil || plan.OwnerPath != f.Root || plan.DestinationPath != f.Worker {
			t.Fatalf("plan=%+v err=%v", plan, err)
		}
		var bound control.EngineerSendPromptInput
		json.Unmarshal(inv.Args, &bound)
		if bound.ProjectPath != path || bound.TargetSessionID != "worker-session" || bound.Provider != control.ProviderCodex {
			t.Fatalf("routing identity changed: %+v", bound)
		}
		bound.TargetSessionID = "caller-session"
		if _, err := todocontrol.InspectSend(t.Context(), f.Store, bound); err == nil {
			t.Fatal("accepted unrelated exact session")
		}
		bound.TargetSessionID = "worker-session"
		bound.DestinationPath = f.Caller
		if _, err := todocontrol.InspectSend(t.Context(), f.Store, bound); err == nil {
			t.Fatal("accepted stale destination")
		}
	}
	if _, err := todocontrol.InspectSend(t.Context(), f.Store, control.EngineerSendPromptInput{ProjectPath: f.Caller, TodoID: todo.ID}); err == nil || !strings.Contains(err.Error(), "omit todo_id") {
		t.Fatalf("wrong sibling accepted: %v", err)
	}
}

func TestKnownTodoWorktreeWithoutSessionIsNotLaunchedAgain(t *testing.T) {
	f := ownertest.New(t)
	todo, err := f.Store.AddTodo(t.Context(), f.Root, "legacy worktree assignment")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.SetWorktreeOriginTodoID(t.Context(), f.Worker, todo.ID); err != nil {
		t.Fatal(err)
	}
	launch := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "new-launch", ProjectPath: f.Caller, TodoID: todo.ID, TodoText: todo.Text, Prompt: "work", Provider: control.ProviderCodex}
	if _, err := todocontrol.InspectLaunch(t.Context(), f.Store, launch); err == nil {
		t.Fatal("ignored existing origin TODO worktree")
	}
	if _, err := f.Store.AddTodoForLaunch(t.Context(), control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "bypass-preflight", ProjectPath: f.Root, TodoID: todo.ID, TodoText: todo.Text, Prompt: "work", Provider: control.ProviderCodex}); err == nil {
		t.Fatal("storage eligibility ignored existing origin")
	}
	if _, err := todocontrol.InspectSend(t.Context(), f.Store, control.EngineerSendPromptInput{ProjectPath: f.Worker, TodoID: todo.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRejectsMissingStaleUnrelatedAndAmbiguousOwners(t *testing.T) {
	for _, scenario := range []string{"missing-root", "stale-mapping", "unrelated", "ambiguous-alias"} {
		t.Run(scenario, func(t *testing.T) {
			f := ownertest.New(t)
			input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "launch", ProjectPath: f.Caller, TodoText: "work", Prompt: "work", Provider: control.ProviderCodex}
			switch scenario {
			case "missing-root":
				if err := os.Rename(f.Root, f.Root+"-missing"); err != nil {
					t.Fatal(err)
				}
			case "stale-mapping":
				if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: f.Caller, Name: "caller", InScope: true, PresentOnDisk: true, WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: f.Root + "-wrong", UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				other := ownertest.New(t)
				todo, err := f.Store.AddTodo(t.Context(), f.Root, "root task")
				if err != nil {
					t.Fatal(err)
				}
				input.ProjectPath, input.TodoID = other.Root, todo.ID
				if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: other.Root, Name: "other", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			case "ambiguous-alias":
				alias := filepath.Join(filepath.Dir(f.Root), "alias")
				if err := os.Symlink(f.Root, alias); err != nil {
					t.Fatal(err)
				}
				if err := f.Store.UpsertProjectState(t.Context(), model.ProjectState{Path: alias, Name: "alias", InScope: true, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := todocontrol.InspectLaunch(t.Context(), f.Store, input); err == nil {
				t.Fatal("unsafe owner accepted")
			}
		})
	}
}
