package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

const (
	bossAssistantReasoningEffort      = "high"
	bossReadOnlyRouterReasoningEffort = "low"
)

type ChatMessage struct {
	Role    string
	Content string
	At      time.Time
	Handoff *HandoffHighlight
}

type HandoffHighlight struct {
	EngineerName string
	ProjectLabel string
}

type AssistantRequest struct {
	StateBrief string
	Snapshot   StateSnapshot
	View       ViewContext
	Messages   []ChatMessage
}

type AssistantResponse struct {
	Content           string
	Model             string
	Usage             model.LLMUsage
	ControlInvocation *control.Invocation
	GoalProposal      *bossrun.GoalProposal
}

type AssistantStreamEventKind string

const (
	AssistantStreamTextDelta AssistantStreamEventKind = "text_delta"
	AssistantStreamToolCall  AssistantStreamEventKind = "tool_call"
)

type AssistantStreamEvent struct {
	Kind      AssistantStreamEventKind
	Delta     string
	ToolCall  string
	ToolState string
}

type Assistant struct {
	runner      llm.TextRunner
	planner     llm.JSONSchemaRunner
	queryRouter llm.JSONSchemaRunner
	query       *QueryExecutor
	model       string
	backend     config.AIBackend
}

func NewAssistant(svc *service.Service) *Assistant {
	if svc == nil {
		return &Assistant{}
	}
	runner, modelName, backend := svc.NewBossTextRunner()
	planner, plannerModel, plannerBackend := svc.NewBossJSONRunner()
	if strings.TrimSpace(modelName) == "" {
		modelName = plannerModel
	}
	if backend == config.AIBackendUnset {
		backend = plannerBackend
	}
	query := newQueryExecutorWithBossSessions(svc.Store(), newBossSessionStoreForService(svc))
	if query != nil {
		query.codexHome = svc.Config().CodexHome
		query.codexHomeFallbacks = bossDefaultCodexHomeFallbacks(query.codexHome)
		query.openCodeHome = svc.Config().OpenCodeHome
	}
	return &Assistant{
		runner:      runner,
		planner:     planner,
		queryRouter: planner,
		query:       query,
		model:       strings.TrimSpace(modelName),
		backend:     backend,
	}
}

func (a *Assistant) Configured() bool {
	return a != nil && (a.runner != nil || a.planner != nil) && (!a.requiresExplicitModel() || strings.TrimSpace(a.model) != "")
}

func (a *Assistant) Label() string {
	if a == nil {
		return "Boss chat offline"
	}
	if !a.Configured() {
		switch a.backend {
		case config.AIBackendOpenAIAPI:
			return "Boss chat needs an OpenAI API key"
		case config.AIBackendMLX, config.AIBackendOllama:
			return "Boss chat needs " + a.backend.Label()
		case config.AIBackendUnset:
			return "Boss chat needs a backend"
		case config.AIBackendDisabled:
			return "Boss chat disabled"
		default:
			return "Boss chat needs a supported API backend"
		}
	}
	if strings.TrimSpace(a.model) == "" {
		return fmt.Sprintf("Boss chat via %s (auto model)", a.backend.Label())
	}
	return fmt.Sprintf("Boss chat via %s", a.model)
}

func (a *Assistant) Reply(ctx context.Context, req AssistantRequest) (AssistantResponse, error) {
	if a == nil || (a.runner == nil && a.planner == nil) {
		backend := config.AIBackendUnset
		if a != nil {
			backend = a.backend
		}
		return AssistantResponse{}, errors.New(unconfiguredAssistantMessage(backend))
	}
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}
	if a.query != nil && (a.planner != nil || a.queryRouter != nil) {
		return a.replyWithTools(ctx, req)
	}
	return a.replyDirect(ctx, req)
}

func (a *Assistant) ReplyStream(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (AssistantResponse, error) {
	if a == nil || (a.runner == nil && a.planner == nil) {
		backend := config.AIBackendUnset
		if a != nil {
			backend = a.backend
		}
		return AssistantResponse{}, errors.New(unconfiguredAssistantMessage(backend))
	}
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}
	if a.query != nil && (a.planner != nil || a.queryRouter != nil) {
		return a.replyWithToolsStream(ctx, req, emit)
	}
	return a.replyDirectStream(ctx, req, emit)
}

func (a *Assistant) replyDirect(ctx context.Context, req AssistantRequest) (AssistantResponse, error) {
	if a == nil || a.runner == nil {
		return AssistantResponse{}, errors.New("boss chat needs text chat inference for this request")
	}
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}

	messages := []llm.TextMessage{{
		Role:    "user",
		Content: strings.TrimSpace(requestContextBrief(req)),
	}}
	for _, message := range trimChatHistory(req.Messages, 16) {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.TextMessage{
			Role:    normalizeChatRole(message.Role),
			Content: content,
		})
	}

	resp, err := a.runner.RunText(ctx, llm.TextRequest{
		Model:           modelName,
		SystemText:      bossAssistantSystemPrompt(),
		Messages:        messages,
		ReasoningEffort: bossAssistantReasoningEffort,
	})
	if err != nil {
		return AssistantResponse{}, err
	}
	return AssistantResponse{
		Content: strings.TrimSpace(resp.OutputText),
		Model:   strings.TrimSpace(resp.Model),
		Usage:   resp.Usage,
	}, nil
}

func (a *Assistant) replyDirectStream(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (AssistantResponse, error) {
	if a == nil || a.runner == nil {
		return AssistantResponse{}, errors.New("boss chat needs text chat inference for this request")
	}
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}

	resp, err := runBossText(ctx, a.runner, llm.TextRequest{
		Model:           modelName,
		SystemText:      bossAssistantSystemPrompt(),
		Messages:        bossDirectMessages(req),
		ReasoningEffort: bossAssistantReasoningEffort,
	}, emit)
	if err != nil {
		return AssistantResponse{}, err
	}
	return AssistantResponse{
		Content: strings.TrimSpace(resp.OutputText),
		Model:   strings.TrimSpace(resp.Model),
		Usage:   resp.Usage,
	}, nil
}

