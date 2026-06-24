package codexapp

import (
	"fmt"
	"lcroom/internal/browserctl"
	"strings"
	"time"
)

func newAppServerSession(req LaunchRequest, notify func()) (Session, error) {
	policy := req.PlaywrightPolicy.Normalize()
	ensureManagedPlaywrightSessionKey(&req)
	s := &appServerSession{
		projectPath:              req.ProjectPath,
		preset:                   req.Preset,
		notify:                   notify,
		playwrightPolicy:         policy,
		playwrightMCPExpected:    shouldShadowPlaywrightSkill(policy),
		runtimeMCPExpected:       shouldShadowRuntimeSkill(req),
		managedBrowserSessionKey: strings.TrimSpace(req.ManagedBrowserSessionKey),
		dataDir:                  strings.TrimSpace(req.AppDataDir),
		runtimeManager:           req.RuntimeManager,
		pending:                  make(map[string]chan rpcEnvelope),
		exitCh:                   make(chan struct{}),
		activeItems:              make(map[string]struct{}),
		entryIndex:               make(map[string]int),
		browserActivity:          browserctl.DefaultSessionActivity(policy),
		mcpServerStartup:         make(map[string]mcpServerStartupState),
		status:                   "Starting Codex app-server...",
		lastActivityAt:           time.Now(),
	}
	if err := s.start(req); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *appServerSession) ProjectPath() string {
	return s.projectPath
}

func (s *appServerSession) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promoteHardStalledBusyLocked(time.Now())
	entries, transcript := s.exportedTranscriptLocked()
	snapshot := s.stateSnapshotLocked()
	snapshot.Entries = entries
	snapshot.Transcript = transcript
	return snapshot
}

func (s *appServerSession) TrySnapshot() (Snapshot, bool) {
	if !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	s.promoteHardStalledBusyLocked(time.Now())
	entries, transcript := s.exportedTranscriptLocked()
	snapshot := s.stateSnapshotLocked()
	snapshot.Entries = entries
	snapshot.Transcript = transcript
	return snapshot, true
}

func (s *appServerSession) StateSnapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promoteHardStalledBusyLocked(time.Now())
	return s.stateSnapshotLocked()
}

func (s *appServerSession) TryStateSnapshot() (Snapshot, bool) {
	if !s.mu.TryLock() {
		return Snapshot{}, false
	}
	defer s.mu.Unlock()
	s.promoteHardStalledBusyLocked(time.Now())
	return s.stateSnapshotLocked(), true
}

func (s *appServerSession) stateSnapshotLocked() Snapshot {
	tokenUsage := exportedTokenUsageSnapshot(s.tokenUsage)
	usageWindows := exportedUsageWindowsSnapshot(s.rateLimits, s.rateLimitsByID)
	return Snapshot{
		Provider:                 ProviderCodex,
		ProjectPath:              s.projectPath,
		ThreadID:                 s.threadID,
		Preset:                   s.preset,
		BrowserActivity:          s.browserActivity.Normalize(),
		ManagedBrowserSessionKey: strings.TrimSpace(s.managedBrowserSessionKey),
		CurrentBrowserPageURL:    strings.TrimSpace(s.currentBrowserPageURL),
		CurrentBrowserPageStale:  s.currentBrowserPageStale,
		TranscriptRevision:       s.transcriptRevision,
		Phase:                    s.phaseLocked(),
		Started:                  s.started,
		Busy:                     s.busy,
		BusyExternal:             s.busyExternal,
		BusySince:                s.busySince,
		LastBusyActivityAt:       s.lastBusyActivityAt,
		Closed:                   s.closed,
		ActiveTurnID:             s.activeTurnID,
		PendingApproval:          cloneApprovalRequest(s.pendingApproval),
		PendingToolInput:         cloneToolInputRequest(s.pendingToolInput),
		PendingElicitation:       cloneElicitationRequest(s.pendingElicitation),
		Status:                   s.status,
		LastError:                s.lastError,
		LastSystemNotice:         s.lastSystemNotice,
		LastActivityAt:           s.lastActivityAt,
		CurrentCWD:               s.currentCWD,
		Model:                    s.model,
		ModelProvider:            s.modelProvider,
		ReasoningEffort:          s.reasoningEffort,
		ServiceTier:              s.serviceTier,
		PendingModel:             s.pendingModel,
		PendingReasoning:         s.pendingReasoning,
		TokenUsage:               tokenUsage,
		UsageWindows:             usageWindows,
		Goal:                     cloneThreadGoal(s.goal),
	}
}

