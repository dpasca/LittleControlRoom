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

	"lcroom/internal/attention"
	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
)

const legacyRepoDirName = "BatonDeck"

type SessionClassifier interface {
	QueueProject(ctx context.Context, state model.ProjectState) (bool, error)
	Notify()
	Start(ctx context.Context)
}

type sessionClassifierRetryer interface {
	QueueProjectRetry(ctx context.Context, state model.ProjectState, retryAfter time.Duration) (bool, error)
}

type Service struct {
	cfg        config.AppConfig
	store      *store.Store
	bus        *events.Bus
	detectors  []detectors.Detector
	classifier SessionClassifier

	commitMessageSuggester   gitops.CommitMessageSuggester
	untrackedFileRecommender gitops.UntrackedFileRecommender

	gitFingerprintReader func(context.Context, string) (scanner.GitFingerprint, error)
	gitRepoStatusReader  func(context.Context, string) (scanner.GitRepoStatus, error)
	gitRepoInitializer   func(context.Context, string) error

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
	commitAssistant := gitops.NewOpenAICommitMessageClientFromEnv()
	return &Service{
		cfg:                      cfg,
		store:                    st,
		bus:                      bus,
		detectors:                detectorList,
		commitMessageSuggester:   commitAssistant,
		untrackedFileRecommender: commitAssistant,
		gitFingerprintReader:     scanner.ReadGitFingerprint,
		gitRepoStatusReader:      scanner.ReadGitRepoStatus,
		gitRepoInitializer:       runGitInit,
	}
}

func (s *Service) Store() *store.Store {
	return s.store
}

func (s *Service) Bus() *events.Bus {
	return s.bus
}

func (s *Service) Config() config.AppConfig {
	cfg := s.cfg
	cfg.IncludePaths = append([]string(nil), s.cfg.IncludePaths...)
	cfg.ExcludePaths = append([]string(nil), s.cfg.ExcludePaths...)
	cfg.ExcludeProjectPatterns = append([]string(nil), s.cfg.ExcludeProjectPatterns...)
	return cfg
}

func (s *Service) SetSessionClassifier(classifier SessionClassifier) {
	s.classifier = classifier
}

func (s *Service) StartSessionClassifier(ctx context.Context) {
	if s.classifier == nil {
		return
	}
	s.classifier.Start(ctx)
}

func (s *Service) HasSessionClassifier() bool {
	return s.classifier != nil
}

func (s *Service) SessionUsage() model.LLMSessionUsage {
	if s.classifier == nil {
		return model.LLMSessionUsage{}
	}
	if usageReader, ok := s.classifier.(interface{ UsageSnapshot() model.LLMSessionUsage }); ok {
		return usageReader.UsageSnapshot()
	}
	return model.LLMSessionUsage{Enabled: true}
}

func (s *Service) ScanOnce(ctx context.Context) (ScanReport, error) {
	return s.ScanWithOptions(ctx, ScanOptions{})
}

