package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/appfs"
	"lcroom/internal/attention"
	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/keyedmutex"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
	"lcroom/internal/todoworktree"
)

const recentActivityDiscoveryWindow = 24 * time.Hour
const asyncProjectRefreshTimeout = 30 * time.Second
const bossAssistantHTTPTimeout = 90 * time.Second
const defaultBossAssistantModel = "gpt-5.4-mini"

var scanGitMetadataTimeout = 1500 * time.Millisecond

type SessionClassifier interface {
	QueueProject(ctx context.Context, state model.ProjectState) (bool, error)
	Notify()
	Start(ctx context.Context)
}

type sessionClassifierRetryer interface {
	QueueProjectRetry(ctx context.Context, state model.ProjectState, retryAfter time.Duration) (bool, error)
}

type Service struct {
	cfg           config.AppConfig
	store         *store.Store
	bus           *events.Bus
	detectors     []detectors.Detector
	classifier    SessionClassifier
	todoSuggester *todoworktree.Manager

	backendDetector func(context.Context, config.AppConfig, config.AIBackend) aibackend.Status

	commitMessageSuggester   gitops.CommitMessageSuggester
	untrackedFileRecommender gitops.UntrackedFileRecommender
	commitAssistantTimeout   time.Duration
	llmUsageTracker          *llm.UsageTracker
	bossChatUsageTracker     *llm.UsageTracker
	opencodeDiscovery        *llm.OpenCodeDiscovery

	gitFingerprintReader   func(context.Context, string) (scanner.GitFingerprint, error)
	gitRepoStatusReader    func(context.Context, string) (scanner.GitRepoStatus, error)
	gitWorktreeInfoReader  func(context.Context, string) (scanner.GitWorktreeInfo, error)
	gitWorktreeListReader  func(context.Context, string) ([]scanner.GitWorktree, error)
	gitRepoInitializer     func(context.Context, string) error
	refreshProjectStatusFn func(context.Context, string) error

	mu sync.Mutex

	projectStateLocks keyedmutex.Locker

	refreshMu    sync.Mutex
	refreshState map[string]asyncProjectRefreshState
}

type asyncProjectRefreshState struct {
	running bool
	queued  bool
}

type serviceRuntimeSnapshot struct {
	cfg                   config.AppConfig
	classifier            SessionClassifier
	gitFingerprintReader  func(context.Context, string) (scanner.GitFingerprint, error)
	gitRepoStatusReader   func(context.Context, string) (scanner.GitRepoStatus, error)
	gitWorktreeInfoReader func(context.Context, string) (scanner.GitWorktreeInfo, error)
	gitWorktreeListReader func(context.Context, string) ([]scanner.GitWorktree, error)
	bus                   *events.Bus
}

type detectedProjectMove struct {
	OldPath     string
	NewPath     string
	Score       int
	SharedHeads []string
}

type ScanReport struct {
	At                    time.Time
	ActivityProjectCount  int
	TrackedProjectCount   int
	UpdatedProjects       []string
	QueuedClassifications int
	States                []model.ProjectState
}

type ScanOptions struct {
	ForceRetryFailedClassifications bool
}

func New(cfg config.AppConfig, st *store.Store, bus *events.Bus, detectorList []detectors.Detector) *Service {
	svc := &Service{
		cfg:                    cfg,
		store:                  st,
		bus:                    bus,
		detectors:              detectorList,
		backendDetector:        aibackend.DetectStatus,
		commitAssistantTimeout: defaultCommitAssistantTimeout,
		llmUsageTracker:        llm.NewUsageTracker(),
		bossChatUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery:      llm.NewOpenCodeDiscovery(),
		gitFingerprintReader:   scanner.ReadGitFingerprint,
		gitRepoStatusReader:    scanner.ReadGitRepoStatus,
		gitWorktreeInfoReader:  scanner.ReadGitWorktreeInfo,
		gitWorktreeListReader:  scanner.ListGitWorktrees,
		gitRepoInitializer:     runGitInit,
	}
	svc.configureAIClientsLocked()
	svc.bestEffortPrepareInternalWorkspaceState()
	return svc
}

func (s *Service) Store() *store.Store {
	return s.store
}

func (s *Service) Bus() *events.Bus {
	return s.bus
}

func (s *Service) currentSessionClassifier() SessionClassifier {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.classifier
}

func (s *Service) currentTodoSuggester() *todoworktree.Manager {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.todoSuggester
}

func (s *Service) currentCommitMessageSuggester() gitops.CommitMessageSuggester {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitMessageSuggester
}

func (s *Service) currentUntrackedFileRecommender() gitops.UntrackedFileRecommender {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.untrackedFileRecommender
}

func (s *Service) currentOpenCodeDiscovery() *llm.OpenCodeDiscovery {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opencodeDiscovery
}

func (s *Service) Config() config.AppConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAppConfig(s.cfg)
}

func cloneAppConfig(cfg config.AppConfig) config.AppConfig {
	cloned := cfg
	cloned.IncludePaths = append([]string(nil), cfg.IncludePaths...)
	cloned.ExcludePaths = append([]string(nil), cfg.ExcludePaths...)
	cloned.ExcludeProjectPatterns = append([]string(nil), cfg.ExcludeProjectPatterns...)
	cloned.PrivacyPatterns = append([]string(nil), cfg.PrivacyPatterns...)
	cloned.RecentCodexModels = append([]string(nil), cfg.RecentCodexModels...)
	cloned.RecentClaudeModels = append([]string(nil), cfg.RecentClaudeModels...)
	cloned.RecentOpenCodeModels = append([]string(nil), cfg.RecentOpenCodeModels...)
	cloned.PlaywrightPolicy = cfg.PlaywrightPolicy.Normalize()
	return cloned
}

func withScanGitMetadataTimeout[T any](reader func(context.Context, string) (T, error), timedOutPaths map[string]struct{}) func(context.Context, string) (T, error) {
	if reader == nil {
		return nil
	}
	return func(parent context.Context, path string) (T, error) {
		var zero T
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if timedOutPaths != nil {
			if _, ok := timedOutPaths[cleanPath]; ok {
				return zero, fmt.Errorf("skipping git metadata read for %s after earlier timeout", cleanPath)
			}
		}
		timeout := scanGitMetadataTimeout
		if timeout <= 0 {
			return reader(parent, path)
		}
		if deadline, ok := parent.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return zero, parent.Err()
			}
			if remaining < timeout {
				timeout = remaining
			}
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		value, err := reader(ctx, path)
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if timedOutPaths != nil {
				timedOutPaths[cleanPath] = struct{}{}
			}
			return zero, fmt.Errorf("git metadata read timed out for %s after %s: %w", path, timeout, ctx.Err())
		}
		return value, err
	}
}

