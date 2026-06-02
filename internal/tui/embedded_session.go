package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

type embeddedModelPreferencesSavedMsg struct {
	settings config.EditableSettings
	path     string
	err      error
}

type codexSessionOpenedMsg struct {
	projectPath      string
	snapshot         codexapp.Snapshot
	status           string
	visibleStatus    string
	backgroundStatus string
	renamedTask      bool
	renameErr        error
	agentTaskID      string
	agentTaskTitle   string
	agentTaskName    string
	perfOpID         int64
	perfDuration     time.Duration
	err              error
}

type codexPendingOpenState struct {
	projectPath      string
	provider         codexapp.Provider
	showWhilePending bool
	newSession       bool
	hideOnOpen       bool
}

type codexUpdateMsg struct {
	projectPath string
}

type codexActionMsg struct {
	projectPath   string
	perfOpID      int64
	perfDuration  time.Duration
	status        string
	closed        bool
	restoreDraft  codexDraft
	provider      codexapp.Provider
	model         string
	modelProvider string
	reasoning     string
	awaitSettle   bool
	refreshView   bool
	renamedTask   bool
	renameErr     error
	err           error
}

type codexModelListMsg struct {
	projectPath  string
	models       []codexapp.ModelOption
	perfOpID     int64
	perfDuration time.Duration
	err          error
}

// codexDeferredSnapshotMsg is sent when a non-blocking TrySnapshot failed due
// to lock contention and a goroutine was spawned to acquire the snapshot in
// the background. When this message arrives, the snapshot is stored in the
// cache and the normal update-cycle follow-up logic (viewport sync, model
// settle, etc.) runs.
type codexDeferredSnapshotMsg struct {
	projectPath string
	snapshot    codexapp.Snapshot
}

func (m codexModelListMsg) statusSummary() string {
	if m.err != nil {
		return ""
	}
	if len(m.models) == 0 {
		return "0 models"
	}
	return fmt.Sprintf("%d models", len(m.models))
}

func (m codexSessionOpenedMsg) statusForReveal(reveal bool) string {
	if reveal {
		if status := strings.TrimSpace(m.visibleStatus); status != "" {
			return status
		}
	} else if status := strings.TrimSpace(m.backgroundStatus); status != "" {
		return status
	}
	return strings.TrimSpace(m.status)
}

type codexResumeChoicesMsg struct {
	projectPath string
	provider    codexapp.Provider
	choices     []codexSessionChoice
	err         error
}

func (m Model) applyCodexSessionOpenedMsg(msg codexSessionOpenedMsg) (tea.Model, tea.Cmd) {
	m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
	m.err = nil
	renameRefreshCmd := m.scratchTaskRenameRefreshCmd(msg.projectPath, msg.renamedTask, msg.renameErr)
	if msg.err != nil {
		provider := m.codexPendingOpenProvider()
		m.finishCodexPendingOpen(msg.projectPath, codexapp.Snapshot{}, false, false)
		m.returnToBossModeAfterCodexHide = false
		m.clearTodoLaunchDraft(msg.projectPath)
		if projectPath := strings.TrimSpace(msg.projectPath); projectPath != "" {
			shouldShowFailure := true
			if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
				shouldShowFailure = snapshot.Closed
			} else if _, ok := m.codexSession(projectPath); ok {
				shouldShowFailure = false
			}
			if shouldShowFailure {
				m.showEmbeddedOpenFailure(msg.projectPath, provider, msg.err)
			}
		}
		m.reportError("Embedded session open failed", msg.err, msg.projectPath)
		return m, nil
	}
	if task, ok := m.agentTaskForProjectPath(msg.projectPath); ok {
		selectedPath := m.currentSelectedProjectPath()
		task.Provider = modelSessionSourceFromCodexProvider(embeddedProvider(msg.snapshot))
		task.SessionID = strings.TrimSpace(msg.snapshot.ThreadID)
		task.LastTouchedAt = m.currentTime()
		m.openAgentTasks = upsertAgentTask(m.openAgentTasks, task)
		m.rebuildProjectList(selectedPath)
	}
	revealOnOpen := m.revealPendingEmbeddedOpenOnSuccess(msg.projectPath)
	focusInput := true
	draft, hasTodoLaunchDraft := m.todoLaunchDraftFor(msg.projectPath)
	if hasTodoLaunchDraft {
		if draft.openModelFirst {
			focusInput = false
		} else if draft.autoSubmit {
			revealOnOpen = false
			focusInput = false
		}
	}
	status := msg.statusForReveal(revealOnOpen)
	seenCmd := m.finishCodexPendingOpen(msg.projectPath, msg.snapshot, true, revealOnOpen)
	todoWorkStartedCmd := tea.Cmd(nil)
	if hasTodoLaunchDraft {
		todoWorkStartedCmd = m.markTodoWorkStartedCmd(msg.projectPath, draft.todoID, msg.snapshot)
		m.clearTodoLaunchDraft(msg.projectPath)
		if draft.openModelFirst {
			m.openCodexModelPickerLoading()
			m.status = "Pick a model, then send the TODO draft."
			return m, tea.Batch(seenCmd, todoWorkStartedCmd, m.openCodexModelPickerCmd())
		}
		if draft.autoSubmit {
			if status != "" {
				m.status = status
			} else {
				m.status = "Started TODO in background"
			}
		} else {
			m.status = "Fresh " + draft.provider.Label() + " session ready with TODO draft. Edit and press Enter to send."
		}
	} else {
		m.status = status
	}
	if focusInput {
		return m, tea.Batch(seenCmd, todoWorkStartedCmd, renameRefreshCmd, m.maybeReadManagedBrowserStateCmd(msg.snapshot), m.codexInput.Focus())
	}
	return m, tea.Batch(seenCmd, todoWorkStartedCmd, renameRefreshCmd, m.maybeReadManagedBrowserStateCmd(msg.snapshot))
}

