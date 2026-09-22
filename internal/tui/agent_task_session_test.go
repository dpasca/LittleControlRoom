package tui

import (
	"context"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentTaskLCAgentCloseAndEnterResumesSameThread(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Delegated implementation", Kind: model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatal(err)
	}
	// LCAgent announces its logical thread after the initial launch snapshot.
	task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceLCAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderLCAgent, ThreadID: "lct_worker", Started: true,
		LastActivityAt: time.Now(), LatestTurnStateKnown: true, LatestTurnCompleted: true,
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: snapshot}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider: codexapp.ProviderLCAgent, ProjectPath: task.WorkspacePath, Preset: codexcli.PresetYolo,
	}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task},
		codexVisibleProject: task.WorkspacePath, codexSnapshots: map[string]codexapp.Snapshot{task.WorkspacePath: snapshot},
	}
	m.rebuildProjectList(task.WorkspacePath)
	updated, closeCmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	if closeCmd == nil {
		t.Fatal("Ctrl+C did not close the idle worker")
	}
	m = updated.(Model)
	closed, ok := closeCmd().(codexActionMsg)
	if !ok || !closed.closed || closed.err != nil {
		t.Fatalf("close result = %#v", closed)
	}
	updated, recordCmd := m.applyCodexActionMsg(closed)
	m = updated.(Model)
	for _, msg := range collectCmdMsgs(recordCmd) {
		if recorded, ok := msg.(embeddedSessionActivityRecordedMsg); ok && recorded.err != nil {
			t.Fatal(recorded.err)
		}
	}
	if m.codexVisibleProject != "" {
		t.Fatal("close did not return to dashboard")
	}
	task, err = svc.GetAgentTask(ctx, task.ID)
	if err != nil || task.SessionID != snapshot.ThreadID || task.Provider != model.SessionSourceLCAgent {
		t.Fatalf("persisted identity = %#v, error %v", task, err)
	}
	// Reload the row without relying on any live/cached provider identity.
	m = Model{ctx: ctx, svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task}, focusedPane: focusProjects}
	m.rebuildProjectList(task.WorkspacePath)
	project, ok := m.selectedProject()
	if !ok {
		t.Fatal("task is not visible")
	}
	label, tag, live := m.projectAgentDisplay(project, time.Now())
	if label != "LA" || tag != "LA" || live {
		t.Fatalf("closed task badge = %q/%q/%v", label, tag, live)
	}
	_, openCmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	if openCmd == nil {
		t.Fatal("Enter did not reopen the task")
	}
	opened, ok := openCmd().(codexSessionOpenedMsg)
	if !ok || opened.err != nil {
		t.Fatalf("open result = %#v", opened)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d", len(requests))
	}
	if req := requests[1]; req.Provider != codexapp.ProviderLCAgent || req.ResumeID != snapshot.ThreadID || req.ForceNew || req.Prompt != "" {
		t.Fatalf("Enter must resume the LCAgent thread without starting work: %#v", req)
	}
}

func TestAgentTaskLateIdentityPreservesAcceptedResult(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "Reviewed task", Kind: model.AgentTaskKindAgent})
	if err != nil {
		t.Fatal(err)
	}
	task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceLCAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkAgentTaskReadyForReview(ctx, task.ID, "Reviewed changes"); err != nil {
		t.Fatal(err)
	}
	accepted, err := svc.CompleteAgentTask(ctx, task.ID, "Accepted")
	if err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, openAgentTasks: []model.AgentTask{accepted}}
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderLCAgent, ThreadID: "lct_late", Started: true, LastActivityAt: time.Now()}
	cmd := m.recordEmbeddedSessionTransitionCmd(task.WorkspacePath, snapshot)
	if cmd == nil {
		t.Fatal("missing identity persistence command")
	}
	if duplicate := m.recordEmbeddedSessionTransitionCmd(task.WorkspacePath, snapshot); duplicate != nil {
		t.Fatal("concurrent identity writes must coalesce")
	}
	msg := cmd().(embeddedSessionActivityRecordedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if followup := m.finishEmbeddedSessionActivityRecordCmd(msg); followup != nil {
		if next := followup().(embeddedSessionActivityRecordedMsg); next.err != nil {
			t.Fatal(next.err)
		}
	}
	// A stale provider or another thread must not replace an established identity.
	for _, source := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceLCAgent} {
		if err := svc.RecordAgentTaskEngineerSession(ctx, task.ID, source, "stale-thread"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != snapshot.ThreadID || got.Provider != model.SessionSourceLCAgent ||
		got.Status != accepted.Status || got.Summary != accepted.Summary ||
		!got.ResultReadyAt.Equal(accepted.ResultReadyAt) || !got.ResultConsumedAt.Equal(accepted.ResultConsumedAt) ||
		!got.CompletedAt.Equal(accepted.CompletedAt) || got.ResultConsumedBy != accepted.ResultConsumedBy {
		t.Fatalf("identity update lost accepted result: %#v", got)
	}
	if id := taskSessionIDForProvider(got, codexapp.ProviderLCAgent); id != snapshot.ThreadID {
		t.Fatalf("resume identity = %q", id)
	}
}

// A bookkeeping write to UpdatedAt must not make a long-finished task look like
// it just acted; store.RecordAgentTaskEngineerSession advances UpdatedAt while
// deliberately leaving the task lifecycle untouched.
func TestAgentTaskLastActivityIgnoresBookkeepingUpdates(t *testing.T) {
	done := time.Now().Add(-44 * time.Hour)
	task := model.AgentTask{
		Status:        model.AgentTaskStatusCompleted,
		CreatedAt:     done.Add(-4 * time.Hour),
		LastTouchedAt: done,
		CompletedAt:   done,
		UpdatedAt:     time.Now(),
	}
	if got := agentTaskLastActivity(task); !got.Equal(done) {
		t.Fatalf("last activity = %v, want %v", got, done)
	}
}

func TestAgentTaskSessionBindingRejectsStaleCompletedTask(t *testing.T) {
	now := time.Now()
	completedAt := func(at time.Time) model.AgentTask {
		return model.AgentTask{
			Status:        model.AgentTaskStatusCompleted,
			CreatedAt:     at.Add(-time.Hour),
			LastTouchedAt: at,
			CompletedAt:   at,
		}
	}
	for _, tc := range []struct {
		name string
		task model.AgentTask
		want bool
	}{
		{"active", model.AgentTask{Status: model.AgentTaskStatusActive}, true},
		{"waiting", model.AgentTask{Status: model.AgentTaskStatusWaiting}, true},
		{"just completed", completedAt(now), true},
		{"completed within grace", completedAt(now.Add(-agentTaskSessionBindingGrace + time.Minute)), true},
		{"completed past grace", completedAt(now.Add(-agentTaskSessionBindingGrace - time.Minute)), false},
		{"completed two days ago", completedAt(now.Add(-44 * time.Hour)), false},
		{"archived", model.AgentTask{Status: model.AgentTaskStatusArchived, LastTouchedAt: now}, false},
	} {
		if got := agentTaskAcceptsSessionBinding(tc.task, now); got != tc.want {
			t.Errorf("%s: accepts binding = %v, want %v", tc.name, got, tc.want)
		}
	}
}
