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
	"sync/atomic"
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
const asyncProjectRefreshConcurrency = 1
const bossAssistantHTTPTimeout = 90 * time.Second
const missingLinkedWorktreeRetention = 7 * 24 * time.Hour
const fullScanLockPollInterval = 25 * time.Millisecond
const scanGitMetadataTimeoutPathLimit = 8

var scanGitMetadataTimeout = 1500 * time.Millisecond
var scanGitMetadataConcurrency = 8

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
	titleAssessor ScratchTaskTitleAssessor

	backendDetector func(context.Context, config.AppConfig, config.AIBackend) aibackend.Status

	commitMessageSuggester   gitops.CommitMessageSuggester
	commitTodoChecker        gitops.CommitTodoCompletionChecker
	untrackedFileRecommender gitops.UntrackedFileRecommender
	commitAssistantTimeout   time.Duration
	llmUsageTracker          *llm.UsageTracker
	bossChatUsageTracker     *llm.UsageTracker
	opencodeDiscovery        *llm.OpenCodeDiscovery
	sessionUsageCache        atomic.Value

	gitFingerprintReader      func(context.Context, string) (scanner.GitFingerprint, error)
	gitRepoStatusReader       func(context.Context, string) (scanner.GitRepoStatus, error)
	gitWorktreeInfoReader     func(context.Context, string) (scanner.GitWorktreeInfo, error)
	gitWorktreeListReader     func(context.Context, string) ([]scanner.GitWorktree, error)
	gitRepoInitializer        func(context.Context, string) error
	refreshProjectAttentionFn func(context.Context, string) error
	refreshProjectStatusFn    func(context.Context, string) error

	mu sync.Mutex

	fullScanMu sync.Mutex

	schedulerWakeOnce    sync.Once
	schedulerWakeCh      chan struct{}
	scheduledScanTimeout time.Duration

	projectStateLocks   keyedmutex.Locker
	worktreeCreateLocks keyedmutex.Locker
	gitWriteLocks       keyedmutex.Locker

	backgroundRefreshMu    sync.Mutex
	backgroundRefreshState map[string]asyncProjectRefreshState
	backgroundRefreshSlots chan struct{}
	projectStateCacheMu    sync.RWMutex
	projectStateCache      map[string]cachedProjectState

	commitTodoNotifyCh  chan struct{}
	commitTodoStartOnce sync.Once
}

type asyncProjectRefreshKind uint8

const (
	asyncProjectRefreshAttention asyncProjectRefreshKind = iota + 1
	asyncProjectRefreshStatus
)

type asyncProjectRefreshState struct {
	requestedKind asyncProjectRefreshKind
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
	At                            time.Time
	ActivityProjectCount          int
	TrackedProjectCount           int
	UpdatedProjects               []string
	QueuedClassifications         int
	GitMetadataTimeoutCount       int
	GitMetadataTimeoutPathSamples []string
	States                        []model.ProjectState
}

type ScanOptions struct {
	ForceRetryFailedClassifications bool
	SkipLinkedWorktreeStatusRefresh bool
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
		commitTodoNotifyCh:     make(chan struct{}, 1),
		projectStateCache:      make(map[string]cachedProjectState),
		gitFingerprintReader:   scanner.ReadGitFingerprint,
		gitRepoStatusReader:    scanner.ReadGitRepoStatus,
		gitWorktreeInfoReader:  scanner.ReadGitWorktreeInfo,
		gitWorktreeListReader:  scanner.ListGitWorktrees,
		gitRepoInitializer:     runGitInit,
		scheduledScanTimeout:   defaultScheduledScanTimeout,
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

func (s *Service) SetScratchTaskTitleAssessor(assessor ScratchTaskTitleAssessor) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titleAssessor = assessor
}

func (s *Service) currentScratchTaskTitleAssessor() ScratchTaskTitleAssessor {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.titleAssessor
}

func (s *Service) currentCommitMessageSuggester() gitops.CommitMessageSuggester {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitMessageSuggester
}

func (s *Service) currentCommitTodoChecker() gitops.CommitTodoCompletionChecker {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitTodoChecker
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
	cloned.RecentLCAgentModels = append([]string(nil), cfg.RecentLCAgentModels...)
	cloned.PlaywrightPolicy = cfg.PlaywrightPolicy.Normalize()
	return cloned
}

type scanGitMetadataTimeoutSet struct {
	mu    sync.Mutex
	paths map[string]struct{}
}

func (s *scanGitMetadataTimeoutSet) contains(path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.paths[path]
	return ok
}

func (s *scanGitMetadataTimeoutSet) add(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paths == nil {
		s.paths = make(map[string]struct{})
	}
	s.paths[path] = struct{}{}
}

func (s *scanGitMetadataTimeoutSet) snapshot(limit int) (int, []string) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	paths := make([]string, 0, len(s.paths))
	for path := range s.paths {
		paths = append(paths, path)
	}
	s.mu.Unlock()

	sort.Strings(paths)
	count := len(paths)
	if limit >= 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	return count, paths
}

