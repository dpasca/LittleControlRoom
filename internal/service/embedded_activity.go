package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexstate"
	"lcroom/internal/config"
	"lcroom/internal/model"
)

type EmbeddedSessionActivity struct {
	ProjectPath          string
	Source               model.SessionSource
	SessionID            string
	Format               string
	StartedAt            time.Time
	LastActivityAt       time.Time
	LatestTurnStartedAt  time.Time
	LatestTurnStateKnown bool
	LatestTurnCompleted  bool
	WorkState            model.TodoWorkState
}

func (s *Service) RecordEmbeddedSessionActivity(ctx context.Context, activity EmbeddedSessionActivity) error {
	if s == nil || s.store == nil {
		return nil
	}
	projectPath := filepath.Clean(strings.TrimSpace(activity.ProjectPath))
	if projectPath == "" || projectPath == "." || activity.LastActivityAt.IsZero() {
		return nil
	}
	runtime := s.runtimeSnapshot()

	var state model.ProjectState
	if err := func() error {
		unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
		if err != nil {
			return err
		}
		defer unlockProjectState()

		detail, err := s.store.GetProjectDetail(ctx, projectPath, 20)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) || strings.HasPrefix(err.Error(), "project not found:") {
				return nil
			}
			return err
		}

		activitySession := embeddedActivitySession(projectPath, activity, runtime.cfg)
		if activitySession.SessionID != "" {
			detail.Sessions = mergeEmbeddedActivitySession(detail.Sessions, activitySession)
		}
		if activity.LastActivityAt.After(detail.Summary.LastActivity) {
			detail.Summary.LastActivity = activity.LastActivityAt
		}
		if len(detail.Sessions) > 0 && detail.Sessions[0].LastEventAt.After(detail.Summary.LastActivity) {
			detail.Summary.LastActivity = detail.Sessions[0].LastEventAt
		}

		state, err = s.persistProjectStateUpdate(ctx, detail, time.Now(), projectStatusRefreshOverrides{
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
		}, runtime.cfg, nil, ScanOptions{})
		return err
	}(); err != nil {
		return err
	}
	if state.Path == "" {
		return nil
	}
	if runtime.classifier != nil {
		queued, err := queueProjectClassification(ctx, runtime.classifier, state, ScanOptions{})
		if err != nil {
			return err
		}
		if queued {
			runtime.classifier.Notify()
		}
	}
	return s.updateTodoWorkStateForEmbeddedActivity(ctx, projectPath, activity)
}

func embeddedActivitySession(projectPath string, activity EmbeddedSessionActivity, cfg config.AppConfig) model.SessionEvidence {
	source := model.NormalizeSessionSource(activity.Source)
	format := strings.TrimSpace(activity.Format)
	if format == "" {
		format = embeddedActivityDefaultFormat(source)
	}
	source, sessionID, rawSessionID := model.NormalizeSessionIdentity(source, format, strings.TrimSpace(activity.SessionID), "")
	if sessionID == "" {
		return model.SessionEvidence{}
	}
	sessionFile := resolveEmbeddedSessionFile(source, sessionID, rawSessionID, activity.StartedAt, activity.LastActivityAt, cfg)
	return model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               source,
		SessionID:            sessionID,
		RawSessionID:         rawSessionID,
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		SessionFile:          sessionFile,
		Format:               format,
		StartedAt:            activity.StartedAt,
		LastEventAt:          activity.LastActivityAt,
		LatestTurnStartedAt:  activity.LatestTurnStartedAt,
		LatestTurnStateKnown: activity.LatestTurnStateKnown,
		LatestTurnCompleted:  activity.LatestTurnCompleted,
	})
}

func (s *Service) updateTodoWorkStateForEmbeddedActivity(ctx context.Context, projectPath string, activity EmbeddedSessionActivity) error {
	state := model.NormalizeTodoWorkState(activity.WorkState)
	if state == "" {
		return nil
	}
	source, sessionID := normalizeTodoWorkSessionIdentity(activity.Source, strings.TrimSpace(activity.SessionID))
	if sessionID == "" {
		return nil
	}
	at := activity.LastActivityAt
	if at.IsZero() {
		at = time.Now()
	}
	updated, err := s.store.UpdateTodoWorkStateForSession(ctx, projectPath, source, sessionID, state, at)
	if err != nil || updated == 0 {
		return err
	}
	paths, err := s.store.TodoProjectPathsForWorkSession(ctx, projectPath, source, sessionID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if strings.TrimSpace(path) != "" && strings.TrimSpace(path) != projectPath {
			s.refreshProjectStatusAsync(path)
		}
	}
	return nil
}

func normalizeTodoWorkSessionIdentity(source model.SessionSource, sessionID string) (model.SessionSource, string) {
	source = model.NormalizeSessionSource(source)
	format := embeddedActivityDefaultFormat(source)
	normalizedSource, normalizedSessionID, _ := model.NormalizeSessionIdentity(source, format, strings.TrimSpace(sessionID), "")
	return normalizedSource, strings.TrimSpace(normalizedSessionID)
}