func (s *Service) runtimeSnapshot() serviceRuntimeSnapshot {
	if s == nil {
		return serviceRuntimeSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return serviceRuntimeSnapshot{
		cfg:                   cloneAppConfig(s.cfg),
		classifier:            s.classifier,
		gitFingerprintReader:  s.gitFingerprintReader,
		gitRepoStatusReader:   s.gitRepoStatusReader,
		gitWorktreeInfoReader: s.gitWorktreeInfoReader,
		gitWorktreeListReader: s.gitWorktreeListReader,
		bus:                   s.bus,
	}
}

func (s *Service) lockProjectStateMutation(projectPath string) func() {
	if s == nil {
		return func() {}
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return func() {}
	}
	return s.projectStateLocks.Lock(projectPath)
}

func (s *Service) SetSessionClassifier(classifier SessionClassifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.classifier = classifier
}

func (s *Service) ApplyEditableSettings(settings config.EditableSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reconfigureAIClients := editableSettingsRequireAIClientRefresh(s.cfg, settings)
	currentBackend := s.cfg.EffectiveAIBackend()
	nextBackend := config.ResolveAIBackend(settings.AIBackend, settings.OpenAIAPIKey)
	currentBossChatBackend := s.cfg.EffectiveBossChatBackend()
	nextBossChatBackend := config.ResolveBossChatBackend(settings.BossChatBackend, settings.OpenAIAPIKey)

	s.cfg.AIBackend = settings.AIBackend
	s.cfg.BossChatBackend = settings.BossChatBackend
	s.cfg.BossChatModel = strings.TrimSpace(settings.BossChatModel)
	s.cfg.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
	s.cfg.MLXBaseURL = strings.TrimSpace(settings.MLXBaseURL)
	s.cfg.MLXAPIKey = strings.TrimSpace(settings.MLXAPIKey)
	s.cfg.MLXModel = strings.TrimSpace(settings.MLXModel)
	s.cfg.OllamaBaseURL = strings.TrimSpace(settings.OllamaBaseURL)
	s.cfg.OllamaAPIKey = strings.TrimSpace(settings.OllamaAPIKey)
	s.cfg.OllamaModel = strings.TrimSpace(settings.OllamaModel)
	s.cfg.IncludePaths = append([]string(nil), settings.IncludePaths...)
	s.cfg.ExcludePaths = append([]string(nil), settings.ExcludePaths...)
	s.cfg.ExcludeProjectPatterns = append([]string(nil), settings.ExcludeProjectPatterns...)
	s.cfg.EmbeddedCodexModel = strings.TrimSpace(settings.EmbeddedCodexModel)
	s.cfg.EmbeddedCodexReasoning = strings.TrimSpace(settings.EmbeddedCodexReasoning)
	s.cfg.EmbeddedClaudeModel = strings.TrimSpace(settings.EmbeddedClaudeModel)
	s.cfg.EmbeddedClaudeReasoning = strings.TrimSpace(settings.EmbeddedClaudeReasoning)
	s.cfg.EmbeddedOpenCodeModel = strings.TrimSpace(settings.EmbeddedOpenCodeModel)
	s.cfg.EmbeddedOpenCodeReasoning = strings.TrimSpace(settings.EmbeddedOpenCodeReasoning)
	s.cfg.OpenCodeModelTier = strings.TrimSpace(settings.OpenCodeModelTier)
	s.cfg.CodexLaunchPreset = settings.CodexLaunchPreset
	s.cfg.PlaywrightPolicy = settings.PlaywrightPolicy.Normalize()
	s.cfg.ScanInterval = settings.ScanInterval
	s.cfg.ActiveThreshold = settings.ActiveThreshold
	s.cfg.StuckThreshold = settings.StuckThreshold
	if reconfigureAIClients {
		s.configureAIClientsLocked()
	}
	if currentBackend != nextBackend {
		s.resetSessionUsageLocked()
	}
	if currentBossChatBackend != nextBossChatBackend {
		s.resetBossChatUsageLocked()
	}
}

func (s *Service) resetSessionUsageLocked() {
	if s.llmUsageTracker != nil {
		s.llmUsageTracker.Reset()
	}
	if resetter, ok := s.classifier.(interface{ ResetUsage() }); ok {
		resetter.ResetUsage()
	}
}

func (s *Service) resetBossChatUsageLocked() {
	if s.bossChatUsageTracker != nil {
		s.bossChatUsageTracker.Reset()
	}
}

func editableSettingsRequireAIClientRefresh(current config.AppConfig, settings config.EditableSettings) bool {
	if current.EffectiveAIBackend() != settings.AIBackend {
		return true
	}
	if strings.TrimSpace(current.OpenAIAPIKey) != strings.TrimSpace(settings.OpenAIAPIKey) {
		return true
	}
	if strings.TrimSpace(current.MLXBaseURL) != strings.TrimSpace(settings.MLXBaseURL) {
		return true
	}
	if strings.TrimSpace(current.MLXAPIKey) != strings.TrimSpace(settings.MLXAPIKey) {
		return true
	}
	if strings.TrimSpace(current.MLXModel) != strings.TrimSpace(settings.MLXModel) {
		return true
	}
	if strings.TrimSpace(current.OllamaBaseURL) != strings.TrimSpace(settings.OllamaBaseURL) {
		return true
	}
	if strings.TrimSpace(current.OllamaAPIKey) != strings.TrimSpace(settings.OllamaAPIKey) {
		return true
	}
	if strings.TrimSpace(current.OllamaModel) != strings.TrimSpace(settings.OllamaModel) {
		return true
	}
	return strings.TrimSpace(current.OpenCodeModelTier) != strings.TrimSpace(settings.OpenCodeModelTier)
}

func (s *Service) StartSessionClassifier(ctx context.Context) {
	classifier := s.currentSessionClassifier()
	if classifier == nil {
		return
	}
	classifier.Start(ctx)
}

func (s *Service) StartBackgroundDiscovery(ctx context.Context) {
	discovery := s.currentOpenCodeDiscovery()
	if discovery == nil {
		return
	}
	go func() {
		_, _ = ctx.Deadline()
		bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = discovery.Discover(bgCtx)
	}()
}

func (s *Service) StartTodoWorktreeSuggester(ctx context.Context) {
	suggester := s.currentTodoSuggester()
	if suggester == nil {
		return
	}
	suggester.Start(ctx)
}

func (s *Service) HasTodoWorktreeSuggester() bool {
	suggester := s.currentTodoSuggester()
	if suggester == nil {
		return false
	}
	return suggester.Enabled()
}

func (s *Service) HasSessionClassifier() bool {
	classifier := s.currentSessionClassifier()
	if classifier == nil {
		return false
	}
	if enabled, ok := classifier.(interface{ Enabled() bool }); ok {
		return enabled.Enabled()
	}
	return true
}

func (s *Service) SessionUsage() model.LLMSessionUsage {
	s.mu.Lock()
	classifier := s.classifier
	commitMessageSuggesterConfigured := s.commitMessageSuggester != nil
	usageTracker := s.llmUsageTracker
	s.mu.Unlock()

	classifierEnabled := false
	if classifier != nil {
		if enabled, ok := classifier.(interface{ Enabled() bool }); ok {
			classifierEnabled = enabled.Enabled()
		} else {
			classifierEnabled = true
		}
	}
	enabled := classifierEnabled || commitMessageSuggesterConfigured
	if usageTracker != nil {
		snapshot := usageTracker.Snapshot(enabled)
		if hasMeaningfulLLMUsage(snapshot) {
			return snapshot
		}
	}
	if classifier == nil {
		if enabled {
			return model.LLMSessionUsage{Enabled: true}
		}
		return model.LLMSessionUsage{}
	}
	if usageReader, ok := classifier.(interface{ UsageSnapshot() model.LLMSessionUsage }); ok {
		return usageReader.UsageSnapshot()
	}
	return model.LLMSessionUsage{Enabled: enabled}
}

func (s *Service) NewBossTextRunner() (llm.TextRunner, string, config.AIBackend) {
	if s == nil {
		return nil, "", config.AIBackendUnset
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()

	backend := cfg.EffectiveBossChatBackend()
	modelName := configuredBossAssistantModelForBackend(cfg, backend)
	switch backend {
	case config.AIBackendOpenAIAPI:
		return llm.NewResponsesTextClient(strings.TrimSpace(cfg.OpenAIAPIKey), bossAssistantHTTPTimeout, usageTracker), modelName, backend
	case config.AIBackendMLX, config.AIBackendOllama:
		return llm.NewOpenAICompatibleTextRunner(cfg.OpenAICompatibleBaseURL(backend), cfg.OpenAICompatibleAPIKey(backend), modelName, bossAssistantHTTPTimeout, usageTracker), modelName, backend
	default:
		return nil, modelName, backend
	}
}

func (s *Service) NewBossJSONRunner() (llm.JSONSchemaRunner, string, config.AIBackend) {
	if s == nil {
		return nil, "", config.AIBackendUnset
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()

	backend := cfg.EffectiveBossChatBackend()
	modelName := configuredBossAssistantModelForBackend(cfg, backend)
	switch backend {
	case config.AIBackendOpenAIAPI:
		return llm.NewResponsesClient(strings.TrimSpace(cfg.OpenAIAPIKey), bossAssistantHTTPTimeout, usageTracker), modelName, backend
	case config.AIBackendMLX, config.AIBackendOllama:
		return llm.NewOpenAICompatibleResponsesRunner(cfg.OpenAICompatibleBaseURL(backend), cfg.OpenAICompatibleAPIKey(backend), modelName, bossAssistantHTTPTimeout, usageTracker), modelName, backend
	default:
		return nil, modelName, backend
	}
}

func configuredBossAssistantModel(cfg config.AppConfig) string {
	return configuredBossAssistantModelForBackend(cfg, cfg.EffectiveBossChatBackend())
}

func configuredBossAssistantModelForBackend(cfg config.AppConfig, backend config.AIBackend) string {
	if modelName := strings.TrimSpace(os.Getenv(brand.BossAssistantModelEnvVar)); modelName != "" {
		return modelName
	}
	switch backend {
	case config.AIBackendMLX, config.AIBackendOllama:
		if modelName := strings.TrimSpace(cfg.OpenAICompatibleModel(backend)); modelName != "" {
			return modelName
		}
	}
	if modelName := strings.TrimSpace(cfg.BossChatModel); modelName != "" {
		return modelName
	}
	switch backend {
	case config.AIBackendMLX, config.AIBackendOllama:
		return ""
	}
	return defaultBossAssistantModel
}

func (s *Service) configureAIClientsLocked() {
	var (
		client          sessionclassify.Classifier
		commitAssistant *gitops.OpenAICommitMessageClient
		todoClient      todoworktree.Suggester
		selectedBackend = s.cfg.EffectiveAIBackend()
		detector        = s.backendDetector
		selectedStatus  aibackend.Status
	)
	if detector == nil {
		detector = aibackend.DetectStatus
	}
	selectedStatus = detector(context.Background(), s.cfg, selectedBackend)
	switch selectedBackend {
	case config.AIBackendOpenAIAPI:
		apiKey := strings.TrimSpace(s.cfg.OpenAIAPIKey)
		commitAssistant = gitops.NewOpenAICommitMessageClientWithUsageTracker(apiKey, s.llmUsageTracker)
		client = sessionclassify.NewOpenAIClientWithUsageTracker(apiKey, s.llmUsageTracker)
		todoClient = todoworktree.NewOpenAIClientWithUsageTracker(apiKey, s.llmUsageTracker)
	case config.AIBackendCodex:
		if selectedStatus.Ready {
			commitAssistant = gitops.NewCodexCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			client = sessionclassify.NewCodexClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			todoClient = todoworktree.NewCodexClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
		}
	case config.AIBackendOpenCode:
		if selectedStatus.Ready {
			tier, _ := config.ParseModelTier(s.cfg.OpenCodeModelTier)
			if s.opencodeDiscovery != nil {
				commitAssistant = gitops.NewOpenCodeCommitMessageClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
				client = sessionclassify.NewOpenCodeClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
				todoClient = todoworktree.NewOpenCodeClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
			} else {
				commitAssistant = gitops.NewOpenCodeCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
				client = sessionclassify.NewOpenCodeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
				todoClient = todoworktree.NewOpenCodeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			}
		}
	case config.AIBackendClaude:
		if selectedStatus.Ready {
			commitAssistant = gitops.NewClaudeCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			client = sessionclassify.NewClaudeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			todoClient = todoworktree.NewClaudeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
		}
	case config.AIBackendMLX, config.AIBackendOllama:
		if selectedStatus.Ready {
			baseURL := s.cfg.OpenAICompatibleBaseURL(selectedBackend)
			apiKey := s.cfg.OpenAICompatibleAPIKey(selectedBackend)
			model := s.cfg.OpenAICompatibleModel(selectedBackend)
			commitAssistant = gitops.NewOpenAICompatibleCommitMessageClientWithUsageTracker(baseURL, apiKey, model, s.llmUsageTracker)
			client = sessionclassify.NewOpenAICompatibleClientWithUsageTracker(baseURL, apiKey, model, s.llmUsageTracker)
			todoClient = todoworktree.NewOpenAICompatibleClientWithUsageTracker(baseURL, apiKey, model, s.llmUsageTracker)
		}
	}
	s.commitMessageSuggester = commitAssistant
	s.untrackedFileRecommender = commitAssistant
	if manager, ok := s.classifier.(*sessionclassify.Manager); ok {
		manager.ConfigureClient(client)
		manager.Notify()
	} else {
		s.classifier = sessionclassify.NewManager(s.store, s.bus, sessionclassify.Options{
			Client:           client,
			OnProjectUpdated: s.RefreshProjectStatus,
		})
	}
	if s.todoSuggester == nil {
		s.todoSuggester = todoworktree.NewManager(s.store, s.bus, todoworktree.Options{
			Client: todoClient,
		})
		return
	}
	s.todoSuggester.ConfigureClient(todoClient)
}

func hasMeaningfulLLMUsage(snapshot model.LLMSessionUsage) bool {
	return snapshot.Started > 0 ||
		snapshot.Completed > 0 ||
		snapshot.Failed > 0 ||
		snapshot.Running > 0 ||
		strings.TrimSpace(snapshot.Model) != "" ||
		snapshot.Totals.InputTokens > 0 ||
		snapshot.Totals.OutputTokens > 0 ||
		snapshot.Totals.TotalTokens > 0 ||
		snapshot.Totals.CachedInputTokens > 0 ||
		snapshot.Totals.ReasoningTokens > 0 ||
		snapshot.Totals.EstimatedCostUSD > 0
}

func (s *Service) bestEffortPrepareInternalWorkspaceState() {
	internalWorkspaceRoot := appfs.InternalWorkspaceRoot(s.cfg.DataDir)
	_ = appfs.CleanupStaleInternalWorkspaces(s.cfg.DataDir, 24*time.Hour)
	if s.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summaries, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return
	}
	for path, summary := range summaries {
		if !appfs.IsManagedInternalPath(path, []string{internalWorkspaceRoot}) {
			continue
		}
		if summary.InScope {
			_ = s.store.SetProjectScope(ctx, path, false)
		}
		if !summary.Forgotten {
			_ = s.store.SetForgotten(ctx, path, true)
		}
	}
}

func (s *Service) ScanOnce(ctx context.Context) (ScanReport, error) {
	return s.ScanWithOptions(ctx, ScanOptions{})
}

func (s *Service) ScanWithOptions(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	runtime := s.runtimeSnapshot()
	cfg := runtime.cfg
	classifier := runtime.classifier
	timedOutGitPaths := map[string]struct{}{}
	gitFingerprintReader := withScanGitMetadataTimeout(runtime.gitFingerprintReader, timedOutGitPaths)
	gitRepoStatusReader := withScanGitMetadataTimeout(runtime.gitRepoStatusReader, timedOutGitPaths)
	gitWorktreeInfoReader := withScanGitMetadataTimeout(runtime.gitWorktreeInfoReader, timedOutGitPaths)
	gitWorktreeListReader := withScanGitMetadataTimeout(runtime.gitWorktreeListReader, timedOutGitPaths)
	bus := runtime.bus
	now := time.Now()
	internalWorkspaceRoot := appfs.InternalWorkspaceRoot(cfg.DataDir)
	scope := scanner.NewPathScope(cfg.IncludePaths, cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot)
	discovered := []string{}
	var err error
	if len(cfg.IncludePaths) > 0 {
		discovered, err = scanner.DiscoverGitProjects(scanner.Discovery{
			Roots:     cfg.IncludePaths,
			MaxDepth:  4,
			SkipPaths: []string{internalWorkspaceRoot},
		})
		if err != nil {
			return ScanReport{}, fmt.Errorf("discover git projects: %w", err)
		}
	}

	oldMap, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return ScanReport{}, fmt.Errorf("load previous project state: %w", err)
	}
	discovered, liveWorktreePathsByRoot := s.expandDiscoveredWorktreePaths(ctx, discovered, oldMap, scope, gitWorktreeInfoReader, gitWorktreeListReader)
	for path, old := range oldMap {
		inScopeNow := scope.Allows(path) || old.ManuallyAdded
		if old.InScope == inScopeNow {
			continue
		}
		if err := s.store.SetProjectScope(ctx, path, inScopeNow); err != nil {
			return ScanReport{}, fmt.Errorf("set project scope: %w", err)
		}
		old.InScope = inScopeNow
		oldMap[path] = old
	}

	rawActivities, err := s.detectProjectActivities(ctx, scope, internalWorkspaceRoot)
	if err != nil {
		return ScanReport{}, err
	}
	if len(cfg.IncludePaths) > 0 {
		fullScopeActivities, err := s.detectProjectActivities(ctx, scanner.NewPathScope(nil, cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot), internalWorkspaceRoot)
		if err != nil {
			return ScanReport{}, err
		}
		for path, activity := range fullScopeActivities {
			if !isRecentSessionActivity(now, activity, recentActivityDiscoveryWindow) {
				continue
			}
			if existing, ok := rawActivities[path]; ok {
				mergeDetectorActivities(existing, activity)
				continue
			}
			rawActivities[path] = activity
		}
	}
	finalizeDetectorActivities(rawActivities)

	cachedFingerprints, err := s.store.GetProjectGitFingerprints(ctx)
	if err != nil {
		return ScanReport{}, fmt.Errorf("load cached git fingerprints: %w", err)
	}

	currentFingerprints := map[string]scanner.GitFingerprint{}
	discoveredSet := map[string]struct{}{}
	for _, path := range discovered {
		cleanPath := filepath.Clean(path)
		discoveredSet[cleanPath] = struct{}{}
		if gitFingerprintReader == nil || !projectPathExists(cleanPath) {
			continue
		}
		fingerprint, readErr := gitFingerprintReader(ctx, cleanPath)
		if readErr == nil {
			currentFingerprints[cleanPath] = fingerprint
		}
	}

	moves := s.detectProjectMoves(oldMap, discoveredSet, cachedFingerprints, currentFingerprints)
	for _, move := range moves {
		if err := s.store.MoveProjectPath(ctx, move.OldPath, move.NewPath, now); err != nil {
			return ScanReport{}, fmt.Errorf("move project path %s -> %s: %w", move.OldPath, move.NewPath, err)
		}
		if err := s.store.UpsertPathAlias(ctx, model.PathAlias{
			OldPath:   move.OldPath,
			NewPath:   move.NewPath,
			Reason:    "git_recent_hash_match",
			UpdatedAt: now,
		}); err != nil {
			return ScanReport{}, fmt.Errorf("persist path alias %s -> %s: %w", move.OldPath, move.NewPath, err)
		}
		s.publishProjectMoved(ctx, now, move)
	}
	if len(moves) > 0 {
		oldMap, err = s.store.GetProjectSummaryMap(ctx)
		if err != nil {
			return ScanReport{}, fmt.Errorf("reload project state after moves: %w", err)
		}
	}

	aliases, err := s.store.GetPathAliases(ctx)
	if err != nil {
		return ScanReport{}, fmt.Errorf("load path aliases: %w", err)
	}

	activities := map[string]*model.DetectorProjectActivity{}
	for path, activity := range rawActivities {
		canonicalPath := resolveProjectPath(filepath.Clean(path), aliases)
		normalized := normalizeActivity(activity, canonicalPath)
		existing, ok := activities[canonicalPath]
		if !ok {
			activities[canonicalPath] = normalized
			continue
		}
		if normalized.LastActivity.After(existing.LastActivity) {
			existing.LastActivity = normalized.LastActivity
		}
		existing.ErrorCount += normalized.ErrorCount
		existing.Sessions = append(existing.Sessions, normalized.Sessions...)
		existing.Artifacts = append(existing.Artifacts, normalized.Artifacts...)
	}
	reconcileGlobalSessionOwnership(activities)
	finalizeDetectorActivities(activities)
	propagateWorktreeActivitiesToRoot(activities, oldMap)

	candidateSet := map[string]struct{}{}
	for p := range activities {
		candidateSet[filepath.Clean(p)] = struct{}{}
	}
	for path, old := range oldMap {
		if !old.InScope {
			continue
		}
		candidateSet[filepath.Clean(path)] = struct{}{}
	}

	paths := make([]string, 0, len(candidateSet))
	for p := range candidateSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	currentRepoStatus := map[string]scanner.GitRepoStatus{}
	currentWorktreeInfo := map[string]scanner.GitWorktreeInfo{}
	for _, path := range paths {
		if !projectPathExists(path) {
			continue
		}
		if gitFingerprintReader != nil {
			fingerprint, readErr := gitFingerprintReader(ctx, path)
			if readErr == nil {
				if err := s.store.UpsertProjectGitFingerprint(ctx, model.ProjectGitFingerprint{
					ProjectPath:  path,
					HeadHash:     fingerprint.HeadHash,
					RecentHashes: append([]string(nil), fingerprint.RecentHashes...),
					UpdatedAt:    now,
				}); err != nil {
					return ScanReport{}, fmt.Errorf("persist git fingerprint: %w", err)
				}
			}
		}
		if gitRepoStatusReader != nil {
			repoStatus, readErr := gitRepoStatusReader(ctx, path)
			if readErr == nil {
				currentRepoStatus[path] = repoStatus
			}
		}
		if gitWorktreeInfoReader != nil {
			worktreeInfo, readErr := gitWorktreeInfoReader(ctx, path)
			if readErr == nil {
				currentWorktreeInfo[path] = worktreeInfo
			}
		}
	}

	updated := []string{}
	queuedClassifications := 0
	states := make([]model.ProjectState, 0, len(paths))

	for _, path := range paths {
		unlockProjectState := s.lockProjectStateMutation(path)
		activity := activities[path]
		old := oldMap[path]
		currentDetail, haveCurrentDetail, err := s.currentProjectDetailForScan(ctx, path)
		if err != nil {
			unlockProjectState()
			return ScanReport{}, err
		}
		if haveCurrentDetail {
			old = currentDetail.Summary
		}
		if old.SnoozedUntil != nil && now.After(*old.SnoozedUntil) {
			if err := s.store.SetSnooze(ctx, path, nil); err != nil {
				unlockProjectState()
				return ScanReport{}, fmt.Errorf("clear expired snooze: %w", err)
			}
			old.SnoozedUntil = nil
		}
		projectKind := model.NormalizeProjectKind(old.Kind)
		projectName := strings.TrimSpace(old.Name)
		if projectName == "" {
			projectName = filepath.Base(path)
		}
		presentOnDisk := projectPathExists(path)
		isGitRepo := presentOnDisk && projectIsGitRepo(path)
		worktreeRootPath := old.WorktreeRootPath
		worktreeKind := old.WorktreeKind
		worktreeParentBranch := old.WorktreeParentBranch
		worktreeMergeStatus := old.WorktreeMergeStatus
		if presentOnDisk && !isGitRepo {
			worktreeRootPath = ""
			worktreeKind = model.WorktreeKindNone
			worktreeParentBranch = ""
			worktreeMergeStatus = model.WorktreeMergeStatus("")
		}
		repoBranch := ""
		repoDirty := false
		repoConflict := false
		repoSyncStatus := model.RepoSyncStatus("")
		repoAheadCount := 0
		repoBehindCount := 0
		if presentOnDisk {
			if worktreeInfo, ok := currentWorktreeInfo[path]; ok {
				worktreeRootPath = filepath.Clean(strings.TrimSpace(worktreeInfo.RootPath))
				worktreeKind = modelWorktreeKindFromGit(worktreeInfo.Kind)
			}
			if repoStatus, ok := currentRepoStatus[path]; ok {
				repoBranch = strings.TrimSpace(repoStatus.Branch)
				repoDirty = repoStatus.Dirty
				repoConflict = repoConflictFromGit(repoStatus)
				repoSyncStatus = repoSyncStatusFromGit(repoStatus)
				repoAheadCount = repoStatus.Ahead
				repoBehindCount = repoStatus.Behind
			} else if isGitRepo {
				repoBranch = old.RepoBranch
				repoDirty = old.RepoDirty
				repoConflict = old.RepoConflict
				repoSyncStatus = old.RepoSyncStatus
				repoAheadCount = old.RepoAheadCount
				repoBehindCount = old.RepoBehindCount
			}
			worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, worktreeRootPath, worktreeKind, repoBranch, worktreeParentBranch)
		}
		forgotten := old.Forgotten
		staleLinkedWorktree := false
		if worktreeKind == model.WorktreeKindLinked && liveLinkedWorktreeMissing(liveWorktreePathsByRoot, worktreeRootPath, path) {
			staleLinkedWorktree = true
			forgotten = true
		}
		if presentOnDisk && forgotten && scope.Allows(path) && !staleLinkedWorktree {
			forgotten = false
		}
		if forgotten && !presentOnDisk {
			if err := s.store.SetForgotten(ctx, path, true); err != nil {
				unlockProjectState()
				return ScanReport{}, fmt.Errorf("mark forgotten worktree: %w", err)
			}
			if err := s.store.SetProjectPresence(ctx, path, false); err != nil {
				unlockProjectState()
				return ScanReport{}, fmt.Errorf("mark missing worktree: %w", err)
			}
			unlockProjectState()
			continue
		}
		latestSessionStart := time.Time{}
		latestTurnKnown := false
		latestTurnComplete := false
		sessions := []model.SessionEvidence{}
		artifacts := []model.ArtifactEvidence{}
		errorCount := 0
		lastActivity := time.Time{}
		hasActivity := false

		if activity != nil {
			hasActivity = !activity.LastActivity.IsZero()
			lastActivity = activity.LastActivity
			sessions = dedupeSessions(activity.Sessions)
			artifacts = dedupeArtifacts(activity.Artifacts)
			errorCount = activity.ErrorCount
			if len(sessions) > 0 {
				gitStatus := sessionclassify.NewGitStatusSnapshot(repoDirty, repoSyncStatus, repoAheadCount, repoBehindCount)
				reuseLatestSessionTurnState(old, &sessions[0])
				ensureLatestSessionTurnState(&sessions[0])
				reuseLatestSessionSnapshotHash(old, &sessions[0], gitStatus)
				ensureSessionSnapshotHash(ctx, path, &sessions[0], gitStatus)
			}
		}
		if haveCurrentDetail {
			sessions = preserveCurrentSessionsNewerThan(sessions, currentDetail.Sessions, now)
			artifacts = preserveCurrentArtifactsNewerThan(artifacts, currentDetail.Artifacts, now)
			for _, session := range sessions {
				if session.LastEventAt.After(lastActivity) {
					lastActivity = session.LastEventAt
					hasActivity = true
				}
			}
		}
		if len(sessions) > 0 {
			latestSessionStart = sessions[0].StartedAt
			latestTurnKnown = sessions[0].LatestTurnStateKnown
			latestTurnComplete = sessions[0].LatestTurnCompleted
		}
		classificationKnown, classificationCategory := s.latestSessionClassification(ctx, path, sessions, now)

		score := attention.Score(attention.Input{
			Path:                       path,
			Now:                        now,
			LastActivity:               lastActivity,
			CreatedAt:                  old.CreatedAt,
			RepoDirty:                  repoDirty,
			Pinned:                     old.Pinned,
			Unread:                     attention.AssessmentUnread(old),
			SnoozedUntil:               old.SnoozedUntil,
			ErrorCount:                 errorCount,
			LatestSessionStart:         latestSessionStart,
			LatestTurnKnown:            latestTurnKnown,
			LatestTurnComplete:         latestTurnComplete,
			LatestSessionCategoryKnown: classificationKnown,
			LatestSessionCategory:      classificationCategory,
			HasActivity:                hasActivity,
			ActiveThreshold:            cfg.ActiveThreshold,
			StuckThreshold:             cfg.StuckThreshold,
			OpenTodoCount:              old.OpenTODOCount,
		})

		state := model.ProjectState{
			Path:                 path,
			Name:                 projectName,
			Kind:                 projectKind,
			LastActivity:         lastActivity,
			Status:               score.Status,
			AttentionScore:       score.Score,
			PresentOnDisk:        presentOnDisk,
			WorktreeRootPath:     worktreeRootPath,
			WorktreeKind:         worktreeKind,
			WorktreeParentBranch: worktreeParentBranch,
			WorktreeMergeStatus:  worktreeMergeStatus,
			WorktreeOriginTodoID: old.WorktreeOriginTodoID,
			RepoBranch:           repoBranch,
			RepoDirty:            repoDirty,
			RepoConflict:         repoConflict,
			RepoSyncStatus:       repoSyncStatus,
			RepoAheadCount:       repoAheadCount,
			RepoBehindCount:      repoBehindCount,
			Forgotten:            forgotten,
			ManuallyAdded:        old.ManuallyAdded,
			InScope:              scope.Allows(path) || old.ManuallyAdded,
			Pinned:               old.Pinned,
			SnoozedUntil:         old.SnoozedUntil,
			MovedFromPath:        old.MovedFromPath,
			MovedAt:              old.MovedAt,
			AttentionReason:      score.Reasons,
			Sessions:             sessions,
			Artifacts:            artifacts,
			UpdatedAt:            now,
		}

		if err := s.store.UpsertProjectState(ctx, state); err != nil {
			unlockProjectState()
			return ScanReport{}, fmt.Errorf("persist project state: %w", err)
		}
		if classifier != nil {
			queued, err := queueProjectClassification(ctx, classifier, state, opts)
			if err != nil {
				unlockProjectState()
				return ScanReport{}, fmt.Errorf("queue session classification: %w", err)
			}
			if queued {
				queuedClassifications++
			}
		}

		if projectStateChanged(old, state) {
			updated = append(updated, path)
			s.publishProjectChanged(ctx, now, state)
		}

		states = append(states, state)
		unlockProjectState()
	}

	report := ScanReport{
		At:                    now,
		ActivityProjectCount:  len(activities),
		TrackedProjectCount:   len(states),
		UpdatedProjects:       updated,
		QueuedClassifications: queuedClassifications,
		States:                states,
	}
	if queuedClassifications > 0 && classifier != nil {
		classifier.Notify()
	}

	if bus != nil {
		bus.Publish(events.Event{
			Type: events.ScanCompleted,
			At:   now,
			Payload: map[string]string{
				"updated":                fmt.Sprintf("%d", len(updated)),
				"queued_classifications": fmt.Sprintf("%d", queuedClassifications),
			},
		})
	}

	return report, nil
}