func withScanGitMetadataTimeout[T any](reader func(context.Context, string) (T, error), timedOutPaths *scanGitMetadataTimeoutSet) func(context.Context, string) (T, error) {
	if reader == nil {
		return nil
	}
	return func(parent context.Context, path string) (T, error) {
		var zero T
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if timedOutPaths.contains(cleanPath) {
			return zero, fmt.Errorf("skipping git metadata read for %s after earlier timeout", cleanPath)
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
			timedOutPaths.add(cleanPath)
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

func (s *Service) lockProjectStateMutationContext(ctx context.Context, projectPath string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return func() {}, nil
	}
	unlock, err := s.projectStateLocks.LockContext(ctx, projectPath)
	if err != nil {
		return nil, fmt.Errorf("wait for project state mutation lock for %s: %w", projectPath, err)
	}
	return unlock, nil
}

func (s *Service) lockProjectStateMutationsContext(ctx context.Context, projectPaths []string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(projectPaths))
	for _, projectPath := range projectPaths {
		projectPath = filepath.Clean(strings.TrimSpace(projectPath))
		if projectPath == "" || projectPath == "." {
			continue
		}
		if _, ok := seen[projectPath]; ok {
			continue
		}
		seen[projectPath] = struct{}{}
		paths = append(paths, projectPath)
	}
	sort.Strings(paths)

	unlocks := make([]func(), 0, len(paths))
	for _, projectPath := range paths {
		unlock, err := s.lockProjectStateMutationContext(ctx, projectPath)
		if err != nil {
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
			return nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}, nil
}

func (s *Service) lockGitWrite(ctx context.Context, repoPath string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	if repoPath == "" || repoPath == "." {
		return func() {}, nil
	}
	unlock, err := s.gitWriteLocks.LockContext(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("wait for git write lock for %s: %w", repoPath, err)
	}
	return unlock, nil
}

func (s *Service) lockMutation(ctx context.Context) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if s.mu.TryLock() {
			return s.mu.Unlock, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for service mutation lock: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *Service) lockFullScanContext(ctx context.Context) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(fullScanLockPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("wait for another project scan: %w", err)
		}
		if s.fullScanMu.TryLock() {
			return s.fullScanMu.Unlock, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for another project scan: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) tryLockFullScan() (func(), bool) {
	if s == nil {
		return func() {}, true
	}
	if !s.fullScanMu.TryLock() {
		return nil, false
	}
	return s.fullScanMu.Unlock, true
}

func (s *Service) schedulerWakeChannel() chan struct{} {
	if s == nil {
		return nil
	}
	s.schedulerWakeOnce.Do(func() {
		s.schedulerWakeCh = make(chan struct{}, 1)
	})
	return s.schedulerWakeCh
}

func (s *Service) notifySchedulerConfigChanged() {
	if s == nil {
		return
	}
	wakeCh := s.schedulerWakeChannel()
	select {
	case wakeCh <- struct{}{}:
	default:
	}
}

func (s *Service) SetSessionClassifier(classifier SessionClassifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.classifier = classifier
}

func (s *Service) ApplyEditableSettings(settings config.EditableSettings) {
	settings = config.NormalizeEditableSettings(settings)
	s.mu.Lock()

	reconfigureAIClients := editableSettingsRequireAIClientRefresh(s.cfg, settings)
	scanIntervalChanged := s.cfg.ScanInterval != settings.ScanInterval
	currentBackend := s.cfg.EffectiveAIBackend()
	nextBackend := config.ResolveAIBackend(settings.AIBackend, settings.OpenAIAPIKey)
	currentBossChatBackend := s.cfg.EffectiveBossChatBackend()
	nextBossChatBackend := config.ResolveBossChatBackend(settings.BossChatBackend, settings.OpenAIAPIKey)
	currentBossChatOllamaThinking := s.cfg.BossChatOllamaThinking

	s.cfg.AIBackend = settings.AIBackend
	s.cfg.BossChatBackend = settings.BossChatBackend
	s.cfg.BossChatModel = strings.TrimSpace(settings.BossChatModel)
	s.cfg.BossHelmModel = strings.TrimSpace(settings.BossHelmModel)
	s.cfg.BossUtilityModel = strings.TrimSpace(settings.BossUtilityModel)
	s.cfg.BossChatOllamaThinking = settings.BossChatOllamaThinking
	s.cfg.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
	s.cfg.OpenRouterAPIKey = strings.TrimSpace(settings.OpenRouterAPIKey)
	s.cfg.OpenRouterModel = strings.TrimSpace(settings.OpenRouterModel)
	s.cfg.DeepSeekAPIKey = strings.TrimSpace(settings.DeepSeekAPIKey)
	s.cfg.DeepSeekModel = strings.TrimSpace(settings.DeepSeekModel)
	s.cfg.MoonshotAPIKey = strings.TrimSpace(settings.MoonshotAPIKey)
	s.cfg.MoonshotModel = strings.TrimSpace(settings.MoonshotModel)
	s.cfg.XiaomiBaseURL = strings.TrimSpace(settings.XiaomiBaseURL)
	s.cfg.XiaomiAPIKey = strings.TrimSpace(settings.XiaomiAPIKey)
	s.cfg.XiaomiModel = strings.TrimSpace(settings.XiaomiModel)
	s.cfg.ProjectReasoningEffort = strings.TrimSpace(settings.ProjectReasoningEffort)
	s.cfg.MLXBaseURL = strings.TrimSpace(settings.MLXBaseURL)
	s.cfg.MLXAPIKey = strings.TrimSpace(settings.MLXAPIKey)
	s.cfg.MLXModel = strings.TrimSpace(settings.MLXModel)
	s.cfg.OllamaBaseURL = strings.TrimSpace(settings.OllamaBaseURL)
	s.cfg.OllamaAPIKey = strings.TrimSpace(settings.OllamaAPIKey)
	s.cfg.OllamaModel = strings.TrimSpace(settings.OllamaModel)
	s.cfg.IncludePaths = append([]string(nil), settings.IncludePaths...)
	s.cfg.ExcludePaths = append([]string(nil), settings.ExcludePaths...)
	s.cfg.ExcludeProjectPatterns = append([]string(nil), settings.ExcludeProjectPatterns...)
	s.cfg.PrivacyPatterns = append([]string(nil), settings.PrivacyPatterns...)
	s.cfg.EmbeddedCodexModel = strings.TrimSpace(settings.EmbeddedCodexModel)
	s.cfg.EmbeddedCodexReasoning = strings.TrimSpace(settings.EmbeddedCodexReasoning)
	s.cfg.EmbeddedClaudeModel = strings.TrimSpace(settings.EmbeddedClaudeModel)
	s.cfg.EmbeddedClaudeReasoning = strings.TrimSpace(settings.EmbeddedClaudeReasoning)
	s.cfg.EmbeddedOpenCodeModel = strings.TrimSpace(settings.EmbeddedOpenCodeModel)
	s.cfg.EmbeddedOpenCodeReasoning = strings.TrimSpace(settings.EmbeddedOpenCodeReasoning)
	s.cfg.EmbeddedLCAgentModel = strings.TrimSpace(settings.EmbeddedLCAgentModel)
	s.cfg.EmbeddedLCAgentReasoning = strings.TrimSpace(settings.EmbeddedLCAgentReasoning)
	s.cfg.OpenCodeModelTier = strings.TrimSpace(settings.OpenCodeModelTier)
	s.cfg.RecentCodexModels = append([]string(nil), settings.RecentCodexModels...)
	s.cfg.RecentClaudeModels = append([]string(nil), settings.RecentClaudeModels...)
	s.cfg.RecentOpenCodeModels = append([]string(nil), settings.RecentOpenCodeModels...)
	s.cfg.RecentLCAgentModels = append([]string(nil), settings.RecentLCAgentModels...)
	s.cfg.LCAgentPath = strings.TrimSpace(settings.LCAgentPath)
	s.cfg.LCAgentEnvFile = strings.TrimSpace(settings.LCAgentEnvFile)
	s.cfg.LCAgentRoutePreset = strings.TrimSpace(settings.LCAgentRoutePreset)
	s.cfg.LCAgentProvider = strings.TrimSpace(settings.LCAgentProvider)
	s.cfg.LCAgentAuto = strings.TrimSpace(settings.LCAgentAuto)
	s.cfg.LCAgentAdminWrite = settings.LCAgentAdminWrite
	s.cfg.LCAgentToolProfile = strings.TrimSpace(settings.LCAgentToolProfile)
	s.cfg.LCAgentContextProfile = strings.TrimSpace(settings.LCAgentContextProfile)
	s.cfg.LCAgentRequestTimeout = settings.LCAgentRequestTimeout
	s.cfg.LCAgentUtilityProvider = strings.TrimSpace(settings.LCAgentUtilityProvider)
	s.cfg.LCAgentUtilityModel = strings.TrimSpace(settings.LCAgentUtilityModel)
	s.cfg.LCAgentVisionProvider = strings.TrimSpace(settings.LCAgentVisionProvider)
	s.cfg.LCAgentVisionModel = strings.TrimSpace(settings.LCAgentVisionModel)
	s.cfg.LCAgentMainVisionProvider = strings.TrimSpace(settings.LCAgentMainVisionProvider)
	s.cfg.LCAgentMainVisionModel = strings.TrimSpace(settings.LCAgentMainVisionModel)
	s.cfg.LCAgentWebSearchBackend = strings.TrimSpace(settings.LCAgentWebSearchBackend)
	s.cfg.LCAgentWebSearchAPIKey = strings.TrimSpace(settings.LCAgentWebSearchAPIKey)
	s.cfg.LCAgentWebSearchEngineID = strings.TrimSpace(settings.LCAgentWebSearchEngineID)
	s.cfg.LCAgentWebSearchURL = strings.TrimSpace(settings.LCAgentWebSearchURL)
	s.cfg.CodexLaunchPreset = settings.CodexLaunchPreset
	s.cfg.PlaywrightPolicy = settings.PlaywrightPolicy.Normalize()
	s.cfg.ScanInterval = settings.ScanInterval
	s.cfg.ActiveThreshold = settings.ActiveThreshold
	s.cfg.StuckThreshold = settings.StuckThreshold
	s.cfg.MobileEnabled = settings.MobileEnabled
	s.cfg.MobileInputEnabled = settings.MobileInputEnabled
	s.cfg.MobileListenAddress = strings.TrimSpace(settings.MobileListenAddress)
	s.cfg.HideReasoningSections = settings.HideReasoningSections
	s.cfg.PrivacyMode = settings.PrivacyMode
	if reconfigureAIClients {
		s.configureAIClientsLocked()
	}
	if currentBackend != nextBackend {
		s.resetSessionUsageLocked()
	}
	if currentBossChatBackend != nextBossChatBackend || currentBossChatOllamaThinking != settings.BossChatOllamaThinking {
		s.resetBossChatUsageLocked()
	}
	s.mu.Unlock()

	if scanIntervalChanged {
		s.notifySchedulerConfigChanged()
	}
}

func (s *Service) resetSessionUsageLocked() {
	if s.llmUsageTracker != nil {
		s.llmUsageTracker.Reset()
	}
	if resetter, ok := s.classifier.(interface{ ResetUsage() }); ok {
		resetter.ResetUsage()
	}
	s.sessionUsageCache.Store(model.LLMSessionUsage{})
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
	if strings.TrimSpace(current.OpenRouterAPIKey) != strings.TrimSpace(settings.OpenRouterAPIKey) ||
		strings.TrimSpace(current.OpenRouterModel) != strings.TrimSpace(settings.OpenRouterModel) ||
		strings.TrimSpace(current.DeepSeekAPIKey) != strings.TrimSpace(settings.DeepSeekAPIKey) ||
		strings.TrimSpace(current.DeepSeekModel) != strings.TrimSpace(settings.DeepSeekModel) ||
		strings.TrimSpace(current.MoonshotAPIKey) != strings.TrimSpace(settings.MoonshotAPIKey) ||
		strings.TrimSpace(current.MoonshotModel) != strings.TrimSpace(settings.MoonshotModel) ||
		strings.TrimSpace(current.XiaomiBaseURL) != strings.TrimSpace(settings.XiaomiBaseURL) ||
		strings.TrimSpace(current.XiaomiAPIKey) != strings.TrimSpace(settings.XiaomiAPIKey) ||
		strings.TrimSpace(current.XiaomiModel) != strings.TrimSpace(settings.XiaomiModel) ||
		strings.TrimSpace(current.ProjectReasoningEffort) != strings.TrimSpace(settings.ProjectReasoningEffort) {
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

func (s *Service) StartCommitTodoChecker(ctx context.Context) {
	if s == nil || s.store == nil || s.commitTodoNotifyCh == nil {
		return
	}
	s.commitTodoStartOnce.Do(func() {
		s.NotifyCommitTodoChecker()
		go s.commitTodoCheckWorker(ctx)
	})
}

func (s *Service) NotifyCommitTodoChecker() {
	if s == nil || s.commitTodoNotifyCh == nil {
		return
	}
	select {
	case s.commitTodoNotifyCh <- struct{}{}:
	default:
	}
}

func (s *Service) HasTodoWorktreeSuggester() bool {
	suggester := s.currentTodoSuggester()
	if suggester == nil {
		return false
	}
	return suggester.Enabled()
}

func (s *Service) TodoWorktreeSuggesterUnavailableReason() string {
	suggester := s.currentTodoSuggester()
	if suggester == nil {
		return ""
	}
	return suggester.UnavailableReason()
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
	if s == nil {
		return model.LLMSessionUsage{}
	}
	if !s.mu.TryLock() {
		if cached, ok := s.sessionUsageCache.Load().(model.LLMSessionUsage); ok {
			return cached
		}
		return model.LLMSessionUsage{}
	}
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
			s.sessionUsageCache.Store(snapshot)
			return snapshot
		}
	}
	var usage model.LLMSessionUsage
	if classifier == nil {
		if enabled {
			usage = model.LLMSessionUsage{Enabled: true}
		} else {
			usage = model.LLMSessionUsage{}
		}
		s.sessionUsageCache.Store(usage)
		return usage
	}
	if usageReader, ok := classifier.(interface{ UsageSnapshot() model.LLMSessionUsage }); ok {
		usage = usageReader.UsageSnapshot()
	} else {
		usage = model.LLMSessionUsage{Enabled: enabled}
	}
	s.sessionUsageCache.Store(usage)
	return usage
}

func (s *Service) BossChatUsage() model.LLMSessionUsage {
	if s == nil {
		return model.LLMSessionUsage{}
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()

	backend := cfg.EffectiveBossChatBackend()
	enabled := backend == config.AIBackendOpenAIAPI || backend.UsesOpenAICompatibleAPI()
	if usageTracker == nil {
		return model.LLMSessionUsage{Enabled: enabled}
	}
	snapshot := usageTracker.Snapshot(enabled)
	if hasMeaningfulLLMUsage(snapshot) {
		return snapshot
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
	modelName := configuredBossHelmModelForBackend(cfg, backend)
	switch backend {
	case config.AIBackendOpenAIAPI:
		return llm.NewResponsesTextClient(strings.TrimSpace(cfg.OpenAIAPIKey), bossAssistantHTTPTimeout, usageTracker), modelName, backend
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		if backend == config.AIBackendOllama {
			return llm.NewOllamaTextRunnerWithOptions(cfg.OpenAICompatibleBaseURL(backend), modelName, bossAssistantHTTPTimeout, usageTracker, llm.OllamaChatOptions{
				Think: cfg.BossChatOllamaThinking,
			}), modelName, backend
		}
		return llm.NewOpenAICompatibleTextRunnerWithOptions(cfg.OpenAICompatibleBaseURL(backend), cfg.OpenAICompatibleAPIKey(backend), modelName, bossAssistantHTTPTimeout, usageTracker, openAICompatibleResponsesRunnerOptions(backend, modelName, backend.UsesCloudAPIKey())), modelName, backend
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
	modelName := configuredBossHelmModelForBackend(cfg, backend)
	switch backend {
	case config.AIBackendOpenAIAPI:
		return llm.NewResponsesClient(strings.TrimSpace(cfg.OpenAIAPIKey), bossAssistantHTTPTimeout, usageTracker), modelName, backend
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		if backend == config.AIBackendOllama {
			return llm.NewOllamaJSONSchemaRunner(cfg.OpenAICompatibleBaseURL(backend), modelName, bossAssistantHTTPTimeout, usageTracker), modelName, backend
		}
		return llm.NewOpenAICompatibleResponsesRunnerWithOptions(cfg.OpenAICompatibleBaseURL(backend), cfg.OpenAICompatibleAPIKey(backend), modelName, bossAssistantHTTPTimeout, usageTracker, openAICompatibleResponsesRunnerOptions(backend, modelName, backend.UsesCloudAPIKey())), modelName, backend
	default:
		return nil, modelName, backend
	}
}

func (s *Service) NewBossUtilityJSONRunner() (llm.JSONSchemaRunner, string, config.AIBackend) {
	if s == nil {
		return nil, "", config.AIBackendUnset
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()

	backend := cfg.EffectiveBossChatBackend()
	modelName := configuredBossUtilityModelForBackend(cfg, backend)
	switch backend {
	case config.AIBackendOpenAIAPI:
		return llm.NewResponsesClient(strings.TrimSpace(cfg.OpenAIAPIKey), bossAssistantHTTPTimeout, usageTracker), modelName, backend
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		if backend == config.AIBackendOllama {
			return llm.NewOllamaJSONSchemaRunner(cfg.OpenAICompatibleBaseURL(backend), modelName, bossAssistantHTTPTimeout, usageTracker), modelName, backend
		}
		return llm.NewOpenAICompatibleResponsesRunnerWithOptions(cfg.OpenAICompatibleBaseURL(backend), cfg.OpenAICompatibleAPIKey(backend), modelName, bossAssistantHTTPTimeout, usageTracker, openAICompatibleResponsesRunnerOptions(backend, modelName, backend.UsesCloudAPIKey())), modelName, backend
	default:
		return nil, modelName, backend
	}
}

func configuredBossAssistantModel(cfg config.AppConfig) string {
	return configuredBossHelmModelForBackend(cfg, cfg.EffectiveBossChatBackend())
}

func configuredBossHelmModelForBackend(cfg config.AppConfig, backend config.AIBackend) string {
	if modelName := strings.TrimSpace(os.Getenv(brand.BossAssistantModelEnvVar)); modelName != "" {
		return modelName
	}
	if modelName := strings.TrimSpace(cfg.BossHelmModel); modelName != "" {
		return modelName
	}
	if modelName := strings.TrimSpace(cfg.BossChatModel); modelName != "" {
		return modelName
	}
	switch backend {
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi:
		return backend.DefaultBossHelmModel()
	case config.AIBackendMLX, config.AIBackendOllama:
		if modelName := strings.TrimSpace(cfg.OpenAICompatibleModel(backend)); modelName != "" {
			return modelName
		}
	}
	switch backend {
	case config.AIBackendOpenRouter, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		return ""
	}
	return config.DefaultBossHelmModel
}

func configuredBossUtilityModelForBackend(cfg config.AppConfig, backend config.AIBackend) string {
	if modelName := strings.TrimSpace(os.Getenv(brand.BossAssistantModelEnvVar)); modelName != "" {
		return modelName
	}
	if modelName := strings.TrimSpace(cfg.BossUtilityModel); modelName != "" {
		return modelName
	}
	switch backend {
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi:
		return backend.DefaultBossUtilityModel()
	case config.AIBackendMLX, config.AIBackendOllama:
		if modelName := strings.TrimSpace(cfg.OpenAICompatibleModel(backend)); modelName != "" {
			return modelName
		}
		return ""
	}
	return config.DefaultBossUtilityModel
}

func openAICompatibleResponsesRunnerOptions(backend config.AIBackend, modelName string, preferChatCompletions bool) llm.OpenAICompatibleResponsesRunnerOptions {
	return llm.OpenAICompatibleResponsesRunnerOptionsForProviderModel(string(backend), modelName, llm.OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: preferChatCompletions,
	})
}

func (s *Service) configureAIClientsLocked() {
	var (
		client          sessionclassify.Classifier
		commitAssistant *gitops.OpenAICommitMessageClient
		todoClient      todoworktree.Suggester
		titleAssessor   ScratchTaskTitleAssessor
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
		reasoning := strings.TrimSpace(s.cfg.ProjectReasoningEffort)
		commitAssistant = gitops.NewOpenAICommitMessageClientWithUsageTracker(apiKey, s.llmUsageTracker).WithReasoningEffort(reasoning)
		client = sessionclassify.NewOpenAIClientWithUsageTracker(apiKey, s.llmUsageTracker).WithReasoningEffort(reasoning)
		todoClient = todoworktree.NewOpenAIClientWithUsageTracker(apiKey, s.llmUsageTracker).WithReasoningEffort(reasoning)
		titleAssessor = newScratchTaskTitleLLMAssessor(sessionclassify.DefaultModel, llm.NewResponsesClient(apiKey, 60*time.Second, s.llmUsageTracker), reasoning)
	case config.AIBackendCodex:
		if selectedStatus.Ready {
			commitAssistant = gitops.NewCodexCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			client = sessionclassify.NewCodexClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			todoClient = todoworktree.NewCodexClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			titleAssessor = newScratchTaskTitleLLMAssessor(scratchTaskTitleLocalRunnerModel, llm.NewCodexCapacityFallbackRunner(llm.NewPersistentCodexRunnerInDataDir(s.cfg.DataDir, 60*time.Second, s.llmUsageTracker)), "")
		}
	case config.AIBackendOpenCode:
		if selectedStatus.Ready {
			tier, _ := config.ParseModelTier(s.cfg.OpenCodeModelTier)
			if s.opencodeDiscovery != nil {
				commitAssistant = gitops.NewOpenCodeCommitMessageClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
				client = sessionclassify.NewOpenCodeClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
				todoClient = todoworktree.NewOpenCodeClientWithFallback(s.opencodeDiscovery, tier, s.llmUsageTracker)
				titleCfg := llm.DefaultModelSelectionConfig()
				titleCfg.Tier = llm.ModelTier(tier)
				titleAssessor = newScratchTaskTitleLLMAssessor("", llm.NewFallbackRunner(s.opencodeDiscovery, llm.NewOpenCodeRunRunner(60*time.Second, s.llmUsageTracker), titleCfg, s.llmUsageTracker), "")
			} else {
				commitAssistant = gitops.NewOpenCodeCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
				client = sessionclassify.NewOpenCodeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
				todoClient = todoworktree.NewOpenCodeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
				titleAssessor = newScratchTaskTitleLLMAssessor(scratchTaskTitleLocalRunnerModel, llm.NewOpenCodeRunRunnerInDataDir(s.cfg.DataDir, 60*time.Second, s.llmUsageTracker), "")
			}
		}
	case config.AIBackendClaude:
		if selectedStatus.Ready {
			commitAssistant = gitops.NewClaudeCommitMessageClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			client = sessionclassify.NewClaudeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			todoClient = todoworktree.NewClaudeClientWithUsageTrackerInDataDir(s.cfg.DataDir, s.llmUsageTracker)
			titleAssessor = newScratchTaskTitleLLMAssessor(scratchTaskTitleClaudeLocalRunnerModel, llm.NewClaudePrintRunnerInDataDir(s.cfg.DataDir, 60*time.Second, s.llmUsageTracker), "")
		}
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		if selectedStatus.Ready {
			baseURL := s.cfg.OpenAICompatibleBaseURL(selectedBackend)
			apiKey := s.cfg.OpenAICompatibleAPIKey(selectedBackend)
			model := s.cfg.OpenAICompatibleModel(selectedBackend)
			if selectedBackend == config.AIBackendOllama {
				commitAssistant = gitops.NewOpenAICommitMessageClientWithRunner(model, llm.NewOllamaJSONSchemaRunner(baseURL, model, defaultCommitAssistantTimeout, s.llmUsageTracker))
				client = sessionclassify.NewClientWithRunner(model, llm.NewOllamaJSONSchemaRunner(baseURL, model, 60*time.Second, s.llmUsageTracker))
				todoClient = todoworktree.NewClientWithRunner(model, llm.NewOllamaJSONSchemaRunner(baseURL, model, defaultCommitAssistantTimeout, s.llmUsageTracker))
				titleAssessor = newScratchTaskTitleLLMAssessor(model, llm.NewOllamaJSONSchemaRunner(baseURL, model, 60*time.Second, s.llmUsageTracker), "")
			} else {
				opts := openAICompatibleResponsesRunnerOptions(selectedBackend, model, true)
				reasoning := strings.TrimSpace(s.cfg.ProjectReasoningEffort)
				commitAssistant = gitops.NewOpenAICompatibleCommitMessageClientWithUsageTrackerAndOptions(baseURL, apiKey, model, s.llmUsageTracker, opts).WithReasoningEffort(reasoning)
				client = sessionclassify.NewOpenAICompatibleClientWithUsageTrackerAndOptions(baseURL, apiKey, model, s.llmUsageTracker, opts).WithReasoningEffort(reasoning)
				todoClient = todoworktree.NewOpenAICompatibleClientWithUsageTrackerAndOptions(baseURL, apiKey, model, s.llmUsageTracker, opts).WithReasoningEffort(reasoning)
				titleAssessor = newScratchTaskTitleLLMAssessor(model, llm.NewOpenAICompatibleResponsesRunnerWithOptions(baseURL, apiKey, model, 60*time.Second, s.llmUsageTracker, opts), reasoning)
			}
		}
	}
	s.titleAssessor = titleAssessor
	unavailableReason := ""
	if client == nil {
		unavailableReason = sessionClassifierUnavailableReason(selectedBackend, selectedStatus)
	}
	s.commitMessageSuggester = commitAssistant
	s.commitTodoChecker = commitAssistant
	s.untrackedFileRecommender = commitAssistant
	if manager, ok := s.classifier.(*sessionclassify.Manager); ok {
		manager.ConfigureClientWithUnavailableReason(client, unavailableReason)
		manager.Notify()
	} else {
		s.classifier = sessionclassify.NewManager(s.store, s.bus, sessionclassify.Options{
			Client:            client,
			UnavailableReason: unavailableReason,
			OnProjectUpdated:  s.RefreshProjectStatus,
		})
	}
	if s.todoSuggester == nil {
		s.todoSuggester = todoworktree.NewManager(s.store, s.bus, todoworktree.Options{
			Client:            todoClient,
			UnavailableReason: unavailableReason,
		})
		return
	}
	s.todoSuggester.ConfigureClientWithUnavailableReason(todoClient, unavailableReason)
}

func sessionClassifierUnavailableReason(backend config.AIBackend, status aibackend.Status) string {
	switch backend {
	case config.AIBackendUnset, config.AIBackendDisabled:
		return ""
	}
	var parts []string
	label := strings.TrimSpace(backend.Label())
	if label == "" {
		label = "selected"
	}
	parts = append(parts, label+" assessment backend is not ready")
	if detail := strings.TrimSpace(status.Detail); detail != "" {
		parts = append(parts, detail)
	}
	if hint := strings.TrimSpace(status.LoginHint); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, ": ")
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
	if ctx == nil {
		ctx = context.Background()
	}
	progress := &scanProgressTracker{}
	progress.setPhase("waiting for another project scan")
	unlock, err := s.lockFullScanContext(ctx)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(err)
	}
	defer unlock()
	return s.scanWithOptions(ctx, opts, progress)
}

func (s *Service) tryScanWithOptions(ctx context.Context, opts ScanOptions) (ScanReport, error, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ScanReport{}, err, false
	}
	unlock, ok := s.tryLockFullScan()
	if !ok {
		return ScanReport{}, nil, false
	}
	defer unlock()

	progress := &scanProgressTracker{}
	returnReport, err := s.scanWithOptions(ctx, opts, progress)
	return returnReport, err, true
}

func (s *Service) scanWithOptions(ctx context.Context, opts ScanOptions, progress *scanProgressTracker) (ScanReport, error) {
	progress.setPhase("starting project scan")
	runtime := s.runtimeSnapshot()
	cfg := runtime.cfg
	classifier := runtime.classifier
	timedOutGitPaths := &scanGitMetadataTimeoutSet{}
	gitFingerprintReader := withScanGitMetadataTimeout(runtime.gitFingerprintReader, timedOutGitPaths)
	gitRepoStatusReader := withScanGitMetadataTimeout(runtime.gitRepoStatusReader, timedOutGitPaths)
	gitWorktreeInfoReader := withScanGitMetadataTimeout(runtime.gitWorktreeInfoReader, timedOutGitPaths)
	gitWorktreeListReader := withScanGitMetadataTimeout(runtime.gitWorktreeListReader, timedOutGitPaths)
	bus := runtime.bus
	now := time.Now()
	progress.setPhase("purging expired missing linked worktrees")
	if _, err := s.store.DeleteExpiredMissingLinkedWorktrees(ctx, now, missingLinkedWorktreeRetention); err != nil {
		return ScanReport{}, progress.wrapTimeout(fmt.Errorf("purge expired missing linked worktrees: %w", err))
	}
	internalWorkspaceRoot := appfs.InternalWorkspaceRoot(cfg.DataDir)
	scope := scanner.NewPathScope(cfg.IncludePaths, cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot)
	discovered := []string{}
	var err error
	if len(cfg.IncludePaths) > 0 {
		progress.setPhase("discovering git projects")
		discovered, err = scanner.DiscoverGitProjects(scanner.Discovery{
			Roots:     cfg.IncludePaths,
			MaxDepth:  4,
			SkipPaths: []string{internalWorkspaceRoot},
		})
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("discover git projects: %w", err))
		}
	}

	progress.setPhase("loading previous project state")
	oldMap, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(fmt.Errorf("load previous project state: %w", err))
	}
	if shouldDiscoverScratchTaskFolders(cfg, oldMap) {
		progress.setPhase("discovering scratch tasks")
		scratchTasks, err := discoverScratchTaskFolders(cfg.ScratchRoot)
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("discover scratch tasks: %w", err))
		}
		for _, task := range scratchTasks {
			if _, ok := oldMap[task.Path]; ok {
				continue
			}
			oldMap[task.Path] = model.ProjectSummary{
				Path:          task.Path,
				Name:          task.Title,
				Kind:          model.ProjectKindScratchTask,
				PresentOnDisk: true,
				ManuallyAdded: true,
				InScope:       true,
				CreatedAt:     task.CreatedAt,
			}
		}
	}
	progress.setPhase("expanding linked worktree paths")
	discovered, liveWorktreePathsByRoot := s.expandDiscoveredWorktreePaths(ctx, discovered, oldMap, scope, gitWorktreeInfoReader, gitWorktreeListReader)
	if err := progress.contextErr(ctx); err != nil {
		return ScanReport{}, err
	}
	progress.setPhase("updating project scope")
	for path, old := range oldMap {
		inScopeNow := scope.Allows(path) || old.ManuallyAdded
		if old.InScope == inScopeNow {
			continue
		}
		if err := s.store.SetProjectScope(ctx, path, inScopeNow); err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("set project scope: %w", err))
		}
		old.InScope = inScopeNow
		oldMap[path] = old
	}

	activityScope := scope
	filterRecentOutOfScopeActivities := len(cfg.IncludePaths) > 0
	if filterRecentOutOfScopeActivities {
		activityScope = scanner.NewPathScope(nil, cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot)
	}
	rawActivities, err := s.detectProjectActivities(ctx, activityScope, internalWorkspaceRoot, progress)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(err)
	}
	progress.setPhase("filtering detected activity")
	if filterRecentOutOfScopeActivities {
		filterIncludedOrRecentActivities(rawActivities, scope, now, recentActivityDiscoveryWindow, manuallyTrackedActivityPaths(oldMap))
	}
	finalizeDetectorActivities(rawActivities)

	progress.setPhase("loading cached git fingerprints")
	cachedFingerprints, err := s.store.GetProjectGitFingerprints(ctx)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(fmt.Errorf("load cached git fingerprints: %w", err))
	}

	currentFingerprints := map[string]scanner.GitFingerprint{}
	discoveredSet := map[string]struct{}{}
	for index, path := range discovered {
		cleanPath := filepath.Clean(path)
		progress.setProject("reading discovered project fingerprints", index+1, len(discovered), cleanPath)
		discoveredSet[cleanPath] = struct{}{}
		if gitFingerprintReader == nil || !projectPathExists(cleanPath) {
			continue
		}
		fingerprint, readErr := gitFingerprintReader(ctx, cleanPath)
		if readErr == nil {
			currentFingerprints[cleanPath] = fingerprint
		}
	}
	if err := progress.contextErr(ctx); err != nil {
		return ScanReport{}, err
	}

	progress.setPhase("detecting project moves")
	moves := s.detectProjectMoves(oldMap, discoveredSet, cachedFingerprints, currentFingerprints)
	for index, move := range moves {
		progress.setProject("persisting detected project moves", index+1, len(moves), move.NewPath)
		if err := s.store.MoveProjectPath(ctx, move.OldPath, move.NewPath, now); err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("move project path %s -> %s: %w", move.OldPath, move.NewPath, err))
		}
		if err := s.store.UpsertPathAlias(ctx, model.PathAlias{
			OldPath:   move.OldPath,
			NewPath:   move.NewPath,
			Reason:    "git_recent_hash_match",
			UpdatedAt: now,
		}); err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("persist path alias %s -> %s: %w", move.OldPath, move.NewPath, err))
		}
		s.publishProjectMoved(ctx, now, move)
	}
	if len(moves) > 0 {
		progress.setPhase("reloading project state after moves")
		oldMap, err = s.store.GetProjectSummaryMap(ctx)
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("reload project state after moves: %w", err))
		}
	}
	progress.setPhase("consolidating moved project duplicates")
	consolidatedMovedDuplicates, err := s.consolidateMovedFromProjectDuplicates(ctx, oldMap, now)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(err)
	}
	if consolidatedMovedDuplicates > 0 {
		progress.setPhase("reloading project state after duplicate consolidation")
		oldMap, err = s.store.GetProjectSummaryMap(ctx)
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("reload project state after moved duplicate consolidation: %w", err))
		}
	}

	progress.setPhase("loading path aliases")
	aliases, err := s.store.GetPathAliases(ctx)
	if err != nil {
		return ScanReport{}, progress.wrapTimeout(fmt.Errorf("load path aliases: %w", err))
	}

	progress.setPhase("normalizing detected activity")
	knownPathVariants := append([]string(nil), discovered...)
	knownPathVariants = append(knownPathVariants, mapKeys(oldMap)...)
	for path := range rawActivities {
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if cleanPath == "" || cleanPath == "." {
			continue
		}
		knownPathVariants = append(knownPathVariants, resolveProjectPath(cleanPath, aliases))
	}
	pathVariantResolver := newKnownPathVariantResolver(knownPathVariants)
	normalizeWorktreeInfoPaths := func(info scanner.GitWorktreeInfo) scanner.GitWorktreeInfo {
		if strings.TrimSpace(info.RootPath) != "" {
			info.RootPath = pathVariantResolver.preferred(info.RootPath)
		}
		if strings.TrimSpace(info.TopLevelPath) != "" {
			info.TopLevelPath = pathVariantResolver.preferred(info.TopLevelPath)
		}
		return info
	}
	worktreeInfoCache := map[string]scanner.GitWorktreeInfo{}
	worktreeInfoMisses := map[string]struct{}{}
	var worktreeInfoMu sync.Mutex
	readWorktreeInfo := func(path string) (scanner.GitWorktreeInfo, bool) {
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if cleanPath == "" || cleanPath == "." || gitWorktreeInfoReader == nil || !projectPathExists(cleanPath) {
			return scanner.GitWorktreeInfo{}, false
		}
		worktreeInfoMu.Lock()
		if info, ok := worktreeInfoCache[cleanPath]; ok {
			worktreeInfoMu.Unlock()
			return info, true
		}
		if _, ok := worktreeInfoMisses[cleanPath]; ok {
			worktreeInfoMu.Unlock()
			return scanner.GitWorktreeInfo{}, false
		}
		worktreeInfoMu.Unlock()
		info, readErr := gitWorktreeInfoReader(ctx, cleanPath)
		if readErr != nil {
			worktreeInfoMu.Lock()
			worktreeInfoMisses[cleanPath] = struct{}{}
			worktreeInfoMu.Unlock()
			return scanner.GitWorktreeInfo{}, false
		}
		info = normalizeWorktreeInfoPaths(info)
		worktreeInfoMu.Lock()
		worktreeInfoCache[cleanPath] = info
		worktreeInfoMu.Unlock()
		return info, true
	}

	activities := map[string]*model.DetectorProjectActivity{}
	for path, activity := range rawActivities {
		activityPath := resolveProjectPath(filepath.Clean(path), aliases)
		canonicalPath := canonicalGitProjectPath(activityPath, readWorktreeInfo)
		canonicalPath = pathVariantResolver.preferred(canonicalPath)
		canonicalPath = resolveProjectPath(canonicalPath, aliases)
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
	progress.setProject("reading git metadata", 0, len(paths), "")
	gitMetadata := readScanGitMetadata(ctx, paths, gitFingerprintReader, gitRepoStatusReader, readWorktreeInfo)
	if err := progress.contextErr(ctx); err != nil {
		return ScanReport{}, err
	}
	for _, metadata := range gitMetadata {
		path := metadata.path
		if !metadata.presentOnDisk {
			continue
		}
		if metadata.haveFingerprint {
			if cached, ok := cachedFingerprints[path]; ok {
				s.queueCommitTodoCheckForFingerprintChange(ctx, path, oldMap[path], cached, metadata.fingerprint)
			}
			fingerprint := model.ProjectGitFingerprint{
				ProjectPath:  path,
				HeadHash:     metadata.fingerprint.HeadHash,
				RecentHashes: append([]string(nil), metadata.fingerprint.RecentHashes...),
				UpdatedAt:    now,
			}
			if cached, ok := cachedFingerprints[path]; !ok || !sameProjectGitFingerprint(cached, fingerprint) {
				if err := s.store.UpsertProjectGitFingerprint(ctx, fingerprint); err != nil {
					return ScanReport{}, progress.wrapTimeout(fmt.Errorf("persist git fingerprint: %w", err))
				}
			}
		}
		if metadata.haveRepoStatus {
			currentRepoStatus[path] = metadata.repoStatus
		}
		if metadata.haveWorktreeInfo {
			currentWorktreeInfo[path] = metadata.worktreeInfo
		}
	}
	if s.projectStateCacheNeedsPrime(oldMap) {
		progress.setPhase("loading project state into memory")
		evidence, err := s.store.GetProjectScanEvidenceMap(ctx)
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(fmt.Errorf("load project scan evidence: %w", err))
		}
		s.primeProjectStateCache(oldMap, evidence)
	}

	updated := []string{}
	queuedClassifications := 0
	states := make([]model.ProjectState, 0, len(paths))

	for index, path := range paths {
		progress.setProject("updating project state", index+1, len(paths), path)
		unlockProjectState, err := s.lockProjectStateMutationContext(ctx, path)
		if err != nil {
			return ScanReport{}, progress.wrapTimeout(err)
		}
		activity := activities[path]
		old := oldMap[path]
		currentState, haveCurrentState, err := s.currentProjectStateForScan(ctx, path)
		if err != nil {
			unlockProjectState()
			return ScanReport{}, progress.wrapTimeout(err)
		}
		if haveCurrentState && currentState.UpdatedAt.After(now) {
			// The bulk summary snapshot was loaded earlier in this scan. Only a
			// newer in-process mutation may override those durable summary fields.
			old = overlayProjectSummaryWithState(old, currentState)
		}
		if old.SnoozedUntil != nil && now.After(*old.SnoozedUntil) {
			if err := s.store.SetSnooze(ctx, path, nil); err != nil {
				unlockProjectState()
				return ScanReport{}, progress.wrapTimeout(fmt.Errorf("clear expired snooze: %w", err))
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
		worktreeInfo, haveWorktreeInfo := currentWorktreeInfo[path]
		if presentOnDisk && shouldForgetDerivedGitSubdirProject(old, projectKind, path, worktreeInfo, haveWorktreeInfo) {
			worktreeRootPath := filepath.Clean(strings.TrimSpace(worktreeInfo.RootPath))
			worktreeKind := modelWorktreeKindFromGit(worktreeInfo.Kind)
			archived := archivedWithWorktreeRoot(old.Archived, worktreeRootPath, worktreeKind, oldMap)
			state := model.ProjectState{
				Path:                       path,
				Name:                       projectName,
				Kind:                       projectKind,
				LastActivity:               old.LastActivity,
				Status:                     model.StatusIdle,
				AttentionScore:             0,
				PresentOnDisk:              true,
				WorktreeRootPath:           worktreeRootPath,
				WorktreeKind:               worktreeKind,
				WorktreeParentBranch:       old.WorktreeParentBranch,
				WorktreeMergeStatus:        old.WorktreeMergeStatus,
				WorktreeOriginTodoID:       old.WorktreeOriginTodoID,
				RepoBranch:                 old.RepoBranch,
				RepoDirty:                  old.RepoDirty,
				RepoConflict:               old.RepoConflict,
				RepoSyncStatus:             old.RepoSyncStatus,
				RepoAheadCount:             old.RepoAheadCount,
				RepoBehindCount:            old.RepoBehindCount,
				RepoSubmoduleDirtyCount:    old.RepoSubmoduleDirtyCount,
				RepoSubmoduleUnpushedCount: old.RepoSubmoduleUnpushedCount,
				Forgotten:                  true,
				ManuallyAdded:              old.ManuallyAdded,
				InScope:                    false,
				Archived:                   archived,
				Pinned:                     old.Pinned,
				SnoozedUntil:               old.SnoozedUntil,
				RunCommand:                 old.RunCommand,
				MovedFromPath:              old.MovedFromPath,
				MovedAt:                    old.MovedAt,
				PreferredSessionSource:     old.PreferredSessionSource,
				CreatedAt:                  old.CreatedAt,
				UpdatedAt:                  now,
			}
			if s.projectStateNeedsPersistence(state) {
				if err := s.store.UpsertProjectState(ctx, state); err != nil {
					unlockProjectState()
					return ScanReport{}, progress.wrapTimeout(fmt.Errorf("persist derived subdirectory project state: %w", err))
				}
				s.rememberProjectState(state)
			}
			if projectStateChanged(old, state) {
				updated = append(updated, path)
				s.publishProjectChanged(ctx, now, state)
			}
			unlockProjectState()
			continue
		}
		worktreeRootPath := old.WorktreeRootPath
		worktreeKind := old.WorktreeKind
		worktreeParentBranch := old.WorktreeParentBranch
		worktreeMergeStatus := old.WorktreeMergeStatus
		inferredMissingLinkedWorktree := false
		if presentOnDisk && !isGitRepo {
			worktreeRootPath = ""
			worktreeKind = model.WorktreeKindNone
			worktreeParentBranch = ""
			worktreeMergeStatus = model.WorktreeMergeStatus("")
		} else if !presentOnDisk {
			if worktreeKind == model.WorktreeKindNone || strings.TrimSpace(worktreeRootPath) == "" {
				if inferredRootPath, ok := inferMissingLinkedWorktreeRoot(path); ok {
					worktreeRootPath = inferredRootPath
					worktreeKind = model.WorktreeKindLinked
					worktreeMergeStatus = model.WorktreeMergeStatus("")
					inferredMissingLinkedWorktree = true
				}
			}
		}
		repoBranch := ""
		repoDirty := false
		repoConflict := false
		repoSyncStatus := model.RepoSyncStatus("")
		repoAheadCount := 0
		repoBehindCount := 0
		repoSubmoduleDirtyCount := 0
		repoSubmoduleUnpushedCount := 0
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
				repoSubmoduleDirtyCount = repoStatus.SubmoduleDirtyCount()
				repoSubmoduleUnpushedCount = repoStatus.SubmoduleUnpushedCount()
			} else if isGitRepo {
				repoBranch = old.RepoBranch
				repoDirty = old.RepoDirty
				repoConflict = old.RepoConflict
				repoSyncStatus = old.RepoSyncStatus
				repoAheadCount = old.RepoAheadCount
				repoBehindCount = old.RepoBehindCount
				repoSubmoduleDirtyCount = old.RepoSubmoduleDirtyCount
				repoSubmoduleUnpushedCount = old.RepoSubmoduleUnpushedCount
			}
			worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, worktreeRootPath, worktreeKind, repoBranch, worktreeParentBranch)
		}
		archived := archivedWithWorktreeRoot(old.Archived, worktreeRootPath, worktreeKind, oldMap)
		forgotten := old.Forgotten
		staleLinkedWorktree := false
		if worktreeKind == model.WorktreeKindLinked && liveLinkedWorktreeMissing(liveWorktreePathsByRoot, worktreeRootPath, path) {
			staleLinkedWorktree = true
			forgotten = true
		}
		if inferredMissingLinkedWorktree {
			forgotten = true
		}
		if presentOnDisk && forgotten && scope.Allows(path) && !staleLinkedWorktree {
			forgotten = false
		}
		if forgotten && !presentOnDisk {
			if inferredMissingLinkedWorktree {
				if err := s.store.SetProjectWorktreeInfo(ctx, path, worktreeRootPath, worktreeKind); err != nil {
					unlockProjectState()
					return ScanReport{}, progress.wrapTimeout(fmt.Errorf("record inferred missing worktree info: %w", err))
				}
			}
			if err := s.store.SetForgotten(ctx, path, true); err != nil {
				unlockProjectState()
				return ScanReport{}, progress.wrapTimeout(fmt.Errorf("mark forgotten worktree: %w", err))
			}
			if err := s.store.SetProjectPresence(ctx, path, false); err != nil {
				unlockProjectState()
				return ScanReport{}, progress.wrapTimeout(fmt.Errorf("mark missing worktree: %w", err))
			}
			if worktreeKind == model.WorktreeKindLinked {
				if _, err := s.store.ClearTodoWorkForProjectPath(ctx, path); err != nil {
					unlockProjectState()
					return ScanReport{}, progress.wrapTimeout(fmt.Errorf("clear TODO work session for missing worktree: %w", err))
				}
			}
			s.forgetProjectState(path)
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
		if haveCurrentState {
			sessions = preserveCurrentSessionsNewerThan(sessions, currentState.Sessions, now)
			artifacts = preserveCurrentArtifactsNewerThan(artifacts, currentState.Artifacts, now)
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
			RepoSubmoduleDirtyCount:    repoSubmoduleDirtyCount,
			RepoSubmoduleUnpushedCount: repoSubmoduleUnpushedCount,
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
			Path:                       path,
			Name:                       projectName,
			Kind:                       projectKind,
			LastActivity:               lastActivity,
			Status:                     score.Status,
			AttentionScore:             score.Score,
			PresentOnDisk:              presentOnDisk,
			WorktreeRootPath:           worktreeRootPath,
			WorktreeKind:               worktreeKind,
			WorktreeParentBranch:       worktreeParentBranch,
			WorktreeMergeStatus:        worktreeMergeStatus,
			WorktreeOriginTodoID:       old.WorktreeOriginTodoID,
			RepoBranch:                 repoBranch,
			RepoDirty:                  repoDirty,
			RepoConflict:               repoConflict,
			RepoSyncStatus:             repoSyncStatus,
			RepoAheadCount:             repoAheadCount,
			RepoBehindCount:            repoBehindCount,
			RepoSubmoduleDirtyCount:    repoSubmoduleDirtyCount,
			RepoSubmoduleUnpushedCount: repoSubmoduleUnpushedCount,
			Forgotten:                  forgotten,
			ManuallyAdded:              old.ManuallyAdded,
			InScope:                    scope.Allows(path) || old.ManuallyAdded,
			Archived:                   archived,
			Pinned:                     old.Pinned,
			SnoozedUntil:               old.SnoozedUntil,
			RunCommand:                 old.RunCommand,
			MovedFromPath:              old.MovedFromPath,
			MovedAt:                    old.MovedAt,
			PreferredSessionSource:     old.PreferredSessionSource,
			AttentionReason:            score.Reasons,
			Sessions:                   sessions,
			Artifacts:                  artifacts,
			CreatedAt:                  old.CreatedAt,
			UpdatedAt:                  now,
		}

		if s.projectStateNeedsPersistence(state) {
			if err := s.store.UpsertProjectState(ctx, state); err != nil {
				unlockProjectState()
				return ScanReport{}, progress.wrapTimeout(fmt.Errorf("persist project state: %w", err))
			}
			s.rememberProjectState(state)
		}
		if classifier != nil {
			queued, err := queueProjectClassification(ctx, classifier, state, opts)
			if err != nil {
				unlockProjectState()
				return ScanReport{}, progress.wrapTimeout(fmt.Errorf("queue session classification: %w", err))
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

	gitMetadataTimeoutCount, gitMetadataTimeoutPaths := timedOutGitPaths.snapshot(scanGitMetadataTimeoutPathLimit)
	report := ScanReport{
		At:                            now,
		ActivityProjectCount:          len(activities),
		TrackedProjectCount:           len(states),
		UpdatedProjects:               updated,
		QueuedClassifications:         queuedClassifications,
		GitMetadataTimeoutCount:       gitMetadataTimeoutCount,
		GitMetadataTimeoutPathSamples: gitMetadataTimeoutPaths,
		States:                        states,
	}
	if queuedClassifications > 0 && classifier != nil {
		progress.setPhase("notifying queued classifications")
		classifier.Notify()
	}
	progress.setPhase("reconciling linked worktree archive state")
	if _, err := s.store.ReconcileLinkedWorktreeArchiveState(ctx); err != nil {
		return ScanReport{}, progress.wrapTimeout(err)
	}

	if bus != nil {
		progress.setPhase("publishing scan completion")
		bus.Publish(events.Event{
			Type: events.ScanCompleted,
			At:   now,
			Payload: map[string]string{
				"updated":                           fmt.Sprintf("%d", len(updated)),
				"queued_classifications":            fmt.Sprintf("%d", queuedClassifications),
				"git_metadata_timeouts":             fmt.Sprintf("%d", gitMetadataTimeoutCount),
				"git_metadata_timeout_path_samples": strings.Join(gitMetadataTimeoutPaths, "\n"),
			},
		})
	}

	return report, nil
}

type scanGitMetadataResult struct {
	path             string
	presentOnDisk    bool
	fingerprint      scanner.GitFingerprint
	haveFingerprint  bool
	repoStatus       scanner.GitRepoStatus
	haveRepoStatus   bool
	worktreeInfo     scanner.GitWorktreeInfo
	haveWorktreeInfo bool
}

func readScanGitMetadata(
	ctx context.Context,
	paths []string,
	gitFingerprintReader func(context.Context, string) (scanner.GitFingerprint, error),
	gitRepoStatusReader func(context.Context, string) (scanner.GitRepoStatus, error),
	readWorktreeInfo func(string) (scanner.GitWorktreeInfo, bool),
) []scanGitMetadataResult {
	results := make([]scanGitMetadataResult, len(paths))
	for i, path := range paths {
		results[i].path = path
	}
	if len(paths) == 0 {
		return results
	}

	concurrency := scanGitMetadataConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(paths) {
		concurrency = len(paths)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				path := paths[index]
				result := scanGitMetadataResult{
					path:          path,
					presentOnDisk: projectPathExists(path),
				}
				if !result.presentOnDisk {
					results[index] = result
					continue
				}
				if ctx.Err() != nil {
					results[index] = result
					continue
				}
				if gitFingerprintReader != nil {
					if fingerprint, err := gitFingerprintReader(ctx, path); err == nil {
						result.fingerprint = fingerprint
						result.haveFingerprint = true
					}
				}
				if ctx.Err() != nil {
					results[index] = result
					continue
				}
				if gitRepoStatusReader != nil {
					if repoStatus, err := gitRepoStatusReader(ctx, path); err == nil {
						result.repoStatus = repoStatus
						result.haveRepoStatus = true
					}
				}
				if ctx.Err() != nil {
					results[index] = result
					continue
				}
				if readWorktreeInfo != nil {
					if worktreeInfo, ok := readWorktreeInfo(path); ok {
						result.worktreeInfo = worktreeInfo
						result.haveWorktreeInfo = true
					}
				}
				results[index] = result
			}
		}()
	}

sendLoop:
	for i := range paths {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

func (s *Service) currentProjectStateForScan(ctx context.Context, path string) (model.ProjectState, bool, error) {
	if state, ok := s.cachedProjectState(path); ok {
		return state, true, nil
	}
	detail, err := s.store.GetProjectDetail(ctx, path, 20)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.HasPrefix(err.Error(), "project not found:") {
			return model.ProjectState{}, false, nil
		}
		return model.ProjectState{}, false, fmt.Errorf("reload project state for scan: %w", err)
	}
	state := projectStateFromDetail(detail)
	s.rememberProjectState(state)
	return state, true, nil
}

func (s *Service) consolidateMovedFromProjectDuplicates(ctx context.Context, projects map[string]model.ProjectSummary, now time.Time) (int, error) {
	targetsByOldPath := map[string][]string{}
	for targetPath, project := range projects {
		oldPath := filepath.Clean(strings.TrimSpace(project.MovedFromPath))
		targetPath = filepath.Clean(targetPath)
		if oldPath == "" || oldPath == "." || oldPath == targetPath {
			continue
		}
		if _, ok := projects[oldPath]; !ok {
			continue
		}
		targetsByOldPath[oldPath] = append(targetsByOldPath[oldPath], targetPath)
	}

	oldPaths := make([]string, 0, len(targetsByOldPath))
	for oldPath := range targetsByOldPath {
		oldPaths = append(oldPaths, oldPath)
	}
	sort.Strings(oldPaths)

	consolidated := 0
	for _, oldPath := range oldPaths {
		targets := targetsByOldPath[oldPath]
		sort.Strings(targets)
		if len(targets) != 1 || projectPathExists(oldPath) {
			continue
		}
		newPath := targets[0]
		if err := s.store.ConsolidateProjectPath(ctx, oldPath, newPath, now); err != nil {
			return consolidated, fmt.Errorf("consolidate moved duplicate %s -> %s: %w", oldPath, newPath, err)
		}
		if err := s.store.UpsertPathAlias(ctx, model.PathAlias{
			OldPath:   oldPath,
			NewPath:   newPath,
			Reason:    "moved_from_project_duplicate",
			UpdatedAt: now,
		}); err != nil {
			return consolidated, fmt.Errorf("persist moved duplicate alias %s -> %s: %w", oldPath, newPath, err)
		}
		consolidated++
	}
	return consolidated, nil
}

func (s *Service) detectProjectActivities(ctx context.Context, scope scanner.PathScope, internalWorkspaceRoot string, progress *scanProgressTracker) (map[string]*model.DetectorProjectActivity, error) {
	out := map[string]*model.DetectorProjectActivity{}
	for _, detector := range s.detectors {
		if detector == nil {
			continue
		}
		progress.setDetector("detecting project activity", detector.Name())
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

func manuallyTrackedActivityPaths(projects map[string]model.ProjectSummary) map[string]struct{} {
	out := map[string]struct{}{}
	for path, project := range projects {
		if !project.ManuallyAdded {
			continue
		}
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if cleanPath == "" || cleanPath == "." {
			continue
		}
		out[cleanPath] = struct{}{}
	}
	return out
}

func filterIncludedOrRecentActivities(activities map[string]*model.DetectorProjectActivity, scope scanner.PathScope, now time.Time, recentWindow time.Duration, alwaysKeep map[string]struct{}) {
	for path, activity := range activities {
		activityPath := filepath.Clean(path)
		if activity != nil && strings.TrimSpace(activity.ProjectPath) != "" {
			activityPath = filepath.Clean(activity.ProjectPath)
		}
		if _, ok := alwaysKeep[activityPath]; ok {
			continue
		}
		if scope.Allows(activityPath) {
			continue
		}
		if isRecentSessionActivity(now, activity, recentWindow) {
			continue
		}
		delete(activities, path)
	}
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

func inferMissingLinkedWorktreeRoot(projectPath string) (string, bool) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." || projectPathExists(projectPath) {
		return "", false
	}
	dir := filepath.Dir(projectPath)
	name := filepath.Base(projectPath)
	searchUntil := len(name)
	for {
		idx := strings.LastIndex(name[:searchUntil], "--")
		if idx <= 0 {
			return "", false
		}
		candidate := filepath.Clean(filepath.Join(dir, name[:idx]))
		if candidate != projectPath && projectPathExists(candidate) && projectIsGitRepo(candidate) {
			return candidate, true
		}
		searchUntil = idx
	}
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
		old.RepoSubmoduleDirtyCount != state.RepoSubmoduleDirtyCount ||
		old.RepoSubmoduleUnpushedCount != state.RepoSubmoduleUnpushedCount ||
		old.Forgotten != state.Forgotten ||
		old.ManuallyAdded != state.ManuallyAdded ||
		old.Archived != state.Archived ||
		!timesEqual(old.LastActivity, state.LastActivity)
}

func (s *Service) publishProjectChanged(ctx context.Context, now time.Time, state model.ProjectState) {
	payload := map[string]string{
		"status":                   string(state.Status),
		"score":                    fmt.Sprintf("%d", state.AttentionScore),
		"dirty":                    fmt.Sprintf("%t", state.RepoDirty),
		"conflict":                 fmt.Sprintf("%t", state.RepoConflict),
		"merged":                   string(state.WorktreeMergeStatus),
		"remote":                   string(state.RepoSyncStatus),
		"submodule_dirty_count":    fmt.Sprintf("%d", state.RepoSubmoduleDirtyCount),
		"submodule_unpushed_count": fmt.Sprintf("%d", state.RepoSubmoduleUnpushedCount),
	}
	s.bus.Publish(events.Event{Type: events.ProjectChanged, At: now, ProjectPath: state.Path, Payload: payload})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: state.Path,
		Type:        string(events.ProjectChanged),
		Payload:     fmt.Sprintf("status=%s score=%d dirty=%t conflict=%t merged=%s remote=%s submodules_dirty=%d submodules_unpushed=%d", state.Status, state.AttentionScore, state.RepoDirty, state.RepoConflict, state.WorktreeMergeStatus, state.RepoSyncStatus, state.RepoSubmoduleDirtyCount, state.RepoSubmoduleUnpushedCount),
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

func canonicalGitProjectPath(path string, readWorktreeInfo func(string) (scanner.GitWorktreeInfo, bool)) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || readWorktreeInfo == nil {
		return path
	}
	info, ok := readWorktreeInfo(path)
	if !ok {
		return path
	}
	topLevelPath := filepath.Clean(strings.TrimSpace(info.TopLevelPath))
	if topLevelPath == "" || topLevelPath == "." {
		return path
	}
	return topLevelPath
}

func shouldForgetDerivedGitSubdirProject(old model.ProjectSummary, projectKind model.ProjectKind, path string, info scanner.GitWorktreeInfo, haveInfo bool) bool {
	if !haveInfo || old.ManuallyAdded || old.Pinned || model.NormalizeProjectKind(projectKind) != model.ProjectKindProject {
		return false
	}
	if old.OpenTODOCount > 0 || old.TotalTODOCount > 0 || strings.TrimSpace(old.RunCommand) != "" {
		return false
	}
	path = filepath.Clean(strings.TrimSpace(path))
	topLevelPath := filepath.Clean(strings.TrimSpace(info.TopLevelPath))
	return path != "" && path != "." && topLevelPath != "" && topLevelPath != "." && topLevelPath != path
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
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	runtime := s.runtimeSnapshot()
	gitRepoStatusReader := withScanGitMetadataTimeout(runtime.gitRepoStatusReader, nil)
	gitWorktreeInfoReader := withScanGitMetadataTimeout(runtime.gitWorktreeInfoReader, nil)
	gitWorktreeListReader := withScanGitMetadataTimeout(runtime.gitWorktreeListReader, nil)
	now := time.Now()
	initialDetail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		return err
	}
	metadata := s.readProjectStatusRefreshMetadata(ctx, initialDetail.Summary, gitRepoStatusReader, gitWorktreeInfoReader, gitWorktreeListReader)

	refreshLinkedWorktrees := false
	refreshLinkedRootPath := ""
	if err := func() error {
		unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
		if err != nil {
			return err
		}
		defer unlockProjectState()

		detail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
		if err != nil {
			return err
		}

		worktreeRootPath := detail.Summary.WorktreeRootPath
		worktreeKind := detail.Summary.WorktreeKind
		worktreeParentBranch := detail.Summary.WorktreeParentBranch
		worktreeMergeStatus := detail.Summary.WorktreeMergeStatus
		repoBranch := ""
		repoDirty := false
		repoConflict := false
		repoSyncStatus := model.RepoSyncStatus("")
		repoAheadCount := 0
		repoBehindCount := 0
		repoSubmoduleDirtyCount := 0
		repoSubmoduleUnpushedCount := 0
		if metadata.presentOnDisk && !metadata.isGitRepo {
			worktreeRootPath = ""
			worktreeKind = model.WorktreeKindNone
			worktreeParentBranch = ""
			worktreeMergeStatus = model.WorktreeMergeStatus("")
		} else if metadata.presentOnDisk {
			worktreeRootPath = metadata.worktreeRootPath
			worktreeKind = metadata.worktreeKind
			worktreeMergeStatus = metadata.worktreeMergeStatus
			repoBranch = metadata.repoBranch
			repoDirty = metadata.repoDirty
			repoConflict = metadata.repoConflict
			repoSyncStatus = metadata.repoSyncStatus
			repoAheadCount = metadata.repoAheadCount
			repoBehindCount = metadata.repoBehindCount
			repoSubmoduleDirtyCount = metadata.repoSubmoduleDirtyCount
			repoSubmoduleUnpushedCount = metadata.repoSubmoduleUnpushedCount
			if !metadata.haveRepoStatus && metadata.isGitRepo {
				repoBranch = detail.Summary.RepoBranch
				repoDirty = detail.Summary.RepoDirty
				repoConflict = detail.Summary.RepoConflict
				repoSyncStatus = detail.Summary.RepoSyncStatus
				repoAheadCount = detail.Summary.RepoAheadCount
				repoBehindCount = detail.Summary.RepoBehindCount
				repoSubmoduleDirtyCount = detail.Summary.RepoSubmoduleDirtyCount
				repoSubmoduleUnpushedCount = detail.Summary.RepoSubmoduleUnpushedCount
			}
		}

		forgotten := detail.Summary.Forgotten
		if metadata.staleLinkedWorktree {
			forgotten = true
		}
		if metadata.presentOnDisk && forgotten && detail.Summary.InScope && !metadata.staleLinkedWorktree {
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
		if _, err := s.persistProjectStateUpdate(ctx, detail, now, projectStatusRefreshOverrides{
			presentOnDisk:              metadata.presentOnDisk,
			worktreeRootPath:           worktreeRootPath,
			worktreeKind:               worktreeKind,
			worktreeParentBranch:       worktreeParentBranch,
			worktreeMergeStatus:        worktreeMergeStatus,
			repoBranch:                 repoBranch,
			repoDirty:                  repoDirty,
			repoConflict:               repoConflict,
			repoSyncStatus:             repoSyncStatus,
			repoAheadCount:             repoAheadCount,
			repoBehindCount:            repoBehindCount,
			repoSubmoduleDirtyCount:    repoSubmoduleDirtyCount,
			repoSubmoduleUnpushedCount: repoSubmoduleUnpushedCount,
			forgotten:                  forgotten,
			archived:                   detail.Summary.Archived,
		}, runtime.cfg, runtime.classifier, opts); err != nil {
			return err
		}
		if worktreeKind == model.WorktreeKindMain && !opts.SkipLinkedWorktreeStatusRefresh {
			refreshLinkedWorktrees = true
			refreshLinkedRootPath = detail.Summary.Path
		}
		return nil
	}(); err != nil {
		return err
	}
	if refreshLinkedWorktrees {
		return s.refreshLinkedWorktreeStatusesForRoot(ctx, refreshLinkedRootPath)
	}
	return nil
}

type projectStatusRefreshMetadata struct {
	presentOnDisk              bool
	isGitRepo                  bool
	worktreeRootPath           string
	worktreeKind               model.WorktreeKind
	worktreeMergeStatus        model.WorktreeMergeStatus
	repoBranch                 string
	repoDirty                  bool
	repoConflict               bool
	repoSyncStatus             model.RepoSyncStatus
	repoAheadCount             int
	repoBehindCount            int
	repoSubmoduleDirtyCount    int
	repoSubmoduleUnpushedCount int
	haveRepoStatus             bool
	staleLinkedWorktree        bool
}

func (s *Service) readProjectStatusRefreshMetadata(
	ctx context.Context,
	summary model.ProjectSummary,
	gitRepoStatusReader func(context.Context, string) (scanner.GitRepoStatus, error),
	gitWorktreeInfoReader func(context.Context, string) (scanner.GitWorktreeInfo, error),
	gitWorktreeListReader func(context.Context, string) ([]scanner.GitWorktree, error),
) projectStatusRefreshMetadata {
	meta := projectStatusRefreshMetadata{
		worktreeRootPath:           summary.WorktreeRootPath,
		worktreeKind:               summary.WorktreeKind,
		worktreeMergeStatus:        summary.WorktreeMergeStatus,
		repoBranch:                 summary.RepoBranch,
		repoDirty:                  summary.RepoDirty,
		repoConflict:               summary.RepoConflict,
		repoSyncStatus:             summary.RepoSyncStatus,
		repoAheadCount:             summary.RepoAheadCount,
		repoBehindCount:            summary.RepoBehindCount,
		repoSubmoduleDirtyCount:    summary.RepoSubmoduleDirtyCount,
		repoSubmoduleUnpushedCount: summary.RepoSubmoduleUnpushedCount,
	}
	projectPath := filepath.Clean(strings.TrimSpace(summary.Path))
	meta.presentOnDisk = projectPathExists(projectPath)
	meta.isGitRepo = meta.presentOnDisk && projectIsGitRepo(projectPath)
	if !meta.presentOnDisk {
		meta.staleLinkedWorktree = s.staleLinkedWorktreeOnDiskWithReader(ctx, meta.worktreeRootPath, meta.worktreeKind, projectPath, gitWorktreeListReader)
		return meta
	}
	if !meta.isGitRepo {
		meta.worktreeRootPath = ""
		meta.worktreeKind = model.WorktreeKindNone
		meta.worktreeMergeStatus = model.WorktreeMergeStatus("")
		meta.repoBranch = ""
		meta.repoDirty = false
		meta.repoConflict = false
		meta.repoSyncStatus = model.RepoSyncStatus("")
		meta.repoAheadCount = 0
		meta.repoBehindCount = 0
		meta.repoSubmoduleDirtyCount = 0
		meta.repoSubmoduleUnpushedCount = 0
		return meta
	}
	if nextRootPath, nextKind := s.readProjectWorktreeInfoWithReader(ctx, projectPath, gitWorktreeInfoReader); nextRootPath != "" || nextKind != model.WorktreeKindNone {
		meta.worktreeRootPath = nextRootPath
		meta.worktreeKind = nextKind
	}
	if gitRepoStatusReader != nil {
		if repoStatus, err := gitRepoStatusReader(ctx, projectPath); err == nil {
			meta.repoBranch = strings.TrimSpace(repoStatus.Branch)
			meta.repoDirty = repoStatus.Dirty
			meta.repoConflict = repoConflictFromGit(repoStatus)
			meta.repoSyncStatus = repoSyncStatusFromGit(repoStatus)
			meta.repoAheadCount = repoStatus.Ahead
			meta.repoBehindCount = repoStatus.Behind
			meta.repoSubmoduleDirtyCount = repoStatus.SubmoduleDirtyCount()
			meta.repoSubmoduleUnpushedCount = repoStatus.SubmoduleUnpushedCount()
			meta.haveRepoStatus = true
		}
	}
	meta.worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, meta.worktreeRootPath, meta.worktreeKind, meta.repoBranch, summary.WorktreeParentBranch)
	// Keep linked-worktree cleanup consistent with ScanOnce: once a linked
	// checkout disappears from `git worktree list`, treat it as stale even if
	// the directory itself is already gone. This prevents async status refreshes
	// from briefly re-surfacing removed worktrees as plain "missing" folders.
	meta.staleLinkedWorktree = s.staleLinkedWorktreeOnDiskWithReader(ctx, meta.worktreeRootPath, meta.worktreeKind, projectPath, gitWorktreeListReader)
	return meta
}

func (s *Service) refreshLinkedWorktreeStatusesForRoot(ctx context.Context, rootPath string) error {
	if s == nil || s.store == nil {
		return nil
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return nil
	}
	projectPaths, err := s.store.ListLinkedWorktreePathsForRoot(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("list linked worktrees for root refresh: %w", err)
	}
	var errs []string
	for _, path := range projectPaths {
		projectPath := filepath.Clean(strings.TrimSpace(path))
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
	presentOnDisk              bool
	worktreeRootPath           string
	worktreeKind               model.WorktreeKind
	worktreeParentBranch       string
	worktreeMergeStatus        model.WorktreeMergeStatus
	repoBranch                 string
	repoDirty                  bool
	repoConflict               bool
	repoSyncStatus             model.RepoSyncStatus
	repoAheadCount             int
	repoBehindCount            int
	repoSubmoduleDirtyCount    int
	repoSubmoduleUnpushedCount int
	forgotten                  bool
	archived                   bool
}

func (s *Service) persistProjectStateUpdate(ctx context.Context, detail model.ProjectDetail, now time.Time, overrides projectStatusRefreshOverrides, cfg config.AppConfig, classifier SessionClassifier, opts ScanOptions) (model.ProjectState, error) {
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
		RepoSubmoduleDirtyCount:    overrides.repoSubmoduleDirtyCount,
		RepoSubmoduleUnpushedCount: overrides.repoSubmoduleUnpushedCount,
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
		Path:                       detail.Summary.Path,
		Name:                       detail.Summary.Name,
		Kind:                       model.NormalizeProjectKind(detail.Summary.Kind),
		LastActivity:               detail.Summary.LastActivity,
		Status:                     score.Status,
		AttentionScore:             score.Score,
		PresentOnDisk:              overrides.presentOnDisk,
		WorktreeRootPath:           overrides.worktreeRootPath,
		WorktreeKind:               overrides.worktreeKind,
		WorktreeParentBranch:       overrides.worktreeParentBranch,
		WorktreeMergeStatus:        overrides.worktreeMergeStatus,
		WorktreeOriginTodoID:       detail.Summary.WorktreeOriginTodoID,
		RepoBranch:                 overrides.repoBranch,
		RepoDirty:                  overrides.repoDirty,
		RepoConflict:               overrides.repoConflict,
		RepoSyncStatus:             overrides.repoSyncStatus,
		RepoAheadCount:             overrides.repoAheadCount,
		RepoBehindCount:            overrides.repoBehindCount,
		RepoSubmoduleDirtyCount:    overrides.repoSubmoduleDirtyCount,
		RepoSubmoduleUnpushedCount: overrides.repoSubmoduleUnpushedCount,
		Forgotten:                  overrides.forgotten,
		ManuallyAdded:              detail.Summary.ManuallyAdded,
		InScope:                    detail.Summary.InScope,
		Archived:                   overrides.archived,
		Pinned:                     detail.Summary.Pinned,
		SnoozedUntil:               detail.Summary.SnoozedUntil,
		RunCommand:                 detail.Summary.RunCommand,
		MovedFromPath:              detail.Summary.MovedFromPath,
		MovedAt:                    detail.Summary.MovedAt,
		PreferredSessionSource:     detail.Summary.PreferredSessionSource,
		AttentionReason:            score.Reasons,
		Sessions:                   detail.Sessions,
		Artifacts:                  detail.Artifacts,
		CreatedAt:                  detail.Summary.CreatedAt,
		UpdatedAt:                  now,
	}
	if err := s.store.UpsertProjectState(ctx, state); err != nil {
		return model.ProjectState{}, fmt.Errorf("persist refreshed project state: %w", err)
	}
	s.rememberProjectState(state)
	if classifier != nil {
		queued, err := queueProjectClassification(ctx, classifier, state, opts)
		if err != nil {
			return model.ProjectState{}, fmt.Errorf("queue session classification: %w", err)
		}
		if queued {
			classifier.Notify()
		}
	}
	if projectStateChanged(detail.Summary, state) {
		s.publishProjectChanged(ctx, now, state)
	}
	return state, nil
}

func (s *Service) refreshProjectAttention(ctx context.Context, projectPath string) error {
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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
	_, err = s.persistProjectStateUpdate(ctx, detail, now, projectStatusRefreshOverrides{
		presentOnDisk:              detail.Summary.PresentOnDisk,
		worktreeRootPath:           detail.Summary.WorktreeRootPath,
		worktreeKind:               detail.Summary.WorktreeKind,
		worktreeParentBranch:       detail.Summary.WorktreeParentBranch,
		worktreeMergeStatus:        detail.Summary.WorktreeMergeStatus,
		repoBranch:                 detail.Summary.RepoBranch,
		repoDirty:                  detail.Summary.RepoDirty,
		repoConflict:               detail.Summary.RepoConflict,
		repoSyncStatus:             detail.Summary.RepoSyncStatus,
		repoAheadCount:             detail.Summary.RepoAheadCount,
		repoBehindCount:            detail.Summary.RepoBehindCount,
		repoSubmoduleDirtyCount:    detail.Summary.RepoSubmoduleDirtyCount,
		repoSubmoduleUnpushedCount: detail.Summary.RepoSubmoduleUnpushedCount,
		forgotten:                  detail.Summary.Forgotten,
		archived:                   detail.Summary.Archived,
	}, cfg, nil, ScanOptions{})
	return err
}

func (s *Service) TogglePin(ctx context.Context, projectPath string) error {
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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

func (s *Service) ArchiveProject(ctx context.Context, projectPath string) error {
	return s.setProjectArchived(ctx, projectPath, true)
}

func (s *Service) UnarchiveProject(ctx context.Context, projectPath string) error {
	return s.setProjectArchived(ctx, projectPath, false)
}

func (s *Service) ArchiveProjects(ctx context.Context, projectPaths []string) error {
	return s.setProjectsArchived(ctx, projectPaths, true)
}

func (s *Service) UnarchiveProjects(ctx context.Context, projectPaths []string) error {
	return s.setProjectsArchived(ctx, projectPaths, false)
}

func (s *Service) setProjectArchived(ctx context.Context, projectPath string, archived bool) error {
	return s.setProjectsArchived(ctx, []string{projectPath}, archived)
}

func (s *Service) setProjectsArchived(ctx context.Context, projectPaths []string, archived bool) error {
	paths := cleanProjectPathList(projectPaths)
	if len(paths) == 0 {
		return fmt.Errorf("project path is required")
	}

	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return err
	}
	if !archived {
		if err := validateLinkedWorktreeUnarchiveTargets(projects, paths); err != nil {
			return err
		}
	}
	paths, err = expandProjectArchiveFamilyPaths(projects, paths)
	if err != nil {
		return err
	}

	unlockProjectState, err := s.lockProjectStateMutationsContext(ctx, paths)
	if err != nil {
		return err
	}
	defer unlockProjectState()

	projects, err = s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return err
	}

	changedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		project := projects[path]
		if project.Path == "" {
			return fmt.Errorf("project not found: %s", path)
		}
		if project.Archived == archived {
			continue
		}
		changedPaths = append(changedPaths, path)
	}
	if len(changedPaths) == 0 {
		return nil
	}

	if err := s.store.SetProjectsArchived(ctx, changedPaths, archived); err != nil {
		return err
	}
	now := time.Now()
	action := "archive_project"
	if !archived {
		action = "unarchive_project"
	}
	for _, path := range changedPaths {
		s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: path, Payload: map[string]string{"action": action}})
		_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: path, Type: string(events.ActionApplied), Payload: action})
	}
	return nil
}

