package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

func TestStorePersistsGoalRunTraceAndResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	record, err := st.CreateGoalRun(ctx, bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:      bossrun.GoalKindAgentTaskCleanup,
			Title:     "Clear stale delegated agents",
			Objective: "Archive stale delegated agent task records.",
		},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: "agt_one", Label: "old review"},
			{Kind: control.ResourceAgentTask, ID: "agt_two", Label: "old follow-up"},
		},
	})
	if err != nil {
		t.Fatalf("CreateGoalRun() error = %v", err)
	}
	if !strings.HasPrefix(record.Proposal.Run.ID, "goal_") {
		t.Fatalf("goal id = %q, want generated goal id", record.Proposal.Run.ID)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusWaitingForApproval {
		t.Fatalf("status = %q, want waiting_for_approval", record.Proposal.Run.Status)
	}

	record, err = st.UpdateGoalRunStatus(ctx, record.Proposal.Run.ID, bossrun.GoalStatusRunning)
	if err != nil {
		t.Fatalf("UpdateGoalRunStatus() error = %v", err)
	}
	if record.Proposal.Run.Status != bossrun.GoalStatusRunning {
		t.Fatalf("status = %q, want running", record.Proposal.Run.Status)
	}
	for _, taskID := range []string{"agt_one", "agt_two"} {
		if err := st.AppendGoalRunTrace(ctx, record.Proposal.Run.ID, bossrun.TraceEntry{
			StepID:     "archive-agent-tasks",
			Capability: control.CapabilityAgentTaskClose,
			ResourceID: taskID,
			Status:     "completed",
			Summary:    "Archived agent task record.",
		}); err != nil {
			t.Fatalf("AppendGoalRunTrace(%s) error = %v", taskID, err)
		}
	}

	completed, err := st.CompleteGoalRun(ctx, bossrun.GoalResult{
		RunID:           record.Proposal.Run.ID,
		Kind:            bossrun.GoalKindAgentTaskCleanup,
		ArchivedTaskIDs: []string{"agt_one", "agt_two"},
		Verified:        true,
	}, nil)
	if err != nil {
		t.Fatalf("CompleteGoalRun() error = %v", err)
	}
	if completed.Proposal.Run.Status != bossrun.GoalStatusCompleted {
		t.Fatalf("status = %q, want completed", completed.Proposal.Run.Status)
	}
	if len(completed.Trace) != 2 || len(completed.Result.Trace) != 2 {
		t.Fatalf("trace record/result lengths = %d/%d, want 2/2", len(completed.Trace), len(completed.Result.Trace))
	}
	if !completed.Result.Verified || !strings.Contains(completed.Result.Summary, "Archived 2 delegated agent task records") {
		t.Fatalf("result = %#v, want verified archive summary", completed.Result)
	}
}

func TestStoreMarksGoalRunFailedWithResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	record, err := st.CreateGoalRun(ctx, bossrun.GoalProposal{
		Run: bossrun.GoalRun{Kind: bossrun.GoalKindAgentTaskCleanup},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: "agt_missing"},
		},
	})
	if err != nil {
		t.Fatalf("CreateGoalRun() error = %v", err)
	}
	completed, err := st.CompleteGoalRun(ctx, bossrun.GoalResult{
		RunID: record.Proposal.Run.ID,
		Kind:  bossrun.GoalKindAgentTaskCleanup,
		FailedTasks: []bossrun.TaskFailure{
			{TaskID: "agt_missing", Error: "not found"},
		},
	}, errors.New("one or more goal steps need review"))
	if err != nil {
		t.Fatalf("CompleteGoalRun() error = %v", err)
	}
	if completed.Proposal.Run.Status != bossrun.GoalStatusFailed {
		t.Fatalf("status = %q, want failed", completed.Proposal.Run.Status)
	}
	if !strings.Contains(completed.Error, "need review") {
		t.Fatalf("error = %q, want stored failure", completed.Error)
	}
}