func (s *Service) currentProjectDetailForScan(ctx context.Context, path string) (model.ProjectDetail, bool, error) {
	detail, err := s.store.GetProjectDetail(ctx, path, 20)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.HasPrefix(err.Error(), "project not found:") {
			return model.ProjectDetail{}, false, nil
		}
		return model.ProjectDetail{}, false, fmt.Errorf("reload project state for scan: %w", err)
	}
	return detail, true, nil
}

func (s *Service) detectProjectActivities(ctx context.Context, scope scanner.PathScope, internalWorkspaceRoot string) (map[string]*model.DetectorProjectActivity, error) {
	out := map[string]*model.DetectorProjectActivity{}
	for _, detector := range s.detectors {
		if detector == nil {
			continue
		}
		activityByPath, err := detector.Detect(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("detect project activity (%s): %w", detector.Name(), err)
		}
		for rawPath, activity := range activityByPath {
			if activity == nil {
				continue
			}
			path := filepath.Clean(rawPath)
			if path == "." {
				path = filepath.Clean(activity.ProjectPath)
			}
			if path == "." || path == "" {
				continue
			}
			if appfs.IsManagedInternalPath(path, []string{internalWorkspaceRoot}) {
				continue
			}
			if existing, ok := out[path]; ok {
				mergeDetectorActivities(existing, activity)
				continue
			}
			activity.ProjectPath = path
			copyActivity := *activity
			copyActivity.Sessions = append([]model.SessionEvidence(nil), activity.Sessions...)
			copyActivity.Artifacts = append([]model.ArtifactEvidence(nil), activity.Artifacts...)
			out[path] = &copyActivity
		}
	}
	return out, nil
}