func validateLinkedWorktreeUnarchiveTargets(projects map[string]model.ProjectSummary, projectPaths []string) error {
	targets := make(map[string]struct{}, len(projectPaths))
	for _, path := range projectPaths {
		targets[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	for path := range targets {
		project := projects[path]
		if project.Path == "" || project.WorktreeKind != model.WorktreeKindLinked {
			continue
		}
		rootPath := filepath.Clean(strings.TrimSpace(project.WorktreeRootPath))
		if rootPath == "" || rootPath == "." || rootPath == path {
			continue
		}
		root := projects[rootPath]
		if root.Path == "" || !root.Archived {
			continue
		}
		if _, ok := targets[rootPath]; ok {
			continue
		}
		return fmt.Errorf("linked worktree %s cannot be unarchived while repository root %s is archived; unarchive the root instead", path, rootPath)
	}
	return nil
}

func expandProjectArchiveFamilyPaths(projects map[string]model.ProjectSummary, projectPaths []string) ([]string, error) {
	paths := cleanProjectPathList(projectPaths)
	seen := make(map[string]struct{}, len(paths))
	familyRoots := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		project := projects[path]
		if project.Path == "" {
			return nil, fmt.Errorf("project not found: %s", path)
		}
		seen[path] = struct{}{}

		rootPath := filepath.Clean(strings.TrimSpace(project.WorktreeRootPath))
		if project.WorktreeKind != model.WorktreeKindLinked || rootPath == "" || rootPath == "." || rootPath == path {
			familyRoots[path] = struct{}{}
		}
	}

	linkedPaths := make([]string, 0)
	for _, project := range projects {
		if project.WorktreeKind != model.WorktreeKindLinked {
			continue
		}
		rootPath := filepath.Clean(strings.TrimSpace(project.WorktreeRootPath))
		if _, ok := familyRoots[rootPath]; !ok {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		linkedPaths = append(linkedPaths, path)
	}
	sort.Strings(linkedPaths)
	return append(paths, linkedPaths...), nil
}

func archivedWithWorktreeRoot(archived bool, rootPath string, kind model.WorktreeKind, projects map[string]model.ProjectSummary) bool {
	if archived || kind != model.WorktreeKindLinked {
		return archived
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return false
	}
	root := projects[rootPath]
	return root.Path != "" && root.Archived
}

func cleanProjectPathList(projectPaths []string) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(projectPaths))
	for _, projectPath := range projectPaths {
		projectPath = filepath.Clean(strings.TrimSpace(projectPath))
		if projectPath == "" || projectPath == "." {
			continue
		}
		if _, ok := seen[projectPath]; ok {
			continue
		}
		seen[projectPath] = struct{}{}
		paths = append(paths, projectPath)
	}
	return paths
}