func (a *Assistant) replyStructuredHandle(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (AssistantResponse, bool, error) {
	if a == nil || a.query == nil {
		return AssistantResponse{}, false, nil
	}
	handleResult, handledHandle, err := a.tryStructuredHandleQueryRoute(ctx, req, emit)
	if err != nil {
		return AssistantResponse{}, false, err
	}
	if !handledHandle {
		return AssistantResponse{}, false, nil
	}
	content := directGoalRunReportAnswer(*handleResult)
	emitAssistantDelta(emit, content)
	return AssistantResponse{
		Content: content,
		Model:   strings.TrimSpace(a.model),
	}, true, nil
}

func (a *Assistant) replyWithTools(ctx context.Context, req AssistantRequest) (AssistantResponse, error) {
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}

	var (
		toolResults []bossToolResult
		totalUsage  model.LLMUsage
		usedModel   string
	)
	if resp, handled, err := a.replyStructuredHandle(ctx, req, nil); err != nil {
		return AssistantResponse{}, err
	} else if handled {
		resp.Model = firstNonEmpty(resp.Model, modelName)
		resp.Usage = totalUsage
		return resp, nil
	}
	routeResponse, routeResult, routed, err := a.tryReadOnlyQueryRoute(ctx, req, nil)
	if err != nil {
		return AssistantResponse{}, err
	}
	if strings.TrimSpace(routeResponse.OutputText) != "" {
		addLLMUsage(&totalUsage, routeResponse.Usage)
		if modelName := strings.TrimSpace(routeResponse.Model); modelName != "" {
			usedModel = modelName
		}
	}
	if routed {
		if routeResult != nil && routeResult.Name == bossActionGoalRunReport {
			content := directGoalRunReportAnswer(*routeResult)
			return AssistantResponse{
				Content: content,
				Model:   firstNonEmpty(usedModel, modelName),
				Usage:   totalUsage,
			}, nil
		}
		if routeResult != nil {
			toolResults = append(toolResults, *routeResult)
		}
	}
	if a.planner == nil {
		return AssistantResponse{}, errors.New("boss chat needs structured planning for this request")
	}
	for round := 0; round < bossAssistantMaxToolRounds; round++ {
		forceAnswer := round == bossAssistantMaxToolRounds-1
		response, action, err := a.planAction(ctx, req, toolResults, forceAnswer)
		if err != nil {
			return AssistantResponse{}, err
		}
		addLLMUsage(&totalUsage, response.Usage)
		if modelName := strings.TrimSpace(response.Model); modelName != "" {
			usedModel = modelName
		}

		if normalizeBossActionKind(action.Kind) == bossActionAnswer {
			answer := strings.TrimSpace(action.Answer)
			if answer == "" {
				return AssistantResponse{}, errors.New("boss chat returned an empty final answer")
			}
			return AssistantResponse{
				Content: answer,
				Model:   firstNonEmpty(usedModel, modelName),
				Usage:   totalUsage,
			}, nil
		}
		if normalizeBossActionKind(action.Kind) == bossActionProposeControl {
			inv, content, err := controlProposalFromBossAction(action)
			if err != nil {
				return AssistantResponse{}, wrapControlProposalError(err)
			}
			return AssistantResponse{
				Content:           content,
				Model:             firstNonEmpty(usedModel, modelName),
				Usage:             totalUsage,
				ControlInvocation: &inv,
			}, nil
		}
		if normalizeBossActionKind(action.Kind) == bossActionProposeGoal {
			proposal, content, err := goalProposalFromBossAction(action)
			if err != nil {
				return AssistantResponse{}, wrapGoalProposalError(err)
			}
			return AssistantResponse{
				Content:      content,
				Model:        firstNonEmpty(usedModel, modelName),
				Usage:        totalUsage,
				GoalProposal: &proposal,
			}, nil
		}

		result, err := a.query.Execute(ctx, action, req.Snapshot, req.View)
		if err != nil {
			result = bossToolResult{
				Name: normalizeBossActionKind(action.Kind),
				Text: "Tool error: " + err.Error(),
			}
		}
		if reason := strings.TrimSpace(action.Reason); reason != "" {
			result.Text = "Query reason: " + clipText(reason, 220) + "\n" + strings.TrimSpace(result.Text)
		}
		toolResults = append(toolResults, result)
	}

	return AssistantResponse{
		Content: synthesizeToolLoopFallback(toolResults),
		Model:   firstNonEmpty(usedModel, modelName),
		Usage:   totalUsage,
	}, nil
}

func (a *Assistant) replyWithToolsStream(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (AssistantResponse, error) {
	modelName := strings.TrimSpace(a.model)
	if modelName == "" && a.requiresExplicitModel() {
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_chat_model or " + brand.BossAssistantModelEnvVar)
	}

	var (
		toolResults []bossToolResult
		totalUsage  model.LLMUsage
		usedModel   string
	)
	if resp, handled, err := a.replyStructuredHandle(ctx, req, emit); err != nil {
		return AssistantResponse{}, err
	} else if handled {
		resp.Model = firstNonEmpty(resp.Model, modelName)
		resp.Usage = totalUsage
		return resp, nil
	}
	routeResponse, routeResult, routed, err := a.tryReadOnlyQueryRoute(ctx, req, emit)
	if err != nil {
		return AssistantResponse{}, err
	}
	if strings.TrimSpace(routeResponse.OutputText) != "" {
		addLLMUsage(&totalUsage, routeResponse.Usage)
		if modelName := strings.TrimSpace(routeResponse.Model); modelName != "" {
			usedModel = modelName
		}
	}
	if routed {
		if routeResult != nil && routeResult.Name == bossActionGoalRunReport {
			content := directGoalRunReportAnswer(*routeResult)
			emitAssistantDelta(emit, content)
			return AssistantResponse{
				Content: content,
				Model:   firstNonEmpty(usedModel, modelName),
				Usage:   totalUsage,
			}, nil
		}
		if routeResult != nil {
			toolResults = append(toolResults, *routeResult)
		}
	}
	if a.planner == nil {
		return AssistantResponse{}, errors.New("boss chat needs structured planning for this request")
	}
	for round := 0; round < bossAssistantMaxToolRounds; round++ {
		forceAnswer := round == bossAssistantMaxToolRounds-1
		response, action, err := a.planAction(ctx, req, toolResults, forceAnswer)
		if err != nil {
			return AssistantResponse{}, err
		}
		addLLMUsage(&totalUsage, response.Usage)
		if modelName := strings.TrimSpace(response.Model); modelName != "" {
			usedModel = modelName
		}

		if normalizeBossActionKind(action.Kind) == bossActionAnswer {
			answer := strings.TrimSpace(action.Answer)
			if answer == "" {
				return AssistantResponse{}, errors.New("boss chat returned an empty final answer")
			}
			if a.runner == nil {
				emitAssistantDelta(emit, answer)
				return AssistantResponse{
					Content: answer,
					Model:   firstNonEmpty(usedModel, modelName),
					Usage:   totalUsage,
				}, nil
			}
			final, err := a.streamFinalAnswer(ctx, req, toolResults, answer, emit)
			if err != nil {
				return AssistantResponse{}, err
			}
			addLLMUsage(&totalUsage, final.Usage)
			return AssistantResponse{
				Content: strings.TrimSpace(final.Content),
				Model:   firstNonEmpty(final.Model, usedModel, modelName),
				Usage:   totalUsage,
			}, nil
		}
		if normalizeBossActionKind(action.Kind) == bossActionProposeControl {
			inv, content, err := controlProposalFromBossAction(action)
			if err != nil {
				return AssistantResponse{}, wrapControlProposalError(err)
			}
			emitToolCall(emit, describeBossAction(action), "ready")
			emitAssistantDelta(emit, content)
			return AssistantResponse{
				Content:           content,
				Model:             firstNonEmpty(usedModel, modelName),
				Usage:             totalUsage,
				ControlInvocation: &inv,
			}, nil
		}
		if normalizeBossActionKind(action.Kind) == bossActionProposeGoal {
			proposal, content, err := goalProposalFromBossAction(action)
			if err != nil {
				return AssistantResponse{}, wrapGoalProposalError(err)
			}
			emitToolCall(emit, describeBossAction(action), "ready")
			emitAssistantDelta(emit, content)
			return AssistantResponse{
				Content:      content,
				Model:        firstNonEmpty(usedModel, modelName),
				Usage:        totalUsage,
				GoalProposal: &proposal,
			}, nil
		}

		toolCall := describeBossAction(action)
		emitToolCall(emit, toolCall, "running")
		result, err := a.query.Execute(ctx, action, req.Snapshot, req.View)
		if err != nil {
			result = bossToolResult{
				Name: normalizeBossActionKind(action.Kind),
				Text: "Tool error: " + err.Error(),
			}
			emitToolCall(emit, toolCall, "error")
		} else {
			emitToolCall(emit, toolCall, "done")
		}
		if reason := strings.TrimSpace(action.Reason); reason != "" {
			result.Text = "Query reason: " + clipText(reason, 220) + "\n" + strings.TrimSpace(result.Text)
		}
		toolResults = append(toolResults, result)
	}

	fallback := synthesizeToolLoopFallback(toolResults)
	emitAssistantDelta(emit, fallback)
	return AssistantResponse{
		Content: fallback,
		Model:   firstNonEmpty(usedModel, modelName),
		Usage:   totalUsage,
	}, nil
}

func (a *Assistant) streamFinalAnswer(ctx context.Context, req AssistantRequest, toolResults []bossToolResult, plannerAnswer string, emit func(AssistantStreamEvent)) (AssistantResponse, error) {
	if a == nil || a.runner == nil {
		return AssistantResponse{}, errors.New("boss chat needs text chat inference for this request")
	}
	resp, err := runBossText(ctx, a.runner, llm.TextRequest{
		Model:           strings.TrimSpace(a.model),
		SystemText:      bossAssistantSystemPrompt(),
		Messages:        bossFinalAnswerMessages(req, toolResults, plannerAnswer),
		ReasoningEffort: bossAssistantReasoningEffort,
	}, emit)
	if err != nil {
		return AssistantResponse{}, err
	}
	return AssistantResponse{
		Content: strings.TrimSpace(resp.OutputText),
		Model:   strings.TrimSpace(resp.Model),
		Usage:   resp.Usage,
	}, nil
}

type bossReadOnlyRoute struct {
	Kind              string `json:"kind"`
	Target            string `json:"target"`
	Query             string `json:"query"`
	Command           string `json:"command"`
	ProjectPath       string `json:"project_path"`
	ProjectName       string `json:"project_name"`
	SessionID         string `json:"session_id"`
	IncludeHistorical bool   `json:"include_historical"`
	Limit             int    `json:"limit"`
	Reason            string `json:"reason"`
}

const bossReadOnlyRoutePass = "pass"

func (a *Assistant) tryStructuredHandleQueryRoute(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (*bossToolResult, bool, error) {
	if a == nil || a.query == nil {
		return nil, false, nil
	}
	id := explicitGoalRunIDFromRequest(req)
	if id == "" {
		id = explicitGoalRunIDFromStore(ctx, req, a.query)
	}
	if id == "" {
		return nil, false, nil
	}
	action := bossAction{Kind: bossActionGoalRunReport, Query: id}
	normalizeBossAction(&action)
	result := a.executeReadOnlyQueryAction(ctx, action, req, emit)
	return &result, true, nil
}

func (a *Assistant) tryReadOnlyQueryRoute(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (llm.JSONSchemaResponse, *bossToolResult, bool, error) {
	if a == nil || a.queryRouter == nil || a.query == nil {
		return llm.JSONSchemaResponse{}, nil, false, nil
	}
	response, route, err := a.planReadOnlyQueryRoute(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return response, nil, false, err
		}
		return response, nil, false, nil
	}
	action, ok := bossActionFromReadOnlyRoute(route)
	if !ok {
		return response, nil, false, nil
	}
	result := a.executeReadOnlyQueryAction(ctx, action, req, emit)
	return response, &result, true, nil
}

func (a *Assistant) executeReadOnlyQueryAction(ctx context.Context, action bossAction, req AssistantRequest, emit func(AssistantStreamEvent)) bossToolResult {
	toolCall := describeBossAction(action)
	emitToolCall(emit, toolCall, "running")
	result, execErr := a.query.Execute(ctx, action, req.Snapshot, req.View)
	if execErr != nil {
		result = bossToolResult{
			Name: normalizeBossActionKind(action.Kind),
			Text: "Tool error: " + execErr.Error(),
		}
		emitToolCall(emit, toolCall, "error")
	} else {
		emitToolCall(emit, toolCall, "done")
	}
	return result
}

func (a *Assistant) planReadOnlyQueryRoute(ctx context.Context, req AssistantRequest) (llm.JSONSchemaResponse, bossReadOnlyRoute, error) {
	response, err := a.queryRouter.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           strings.TrimSpace(a.model),
		SystemText:      bossReadOnlyRouterSystemPrompt(),
		UserText:        bossReadOnlyRouterUserText(req),
		SchemaName:      "boss_read_only_query_route",
		Schema:          bossReadOnlyRouteSchema(),
		ReasoningEffort: bossReadOnlyRouterReasoningEffort,
	})
	if err != nil {
		return llm.JSONSchemaResponse{}, bossReadOnlyRoute{}, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return response, bossReadOnlyRoute{}, errors.New("boss chat returned no read-only route")
	}
	var route bossReadOnlyRoute
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &route); err != nil {
		return response, bossReadOnlyRoute{}, fmt.Errorf("decode boss read-only route: %w", err)
	}
	normalizeBossReadOnlyRoute(&route)
	return response, route, nil
}

