package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/control"
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
		ID:                 "agt_test",
		ParentTaskID:       "agt_parent",
		Title:              " Investigate runaway processes ",
		Kind:               model.AgentTaskKindAgent,
		Summary:            "First pass.",
		Capabilities:       []string{"process.inspect", "process.terminate", "process.inspect"},
		Provider:           model.SessionSourceCodex,
		SessionID:          "codex:ses-1",
		WorkspacePath:      "/tmp/agent-task",
		OriginOperationID:  "lcrop_origin",
		OriginProjectPath:  "/tmp/chatnext",
		OriginWorktreePath: "/tmp/chatnext--worker",
		OriginProvider:     model.SessionSourceCodex,
		OriginSessionID:    "caller-session",
		Resources: []model.AgentTaskResource{
			{Kind: model.AgentTaskResourceProject, ProjectPath: "/tmp/chatnext"},
			{Kind: model.AgentTaskResourceTodo, RefID: "42", ProjectPath: "/tmp/chatnext", Label: "Repository TODO #42"},
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
	if task.ParentTaskID != "agt_parent" {
		t.Fatalf("parent task id = %q, want agt_parent", task.ParentTaskID)
	}
	if task.Kind != model.AgentTaskKindAgent || task.Status != model.AgentTaskStatusActive {
		t.Fatalf("created task kind/status = %q/%q", task.Kind, task.Status)
	}
	if len(task.Capabilities) != 2 || task.Capabilities[0] != "process.inspect" || task.Capabilities[1] != "process.terminate" {
		t.Fatalf("capabilities = %#v", task.Capabilities)
	}
	if task.OriginOperationID != "lcrop_origin" || task.OriginProjectPath != "/tmp/chatnext" ||
		task.OriginWorktreePath != "/tmp/chatnext--worker" || task.OriginProvider != model.SessionSourceCodex ||
		task.OriginSessionID != "caller-session" {
		t.Fatalf("created task origin = %#v", task)
	}
	if len(task.Resources) != 4 {
		t.Fatalf("created resources = %d, want 4", len(task.Resources))
	}
	if task.Resources[1].Kind != model.AgentTaskResourceTodo || task.Resources[1].RefID != "42" {
		t.Fatalf("TODO resource = %#v", task.Resources[1])
	}
	if task.Resources[2].PID != 49995 || task.Resources[2].Label != "ts-node-dev" {
		t.Fatalf("process resource = %#v", task.Resources[2])
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

func TestAgentTaskResultLifecyclePersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "agent-task-result.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{ID: "agt_result", Title: "Review result"})
	if err != nil {
		t.Fatal(err)
	}
	readyAt := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	deliveredAt := readyAt.Add(time.Minute)
	consumedAt := deliveredAt.Add(time.Minute)
	messageID := "lcmsg_result"
	consumer := "codex caller-session"
	deliveryError := "caller session no longer exists"
	waiting := model.AgentTaskStatusWaiting
	updated, err := st.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:                  task.ID,
		Status:              &waiting,
		ResultMessageID:     &messageID,
		ResultReadyAt:       &readyAt,
		ResultDeliveredAt:   &deliveredAt,
		ResultDeliveryError: &deliveryError,
		ResultConsumedAt:    &consumedAt,
		ResultConsumedBy:    &consumer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResultMessageID != messageID || !updated.ResultReadyAt.Equal(readyAt) ||
		!updated.ResultDeliveredAt.Equal(deliveredAt) || !updated.ResultConsumedAt.Equal(consumedAt) ||
		updated.ResultDeliveryError != deliveryError || updated.ResultConsumedBy != consumer {
		t.Fatalf("result lifecycle = %#v", updated)
	}
}

func TestAgentTaskOriginBackfillFromCompletedCreateOperation(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agent-task-origin-backfill.sqlite")
	st, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	const (
		taskID       = "agt_backfill"
		operationID  = "lcrop_backfill"
		projectPath  = "/tmp/lcr-intercept"
		worktreePath = "/tmp/lcr-intercept--carrier"
	)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "lcr-intercept",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		WorktreeKind:  model.WorktreeKindMain,
		InScope:       true,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             worktreePath,
		Name:             "lcr-intercept--carrier",
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: projectPath,
		WorktreeKind:     model.WorktreeKindLinked,
		InScope:          true,
		UpdatedAt:        time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	category, err := st.CreateProjectCategory(ctx, "Flight Sim")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetResourceCategory(ctx, model.CategoryResourceProject, projectPath, category.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:      taskID,
		Title:   "Texture the F-14",
		Status:  model.AgentTaskStatusWaiting,
		Summary: "Texture result is ready.",
	}); err != nil {
		t.Fatal(err)
	}
	input := control.AgentTaskCreateInput{
		RequestID: operationID,
		Title:     "Texture the F-14",
		Kind:      control.AgentTaskKindAgent,
		Resources: []control.ResourceRef{{
			Kind:        control.ResourceTodo,
			TodoID:      1298,
			ProjectPath: projectPath,
			Label:       "Repository TODO #1298",
		}},
	}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateControlOperation(ctx, control.Operation{
		ID:          operationID,
		Status:      control.OperationProposed,
		Invocation:  control.Invocation{RequestID: operationID, Capability: control.CapabilityAgentTaskCreate, Args: args},
		Source:      "little-control-room-runtime",
		Provider:    "codex",
		SessionKey:  "caller-session",
		ProjectPath: worktreePath,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateControlOperationStatus(ctx, operationID, control.OperationCompleted,
		json.RawMessage(`{"status":"started","activity":{"TaskID":"`+taskID+`"}}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task, err := st.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.OriginOperationID != operationID || task.OriginProjectPath != projectPath ||
		task.OriginWorktreePath != worktreePath || task.OriginProvider != model.SessionSourceCodex ||
		task.OriginSessionID != "caller-session" {
		t.Fatalf("backfilled origin = %#v", task)
	}
	if task.CategoryID != category.ID || task.ResultReadyAt.IsZero() {
		t.Fatalf("backfilled affiliation/lifecycle = %#v", task)
	}
	if len(task.Resources) != 1 || task.Resources[0].Kind != model.AgentTaskResourceTodo || task.Resources[0].RefID != "1298" {
		t.Fatalf("backfilled resources = %#v", task.Resources)
	}
}

func TestAgentTaskCategoryAssignmentAppearsInList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	category, err := st.CreateProjectCategory(ctx, "Ops")
	if err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_category",
		Title:         "Watch deploy",
		Kind:          model.AgentTaskKindAgent,
		WorkspacePath: "/tmp/agent-category",
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if err := st.SetResourceCategory(ctx, model.CategoryResourceAgentTask, task.ID, category.ID); err != nil {
		t.Fatalf("SetResourceCategory() error = %v", err)
	}
	tasks, err := st.ListAgentTasks(ctx, model.AgentTaskFilter{})
	if err != nil {
		t.Fatalf("ListAgentTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].CategoryID != category.ID || tasks[0].CategoryName != "Ops" {
		t.Fatalf("tasks = %#v, want categorized agent task", tasks)
	}
}
