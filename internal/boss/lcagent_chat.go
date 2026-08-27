package boss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"lcroom/internal/agentquery"
	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/demorecord"
	"lcroom/internal/lcagent"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

const (
	helpChatAgentMaxTurns         = 6
	helpChatAgentProgressInterval = 2 * time.Second
)

type helpChatAgentTerminal struct {
	ControlInvocation *control.Invocation
	GoalProposal      *bossrun.GoalProposal
}

func (a *Assistant) replyWithLCAgent(ctx context.Context, req AssistantRequest, emit func(AssistantStreamEvent)) (AssistantResponse, error) {
	if a == nil || a.agentModel == nil {
		return AssistantResponse{}, errors.New("Chat needs an LCAgent conversation model")
	}
	if response, handled, err := a.replyStructuredHandle(ctx, req, emit); err != nil {
		return AssistantResponse{}, err
	} else if handled {
		response.Model = firstNonEmpty(response.Model, strings.TrimSpace(a.model))
		return response, nil
	}

	prepared, contextUsage, contextModel, err := a.preparePromptContext(ctx, req)
	if err != nil {
		return AssistantResponse{}, err
	}
	req = prepared
	tools, err := a.helpChatAgentTools(req)
	if err != nil {
		return AssistantResponse{}, err
	}
	runtime, err := lcagent.NewAgentRuntime(lcagent.AgentRuntimeConfig{
		Model:            a.agentModel,
		Provider:         a.agentProvider,
		Tools:            tools,
		MaxTurns:         helpChatAgentMaxTurns,
		ProgressInterval: helpChatAgentProgressInterval,
		Completion:       helpChatAgentCompletionOptions(a.agentProvider, a.agentModel.Model()),
		Emit: func(event lcagent.AgentRuntimeEvent) {
			emitHelpChatAgentEvent(emit, event)
		},
	})
	if err != nil {
		return AssistantResponse{}, err
	}

	agentResponse, err := runtime.Run(ctx, lcagent.AgentRuntimeRequest{
		SystemPrompt: helpChatAgentSystemPrompt(req),
		Messages:     helpChatAgentMessages(req),
	})
	if err != nil {
		return AssistantResponse{}, err
	}
	addLLMUsage(&agentResponse.Usage, contextUsage)
	content := appendHelpChatAgentReceipts(agentResponse.Content, agentResponse.Receipts)
	if content == "" {
		return AssistantResponse{}, errors.New("Chat returned an empty LCAgent response")
	}
	emitAssistantDelta(emit, content)

	response := AssistantResponse{
		Content:       content,
		Model:         firstNonEmpty(strings.TrimSpace(agentResponse.Model), contextModel, strings.TrimSpace(a.model)),
		Usage:         agentResponse.Usage,
		PromptContext: req.PromptContext,
	}
	if terminal, ok := agentResponse.TerminalValue.(helpChatAgentTerminal); ok {
		response.ControlInvocation = terminal.ControlInvocation
		response.GoalProposal = terminal.GoalProposal
	}
	return response, nil
}

func helpChatAgentCompletionOptions(provider, modelName string) modeladapter.CompletionOptions {
	effort := bossAssistantReasoningEffort
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ollama", "mlx":
		effort = ""
	case "moonshot":
		if !modeladapter.MoonshotSupportsReasoningEffort(modelName) {
			effort = ""
		}
	}
	return modeladapter.CompletionOptions{ReasoningEffort: effort}
}

func helpChatAgentMessages(req AssistantRequest) []modeladapter.Message {
	direct := bossDirectMessages(req)
	messages := make([]modeladapter.Message, 0, len(direct))
	for _, message := range direct {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if role == "" || content == "" {
			continue
		}
		messages = append(messages, modeladapter.Message{Role: role, Content: content})
	}
	return messages
}

