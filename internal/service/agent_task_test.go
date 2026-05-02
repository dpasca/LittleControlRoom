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
