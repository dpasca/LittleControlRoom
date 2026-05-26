package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/lcagent/modeladapter"
	lcrmodel "lcroom/internal/model"
	"lcroom/internal/projectrun"
)

const (
	lcagentDefaultAuto            = "low"
	lcagentDefaultProvider        = "openrouter"
	lcagentDefaultToolProfile     = "balanced"
	lcagentDefaultContextProfile  = "balanced"
	lcagentDefaultRequestTimeout  = 10 * time.Minute
	lcagentDefaultUtilityProvider = "main"
	lcagentDefaultWebSearch       = "off"
)

type lcagentRunOptions struct {
	autoOverride   string
	disableResume  bool
	startingStatus string
	runningStatus  string
}

type lcagentSession struct {
	projectPath       string
	dataDir           string
	execPath          string
	envFile           string
	openAIAPIKey      string
	openRouterAPIKey  string
	deepSeekAPIKey    string
	moonshotAPIKey    string
	routePreset       string
	provider          string
	auto              string
	adminWrite        bool
	toolProfile       string
	contextProfile    string
	requestTimeout    time.Duration
	utilityProvider   string
	utilityModel      string
	webSearchBackend  string
	webSearchAPIKey   string
	webSearchEngineID string
	webSearchURL      string
	runtimeManager    *projectrun.Manager
	notify            func()

	mu                 sync.Mutex
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	cancel             context.CancelFunc
	threadID           string
	runID              string
	started            bool
	busy               bool
	closed             bool
	busySince          time.Time
	lastBusyActivityAt time.Time
	lastActivityAt     time.Time
	status             string
	lastError          string
	model              string
	modelProvider      string
	reasoningEffort    string
	tokenUsage         *threadTokenUsage
	pendingApproval    *ApprovalRequest
	replayLoaded       bool
	entries            []TranscriptEntry
	revision           uint64
	cache              transcriptExportCache
}

func newLCAgentSession(req LaunchRequest, notify func()) (Session, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	dataDir := strings.TrimSpace(req.AppDataDir)
	if dataDir == "" {
		dataDir = appfs.DefaultDataDir()
	}
	provider, err := lcagentProviderValue(req.LCAgentProvider)
	if err != nil {
		return nil, err
	}
	routePreset, err := lcagentRoutePresetValue(req.LCAgentRoutePreset)
	if err != nil {
		return nil, err
	}
	toolProfile, err := lcagentToolProfileValue(req.LCAgentToolProfile)
	if err != nil {
		return nil, err
	}
	contextProfile, err := lcagentContextProfileValue(req.LCAgentContextProfile)
	if err != nil {
		return nil, err
	}
	requestTimeout := lcagentRequestTimeoutValue(req.LCAgentRequestTimeout)
	model := strings.TrimSpace(req.PendingModel)
	if model == "" && routePreset == "" {
		model = lcagentDefaultModel(provider)
	}
	modelProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), provider)
	model = modeladapter.NormalizeModelForProvider(modelProvider, model)
	utilityProvider := lcagentResolvedUtilityProvider(routePreset, provider, req.LCAgentUtilityProvider)
	utilityModel := modeladapter.NormalizeModelForProvider(utilityProvider, lcagentResolvedUtilityModel(routePreset, model, req.LCAgentUtilityProvider, req.LCAgentUtilityModel))
	session := &lcagentSession{
		projectPath:       strings.TrimSpace(req.ProjectPath),
		dataDir:           dataDir,
		execPath:          strings.TrimSpace(req.LCAgentPath),
		envFile:           strings.TrimSpace(req.LCAgentEnvFile),
		openAIAPIKey:      strings.TrimSpace(req.LCAgentOpenAIAPIKey),
		openRouterAPIKey:  strings.TrimSpace(req.LCAgentOpenRouterAPIKey),
		deepSeekAPIKey:    strings.TrimSpace(req.LCAgentDeepSeekAPIKey),
		moonshotAPIKey:    strings.TrimSpace(req.LCAgentMoonshotAPIKey),
		routePreset:       routePreset,
		provider:          provider,
		auto:              strings.TrimSpace(req.LCAgentAuto),
		adminWrite:        req.LCAgentAdminWrite,
		toolProfile:       toolProfile,
		contextProfile:    contextProfile,
		requestTimeout:    requestTimeout,
		utilityProvider:   utilityProvider,
		utilityModel:      utilityModel,
		webSearchBackend:  lcagentWebSearchBackendValue(req.LCAgentWebSearchBackend),
		webSearchAPIKey:   strings.TrimSpace(req.LCAgentWebSearchAPIKey),
		webSearchEngineID: strings.TrimSpace(req.LCAgentWebSearchEngineID),
		webSearchURL:      strings.TrimSpace(req.LCAgentWebSearchURL),
		runtimeManager:    req.RuntimeManager,
		notify:            notify,
		model:             model,
		modelProvider:     modelProvider,
		reasoningEffort:   strings.TrimSpace(req.PendingReasoning),
		status:            "Ready",
	}
	if replay, err := loadLCAgentReplay(req); err != nil {
		return nil, err
	} else if replay != nil {
		session.applyReplay(replay)
	}
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		if err := session.Submit(prompt); err != nil {
			return nil, err
		}
	} else if !session.started {
		session.appendEntryLocked(TranscriptStatus, "Experimental LCAgent is ready. Send a prompt to start a one-shot run.")
		if warning := session.webSearchWarning(); warning != "" {
			session.appendEntryLocked(TranscriptStatus, warning)
		}
	}
	return session, nil
}

func (s *lcagentSession) ProjectPath() string {
	if s == nil {
		return ""
	}
	return s.projectPath
}

func (s *lcagentSession) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *lcagentSession) TrySnapshot() (Snapshot, bool) {
	if s == nil || !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	return s.snapshotLocked(), true
}

func (s *lcagentSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *lcagentSession) SubmitInput(input Submission) error {
	if input.Empty() {
		return nil
	}
	return s.startRun(input.TranscriptText(), input.TranscriptDisplayText())
}

func (s *lcagentSession) ShowStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := strings.TrimSpace(s.status)
	if status == "" {
		status = "LCAgent status unavailable"
	}
	s.appendEntryLocked(TranscriptStatus, status)
	return nil
}

func (s *lcagentSession) ShowGoal() error {
	return s.unsupportedNotice("LCAgent goal state is not wired yet.")
}

func (s *lcagentSession) SetGoal(string, *int64) error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) PauseGoal() error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) ResumeGoal() error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) ClearGoal() error {
	return s.unsupportedNotice("LCAgent goals are not wired yet.")
}

func (s *lcagentSession) Compact() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent session is closed")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("cannot compact while LCAgent is running")
	}
	sessionID := strings.TrimSpace(s.runID)
	dataDir := s.dataDir
	projectPath := s.projectPath
	s.mu.Unlock()

	trace, err := LoadLCAgentTrace(dataDir, sessionID, projectPath)
	if err != nil {
		if sessionID == "" && strings.Contains(err.Error(), "artifact not found") {
			return s.unsupportedNotice("No LCAgent session to compact yet.")
		}
		return err
	}
	path, summary, err := writeLCAgentCompactSummary(dataDir, trace)
	if err != nil {
		return err
	}
	notice := "LCAgent compact summary refreshed"
	if path != "" {
		notice += ": " + path
	}
	s.mu.Lock()
	if trace.SessionID != "" {
		s.runID = trace.SessionID
	}
	s.status = notice
	s.appendEntryLocked(TranscriptStatus, notice)
	s.appendEntryLocked(TranscriptStatus, summary)
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) Review() error {
	prompt, ok, err := lcagentReviewPrompt(s.projectPath)
	if err != nil {
		return err
	}
	if !ok {
		return s.unsupportedNotice("No uncommitted changes to review.")
	}
	return s.startRunWithOptions(prompt, "/review current uncommitted changes", lcagentRunOptions{
		autoOverride:   "off",
		disableResume:  true,
		startingStatus: "Starting LCAgent review",
		runningStatus:  "LCAgent reviewing uncommitted changes",
	})
}

