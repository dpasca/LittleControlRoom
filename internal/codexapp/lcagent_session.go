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
	"lcroom/internal/browserctl"
	"lcroom/internal/lcagent"
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
	xiaomiAPIKey      string
	xiaomiBaseURL     string
	preflightAccess   bool
	routePreset       string
	provider          string
	auto              string
	sessionAuto       string
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
	playwrightPolicy  browserctl.Policy

	mu                       sync.Mutex
	cmd                      *exec.Cmd
	stdin                    io.WriteCloser
	cancel                   context.CancelFunc
	threadID                 string
	runID                    string
	started                  bool
	busy                     bool
	closed                   bool
	busySince                time.Time
	lastBusyActivityAt       time.Time
	lastActivityAt           time.Time
	status                   string
	lastError                string
	model                    string
	modelProvider            string
	reasoningEffort          string
	tokenUsage               *threadTokenUsage
	pendingApproval          *ApprovalRequest
	replayLoaded             bool
	managedBrowserSessionKey string
	browserProfileKey        string
	browserLaunchMode        browserctl.ManagedLaunchMode
	browserActivity          browserctl.SessionActivity
	currentBrowserPageURL    string
	currentBrowserPageStale  bool
	pendingInitialUserEcho   string
	pendingSteerEchoes       []string
	entries                  []TranscriptEntry
	revision                 uint64
	cache                    transcriptExportCache
}

const lcagentIdleShutdownNotice = "Closed embedded LCAgent session after 1 hour of inactivity."

