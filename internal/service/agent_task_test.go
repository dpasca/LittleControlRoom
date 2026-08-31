package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/appfs"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestServiceCreatesAgentTaskWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := New(cfg, st, events.NewBus(), nil)

	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Investigate runaway local processes",
		Kind:  model.AgentTaskKindAgent,
		Resources: []model.AgentTaskResource{
			{Kind: model.AgentTaskResourceProcess, PID: 93624},
		},
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if task.Kind != model.AgentTaskKindAgent || task.Status != model.AgentTaskStatusActive {
		t.Fatalf("task kind/status = %q/%q", task.Kind, task.Status)
	}
	if task.WorkspacePath == "" {
		t.Fatalf("agent task should get a workspace path")
	}
	if _, err := os.Stat(task.WorkspacePath); err != nil {
		t.Fatalf("workspace path should exist: %v", err)
	}
	if !appfs.IsManagedInternalPath(task.WorkspacePath, []string{appfs.InternalWorkspaceRoot(cfg.DataDir)}) {
		t.Fatalf("workspace path should be managed internal path: %s", task.WorkspacePath)
	}

	openTasks, err := svc.ListOpenAgentTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ListOpenAgentTasks() error = %v", err)
	}
	if len(openTasks) != 1 || openTasks[0].ID != task.ID {
		t.Fatalf("open tasks = %#v, want created task", openTasks)
	}

	completed, err := svc.CompleteAgentTask(ctx, task.ID, "Stopped the stale process.")
	if err != nil {
		t.Fatalf("CompleteAgentTask() error = %v", err)
	}
	if completed.Status != model.AgentTaskStatusCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("completed task = %#v", completed)
	}
	openTasks, err = svc.ListOpenAgentTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ListOpenAgentTasks() after complete error = %v", err)
	}
	if len(openTasks) != 0 {
		t.Fatalf("completed task should leave open set, got %#v", openTasks)
	}

	archived, err := svc.ArchiveAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ArchiveAgentTask() error = %v", err)
	}
	if archived.Status != model.AgentTaskStatusArchived || archived.ArchivedAt.IsZero() || archived.ExpiresAt.IsZero() {
		t.Fatalf("archived task = %#v", archived)
	}
}

func TestServiceConsumesReadyAgentTaskBeforeArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(cfg, st, events.NewBus(), nil)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "Review delegated output", Kind: model.AgentTaskKindAgent})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := svc.MarkAgentTaskReadyForReview(ctx, task.ID, "Ready to integrate.")
	if err != nil || ready.ResultReadyAt.IsZero() || ready.Status != model.AgentTaskStatusWaiting {
		t.Fatalf("ready task = %#v, err=%v", ready, err)
	}
	consumed, err := svc.ConsumeAgentTaskResult(ctx, task.ID, "codex caller-session")
	if err != nil || consumed.ResultConsumedAt.IsZero() || consumed.ResultConsumedBy != "codex caller-session" {
		t.Fatalf("consumed task = %#v, err=%v", consumed, err)
	}
	archived, err := svc.ArchiveAgentTask(ctx, task.ID)
	if err != nil || archived.Status != model.AgentTaskStatusArchived || archived.ExpiresAt.IsZero() {
		t.Fatalf("archived task = %#v, err=%v", archived, err)
	}
	if archived.ResultConsumedBy != "codex caller-session" {
		t.Fatalf("archive overwrote result consumer: %#v", archived)
	}
}

func TestListOpenAgentTasksReconcilesDurableResultCallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(cfg, st, events.NewBus(), nil)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title:              "Review F-14 texture result",
		Kind:               model.AgentTaskKindAgent,
		OriginProjectPath:  "/tmp/lcr-intercept",
		OriginWorktreePath: "/tmp/lcr-intercept--carrier",
		OriginProvider:     model.SessionSourceCodex,
		OriginSessionID:    "caller-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkAgentTaskReadyForReview(ctx, task.ID, "The texture is ready to integrate."); err != nil {
		t.Fatal(err)
	}

	openTasks, err := svc.ListOpenAgentTasks(ctx, 10)
	if err != nil || len(openTasks) != 1 || openTasks[0].ResultMessageID == "" {
		t.Fatalf("reconciled tasks = %#v, err=%v", openTasks, err)
	}
	messages, err := st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("queued callbacks = %#v, err=%v", messages, err)
	}
	callback := messages[0]
	if callback.AgentTaskID != task.ID || callback.ProjectPath != "/tmp/lcr-intercept--carrier" ||
		callback.Provider != "codex" || callback.TargetSessionID != "caller-session" {
		t.Fatalf("callback target = %#v", callback)
	}
	if _, err := svc.ListOpenAgentTasks(ctx, 10); err != nil {
		t.Fatal(err)
	}
	messages, err = st.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("idempotent callbacks = %#v, err=%v", messages, err)
	}
	continued, err := svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceCodex, "worker-session-2")
	if err != nil {
		t.Fatal(err)
	}
	if continued.Status != model.AgentTaskStatusActive || continued.ResultMessageID != "" ||
		!continued.ResultReadyAt.IsZero() || !continued.ResultDeliveredAt.IsZero() ||
		continued.ResultDeliveryError != "" || !continued.ResultConsumedAt.IsZero() || continued.ResultConsumedBy != "" {
		t.Fatalf("continued task lifecycle was not reset: %#v", continued)
	}
	if _, claimed, err := st.ClaimEngineerMessage(ctx, callback.ID, "caller-session"); err != nil || !claimed {
		t.Fatalf("claim stale callback: claimed=%t err=%v", claimed, err)
	}
	if _, err := st.RecordEngineerMessageState(ctx, callback.ID, "delivered", "caller-session", "delivered", nil); err != nil {
		t.Fatal(err)
	}
	continued, err = svc.GetAgentTask(ctx, task.ID)
	if err != nil || !continued.ResultDeliveredAt.IsZero() {
		t.Fatalf("stale callback mutated the new result cycle: %#v, err=%v", continued, err)
	}
}
