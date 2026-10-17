package agentcontrol

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/todocontrol/ownertest"
)

func TestPlainTodoProposalBindsOwnerWithoutExpandingPermissionTarget(t *testing.T) {
	f := ownertest.New(t)
	e, err := NewExecutor(Options{Store: f.Store, OriginProjectPath: f.Caller, Scope: control.AuthorityScopePortfolio, Source: "codex", Provider: "codex", SessionKey: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	input := control.TodoAddInput{ProjectPath: f.Caller, Text: "repository backlog task"}
	args, _ := json.Marshal(input)
	report, err := e.Propose(t.Context(), string(control.CapabilityTodoAdd), args, "plain-add")
	if err != nil {
		t.Fatal(err)
	}
	op := report["operation"].(control.Operation)
	var bound control.TodoAddInput
	if err := json.Unmarshal(op.Invocation.Args, &bound); err != nil {
		t.Fatal(err)
	}
	if bound.ProjectPath != f.Caller || bound.TodoOwnerPath != f.Root {
		t.Fatalf("bound=%+v", bound)
	}
	permission, eligible := control.PermissionForOperation(op)
	if !eligible || permission.Target != f.Caller || !strings.Contains(permission.Constraints, f.Root) {
		t.Fatalf("permission scope changed: %+v eligible=%t", permission, eligible)
	}
	input.TodoOwnerPath = f.Caller
	args, _ = json.Marshal(input)
	if _, err := e.Propose(t.Context(), string(control.CapabilityTodoAdd), args, "wrong-add-owner"); err == nil {
		t.Fatal("wrong owner reached confirmation")
	}
	if _, found, err := f.Store.FindControlOperationByClientRequest(t.Context(), "codex", "caller", "wrong-add-owner"); err != nil || found {
		t.Fatalf("invalid proposal queued: found=%t err=%v", found, err)
	}
}

func TestTodoOwnershipProposalPreflightBindsOwnerAndRefusesBeforeConfirmation(t *testing.T) {
	f := ownertest.New(t)
	todo, err := f.Store.AddTodo(t.Context(), f.Root, "existing task")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.AttachTodoWorkSession(t.Context(), todo.ID, f.Worker, model.SessionSourceCodex, "codex:worker", model.TodoWorkStateIdle, time.Now()); err != nil {
		t.Fatal(err)
	}
	e, err := NewExecutor(Options{Store: f.Store, OriginProjectPath: f.Caller, Scope: control.AuthorityScopePortfolio, Source: "codex", Provider: "codex", SessionKey: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	input := control.EngineerSendPromptInput{ProjectPath: f.Worker, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "worker", Prompt: "continue", TodoID: todo.ID}
	args, _ := json.Marshal(input)
	report, err := e.Propose(t.Context(), string(control.CapabilityEngineerSendPrompt), args, "good")
	if err != nil {
		t.Fatal(err)
	}
	op := report["operation"].(control.Operation)
	var bound control.EngineerSendPromptInput
	json.Unmarshal(op.Invocation.Args, &bound)
	if bound.ProjectPath != f.Worker || bound.DestinationPath != f.Worker || bound.TodoOwnerPath != f.Root || bound.TargetSessionID != "worker" {
		t.Fatalf("bound=%+v", bound)
	}
	for _, variant := range []string{"wrong-session", "wrong-checkout", "wrong-owner-expectation"} {
		bad := input
		switch variant {
		case "wrong-session":
			bad.TargetSessionID = "caller"
		case "wrong-checkout":
			bad.ProjectPath = f.Caller
		case "wrong-owner-expectation":
			bad.TodoOwnerPath = f.Caller
		}
		raw, _ := json.Marshal(bad)
		if _, err := e.Propose(t.Context(), string(control.CapabilityEngineerSendPrompt), raw, variant); err == nil {
			t.Fatalf("accepted %s", variant)
		}
		if _, found, err := f.Store.FindControlOperationByClientRequest(t.Context(), "codex", "caller", variant); err != nil || found {
			t.Fatalf("invalid request reached confirmation: %s found=%t err=%v", variant, found, err)
		}
	}
	launch := control.TodoCreateWorktreeAndStartEngineerInput{ProjectPath: f.Caller, TodoID: todo.ID, TodoText: "existing task", Prompt: "work", Provider: control.ProviderCodex}
	raw, _ := json.Marshal(launch)
	if _, err := e.Propose(t.Context(), string(control.CapabilityTodoCreateWorktreeAndStartEngineer), raw, "already-assigned"); err == nil {
		t.Fatal("accepted existing assigned TODO for new launch")
	}
	if _, err := e.Describe(string(control.CapabilityTodoRepairOwner)); err == nil {
		t.Fatal("embedded portfolio scope exposed host repair")
	}
	eligible, err := f.Store.AddTodo(t.Context(), f.Root, "existing unassigned TODO")
	if err != nil {
		t.Fatal(err)
	}
	launch.TodoID = eligible.ID
	raw, _ = json.Marshal(launch)
	report, err = e.Propose(t.Context(), string(control.CapabilityTodoCreateWorktreeAndStartEngineer), raw, "existing")
	if err != nil {
		t.Fatal(err)
	}
	op = report["operation"].(control.Operation)
	var launchBound control.TodoCreateWorktreeAndStartEngineerInput
	json.Unmarshal(op.Invocation.Args, &launchBound)
	if launchBound.TodoID != eligible.ID || launchBound.TodoOwnerPath != f.Root {
		t.Fatalf("existing TODO dropped: %+v", launchBound)
	}
	if _, err := f.Store.UpdateControlOperationStatus(t.Context(), op.ID, control.OperationRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Store.UpdateControlOperationStatus(t.Context(), op.ID, control.OperationCompleted, nil, nil); err != nil {
		t.Fatal(err)
	}
	// A terminal exact retry is a receipt read even if the root is unavailable.
	if err := os.Rename(f.Root, f.Root+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	replayed, err := e.Propose(t.Context(), string(control.CapabilityTodoCreateWorktreeAndStartEngineer), raw, "existing")
	if err != nil || replayed["idempotent_replay"] != true || replayed["operation"].(control.Operation).ID != op.ID {
		t.Fatalf("terminal replay=%+v err=%v", replayed, err)
	}
}