func (a *Assistant) planAction(ctx context.Context, req AssistantRequest, toolResults []bossToolResult, forceAnswer bool) (llm.JSONSchemaResponse, bossAction, error) {
	response, err := a.planner.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           strings.TrimSpace(a.model),
		SystemText:      bossActionPlannerSystemPrompt(),
		UserText:        bossActionPlannerUserText(req, toolResults, forceAnswer),
		SchemaName:      "boss_next_action",
		Schema:          bossActionSchema(),
		ReasoningEffort: bossAssistantReasoningEffort,
	})
	if err != nil {
		return llm.JSONSchemaResponse{}, bossAction{}, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return response, bossAction{}, errors.New("boss chat returned no structured action")
	}
	var action bossAction
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &action); err != nil {
		return response, bossAction{}, fmt.Errorf("decode boss chat action: %w", err)
	}
	normalizeBossAction(&action)
	prepareBossActionForRequest(&action, req)
	if err := validateBossAction(action); err != nil {
		if normalizeBossActionKind(action.Kind) == bossActionProposeControl {
			return response, bossAction{}, wrapControlProposalError(err)
		}
		if normalizeBossActionKind(action.Kind) == bossActionProposeGoal {
			return response, bossAction{}, wrapGoalProposalError(err)
		}
		return response, bossAction{}, err
	}
	if forceAnswer && action.Kind != bossActionAnswer && action.Kind != bossActionProposeControl && action.Kind != bossActionProposeGoal {
		action = bossAction{
			Kind:   bossActionAnswer,
			Answer: synthesizeToolLoopFallback(toolResults),
		}
	}
	return response, action, nil
}

func unconfiguredAssistantMessage(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return "Boss chat is not connected yet. Configure an OpenAI API key in /setup, then reopen boss mode."
	case config.AIBackendMLX:
		return "Boss chat is not connected yet. Choose MLX in /setup and confirm the local endpoint/model."
	case config.AIBackendOllama:
		return "Boss chat is not connected yet. Choose Ollama in /setup and confirm the local endpoint/model."
	case config.AIBackendDisabled:
		return "Boss chat is disabled. Use /setup to enable a boss chat backend."
	case config.AIBackendCodex, config.AIBackendOpenCode, config.AIBackendClaude:
		return "Boss chat currently uses direct API inference, not embedded engineer sessions. Choose OpenAI API, MLX, or Ollama for boss chat while keeping project reports on your preferred backend."
	default:
		return "Boss chat is not connected yet. Boss mode supports direct API chat through OpenAI API, MLX, or Ollama."
	}
}

func (a *Assistant) requiresExplicitModel() bool {
	if a == nil {
		return true
	}
	switch a.backend {
	case config.AIBackendMLX, config.AIBackendOllama:
		return false
	default:
		return true
	}
}