func (m Model) applyCodexActionMsg(msg codexActionMsg) (tea.Model, tea.Cmd) {
	m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
	renameRefreshCmd := m.scratchTaskRenameRefreshCmd(msg.projectPath, msg.renamedTask, msg.renameErr)
	if msg.err != nil {
		m.reportError("Embedded session action failed", msg.err, msg.projectPath)
		if msg.projectPath != "" && !msg.restoreDraft.Empty() {
			m.restoreCodexDraft(msg.projectPath, msg.restoreDraft)
		}
		return m, nil
	}
	m.err = nil
	if msg.status != "" {
		m.status = msg.status
	}
	refreshCmd := tea.Cmd(nil)
	linkScanCmd := tea.Cmd(nil)
	if msg.refreshView && strings.TrimSpace(msg.projectPath) != "" {
		prevSnapshot, hadPrevSnapshot := m.codexSnapshots[strings.TrimSpace(msg.projectPath)]
		snapshot, ok, needsAsync := m.refreshCodexSnapshot(msg.projectPath)
		if needsAsync {
			refreshCmd = m.deferredCodexSnapshotCmd(msg.projectPath)
		}
		if ok {
			transcriptChanged := !hadPrevSnapshot || codexTranscriptStateChanged(prevSnapshot, snapshot)
			if strings.TrimSpace(m.codexVisibleProject) == strings.TrimSpace(msg.projectPath) {
				m.resetCodexToolAnswerState(msg.projectPath)
				m.syncCodexViewport(transcriptChanged)
				linkScanCmd = m.maybeStartCodexArtifactLinkScan(msg.projectPath, snapshot)
			}
			if !snapshot.Closed {
				m.markCodexSessionLive(msg.projectPath)
			}
		}
	}
	if msg.provider.Normalized() != "" && (strings.TrimSpace(msg.model) != "" || strings.TrimSpace(msg.reasoning) != "") {
		var asyncCmd tea.Cmd
		if msg.awaitSettle {
			m.beginModelSettleLatency(msg.projectPath, strings.TrimSpace(msg.provider.Label()+" "+msg.model+" "+msg.reasoning), msg.model, msg.reasoning)
			if snapshot, ok := m.stageEmbeddedModelSelectionInCache(msg.projectPath, msg.provider, msg.model, msg.reasoning); ok {
				m.completeModelSettleLatency(msg.projectPath, snapshot)
			}
			m.markCodexSkipNextLiveRefresh(msg.projectPath)
		}
		m.rememberEmbeddedModelPreference(msg.provider, msg.model, msg.reasoning)
		if msg.provider == codexapp.ProviderLCAgent && strings.TrimSpace(msg.modelProvider) != "" {
			pref := m.embeddedModelPrefs[codexapp.ProviderLCAgent]
			pref.ModelProvider = strings.TrimSpace(msg.modelProvider)
			m.embeddedModelPrefs[codexapp.ProviderLCAgent] = pref
		}
		m.recordRecentModel(msg.provider, msg.model)
		m.returnToTodoFromModelPicker()
		if strings.TrimSpace(m.codexVisibleProject) == strings.TrimSpace(msg.projectPath) && m.todoDialog == nil && m.todoCopyDialog == nil {
			return m, tea.Batch(asyncCmd, m.saveEmbeddedModelPreferencesCmd(), m.codexInput.Focus())
		}
		return m, tea.Batch(asyncCmd, m.saveEmbeddedModelPreferencesCmd())
	}
	if msg.closed {
		m.cancelModelSettleLatency(msg.projectPath, "session closed")
		delete(m.codexClosedHandled, msg.projectPath)
		if m.codexVisibleProject == msg.projectPath {
			m.codexVisibleProject = ""
			m.codexInput.Blur()
			m.syncDetailViewport(false)
		}
		if m.codexHiddenProject == msg.projectPath {
			m.codexHiddenProject = ""
		}
		refresh := invalidateProjectScan(m.visibleDetailPathForProject(msg.projectPath), false)
		return m, m.markProjectSessionSeenWithRefresh(msg.projectPath, refresh)
	}
	return m, batchCmds(renameRefreshCmd, refreshCmd, linkScanCmd)
}

func (m *Model) scratchTaskRenameRefreshCmd(projectPath string, renamed bool, err error) tea.Cmd {
	if err != nil {
		m.appendErrorLogEntry("Scratch task auto-name failed", err, projectPath)
	}
	if !renamed {
		return nil
	}
	return m.requestProjectInvalidationCmd(invalidateProjectStructure(m.visibleDetailPathForProject(projectPath)))
}

func (m Model) applyCodexModelListMsg(msg codexModelListMsg) (tea.Model, tea.Cmd) {
	result := msg.statusSummary()
	if strings.TrimSpace(msg.projectPath) != strings.TrimSpace(m.codexVisibleProject) {
		if result == "" {
			result = "stale"
		}
		m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, result)
		return m, nil
	}
	m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, result)
	if msg.err != nil {
		m.codexModelPicker = nil
		m.reportError("Embedded model picker failed", msg.err, msg.projectPath)
		m.returnToTodoFromModelPicker()
		return m, nil
	}
	m.err = nil
	m.openLoadedCodexModelPicker(msg.models)
	return m, nil
}