func (s *appServerSession) invalidateTranscriptCacheLocked() {
	s.transcriptCache.invalidate(&s.transcriptRevision)
}

func (s *appServerSession) exportedTranscriptLocked() ([]TranscriptEntry, string) {
	if !s.transcriptCache.ready || s.transcriptCache.revision != s.transcriptRevision {
		entries := exportTranscriptEntries(s.entries)
		s.transcriptCache.entries = entries
		s.transcriptCache.transcript = buildTranscriptText(ProviderCodex, entries, "\n\n", false)
		s.transcriptCache.revision = s.transcriptRevision
		s.transcriptCache.ready = true
	}
	return cloneTranscriptEntries(s.transcriptCache.entries), s.transcriptCache.transcript
}

func newEmbeddedSession(req LaunchRequest, notify func()) (Session, error) {
	switch req.Provider.Normalized() {
	case ProviderOpenCode:
		return newOpenCodeSession(req, notify)
	case ProviderClaudeCode:
		return newClaudeCodeSession(req, notify)
	case ProviderLCAgent:
		return newLCAgentSession(req, notify)
	default:
		return newAppServerSession(req, notify)
	}
}

func (s *appServerSession) phaseLocked() SessionPhase {
	switch {
	case s.closed:
		return SessionPhaseClosed
	case s.busyExternal:
		return SessionPhaseExternal
	case s.compacting:
		return SessionPhaseReconciling
	case s.stalled:
		return SessionPhaseStalled
	case s.reconciling:
		return SessionPhaseReconciling
	case s.pendingCompletion != nil && s.busy:
		return SessionPhaseFinishing
	case s.busy:
		return SessionPhaseRunning
	default:
		return SessionPhaseIdle
	}
}

func (s *appServerSession) setBusyLocked(turnID string, external bool) {
	s.updateBusyLocked(turnID, external, true)
}

func (s *appServerSession) restoreBusyLocked(turnID string, external bool) {
	s.updateBusyLocked(turnID, external, false)
}

func (s *appServerSession) updateBusyLocked(turnID string, external, refreshActivity bool) {
	turnID = strings.TrimSpace(turnID)
	turnChanged := turnID != "" && s.activeTurnID != "" && s.activeTurnID != turnID
	if turnChanged {
		s.activeItems = nil
		s.pendingCompletion = nil
		s.busySince = time.Time{}
	}
	if s.pendingCompletion != nil {
		pendingTurnID := strings.TrimSpace(s.pendingCompletion.TurnID)
		if turnID != "" && pendingTurnID != "" && pendingTurnID != turnID {
			s.pendingCompletion = nil
		}
	}
	s.busy = true
	s.busyExternal = external
	s.reconciling = false
	s.stalled = false
	s.stallCount = 0
	if turnID != "" {
		s.activeTurnID = turnID
	}
	now := time.Now()
	if s.busySince.IsZero() {
		s.busySince = now
	}
	if refreshActivity {
		s.lastActivityAt = now
		s.lastBusyActivityAt = now
	} else if s.lastActivityAt.IsZero() {
		s.lastActivityAt = now
	}
}

func (s *appServerSession) shouldRefreshBusyBeforeSteerLocked(now time.Time) bool {
	if !s.busy || s.busyExternal || strings.TrimSpace(s.activeTurnID) == "" {
		return false
	}
	age, ok := s.busyActivityAgeLocked(now)
	if !ok {
		return false
	}
	return age >= busyStateReconcileAfter
}

func (s *appServerSession) busyActivityAgeLocked(now time.Time) (time.Duration, bool) {
	lastBusyActivityAt := s.lastActivityAt
	if !s.lastBusyActivityAt.IsZero() {
		lastBusyActivityAt = s.lastBusyActivityAt
	}
	if lastBusyActivityAt.IsZero() {
		return 0, false
	}
	if now.Before(lastBusyActivityAt) {
		return 0, true
	}
	return now.Sub(lastBusyActivityAt), true
}

func (s *appServerSession) activeTurnLooksStuckLocked(turnID string, now time.Time) bool {
	if !s.busy || s.busyExternal || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || strings.TrimSpace(s.activeTurnID) != turnID {
		return false
	}
	age, ok := s.busyActivityAgeLocked(now)
	if !ok {
		return false
	}
	return age >= busyStateUnresponsiveFor
}

