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
	"lcroom/internal/store"

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
	if want := fmt.Sprintf("TODO #%d engineer finished; its report is queued for the requesting session.", todo.ID); got.status != want {
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

func queuedDispatchMessage(t *testing.T, st *store.Store, callerKey, workerPath string, provider control.Provider) control.EngineerMessage {
	t.Helper()
	ctx := t.Context()
	callerPath := filepath.Join(filepath.Dir(workerPath), callerKey)
	if err := st.BindEngineerSession(ctx, callerPath, model.SessionSourceClaudeCode, callerKey, callerKey+"-thread"); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(control.EngineerSendPromptInput{
		ProjectPath: workerPath, Provider: provider, SessionMode: control.SessionModeResumeOrNew,
		TargetSessionID: "worker-thread", Prompt: "Render the rehearsal.",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "lcrop_tui_dispatch_" + callerKey
	op, err := st.CreateControlOperation(ctx, control.Operation{
		ID: id, Provider: "claude_code", SessionKey: callerKey, ProjectPath: callerPath, Source: "mcp",
		Invocation: control.Invocation{RequestID: id, Capability: control.CapabilityEngineerSendPrompt, Args: args},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{
		OperationID: op.ID, ProjectPath: workerPath, Provider: provider, SessionMode: control.SessionModeResumeOrNew,
		TargetSessionID: "worker-thread", Prompt: "Render the rehearsal.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestEngineerDispatchReconcilesCompletionBeforeDeliveryReceipt(t *testing.T) {
	for _, provider := range []control.Provider{control.ProviderCodex, control.ProviderClaudeCode, control.ProviderOpenCode, control.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			ctx := t.Context()
			svc := newControlTestService(t)
			st := svc.Store()
			workerPath := filepath.Join(t.TempDir(), "worker")
			message := queuedDispatchMessage(t, st, "caller", workerPath, provider)
			idle := codexapp.Snapshot{
				Provider: codexProviderFromControlProvider(provider), ProjectPath: workerPath, ThreadID: "worker-thread", Started: true,
				Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "The rehearsal is ready."}},
			}
			live := &fakeCodexSession{projectPath: workerPath, snapshot: idle}
			manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
			if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: workerPath, Provider: idle.Provider}); err != nil {
				t.Fatal(err)
			}
			m := Model{ctx: ctx, svc: svc, codexManager: manager}
			// The worker's completion event wins the race with delivery persistence.
			collectCmdMsgs(m.routeEngineerDispatchReplyCmd(workerPath, idle, live))
			if messages, err := st.ListQueuedEngineerMessages(ctx, 10); err != nil || len(messages) != 1 || messages[0].ID != message.ID {
				t.Fatalf("completion before receipt queued a report: %#v, %v", messages, err)
			}
			if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "worker-thread"); err != nil || !claimed {
				t.Fatalf("claim request: claimed=%t err=%v", claimed, err)
			}
			message, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := live.snapshotCalls
			updated, cmd := m.applyEngineerMessageDeliveryRecorded(engineerMessageDeliveryRecordedMsg{message: message})
			if live.snapshotCalls != calls {
				t.Fatal("delivery receipt read the live session on the UI thread")
			}
			m = updated.(Model)
			collectCmdMsgs(cmd)
			messages, err := st.ListQueuedEngineerMessages(ctx, 10)
			if err != nil || len(messages) != 1 || messages[0].TargetSessionID != "caller-thread" || !strings.Contains(messages[0].Prompt, "The rehearsal is ready.") {
				t.Fatalf("late receipt reports = %#v, %v", messages, err)
			}
			// A receipt retry and a duplicate completion must not queue duplicates.
			_, cmd = m.applyEngineerMessageDeliveryRecorded(engineerMessageDeliveryRecordedMsg{message: message})
			collectCmdMsgs(cmd)
			collectCmdMsgs(m.routeEngineerDispatchReplyCmd(workerPath, idle, live))
			if messages, err := st.ListQueuedEngineerMessages(ctx, 10); err != nil || len(messages) != 1 {
				t.Fatalf("repeated completion reports = %#v, %v", messages, err)
			}
		})
	}
}