func (m Model) applyCodexUpdateMsg(msg codexUpdateMsg) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitCodexCmd()}
	if m.codexManager != nil {
		m.codexManager.AckUpdate(msg.projectPath)
	}
	if m.consumeCodexSkipNextLiveRefresh(msg.projectPath) {
		if deferred := m.deferredCodexSnapshotCmd(msg.projectPath); deferred != nil {
			cmds = append(cmds, deferred)
		}
		if snapshot, ok := m.codexCachedSnapshot(msg.projectPath); ok {
			m.completeModelSettleLatency(msg.projectPath, snapshot)
			if !snapshot.Closed {
				m.markCodexSessionLive(msg.projectPath)
				m.detectBrowserAttentionNotification(msg.projectPath, snapshot)
				m.detectQuestionNotification(msg.projectPath, snapshot)
				return m, tea.Batch(cmds...)
			}
		}
		return m, tea.Batch(cmds...)
	}
	prevSnapshot, hadPrevSnapshot := m.codexSnapshots[strings.TrimSpace(msg.projectPath)]
	refreshStarted := time.Now()
	snapshot, ok, needsAsync := m.refreshCodexSnapshot(msg.projectPath)
	refreshDuration := time.Since(refreshStarted)
	if needsAsync {
		cmds = append(cmds, m.deferredCodexSnapshotCmd(msg.projectPath))
	}
	providerLabel := ""
	transcriptChanged := false
	statusRefreshCmd := tea.Cmd(nil)
	sidebarDiffRefreshCmd := tea.Cmd(nil)
	bossNoticeCmd := tea.Cmd(nil)
	if ok {
		providerLabel = embeddedProvider(snapshot).Label()
		transcriptChanged = !hadPrevSnapshot || codexTranscriptStateChanged(prevSnapshot, snapshot)
		if normalizeProjectPath(m.codexPendingOpenProject()) == normalizeProjectPath(msg.projectPath) && codexSnapshotCanSettlePendingOpen(snapshot) {
			reveal := m.revealPendingEmbeddedOpenOnSuccess(msg.projectPath) || codexSnapshotHasPendingUserResponse(snapshot)
			cmds = append(cmds, m.finishCodexPendingOpen(msg.projectPath, snapshot, true, reveal))
		}
		m.observeManagedBrowserLease(msg.projectPath, snapshot)
		cmds = append(cmds, m.maybeReadManagedBrowserStateCmd(snapshot))
		if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(hadPrevSnapshot, prevSnapshot, snapshot) {
			statusRefreshCmd = m.recordEmbeddedSessionActivityCmd(msg.projectPath, snapshot)
		}
		if shouldRefreshProjectStatusAfterCodexSnapshot(prevSnapshot, snapshot) {
			statusRefreshCmd = m.recordEmbeddedSessionSettledAndRefreshCmd(msg.projectPath, snapshot)
			sidebarDiffRefreshCmd = m.requestVisibleEmbeddedSidebarDiffRefreshCmd(msg.projectPath, true)
		}
		if m.bossMode {
			if notice := bossBrowserAttentionHostNoticeForSnapshot(msg.projectPath, hadPrevSnapshot, prevSnapshot, snapshot); notice != "" {
				var cmd tea.Cmd
				m, cmd = m.updateBossHostNotice(notice)
				bossNoticeCmd = batchCmds(bossNoticeCmd, cmd)
			}
		}
		var cmd tea.Cmd
		m, cmd = m.handleBossEngineerTurnCompletion(msg.projectPath, hadPrevSnapshot, prevSnapshot, snapshot)
		bossNoticeCmd = batchCmds(bossNoticeCmd, cmd)
	}
	m.recordAISyncLatency("Embedded snapshot", msg.projectPath, providerLabel, refreshDuration, "")
	if m.codexVisibleProject == msg.projectPath {
		viewportStarted := time.Now()
		m.resetCodexToolAnswerState(msg.projectPath)
		m.syncCodexViewport(transcriptChanged)
		m.recordAISyncLatency("Embedded viewport", msg.projectPath, providerLabel, time.Since(viewportStarted), "")
		if ok {
			cmds = append(cmds, m.maybeStartCodexArtifactLinkScan(msg.projectPath, snapshot))
			if codexSnapshotBrowserWaitingForUser(snapshot) {
				cmds = append(cmds, m.codexInput.Focus())
			}
		}
	}
	if ok {
		m.completeModelSettleLatency(msg.projectPath, snapshot)
		if !snapshot.Closed {
			m.markCodexSessionLive(msg.projectPath)
			m.detectBrowserAttentionNotification(msg.projectPath, snapshot)
			m.detectQuestionNotification(msg.projectPath, snapshot)
			return m, batchCmds(append(cmds, statusRefreshCmd, sidebarDiffRefreshCmd, bossNoticeCmd)...)
		}
		m.cancelModelSettleLatency(msg.projectPath, "session closed")
		if !m.markCodexSessionClosedHandled(msg.projectPath) {
			return m, tea.Batch(cmds...)
		}
		if m.codexHiddenProject == msg.projectPath {
			m.codexHiddenProject = ""
		}
		if m.codexVisibleProject == msg.projectPath && strings.TrimSpace(snapshot.Status) != "" {
			m.status = snapshot.Status
		} else if strings.TrimSpace(snapshot.Status) != "" {
			m.status = snapshot.Status
		}
		m.removeManagedBrowserLease(msg.projectPath, snapshot)
		m.loading = true
		if shouldRecordEmbeddedSessionSettledAfterClose(hadPrevSnapshot, prevSnapshot, snapshot) {
			cmds = append(cmds, m.recordEmbeddedSessionSettledCmd(msg.projectPath, snapshot))
		}
		cmds = append(cmds, m.requestProjectInvalidationCmd(invalidateProjectScan("", false)))
	} else if !needsAsync {
		m.cancelModelSettleLatency(msg.projectPath, "stale")
		if hadPrevSnapshot {
			m.removeManagedBrowserLease(msg.projectPath, prevSnapshot)
			if shouldRecordEmbeddedSessionSettledAfterDisappearance(prevSnapshot) {
				cmds = append(cmds, m.recordEmbeddedSessionSettledCmd(msg.projectPath, prevSnapshot))
			}
		}
		m.dropCodexSnapshot(msg.projectPath)
	}
	return m, tea.Batch(cmds...)
}

func (m Model) applyCodexDeferredSnapshotMsg(msg codexDeferredSnapshotMsg) (tea.Model, tea.Cmd) {
	projectPath := strings.TrimSpace(msg.projectPath)
	if projectPath == "" {
		return m, nil
	}
	prevSnapshot, hadPrev := m.codexSnapshots[projectPath]
	m.storeCodexSnapshot(projectPath, msg.snapshot)
	snapshot := msg.snapshot
	m.observeManagedBrowserLease(projectPath, snapshot)
	providerLabel := embeddedProvider(snapshot).Label()
	transcriptChanged := !hadPrev || codexTranscriptStateChanged(prevSnapshot, snapshot)
	statusRefreshCmd := tea.Cmd(nil)
	sidebarDiffRefreshCmd := tea.Cmd(nil)
	bossNoticeCmd := tea.Cmd(nil)
	if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(hadPrev, prevSnapshot, snapshot) {
		statusRefreshCmd = m.recordEmbeddedSessionActivityCmd(projectPath, snapshot)
	}
	if hadPrev && shouldRefreshProjectStatusAfterCodexSnapshot(prevSnapshot, snapshot) {
		statusRefreshCmd = m.recordEmbeddedSessionSettledAndRefreshCmd(projectPath, snapshot)
		sidebarDiffRefreshCmd = m.requestVisibleEmbeddedSidebarDiffRefreshCmd(projectPath, true)
	}
	if m.bossMode {
		if notice := bossBrowserAttentionHostNoticeForSnapshot(projectPath, hadPrev, prevSnapshot, snapshot); notice != "" {
			var cmd tea.Cmd
			m, cmd = m.updateBossHostNotice(notice)
			bossNoticeCmd = batchCmds(bossNoticeCmd, cmd)
		}
	}
	var cmd tea.Cmd
	m, cmd = m.handleBossEngineerTurnCompletion(projectPath, hadPrev, prevSnapshot, snapshot)
	bossNoticeCmd = batchCmds(bossNoticeCmd, cmd)
	if m.codexVisibleProject == projectPath {
		viewportStarted := time.Now()
		m.resetCodexToolAnswerState(projectPath)
		m.syncCodexViewport(transcriptChanged)
		m.recordAISyncLatency("Embedded viewport", projectPath, providerLabel, time.Since(viewportStarted), "deferred")
		if codexSnapshotBrowserWaitingForUser(snapshot) {
			bossNoticeCmd = batchCmds(bossNoticeCmd, m.codexInput.Focus())
		}
	}
	linkScanCmd := tea.Cmd(nil)
	if m.codexVisibleProject == projectPath {
		linkScanCmd = m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	}
	browserStateCmd := m.maybeReadManagedBrowserStateCmd(snapshot)
	m.completeModelSettleLatency(projectPath, snapshot)
	if !snapshot.Closed {
		m.markCodexSessionLive(projectPath)
		m.detectBrowserAttentionNotification(projectPath, snapshot)
		m.detectQuestionNotification(projectPath, snapshot)
		return m, batchCmds(statusRefreshCmd, sidebarDiffRefreshCmd, linkScanCmd, browserStateCmd, bossNoticeCmd)
	}
	m.removeManagedBrowserLease(projectPath, snapshot)
	m.cancelModelSettleLatency(projectPath, "session closed")
	if !m.markCodexSessionClosedHandled(projectPath) {
		return m, nil
	}
	if m.codexHiddenProject == projectPath {
		m.codexHiddenProject = ""
	}
	if m.codexVisibleProject == projectPath && strings.TrimSpace(snapshot.Status) != "" {
		m.status = snapshot.Status
	} else if strings.TrimSpace(snapshot.Status) != "" {
		m.status = snapshot.Status
	}
	m.loading = true
	cmds := []tea.Cmd{linkScanCmd, browserStateCmd, m.requestProjectInvalidationCmd(invalidateProjectScan("", false))}
	if shouldRecordEmbeddedSessionSettledAfterClose(hadPrev, prevSnapshot, snapshot) {
		cmds = append([]tea.Cmd{m.recordEmbeddedSessionSettledCmd(projectPath, snapshot)}, cmds...)
	}
	return m, batchCmds(cmds...)
}

