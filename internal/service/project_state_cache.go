package service

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

type cachedProjectState struct {
	state       model.ProjectState
	fingerprint [sha256.Size]byte
}

func (s *Service) projectStateCacheNeedsPrime(projects map[string]model.ProjectSummary) bool {
	if s == nil || len(projects) == 0 {
		return false
	}
	s.projectStateCacheMu.RLock()
	defer s.projectStateCacheMu.RUnlock()
	for path := range projects {
		if _, ok := s.projectStateCache[path]; !ok {
			return true
		}
	}
	return false
}

func (s *Service) primeProjectStateCache(projects map[string]model.ProjectSummary, evidence map[string]store.ProjectScanEvidence) {
	if s == nil {
		return
	}
	for path, summary := range projects {
		state := projectStateFromSummaryAndEvidence(summary, evidence[path])
		s.rememberProjectStateIfAbsent(path, state)
	}
}

func (s *Service) cachedProjectState(projectPath string) (model.ProjectState, bool) {
	if s == nil {
		return model.ProjectState{}, false
	}
	projectPath = strings.TrimSpace(projectPath)
	s.projectStateCacheMu.RLock()
	entry, ok := s.projectStateCache[projectPath]
	s.projectStateCacheMu.RUnlock()
	if !ok {
		return model.ProjectState{}, false
	}
	return cloneProjectState(entry.state), true
}

func (s *Service) rememberProjectStateIfAbsent(projectPath string, state model.ProjectState) {
	if s == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	s.projectStateCacheMu.Lock()
	defer s.projectStateCacheMu.Unlock()
	if s.projectStateCache == nil {
		s.projectStateCache = make(map[string]cachedProjectState)
	}
	if _, ok := s.projectStateCache[projectPath]; ok {
		return
	}
	s.projectStateCache[projectPath] = newCachedProjectState(state)
}

func (s *Service) rememberProjectState(state model.ProjectState) {
	if s == nil || strings.TrimSpace(state.Path) == "" {
		return
	}
	state = cloneProjectState(state)
	s.projectStateCacheMu.Lock()
	defer s.projectStateCacheMu.Unlock()
	if s.projectStateCache == nil {
		s.projectStateCache = make(map[string]cachedProjectState)
	}
	s.projectStateCache[state.Path] = newCachedProjectState(state)
}