func (s *lcagentSession) ListModels() ([]ModelOption, error) {
	s.mu.Lock()
	cfg := LCAgentModelListConfig{
		Provider:         s.provider,
		Model:            s.model,
		EnvFile:          s.envFile,
		OpenAIAPIKey:     s.openAIAPIKey,
		OpenRouterAPIKey: s.openRouterAPIKey,
		DeepSeekAPIKey:   s.deepSeekAPIKey,
		MoonshotAPIKey:   s.moonshotAPIKey,
		RequestTimeout:   s.requestTimeout,
	}
	s.mu.Unlock()
	models, err := LCAgentModelOptions(context.Background(), cfg)
	if len(models) > 0 {
		return models, nil
	}
	return nil, err
}

type LCAgentModelListConfig struct {
	Provider         string
	Model            string
	EnvFile          string
	OpenAIAPIKey     string
	OpenRouterAPIKey string
	DeepSeekAPIKey   string
	MoonshotAPIKey   string
	RequestTimeout   time.Duration
}

func LCAgentModelOptions(ctx context.Context, cfg LCAgentModelListConfig) ([]ModelOption, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = lcagentDefaultProvider
	}
	cfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
	curated := lcagentModelOptionsForProvider(provider)
	live, err := lcagentProviderModelOptions(ctx, cfg, provider)
	if err != nil {
		return lcagentModelOptionsWithCurrent(curated, cfg.Model, false), err
	}
	merged := mergeLCAgentModelOptions(curated, live)
	return lcagentModelOptionsWithCurrent(merged, cfg.Model, true), nil
}

func lcagentProviderModelOptions(ctx context.Context, cfg LCAgentModelListConfig, provider string) ([]ModelOption, error) {
	client, err := lcagentModelListClient(provider, cfg)
	if err != nil {
		return nil, err
	}
	listed, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	defaultModel := lcagentDefaultModel(provider)
	options := make([]ModelOption, 0, len(listed))
	for _, item := range listed {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		displayName := strings.TrimSpace(item.Name)
		if displayName == "" {
			displayName = id
		}
		description := strings.TrimSpace(item.Description)
		if description == "" {
			description = "Returned by the " + lcagentProviderDisplayName(provider) + " model list."
		}
		options = append(options, ModelOption{
			ID:                        id,
			Model:                     id,
			DisplayName:               displayName,
			Description:               description,
			SupportedReasoningEfforts: lcagentReasoningEffortOptions(),
			DefaultReasoningEffort:    lcagentDefaultReasoningEffort(provider, id),
			IsDefault:                 strings.EqualFold(id, defaultModel),
		})
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("%s model list returned no usable models", lcagentProviderDisplayName(provider))
	}
	return options, nil
}

func lcagentModelListClient(provider string, cfg LCAgentModelListConfig) (*modeladapter.Client, error) {
	adapterCfg := modeladapter.OpenRouterConfig{
		APIKey:         lcagentModelListAPIKey(provider, cfg),
		Model:          firstNonEmpty(strings.TrimSpace(cfg.Model), lcagentDefaultModel(provider)),
		EnvFile:        strings.TrimSpace(cfg.EnvFile),
		RequestTimeout: lcagentModelListTimeout(cfg.RequestTimeout),
	}
	switch provider {
	case "openai":
		return modeladapter.NewOpenAIClient(adapterCfg)
	case "deepseek":
		return modeladapter.NewDeepSeekClient(adapterCfg)
	case "moonshot":
		return modeladapter.NewMoonshotClient(adapterCfg)
	default:
		return modeladapter.NewOpenRouterClient(adapterCfg)
	}
}

func lcagentModelListAPIKey(provider string, cfg LCAgentModelListConfig) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return strings.TrimSpace(cfg.OpenAIAPIKey)
	case "deepseek":
		return strings.TrimSpace(cfg.DeepSeekAPIKey)
	case "moonshot":
		return strings.TrimSpace(cfg.MoonshotAPIKey)
	default:
		return strings.TrimSpace(cfg.OpenRouterAPIKey)
	}
}

func lcagentModelListTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 15 * time.Second
	}
	if timeout > 30*time.Second {
		return 30 * time.Second
	}
	return timeout
}

func lcagentModelOptionsForProvider(provider string) []ModelOption {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = lcagentDefaultProvider
	}
	defaultModel := lcagentDefaultModel(provider)
	reasoning := lcagentReasoningEffortOptions()
	option := func(model, displayName, description, defaultReasoning string, isDefault bool) ModelOption {
		return ModelOption{
			ID:                        model,
			Model:                     model,
			DisplayName:               displayName,
			Description:               description,
			SupportedReasoningEfforts: reasoning,
			DefaultReasoningEffort:    defaultReasoning,
			IsDefault:                 isDefault,
		}
	}
	switch provider {
	case "openrouter":
		return []ModelOption{
			option(modeladapter.DefaultOpenRouterModel, "Balanced: DeepSeek V4 Pro", "Recommended balanced OpenRouter coding route.", "", defaultModel == modeladapter.DefaultOpenRouterModel),
			option("openai/gpt-5.5", "Quality: GPT-5.5", "Higher-quality OpenRouter coding route.", "low", defaultModel == "openai/gpt-5.5"),
			option("deepseek/deepseek-v4-flash", "Cheap Scout: DeepSeek V4 Flash", "Lower-cost route for bounded read-first exploration.", "", defaultModel == "deepseek/deepseek-v4-flash"),
		}
	case "deepseek":
		return []ModelOption{
			option(modeladapter.DefaultDeepSeekModel, "Balanced: DeepSeek V4 Pro", "Direct DeepSeek coding route.", "", defaultModel == modeladapter.DefaultDeepSeekModel),
			option("deepseek-v4-flash", "Cheap Scout: DeepSeek V4 Flash", "Lower-cost direct DeepSeek exploration route.", "", defaultModel == "deepseek-v4-flash"),
		}
	case "moonshot":
		return []ModelOption{
			option(modeladapter.DefaultMoonshotModel, "Balanced: Kimi K2.6", "Direct Moonshot/Kimi coding route.", "", true),
		}
	case "openai":
		return []ModelOption{
			option(modeladapter.DefaultOpenAIModel, "Quality: GPT-5.5", "Direct OpenAI coding route.", "low", true),
		}
	default:
		return []ModelOption{
			option(defaultModel, defaultModel, "Experimental LCAgent tool-calling model.", "", true),
		}
	}
}

func lcagentReasoningEffortOptions() []ReasoningEffortOption {
	return []ReasoningEffortOption{
		{ReasoningEffort: "low", Description: "Light reasoning for coding turns."},
		{ReasoningEffort: "medium", Description: "More reasoning for harder coding turns."},
		{ReasoningEffort: "high", Description: "Deeper reasoning for difficult reviews or refactors."},
	}
}

func lcagentDefaultReasoningEffort(provider, model string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "openai") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "openai/") {
		return "low"
	}
	return ""
}

func mergeLCAgentModelOptions(curated, discovered []ModelOption) []ModelOption {
	merged := append([]ModelOption(nil), curated...)
	index := map[string]int{}
	for i, option := range merged {
		key := strings.ToLower(strings.TrimSpace(option.Model))
		if key != "" {
			index[key] = i
		}
	}
	for _, option := range discovered {
		key := strings.ToLower(strings.TrimSpace(option.Model))
		if key == "" {
			continue
		}
		if existingIndex, ok := index[key]; ok {
			existing := merged[existingIndex]
			if strings.TrimSpace(existing.Description) == "" {
				existing.Description = "Verified by provider model list."
			} else if !strings.Contains(strings.ToLower(existing.Description), "verified") {
				existing.Description = strings.TrimSpace(existing.Description) + " Verified by provider model list."
			}
			if strings.TrimSpace(existing.DisplayName) == strings.TrimSpace(existing.Model) && strings.TrimSpace(option.DisplayName) != "" {
				existing.DisplayName = strings.TrimSpace(option.DisplayName)
			}
			existing.IsDefault = existing.IsDefault || option.IsDefault
			merged[existingIndex] = existing
			continue
		}
		merged = append(merged, option)
		index[key] = len(merged) - 1
	}
	return merged
}