func (m Model) projectPendingEmbeddedApproval(projectPath string) (*codexapp.ApprovalRequest, codexapp.Provider, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil, "", false
	}
	snapshot, ok := m.cachedLiveCodexSnapshot(projectPath)
	if !ok {
		return nil, "", false
	}
	if snapshot.Closed || snapshot.PendingApproval == nil {
		return nil, "", false
	}
	return snapshot.PendingApproval, embeddedProvider(snapshot), true
}

func (m Model) projectApprovalPulseActive(projectPath string) bool {
	if _, _, ok := m.projectPendingEmbeddedApproval(projectPath); !ok {
		return false
	}
	return m.spinnerFrame%2 == 0
}

func (m Model) projectPendingEmbeddedQuestion(projectPath string) (string, codexapp.Provider, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", "", false
	}
	snapshot, ok := m.cachedLiveCodexSnapshot(projectPath)
	if !ok {
		return "", "", false
	}
	if snapshot.Closed {
		return "", "", false
	}
	if snapshot.PendingToolInput != nil {
		return snapshot.PendingToolInput.Summary(), embeddedProvider(snapshot), true
	}
	if snapshot.PendingElicitation != nil {
		return snapshot.PendingElicitation.Summary(), embeddedProvider(snapshot), true
	}
	return "", "", false
}

func (m Model) projectQuestionPulseActive(projectPath string) bool {
	if _, _, ok := m.projectPendingEmbeddedQuestion(projectPath); !ok {
		return false
	}
	return m.spinnerFrame%2 == 0
}

func shouldRefreshProjectStatusAfterCodexSnapshot(prev, next codexapp.Snapshot) bool {
	return !prev.Closed && prev.Busy && !next.Closed && !next.Busy
}

func shouldRecordEmbeddedSessionSettledAfterClose(hadPrev bool, prev, next codexapp.Snapshot) bool {
	if next.Closed || !next.Started || embeddedSnapshotActivityAt(next).IsZero() {
		return false
	}
	if !hadPrev {
		return false
	}
	return prev.Busy
}

func shouldRecordEmbeddedSessionSettledAfterDisappearance(prev codexapp.Snapshot) bool {
	if prev.Closed || !prev.Started || embeddedSnapshotActivityAt(prev).IsZero() {
		return false
	}
	return prev.Busy
}

func shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(hadPrev bool, prev, next codexapp.Snapshot) bool {
	if next.Closed || !next.Started || !next.Busy || embeddedSnapshotActivityAt(next).IsZero() {
		return false
	}
	if !hadPrev {
		return true
	}
	return embeddedSnapshotActivityAt(next).After(embeddedSnapshotActivityAt(prev))
}

func embeddedSessionActivityFromSnapshot(projectPath string, snapshot codexapp.Snapshot) (service.EmbeddedSessionActivity, bool) {
	return embeddedSessionActivityFromSnapshotWithTurnState(projectPath, snapshot, snapshot.Busy, false)
}

func embeddedSessionSettledActivityFromSnapshot(projectPath string, snapshot codexapp.Snapshot) (service.EmbeddedSessionActivity, bool) {
	if !snapshot.Started {
		return service.EmbeddedSessionActivity{}, false
	}
	return embeddedSessionActivityFromSnapshotWithTurnState(projectPath, snapshot, true, true)
}

func embeddedSessionActivityFromSnapshotWithTurnState(projectPath string, snapshot codexapp.Snapshot, latestTurnKnown, latestTurnCompleted bool) (service.EmbeddedSessionActivity, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		projectPath = normalizeProjectPath(snapshot.ProjectPath)
	}
	sessionID := strings.TrimSpace(snapshot.ThreadID)
	lastActivity := embeddedSnapshotActivityAt(snapshot)
	if projectPath == "" || sessionID == "" || lastActivity.IsZero() {
		return service.EmbeddedSessionActivity{}, false
	}
	return service.EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               embeddedSessionSource(snapshot.Provider),
		SessionID:            sessionID,
		Format:               embeddedSessionFormat(snapshot.Provider),
		LastActivityAt:       lastActivity,
		LatestTurnStartedAt:  snapshot.BusySince,
		LatestTurnStateKnown: latestTurnKnown,
		LatestTurnCompleted:  latestTurnCompleted,
		WorkState:            todoWorkStateFromEmbeddedSnapshot(snapshot, latestTurnCompleted),
	}, true
}

func todoWorkStateFromEmbeddedSnapshot(snapshot codexapp.Snapshot, latestTurnCompleted bool) model.TodoWorkState {
	if snapshot.Closed || latestTurnCompleted {
		return model.TodoWorkStateIdle
	}
	if snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return model.TodoWorkStateWaiting
	}
	if snapshot.BrowserActivity.Normalize().State == browserctl.SessionActivityStateWaitingForUser {
		return model.TodoWorkStateWaiting
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		return model.TodoWorkStateBlocked
	}
	if snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return model.TodoWorkStateWorking
	}
	return model.TodoWorkStateIdle
}

func embeddedSessionSource(provider codexapp.Provider) model.SessionSource {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case codexapp.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case codexapp.ProviderLCAgent:
		return model.SessionSourceLCAgent
	default:
		return model.SessionSourceCodex
	}
}

func embeddedSessionFormat(provider codexapp.Provider) string {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return "opencode_db"
	case codexapp.ProviderClaudeCode:
		return "claude_code"
	case codexapp.ProviderLCAgent:
		return "lcagent_jsonl"
	default:
		return "modern"
	}
}

func embeddedSnapshotActivityAt(snapshot codexapp.Snapshot) time.Time {
	if !snapshot.LastBusyActivityAt.IsZero() {
		return snapshot.LastBusyActivityAt
	}
	return snapshot.LastActivityAt
}

func (m Model) projectHasLiveCodexSession(projectPath string) bool {
	snapshot, ok := m.liveCodexSnapshot(projectPath)
	if !ok {
		return false
	}
	return snapshot.Started && !snapshot.Closed
}

func (m Model) liveCodexSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	snapshot, ok := m.cachedLiveCodexSnapshot(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
}

func (m Model) launchCodexForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderCodex, forceNew, prompt)
}

func (m Model) launchOpenCodeForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, forceNew, prompt)
}

func (m Model) launchClaudeForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, forceNew, prompt)
}

func (m Model) launchLCAgentForSelection(forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForSelection(codexapp.ProviderLCAgent, forceNew, prompt)
}

