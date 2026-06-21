package lcagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/buildinfo"
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
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, buildinfo.Summary("lcagent"))
		return 0
	case "exec":
		if err := runExec(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "scout":
		if err := runScout(args[1:], stdout); err != nil {
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
	case "live-eval":
		if err := runLiveEval(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "presets":
		if err := runPresets(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "smoke":
		if err := runSmoke(args[1:], stdout); err != nil {
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
	return "usage: lcagent version\n       lcagent exec [flags] <prompt>\n       lcagent scout [exec flags] <prompt>\n       lcagent presets [flags]\n       lcagent metrics <session.jsonl>...\n       lcagent eval [flags]\n       lcagent live-eval [flags]\n       lcagent smoke [flags]"
}

type execRunOptions struct {
	CommandName           string
	DefaultAuto           string
	DefaultRoutePreset    string
	DefaultMaxTurns       int
	DelegationMode        string
	DelegationDescription string
	PromptTransform       func(string) string
}

type browserCapabilityConfig struct {
	Enabled        bool
	SessionKey     string
	ProfileKey     string
	LaunchMode     browserctl.ManagedLaunchMode
	DisabledReason string
	Err            error
}

func resolveBrowserCapability(controlRaw, sessionKeyRaw, profileKeyRaw, launchModeRaw string) browserCapabilityConfig {
	control := strings.ToLower(strings.TrimSpace(controlRaw))
	if control == "" {
		control = "off"
	}
	launchMode := browserctl.ManagedLaunchMode(launchModeRaw).Normalize()
	if control == "off" {
		return browserCapabilityConfig{
			LaunchMode:     launchMode,
			DisabledReason: "browser control disabled",
		}
	}
	if control != "managed" {
		return browserCapabilityConfig{Err: fmt.Errorf("browser-control must be one of: off, managed")}
	}
	sessionKey := strings.TrimSpace(sessionKeyRaw)
	profileKey := strings.TrimSpace(profileKeyRaw)
	if sessionKey == "" || profileKey == "" {
		return browserCapabilityConfig{
			LaunchMode:     launchMode,
			SessionKey:     sessionKey,
			ProfileKey:     profileKey,
			DisabledReason: "managed browser session and profile keys are required",
		}
	}
	return browserCapabilityConfig{
		Enabled:    true,
		SessionKey: sessionKey,
		ProfileKey: profileKey,
		LaunchMode: launchMode,
	}
}

type lcagentBrowserRunner struct {
	session browserctl.BrowserSession
}

func (r lcagentBrowserRunner) RunBrowserTool(ctx context.Context, tool string, args json.RawMessage) tools.ToolResult {
	if r.session == nil {
		return tools.ToolResult{Success: false, Error: "managed browser runtime is not configured"}
	}
	var (
		result browserctl.BrowserActionResult
		err    error
	)
	switch tool {
	case "browser_navigate":
		var parsed struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Navigate(ctx, parsed.URL)
	case "browser_snapshot":
		var parsed struct {
			MaxChars int `json:"max_chars"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Snapshot(ctx, parsed.MaxChars)
	case "browser_click":
		var parsed struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Click(ctx, parsed.Ref)
	case "browser_fill":
		var parsed struct {
			Ref   string `json:"ref"`
			Value string `json:"value"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Fill(ctx, parsed.Ref, parsed.Value)
	case "browser_press":
		var parsed struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Press(ctx, parsed.Key)
	case "browser_screenshot":
		var parsed struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &parsed)
		result, err = r.session.Screenshot(ctx, parsed.Path)
	case "browser_current_page":
		result, err = r.session.CurrentPage(ctx)
	default:
		return tools.ToolResult{Success: false, Error: "unsupported browser tool: " + tool}
	}
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}
	}
	return browserToolResult(result)
}

func browserToolResult(result browserctl.BrowserActionResult) tools.ToolResult {
	lines := []string{}
	if strings.TrimSpace(result.Status) != "" {
		lines = append(lines, "status: "+strings.TrimSpace(result.Status))
	}
	if strings.TrimSpace(result.URL) != "" {
		lines = append(lines, "url: "+strings.TrimSpace(result.URL))
	}
	if strings.TrimSpace(result.Title) != "" {
		lines = append(lines, "title: "+strings.TrimSpace(result.Title))
	}
	lines = append(lines, "fresh: "+strconv.FormatBool(result.Fresh))
	if strings.TrimSpace(result.Snapshot) != "" {
		lines = append(lines, strings.TrimSpace(result.Snapshot))
	}
	if strings.TrimSpace(result.ArtifactPath) != "" {
		lines = append(lines, "artifact: "+strings.TrimSpace(result.ArtifactPath))
	}
	return tools.ToolResult{
		Success:      true,
		Output:       strings.TrimSpace(strings.Join(lines, "\n")) + "\n",
		ArtifactPath: strings.TrimSpace(result.ArtifactPath),
	}
}

func runPresets(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("presets", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var outputRaw string
	fs.StringVar(&outputRaw, "output", string(outputText), "output: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("usage: lcagent presets [--output text|json]")
	}
	switch outputMode(strings.TrimSpace(outputRaw)) {
	case "", outputText:
		printLCAgentRoutePresets(stdout)
	case outputJSON:
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(lcagentRoutePresets())
	default:
		return fmt.Errorf("unsupported output mode: %s", outputRaw)
	}
	return nil
}

func visitedFlagNames(fs *flag.FlagSet) map[string]bool {
	visited := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	return visited
}

func applyLCAgentRoutePreset(preset lcagentRoutePreset, visited map[string]bool, provider, model, finalModel, reasoningEffort, autoRaw, toolProfileRaw, contextProfileRaw, providerOnlyRaw, temperatureRaw *string, requestTimeout *time.Duration) {
	if !visited["provider"] && strings.TrimSpace(preset.Provider) != "" {
		*provider = preset.Provider
	}
	if !visited["model"] && strings.TrimSpace(preset.Model) != "" {
		*model = preset.Model
	}
	if !visited["final-model"] && strings.TrimSpace(preset.FinalModel) != "" {
		*finalModel = preset.FinalModel
	}
	if !visited["reasoning-effort"] && strings.TrimSpace(preset.ReasoningEffort) != "" {
		*reasoningEffort = preset.ReasoningEffort
	}
	if !visited["auto"] && strings.TrimSpace(preset.Auto) != "" {
		*autoRaw = preset.Auto
	}
	if !visited["tool-profile"] && strings.TrimSpace(preset.ToolProfile) != "" {
		*toolProfileRaw = preset.ToolProfile
	}
	if !visited["context-profile"] && strings.TrimSpace(preset.ContextProfile) != "" {
		*contextProfileRaw = preset.ContextProfile
	}
	if !visited["openrouter-provider-only"] && len(preset.ProviderOnly) > 0 {
		*providerOnlyRaw = strings.Join(preset.ProviderOnly, ",")
	}
	if !visited["temperature"] && strings.TrimSpace(preset.Temperature) != "" {
		*temperatureRaw = preset.Temperature
	}
	if !visited["request-timeout"] && preset.RequestTimeout > 0 {
		*requestTimeout = preset.RequestTimeout
	}
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
	return runExecWithOptions(args, stdout, execRunOptions{CommandName: "exec"})
}

func runScout(args []string, stdout io.Writer) error {
	return runExecWithOptions(args, stdout, execRunOptions{
		CommandName:           "scout",
		DefaultAuto:           "off",
		DefaultRoutePreset:    "cheap-scout",
		DefaultMaxTurns:       4,
		DelegationMode:        "cheap_scout",
		DelegationDescription: "Low-cost read-only scout for bounded exploration and handoff notes.",
		PromptTransform:       scoutDelegationPrompt,
	})
}

func runExecWithOptions(args []string, stdout io.Writer, opts execRunOptions) error {
	commandName := strings.TrimSpace(opts.CommandName)
	if commandName == "" {
		commandName = "exec"
	}
	defaultAuto := strings.TrimSpace(opts.DefaultAuto)
	if defaultAuto == "" {
		defaultAuto = "off"
	}
	defaultRoutePreset := strings.TrimSpace(opts.DefaultRoutePreset)
	defaultMaxTurns := opts.DefaultMaxTurns
	if defaultMaxTurns <= 0 {
		defaultMaxTurns = modeladapter.DefaultOpenRouterMaxTurns
	}
	fs := flag.NewFlagSet(commandName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cwd, dataDir, autoRaw, outputRaw, scriptPath, provider, model, finalModel, envFile, reasoningEffort, temperatureRaw, providerOnlyRaw, toolProfileRaw, contextProfileRaw, resumeRaw, continueRaw, routePresetRaw, approvalModeRaw string
	var utilityProviderRaw, utilityModel string
	var visionProviderRaw, visionModel string
	var webSearchBackend, webSearchAPIKey, webSearchEngineID, webSearchURL string
	var browserControlRaw, browserSessionKey, browserProfileKey, browserLaunchModeRaw string
	var requestTimeout time.Duration
	var maxTurns int
	var searchRefineMinBytes int
	var adminWrite, requireFinalResponseTool, planningPreflightEnabled bool
	fs.StringVar(&cwd, "cwd", "", "workspace root")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root")
	fs.StringVar(&autoRaw, "auto", defaultAuto, "permission level: off denies edits and non-read commands; low allows workspace edits/read/verifiers; medium allows workspace commands")
	fs.StringVar(&outputRaw, "output", string(outputStreamJSON), "output: text, json, stream-json")
	fs.StringVar(&scriptPath, "script", "", "scripted JSONL actions")
	fs.StringVar(&routePresetRaw, "route-preset", defaultRoutePreset, "coding route preset: balanced, quality, mimo-2.5-pro-low, mimo-2.5-pro-high, mimo-2.5-pro-max, or cheap-scout; explicit flags override preset values")
	fs.StringVar(&provider, "provider", "scripted", "provider: scripted, openrouter, openai, deepseek, moonshot, xiaomi, or ollama")
	fs.StringVar(&model, "model", "", "model name")
	fs.StringVar(&finalModel, "final-model", "", "optional model for no-tools final synthesis")
	fs.StringVar(&approvalModeRaw, "approval-mode", approvalModeDeny, "approval mode for denied low-autonomy commands: deny or ask")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "optional provider reasoning effort, for example low")
	fs.StringVar(&temperatureRaw, "temperature", "", "optional sampling temperature; defaults to 0.2 for chat-completions providers that send temperature; use omitted to suppress")
	fs.StringVar(&providerOnlyRaw, "openrouter-provider-only", "", "comma-separated OpenRouter provider slugs allowed for this request, for example anthropic")
	fs.StringVar(&utilityProviderRaw, "utility-provider", defaultUtilityProvider, "utility provider for oversized search refinement: main, off, openrouter, openai, deepseek, moonshot, xiaomi, or ollama")
	fs.StringVar(&utilityModel, "utility-model", defaultUtilityModel, "utility model for oversized search refinement; blank with provider main uses the main model")
	fs.StringVar(&visionProviderRaw, "vision-provider", defaultVisionProvider, "vision provider for analyze_image: off, main, openrouter, openai, deepseek, moonshot, xiaomi, or ollama")
	fs.StringVar(&visionModel, "vision-model", defaultVisionModel, "optional vision model; blank with provider main uses the main model")
	fs.StringVar(&toolProfileRaw, "tool-profile", string(tools.FileProfileBalanced), "file tool budget profile: balanced or generous")
	fs.StringVar(&contextProfileRaw, "context-profile", string(openRouterContextProfileBalanced), "provider loop context profile: balanced or large; known model windows adapt packing budgets")
	fs.BoolVar(&adminWrite, "admin-write", false, "allow write tools to use absolute paths outside the workspace for explicit system/admin edits")
	fs.BoolVar(&requireFinalResponseTool, "require-final-response-tool", false, "require provider runs to finish through the structured final_response tool")
	fs.BoolVar(&planningPreflightEnabled, "planning-preflight", false, "run a model-based scope preflight and require phased quality plans for sizable artifact work")
	fs.StringVar(&resumeRaw, "resume", "", "previous LCAgent thread id to continue from")
	fs.StringVar(&continueRaw, "continue-from", "", "previous LCAgent thread id to continue from")
	fs.StringVar(&webSearchBackend, "web-search-backend", "", "web search backend: off, exa, google, or searxng")
	fs.StringVar(&webSearchAPIKey, "web-search-api-key", "", "optional web search API key, used by exa or google")
	fs.StringVar(&webSearchEngineID, "web-search-engine-id", "", "optional Google Programmable Search engine ID")
	fs.StringVar(&webSearchURL, "web-search-url", "", "optional web search endpoint URL, used by searxng")
	fs.StringVar(&browserControlRaw, "browser-control", "off", "browser control: off or managed")
	fs.StringVar(&browserSessionKey, "browser-session-key", "", "managed browser session key")
	fs.StringVar(&browserProfileKey, "browser-profile-key", "", "managed browser profile key")
	fs.StringVar(&browserLaunchModeRaw, "browser-launch-mode", string(browserctl.ManagedLaunchModeHeadless), "managed browser launch mode: headless, headed, or background")
	fs.DurationVar(&requestTimeout, "request-timeout", 0, "provider HTTP request timeout, for example 10m; default 2m")
	fs.IntVar(&maxTurns, "max-turns", defaultMaxTurns, "maximum model turns for provider loops")
	fs.IntVar(&searchRefineMinBytes, "search-refine-min-bytes", script.DefaultSearchRefineMinBytes, "minimum search output bytes before utility refinement or compaction")
	if err := fs.Parse(args); err != nil {
		return err
	}
	visitedFlags := visitedFlagNames(fs)
	var routePreset lcagentRoutePreset
	routePresetSet := false
	if strings.TrimSpace(routePresetRaw) != "" {
		var ok bool
		routePreset, ok = lcagentRoutePresetByName(routePresetRaw)
		if !ok {
			return fmt.Errorf("unknown route preset %q; available presets: %s", routePresetRaw, lcagentRoutePresetNames())
		}
		routePresetSet = true
		applyLCAgentRoutePreset(routePreset, visitedFlags, &provider, &model, &finalModel, &reasoningEffort, &autoRaw, &toolProfileRaw, &contextProfileRaw, &providerOnlyRaw, &temperatureRaw, &requestTimeout)
	}
	if !visitedFlags["max-turns"] {
		maxTurns = modeladapter.MaxTurnsForRequestTimeout(requestTimeout)
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if opts.PromptTransform != nil {
		prompt = strings.TrimSpace(opts.PromptTransform(prompt))
	}
	browserCapability := resolveBrowserCapability(browserControlRaw, browserSessionKey, browserProfileKey, browserLaunchModeRaw)
	if browserCapability.Err != nil {
		return browserCapability.Err
	}
	resumeSourceRaw := strings.TrimSpace(resumeRaw)
	continueSourceRaw := strings.TrimSpace(continueRaw)
	if resumeSourceRaw != "" && continueSourceRaw != "" && resumeSourceRaw != continueSourceRaw {
		return fmt.Errorf("--resume and --continue-from refer to different threads")
	}
	continuationReason := ""
	if continueSourceRaw != "" {
		resumeSourceRaw = continueSourceRaw
		continuationReason = "continue_from"
	} else if resumeSourceRaw != "" {
		continuationReason = "resume"
	}
	auto, err := policy.ParseAutonomy(autoRaw)
	if err != nil {
		return err
	}
	workspace, err := policy.NewWorkspace(cwd, auto)
	if err != nil {
		return err
	}
	workspace.AdminWrite = adminWrite
	instructions, err := projectinstructions.LoadWorkspace(workspace.Root)
	if err != nil {
		return fmt.Errorf("load project instructions: %w", err)
	}
	skillOptions := skillcatalog.DefaultOptions(workspace.Root)
	if browserCapability.Enabled {
		skillOptions.BrowserMode = skillcatalog.BrowserModeNativeTools
	} else {
		skillOptions.BrowserMode = skillcatalog.BrowserModeUnavailable
	}
	catalog, err := skillcatalog.Discover(context.Background(), skillOptions)
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
	utilityProvider, err := normalizeUtilityProvider(utilityProviderRaw)
	if err != nil {
		return err
	}
	visionProvider, err := normalizeVisionProvider(visionProviderRaw)
	if err != nil {
		return err
	}
	if searchRefineMinBytes < 0 {
		return fmt.Errorf("search-refine-min-bytes must be >= 0")
	}
	reasoningEffort = openRouterReasoningEffortForProvider(provider, reasoningEffort)
	approvalMode, err := normalizeApprovalMode(approvalModeRaw)
	if err != nil {
		return err
	}
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
		case "xiaomi":
			model = modeladapter.DefaultXiaomiModel
		case "ollama":
			resolved, err := resolveOllamaModel(context.Background(), envFile, requestTimeout)
			if err != nil {
				return err
			}
			model = resolved
		default:
			model = "scripted"
		}
	}
	model = modeladapter.NormalizeModelForProvider(provider, model)
	finalModel = modeladapter.NormalizeModelForProvider(provider, finalModel)
	contextOptions := openRouterContextOptionsForProfileAndModel(contextProfile, provider, model)
	resumeContext, err := loadResumeContext(dataDir, resumeSourceRaw, workspace.Root)
	if err != nil {
		return err
	}
	threadID := ""
	if resumeContext != nil {
		threadID = firstResumeNonEmpty(resumeContext.ThreadID, resumeContext.rootSessionID(), resumeContext.SourceSessionID)
	}
	if threadID == "" {
		threadID, err = newLCAgentThreadID()
		if err != nil {
			return err
		}
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
	hostOS, hostArch := currentHostEnvironment()
	meta := session.Meta(sessionID, workspace.Root, string(auto), provider, model, buildinfo.Version(), started)
	meta["thread_id"] = threadID
	meta["run_id"] = sessionID
	meta["host_os"] = hostOS
	meta["host_arch"] = hostArch
	meta["admin_write"] = adminWrite
	meta["approval_mode"] = approvalMode
	meta["request_timeout"] = requestTimeout.String()
	meta["max_turns"] = maxTurns
	meta["require_final_response_tool"] = requireFinalResponseTool
	meta["planning_preflight"] = planningPreflightEnabled
	if resumeContext != nil {
		meta["parent_session_id"] = resumeContext.SourceSessionID
		meta["root_session_id"] = resumeContext.rootSessionID()
		meta["continuation_depth"] = resumeContext.nextChainDepth()
		meta["continuation_reason"] = continuationReason
		if resumeContext.HandoffSource != "" {
			meta["handoff_source"] = resumeContext.HandoffSource
		}
	}
	if err := writer.Write(meta); err != nil {
		return err
	}
	threadStore := newThreadStateStore(dataDir, threadID, workspace.Root, sessionID, started)
	if routePresetSet {
		if err := writer.Write(session.Event{
			"type":              "route_preset",
			"session_id":        sessionID,
			"name":              routePreset.Name,
			"display_name":      routePreset.DisplayName,
			"description":       routePreset.Description,
			"resolved_provider": provider,
			"resolved_model":    model,
			"auto":              string(auto),
			"tool_profile":      string(toolProfile),
			"context_profile":   string(contextProfile),
			"reasoning_effort":  reasoningEffort,
			"request_timeout":   requestTimeout.String(),
			"max_turns":         maxTurns,
		}); err != nil {
			return err
		}
	}
	if strings.TrimSpace(opts.DelegationMode) != "" {
		if err := writer.Write(session.Event{
			"type":          "delegation_mode",
			"session_id":    sessionID,
			"mode":          strings.TrimSpace(opts.DelegationMode),
			"description":   strings.TrimSpace(opts.DelegationDescription),
			"read_only":     true,
			"handoff_items": []string{"Findings", "Relevant files", "Suggested next steps", "Risks or unknowns"},
		}); err != nil {
			return err
		}
	}
	if err := writer.Write(session.Event{
		"type":            "browser_capability",
		"session_id":      sessionID,
		"enabled":         browserCapability.Enabled,
		"launch_mode":     string(browserCapability.LaunchMode),
		"session_key":     browserCapability.SessionKey,
		"profile_key":     browserCapability.ProfileKey,
		"disabled_reason": browserCapability.DisabledReason,
	}); err != nil {
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
		if err := writer.Write(resumeContext.continuationEvent(sessionID, continuationReason)); err != nil {
			return err
		}
		if err := writer.Write(resumeContext.event(sessionID, continuationReason)); err != nil {
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
	var browserRunner script.BrowserRunner
	if browserCapability.Enabled {
		browserSession, err := browserctl.NewPlaywrightMCPBrowserSession(browserctl.BrowserSessionConfig{
			DataDir:     dataDir,
			Provider:    "lcagent",
			ProjectPath: workspace.Root,
			SessionKey:  browserCapability.SessionKey,
			ProfileKey:  browserCapability.ProfileKey,
			LaunchMode:  browserCapability.LaunchMode,
			Policy:      browserctl.PolicyFromEnv(),
		})
		if err != nil {
			return err
		}
		defer browserSession.Close()
		browserRunner = lcagentBrowserRunner{session: browserSession}
	}
	webSearch, webSearchStatus := tools.NewWebSearchRunner(tools.WebSearchConfig{
		Backend:        webSearchBackend,
		APIKey:         webSearchAPIKey,
		SearchEngineID: webSearchEngineID,
		URL:            webSearchURL,
		EnvFile:        envFile,
	})
	if err := writer.Write(session.Event{
		"type":       "web_search_profile",
		"session_id": sessionID,
		"enabled":    webSearchStatus.Enabled,
		"backend":    webSearchStatus.Backend,
		"message":    webSearchStatus.Message,
	}); err != nil {
		return err
	}
	runner := script.Runner{
		Session:          writer,
		Command:          tools.CommandRunner{Workspace: workspace, ArtifactDir: artifactDir},
		Patch:            tools.PatchApplier{Workspace: workspace},
		Files:            tools.FileTools{Workspace: workspace, Limits: fileLimits},
		WebSearch:        webSearch,
		WebSearchOn:      webSearchStatus.Enabled,
		BrowserAvailable: browserCapability.Enabled,
		Browser:          browserRunner,
		Skills:           catalog,
		SessionID:        sessionID,
		Prompt:           prompt,
		ArtifactsDir:     artifactDir,
	}
	if approvalMode == approvalModeAsk {
		broker := newStdioApprovalBroker(writer, sessionID, workspace.Root, os.Stdin)
		runner.Approvals = broker
		runner.Processes = broker
		runner.SteerMessages = broker.steerMessages
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
	case "openrouter", "openai", "deepseek", "moonshot", "xiaomi", "ollama":
		runErr = runChatLoop(context.Background(), writer, runner, threadStore, instructions.PromptSection(), resumeContext, modeladapter.OpenRouterConfig{
			Model:           model,
			FinalModel:      finalModel,
			EnvFile:         envFile,
			MaxTurns:        maxTurns,
			RequestTimeout:  requestTimeout,
			ReasoningEffort: reasoningEffort,
			Temperature:     temperature,
			OmitTemperature: omitTemperature,
			ProviderOnly:    splitCommaFields(providerOnlyRaw),
		}, modeladapter.OpenRouterConfig{
			Model:           utilityModel,
			EnvFile:         envFile,
			MaxTurns:        1,
			RequestTimeout:  requestTimeout,
			Temperature:     temperature,
			OmitTemperature: omitTemperature,
		}, modeladapter.OpenRouterConfig{
			Model:           visionModel,
			EnvFile:         envFile,
			MaxTurns:        1,
			RequestTimeout:  requestTimeout,
			Temperature:     temperature,
			OmitTemperature: omitTemperature,
		}, strings.ToLower(strings.TrimSpace(provider)), utilityProvider, visionProvider, searchRefineMinBytes, toolProfile, fileLimits, contextOptions, requireFinalResponseTool, planningPreflightEnabled, webSearchStatus.Enabled)
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

func runChatLoop(ctx context.Context, writer *session.Writer, runner script.Runner, threadStore *threadStateStore, projectInstructionPrompt string, resumeContext *resumeContext, cfg modeladapter.OpenRouterConfig, utilityCfg modeladapter.OpenRouterConfig, visionCfg modeladapter.OpenRouterConfig, provider, utilityProvider, visionProvider string, searchRefineMinBytes int, toolProfile tools.FileProfile, fileLimits tools.FileLimits, contextOptions openRouterContextOptions, requireFinalResponseTool bool, planningPreflightEnabled bool, webSearchEnabled bool) error {
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
	searchRefine := newSearchRefineProfile(utilityProvider, utilityCfg, searchRefineMinBytes, providerLabel, client.Model())
	if searchRefine.Enabled {
		runner.SearchRefiner = searchRefine.Refiner
		runner.CodeScout = searchRefine.Scout
		runner.SearchRefineMinBytes = searchRefine.MinBytes
	}
	vision := newVisionProfile(visionProvider, visionCfg, providerLabel, client.Model())
	if vision.Enabled {
		runner.ImageAnalyzer = visionAnalyzer{
			profile:       vision,
			workspaceRoot: runner.Files.Workspace.Root,
		}
	}
	planningPreflight := newPlanningPreflightProfile(planningPreflightEnabled, utilityProvider, utilityCfg, providerLabel, client.Model())
	if err := writer.Write(session.Event{
		"type":       "search_refine_profile",
		"session_id": runner.SessionID,
		"enabled":    searchRefine.Enabled,
		"provider":   searchRefine.Provider,
		"model":      searchRefine.Model,
		"min_bytes":  searchRefine.MinBytes,
		"message":    searchRefine.Message,
	}); err != nil {
		return err
	}
	if err := writer.Write(session.Event{
		"type":       "vision_profile",
		"session_id": runner.SessionID,
		"enabled":    vision.Enabled,
		"provider":   vision.Provider,
		"model":      vision.Model,
		"message":    vision.Message,
	}); err != nil {
		return err
	}
	if err := writer.Write(session.Event{
		"type":       "planning_preflight_profile",
		"session_id": runner.SessionID,
		"enabled":    planningPreflight.Enabled,
		"provider":   planningPreflight.Provider,
		"model":      planningPreflight.Model,
		"message":    planningPreflight.Message,
	}); err != nil {
		return err
	}
	var planningLeadMessage string
	if payload, ran, err := runPlanningPreflight(ctx, writer, runner.SessionID, planningPreflight, runner.Prompt, vision.Enabled); err != nil {
		return err
	} else if ran && planningPreflightRequiresQualityPlan(payload) {
		runner.QualityPlanRequired = true
		runner.QualityPlanRequirementReason = payload.Reason
		runner.QualityPlanRequirementScope = payload.Scope
		planningLeadMessage = planningPreflightLeadMessage(payload)
	}
	if err := writer.Write(session.Event{
		"type":       "user_message",
		"session_id": runner.SessionID,
		"origin":     "initial_prompt",
		"message":    runner.Prompt,
	}); err != nil {
		return err
	}
	activeObjective := trimActiveObjective(runner.Prompt)
	if threadStore != nil {
		threadStore.SetActiveObjective(activeObjective)
	}
	if activeObjective != "" {
		if err := writer.Write(session.Event{
			"type":       "active_objective",
			"session_id": runner.SessionID,
			"thread_id":  firstResumeNonEmpty(threadStoreThreadID(threadStore), runner.SessionID),
			"objective":  activeObjective,
		}); err != nil {
			return err
		}
	}

	systemPromptOptions := modelSystemPromptOptions(toolProfile, fileLimits)
	systemPromptOptions.WebSearchEnabled = webSearchEnabled
	systemPromptOptions.ManagedProcessesEnabled = runner.Processes != nil
	systemPromptOptions.AdminWrite = runner.Patch.Workspace.AdminWrite
	systemPromptOptions.BrowserAvailable = runner.BrowserAvailable
	systemPromptOptions.VisionAnalysisEnabled = vision.Enabled
	if resumeSection := resumeContext.systemPromptSection(); resumeSection != "" {
		projectInstructionPrompt = strings.TrimSpace(projectInstructionPrompt + "\n\n" + resumeSection)
	}
	systemPrompt := modeladapter.SystemPromptWithOptions(runner.Skills.PromptIndex(0), projectInstructionPrompt, systemPromptOptions)
	readLedger := newReadLedger()
	var messages []modeladapter.Message
	contextCompacted := resumeContext != nil && strings.EqualFold(strings.TrimSpace(resumeContext.ThreadContextMode), threadContextModeCompacted)
	if resumeContext != nil && resumeContext.hasExactMessages() {
		messages = resumeContext.exactMessages()
		if strings.TrimSpace(systemPrompt) != "" {
			messages = withCurrentSystemMessage(messages, systemPrompt)
		}
		observeReadLedgerMessages(readLedger, messages)
		messages = append(messages, modeladapter.Message{Role: "user", Content: runner.Prompt})
		if compactedMessages, compaction, compacted := compactOpenRouterLoopMessagesWithOptions(messages, readLedger, contextOptions); compacted {
			if err := writer.Write(session.Event{
				"type":             "context_compacted",
				"session_id":       runner.SessionID,
				"turn":             0,
				"threshold":        contextOptions.LoopCompactionCharThreshold,
				"threshold_tokens": contextOptions.LoopCompactionTokenBudget,
				"reason":           "continuation_preflight",
				"stats":            compaction,
			}); err != nil {
				return err
			}
			messages = compactedMessages
			contextCompacted = true
		}
	} else {
		messages = []modeladapter.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: runner.Prompt},
		}
	}
	if strings.TrimSpace(planningLeadMessage) != "" {
		messages = append(messages, modeladapter.Message{Role: "user", Content: planningLeadMessage})
	}
	toolOptions := modelToolOptions(toolProfile, fileLimits)
	toolOptions.WebSearchEnabled = webSearchEnabled
	toolOptions.ManagedProcessesEnabled = runner.Processes != nil
	toolOptions.AdminWrite = runner.Patch.Workspace.AdminWrite
	toolOptions.BrowserAvailable = runner.BrowserAvailable
	toolOptions.VisionAnalysisEnabled = vision.Enabled
	toolsDef := modeladapter.ToolsWithOptions(toolOptions)
	finalVerificationFeedbacks := 0
	finalResponseToolFeedbacks := 0
	lastPassedVerificationFileTouchEvents := 0
	deferNextSynthesis := false
	feedbackTracker := newOpenRouterFeedbackTracker()
	progressTracker := newOpenRouterLoopProgressTracker(messages, runner)
	lastQualityPlanCompletedPrefix := runner.QualityPlanCompletedPrefix()
	for turn := 0; turn < client.MaxTurns(); turn++ {
		progressTracker.Observe(messages, runner)
		select {
		case steerMsg := <-runner.SteerMessages:
			if strings.TrimSpace(steerMsg) != "" {
				activeObjective = trimActiveObjective(steerMsg)
				if threadStore != nil {
					threadStore.SetActiveObjective(activeObjective)
				}
				if err := writer.Write(session.Event{
					"type":       "user_message",
					"session_id": runner.SessionID,
					"origin":     "steer",
					"message":    steerMsg,
				}); err != nil {
					return err
				}
				if activeObjective != "" {
					if err := writer.Write(session.Event{
						"type":       "active_objective",
						"session_id": runner.SessionID,
						"thread_id":  firstResumeNonEmpty(threadStoreThreadID(threadStore), runner.SessionID),
						"objective":  activeObjective,
					}); err != nil {
						return err
					}
				}
				messages = append(messages, modeladapter.Message{Role: "user", Content: steerMsg})
			}
		default:
		}
		if compactedMessages, compaction, compacted := compactOpenRouterLoopMessagesWithOptions(messages, readLedger, contextOptions); compacted {
			if err := writer.Write(session.Event{
				"type":             "context_compacted",
				"session_id":       runner.SessionID,
				"turn":             turn + 1,
				"threshold":        contextOptions.LoopCompactionCharThreshold,
				"threshold_tokens": contextOptions.LoopCompactionTokenBudget,
				"stats":            compaction,
			}); err != nil {
				return err
			}
			messages = compactedMessages
			contextCompacted = true
		}
		guidance := openRouterGuidanceForTurnWithOptions(turn+1, client.MaxTurns(), messages, readLedger, openRouterGuidanceOptions{ToolProfile: string(toolProfile)})
		if progressTracker.ShouldForceSynthesis(guidance) {
			guidance.Phase = "synthesis"
			guidance.ForceSynthesis = true
			guidance.SynthesisReason = "stalled"
			guidance.NoProgressTurns = progressTracker.NoProgressTurns()
		}
		if deferNextSynthesis {
			if guidance.ForceSynthesis && guidance.TurnsRemaining > 0 {
				guidance.Phase = "consolidation"
				guidance.ForceSynthesis = false
				guidance.SynthesisReason = ""
				guidance.NoProgressTurns = 0
			}
			deferNextSynthesis = false
		}
		if shouldDeferSynthesisForUnverifiedChanges(guidance, runner.FileTouchEvents(), lastPassedVerificationFileTouchEvents) {
			guidance.Phase = "consolidation"
			guidance.ForceSynthesis = false
		}
		requestMessages := appendOpenRouterProgressNote(messages, guidance, readLedger)
		requestTools := toolsDef
		if guidance.ForceSynthesis {
			var compaction finalHandoffCompactionStats
			requestMessages, compaction = compactOpenRouterFinalMessagesWithOptions(messages, openRouterSynthesisFinalPrompt(guidance, requireFinalResponseTool), readLedger, contextOptions)
			if requireFinalResponseTool {
				requestTools = finalResponseOnlyTools(toolsDef)
			} else {
				requestTools = nil
			}
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
		if threadStore != nil {
			if err := threadStore.MarkInFlight("model_request", messages, contextCompacted); err != nil {
				return err
			}
		}
		var completion modeladapter.Completion
		requestClient := client
		requestPhase := "tool_loop"
		requestOptions := openRouterCompletionOptions(cfg)
		if guidance.ForceSynthesis {
			requestClient = finalClient
			requestPhase = "synthesis"
			requestOptions = openRouterFinalCompletionOptions(cfg)
		}
		completion, err = completeProviderWithRetriesValidated(ctx, writer, runner.SessionID, providerLabel, requestPhase, turn+1, requestClient.Model(), validateVisibleCompletion(providerLabel), func() (modeladapter.Completion, error) {
			return requestClient.CompleteWithOptions(ctx, requestMessages, requestTools, requestOptions)
		})
		if err != nil {
			return abortOpenRouterRun(writer, runner.SessionID, err)
		}
		msg := completion.Message
		if err := writer.Write(modelResponseEvent(runner.SessionID, providerLabel, requestPhase, turn+1, completion, len(msg.ToolCalls))); err != nil {
			return err
		}
		sanitizedContent, strippedProviderMarkup := modeladapter.SanitizeAssistantContent(msg.Content)
		msg.Content = sanitizedContent
		ensureToolCallIDs(msg.ToolCalls, turn+1)
		if guidance.ForceSynthesis && len(msg.ToolCalls) > 0 && (!requireFinalResponseTool || !allToolCallsNamed(msg.ToolCalls, "final_response")) {
			feedback := synthesisToolCallRejectedFeedbackMessage()
			if err := writeSynthesisToolCallRejected(writer, runner.SessionID, feedback, msg.ToolCalls); err != nil {
				return err
			}
			if guidance.TurnsRemaining > 0 {
				messages = append(messages, modeladapter.Message{Role: "user", Content: feedback})
				deferNextSynthesis = true
				continue
			}
			return finalizeMaxTurnsFallback(writer, runner, threadStore, requestMessages, client.MaxTurns(), runner.FilesTouched(), runner.VerificationDetails(), fmt.Sprintf("%s synthesis request tried to call unavailable tools at the hard turn limit: %s", providerLabel, strings.Join(toolCallNames(msg.ToolCalls), ", ")))
		}
		messages = append(messages, msg)
		if len(msg.ToolCalls) == 0 {
			if strippedProviderMarkup {
				if turn+1 >= client.MaxTurns() {
					return finalizeMaxTurnsFallback(writer, runner, threadStore, messages, client.MaxTurns(), runner.FilesTouched(), runner.VerificationDetails(), fmt.Sprintf("%s response contained provider tool-call markup but no structured tool calls at the turn limit", providerLabel))
				}
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response contained provider tool-call markup but no structured tool calls", providerLabel))
			}
			if strings.EqualFold(completion.FinishReason, "tool_calls") {
				if turn+1 >= client.MaxTurns() {
					return finalizeMaxTurnsFallback(writer, runner, threadStore, messages, client.MaxTurns(), runner.FilesTouched(), runner.VerificationDetails(), fmt.Sprintf("%s response finished with tool_calls but returned no structured tool calls at the turn limit", providerLabel))
				}
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response finished with tool_calls but returned no structured tool calls", providerLabel))
			}
			if strings.TrimSpace(msg.Content) == "" {
				if turn+1 >= client.MaxTurns() {
					return finalizeMaxTurnsFallback(writer, runner, threadStore, messages, client.MaxTurns(), runner.FilesTouched(), runner.VerificationDetails(), fmt.Sprintf("%s response had no content or tool calls at the turn limit", providerLabel))
				}
				return abortOpenRouterRun(writer, runner.SessionID, fmt.Errorf("%s response had no content or tool calls", providerLabel))
			}
			if requireFinalResponseTool {
				finalResponseToolFeedbacks++
				feedback := finalResponseToolFeedbackMessage()
				if err := writer.Write(session.Event{
					"type":       "final_response_feedback",
					"session_id": runner.SessionID,
					"message":    feedback,
				}); err != nil {
					return err
				}
				if turn+1 >= client.MaxTurns() {
					return finalizeMaxTurnsFallback(writer, runner, threadStore, messages, client.MaxTurns(), runner.FilesTouched(), runner.VerificationDetails(), fmt.Sprintf("%s did not call final_response before the turn limit", providerLabel))
				}
				messages = append(messages, modeladapter.Message{Role: "user", Content: feedback})
				deferNextSynthesis = true
				continue
			}
			final := script.Action{
				Type:    "final_response",
				Summary: msg.Content,
			}
			audit := runner.FinalResponseAudit(final)
			if shouldBounceFinalAudit(audit, finalVerificationFeedbacks) {
				finalVerificationFeedbacks++
				if err := runner.WriteFinalResponseAudit(audit); err != nil {
					return err
				}
				feedback, _ := audit.VerificationFeedback()
				if err := runner.WriteVerificationFeedback(feedback); err != nil {
					return err
				}
				messages = append(messages, modeladapter.Message{Role: "user", Content: feedback.ModelMessage()})
				deferNextSynthesis = true
				continue
			}
			if err := runner.Final(final); err != nil {
				return err
			}
			if err := writeModelContextSnapshot(writer, threadStore, runner.SessionID, "assistant_message", messages, contextCompacted); err != nil {
				return err
			}
			return nil
		}
		if threadStore != nil {
			if err := threadStore.MarkPendingTools("assistant_tool_calls", messages, contextCompacted, msg.ToolCalls); err != nil {
				return err
			}
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
		var pendingVerificationFeedback []script.VerificationFeedback
		var pendingPatchFeedback []script.PatchFeedback
		qualityPhaseCompactionPending := false
		for _, call := range msg.ToolCalls {
			args, err := modeladapter.NormalizeArguments(call.Function.Arguments)
			if err != nil {
				result := invalidToolArgumentsResult(toolCallName(call), err)
				if err := writeInvalidToolArgumentsResult(writer, runner.SessionID, call, call.Function.Arguments, result); err != nil {
					return err
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
				deferNextSynthesis = true
				continue
			}
			action := script.Action{Type: "tool_call", Tool: call.Function.Name, Args: args}
			if call.Function.Name == "final_response" {
				final, err := script.DecodeFinalResponseArgs(args)
				if err != nil {
					result := invalidToolArgumentsResult("final_response", err)
					if err := writeInvalidToolArgumentsResult(writer, runner.SessionID, call, args, result); err != nil {
						return err
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
					deferNextSynthesis = true
					continue
				}
				audit := runner.FinalResponseAudit(final)
				if shouldBounceFinalAudit(audit, finalVerificationFeedbacks) {
					finalVerificationFeedbacks++
					if err := writer.Write(session.Event{
						"type":       "tool_call",
						"session_id": runner.SessionID,
						"tool":       call.Function.Name,
						"args":       json.RawMessage(args),
					}); err != nil {
						return err
					}
					if err := runner.WriteFinalResponseAudit(audit); err != nil {
						return err
					}
					feedback, _ := audit.VerificationFeedback()
					if err := runner.WriteVerificationFeedback(feedback); err != nil {
						return err
					}
					result := tools.ToolResult{Success: false, Error: feedback.ModelMessage()}
					if err := writer.Write(session.Event{
						"type":       "tool_result",
						"session_id": runner.SessionID,
						"tool":       call.Function.Name,
						"result":     result,
					}); err != nil {
						return err
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
					messages = append(messages, modeladapter.Message{Role: "user", Content: feedback.ModelMessage()})
					deferNextSynthesis = true
					continue
				}
				snapshotMessages := appendFinalResponseForContextSnapshot(messages, call.ID, final)
				if err := runner.Final(final); err != nil {
					return err
				}
				if err := writeModelContextSnapshot(writer, threadStore, runner.SessionID, "final_response", snapshotMessages, contextCompacted); err != nil {
					return err
				}
				return nil
			}
			result, err := runner.RunTool(ctx, action)
			if call.Function.Name == "read_file" {
				readLedger.ObserveReadResult(result)
			}
			if call.Function.Name == "update_quality_plan" && result.Success {
				completedPrefix := runner.QualityPlanCompletedPrefix()
				if completedPrefix > lastQualityPlanCompletedPrefix {
					lastQualityPlanCompletedPrefix = completedPrefix
					qualityPhaseCompactionPending = true
				}
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
			if feedback, ok := runner.VerificationFeedbackForResult(result); ok {
				pendingVerificationFeedback = append(pendingVerificationFeedback, feedback)
			}
			if strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) && result.Success {
				lastPassedVerificationFileTouchEvents = runner.FileTouchEvents()
			}
			if feedback, ok := script.PatchFeedbackForResult(result); ok {
				pendingPatchFeedback = append(pendingPatchFeedback, feedback)
			}
			if err != nil {
				// Feed tool failures back to the model once; the structured event has
				// already recorded the failure for LCR.
				continue
			}
		}
		for _, feedback := range pendingPatchFeedback {
			if !feedbackTracker.Allow("patch", feedback.ModelMessage()) {
				count := feedbackTracker.Count("patch", feedback.ModelMessage())
				if err := writeRepairFeedbackSuppressed(writer, runner.SessionID, "patch", feedback.ModelMessage(), count); err != nil {
					return err
				}
				if guidance := script.PatchRetryGuidance(feedback, count); guidance != "" && count == 2 {
					if err := writeRepairGuidance(writer, runner.SessionID, "patch", guidance, count); err != nil {
						return err
					}
					messages = append(messages, modeladapter.Message{Role: "user", Content: guidance})
					deferNextSynthesis = true
				}
				continue
			}
			if err := runner.WritePatchFeedback(feedback); err != nil {
				return err
			}
			messages = append(messages, modeladapter.Message{Role: "user", Content: feedback.ModelMessage()})
			deferNextSynthesis = true
		}
		for _, feedback := range pendingVerificationFeedback {
			if !feedbackTracker.Allow("verification", feedback.ModelMessage()) {
				if err := writeRepairFeedbackSuppressed(writer, runner.SessionID, "verification", feedback.ModelMessage(), feedbackTracker.Count("verification", feedback.ModelMessage())); err != nil {
					return err
				}
				continue
			}
			if err := runner.WriteVerificationFeedback(feedback); err != nil {
				return err
			}
			messages = append(messages, modeladapter.Message{Role: "user", Content: feedback.ModelMessage()})
			deferNextSynthesis = true
		}
		if qualityPhaseCompactionPending {
			compactedMessages, compacted, err := forceOpenRouterLoopCompaction(writer, runner.SessionID, turn+1, "quality_phase_completed", messages, readLedger, contextOptions)
			if err != nil {
				return err
			}
			if compacted {
				messages = compactedMessages
				contextCompacted = true
			}
		}
		if err := writeModelContextSnapshot(writer, threadStore, runner.SessionID, "tool_result", messages, contextCompacted); err != nil {
			return err
		}
	}
	return finalizeChatLoopAfterMaxTurns(ctx, writer, runner, threadStore, client, finalClient, messages, readLedger, providerLabel, cfg, contextOptions)
}

func newChatProviderClient(provider string, cfg modeladapter.OpenRouterConfig) (*modeladapter.Client, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return modeladapter.NewOpenAIClient(cfg)
	case "deepseek":
		return modeladapter.NewDeepSeekClient(cfg)
	case "moonshot":
		return modeladapter.NewMoonshotClient(cfg)
	case "xiaomi":
		return modeladapter.NewXiaomiClient(cfg)
	case "ollama":
		return modeladapter.NewOllamaClient(cfg)
	default:
		return modeladapter.NewOpenRouterClient(cfg)
	}
}

func resolveOllamaModel(ctx context.Context, envFile string, timeout time.Duration) (string, error) {
	requestTimeout := timeout
	if requestTimeout <= 0 || requestTimeout > 30*time.Second {
		requestTimeout = 15 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client, err := modeladapter.NewOllamaClient(modeladapter.OpenRouterConfig{
		EnvFile:        envFile,
		RequestTimeout: requestTimeout,
	})
	if err != nil {
		return "", err
	}
	models, err := client.ListModels(checkCtx)
	if err != nil {
		return "", fmt.Errorf("ollama model is required and /v1/models could not be read: %w", err)
	}
	for _, item := range models {
		if id := strings.TrimSpace(item.ID); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("ollama model is required and /v1/models returned no usable models")
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
	if strings.EqualFold(strings.TrimSpace(provider), "ollama") {
		return strings.TrimSpace(os.Getenv("OLLAMA_FINAL_MODEL"))
	}
	return strings.TrimSpace(os.Getenv("OPENROUTER_FINAL_MODEL"))
}

func shouldBounceFinalAudit(audit script.FinalResponseAudit, feedbackCount int) bool {
	if !audit.Blocking {
		return false
	}
	code := strings.TrimSpace(audit.Code)
	if strings.EqualFold(code, "browser_wait_required") {
		return true
	}
	if strings.HasPrefix(code, "quality_plan_") {
		return true
	}
	return feedbackCount == 0
}

func forceOpenRouterLoopCompaction(writer *session.Writer, sessionID string, turn int, reason string, messages []modeladapter.Message, readLedger *readLedger, contextOptions openRouterContextOptions) ([]modeladapter.Message, bool, error) {
	contextOptions = contextOptions.withDefaults()
	forcedOptions := contextOptions
	forcedOptions.LoopCompactionCharThreshold = 1
	compactedMessages, compaction, compacted := compactOpenRouterLoopMessagesWithOptions(messages, readLedger, forcedOptions)
	if !compacted {
		return messages, false, nil
	}
	if writer != nil {
		if err := writer.Write(session.Event{
			"type":             "context_compacted",
			"session_id":       sessionID,
			"turn":             turn,
			"threshold":        forcedOptions.LoopCompactionCharThreshold,
			"threshold_tokens": contextOptions.LoopCompactionTokenBudget,
			"reason":           strings.TrimSpace(reason),
			"stats":            compaction,
		}); err != nil {
			return nil, false, err
		}
	}
	return compactedMessages, true, nil
}

func shouldDeferSynthesisForUnverifiedChanges(guidance openRouterProgressGuidance, fileTouchEvents int, lastPassedVerificationFileTouchEvents int) bool {
	if !guidance.ForceSynthesis || guidance.TurnsRemaining <= 0 {
		return false
	}
	if strings.EqualFold(guidance.SynthesisReason, "stalled") {
		return false
	}
	return fileTouchEvents > lastPassedVerificationFileTouchEvents
}

type openRouterLoopProgressTracker struct {
	observedMessages       int
	seenToolResultKeys     map[string]struct{}
	lastFileTouchEvents    int
	lastVerificationChecks int
	noProgressTurns        int
}

func newOpenRouterLoopProgressTracker(messages []modeladapter.Message, runner script.Runner) *openRouterLoopProgressTracker {
	return &openRouterLoopProgressTracker{
		observedMessages:       len(messages),
		seenToolResultKeys:     map[string]struct{}{},
		lastFileTouchEvents:    runner.FileTouchEvents(),
		lastVerificationChecks: len(runner.VerificationDetails()),
	}
}

func (t *openRouterLoopProgressTracker) Observe(messages []modeladapter.Message, runner script.Runner) {
	if t == nil {
		return
	}
	if len(messages) < t.observedMessages {
		t.observedMessages = len(messages)
	}
	hadNewObservation := len(messages) > t.observedMessages
	progress := false
	for _, msg := range messages[t.observedMessages:] {
		if msg.Role != "tool" {
			continue
		}
		key := openRouterToolResultProgressKey(msg.Content)
		if key == "" {
			continue
		}
		if _, ok := t.seenToolResultKeys[key]; ok {
			continue
		}
		t.seenToolResultKeys[key] = struct{}{}
		progress = true
	}
	t.observedMessages = len(messages)

	fileTouchEvents := runner.FileTouchEvents()
	if fileTouchEvents != t.lastFileTouchEvents {
		hadNewObservation = true
		if fileTouchEvents > t.lastFileTouchEvents {
			progress = true
		}
		t.lastFileTouchEvents = fileTouchEvents
	}
	verificationChecks := len(runner.VerificationDetails())
	if verificationChecks != t.lastVerificationChecks {
		hadNewObservation = true
		if verificationChecks > t.lastVerificationChecks {
			progress = true
		}
		t.lastVerificationChecks = verificationChecks
	}
	if !hadNewObservation {
		return
	}
	if progress {
		t.noProgressTurns = 0
		return
	}
	t.noProgressTurns++
}

func (t *openRouterLoopProgressTracker) ShouldForceSynthesis(guidance openRouterProgressGuidance) bool {
	if t == nil || guidance.ForceSynthesis {
		return false
	}
	if guidance.Turn < openRouterMinimumTurnBeforeStallCheck {
		return false
	}
	return t.noProgressTurns >= openRouterStallSynthesisAfterTurns
}

func (t *openRouterLoopProgressTracker) NoProgressTurns() int {
	if t == nil {
		return 0
	}
	return t.noProgressTurns
}

func openRouterToolResultProgressKey(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var result tools.ToolResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		result.Duration = 0
		if stable, err := json.Marshal(result); err == nil {
			content = string(stable)
		}
	}
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}

func finalizeChatLoopAfterMaxTurns(ctx context.Context, writer *session.Writer, runner script.Runner, threadStore *threadStateStore, client *modeladapter.Client, finalClient *modeladapter.Client, messages []modeladapter.Message, readLedger *readLedger, providerLabel string, cfg modeladapter.OpenRouterConfig, contextOptions openRouterContextOptions) error {
	maxTurns := client.MaxTurns()
	filesChanged := runner.FilesTouched()
	verification := runner.VerificationDetails()
	compactedMessages, compaction := compactOpenRouterFinalMessagesWithOptions(messages, openRouterMaxTurnsFinalPrompt(maxTurns, filesChanged, verification), readLedger, contextOptions)
	if err := writer.Write(session.Event{
		"type":        "final_handoff_compacted",
		"session_id":  runner.SessionID,
		"final_model": finalClient.Model(),
		"stats":       compaction,
	}); err != nil {
		return err
	}
	if threadStore != nil {
		if err := threadStore.MarkInFlight("final_handoff_request", compactedMessages, true); err != nil {
			return err
		}
	}
	completion, err := completeProviderWithRetriesValidated(ctx, writer, runner.SessionID, providerLabel, "final_handoff", maxTurns+1, finalClient.Model(), validateVisibleCompletion(providerLabel), func() (modeladapter.Completion, error) {
		return finalClient.CompleteWithOptions(ctx, compactedMessages, nil, openRouterFinalCompletionOptions(cfg))
	})
	if err != nil {
		return finalizeMaxTurnsFallback(writer, runner, threadStore, compactedMessages, maxTurns, filesChanged, verification, fmt.Sprintf("%s final handoff failed: %v", providerLabel, err))
	}
	msg := completion.Message
	if err := writer.Write(modelResponseEvent(runner.SessionID, providerLabel, "final_handoff", maxTurns+1, completion, len(msg.ToolCalls))); err != nil {
		return err
	}
	sanitizedContent, strippedProviderMarkup := modeladapter.SanitizeAssistantContent(msg.Content)
	if len(msg.ToolCalls) > 0 {
		return finalizeMaxTurnsFallback(writer, runner, threadStore, compactedMessages, maxTurns, filesChanged, verification, fmt.Sprintf("%s final handoff tried to call tools", providerLabel))
	}
	if strippedProviderMarkup && strings.TrimSpace(sanitizedContent) == "" {
		return finalizeMaxTurnsFallback(writer, runner, threadStore, compactedMessages, maxTurns, filesChanged, verification, fmt.Sprintf("%s final handoff contained only provider tool-call markup", providerLabel))
	}
	sanitizedContent = strings.TrimSpace(sanitizedContent)
	if sanitizedContent == "" {
		return finalizeMaxTurnsFallback(writer, runner, threadStore, compactedMessages, maxTurns, filesChanged, verification, fmt.Sprintf("%s final handoff was empty", providerLabel))
	}
	final := script.Action{
		Type:         "final_response",
		Summary:      sanitizedContent,
		FilesChanged: filesChanged,
		Verification: verification,
	}
	if err := runner.Final(final); err != nil {
		return err
	}
	snapshotMessages := appendAssistantContentForContextSnapshot(compactedMessages, sanitizedContent)
	if err := writeModelContextSnapshot(writer, threadStore, runner.SessionID, "final_handoff", snapshotMessages, true); err != nil {
		return err
	}
	return nil
}

func finalizeMaxTurnsFallback(writer *session.Writer, runner script.Runner, threadStore *threadStateStore, compactedMessages []modeladapter.Message, maxTurns int, filesChanged []string, verification []string, reason string) error {
	summary := maxTurnsFallbackSummary(maxTurns, filesChanged, verification, reason)
	if err := writer.Write(session.Event{
		"type":             "final_handoff_fallback",
		"session_id":       runner.SessionID,
		"reason":           strings.TrimSpace(reason),
		"files_changed":    filesChanged,
		"verification":     verification,
		"fallback_summary": summary,
	}); err != nil {
		return err
	}
	final := script.Action{
		Type:         "final_response",
		Summary:      summary,
		Outcome:      "partial",
		FilesChanged: filesChanged,
		Verification: verification,
	}
	if err := runner.Final(final); err != nil {
		return err
	}
	snapshotMessages := appendAssistantContentForContextSnapshot(compactedMessages, summary)
	return writeModelContextSnapshot(writer, threadStore, runner.SessionID, "final_handoff_fallback", snapshotMessages, true)
}

func maxTurnsFallbackSummary(maxTurns int, filesChanged []string, verification []string, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The configured model turn budget was reached after %d turns before LCAgent could produce a normal final handoff.", maxTurns)
	if reason = strings.TrimSpace(reason); reason != "" {
		b.WriteString(" Final handoff fallback reason: ")
		b.WriteString(reason)
		b.WriteString(".")
	}
	if len(filesChanged) > 0 {
		b.WriteString(" Files changed: ")
		b.WriteString(strings.Join(filesChanged, ", "))
		b.WriteString(".")
	} else {
		b.WriteString(" No file changes were recorded.")
	}
	if len(verification) > 0 {
		b.WriteString(" Verification recorded: ")
		b.WriteString(strings.Join(verification, "; "))
		b.WriteString(".")
	} else {
		b.WriteString(" No verification evidence was recorded.")
	}
	b.WriteString(" The session state was saved so the task can be continued from this point.")
	return b.String()
}

func openRouterMaxTurnsFinalPrompt(maxTurns int, filesChanged, verification []string) string {
	prompt := fmt.Sprintf(`You have reached the configured maximum of %d model turns.

Do not call more tools. Produce a concise handoff for the user instead:
- Say that the turn budget was reached.
- Summarize what you completed or learned from the available tool results.
- List any files you believe were changed, or say none/unknown.
- List verification already run, or say not run.
- State the next concrete step the user can ask for to continue.`, maxTurns)
	var state []string
	if len(filesChanged) > 0 {
		state = append(state, "Files touched by edit tools: "+strings.Join(filesChanged, ", "))
	}
	if len(verification) > 0 {
		state = append(state, "Verification checks already recorded: "+strings.Join(verification, "; "))
	}
	if len(state) > 0 {
		prompt += "\n\nHarness-known continuation state:\n- " + strings.Join(state, "\n- ")
	}
	return prompt
}

func openRouterCompletionOptions(cfg modeladapter.OpenRouterConfig) modeladapter.CompletionOptions {
	return modeladapter.CompletionOptions{
		ReasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
	}
}

type openRouterFeedbackTracker struct {
	counts map[string]int
}

func newOpenRouterFeedbackTracker() *openRouterFeedbackTracker {
	return &openRouterFeedbackTracker{counts: map[string]int{}}
}

func (t *openRouterFeedbackTracker) Allow(kind, message string) bool {
	key := openRouterFeedbackKey(kind, message)
	if key == "" {
		return true
	}
	t.counts[key]++
	return t.counts[key] == 1
}

func (t *openRouterFeedbackTracker) Count(kind, message string) int {
	if t == nil {
		return 0
	}
	return t.counts[openRouterFeedbackKey(kind, message)]
}

func openRouterFeedbackKey(kind, message string) string {
	kind = strings.TrimSpace(kind)
	message = strings.TrimSpace(message)
	if kind == "" || message == "" {
		return ""
	}
	return kind + "\x00" + message
}

func writeRepairFeedbackSuppressed(writer *session.Writer, sessionID, kind, message string, count int) error {
	return writer.Write(session.Event{
		"type":       "repair_feedback_suppressed",
		"session_id": sessionID,
		"kind":       strings.TrimSpace(kind),
		"message":    strings.TrimSpace(message),
		"count":      count,
		"reason":     "duplicate feedback already sent to model",
	})
}

func writeRepairGuidance(writer *session.Writer, sessionID, kind, message string, count int) error {
	return writer.Write(session.Event{
		"type":       "repair_guidance",
		"session_id": sessionID,
		"kind":       strings.TrimSpace(kind),
		"message":    strings.TrimSpace(message),
		"count":      count,
		"reason":     "duplicate feedback escalated to strategy guidance",
	})
}

func writeSynthesisToolCallRejected(writer *session.Writer, sessionID string, message string, calls []modeladapter.ToolCall) error {
	if writer == nil {
		return nil
	}
	return writer.Write(session.Event{
		"type":            "synthesis_tool_call_rejected",
		"session_id":      sessionID,
		"message":         strings.TrimSpace(message),
		"attempted_tools": toolCallNames(calls),
		"reason":          "synthesis accepts only final_response or plain final content",
	})
}

func writeInvalidToolArgumentsResult(writer *session.Writer, sessionID string, call modeladapter.ToolCall, rawArgs json.RawMessage, result tools.ToolResult) error {
	if writer == nil {
		return nil
	}
	args := json.RawMessage(`null`)
	rawArgs = json.RawMessage(bytes.TrimSpace(rawArgs))
	event := session.Event{
		"type":       "tool_call",
		"session_id": sessionID,
		"tool":       toolCallName(call),
		"args":       args,
	}
	if json.Valid(rawArgs) {
		event["args"] = rawArgs
	} else if len(rawArgs) > 0 {
		event["raw_args"] = string(rawArgs)
	}
	event["args_decode_error"] = strings.TrimSpace(result.Error)
	if err := writer.Write(event); err != nil {
		return err
	}
	return writer.Write(session.Event{
		"type":       "tool_result",
		"session_id": sessionID,
		"tool":       toolCallName(call),
		"result":     result,
	})
}

const providerRetryMaxAttempts = 3

var modelRequestProgressInterval = 15 * time.Second

func completeProviderWithRetries(ctx context.Context, writer *session.Writer, sessionID, provider, phase string, turn int, modelName string, call func() (modeladapter.Completion, error)) (modeladapter.Completion, error) {
	return completeProviderWithRetriesValidated(ctx, writer, sessionID, provider, phase, turn, modelName, nil, call)
}

func completeProviderWithRetriesValidated(ctx context.Context, writer *session.Writer, sessionID, provider, phase string, turn int, modelName string, validate func(modeladapter.Completion) error, call func() (modeladapter.Completion, error)) (modeladapter.Completion, error) {
	var lastErr error
	for attempt := 1; attempt <= providerRetryMaxAttempts; attempt++ {
		startedAt := time.Now()
		if err := writeModelRequestEvent(writer, "model_request_started", sessionID, provider, phase, turn, modelName, attempt, 0); err != nil {
			return modeladapter.Completion{}, err
		}
		stopProgress := startModelRequestProgress(ctx, writer, sessionID, provider, phase, turn, modelName, attempt, startedAt)
		completion, err := call()
		if progressErr := stopProgress(); progressErr != nil {
			return modeladapter.Completion{}, progressErr
		}
		if err == nil && validate != nil {
			if validationErr := validate(completion); validationErr != nil {
				event := modelResponseEvent(sessionID, provider, phase, turn, completion, len(completion.Message.ToolCalls))
				event["invalid"] = true
				event["attempt"] = attempt
				if writeErr := writer.Write(event); writeErr != nil {
					return modeladapter.Completion{}, writeErr
				}
				err = validationErr
			}
		}
		if err == nil {
			if attempt > 1 {
				if writeErr := writer.Write(session.Event{
					"type":       "provider_retry_succeeded",
					"session_id": sessionID,
					"provider":   strings.TrimSpace(provider),
					"model":      strings.TrimSpace(modelName),
					"phase":      strings.TrimSpace(phase),
					"turn":       turn,
					"attempt":    attempt,
				}); writeErr != nil {
					return modeladapter.Completion{}, writeErr
				}
			}
			return completion, nil
		}
		lastErr = err
		failure, _ := modeladapter.AsProviderError(err)
		retryable := failure != nil && failure.Retryable && attempt < providerRetryMaxAttempts && ctx.Err() == nil
		delay := providerRetryDelay(failure, attempt)
		if writeErr := writeProviderFailure(writer, sessionID, provider, phase, turn, modelName, attempt, retryable, delay, err, failure); writeErr != nil {
			return modeladapter.Completion{}, writeErr
		}
		if !retryable {
			return modeladapter.Completion{}, err
		}
		if writeErr := writer.Write(session.Event{
			"type":           "provider_retry",
			"session_id":     sessionID,
			"provider":       strings.TrimSpace(provider),
			"model":          strings.TrimSpace(modelName),
			"phase":          strings.TrimSpace(phase),
			"turn":           turn,
			"attempt":        attempt + 1,
			"previous_error": err.Error(),
			"delay_ms":       delay.Milliseconds(),
		}); writeErr != nil {
			return modeladapter.Completion{}, writeErr
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return modeladapter.Completion{}, ctx.Err()
		case <-timer.C:
		}
	}
	return modeladapter.Completion{}, lastErr
}

func startModelRequestProgress(ctx context.Context, writer *session.Writer, sessionID, provider, phase string, turn int, modelName string, attempt int, startedAt time.Time) func() error {
	interval := modelRequestProgressInterval
	if interval <= 0 {
		return func() error { return nil }
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- nil
				return
			case <-timer.C:
				elapsed := time.Since(startedAt)
				if err := writeModelRequestEvent(writer, "model_request_progress", sessionID, provider, phase, turn, modelName, attempt, elapsed); err != nil {
					done <- err
					return
				}
				timer.Reset(interval)
			}
		}
	}()
	return func() error {
		close(stop)
		return <-done
	}
}

func writeModelRequestEvent(writer *session.Writer, eventType, sessionID, provider, phase string, turn int, modelName string, attempt int, elapsed time.Duration) error {
	event := session.Event{
		"type":       eventType,
		"session_id": sessionID,
		"provider":   strings.TrimSpace(provider),
		"model":      strings.TrimSpace(modelName),
		"phase":      strings.TrimSpace(phase),
		"turn":       turn,
		"attempt":    attempt,
	}
	if elapsed > 0 {
		event["elapsed_ms"] = elapsed.Round(time.Millisecond).Milliseconds()
	}
	return writer.Write(event)
}

func validateVisibleCompletion(provider string) func(modeladapter.Completion) error {
	provider = strings.TrimSpace(provider)
	return func(completion modeladapter.Completion) error {
		if len(completion.Message.ToolCalls) > 0 || strings.TrimSpace(completion.Message.Content) != "" {
			return nil
		}
		return &modeladapter.ProviderError{
			Provider:  provider,
			Kind:      modeladapter.ProviderFailureMalformedResponse,
			Message:   "response had no content or tool calls",
			Retryable: true,
		}
	}
}

func openRouterReasoningEffortForProvider(provider, reasoningEffort string) string {
	reasoningEffort = strings.TrimSpace(reasoningEffort)
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "moonshot", "ollama":
		return ""
	default:
		return reasoningEffort
	}
}

func writeProviderFailure(writer *session.Writer, sessionID, provider, phase string, turn int, modelName string, attempt int, retrying bool, delay time.Duration, err error, failure *modeladapter.ProviderError) error {
	event := session.Event{
		"type":       "provider_failure",
		"session_id": sessionID,
		"provider":   strings.TrimSpace(provider),
		"model":      strings.TrimSpace(modelName),
		"phase":      strings.TrimSpace(phase),
		"turn":       turn,
		"attempt":    attempt,
		"message":    err.Error(),
		"retrying":   retrying,
	}
	if retrying {
		event["retry_delay_ms"] = delay.Milliseconds()
	}
	if failure != nil {
		event["kind"] = string(failure.Kind)
		event["retryable"] = failure.Retryable
		if failure.StatusCode > 0 {
			event["status_code"] = failure.StatusCode
		}
		if failure.RetryAfter > 0 {
			event["retry_after_ms"] = failure.RetryAfter.Milliseconds()
		}
	} else {
		event["kind"] = "unknown"
		event["retryable"] = false
	}
	return writer.Write(event)
}

func providerRetryDelay(failure *modeladapter.ProviderError, attempt int) time.Duration {
	delay := time.Duration(250*attempt) * time.Millisecond
	if failure != nil && failure.RetryAfter > 0 {
		delay = failure.RetryAfter
	}
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	if delay <= 0 {
		return 250 * time.Millisecond
	}
	return delay
}

func openRouterFinalCompletionOptions(cfg modeladapter.OpenRouterConfig) modeladapter.CompletionOptions {
	return openRouterCompletionOptions(cfg)
}

func openRouterSynthesisFinalPrompt(guidance openRouterProgressGuidance, requireFinalResponseTool bool) string {
	reasonLine := "- Do not say the turn budget was reached."
	if strings.EqualFold(guidance.SynthesisReason, "stalled") {
		reasonLine = fmt.Sprintf("- Recent turns have not produced new tool evidence, file changes, or verification for %d turn%s. Finish honestly from the gathered evidence instead of continuing to churn.", guidance.NoProgressTurns, pluralSuffix(guidance.NoProgressTurns))
	}
	if requireFinalResponseTool {
		return fmt.Sprintf(`This is a planned synthesis checkpoint at turn %d of %d, before the hard cap.

Only final_response is available for this request. Call final_response now from the gathered evidence:
%s
- Answer the current user request directly in final_response.summary.
- Set final_response.outcome honestly: completed only when the requested work is actually complete and verified; partial, blocked, or failed when important work remains or evidence is insufficient.
- Distinguish confirmed gaps from unverified items.
- A feature is not missing merely because there is no same-named file; it may be implemented inline in CLI, script, model adapter, or orchestration code.
- Keep uncertainty where it is honest, but do not ask the user to continue unless a concrete blocker remains.
- Prefer a concise structured answer over exhaustive audit notes.`, guidance.Turn, guidance.MaxTurns, reasonLine)
	}
	return fmt.Sprintf(`This is a planned synthesis checkpoint at turn %d of %d, before the hard cap.

Tools are unavailable for this request. Produce the final user-facing answer now from the gathered evidence:
%s
- Answer the current user request directly.
- Distinguish confirmed gaps from unverified items.
- A feature is not missing merely because there is no same-named file; it may be implemented inline in CLI, script, model adapter, or orchestration code.
- Keep uncertainty where it is honest, but do not ask the user to continue unless a concrete blocker remains.
- Prefer a concise structured answer over exhaustive audit notes.`, guidance.Turn, guidance.MaxTurns, reasonLine)
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

func ensureToolCallIDs(calls []modeladapter.ToolCall, turn int) {
	for i := range calls {
		if strings.TrimSpace(calls[i].ID) == "" {
			calls[i].ID = fmt.Sprintf("call_lcagent_%d_%d", turn, i+1)
		}
		if strings.TrimSpace(calls[i].Type) == "" {
			calls[i].Type = "function"
		}
	}
}

func allToolCallsNamed(calls []modeladapter.ToolCall, name string) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if call.Function.Name != name {
			return false
		}
	}
	return true
}

func toolCallName(call modeladapter.ToolCall) string {
	return firstNonEmptyString(strings.TrimSpace(call.Function.Name), "unknown tool")
}

func toolCallNames(calls []modeladapter.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, toolCallName(call))
	}
	return out
}

func finalResponseOnlyTools(defs []modeladapter.ToolDefinition) []modeladapter.ToolDefinition {
	for _, def := range defs {
		if def.Function.Name == "final_response" {
			return []modeladapter.ToolDefinition{def}
		}
	}
	return nil
}

func finalResponseToolFeedbackMessage() string {
	return "Final response feedback: call the final_response tool with summary, outcome, files_changed, and verification instead of returning plain assistant text."
}

func invalidToolArgumentsResult(toolName string, err error) tools.ToolResult {
	return tools.ToolResult{
		Success: false,
		Error:   fmt.Sprintf("invalid %s arguments: %v", firstNonEmptyString(strings.TrimSpace(toolName), "tool"), err),
	}
}

func synthesisToolCallRejectedFeedbackMessage() string {
	return "Synthesis feedback: this planned synthesis checkpoint could not accept the attempted tool call. Return to the normal tool loop for one focused step: run the concrete tool if it is still necessary, otherwise call final_response."
}

func modelResponseEvent(sessionID string, provider string, phase string, turn int, completion modeladapter.Completion, toolCallCount int) session.Event {
	event := session.Event{
		"type":            "model_response",
		"session_id":      sessionID,
		"provider":        strings.TrimSpace(provider),
		"phase":           strings.TrimSpace(phase),
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

func scoutDelegationPrompt(userPrompt string) string {
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return ""
	}
	return fmt.Sprintf(`Scout task.

You are a delegated low-cost scout for another coding agent. Investigate only what is needed, do not modify files, and prefer targeted reads/searches over broad scans. If a code change seems necessary, describe the exact change instead of applying it.

Return a compact handoff with these sections:
- Findings
- Relevant files
- Suggested next steps
- Risks or unknowns

User request:
%s`, userPrompt)
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
	hostOS, hostArch := currentHostEnvironment()
	return modeladapter.SystemPromptOptions{
		ToolProfile:          string(profile),
		DefaultReadLineLimit: limits.DefaultReadLineLimit,
		MaxReadLineLimit:     limits.MaxReadLineLimit,
		HostOS:               hostOS,
		HostArch:             hostArch,
	}
}

func currentHostEnvironment() (string, string) {
	return hostOSDisplayName(runtime.GOOS), strings.TrimSpace(runtime.GOARCH)
}

func hostOSDisplayName(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "":
		return ""
	case "darwin":
		return "macOS (darwin)"
	case "linux":
		return "Linux (linux)"
	case "windows":
		return "Windows (windows)"
	default:
		return strings.TrimSpace(goos)
	}
}