func newLCAgentSession(req LaunchRequest, notify func()) (Session, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ensureManagedPlaywrightSessionKey(&req)
	playwrightPolicy := req.PlaywrightPolicy.Normalize()
	managedSessionKey := strings.TrimSpace(req.ManagedBrowserSessionKey)
	browserLaunchMode := browserctl.ManagedLaunchModeForPolicy(playwrightPolicy)
	browserProfileKey := ""
	if managedSessionKey != "" && playwrightPolicy.ManagementMode == browserctl.ManagementModeManaged {
		browserProfileKey = browserctl.ManagedProfileKey(playwrightPolicy, string(ProviderLCAgent), req.ProjectPath, req.ResumeID, managedSessionKey)
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
	utilityModel := modeladapter.NormalizeModelForProvider(utilityProvider, lcagentResolvedUtilityModel(routePreset, provider, model, req.LCAgentUtilityProvider, req.LCAgentUtilityModel))
	session := &lcagentSession{
		projectPath:              strings.TrimSpace(req.ProjectPath),
		dataDir:                  dataDir,
		execPath:                 strings.TrimSpace(req.LCAgentPath),
		envFile:                  strings.TrimSpace(req.LCAgentEnvFile),
		openAIAPIKey:             strings.TrimSpace(req.LCAgentOpenAIAPIKey),
		openRouterAPIKey:         strings.TrimSpace(req.LCAgentOpenRouterAPIKey),
		deepSeekAPIKey:           strings.TrimSpace(req.LCAgentDeepSeekAPIKey),
		moonshotAPIKey:           strings.TrimSpace(req.LCAgentMoonshotAPIKey),
		xiaomiAPIKey:             strings.TrimSpace(req.LCAgentXiaomiAPIKey),
		xiaomiBaseURL:            strings.TrimSpace(req.LCAgentXiaomiBaseURL),
		preflightAccess:          req.LCAgentPreflightAccess,
		routePreset:              routePreset,
		provider:                 provider,
		auto:                     strings.TrimSpace(req.LCAgentAuto),
		adminWrite:               req.LCAgentAdminWrite,
		toolProfile:              toolProfile,
		contextProfile:           contextProfile,
		requestTimeout:           requestTimeout,
		utilityProvider:          utilityProvider,
		utilityModel:             utilityModel,
		webSearchBackend:         lcagentWebSearchBackendValue(req.LCAgentWebSearchBackend),
		webSearchAPIKey:          strings.TrimSpace(req.LCAgentWebSearchAPIKey),
		webSearchEngineID:        strings.TrimSpace(req.LCAgentWebSearchEngineID),
		webSearchURL:             strings.TrimSpace(req.LCAgentWebSearchURL),
		runtimeManager:           req.RuntimeManager,
		notify:                   notify,
		playwrightPolicy:         playwrightPolicy,
		model:                    model,
		modelProvider:            modelProvider,
		reasoningEffort:          strings.TrimSpace(req.PendingReasoning),
		status:                   "Ready",
		managedBrowserSessionKey: managedSessionKey,
		browserProfileKey:        browserProfileKey,
		browserLaunchMode:        browserLaunchMode,
		browserActivity:          browserctl.DefaultSessionActivity(playwrightPolicy),
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

func (s *lcagentSession) StateSnapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateSnapshotLocked()
}

func (s *lcagentSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *lcagentSession) SubmitInput(input Submission) error {
	if input.Empty() {
		return nil
	}
	transcriptText := input.TranscriptText()
	displayText := firstNonEmpty(input.TranscriptDisplayText(), transcriptText)
	s.mu.Lock()
	if s.busy && s.stdin != nil {
		stdin := s.stdin
		s.status = "Queued steer for LCAgent"
		s.lastError = ""
		s.appendEntryLocked(TranscriptUser, displayText)
		s.appendEntryLocked(TranscriptStatus, "Queued for LCAgent; it will be read at the next turn boundary. Use ctrl+c to interrupt the current run.")
		s.pendingSteerEchoes = append(s.pendingSteerEchoes, strings.TrimSpace(transcriptText))
		s.mu.Unlock()
		if s.notify != nil {
			s.notify()
		}
		payload, err := json.Marshal(map[string]string{
			"type":    "steer",
			"message": transcriptText,
		})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(stdin, string(payload)); err != nil {
			s.appendAsync(TranscriptError, "LCAgent steer failed: "+err.Error())
			return err
		}
		return nil
	}
	s.mu.Unlock()
	return s.startRunAsync(transcriptText, input.TranscriptDisplayText())
}

func (s *lcagentSession) ShowStatus() error {
	s.mu.Lock()
	s.appendEntryLocked(TranscriptStatus, s.statusTextLocked())
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) ShowPermissions() error {
	s.mu.Lock()
	s.appendEntryLocked(TranscriptStatus, s.permissionsTextLocked(false))
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
	return nil
}

func (s *lcagentSession) SetPermissionLevel(level string) error {
	level, err := lcagentNormalizeAutoLevel(level)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("LCAgent session is closed")
	}
	s.sessionAuto = level
	message := "LCAgent permission level set to " + lcagentAutoLabel(level) + "."
	if s.busy {
		message += " The running turn keeps the level it launched with; the next prompt in this session will use " + lcagentAutoLabel(level) + "."
	} else {
		message += " The next prompt in this session will use " + lcagentAutoLabel(level) + "."
	}
	s.status = message
	s.appendEntryLocked(TranscriptStatus, message)
	s.touchLocked()
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
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
	return s.startRunWithOptionsAsync(prompt, "/review current uncommitted changes", lcagentRunOptions{
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
		IncludeAvailable: true,
		EnvFile:          s.envFile,
		OpenAIAPIKey:     s.openAIAPIKey,
		OpenRouterAPIKey: s.openRouterAPIKey,
		DeepSeekAPIKey:   s.deepSeekAPIKey,
		MoonshotAPIKey:   s.moonshotAPIKey,
		XiaomiAPIKey:     s.xiaomiAPIKey,
		XiaomiBaseURL:    s.xiaomiBaseURL,
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
	IncludeAvailable bool
	EnvFile          string
	OpenAIAPIKey     string
	OpenRouterAPIKey string
	DeepSeekAPIKey   string
	MoonshotAPIKey   string
	XiaomiAPIKey     string
	XiaomiBaseURL    string
	RequestTimeout   time.Duration
}

func LCAgentModelOptions(ctx context.Context, cfg LCAgentModelListConfig) ([]ModelOption, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = lcagentDefaultProvider
	}
	if cfg.IncludeAvailable {
		return lcagentAvailableModelOptions(ctx, cfg, provider)
	}
	cfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
	curated := lcagentModelOptionsForProvider(provider)
	live, err := lcagentProviderModelOptions(ctx, cfg, provider)
	if err != nil {
		return lcagentModelOptionsWithCurrent(curated, cfg.Model, false, provider), err
	}
	merged := mergeLCAgentModelOptions(curated, live)
	return lcagentModelOptionsWithCurrent(merged, cfg.Model, true, provider), nil
}

func CheckLCAgentProviderAccess(ctx context.Context, req LaunchRequest) error {
	provider, err := lcagentProviderValue(req.LCAgentProvider)
	if err != nil {
		return err
	}
	routePreset, err := lcagentRoutePresetValue(req.LCAgentRoutePreset)
	if err != nil {
		return err
	}
	modelProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), provider)
	model := modeladapter.NormalizeModelForProvider(modelProvider, req.PendingModel)
	if model == "" {
		model = lcagentDefaultModel(modelProvider)
	}
	if !lcagentLaunchHasCredential(req, modelProvider) {
		return nil
	}
	cfg := LCAgentModelListConfig{
		Provider:         modelProvider,
		Model:            model,
		EnvFile:          req.LCAgentEnvFile,
		OpenAIAPIKey:     req.LCAgentOpenAIAPIKey,
		OpenRouterAPIKey: req.LCAgentOpenRouterAPIKey,
		DeepSeekAPIKey:   req.LCAgentDeepSeekAPIKey,
		MoonshotAPIKey:   req.LCAgentMoonshotAPIKey,
		XiaomiAPIKey:     req.LCAgentXiaomiAPIKey,
		XiaomiBaseURL:    req.LCAgentXiaomiBaseURL,
		RequestTimeout:   req.LCAgentRequestTimeout,
	}
	checkCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		checkCtx, cancel = context.WithTimeout(ctx, lcagentModelListTimeout(req.LCAgentRequestTimeout))
	}
	defer cancel()
	if _, err := LCAgentModelOptions(checkCtx, cfg); err != nil {
		return fmt.Errorf("LCAgent %s provider check failed before launch: %w", lcagentProviderDisplayName(modelProvider), err)
	}
	return nil
}

func lcagentLaunchHasCredential(req LaunchRequest, provider string) bool {
	if strings.TrimSpace(req.LCAgentEnvFile) != "" {
		return true
	}
	keyName, key := lcagentLaunchCredential(req, provider)
	if strings.TrimSpace(key) != "" {
		return true
	}
	return keyName != "" && strings.TrimSpace(os.Getenv(keyName)) != ""
}

func lcagentLaunchCredential(req LaunchRequest, provider string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "OPENAI_API_KEY", strings.TrimSpace(req.LCAgentOpenAIAPIKey)
	case "", "openrouter":
		return "OPENROUTER_API_KEY", strings.TrimSpace(req.LCAgentOpenRouterAPIKey)
	case "deepseek":
		return "DEEPSEEK_API_KEY", strings.TrimSpace(req.LCAgentDeepSeekAPIKey)
	case "moonshot":
		return "MOONSHOT_API_KEY", strings.TrimSpace(req.LCAgentMoonshotAPIKey)
	case "xiaomi":
		return "XIAOMI_API_KEY", strings.TrimSpace(req.LCAgentXiaomiAPIKey)
	default:
		return "", ""
	}
}