func (s *Service) forgetProjectState(projectPath string) {
	if s == nil {
		return
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	s.projectStateCacheMu.Lock()
	delete(s.projectStateCache, projectPath)
	s.projectStateCacheMu.Unlock()
}

func (s *Service) projectStateNeedsPersistence(state model.ProjectState) bool {
	if s == nil || strings.TrimSpace(state.Path) == "" {
		return true
	}
	fingerprint := projectStatePersistenceFingerprint(state)
	s.projectStateCacheMu.RLock()
	entry, ok := s.projectStateCache[state.Path]
	s.projectStateCacheMu.RUnlock()
	return !ok || entry.fingerprint != fingerprint
}

func newCachedProjectState(state model.ProjectState) cachedProjectState {
	return cachedProjectState{
		state:       cloneProjectState(state),
		fingerprint: projectStatePersistenceFingerprint(state),
	}
}

func projectStatePersistenceFingerprint(state model.ProjectState) [sha256.Size]byte {
	state = cloneProjectState(state)
	// A scan timestamp is not project state. Ignoring it lets identical scans
	// avoid rewriting every project and all of its child rows.
	state.UpdatedAt = time.Time{}
	payload, _ := json.Marshal(state)
	return sha256.Sum256(payload)
}

func cloneProjectState(state model.ProjectState) model.ProjectState {
	cloned := state
	cloned.AttentionReason = append([]model.AttentionReason(nil), state.AttentionReason...)
	cloned.Sessions = append([]model.SessionEvidence(nil), state.Sessions...)
	cloned.Artifacts = append([]model.ArtifactEvidence(nil), state.Artifacts...)
	if state.SnoozedUntil != nil {
		snoozedUntil := *state.SnoozedUntil
		cloned.SnoozedUntil = &snoozedUntil
	}
	return cloned
}

func projectStateFromSummaryAndEvidence(summary model.ProjectSummary, evidence store.ProjectScanEvidence) model.ProjectState {
	return model.ProjectState{
		Path:                       summary.Path,
		Name:                       summary.Name,
		Kind:                       model.NormalizeProjectKind(summary.Kind),
		LastActivity:               summary.LastActivity,
		Status:                     summary.Status,
		AttentionScore:             summary.AttentionScore,
		PresentOnDisk:              summary.PresentOnDisk,
		WorktreeRootPath:           summary.WorktreeRootPath,
		WorktreeKind:               summary.WorktreeKind,
		WorktreeParentBranch:       summary.WorktreeParentBranch,
		WorktreeInitialBranch:      summary.WorktreeInitialBranch,
		WorktreeMergeStatus:        summary.WorktreeMergeStatus,
		WorktreeOriginTodoID:       summary.WorktreeOriginTodoID,
		RepoBranch:                 summary.RepoBranch,
		RepoDirty:                  summary.RepoDirty,
		RepoConflict:               summary.RepoConflict,
		RepoSyncStatus:             summary.RepoSyncStatus,
		RepoAheadCount:             summary.RepoAheadCount,
		RepoBehindCount:            summary.RepoBehindCount,
		RepoSubmoduleDirtyCount:    summary.RepoSubmoduleDirtyCount,
		RepoSubmoduleUnpushedCount: summary.RepoSubmoduleUnpushedCount,
		Forgotten:                  summary.Forgotten,
		ManuallyAdded:              summary.ManuallyAdded,
		InScope:                    summary.InScope,
		Archived:                   summary.Archived,
		Pinned:                     summary.Pinned,
		SnoozedUntil:               summary.SnoozedUntil,
		RunCommand:                 summary.RunCommand,
		MovedFromPath:              summary.MovedFromPath,
		MovedAt:                    summary.MovedAt,
		PreferredSessionSource:     summary.PreferredSessionSource,
		AttentionReason:            append([]model.AttentionReason(nil), evidence.Reasons...),
		Sessions:                   append([]model.SessionEvidence(nil), evidence.Sessions...),
		Artifacts:                  append([]model.ArtifactEvidence(nil), evidence.Artifacts...),
		CreatedAt:                  summary.CreatedAt,
	}
}

func sameProjectGitFingerprint(left, right model.ProjectGitFingerprint) bool {
	if strings.TrimSpace(left.HeadHash) != strings.TrimSpace(right.HeadHash) || len(left.RecentHashes) != len(right.RecentHashes) {
		return false
	}
	for i := range left.RecentHashes {
		if strings.TrimSpace(left.RecentHashes[i]) != strings.TrimSpace(right.RecentHashes[i]) {
			return false
		}
	}
	return true
}

func overlayProjectSummaryWithState(summary model.ProjectSummary, state model.ProjectState) model.ProjectSummary {
	summary.Path = state.Path
	summary.Name = state.Name
	summary.Kind = model.NormalizeProjectKind(state.Kind)
	summary.LastActivity = state.LastActivity
	summary.Status = state.Status
	summary.AttentionScore = state.AttentionScore
	summary.PresentOnDisk = state.PresentOnDisk
	summary.WorktreeRootPath = state.WorktreeRootPath
	summary.WorktreeKind = state.WorktreeKind
	summary.WorktreeParentBranch = state.WorktreeParentBranch
	summary.WorktreeMergeStatus = state.WorktreeMergeStatus
	summary.WorktreeOriginTodoID = state.WorktreeOriginTodoID
	summary.RepoBranch = state.RepoBranch
	summary.RepoDirty = state.RepoDirty
	summary.RepoConflict = state.RepoConflict
	summary.RepoSyncStatus = state.RepoSyncStatus
	summary.RepoAheadCount = state.RepoAheadCount
	summary.RepoBehindCount = state.RepoBehindCount
	summary.RepoSubmoduleDirtyCount = state.RepoSubmoduleDirtyCount
	summary.RepoSubmoduleUnpushedCount = state.RepoSubmoduleUnpushedCount
	summary.Forgotten = state.Forgotten
	summary.ManuallyAdded = state.ManuallyAdded
	summary.InScope = state.InScope
	summary.Archived = state.Archived
	summary.Pinned = state.Pinned
	summary.SnoozedUntil = state.SnoozedUntil
	summary.RunCommand = state.RunCommand
	summary.MovedFromPath = state.MovedFromPath
	summary.MovedAt = state.MovedAt
	summary.PreferredSessionSource = state.PreferredSessionSource
	summary.CreatedAt = state.CreatedAt

	if len(state.Sessions) == 0 {
		summary.LatestSessionSource = model.SessionSourceUnknown
		summary.LatestSessionID = ""
		summary.LatestRawSessionID = ""
		summary.LatestSessionFormat = ""
		summary.LatestSessionDetectedProjectPath = ""
		summary.LatestSessionSnapshotHash = ""
		summary.LatestSessionLastEventAt = time.Time{}
		summary.LatestTurnStartedAt = time.Time{}
		summary.LatestTurnStateKnown = false
		summary.LatestTurnCompleted = false
		return summary
	}

	latest := model.NormalizeSessionEvidenceIdentity(state.Sessions[0])
	summary.LatestSessionSource = latest.Source
	summary.LatestSessionID = latest.SessionID
	summary.LatestRawSessionID = latest.RawSessionID
	summary.LatestSessionFormat = latest.Format
	summary.LatestSessionDetectedProjectPath = latest.DetectedProjectPath
	summary.LatestSessionSnapshotHash = latest.SnapshotHash
	summary.LatestSessionLastEventAt = latest.LastEventAt
	summary.LatestTurnStartedAt = latest.LatestTurnStartedAt
	summary.LatestTurnStateKnown = latest.LatestTurnStateKnown
	summary.LatestTurnCompleted = latest.LatestTurnCompleted
	return summary
}
