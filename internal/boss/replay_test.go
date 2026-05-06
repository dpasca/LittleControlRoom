package boss

import (
	"context"
	"strings"
	"testing"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

func TestBossReplayRoutingScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		store             *fakeBossStore
		snapshot          StateSnapshot
		stateBrief        string
		userPrompt        string
		routerResponses   []bossReadOnlyRoute
		plannerResponses  []bossAction
		wantRouterCalls   int
		wantPlannerCalls  int
		wantContent       []string
		wantGoalKind      string
		wantGoalTaskIDs   []string
		wantNoControl     bool
		wantNoGoal        bool
		wantNoModelRouted bool
	}{
		{
			name:              "direct goal id audit bypasses models",
			store:             &fakeBossStore{goalRuns: []bossrun.GoalRecord{completedGoalReplayRecord("goal_demo")}},
			stateBrief:        "Visible projects: 1.",
			userPrompt:        "inspect the goal-run trace for goal_demo",
			wantContent:       []string{"Boss goal run", "goal_demo", "archive-agent-tasks agt_one [completed]", "verify-active-set [completed]"},
			wantNoGoal:        true,
			wantNoControl:     true,
			wantNoModelRouted: true,
		},
		{
			name:       "goal followup can route through read only model",
			store:      &fakeBossStore{goalRuns: []bossrun.GoalRecord{completedGoalReplayRecord("goal_demo")}},
			stateBrief: "Recent Boss goal runs:\n- Clear stale delegated agents (goal_demo/completed); inspect: goal_run_report query=goal_demo",
			userPrompt: "what happened with that goal run?",
			routerResponses: []bossReadOnlyRoute{{
				Kind:  bossActionGoalRunReport,
				Query: "goal_demo",
			}},
			wantRouterCalls: 1,
			wantContent:     []string{"Archived 2 delegated agent task records", "trace entries: 2"},
			wantNoGoal:      true,
			wantNoControl:   true,
		},
		{
			name:              "failed goal run report exposes failure evidence",
			store:             &fakeBossStore{goalRuns: []bossrun.GoalRecord{failedGoalReplayRecord("goal_failed")}},
			stateBrief:        "Visible projects: 1.",
			userPrompt:        "inspect the goal-run trace for goal_failed",
			wantContent:       []string{"goal_failed", "kind/status: agent_task_cleanup/failed", "error: missing delegated task", "archive-agent-tasks agt_missing [failed]"},
			wantNoGoal:        true,
			wantNoControl:     true,
			wantNoModelRouted: true,
		},
		{
			name:       "missing goal id reports miss without planner",
			store:      &fakeBossStore{},
			stateBrief: "Visible projects: 1.",
			userPrompt: "inspect the goal-run trace for goal_missing",
			routerResponses: []bossReadOnlyRoute{{
				Kind:  bossActionGoalRunReport,
				Query: "goal_missing",
			}},
			wantRouterCalls: 1,
			wantContent:     []string{"No Boss goal run found for id: goal_missing"},
			wantNoGoal:      true,
			wantNoControl:   true,
		},
		{
			name:       "stale agent cleanup still proposes one scoped goal",
			store:      &fakeBossStore{},
			stateBrief: "Open delegated agent tasks:\n- old review (agt_one)\n- old follow-up (agt_two)",
			userPrompt: "we have some stale agents that have served their scope. Let's remove them now",
			routerResponses: []bossReadOnlyRoute{{
				Kind: bossReadOnlyRoutePass,
			}},
			plannerResponses: []bossAction{{
				Kind:                bossActionProposeGoal,
				GoalKind:            bossrun.GoalKindAgentTaskCleanup,
				GoalTitle:           "Clear stale delegated agents",
				GoalObjective:       "Archive stale delegated agent task records that have served their scope.",
				GoalSuccessCriteria: "Selected tasks no longer appear in the active delegated agent task set.",
				GoalResources: []control.ResourceRef{
					{Kind: control.ResourceAgentTask, ID: "agt_one", Label: "old review"},
					{Kind: control.ResourceAgentTask, ID: "agt_two", Label: "old follow-up"},
				},
				GoalAllowedCapabilities:  []string{"agent_task.close"},
				GoalForbiddenSideEffects: []string{"close live engineer sessions", "delete files or workspaces"},
				GoalMaxRisk:              "write",
			}},
			wantRouterCalls:  1,
			wantPlannerCalls: 1,
			wantContent:      []string{"Archive 2 delegated agent task records?", "Forbidden side effects"},
			wantGoalKind:     bossrun.GoalKindAgentTaskCleanup,
			wantGoalTaskIDs:  []string{"agt_one", "agt_two"},
			wantNoControl:    true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			router := &fakeJSONSchemaRunner{resp: encodeReadOnlyRoutes(t, tc.routerResponses)}
			planner := &fakeJSONSchemaRunner{resp: encodeBossActions(t, tc.plannerResponses)}
			assistant := &Assistant{
				planner:     planner,
				queryRouter: router,
				query:       newQueryExecutor(tc.store),
				model:       "gpt-test",
			}

			resp, err := assistant.Reply(context.Background(), AssistantRequest{
				StateBrief: tc.stateBrief,
				Snapshot:   tc.snapshot,
				Messages:   []ChatMessage{{Role: "user", Content: tc.userPrompt}},
			})
			if err != nil {
				t.Fatalf("Reply() error = %v", err)
			}
			if len(router.reqs) != tc.wantRouterCalls {
				t.Fatalf("router calls = %d, want %d", len(router.reqs), tc.wantRouterCalls)
			}
			if len(planner.reqs) != tc.wantPlannerCalls {
				t.Fatalf("planner calls = %d, want %d", len(planner.reqs), tc.wantPlannerCalls)
			}
			if tc.wantNoModelRouted && (len(router.reqs) != 0 || len(planner.reqs) != 0) {
				t.Fatalf("model calls router/planner = %d/%d, want none", len(router.reqs), len(planner.reqs))
			}
			for _, want := range tc.wantContent {
				if !strings.Contains(resp.Content, want) {
					t.Fatalf("response missing %q:\n%s", want, resp.Content)
				}
			}
			if tc.wantNoControl && resp.ControlInvocation != nil {
				t.Fatalf("ControlInvocation = %#v, want nil", resp.ControlInvocation)
			}
			if tc.wantNoGoal && resp.GoalProposal != nil {
				t.Fatalf("GoalProposal = %#v, want nil", resp.GoalProposal)
			}
			if tc.wantGoalKind != "" {
				if resp.GoalProposal == nil {
					t.Fatalf("GoalProposal = nil, want %s", tc.wantGoalKind)
				}
				if resp.GoalProposal.Run.Kind != tc.wantGoalKind {
					t.Fatalf("goal kind = %q, want %q", resp.GoalProposal.Run.Kind, tc.wantGoalKind)
				}
			}
			if len(tc.wantGoalTaskIDs) > 0 {
				if resp.GoalProposal == nil {
					t.Fatalf("GoalProposal = nil, want task ids %#v", tc.wantGoalTaskIDs)
				}
				got := bossrun.AgentTaskResourceIDs(resp.GoalProposal.Authority.Resources)
				if strings.Join(got, ",") != strings.Join(tc.wantGoalTaskIDs, ",") {
					t.Fatalf("goal task ids = %#v, want %#v", got, tc.wantGoalTaskIDs)
				}
			}
		})
	}
}