func bossAssistantSystemPrompt() string {
	return strings.Join([]string{
		"You are the unnamed Boss Chat helper inside Little Control Room; the user is the boss.",
		"Help the user decide what deserves attention across coding projects.",
		"Use the compact app-state brief, but do not invent facts that are not present there.",
		"Codex, OpenCode, and Claude Code do implementation work in engineer threads; keep that architecture mostly invisible.",
		"Boss Chat is the top-level conversation. Engineer task output lives in linked task/thread records, so inspect that record when the user asks what happened or what the engineer knows.",
		"System notices in the app-state brief are background event context, not spoken Boss Chat turns; never answer with only a raw control or task-status notice.",
		"Open agent tasks are delegated engineer work items, separate from project TODOs; an agent task should read as one tracked task with its linked engineer thread, not as random separate memory.",
		"Delegated agent tasks can be archived to get them out of the active record, including externally spawned or stray tasks; do not route that request to project TODO cleanup.",
		"Scratch-task projects are project records with kind=scratch_task, separate from both project TODOs and delegated agent tasks; a completed scratch task can be archived out of the active dashboard.",
		"Linked worktrees under the same worktree root are part of the same repo effort; treat their recent work as relevant to repo-level status.",
		"If the user says 'the assistant' or 'the AI assistant' about project work, treat that as likely meaning the engineer session unless they clearly mean Boss Chat.",
		"Act like a high-level coordinator over the active work.",
		"Live engineer sessions may have readable names such as Ada or Grace. Use those names for active delegated work when the context provides them; do not invent a name if none is present.",
		"Do not explain a missing task detail as the engineer having no persistent memory; say we need to inspect the task output or ask the same task for a more specific comparison.",
		"Be proactive about finding facts: before saying you do not know, inspect the available linked task or project context when the current state points to one.",
		"Do not answer commit, deploy, release, migration, schema, storage, or API-shape safety questions from summaries alone. Say it needs a fresh check or propose asking the engineer to inspect the current diff before claiming it is safe.",
		"Never say a deploy needs no DB migration unless direct evidence explicitly covers migrations, schema, storage, or the current diff.",
		"If a review task's saved output does not answer the user's exact question, propose sending the same named engineer back with that specific question instead of asking whether you can ask.",
		"Assume an ongoing coworker chat: skip onboarding, capability pitches, generic menus, and optional handoff offers.",
		"Assume the user tracks many things and wants the highest-level read first, not implementation telemetry.",
		"Do not describe UI mechanics such as timers, Attention rows, temporary activity lines, or tool-call notices; those are implicit.",
		"When acknowledging delegated work, keep the spoken update concise; the actual engineer prompt should preserve the user's source, metric, timeframe, negations, and explicit exclusions.",
		"When reframing user requests for engineers, use lossless reframing: make the task executable while preserving short original wording, hard constraints, and the success condition.",
		"When summarizing engineer output, give the meaningful result and what still needs attention; omit raw logs and mechanical transcript details unless they change the decision.",
		"When summarizing engineer output, do not smooth over a source/metric mismatch. If the engineer checked a different source, metric, timeframe, or scope than the user requested, say the request is not satisfied yet.",
		"When a delegated agent task is waiting for review, summarize the result and recommend one next move: close it, keep it open, or send the named engineer back with a sharper question.",
		"Minimize redundant information. Treat routine work-in-progress repo hygiene as background, not news.",
		"For single-project status questions, answer like an in-the-know coworker in one or two plain sentences.",
		"Default shape: what we have working or learned; then what we still need to validate, decide, or watch.",
		"Silently translate codenames, aliases, and paths after a single clear match. Do not start with mapping phrases like 'appears to be', 'maps to', or path/status recaps.",
		"Treat codenames as shared coworker context. For status questions, never explain what the codename is unless the user asks for the definition.",
		"Assume the user already knows which codenames live in which projects or repos. Alias resolution is private routing, not part of the spoken update.",
		"For alias or codename status questions, do not say '<alias> is in <project/repo>' or similar location/mapping phrasing unless the user asks what or where the alias is.",
		"Write like a sharp but casual coworker update, not like a corporate status report or status dashboard.",
		"Use we/us naturally for the shared project when it fits, but do not claim you personally changed files, ran tools, or made decisions.",
		"Prefer phrases like 'we've got', 'we still need', and 'next we should' over corporate phrases like 'actively being worked', 'current focus', 'operational takeaway', or 'notable residue'.",
		"Do not lead with 'X is actively being worked'; lead with the actual work or result.",
		"Use bullets only for multiple decisions, risks, or options.",
		"Prefer active or latest session evidence; mention repo hygiene, counts, scores, branches, freshness, or board stats only when they explain a real blocker or decision.",
		"In an active project, dirty working tree, ahead commits, and the current branch are normal background state. Do not mention them for casual status questions unless the user asks about repo health, publishing, or merge readiness.",
		"Repo hygiene is material when it includes conflicts, behind/diverged state, failed push/merge/rebase, release risk, or no active work explaining dirty changes.",
		"Use reference metadata internally for disambiguation and blockers; do not recite it in the answer.",
		"Prefer verbs from the evidence: extracted, fixed, blocked, waiting, testing, validating. Do not pad with status adjectives.",
		"Use confident wording when the evidence is direct; reserve hedging for genuinely uncertain mappings or stale data.",
		"You can propose project engineer prompts or generic agent-task actions through structured control actions, but the user must confirm before anything is sent or changed.",
		"Do not say agent work will be done unless you are returning a control proposal for that work or clearly saying it still needs confirmation.",
		"State the next useful check directly when follow-up work is needed.",
	}, "\n")
}

const bossAssistantMaxToolRounds = 4