func (m Model) revealPendingEmbeddedOpen(projectPath string) (Model, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || m.codexPendingOpen == nil {
		return m, false
	}
	pendingPath := normalizeProjectPath(m.codexPendingOpen.projectPath)
	if pendingPath == "" || pendingPath != projectPath {
		return m, false
	}
	if current := normalizeProjectPath(m.codexVisibleProject); current != "" && current != pendingPath {
		m.persistVisibleCodexDraft()
	}
	m.codexPendingOpen.showWhilePending = true
	m.codexPendingOpen.hideOnOpen = false
	label := m.codexPendingOpenProvider().Label()
	m.status = "Embedded " + label + " session is still starting..."
	return m, true
}

func (m Model) launchEmbeddedForSelection(provider codexapp.Provider, forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	p, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	return m.launchEmbeddedForProject(p, provider, forceNew, prompt)
}

func (m Model) launchEmbeddedForProject(p model.ProjectSummary, provider codexapp.Provider, forceNew bool, prompt string) (tea.Model, tea.Cmd) {
	return m.launchEmbeddedForProjectWithOptions(p, provider, embeddedLaunchOptions{
		forceNew: forceNew,
		prompt:   prompt,
		reveal:   true,
	})
}

type embeddedLaunchOptions struct {
	forceNew bool
	prompt   string
	reveal   bool
	resumeID string
}

func (m Model) launchEmbeddedForProjectWithOptions(p model.ProjectSummary, provider codexapp.Provider, options embeddedLaunchOptions) (tea.Model, tea.Cmd) {
	provider = provider.Normalized()
	if !options.forceNew && strings.TrimSpace(options.prompt) == "" {
		if updated, ok := m.revealPendingEmbeddedOpen(p.Path); ok {
			return updated, nil
		}
	}
	if !p.PresentOnDisk {
		m.status = provider.Label() + " launch requires a folder present on disk"
		return m, nil
	}
	if block, blocked := m.embeddedLaunchBlock(p, provider, options.forceNew); blocked {
		actionLabel := m.attentionDialogSessionActionLabel(p, block.BlockingProvider)
		hint := fmt.Sprintf("Finish or close the %s session, then try starting %s here again.", block.BlockingProvider.Label(), provider.Label())
		if actionLabel != "" {
			hint = fmt.Sprintf("%s to finish or close it, then try starting %s here again.", actionLabel, provider.Label())
		}
		m.showAttentionDialog(attentionDialogState{
			Title:           "Launch blocked",
			ProjectName:     projectNameForPicker(p, p.Path),
			ProjectPath:     p.Path,
			Message:         block.Message,
			Hint:            hint,
			PrimaryLabel:    actionLabel,
			PrimaryProvider: block.BlockingProvider,
		})
		return m, nil
	}
	if !options.forceNew && strings.TrimSpace(options.prompt) == "" {
		if _, ok := m.liveEmbeddedSnapshotForProject(p.Path, provider); ok {
			m.rememberEmbeddedProvider(provider)
			if !options.reveal {
				m.status = "Embedded " + provider.Label() + " session is already open in the background"
				return m, nil
			}
			return m.showCodexProject(p.Path, "Embedded "+provider.Label()+" session reopened. Alt+Up hides it.")
		}
		if m.hasRestorableEmbeddedSession(p.Path, provider) {
			m.rememberEmbeddedProvider(provider)
			if !options.reveal {
				m.status = "Embedded " + provider.Label() + " session is already available in the background"
				return m, nil
			}
			return m.showCodexProject(p.Path, "Embedded "+provider.Label()+" session reopened. Alt+Up hides it.")
		}
	}

	req := codexapp.LaunchRequest{
		Provider:                 provider,
		ProjectPath:              p.Path,
		ResumeID:                 firstNonEmptyTrimmed(options.resumeID, m.selectedProjectSessionID(p, provider)),
		ForceNew:                 options.forceNew,
		Prompt:                   options.prompt,
		Preset:                   m.currentCodexLaunchPreset(),
		PlaywrightPolicy:         m.currentPlaywrightPolicy(),
		AppDataDir:               m.appDataDir(),
		CodexHome:                m.codexHome(),
		LCAgentPath:              m.lcagentPath(),
		LCAgentEnvFile:           m.lcagentEnvFile(),
		LCAgentOpenAIAPIKey:      m.openAIAPIKey(),
		LCAgentOpenRouterAPIKey:  m.openRouterAPIKey(),
		LCAgentDeepSeekAPIKey:    m.deepSeekAPIKey(),
		LCAgentMoonshotAPIKey:    m.moonshotAPIKey(),
		LCAgentXiaomiAPIKey:      m.xiaomiAPIKey(),
		LCAgentXiaomiBaseURL:     m.xiaomiBaseURL(),
		LCAgentPreflightAccess:   true,
		LCAgentRoutePreset:       m.lcagentRoutePreset(),
		LCAgentProvider:          m.lcagentProvider(),
		LCAgentAuto:              m.lcagentAuto(),
		LCAgentAdminWrite:        m.lcagentAdminWrite(),
		LCAgentToolProfile:       m.lcagentToolProfile(),
		LCAgentContextProfile:    m.lcagentContextProfile(),
		LCAgentRequestTimeout:    m.lcagentRequestTimeout(),
		LCAgentUtilityProvider:   m.lcagentUtilityProvider(),
		LCAgentUtilityModel:      m.lcagentUtilityModel(),
		LCAgentWebSearchBackend:  m.lcagentWebSearchBackend(),
		LCAgentWebSearchAPIKey:   m.lcagentWebSearchAPIKey(),
		LCAgentWebSearchEngineID: m.lcagentWebSearchEngineID(),
		LCAgentWebSearchURL:      m.lcagentWebSearchURL(),
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.ensureCodexRuntime()
	if strings.TrimSpace(req.ResumeID) != "" {
		m.clearDismissedSuspendedTurn(req.ProjectPath, provider, req.ResumeID)
	}
	m.clearEmbeddedLaunchProviderOverride(p.Path)
	if options.forceNew {
		m.beginNewCodexPendingOpenWithVisibilityAndReveal(p.Path, provider, options.reveal, options.reveal)
	} else {
		m.beginCodexPendingOpenWithVisibilityAndReveal(p.Path, provider, options.reveal, options.reveal)
	}
	m.err = nil
	m.rememberEmbeddedProvider(provider)
	if options.forceNew {
		m.status = "Starting a new embedded " + provider.Label() + " session..."
	} else {
		m.status = "Opening embedded " + provider.Label() + " session..."
	}
	return m, m.openCodexSessionCmdWithVisibility(req, options.reveal)
}

func (m Model) hasRestorableEmbeddedSession(projectPath string, provider codexapp.Provider) bool {
	session, ok := m.codexSession(projectPath)
	if !ok {
		return false
	}
	provider = provider.Normalized()
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	if snapshot, got := session.TrySnapshot(); got {
		return !snapshot.Closed && embeddedProvider(snapshot) == provider
	}
	if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
		return !snapshot.Closed && embeddedProvider(snapshot) == provider
	}
	return false
}