func isRecentSessionActivity(now time.Time, activity *model.DetectorProjectActivity, window time.Duration) bool {
	if activity == nil {
		return false
	}
	lastActivity := activity.LastActivity
	for _, session := range activity.Sessions {
		if session.LastEventAt.After(lastActivity) {
			lastActivity = session.LastEventAt
		}
	}
	if lastActivity.IsZero() {
		return false
	}
	return now.Sub(lastActivity) <= window
}

func mergeDetectorActivities(dst *model.DetectorProjectActivity, src *model.DetectorProjectActivity) {
	if dst == nil || src == nil {
		return
	}
	if src.ProjectPath != "" {
		dst.ProjectPath = filepath.Clean(src.ProjectPath)
	}
	dst.Sessions = append(dst.Sessions, src.Sessions...)
	dst.Artifacts = append(dst.Artifacts, src.Artifacts...)
	dst.ErrorCount += src.ErrorCount
	if src.LastActivity.After(dst.LastActivity) {
		dst.LastActivity = src.LastActivity
	}
	if dst.Source == "" {
		dst.Source = src.Source
	}
}

func finalizeDetectorActivities(activities map[string]*model.DetectorProjectActivity) {
	for path, activity := range activities {
		if activity == nil {
			delete(activities, path)
			continue
		}
		activity.ProjectPath = filepath.Clean(activity.ProjectPath)
		if activity.ProjectPath == "." || activity.ProjectPath == "" {
			activity.ProjectPath = filepath.Clean(path)
		}
		activity.Sessions = dedupeSessions(activity.Sessions)
		activity.Artifacts = dedupeArtifacts(activity.Artifacts)
		if !activity.LastActivity.IsZero() {
			for _, session := range activity.Sessions {
				if session.LastEventAt.After(activity.LastActivity) {
					activity.LastActivity = session.LastEventAt
				}
			}
			continue
		}
		for _, session := range activity.Sessions {
			if session.LastEventAt.After(activity.LastActivity) {
				activity.LastActivity = session.LastEventAt
			}
		}
		if len(activity.Artifacts) == 0 && len(activity.Sessions) == 0 {
			delete(activities, path)
		}
	}
}