func bossActionPlannerSystemPrompt() string {
	return strings.Join([]string{
		"You are the unnamed Boss Chat helper inside Little Control Room; the user is the boss.",
		"You decide whether to answer now, request exactly one read-only query, propose one single control action, or propose one scoped goal run for user confirmation.",
		"Codex, OpenCode, and Claude Code do implementation work in engineer threads; keep that architecture mostly invisible.",
		"Boss Chat is the top-level conversation. Engineer task output lives in linked task/thread records, so inspect that record when the user asks what happened or what the engineer knows.",
		"System notices in the app-state brief are background event context, not spoken Boss Chat turns; never answer with only a raw control or task-status notice.",
		"For a delegated agent task, treat the visible named engineer as attached to that same task unless the data says it changed; do not imply a fresh unrelated session when continuing a task.",
		"Linked worktrees under the same worktree root are part of the same repo effort; treat their recent work as relevant to repo-level status.",
		"If the user says 'the assistant' or 'the AI assistant' about project work, treat that as likely meaning the engineer session unless they clearly mean Boss Chat.",
		"Act like a high-level coordinator over the active work.",
		"Live engineer sessions may have readable names such as Ada or Grace. Use those names for active delegated work when the context provides them; do not invent a name if none is present.",
		"Do not explain a missing task detail as the engineer having no persistent memory; say we need to inspect the task output or ask the same task for a more specific comparison.",
		"Be proactive about finding facts: before saying you do not know, inspect the available linked task or project context when the current state points to one.",
		"Do not answer commit, deploy, release, migration, schema, storage, or API-shape safety questions from summaries alone. Use project_detail or context first; if direct evidence does not explicitly inspect the current diff or latest engineer output, propose engineer.send_prompt with session_mode=new for a fresh verification.",
		"Never say a deploy needs no DB migration unless direct evidence explicitly covers migrations, schema, storage, or the current diff.",
		"Use queries when the user asks about a concrete project, project TODOs, open agent tasks, delegated/background agents, Boss goal runs, assessment status, current TUI state, Codex skills, suspicious PIDs/processes/CPU, codenames, aliases, concepts, or anything that requires more than the compact brief.",
		"Available read-only query kinds: list_projects, project_detail, session_classifications, todo_report, agent_task_report, current_tui, assessment_queue, process_report, search_context, search_boss_sessions, context_command, skills_inventory, goal_run_report.",
		"Use agent_task_report when the user asks about open, active, completed, archived, historical, or delegated agent tasks, background agents, task cleanup, or what the agents are doing.",
		"For completed/archived/historical agent-task lookup, or when the user asks to remove, clear, archive, hide, or get rid of a delegated task that is not listed as open, set include_historical=true on agent_task_report.",
		"Use todo_report for project TODOs; project TODOs are separate from delegated agent tasks.",
		"Do not answer that there are no open agent tasks when the app-state brief lists open delegated agent tasks.",
		"Use process_report or the CPU system notice when the user asks about suspicious PIDs, hot CPU, orphaned processes, project-local Node/server processes, or whether stale dev servers are still running.",
		"Use skills_inventory when the user asks about Codex skills, stale skills, installed skills, skill duplicates, or skill management.",
		"Use goal_run_report when the user asks what Boss goal runs have happened, whether a multi-step goal was completed, what was verified, what failed, or asks to inspect goal-run traces.",
		"Available control action kind: propose_control with control_capability equal to engineer.send_prompt, agent_task.create, agent_task.continue, agent_task.close, or scratch_task.archive.",
		"Available goal action kind: propose_goal. Initial goal_kind is agent_task_cleanup for a scoped run that archives multiple delegated agent task records under one user approval, executes primitive agent_task.close archived actions, refreshes state, verifies that selected records left the active set, and reports failures.",
		"Use engineer.send_prompt only for explicit project/repo work on a loaded project. Do not use it for host operations or generic temporary work.",
		"Use agent_task.create for temporary delegated work with no natural loaded project, including host/process/browser/system investigation. Use a generic agent task with resources and capabilities; do not encode special domains as task kinds.",
		"Use agent_task.continue when the user asks to hit an existing open agent task again. Use agent_task.close when the task is done, should wait, or should be archived.",
		"Use scratch_task.archive when project metadata identifies kind=scratch_task and the user asks to remove, clear, archive, hide, or get rid of that scratch task.",
		"If a visible agent task is in review/waiting and fresh read-only evidence resolves it with no remaining work, propose agent_task.close with status completed instead of merely answering that the task is still open.",
		"A status or situation question is enough to close a review/waiting agent task when the gathered evidence directly says the review found no issue, completed the check, or needs no further action.",
		"If the user asks to remove, erase, archive, hide, close, get rid of, or clear from the active record a delegated agent task, propose agent_task.close with task_close_status=archived even when the task is open, active, review, or waiting. Do not use waiting for cleanup/removal requests.",
		"For cleanup of stale or stray agent-task records, set close_session=false unless the user explicitly asks to close the live engineer session too.",
		"If the user asks to remove multiple delegated agent tasks and concrete task ids are known from state or gathered evidence, use propose_goal with goal_kind=agent_task_cleanup instead of splitting the cleanup into one confirmation per task.",
		"For agent_task_cleanup goals, put every task to archive in goal_resources as kind=agent_task. Put tasks intentionally excluded from the run in goal_keep_resources, and uncertain tasks that need a human look in goal_review_resources.",
		"For agent_task_cleanup goals, set goal_allowed_capabilities to [\"agent_task.close\"], goal_max_risk to write, and goal_forbidden_side_effects to include closing live engineer sessions and deleting files or workspaces.",
		"If only one delegated agent task should be archived, use propose_control with agent_task.close instead of propose_goal.",
		"When the user asks to solve, finish, continue, or make progress on open agent tasks, treat that as a request to manage those agent tasks, not as a request for only a status answer.",
		"If the user asks to solve or make progress on multiple open agent tasks, propose exactly one agent_task.continue for the next concrete task. Prefer the user-named task; otherwise choose the stalest or highest-risk task from the available agent-task evidence, and mention that the remaining tasks can follow after this one is confirmed.",
		"If the user assents to a prior Boss Chat plan for clearing open agent tasks, propose agent_task.continue for the next task in that plan instead of restating the plan.",
		"Do not answer with only a priority order when the user asks Boss to get open agent tasks solved and a task id is visible.",
		"Do not use the Little Control Room project or another unrelated active engineer session as a proxy venue for generic or host-level work.",
		"Before propose_control, resolve ambiguous targets with read-only queries or ask the user to name the project. Do not infer a project from hidden UI cursor state.",
		"For engineer.send_prompt, set provider to auto unless the user explicitly names Codex, OpenCode, or Claude Code. Default project handoffs to session_mode=new so unrelated work does not inherit stale engineer context. Use session_mode=resume_or_new only when the user explicitly asks to resume/continue a prior engineer session, clearly gives a same-topic follow-up, or provides an operator note meant to steer active work. Set reveal true only when the user asks to show/open the session.",
		"If the current view lists active Codex engineer work and the user gives an operator note meant to steer that work, such as offering to log in or clarifying what the engineer should try next, propose engineer.send_prompt with session_mode=resume_or_new for that same project/task. The host will steer the active Codex turn when possible. Active work alone is not enough reason to resume; start a fresh separate handoff for new or unrelated project work.",
		"Wanting to see an app, page, server, screenshot, or browser result is not a request to reveal the engineer transcript pane; keep reveal false unless the user explicitly asks to show, open, or watch that engineer session.",
		"For agent_task.create, task_kind must be agent unless parent_task_id is set and the user asked for a subagent; put affected projects, PIDs, ports, files, sessions, or related tasks in resources; put allowed action namespaces such as process.inspect, process.terminate, repo.edit, test.run, browser.inspect in capabilities.",
		"For agent_task.continue, include task_id and a fresh prompt. For agent_task.close, include task_id, task_close_status, task_summary, and close_session.",
		"If the user asks to remove, erase, archive, hide, close, get rid of, or clear from the active record a delegated agent task and the task id is known, propose agent_task.close with task_close_status=archived. This applies to open/review/waiting tasks too; do not downgrade cleanup to task_close_status=waiting.",
		"For propose_control, the prompt field is the boss-reframed executable task for the engineer session or task.",
		"For prompt-bearing propose_control actions, fill intent_excerpt with a short excerpt of the user's wording that must survive reframing; fill preserved_meaning with source, metric, timeframe, negations, and explicit exclusions; fill success_condition with what the engineer must return or what missing evidence must be reported.",
		"Use context_command for command-shaped context lookup: ctx search engineer, ctx show, ctx show agent_task, ctx recent engineer, or ctx search boss.",
		"Use ctx search engineer when the user asks to recall, quote, verify, or inspect what an engineer session said. Use ctx show on the returned handle before quoting or correcting exact details.",
		"When the user asks for the output or result of an open agent task, use ctx show agent_task:<task-id>; if only an engineer session id is available, use ctx show engineer:<session-id>.",
		"When the user asks for details of a visible review/waiting agent task, use ctx show agent_task:<task-id> before answering from memory, summaries, or the compact brief.",
		"If gathered task output still lacks the requested detail and the task id is clear, propose agent_task.continue with a precise follow-up for the same task instead of asking whether to ask the engineer.",
		"If the latest Boss Chat assistant message says an agent task was created or continued, and the user follows up with a vague request like \"please try again\" without new work instructions, first read the task output with ctx show agent_task:<task-id> instead of proposing agent_task.continue.",
		"Use ctx search boss only when the user asks to recall, search, or quote earlier Boss Chat conversations.",
		"Use search_context when the user asks what a codename, acronym, feature, branch phrase, or unfamiliar term refers to; it searches project metadata, summaries, assessments, TODOs, and cached engineer-session text.",
		"Use search_boss_sessions when the user asks to recall, search, or quote earlier boss chat conversations; it returns XML-like boss_session and turn snippets from saved local boss-chat transcripts.",
		"Do not answer that a concrete term is unknown until search_context has been tried.",
		"For codename or alias status questions, search_context should usually come first; after it finds one project path, inspect project_detail before answering.",
		"Prefer project_detail when the answer depends on a project's current state, especially after another query identifies the relevant project.",
		"When project_detail includes live engineer work context, treat it as fresher than stored assessments or board stats.",
		"When project_detail includes worktree family activity, treat linked entries as current work on the same repo, not unrelated projects.",
		"For project-specific queries, use project_path when a path is available or project_name when the user gives an exact project name.",
		"Do not infer a project from hidden UI cursor state; if the target is ambiguous, ask the user to name the project.",
		"Do not invent facts. After query results are provided, answer from those results and the app-state brief.",
		"Never claim you changed files, projects, TODOs, snoozes, panels, or sessions. Read-only query tools are report-only; control actions are proposals that need user confirmation before execution.",
		"Final answers should sound like a concise coworker update: turn tool output into judgment instead of mirroring its bullet structure, and avoid capability pitches or optional menus.",
		"Do not describe UI mechanics such as timers, Attention rows, temporary activity lines, or tool-call notices; those are implicit.",
		"When acknowledging delegated work, keep the spoken update concise; the actual engineer prompt should preserve the user's source, metric, timeframe, negations, and explicit exclusions.",
		"When reframing user requests for engineers, use lossless reframing: make the task executable while preserving short original wording, hard constraints, and the success condition.",
		"When summarizing engineer output, give the meaningful result and what still needs attention; omit raw logs and mechanical transcript details unless they change the decision.",
		"When summarizing engineer output, do not smooth over a source/metric mismatch. If the engineer checked a different source, metric, timeframe, or scope than the user requested, say the request is not satisfied yet.",
		"When a delegated agent task is waiting for review, summarize the result and recommend one next move: close it, keep it open, or send the named engineer back with a sharper question.",
		"When answering from project_detail or search_context, use name/path/status metadata to choose the target, then answer the operational substance rather than reciting the lookup.",
		"Treat codenames and aliases as shared coworker context; for status questions, do not explain what the codename maps to unless the user asks for the definition.",
		"Assume the user already knows which codenames live in which projects or repos. Alias resolution is private routing; do not surface it as the answer.",
		"For alias or codename status questions, avoid phrasing like '<alias> is in <project>', '<alias> maps to <repo>', or '<alias> lives at <path>' unless the user asks what or where it is.",
		"For single-project status questions, default to one or two plain sentences: what we have working or learned, plus what we still need to validate, decide, or watch.",
		"Use we/us naturally for the shared project when it fits, but do not claim you personally changed files, ran tools, or made decisions.",
		"Avoid corporate phrases like 'actively being worked', 'current focus', 'operational takeaway', or 'notable residue'; use direct coworker phrasing instead.",
		"Do not lead with 'X is actively being worked'; lead with the actual work or result.",
		"Minimize redundant information. If the project is actively being worked, dirty working tree, ahead commits, and the current branch are expected background state.",
		"Do not include mappings, paths, dirty/ahead state, branch names, ages, attention scores, confidence, queue, or classification telemetry unless it materially changes what the user should do.",
		"Treat repo hygiene as material only for conflicts, behind/diverged state, failed push/merge/rebase, release risk, explicit repo-health questions, or dirty changes without active work context.",
		"Use reference metadata only to disambiguate targets and detect blockers; do not surface it as the answer.",
		"Do not hedge a single clear match with phrases like 'appears to be', 'looks like', or 'maps to'.",
	}, "\n")
}