func (s *Service) Snooze(ctx context.Context, projectPath string, duration time.Duration) error {
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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

// refreshProjectAttentionAsync keeps heartbeat-derived attention state current
// without launching git reads or linked-worktree cascades.
func (s *Service) refreshProjectAttentionAsync(projectPath string) {
	s.requestProjectRefreshAsync(projectPath, asyncProjectRefreshAttention)
}

// refreshProjectStatusAsync keeps TODO mutations asynchronous while limiting
// them to the affected project. Background refreshes are serialized so SQLite
// capacity and CPU time remain available for interactive TUI reads.
func (s *Service) refreshProjectStatusAsync(projectPath string) {
	s.requestProjectRefreshAsync(projectPath, asyncProjectRefreshStatus)
}

func (s *Service) requestProjectRefreshAsync(projectPath string, kind asyncProjectRefreshKind) {
	if s == nil {
		return
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return
	}
	if !s.beginAsyncProjectRefresh(projectPath, kind) {
		return
	}
	go func(path string) {
		for {
			release := s.acquireAsyncProjectRefreshSlot()
			nextKind, ok := s.takeAsyncProjectRefreshRequest(path)
			if !ok {
				release()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), asyncProjectRefreshTimeout)
			_ = s.projectRefreshRunner(nextKind)(ctx, path)
			cancel()
			release()
			if !s.finishAsyncProjectRefresh(path) {
				return
			}
		}
	}(projectPath)
}

