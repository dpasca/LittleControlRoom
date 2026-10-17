package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/todocontrol"
	"lcroom/internal/todocontrol/ownertest"
)

func TestPlainTodoHostAddAndEditorRefreshOwnerWithoutLosingCallerState(t *testing.T) {
	f := ownertest.New(t)
	svc := service.New(config.Default(), f.Store, events.NewBus(), nil)
	t.Cleanup(func() { waitForControlAsyncRefreshes(t, svc) })
	projects, err := f.Store.ListProjects(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	input := control.TodoAddInput{ProjectPath: f.Caller, Text: "plain host TODO"}
	raw, _ := json.Marshal(input)
	inv, _, err := todocontrol.Inspect(t.Context(), f.Store, control.Invocation{Capability: control.CapabilityTodoAdd, Args: raw})
	if err != nil {
		t.Fatal(err)
	}
	var caller model.ProjectSummary
	for _, project := range projects {
		if project.Path == f.Caller {
			caller = project
		}
	}
	// Keep the owner outside both UI caches, as with a filtered repository.
	m := Model{ctx: t.Context(), svc: svc, allProjects: []model.ProjectSummary{caller}, projects: []model.ProjectSummary{caller}}
	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	m = updated.(Model)
	added := cmd().(bossTodoAddedMsg)
	if added.err != nil || added.todo.ProjectPath != f.Root || added.project.Path != f.Root || added.input.ProjectPath != f.Caller {
		t.Fatalf("added=%+v", added)
	}
	updated, _ = m.Update(added)
	m = updated.(Model)
	if !strings.Contains(m.status, f.Root) || !m.summaryReloadInFlight[f.Root] || m.summaryReloadInFlight[f.Caller] {
		t.Fatalf("status=%s refresh=%+v", m.status, m.summaryReloadInFlight)
	}
	// The ordinary editor retains its triggering checkout to clear submitting
	// state, but refreshes the actual owner as well as that caller view.
	m.todoDialog = &todoDialogState{ProjectPath: f.Caller, Busy: true}
	m.todoPendingSave = &todoPendingSaveState{ProjectPath: f.Caller, Text: "with evidence"}
	saved := m.addTodoCmd(f.Caller, "with evidence", []model.TodoAttachment{{Path: "/evidence.png"}})().(todoActionMsg)
	if saved.err != nil || saved.projectPath != f.Caller || saved.ownerPath != f.Root {
		t.Fatalf("saved=%+v", saved)
	}
	updated, _ = m.Update(saved)
	m = updated.(Model)
	if m.todoPendingSave != nil || m.todoDialog.Busy || m.todoDialog.ProjectPath != f.Root || !m.summaryReloadInFlight[f.Root] || !m.summaryReloadInFlight[f.Caller] {
		t.Fatalf("pending=%+v dialog=%+v refresh=%+v", m.todoPendingSave, m.todoDialog, m.summaryReloadInFlight)
	}
	if selected, ok := m.selectedProject(); !ok || selected.Path != f.Caller {
		t.Fatalf("selected checkout changed: %+v", selected)
	}
	root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
	linked, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
	if len(root) != 2 || len(linked) != 0 {
		t.Fatalf("root=%+v linked=%+v", root, linked)
	}
}

func TestLinkedCallerHostLaunchUsesExistingTodoAndOneSeparateSession(t *testing.T) {
	f := ownertest.New(t)
	svc := service.New(config.Default(), f.Store, events.NewBus(), nil)
	todo, err := f.Store.AddTodo(t.Context(), f.Root, "Use this existing TODO")
	if err != nil {
		t.Fatal(err)
	}
	projects, err := f.Store.ListProjects(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: req.Provider, ThreadID: "new-worker", Started: true, LastActivityAt: time.Now()}}, nil
	})
	m := Model{ctx: t.Context(), svc: svc, allProjects: projects, projects: projects, codexManager: manager}
	inv := trackedWorktreeControlInvocationForTest(t, control.TodoCreateWorktreeAndStartEngineerInput{ProjectPath: f.Caller, TodoID: todo.ID, TodoText: todo.Text, Prompt: "Implement this exact TODO", Provider: control.ProviderAuto})
	updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	m = updated.(Model)
	created := cmd().(bossTodoWorktreeTodoCreatedMsg)
	if created.err != nil || created.todo.ID != todo.ID || created.project.Path != f.Root {
		t.Fatalf("created=%+v", created)
	}
	updated, cmd = m.Update(created)
	m = updated.(Model)
	var prepared bossTodoWorktreePreparedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if typed, ok := msg.(bossTodoWorktreePreparedMsg); ok {
			prepared = typed
		}
	}
	if prepared.err != nil || prepared.result.RootProjectPath != f.Root || prepared.result.WorktreePath == f.Caller {
		t.Fatalf("prepared=%+v", prepared)
	}
	updated, cmd = m.Update(prepared)
	m = updated.(Model)
	for _, msg := range collectCmdMsgs(cmd) {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok && typed.Err != nil {
			t.Fatal(typed.Err)
		}
	}
	if len(requests) != 1 || requests[0].ProjectPath != prepared.result.WorktreePath || !requests[0].ForceNew {
		t.Fatalf("requests=%+v", requests)
	}
	root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
	caller, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
	if len(root) != 1 || root[0].ID != todo.ID || len(caller) != 0 {
		t.Fatalf("root=%+v caller=%+v", root, caller)
	}
	tracked, err := f.Store.GetTodo(t.Context(), todo.ID)
	if err != nil || tracked.WorkProjectPath != prepared.result.WorktreePath || tracked.WorkSessionID != "codex:new-worker" {
		t.Fatalf("tracked=%+v err=%v", tracked, err)
	}
	// Same confirmed launch command after restart/retry cannot start a second session.
	updated, cmd = m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
	m = updated.(Model)
	replayed := cmd().(bossTodoWorktreeTodoCreatedMsg)
	if replayed.err != nil || replayed.todo.ID != todo.ID {
		t.Fatalf("replay=%+v", replayed)
	}
	updated, cmd = m.Update(replayed)
	m = updated.(Model)
	for _, msg := range collectCmdMsgs(cmd) {
		if typed, ok := msg.(bossui.ControlInvocationResultMsg); ok && !strings.Contains(typed.Status, "no duplicate") {
			t.Fatalf("retry receipt=%+v", typed)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("duplicate launch=%+v", requests)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestHostTodoPreflightRefusesStaleOwnerWithoutShowingConfirmation(t *testing.T) {
	f := ownertest.New(t)
	svc := service.New(config.Default(), f.Store, events.NewBus(), nil)
	todo, err := f.Store.AddTodo(t.Context(), f.Root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.AttachTodoWorkSession(t.Context(), todo.ID, f.Worker, model.SessionSourceCodex, "worker", model.TodoWorkStateIdle, time.Now()); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: f.Caller, TodoID: todo.ID, Prompt: "work", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew})
	inv, err := control.BuildProposedInvocation("lcrop_owner_test", control.CapabilityEngineerSendPrompt, args)
	if err != nil {
		t.Fatal(err)
	}
	op, err := f.Store.CreateControlOperation(t.Context(), control.Operation{ID: inv.RequestID, Invocation: inv, Source: "codex", Provider: "codex", SessionKey: "caller", ProjectPath: f.Caller})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Store.ClaimNextControlOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: t.Context(), svc: svc}
	loaded := m.loadExternalControlProposalCmd(op.ID)().(externalControlProposalLoadedMsg)
	if loaded.err != nil || loaded.preflight.refusal == nil {
		t.Fatalf("loaded=%+v", loaded)
	}
	updated, cmd := m.applyExternalControlProposalLoaded(loaded)
	if updated.(Model).externalControlConfirmation != nil || cmd == nil {
		t.Fatal("invalid ownership reached confirmation")
	}
	cmd()
	stored, err := f.Store.GetControlOperation(t.Context(), op.ID)
	if err != nil || stored.Status != control.OperationFailed {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestTodoSessionPreflightRefusesLiveReplacement(t *testing.T) {
	f := ownertest.New(t)
	session := &fakeCodexSession{projectPath: f.Worker, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "replacement", Started: true}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return session, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: f.Worker, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	m := Model{codexManager: manager}
	input := control.EngineerSendPromptInput{ProjectPath: f.Worker, DestinationPath: f.Worker, TodoOwnerPath: f.Root, TodoID: 1618, TargetSessionID: "original", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "continue"}
	args, _ := json.Marshal(input)
	inv := control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}
	if err := m.preflightTodoTargetSession(inv); err == nil {
		t.Fatal("live replacement accepted before confirmation")
	}
	input.TargetSessionID = "replacement"
	inv.Args, _ = json.Marshal(input)
	if err := m.preflightTodoTargetSession(inv); err != nil {
		t.Fatal(err)
	}
}