func completedGoalReplayRecord(id string) bossrun.GoalRecord {
	return bossrun.GoalRecord{
		Proposal: bossrun.GoalProposal{
			Run: bossrun.GoalRun{
				ID:              id,
				Kind:            bossrun.GoalKindAgentTaskCleanup,
				Title:           "Clear stale delegated agents",
				Objective:       "Archive stale delegated agent task records.",
				SuccessCriteria: "Selected records leave the active set.",
				Status:          bossrun.GoalStatusCompleted,
			},
		},
		Result: bossrun.GoalResult{
			Summary:  "Archived 2 delegated agent task records and verified the selected tasks are out of the active set.",
			Verified: true,
		},
		Trace: []bossrun.TraceEntry{
			{StepID: "archive-agent-tasks", ResourceID: "agt_one", Status: "completed", Summary: "Archived agent task record."},
			{StepID: "verify-active-set", Status: "completed", Summary: "Refreshed open agent-task state."},
		},
	}
}

func failedGoalReplayRecord(id string) bossrun.GoalRecord {
	return bossrun.GoalRecord{
		Proposal: bossrun.GoalProposal{
			Run: bossrun.GoalRun{
				ID:        id,
				Kind:      bossrun.GoalKindAgentTaskCleanup,
				Title:     "Clear stale delegated agents",
				Objective: "Archive stale delegated agent task records.",
				Status:    bossrun.GoalStatusFailed,
			},
		},
		Result: bossrun.GoalResult{
			Summary: "The goal run could not archive the selected delegated agent task records; 1 task failed.",
			FailedTasks: []bossrun.TaskFailure{{
				TaskID: "agt_missing",
				Error:  "missing delegated task",
			}},
		},
		Error: "missing delegated task",
		Trace: []bossrun.TraceEntry{
			{StepID: "archive-agent-tasks", ResourceID: "agt_missing", Status: "failed", Summary: "missing delegated task"},
		},
	}
}

func encodeReadOnlyRoutes(t *testing.T, routes []bossReadOnlyRoute) []llm.JSONSchemaResponse {
	t.Helper()
	out := make([]llm.JSONSchemaResponse, 0, len(routes))
	for _, route := range routes {
		out = append(out, llm.JSONSchemaResponse{
			Model:      "gpt-test",
			OutputText: encodedReadOnlyRoute(t, route),
			Usage:      model.LLMUsage{TotalTokens: 1},
		})
	}
	return out
}

func encodeBossActions(t *testing.T, actions []bossAction) []llm.JSONSchemaResponse {
	t.Helper()
	out := make([]llm.JSONSchemaResponse, 0, len(actions))
	for _, action := range actions {
		out = append(out, llm.JSONSchemaResponse{
			Model:      "gpt-test",
			OutputText: encodedBossAction(t, action),
			Usage:      model.LLMUsage{TotalTokens: 1},
		})
	}
	return out
}