func TestEngineerDispatchReconciliationWaitsForExactIdleWorker(t *testing.T) {
	ctx := t.Context()
	svc := newControlTestService(t)
	st := svc.Store()
	workerPath := filepath.Join(t.TempDir(), "worker")
	message := queuedDispatchMessage(t, st, "caller", workerPath, control.ProviderClaudeCode)
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "worker-thread"); err != nil || !claimed {
		t.Fatalf("claim request: claimed=%t err=%v", claimed, err)
	}
	message, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil)
	if err != nil {
		t.Fatal(err)
	}
	idle := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, ThreadID: "worker-thread", Started: true}
	live := &fakeCodexSession{projectPath: workerPath, snapshot: idle}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: workerPath, Provider: idle.Provider}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, codexManager: manager}
	for _, change := range []func(*codexapp.Snapshot){
		func(s *codexapp.Snapshot) { s.Busy = true },
		func(s *codexapp.Snapshot) { s.ThreadID = "replacement" },
		func(s *codexapp.Snapshot) { s.Provider = codexapp.ProviderCodex },
		func(s *codexapp.Snapshot) { s.Closed = true },
	} {
		live.snapshot = idle
		change(&live.snapshot)
		collectCmdMsgs(m.reconcileEngineerMessageReplyCmd(message))
		if messages, err := st.ListQueuedEngineerMessages(ctx, 10); err != nil || len(messages) != 0 {
			t.Fatalf("ineligible worker queued a report: %#v, %v", messages, err)
		}
	}
	live.snapshot = idle
	collectCmdMsgs(m.reconcileEngineerMessageReplyCmd(message))
	if messages, err := st.ListQueuedEngineerMessages(ctx, 10); err != nil || len(messages) != 1 || messages[0].TargetSessionID != "caller-thread" {
		t.Fatalf("idle worker reports = %#v, %v", messages, err)
	}
}

func TestEngineerDispatchCompletionNotifiesEveryCaller(t *testing.T) {
	ctx := t.Context()
	svc := newControlTestService(t)
	st := svc.Store()
	workerPath := filepath.Join(t.TempDir(), "worker")
	for _, key := range []string{"caller-one", "caller-two"} {
		message := queuedDispatchMessage(t, st, key, workerPath, control.ProviderCodex)
		if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, "worker-thread"); err != nil || !claimed {
			t.Fatalf("claim request: claimed=%t err=%v", claimed, err)
		}
		if _, err := st.RecordEngineerMessageState(ctx, message.ID, control.EngineerMessageDelivered, "worker-thread", "delivered", nil); err != nil {
			t.Fatal(err)
		}
	}
	idle := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex, ProjectPath: workerPath, ThreadID: "worker-thread", Started: true,
		Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "Both requests are complete."}},
	}
	busy := idle
	busy.Busy = true
	m := Model{ctx: ctx, svc: svc}
	_, cmd := m.handleBossEngineerTurnCompletion(workerPath, true, busy, idle)
	routed := 0
	for _, msg := range collectCmdMsgs(cmd) {
		if reply, ok := msg.(engineerDispatchReplyRoutedMsg); ok {
			if reply.err != nil || !reply.routed {
				t.Fatalf("route report: %#v", reply)
			}
			routed++
		}
	}
	messages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || routed != 2 || len(messages) != 2 {
		t.Fatalf("fan-out reports = %#v, routed=%d err=%v", messages, routed, err)
	}
	targets := map[string]bool{}
	for _, message := range messages {
		targets[message.TargetSessionID] = true
	}
	if !targets["caller-one-thread"] || !targets["caller-two-thread"] {
		t.Fatalf("report targets = %#v", targets)
	}
}

func TestEngineerDispatchReportsLCAgentResumeAliasCompletion(t *testing.T) {
	ctx := t.Context()
	svc := newControlTestService(t)
	st := svc.Store()
	workerPath := filepath.Join(t.TempDir(), "worker")
	message := queuedDispatchMessage(t, st, "caller", workerPath, control.ProviderLCAgent)
	if _, claimed, err := st.ClaimEngineerMessage(ctx, message.ID, message.TargetSessionID); err != nil || !claimed {
		t.Fatalf("claim request: claimed=%t err=%v", claimed, err)
	}
	// The requested run ID resolved to a different canonical thread ID.
	idle := codexapp.Snapshot{
		Provider: codexapp.ProviderLCAgent, ProjectPath: workerPath, ThreadID: "lca-canonical-thread", Started: true,
		Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "The rehearsal is ready."}},
	}
	live := &fakeCodexSession{projectPath: workerPath, snapshot: idle}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return live, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: workerPath, Provider: idle.Provider}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, codexManager: manager}
	cmd := m.engineerMessageDeliveryCmd(message, func() tea.Msg {
		return codexSessionOpenedMsg{projectPath: workerPath, provider: idle.Provider, snapshot: idle}
	})
	var receipt *engineerMessageDeliveryRecordedMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if recorded, ok := msg.(engineerMessageDeliveryRecordedMsg); ok {
			receipt = &recorded
		}
	}
	if receipt == nil || receipt.recordErr != nil || receipt.message.TargetSessionID != idle.ThreadID || receipt.message.RequestedTargetSessionID != message.TargetSessionID {
		t.Fatalf("alias delivery receipt = %#v", receipt)
	}
	_, cmd = m.applyEngineerMessageDeliveryRecorded(*receipt)
	collectCmdMsgs(cmd)
	messages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].TargetSessionID != "caller-thread" || !strings.Contains(messages[0].Prompt, "target_session_id lca-canonical-thread") {
		t.Fatalf("alias completion reports = %#v, %v", messages, err)
	}
}
