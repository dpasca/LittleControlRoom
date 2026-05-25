package boss

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

func normalizeBossAction(action *bossAction) {
	if action == nil {
		return
	}
	action.Kind = normalizeBossActionKind(action.Kind)
	action.Answer = strings.TrimSpace(action.Answer)
	action.Target = strings.TrimSpace(strings.ToLower(action.Target))
	action.Query = strings.TrimSpace(action.Query)
	action.Command = strings.TrimSpace(action.Command)
	action.ProjectPath = strings.TrimSpace(action.ProjectPath)
	action.ProjectName = strings.TrimSpace(action.ProjectName)
	action.SessionID = strings.TrimSpace(action.SessionID)
	action.ControlCapability = strings.TrimSpace(action.ControlCapability)
	action.RequestID = strings.TrimSpace(action.RequestID)
	action.TaskID = strings.TrimSpace(action.TaskID)
	action.TaskTitle = strings.TrimSpace(action.TaskTitle)
	if taskKind := control.NormalizeAgentTaskKind(action.TaskKind); taskKind != "" {
		action.TaskKind = string(taskKind)
	} else {
		action.TaskKind = strings.TrimSpace(action.TaskKind)
	}
	action.ParentTaskID = strings.TrimSpace(action.ParentTaskID)
	if closeStatus := control.NormalizeAgentTaskCloseStatus(action.TaskCloseStatus); closeStatus != "" {
		action.TaskCloseStatus = string(closeStatus)
	} else {
		action.TaskCloseStatus = strings.TrimSpace(action.TaskCloseStatus)
	}
	action.TaskSummary = strings.TrimSpace(action.TaskSummary)
	if archiveAction := control.NormalizeProjectArchiveAction(action.ProjectArchiveAction); archiveAction != "" {
		action.ProjectArchiveAction = string(archiveAction)
	} else {
		action.ProjectArchiveAction = strings.TrimSpace(action.ProjectArchiveAction)
	}
	if action.TodoID < 0 {
		action.TodoID = 0
	}
	action.TodoLabel = strings.TrimSpace(action.TodoLabel)
	action.TodoText = strings.TrimSpace(action.TodoText)
	action.TodoEvidence = strings.TrimSpace(action.TodoEvidence)
	if provider := control.NormalizeProvider(action.EngineerProvider); provider != "" {
		action.EngineerProvider = string(provider)
	} else {
		action.EngineerProvider = strings.TrimSpace(action.EngineerProvider)
	}
	if mode := control.NormalizeSessionMode(action.SessionMode); mode != "" {
		action.SessionMode = string(mode)
	} else {
		action.SessionMode = strings.TrimSpace(action.SessionMode)
	}
	action.Prompt = strings.TrimSpace(action.Prompt)
	action.IntentExcerpt = strings.TrimSpace(action.IntentExcerpt)
	action.PreservedMeaning = strings.TrimSpace(action.PreservedMeaning)
	action.SuccessCondition = strings.TrimSpace(action.SuccessCondition)
	action.Capabilities = normalizeBossActionStringList(action.Capabilities)
	action.Resources = normalizeBossActionResources(action.Resources)
	action.GoalKind = strings.TrimSpace(strings.ToLower(action.GoalKind))
	action.GoalTitle = strings.TrimSpace(action.GoalTitle)
	action.GoalObjective = strings.TrimSpace(action.GoalObjective)
	action.GoalSuccessCriteria = strings.TrimSpace(action.GoalSuccessCriteria)
	action.GoalPreview = strings.TrimSpace(action.GoalPreview)
	action.GoalMaxRisk = strings.TrimSpace(strings.ToLower(action.GoalMaxRisk))
	action.GoalResources = normalizeBossActionResources(action.GoalResources)
	action.GoalKeepResources = normalizeBossActionResources(action.GoalKeepResources)
	action.GoalReviewResources = normalizeBossActionResources(action.GoalReviewResources)
	action.GoalAllowedCapabilities = normalizeBossActionStringList(action.GoalAllowedCapabilities)
	action.GoalForbiddenSideEffects = normalizeBossActionStringList(action.GoalForbiddenSideEffects)
	action.GoalPlanSteps = normalizeBossPlanSteps(action.GoalPlanSteps)
	action.Reason = strings.TrimSpace(action.Reason)
}

