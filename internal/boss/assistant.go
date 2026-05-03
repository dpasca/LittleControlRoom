package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

const bossAssistantReasoningEffort = "high"

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
	Content           string
	Model             string
	Usage             model.LLMUsage
	ControlInvocation *control.Invocation
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
	query := newQueryExecutorWithBossSessions(svc.Store(), newBossSessionStoreForService(svc))
	if query != nil {
		query.codexHome = svc.Config().CodexHome
		query.codexHomeFallbacks = bossDefaultCodexHomeFallbacks(query.codexHome)
		query.openCodeHome = svc.Config().OpenCodeHome
	}
	return &Assistant{
		runner:  runner,
		planner: planner,
		query:   query,
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
	if a.planner != nil && a.query != nil {
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
		if normalizeBossActionKind(action.Kind) == bossActionProposeControl {
			return response, bossAction{}, wrapControlProposalError(err)
		}
		return response, bossAction{}, err
	}
	if forceAnswer && action.Kind != bossActionAnswer && action.Kind != bossActionProposeControl {
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
		"Codex, OpenCode, and Claude Code work-session transcripts are called engineer sessions.",
		"Your own prior messages are Boss Chat messages. Engineer-session messages are separate and may need engineer transcript lookup.",
		"Open agent tasks are delegated engineer work items, separate from project TODOs; include them when the user asks about active tasks, background agents, or delegated work.",
		"Linked worktrees under the same worktree root are part of the same repo effort; treat their recent work as relevant to repo-level status.",
		"If the user says 'the assistant' or 'the AI assistant' about project work, treat that as likely meaning the engineer session unless they clearly mean Boss Chat.",
		"Act like a high-level extension of the active engineer sessions.",
		"Live engineer sessions may have readable names such as Ada or Grace. Use those names for active delegated work when the context provides them; do not invent a name if none is present.",
		"Assume an ongoing coworker chat: skip onboarding, capability pitches, generic menus, and optional handoff offers.",
		"Assume the user tracks many things and wants the highest-level read first, not implementation telemetry.",
		"Do not describe UI mechanics such as timers, Attention rows, temporary activity lines, or tool-call notices; those are implicit.",
		"When acknowledging delegated work, paraphrase the intent at boss level instead of echoing the exact engineer prompt.",
		"When summarizing engineer output, give the meaningful result and what still needs attention; omit raw logs and mechanical transcript details unless they change the decision.",
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
		"You decide whether to answer now, request exactly one read-only query, or propose exactly one control action for user confirmation.",
		"Codex, OpenCode, and Claude Code work-session transcripts are called engineer sessions.",
		"Your own prior messages are Boss Chat messages. Engineer-session messages are separate and may need engineer transcript lookup.",
		"Linked worktrees under the same worktree root are part of the same repo effort; treat their recent work as relevant to repo-level status.",
		"If the user says 'the assistant' or 'the AI assistant' about project work, treat that as likely meaning the engineer session unless they clearly mean Boss Chat.",
		"Act like a high-level extension of the active engineer sessions.",
		"Live engineer sessions may have readable names such as Ada or Grace. Use those names for active delegated work when the context provides them; do not invent a name if none is present.",
		"Use queries when the user asks about a concrete project, project TODOs, open agent tasks, delegated/background agents, assessment status, current TUI state, Codex skills, suspicious PIDs/processes/CPU, codenames, aliases, concepts, or anything that requires more than the compact brief.",
		"Available read-only query kinds: list_projects, project_detail, session_classifications, todo_report, agent_task_report, current_tui, assessment_queue, process_report, search_context, search_boss_sessions, context_command, skills_inventory.",
		"Use agent_task_report when the user asks about open or active agent tasks, delegated tasks, background agents, or what the agents are doing.",
		"Use todo_report for project TODOs; project TODOs are separate from delegated agent tasks.",
		"Do not answer that there are no open agent tasks when the app-state brief lists open delegated agent tasks.",
		"Use process_report when the user asks about suspicious PIDs, hot CPU, orphaned processes, project-local Node/server processes, or whether stale dev servers are still running.",
		"Use skills_inventory when the user asks about Codex skills, stale skills, installed skills, skill duplicates, or skill management.",
		"Available control action kind: propose_control with control_capability equal to engineer.send_prompt, agent_task.create, agent_task.continue, or agent_task.close.",
		"Use engineer.send_prompt only for explicit project/repo work on a loaded project. Do not use it for host operations or generic temporary work.",
		"Use agent_task.create for temporary delegated work with no natural loaded project, including host/process/browser/system investigation. Use a generic agent task with resources and capabilities; do not encode special domains as task kinds.",
		"Use agent_task.continue when the user asks to hit an existing open agent task again. Use agent_task.close when the task is done, should wait, or should be archived.",
		"If a visible agent task is in review/waiting and fresh read-only evidence resolves it with no remaining work, propose agent_task.close with status completed instead of merely answering that the task is still open.",
		"A status or situation question is enough to close a review/waiting agent task when the gathered evidence directly says the review found no issue, completed the check, or needs no further action.",
		"When the user asks to solve, clear, finish, continue, or make progress on open agent tasks, treat that as a request to manage those agent tasks, not as a request for only a status answer.",
		"If the user asks to solve or clear multiple open agent tasks, propose exactly one agent_task.continue for the next concrete task. Prefer the user-named task; otherwise choose the stalest or highest-risk task from the available agent-task evidence, and mention that the remaining tasks can follow after this one is confirmed.",
		"If the user assents to a prior Boss Chat plan for clearing open agent tasks, propose agent_task.continue for the next task in that plan instead of restating the plan.",
		"Do not answer with only a priority order when the user asks Boss to get open agent tasks solved and a task id is visible.",
		"Do not use the Little Control Room project or another unrelated active engineer session as a proxy venue for generic or host-level work.",
		"Before propose_control, resolve ambiguous targets with read-only queries or ask the user to name the project. Do not infer a project from hidden UI cursor state.",
		"For engineer.send_prompt, set provider to auto unless the user explicitly names Codex, OpenCode, or Claude Code. Set session_mode to new only when the user asks for a fresh/new session; otherwise use resume_or_new. Set reveal true only when the user asks to show/open the session.",
		"Wanting to see an app, page, server, screenshot, or browser result is not a request to reveal the engineer transcript pane; keep reveal false unless the user explicitly asks to show, open, or watch that engineer session.",
		"For agent_task.create, task_kind must be agent unless parent_task_id is set and the user asked for a subagent; put affected projects, PIDs, ports, files, sessions, or related tasks in resources; put allowed action namespaces such as process.inspect, process.terminate, repo.edit, test.run, browser.inspect in capabilities.",
		"For agent_task.continue, include task_id and a fresh prompt. For agent_task.close, include task_id, task_close_status, task_summary, and close_session.",
		"For propose_control, the prompt field is the exact instruction to send to the engineer session or task. Keep it actionable and include only the relevant work request.",
		"Use context_command for command-shaped context lookup: ctx search engineer, ctx show, ctx show agent_task, ctx recent engineer, or ctx search boss.",
		"Use ctx search engineer when the user asks to recall, quote, verify, or inspect what an engineer session said. Use ctx show on the returned handle before quoting or correcting exact details.",
		"When the user asks for the output or result of an open agent task, use ctx show agent_task:<task-id>; if only an engineer session id is available, use ctx show engineer:<session-id>.",
		"If the latest Boss Chat assistant message says an agent task was created or continued, and the user follows up with a vague request like \"please try again\" without new work instructions, first read the task output with ctx show agent_task:<task-id> instead of proposing agent_task.continue.",
		"Use ctx search boss only when the user asks to recall, search, or quote earlier Boss Chat conversations.",
		"Use search_context when the user asks what a codename, acronym, feature, branch phrase, or unfamiliar term refers to; it searches project metadata, summaries, assessments, TODOs, and cached engineer-session text.",
		"Use search_boss_sessions when the user asks to recall, search, or quote earlier boss chat conversations; it returns XML-like boss_session and turn snippets from saved local boss-chat transcripts.",
		"Do not answer that a concrete term is unknown until search_context has been tried.",
		"For codename or alias status questions, search_context should usually come first; after it finds one project path, inspect project_detail before answering.",
		"Prefer project_detail when the answer depends on a project's current state, especially after another query identifies the relevant project.",
		"When project_detail includes live engineer session context, treat it as fresher than stored assessments or board stats.",
		"When project_detail includes worktree family activity, treat linked entries as current work on the same repo, not unrelated projects.",
		"For project-specific queries, use project_path when a path is available or project_name when the user gives an exact project name.",
		"Do not infer a project from hidden UI cursor state; if the target is ambiguous, ask the user to name the project.",
		"Do not invent facts. After query results are provided, answer from those results and the app-state brief.",
		"Never claim you changed files, projects, TODOs, snoozes, panels, or sessions. Read-only query tools are report-only; control actions are proposals that need user confirmation before execution.",
		"Final answers should sound like a concise coworker update: turn tool output into judgment instead of mirroring its bullet structure, and avoid capability pitches or optional menus.",
		"Do not describe UI mechanics such as timers, Attention rows, temporary activity lines, or tool-call notices; those are implicit.",
		"When acknowledging delegated work, paraphrase the intent at boss level instead of echoing the exact engineer prompt.",
		"When summarizing engineer output, give the meaningful result and what still needs attention; omit raw logs and mechanical transcript details unless they change the decision.",
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
		b.WriteString("\nYou must choose kind=\"answer\" or kind=\"propose_control\" now. Use the gathered data; do not request more read-only queries. If the gathered data resolves a visible review/waiting agent task with no remaining work, choose kind=\"propose_control\" with control_capability=\"agent_task.close\" instead of a plain answer.\n")
	} else {
		b.WriteString("\nChoose kind=\"answer\" if you have enough data, choose kind=\"propose_control\" if the user asked to delegate project work or manage/continue/clear/solve an agent task or if fresh gathered data resolves a visible review/waiting agent task and a task id is clear; for a resolved review/waiting task use control_capability=\"agent_task.close\". Otherwise choose one read-only query kind. For multiple open agent tasks, propose one concrete next agent_task.continue rather than only giving an order.\n")
	}
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
	b.WriteString("Answer the user's latest Boss Chat message now. Use the gathered data, keep the coworker-update style, and do not mention tool calls unless the user asks about them.")
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
					bossActionProposeControl,
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
				"description": "Search text when kind is search_context or search_boss_sessions; optional fallback command text when kind is context_command; otherwise empty.",
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
				"enum":        []string{"", string(control.CapabilityEngineerSendPrompt), string(control.CapabilityAgentTaskCreate), string(control.CapabilityAgentTaskContinue), string(control.CapabilityAgentTaskClose)},
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
				"description": "For agent_task.close proposals: completed, archived, or waiting. Empty is treated as completed.",
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
				"description": "For engineer.send_prompt proposals: resume_or_new unless the user explicitly asks for a new session.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "For engineer.send_prompt, agent_task.create, or agent_task.continue proposals, the exact prompt to send. Otherwise empty.",
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
				"type": "array",
				"items": map[string]any{
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
				},
				"description": "For agent_task.create proposals, resources touched by the task.",
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
			"reveal",
			"close_session",
			"capabilities",
			"resources",
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
	action.Capabilities = normalizeBossActionStringList(action.Capabilities)
	action.Resources = normalizeBossActionResources(action.Resources)
	action.Reason = strings.TrimSpace(action.Reason)
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

func validateBossAction(action bossAction) error {
	switch action.Kind {
	case bossActionAnswer:
		return nil
	case bossActionListProjects, bossActionProjectDetail, bossActionSessionClassifications, bossActionTodoReport, bossActionAgentTaskReport, bossActionCurrentTUI, bossActionAssessmentQueue, bossActionProcessReport, bossActionSearchContext, bossActionSearchBossSessions, bossActionContextCommand, bossActionSkillsInventory:
		return nil
	case bossActionProposeControl:
		_, _, err := controlProposalFromBossAction(action)
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
	case bossActionSkillsInventory:
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