func helpChatAgentSystemPrompt(req AssistantRequest) string {
	return strings.TrimSpace(strings.Join([]string{
		bossAssistantSystemPromptForRequest(req),
		"",
		"You are running inside the lean Help Chat LCAgent profile. Use only the exact tools supplied to this turn; coding, shell, filesystem-write, browser, and generic MCP tools are intentionally unavailable.",
		"Answer directly without tools for greetings, acknowledgements, ordinary conversation, and questions already established in this same Chat session.",
		"For Little Control Room commands, keybindings, launch flags, recording, or workflows, call lookup_lcr_help before answering. Its generated help corpus is authoritative; do not say a feature is unavailable merely because you do not remember it.",
		"For persisted LCR state, use the progressive query catalog: list_lcr_queries without a domain, list it again with one exact domain, describe_lcr_query, then run_lcr_query. Do not invent query names or argument fields.",
		"For live TUI state, processes, Chat recall, linked transcript context, installed skills, or fresh repository inspection, use the matching Help Chat inspection tool.",
		"For an app mutation or engineer handoff, use list_control_capabilities, describe_control_capability, then propose_control_operation. A proposal is terminal and is not execution: the host will show the existing confirmation UI, and you must never claim it already ran.",
		"Use project.set_category for registering or organizing an existing folder in an LCR category. Use todo.add only to park explicit backlog work. Use todo.create_worktree_and_start_engineer for loaded-project implementation requested now. Use project.create_and_start_engineer for a new or untracked repository that should be worked on now.",
		"For prompt-bearing controls, preserve named sources, metrics, timeframes, negations, and exclusions in intent_excerpt, preserved_meaning, and success_condition. Do not silently broaden or substitute the task.",
		"Use propose_goal only when the user explicitly asks for a traceable multi-step LCR goal or when multiple delegated agent-task records must be cleaned up together.",
		"Tool results are evidence, not text to dump verbatim. Synthesize a concise answer and do not duplicate host-appended receipts.",
	}, "\n"))
}

func emitHelpChatAgentEvent(emit func(AssistantStreamEvent), event lcagent.AgentRuntimeEvent) {
	if emit == nil {
		return
	}
	switch event.Kind {
	case lcagent.AgentRuntimeModelStarted:
		emitProgress(emit, fmt.Sprintf("LCAgent thinking (turn %d)", event.Turn), "running")
	case lcagent.AgentRuntimeModelProgress:
		emitProgress(emit, fmt.Sprintf("LCAgent thinking (turn %d, %s)", event.Turn, compactAgentElapsed(event.Elapsed)), "running")
	case lcagent.AgentRuntimeModelFinished:
		emitProgress(emit, fmt.Sprintf("LCAgent model turn %d", event.Turn), "done")
	case lcagent.AgentRuntimeModelFailed:
		state := "error"
		label := fmt.Sprintf("LCAgent model turn %d", event.Turn)
		if event.Retrying {
			state = "running"
			label = fmt.Sprintf("LCAgent retrying turn %d in %s", event.Turn, compactAgentElapsed(event.RetryDelay))
		}
		emitProgress(emit, label, state)
	case lcagent.AgentRuntimeToolStarted:
		emitToolCall(emit, helpChatAgentToolLabel(event.Tool), "running")
	case lcagent.AgentRuntimeToolFinished:
		emitToolCall(emit, helpChatAgentToolLabel(event.Tool), "done")
	case lcagent.AgentRuntimeToolFailed:
		emitToolCall(emit, helpChatAgentToolLabel(event.Tool), "error")
	}
}

func compactAgentElapsed(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	if value < time.Second {
		return "<1s"
	}
	return value.Round(time.Second).String()
}

func helpChatAgentToolLabel(name string) string {
	switch strings.TrimSpace(name) {
	case "list_lcr_queries":
		return "discovering LCR query domains"
	case "describe_lcr_query":
		return "loading an LCR query schema"
	case "run_lcr_query":
		return "querying LCR state"
	case "list_control_capabilities":
		return "discovering LCR controls"
	case "describe_control_capability":
		return "loading an LCR control schema"
	case "propose_control_operation":
		return "preparing a confirmable LCR action"
	case "propose_goal":
		return "preparing a confirmable LCR goal"
	case "lookup_lcr_help":
		return "checking LCR help"
	case "inspect_current_tui":
		return "reading current TUI state"
	case "inspect_live_processes":
		return "inspecting project processes"
	case "search_lcr_context":
		return "searching LCR context"
	case "search_chat_sessions":
		return "searching Chat sessions"
	case "inspect_linked_context":
		return "reading linked task context"
	case "inspect_lcr_skills":
		return "reading the skill inventory"
	case "scout_repository":
		return "scouting repository files"
	default:
		return strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	}
}

