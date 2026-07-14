package boss

import (
	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

func bossActionSchemaForRequest(req AssistantRequest) map[string]any {
	domain := normalizeBossPlannerDomain(req.PlannerDomain)
	if domain == bossPlannerDomainGeneral {
		return bossActionSchema()
	}

	full := bossActionSchema()
	fullProperties, _ := full["properties"].(map[string]any)
	fields := bossPlannerDomainActionFields(domain)
	properties := make(map[string]any, len(fields))
	for _, field := range fields {
		if property, ok := fullProperties[field]; ok {
			properties[field] = property
		}
	}
	properties["kind"] = bossEnumStringSchema(
		bossActionKindStringsForDomain(domain),
		"Choose only an action kind allowed for the model-selected planner domain.",
	)
	if capabilities := bossPlannerDomainControlCapabilities(domain); len(capabilities) > 0 {
		values := []string{""}
		for _, capability := range capabilities {
			values = append(values, string(capability))
		}
		properties["control_capability"] = bossEnumStringSchema(
			values,
			"For propose_control, choose one capability from the scoped control_reference; otherwise empty.",
		)
	}
	if _, ok := properties["goal_kind"]; ok {
		properties["goal_kind"] = bossEnumStringSchema(
			[]string{"", bossrun.GoalKindAgentTaskCleanup, bossrun.GoalKindLCAgentTask},
			"For propose_goal, choose one supported scoped goal kind; otherwise empty.",
		)
	}
	return bossObjectSchema(properties, fields)
}

func bossActionKindStringsForDomain(domain string) []string {
	kinds := []string{bossActionAnswer}
	for _, kind := range bossReadOnlyRouteKindStrings() {
		if kind == bossReadOnlyRoutePass || kind == bossActionAnswer {
			continue
		}
		kinds = append(kinds, kind)
	}
	switch normalizeBossPlannerDomain(domain) {
	case bossPlannerDomainProjectWork,
		bossPlannerDomainProjectLifecycle,
		bossPlannerDomainSettings,
		bossPlannerDomainGit:
		kinds = append(kinds, bossActionProposeControl)
	case bossPlannerDomainAgentTask:
		kinds = append(kinds, bossActionProposeControl, bossActionProposeGoal)
	case bossPlannerDomainGoal:
		kinds = append(kinds, bossActionProposeGoal)
	}
	return kinds
}

func bossPlannerDomainControlCapabilities(domain string) []control.CapabilityName {
	capabilities, _ := bossControlReferenceSpec(domain)
	return append([]control.CapabilityName(nil), capabilities...)
}

func bossPlannerDomainActionFields(domain string) []string {
	base := []string{
		"kind",
		"answer",
		"target",
		"query",
		"command",
		"project_path",
		"project_name",
		"session_id",
		"include_historical",
		"limit",
		"reason",
	}
	switch normalizeBossPlannerDomain(domain) {
	case bossPlannerDomainProjectWork:
		return append(base,
			"project_parent_path",
			"control_capability",
			"request_id",
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
		)
	case bossPlannerDomainAgentTask:
		return append(append(base,
			"control_capability",
			"request_id",
			"task_id",
			"task_title",
			"task_kind",
			"parent_task_id",
			"task_close_status",
			"task_summary",
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
		), bossGoalActionFields()...)
	case bossPlannerDomainProjectLifecycle:
		return append(base,
			"project_parent_path",
			"control_capability",
			"request_id",
			"project_archive_action",
			"todo_text",
			"engineer_provider",
			"prompt",
			"intent_excerpt",
			"preserved_meaning",
			"success_condition",
			"reveal",
			"resources",
		)
	case bossPlannerDomainSettings:
		return append(base,
			"control_capability",
			"request_id",
			"settings_changes",
		)
	case bossPlannerDomainGit:
		return append(base,
			"control_capability",
			"request_id",
			"commit_message",
			"push_after_commit",
		)
	case bossPlannerDomainGoal:
		return append(base, bossGoalActionFields()...)
	default:
		return base
	}
}

func bossGoalActionFields() []string {
	return []string{
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
	}
}