func (s *Service) projectRefreshRunner(kind asyncProjectRefreshKind) func(context.Context, string) error {
	if kind == asyncProjectRefreshAttention {
		if s.refreshProjectAttentionFn != nil {
			return s.refreshProjectAttentionFn
		}
		return s.refreshProjectAttention
	}
	if s.refreshProjectStatusFn != nil {
		return s.refreshProjectStatusFn
	}
	return func(ctx context.Context, projectPath string) error {
		return s.RefreshProjectStatusWithOptions(ctx, projectPath, ScanOptions{
			SkipLinkedWorktreeStatusRefresh: true,
		})
	}
}

func (s *Service) beginAsyncProjectRefresh(projectPath string, kind asyncProjectRefreshKind) bool {
	s.backgroundRefreshMu.Lock()
	defer s.backgroundRefreshMu.Unlock()

	if s.backgroundRefreshState == nil {
		s.backgroundRefreshState = map[string]asyncProjectRefreshState{}
	}
	state, exists := s.backgroundRefreshState[projectPath]
	if exists {
		if kind > state.requestedKind {
			state.requestedKind = kind
		}
		s.backgroundRefreshState[projectPath] = state
		return false
	}
	s.backgroundRefreshState[projectPath] = asyncProjectRefreshState{requestedKind: kind}
	return true
}