func appendHelpChatAgentReceipts(answer string, receipts []string) string {
	results := make([]bossToolResult, 0, len(receipts))
	for _, receipt := range receipts {
		results = append(results, bossToolResult{UserReceipt: receipt})
	}
	return appendBossToolReceipts(answer, results)
}

func (a *Assistant) helpChatAgentTools(req AssistantRequest) ([]lcagent.AgentRuntimeTool, error) {
	queryExecutor, err := a.helpChatAgentQueryExecutor(req)
	if err != nil {
		return nil, err
	}
	tools := make([]lcagent.AgentRuntimeTool, 0, 16)
	for _, definition := range modeladapter.LCRQueryToolDefinitions() {
		definition := definition
		switch definition.Function.Name {
		case "list_lcr_queries":
			tools = append(tools, lcagent.AgentRuntimeTool{Definition: definition, Run: func(_ context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				var args struct {
					Domain string `json:"domain"`
				}
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				report, err := agentquery.ListReport(args.Domain, agentquery.ScopePortfolio, queryExecutor != nil)
				if err == nil && queryExecutor != nil {
					report["privacy_contract"] = queryExecutor.PrivacyContract()
				}
				return lcagent.AgentRuntimeToolOutcome{Result: report}, err
			}})
		case "describe_lcr_query":
			tools = append(tools, lcagent.AgentRuntimeTool{Definition: definition, Run: func(_ context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				var args struct {
					Name string `json:"name"`
				}
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				report, err := agentquery.DescribeReport(args.Name, agentquery.ScopePortfolio, "run_lcr_query")
				return lcagent.AgentRuntimeToolOutcome{Result: report}, err
			}})
		case "run_lcr_query":
			tools = append(tools, lcagent.AgentRuntimeTool{Definition: definition, Run: func(ctx context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				if queryExecutor == nil {
					return lcagent.AgentRuntimeToolOutcome{}, errors.New("LCR state queries are not connected")
				}
				var args struct {
					Query     string          `json:"query"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				report, err := queryExecutor.Execute(ctx, agentquery.Name(strings.TrimSpace(args.Query)), args.Arguments)
				return lcagent.AgentRuntimeToolOutcome{Result: report}, err
			}})
		}
	}
	tools = append(tools, a.helpChatControlTools(req)...)
	tools = append(tools, a.helpChatInspectionTools(req)...)
	tools = append(tools, a.helpChatGoalTool(req))
	return tools, nil
}

func (a *Assistant) helpChatAgentQueryExecutor(req AssistantRequest) (*agentquery.Executor, error) {
	if a == nil || a.agentQueryReader == nil {
		return nil, nil
	}
	disclosure := agentquery.DisclosureHost
	if req.View.PrivacyMode {
		disclosure = agentquery.DisclosureHidePrivate
	}
	var demoRecordings agentquery.DemoRecordingReader
	if strings.TrimSpace(a.dataDir) != "" {
		demoRecordings = demorecord.NewDiscovery(a.dataDir)
	}
	return agentquery.NewExecutor(agentquery.Options{
		Reader:         a.agentQueryReader,
		Scope:          agentquery.ScopePortfolio,
		Disclosure:     disclosure,
		DemoRecordings: demoRecordings,
	})
}

func (a *Assistant) helpChatControlTools(req AssistantRequest) []lcagent.AgentRuntimeTool {
	return []lcagent.AgentRuntimeTool{
		{
			Definition: helpChatToolDefinition("list_control_capabilities", "Discover confirmable Little Control Room control domains and compact capability summaries.", map[string]any{
				"domain": map[string]any{"type": "string", "enum": control.CapabilityDomainStrings(false), "description": "Optional exact domain. Omit it to list domains."},
			}, nil),
			Run: func(_ context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				var args struct {
					Domain string `json:"domain"`
				}
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				report, err := control.ListReport(args.Domain, control.AuthorityScopeHost, true)
				return lcagent.AgentRuntimeToolOutcome{Result: report}, err
			},
		},
		{
			Definition: helpChatToolDefinition("describe_control_capability", "Load the exact schema, risk, and confirmation contract for one registered LCR control capability.", map[string]any{
				"name": map[string]any{"type": "string", "minLength": 1, "description": "Exact capability name returned by list_control_capabilities."},
			}, []string{"name"}),
			Run: func(_ context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				var args struct {
					Name string `json:"name"`
				}
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				report, err := control.DescribeReport(args.Name, control.AuthorityScopeHost, "propose_control_operation")
				return lcagent.AgentRuntimeToolOutcome{Result: report}, err
			},
		},
		{
			Definition: helpChatToolDefinition("propose_control_operation", "Prepare one typed LCR control invocation for the host confirmation dialog. This never executes the operation.", map[string]any{
				"capability":        map[string]any{"type": "string", "enum": control.CapabilityNameStrings(false)},
				"arguments":         map[string]any{"type": "object", "description": "Exact arguments matching the described capability input_schema."},
				"request_id":        map[string]any{"type": "string", "description": "Optional stable idempotency key."},
				"scope_note":        map[string]any{"type": "string", "description": "Optional one-sentence note before the host confirmation preview."},
				"intent_excerpt":    map[string]any{"type": "string", "description": "Prompt-bearing controls only: short original wording whose constraints must survive."},
				"preserved_meaning": map[string]any{"type": "string", "description": "Prompt-bearing controls only: exact source, metric, timeframe, scope, negations, and exclusions."},
				"success_condition": map[string]any{"type": "string", "description": "Prompt-bearing controls only: the result or explicit evidence mismatch that satisfies the request."},
			}, []string{"capability", "arguments"}),
			Run: func(ctx context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				var args helpChatControlProposalArgs
				if err := decodeHelpChatToolArgs(raw, &args); err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				arguments, err := addHelpChatLosslessPacket(args)
				if err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				invocation, err := control.BuildProposedInvocation(args.RequestID, control.CapabilityName(args.Capability), arguments)
				if err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, wrapControlProposalError(err)
				}
				invocation, preview, usage, err := a.reviewHelpChatControlProposal(ctx, req, invocation, args.ScopeNote)
				if err != nil {
					return lcagent.AgentRuntimeToolOutcome{Usage: usage}, err
				}
				return lcagent.AgentRuntimeToolOutcome{
					Result:    map[string]any{"success": true, "status": "awaiting_host_confirmation", "capability": invocation.Capability},
					Terminal:  true,
					FinalText: preview,
					Value:     helpChatAgentTerminal{ControlInvocation: &invocation},
					Usage:     usage,
				}, nil
			},
		},
	}
}

type helpChatControlProposalArgs struct {
	Capability       string          `json:"capability"`
	Arguments        json.RawMessage `json:"arguments"`
	RequestID        string          `json:"request_id"`
	ScopeNote        string          `json:"scope_note"`
	IntentExcerpt    string          `json:"intent_excerpt"`
	PreservedMeaning string          `json:"preserved_meaning"`
	SuccessCondition string          `json:"success_condition"`
}

func addHelpChatLosslessPacket(args helpChatControlProposalArgs) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(args.Arguments, &payload); err != nil || payload == nil {
		return nil, errors.New("control arguments must be a JSON object matching the described input schema")
	}
	switch control.CapabilityName(strings.TrimSpace(args.Capability)) {
	case control.CapabilityEngineerSendPrompt,
		control.CapabilityAgentTaskCreate,
		control.CapabilityAgentTaskContinue,
		control.CapabilityProjectCreateAndStartEngineer,
		control.CapabilityTodoCreateWorktreeAndStartEngineer:
		prompt, _ := payload["prompt"].(string)
		action := bossAction{
			Prompt:           prompt,
			IntentExcerpt:    args.IntentExcerpt,
			PreservedMeaning: args.PreservedMeaning,
			SuccessCondition: args.SuccessCondition,
		}
		payload["prompt"] = bossLosslessControlPrompt(action)
	}
	encoded, err := json.Marshal(payload)
	return encoded, err
}

func (a *Assistant) reviewHelpChatControlProposal(ctx context.Context, req AssistantRequest, invocation control.Invocation, scopeNote string) (control.Invocation, string, model.LLMUsage, error) {
	action, err := bossActionFromControlInvocation(invocation)
	if err != nil {
		return control.Invocation{}, "", model.LLMUsage{}, wrapControlProposalError(err)
	}
	action.Answer = strings.TrimSpace(scopeNote)
	var usage model.LLMUsage
	if reviewed, changed, response, reviewErr := a.reviewTodoAddPolicy(ctx, req, action); reviewErr != nil {
		if ctx.Err() != nil {
			return control.Invocation{}, "", usage, reviewErr
		}
	} else {
		addLLMUsage(&usage, response.Usage)
		if changed {
			action = reviewed
		}
	}
	if reviewed, changed, response, reviewErr := a.reviewHelpProjectWorkPolicy(ctx, req, action); reviewErr != nil {
		if ctx.Err() != nil {
			return control.Invocation{}, "", usage, reviewErr
		}
	} else {
		addLLMUsage(&usage, response.Usage)
		if changed {
			action = reviewed
		}
	}
	if control.CapabilityName(strings.TrimSpace(action.ControlCapability)) != invocation.Capability {
		reviewedInvocation, preview, err := controlProposalFromBossAction(action)
		if err != nil {
			return control.Invocation{}, "", usage, wrapControlProposalError(err)
		}
		return reviewedInvocation, preview, usage, nil
	}
	preview, err := controlConfirmationContent(invocation)
	if err != nil {
		return control.Invocation{}, "", usage, wrapControlProposalError(err)
	}
	return invocation, proposalContentWithScopeNote(scopeNote, preview), usage, nil
}

func bossActionFromControlInvocation(invocation control.Invocation) (bossAction, error) {
	action := bossAction{
		Kind:              bossActionProposeControl,
		ControlCapability: string(invocation.Capability),
		RequestID:         invocation.RequestID,
	}
	switch invocation.Capability {
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName = input.ProjectPath, input.ProjectName
		action.EngineerProvider, action.SessionMode, action.Prompt = string(input.Provider), string(input.SessionMode), input.Prompt
		action.TodoID, action.TodoLabel, action.TodoText, action.Reveal = input.TodoID, input.TodoLabel, input.TodoText, input.Reveal
	case control.CapabilityAgentTaskCreate:
		var input control.AgentTaskCreateInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.TaskTitle, action.TaskKind, action.ParentTaskID = input.Title, string(input.Kind), input.ParentTaskID
		action.Prompt, action.EngineerProvider, action.Reveal = input.Prompt, string(input.Provider), input.Reveal
		action.Capabilities, action.Resources = input.Capabilities, input.Resources
	case control.CapabilityAgentTaskContinue:
		var input control.AgentTaskContinueInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.TaskID, action.Prompt = input.TaskID, input.Prompt
		action.EngineerProvider, action.SessionMode, action.Reveal = string(input.Provider), string(input.SessionMode), input.Reveal
	case control.CapabilityAgentTaskClose:
		var input control.AgentTaskCloseInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.TaskID, action.TaskCloseStatus, action.TaskSummary, action.CloseSession = input.TaskID, string(input.Status), input.Summary, input.CloseSession
	case control.CapabilityProjectCreateAndStartEngineer:
		var input control.ProjectCreateAndStartEngineerInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectParentPath, action.ProjectPath, action.ProjectName = input.ParentPath, input.ProjectPath, input.ProjectName
		action.TodoText, action.Prompt, action.EngineerProvider, action.Reveal = input.TodoText, input.Prompt, string(input.Provider), input.Reveal
	case control.CapabilityProjectSetCategory:
		var input control.ProjectSetCategoryInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName, action.ProjectCategoryName = input.ProjectPath, input.ProjectName, input.CategoryName
	case control.CapabilityProjectArchive:
		var input control.ProjectArchiveInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName, action.ProjectArchiveAction = input.ProjectPath, input.ProjectName, string(input.Action)
		action.Resources = input.Resources
	case control.CapabilityScratchTaskArchive:
		var input control.ScratchTaskArchiveInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName = input.ProjectPath, input.ProjectName
	case control.CapabilityTodoAdd:
		var input control.TodoAddInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName, action.TodoText = input.ProjectPath, input.ProjectName, input.Text
	case control.CapabilityTodoCreateWorktreeAndStartEngineer:
		var input control.TodoCreateWorktreeAndStartEngineerInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName = input.ProjectPath, input.ProjectName
		action.TodoText, action.Prompt, action.EngineerProvider, action.Reveal = input.TodoText, input.Prompt, string(input.Provider), input.Reveal
	case control.CapabilityTodoComplete:
		var input control.TodoCompleteInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName = input.ProjectPath, input.ProjectName
		action.TodoID, action.TodoLabel, action.TodoText, action.TodoEvidence = input.TodoID, input.TodoLabel, input.TodoText, input.Evidence
	case control.CapabilitySettingsUpdate:
		var input control.SettingsUpdateInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.SettingsChanges = input.Changes
	case control.CapabilityGitPrepareCommit:
		var input control.GitPrepareCommitInput
		if err := json.Unmarshal(invocation.Args, &input); err != nil {
			return action, err
		}
		action.ProjectPath, action.ProjectName = input.ProjectPath, input.ProjectName
		action.CommitMessage, action.PushAfterCommit = input.Message, input.PushAfterCommit
	default:
		return action, fmt.Errorf("unsupported control capability: %s", invocation.Capability)
	}
	return action, nil
}

func (a *Assistant) helpChatInspectionTools(req AssistantRequest) []lcagent.AgentRuntimeTool {
	tool := func(name, description string, properties map[string]any, required []string, action func(json.RawMessage) (bossAction, error), terminalAnswer bool) lcagent.AgentRuntimeTool {
		return lcagent.AgentRuntimeTool{
			Definition: helpChatToolDefinition(name, description, properties, required),
			Run: func(ctx context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
				if a == nil || a.query == nil {
					return lcagent.AgentRuntimeToolOutcome{}, errors.New("Help Chat inspection tools are not connected")
				}
				queryAction, err := action(raw)
				if err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				result, err := a.query.Execute(ctx, queryAction, req.Snapshot, req.View)
				if err != nil {
					return lcagent.AgentRuntimeToolOutcome{}, err
				}
				outcome := lcagent.AgentRuntimeToolOutcome{
					Result:  map[string]any{"success": true, "inspection": result.Name, "output": result.Text},
					Receipt: result.UserReceipt,
					Usage:   result.Usage,
				}
				if terminalAnswer && strings.TrimSpace(result.UserAnswer) != "" {
					outcome.Terminal = true
					outcome.FinalText = strings.TrimSpace(result.UserAnswer)
				}
				return outcome, nil
			},
		}
	}
	return []lcagent.AgentRuntimeTool{
		tool("lookup_lcr_help", "Search the generated Little Control Room command, keybinding, capability, and workflow corpus. Use for every app-usage question, including recording and launch commands.", map[string]any{
			"query": map[string]any{"type": "string", "minLength": 1},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		}, []string{"query"}, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionHelpReference, Query: args.Query, Limit: args.Limit}, nil
		}, true),
		tool("inspect_current_tui", "Read the current Little Control Room TUI/view state and compact host snapshot.", map[string]any{}, nil, func(raw json.RawMessage) (bossAction, error) {
			var args struct{}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionCurrentTUI}, nil
		}, false),
		tool("inspect_live_processes", "Inspect suspicious project-local processes. This is report-only and cannot stop processes.", map[string]any{
			"project_path":       map[string]any{"type": "string"},
			"project_name":       map[string]any{"type": "string"},
			"include_historical": map[string]any{"type": "boolean"},
			"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
		}, nil, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				ProjectPath       string `json:"project_path"`
				ProjectName       string `json:"project_name"`
				IncludeHistorical bool   `json:"include_historical"`
				Limit             int    `json:"limit"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionProcessReport, ProjectPath: args.ProjectPath, ProjectName: args.ProjectName, IncludeHistorical: args.IncludeHistorical, Limit: args.Limit}, nil
		}, false),
		tool("search_lcr_context", "Search project names, summaries, assessments, and cached engineer context to resolve an unfamiliar term or target.", map[string]any{
			"query":              map[string]any{"type": "string", "minLength": 1},
			"project_path":       map[string]any{"type": "string"},
			"project_name":       map[string]any{"type": "string"},
			"include_historical": map[string]any{"type": "boolean"},
			"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": 24},
		}, []string{"query"}, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				Query             string `json:"query"`
				ProjectPath       string `json:"project_path"`
				ProjectName       string `json:"project_name"`
				IncludeHistorical bool   `json:"include_historical"`
				Limit             int    `json:"limit"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionSearchContext, Query: args.Query, ProjectPath: args.ProjectPath, ProjectName: args.ProjectName, IncludeHistorical: args.IncludeHistorical, Limit: args.Limit}, nil
		}, false),
		tool("search_chat_sessions", "Search persisted Help Chat and Chat session transcripts for earlier user/assistant turns.", map[string]any{
			"query": map[string]any{"type": "string", "minLength": 1},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 16},
		}, []string{"query"}, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionSearchBossSessions, Query: args.Query, Limit: args.Limit}, nil
		}, false),
		tool("inspect_linked_context", "Run one bounded ctx lookup for exact linked engineer or agent-task transcript evidence.", map[string]any{
			"command": map[string]any{"type": "string", "minLength": 1, "description": "One ctx search/show/recent command using a known handle or target."},
		}, []string{"command"}, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionContextCommand, Command: args.Command}, nil
		}, false),
		tool("inspect_lcr_skills", "Read the installed Codex skill inventory visible to Little Control Room.", map[string]any{
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 40},
		}, nil, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				Limit int `json:"limit"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionSkillsInventory, Limit: args.Limit}, nil
		}, false),
		tool("scout_repository", "Run a fresh, bounded, read-only LCAgent Repository Scout over one tracked project after its exact target is resolved.", map[string]any{
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"question":     map[string]any{"type": "string", "minLength": 1},
		}, []string{"question"}, func(raw json.RawMessage) (bossAction, error) {
			var args struct {
				ProjectPath string `json:"project_path"`
				ProjectName string `json:"project_name"`
				Question    string `json:"question"`
			}
			if err := decodeHelpChatToolArgs(raw, &args); err != nil {
				return bossAction{}, err
			}
			return bossAction{Kind: bossActionProjectScout, ProjectPath: args.ProjectPath, ProjectName: args.ProjectName, Query: args.Question}, nil
		}, false),
	}
}

