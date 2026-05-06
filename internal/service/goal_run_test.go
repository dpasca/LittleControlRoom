package service

import (
	"context"
	"path/filepath"
	"testing"

	"lcroom/internal/bossrun"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestServiceExecutesAgentTaskCleanupGoalRunFromPlanSteps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newGoalRunTestService(t)
	taskOne, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Old review",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask(one) error = %v", err)
	}
	taskTwo, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Old follow-up",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask(two) error = %v", err)
	}
	resources := []control.ResourceRef{
		{Kind: control.ResourceAgentTask, ID: taskOne.ID, Label: taskOne.Title},
		{Kind: control.ResourceAgentTask, ID: taskTwo.ID, Label: taskTwo.Title},
	}

	result, err := svc.ExecuteBossGoalRun(ctx, bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:      bossrun.GoalKindAgentTaskCleanup,
			Title:     "Clear stale delegated agents",
			Objective: "Archive stale delegated agent task records.",
		},
		Plan: bossrun.Plan{
			Version: 1,
			Steps: []bossrun.PlanStep{
				{ID: "custom-select", Kind: bossrun.PlanStepSelect, Title: "Select stale tasks", Resources: resources, Confidence: 1},
				{ID: "custom-archive", Kind: bossrun.PlanStepAct, Title: "Archive stale tasks", Capability: control.CapabilityAgentTaskClose, Resources: resources[:1], Confidence: 1},
				{ID: "custom-verify", Kind: bossrun.PlanStepVerify, Title: "Verify active task set", Resources: resources, Confidence: 1},
			},
		},
		ArchiveResources: resources,
	})
	if err != nil {
		t.Fatalf("ExecuteBossGoalRun() error = %v", err)
	}
	if !result.Verified || len(result.ArchivedTaskIDs) != 2 {
		t.Fatalf("result = %#v, want verified archive of both tasks", result)
	}
	record, err := svc.Store().GetGoalRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("GetGoalRun() error = %v", err)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusCompleted {
		t.Fatalf("status = %q, want completed", record.Proposal.Run.Status)
	}
	gotSteps := []string{}
	for _, entry := range record.Trace {
		gotSteps = append(gotSteps, entry.StepID)
	}
	wantSteps := []string{"custom-archive", "custom-archive", "custom-verify"}
	if len(gotSteps) != len(wantSteps) {
		t.Fatalf("trace steps = %#v, want %#v", gotSteps, wantSteps)
	}
	for i := range wantSteps {
		if gotSteps[i] != wantSteps[i] {
			t.Fatalf("trace steps = %#v, want %#v", gotSteps, wantSteps)
		}
	}
}

func TestServiceMarksAgentTaskCleanupGoalRunFailedOnMissingTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newGoalRunTestService(t)
	result, err := svc.ExecuteBossGoalRun(ctx, bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:  bossrun.GoalKindAgentTaskCleanup,
			Title: "Clear stale delegated agents",
		},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: "agt_missing", Label: "Missing task"},
		},
	})
	if err == nil {
		t.Fatalf("ExecuteBossGoalRun() error = nil, want failed run")
	}
	if result.Verified || len(result.FailedTasks) == 0 {
		t.Fatalf("result = %#v, want failed unverified run", result)
	}
	record, getErr := svc.Store().GetGoalRun(ctx, result.RunID)
	if getErr != nil {
		t.Fatalf("GetGoalRun() error = %v", getErr)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusFailed {
		t.Fatalf("status = %q, want failed", record.Proposal.Run.Status)
	}
	if len(record.Trace) != 2 {
		t.Fatalf("trace len = %d, want archive attempt and verification", len(record.Trace))
	}
}

func newGoalRunTestService(t *testing.T) *Service {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(cfg, st, events.NewBus(), nil)
}