func lcagentAvailableModelOptions(ctx context.Context, cfg LCAgentModelListConfig, selectedProvider string) ([]ModelOption, error) {
	providers := lcagentAvailableModelProviders(cfg, selectedProvider)
	var merged []ModelOption
	var firstErr error
	for _, provider := range providers {
		providerCfg := cfg
		providerCfg.Provider = provider
		providerCfg.IncludeAvailable = false
		providerCfg.Model = ""
		if provider == selectedProvider {
			providerCfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
		}
		options, err := LCAgentModelOptions(ctx, providerCfg)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, option := range options {
			if strings.TrimSpace(option.ModelProvider) == "" {
				option.ModelProvider = provider
			}
			if provider != selectedProvider && option.IsDefault {
				option.IsDefault = false
			}
			if provider != selectedProvider {
				option.DisplayName = lcagentProviderDisplayName(provider) + ": " + strings.TrimSpace(firstNonEmpty(option.DisplayName, option.Model))
			}
			merged = append(merged, option)
		}
	}
	if len(merged) == 0 {
		return nil, firstErr
	}
	return merged, firstErr
}

func lcagentAvailableModelProviders(cfg LCAgentModelListConfig, selectedProvider string) []string {
	selectedProvider = strings.ToLower(strings.TrimSpace(selectedProvider))
	if selectedProvider == "" {
		selectedProvider = lcagentDefaultProvider
	}
	hasEnvFile := strings.TrimSpace(cfg.EnvFile) != ""
	hasKey := func(provider string) bool {
		if provider == selectedProvider {
			return true
		}
		if hasEnvFile {
			return true
		}
		return strings.TrimSpace(lcagentModelListAPIKey(provider, cfg)) != ""
	}
	all := []string{"openrouter", "openai", "deepseek", "moonshot", "xiaomi"}
	out := []string{selectedProvider}
	for _, provider := range all {
		if provider == selectedProvider || !hasKey(provider) {
			continue
		}
		out = append(out, provider)
	}
	return out
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
	supportedReasoningEfforts := lcagentReasoningEffortOptionsForProvider(provider)
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
			ModelProvider:             provider,
			DisplayName:               displayName,
			Description:               description,
			SupportedReasoningEfforts: supportedReasoningEfforts,
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
		BaseURL:        lcagentModelListBaseURL(provider, cfg),
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
	case "xiaomi":
		return modeladapter.NewXiaomiClient(adapterCfg)
	default:
		return modeladapter.NewOpenRouterClient(adapterCfg)
	}
}

func lcagentModelListBaseURL(provider string, cfg LCAgentModelListConfig) string {
	if strings.EqualFold(strings.TrimSpace(provider), "xiaomi") {
		return strings.TrimSpace(cfg.XiaomiBaseURL)
	}
	return ""
}

