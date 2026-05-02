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
	projectPath  string
	perfOpID     int64
	perfDuration time.Duration
	status       string
	closed       bool
	restoreDraft codexDraft
	provider     codexapp.Provider
	model        string
	reasoning    string
	awaitSettle  bool
	err          error
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
	if msg.err != nil {
		provider := m.codexPendingOpenProvider()
		m.finishCodexPendingOpen(msg.projectPath, codexapp.Snapshot{}, false, false)
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
	if hasTodoLaunchDraft {
		m.clearTodoLaunchDraft(msg.projectPath)
		if draft.openModelFirst {
			m.openCodexModelPickerLoading()
			m.status = "Pick a model, then send the TODO draft."
			return m, tea.Batch(seenCmd, m.openCodexModelPickerCmd())
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
		return m, tea.Batch(seenCmd, m.codexInput.Focus())
	}
	return m, seenCmd
}

func (m Model) applyCodexActionMsg(msg codexActionMsg) (tea.Model, tea.Cmd) {
	m.completeAILatencyOp(msg.perfOpID, msg.perfDuration, msg.err, msg.status)
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
	return m, nil
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
	if ok {
		providerLabel = embeddedProvider(snapshot).Label()
		transcriptChanged = !hadPrevSnapshot || codexTranscriptStateChanged(prevSnapshot, snapshot)
		m.observeManagedBrowserLease(msg.projectPath, snapshot)
		if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(hadPrevSnapshot, prevSnapshot, snapshot) {
			statusRefreshCmd = m.recordEmbeddedSessionActivityCmd(msg.projectPath, snapshot)
		}
		if shouldRefreshProjectStatusAfterCodexSnapshot(prevSnapshot, snapshot) {
			statusRefreshCmd = m.recordEmbeddedSessionSettledAndRefreshCmd(msg.projectPath, snapshot)
		}
	}
	m.recordAISyncLatency("Embedded snapshot", msg.projectPath, providerLabel, refreshDuration, "")
	if m.codexVisibleProject == msg.projectPath {
		viewportStarted := time.Now()
		m.resetCodexToolAnswerState(msg.projectPath)
		m.syncCodexViewport(transcriptChanged)
		m.recordAISyncLatency("Embedded viewport", msg.projectPath, providerLabel, time.Since(viewportStarted), "")
		if ok {
			cmds = append(cmds, m.maybeStartCodexArtifactLinkScan(msg.projectPath, snapshot))
		}
	}
	if ok {
		m.completeModelSettleLatency(msg.projectPath, snapshot)
		if !snapshot.Closed {
			m.markCodexSessionLive(msg.projectPath)
			m.detectBrowserAttentionNotification(msg.projectPath, snapshot)
			m.detectQuestionNotification(msg.projectPath, snapshot)
			return m, batchCmds(append(cmds, statusRefreshCmd)...)
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
	if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(hadPrev, prevSnapshot, snapshot) {
		statusRefreshCmd = m.recordEmbeddedSessionActivityCmd(projectPath, snapshot)
	}
	if hadPrev && shouldRefreshProjectStatusAfterCodexSnapshot(prevSnapshot, snapshot) {
		statusRefreshCmd = m.recordEmbeddedSessionSettledAndRefreshCmd(projectPath, snapshot)
	}
	if m.codexVisibleProject == projectPath {
		viewportStarted := time.Now()
		m.resetCodexToolAnswerState(projectPath)
		m.syncCodexViewport(transcriptChanged)
		m.recordAISyncLatency("Embedded viewport", projectPath, providerLabel, time.Since(viewportStarted), "deferred")
	}
	linkScanCmd := tea.Cmd(nil)
	if m.codexVisibleProject == projectPath {
		linkScanCmd = m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	}
	m.completeModelSettleLatency(projectPath, snapshot)
	if !snapshot.Closed {
		m.markCodexSessionLive(projectPath)
		m.detectBrowserAttentionNotification(projectPath, snapshot)
		m.detectQuestionNotification(projectPath, snapshot)
		return m, batchCmds(statusRefreshCmd, linkScanCmd)
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
	cmds := []tea.Cmd{linkScanCmd, m.requestProjectInvalidationCmd(invalidateProjectScan("", false))}
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
	}, true
}

func embeddedSessionSource(provider codexapp.Provider) model.SessionSource {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case codexapp.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
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
	if !options.forceNew && strings.TrimSpace(options.prompt) == "" {
		if updated, ok := m.revealPendingEmbeddedOpen(p.Path); ok {
			return updated, nil
		}
	}
	if !p.PresentOnDisk {
		m.status = provider.Label() + " launch requires a folder present on disk"
		return m, nil
	}
	if block, blocked := m.embeddedLaunchBlock(p, provider); blocked {
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
			if !options.reveal {
				m.status = "Embedded " + provider.Label() + " session is already open in the background"
				return m, nil
			}
			return m.showCodexProject(p.Path, "Embedded "+provider.Label()+" session reopened. Alt+Up hides it.")
		}
		if m.hasRestorableEmbeddedSession(p.Path) {
			if !options.reveal {
				m.status = "Embedded session is already available in the background"
				return m, nil
			}
			return m.showCodexProject(p.Path, "Embedded session reopened. Alt+Up hides it.")
		}
	}

	req := codexapp.LaunchRequest{
		Provider:         provider,
		ProjectPath:      p.Path,
		ResumeID:         firstNonEmptyTrimmed(options.resumeID, m.selectedProjectSessionID(p, provider)),
		ForceNew:         options.forceNew,
		Prompt:           options.prompt,
		Preset:           m.currentCodexLaunchPreset(),
		PlaywrightPolicy: m.currentPlaywrightPolicy(),
		AppDataDir:       m.appDataDir(),
		CodexHome:        m.codexHome(),
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.ensureCodexRuntime()
	if options.forceNew {
		m.beginNewCodexPendingOpenWithVisibilityAndReveal(p.Path, provider, options.reveal, options.reveal)
	} else {
		m.beginCodexPendingOpenWithVisibilityAndReveal(p.Path, provider, options.reveal, options.reveal)
	}
	m.err = nil
	if options.forceNew {
		m.status = "Starting a new embedded " + provider.Label() + " session..."
	} else {
		m.status = "Opening embedded " + provider.Label() + " session..."
	}
	return m, m.openCodexSessionCmdWithVisibility(req, options.reveal)
}

func (m Model) hasRestorableEmbeddedSession(projectPath string) bool {
	session, ok := m.codexSession(projectPath)
	if !ok {
		return false
	}
	if snapshot, got := session.TrySnapshot(); got {
		return !snapshot.Closed
	}
	// If the session lock is contended, assume the helper is still restorable.
	// A later async snapshot refresh will reconcile the exact state.
	return true
}

func (m Model) appDataDir() string {
	if m.svc != nil {
		return m.svc.Config().DataDir
	}
	return config.Default().DataDir
}

func (m Model) codexHome() string {
	if m.svc != nil {
		return strings.TrimSpace(m.svc.Config().CodexHome)
	}
	return strings.TrimSpace(config.Default().CodexHome)
}

type embeddedLaunchBlock struct {
	Message          string
	BlockingProvider codexapp.Provider
}

func (m Model) embeddedLaunchBlock(project model.ProjectSummary, requested codexapp.Provider) (embeddedLaunchBlock, bool) {
	requested = requested.Normalized()
	if requested == "" {
		requested = codexapp.ProviderCodex
	}
	if project.Path == "" {
		return embeddedLaunchBlock{}, false
	}
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		liveProvider := embeddedProvider(snapshot)
		if liveProvider != requested && embeddedSessionBlocksProviderSwitch(snapshot) {
			return embeddedLaunchBlock{
				Message:          fmt.Sprintf("This project already has an active embedded %s session. Finish or close it before starting %s here.", liveProvider.Label(), requested.Label()),
				BlockingProvider: liveProvider,
			}, true
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
	return preferredEmbeddedProviderFromProjectSummary(project)
}

func (m Model) currentEmbeddedLaunchLabel() string {
	if project, ok := m.selectedProject(); ok {
		return m.preferredEmbeddedProviderForProject(project).Label()
	}
	return codexapp.ProviderCodex.Label()
}