func lcagentModelOptionsWithCurrent(models []ModelOption, current string, checkedProviderList bool) []ModelOption {
	current = strings.TrimSpace(current)
	if current == "" || lcagentModelOptionExists(models, current) {
		return models
	}
	description := "Custom LCAgent model."
	if checkedProviderList {
		description = "Custom LCAgent model. The provider model list did not return this ID."
	}
	return append([]ModelOption{{
		ID:                        current,
		Model:                     current,
		DisplayName:               current,
		Description:               description,
		SupportedReasoningEfforts: lcagentReasoningEffortOptions(),
		DefaultReasoningEffort:    lcagentDefaultReasoningEffort("", current),
	}}, models...)
}

func lcagentProviderDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "OpenAI"
	case "deepseek":
		return "DeepSeek"
	case "moonshot":
		return "Moonshot"
	default:
		return "OpenRouter"
	}
}

func lcagentModelOptionExists(models []ModelOption, id string) bool {
	for _, option := range models {
		if strings.EqualFold(strings.TrimSpace(option.ID), strings.TrimSpace(id)) ||
			strings.EqualFold(strings.TrimSpace(option.Model), strings.TrimSpace(id)) {
			return true
		}
	}
	return false
}

func (s *lcagentSession) StageModelOverride(model, reasoningEffort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(model) != "" {
		s.model = strings.TrimSpace(model)
	}
	s.reasoningEffort = strings.TrimSpace(reasoningEffort)
	return nil
}

func (s *lcagentSession) Interrupt() error {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *lcagentSession) RespondApproval(decision ApprovalDecision) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent session is closed")
	}
	request := cloneApprovalRequest(s.pendingApproval)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending approval")
	}
	if !request.AllowsDecision(decision) {
		s.mu.Unlock()
		return fmt.Errorf("approval decision %q is not supported for %s approvals", decision, request.Kind)
	}
	stdin := s.stdin
	if stdin == nil {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent approval channel is not available")
	}
	s.status = "Sending LCAgent approval decision..."
	s.touchLocked()
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}

	payload, err := json.Marshal(map[string]string{
		"type":     "approval_response",
		"id":       request.ID,
		"decision": string(decision),
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdin, string(payload)); err != nil {
		s.appendAsync(TranscriptError, "LCAgent approval response failed: "+err.Error())
		return err
	}
	s.mu.Lock()
	s.status = "LCAgent approval decision sent"
	action := "LCAgent approval decision sent"
	if decision == DecisionAcceptForSession {
		action = "LCAgent permission level changed to Medium for this run"
	}
	if request.Command != "" {
		s.appendEntryLocked(TranscriptStatus, action+": "+request.Command)
	} else {
		s.appendEntryLocked(TranscriptStatus, action)
	}
	s.touchLocked()
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) RespondToolInput(map[string][]string) error {
	return fmt.Errorf("LCAgent structured input is not supported yet")
}

func (s *lcagentSession) RespondElicitation(ElicitationDecision, json.RawMessage) error {
	return fmt.Errorf("LCAgent elicitation is not supported yet")
}

func (s *lcagentSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.status = "Closed"
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) startRun(prompt, displayPrompt string) error {
	return s.startRunWithOptions(prompt, displayPrompt, lcagentRunOptions{})
}

func (s *lcagentSession) startRunWithOptions(prompt, displayPrompt string, opts lcagentRunOptions) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent session is closed")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent is already running")
	}
	now := time.Now()
	s.started = true
	s.busy = true
	s.busySince = now
	s.lastBusyActivityAt = now
	s.lastActivityAt = now
	s.status = firstNonEmpty(opts.startingStatus, "Starting LCAgent")
	s.lastError = ""
	resumeID := strings.TrimSpace(s.threadID)
	if opts.disableResume {
		resumeID = ""
	}
	if resumeID != "" {
		if s.replayLoaded && len(s.entries) > 0 {
			s.appendEntryLocked(TranscriptStatus, "Starting a continuing LCAgent run from thread "+resumeID+".")
		} else {
			s.appendEntryLocked(TranscriptStatus, "Continuing LCAgent thread "+resumeID+".")
		}
		s.replayLoaded = false
	}
	s.appendEntryLocked(TranscriptUser, firstNonEmpty(displayPrompt, prompt))
	provider := firstNonEmpty(s.provider, lcagentDefaultProvider)
	routePreset := strings.TrimSpace(s.routePreset)
	model := strings.TrimSpace(s.model)
	if model == "" && routePreset == "" {
		model = lcagentDefaultModel(provider)
	}
	modelProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), provider)
	model = modeladapter.NormalizeModelForProvider(modelProvider, model)
	toolProfile := firstNonEmpty(s.toolProfile, lcagentDefaultToolProfile)
	contextProfile := firstNonEmpty(s.contextProfile, lcagentDefaultContextProfile)
	adminWrite := s.adminWrite
	requestTimeout := s.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = lcagentDefaultRequestTimeout
	}
	reasoningEffort := strings.TrimSpace(s.reasoningEffort)
	credentialProvider := modelProvider
	providerAPIKeyName, providerAPIKey := s.providerCredentialLocked(credentialProvider)
	webSearchBackend := firstNonEmpty(s.webSearchBackend, lcagentDefaultWebSearch)
	webSearchAPIKey := strings.TrimSpace(s.webSearchAPIKey)
	webSearchEngineID := strings.TrimSpace(s.webSearchEngineID)
	webSearchURL := strings.TrimSpace(s.webSearchURL)
	utilityProvider := firstNonEmpty(s.utilityProvider, lcagentDefaultUtilityProvider)
	utilityModel := modeladapter.NormalizeModelForProvider(utilityProvider, s.utilityModel)
	utilityAPIKeyName, utilityAPIKey := s.providerCredentialLocked(utilityProvider)
	if warning := s.webSearchWarningLocked(); warning != "" {
		s.appendEntryLocked(TranscriptStatus, warning)
	}
	s.mu.Unlock()

	spec, err := lcagentCommandSpec(s.execPath)
	if err != nil {
		s.finishRun("", false, err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	args := append([]string{}, spec.Args...)
	args = append(args,
		"exec",
		"--cwd", s.projectPath,
		"--data-dir", s.dataDir,
		"--output", "stream-json",
		"--approval-mode", "ask",
		"--utility-provider", utilityProvider,
		"--web-search-backend", webSearchBackend,
	)
	if utilityModel != "" {
		args = append(args, "--utility-model", utilityModel)
	}
	if adminWrite {
		args = append(args, "--admin-write")
	}
	if routePreset != "" && opts.autoOverride == "" {
		args = append(args,
			"--route-preset", routePreset,
			"--request-timeout", requestTimeout.String(),
		)
	} else {
		args = append(args,
			"--auto", lcagentAutoLevel(firstNonEmpty(opts.autoOverride, s.auto)),
			"--provider", provider,
		)
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args,
			"--tool-profile", toolProfile,
			"--context-profile", contextProfile,
			"--request-timeout", requestTimeout.String(),
		)
		if reasoningEffort != "" {
			args = append(args, "--reasoning-effort", reasoningEffort)
		}
	}
	if webSearchEngineID != "" {
		args = append(args, "--web-search-engine-id", webSearchEngineID)
	}
	if webSearchURL != "" {
		args = append(args, "--web-search-url", webSearchURL)
	}
	envFile := firstNonEmpty(s.envFile, os.Getenv("LCROOM_LCAGENT_ENV_FILE"))
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	if resumeID != "" {
		args = append(args, "--continue-from", resumeID)
	}
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, spec.Command, args...)
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	if providerAPIKeyName != "" && providerAPIKey != "" {
		cmd.Env = setCommandEnv(cmd.Env, providerAPIKeyName, providerAPIKey)
	}
	if utilityAPIKeyName != "" && utilityAPIKey != "" {
		cmd.Env = setCommandEnv(cmd.Env, utilityAPIKeyName, utilityAPIKey)
	}
	if webSearchAPIKey != "" {
		switch webSearchBackend {
		case "exa":
			cmd.Env = setCommandEnv(cmd.Env, "EXA_API_KEY", webSearchAPIKey)
		default:
			cmd.Env = setCommandEnv(cmd.Env, "GOOGLE_SEARCH_API_KEY", webSearchAPIKey)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		s.finishRun("", false, err)
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		s.finishRun("", false, err)
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.cancel = cancel
	s.status = firstNonEmpty(opts.runningStatus, "LCAgent running")
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}

	stdoutDone := make(chan error, 1)
	stderrDone := make(chan error, 1)
	go func() {
		stdoutDone <- s.readStream(stdout)
	}()
	go func() {
		stderrDone <- s.readStderr(stderr)
	}()
	go func() {
		stdoutErr := <-stdoutDone
		stderrErr := <-stderrDone
		err := cmd.Wait()
		cancel()
		if stdoutErr != nil && !lcagentIgnorableStreamError(stdoutErr) {
			s.appendAsync(TranscriptError, "LCAgent stream error: "+stdoutErr.Error())
		}
		if stderrErr != nil && !lcagentIgnorableStreamError(stderrErr) {
			s.appendAsync(TranscriptError, "LCAgent stderr stream error: "+stderrErr.Error())
		}
		if ctx.Err() == context.Canceled && err != nil {
			err = errors.New("LCAgent run interrupted")
		}
		processState := ""
		if cmd.ProcessState != nil {
			processState = cmd.ProcessState.String()
		}
		s.finishRun(processState, err == nil, err)
	}()
	return nil
}