func (s *appServerSession) setBusyStalledLocked(notice ...string) {
	message := codexReconnectSuggestion
	if len(notice) > 0 && strings.TrimSpace(notice[0]) != "" {
		message = strings.TrimSpace(notice[0])
	}
	if !s.stalled {
		s.appendEntryLocked("", TranscriptSystem, message)
	}
	s.stalled = true
	if s.stallCount < busyStateStallAfter {
		s.stallCount = busyStateStallAfter
	}
	s.status = message
	s.lastSystemNotice = message
}

func (s *appServerSession) promoteHardStalledBusyLocked(now time.Time) {
	if !s.busy || s.busyExternal || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil {
		return
	}
	if strings.TrimSpace(s.activeTurnID) == "" {
		return
	}
	age, ok := s.busyActivityAgeLocked(now)
	threshold := busyStateHardStallAfter
	stallNotice := codexReconnectSuggestion
	if s.compacting || s.contextCompactionActive {
		threshold = compactionWaitTimeout
		stallNotice = codexCompactionStuckSuggestion
	}
	if !ok || age < threshold {
		return
	}
	s.compacting = false
	s.contextCompactionActive = false
	s.reconciling = false
	s.setBusyStalledLocked(stallNotice)
}

func (s *appServerSession) noteSuspectedBusyStallLocked() {
	s.stallCount++
	if s.stallCount >= busyStateStallAfter {
		s.setBusyStalledLocked()
	}
}

func (s *appServerSession) clearBusyLocked(turnID string) {
	s.busy = false
	s.busyExternal = false
	s.reconciling = false
	s.stalled = false
	s.stallCount = 0
	s.busySince = time.Time{}
	s.lastBusyActivityAt = time.Time{}
	s.activeItems = nil
	s.activeCompactionItems = nil
	s.pendingCompletion = nil
	s.contextCompactionActive = false
	if turnID == "" || s.activeTurnID == turnID {
		s.activeTurnID = ""
	}
}

func (s *appServerSession) markItemActiveLocked(turnID, itemID string) {
	s.reconciling = false
	if itemID = strings.TrimSpace(itemID); itemID != "" {
		if s.activeItems == nil {
			s.activeItems = make(map[string]struct{})
		}
		s.activeItems[itemID] = struct{}{}
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	if !s.busy || s.activeTurnID == "" || s.activeTurnID == turnID {
		s.setBusyLocked(turnID, false)
	}
	s.status = "Codex is working..."
}

func (s *appServerSession) markItemCompletedLocked(itemID string) {
	itemID = strings.TrimSpace(itemID)
	if itemID != "" && len(s.activeItems) > 0 {
		delete(s.activeItems, itemID)
		if len(s.activeItems) == 0 {
			s.activeItems = nil
		}
	}
	if len(s.activeItems) == 0 {
		s.finishPendingCompletionLocked()
	}
}

func (s *appServerSession) queueTurnCompletionLocked(turnID, status string) {
	s.pendingCompletion = &turnCompletionState{
		TurnID: strings.TrimSpace(turnID),
		Status: strings.TrimSpace(status),
	}
	if len(s.activeItems) == 0 {
		s.finishPendingCompletionLocked()
	}
}

func (s *appServerSession) finishPendingCompletionLocked() {
	if s.pendingCompletion == nil {
		return
	}
	completion := *s.pendingCompletion
	s.clearBusyLocked(completion.TurnID)
	if completion.Status != "" {
		s.status = completion.Status
		s.lastSystemNotice = completion.Status
	}
}

func tracksBusyItemLifecycle(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "agentMessage", "commandExecution", "fileChange", "plan", "reasoning", "mcpToolCall", "imageGeneration", "contextCompaction":
		return true
	default:
		return false
	}
}

func formatTurnCompletionStatus(turnStatus string, busySince, now time.Time) string {
	status := normalizeTurnStatus(turnStatus)
	switch status {
	case "", "complete", "completed":
		if !busySince.IsZero() {
			return "Completed in " + formatTurnStatusDuration(now.Sub(busySince))
		}
		return "Turn completed"
	default:
		return "Turn " + status
	}
}

func normalizeTurnStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	status = strings.ReplaceAll(status, "_", " ")
	status = strings.ReplaceAll(status, "-", " ")
	return strings.Join(strings.Fields(status), " ")
}

func formatTurnStatusDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	days := totalSeconds / (24 * 60 * 60)
	hours := (totalSeconds % (24 * 60 * 60)) / (60 * 60)
	minutes := (totalSeconds % (60 * 60)) / 60
	seconds := totalSeconds % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %02dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case totalSeconds >= int64(time.Hour/time.Second):
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	}
}