func normalizeBossReadOnlyRoute(route *bossReadOnlyRoute) {
	if route == nil {
		return
	}
	route.Kind = normalizeBossActionKind(route.Kind)
	route.Target = strings.TrimSpace(strings.ToLower(route.Target))
	route.Query = strings.TrimSpace(route.Query)
	route.Command = strings.TrimSpace(route.Command)
	route.ProjectPath = strings.TrimSpace(route.ProjectPath)
	route.ProjectName = strings.TrimSpace(route.ProjectName)
	route.SessionID = strings.TrimSpace(route.SessionID)
	route.Reason = strings.TrimSpace(route.Reason)
}

func bossActionFromReadOnlyRoute(route bossReadOnlyRoute) (bossAction, bool) {
	kind := normalizeBossActionKind(route.Kind)
	if kind == "" || kind == bossReadOnlyRoutePass || !bossActionIsReadOnlyQuery(kind) {
		return bossAction{}, false
	}
	action := bossAction{
		Kind:              kind,
		Target:            route.Target,
		Query:             route.Query,
		Command:           route.Command,
		ProjectPath:       route.ProjectPath,
		ProjectName:       route.ProjectName,
		SessionID:         route.SessionID,
		IncludeHistorical: route.IncludeHistorical,
		Limit:             route.Limit,
		Reason:            route.Reason,
	}
	normalizeBossAction(&action)
	return action, true
}

func bossActionIsReadOnlyQuery(kind string) bool {
	switch normalizeBossActionKind(kind) {
	case bossActionListProjects,
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
		bossActionGoalRunReport:
		return true
	default:
		return false
	}
}

func prepareBossActionForRequest(action *bossAction, req AssistantRequest) {
	if action == nil || !bossActionCarriesEngineerPrompt(*action) {
		return
	}
	if strings.TrimSpace(action.IntentExcerpt) == "" {
		action.IntentExcerpt = recentUserWordingExcerpt(req.Messages, 3, 900)
	}
}

func bossActionCarriesEngineerPrompt(action bossAction) bool {
	if normalizeBossActionKind(action.Kind) != bossActionProposeControl || strings.TrimSpace(action.Prompt) == "" {
		return false
	}
	switch control.CapabilityName(strings.TrimSpace(action.ControlCapability)) {
	case control.CapabilityEngineerSendPrompt, control.CapabilityAgentTaskCreate, control.CapabilityAgentTaskContinue:
		return true
	default:
		return false
	}
}

func recentUserWordingExcerpt(messages []ChatMessage, maxMessages, maxChars int) string {
	if maxMessages <= 0 || maxChars <= 0 {
		return ""
	}
	parts := make([]string, 0, maxMessages)
	for i := len(messages) - 1; i >= 0 && len(parts) < maxMessages; i-- {
		if normalizeChatRole(messages[i].Role) != "user" {
			continue
		}
		content := strings.TrimSpace(messages[i].Content)
		if content == "" {
			continue
		}
		parts = append(parts, clipText(content, maxChars))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, "- "+part)
	}
	return strings.Join(lines, "\n")
}

func normalizeBossActionStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeBossActionResources(resources []control.ResourceRef) []control.ResourceRef {
	out := make([]control.ResourceRef, 0, len(resources))
	for _, resource := range resources {
		resource.Kind = control.ResourceKind(strings.TrimSpace(string(resource.Kind)))
		resource.ID = strings.TrimSpace(resource.ID)
		resource.Path = strings.TrimSpace(resource.Path)
		resource.ProjectPath = strings.TrimSpace(resource.ProjectPath)
		resource.Provider = resource.Provider.Normalized()
		resource.SessionID = strings.TrimSpace(resource.SessionID)
		resource.Label = strings.TrimSpace(resource.Label)
		if resource.Kind == "" {
			continue
		}
		out = append(out, resource)
	}
	return out
}

func normalizeBossPlanSteps(steps []bossrun.PlanStep) []bossrun.PlanStep {
	out := make([]bossrun.PlanStep, 0, len(steps))
	for _, step := range steps {
		step.ID = strings.TrimSpace(step.ID)
		step.Kind = bossrun.PlanStepKind(strings.TrimSpace(strings.ToLower(string(step.Kind))))
		step.Title = strings.TrimSpace(step.Title)
		step.Capability = control.CapabilityName(strings.TrimSpace(string(step.Capability)))
		step.Resources = normalizeBossActionResources(step.Resources)
		step.Evidence = strings.TrimSpace(step.Evidence)
		if step.ID == "" && step.Title == "" && step.Kind == "" {
			continue
		}
		out = append(out, step)
	}
	return out
}

