package lcagent

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	projectinstructions "lcroom/internal/lcagent/instructions"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/sessionmetrics"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
	lcrmodel "lcroom/internal/model"
)

const version = "dev"

type outputMode string

const (
	outputText       outputMode = "text"
	outputJSON       outputMode = "json"
	outputStreamJSON outputMode = "stream-json"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, lcagentUsage())
		return 2
	}
	switch args[0] {
	case "exec":
		if err := runExec(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "metrics":
		if err := runMetrics(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "eval":
		if err := runEval(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, lcagentUsage())
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func lcagentUsage() string {
	return "usage: lcagent exec [flags] <prompt>\n       lcagent metrics <session.jsonl>...\n       lcagent eval [flags]"
}

func runMetrics(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lcagent metrics <session.jsonl>...")
	}
	summary, err := sessionmetrics.AnalyzeFiles(args)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

func runExec(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cwd, dataDir, autoRaw, outputRaw, scriptPath, provider, model, finalModel, envFile, reasoningEffort, temperatureRaw, providerOnlyRaw, toolProfileRaw, contextProfileRaw, resumeRaw string
	var requestTimeout time.Duration
	var maxTurns int
	fs.StringVar(&cwd, "cwd", "", "workspace root")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root")
	fs.StringVar(&autoRaw, "auto", "off", "autonomy: off, low, medium")
	fs.StringVar(&outputRaw, "output", string(outputStreamJSON), "output: text, json, stream-json")
	fs.StringVar(&scriptPath, "script", "", "scripted JSONL actions")
	fs.StringVar(&provider, "provider", "scripted", "provider: scripted, openrouter, openai, deepseek, or moonshot")
	fs.StringVar(&model, "model", "", "model name")
	fs.StringVar(&finalModel, "final-model", "", "optional model for no-tools final synthesis")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "optional provider reasoning effort, for example low")
	fs.StringVar(&temperatureRaw, "temperature", "", "optional sampling temperature; defaults to 0.2 for chat-completions providers that send temperature; use omitted to suppress")
	fs.StringVar(&providerOnlyRaw, "openrouter-provider-only", "", "comma-separated OpenRouter provider slugs allowed for this request, for example anthropic")
	fs.StringVar(&toolProfileRaw, "tool-profile", string(tools.FileProfileBalanced), "file tool budget profile: balanced or generous")
	fs.StringVar(&contextProfileRaw, "context-profile", string(openRouterContextProfileBalanced), "provider loop context profile: balanced or large")
	fs.StringVar(&resumeRaw, "resume", "", "previous LCAgent session id or .jsonl artifact to summarize as continuation context")
	fs.DurationVar(&requestTimeout, "request-timeout", 0, "provider HTTP request timeout, for example 10m; default 2m")
	fs.IntVar(&maxTurns, "max-turns", modeladapter.DefaultOpenRouterMaxTurns, "maximum model turns for provider loops")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	auto, err := policy.ParseAutonomy(autoRaw)
	if err != nil {
		return err
	}
	workspace, err := policy.NewWorkspace(cwd, auto)
	if err != nil {
		return err
	}
	instructions, err := projectinstructions.LoadWorkspace(workspace.Root)
	if err != nil {
		return fmt.Errorf("load project instructions: %w", err)
	}
	catalog, err := skillcatalog.Discover(context.Background(), skillcatalog.DefaultOptions(workspace.Root))
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	outMode := outputMode(strings.TrimSpace(outputRaw))
	if outMode != outputText && outMode != outputJSON && outMode != outputStreamJSON {
		return fmt.Errorf("unsupported output mode: %s", outputRaw)
	}
	temperature, omitTemperature, err := parseTemperatureOption(temperatureRaw)
	if err != nil {
		return err
	}
	toolProfile, err := tools.ParseFileProfile(toolProfileRaw)
	if err != nil {
		return err
	}
	fileLimits := tools.FileLimitsForProfile(toolProfile)
	contextProfile, err := parseOpenRouterContextProfile(contextProfileRaw)
	if err != nil {
		return err
	}
	contextOptions := openRouterContextOptionsForProfile(contextProfile)
	if strings.TrimSpace(model) == "" {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "openrouter":
			model = modeladapter.DefaultOpenRouterModel
		case "openai":
			model = modeladapter.DefaultOpenAIModel
		case "deepseek":
			model = modeladapter.DefaultDeepSeekModel
		case "moonshot":
			model = modeladapter.DefaultMoonshotModel
		default:
			model = "scripted"
		}
	}
	resumeContext, err := loadResumeContext(dataDir, resumeRaw, workspace.Root)
	if err != nil {
		return err
	}
	stream := io.Writer(nil)
	if outMode == outputStreamJSON {
		stream = stdout
	}
	started := time.Now()
	writer, sessionID, err := session.NewWriter(dataDir, started, stream)
	if err != nil {
		return err
	}
	defer writer.Close()
	if err := writer.Write(session.Meta(sessionID, workspace.Root, string(auto), provider, model, version, started)); err != nil {
		return err
	}
	if err := writer.Write(session.Event{
		"type":         "tool_profile",
		"session_id":   sessionID,
		"profile":      string(toolProfile),
		"file_limits":  fileLimits,
		"schema_label": "file_tools",
	}); err != nil {
		return err
	}
	if err := writer.Write(session.Event{
		"type":       "context_profile",
		"session_id": sessionID,
		"profile":    string(contextProfile),
		"options":    contextOptions,
	}); err != nil {
		return err
	}
	if resumeContext != nil {
		if err := writer.Write(resumeContext.event(sessionID)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(instructions.Body) != "" {
		if err := writer.Write(session.Event{
			"type":       "project_instructions",
			"session_id": sessionID,
			"path":       instructions.Path,
			"body":       instructions.Body,
			"truncated":  instructions.Truncated,
		}); err != nil {
			return err
		}
	}
	if len(catalog.Skills) > 0 {
		if err := writer.Write(session.Event{
			"type":       "skill_catalog",
			"session_id": sessionID,
			"count":      len(catalog.Skills),
			"skills":     catalog.EventSkills(40),
		}); err != nil {
			return err
		}
	}

	artifactDir := filepath.Join(filepath.Dir(writer.Path()), sessionID+"-artifacts")
	runner := script.Runner{
		Session:      writer,
		Command:      tools.CommandRunner{Workspace: workspace, ArtifactDir: artifactDir},
		Patch:        tools.PatchApplier{Workspace: workspace},
		Files:        tools.FileTools{Workspace: workspace, Limits: fileLimits},
		Skills:       catalog,
		SessionID:    sessionID,
		Prompt:       prompt,
		ArtifactsDir: artifactDir,
	}

	var runErr error
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "scripted":
		if scriptPath == "" {
			return fmt.Errorf("--script is required for scripted provider")
		}
		actions, err := script.Load(scriptPath)
		if err != nil {
			return err
		}
		runErr = runner.Run(context.Background(), actions)
	case "openrouter", "openai", "deepseek", "moonshot":
		runErr = runOpenRouter(context.Background(), writer, runner, instructions.PromptSection(), resumeContext, modeladapter.OpenRouterConfig{
			Model:           model,
			FinalModel:      finalModel,
			EnvFile:         envFile,
			MaxTurns:        maxTurns,
			RequestTimeout:  requestTimeout,
			ReasoningEffort: reasoningEffort,
			Temperature:     temperature,
			OmitTemperature: omitTemperature,
			ProviderOnly:    splitCommaFields(providerOnlyRaw),
		}, strings.ToLower(strings.TrimSpace(provider)), toolProfile, fileLimits, contextOptions)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
	if runErr != nil {
		return runErr
	}
	if outMode == outputText {
		fmt.Fprintf(stdout, "Session %s complete\nArtifact: %s\n", sessionID, writer.Path())
	}
	if outMode == outputJSON {
		return json.NewEncoder(stdout).Encode(map[string]any{
			"session_id": sessionID,
			"artifact":   writer.Path(),
			"status":     "complete",
		})
	}
	return nil
}

func runOpenRouter(ctx context.Context, writer *session.Writer, runner script.Runner, projectInstructionPrompt string, resumeContext *resumeContext, cfg modeladapter.OpenRouterConfig, provider string, toolProfile tools.FileProfile, fileLimits tools.FileLimits, contextOptions openRouterContextOptions) error {
	contextOptions = contextOptions.withDefaults()
	providerLabel := strings.ToLower(strings.TrimSpace(provider))
	if providerLabel == "" {
		providerLabel = "openrouter"
	}
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		_ = writer.Write(session.Event{
			"type":       "turn_aborted",
			"session_id": runner.SessionID,
			"reason":     err.Error(),
		})
		return err
	}
	finalClient, err := openRouterFinalClient(provider, cfg, client)
	if err != nil {
		_ = writer.Write(session.Event{
			"type":       "turn_aborted",
			"session_id": runner.SessionID,
			"reason":     err.Error(),
		})
		return err
	}
	if err := writer.Write(session.Event{
		"type":       "user_message",
		"session_id": runner.SessionID,
		"message":    runner.Prompt,
	}); err != nil {
		return err
	}

	systemPrompt := modeladapter.SystemPromptWithOptions(runner.Skills.PromptIndex(0), projectInstructionPrompt, modelSystemPromptOptions(toolProfile, fileLimits))
	if resumeSection := strings.TrimSpace(resumeContext.systemPromptSection()); resumeSection != "" {
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + resumeSection)
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: runner.Prompt},
	}
	readLedger := newReadLedger()
	toolsDef := modeladapter.ToolsWithOptions(modelToolOptions(toolProfile, fileLimits))
	for turn := 0; turn < client.MaxTurns(); turn++ {
		if compactedMessages, compaction, compacted := compactOpenRouterLoopMessagesWithOptions(messages, readLedger, contextOptions); compacted {
			if err := writer.Write(session.Event{
				"type":       "context_compacted",
				"session_id": runner.SessionID,
				"turn":       turn + 1,
				"threshold":  contextOptions.LoopCompactionCharThreshold,
				"stats":      compaction,
			}); err != nil {
				return err
			}
			messages = compactedMessages
		}
		guidance := openRouterGuidanceForTurnWithOptions(turn+1, client.MaxTurns(), messages, readLedger, openRouterGuidanceOptions{ToolProfile: string(toolProfile)})
		requestMessages := appendOpenRouterProgressNote(messages, guidance, readLedger)
		requestTools := toolsDef
		if guidance.ForceSynthesis {
			var compaction finalHandoffCompactionStats
			requestMessages, compaction = compactOpenRouterFinalMessagesWithOptions(messages, openRouterSynthesisFinalPrompt(guidance), readLedger, contextOptions)
			requestTools = nil
			if err := writer.Write(session.Event{
				"type":        "synthesis_requested",
				"session_id":  runner.SessionID,
				"guidance":    guidance,
				"final_model": finalClient.Model(),
				"stats":       compaction,
			}); err != nil {
				return err
			}
		}
		var completion modeladapter.Completion
		if guidance.ForceSynthesis {
			completion, err = finalClient.CompleteWithOptions(ctx, requestMessages, requestTools, openRouterFinalCompletionOptions(cfg))
		} else {
			completion, err = client.CompleteWithOptions(ctx, requestMessages, requestTools, openRouterCompletionOptions(cfg))
		}
		if err != nil {
			_ = writer.Write(session.Event{
				"type":       "turn_aborted",
				"session_id": runner.SessionID,
				"reason":     err.Error(),
			})
			return err
		}
		msg := completion.Message
		if err := writer.Write(modelResponseEvent(runner.SessionID, turn+1, completion, len(msg.ToolCalls))); err != nil {
			return err
		}
		sanitizedContent, strippedProviderMarkup := modeladapter.SanitizeAssistantContent(msg.Content)
		msg.Content = sanitizedContent
		if guidance.ForceSynthesis && len(msg.ToolCalls) > 0 {
			return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s synthesis request returned tool calls", providerLabel))
		}
		messages = append(messages, msg)
		if len(msg.ToolCalls) == 0 {
			if strippedProviderMarkup {
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response contained provider tool-call markup but no structured tool calls", providerLabel))
			}
			if strings.EqualFold(completion.FinishReason, "tool_calls") {
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response finished with tool_calls but returned no structured tool calls", providerLabel))
			}
			if strings.TrimSpace(msg.Content) == "" {
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response had no content or tool calls", providerLabel))
			}
			return runner.Final(script.Action{
				Type:    "final_response",
				Summary: msg.Content,
			})
		}
		if msg.Content != "" && !hasToolCall(msg.ToolCalls, "final_response") {
			if err := writer.Write(session.Event{
				"type":       "assistant_message",
				"session_id": runner.SessionID,
				"message":    msg.Content,
			}); err != nil {
				return err
			}
		}
		for _, call := range msg.ToolCalls {
			args, err := modeladapter.NormalizeArguments(call.Function.Arguments)
			if err != nil {
				toolName := strings.TrimSpace(call.Function.Name)
				if toolName == "" {
					toolName = "unknown tool"
				}
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("decode arguments for %s: %w", toolName, err))
			}
			action := script.Action{Type: "tool_call", Tool: call.Function.Name, Args: args}
			if call.Function.Name == "final_response" {
				var final script.Action
				if err := json.Unmarshal(args, &final); err != nil {
					return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("decode final_response arguments: %w", err))
				}
				final.Type = "final_response"
				return runner.Final(final)
			}
			result, err := runner.RunTool(ctx, action)
			if call.Function.Name == "read_file" {
				readLedger.ObserveReadResult(result)
			}
			resultJSON, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return marshalErr
			}
			messages = append(messages, modeladapter.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    string(resultJSON),
			})
			if err != nil {
				// Feed tool failures back to the model once; the structured event has
				// already recorded the failure for LCR.
				continue
			}
		}
	}
	return finalizeOpenRouterAfterMaxTurns(ctx, writer, runner, client, finalClient, messages, readLedger, providerLabel, cfg, contextOptions)
}