func bossReadOnlyRouterSystemPrompt() string {
	return strings.Join([]string{
		"You are the fast read-only query router for Boss Chat in Little Control Room.",
		"Choose exactly one read-only query only when the latest user message is asking for information, status, recall, inspection, or audit details that a single query can gather.",
		"Choose pass for requests to change state, delegate work, continue work, clear tasks, archive records, launch/stop processes, commit, fix, confirm a proposal, or anything that may need user confirmation.",
		"Choose pass when the request needs multiple read-only queries or high-level planning; the full Boss planner will handle it.",
		"Use goal_run_report when the user asks what Boss goal runs happened, whether a goal finished, what was verified, what failed, or asks to inspect a goal-run trace. If an exact goal run id is known from the user message or state brief, put only that id in query.",
		"Use agent_task_report for questions about delegated/background agent tasks or what agents are doing.",
		"Use project_detail for questions about one concrete project when an exact project path or name is available.",
		"Use process_report for suspicious PIDs, hot CPU, ports, or project-local processes.",
		"Use skills_inventory for Codex skill inventory questions.",
		"Use search_context for unfamiliar project terms, codenames, aliases, or concepts.",
		"Use search_boss_sessions only when the user asks to recall earlier Boss Chat turns.",
		"Use context_command only when the user asks to inspect or quote linked engineer or agent-task transcript output and an exact ctx command can be formed.",
		"Do not answer the user. Return only the structured route.",
	}, "\n")
}

func bossActionPlannerUserText(req AssistantRequest, toolResults []bossToolResult, forceAnswer bool) string {
	var b strings.Builder
	b.WriteString(requestContextBrief(req))
	b.WriteString("\n\nRecent chat:\n")
	for _, message := range trimChatHistory(req.Messages, 18) {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		b.WriteString(normalizeChatRole(message.Role))
		b.WriteString(": ")
		b.WriteString(clipText(content, 1200))
		b.WriteString("\n")
	}
	if len(toolResults) > 0 {
		b.WriteString("\nTool results already gathered:\n")
		for _, result := range toolResults {
			b.WriteString("[")
			b.WriteString(strings.TrimSpace(result.Name))
			b.WriteString("]\n")
			b.WriteString(strings.TrimSpace(result.Text))
			b.WriteString("\n\n")
		}
	}
	if forceAnswer {
		b.WriteString("\nYou must choose kind=\"answer\", kind=\"propose_control\", or kind=\"propose_goal\" now. Use the gathered data; do not request more read-only queries. If the gathered data resolves a visible review/waiting agent task with no remaining work, choose kind=\"propose_control\" with control_capability=\"agent_task.close\" instead of a plain answer. If the user asks to remove, erase, archive, hide, close, get rid of, or clear from the active record multiple delegated agent tasks and gathered data identifies more than one task id, choose kind=\"propose_goal\" with goal_kind=\"agent_task_cleanup\" and put all selected ids in goal_resources. If exactly one delegated task should be removed and the task id is known, choose kind=\"propose_control\" with control_capability=\"agent_task.close\" and task_close_status=\"archived\". This applies to open/review/waiting tasks too; do not use task_close_status=\"waiting\" for cleanup. If gathered project data identifies kind=scratch_task and the user wants that task gone, choose kind=\"propose_control\" with control_capability=\"scratch_task.archive\". If gathered task data lacks the detail the user asked for and a task id is clear, and the user is asking for information or progress, choose kind=\"propose_control\" with control_capability=\"agent_task.continue\" and ask that same task for the missing detail. If the user asks whether commit/deploy/release is safe, or whether DB migration/schema/storage/API shape changed, do not answer from summaries; only answer if gathered data directly covers current-diff evidence, otherwise choose kind=\"propose_control\" with control_capability=\"engineer.send_prompt\" and session_mode=\"new\" for a fresh verification.\n")
	} else {
		b.WriteString("\nChoose kind=\"answer\" if you have enough data. If a visible linked task or project can likely answer the question, choose one read-only query before answering. If the user asks to remove, erase, archive, hide, close, get rid of, or clear from the active record delegated agent tasks and no task id is visible yet, choose agent_task_report with include_historical=true before answering. If the user asks to remove, clear, hide, archive, or get rid of a task/project and the state does not show whether it is a scratch task, choose project_detail before answering. Choose kind=\"propose_control\" if the user asked to delegate project work, manage/continue/solve/archive/remove an agent task, manage/continue/solve/archive/remove one agent task, or archive/remove a scratch task whose project metadata says kind=scratch_task; if fresh gathered data resolves a visible review/waiting agent task and a task id is clear; or if gathered task data lacks the detail the user asked for and the same task should be asked a sharper follow-up. Choose kind=\"propose_goal\" with goal_kind=\"agent_task_cleanup\" when the user wants multiple delegated agent task records removed/cleared/archived and the selected task ids are known. For commit/deploy/release safety, or DB migration/schema/storage/API-shape questions, do not answer from summaries; use a read-only project/context query first and then propose control_capability=\"engineer.send_prompt\" with session_mode=\"new\" if direct current-diff evidence is still missing. For a resolved review/waiting task use control_capability=\"agent_task.close\"; for any delegated task the user wants gone from the active record and exactly one task is selected, use control_capability=\"agent_task.close\" with task_close_status=\"archived\"; for multiple tasks the user wants removed, use propose_goal agent_task_cleanup; for multiple delegated task records the user wants gone use propose_goal agent_task_cleanup; for a scratch task project the user wants gone use control_capability=\"scratch_task.archive\"; for a missing detail or progress request use control_capability=\"agent_task.continue\". For multiple open agent tasks that need progress, propose one concrete next agent_task.continue rather than only giving an order.\n")
	}
	return strings.TrimSpace(b.String())
}