func embeddedActivityDefaultFormat(source model.SessionSource) string {
	switch source {
	case model.SessionSourceOpenCode:
		return "opencode_db"
	case model.SessionSourceClaudeCode:
		return "claude_code"
	case model.SessionSourceLCAgent:
		return "lcagent_jsonl"
	default:
		return "modern"
	}
}

func mergeEmbeddedActivitySession(sessions []model.SessionEvidence, activity model.SessionEvidence) []model.SessionEvidence {
	out := append([]model.SessionEvidence(nil), sessions...)
	for i := range out {
		if !sameEmbeddedActivitySession(out[i], activity) {
			continue
		}
		updated := mergeSessionEvidence(out[i], activity)
		if activity.LastEventAt.After(out[i].LastEventAt) {
			updated.SnapshotHash = ""
		}
		out[i] = updated
		sort.Slice(out, func(i, j int) bool {
			return out[i].LastEventAt.After(out[j].LastEventAt)
		})
		return out
	}
	out = append(out, activity)
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastEventAt.After(out[j].LastEventAt)
	})
	return out
}

func sameEmbeddedActivitySession(existing, activity model.SessionEvidence) bool {
	existing = model.NormalizeSessionEvidenceIdentity(existing)
	activity = model.NormalizeSessionEvidenceIdentity(activity)
	if existing.SessionID != "" && existing.SessionID == activity.SessionID {
		return true
	}
	if existing.RawSessionID != "" && existing.RawSessionID == activity.RawSessionID {
		return true
	}
	return existing.ExternalID() != "" && existing.ExternalID() == activity.ExternalID()
}

func resolveEmbeddedSessionFile(source model.SessionSource, sessionID, rawSessionID string, startedAt, lastActivityAt time.Time, cfg config.AppConfig) string {
	source = model.NormalizeSessionSource(source)
	sessionID = strings.TrimSpace(sessionID)
	rawSessionID = strings.TrimSpace(rawSessionID)
	if rawSessionID == "" {
		if _, parsedRaw := model.ParseCanonicalSessionID(sessionID); parsedRaw != "" {
			rawSessionID = parsedRaw
		}
	}

	switch source {
	case model.SessionSourceOpenCode:
		if rawSessionID != "" && strings.TrimSpace(cfg.OpenCodeHome) != "" {
			return filepath.Join(cfg.OpenCodeHome, "opencode.db") + "#session:" + rawSessionID
		}
	case model.SessionSourceCodex:
		lookupID := rawSessionID
		if lookupID == "" {
			lookupID = sessionID
		}
		return resolveCodexSessionFile(cfg.CodexHome, lookupID, startedAt, lastActivityAt)
	case model.SessionSourceLCAgent:
		lookupID := rawSessionID
		if lookupID == "" {
			lookupID = sessionID
		}
		return resolveLCAgentSessionFile(cfg.DataDir, lookupID, startedAt, lastActivityAt)
	}

	return ""
}

func resolveCodexSessionFile(codexHome, sessionID string, times ...time.Time) string {
	codexHome = codexstate.ResolveHomeRoot(codexHome)
	sessionID = strings.TrimSpace(sessionID)
	if codexHome == "" || sessionID == "" {
		return ""
	}

	candidates := codexSessionDateCandidates(times...)
	for _, root := range []string{"sessions", "archived_sessions"} {
		for _, day := range candidates {
			pattern := filepath.Join(
				codexHome,
				root,
				day.Format("2006"),
				day.Format("01"),
				day.Format("02"),
				"*"+sessionID+"*.jsonl",
			)
			matches, err := filepath.Glob(pattern)
			if err != nil || len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			return matches[len(matches)-1]
		}
	}

	return ""
}

func resolveLCAgentSessionFile(dataDir, sessionID string, times ...time.Time) string {
	dataDir = strings.TrimSpace(dataDir)
	sessionID = strings.TrimSpace(sessionID)
	if dataDir == "" || sessionID == "" {
		return ""
	}

	for _, day := range codexSessionDateCandidates(times...) {
		path := filepath.Join(
			dataDir,
			"lcagent",
			"sessions",
			day.Format("2006"),
			day.Format("01"),
			day.Format("02"),
			sessionID+".jsonl",
		)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}

	return ""
}

func codexSessionDateCandidates(times ...time.Time) []time.Time {
	seen := map[string]struct{}{}
	out := make([]time.Time, 0, len(times)*3+1)
	add := func(t time.Time) {
		if t.IsZero() {
			return
		}
		day := time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
		key := day.Format("2006-01-02")
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, day)
	}

	for _, t := range times {
		if t.IsZero() {
			continue
		}
		add(t)
		add(t.Add(-24 * time.Hour))
		add(t.Add(24 * time.Hour))
	}
	if len(out) == 0 {
		add(time.Now())
	}
	return out
}