func (a *Assistant) helpChatGoalTool(_ AssistantRequest) lcagent.AgentRuntimeTool {
	fullSchema := bossActionSchema()
	fullProperties, _ := fullSchema["properties"].(map[string]any)
	names := []string{
		"answer",
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
		"resources",
	}
	properties := make(map[string]any, len(names))
	for _, name := range names {
		properties[name] = fullProperties[name]
	}
	return lcagent.AgentRuntimeTool{
		Definition: helpChatToolDefinition("propose_goal", "Prepare one typed, traceable LCR goal for the existing host confirmation dialog. This never starts the goal.", properties, []string{
			"goal_kind", "goal_title", "goal_objective", "goal_success_criteria", "goal_max_risk", "goal_resources", "goal_allowed_capabilities", "goal_forbidden_side_effects",
		}),
		Run: func(_ context.Context, raw json.RawMessage) (lcagent.AgentRuntimeToolOutcome, error) {
			var action bossAction
			if err := decodeHelpChatToolArgs(raw, &action); err != nil {
				return lcagent.AgentRuntimeToolOutcome{}, err
			}
			action.Kind = bossActionProposeGoal
			proposal, preview, err := goalProposalFromBossAction(action)
			if err != nil {
				return lcagent.AgentRuntimeToolOutcome{}, wrapGoalProposalError(err)
			}
			return lcagent.AgentRuntimeToolOutcome{
				Result:    map[string]any{"success": true, "status": "awaiting_host_confirmation", "goal_kind": proposal.Run.Kind},
				Terminal:  true,
				FinalText: preview,
				Value:     helpChatAgentTerminal{GoalProposal: &proposal},
			}, nil
		},
	}
}

func helpChatToolDefinition(name, description string, properties map[string]any, required []string) modeladapter.ToolDefinition {
	parameters := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		parameters["required"] = required
	}
	return modeladapter.ToolDefinition{
		Type: "function",
		Function: modeladapter.FunctionSpec{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

func decodeHelpChatToolArgs(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("invalid tool arguments: multiple JSON values")
		}
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}