func newChatProviderClient(provider string, cfg modeladapter.OpenRouterConfig) (*modeladapter.Client, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return modeladapter.NewOpenAIClient(cfg)
	case "deepseek":
		return modeladapter.NewDeepSeekClient(cfg)
	case "moonshot":
		return modeladapter.NewMoonshotClient(cfg)
	default:
		return modeladapter.NewOpenRouterClient(cfg)
	}
}

func openRouterFinalClient(provider string, cfg modeladapter.OpenRouterConfig, client *modeladapter.Client) (*modeladapter.Client, error) {
	finalModel := openRouterFinalModel(provider, cfg)
	if finalModel == "" || finalModel == client.Model() {
		return client, nil
	}
	finalCfg := cfg
	finalCfg.Model = finalModel
	return newChatProviderClient(provider, finalCfg)
}

func openRouterFinalModel(provider string, cfg modeladapter.OpenRouterConfig) string {
	if finalModel := strings.TrimSpace(cfg.FinalModel); finalModel != "" {
		return finalModel
	}
	if strings.EqualFold(strings.TrimSpace(provider), "deepseek") {
		return strings.TrimSpace(os.Getenv("DEEPSEEK_FINAL_MODEL"))
	}
	if strings.EqualFold(strings.TrimSpace(provider), "openai") {
		return strings.TrimSpace(os.Getenv("OPENAI_FINAL_MODEL"))
	}
	if strings.EqualFold(strings.TrimSpace(provider), "moonshot") {
		return strings.TrimSpace(os.Getenv("MOONSHOT_FINAL_MODEL"))
	}
	return strings.TrimSpace(os.Getenv("OPENROUTER_FINAL_MODEL"))
}