func (s *Service) takeAsyncProjectRefreshRequest(projectPath string) (asyncProjectRefreshKind, bool) {
	s.backgroundRefreshMu.Lock()
	defer s.backgroundRefreshMu.Unlock()

	state, ok := s.backgroundRefreshState[projectPath]
	if !ok || state.requestedKind == 0 {
		return 0, false
	}
	kind := state.requestedKind
	state.requestedKind = 0
	s.backgroundRefreshState[projectPath] = state
	return kind, true
}

func (s *Service) acquireAsyncProjectRefreshSlot() func() {
	s.backgroundRefreshMu.Lock()
	if s.backgroundRefreshSlots == nil {
		s.backgroundRefreshSlots = make(chan struct{}, asyncProjectRefreshConcurrency)
	}
	slots := s.backgroundRefreshSlots
	s.backgroundRefreshMu.Unlock()

	slots <- struct{}{}
	return func() { <-slots }
}

func (s *Service) finishAsyncProjectRefresh(projectPath string) bool {
	s.backgroundRefreshMu.Lock()
	defer s.backgroundRefreshMu.Unlock()

	state, ok := s.backgroundRefreshState[projectPath]
	if !ok {
		return false
	}
	if state.requestedKind != 0 {
		s.backgroundRefreshState[projectPath] = state
		return true
	}
	delete(s.backgroundRefreshState, projectPath)
	return false
}