func bossReadOnlyRouterUserText(req AssistantRequest) string {
	var b strings.Builder
	b.WriteString(requestContextBrief(req))
	b.WriteString("\n\nRecent chat:\n")
	for _, message := range trimChatHistory(req.Messages, 8) {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		b.WriteString(normalizeChatRole(message.Role))
		b.WriteString(": ")
		b.WriteString(clipText(content, 900))
		b.WriteString("\n")
	}
	b.WriteString("\nPick one read-only route for the latest user message, or kind=\"pass\" if this is not a single-query inspection request.")
	return strings.TrimSpace(b.String())
}

func bossDirectMessages(req AssistantRequest) []llm.TextMessage {
	messages := []llm.TextMessage{{
		Role:    "user",
		Content: strings.TrimSpace(requestContextBrief(req)),
	}}
	for _, message := range trimChatHistory(req.Messages, 16) {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.TextMessage{
			Role:    normalizeChatRole(message.Role),
			Content: content,
		})
	}
	return messages
}

func bossFinalAnswerMessages(req AssistantRequest, toolResults []bossToolResult, plannerAnswer string) []llm.TextMessage {
	messages := bossDirectMessages(req)
	var b strings.Builder
	if len(toolResults) > 0 {
		b.WriteString("Read-only tool results gathered for this turn:\n")
		for _, result := range toolResults {
			b.WriteString("[")
			b.WriteString(strings.TrimSpace(result.Name))
			b.WriteString("]\n")
			b.WriteString(strings.TrimSpace(result.Text))
			b.WriteString("\n\n")
		}
	}
	if draft := strings.TrimSpace(plannerAnswer); draft != "" {
		b.WriteString("Structured planner draft:\n")
		b.WriteString(draft)
		b.WriteString("\n\n")
	}
	b.WriteString("Answer the user's latest Boss Chat message now. Use the gathered data, keep the coworker-update style, and do not mention tool calls unless the user asks about them. Preserve the user's source, metric, timeframe, negations, and explicit exclusions when deciding whether gathered evidence satisfies the request; if the evidence covers a different source or metric, say it is not satisfied yet instead of smoothing over the mismatch. Do not claim commit/deploy/release safety or no DB migration unless the gathered data directly covers the current diff, migrations, schema, storage, or API shape.")
	messages = append(messages, llm.TextMessage{
		Role:    "user",
		Content: strings.TrimSpace(b.String()),
	})
	return messages
}

func requestContextBrief(req AssistantRequest) string {
	now := time.Now()
	parts := []string{
		"Current exchange time: " + formatBossTimestamp(now),
		"Current app state brief:\n" + strings.TrimSpace(req.StateBrief),
	}
	if brief := BuildViewContextBrief(req.View, now); brief != "" {
		parts = append(parts, brief)
	}
	return strings.Join(parts, "\n\n")
}

func bossResourceRefSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind":         map[string]any{"type": "string", "enum": []string{string(control.ResourceProject), string(control.ResourceEngineerSession), string(control.ResourceTodo), string(control.ResourceAgentTask), string(control.ResourceProcess), string(control.ResourcePort), string(control.ResourceFile)}},
			"id":           map[string]any{"type": "string"},
			"path":         map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"provider":     map[string]any{"type": "string", "enum": []string{"", string(control.ProviderAuto), string(control.ProviderCodex), string(control.ProviderOpenCode), string(control.ProviderClaudeCode)}},
			"session_id":   map[string]any{"type": "string"},
			"todo_id":      map[string]any{"type": "integer"},
			"pid":          map[string]any{"type": "integer"},
			"port":         map[string]any{"type": "integer"},
			"label":        map[string]any{"type": "string"},
		},
		"required": []string{"kind", "id", "path", "project_path", "provider", "session_id", "todo_id", "pid", "port", "label"},
	}
}

func bossReadOnlyRouteSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string",
				"enum": []string{
					bossReadOnlyRoutePass,
					bossActionListProjects,
					bossActionProjectDetail,
					bossActionSessionClassifications,
					bossActionTodoReport,
					bossActionAgentTaskReport,
					bossActionCurrentTUI,
					bossActionAssessmentQueue,
					bossActionProcessReport,
					bossActionSearchContext,
					bossActionSearchBossSessions,
					bossActionContextCommand,
					bossActionSkillsInventory,
					bossActionGoalRunReport,
				},
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{"", "selected"},
				"description": "Use selected only when the user explicitly asks about the selected classic TUI project.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search text for search_context/search_boss_sessions; exact goal run id for goal_run_report when known; otherwise empty.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "For context_command, one exact ctx command; otherwise empty.",
			},
			"project_path": map[string]any{
				"type":        "string",
				"description": "Exact project path for project-specific queries, or empty.",
			},
			"project_name": map[string]any{
				"type":        "string",
				"description": "Exact project name for project-specific queries, or empty.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Exact session id for assessment/session queries, or empty.",
			},
			"include_historical": map[string]any{
				"type":        "boolean",
				"description": "Whether historical/archived records are needed.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Requested result limit, or 0 for default.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Short private reason for the route, or empty.",
			},
		},
		"required": []string{
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
		},
	}
}

func bossActionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string",
				"enum": []string{
					bossActionAnswer,
					bossActionListProjects,
					bossActionProjectDetail,
					bossActionSessionClassifications,
					bossActionTodoReport,
					bossActionAgentTaskReport,
					bossActionCurrentTUI,
					bossActionAssessmentQueue,
					bossActionProcessReport,
					bossActionSearchContext,
					bossActionSearchBossSessions,
					bossActionContextCommand,
					bossActionSkillsInventory,
					bossActionGoalRunReport,
					bossActionProposeControl,
					bossActionProposeGoal,
				},
			},
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
				"enum":        []string{"", string(control.CapabilityEngineerSendPrompt), string(control.CapabilityAgentTaskCreate), string(control.CapabilityAgentTaskContinue), string(control.CapabilityAgentTaskClose), string(control.CapabilityScratchTaskArchive)},
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
				"enum":        []string{"", string(control.AgentTaskKindAgent), string(control.AgentTaskKindSubagent)},
				"description": "For agent_task.create proposals: agent or subagent. Empty is treated as agent.",
			},
			"parent_task_id": map[string]any{
				"type":        "string",
				"description": "For subagent task creation, the parent agent task id. Otherwise empty.",
			},
			"task_close_status": map[string]any{
				"type":        "string",
				"enum":        []string{"", string(control.AgentTaskCloseCompleted), string(control.AgentTaskCloseArchived), string(control.AgentTaskCloseWaiting)},
				"description": "For agent_task.close proposals: completed, archived, or waiting. Use archived for remove/erase/archive/hide/close/get-rid cleanup requests, even for open tasks; use waiting only when intentionally keeping the task open for later review. Empty is treated as completed.",
			},
			"task_summary": map[string]any{
				"type":        "string",
				"description": "For agent_task.close proposals, a concise durable summary. Otherwise empty.",
			},
			"engineer_provider": map[string]any{
				"type":        "string",
				"enum":        []string{"", string(control.ProviderAuto), string(control.ProviderCodex), string(control.ProviderOpenCode), string(control.ProviderClaudeCode)},
				"description": "For engineer.send_prompt and agent task launch proposals: auto, codex, opencode, claude_code. Empty is treated as auto.",
			},
			"session_mode": map[string]any{
				"type":        "string",
				"enum":        []string{"", string(control.SessionModeResumeOrNew), string(control.SessionModeNew)},
				"description": "For engineer.send_prompt proposals: new by default for standalone project work; resume_or_new only for explicit resume/continue, same-topic follow-up, or active-work steering. For agent_task.continue: resume_or_new unless the user explicitly asks for a fresh task session.",
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
				"description": "For agent_task.create proposals, resources touched by the task.",
			},
			"goal_kind": map[string]any{
				"type":        "string",
				"enum":        []string{"", bossrun.GoalKindAgentTaskCleanup},
				"description": "For kind=propose_goal. Initial supported value is agent_task_cleanup.",
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
				"enum":        []string{"", string(control.RiskRead), string(control.RiskWrite), string(control.RiskExternal), string(control.RiskDestructive)},
				"description": "For kind=propose_goal, the maximum risk approved by this grant. Use write for agent_task_cleanup.",
			},
			"goal_resources": map[string]any{
				"type":        "array",
				"items":       bossResourceRefSchema(),
				"description": "For kind=propose_goal, resources the run is authorized to act on. For agent_task_cleanup, each selected task is kind=agent_task with id.",
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
				"items":       map[string]any{"type": "string", "enum": []string{string(control.CapabilityEngineerSendPrompt), string(control.CapabilityAgentTaskCreate), string(control.CapabilityAgentTaskContinue), string(control.CapabilityAgentTaskClose), string(control.CapabilityScratchTaskArchive)}},
				"description": "For kind=propose_goal, primitive capabilities the approved run may execute. For agent_task_cleanup, use agent_task.close.",
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
						"kind":       map[string]any{"type": "string", "enum": []string{string(bossrun.PlanStepObserve), string(bossrun.PlanStepClassify), string(bossrun.PlanStepSelect), string(bossrun.PlanStepPropose), string(bossrun.PlanStepAct), string(bossrun.PlanStepDelegate), string(bossrun.PlanStepAwait), string(bossrun.PlanStepVerify), string(bossrun.PlanStepBranch), string(bossrun.PlanStepReport)}},
						"title":      map[string]any{"type": "string"},
						"capability": map[string]any{"type": "string", "enum": []string{"", string(control.CapabilityEngineerSendPrompt), string(control.CapabilityAgentTaskCreate), string(control.CapabilityAgentTaskContinue), string(control.CapabilityAgentTaskClose), string(control.CapabilityScratchTaskArchive)}},
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
		"required": []string{
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
	}
}

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
	case bossActionListProjects, bossActionProjectDetail, bossActionSessionClassifications, bossActionTodoReport, bossActionAgentTaskReport, bossActionCurrentTUI, bossActionAssessmentQueue, bossActionProcessReport, bossActionSearchContext, bossActionSearchBossSessions, bossActionContextCommand, bossActionSkillsInventory, bossActionGoalRunReport:
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

func normalizeChatRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func addLLMUsage(total *model.LLMUsage, usage model.LLMUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.TotalTokens += usage.TotalTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.EstimatedCostUSD += usage.EstimatedCostUSD
}

func runBossText(ctx context.Context, runner llm.TextRunner, req llm.TextRequest, emit func(AssistantStreamEvent)) (llm.TextResponse, error) {
	if runner == nil {
		return llm.TextResponse{}, errors.New("boss chat needs text chat inference for this request")
	}
	if streamer, ok := runner.(llm.StreamingTextRunner); ok {
		return streamer.RunTextStream(ctx, req, func(event llm.TextStreamEvent) error {
			emitAssistantDelta(emit, event.Delta)
			return nil
		})
	}
	resp, err := runner.RunText(ctx, req)
	if err != nil {
		return llm.TextResponse{}, err
	}
	emitAssistantDelta(emit, resp.OutputText)
	return resp, nil
}

func emitAssistantDelta(emit func(AssistantStreamEvent), delta string) {
	if emit == nil || delta == "" {
		return
	}
	emit(AssistantStreamEvent{
		Kind:  AssistantStreamTextDelta,
		Delta: delta,
	})
}

func emitToolCall(emit func(AssistantStreamEvent), call, state string) {
	if emit == nil || strings.TrimSpace(call) == "" {
		return
	}
	emit(AssistantStreamEvent{
		Kind:      AssistantStreamToolCall,
		ToolCall:  strings.TrimSpace(call),
		ToolState: strings.TrimSpace(state),
	})
}

func describeBossAction(action bossAction) string {
	kind := normalizeBossActionKind(action.Kind)
	switch kind {
	case bossActionProjectDetail:
		target := firstNonEmpty(action.ProjectName, action.ProjectPath, action.Target)
		if target != "" {
			return kind + " " + clipText(target, 80)
		}
	case bossActionSearchContext, bossActionSearchBossSessions:
		if query := strings.TrimSpace(action.Query); query != "" {
			return kind + " " + quoteForStatus(clipText(query, 80))
		}
	case bossActionContextCommand:
		if command := strings.TrimSpace(action.Command); command != "" {
			return kind + " " + clipText(command, 120)
		}
	case bossActionProcessReport:
		target := firstNonEmpty(action.ProjectName, action.ProjectPath, action.Target)
		if target != "" {
			return kind + " " + clipText(target, 80)
		}
	case bossActionProposeControl:
		if capability := strings.TrimSpace(action.ControlCapability); capability != "" {
			return kind + " " + capability
		}
	case bossActionProposeGoal:
		if goalKind := strings.TrimSpace(action.GoalKind); goalKind != "" {
			return kind + " " + goalKind
		}
	case bossActionSkillsInventory:
		return kind
	case bossActionGoalRunReport:
		if query := strings.TrimSpace(action.Query); query != "" {
			return kind + " " + clipText(query, 80)
		}
		return kind
	case bossActionSessionClassifications, bossActionTodoReport:
		target := firstNonEmpty(action.ProjectName, action.ProjectPath, action.SessionID, action.Target)
		if target != "" {
			return kind + " " + clipText(target, 80)
		}
	case bossActionAgentTaskReport:
		return kind
	}
	if kind == "" {
		return "tool"
	}
	return kind
}

func quoteForStatus(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%q", value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