func validateBossAction(action bossAction) error {
	switch action.Kind {
	case bossActionAnswer:
		return nil
	case bossActionListProjects, bossActionProjectDetail, bossActionSessionClassifications, bossActionTodoReport, bossActionAgentTaskReport, bossActionReflectionReport, bossActionCurrentTUI, bossActionAssessmentQueue, bossActionProcessReport, bossActionSearchContext, bossActionSearchBossSessions, bossActionContextCommand, bossActionSkillsInventory, bossActionGoalRunReport:
		return nil
	case bossActionProposeControl:
		_, _, err := controlProposalFromBossAction(action)
		return err
	case bossActionProposeGoal:
		_, _, err := goalProposalFromBossAction(action)
		return err
	default:
		return fmt.Errorf("boss chat returned unsupported action kind %q", action.Kind)
	}
}

func synthesizeToolLoopFallback(results []bossToolResult) string {
	if len(results) == 0 {
		return "I do not have enough project data to answer that yet."
	}
	last := results[len(results)-1]
	return "I gathered the latest project data, but could not compose a polished answer. The most recent report was:\n\n" + strings.TrimSpace(last.Text)
}

func directGoalRunReportAnswer(result bossToolResult) string {
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return "I could not find any stored Boss goal-run detail."
	}
	if strings.HasPrefix(text, "Tool error:") {
		return "I could not inspect the Boss goal run: " + strings.TrimSpace(strings.TrimPrefix(text, "Tool error:"))
	}
	return text
}

// explicitGoalRunIDFromRequest only resolves exact structured handles already
// present in the state snapshot. explicitGoalRunIDFromStore extends that to
// exact identifier tokens that storage can verify as real goal-run ids. Language
// intent still goes through model-based routing; this keeps app-generated audit
// handles responsive without adding a keyword classifier.
func explicitGoalRunIDFromRequest(req AssistantRequest) string {
	latest := latestUserMessageContent(req.Messages)
	if latest == "" || len(req.Snapshot.RecentGoalRuns) == 0 {
		return ""
	}
	for _, goal := range req.Snapshot.RecentGoalRuns {
		id := strings.TrimSpace(goal.ID)
		if id != "" && containsStructuredIdentifier(latest, id) {
			return id
		}
	}
	return ""
}

func explicitGoalRunIDFromStore(ctx context.Context, req AssistantRequest, query *QueryExecutor) string {
	if query == nil || query.store == nil {
		return ""
	}
	reader, ok := query.store.(bossGoalRunReader)
	if !ok {
		return ""
	}
	for _, candidate := range structuredIdentifierCandidates(latestUserMessageContent(req.Messages)) {
		if ctx.Err() != nil {
			return ""
		}
		if _, err := reader.GetGoalRun(ctx, candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func latestUserMessageContent(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if normalizeChatRole(messages[i].Role) != "user" {
			continue
		}
		if content := strings.TrimSpace(messages[i].Content); content != "" {
			return content
		}
	}
	return ""
}

func containsStructuredIdentifier(text, id string) bool {
	text = strings.TrimSpace(text)
	id = strings.TrimSpace(id)
	if text == "" || id == "" {
		return false
	}
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], id)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || isStructuredIdentifierBoundary(text[idx-1])
		after := idx + len(id)
		afterOK := after >= len(text) || isStructuredIdentifierBoundary(text[after])
		if beforeOK && afterOK {
			return true
		}
		start = idx + len(id)
	}
	return false
}

func structuredIdentifierCandidates(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		if r >= 'a' && r <= 'z' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			return false
		}
		if r >= '0' && r <= '9' {
			return false
		}
		return r != '_' && r != '-'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
		if len(out) >= 32 {
			break
		}
	}
	return out
}

func isStructuredIdentifierBoundary(ch byte) bool {
	return !(ch >= 'a' && ch <= 'z') &&
		!(ch >= 'A' && ch <= 'Z') &&
		!(ch >= '0' && ch <= '9') &&
		ch != '_' &&
		ch != '-'
}

func trimChatHistory(messages []ChatMessage, limit int) []ChatMessage {
	if limit <= 0 || len(messages) <= limit {
		return append([]ChatMessage(nil), messages...)
	}
	return append([]ChatMessage(nil), messages[len(messages)-limit:]...)
}

func conversationalChatMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if chatMessageIsFlow(message) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func chatMessageIsFlow(message ChatMessage) bool {
	return normalizeChatMessageKind(message.Kind) == ChatMessageKindFlow
}

func normalizeChatMessageKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case ChatMessageKindFlow:
		return ChatMessageKindFlow
	default:
		return ChatMessageKindChat
	}
}

func normalizeChatRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}