// WaitForAsyncProjectRefreshes waits until the service has drained queued and
// in-flight project refreshes. It is primarily useful for orderly shutdown and
// deterministic integration-test cleanup; normal callers should leave TODO
// refreshes asynchronous.
func (s *Service) WaitForAsyncProjectRefreshes(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.backgroundRefreshMu.Lock()
		idle := len(s.backgroundRefreshState) == 0
		s.backgroundRefreshMu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) AddTodo(ctx context.Context, projectPath, text string) (model.TodoItem, error) {
	return s.AddTodoWithAttachments(ctx, projectPath, text, nil)
}

func (s *Service) AddTodoWithAttachments(ctx context.Context, projectPath, text string, attachments []model.TodoAttachment) (model.TodoItem, error) {
	item, err := s.store.AddTodoWithAttachments(ctx, projectPath, text, attachments)
	if err != nil {
		return model.TodoItem{}, err
	}
	s.queueSavedTodoWorktreeSuggestion(ctx, projectPath, item.ID)
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
	return s.afterTodoUpdated(ctx, projectPath, id)
}

func (s *Service) UpdateTodoWithAttachments(ctx context.Context, projectPath string, id int64, text string, attachments []model.TodoAttachment) error {
	if err := s.store.UpdateTodoWithAttachments(ctx, id, text, attachments); err != nil {
		return err
	}
	return s.afterTodoUpdated(ctx, projectPath, id)
}

