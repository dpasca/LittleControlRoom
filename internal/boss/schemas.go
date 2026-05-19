package boss

import (
	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

func bossTodoAddPolicyReviewSchema() map[string]any {
	return bossObjectSchema(map[string]any{
		"allow_todo_add": bossBooleanSchema("True only when adding a backlog TODO is the intended action."),
		"replacement_control_capability": bossEnumStringSchema(
			[]string{"", string(control.CapabilityEngineerSendPrompt)},
			"When allow_todo_add is false for now-work on a loaded project, use engineer.send_prompt.",
		),
		"replacement_session_mode": bossEnumStringSchema(
			[]string{"", string(control.SessionModeNew)},
			"Use new for replacement engineer.send_prompt handoffs.",
		),
		"reason": bossStringSchema("Concise policy reason for the decision."),
	}, []string{
		"allow_todo_add",
		"replacement_control_capability",
		"replacement_session_mode",
		"reason",
	})
}

func bossResourceRefSchema() map[string]any {
	return bossObjectSchema(map[string]any{
		"kind":         bossEnumStringSchema(control.ResourceKindStrings(false), ""),
		"id":           bossStringSchema(""),
		"path":         bossStringSchema(""),
		"project_path": bossStringSchema(""),
		"provider":     bossEnumStringSchema(control.ProviderStrings(true), ""),
		"session_id":   bossStringSchema(""),
		"todo_id":      bossIntegerSchema(""),
		"pid":          bossIntegerSchema(""),
		"port":         bossIntegerSchema(""),
		"label":        bossStringSchema(""),
	}, []string{"kind", "id", "path", "project_path", "provider", "session_id", "todo_id", "pid", "port", "label"})
}

func bossReadOnlyRouteKindStrings() []string {
	return []string{
		bossReadOnlyRoutePass,
		bossActionListProjects,
		bossActionProjectDetail,
		bossActionSessionClassifications,
		bossActionTodoReport,
		bossActionAgentTaskReport,
		bossActionReflectionReport,
		bossActionCurrentTUI,
		bossActionAssessmentQueue,
		bossActionProcessReport,
		bossActionSearchContext,
		bossActionSearchBossSessions,
		bossActionContextCommand,
		bossActionSkillsInventory,
		bossActionGoalRunReport,
	}
}

func bossActionKindStrings() []string {
	kinds := append([]string{bossActionAnswer}, bossReadOnlyRouteKindStrings()[1:]...)
	return append(kinds, bossActionProposeControl, bossActionProposeGoal)
}

func bossPlanStepKindStrings() []string {
	return []string{
		string(bossrun.PlanStepObserve),
		string(bossrun.PlanStepClassify),
		string(bossrun.PlanStepSelect),
		string(bossrun.PlanStepPropose),
		string(bossrun.PlanStepAct),
		string(bossrun.PlanStepDelegate),
		string(bossrun.PlanStepAwait),
		string(bossrun.PlanStepVerify),
		string(bossrun.PlanStepBranch),
		string(bossrun.PlanStepReport),
	}
}

func bossReadOnlyRouteSchema() map[string]any {
	return bossObjectSchema(map[string]any{
		"kind": bossEnumStringSchema(bossReadOnlyRouteKindStrings(), ""),
		"target": bossEnumStringSchema(
			[]string{"", "selected"},
			"Use selected only when the user explicitly asks about the selected classic TUI project.",
		),
		"query":              bossStringSchema("Search text for search_context/search_boss_sessions; exact goal run id for goal_run_report when known; otherwise empty."),
		"command":            bossStringSchema("For context_command, one exact ctx command; otherwise empty."),
		"project_path":       bossStringSchema("Exact project path for project-specific queries, or empty."),
		"project_name":       bossStringSchema("Exact project name for project-specific queries, or empty."),
		"session_id":         bossStringSchema("Exact session id for assessment/session queries, or empty."),
		"include_historical": bossBooleanSchema("Whether historical/archived records are needed."),
		"limit":              bossIntegerSchema("Requested result limit, or 0 for default."),
		"reason":             bossStringSchema("Short private reason for the route, or empty."),
	}, []string{
		"kind",
		"target",
		"query",
		"command",
		"project_path",
		"project_name",
		"session_id",
		"include_historical",
		"limit",
		"reason",
	})
}

func bossActionSchema() map[string]any {
	return bossObjectSchema(
		map[string]any{
			"kind": bossEnumStringSchema(bossActionKindStrings(), ""),
			"answer": map[string]any{
				"type":        "string",
				"description": "Final user-facing answer when kind is answer; otherwise empty.",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{"", "selected"},
				"description": "Use selected when the query should inspect the project selected in the classic TUI.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search text when kind is search_context or search_boss_sessions; exact goal run id when kind is goal_run_report and a specific run is known; optional fallback command text when kind is context_command; otherwise empty.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "For kind=context_command, one tiny command such as: ctx search engineer \"query\" --project \"LittleControlRoom\" --limit 5; ctx show engineer:<session-id> --query \"query\" --before 1 --after 2 --max-chars 6000; ctx show agent_task:<task-id> --before 1 --after 4 --max-chars 6000; ctx recent engineer --project \"LittleControlRoom\" --limit 5; ctx search boss \"query\" --limit 5. Otherwise empty.",
			},
			"project_path": map[string]any{
				"type":        "string",
				"description": "Exact project path for project-specific queries, or empty.",
			},
			"project_name": map[string]any{
				"type":        "string",
				"description": "Exact project name if path is unavailable, or empty.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Exact session ID for assessment queries, or empty.",
			},
			"control_capability": map[string]any{
				"type":        "string",
				"enum":        control.CapabilityNameStrings(true),
				"description": "For kind=propose_control, the control capability to propose. Otherwise empty.",
			},
			"request_id": map[string]any{
				"type":        "string",
				"description": "Optional stable idempotency key for kind=propose_control; otherwise empty.",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "For agent_task.continue or agent_task.close proposals, the exact task id. Otherwise empty.",
			},
			"task_title": map[string]any{
				"type":        "string",
				"description": "For agent_task.create proposals, a concise task title. Otherwise empty.",
			},
			"task_kind": map[string]any{
				"type":        "string",
				"enum":        control.AgentTaskKindStrings(true),
				"description": "For agent_task.create proposals: agent or subagent. Empty is treated as agent.",
			},
			"parent_task_id": map[string]any{
				"type":        "string",
				"description": "For subagent task creation, the parent agent task id. Otherwise empty.",
			},
			"task_close_status": map[string]any{
				"type":        "string",
				"enum":        control.AgentTaskCloseStatusStrings(true),
				"description": "For agent_task.close proposals: completed, archived, or waiting. Use archived for remove/erase/archive/hide/close/get-rid cleanup requests, even for open tasks; use waiting only when intentionally keeping the task open for later review. Empty is treated as completed.",
			},
			"task_summary": map[string]any{
				"type":        "string",
				"description": "For agent_task.close proposals, a concise durable summary. Otherwise empty.",
			},
			"project_archive_action": map[string]any{
				"type":        "string",
				"enum":        control.ProjectArchiveActionStrings(true),
				"description": "For project.set_archive_state proposals: archive or unarchive. Use archive to move a regular project out of Active; use unarchive to restore it to Active when in scope.",
			},
			"todo_text": map[string]any{
				"type":        "string",
				"description": "For todo.add proposals, the durable project TODO text to add; use todo.add only for explicit backlog/TODO intent or accepted later parking while active work is mid-turn. For engineer.send_prompt or todo.complete, the known linked TODO text. Otherwise empty.",
			},
			"todo_label": map[string]any{
				"type":        "string",
				"description": "For engineer.send_prompt or todo.complete, a short display label for the linked TODO if known. Otherwise empty.",
			},
			"todo_id": map[string]any{
				"type":        "integer",
				"description": "For engineer.send_prompt linked to an open project TODO, or todo.complete proposals, the exact TODO id. Use 0 when no TODO is linked.",
			},
			"todo_evidence": map[string]any{
				"type":        "string",
				"description": "For todo.complete proposals, concise evidence that the TODO has been satisfied. Otherwise empty.",
			},
			"engineer_provider": map[string]any{
				"type":        "string",
				"enum":        control.ProviderStrings(true),
				"description": "For engineer.send_prompt and agent task launch proposals: auto, codex, opencode, claude_code, lcagent. Empty is treated as auto.",
			},
			"session_mode": map[string]any{
				"type":        "string",
				"enum":        control.SessionModeStrings(true),
				"description": "For engineer.send_prompt proposals: new by default for standalone project work, including work for a project with an open idle Codex/OpenCode session; resume_or_new only for explicit resume/continue, same-topic follow-up, or active-work steering. For agent_task.continue: resume_or_new unless the user explicitly asks for a fresh task session.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "For engineer.send_prompt, agent_task.create, or agent_task.continue proposals, the boss-reframed executable task for the engineer. Otherwise empty.",
			},
			"intent_excerpt": map[string]any{
				"type":        "string",
				"description": "For prompt-bearing control proposals, a short excerpt of the user's wording that must survive reframing. Preserve named sources, metrics, timeframes, negations, and exclusions. Otherwise empty.",
			},
			"preserved_meaning": map[string]any{
				"type":        "string",
				"description": "For prompt-bearing control proposals, concise hard constraints the engineer must not drift from: source, metric, timeframe, scope, negations, and explicit exclusions. Otherwise empty.",
			},
			"success_condition": map[string]any{
				"type":        "string",
				"description": "For prompt-bearing control proposals, what the engineer result must include to satisfy the user, or what missing evidence/source mismatch must be stated. Otherwise empty.",
			},
			"reveal": map[string]any{
				"type":        "boolean",
				"description": "For engineer.send_prompt and agent task launch proposals, whether to reveal the engineer session after sending.",
			},
			"close_session": map[string]any{
				"type":        "boolean",
				"description": "For agent_task.close proposals, whether Little Control Room should close the task's idle embedded engineer session too.",
			},
			"capabilities": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "For agent_task.create proposals, allowed action namespaces such as process.inspect, process.terminate, repo.edit, test.run, browser.inspect.",
			},
			"resources": map[string]any{
				"type":        "array",
				"items":       bossResourceRefSchema(),
				"description": "For agent_task.create proposals, resources touched by the task. For batch project.set_archive_state proposals, every selected target as kind=project with project_path and label.",
			},
			"goal_kind": map[string]any{
				"type":        "string",
				"enum":        []string{"", bossrun.GoalKindAgentTaskCleanup, bossrun.GoalKindLCAgentTask},
				"description": "For kind=propose_goal. Supported values: agent_task_cleanup or lcagent_task.",
			},
			"goal_title": map[string]any{
				"type":        "string",
				"description": "For kind=propose_goal, a concise user-facing run title.",
			},
			"goal_objective": map[string]any{
				"type":        "string",
				"description": "For kind=propose_goal, the user objective this run will achieve.",
			},
			"goal_success_criteria": map[string]any{
				"type":        "string",
				"description": "For kind=propose_goal, the state predicate that must be verified before reporting completion.",
			},
			"goal_preview": map[string]any{
				"type":        "string",
				"description": "Optional scoped confirmation preview for the goal. Empty lets Little Control Room render one from the structured fields.",
			},
			"goal_max_risk": map[string]any{
				"type":        "string",
				"enum":        control.RiskLevelStrings(true),
				"description": "For kind=propose_goal, the maximum risk approved by this grant. Use write for agent_task_cleanup and external for lcagent_task.",
			},
			"goal_resources": map[string]any{
				"type":        "array",
				"items":       bossResourceRefSchema(),
				"description": "For kind=propose_goal, resources the run is authorized to act on. For agent_task_cleanup, each selected task is kind=agent_task with id. For lcagent_task, include scoped project/file/process/session resources.",
			},
			"goal_keep_resources": map[string]any{
				"type":        "array",
				"items":       bossResourceRefSchema(),
				"description": "For kind=propose_goal, resources explicitly excluded from automatic action.",
			},
			"goal_review_resources": map[string]any{
				"type":        "array",
				"items":       bossResourceRefSchema(),
				"description": "For kind=propose_goal, ambiguous resources that need review rather than automatic action.",
			},
			"goal_allowed_capabilities": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": control.CapabilityNameStrings(false)},
				"description": "For kind=propose_goal, primitive capabilities the approved run may execute. For agent_task_cleanup, use agent_task.close. For lcagent_task, use agent_task.create.",
			},
			"goal_forbidden_side_effects": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "For kind=propose_goal, side effects outside the user's grant, such as closing live engineer sessions or deleting files/workspaces.",
			},
			"goal_plan_steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"kind":       map[string]any{"type": "string", "enum": bossPlanStepKindStrings()},
						"title":      map[string]any{"type": "string"},
						"capability": map[string]any{"type": "string", "enum": control.CapabilityNameStrings(true)},
						"resources":  map[string]any{"type": "array", "items": bossResourceRefSchema()},
						"evidence":   map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					},
					"required": []string{"id", "kind", "title", "capability", "resources", "evidence", "confidence"},
				},
				"description": "For kind=propose_goal, compact executable plan IR. Empty lets Little Control Room use the default observe/act/verify/report plan for the goal kind.",
			},
			"include_historical": map[string]any{
				"type":        "boolean",
				"description": "Whether to include out-of-scope/historical projects.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     40,
				"description": "Optional result limit; 0 lets Little Control Room choose a compact default.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Short internal reason for the chosen answer or query.",
			},
		},
		[]string{
			"kind",
			"answer",
			"target",
			"query",
			"command",
			"project_path",
			"project_name",
			"session_id",
			"control_capability",
			"request_id",
			"task_id",
			"task_title",
			"task_kind",
			"parent_task_id",
			"task_close_status",
			"task_summary",
			"project_archive_action",
			"todo_id",
			"todo_label",
			"todo_text",
			"todo_evidence",
			"engineer_provider",
			"session_mode",
			"prompt",
			"intent_excerpt",
			"preserved_meaning",
			"success_condition",
			"reveal",
			"close_session",
			"capabilities",
			"resources",
			"goal_kind",
			"goal_title",
			"goal_objective",
			"goal_success_criteria",
			"goal_preview",
			"goal_max_risk",
			"goal_resources",
			"goal_keep_resources",
			"goal_review_resources",
			"goal_allowed_capabilities",
			"goal_forbidden_side_effects",
			"goal_plan_steps",
			"include_historical",
			"limit",
			"reason",
		},
	)
}
