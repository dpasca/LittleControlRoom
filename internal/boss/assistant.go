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
	runner       llm.TextRunner
	planner      llm.JSONSchemaRunner
	queryRouter  llm.JSONSchemaRunner
	query        *QueryExecutor
	model        string
	utilityModel string
	backend      config.AIBackend
}

func NewAssistant(svc *service.Service) *Assistant {
	if svc == nil {
		return &Assistant{}
	}
	runner, modelName, backend := svc.NewBossTextRunner()
	planner, plannerModel, plannerBackend := svc.NewBossJSONRunner()
	queryRouter, utilityModel, _ := svc.NewBossUtilityJSONRunner()
	if strings.TrimSpace(modelName) == "" {
		modelName = plannerModel
	}
	if queryRouter == nil {
		queryRouter = planner
	}
	if strings.TrimSpace(utilityModel) == "" {
		utilityModel = modelName
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
		runner:       runner,
		planner:      planner,
		queryRouter:  queryRouter,
		query:        query,
		model:        strings.TrimSpace(modelName),
		utilityModel: strings.TrimSpace(utilityModel),
		backend:      backend,
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
	if utilityModel := strings.TrimSpace(a.utilityModel); utilityModel != "" && utilityModel != strings.TrimSpace(a.model) {
		return fmt.Sprintf("Boss chat via %s (utility %s)", a.model, utilityModel)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
		return AssistantResponse{}, errors.New("boss chat needs a chat model; set boss_helm_model, boss_chat_model, or " + brand.BossAssistantModelEnvVar)
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
			emitAssistantDelta(emit, answer)
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

type bossTodoAddPolicyReview struct {
	AllowTodoAdd                 bool   `json:"allow_todo_add"`
	ReplacementControlCapability string `json:"replacement_control_capability"`
	ReplacementSessionMode       string `json:"replacement_session_mode"`
	Reason                       string `json:"reason"`
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
		Model:           firstNonEmpty(strings.TrimSpace(a.utilityModel), strings.TrimSpace(a.model)),
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
	if reviewed, changed, reviewResponse, err := a.reviewTodoAddPolicy(ctx, req, action); err != nil {
		if ctx.Err() != nil {
			return response, bossAction{}, err
		}
	} else {
		addLLMUsage(&response.Usage, reviewResponse.Usage)
		if changed {
			action = reviewed
			normalizeBossAction(&action)
			prepareBossActionForRequest(&action, req)
		}
	}
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

func (a *Assistant) reviewTodoAddPolicy(ctx context.Context, req AssistantRequest, action bossAction) (bossAction, bool, llm.JSONSchemaResponse, error) {
	if a == nil || a.queryRouter == nil || !bossActionIsTodoAddProposal(action) {
		return action, false, llm.JSONSchemaResponse{}, nil
	}
	response, err := a.queryRouter.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           firstNonEmpty(strings.TrimSpace(a.utilityModel), strings.TrimSpace(a.model)),
		SystemText:      bossTodoAddPolicyReviewSystemPrompt(),
		UserText:        bossTodoAddPolicyReviewUserText(req, action),
		SchemaName:      "boss_todo_add_policy_review",
		Schema:          bossTodoAddPolicyReviewSchema(),
		ReasoningEffort: bossReadOnlyRouterReasoningEffort,
	})
	if err != nil {
		return action, false, response, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return action, false, response, errors.New("boss chat returned no todo.add policy review")
	}
	var review bossTodoAddPolicyReview
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &review); err != nil {
		return action, false, response, fmt.Errorf("decode boss todo.add policy review: %w", err)
	}
	if review.AllowTodoAdd {
		return action, false, response, nil
	}
	if control.CapabilityName(strings.TrimSpace(review.ReplacementControlCapability)) != control.CapabilityEngineerSendPrompt {
		return action, false, response, nil
	}
	replacement := action
	replacement.ControlCapability = string(control.CapabilityEngineerSendPrompt)
	replacement.EngineerProvider = firstNonEmpty(replacement.EngineerProvider, string(control.ProviderAuto))
	replacement.SessionMode = string(control.SessionModeNew)
	if mode := control.NormalizeSessionMode(review.ReplacementSessionMode); mode != "" {
		replacement.SessionMode = string(mode)
	}
	replacement.Prompt = firstNonEmpty(replacement.Prompt, replacement.TodoText)
	replacement.TodoID = 0
	replacement.TodoLabel = ""
	replacement.TodoText = ""
	replacement.TodoEvidence = ""
	if reason := strings.TrimSpace(review.Reason); reason != "" {
		replacement.Reason = "TODO policy review: " + reason
	}
	return replacement, true, response, nil
}

func bossActionIsTodoAddProposal(action bossAction) bool {
	return normalizeBossActionKind(action.Kind) == bossActionProposeControl &&
		control.CapabilityName(strings.TrimSpace(action.ControlCapability)) == control.CapabilityTodoAdd
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