func (m Model) shouldReloadEmbeddedLCAgentAfterSettingsSave(previous, saved config.EditableSettings) (string, bool) {
	projectPath := strings.TrimSpace(m.settingsEmbeddedProject)
	if projectPath == "" || m.settingsEmbeddedProvider.Normalized() != codexapp.ProviderLCAgent {
		return "", false
	}
	if !lcagentLaunchSettingsChanged(previous, saved) {
		return "", false
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return "", false
	}
	if snapshot, got := session.TrySnapshot(); got {
		if snapshot.Closed || embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
			return "", false
		}
		return projectPath, true
	}
	if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
		if snapshot.Closed || embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
			return "", false
		}
	}
	return projectPath, true
}

func lcagentLaunchSettingsChanged(previous, saved config.EditableSettings) bool {
	return strings.TrimSpace(previous.LCAgentPath) != strings.TrimSpace(saved.LCAgentPath) ||
		strings.TrimSpace(previous.LCAgentEnvFile) != strings.TrimSpace(saved.LCAgentEnvFile) ||
		strings.TrimSpace(previous.OpenAIAPIKey) != strings.TrimSpace(saved.OpenAIAPIKey) ||
		strings.TrimSpace(previous.OpenRouterAPIKey) != strings.TrimSpace(saved.OpenRouterAPIKey) ||
		strings.TrimSpace(previous.DeepSeekAPIKey) != strings.TrimSpace(saved.DeepSeekAPIKey) ||
		strings.TrimSpace(previous.MoonshotAPIKey) != strings.TrimSpace(saved.MoonshotAPIKey) ||
		strings.TrimSpace(previous.XiaomiAPIKey) != strings.TrimSpace(saved.XiaomiAPIKey) ||
		strings.TrimSpace(previous.XiaomiBaseURL) != strings.TrimSpace(saved.XiaomiBaseURL) ||
		strings.TrimSpace(previous.LCAgentRoutePreset) != strings.TrimSpace(saved.LCAgentRoutePreset) ||
		strings.TrimSpace(previous.LCAgentProvider) != strings.TrimSpace(saved.LCAgentProvider) ||
		strings.TrimSpace(previous.EmbeddedLCAgentModel) != strings.TrimSpace(saved.EmbeddedLCAgentModel) ||
		strings.TrimSpace(previous.EmbeddedLCAgentReasoning) != strings.TrimSpace(saved.EmbeddedLCAgentReasoning) ||
		strings.TrimSpace(previous.LCAgentAuto) != strings.TrimSpace(saved.LCAgentAuto) ||
		previous.LCAgentAdminWrite != saved.LCAgentAdminWrite ||
		strings.TrimSpace(previous.LCAgentToolProfile) != strings.TrimSpace(saved.LCAgentToolProfile) ||
		strings.TrimSpace(previous.LCAgentContextProfile) != strings.TrimSpace(saved.LCAgentContextProfile) ||
		previous.LCAgentRequestTimeout != saved.LCAgentRequestTimeout ||
		strings.TrimSpace(previous.LCAgentUtilityProvider) != strings.TrimSpace(saved.LCAgentUtilityProvider) ||
		strings.TrimSpace(previous.LCAgentUtilityModel) != strings.TrimSpace(saved.LCAgentUtilityModel) ||
		strings.TrimSpace(previous.LCAgentWebSearchBackend) != strings.TrimSpace(saved.LCAgentWebSearchBackend) ||
		strings.TrimSpace(previous.LCAgentWebSearchAPIKey) != strings.TrimSpace(saved.LCAgentWebSearchAPIKey) ||
		strings.TrimSpace(previous.LCAgentWebSearchEngineID) != strings.TrimSpace(saved.LCAgentWebSearchEngineID) ||
		strings.TrimSpace(previous.LCAgentWebSearchURL) != strings.TrimSpace(saved.LCAgentWebSearchURL)
}

func (m Model) lcagentLaunchRequestFromSettings(projectPath string, settings config.EditableSettings) codexapp.LaunchRequest {
	return codexapp.LaunchRequest{
		Provider:                 codexapp.ProviderLCAgent,
		ProjectPath:              strings.TrimSpace(projectPath),
		PendingModel:             strings.TrimSpace(settings.EmbeddedLCAgentModel),
		PendingReasoning:         strings.TrimSpace(settings.EmbeddedLCAgentReasoning),
		PlaywrightPolicy:         settings.PlaywrightPolicy,
		AppDataDir:               m.appDataDir(),
		CodexHome:                m.codexHome(),
		LCAgentPath:              strings.TrimSpace(settings.LCAgentPath),
		LCAgentEnvFile:           strings.TrimSpace(settings.LCAgentEnvFile),
		LCAgentOpenAIAPIKey:      strings.TrimSpace(settings.OpenAIAPIKey),
		LCAgentOpenRouterAPIKey:  strings.TrimSpace(settings.OpenRouterAPIKey),
		LCAgentDeepSeekAPIKey:    strings.TrimSpace(settings.DeepSeekAPIKey),
		LCAgentMoonshotAPIKey:    strings.TrimSpace(settings.MoonshotAPIKey),
		LCAgentXiaomiAPIKey:      strings.TrimSpace(settings.XiaomiAPIKey),
		LCAgentXiaomiBaseURL:     strings.TrimSpace(settings.XiaomiBaseURL),
		LCAgentPreflightAccess:   true,
		LCAgentRoutePreset:       strings.TrimSpace(settings.LCAgentRoutePreset),
		LCAgentProvider:          strings.TrimSpace(settings.LCAgentProvider),
		LCAgentAuto:              strings.TrimSpace(settings.LCAgentAuto),
		LCAgentAdminWrite:        settings.LCAgentAdminWrite,
		LCAgentToolProfile:       strings.TrimSpace(settings.LCAgentToolProfile),
		LCAgentContextProfile:    strings.TrimSpace(settings.LCAgentContextProfile),
		LCAgentRequestTimeout:    settings.LCAgentRequestTimeout,
		LCAgentUtilityProvider:   strings.TrimSpace(settings.LCAgentUtilityProvider),
		LCAgentUtilityModel:      strings.TrimSpace(settings.LCAgentUtilityModel),
		LCAgentWebSearchBackend:  strings.TrimSpace(settings.LCAgentWebSearchBackend),
		LCAgentWebSearchAPIKey:   strings.TrimSpace(settings.LCAgentWebSearchAPIKey),
		LCAgentWebSearchEngineID: strings.TrimSpace(settings.LCAgentWebSearchEngineID),
		LCAgentWebSearchURL:      strings.TrimSpace(settings.LCAgentWebSearchURL),
		RuntimeManager:           m.runtimeManager,
	}
}