func (s *Service) ScanWithOptions(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	scope := scanner.NewPathScope(s.cfg.IncludePaths, s.cfg.ExcludePaths)
	discovered := []string{}
	var err error
	if len(s.cfg.IncludePaths) > 0 {
		discovered, err = scanner.DiscoverGitProjects(scanner.Discovery{
			Roots:    s.cfg.IncludePaths,
			MaxDepth: 4,
		})
		if err != nil {
			return ScanReport{}, fmt.Errorf("discover git projects: %w", err)
		}
	}

	oldMap, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return ScanReport{}, fmt.Errorf("load previous project state: %w", err)
	}
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

	rawActivities := map[string]*model.DetectorProjectActivity{}
	for _, detector := range s.detectors {
		detected, detectErr := detector.Detect(ctx, scope)
		if detectErr != nil {
			continue
		}
		for path, activity := range detected {
			rawPath := filepath.Clean(path)
			normalized := normalizeActivity(activity, rawPath)
			existing, ok := rawActivities[rawPath]
			if !ok {
				rawActivities[rawPath] = normalized
				continue
			}
			if normalized.LastActivity.After(existing.LastActivity) {
				existing.LastActivity = normalized.LastActivity
			}
			existing.ErrorCount += normalized.ErrorCount
			existing.Sessions = append(existing.Sessions, normalized.Sessions...)
			existing.Artifacts = append(existing.Artifacts, normalized.Artifacts...)
		}
	}

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
	}

	updated := []string{}
	queuedClassifications := 0
	states := make([]model.ProjectState, 0, len(paths))

	for _, path := range paths {
		activity := activities[path]
		old := oldMap[path]
		presentOnDisk := projectPathExists(path)
		repoDirty := false
		repoSyncStatus := model.RepoSyncStatus("")
		repoAheadCount := 0
		repoBehindCount := 0
		if presentOnDisk {
			if repoStatus, ok := currentRepoStatus[path]; ok {
				repoDirty = repoStatus.Dirty
				repoSyncStatus = repoSyncStatusFromGit(repoStatus)
				repoAheadCount = repoStatus.Ahead
				repoBehindCount = repoStatus.Behind
			} else {
				repoDirty = old.RepoDirty
				repoSyncStatus = old.RepoSyncStatus
				repoAheadCount = old.RepoAheadCount
				repoBehindCount = old.RepoBehindCount
			}
		}
		forgotten := old.Forgotten
		if presentOnDisk && forgotten {
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
				reuseLatestSessionSnapshotHash(old, &sessions[0])
				ensureSessionSnapshotHash(ctx, path, &sessions[0], sessionclassify.NewGitStatusSnapshot(repoDirty, repoSyncStatus, repoAheadCount, repoBehindCount))
			}
			if len(sessions) > 0 {
				latestSessionStart = sessions[0].StartedAt
				latestTurnKnown = sessions[0].LatestTurnStateKnown
				latestTurnComplete = sessions[0].LatestTurnCompleted
			}
		}
		classificationKnown, classificationCategory := s.latestSessionClassification(ctx, path, sessions)

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
			Path:            path,
			Name:            filepath.Base(path),
			LastActivity:    lastActivity,
			Status:          score.Status,
			AttentionScore:  score.Score,
			PresentOnDisk:   presentOnDisk,
			RepoDirty:       repoDirty,
			RepoSyncStatus:  repoSyncStatus,
			RepoAheadCount:  repoAheadCount,
			RepoBehindCount: repoBehindCount,
			Forgotten:       forgotten,
			ManuallyAdded:   old.ManuallyAdded,
			InScope:         true,
			Pinned:          old.Pinned,
			SnoozedUntil:    old.SnoozedUntil,
			Note:            old.Note,
			MovedFromPath:   old.MovedFromPath,
			MovedAt:         old.MovedAt,
			AttentionReason: score.Reasons,
			Sessions:        sessions,
			Artifacts:       artifacts,
			UpdatedAt:       now,
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
	seen := map[string]struct{}{}
	out := make([]model.SessionEvidence, 0, len(in))
	for _, s := range in {
		if s.SessionID == "" {
			continue
		}
		if _, ok := seen[s.SessionID]; ok {
			continue
		}
		seen[s.SessionID] = struct{}{}
		out = append(out, s)
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
		old.RepoDirty != state.RepoDirty ||
		old.RepoSyncStatus != state.RepoSyncStatus ||
		old.RepoAheadCount != state.RepoAheadCount ||
		old.RepoBehindCount != state.RepoBehindCount ||
		old.Forgotten != state.Forgotten ||
		old.ManuallyAdded != state.ManuallyAdded ||
		!timesEqual(old.LastActivity, state.LastActivity)
}