func finalizeOpenRouterAfterMaxTurns(ctx context.Context, writer *session.Writer, runner script.Runner, client *modeladapter.Client, finalClient *modeladapter.Client, messages []modeladapter.Message, readLedger *readLedger, providerLabel string, cfg modeladapter.OpenRouterConfig, contextOptions openRouterContextOptions) error {
	maxTurns := client.MaxTurns()
	compactedMessages, compaction := compactOpenRouterFinalMessagesWithOptions(messages, openRouterMaxTurnsFinalPrompt(maxTurns), readLedger, contextOptions)
	if err := writer.Write(session.Event{
		"type":        "final_handoff_compacted",
		"session_id":  runner.SessionID,
		"final_model": finalClient.Model(),
		"stats":       compaction,
	}); err != nil {
		return err
	}
	completion, err := finalClient.CompleteWithOptions(ctx, compactedMessages, nil, openRouterFinalCompletionOptions(cfg))
	if err != nil {
		return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s model loop exceeded maximum turns; final handoff failed: %w", providerLabel, err))
	}
	msg := completion.Message
	if err := writer.Write(modelResponseEvent(runner.SessionID, maxTurns+1, completion, len(msg.ToolCalls))); err != nil {
		return err
	}
	sanitizedContent, strippedProviderMarkup := modeladapter.SanitizeAssistantContent(msg.Content)
	if len(msg.ToolCalls) > 0 {
		return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s model loop exceeded maximum turns; final handoff tried to call tools", providerLabel))
	}
	if strippedProviderMarkup && strings.TrimSpace(sanitizedContent) == "" {
		return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s model loop exceeded maximum turns; final handoff contained only provider tool-call markup", providerLabel))
	}
	sanitizedContent = strings.TrimSpace(sanitizedContent)
	if sanitizedContent == "" {
		return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s model loop exceeded maximum turns; final handoff was empty", providerLabel))
	}
	return runner.Final(script.Action{
		Type:    "final_response",
		Summary: sanitizedContent,
	})
}