func (m *Model) reloadEmbeddedLCAgentAfterSettingsCmd(projectPath string, settings config.EditableSettings) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	manager := m.codexManager
	req := m.lcagentLaunchRequestFromSettings(projectPath, settings)
	perfOpID := m.beginAILatencyOp("Embedded reload", projectPath, "LCAgent settings")
	return func() tea.Msg {
		startedAt := time.Now()
		if manager == nil {
			return codexSessionOpenedMsg{
				projectPath:  projectPath,
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          fmt.Errorf("LCAgent manager unavailable"),
			}
		}
		if existing, ok := manager.Session(projectPath); ok {
			snapshot := existing.Snapshot()
			if embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
				return codexActionMsg{
					projectPath:  projectPath,
					perfOpID:     perfOpID,
					perfDuration: time.Since(startedAt),
					status:       "Settings saved. New LCAgent sessions will use the saved configuration.",
				}
			}
			if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
				req.ResumeID = threadID
			}
			if err := manager.CloseProject(projectPath); err != nil {
				return codexSessionOpenedMsg{
					projectPath:  projectPath,
					perfOpID:     perfOpID,
					perfDuration: time.Since(startedAt),
					err:          err,
				}
			}
		}
		if err := req.Validate(); err != nil {
			return codexSessionOpenedMsg{
				projectPath:  projectPath,
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          err,
			}
		}
		session, _, err := manager.Open(req)
		if err != nil {
			return codexSessionOpenedMsg{
				projectPath:  projectPath,
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          err,
			}
		}
		snapshot := session.Snapshot()
		return codexSessionOpenedMsg{
			projectPath:      projectPath,
			snapshot:         snapshot,
			status:           "Settings saved. Restarted LCAgent so the next run uses the new configuration.",
			visibleStatus:    "Settings saved. Restarted LCAgent so the next run uses the new configuration.",
			backgroundStatus: "Settings saved. Restarted LCAgent in the background.",
			perfOpID:         perfOpID,
			perfDuration:     time.Since(startedAt),
		}
	}
}

func (m Model) appDataDir() string {
	if path := strings.TrimSpace(m.appDataDirPath); path != "" {
		return path
	}
	return config.Default().DataDir
}

func (m Model) codexHome() string {
	if path := strings.TrimSpace(m.codexHomePath); path != "" {
		return path
	}
	return strings.TrimSpace(config.Default().CodexHome)
}

func (m Model) openAIAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().OpenAIAPIKey)
}

func (m Model) openRouterAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().OpenRouterAPIKey)
}

func (m Model) deepSeekAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().DeepSeekAPIKey)
}

func (m Model) moonshotAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().MoonshotAPIKey)
}

func (m Model) xiaomiAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().XiaomiAPIKey)
}

func (m Model) xiaomiBaseURL() string {
	return strings.TrimSpace(m.currentSettingsBaseline().XiaomiBaseURL)
}

func (m Model) lcagentPath() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentPath)
}

func (m Model) lcagentEnvFile() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentEnvFile)
}

func (m Model) lcagentRoutePreset() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentRoutePreset)
}

func (m Model) lcagentProvider() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentProvider)
}

func (m Model) lcagentAuto() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentAuto)
}

func (m Model) lcagentAdminWrite() bool {
	return m.currentSettingsBaseline().LCAgentAdminWrite
}

func (m Model) lcagentToolProfile() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentToolProfile)
}

func (m Model) lcagentContextProfile() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentContextProfile)
}

func (m Model) lcagentRequestTimeout() time.Duration {
	return m.currentSettingsBaseline().LCAgentRequestTimeout
}

func (m Model) lcagentUtilityProvider() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentUtilityProvider)
}

func (m Model) lcagentUtilityModel() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentUtilityModel)
}

func (m Model) lcagentWebSearchBackend() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentWebSearchBackend)
}

func (m Model) lcagentWebSearchAPIKey() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentWebSearchAPIKey)
}

func (m Model) lcagentWebSearchEngineID() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentWebSearchEngineID)
}

func (m Model) lcagentWebSearchURL() string {
	return strings.TrimSpace(m.currentSettingsBaseline().LCAgentWebSearchURL)
}

type embeddedLaunchBlock struct {
	Message          string
	BlockingProvider codexapp.Provider
}

func (m Model) embeddedLaunchBlock(project model.ProjectSummary, requested codexapp.Provider, forceNew bool) (embeddedLaunchBlock, bool) {
	requested = requested.Normalized()
	if requested == "" {
		requested = codexapp.ProviderCodex
	}
	if project.Path == "" {
		return embeddedLaunchBlock{}, false
	}
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		liveProvider := embeddedProvider(snapshot)
		if liveProvider != requested && snapshot.Started && !snapshot.Closed {
			blocking := embeddedSessionBlocksProviderSwitch(snapshot)
			if blocking || !forceNew {
				message := fmt.Sprintf("This project already has an open embedded %s session. Close it before starting %s here.", liveProvider.Label(), requested.Label())
				if blocking {
					message = fmt.Sprintf("This project already has an active embedded %s session. Finish or close it before starting %s here.", liveProvider.Label(), requested.Label())
				}
				return embeddedLaunchBlock{
					Message:          message,
					BlockingProvider: liveProvider,
				}, true
			}
		}
	}
	latestProvider := providerForSessionFormat(project.LatestSessionFormat)
	if latestProvider == "" || latestProvider == requested {
		return embeddedLaunchBlock{}, false
	}
	if !projectLatestSessionBlocksProviderSwitch(project, m.currentTime(), m.embeddedLaunchProtectionWindow()) {
		return embeddedLaunchBlock{}, false
	}
	return embeddedLaunchBlock{
		Message:          fmt.Sprintf("This project already has an unfinished %s session. Finish or close it before starting %s here.", latestProvider.Label(), requested.Label()),
		BlockingProvider: latestProvider,
	}, true
}

func embeddedSessionBlocksProviderSwitch(snapshot codexapp.Snapshot) bool {
	if !snapshot.Started || snapshot.Closed {
		return false
	}
	if snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return true
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return true
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseRunning, codexapp.SessionPhaseFinishing, codexapp.SessionPhaseReconciling, codexapp.SessionPhaseStalled, codexapp.SessionPhaseExternal:
		return true
	default:
		return false
	}
}

func projectLatestSessionBlocksProviderSwitch(project model.ProjectSummary, now time.Time, protectionWindow time.Duration) bool {
	if !project.LatestTurnStateKnown || project.LatestTurnCompleted {
		return false
	}
	if project.LatestSessionLastEventAt.IsZero() {
		return true
	}
	if protectionWindow <= 0 || now.IsZero() {
		return true
	}
	return now.Sub(project.LatestSessionLastEventAt) <= protectionWindow
}

func (m Model) embeddedLaunchProtectionWindow() time.Duration {
	settings := m.currentSettingsBaseline()
	if settings.StuckThreshold > 0 {
		return settings.StuckThreshold
	}
	if settings.ActiveThreshold > 0 {
		return settings.ActiveThreshold
	}
	return config.Default().StuckThreshold
}

func (m Model) selectedProjectCodexSessionID(project model.ProjectSummary) string {
	return m.selectedProjectSessionID(project, codexapp.ProviderCodex)
}