func (s *Service) afterTodoUpdated(ctx context.Context, projectPath string, id int64) error {
	if err := s.store.DeleteTodoWorktreeSuggestion(ctx, id); err != nil {
		return err
	}
	s.queueSavedTodoWorktreeSuggestion(ctx, projectPath, id)
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "update_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "update_todo"})
	return nil
}

func (s *Service) queueSavedTodoWorktreeSuggestion(ctx context.Context, projectPath string, todoID int64) {
	if s == nil || s.store == nil || todoID <= 0 {
		return
	}
	changed, err := s.store.ForceQueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		if s.bus != nil {
			s.bus.Publish(events.Event{
				Type:        events.ActionApplied,
				At:          time.Now(),
				ProjectPath: projectPath,
				Payload: map[string]string{
					"action":  "todo_worktree_suggestion_failed",
					"todo_id": fmt.Sprintf("%d", todoID),
					"error":   err.Error(),
				},
			})
		}
		return
	}
	if !changed {
		return
	}
	if suggester := s.currentTodoSuggester(); suggester != nil {
		suggester.Notify()
	}
}

func (s *Service) ToggleTodoDone(ctx context.Context, projectPath string, id int64, done bool) error {
	eventProjectPath := strings.TrimSpace(projectPath)
	if todo, err := s.store.GetTodo(ctx, id); err == nil && strings.TrimSpace(todo.ProjectPath) != "" {
		eventProjectPath = strings.TrimSpace(todo.ProjectPath)
	}
	if err := s.store.ToggleTodoDone(ctx, id, done); err != nil {
		return err
	}
	s.refreshProjectStatusAsync(eventProjectPath)
	if strings.TrimSpace(projectPath) != "" && filepath.Clean(strings.TrimSpace(projectPath)) != filepath.Clean(eventProjectPath) {
		s.refreshProjectStatusAsync(projectPath)
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: eventProjectPath, Payload: map[string]string{"action": "toggle_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: eventProjectPath, Type: string(events.ActionApplied), Payload: "toggle_todo"})
	return nil
}

func (s *Service) MarkTodoWorkStarted(ctx context.Context, projectPath string, id int64, provider model.SessionSource, sessionID string, at time.Time) error {
	if s == nil || s.store == nil {
		return nil
	}
	workProjectPath := strings.TrimSpace(projectPath)
	source, normalizedSessionID := normalizeTodoWorkSessionIdentity(provider, sessionID)
	if normalizedSessionID == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	if err := s.store.AttachTodoWorkSession(ctx, id, workProjectPath, source, normalizedSessionID, model.TodoWorkStateWorking, at); err != nil {
		return err
	}
	rootProjectPath := workProjectPath
	if todo, err := s.store.GetTodo(ctx, id); err == nil && strings.TrimSpace(todo.ProjectPath) != "" {
		rootProjectPath = strings.TrimSpace(todo.ProjectPath)
	}
	s.refreshProjectStatusAsync(rootProjectPath)
	if workProjectPath != "" && workProjectPath != rootProjectPath {
		s.refreshProjectStatusAsync(workProjectPath)
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: rootProjectPath, Payload: map[string]string{"action": "todo_work_started"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: rootProjectPath, Type: string(events.ActionApplied), Payload: "todo_work_started"})
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
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return err
	}
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
