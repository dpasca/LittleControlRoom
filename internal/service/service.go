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
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
	"lcroom/internal/todoworktree"
)

const legacyRepoDirName = "BatonDeck"
const recentActivityDiscoveryWindow = 24 * time.Hour

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
	llmUsageTracker          *llm.UsageTracker
	opencodeDiscovery        *llm.OpenCodeDiscovery

	gitFingerprintReader  func(context.Context, string) (scanner.GitFingerprint, error)
	gitRepoStatusReader   func(context.Context, string) (scanner.GitRepoStatus, error)
	gitWorktreeInfoReader func(context.Context, string) (scanner.GitWorktreeInfo, error)
	gitWorktreeListReader func(context.Context, string) ([]scanner.GitWorktree, error)
	gitRepoInitializer    func(context.Context, string) error

	mu sync.Mutex
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
		cfg:                   cfg,
		store:                 st,
		bus:                   bus,
		detectors:             detectorList,
		backendDetector:       aibackend.DetectStatus,
		llmUsageTracker:       llm.NewUsageTracker(),
		opencodeDiscovery:     llm.NewOpenCodeDiscovery(),
		gitFingerprintReader:  scanner.ReadGitFingerprint,
		gitRepoStatusReader:   scanner.ReadGitRepoStatus,
		gitWorktreeInfoReader: scanner.ReadGitWorktreeInfo,
		gitWorktreeListReader: scanner.ListGitWorktrees,
		gitRepoInitializer:    runGitInit,
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

func (s *Service) Config() config.AppConfig {
	cfg := s.cfg
	cfg.AIBackend = s.cfg.AIBackend
	cfg.OpenAIAPIKey = s.cfg.OpenAIAPIKey
	cfg.IncludePaths = append([]string(nil), s.cfg.IncludePaths...)
	cfg.ExcludePaths = append([]string(nil), s.cfg.ExcludePaths...)
	cfg.ExcludeProjectPatterns = append([]string(nil), s.cfg.ExcludeProjectPatterns...)
	cfg.EmbeddedCodexModel = s.cfg.EmbeddedCodexModel
	cfg.EmbeddedCodexReasoning = s.cfg.EmbeddedCodexReasoning
	cfg.EmbeddedOpenCodeModel = s.cfg.EmbeddedOpenCodeModel
	cfg.EmbeddedOpenCodeReasoning = s.cfg.EmbeddedOpenCodeReasoning
	return cfg
}

func (s *Service) SetSessionClassifier(classifier SessionClassifier) {
	s.classifier = classifier
}