// propagateWorktreeActivitiesToRoot ensures that activity detected in linked
// worktrees is reflected in the root project's LastActivity timestamp. Without
// this, work done exclusively in worktrees leaves the root showing a stale time.
func propagateWorktreeActivitiesToRoot(activities map[string]*model.DetectorProjectActivity, oldMap map[string]model.ProjectSummary) {
	for path, activity := range activities {
		if activity == nil || activity.LastActivity.IsZero() {
			continue
		}
		old, ok := oldMap[path]
		if !ok || old.WorktreeKind != model.WorktreeKindLinked {
			continue
		}
		rootPath := filepath.Clean(strings.TrimSpace(old.WorktreeRootPath))
		if rootPath == "" || rootPath == path {
			continue
		}
		existing, ok := activities[rootPath]
		if !ok {
			activities[rootPath] = &model.DetectorProjectActivity{
				ProjectPath:  rootPath,
				LastActivity: activity.LastActivity,
			}
			continue
		}
		if activity.LastActivity.After(existing.LastActivity) {
			existing.LastActivity = activity.LastActivity
		}
	}
}

func liveLinkedWorktreeMissing(livePathsByRoot map[string]map[string]struct{}, rootPath, projectPath string) bool {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if rootPath == "" || projectPath == "" {
		return false
	}
	livePaths := livePathsByRoot[rootPath]
	if len(livePaths) == 0 {
		return false
	}
	_, ok := livePaths[projectPath]
	return !ok
}

func (s *Service) staleLinkedWorktreeOnDisk(ctx context.Context, rootPath string, kind model.WorktreeKind, projectPath string) bool {
	return s.staleLinkedWorktreeOnDiskWithReader(ctx, rootPath, kind, projectPath, s.gitWorktreeListReader)
}

func (s *Service) staleLinkedWorktreeOnDiskWithReader(ctx context.Context, rootPath string, kind model.WorktreeKind, projectPath string, worktreeListReader func(context.Context, string) ([]scanner.GitWorktree, error)) bool {
	if s == nil || worktreeListReader == nil || kind != model.WorktreeKindLinked {
		return false
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if rootPath == "" || projectPath == "" {
		return false
	}
	worktrees, err := worktreeListReader(ctx, rootPath)
	if err != nil || len(worktrees) == 0 {
		return false
	}
	for _, worktree := range worktrees {
		if filepath.Clean(strings.TrimSpace(worktree.Path)) == projectPath {
			return false
		}
	}
	return true
}

func reconcileGlobalSessionOwnership(activities map[string]*model.DetectorProjectActivity) {
	type ownedSession struct {
		projectPath string
		session     model.SessionEvidence
	}

	if len(activities) == 0 {
		return
	}

	resolved := map[string]ownedSession{}
	for path, activity := range activities {
		if activity == nil {
			continue
		}
		ownerPath := filepath.Clean(path)
		for _, session := range activity.Sessions {
			if strings.TrimSpace(session.SessionID) == "" {
				continue
			}
			session.ProjectPath = ownerPath
			if existing, ok := resolved[session.SessionID]; ok {
				merged := mergeSessionEvidence(existing.session, session)
				resolved[session.SessionID] = ownedSession{
					projectPath: merged.ProjectPath,
					session:     merged,
				}
				continue
			}
			resolved[session.SessionID] = ownedSession{
				projectPath: ownerPath,
				session:     session,
			}
		}
		activity.Sessions = nil
		activity.ErrorCount = 0
		activity.LastActivity = time.Time{}
	}

	for _, item := range resolved {
		ownerPath := filepath.Clean(item.projectPath)
		if ownerPath == "" || ownerPath == "." {
			continue
		}
		entry, ok := activities[ownerPath]
		if !ok || entry == nil {
			entry = &model.DetectorProjectActivity{ProjectPath: ownerPath}
			activities[ownerPath] = entry
		}
		entry.ProjectPath = ownerPath
		entry.Sessions = append(entry.Sessions, item.session)
		entry.ErrorCount += item.session.ErrorCount
		if item.session.LastEventAt.After(entry.LastActivity) {
			entry.LastActivity = item.session.LastEventAt
		}
	}
}

func mergeSessionEvidence(existing, candidate model.SessionEvidence) model.SessionEvidence {
	existing = model.NormalizeSessionEvidenceIdentity(existing)
	candidate = model.NormalizeSessionEvidenceIdentity(candidate)
	if strings.TrimSpace(existing.SessionID) == "" {
		return candidate
	}

	preferred := existing
	other := candidate
	if preferSessionEvidence(candidate, existing) {
		preferred = candidate
		other = existing
	}

	merged := preferred
	if merged.ProjectPath == "" {
		merged.ProjectPath = other.ProjectPath
	}
	if merged.Source == model.SessionSourceUnknown {
		merged.Source = other.Source
	}
	if merged.RawSessionID == "" {
		merged.RawSessionID = other.RawSessionID
	}
	if merged.DetectedProjectPath == "" {
		merged.DetectedProjectPath = other.DetectedProjectPath
	}
	if merged.SessionFile == "" {
		merged.SessionFile = other.SessionFile
	}
	if merged.Format == "" {
		merged.Format = other.Format
	}
	if merged.SnapshotHash == "" {
		merged.SnapshotHash = other.SnapshotHash
	}
	if merged.StartedAt.IsZero() {
		merged.StartedAt = other.StartedAt
	}
	if other.LastEventAt.After(merged.LastEventAt) {
		merged.LastEventAt = other.LastEventAt
	}
	if other.ErrorCount > merged.ErrorCount {
		merged.ErrorCount = other.ErrorCount
	}
	if other.LatestTurnStateKnown {
		adoptTurnState := !merged.LatestTurnStateKnown
		if !adoptTurnState && other.LastEventAt.After(merged.LastEventAt) {
			adoptTurnState = true
		}
		// Let a settled turn override an unfinished one when both pieces of
		// evidence describe the same last event. This keeps explicit session
		// closures from resurfacing stale live timers until the next full scan.
		if !adoptTurnState && timesEqual(other.LastEventAt, merged.LastEventAt) && other.LatestTurnCompleted && !merged.LatestTurnCompleted {
			adoptTurnState = true
		}
		if adoptTurnState {
			merged.LatestTurnStateKnown = other.LatestTurnStateKnown
			merged.LatestTurnCompleted = other.LatestTurnCompleted
			merged.LatestTurnStartedAt = other.LatestTurnStartedAt
		}
	}
	if merged.LatestTurnStartedAt.IsZero() && !other.LatestTurnStartedAt.IsZero() {
		merged.LatestTurnStartedAt = other.LatestTurnStartedAt
	}
	return model.NormalizeSessionEvidenceIdentity(merged)
}

func preferSessionEvidence(candidate, existing model.SessionEvidence) bool {
	if candidate.LastEventAt.After(existing.LastEventAt) {
		return true
	}
	if existing.LastEventAt.After(candidate.LastEventAt) {
		return false
	}
	candidateScore := sessionEvidenceScore(candidate)
	existingScore := sessionEvidenceScore(existing)
	if candidateScore != existingScore {
		return candidateScore > existingScore
	}
	return strings.Compare(candidate.ProjectPath, existing.ProjectPath) < 0
}

func sessionEvidenceScore(session model.SessionEvidence) int {
	score := 0
	if strings.TrimSpace(session.SessionFile) != "" {
		score += 4
	}
	if !session.StartedAt.IsZero() {
		score += 2
	}
	if strings.TrimSpace(session.DetectedProjectPath) != "" {
		score += 2
	}
	if strings.TrimSpace(session.SnapshotHash) != "" {
		score += 2
	}
	if session.LatestTurnStateKnown {
		score += 2
	}
	if !session.LatestTurnStartedAt.IsZero() {
		score++
	}
	if session.ErrorCount > 0 {
		score++
	}
	return score
}

func queueProjectClassification(ctx context.Context, classifier SessionClassifier, state model.ProjectState, opts ScanOptions) (bool, error) {
	if classifier == nil {
		return false, nil
	}
	if opts.ForceRetryFailedClassifications {
		if retryer, ok := classifier.(sessionClassifierRetryer); ok {
			return retryer.QueueProjectRetry(ctx, state, 0)
		}
	}
	return classifier.QueueProject(ctx, state)
}

func dedupeSessions(in []model.SessionEvidence) []model.SessionEvidence {
	seen := map[string]model.SessionEvidence{}
	out := make([]model.SessionEvidence, 0, len(in))
	for _, s := range in {
		s = model.NormalizeSessionEvidenceIdentity(s)
		if s.SessionID == "" {
			continue
		}
		if existing, ok := seen[s.SessionID]; ok {
			seen[s.SessionID] = mergeSessionEvidence(existing, s)
			continue
		}
		seen[s.SessionID] = s
	}
	for _, session := range seen {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastEventAt.After(out[j].LastEventAt)
	})
	return out
}

func preserveCurrentSessionsNewerThan(in, current []model.SessionEvidence, cutoff time.Time) []model.SessionEvidence {
	if len(current) == 0 || cutoff.IsZero() {
		return in
	}
	out := append([]model.SessionEvidence(nil), in...)
	for _, session := range current {
		if timeAtOrAfterStorageSecond(session.LastEventAt, cutoff) {
			out = append(out, session)
		}
	}
	return dedupeSessions(out)
}