func (s *Service) publishProjectChanged(ctx context.Context, now time.Time, state model.ProjectState) {
	payload := map[string]string{
		"status": string(state.Status),
		"score":  fmt.Sprintf("%d", state.AttentionScore),
		"dirty":  fmt.Sprintf("%t", state.RepoDirty),
		"remote": string(state.RepoSyncStatus),
	}
	s.bus.Publish(events.Event{Type: events.ProjectChanged, At: now, ProjectPath: state.Path, Payload: payload})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: state.Path,
		Type:        string(events.ProjectChanged),
		Payload:     fmt.Sprintf("status=%s score=%d dirty=%t remote=%s", state.Status, state.AttentionScore, state.RepoDirty, state.RepoSyncStatus),
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

func (s *Service) latestSessionClassification(ctx context.Context, path string, sessions []model.SessionEvidence) (bool, model.SessionCategory) {
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
	return true, classification.Category
}

func reuseLatestSessionSnapshotHash(old model.ProjectSummary, session *model.SessionEvidence) {
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
	session.SnapshotHash = old.LatestSessionSnapshotHash
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

	errorCount := 0
	latestSessionStart := time.Time{}
	if len(detail.Sessions) > 0 {
		ensureSessionSnapshotHash(ctx, projectPath, &detail.Sessions[0], sessionclassify.NewGitStatusSnapshot(detail.Summary.RepoDirty, detail.Summary.RepoSyncStatus, detail.Summary.RepoAheadCount, detail.Summary.RepoBehindCount))
		latestSessionStart = detail.Sessions[0].StartedAt
	}
	for _, session := range detail.Sessions {
		errorCount += session.ErrorCount
	}

	classificationKnown, classificationCategory := s.latestSessionClassification(ctx, projectPath, detail.Sessions)
	presentOnDisk := projectPathExists(detail.Summary.Path)
	repoDirty := false
	repoSyncStatus := model.RepoSyncStatus("")
	repoAheadCount := 0
	repoBehindCount := 0
	if presentOnDisk {
		if s.gitRepoStatusReader != nil {
			if repoStatus, err := s.gitRepoStatusReader(ctx, detail.Summary.Path); err == nil {
				repoDirty = repoStatus.Dirty
				repoSyncStatus = repoSyncStatusFromGit(repoStatus)
				repoAheadCount = repoStatus.Ahead
				repoBehindCount = repoStatus.Behind
			} else {
				repoDirty = detail.Summary.RepoDirty
				repoSyncStatus = detail.Summary.RepoSyncStatus
				repoAheadCount = detail.Summary.RepoAheadCount
				repoBehindCount = detail.Summary.RepoBehindCount
			}
		} else {
			repoDirty = detail.Summary.RepoDirty
			repoSyncStatus = detail.Summary.RepoSyncStatus
			repoAheadCount = detail.Summary.RepoAheadCount
			repoBehindCount = detail.Summary.RepoBehindCount
		}
	}

	score := attention.Score(attention.Input{
		Path:                       detail.Summary.Path,
		Now:                        now,
		LastActivity:               detail.Summary.LastActivity,
		RepoDirty:                  repoDirty,
		Pinned:                     detail.Summary.Pinned,
		SnoozedUntil:               detail.Summary.SnoozedUntil,
		ErrorCount:                 errorCount,
		LatestSessionStart:         latestSessionStart,
		LatestSessionCategoryKnown: classificationKnown,
		LatestSessionCategory:      classificationCategory,
		HasActivity:                !detail.Summary.LastActivity.IsZero(),
		ActiveThreshold:            s.cfg.ActiveThreshold,
		StuckThreshold:             s.cfg.StuckThreshold,
	})

	state := model.ProjectState{
		Path:            detail.Summary.Path,
		Name:            detail.Summary.Name,
		LastActivity:    detail.Summary.LastActivity,
		Status:          score.Status,
		AttentionScore:  score.Score,
		PresentOnDisk:   presentOnDisk,
		RepoDirty:       repoDirty,
		RepoSyncStatus:  repoSyncStatus,
		RepoAheadCount:  repoAheadCount,
		RepoBehindCount: repoBehindCount,
		Forgotten:       detail.Summary.Forgotten,
		ManuallyAdded:   detail.Summary.ManuallyAdded,
		InScope:         detail.Summary.InScope,
		Pinned:          detail.Summary.Pinned,
		SnoozedUntil:    detail.Summary.SnoozedUntil,
		Note:            detail.Summary.Note,
		MovedFromPath:   detail.Summary.MovedFromPath,
		MovedAt:         detail.Summary.MovedAt,
		AttentionReason: score.Reasons,
		Sessions:        detail.Sessions,
		Artifacts:       detail.Artifacts,
		UpdatedAt:       now,
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

func (s *Service) SetNote(ctx context.Context, projectPath, note string) error {
	if err := s.store.SetNote(ctx, projectPath, note); err != nil {
		return err
	}
	now := time.Now()
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "set_note"}})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: "set_note"})
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