func (s *lcagentSession) providerCredentialLocked(provider string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "OPENAI_API_KEY", strings.TrimSpace(s.openAIAPIKey)
	case "", "openrouter":
		return "OPENROUTER_API_KEY", strings.TrimSpace(s.openRouterAPIKey)
	case "deepseek":
		return "DEEPSEEK_API_KEY", strings.TrimSpace(s.deepSeekAPIKey)
	case "moonshot":
		return "MOONSHOT_API_KEY", strings.TrimSpace(s.moonshotAPIKey)
	default:
		return "", ""
	}
}

func setCommandEnv(env []string, key, value string) []string {
	key = strings.TrimSpace(key)
	if key == "" {
		return env
	}
	prefix := key + "="
	out := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func (s *lcagentSession) readStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		s.handleEvent(scanner.Bytes())
	}
	return scanner.Err()
}

func (s *lcagentSession) readStderr(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			s.appendAsync(TranscriptError, text)
		}
	}
	return scanner.Err()
}

func lcagentIgnorableStreamError(err error) bool {
	return err == nil || errors.Is(err, os.ErrClosed)
}

func (s *lcagentSession) handleEvent(line []byte) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(line, &event); err != nil {
		s.appendAsync(TranscriptOther, string(line))
		return
	}
	eventType := rawJSONString(event["type"])
	switch eventType {
	case "session_meta":
		id := rawJSONString(event["id"])
		threadID := firstNonEmpty(rawJSONString(event["thread_id"]), id)
		s.mu.Lock()
		if id != "" {
			s.runID = id
		}
		if threadID != "" {
			s.threadID = threadID
		}
		s.status = "LCAgent thread " + firstNonEmpty(threadID, id, "started")
		s.touchLocked()
		s.mu.Unlock()
	case "model_response":
		modelName := rawJSONString(event["model"])
		usage, ok := lcagentUsageFromModelResponseEvent(event, modelName)
		s.mu.Lock()
		if modelName != "" {
			s.model = modelName
		}
		if ok {
			s.addTokenUsageLocked(usage)
		}
		s.touchLocked()
		s.mu.Unlock()
	case "tool_call":
		tool := rawJSONString(event["tool"])
		s.appendAsync(TranscriptTool, lcagentToolCallText(tool, event["args"]))
	case "tool_result":
		tool := rawJSONString(event["tool"])
		text := lcagentToolResultText(tool, event["result"])
		s.appendAsync(TranscriptTool, text)
	case "permission_denied":
		reason := rawJSONString(event["reason"])
		tool := rawJSONString(event["tool"])
		if tool != "" {
			reason = firstNonEmpty(reason, tool+" denied")
		}
		if reason == "" {
			reason = "LCAgent permission denied"
		}
		s.appendAsync(TranscriptError, "Permission denied: "+reason)
	case "approval_request":
		request := lcagentApprovalRequestFromEvent(event, "")
		if request != nil {
			s.mu.Lock()
			s.pendingApproval = request
			s.status = "Waiting for command approval"
			s.touchLocked()
			s.mu.Unlock()
			s.appendAsync(TranscriptStatus, "LCAgent requested command approval: "+request.Summary())
		}
	case "approval_resolved":
		id := rawJSONString(event["id"])
		status := lcagentApprovalResolvedStatus(event)
		s.mu.Lock()
		if s.pendingApproval != nil && (id == "" || id == s.pendingApproval.ID) {
			s.pendingApproval = nil
		}
		if status != "" {
			s.status = status
		}
		s.touchLocked()
		s.mu.Unlock()
		if text := lcagentApprovalResolvedText(event); text != "" {
			s.appendAsync(TranscriptStatus, text)
		}
	case "permission_level_changed":
		if text := lcagentPermissionLevelChangedText(event); text != "" {
			s.mu.Lock()
			s.status = text
			s.touchLocked()
			s.mu.Unlock()
			s.appendAsync(TranscriptStatus, text)
		}
	case "process_request":
		s.handleLCAgentProcessRequest(event)
	case "patch_diff_summary":
		if summary := rawJSONString(event["summary"]); summary != "" {
			s.appendAsync(TranscriptFileChange, summary)
		}
	case "patch_feedback":
		s.appendAsync(TranscriptError, lcagentPatchFeedbackText(event))
	case "verification_check":
		s.appendAsync(TranscriptStatus, lcagentVerificationCheckText(event))
	case "verification_feedback":
		s.appendAsync(TranscriptStatus, lcagentVerificationFeedbackText(event))
	case "repair_feedback_suppressed":
		s.appendAsync(TranscriptStatus, lcagentRepairFeedbackSuppressedText(event))
	case "repair_guidance":
		s.appendAsync(TranscriptStatus, lcagentRepairGuidanceText(event))
	case "provider_failure":
		s.appendAsync(TranscriptError, lcagentProviderFailureText(event))
	case "provider_retry":
		s.appendAsync(TranscriptStatus, lcagentProviderRetryText(event))
	case "provider_retry_succeeded":
		s.appendAsync(TranscriptStatus, lcagentProviderRetrySucceededText(event))
	case "verification_summary":
		status := rawJSONString(event["status"])
		message := rawJSONString(event["message"])
		if message == "" && status != "" {
			message = "Verification status: " + status
		}
		if message != "" {
			s.appendAsync(TranscriptStatus, message)
		}
	case "continuation":
		s.appendAsync(TranscriptStatus, lcagentContinuationText(event))
	case "resume_context":
		s.appendAsync(TranscriptStatus, lcagentResumeContextText(event))
	case "context_compacted":
		s.appendAsync(TranscriptStatus, lcagentContextCompactedText(event))
	case "web_search_profile":
		enabled := rawJSONBool(event["enabled"])
		message := rawJSONString(event["message"])
		backend := rawJSONString(event["backend"])
		if !enabled {
			s.appendAsync(TranscriptStatus, firstNonEmpty(message, "LCAgent web search is not available. Use /settings here to configure a web search backend and API key."))
		} else if backend != "" {
			s.appendAsync(TranscriptStatus, "LCAgent web search enabled: "+backend)
		}
	case "search_refine_profile":
		enabled := rawJSONBool(event["enabled"])
		message := rawJSONString(event["message"])
		provider := rawJSONString(event["provider"])
		model := rawJSONString(event["model"])
		if enabled {
			label := strings.TrimSpace(strings.Join([]string{provider, model}, " "))
			s.appendAsync(TranscriptStatus, "LCAgent oversized search refinement enabled: "+strings.TrimSpace(label))
		} else if strings.TrimSpace(message) != "" && provider != "off" {
			s.appendAsync(TranscriptStatus, message)
		}
	case "search_refine_result":
		if rawJSONBool(event["success"]) {
			model := rawJSONString(event["model"])
			if model != "" {
				s.appendAsync(TranscriptStatus, "LCAgent condensed an oversized search with "+model)
			}
		} else if message := rawJSONString(event["message"]); message != "" {
			s.appendAsync(TranscriptStatus, "LCAgent search refinement skipped: "+message)
		}
	case "plan_update":
		s.appendAsync(TranscriptPlan, lcagentPlanText(event["items"]))
	case "assistant_message":
		if text := rawJSONString(event["message"]); text != "" {
			s.appendAsync(TranscriptAgent, text)
		}
	case "files_touched":
		s.appendAsync(TranscriptFileChange, lcagentFilesTouchedText(event["files"]))
	case "turn_complete":
		if text := lcagentTurnCompleteTraceText(event); text != "" {
			s.appendAsync(TranscriptStatus, text)
		}
		s.mu.Lock()
		s.status = "LCAgent run complete"
		s.touchLocked()
		s.mu.Unlock()
	case "turn_aborted":
		reason := rawJSONString(event["reason"])
		if reason == "" {
			reason = "LCAgent run aborted"
		}
		s.mu.Lock()
		s.lastError = reason
		s.status = s.lastError
		s.touchLocked()
		s.mu.Unlock()
		s.appendAsync(TranscriptError, reason)
	}
	if s.notify != nil {
		s.notify()
	}
}