func lcagentModelListAPIKey(provider string, cfg LCAgentModelListConfig) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return strings.TrimSpace(cfg.OpenAIAPIKey)
	case "deepseek":
		return strings.TrimSpace(cfg.DeepSeekAPIKey)
	case "moonshot":
		return strings.TrimSpace(cfg.MoonshotAPIKey)
	case "xiaomi":
		return strings.TrimSpace(cfg.XiaomiAPIKey)
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
	reasoning := lcagentReasoningEffortOptionsForProvider(provider)
	option := func(model, displayName, description, defaultReasoning string, isDefault bool) ModelOption {
		return ModelOption{
			ID:                        model,
			Model:                     model,
			ModelProvider:             provider,
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
			option("xiaomi/mimo-v2.5-pro", "Benchmark: MiMo 2.5 Pro", "Xiaomi MiMo-V2.5-Pro benchmark route through OpenRouter.", "low", defaultModel == "xiaomi/mimo-v2.5-pro"),
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
	case "xiaomi":
		return []ModelOption{
			option(modeladapter.DefaultXiaomiModel, "Balanced: MiMo 2.5 Pro", "Direct Xiaomi MiMo coding route.", "low", true),
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

func lcagentReasoningEffortOptionsForProvider(provider string) []ReasoningEffortOption {
	if strings.EqualFold(strings.TrimSpace(provider), "moonshot") {
		return nil
	}
	return lcagentReasoningEffortOptions()
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

func lcagentModelOptionsWithCurrent(models []ModelOption, current string, checkedProviderList bool, provider string) []ModelOption {
	current = strings.TrimSpace(current)
	if current == "" || lcagentModelOptionExists(models, current) {
		return models
	}
	reasoningEfforts := lcagentReasoningEffortOptionsForProvider(provider)
	description := "Custom LCAgent model."
	if checkedProviderList {
		description = "Custom LCAgent model. The provider model list did not return this ID."
	}
	return append([]ModelOption{{
		ID:                        current,
		Model:                     current,
		ModelProvider:             provider,
		DisplayName:               current,
		Description:               description,
		SupportedReasoningEfforts: reasoningEfforts,
		DefaultReasoningEffort:    lcagentDefaultReasoningEffort(provider, current),
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
	case "xiaomi":
		return "Xiaomi"
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
	return s.StageModelProviderOverride("", model, reasoningEffort)
}

func (s *lcagentSession) StageModelProviderOverride(provider, model, reasoningEffort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "" {
		s.provider = provider
		s.modelProvider = provider
		s.routePreset = ""
		model = modeladapter.NormalizeModelForProvider(provider, model)
	}
	if strings.TrimSpace(model) != "" {
		s.model = strings.TrimSpace(model)
	} else if provider != "" {
		// Provider changed without an explicit model; clear stale model
		// so the next run defaults to the correct model for the new provider.
		s.model = ""
	}
	s.reasoningEffort = strings.TrimSpace(reasoningEffort)
	return nil
}

func (s *lcagentSession) Interrupt() error {
	s.mu.Lock()
	cancel := s.cancel
	cmd := s.cmd
	s.mu.Unlock()
	return cancelLCAgentRun(cancel, cmd)
}

func cancelLCAgentRun(cancel context.CancelFunc, cmd *exec.Cmd) error {
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		return terminateAppServerCommand(cmd)
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
	cmd := s.cmd
	s.mu.Unlock()
	err := cancelLCAgentRun(cancel, cmd)
	if s.notify != nil {
		s.notify()
	}
	return err
}

func (s *lcagentSession) CloseDueToInactivity() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.busy || s.pendingApproval != nil {
		s.touchLocked()
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.status = lcagentIdleShutdownNotice
	s.appendEntryLocked(TranscriptSystem, lcagentIdleShutdownNotice)
	cancel := s.cancel
	cmd := s.cmd
	s.mu.Unlock()

	err := cancelLCAgentRun(cancel, cmd)
	if s.notify != nil {
		s.notify()
	}
	return err
}

func (s *lcagentSession) startRun(prompt, displayPrompt string) error {
	return s.startRunWithOptions(prompt, displayPrompt, lcagentRunOptions{})
}

func (s *lcagentSession) startRunAsync(prompt, displayPrompt string) error {
	return s.startRunWithOptionsAsync(prompt, displayPrompt, lcagentRunOptions{})
}

type lcagentPreparedRun struct {
	prompt             string
	resumeID           string
	provider           string
	routePreset        string
	autoOverride       string
	autoLevel          string
	sessionAuto        string
	runningStatus      string
	model              string
	modelProvider      string
	toolProfile        string
	contextProfile     string
	adminWrite         bool
	requestTimeout     time.Duration
	maxTurns           int
	reasoningEffort    string
	credentialProvider string
	providerAPIKeyName string
	providerAPIKey     string
	preflightAccess    bool
	preflightReq       LaunchRequest
	webSearchBackend   string
	webSearchAPIKey    string
	webSearchEngineID  string
	webSearchURL       string
	xiaomiBaseURL      string
	utilityProvider    string
	utilityModel       string
	utilityAPIKeyName  string
	utilityAPIKey      string
	browserControl     string
	browserSessionKey  string
	browserProfileKey  string
	browserLaunchMode  browserctl.ManagedLaunchMode
}

func (s *lcagentSession) startRunWithOptions(prompt, displayPrompt string, opts lcagentRunOptions) error {
	prepared, err := s.prepareRun(prompt, displayPrompt, opts)
	if err != nil {
		return err
	}
	return s.launchPreparedRun(prepared)
}

func (s *lcagentSession) startRunWithOptionsAsync(prompt, displayPrompt string, opts lcagentRunOptions) error {
	prepared, err := s.prepareRun(prompt, displayPrompt, opts)
	if err != nil {
		return err
	}
	go func() {
		_ = s.launchPreparedRun(prepared)
	}()
	return nil
}

func (s *lcagentSession) prepareRun(prompt, displayPrompt string, opts lcagentRunOptions) (lcagentPreparedRun, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return lcagentPreparedRun{}, fmt.Errorf("LCAgent session is closed")
	}
	if s.busy {
		s.mu.Unlock()
		return lcagentPreparedRun{}, fmt.Errorf("LCAgent is already running")
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
	renderedPrompt := firstNonEmpty(displayPrompt, prompt)
	s.appendEntryLocked(TranscriptUser, renderedPrompt)
	s.pendingInitialUserEcho = strings.TrimSpace(renderedPrompt)
	provider := firstNonEmpty(s.provider, lcagentDefaultProvider)
	routePreset := strings.TrimSpace(s.routePreset)
	sessionAuto := strings.TrimSpace(s.sessionAuto)
	autoLevel := lcagentAutoLevel(firstNonEmpty(opts.autoOverride, s.sessionAuto, s.auto))
	model := strings.TrimSpace(s.model)
	if model == "" && routePreset == "" {
		model = lcagentDefaultModel(provider)
	}
	modelProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), provider)
	model = modeladapter.NormalizeModelForProvider(modelProvider, model)
	if routePreset == "" && !modeladapter.ModelIsKnownForProvider(modelProvider, model) {
		model = lcagentDefaultModel(modelProvider)
	}
	toolProfile := firstNonEmpty(s.toolProfile, lcagentDefaultToolProfile)
	contextProfile := firstNonEmpty(s.contextProfile, lcagentDefaultContextProfile)
	adminWrite := s.adminWrite
	requestTimeout := s.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = lcagentDefaultRequestTimeout
	}
	maxTurns := modeladapter.MaxTurnsForRequestTimeout(requestTimeout)
	reasoningEffort := strings.TrimSpace(s.reasoningEffort)
	credentialProvider := modelProvider
	providerAPIKeyName, providerAPIKey := s.providerCredentialLocked(credentialProvider)
	preflightAccess := s.preflightAccess
	preflightReq := LaunchRequest{
		Provider:                ProviderLCAgent,
		ProjectPath:             s.projectPath,
		PendingModel:            model,
		LCAgentEnvFile:          s.envFile,
		LCAgentOpenAIAPIKey:     s.openAIAPIKey,
		LCAgentOpenRouterAPIKey: s.openRouterAPIKey,
		LCAgentDeepSeekAPIKey:   s.deepSeekAPIKey,
		LCAgentMoonshotAPIKey:   s.moonshotAPIKey,
		LCAgentXiaomiAPIKey:     s.xiaomiAPIKey,
		LCAgentXiaomiBaseURL:    s.xiaomiBaseURL,
		LCAgentRoutePreset:      routePreset,
		LCAgentProvider:         provider,
		LCAgentRequestTimeout:   requestTimeout,
	}
	webSearchBackend := firstNonEmpty(s.webSearchBackend, lcagentDefaultWebSearch)
	webSearchAPIKey := strings.TrimSpace(s.webSearchAPIKey)
	webSearchEngineID := strings.TrimSpace(s.webSearchEngineID)
	webSearchURL := strings.TrimSpace(s.webSearchURL)
	xiaomiBaseURL := strings.TrimSpace(s.xiaomiBaseURL)
	utilityProvider := firstNonEmpty(s.utilityProvider, lcagentDefaultUtilityProvider)
	utilityModel := modeladapter.NormalizeModelForProvider(utilityProvider, s.utilityModel)
	utilityAPIKeyName, utilityAPIKey := s.providerCredentialLocked(utilityProvider)
	browserControl := "off"
	browserSessionKey := strings.TrimSpace(s.managedBrowserSessionKey)
	browserProfileKey := strings.TrimSpace(s.browserProfileKey)
	browserLaunchMode := s.browserLaunchMode.Normalize()
	if s.playwrightPolicy.Normalize().ManagementMode == browserctl.ManagementModeManaged && browserSessionKey != "" && browserProfileKey != "" {
		browserControl = "managed"
	}
	if warning := s.webSearchWarningLocked(); warning != "" {
		s.appendEntryLocked(TranscriptStatus, warning)
	}
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}

	return lcagentPreparedRun{
		prompt:             prompt,
		resumeID:           resumeID,
		provider:           provider,
		routePreset:        routePreset,
		autoOverride:       opts.autoOverride,
		autoLevel:          autoLevel,
		sessionAuto:        sessionAuto,
		runningStatus:      opts.runningStatus,
		model:              model,
		modelProvider:      modelProvider,
		toolProfile:        toolProfile,
		contextProfile:     contextProfile,
		adminWrite:         adminWrite,
		requestTimeout:     requestTimeout,
		maxTurns:           maxTurns,
		reasoningEffort:    reasoningEffort,
		credentialProvider: credentialProvider,
		providerAPIKeyName: providerAPIKeyName,
		providerAPIKey:     providerAPIKey,
		preflightAccess:    preflightAccess,
		preflightReq:       preflightReq,
		webSearchBackend:   webSearchBackend,
		webSearchAPIKey:    webSearchAPIKey,
		webSearchEngineID:  webSearchEngineID,
		webSearchURL:       webSearchURL,
		xiaomiBaseURL:      xiaomiBaseURL,
		utilityProvider:    utilityProvider,
		utilityModel:       utilityModel,
		utilityAPIKeyName:  utilityAPIKeyName,
		utilityAPIKey:      utilityAPIKey,
		browserControl:     browserControl,
		browserSessionKey:  browserSessionKey,
		browserProfileKey:  browserProfileKey,
		browserLaunchMode:  browserLaunchMode,
	}, nil
}

func (s *lcagentSession) launchPreparedRun(prepared lcagentPreparedRun) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed || !s.busy {
		s.mu.Unlock()
		cancel()
		return nil
	}
	s.cancel = cancel
	s.mu.Unlock()

	if prepared.resumeID != "" {
		// Force-stabilize the thread state if it's stuck in a non-stable status
		// (e.g. in_flight_model_response from a previous crashed run).
		if err := lcagent.ForceStabilizeThreadState(s.dataDir, prepared.resumeID, s.projectPath); err != nil {
			err = fmt.Errorf("LCAgent resume stabilize: %w", err)
			s.finishRun("", false, err)
			return err
		}
	}

	if prepared.preflightAccess {
		if err := CheckLCAgentProviderAccess(ctx, prepared.preflightReq); err != nil {
			s.finishRun("", false, err)
			return err
		}
	}

	spec, err := lcagentCommandSpec(s.execPath)
	if err != nil {
		s.finishRun("", false, err)
		return err
	}
	args := append([]string{}, spec.Args...)
	args = append(args,
		"exec",
		"--cwd", s.projectPath,
		"--data-dir", s.dataDir,
		"--output", "stream-json",
		"--approval-mode", "ask",
		"--utility-provider", prepared.utilityProvider,
		"--web-search-backend", prepared.webSearchBackend,
		"--browser-control", prepared.browserControl,
		"--max-turns", strconv.Itoa(prepared.maxTurns),
	)
	if prepared.browserControl == "managed" {
		args = append(args,
			"--browser-session-key", prepared.browserSessionKey,
			"--browser-profile-key", prepared.browserProfileKey,
			"--browser-launch-mode", string(prepared.browserLaunchMode),
		)
	}
	if prepared.utilityModel != "" {
		args = append(args, "--utility-model", prepared.utilityModel)
	}
	if prepared.adminWrite {
		args = append(args, "--admin-write")
	}
	if prepared.routePreset != "" && prepared.autoOverride == "" {
		args = append(args,
			"--route-preset", prepared.routePreset,
			"--request-timeout", prepared.requestTimeout.String(),
		)
		if strings.TrimSpace(prepared.sessionAuto) != "" {
			args = append(args, "--auto", prepared.autoLevel)
		}
	} else {
		args = append(args,
			"--auto", prepared.autoLevel,
			"--provider", prepared.provider,
		)
		if prepared.model != "" {
			args = append(args, "--model", prepared.model)
		}
		args = append(args,
			"--tool-profile", prepared.toolProfile,
			"--context-profile", prepared.contextProfile,
			"--request-timeout", prepared.requestTimeout.String(),
		)
		if prepared.reasoningEffort != "" {
			args = append(args, "--reasoning-effort", prepared.reasoningEffort)
		}
	}
	if prepared.webSearchEngineID != "" {
		args = append(args, "--web-search-engine-id", prepared.webSearchEngineID)
	}
	if prepared.webSearchURL != "" {
		args = append(args, "--web-search-url", prepared.webSearchURL)
	}
	envFile := firstNonEmpty(s.envFile, os.Getenv("LCROOM_LCAGENT_ENV_FILE"))
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	if prepared.resumeID != "" {
		args = append(args, "--continue-from", prepared.resumeID)
	}
	args = append(args, prepared.prompt)
	cmd := exec.CommandContext(ctx, spec.Command, args...)
	configureAppServerCommand(cmd)
	cmd.Dir = spec.Dir
	cmd.Env = browserctl.AppendEnv(os.Environ(), string(ProviderLCAgent), s.playwrightPolicy)
	if prepared.providerAPIKeyName != "" && prepared.providerAPIKey != "" {
		cmd.Env = setCommandEnv(cmd.Env, prepared.providerAPIKeyName, prepared.providerAPIKey)
	}
	if prepared.utilityAPIKeyName != "" && prepared.utilityAPIKey != "" {
		cmd.Env = setCommandEnv(cmd.Env, prepared.utilityAPIKeyName, prepared.utilityAPIKey)
	}
	if prepared.xiaomiBaseURL != "" && (strings.EqualFold(prepared.credentialProvider, "xiaomi") || strings.EqualFold(prepared.utilityProvider, "xiaomi")) {
		cmd.Env = setCommandEnv(cmd.Env, "XIAOMI_BASE_URL", prepared.xiaomiBaseURL)
	}
	if prepared.webSearchAPIKey != "" {
		switch prepared.webSearchBackend {
		case "exa":
			cmd.Env = setCommandEnv(cmd.Env, "EXA_API_KEY", prepared.webSearchAPIKey)
		default:
			cmd.Env = setCommandEnv(cmd.Env, "GOOGLE_SEARCH_API_KEY", prepared.webSearchAPIKey)
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
	s.status = firstNonEmpty(prepared.runningStatus, "LCAgent running")
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
	case "xiaomi":
		return "XIAOMI_API_KEY", strings.TrimSpace(s.xiaomiAPIKey)
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
	case "user_message":
		s.handleUserMessageEvent(event)
	case "tool_call":
		tool := rawJSONString(event["tool"])
		s.appendAsync(TranscriptTool, lcagentToolCallText(tool, event["args"]))
	case "tool_result":
		tool := rawJSONString(event["tool"])
		text := lcagentToolResultText(tool, event["result"])
		s.appendAsync(TranscriptTool, text)
	case "browser_activity_started":
		s.handleBrowserActivityEvent(event, browserctl.SessionActivityStateActive)
	case "browser_activity_finished":
		s.handleBrowserActivityEvent(event, browserctl.SessionActivityStateIdle)
	case "browser_waiting_for_user":
		s.handleBrowserActivityEvent(event, browserctl.SessionActivityStateWaitingForUser)
		s.handleBrowserPageEvent(event)
		if text := lcagentBrowserWaitingText(event); text != "" {
			s.appendAsync(TranscriptStatus, text)
		}
	case "browser_page":
		s.handleBrowserPageEvent(event)
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

func (s *lcagentSession) handleUserMessageEvent(event map[string]json.RawMessage) {
	message := strings.TrimSpace(rawJSONString(event["message"]))
	if message == "" {
		return
	}
	origin := strings.TrimSpace(rawJSONString(event["origin"]))
	s.mu.Lock()
	if s.shouldSuppressPendingSteerEchoLocked(message, origin) {
		s.touchLocked()
		s.mu.Unlock()
		if s.notify != nil {
			s.notify()
		}
		return
	}
	if s.shouldSuppressInitialUserEchoLocked(message, origin) {
		s.pendingInitialUserEcho = ""
		s.touchLocked()
		s.mu.Unlock()
		if s.notify != nil {
			s.notify()
		}
		return
	}
	s.pendingInitialUserEcho = ""
	s.appendEntryLocked(TranscriptUser, message)
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}

func (s *lcagentSession) shouldSuppressPendingSteerEchoLocked(message, origin string) bool {
	if !strings.EqualFold(origin, "steer") {
		return false
	}
	message = strings.TrimSpace(message)
	for i, pending := range s.pendingSteerEchoes {
		if strings.TrimSpace(pending) != message {
			continue
		}
		s.pendingSteerEchoes = append(s.pendingSteerEchoes[:i], s.pendingSteerEchoes[i+1:]...)
		return true
	}
	return false
}

func (s *lcagentSession) shouldSuppressInitialUserEchoLocked(message, origin string) bool {
	pending := strings.TrimSpace(s.pendingInitialUserEcho)
	if pending == "" {
		return false
	}
	if strings.EqualFold(origin, "initial_prompt") {
		return true
	}
	return origin == "" && message == pending
}

func (s *lcagentSession) handleBrowserActivityEvent(event map[string]json.RawMessage, state browserctl.SessionActivityState) {
	s.mu.Lock()
	activity := s.browserActivity.Normalize()
	activity.Policy = s.playwrightPolicy.Normalize()
	activity.State = state
	activity.ServerName = firstNonEmpty(rawJSONString(event["server_name"]), "playwright")
	activity.ToolName = firstNonEmpty(rawJSONString(event["tool"]), rawJSONString(event["tool_name"]), activity.ToolName)
	activity.LastEventAt = rawJSONTime(event["timestamp"])
	if activity.LastEventAt.IsZero() {
		activity.LastEventAt = time.Now()
	}
	s.browserActivity = activity.Normalize()
	if state == browserctl.SessionActivityStateWaitingForUser {
		s.status = "Browser waiting for user input"
	}
	s.touchLocked()
	s.mu.Unlock()
}

func (s *lcagentSession) handleBrowserPageEvent(event map[string]json.RawMessage) {
	url := strings.TrimSpace(rawJSONString(event["url"]))
	if url == "" {
		return
	}
	fresh := true
	if _, ok := event["fresh"]; ok {
		fresh = rawJSONBool(event["fresh"])
	}
	s.mu.Lock()
	s.currentBrowserPageURL = url
	s.currentBrowserPageStale = !fresh
	if key := strings.TrimSpace(rawJSONString(event["session_key"])); key != "" {
		s.managedBrowserSessionKey = key
	}
	s.touchLocked()
	s.mu.Unlock()
}

func lcagentBrowserWaitingText(event map[string]json.RawMessage) string {
	message := strings.TrimSpace(rawJSONString(event["message"]))
	if message != "" {
		return message
	}
	if url := strings.TrimSpace(rawJSONString(event["url"])); url != "" {
		return "Browser waiting for user input: " + url
	}
	return "Browser waiting for user input"
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

func (s *lcagentSession) statusTextLocked() string {
	status := strings.TrimSpace(s.status)
	if status == "" {
		status = "LCAgent status unavailable"
	}
	parts := []string{
		"Embedded LCAgent status",
		"status: " + status,
		"permissions: " + lcagentAutoLabel(s.effectiveAutoLocked()),
	}
	if route := strings.TrimSpace(s.routePreset); route != "" {
		parts = append(parts, "route preset: "+route)
	}
	if provider := strings.TrimSpace(firstNonEmpty(s.modelProvider, lcagentRoutePresetProvider(s.routePreset), s.provider)); provider != "" {
		parts = append(parts, "provider: "+provider)
	}
	if model := strings.TrimSpace(firstNonEmpty(s.model, lcagentRoutePresetModel(s.routePreset), lcagentDefaultModel(s.provider))); model != "" {
		parts = append(parts, "model: "+model)
	}
	if reasoning := strings.TrimSpace(s.reasoningEffort); reasoning != "" {
		parts = append(parts, "reasoning effort: "+reasoning)
	}
	tokenUsage := cloneThreadTokenUsage(s.tokenUsage)
	s.applyContextWindowToTokenUsageLocked(tokenUsage)
	appendThreadTokenUsageStatusLines(&parts, tokenUsage)
	parts = append(parts, s.permissionsTextLocked(true))
	return strings.Join(parts, "\n")
}

func (s *lcagentSession) permissionsTextLocked(compact bool) string {
	current := s.effectiveAutoLocked()
	lines := []string{
		"LCAgent permissions",
		"current: " + lcagentAutoLabel(current),
	}
	if strings.TrimSpace(s.routePreset) != "" && strings.TrimSpace(s.sessionAuto) == "" {
		lines = append(lines, "source: route preset "+s.routePreset)
	}
	if strings.TrimSpace(s.sessionAuto) != "" {
		lines = append(lines, "source: this session override")
	}
	if s.adminWrite {
		lines = append(lines, "admin write: on; explicit absolute-path writes outside the workspace are allowed")
	} else {
		lines = append(lines, "admin write: off; write tools stay inside the workspace")
	}
	lines = append(lines,
		"",
		"Off: write tools are denied. Commands are limited to explicit read-only forms.",
		"Low: project-local file edits are allowed. Commands may inspect, or run approved argv-only verification forms such as go test ./..., npm run test, make test, cargo test, pytest, tsc --noEmit, eslint, ruff, prettier --check, or similar checks. Other commands ask for approval.",
		"Medium: project-local file edits stay allowed, and command execution no longer uses the Low allowlist, so trusted local setup/build/process commands can run without repeated approval. Write tools still stay inside the workspace unless admin write is on.",
	)
	if !compact {
		lines = append(lines, "", "Change it here with /permissions off, /permissions low, or /permissions medium.")
	}
	return strings.Join(lines, "\n")
}

func (s *lcagentSession) effectiveAutoLocked() string {
	if auto := strings.TrimSpace(s.sessionAuto); auto != "" {
		return lcagentAutoLevel(auto)
	}
	if routeAuto := lcagentRoutePresetAuto(s.routePreset); routeAuto != "" {
		return routeAuto
	}
	return lcagentAutoLevel(s.auto)
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
	openedAt := time.Now()
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
	if activity := replay.browserActivity.Normalize(); activity.State != "" || activity.ServerName != "" || activity.ToolName != "" {
		activity.Policy = s.playwrightPolicy.Normalize()
		s.browserActivity = activity.Normalize()
	}
	if key := strings.TrimSpace(replay.managedBrowserSessionKey); key != "" {
		s.managedBrowserSessionKey = key
	}
	s.currentBrowserPageURL = strings.TrimSpace(replay.currentBrowserPageURL)
	s.currentBrowserPageStale = replay.currentBrowserPageStale
	if model := strings.TrimSpace(replay.model); model != "" {
		s.model = model
	}
	if provider := strings.TrimSpace(replay.modelProvider); provider != "" {
		s.modelProvider = provider
	}
	// If the restored model doesn't belong to the session's current provider,
	// clear it so the next run defaults to the correct model.
	sessionProvider := firstNonEmpty(s.provider, lcagentDefaultProvider)
	if s.model != "" && !modeladapter.ModelIsKnownForProvider(sessionProvider, s.model) {
		s.model = ""
	}
	s.tokenUsage = cloneThreadTokenUsage(replay.tokenUsage)
	label := firstNonEmpty(s.threadID, "history")
	s.status = "Loaded LCAgent thread " + label + " from disk"
	s.replayLoaded = true
	s.entries = nil
	s.revision = 0
	s.cache.ready = false
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
	s.lastActivityAt = openedAt
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
	s.applyContextWindowToTokenUsageLocked(s.tokenUsage)
	s.tokenUsage.Last = breakdown
	s.tokenUsage.Total.CachedInputTokens += breakdown.CachedInputTokens
	s.tokenUsage.Total.InputTokens += breakdown.InputTokens
	s.tokenUsage.Total.OutputTokens += breakdown.OutputTokens
	s.tokenUsage.Total.ReasoningOutputTokens += breakdown.ReasoningOutputTokens
	s.tokenUsage.Total.TotalTokens += breakdown.TotalTokens
}

func (s *lcagentSession) applyContextWindowToTokenUsageLocked(tokenUsage *threadTokenUsage) {
	if tokenUsage == nil {
		return
	}
	if budget := lcagent.ContextCompactionApproxTokenBudget(s.contextProfile); budget > 0 {
		tokenUsage.ModelContextWindow = &budget
	}
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
	case "openrouter", "openai", "deepseek", "moonshot", "xiaomi":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent provider must be one of: openrouter, openai, deepseek, moonshot, xiaomi")
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
	case "mimo", "mimo-pro", "mimo25pro", "mimo-25-pro", "mimo-2.5-pro", "xiaomi", "xiaomi-mimo":
		return "mimo-2.5-pro-low", nil
	case "balanced", "quality", "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max", "cheap-scout":
		return value, nil
	default:
		return "", fmt.Errorf("LCAgent route preset must be blank or one of: balanced, quality, mimo-2.5-pro-low, mimo-2.5-pro-high, mimo-2.5-pro-max, cheap-scout")
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
	case "off", "openai", "deepseek", "moonshot", "xiaomi":
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

func lcagentResolvedUtilityModel(routePreset string, mainProvider string, mainModel string, configuredProvider string, configuredModel string) string {
	model := strings.TrimSpace(configuredModel)
	if model != "" {
		return model
	}
	utilityProvider := lcagentUtilityProviderValue(configuredProvider)
	if utilityProvider != lcagentDefaultUtilityProvider {
		return ""
	}
	resolvedProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), mainProvider, lcagentDefaultProvider)
	if strings.EqualFold(strings.TrimSpace(resolvedProvider), "xiaomi") {
		return modeladapter.DefaultXiaomiUtilityModel
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
	case "xiaomi":
		return modeladapter.DefaultXiaomiModel
	default:
		return modeladapter.DefaultOpenRouterModel
	}
}

func lcagentRoutePresetProvider(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return "openai"
	case "balanced", "cheap-scout":
		return "deepseek"
	case "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max":
		return "xiaomi"
	default:
		return ""
	}
}

func lcagentRoutePresetModel(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "quality":
		return modeladapter.DefaultOpenAIModel
	case "balanced":
		return modeladapter.DefaultDeepSeekModel
	case "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max":
		return modeladapter.DefaultXiaomiModel
	case "cheap-scout":
		return "deepseek-v4-flash"
	default:
		return ""
	}
}

func lcagentRoutePresetAuto(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "balanced", "quality", "mimo-2.5-pro-low", "mimo-2.5-pro-high", "mimo-2.5-pro-max":
		return "low"
	case "cheap-scout":
		return "off"
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
	snapshot := s.stateSnapshotLocked()
	snapshot.Entries = cloneTranscriptEntries(entries)
	snapshot.Transcript = transcript
	return snapshot
}

func (s *lcagentSession) stateSnapshotLocked() Snapshot {
	phase := SessionPhaseIdle
	if s.closed {
		phase = SessionPhaseClosed
	} else if s.busy {
		phase = SessionPhaseRunning
	}
	tokenUsage := cloneThreadTokenUsage(s.tokenUsage)
	s.applyContextWindowToTokenUsageLocked(tokenUsage)
	return Snapshot{
		Provider:                 ProviderLCAgent,
		ProjectPath:              s.projectPath,
		ThreadID:                 s.threadID,
		BrowserActivity:          s.browserActivity.Normalize(),
		ManagedBrowserSessionKey: strings.TrimSpace(s.managedBrowserSessionKey),
		CurrentBrowserPageURL:    strings.TrimSpace(s.currentBrowserPageURL),
		CurrentBrowserPageStale:  s.currentBrowserPageStale,
		TranscriptRevision:       s.revision,
		Phase:                    phase,
		Started:                  s.started,
		Busy:                     s.busy,
		BusySince:                s.busySince,
		LastBusyActivityAt:       s.lastBusyActivityAt,
		Closed:                   s.closed,
		Status:                   s.status,
		LastError:                s.lastError,
		PendingApproval:          cloneApprovalRequest(s.pendingApproval),
		LastActivityAt:           s.lastActivityAt,
		CurrentCWD:               s.projectPath,
		PermissionLevel:          s.effectiveAutoLocked(),
		Model:                    firstNonEmpty(s.model, lcagentRoutePresetModel(s.routePreset), lcagentDefaultModel(s.provider)),
		ModelProvider:            firstNonEmpty(s.modelProvider, lcagentRoutePresetProvider(s.routePreset), lcagentDefaultProvider),
		ReasoningEffort:          s.reasoningEffort,
		TokenUsage:               exportedTokenUsageSnapshot(tokenUsage),
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

func lcagentNormalizeAutoLevel(configured string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "off", "low", "medium":
		return strings.ToLower(strings.TrimSpace(configured)), nil
	default:
		return "", fmt.Errorf("LCAgent permissions must be one of: off, low, medium")
	}
}

func lcagentAutoLabel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return "Off"
	case "medium":
		return "Medium"
	default:
		return "Low"
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
	case "browser_navigate":
		var args struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.URL)
		}
	case "browser_snapshot":
		var args struct {
			MaxChars int `json:"max_chars"`
		}
		if json.Unmarshal(raw, &args) == nil && args.MaxChars > 0 {
			return fmt.Sprintf("max %d chars", args.MaxChars)
		}
	case "browser_click":
		var args struct {
			Ref string `json:"ref"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Ref)
		}
	case "browser_fill":
		var args struct {
			Ref string `json:"ref"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Ref)
		}
	case "browser_press":
		var args struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Key)
		}
	case "browser_screenshot":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &args) == nil {
			return strings.TrimSpace(args.Path)
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
	case "browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_press", "browser_screenshot", "browser_current_page":
		if strings.TrimSpace(artifactPath) != "" {
			return "artifact " + strings.TrimSpace(artifactPath)
		}
		return firstOutputLine(output)
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
