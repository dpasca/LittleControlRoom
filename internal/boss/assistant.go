package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

const bossAssistantReasoningEffort = "low"

type ChatMessage struct {
	Role    string
	Content string
	At      time.Time
}

type AssistantRequest struct {
	StateBrief string
	Snapshot   StateSnapshot
	View       ViewContext
	Messages   []ChatMessage
}

type AssistantResponse struct {
	Content string
	Model   string
	Usage   model.LLMUsage
}

type Assistant struct {
	runner  llm.TextRunner
	planner llm.JSONSchemaRunner
	query   *QueryExecutor
	model   string
	backend config.AIBackend
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
	return &Assistant{
		runner:  runner,
		planner: planner,
		query:   newQueryExecutor(svc.Store()),
		model:   strings.TrimSpace(modelName),
		backend: backend,
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
	if a.planner != nil && a.query != nil {
		return a.replyWithTools(ctx, req)
	}
	return a.replyDirect(ctx, req)
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
	if err := validateBossAction(action); err != nil {
		return response, bossAction{}, err
	}
	if forceAnswer && action.Kind != bossActionAnswer {
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
		return "Boss chat currently uses direct API inference, not embedded coding-agent sessions. Choose OpenAI API, MLX, or Ollama for boss chat while keeping project reports on your preferred backend."
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
		"You are the calm project-management assistant inside Little Control Room.",
		"Help the user decide what deserves attention across coding projects.",
		"Use the compact app-state brief, but do not invent facts that are not present there.",
		"Keep replies concise, concrete, and friendly. Prefer clear next steps over dashboards.",
		"You cannot change projects or panels yet. If an action is needed, say what you would inspect or do next.",
		"The classic TUI remains available for detailed micromanagement.",
	}, "\n")
}

const bossAssistantMaxToolRounds = 4

func bossActionPlannerSystemPrompt() string {
	return strings.Join([]string{
		"You are the calm project-management assistant inside Little Control Room.",
		"You decide whether to answer now or request exactly one read-only query before answering.",
		"Use queries when the user asks about a concrete project, TODOs, assessment status, current TUI state, codenames, aliases, concepts, or anything that requires more than the compact brief.",
		"Available read-only query kinds: list_projects, project_detail, session_classifications, todo_report, current_tui, assessment_queue, search_context.",
		"Use search_context when the user asks what a codename, acronym, feature, branch phrase, or unfamiliar term refers to; it searches project metadata, summaries, assessments, TODOs, and cached assistant-session text.",
		"For project-specific queries, use project_path when a path is available or project_name when the user gives an exact project name.",
		"Do not infer a project from hidden UI cursor state; if the target is ambiguous, ask the user to name the project.",
		"Do not invent facts. After query results are provided, answer from those results and the app-state brief.",
		"Never claim you changed files, projects, TODOs, snoozes, panels, or sessions; these tools are report-only.",
		"Keep final answers concise, concrete, friendly, and focused on what deserves attention next.",
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
		b.WriteString("\nYou must choose kind=\"answer\" now. Use the gathered data; do not request more queries.\n")
	} else {
		b.WriteString("\nChoose kind=\"answer\" if you have enough data, otherwise choose one read-only query kind.\n")
	}
	return strings.TrimSpace(b.String())
}

func requestContextBrief(req AssistantRequest) string {
	parts := []string{"Current app state brief:\n" + strings.TrimSpace(req.StateBrief)}
	if brief := BuildViewContextBrief(req.View, time.Now()); brief != "" {
		parts = append(parts, brief)
	}
	return strings.Join(parts, "\n\n")
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
					bossActionCurrentTUI,
					bossActionAssessmentQueue,
					bossActionSearchContext,
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
				"description": "Search text when kind is search_context; otherwise empty.",
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
			"project_path",
			"project_name",
			"session_id",
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
	action.ProjectPath = strings.TrimSpace(action.ProjectPath)
	action.ProjectName = strings.TrimSpace(action.ProjectName)
	action.SessionID = strings.TrimSpace(action.SessionID)
	action.Reason = strings.TrimSpace(action.Reason)
}

func validateBossAction(action bossAction) error {
	switch action.Kind {
	case bossActionAnswer:
		return nil
	case bossActionListProjects, bossActionProjectDetail, bossActionSessionClassifications, bossActionTodoReport, bossActionCurrentTUI, bossActionAssessmentQueue, bossActionSearchContext:
		return nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
