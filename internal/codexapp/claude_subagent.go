package codexapp

import (
	"fmt"
	"reflect"
	"time"

	"lcroom/internal/claudeartifact"
)

const claudeSubagentReadOnly = "Claude Code subagents are read-only in LCR; manage this agent from its parent conversation"

// A child transcript has no independent process-liveness signal. Recent turn
// events support an activity inference, but an unfinished log cannot keep it
// busy forever after interruption or loss of its parent.
const claudeSubagentActivityWindow = claudeartifact.SubagentActivityWindow

type claudeSubagentProgressReader interface {
	Read(parentFile, parentID string, since time.Time) ([]claudeartifact.SubagentProgress, error)
}

func (s Snapshot) SubagentActivitySummary(now time.Time) string {
	if len(s.Subagents) == 0 {
		return s.SubagentProgressError
	}
	active, complete, quiet := 0, 0, 0
	for _, child := range s.Subagents {
		switch child.State(now) {
		case "active":
			active++
		case "completed":
			complete++
		default:
			quiet++
		}
	}
	text := fmt.Sprintf("Subagents: %d active", active)
	if complete > 0 {
		text += fmt.Sprintf(" · %d completed", complete)
	}
	if quiet > 0 {
		text += fmt.Sprintf(" · %d without recent activity", quiet)
	}
	return text
}

// Snapshots only queue a coalesced refresh. File reads and parsing run outside
// the session mutex so large child logs cannot block rendering or stream input.
func (s *claudeCodeSession) scheduleSubagentProgressRefreshLocked(now time.Time) {
	if s.closed || s.readOnlySubagent || s.sessionFile == "" || s.sessionID == "" || (!s.busy && !s.externalTurnActive) {
		return
	}
	since := s.latestTurnStartedAt
	// A queued follow-up updates latestTurnStartedAt before the current
	// foreground worker finishes. Keep that worker visible for the busy span.
	if since.IsZero() || (!s.busySince.IsZero() && s.busySince.Before(since)) {
		since = s.busySince
	}
	if since.IsZero() {
		return
	}
	if !since.Equal(s.subagentProgressSince) {
		s.subagentProgress = nil
		s.subagentProgressError = ""
		s.subagentProgressSince = since
		s.subagentProgressRefreshAt = time.Time{}
	}
	if s.subagentProgressRefreshing || now.Sub(s.subagentProgressRefreshAt) < 5*time.Second {
		return
	}
	s.subagentProgressRefreshing = true
	s.subagentProgressRefreshAt = now
	if s.subagentProgressReader == nil {
		s.subagentProgressReader = &claudeartifact.SubagentProgressReader{}
	}
	reader, file, id := s.subagentProgressReader, s.sessionFile, s.sessionID
	go func() {
		progress, err := reader.Read(file, id, since)
		errorText := ""
		if err != nil {
			errorText = "Subagent activity unavailable: " + err.Error()
		}
		s.mu.Lock()
		s.subagentProgressRefreshing = false
		changed := false
		if !s.closed && s.sessionID == id && s.sessionFile == file && s.subagentProgressSince.Equal(since) {
			changed = !reflect.DeepEqual(s.subagentProgress, progress) || s.subagentProgressError != errorText
			s.subagentProgress = progress
			s.subagentProgressError = errorText
		}
		s.mu.Unlock()
		if changed {
			s.notifyAsync()
		}
	}()
}

func (s Snapshot) IsClaudeSubagent() bool {
	_, _, ok := claudeartifact.ParseSubagentSessionID(s.ThreadID)
	return s.Provider == ProviderClaudeCode && ok
}

func newClaudeSubagentSession(req LaunchRequest, claudeHome string, notify func()) (Session, error) {
	if !launchRequestInitialInput(req).Empty() || req.ContinueInterruptedTurn {
		return nil, fmt.Errorf("%s", claudeSubagentReadOnly)
	}
	path, err := claudeartifact.FindSubagentTranscript(claudeHome, req.ProjectPath, req.ResumeID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionChanged, err)
	}
	parentID, agentID, _ := claudeartifact.ParseSubagentSessionID(req.ResumeID)
	s := &claudeCodeSession{
		projectPath:      req.ProjectPath,
		claudeHome:       claudeHome,
		sessionID:        req.ResumeID,
		sessionFile:      path,
		readOnlySubagent: true,
		started:          true,
		notify:           notify,
		closedCh:         make(chan struct{}),
		lastSystemNotice: fmt.Sprintf("Claude Code subagent %s belongs to parent session %s. This transcript refreshes from disk and stays read-only after completion. Manage the agent from its parent conversation.", agentID, parentID),
	}
	if err := s.loadTranscriptLocked(); err != nil {
		return nil, fmt.Errorf("load Claude Code subagent transcript: %w", err)
	}
	s.refreshActiveLocked()
	s.updateStatusLocked()
	return s, nil
}

func (s *claudeCodeSession) refreshSubagentActivityLocked(now time.Time) {
	// A parent's live PID/status cannot establish which child is still working.
	// Keep ownership read-only and derive this child's turn only from its log.
	s.busyExternal = true
	s.externalTurnActive = s.latestTurnStateKnown && !s.latestTurnCompleted &&
		!s.latestTurnStateAt.IsZero() && now.Sub(s.latestTurnStateAt) <= claudeSubagentActivityWindow
	s.busySince = time.Time{}
	if s.externalTurnActive {
		s.busySince = s.latestTurnStartedAt
	}
}
