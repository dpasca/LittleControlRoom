package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDispatchedTodoEngineerReportsCompletedTurnToLaunchingSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newControlTestService(t)
	st := svc.Store()
	root := t.TempDir()
	projectPath := filepath.Join(root, "game")
	callerPath := filepath.Join(root, "game--producer")
	workerPath := filepath.Join(root, "game--todo-fly-1")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path: projectPath, Name: "Game", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, LastActivity: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	todo, err := svc.AddTodo(ctx, projectPath, "FLY-1 human pilot layer")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindEngineerSession(ctx, callerPath, model.SessionSourceClaudeCode, "producer-key", "producer-thread"); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(control.TodoCreateWorktreeAndStartEngineerInput{ProjectPath: projectPath, TodoText: todo.Text, Provider: control.ProviderCodex})
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateControlOperation(ctx, control.Operation{
		ID: "lcrop_tui_dispatch", Provider: "claude_code", SessionKey: "producer-key", ProjectPath: callerPath, Source: "mcp",
		Invocation: control.Invocation{RequestID: "lcrop_tui_dispatch", Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, width: 100, height: 30}

	launch := func() tea.Msg {
		return codexSessionOpenedMsg{projectPath: workerPath, provider: codexapp.ProviderCodex, snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex, ProjectPath: workerPath, ThreadID: "worker-thread", Started: true, LastActivityAt: time.Now(),
		}}
	}
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: op.ID, ProjectPath: projectPath, ProjectName: "Game", TodoText: todo.Text, WorktreePath: workerPath}
	opened, ok := m.trackBossTodoWorktreeEngineerLaunchCmd(input, codexapp.ProviderCodex, todo, "", launch)().(codexSessionOpenedMsg)
	if !ok || !strings.Contains(opened.status, "Its completed turn will be reported back to the requesting session.") {
		t.Fatalf("launch receipt = %#v", opened)
	}

	now := time.Now()
	idle := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex, ProjectPath: workerPath, ThreadID: "worker-thread", Started: true, LastActivityAt: now,
		Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "Implemented the human pilot layer and ctest passes on the dev preset."}},
	}
	busy := idle
	busy.Busy = true
	busy.BusySince = now.Add(-time.Minute)
	busy.ActiveTurnID = "turn-1"
	busy.Phase = codexapp.SessionPhaseRunning

	got, cmd := m.handleBossEngineerTurnCompletion(workerPath, true, busy, idle)
	var routed *engineerDispatchReplyRoutedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if msg, ok := msg.(engineerDispatchReplyRoutedMsg); ok {
			routed = &msg
			updated, _ := got.Update(msg)
			got = updated.(Model)
		}
	}
	if routed == nil || routed.err != nil || !routed.routed {
		t.Fatalf("completion routing = %#v", routed)
	}
	if want := fmt.Sprintf("TODO #%d engineer finished; its report is queued for the session that launched it.", todo.ID); got.status != want {
		t.Fatalf("status = %q, want %q", got.status, want)
	}
	messages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("queued reports = %#v, %v", messages, err)
	}
	report := messages[0]
	if report.ProjectPath != callerPath || report.Provider != control.ProviderClaudeCode || report.TargetSessionID != "producer-thread" ||
		!strings.Contains(report.Prompt, "Implemented the human pilot layer") {
		t.Fatalf("report = %#v", report)
	}
}