func openRouterMaxTurnsFinalPrompt(maxTurns int) string {
	return fmt.Sprintf(`You have reached the configured maximum of %d model turns.

Do not call more tools. Produce a concise handoff for the user instead:
- Say that the turn budget was reached.
- Summarize what you completed or learned from the available tool results.
- List any files you believe were changed, or say none/unknown.
- List verification already run, or say not run.
- State the next concrete step the user can ask for to continue.`, maxTurns)
}

func openRouterCompletionOptions(cfg modeladapter.OpenRouterConfig) modeladapter.CompletionOptions {
	return modeladapter.CompletionOptions{
		ReasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
	}
}

func openRouterFinalCompletionOptions(cfg modeladapter.OpenRouterConfig) modeladapter.CompletionOptions {
	return openRouterCompletionOptions(cfg)
}

func openRouterSynthesisFinalPrompt(guidance openRouterProgressGuidance) string {
	return fmt.Sprintf(`This is a planned synthesis checkpoint at turn %d of %d, before the hard cap.

Tools are unavailable for this request. Produce the final user-facing answer now from the gathered evidence:
- Do not say the turn budget was reached.
- Answer the original user request directly.
- Distinguish confirmed gaps from unverified items.
- A feature is not missing merely because there is no same-named file; it may be implemented inline in CLI, script, model adapter, or orchestration code.
- Keep uncertainty where it is honest, but do not ask the user to continue unless a concrete blocker remains.
- Prefer a concise structured answer over exhaustive audit notes.`, guidance.Turn, guidance.MaxTurns)
}