func dedupeArtifacts(in []model.ArtifactEvidence) []model.ArtifactEvidence {
	seen := map[string]struct{}{}
	out := make([]model.ArtifactEvidence, 0, len(in))
	for _, a := range in {
		key := a.Kind + "|" + a.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func preserveCurrentArtifactsNewerThan(in, current []model.ArtifactEvidence, cutoff time.Time) []model.ArtifactEvidence {
	if len(current) == 0 || cutoff.IsZero() {
		return in
	}
	out := append([]model.ArtifactEvidence(nil), in...)
	for _, artifact := range current {
		if timeAtOrAfterStorageSecond(artifact.UpdatedAt, cutoff) {
			out = append(out, artifact)
		}
	}
	return dedupeArtifacts(out)
}

func timeAtOrAfterStorageSecond(value, cutoff time.Time) bool {
	if value.IsZero() || cutoff.IsZero() {
		return false
	}
	return value.Unix() >= cutoff.Unix()
}

func timesEqual(a, b time.Time) bool {
	if a.IsZero() && b.IsZero() {
		return true
	}
	return a.Unix() == b.Unix()
}

func projectStateChanged(old model.ProjectSummary, state model.ProjectState) bool {
	return old.Path == "" ||
		old.Name != state.Name ||
		model.NormalizeProjectKind(old.Kind) != model.NormalizeProjectKind(state.Kind) ||
		old.Status != state.Status ||
		old.AttentionScore != state.AttentionScore ||
		old.PresentOnDisk != state.PresentOnDisk ||
		old.WorktreeRootPath != state.WorktreeRootPath ||
		old.WorktreeKind != state.WorktreeKind ||
		old.WorktreeParentBranch != state.WorktreeParentBranch ||
		old.WorktreeMergeStatus != state.WorktreeMergeStatus ||
		old.RepoBranch != state.RepoBranch ||
		old.RepoDirty != state.RepoDirty ||
		old.RepoConflict != state.RepoConflict ||
		old.RepoSyncStatus != state.RepoSyncStatus ||
		old.RepoAheadCount != state.RepoAheadCount ||
		old.RepoBehindCount != state.RepoBehindCount ||
		old.Forgotten != state.Forgotten ||
		old.ManuallyAdded != state.ManuallyAdded ||
		!timesEqual(old.LastActivity, state.LastActivity)
}

func (s *Service) publishProjectChanged(ctx context.Context, now time.Time, state model.ProjectState) {
	payload := map[string]string{
		"status":   string(state.Status),
		"score":    fmt.Sprintf("%d", state.AttentionScore),
		"dirty":    fmt.Sprintf("%t", state.RepoDirty),
		"conflict": fmt.Sprintf("%t", state.RepoConflict),
		"merged":   string(state.WorktreeMergeStatus),
		"remote":   string(state.RepoSyncStatus),
	}
	s.bus.Publish(events.Event{Type: events.ProjectChanged, At: now, ProjectPath: state.Path, Payload: payload})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: state.Path,
		Type:        string(events.ProjectChanged),
		Payload:     fmt.Sprintf("status=%s score=%d dirty=%t conflict=%t merged=%s remote=%s", state.Status, state.AttentionScore, state.RepoDirty, state.RepoConflict, state.WorktreeMergeStatus, state.RepoSyncStatus),
	})
}

func repoSyncStatusFromGit(status scanner.GitRepoStatus) model.RepoSyncStatus {
	if !status.HasRemote {
		return model.RepoSyncNoRemote
	}
	if !status.HasUpstream {
		return model.RepoSyncNoUpstream
	}
	switch {
	case status.Ahead > 0 && status.Behind > 0:
		return model.RepoSyncDiverged
	case status.Ahead > 0:
		return model.RepoSyncAhead
	case status.Behind > 0:
		return model.RepoSyncBehind
	default:
		return model.RepoSyncSynced
	}
}

func repoConflictFromGit(status scanner.GitRepoStatus) bool {
	for _, change := range status.Changes {
		if change.Kind == scanner.GitChangeUnmerged {
			return true
		}
	}
	return false
}

func (s *Service) publishProjectMoved(ctx context.Context, now time.Time, move detectedProjectMove) {
	payload := map[string]string{
		"from":  move.OldPath,
		"to":    move.NewPath,
		"score": fmt.Sprintf("%d", move.Score),
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: events.ProjectMoved, At: now, ProjectPath: move.NewPath, Payload: payload})
	}
	message := fmt.Sprintf("moved from=%s score=%d", move.OldPath, move.Score)
	if len(move.SharedHeads) > 0 {
		message = fmt.Sprintf("%s hashes=%s", message, strings.Join(shortHashes(move.SharedHeads), ","))
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: move.NewPath,
		Type:        string(events.ProjectMoved),
		Payload:     message,
	})
}

func (s *Service) detectProjectMoves(
	oldMap map[string]model.ProjectSummary,
	discoveredSet map[string]struct{},
	cached map[string]model.ProjectGitFingerprint,
	current map[string]scanner.GitFingerprint,
) []detectedProjectMove {
	currentProjects := map[string]struct{}{}
	for path := range oldMap {
		currentProjects[filepath.Clean(path)] = struct{}{}
	}

	newPaths := make([]string, 0, len(discoveredSet))
	for path := range discoveredSet {
		if _, ok := currentProjects[path]; ok {
			continue
		}
		if _, ok := current[path]; !ok {
			continue
		}
		newPaths = append(newPaths, path)
	}
	sort.Strings(newPaths)

	proposalsByNew := map[string][]detectedProjectMove{}
	for oldPath, project := range oldMap {
		if !project.InScope {
			continue
		}
		oldPath = filepath.Clean(oldPath)
		if _, ok := discoveredSet[oldPath]; ok {
			continue
		}
		fingerprint, ok := cached[oldPath]
		if !ok || len(fingerprint.RecentHashes) == 0 {
			continue
		}

		bestScore := 0
		bestNewPath := ""
		bestShared := []string(nil)
		ambiguous := false
		for _, newPath := range newPaths {
			shared := overlappingHashes(fingerprint.RecentHashes, current[newPath].RecentHashes)
			score := len(shared)
			if score == 0 {
				continue
			}
			if score > bestScore {
				bestScore = score
				bestNewPath = newPath
				bestShared = shared
				ambiguous = false
				continue
			}
			if score == bestScore {
				ambiguous = true
			}
		}
		if bestScore == 0 || ambiguous || bestNewPath == "" {
			continue
		}
		proposalsByNew[bestNewPath] = append(proposalsByNew[bestNewPath], detectedProjectMove{
			OldPath:     oldPath,
			NewPath:     bestNewPath,
			Score:       bestScore,
			SharedHeads: bestShared,
		})
	}

	moves := make([]detectedProjectMove, 0, len(proposalsByNew))
	for _, proposals := range proposalsByNew {
		if len(proposals) != 1 {
			continue
		}
		moves = append(moves, proposals[0])
	}
	sort.Slice(moves, func(i, j int) bool {
		if moves[i].OldPath == moves[j].OldPath {
			return moves[i].NewPath < moves[j].NewPath
		}
		return moves[i].OldPath < moves[j].OldPath
	})
	return moves
}

func resolveProjectPath(path string, aliases map[string]model.PathAlias) string {
	path = filepath.Clean(path)
	seen := map[string]struct{}{}
	for path != "" {
		if _, ok := seen[path]; ok {
			return path
		}
		seen[path] = struct{}{}
		alias, ok := aliases[path]
		if !ok || alias.NewPath == "" {
			return path
		}
		path = filepath.Clean(alias.NewPath)
	}
	return path
}

func normalizeActivity(activity *model.DetectorProjectActivity, projectPath string) *model.DetectorProjectActivity {
	if activity == nil {
		return nil
	}
	normalized := *activity
	normalized.ProjectPath = projectPath
	normalized.Sessions = append([]model.SessionEvidence(nil), activity.Sessions...)
	for i := range normalized.Sessions {
		normalized.Sessions[i] = model.NormalizeSessionEvidenceIdentity(normalized.Sessions[i])
		detectedPath := normalized.Sessions[i].DetectedProjectPath
		if detectedPath == "" {
			detectedPath = normalized.Sessions[i].ProjectPath
		}
		if detectedPath == "" {
			detectedPath = activity.ProjectPath
		}
		if detectedPath != "" {
			normalized.Sessions[i].DetectedProjectPath = filepath.Clean(detectedPath)
		}
		normalized.Sessions[i].ProjectPath = projectPath
	}
	normalized.Artifacts = append([]model.ArtifactEvidence(nil), activity.Artifacts...)
	return &normalized
}

func overlappingHashes(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, hash := range b {
		if hash == "" {
			continue
		}
		seen[hash] = struct{}{}
	}
	shared := []string{}
	for _, hash := range a {
		if _, ok := seen[hash]; ok {
			shared = append(shared, hash)
		}
	}
	return shared
}

func shortHashes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, hash := range in {
		if len(hash) > 7 {
			out = append(out, hash[:7])
			continue
		}
		out = append(out, hash)
	}
	return out
}

func projectPathExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// projectIsGitRepo reports whether path contains a .git entry (file or directory).
// A directory that exists but lacks .git is not a git repository and should not
// fall back to cached git state.
func projectIsGitRepo(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

func (s *Service) latestSessionClassification(ctx context.Context, path string, sessions []model.SessionEvidence, now time.Time) (bool, model.SessionCategory) {
	return s.latestSessionClassificationWithConfig(ctx, path, sessions, now, s.runtimeSnapshot().cfg)
}

func (s *Service) latestSessionClassificationWithConfig(ctx context.Context, path string, sessions []model.SessionEvidence, now time.Time, cfg config.AppConfig) (bool, model.SessionCategory) {
	if len(sessions) == 0 {
		return false, model.SessionCategoryUnknown
	}

	classification, err := s.store.GetSessionClassification(ctx, sessions[0].SessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, model.SessionCategoryUnknown
		}
		return false, model.SessionCategoryUnknown
	}
	if classification.Status != model.ClassificationCompleted {
		return false, model.SessionCategoryUnknown
	}
	expectedHash := strings.TrimSpace(sessions[0].SnapshotHash)
	if expectedHash == "" {
		return false, model.SessionCategoryUnknown
	}
	if classification.SnapshotHash != expectedHash {
		return false, model.SessionCategoryUnknown
	}
	effective := sessionclassify.DeriveEffectiveAssessment(sessionclassify.EffectiveAssessmentInput{
		Status:               classification.Status,
		Category:             classification.Category,
		Summary:              classification.Summary,
		LastEventAt:          sessions[0].LastEventAt,
		LatestTurnStateKnown: sessions[0].LatestTurnStateKnown,
		LatestTurnCompleted:  sessions[0].LatestTurnCompleted,
		Now:                  now,
		StuckThreshold:       sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold),
	})
	return true, effective.Category
}

func reuseLatestSessionSnapshotHash(old model.ProjectSummary, session *model.SessionEvidence, gitStatus sessionclassify.GitStatusSnapshot) {
	if session == nil || strings.TrimSpace(session.SnapshotHash) != "" {
		return
	}
	if old.LatestSessionID == "" || old.LatestSessionSnapshotHash == "" {
		return
	}
	if old.LatestSessionID != session.SessionID || old.LatestSessionFormat != session.Format {
		return
	}
	if !timesEqual(old.LatestSessionLastEventAt, session.LastEventAt) {
		return
	}
	if old.LatestTurnStateKnown != session.LatestTurnStateKnown || old.LatestTurnCompleted != session.LatestTurnCompleted {
		return
	}
	if !projectSummaryMatchesGitStatus(old, gitStatus) {
		return
	}
	session.SnapshotHash = old.LatestSessionSnapshotHash
}

