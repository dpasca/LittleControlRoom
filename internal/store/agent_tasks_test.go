package store

import (
	"context"
	"path/filepath"
	"testing"

	"lcroom/internal/model"
)

func TestAgentTaskLifecyclePersistsResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_test",
		Title:         " Investigate runaway processes ",
		Kind:          model.AgentTaskKindSystemOps,
		Summary:       "First pass.",
		Provider:      model.SessionSourceCodex,
		SessionID:     "codex:ses-1",
		WorkspacePath: "/tmp/agent-task",
		Resources: []model.AgentTaskResource{
			{Kind: model.AgentTaskResourceProject, ProjectPath: "/tmp/chatnext"},
			{Kind: model.AgentTaskResourceProcess, PID: 49995, Label: "ts-node-dev"},
			{Kind: model.AgentTaskResourceEngineerSession, Provider: model.SessionSourceCodex, SessionID: "codex:ses-1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if task.ID != "agt_test" || task.Title != "Investigate runaway processes" {
		t.Fatalf("created task identity = %#v", task)
	}
	if task.Kind != model.AgentTaskKindSystemOps || task.Status != model.AgentTaskStatusActive {
		t.Fatalf("created task kind/status = %q/%q", task.Kind, task.Status)
	}
	if len(task.Resources) != 3 {
		t.Fatalf("created resources = %d, want 3", len(task.Resources))
	}
	if task.Resources[1].PID != 49995 || task.Resources[1].Label != "ts-node-dev" {
		t.Fatalf("process resource = %#v", task.Resources[1])
	}

	openTasks, err := st.ListAgentTasks(ctx, model.AgentTaskFilter{})
	if err != nil {
		t.Fatalf("ListAgentTasks() error = %v", err)
	}
	if len(openTasks) != 1 || openTasks[0].ID != task.ID {
		t.Fatalf("open tasks = %#v, want created task", openTasks)
	}

	completed := model.AgentTaskStatusCompleted
	summary := "Confirmed both hot processes were stale."
	updated, err := st.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:               task.ID,
		Status:           &completed,
		Summary:          &summary,
		ReplaceResources: true,
		Resources: []model.AgentTaskResource{
			{Kind: model.AgentTaskResourcePort, Port: 9229, Label: "debug listener"},
		},
		Touch: true,
	})
	if err != nil {
		t.Fatalf("UpdateAgentTask() error = %v", err)
	}
	if updated.Status != model.AgentTaskStatusCompleted || updated.Summary != summary {
		t.Fatalf("updated task = %#v", updated)
	}
	if updated.CompletedAt.IsZero() {
		t.Fatalf("completed task should record completed_at")
	}
	if len(updated.Resources) != 1 || updated.Resources[0].Port != 9229 {
		t.Fatalf("updated resources = %#v", updated.Resources)
	}

	archived := model.AgentTaskStatusArchived
	if _, err := st.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Status: &archived, Touch: true}); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	visible, err := st.ListAgentTasks(ctx, model.AgentTaskFilter{})
	if err != nil {
		t.Fatalf("list visible tasks: %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("default task listing should hide archived tasks, got %#v", visible)
	}
	allTasks, err := st.ListAgentTasks(ctx, model.AgentTaskFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	if len(allTasks) != 1 || allTasks[0].Status != model.AgentTaskStatusArchived {
		t.Fatalf("all tasks = %#v, want archived task", allTasks)
	}
}