func (s *Service) ApplyEditableSettings(settings config.EditableSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reconfigureAIClients := editableSettingsRequireAIClientRefresh(s.cfg, settings)
	currentBackend := s.cfg.EffectiveAIBackend()
	nextBackend := config.ResolveAIBackend(settings.AIBackend, settings.OpenAIAPIKey)

	s.cfg.AIBackend = settings.AIBackend
	s.cfg.OpenAIAPIKey = strings.TrimSpace(settings.OpenAIAPIKey)
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
	s.cfg.ScanInterval = settings.ScanInterval
	s.cfg.ActiveThreshold = settings.ActiveThreshold
	s.cfg.StuckThreshold = settings.StuckThreshold
	if reconfigureAIClients {
		s.configureAIClientsLocked()
	}
	if currentBackend != nextBackend {
		s.resetSessionUsageLocked()
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

func editableSettingsRequireAIClientRefresh(current config.AppConfig, settings config.EditableSettings) bool {
	if current.EffectiveAIBackend() != settings.AIBackend {
		return true
	}
	if strings.TrimSpace(current.OpenAIAPIKey) != strings.TrimSpace(settings.OpenAIAPIKey) {
		return true
	}
	return strings.TrimSpace(current.OpenCodeModelTier) != strings.TrimSpace(settings.OpenCodeModelTier)
}

func (s *Service) StartSessionClassifier(ctx context.Context) {
	if s.classifier == nil {
		return
	}
	s.classifier.Start(ctx)
}

func (s *Service) StartBackgroundDiscovery(ctx context.Context) {
	if s.opencodeDiscovery == nil {
		return
	}
	go func() {
		_, _ = ctx.Deadline()
		bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = s.opencodeDiscovery.Discover(bgCtx)
	}()
}

func (s *Service) StartTodoWorktreeSuggester(ctx context.Context) {
	if s.todoSuggester == nil {
		return
	}
	s.todoSuggester.Start(ctx)
}

func (s *Service) HasTodoWorktreeSuggester() bool {
	if s == nil || s.todoSuggester == nil {
		return false
	}
	return s.todoSuggester.Enabled()
}

func (s *Service) HasSessionClassifier() bool {
	if s.classifier == nil {
		return false
	}
	if enabled, ok := s.classifier.(interface{ Enabled() bool }); ok {
		return enabled.Enabled()
	}
	return true
}

func (s *Service) SessionUsage() model.LLMSessionUsage {
	enabled := s.HasSessionClassifier() || s.commitMessageSuggester != nil
	if s.llmUsageTracker != nil {
		snapshot := s.llmUsageTracker.Snapshot(enabled)
		if hasMeaningfulLLMUsage(snapshot) {
			return snapshot
		}
	}
	if s.classifier == nil {
		if enabled {
			return model.LLMSessionUsage{Enabled: true}
		}
		return model.LLMSessionUsage{}
	}
	if usageReader, ok := s.classifier.(interface{ UsageSnapshot() model.LLMSessionUsage }); ok {
		return usageReader.UsageSnapshot()
	}
	return model.LLMSessionUsage{Enabled: enabled}
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
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	internalWorkspaceRoot := appfs.InternalWorkspaceRoot(s.cfg.DataDir)
	scope := scanner.NewPathScope(s.cfg.IncludePaths, s.cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot)
	discovered := []string{}
	var err error
	if len(s.cfg.IncludePaths) > 0 {
		discovered, err = scanner.DiscoverGitProjects(scanner.Discovery{
			Roots:     s.cfg.IncludePaths,
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
	discovered, liveWorktreePathsByRoot := s.expandDiscoveredWorktreePaths(ctx, discovered, oldMap, scope)
	aliasesChanged, err := s.applyStaticPathAliases(ctx, now, oldMap)
	if err != nil {
		return ScanReport{}, err
	}
	if aliasesChanged {
		oldMap, err = s.store.GetProjectSummaryMap(ctx)
		if err != nil {
			return ScanReport{}, fmt.Errorf("reload project state after path aliases: %w", err)
		}
	}
	for path, old := range oldMap {
		inScopeNow := scope.Allows(path)
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
	if len(s.cfg.IncludePaths) > 0 {
		fullScopeActivities, err := s.detectProjectActivities(ctx, scanner.NewPathScope(nil, s.cfg.ExcludePaths).WithAlwaysExcluded(internalWorkspaceRoot), internalWorkspaceRoot)
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
		if s.gitFingerprintReader == nil || !projectPathExists(cleanPath) {
			continue
		}
		fingerprint, readErr := s.gitFingerprintReader(ctx, cleanPath)
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
		if s.gitFingerprintReader != nil {
			fingerprint, readErr := s.gitFingerprintReader(ctx, path)
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
		if s.gitRepoStatusReader != nil {
			repoStatus, readErr := s.gitRepoStatusReader(ctx, path)
			if readErr == nil {
				currentRepoStatus[path] = repoStatus
			}
		}
		if s.gitWorktreeInfoReader != nil {
			worktreeInfo, readErr := s.gitWorktreeInfoReader(ctx, path)
			if readErr == nil {
				currentWorktreeInfo[path] = worktreeInfo
			}
		}
	}

	updated := []string{}
	queuedClassifications := 0
	states := make([]model.ProjectState, 0, len(paths))

	for _, path := range paths {
		activity := activities[path]
		old := oldMap[path]
		if old.SnoozedUntil != nil && now.After(*old.SnoozedUntil) {
			if err := s.store.SetSnooze(ctx, path, nil); err != nil {
				return ScanReport{}, fmt.Errorf("clear expired snooze: %w", err)
			}
			old.SnoozedUntil = nil
		}
		presentOnDisk := projectPathExists(path)
		worktreeRootPath := old.WorktreeRootPath
		worktreeKind := old.WorktreeKind
		worktreeParentBranch := old.WorktreeParentBranch
		worktreeMergeStatus := old.WorktreeMergeStatus
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
			} else {
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
		if !presentOnDisk && old.WorktreeKind == model.WorktreeKindLinked && strings.TrimSpace(old.WorktreeRootPath) != "" {
			if livePaths := liveWorktreePathsByRoot[old.WorktreeRootPath]; len(livePaths) > 0 {
				if _, ok := livePaths[path]; !ok {
					forgotten = true
				}
			}
		}
		if presentOnDisk && forgotten && scope.Allows(path) {
			forgotten = false
		}
		if forgotten && !presentOnDisk {
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
			if len(sessions) > 0 {
				latestSessionStart = sessions[0].StartedAt
				latestTurnKnown = sessions[0].LatestTurnStateKnown
				latestTurnComplete = sessions[0].LatestTurnCompleted
			}
		}
		classificationKnown, classificationCategory := s.latestSessionClassification(ctx, path, sessions, now)

		score := attention.Score(attention.Input{
			Path:                       path,
			Now:                        now,
			LastActivity:               lastActivity,
			RepoDirty:                  repoDirty,
			Pinned:                     old.Pinned,
			SnoozedUntil:               old.SnoozedUntil,
			ErrorCount:                 errorCount,
			LatestSessionStart:         latestSessionStart,
			LatestTurnKnown:            latestTurnKnown,
			LatestTurnComplete:         latestTurnComplete,
			LatestSessionCategoryKnown: classificationKnown,
			LatestSessionCategory:      classificationCategory,
			HasActivity:                hasActivity,
			ActiveThreshold:            s.cfg.ActiveThreshold,
			StuckThreshold:             s.cfg.StuckThreshold,
		})

		state := model.ProjectState{
			Path:                 path,
			Name:                 filepath.Base(path),
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
			InScope:              scope.Allows(path),
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
			return ScanReport{}, fmt.Errorf("persist project state: %w", err)
		}
		if s.classifier != nil {
			queued, err := s.queueProjectClassification(ctx, state, opts)
			if err != nil {
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
	}

	report := ScanReport{
		At:                    now,
		ActivityProjectCount:  len(activities),
		TrackedProjectCount:   len(states),
		UpdatedProjects:       updated,
		QueuedClassifications: queuedClassifications,
		States:                states,
	}
	if queuedClassifications > 0 && s.classifier != nil {
		s.classifier.Notify()
	}

	s.bus.Publish(events.Event{
		Type: events.ScanCompleted,
		At:   now,
		Payload: map[string]string{
			"updated":                fmt.Sprintf("%d", len(updated)),
			"queued_classifications": fmt.Sprintf("%d", queuedClassifications),
		},
	})

	return report, nil
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
	if !merged.LatestTurnStateKnown && other.LatestTurnStateKnown {
		merged.LatestTurnStateKnown = other.LatestTurnStateKnown
		merged.LatestTurnCompleted = other.LatestTurnCompleted
		merged.LatestTurnStartedAt = other.LatestTurnStartedAt
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

func (s *Service) queueProjectClassification(ctx context.Context, state model.ProjectState, opts ScanOptions) (bool, error) {
	if s.classifier == nil {
		return false, nil
	}
	if opts.ForceRetryFailedClassifications {
		if retryer, ok := s.classifier.(sessionClassifierRetryer); ok {
			return retryer.QueueProjectRetry(ctx, state, 0)
		}
	}
	return s.classifier.QueueProject(ctx, state)
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

func timesEqual(a, b time.Time) bool {
	if a.IsZero() && b.IsZero() {
		return true
	}
	return a.Unix() == b.Unix()
}

func projectStateChanged(old model.ProjectSummary, state model.ProjectState) bool {
	return old.Path == "" ||
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

func (s *Service) applyStaticPathAliases(ctx context.Context, now time.Time, oldMap map[string]model.ProjectSummary) (bool, error) {
	aliases := staticPathAliases(s.cfg.IncludePaths, now)
	if len(aliases) == 0 {
		return false, nil
	}

	changed := false
	for _, alias := range aliases {
		if err := s.store.UpsertPathAlias(ctx, alias); err != nil {
			return false, fmt.Errorf("persist path alias %s -> %s: %w", alias.OldPath, alias.NewPath, err)
		}
		if _, ok := oldMap[alias.OldPath]; !ok {
			continue
		}
		if _, targetExists := oldMap[alias.NewPath]; targetExists {
			if err := s.store.ConsolidateProjectPath(ctx, alias.OldPath, alias.NewPath, now); err != nil {
				return false, fmt.Errorf("consolidate project path %s -> %s: %w", alias.OldPath, alias.NewPath, err)
			}
		} else {
			if err := s.store.MoveProjectPath(ctx, alias.OldPath, alias.NewPath, now); err != nil {
				return false, fmt.Errorf("move project path %s -> %s: %w", alias.OldPath, alias.NewPath, err)
			}
		}
		changed = true
	}
	return changed, nil
}

func staticPathAliases(includePaths []string, now time.Time) []model.PathAlias {
	out := make([]model.PathAlias, 0, len(includePaths))
	newRepoDirName := strings.ReplaceAll(brand.Name, " ", "")
	for _, includePath := range includePaths {
		cleanIncludePath := filepath.Clean(includePath)
		newPath := filepath.Join(cleanIncludePath, newRepoDirName)
		if !projectPathExists(newPath) {
			continue
		}
		out = append(out, model.PathAlias{
			OldPath:   filepath.Join(cleanIncludePath, legacyRepoDirName),
			NewPath:   newPath,
			Reason:    "repo_rename",
			UpdatedAt: now,
		})
	}
	return out
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

func (s *Service) latestSessionClassification(ctx context.Context, path string, sessions []model.SessionEvidence, now time.Time) (bool, model.SessionCategory) {
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
		StuckThreshold:       sessionclassify.EffectiveAssessmentStallThreshold(s.cfg.ActiveThreshold, s.cfg.StuckThreshold),
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
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		return err
	}

	presentOnDisk := projectPathExists(detail.Summary.Path)
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
	if presentOnDisk {
		worktreeRootPath, worktreeKind = s.readProjectWorktreeInfo(ctx, detail.Summary.Path)
		if s.gitRepoStatusReader != nil {
			if repoStatus, err := s.gitRepoStatusReader(ctx, detail.Summary.Path); err == nil {
				repoBranch = strings.TrimSpace(repoStatus.Branch)
				repoDirty = repoStatus.Dirty
				repoConflict = repoConflictFromGit(repoStatus)
				repoSyncStatus = repoSyncStatusFromGit(repoStatus)
				repoAheadCount = repoStatus.Ahead
				repoBehindCount = repoStatus.Behind
			} else {
				repoBranch = detail.Summary.RepoBranch
				repoDirty = detail.Summary.RepoDirty
				repoConflict = detail.Summary.RepoConflict
				repoSyncStatus = detail.Summary.RepoSyncStatus
				repoAheadCount = detail.Summary.RepoAheadCount
				repoBehindCount = detail.Summary.RepoBehindCount
			}
		} else {
			repoBranch = detail.Summary.RepoBranch
			repoDirty = detail.Summary.RepoDirty
			repoConflict = detail.Summary.RepoConflict
			repoSyncStatus = detail.Summary.RepoSyncStatus
			repoAheadCount = detail.Summary.RepoAheadCount
			repoBehindCount = detail.Summary.RepoBehindCount
		}
		worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, worktreeRootPath, worktreeKind, repoBranch, worktreeParentBranch)
	}

	errorCount := 0
	latestSessionStart := time.Time{}
	latestTurnKnown := false
	latestTurnComplete := false
	if len(detail.Sessions) > 0 {
		ensureLatestSessionTurnState(&detail.Sessions[0])
		ensureSessionSnapshotHash(ctx, projectPath, &detail.Sessions[0], sessionclassify.NewGitStatusSnapshot(repoDirty, repoSyncStatus, repoAheadCount, repoBehindCount))
		latestSessionStart = detail.Sessions[0].StartedAt
		latestTurnKnown = detail.Sessions[0].LatestTurnStateKnown
		latestTurnComplete = detail.Sessions[0].LatestTurnCompleted
	}
	for _, session := range detail.Sessions {
		errorCount += session.ErrorCount
	}

	classificationKnown, classificationCategory := s.latestSessionClassification(ctx, projectPath, detail.Sessions, now)
	score := attention.Score(attention.Input{
		Path:                       detail.Summary.Path,
		Now:                        now,
		LastActivity:               detail.Summary.LastActivity,
		RepoDirty:                  repoDirty,
		Pinned:                     detail.Summary.Pinned,
		SnoozedUntil:               detail.Summary.SnoozedUntil,
		ErrorCount:                 errorCount,
		LatestSessionStart:         latestSessionStart,
		LatestTurnKnown:            latestTurnKnown,
		LatestTurnComplete:         latestTurnComplete,
		LatestSessionCategoryKnown: classificationKnown,
		LatestSessionCategory:      classificationCategory,
		HasActivity:                !detail.Summary.LastActivity.IsZero(),
		ActiveThreshold:            s.cfg.ActiveThreshold,
		StuckThreshold:             s.cfg.StuckThreshold,
	})

	state := model.ProjectState{
		Path:                 detail.Summary.Path,
		Name:                 detail.Summary.Name,
		LastActivity:         detail.Summary.LastActivity,
		Status:               score.Status,
		AttentionScore:       score.Score,
		PresentOnDisk:        presentOnDisk,
		WorktreeRootPath:     worktreeRootPath,
		WorktreeKind:         worktreeKind,
		WorktreeParentBranch: worktreeParentBranch,
		WorktreeMergeStatus:  worktreeMergeStatus,
		WorktreeOriginTodoID: detail.Summary.WorktreeOriginTodoID,
		RepoBranch:           repoBranch,
		RepoDirty:            repoDirty,
		RepoConflict:         repoConflict,
		RepoSyncStatus:       repoSyncStatus,
		RepoAheadCount:       repoAheadCount,
		RepoBehindCount:      repoBehindCount,
		Forgotten:            detail.Summary.Forgotten,
		ManuallyAdded:        detail.Summary.ManuallyAdded,
		InScope:              detail.Summary.InScope,
		Pinned:               detail.Summary.Pinned,
		SnoozedUntil:         detail.Summary.SnoozedUntil,
		MovedFromPath:        detail.Summary.MovedFromPath,
		MovedAt:              detail.Summary.MovedAt,
		AttentionReason:      score.Reasons,
		Sessions:             detail.Sessions,
		Artifacts:            detail.Artifacts,
		UpdatedAt:            now,
	}
	if err := s.store.UpsertProjectState(ctx, state); err != nil {
		return fmt.Errorf("persist refreshed project state: %w", err)
	}
	if projectStateChanged(detail.Summary, state) {
		s.publishProjectChanged(ctx, now, state)
	}
	return nil
}

func (s *Service) TogglePin(ctx context.Context, projectPath string) error {
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
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "toggle_pin"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "toggle_pin"})
	return nil
}

func (s *Service) Snooze(ctx context.Context, projectPath string, duration time.Duration) error {
	until := time.Now().Add(duration)
	if err := s.store.SetSnooze(ctx, projectPath, &until); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "snooze", "until": until.Format(time.RFC3339)}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: fmt.Sprintf("snooze until %s", until.Format(time.RFC3339))})
	return nil
}

func (s *Service) ClearSnooze(ctx context.Context, projectPath string) error {
	if err := s.store.SetSnooze(ctx, projectPath, nil); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "clear_snooze"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "clear_snooze"})
	return nil
}

func (s *Service) AddTodo(ctx context.Context, projectPath, text string) (model.TodoItem, error) {
	item, err := s.store.AddTodo(ctx, projectPath, text)
	if err != nil {
		return model.TodoItem{}, err
	}
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
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "toggle_todo"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "toggle_todo"})
	return nil
}

func (s *Service) DeleteTodo(ctx context.Context, projectPath string, id int64) error {
	if err := s.store.DeleteTodo(ctx, id); err != nil {
		return err
	}
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