func lcagentApprovalRequestFromEvent(event map[string]json.RawMessage, fallbackThreadID string) *ApprovalRequest {
	id := rawJSONString(event["id"])
	if id == "" {
		return nil
	}
	return &ApprovalRequest{
		ID:       id,
		Kind:     ApprovalCommandExecution,
		ThreadID: firstNonEmpty(rawJSONString(event["session_id"]), fallbackThreadID),
		Command:  rawJSONString(event["command"]),
		CWD:      rawJSONString(event["cwd"]),
		Reason:   rawJSONString(event["reason"]),
		Scope:    rawJSONString(event["scope"]),
	}
}

func lcagentApprovalResolvedStatus(event map[string]json.RawMessage) string {
	switch rawJSONString(event["decision"]) {
	case string(DecisionAcceptForSession):
		return "LCAgent permission level is Medium for this run"
	case string(DecisionAccept):
		return "LCAgent command approval accepted"
	case string(DecisionDecline):
		return "LCAgent command approval declined"
	case string(DecisionCancel):
		return "LCAgent command approval canceled"
	default:
		switch rawJSONString(event["status"]) {
		case "approved":
			return "LCAgent command approval accepted"
		case "declined":
			return "LCAgent command approval declined"
		case "canceled":
			return "LCAgent command approval canceled"
		default:
			return ""
		}
	}
}

func lcagentPermissionLevelChangedText(event map[string]json.RawMessage) string {
	from := strings.ToLower(strings.TrimSpace(rawJSONString(event["from"])))
	to := strings.ToLower(strings.TrimSpace(rawJSONString(event["to"])))
	if to == "" {
		return ""
	}
	if from != "" && from != to {
		return "LCAgent permission level changed from " + lcagentPermissionLevelLabel(from) + " to " + lcagentPermissionLevelLabel(to) + " for this run"
	}
	return "LCAgent permission level is " + lcagentPermissionLevelLabel(to) + " for this run"
}

func lcagentPermissionLevelLabel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return "Off"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	default:
		return strings.TrimSpace(level)
	}
}

func lcagentApprovalResolvedText(event map[string]json.RawMessage) string {
	status := lcagentApprovalResolvedStatus(event)
	if status == "" {
		return ""
	}
	if command := rawJSONString(event["command"]); command != "" {
		return status + ": " + command
	}
	return status
}

func (s *lcagentSession) handleLCAgentProcessRequest(event map[string]json.RawMessage) {
	request := lcagentManagedProcessRequest{
		ID:        rawJSONString(event["id"]),
		Action:    strings.TrimSpace(rawJSONString(event["action"])),
		ProcessID: strings.TrimSpace(rawJSONString(event["process_id"])),
		Name:      strings.TrimSpace(rawJSONString(event["name"])),
		Command:   strings.TrimSpace(rawJSONString(event["command"])),
		CWD:       strings.TrimSpace(rawJSONString(event["cwd"])),
	}
	if request.ID == "" {
		return
	}
	s.mu.Lock()
	bridge := lcagentProcessBridge{
		manager:     s.runtimeManager,
		projectPath: s.projectPath,
		stdin:       s.stdin,
		appendAsync: s.appendAsync,
	}
	s.status = lcagentProcessRequestStatus(request.Action)
	if text := lcagentProcessRequestText(request.Action, request.Command, request.CWD); text != "" {
		s.appendEntryLocked(TranscriptStatus, text)
	}
	s.touchLocked()
	s.mu.Unlock()

	bridge.handle(request)
}

func (s *lcagentSession) finishRun(processState string, ok bool, err error) {
	s.mu.Lock()
	runID := strings.TrimSpace(s.runID)
	dataDir := s.dataDir
	projectPath := s.projectPath
	stdin := s.stdin
	s.busy = false
	s.cancel = nil
	s.cmd = nil
	s.stdin = nil
	s.pendingApproval = nil
	s.lastBusyActivityAt = time.Now()
	s.lastActivityAt = s.lastBusyActivityAt
	if err != nil {
		s.lastError = err.Error()
		s.status = "LCAgent failed: " + err.Error()
		s.appendEntryLocked(TranscriptError, s.status)
	} else if ok {
		s.status = "LCAgent run complete"
	} else {
		s.status = firstNonEmpty(processState, "LCAgent stopped")
	}
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if err == nil && ok {
		if trace, traceErr := LoadLCAgentTrace(dataDir, runID, projectPath); traceErr == nil {
			if quality := trace.TraceQualitySummaryLabel(); quality != "" {
				s.appendAsync(TranscriptStatus, "LCAgent "+quality)
			}
		}
	}
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) unsupportedNotice(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEntryLocked(TranscriptStatus, text)
	return nil
}

func (s *lcagentSession) appendAsync(kind TranscriptKind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	s.appendEntryLocked(kind, text)
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) appendEntryLocked(kind TranscriptKind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.entries = append(s.entries, TranscriptEntry{
		ItemID: fmt.Sprintf("lcagent-%d", len(s.entries)+1),
		Kind:   kind,
		Text:   text,
	})
	s.touchLocked()
}

func (s *lcagentSession) applyReplay(replay *lcagentReplay) {
	if s == nil || replay == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = firstNonEmpty(strings.TrimSpace(replay.threadID), strings.TrimSpace(replay.sessionID))
	s.runID = strings.TrimSpace(replay.sessionID)
	s.started = true
	s.busy = false
	s.closed = false
	s.busySince = time.Time{}
	s.lastBusyActivityAt = time.Time{}
	s.lastActivityAt = replay.lastActivityAt
	if s.lastActivityAt.IsZero() {
		s.lastActivityAt = replay.startedAt
	}
	if s.lastActivityAt.IsZero() {
		s.lastActivityAt = time.Now()
	}
	s.lastError = strings.TrimSpace(replay.lastError)
	if model := strings.TrimSpace(replay.model); model != "" {
		s.model = model
	}
	if provider := strings.TrimSpace(replay.modelProvider); provider != "" {
		s.modelProvider = provider
	}
	s.tokenUsage = cloneThreadTokenUsage(replay.tokenUsage)
	label := firstNonEmpty(s.threadID, "history")
	s.status = "Loaded LCAgent thread " + label + " from disk"
	s.replayLoaded = true
	s.entries = nil
	s.revision = 0
	s.cache.ready = false
	replayActivityAt := s.lastActivityAt
	s.appendEntryLocked(TranscriptStatus, "Loaded LCAgent thread "+label+" from disk. Sending a prompt starts a continuing run from canonical thread state.")
	for _, entry := range replay.entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		s.entries = append(s.entries, TranscriptEntry{
			ItemID:      firstNonEmpty(strings.TrimSpace(entry.ItemID), fmt.Sprintf("lcagent-replay-%d", len(s.entries)+1)),
			Kind:        entry.Kind,
			Text:        text,
			DisplayText: strings.TrimSpace(entry.DisplayText),
		})
	}
	s.lastActivityAt = replayActivityAt
	s.cache.invalidate(&s.revision)
}