func (m Model) liveEmbeddedSnapshotForProject(projectPath string, provider codexapp.Provider) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	if projectPath == "" || provider == "" {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := m.cachedLiveCodexSnapshot(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	if strings.TrimSpace(snapshot.ThreadID) == "" {
		return codexapp.Snapshot{}, false
	}
	if embeddedProvider(snapshot) != provider {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
}

func (m Model) selectedProjectSessionID(project model.ProjectSummary, provider codexapp.Provider) string {
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(project.Path, provider); ok {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	if m.detail.Summary.Path == project.Path {
		for _, session := range m.detail.Sessions {
			sessionID := session.ExternalID()
			if providerForSessionFormat(session.Format) == provider.Normalized() && strings.TrimSpace(sessionID) != "" {
				return sessionID
			}
		}
	}
	if providerForSessionFormat(project.LatestSessionFormat) == provider.Normalized() {
		return strings.TrimSpace(project.ExternalLatestSessionID())
	}
	return ""
}

func (m Model) currentCodexLaunchPreset() codexcli.Preset {
	settings := m.currentSettingsBaseline()
	if settings.CodexLaunchPreset == "" {
		return codexcli.DefaultPreset()
	}
	return settings.CodexLaunchPreset
}

func (m Model) currentPlaywrightPolicy() browserctl.Policy {
	return m.currentSettingsBaseline().PlaywrightPolicy.Normalize()
}

func isCodexSessionFormat(format string) bool {
	return providerForSessionFormat(format) == codexapp.ProviderCodex
}

func isOpenCodeSessionFormat(format string) bool {
	return providerForSessionFormat(format) == codexapp.ProviderOpenCode
}

func providerForSessionFormat(format string) codexapp.Provider {
	switch strings.TrimSpace(format) {
	case "modern", "legacy":
		return codexapp.ProviderCodex
	case "opencode_db":
		return codexapp.ProviderOpenCode
	case "claude_code":
		return codexapp.ProviderClaudeCode
	case "lcagent_jsonl":
		return codexapp.ProviderLCAgent
	default:
		return ""
	}
}

func preferredEmbeddedProviderFromProjectSummary(project model.ProjectSummary) codexapp.Provider {
	if provider := providerForSessionFormat(project.LatestSessionFormat); provider != "" {
		return provider
	}
	return codexapp.ProviderCodex
}

func (m Model) preferredEmbeddedProviderForProject(project model.ProjectSummary) codexapp.Provider {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		return embeddedProvider(snapshot)
	}
	if provider, ok := m.embeddedLaunchProviderOverride(project.Path); ok {
		return provider
	}
	return preferredEmbeddedProviderFromProjectSummary(project)
}

func embeddedLaunchProviderOptions() []codexapp.Provider {
	return []codexapp.Provider{
		codexapp.ProviderCodex,
		codexapp.ProviderOpenCode,
		codexapp.ProviderClaudeCode,
		codexapp.ProviderLCAgent,
	}
}

func (m Model) initialEmbeddedProviderForNewItem(requested codexapp.Provider) (codexapp.Provider, string) {
	if provider := explicitEmbeddedProvider(requested); provider != "" {
		return provider, ""
	}
	return m.defaultEmbeddedProviderForNewItem()
}

func (m Model) defaultEmbeddedProviderForNewItem() (codexapp.Provider, string) {
	if provider := explicitEmbeddedProvider(m.lastEmbeddedProvider); provider != "" {
		return provider, "last used"
	}
	if provider, ok := m.latestProjectListEmbeddedProvider(); ok {
		return provider, "last used"
	}
	if project, ok := m.selectedProject(); ok {
		if provider, ok := m.embeddedProviderHintForProject(project); ok {
			return provider, "selected project"
		}
	}
	if provider := embeddedProviderFromAIBackend(m.currentSettingsBaseline().AIBackend); provider != "" {
		return provider, "configured backend"
	}
	return codexapp.ProviderCodex, "built-in default"
}

func (m Model) latestProjectListEmbeddedProvider() (codexapp.Provider, bool) {
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}
	var latest time.Time
	var selected codexapp.Provider
	for _, project := range projects {
		provider := providerForSessionFormat(project.LatestSessionFormat)
		if provider == "" {
			continue
		}
		at := project.LatestSessionLastEventAt
		if at.IsZero() {
			at = project.LastActivity
		}
		if selected == "" || at.After(latest) {
			selected = provider
			latest = at
		}
	}
	if selected == "" {
		return "", false
	}
	return selected, true
}

func (m Model) embeddedProviderHintForProject(project model.ProjectSummary) (codexapp.Provider, bool) {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		if provider := explicitEmbeddedProvider(embeddedProvider(snapshot)); provider != "" {
			return provider, true
		}
	}
	if provider, ok := m.embeddedLaunchProviderOverride(project.Path); ok {
		return provider, true
	}
	if provider := providerForSessionFormat(project.LatestSessionFormat); provider != "" {
		return provider, true
	}
	return "", false
}

func embeddedProviderFromAIBackend(backend config.AIBackend) codexapp.Provider {
	switch backend {
	case config.AIBackendCodex:
		return codexapp.ProviderCodex
	case config.AIBackendOpenCode:
		return codexapp.ProviderOpenCode
	case config.AIBackendClaude:
		return codexapp.ProviderClaudeCode
	default:
		return ""
	}
}

func (m *Model) rememberEmbeddedProvider(provider codexapp.Provider) {
	if m == nil {
		return
	}
	if provider := explicitEmbeddedProvider(provider); provider != "" {
		m.lastEmbeddedProvider = provider
	}
}

func (m Model) embeddedLaunchProviderOverride(projectPath string) (codexapp.Provider, bool) {
	key := normalizeProjectPath(projectPath)
	if key == "" || len(m.embeddedProviderOverrides) == 0 {
		return "", false
	}
	provider := explicitEmbeddedProvider(m.embeddedProviderOverrides[key])
	if provider == "" {
		return "", false
	}
	return provider, true
}

func (m *Model) setEmbeddedLaunchProviderOverride(projectPath string, provider codexapp.Provider) {
	if m == nil {
		return
	}
	key := normalizeProjectPath(projectPath)
	provider = explicitEmbeddedProvider(provider)
	if key == "" || provider == "" {
		return
	}
	if m.embeddedProviderOverrides == nil {
		m.embeddedProviderOverrides = make(map[string]codexapp.Provider)
	}
	m.embeddedProviderOverrides[key] = provider
}

func (m *Model) clearEmbeddedLaunchProviderOverride(projectPath string) {
	if m == nil || len(m.embeddedProviderOverrides) == 0 {
		return
	}
	key := normalizeProjectPath(projectPath)
	if key == "" {
		return
	}
	delete(m.embeddedProviderOverrides, key)
}

func explicitEmbeddedProvider(provider codexapp.Provider) codexapp.Provider {
	if strings.TrimSpace(string(provider)) == "" {
		return ""
	}
	return provider.Normalized()
}

func (m Model) currentEmbeddedLaunchLabel() string {
	if project, ok := m.selectedProject(); ok {
		return m.preferredEmbeddedProviderForProject(project).Label()
	}
	return codexapp.ProviderCodex.Label()
}
