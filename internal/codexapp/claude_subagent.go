package codexapp

import (
	"fmt"
	"time"

	"lcroom/internal/claudeartifact"
)

const claudeSubagentReadOnly = "Claude Code subagents are read-only in LCR; manage this agent from its parent conversation"

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

func (s *claudeCodeSession) refreshSubagentActivityLocked() {
	// A parent's live PID/status cannot establish which child is still working.
	// Keep ownership read-only and derive this child's turn only from its log.
	s.busyExternal = true
	s.externalTurnActive = s.latestTurnStateKnown && !s.latestTurnCompleted
	s.busySince = time.Time{}
	if s.externalTurnActive {
		s.busySince = s.latestTurnStartedAt
	}
}