func (s *lcagentSession) touchLocked() {
	s.lastActivityAt = time.Now()
	s.revision++
	s.cache.invalidate(nil)
}

func (s *lcagentSession) addTokenUsageLocked(usage lcrmodel.LLMUsage) {
	if !lcagentUsageTracked(usage) {
		return
	}
	breakdown := lcagentTokenUsageBreakdown(usage)
	if s.tokenUsage == nil {
		s.tokenUsage = &threadTokenUsage{}
	}
	s.tokenUsage.Last = breakdown
	s.tokenUsage.Total.CachedInputTokens += breakdown.CachedInputTokens
	s.tokenUsage.Total.InputTokens += breakdown.InputTokens
	s.tokenUsage.Total.OutputTokens += breakdown.OutputTokens
	s.tokenUsage.Total.ReasoningOutputTokens += breakdown.ReasoningOutputTokens
	s.tokenUsage.Total.TotalTokens += breakdown.TotalTokens
}

func lcagentUsageFromModelResponseEvent(event map[string]json.RawMessage, modelName string) (lcrmodel.LLMUsage, bool) {
	var usage lcrmodel.LLMUsage
	if raw := event["usage_summary"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &usage)
	}
	if !lcagentUsageTracked(usage) {
		usage = modeladapter.UsageFromRaw(event["usage"], modelName)
	}
	return usage, lcagentUsageTracked(usage)
}

func lcagentTokenUsageBreakdown(usage lcrmodel.LLMUsage) tokenUsageBreakdown {
	total := usage.TotalTokens
	if total == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		total = usage.InputTokens + usage.OutputTokens
	}
	return tokenUsageBreakdown{
		CachedInputTokens:     usage.CachedInputTokens,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningTokens,
		TotalTokens:           total,
	}
}

func lcagentUsageTracked(usage lcrmodel.LLMUsage) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.CachedInputTokens != 0 ||
		usage.ReasoningTokens != 0 ||
		usage.EstimatedCostUSD != 0
}

func lcagentProviderValue(configured string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_PROVIDER"))))
	if value == "" {
		return lcagentDefaultProvider, nil
	}
	switch value {
	case "openrouter", "openai", "deepseek", "moonshot":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent provider must be one of: openrouter, openai, deepseek, moonshot")
	}
}

func lcagentRoutePresetValue(configured string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_ROUTE_PRESET"))))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "":
		return "", nil
	case "scout", "cheap", "cheapscout":
		return "cheap-scout", nil
	case "balanced", "quality", "cheap-scout":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent route preset must be blank or one of: balanced, quality, cheap-scout")
	}
}

func lcagentToolProfileValue(configured string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_TOOL_PROFILE"))))
	if value == "" {
		return lcagentDefaultToolProfile, nil
	}
	switch value {
	case "balanced", "generous":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent tool profile must be one of: balanced, generous")
	}
}

func lcagentContextProfileValue(configured string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_CONTEXT_PROFILE"))))
	if value == "" {
		return lcagentDefaultContextProfile, nil
	}
	switch value {
	case "balanced", "large":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent context profile must be one of: balanced, large")
	}
}

func lcagentWebSearchBackendValue(configured string) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_WEB_SEARCH_BACKEND"))))
	switch value {
	case "exa", "google", "searxng":
		return value
	default:
		return lcagentDefaultWebSearch
	}
}

func lcagentUtilityProviderValue(configured string) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_UTILITY_PROVIDER"))))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "main", "same", "same-as-main":
		return lcagentDefaultUtilityProvider
	case "off", "openai", "deepseek", "moonshot":
		return value
	case "openrouter":
		return "openrouter"
	default:
		return lcagentDefaultUtilityProvider
	}
}

func lcagentResolvedUtilityProvider(routePreset string, mainProvider string, configured string) string {
	utilityProvider := lcagentUtilityProviderValue(configured)
	if utilityProvider != lcagentDefaultUtilityProvider {
		return utilityProvider
	}
	return firstNonEmpty(lcagentRoutePresetProvider(routePreset), mainProvider, lcagentDefaultProvider)
}

func lcagentResolvedUtilityModel(routePreset string, mainModel string, configuredProvider string, configuredModel string) string {
	model := strings.TrimSpace(configuredModel)
	if model != "" {
		return model
	}
	if lcagentUtilityProviderValue(configuredProvider) != lcagentDefaultUtilityProvider {
		return ""
	}
	return firstNonEmpty(lcagentRoutePresetModel(routePreset), mainModel)
}

func (s *lcagentSession) webSearchWarning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webSearchWarningLocked()
}

func (s *lcagentSession) webSearchWarningLocked() string {
	backend := lcagentWebSearchBackendValue(s.webSearchBackend)
	switch backend {
	case "exa":
		hasAPIKey := firstNonEmpty(s.webSearchAPIKey, os.Getenv("EXA_API_KEY")) != ""
		if !hasAPIKey && strings.TrimSpace(s.envFile) == "" {
			return "LCAgent web search is not available. Use /settings here to configure the Exa API key."
		}
	case "google":
		hasAPIKey := firstNonEmpty(s.webSearchAPIKey, os.Getenv("GOOGLE_SEARCH_API_KEY"), os.Getenv("GOOGLE_API_KEY")) != ""
		hasEngineID := firstNonEmpty(s.webSearchEngineID, os.Getenv("GOOGLE_SEARCH_ENGINE_ID"), os.Getenv("GOOGLE_CSE_ID")) != ""
		if (!hasAPIKey || !hasEngineID) && strings.TrimSpace(s.envFile) == "" {
			return "LCAgent web search is not available. Use /settings here to configure the Google search API key and search engine ID."
		}
	case "searxng":
		hasURL := firstNonEmpty(s.webSearchURL, os.Getenv("LCAGENT_WEB_SEARCH_URL"), os.Getenv("LCAGENT_SEARXNG_URL")) != ""
		if !hasURL && strings.TrimSpace(s.envFile) == "" {
			return "LCAgent web search is not available. Use /settings here to configure the SearXNG URL."
		}
	default:
		return "LCAgent web search is not available. Use /settings here to configure a web search backend and API key."
	}
	return ""
}

func lcagentRequestTimeoutValue(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	if raw := strings.TrimSpace(os.Getenv("LCROOM_LCAGENT_REQUEST_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return lcagentDefaultRequestTimeout
}

func lcagentDefaultModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return modeladapter.DefaultOpenAIModel
	case "deepseek":
		return modeladapter.DefaultDeepSeekModel
	case "moonshot":
		return modeladapter.DefaultMoonshotModel
	default:
		return modeladapter.DefaultOpenRouterModel
	}
}

func lcagentRoutePresetProvider(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return "openai"
	case "balanced", "cheap-scout":
		return "openrouter"
	default:
		return ""
	}
}

func lcagentRoutePresetModel(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return modeladapter.DefaultOpenAIModel
	case "balanced":
		return modeladapter.DefaultOpenRouterModel
	case "cheap-scout":
		return "deepseek/deepseek-v4-flash"
	default:
		return ""
	}
}