func projectSummaryMatchesGitStatus(summary model.ProjectSummary, gitStatus sessionclassify.GitStatusSnapshot) bool {
	return summary.RepoDirty == gitStatus.WorktreeDirty &&
		summary.RepoSyncStatus == model.RepoSyncStatus(gitStatus.RemoteStatus) &&
		summary.RepoAheadCount == gitStatus.AheadCount &&
		summary.RepoBehindCount == gitStatus.BehindCount
}

func reuseLatestSessionTurnState(old model.ProjectSummary, session *model.SessionEvidence) {
	if session == nil || session.LatestTurnStateKnown {
		return
	}
	if old.LatestSessionID == "" || !old.LatestTurnStateKnown {
		return
	}
	if old.LatestSessionID != session.SessionID || old.LatestSessionFormat != session.Format {
		return
	}
	if !timesEqual(old.LatestSessionLastEventAt, session.LastEventAt) {
		return
	}
	session.LatestTurnStateKnown = true
	session.LatestTurnCompleted = old.LatestTurnCompleted
	session.LatestTurnStartedAt = old.LatestTurnStartedAt
}

func ensureLatestSessionTurnState(session *model.SessionEvidence) {
	if session == nil || session.LatestTurnStateKnown {
		return
	}
	_ = sessionclassify.RecoverSessionTurnState(session)
}

func ensureSessionSnapshotHash(ctx context.Context, projectPath string, session *model.SessionEvidence, gitStatus sessionclassify.GitStatusSnapshot) {
	if session == nil || session.SessionID == "" || session.SessionFile == "" {
		return
	}
	if strings.TrimSpace(session.SnapshotHash) != "" {
		return
	}
	hash, err := sessionclassify.ComputeSnapshotHash(ctx, projectPath, *session, gitStatus)
	if err == nil && strings.TrimSpace(hash) != "" {
		session.SnapshotHash = hash
	}
}

func (s *Service) RefreshProjectStatus(ctx context.Context, projectPath string) error {
	return s.RefreshProjectStatusWithOptions(ctx, projectPath, ScanOptions{})
}

func (s *Service) RefreshProjectStatusWithOptions(ctx context.Context, projectPath string, opts ScanOptions) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	runtime := s.runtimeSnapshot()
	gitRepoStatusReader := withScanGitMetadataTimeout(runtime.gitRepoStatusReader, nil)
	gitWorktreeInfoReader := withScanGitMetadataTimeout(runtime.gitWorktreeInfoReader, nil)
	gitWorktreeListReader := withScanGitMetadataTimeout(runtime.gitWorktreeListReader, nil)
	now := time.Now()
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		return err
	}

	presentOnDisk := projectPathExists(detail.Summary.Path)
	isGitRepo := presentOnDisk && projectIsGitRepo(detail.Summary.Path)
	worktreeRootPath := detail.Summary.WorktreeRootPath
	worktreeKind := detail.Summary.WorktreeKind
	worktreeParentBranch := detail.Summary.WorktreeParentBranch
	worktreeMergeStatus := detail.Summary.WorktreeMergeStatus
	if presentOnDisk && !isGitRepo {
		worktreeRootPath = ""
		worktreeKind = model.WorktreeKindNone
		worktreeParentBranch = ""
		worktreeMergeStatus = model.WorktreeMergeStatus("")
	}
	repoBranch := ""
	repoDirty := false
	repoConflict := false
	repoSyncStatus := model.RepoSyncStatus("")
	repoAheadCount := 0
	repoBehindCount := 0
	if presentOnDisk {
		if nextRootPath, nextKind := s.readProjectWorktreeInfoWithReader(ctx, detail.Summary.Path, gitWorktreeInfoReader); nextRootPath != "" || nextKind != model.WorktreeKindNone {
			worktreeRootPath = nextRootPath
			worktreeKind = nextKind
		}
		if gitRepoStatusReader != nil {
			if repoStatus, err := gitRepoStatusReader(ctx, detail.Summary.Path); err == nil {
				repoBranch = strings.TrimSpace(repoStatus.Branch)
				repoDirty = repoStatus.Dirty
				repoConflict = repoConflictFromGit(repoStatus)
				repoSyncStatus = repoSyncStatusFromGit(repoStatus)
				repoAheadCount = repoStatus.Ahead
				repoBehindCount = repoStatus.Behind
			} else if isGitRepo {
				repoBranch = detail.Summary.RepoBranch
				repoDirty = detail.Summary.RepoDirty
				repoConflict = detail.Summary.RepoConflict
				repoSyncStatus = detail.Summary.RepoSyncStatus
				repoAheadCount = detail.Summary.RepoAheadCount
				repoBehindCount = detail.Summary.RepoBehindCount
			}
		} else if isGitRepo {
			repoBranch = detail.Summary.RepoBranch
			repoDirty = detail.Summary.RepoDirty
			repoConflict = detail.Summary.RepoConflict
			repoSyncStatus = detail.Summary.RepoSyncStatus
			repoAheadCount = detail.Summary.RepoAheadCount
			repoBehindCount = detail.Summary.RepoBehindCount
		}
		worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, worktreeRootPath, worktreeKind, repoBranch, worktreeParentBranch)
	}
	forgotten := detail.Summary.Forgotten
	// Keep linked-worktree cleanup consistent with ScanOnce: once a linked
	// checkout disappears from `git worktree list`, treat it as stale even if
	// the directory itself is already gone. This prevents async status refreshes
	// from briefly re-surfacing removed worktrees as plain "missing" folders.
	staleLinkedWorktree := s.staleLinkedWorktreeOnDiskWithReader(ctx, worktreeRootPath, worktreeKind, detail.Summary.Path, gitWorktreeListReader)
	if staleLinkedWorktree {
		forgotten = true
	}
	if presentOnDisk && forgotten && detail.Summary.InScope && !staleLinkedWorktree {
		forgotten = false
	}

	if len(detail.Sessions) > 0 {
		if strings.TrimSpace(detail.Sessions[0].SessionFile) == "" {
			detail.Sessions[0].SessionFile = resolveEmbeddedSessionFile(
				detail.Sessions[0].Source,
				detail.Sessions[0].SessionID,
				detail.Sessions[0].RawSessionID,
				detail.Sessions[0].StartedAt,
				detail.Sessions[0].LastEventAt,
				runtime.cfg,
			)
		}
		ensureLatestSessionTurnState(&detail.Sessions[0])
		ensureSessionSnapshotHash(ctx, projectPath, &detail.Sessions[0], sessionclassify.NewGitStatusSnapshot(repoDirty, repoSyncStatus, repoAheadCount, repoBehindCount))
	}
	if err := s.persistProjectStateUpdate(ctx, detail, now, projectStatusRefreshOverrides{
		presentOnDisk:        presentOnDisk,
		worktreeRootPath:     worktreeRootPath,
		worktreeKind:         worktreeKind,
		worktreeParentBranch: worktreeParentBranch,
		worktreeMergeStatus:  worktreeMergeStatus,
		repoBranch:           repoBranch,
		repoDirty:            repoDirty,
		repoConflict:         repoConflict,
		repoSyncStatus:       repoSyncStatus,
		repoAheadCount:       repoAheadCount,
		repoBehindCount:      repoBehindCount,
		forgotten:            forgotten,
	}, runtime.cfg, runtime.classifier, opts); err != nil {
		return err
	}
	if worktreeKind == model.WorktreeKindMain {
		return s.refreshLinkedWorktreeStatusesForRoot(ctx, detail.Summary.Path)
	}
	return nil
}

func (s *Service) refreshLinkedWorktreeStatusesForRoot(ctx context.Context, rootPath string) error {
	if s == nil || s.store == nil {
		return nil
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return nil
	}
	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return fmt.Errorf("list linked worktrees for root refresh: %w", err)
	}
	var errs []string
	for _, project := range projects {
		if project.WorktreeKind != model.WorktreeKindLinked {
			continue
		}
		if filepath.Clean(strings.TrimSpace(project.WorktreeRootPath)) != rootPath {
			continue
		}
		projectPath := filepath.Clean(strings.TrimSpace(project.Path))
		if projectPath == "" || projectPath == "." || projectPath == rootPath {
			continue
		}
		if err := s.RefreshProjectStatus(ctx, projectPath); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", projectPath, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("refresh linked worktree statuses for %s: %s", rootPath, strings.Join(errs, "; "))
	}
	return nil
}

type projectStatusRefreshOverrides struct {
	presentOnDisk        bool
	worktreeRootPath     string
	worktreeKind         model.WorktreeKind
	worktreeParentBranch string
	worktreeMergeStatus  model.WorktreeMergeStatus
	repoBranch           string
	repoDirty            bool
	repoConflict         bool
	repoSyncStatus       model.RepoSyncStatus
	repoAheadCount       int
	repoBehindCount      int
	forgotten            bool
}