func abortOpenRouterRun(writer *session.Writer, sessionID string, err error) error {
	_ = writer.Write(session.Event{
		"type":       "turn_aborted",
		"session_id": sessionID,
		"reason":     err.Error(),
	})
	return err
}

func hasToolCall(calls []modeladapter.ToolCall, name string) bool {
	for _, call := range calls {
		if call.Function.Name == name {
			return true
		}
	}
	return false
}

func modelResponseEvent(sessionID string, turn int, completion modeladapter.Completion, toolCallCount int) session.Event {
	event := session.Event{
		"type":            "model_response",
		"session_id":      sessionID,
		"turn":            turn,
		"model":           completion.Model,
		"tool_call_count": toolCallCount,
	}
	if completion.ID != "" {
		event["response_id"] = completion.ID
	}
	if completion.FinishReason != "" {
		event["finish_reason"] = completion.FinishReason
	}
	if len(completion.Usage) > 0 && string(completion.Usage) != "null" {
		event["usage"] = json.RawMessage(completion.Usage)
	}
	if usageTracked(completion.UsageSummary) {
		event["usage_summary"] = completion.UsageSummary
	}
	return event
}

func usageTracked(usage lcrmodel.LLMUsage) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.CachedInputTokens != 0 ||
		usage.ReasoningTokens != 0 ||
		usage.EstimatedCostUSD != 0
}

func defaultDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("LCROOM_DATA_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".little-control-room")
}

func parseTemperatureOption(raw string) (*float64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}
	switch strings.ToLower(raw) {
	case "omit", "omitted", "none", "null":
		return nil, true, nil
	}
	temperature, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, false, fmt.Errorf("invalid --temperature %q: %w", raw, err)
	}
	if temperature < 0 {
		return nil, false, fmt.Errorf("invalid --temperature %q: must be non-negative", raw)
	}
	return &temperature, false, nil
}

func splitCommaFields(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func modelToolOptions(profile tools.FileProfile, limits tools.FileLimits) modeladapter.ToolOptions {
	return modeladapter.ToolOptions{
		ToolProfile:             string(profile),
		DefaultReadLineLimit:    limits.DefaultReadLineLimit,
		MaxReadLineLimit:        limits.MaxReadLineLimit,
		DefaultListEntryLimit:   limits.DefaultListEntryLimit,
		MaxListEntryLimit:       limits.MaxListEntryLimit,
		DefaultSearchMaxMatch:   limits.DefaultSearchMaxMatch,
		MaxSearchMaxMatch:       limits.MaxSearchMaxMatch,
		MaxSearchContextLines:   limits.MaxSearchContextLines,
		DefaultOutlineFileLimit: limits.DefaultOutlineFileLimit,
		MaxOutlineFileLimit:     limits.MaxOutlineFileLimit,
		MaxModuleOutlineChars:   limits.MaxModuleOutlineChars,
	}
}

func modelSystemPromptOptions(profile tools.FileProfile, limits tools.FileLimits) modeladapter.SystemPromptOptions {
	return modeladapter.SystemPromptOptions{
		ToolProfile:          string(profile),
		DefaultReadLineLimit: limits.DefaultReadLineLimit,
		MaxReadLineLimit:     limits.MaxReadLineLimit,
	}
}