func (s *lcagentSession) snapshotLocked() Snapshot {
	entries := cloneTranscriptEntries(s.entries)
	transcript := ""
	if s.cache.ready && s.cache.revision == s.revision {
		transcript = s.cache.transcript
	} else {
		transcript = buildTranscriptText(ProviderLCAgent, entries, "\n", false)
		s.cache.ready = true
		s.cache.revision = s.revision
		s.cache.entries = entries
		s.cache.transcript = transcript
	}
	phase := SessionPhaseIdle
	if s.closed {
		phase = SessionPhaseClosed
	} else if s.busy {
		phase = SessionPhaseRunning
	}
	return Snapshot{
		Provider:           ProviderLCAgent,
		ProjectPath:        s.projectPath,
		ThreadID:           s.threadID,
		TranscriptRevision: s.revision,
		Phase:              phase,
		Started:            s.started,
		Busy:               s.busy,
		BusySince:          s.busySince,
		LastBusyActivityAt: s.lastBusyActivityAt,
		Closed:             s.closed,
		Entries:            cloneTranscriptEntries(entries),
		Transcript:         transcript,
		Status:             s.status,
		LastError:          s.lastError,
		PendingApproval:    cloneApprovalRequest(s.pendingApproval),
		LastActivityAt:     s.lastActivityAt,
		CurrentCWD:         s.projectPath,
		Model:              firstNonEmpty(s.model, lcagentRoutePresetModel(s.routePreset), lcagentDefaultModel(s.provider)),
		ModelProvider:      firstNonEmpty(s.modelProvider, lcagentRoutePresetProvider(s.routePreset), lcagentDefaultProvider),
		ReasoningEffort:    s.reasoningEffort,
		TokenUsage:         exportedTokenUsageSnapshot(s.tokenUsage),
	}
}

type lcagentCommand struct {
	Command string
	Args    []string
	Dir     string
}

func lcagentCommandSpec(configuredPath string) (lcagentCommand, error) {
	if configured := firstNonEmpty(configuredPath, os.Getenv("LCROOM_LCAGENT_PATH")); configured != "" {
		return lcagentCommand{Command: configured}, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "lcagent")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return lcagentCommand{Command: sibling}, nil
		}
	}
	if path, err := exec.LookPath("lcagent"); err == nil {
		return lcagentCommand{Command: path}, nil
	}
	if wd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(wd, "cmd", "lcagent", "main.go")); statErr == nil {
			return lcagentCommand{Command: "go", Args: []string{"run", "./cmd/lcagent"}, Dir: wd}, nil
		}
	}
	return lcagentCommand{}, fmt.Errorf("lcagent executable not found; set LCROOM_LCAGENT_PATH")
}