func (s *Service) persistProjectStateUpdate(ctx context.Context, detail model.ProjectDetail, now time.Time, overrides projectStatusRefreshOverrides, cfg config.AppConfig, classifier SessionClassifier, opts ScanOptions) error {
	projectPath := detail.Summary.Path
	latestSessionStart := time.Time{}
	latestTurnKnown := false
	latestTurnComplete := false
	errorCount := 0
	if len(detail.Sessions) > 0 {
		latestSessionStart = detail.Sessions[0].StartedAt
		latestTurnKnown = detail.Sessions[0].LatestTurnStateKnown
		latestTurnComplete = detail.Sessions[0].LatestTurnCompleted
	}
	for _, session := range detail.Sessions {
		errorCount += session.ErrorCount
	}

	classificationKnown, classificationCategory := s.latestSessionClassificationWithConfig(ctx, projectPath, detail.Sessions, now, cfg)
	score := attention.Score(attention.Input{
		Path:                       detail.Summary.Path,
		Now:                        now,
		LastActivity:               detail.Summary.LastActivity,
		CreatedAt:                  detail.Summary.CreatedAt,
		RepoDirty:                  overrides.repoDirty,
		Pinned:                     detail.Summary.Pinned,
		Unread:                     attention.AssessmentUnread(detail.Summary),
		SnoozedUntil:               detail.Summary.SnoozedUntil,
		ErrorCount:                 errorCount,
		LatestSessionStart:         latestSessionStart,
		LatestTurnKnown:            latestTurnKnown,
		LatestTurnComplete:         latestTurnComplete,
		LatestSessionCategoryKnown: classificationKnown,
		LatestSessionCategory:      classificationCategory,
		HasActivity:                !detail.Summary.LastActivity.IsZero(),
		ActiveThreshold:            cfg.ActiveThreshold,
		StuckThreshold:             cfg.StuckThreshold,
		OpenTodoCount:              detail.Summary.OpenTODOCount,
	})

	state := model.ProjectState{
		Path:                 detail.Summary.Path,
		Name:                 detail.Summary.Name,
		Kind:                 model.NormalizeProjectKind(detail.Summary.Kind),
		LastActivity:         detail.Summary.LastActivity,
		Status:               score.Status,
		AttentionScore:       score.Score,
		PresentOnDisk:        overrides.presentOnDisk,
		WorktreeRootPath:     overrides.worktreeRootPath,
		WorktreeKind:         overrides.worktreeKind,
		WorktreeParentBranch: overrides.worktreeParentBranch,
		WorktreeMergeStatus:  overrides.worktreeMergeStatus,
		WorktreeOriginTodoID: detail.Summary.WorktreeOriginTodoID,
		RepoBranch:           overrides.repoBranch,
		RepoDirty:            overrides.repoDirty,
		RepoConflict:         overrides.repoConflict,
		RepoSyncStatus:       overrides.repoSyncStatus,
		RepoAheadCount:       overrides.repoAheadCount,
		RepoBehindCount:      overrides.repoBehindCount,
		Forgotten:            overrides.forgotten,
		ManuallyAdded:        detail.Summary.ManuallyAdded,
		InScope:              detail.Summary.InScope,
		Pinned:               detail.Summary.Pinned,
		SnoozedUntil:         detail.Summary.SnoozedUntil,
		RunCommand:           detail.Summary.RunCommand,
		MovedFromPath:        detail.Summary.MovedFromPath,
		MovedAt:              detail.Summary.MovedAt,
		AttentionReason:      score.Reasons,
		Sessions:             detail.Sessions,
		Artifacts:            detail.Artifacts,
		CreatedAt:            detail.Summary.CreatedAt,
		UpdatedAt:            now,
	}
	if err := s.store.UpsertProjectState(ctx, state); err != nil {
		return fmt.Errorf("persist refreshed project state: %w", err)
	}
	if classifier != nil {
		queued, err := queueProjectClassification(ctx, classifier, state, opts)
		if err != nil {
			return fmt.Errorf("queue session classification: %w", err)
		}
		if queued {
			classifier.Notify()
		}
	}
	if projectStateChanged(detail.Summary, state) {
		s.publishProjectChanged(ctx, now, state)
	}
	return nil
}

func (s *Service) refreshProjectAttention(ctx context.Context, projectPath string) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	return s.refreshProjectAttentionLocked(ctx, projectPath)
}

func (s *Service) refreshProjectAttentionLocked(ctx context.Context, projectPath string) error {
	cfg := s.runtimeSnapshot().cfg
	now := time.Now()
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		return err
	}
	return s.persistProjectStateUpdate(ctx, detail, now, projectStatusRefreshOverrides{
		presentOnDisk:        detail.Summary.PresentOnDisk,
		worktreeRootPath:     detail.Summary.WorktreeRootPath,
		worktreeKind:         detail.Summary.WorktreeKind,
		worktreeParentBranch: detail.Summary.WorktreeParentBranch,
		worktreeMergeStatus:  detail.Summary.WorktreeMergeStatus,
		repoBranch:           detail.Summary.RepoBranch,
		repoDirty:            detail.Summary.RepoDirty,
		repoConflict:         detail.Summary.RepoConflict,
		repoSyncStatus:       detail.Summary.RepoSyncStatus,
		repoAheadCount:       detail.Summary.RepoAheadCount,
		repoBehindCount:      detail.Summary.RepoBehindCount,
		forgotten:            detail.Summary.Forgotten,
	}, cfg, nil, ScanOptions{})
}

func (s *Service) TogglePin(ctx context.Context, projectPath string) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	m, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return err
	}
	project := m[projectPath]
	if project.Path == "" {
		return fmt.Errorf("project not found: %s", projectPath)
	}
	if err := s.store.SetPinned(ctx, projectPath, !project.Pinned); err != nil {
		return err
	}
	if err := s.refreshProjectAttentionLocked(ctx, projectPath); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "toggle_pin"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "toggle_pin"})
	return nil
}

func (s *Service) Snooze(ctx context.Context, projectPath string, duration time.Duration) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	until := time.Now().Add(duration)
	if err := s.store.SetSnooze(ctx, projectPath, &until); err != nil {
		return err
	}
	if err := s.refreshProjectAttentionLocked(ctx, projectPath); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "snooze", "until": until.Format(time.RFC3339)}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: fmt.Sprintf("snooze until %s", until.Format(time.RFC3339))})
	return nil
}

func (s *Service) ClearSnooze(ctx context.Context, projectPath string) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	if err := s.store.SetSnooze(ctx, projectPath, nil); err != nil {
		return err
	}
	if err := s.refreshProjectAttentionLocked(ctx, projectPath); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "clear_snooze"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "clear_snooze"})
	return nil
}

func (s *Service) MarkProjectSessionSeen(ctx context.Context, projectPath string, seenAt time.Time) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	if err := s.store.SetProjectSessionSeenAt(ctx, projectPath, seenAt); err != nil {
		return err
	}
	if err := s.refreshProjectAttentionLocked(ctx, projectPath); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{
		Type:        events.ActionApplied,
		At:          now,
		ProjectPath: projectPath,
		Payload: map[string]string{
			"action":  "mark_session_seen",
			"seen_at": seenAt.Format(time.RFC3339),
		},
	})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     "mark_session_seen",
	})
	return nil
}

func (s *Service) MarkProjectSessionUnread(ctx context.Context, projectPath string) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	if err := s.store.ClearProjectSessionSeenAt(ctx, projectPath); err != nil {
		return err
	}
	if err := s.refreshProjectAttentionLocked(ctx, projectPath); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{
		Type:        events.ActionApplied,
		At:          now,
		ProjectPath: projectPath,
		Payload: map[string]string{
			"action": "mark_session_unread",
		},
	})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     "mark_session_unread",
	})
	return nil
}

func (s *Service) refreshProjectStatusAsync(projectPath string) {
	if s == nil {
		return
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return
	}
	if !s.beginAsyncProjectRefresh(projectPath) {
		return
	}
	go func(path string) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), asyncProjectRefreshTimeout)
			_ = s.projectStatusRefreshRunner()(ctx, path)
			cancel()
			if !s.finishAsyncProjectRefresh(path) {
				return
			}
		}
	}(projectPath)
}

func (s *Service) projectStatusRefreshRunner() func(context.Context, string) error {
	if s.refreshProjectStatusFn != nil {
		return s.refreshProjectStatusFn
	}
	return s.RefreshProjectStatus
}

func (s *Service) beginAsyncProjectRefresh(projectPath string) bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if s.refreshState == nil {
		s.refreshState = map[string]asyncProjectRefreshState{}
	}
	state := s.refreshState[projectPath]
	if state.running {
		state.queued = true
		s.refreshState[projectPath] = state
		return false
	}
	s.refreshState[projectPath] = asyncProjectRefreshState{running: true}
	return true
}

func (s *Service) finishAsyncProjectRefresh(projectPath string) bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	state, ok := s.refreshState[projectPath]
	if !ok {
		return false
	}
	if state.queued {
		state.queued = false
		s.refreshState[projectPath] = state
		return true
	}
	delete(s.refreshState, projectPath)
	return false
}

func (s *Service) AddTodo(ctx context.Context, projectPath, text string) (model.TodoItem, error) {
	item, err := s.store.AddTodo(ctx, projectPath, text)
	if err != nil {
		return model.TodoItem{}, err
	}
	s.refreshProjectStatusAsync(projectPath)
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "add_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "add_todo"})
	return item, nil
}

func (s *Service) UpdateTodo(ctx context.Context, projectPath string, id int64, text string) error {
	if err := s.store.UpdateTodo(ctx, id, text); err != nil {
		return err
	}
	if err := s.store.DeleteTodoWorktreeSuggestion(ctx, id); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "update_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "update_todo"})
	return nil
}

func (s *Service) ToggleTodoDone(ctx context.Context, projectPath string, id int64, done bool) error {
	if err := s.store.ToggleTodoDone(ctx, id, done); err != nil {
		return err
	}
	s.refreshProjectStatusAsync(projectPath)
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "toggle_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "toggle_todo"})
	return nil
}

func (s *Service) DeleteTodo(ctx context.Context, projectPath string, id int64) error {
	if err := s.store.DeleteTodo(ctx, id); err != nil {
		return err
	}
	s.refreshProjectStatusAsync(projectPath)
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "delete_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "delete_todo"})
	return nil
}

func (s *Service) PurgeDoneTodos(ctx context.Context, projectPath string) (int, error) {
	count, err := s.store.DeleteDoneTodos(ctx, projectPath)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	s.refreshProjectStatusAsync(projectPath)
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "purge_done_todos"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "purge_done_todos"})
	return count, nil
}

func (s *Service) SetRunCommand(ctx context.Context, projectPath, command string) error {
	if err := s.store.SetRunCommand(ctx, projectPath, command); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{
		Type:        events.ActionApplied,
		At:          now,
		ProjectPath: projectPath,
		Payload: map[string]string{
			"action":      "set_run_command",
			"run_command": strings.TrimSpace(command),
		},
	})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     "set_run_command",
	})
	return nil
}

func (s *Service) ForgetProject(ctx context.Context, projectPath string) error {
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	defer unlockProjectState()

	m, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return err
	}
	project := m[projectPath]
	if project.Path == "" {
		return fmt.Errorf("project not found: %s", projectPath)
	}
	if project.PresentOnDisk {
		return fmt.Errorf("project still exists on disk: %s", projectPath)
	}
	if err := s.store.SetForgotten(ctx, projectPath, true); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "forget_project"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "forget_project"})
	return nil
}

func (s *Service) StartScheduler(ctx context.Context) {
	if s.cfg.ScanInterval <= 0 {
		return
	}
	ticker := time.NewTicker(s.cfg.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.ScanOnce(ctx)
		}
	}
}