func lcagentAutoLevel(configured string) string {
	raw := firstNonEmpty(configured, os.Getenv("LCROOM_LCAGENT_AUTO"))
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "low", "medium":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return lcagentDefaultAuto
	}
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func rawJSONBool(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawJSONInt(raw json.RawMessage) int {
	var value int
	_ = json.Unmarshal(raw, &value)
	return value
}

func lcagentToolCallText(tool string, raw json.RawMessage) string {
	tool = firstNonEmpty(strings.TrimSpace(tool), "unknown")
	summary := lcagentToolArgsSummary(tool, raw)
	if summary != "" {
		return fmt.Sprintf("Tool %s running: %s", tool, summary)
	}
	return fmt.Sprintf("Tool %s running", tool)
}

func lcagentToolResultText(tool string, raw json.RawMessage) string {
	var result struct {
		Success      bool          `json:"success"`
		Output       string        `json:"output"`
		Error        string        `json:"error"`
		Command      string        `json:"command"`
		CWD          string        `json:"cwd"`
		ExitCode     int           `json:"exit_code"`
		Duration     time.Duration `json:"duration"`
		TimedOut     bool          `json:"timed_out"`
		Truncated    bool          `json:"truncated"`
		Binary       bool          `json:"binary"`
		ArtifactPath string        `json:"artifact_path"`
		FilesTouched []string      `json:"files_touched"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "Tool " + firstNonEmpty(tool, "unknown") + " completed"
	}
	tool = firstNonEmpty(strings.TrimSpace(tool), "unknown")
	status := "completed"
	if result.TimedOut {
		status = "timed out"
	} else if !result.Success {
		status = "failed"
	}
	summary := lcagentToolResultSummary(tool, result.Output, result.Error, result.Command, result.CWD, result.ExitCode, result.Duration, result.Truncated, result.Binary, result.ArtifactPath, result.FilesTouched)
	if summary != "" {
		return fmt.Sprintf("Tool %s %s: %s", tool, status, summary)
	}
	return fmt.Sprintf("Tool %s %s", tool, status)
}

func lcagentToolArgsSummary(tool string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch strings.TrimSpace(tool) {
	case "read_file":
		var args struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if json.Unmarshal(raw, &args) == nil {
			parts := []string{strings.TrimSpace(args.Path)}
			if args.Offset > 0 || args.Limit > 0 {
				lineSpec := "lines"
				if args.Offset > 0 && args.Limit > 0 {
					lineSpec = fmt.Sprintf("lines %d-%d", args.Offset, args.Offset+args.Limit-1)
				} else if args.Offset > 0 {
					lineSpec = fmt.Sprintf("from line %d", args.Offset)
				} else {
					lineSpec = fmt.Sprintf("%d lines", args.Limit)
				}
				parts = append(parts, lineSpec)
			}
			return strings.Join(nonEmptyStrings(parts), " ")
		}
	case "file_outline":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Path)
		}
	case "repo_overview":
		var args struct {
			Path          string `json:"path"`
			MaxFiles      int    `json:"max_files"`
			IncludeHidden bool   `json:"include_hidden"`
		}
		if json.Unmarshal(raw, &args) == nil {
			parts := []string{firstNonEmpty(args.Path, ".")}
			if args.MaxFiles > 0 {
				parts = append(parts, fmt.Sprintf("%d files", args.MaxFiles))
			}
			if args.IncludeHidden {
				parts = append(parts, "include hidden")
			}
			return strings.Join(nonEmptyStrings(parts), " ")
		}
	case "list_files":
		var args struct {
			Path       string `json:"path"`
			Glob       string `json:"glob"`
			MaxEntries int    `json:"max_entries"`
		}
		if json.Unmarshal(raw, &args) == nil {
			parts := []string{firstNonEmpty(args.Path, ".")}
			if strings.TrimSpace(args.Glob) != "" {
				parts = append(parts, "glob "+strings.TrimSpace(args.Glob))
			}
			return strings.Join(nonEmptyStrings(parts), " ")
		}
	case "search":
		var args struct {
			Query         string `json:"query"`
			Path          string `json:"path"`
			FileGlob      string `json:"file_glob"`
			ContextBefore int    `json:"context_before"`
			ContextAfter  int    `json:"context_after"`
		}
		if json.Unmarshal(raw, &args) == nil {
			parts := []string{quoteIfSpaced(args.Query)}
			if strings.TrimSpace(args.Path) != "" {
				parts = append(parts, "in "+strings.TrimSpace(args.Path))
			}
			if strings.TrimSpace(args.FileGlob) != "" {
				parts = append(parts, "glob "+strings.TrimSpace(args.FileGlob))
			}
			if args.ContextBefore > 0 || args.ContextAfter > 0 {
				parts = append(parts, fmt.Sprintf("context %d/%d", args.ContextBefore, args.ContextAfter))
			}
			return strings.Join(nonEmptyStrings(parts), " ")
		}
	case "web_search":
		var args struct {
			Query       string `json:"query"`
			MaxResults  int    `json:"max_results"`
			Site        string `json:"site"`
			RecencyDays int    `json:"recency_days"`
		}
		if json.Unmarshal(raw, &args) == nil {
			parts := []string{quoteIfSpaced(args.Query)}
			if strings.TrimSpace(args.Site) != "" {
				parts = append(parts, "site "+strings.TrimSpace(args.Site))
			}
			if args.RecencyDays > 0 {
				parts = append(parts, fmt.Sprintf("last %dd", args.RecencyDays))
			}
			if args.MaxResults > 0 {
				parts = append(parts, fmt.Sprintf("%d results", args.MaxResults))
			}
			return strings.Join(nonEmptyStrings(parts), " ")
		}
	case "load_skill":
		var args struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Name)
		}
	case "run_command":
		var args struct {
			Command string   `json:"command"`
			Argv    []string `json:"argv"`
			CWD     string   `json:"cwd"`
		}
		if json.Unmarshal(raw, &args) == nil {
			cwd := strings.TrimSpace(args.CWD)
			var command string
			if strings.TrimSpace(args.Command) != "" {
				command = strings.TrimSpace(args.Command)
			} else {
				command = strings.Join(nonEmptyStrings(args.Argv), " ")
			}
			if cwd != "" {
				return strings.TrimSpace(command + " in " + cwd)
			}
			return command
		}
	case "start_process":
		var args struct {
			Command string `json:"command"`
			CWD     string `json:"cwd"`
		}
		if json.Unmarshal(raw, &args) == nil {
			command := strings.TrimSpace(args.Command)
			if cwd := strings.TrimSpace(args.CWD); cwd != "" {
				return strings.TrimSpace(command + " in " + cwd)
			}
			return command
		}
	}
	return ""
}

func lcagentToolResultSummary(tool, output, errText, command, cwd string, exitCode int, duration time.Duration, truncated, binary bool, artifactPath string, filesTouched []string) string {
	if strings.TrimSpace(errText) != "" {
		prefix := lcagentCommandCWDPrefix(command, cwd)
		return strings.TrimSpace(prefix + strings.TrimSpace(errText))
	}
	switch strings.TrimSpace(tool) {
	case "read_file":
		return lcagentFileReadSummary(output, truncated)
	case "file_outline":
		return lcagentFileOutlineSummary(output)
	case "repo_overview":
		return lcagentRepoOverviewSummary(output, truncated)
	case "list_files":
		return lcagentListFilesSummary(output, truncated)
	case "search":
		return lcagentSearchSummary(output, truncated)
	case "web_search":
		return lcagentWebSearchSummary(output, truncated)
	case "run_command":
		summary := lcagentCommandResultSummary(output, exitCode, duration, truncated, binary, artifactPath)
		return strings.TrimSpace(lcagentCommandCWDPrefix(command, cwd) + summary)
	case "start_process", "list_processes", "stop_process":
		return firstOutputLine(output)
	case "apply_patch":
		if len(filesTouched) > 0 {
			return fmt.Sprintf("touched %s", strings.Join(filesTouched, ", "))
		}
		return firstOutputLine(output)
	case "load_skill":
		return lcagentLoadedSkillSummary(output, truncated)
	default:
		return firstOutputLine(output)
	}
}

func lcagentCommandCWDPrefix(command, cwd string) string {
	parts := []string{}
	if command = strings.TrimSpace(command); command != "" {
		parts = append(parts, command)
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		parts = append(parts, "in "+cwd)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + ": "
}

func lcagentWebSearchSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	parts := []string{}
	if values["query"] != "" {
		parts = append(parts, quoteIfSpaced(values["query"]))
	}
	if values["backend"] != "" {
		parts = append(parts, "via "+values["backend"])
	}
	if values["results"] != "" {
		parts = append(parts, values["results"]+" results")
	}
	if truncated {
		parts = append(parts, "truncated")
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func lcagentFileReadSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	linePart := ""
	if values["lines"] != "" {
		linePart = "lines " + values["lines"]
	}
	summary := strings.Join(nonEmptyStrings([]string{values["file"], linePart}), " ")
	if truncated {
		summary = strings.TrimSpace(summary + " truncated")
	}
	return summary
}

func lcagentFileOutlineSummary(output string) string {
	values := lcagentOutputHeaderValues(output)
	parts := []string{values["file"]}
	if values["type"] != "" {
		parts = append(parts, values["type"])
	}
	if values["total_lines"] != "" {
		parts = append(parts, values["total_lines"]+" lines")
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func lcagentListFilesSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	parts := []string{values["path"]}
	if values["glob"] != "" {
		parts = append(parts, "glob "+values["glob"])
	}
	if values["entries"] != "" {
		parts = append(parts, values["entries"]+" entries")
	}
	if truncated {
		parts = append(parts, "truncated")
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func lcagentRepoOverviewSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	parts := []string{values["path"]}
	if values["git"] == "true" {
		if values["branch"] != "" {
			parts = append(parts, "git "+values["branch"])
		} else {
			parts = append(parts, "git")
		}
		if values["dirty"] == "true" {
			parts = append(parts, "dirty")
		}
	} else if values["git"] == "false" {
		parts = append(parts, "no git")
	}
	if values["tracked_files"] != "" {
		parts = append(parts, values["tracked_files"]+" tracked")
	}
	if values["source"] != "" {
		parts = append(parts, "via "+values["source"])
	}
	if truncated {
		parts = append(parts, "truncated")
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func lcagentSearchSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	parts := []string{}
	if values["query"] != "" {
		parts = append(parts, quoteIfSpaced(values["query"]))
	}
	if values["path"] != "" {
		parts = append(parts, "in "+values["path"])
	}
	if values["matches"] != "" {
		parts = append(parts, values["matches"]+" matches")
	}
	if truncated {
		parts = append(parts, "truncated")
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func lcagentCommandResultSummary(output string, exitCode int, duration time.Duration, truncated, binary bool, artifactPath string) string {
	if binary {
		return "binary output suppressed"
	}
	parts := []string{}
	if outputLine := firstNonMetadataOutputLine(output); outputLine != "" {
		parts = append(parts, outputLine)
	}
	if exitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit %d", exitCode))
	}
	if duration > 0 {
		parts = append(parts, duration.Round(time.Millisecond).String())
	}
	if truncated {
		if strings.TrimSpace(artifactPath) != "" {
			parts = append(parts, "truncated; full output saved")
		} else {
			parts = append(parts, "truncated")
		}
	}
	return strings.Join(nonEmptyStrings(parts), " | ")
}

func lcagentLoadedSkillSummary(output string, truncated bool) string {
	values := lcagentOutputHeaderValues(output)
	summary := strings.Join(nonEmptyStrings([]string{values["skill"], values["source"]}), " from ")
	if truncated {
		summary = strings.TrimSpace(summary + " truncated")
	}
	return summary
}

func lcagentOutputHeaderValues(output string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

func firstOutputLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func firstNonMetadataOutputLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[exit:") || strings.HasPrefix(line, "--- output truncated") || strings.HasPrefix(line, "Full output:") || strings.HasPrefix(line, "Explore:") {
			continue
		}
		return line
	}
	return ""
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func quoteIfSpaced(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || (!strings.ContainsAny(value, " \t\n") && !strings.Contains(value, `"`)) {
		return value
	}
	return strconv.Quote(value)
}

func lcagentPlanText(raw json.RawMessage) string {
	var items []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return "Plan updated"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			continue
		}
		if marker := lcagentPlanStatusMarker(item.Status); marker != "" {
			lines = append(lines, marker+" "+step)
			continue
		}
		status := strings.TrimSpace(item.Status)
		if status == "" {
			lines = append(lines, step)
			continue
		}
		lines = append(lines, "["+status+"] "+step)
	}
	if len(lines) == 0 {
		return "Plan updated"
	}
	return strings.Join(lines, "\n")
}

func lcagentPlanStatusMarker(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done":
		return "[x]"
	case "in_progress", "inprogress", "active", "running":
		return "[>]"
	case "pending", "todo", "not_started":
		return "[ ]"
	default:
		return ""
	}
}

func lcagentFilesTouchedText(raw json.RawMessage) string {
	var files []string
	if err := json.Unmarshal(raw, &files); err != nil || len(files) == 0 {
		return "Files touched"
	}
	return "Files touched:\n" + strings.Join(files, "\n")
}
