package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/codexslash"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxForceNewEmbeddedOpenAttempts     = 3
	maxOpenCodeCollapsedToolRun         = 5
	openCodeToolPreviewCount            = 3
	openCodeCollapsedToolPreviewMaxText = 180
	openCodeCollapsedAgentCodeLineLimit = 80
	openCodeAgentCodePreviewLines       = 14
	openCodeCollapsedCodePreviewMaxText = 240
	openCodeCollapsedAgentPreviewRatio  = 45
	// Massive output caps: applied to any entry regardless of content type.
	openCodeMaxEntryLines         = 200 // hard cap per entry (collapsed mode)
	openCodeMaxEntryPreviewLines  = 20  // preview lines shown when capped
	openCodeRepetitionWindowLines = 6   // sliding window size for repetition detection
	openCodeRepetitionThreshold   = 4   // consecutive repeated windows to trigger collapse
	openCodeMaxReasoningLines     = 120 // reasoning blocks get a tighter cap
	openCodeMaxReasoningPreview   = 12  // preview lines for reasoning
)

func (m *Model) ensureCodexRuntime() {
	if m.codexManager == nil {
		m.codexManager = codexapp.NewManager()
	}
	if m.codexInput.CharLimit == 0 && m.codexInput.Width() == 0 {
		m.codexInput = newCodexTextarea()
	}
	if m.codexDrafts == nil {
		m.codexDrafts = make(map[string]codexDraft)
	}
	if m.codexClosedHandled == nil {
		m.codexClosedHandled = make(map[string]struct{})
	}
	if m.codexToolAnswers == nil {
		m.codexToolAnswers = make(map[string]codexToolAnswerState)
	}
	if m.codexSnapshots == nil {
		m.codexSnapshots = make(map[string]codexapp.Snapshot)
	}
	if m.codexTranscriptRev == nil {
		m.codexTranscriptRev = make(map[string]uint64)
	}
	if m.codexViewport.Width == 0 && m.codexViewport.Height == 0 {
		m.codexViewport = viewport.New(0, 0)
	}
	m.syncCodexComposerSize()
}

func (m Model) waitCodexCmd() tea.Cmd {
	if m.codexManager == nil || m.codexManager.Updates() == nil {
		return nil
	}
	return func() tea.Msg {
		projectPath, ok := <-m.codexManager.Updates()
		if !ok {
			return nil
		}
		return codexUpdateMsg{projectPath: projectPath}
	}
}

func (m Model) codexVisible() bool {
	return strings.TrimSpace(m.codexVisibleProject) != "" || m.codexPendingOpenVisible()
}

func (m Model) codexPendingOpenProject() string {
	if m.codexPendingOpen == nil {
		return ""
	}
	return strings.TrimSpace(m.codexPendingOpen.projectPath)
}

func (m Model) codexPendingOpenVisible() bool {
	if m.codexPendingOpen == nil {
		return false
	}
	if !m.codexPendingOpen.showWhilePending {
		return false
	}
	return strings.TrimSpace(m.codexPendingOpen.projectPath) != ""
}

func (m Model) codexPendingOpenProvider() codexapp.Provider {
	if m.codexPendingOpen == nil {
		return codexapp.ProviderCodex
	}
	if provider := m.codexPendingOpen.provider.Normalized(); provider != "" {
		return provider
	}
	return codexapp.ProviderCodex
}

func embeddedProvider(snapshot codexapp.Snapshot) codexapp.Provider {
	if provider := snapshot.Provider.Normalized(); provider != "" {
		return provider
	}
	return codexapp.ProviderCodex
}

func (m Model) currentEmbeddedProvider() codexapp.Provider {
	// A pending open represents the user's explicit intent to switch providers,
	// so it takes priority over a stale closed snapshot from the previous session.
	if m.codexPendingOpen != nil {
		if provider := m.codexPendingOpen.provider.Normalized(); provider != "" {
			return provider
		}
	}
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		return embeddedProvider(snapshot)
	}
	return codexapp.ProviderCodex
}

func (m Model) currentEmbeddedProviderLabel() string {
	return m.currentEmbeddedProvider().Label()
}

func embeddedNewCommand(provider codexapp.Provider) string {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return "/opencode-new"
	case codexapp.ProviderClaudeCode:
		return "/claude-new"
	default:
		return "/codex-new"
	}
}

func (m *Model) beginCodexPendingOpen(projectPath string, provider codexapp.Provider) {
	m.beginCodexPendingOpenWithVisibility(projectPath, provider, true)
}

func (m *Model) beginCodexPendingOpenWithVisibility(projectPath string, provider codexapp.Provider, showWhilePending bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.codexPendingOpen = nil
		return
	}
	if current := strings.TrimSpace(m.codexVisibleProject); current != "" && current != projectPath {
		m.persistVisibleCodexDraft()
	}
	m.codexPendingOpen = &codexPendingOpenState{
		projectPath:      projectPath,
		provider:         provider.Normalized(),
		showWhilePending: showWhilePending,
	}
}

func (m *Model) finishCodexPendingOpen(projectPath string, snapshot codexapp.Snapshot, opened bool, reveal bool) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if pending := m.codexPendingOpenProject(); pending != "" && pending == projectPath {
		m.codexPendingOpen = nil
	}
	if !opened {
		m.pruneCodexSessionVisibility()
		return nil
	}
	m.markCodexSessionLive(projectPath)
	m.codexHiddenProject = projectPath
	asyncCmd := tea.Cmd(nil)
	if snapshot.Started || strings.TrimSpace(snapshot.ThreadID) != "" || strings.TrimSpace(snapshot.Status) != "" || snapshot.Closed {
		m.storeCodexSnapshot(projectPath, snapshot)
	} else {
		_, _, needsAsync := m.refreshCodexSnapshot(projectPath)
		if needsAsync {
			asyncCmd = m.deferredCodexSnapshotCmd(projectPath)
		}
	}
	if reveal {
		m.codexVisibleProject = projectPath
		m.loadCodexDraft(projectPath)
		m.syncCodexViewport(true)
		m.syncCodexComposerSize()
	}
	return batchCmds(m.markProjectSessionSeen(projectPath), asyncCmd)
}

func (m *Model) pruneCodexSessionVisibility() {
	if projectPath := strings.TrimSpace(m.codexVisibleProject); projectPath != "" {
		if snapshot, ok := m.codexCachedSnapshot(projectPath); ok && snapshot.Closed {
			// Keep closed placeholders visible until another action replaces them.
		} else if _, ok := m.codexSession(projectPath); !ok {
			m.dropCodexSnapshot(projectPath)
			m.codexVisibleProject = ""
			m.codexInput.Blur()
			m.syncDetailViewport(false)
		}
	}
	if projectPath := strings.TrimSpace(m.codexHiddenProject); projectPath != "" {
		if snapshot, ok := m.codexCachedSnapshot(projectPath); ok && snapshot.Closed {
			// Keep closed placeholders restorable without consulting the manager.
		} else if _, ok := m.codexSession(projectPath); !ok {
			m.dropCodexSnapshot(projectPath)
			m.codexHiddenProject = ""
		}
	}
}

func (m Model) codexSession(projectPath string) (codexapp.Session, bool) {
	if m.codexManager == nil {
		return nil, false
	}
	return m.codexManager.Session(projectPath)
}

func (m Model) currentCodexSession() (codexapp.Session, bool) {
	return m.codexSession(m.codexVisibleProject)
}

func (m Model) codexSessionCmd(projectPath string, missing func() tea.Msg, run func(codexapp.Session) tea.Msg) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	manager := m.codexManager
	if manager == nil || projectPath == "" {
		return nil
	}
	return func() tea.Msg {
		session, ok := manager.Session(projectPath)
		if !ok {
			if missing != nil {
				return missing()
			}
			return codexActionMsg{projectPath: projectPath, err: errors.New("embedded session unavailable")}
		}
		return run(session)
	}
}

// codexCachedSnapshot returns the latest cached snapshot for UI/render paths
// without touching the live manager or session locks.
func (m Model) codexCachedSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := m.codexSnapshots[projectPath]
	if !ok {
		return codexapp.Snapshot{}, false
	}
	if strings.TrimSpace(snapshot.ProjectPath) == "" {
		snapshot.ProjectPath = projectPath
	}
	return snapshot, true
}

func (m Model) currentCachedCodexSnapshot() (codexapp.Snapshot, bool) {
	return m.codexCachedSnapshot(m.codexVisibleProject)
}

func (m Model) nonBlockingCodexSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	session, sessionOK := m.codexSession(projectPath)
	if sessionOK {
		if snapshot, got := session.TrySnapshot(); got {
			return snapshot, true
		}
		if snapshot, ok := m.codexCachedSnapshot(projectPath); ok {
			return snapshot, true
		}
		return codexapp.Snapshot{}, false
	}
	if snapshot, ok := m.codexCachedSnapshot(projectPath); ok && snapshot.Closed {
		return snapshot, true
	}
	return codexapp.Snapshot{}, false
}

func (m Model) currentCodexSnapshot() (codexapp.Snapshot, bool) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if snapshot, ok := m.currentCachedCodexSnapshot(); ok {
		return snapshot, true
	}
	session, sessionOK := m.currentCodexSession()
	if !sessionOK {
		if projectPath != "" {
			if snapshot, ok := m.codexCachedSnapshot(projectPath); ok && snapshot.Closed {
				return snapshot, true
			}
		}
		return codexapp.Snapshot{}, false
	}
	// Session exists but no cached snapshot yet. Use TrySnapshot to avoid
	// blocking the event loop when the session lock is contended. If the
	// lock is free we get a fresh snapshot; otherwise we return empty and
	// the next codexUpdateMsg will populate the cache.
	if snapshot, got := session.TrySnapshot(); got {
		return snapshot, true
	}
	return codexapp.Snapshot{}, false
}

// refreshCodexSnapshot attempts a non-blocking snapshot via TrySnapshot.
// If the session lock is contended it returns the cached snapshot and sets
// needsAsync=true so the caller can fire an async retry command.
func (m *Model) refreshCodexSnapshot(projectPath string) (snapshot codexapp.Snapshot, ok bool, needsAsync bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false, false
	}
	session, sessionOK := m.codexSession(projectPath)
	if !sessionOK {
		if snapshot, ok := m.codexSnapshots[projectPath]; ok && snapshot.Closed {
			return snapshot, true, false
		}
		m.dropCodexSnapshot(projectPath)
		return codexapp.Snapshot{}, false, false
	}
	if snap, got := session.TrySnapshot(); got {
		m.storeCodexSnapshot(projectPath, snap)
		return snap, true, false
	}
	// Lock contended — return the cached snapshot (if any) instead of blocking.
	if cached, ok := m.codexSnapshots[projectPath]; ok {
		return cached, true, true
	}
	return codexapp.Snapshot{}, false, true
}

// deferredCodexSnapshotCmd spawns a goroutine that takes a blocking
// session.Snapshot() off the main event loop. Used as a fallback when
// TrySnapshot reports lock contention.
func (m Model) deferredCodexSnapshotCmd(projectPath string) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return codexDeferredSnapshotMsg{
			projectPath: projectPath,
			snapshot:    session.Snapshot(),
		}
	}
}

func (m *Model) showEmbeddedOpenFailure(projectPath string, provider codexapp.Provider, err error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || err == nil {
		return
	}
	provider = provider.Normalized()
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	message := strings.TrimSpace(err.Error())
	snapshot := codexapp.Snapshot{
		Provider:         provider,
		ProjectPath:      projectPath,
		Closed:           true,
		Status:           provider.Label() + " session closed",
		LastError:        message,
		LastSystemNotice: message,
	}
	if message != "" {
		snapshot.Entries = []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptError,
			Text: message,
		}}
		snapshot.Transcript = provider.Label() + " error: " + message
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	m.markCodexSessionLive(projectPath)
	m.codexVisibleProject = projectPath
	m.codexHiddenProject = projectPath
	m.loadCodexDraft(projectPath)
	m.syncCodexViewport(true)
	m.syncCodexComposerSize()
}

func (m *Model) storeCodexSnapshot(projectPath string, snapshot codexapp.Snapshot) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexSnapshots == nil {
		m.codexSnapshots = make(map[string]codexapp.Snapshot)
	}
	if m.codexTranscriptRev == nil {
		m.codexTranscriptRev = make(map[string]uint64)
	}
	if prev, ok := m.codexSnapshots[projectPath]; !ok || codexTranscriptStateChanged(prev, snapshot) {
		m.codexTranscriptRev[projectPath]++
		m.resetCodexTranscriptCaches(projectPath)
	}
	m.codexSnapshots[projectPath] = snapshot
}

func (m *Model) stageEmbeddedModelSelectionInCache(projectPath string, provider codexapp.Provider, model, reasoning string) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := m.codexCachedSnapshot(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	provider = provider.Normalized()
	if provider == "" {
		provider = embeddedProvider(snapshot)
	}
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	snapshot.ProjectPath = projectPath
	snapshot.Provider = provider
	currentModel := strings.TrimSpace(snapshot.Model)
	currentReasoning := strings.TrimSpace(snapshot.ReasoningEffort)
	model = firstNonEmptyTrimmed(model, currentModel)
	reasoning = firstNonEmptyTrimmed(reasoning, currentReasoning)
	if strings.EqualFold(model, currentModel) && strings.EqualFold(reasoning, currentReasoning) {
		snapshot.PendingModel = ""
		snapshot.PendingReasoning = ""
	} else {
		snapshot.PendingModel = model
		snapshot.PendingReasoning = reasoning
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	return snapshot, true
}

func (m *Model) markCodexSkipNextLiveRefresh(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexSkipNextLiveRefresh == nil {
		m.codexSkipNextLiveRefresh = make(map[string]struct{})
	}
	m.codexSkipNextLiveRefresh[projectPath] = struct{}{}
}

func (m *Model) consumeCodexSkipNextLiveRefresh(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || len(m.codexSkipNextLiveRefresh) == 0 {
		return false
	}
	if _, ok := m.codexSkipNextLiveRefresh[projectPath]; !ok {
		return false
	}
	delete(m.codexSkipNextLiveRefresh, projectPath)
	return true
}

func (m *Model) dropCodexSnapshot(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if snapshot, ok := m.codexSnapshots[projectPath]; ok {
		m.removeManagedBrowserLease(projectPath, snapshot)
	}
	delete(m.codexSnapshots, projectPath)
	delete(m.codexTranscriptRev, projectPath)
	m.resetCodexTranscriptCaches(projectPath)
}

func (m *Model) resetCodexTranscriptCaches(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexTranscriptCache.projectPath == projectPath {
		m.codexTranscriptCache = codexTranscriptRenderCache{}
	}
	if m.codexViewportContent.projectPath == projectPath {
		m.codexViewportContent = codexViewportContentState{}
	}
}

func codexTranscriptStateChanged(prev, next codexapp.Snapshot) bool {
	if prev.Provider != next.Provider || prev.Closed != next.Closed {
		return true
	}
	if codexTranscriptFallsBackToNotice(prev) || codexTranscriptFallsBackToNotice(next) {
		if prev.LastSystemNotice != next.LastSystemNotice {
			return true
		}
	}
	if prev.TranscriptRevision != 0 || next.TranscriptRevision != 0 {
		return prev.TranscriptRevision != next.TranscriptRevision
	}
	if !codexTranscriptEntriesEqual(prev.Entries, next.Entries) {
		return true
	}
	if len(prev.Entries) == 0 && len(next.Entries) == 0 && prev.Transcript != next.Transcript {
		return true
	}
	return false
}

func codexTranscriptFallsBackToNotice(snapshot codexapp.Snapshot) bool {
	return !snapshot.Closed && len(snapshot.Entries) == 0 && strings.TrimSpace(snapshot.Transcript) == ""
}

func codexTranscriptEntriesEqual(left, right []codexapp.TranscriptEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ItemID != right[i].ItemID || left[i].Kind != right[i].Kind || left[i].Text != right[i].Text {
			return false
		}
	}
	return true
}

func (m Model) codexTranscriptRevision(projectPath string) uint64 {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return 0
	}
	return m.codexTranscriptRev[projectPath]
}

func (m Model) codexTranscriptCacheMatches(projectPath string, width int) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" &&
		m.codexTranscriptCache.projectPath == projectPath &&
		m.codexTranscriptCache.width == width &&
		m.codexTranscriptCache.denseExpanded == m.codexDenseExpanded &&
		m.codexTranscriptCache.transcriptRev == m.codexTranscriptRevision(projectPath) &&
		m.codexTranscriptCache.rendered != ""
}

func (m Model) cachedCodexTranscriptContent(projectPath string, width int) (string, bool) {
	if !m.codexTranscriptCacheMatches(projectPath, width) {
		return "", false
	}
	return m.codexTranscriptCache.rendered, true
}

func (m *Model) renderAndCacheCodexTranscript(projectPath string, snapshot codexapp.Snapshot, width int) string {
	rendered := ""
	m.measureAISyncLatency("Embedded transcript render", projectPath, embeddedProvider(snapshot).Label(), func() {
		rendered = m.renderCodexTranscriptContentFromSnapshot(snapshot, width)
	})
	m.codexTranscriptCache = codexTranscriptRenderCache{
		projectPath:   strings.TrimSpace(projectPath),
		width:         width,
		denseExpanded: m.codexDenseExpanded,
		transcriptRev: m.codexTranscriptRevision(projectPath),
		rendered:      rendered,
	}
	return rendered
}

func (m Model) codexViewportContentMatches(projectPath string, width int) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" &&
		m.codexViewportContent.projectPath == projectPath &&
		m.codexViewportContent.width == width &&
		m.codexViewportContent.denseExpanded == m.codexDenseExpanded &&
		m.codexViewportContent.transcriptRev == m.codexTranscriptRevision(projectPath)
}

func (m *Model) setCodexViewportTranscript(projectPath string, snapshot codexapp.Snapshot, width int) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	rendered, ok := m.cachedCodexTranscriptContent(projectPath, width)
	if !ok {
		rendered = m.renderAndCacheCodexTranscript(projectPath, snapshot, width)
	}
	m.measureAISyncLatency("Embedded viewport content", projectPath, embeddedProvider(snapshot).Label(), func() {
		m.codexViewport.SetContent(rendered)
	})
	m.codexViewportContent = codexViewportContentState{
		projectPath:   projectPath,
		width:         width,
		denseExpanded: m.codexDenseExpanded,
		transcriptRev: m.codexTranscriptRevision(projectPath),
	}
}

func (m Model) hasHiddenCodexSession() bool {
	return strings.TrimSpace(m.preferredHiddenCodexProject()) != ""
}

type codexBusyElsewhereRefresher interface {
	RefreshBusyElsewhere() error
}

type codexCloseWaiter interface {
	WaitClosed(timeout time.Duration) bool
}

func (m Model) refreshBusyElsewhereCmd(projectPath string) tea.Cmd {
	snapshot, ok := m.codexCachedSnapshot(projectPath)
	if !ok || snapshot.Closed || !snapshot.BusyExternal {
		return nil
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return nil
	}
	refresher, ok := session.(codexBusyElsewhereRefresher)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		_ = refresher.RefreshBusyElsewhere()
		return codexUpdateMsg{projectPath: projectPath}
	}
}

func (m *Model) openCodexSessionCmd(req codexapp.LaunchRequest) tea.Cmd {
	req = m.applyEmbeddedModelPreference(req)
	manager := m.codexManager
	provider := req.Provider.Normalized()
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	perfOpID := m.beginAILatencyOp("Embedded open", req.ProjectPath, provider.Label())
	previousThreadID := ""
	if manager != nil {
		preflightStarted := time.Now()
		if snapshot, ok := m.nonBlockingCodexSnapshot(req.ProjectPath); ok {
			previousThreadID = strings.TrimSpace(snapshot.ThreadID)
		}
		m.recordAISyncLatency("Embedded preflight", req.ProjectPath, provider.Label(), time.Since(preflightStarted), "")
	}
	threadIDsToAvoid := map[string]struct{}{}
	if previousThreadID != "" {
		threadIDsToAvoid[previousThreadID] = struct{}{}
	}
	resumeID := strings.TrimSpace(req.ResumeID)
	if resumeID != "" {
		threadIDsToAvoid[resumeID] = struct{}{}
	}
	return func() tea.Msg {
		startedAt := time.Now()
		label := provider.Label()
		if manager == nil {
			return codexSessionOpenedMsg{
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          fmt.Errorf("%s manager unavailable", label),
			}
		}
		attemptLimit := 1
		if req.ForceNew {
			attemptLimit = maxForceNewEmbeddedOpenAttempts
		}
		var (
			session  codexapp.Session
			reused   bool
			err      error
			snapshot codexapp.Snapshot
		)
		for attempt := 1; attempt <= attemptLimit; attempt++ {
			session, reused, err = manager.Open(req)
			if err != nil {
				if shouldRetryFreshEmbeddedOpenError(req, err) && attempt < attemptLimit {
					if reusedID := extractForceNewReusedThread(err); reusedID != "" {
						threadIDsToAvoid[reusedID] = struct{}{}
					}
					continue
				}
				return codexSessionOpenedMsg{
					projectPath:  req.ProjectPath,
					perfOpID:     perfOpID,
					perfDuration: time.Since(startedAt),
					err:          err,
				}
			}
			snapshot = session.Snapshot()
			// Codex can occasionally hand back the last thread on a forced-new
			// launch even though a second identical launch immediately fixes it.
			if shouldRetryFreshEmbeddedOpen(req, threadIDsToAvoid, snapshot) {
				if currentThreadID := strings.TrimSpace(snapshot.ThreadID); currentThreadID != "" {
					threadIDsToAvoid[currentThreadID] = struct{}{}
				}
				if attempt < attemptLimit {
					continue
				}
			}
			break
		}
		return codexSessionOpenedMsg{
			projectPath:  req.ProjectPath,
			snapshot:     snapshot,
			status:       embeddedSessionOpenStatus(req, threadIDsToAvoid, reused, snapshot),
			perfOpID:     perfOpID,
			perfDuration: time.Since(startedAt),
		}
	}
}

func extractForceNewReusedThread(err error) string {
	var reusedErr *codexapp.ForceNewSessionReusedError
	if errors.As(err, &reusedErr) {
		return strings.TrimSpace(reusedErr.ThreadID)
	}
	return ""
}

func shouldRetryFreshEmbeddedOpenError(req codexapp.LaunchRequest, err error) bool {
	if !req.ForceNew || err == nil {
		return false
	}
	var reusedErr *codexapp.ForceNewSessionReusedError
	return errors.As(err, &reusedErr)
}

func shouldRetryFreshEmbeddedOpen(req codexapp.LaunchRequest, threadIDsToAvoid map[string]struct{}, snapshot codexapp.Snapshot) bool {
	if !req.ForceNew {
		return false
	}
	return forceNewMatchedExistingThread(threadIDsToAvoid, snapshot) && !snapshot.BusyExternal
}

func forceNewMatchedExistingThread(threadIDsToAvoid map[string]struct{}, snapshot codexapp.Snapshot) bool {
	if threadIDsToAvoid == nil {
		return false
	}
	currentThreadID := strings.TrimSpace(snapshot.ThreadID)
	if currentThreadID == "" {
		return false
	}
	_, ok := threadIDsToAvoid[currentThreadID]
	return ok
}

func embeddedSessionOpenStatus(req codexapp.LaunchRequest, threadIDsToAvoid map[string]struct{}, reused bool, snapshot codexapp.Snapshot) string {
	provider := req.Provider.Normalized()
	if provider == "" {
		provider = embeddedProvider(snapshot)
	}
	label := provider.Label()
	currentThreadID := strings.TrimSpace(snapshot.ThreadID)
	matchedExisting := forceNewMatchedExistingThread(threadIDsToAvoid, snapshot)
	sessionLabel := "another session"
	if short := shortID(currentThreadID); short != "" {
		sessionLabel = "session " + short
	}
	freshSessionLabel := "fresh embedded " + label + " " + sessionLabel

	switch {
	case req.ForceNew && snapshot.BusyExternal && matchedExisting:
		return "Could not start a fresh embedded " + label + " session because " + sessionLabel + " is already active in another process. Showing that session read-only instead."
	case req.ForceNew && snapshot.BusyExternal:
		return "Could not start a fresh embedded " + label + " session because " + sessionLabel + " is already active in another process. Embedded view is read-only until it finishes."
	case req.ForceNew && matchedExisting:
		return "Could not start a fresh embedded " + label + " session; " + sessionLabel + " reopened instead."
	case req.ForceNew && strings.TrimSpace(req.Prompt) != "":
		return "Prompt sent to " + freshSessionLabel + ". Alt+Up hides it."
	case req.ForceNew:
		return "Fresh embedded " + label + " " + sessionLabel + " opened. Alt+Up hides it."
	case snapshot.BusyExternal && strings.TrimSpace(req.Prompt) != "":
		if cmd := embeddedNewCommand(provider); cmd != "" {
			return label + " is already active in another process. Prompt was not sent; use " + cmd + " for a separate session."
		}
		return label + " is already active in another process. Prompt was not sent; this is a read-only view."
	case snapshot.BusyExternal:
		return label + " is already active in another process. Embedded view is read-only until it finishes."
	case strings.TrimSpace(req.Prompt) != "":
		return "Prompt sent to embedded " + label + ". Alt+Up hides it."
	case reused:
		return "Embedded " + label + " session reopened. Alt+Up hides it."
	default:
		return "Embedded " + label + " session opened. Alt+Up hides it."
	}
}

func embeddedSessionReconnectStatus(req codexapp.LaunchRequest, snapshot codexapp.Snapshot) string {
	provider := req.Provider.Normalized()
	if provider == "" {
		provider = embeddedProvider(snapshot)
	}
	label := provider.Label()
	sessionLabel := ""
	if short := shortID(strings.TrimSpace(snapshot.ThreadID)); short != "" {
		sessionLabel = " " + short
	}
	if snapshot.BusyExternal {
		return "Reconnected embedded " + label + " session" + sessionLabel + ". It is already active in another process, so the embedded view is read-only until it finishes."
	}
	return "Reconnected embedded " + label + " session" + sessionLabel + ". Alt+Up hides it."
}

func (m Model) submitVisibleCodexCmd(draft codexDraft) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	submission := draft.Submission()
	steer := false
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		steer = codexSnapshotCanSteer(snapshot)
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, func() tea.Msg {
		return codexActionMsg{projectPath: projectPath, restoreDraft: draft, err: errors.New("embedded session unavailable")}
	}, func(session codexapp.Session) tea.Msg {
		if err := session.SubmitInput(submission); err != nil {
			return codexActionMsg{projectPath: projectPath, restoreDraft: draft, err: err}
		}
		status := "Prompt sent to " + label
		if steer {
			status = "Steer sent to " + label
		}
		return codexActionMsg{projectPath: projectPath, status: status}
	})
}

func (m Model) showVisibleCodexStatusCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.ShowStatus(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " status added to the transcript"}
	})
}

func (m Model) restartVisibleCodexSessionCmd(prompt string) tea.Cmd {
	if m.codexManager == nil || strings.TrimSpace(m.codexVisibleProject) == "" {
		return nil
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	req := codexapp.LaunchRequest{
		Provider:         codexapp.ProviderCodex,
		ProjectPath:      projectPath,
		ForceNew:         true,
		Prompt:           prompt,
		PlaywrightPolicy: m.currentPlaywrightPolicy(),
		AppDataDir:       m.appDataDir(),
		CodexHome:        m.codexHome(),
	}
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		req.Provider = embeddedProvider(snapshot)
		req.Preset = snapshot.Preset
	}
	if req.Provider.Normalized() == codexapp.ProviderCodex && req.Preset == "" {
		req.Preset = codexcli.DefaultPreset()
	}
	if err := req.Validate(); err != nil {
		return func() tea.Msg {
			return codexSessionOpenedMsg{projectPath: projectPath, err: err}
		}
	}
	return m.openCodexSessionCmd(req)
}

func (m Model) compactVisibleCodexSessionCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.Compact(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " conversation compaction completed"}
	})
}

func (m Model) reconnectVisibleCodexSessionCmd() tea.Cmd {
	if m.codexManager == nil || strings.TrimSpace(m.codexVisibleProject) == "" {
		return nil
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	req := codexapp.LaunchRequest{
		Provider:         codexapp.ProviderCodex,
		ProjectPath:      projectPath,
		PlaywrightPolicy: m.currentPlaywrightPolicy(),
		AppDataDir:       m.appDataDir(),
		CodexHome:        m.codexHome(),
	}
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		req.Provider = embeddedProvider(snapshot)
		req.ResumeID = strings.TrimSpace(snapshot.ThreadID)
		req.Preset = snapshot.Preset
	}
	if req.Provider.Normalized() == codexapp.ProviderCodex && req.Preset == "" {
		req.Preset = codexcli.DefaultPreset()
	}
	if err := req.Validate(); err != nil {
		return func() tea.Msg {
			return codexSessionOpenedMsg{projectPath: projectPath, err: err}
		}
	}
	manager := m.codexManager
	return func() tea.Msg {
		if existing, ok := manager.Session(projectPath); ok {
			_ = manager.CloseProject(projectPath)
			if waiter, ok := existing.(codexCloseWaiter); ok {
				waiter.WaitClosed(5 * time.Second)
			}
		}
		session, _, err := manager.Open(req)
		if err != nil {
			return codexSessionOpenedMsg{projectPath: projectPath, err: err}
		}
		snapshot := session.Snapshot()
		return codexSessionOpenedMsg{
			projectPath: projectPath,
			snapshot:    snapshot,
			status:      embeddedSessionReconnectStatus(req, snapshot),
		}
	}
}

func (m Model) interruptVisibleCodexCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.Interrupt(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Interrupt sent to " + label}
	})
}

func (m Model) closeVisibleCodexCmd() tea.Cmd {
	if m.codexManager == nil || strings.TrimSpace(m.codexVisibleProject) == "" {
		return nil
	}
	projectPath := m.codexVisibleProject
	manager := m.codexManager
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return func() tea.Msg {
		if err := manager.CloseProject(projectPath); err != nil {
			return codexActionMsg{err: err}
		}
		return codexActionMsg{
			projectPath: projectPath,
			status:      "Embedded " + label + " session closed",
			closed:      true,
		}
	}
}

func (m Model) respondVisibleApprovalCmd(decision codexapp.ApprovalDecision) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.RespondApproval(decision); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Approval decision sent to " + label}
	})
}

func (m Model) respondVisibleToolInputCmd(answers map[string][]string) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.RespondToolInput(answers); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Structured input sent to " + label}
	})
}

func (m Model) respondVisibleElicitationCmd(decision codexapp.ElicitationDecision, content json.RawMessage, restoreDraft codexDraft) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, func() tea.Msg {
		return codexActionMsg{projectPath: projectPath, restoreDraft: restoreDraft, err: errors.New("embedded session unavailable")}
	}, func(session codexapp.Session) tea.Msg {
		if err := session.RespondElicitation(decision, content); err != nil {
			return codexActionMsg{projectPath: projectPath, restoreDraft: restoreDraft, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "MCP input sent to " + label}
	})
}

func (m Model) toggleCodexVisibility() (tea.Model, tea.Cmd) {
	m.ensureCodexRuntime()
	if m.codexVisible() {
		return m.hideCodexSession()
	}
	projectPath := m.preferredHiddenCodexProject()
	if projectPath == "" {
		m.status = "No hidden embedded session"
		return m, nil
	}
	return m.showCodexProject(projectPath, "Embedded session restored")
}

func (m Model) hideCodexSession() (tea.Model, tea.Cmd) {
	if !m.codexVisible() {
		return m, nil
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	label := "Embedded session"
	refreshCmd := tea.Cmd(nil)
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		snapshot, ok = m.codexCachedSnapshot(projectPath)
	}
	if ok {
		label = "Embedded " + embeddedProvider(snapshot).Label() + " session"
		if !snapshot.Busy {
			// Returning to the dashboard after an idle embedded turn should
			// refresh git metadata, not just re-render the last stored state.
			refreshCmd = m.refreshProjectStatusCmd(projectPath)
		}
	}
	m.persistVisibleCodexDraft()
	m.stopCodexInputSelection()
	m.codexHiddenProject = m.codexVisibleProject
	m.codexVisibleProject = ""
	m.codexInput.Blur()
	m.syncDetailViewport(false)
	m.status = label + " hidden."
	return m, batchCmds(
		m.focusProjectPath(projectPath),
		m.markProjectSessionSeen(projectPath),
		refreshCmd,
	)
}

func (m Model) cycleCodexSession(direction int) (tea.Model, tea.Cmd) {
	m.ensureCodexRuntime()
	nextProject := m.nextLiveCodexProject()
	if direction < 0 {
		nextProject = m.previousLiveCodexProject()
	}
	if nextProject == "" {
		m.status = "No live embedded sessions"
		return m, nil
	}
	current := strings.TrimSpace(m.codexVisibleProject)
	if current != "" && nextProject == current && len(m.liveCodexProjects()) == 1 {
		m.status = "Only one live embedded session"
		return m, nil
	}
	label := "embedded session"
	if snapshot, ok := m.liveCodexSnapshot(nextProject); ok {
		label = "embedded " + embeddedProvider(snapshot).Label() + " session"
	}
	if direction < 0 {
		return m.showCodexProject(nextProject, "Switched to the previous "+label)
	}
	return m.showCodexProject(nextProject, "Switched to the next "+label)
}

func (m Model) updateCodexMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if projectPath := m.codexPendingOpenProject(); projectPath != "" {
		label := m.codexPendingOpenProvider().Label()
		switch msg.String() {
		case "esc", "alt+up":
			m.status = "Embedded " + label + " is still starting for " + filepath.Base(projectPath)
		default:
			m.status = "Embedded " + label + " session is still starting..."
		}
		return m, nil
	}

	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		m.codexVisibleProject = ""
		m.status = "Embedded session is no longer available"
		return m, nil
	}
	label := embeddedProvider(snapshot).Label()

	switch msg.String() {
	case "f3":
		return m.cycleCodexSession(1)
	case "alt+down":
		return m.openCodexPicker()
	case "esc":
		return m.hideCodexSession()
	case "alt+up":
		return m.hideCodexSession()
	case "alt+[":
		return m.cycleCodexSession(-1)
	case "alt+]":
		return m.cycleCodexSession(1)
	case "alt+l":
		m.codexDenseExpanded = !m.codexDenseExpanded
		if m.codexDenseExpanded {
			m.status = "Expanded dense transcript blocks"
		} else {
			m.status = "Collapsed dense transcript blocks"
		}
		m.syncCodexViewport(false)
		return m, nil
	case "ctrl+c":
		if snapshot.BusyExternal {
			m.status = "This " + label + " session is busy in another process. Interrupt it there or hide it here with Alt+Up."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling && codexStatusIsCompacting(snapshot.Status) {
			m.status = label + " is compacting conversation history. Wait for it to finish or hide it with Alt+Up."
			return m, nil
		}
		if codexSnapshotCanSteer(snapshot) {
			m.status = "Interrupting " + label + " turn..."
			return m, m.interruptVisibleCodexCmd()
		}
		if snapshot.Phase == codexapp.SessionPhaseStalled && snapshot.Busy && strings.TrimSpace(snapshot.ActiveTurnID) != "" {
			m.status = "Interrupting stuck " + label + " turn..."
			return m, m.interruptVisibleCodexCmd()
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = label + " is finishing the current turn. Wait for the final output to settle or hide it with Alt+Up."
			return m, nil
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			if codexStatusIsCompacting(snapshot.Status) {
				m.status = label + " is compacting conversation history. Wait for it to finish before sending another prompt."
				return m, nil
			}
			m.status = label + " is rechecking whether the current turn has gone idle. Please wait a moment."
			return m, nil
		}
		m.status = "Closing embedded " + label + " session..."
		return m, m.closeVisibleCodexCmd()
	case "pgup", "ctrl+u":
		m.codexViewport.HalfPageUp()
		return m, nil
	case "pgdown", "ctrl+d":
		m.codexViewport.HalfPageDown()
		return m, nil
	}

	if snapshot.PendingApproval != nil {
		switch msg.String() {
		case "a":
			m.status = "Approving " + label + " request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAccept)
		case "A":
			if !snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
				m.status = "This approval cannot be accepted for the whole session"
				return m, nil
			}
			m.status = "Approving " + label + " request for this session..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionAcceptForSession)
		case "d":
			m.status = "Declining " + label + " request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionDecline)
		case "c":
			m.status = "Canceling " + label + " request..."
			return m, m.respondVisibleApprovalCmd(codexapp.DecisionCancel)
		default:
			return m, nil
		}
	}

	if snapshot.PendingToolInput != nil {
		return m.updateCodexToolInputMode(snapshot, msg)
	}

	if snapshot.PendingElicitation != nil {
		return m.updateCodexElicitationMode(snapshot, msg)
	}

	// Clear mouse selection on any keypress.
	m.codexComposerSelection = textSelection{}

	if m.codexInputSelectionActive() {
		return m.updateCodexInputSelectionMode(msg)
	}

	if m.codexSlashActive() {
		switch msg.String() {
		case "tab":
			if m.cycleAndApplyCodexSlashSuggestion(1) {
				return m, nil
			}
		case "shift+tab":
			if m.cycleAndApplyCodexSlashSuggestion(-1) {
				return m, nil
			}
		case "enter":
			raw := m.resolvedCodexSlashInput()
			inv, err := codexslash.Parse(raw)
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.clearCodexDraft(m.codexVisibleProject)
			if snapshot.Closed && (inv.Kind == codexslash.KindModel || inv.Kind == codexslash.KindStatus || inv.Kind == codexslash.KindCompact) {
				m.status = label + " session is closed. Use /resume, /new, or /reconnect to reopen it."
				return m, nil
			}
			switch inv.Kind {
			case codexslash.KindNew:
				m.status = "Starting a fresh embedded " + label + " session..."
				m.beginCodexPendingOpen(m.codexVisibleProject, embeddedProvider(snapshot))
				return m, m.restartVisibleCodexSessionCmd(inv.Prompt)
			case codexslash.KindResume:
				if strings.TrimSpace(inv.SessionID) == "" {
					return m.openVisibleCodexResumePicker()
				}
				return m.openCodexSessionChoice(codexSessionChoice{
					ProjectPath: m.codexVisibleProject,
					ProjectName: projectNameForPicker(m.pickerProjectSummary(m.codexVisibleProject), m.codexVisibleProject),
					SessionID:   inv.SessionID,
					Provider:    embeddedProvider(snapshot),
				})
			case codexslash.KindReconnect:
				m.status = "Reconnecting embedded " + label + " session..."
				m.beginCodexPendingOpen(m.codexVisibleProject, embeddedProvider(snapshot))
				return m, m.reconnectVisibleCodexSessionCmd()
			case codexslash.KindModel:
				m.openCodexModelPickerLoading()
				m.status = "Loading embedded " + label + " models..."
				return m, m.openCodexModelPickerCmd()
			case codexslash.KindStatus:
				m.status = "Reading embedded " + label + " status..."
				return m, m.showVisibleCodexStatusCmd()
			case codexslash.KindCompact:
				m.status = "Starting embedded " + label + " conversation compaction..."
				return m, m.compactVisibleCodexSessionCmd()
			default:
				m.status = "Unsupported embedded slash command"
				return m, nil
			}
		}
	}

	if snapshot.Closed {
		if msg.String() == "enter" {
			m.status = label + " session is closed. Use /resume, /new, /reconnect, or reopen it from the project list."
		}
		return m, nil
	}

	if handled, cmd := m.tryHandleCodexPaste(msg, true); handled {
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+o":
		pageURL := managedBrowserCurrentPageURL(snapshot)
		sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
		if pageURL == "" || sessionKey == "" || snapshot.BusyExternal || snapshot.Closed || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
			if status := m.codexBrowserReconnectStatus(snapshot); status != "" {
				m.status = status
			}
			return m, nil
		}
		m.status = "Showing the managed browser window..."
		return m, m.revealManagedBrowserCmd(
			sessionKey,
			managedBrowserLeaseRef(embeddedProvider(snapshot), firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject), snapshot.ThreadID),
			"Managed browser window is ready. Continue there, then return here when you want Codex to keep going.",
		)
	case "enter":
		draft := m.currentCodexDraft()
		if draft.Empty() {
			return m, nil
		}
		refreshCmd := tea.Cmd(nil)
		if refreshed, ok, needsAsync := m.refreshCodexSubmitSnapshot(m.codexVisibleProject, snapshot); ok {
			snapshot = refreshed
			if needsAsync {
				refreshCmd = m.deferredCodexSnapshotCmd(m.codexVisibleProject)
			}
		} else if needsAsync {
			refreshCmd = m.deferredCodexSnapshotCmd(m.codexVisibleProject)
		}
		if snapshot.BusyExternal {
			if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
				m.status = "This " + label + " session is already active in another process, so the embedded view cannot steer it. Use " + cmd + " for a separate session."
			} else {
				m.status = "This " + label + " session is read-only."
			}
			return m, refreshCmd
		}
		if snapshot.Phase == codexapp.SessionPhaseFinishing {
			m.status = label + " is finishing the current turn. Wait for it to settle before sending another prompt."
			return m, refreshCmd
		}
		if snapshot.Phase == codexapp.SessionPhaseReconciling {
			if codexStatusIsCompacting(snapshot.Status) {
				m.status = label + " is compacting conversation history. Wait for it to finish before sending another prompt."
				return m, refreshCmd
			}
			m.status = label + " is rechecking the current turn state. Wait for that to finish before sending another prompt."
			return m, refreshCmd
		}
		if snapshot.Phase == codexapp.SessionPhaseStalled {
			m.status = label + " looks stuck or disconnected. Interrupt the current turn with Ctrl+C or use /reconnect before sending another prompt."
			return m, refreshCmd
		}
		m.clearCodexDraft(m.codexVisibleProject)
		if codexSnapshotCanSteer(snapshot) {
			m.status = "Sending follow-up to " + label + "..."
		} else {
			m.status = "Sending prompt to " + label + "..."
		}
		return m, batchCmds(refreshCmd, m.submitVisibleCodexCmd(draft))
	case "alt+enter", "ctrl+j":
		m.codexInput.InsertString("\n")
		m.persistVisibleCodexDraft()
		m.syncCodexComposerSize()
		return m, nil
	case "alt+s":
		m.startCodexInputSelection()
		return m, nil
	case "ctrl+v":
		return m, nil
	case "backspace", "delete":
		if m.removeCodexPastedTextMarkerBeforeCursor() {
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		if m.removeCodexAttachmentMarkerBeforeCursor() {
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		if strings.TrimSpace(m.codexInput.Value()) == "" && m.removeLastCurrentCodexAttachment() {
			m.status = "Removed the last image attachment"
			return m, nil
		}
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return m, cmd
}

func (m Model) updateCodexToolInputMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	request := snapshot.PendingToolInput
	if request == nil {
		return m, nil
	}
	state := m.ensureToolAnswerState(m.codexVisibleProject, request)
	if len(request.Questions) == 0 {
		return m, nil
	}
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	question := request.Questions[state.QuestionIndex]

	if handled, cmd := m.tryHandleCodexPaste(msg, false); handled {
		return m, cmd
	}

	switch msg.String() {
	case "tab":
		if len(request.Questions) > 1 {
			state.QuestionIndex = (state.QuestionIndex + 1) % len(request.Questions)
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.status = "Moved to the next structured question"
		}
		return m, nil
	case "shift+tab":
		if len(request.Questions) > 1 {
			state.QuestionIndex = (state.QuestionIndex - 1 + len(request.Questions)) % len(request.Questions)
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.status = "Moved to the previous structured question"
		}
		return m, nil
	case "enter":
		answer := strings.TrimSpace(m.codexInput.Value())
		if answer == "" {
			return m, nil
		}
		state.Answers[question.ID] = []string{answer}
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.clearCodexDraft(m.codexVisibleProject)
		return m.finishOrAdvanceToolInput(request, state)
	case "backspace", "delete":
		// Keep textarea editing behavior below.
	default:
		if optionIndex, ok := numericOptionSelection(msg.String()); ok && optionIndex < len(question.Options) {
			state.Answers[question.ID] = []string{question.Options[optionIndex].Label}
			m.codexToolAnswers[m.codexVisibleProject] = state
			m.clearCodexDraft(m.codexVisibleProject)
			return m.finishOrAdvanceToolInput(request, state)
		}
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	return m, cmd
}

func (m Model) finishOrAdvanceToolInput(request *codexapp.ToolInputRequest, state codexToolAnswerState) (tea.Model, tea.Cmd) {
	nextIndex := firstUnansweredToolQuestion(request, state.Answers)
	m.codexToolAnswers[m.codexVisibleProject] = state
	if nextIndex < len(request.Questions) {
		state.QuestionIndex = nextIndex
		m.codexToolAnswers[m.codexVisibleProject] = state
		m.status = "Answer recorded. Continue with the next structured question."
		return m, m.codexInput.Focus()
	}
	delete(m.codexToolAnswers, m.codexVisibleProject)
	m.status = "Sending structured input..."
	return m, m.respondVisibleToolInputCmd(state.Answers)
}

func (m Model) updateCodexElicitationMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	request := snapshot.PendingElicitation
	if request == nil {
		return m, nil
	}
	loginURL := managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL)

	if request.Mode == codexapp.ElicitationModeForm {
		if handled, cmd := m.tryHandleCodexPaste(msg, false); handled {
			return m, cmd
		}
	}

	switch msg.String() {
	case "o":
		if loginURL == "" || strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
			if status := m.codexBrowserReconnectStatus(snapshot); status != "" {
				m.status = status
			}
			return m, nil
		}
		return m.openManagedBrowserLogin(
			firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject),
			embeddedProvider(snapshot),
			snapshot.ThreadID,
			snapshot.ManagedBrowserSessionKey,
			snapshot.BrowserActivity,
			loginURL,
			"Showing the managed browser window...",
			"Managed browser window is ready. Finish the browser flow there, then press Enter when you are ready to continue.",
		)
	case "d":
		m.status = "Declining MCP input request..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationDecline, nil, codexDraft{})
	case "c":
		m.status = "Canceling MCP input request..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationCancel, nil, codexDraft{})
	case "a", "enter":
		restore := m.currentCodexDraft()
		var content json.RawMessage
		if request.Mode == codexapp.ElicitationModeForm {
			content = encodeElicitationComposerInput(m.codexInput.Value())
			m.clearCodexDraft(m.codexVisibleProject)
		}
		m.status = "Sending MCP input..."
		return m, m.respondVisibleElicitationCmd(codexapp.ElicitationAccept, content, restore)
	case "alt+enter", "ctrl+j":
		if request.Mode == codexapp.ElicitationModeForm {
			m.codexInput.InsertString("\n")
			m.persistVisibleCodexDraft()
			m.syncCodexComposerSize()
			return m, nil
		}
		return m, nil
	}

	if request.Mode != codexapp.ElicitationModeForm {
		return m, nil
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	return m, cmd
}

func (m *Model) tryHandleCodexPaste(msg tea.KeyMsg, allowImage bool) (bool, tea.Cmd) {
	switch {
	case msg.Paste:
		text := string(msg.Runes)
		if !shouldCollapseCodexPaste(text) {
			return false, nil
		}
		m.insertCodexPastedText(text)
		return true, nil
	case msg.Type != tea.KeyCtrlV:
		return false, nil
	}

	if allowImage {
		attached, err := m.tryAttachClipboardImage()
		if err != nil {
			m.status = err.Error()
			return true, nil
		}
		if attached {
			return true, nil
		}
	}

	text, err := clipboardTextReader()
	if err != nil {
		m.reportError("Clipboard paste failed", err, m.codexVisibleProject)
		return true, nil
	}
	if text == "" {
		return true, nil
	}
	if shouldCollapseCodexPaste(text) {
		m.insertCodexPastedText(text)
		return true, nil
	}
	m.codexInput.InsertString(text)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	return true, nil
}

func (m *Model) tryAttachClipboardImage() (bool, error) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return false, nil
	}
	path, err := clipboardImageExporter()
	if err != nil {
		if err == errClipboardHasNoImage {
			return false, nil
		}
		return false, err
	}
	attachment := codexapp.Attachment{
		Kind: codexapp.AttachmentLocalImage,
		Path: path,
	}
	m.appendCurrentCodexAttachment(attachment)
	attachments := m.currentCodexAttachments()
	index := len(attachments) - 1
	m.insertCodexAttachmentMarker(index, attachment)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.status = "Attached " + codexAttachmentComposerToken(index, attachment)
	return true, nil
}

func (m *Model) insertCodexPastedText(text string) {
	if strings.TrimSpace(m.codexVisibleProject) == "" {
		return
	}
	token := m.nextCodexPastedTextToken(text)
	m.insertCodexComposerToken(token)
	pasted := codexPastedText{
		Token: token,
		Text:  text,
	}
	m.appendCurrentCodexPastedText(pasted)
	m.persistVisibleCodexDraft()
	m.syncCodexComposerSize()
	m.syncCodexSlashSelection()
	m.status = "Pasted " + codexPastedTextPlaceholder(text) + " as a placeholder"
}

func (m *Model) insertCodexAttachmentMarker(index int, attachment codexapp.Attachment) {
	m.insertCodexComposerToken(codexAttachmentComposerToken(index, attachment))
}

func (m *Model) insertCodexComposerToken(token string) {
	valueRunes := []rune(m.codexInput.Value())
	cursor := codexTextareaCursorOffset(m.codexInput)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(valueRunes) {
		cursor = len(valueRunes)
	}
	insert := token
	if cursor > 0 && !unicode.IsSpace(valueRunes[cursor-1]) {
		insert = " " + insert
	}
	if cursor == len(valueRunes) {
		if len(valueRunes) == 0 || !unicode.IsSpace(valueRunes[len(valueRunes)-1]) {
			insert += " "
		}
	} else if !unicode.IsSpace(valueRunes[cursor]) {
		insert += " "
	}
	m.codexInput.InsertString(insert)
}

func (m *Model) removeCodexPastedTextMarkerBeforeCursor() bool {
	pastedTexts := m.currentCodexPastedTexts()
	if len(pastedTexts) == 0 {
		return false
	}
	value := m.codexInput.Value()
	cursor := codexTextareaCursorOffset(m.codexInput)
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	prefix := string(runes[:cursor])
	for i := len(pastedTexts) - 1; i >= 0; i-- {
		token := pastedTexts[i].Token
		switch {
		case strings.HasSuffix(prefix, token+" "):
			start := cursor - len([]rune(token)) - 1
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexPastedTextByToken(token)
			m.status = "Removed " + codexPastedTextPlaceholder(pastedTexts[i].Text) + " placeholder"
			return true
		case strings.HasSuffix(prefix, token):
			start := cursor - len([]rune(token))
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexPastedTextByToken(token)
			m.status = "Removed " + codexPastedTextPlaceholder(pastedTexts[i].Text) + " placeholder"
			return true
		}
	}
	return false
}

func (m *Model) removeCodexAttachmentMarkerBeforeCursor() bool {
	attachments := m.currentCodexAttachments()
	if len(attachments) == 0 {
		return false
	}
	value := m.codexInput.Value()
	cursor := codexTextareaCursorOffset(m.codexInput)
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	prefix := string(runes[:cursor])
	for i := len(attachments) - 1; i >= 0; i-- {
		token := codexAttachmentComposerToken(i, attachments[i])
		switch {
		case strings.HasSuffix(prefix, token+" "):
			start := cursor - len([]rune(token)) - 1
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexAttachment(i)
			m.status = "Removed " + token
			return true
		case strings.HasSuffix(prefix, token):
			start := cursor - len([]rune(token))
			m.setCodexComposerValue(string(runes[:start])+string(runes[cursor:]), start)
			m.removeCurrentCodexAttachment(i)
			m.status = "Removed " + token
			return true
		}
	}
	return false
}

func (m *Model) syncCodexComposerSize() {
	width := m.width
	if width <= 0 {
		width = 120
	}
	m.codexInput.SetWidth(max(20, width-4))

	maxHeight := 8
	if m.height > 0 {
		maxHeight = max(3, min(10, m.height/3))
	}
	targetHeight := max(3, min(maxHeight, m.codexInput.LineCount()+1))
	m.codexInput.SetHeight(targetHeight)
}

func (m *Model) syncCodexViewport(resetToBottom bool) {
	done := m.beginUIPhase("syncCodexViewport", strings.TrimSpace(m.codexVisibleProject), fmt.Sprintf("reset=%t", resetToBottom))
	defer done()
	if !m.codexVisible() {
		return
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return
	}
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	projectPath := strings.TrimSpace(m.codexVisibleProject)
	providerLabel := embeddedProvider(snapshot).Label()
	lowerBlocks := []string{}
	m.measureAISyncLatency("Embedded lower blocks", projectPath, providerLabel, func() {
		lowerBlocks = m.codexLowerBlocks(snapshot, width)
	})
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	m.codexViewport.Width = max(24, width)
	m.codexViewport.Height = max(1, transcriptHeight)

	offset := m.codexViewport.YOffset
	if !m.codexViewportContentMatches(projectPath, m.codexViewport.Width) {
		m.measureAISyncLatency("Embedded viewport sync", projectPath, providerLabel, func() {
			m.setCodexViewportTranscript(projectPath, snapshot, m.codexViewport.Width)
		})
	}
	if resetToBottom {
		m.codexViewport.GotoBottom()
		return
	}
	maxOffset := max(0, m.codexViewport.TotalLineCount()-m.codexViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.codexViewport.SetYOffset(offset)
}

func (m Model) renderCodexView() string {
	done := m.beginUIPhase("renderCodexView", strings.TrimSpace(m.codexVisibleProject), "")
	defer done()
	if projectPath := m.codexPendingOpenProject(); projectPath != "" {
		return m.renderCodexOpeningView(projectPath)
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return lipgloss.NewStyle().Bold(true).Render(brand.FullTitle + " | Embedded session unavailable")
	}

	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	lowerBlocks := m.codexLowerBlocks(snapshot, width)
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	transcript := m.codexViewport
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	transcript.Width = max(24, width)
	transcript.Height = max(1, transcriptHeight)
	switch {
	case m.codexViewportContentMatches(projectPath, transcript.Width):
		maxOffset := max(0, transcript.TotalLineCount()-transcript.Height)
		if transcript.YOffset > maxOffset {
			transcript.SetYOffset(maxOffset)
		}
	case func() bool {
		rendered, ok := m.cachedCodexTranscriptContent(projectPath, transcript.Width)
		if !ok {
			return false
		}
		transcript.SetContent(rendered)
		return true
	}():
	default:
		transcript.SetContent(m.renderCodexTranscriptContentFromSnapshot(snapshot, transcript.Width))
	}
	viewOutput := transcript.View()
	if m.codexSelection.dragging && m.codexSelection.hasRange() {
		viewOutput = overlaySelectionHighlight(viewOutput, m.codexSelection, transcript.YOffset)
	}
	body := m.renderHFramedPane(viewOutput, width, transcriptHeight, true)

	lines := []string{m.renderCodexBanner(snapshot, width), body}
	lines = append(lines, lowerBlocks...)
	return strings.Join(lines, "\n")
}

func codexTranscriptContentHeight(totalHeight, lowerHeight int) int {
	return max(3, totalHeight-lowerHeight-3)
}

func (m Model) renderCodexOpeningView(projectPath string) string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	projectName := strings.TrimSpace(filepath.Base(projectPath))
	if projectName == "" || projectName == "." {
		projectName = projectPath
	}
	label := m.codexPendingOpenProvider().Label()
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(label + " | " + projectName)
	bodyHeight := max(3, height-6)
	body := m.renderHFramedPane(strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render("Opening embedded " + label + " session..."),
		"",
		fitFooterWidth("Project: "+projectPath, max(24, width-4)),
		fitFooterWidth("Waiting for the previous embedded session to settle and for the new session to come online.", max(24, width-4)),
	}, "\n"), width, bodyHeight, true)
	footer := renderFooterLine(width, renderFooterStatus("Opening embedded "+label+" session"))
	return strings.Join([]string{renderFooterLine(width, title), body, footer}, "\n")
}

func (m Model) codexLowerBlocks(snapshot codexapp.Snapshot, width int) []string {
	label := embeddedProvider(snapshot).Label()
	switch {
	case snapshot.PendingApproval != nil:
		approvalActions := []footerAction{
			footerPrimaryAction("a", "accept"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerHideAction("Alt+Up", "hide"),
		}
		if snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
			approvalActions = []footerAction{
				footerPrimaryAction("a", "accept"),
				footerNavAction("A", "session"),
				footerExitAction("d", "decline"),
				footerLowAction("c", "cancel"),
				footerHideAction("Alt+Up", "hide"),
			}
		}
		lines := []string{}
		if meta := m.renderCodexSessionMeta(snapshot, width); meta != "" {
			lines = append(lines, meta)
		}
		lines = append(lines,
			fitFooterWidth("Approval: "+snapshot.PendingApproval.Summary(), width),
			renderFooterLine(width, renderFooterActionList(approvalActions...)),
		)
		return lines
	case snapshot.Closed:
		return []string{
			fitFooterWidth(label+" session closed. Esc or Alt+Up hides it; Enter on the project opens a new one.", width),
			"",
		}
	default:
		lines := []string{}
		if meta := m.renderCodexSessionMeta(snapshot, width); meta != "" {
			lines = append(lines, meta)
		}
		if snapshot.BusyExternal {
			lines = append(lines, m.renderCodexBusyElsewhereNotice(snapshot, width))
		}
		lines = append(lines, m.renderCodexFooter(snapshot, width))
		lines = append(lines, m.renderCodexRequestBlocks(snapshot, width)...)
		if browser := m.renderCodexBrowserPanel(snapshot, width); browser != "" {
			lines = append(lines, browser)
		}
		lines = append(lines, m.renderCodexSlashBlocks(width)...)
		input := m.codexInput
		input.SetWidth(max(20, width-4))
		input.SetHeight(max(3, min(10, m.codexInput.LineCount()+1)))
		if m.codexInputSelectionActive() {
			lines = append(lines, renderCodexComposerWithSelection(input, m.codexInputSelection, width))
		} else if m.codexComposerSelection.dragging && m.codexComposerSelection.hasRange() {
			lines = append(lines, renderCodexComposerWithMouseSelection(input, m.codexComposerSelection, width))
		} else {
			lines = append(lines, renderCodexComposer(input, width))
		}
		return lines
	}
}

func (m Model) renderCodexRequestBlocks(snapshot codexapp.Snapshot, width int) []string {
	switch {
	case snapshot.PendingToolInput != nil:
		return m.renderCodexToolInputBlocks(*snapshot.PendingToolInput, width)
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeForm:
		return m.renderCodexElicitationBlocks(*snapshot.PendingElicitation, width)
	default:
		return nil
	}
}

func (m Model) renderCodexBrowserPanel(snapshot codexapp.Snapshot, width int) string {
	lines := []string{}
	lines = append(lines, m.codexBrowserReconnectLines(snapshot)...)
	if snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode != codexapp.ElicitationModeForm {
		lines = append(lines, m.renderCodexElicitationBlocks(*snapshot.PendingElicitation, width)...)
	} else {
		lines = append(lines, m.renderCodexCurrentBrowserPageBlocks(snapshot, width)...)
	}
	lines = compactNonEmptyStrings(lines)
	if len(lines) == 0 {
		return ""
	}
	accent := lipgloss.Color("81")
	if m.codexBrowserPolicyMismatch(snapshot) {
		accent = lipgloss.Color("221")
	}
	return renderCodexMessageBlock("Browser", strings.Join(lines, "\n"), accent, lipgloss.Color("252"), max(24, width-4))
}

func (m Model) renderCodexToolInputBlocks(request codexapp.ToolInputRequest, width int) []string {
	lines := []string{fitFooterWidth("Structured input: "+request.Summary(), width)}
	if len(request.Questions) == 0 {
		return lines
	}
	state := m.toolAnswerStateFor(m.codexVisibleProject, &request)
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	question := request.Questions[state.QuestionIndex]
	if len(request.Questions) > 1 {
		lines = append(lines, fitFooterWidth(fmt.Sprintf("Question %d/%d", state.QuestionIndex+1, len(request.Questions)), width))
	}
	if header := strings.TrimSpace(question.Header); header != "" {
		lines = append(lines, fitFooterWidth(header, width))
	}
	prompt := question.Question
	if question.IsSecret {
		prompt += " [secret]"
	}
	lines = append(lines, fitFooterWidth(prompt, width))
	for i, option := range question.Options {
		line := fmt.Sprintf("%d %s", i+1, strings.TrimSpace(option.Label))
		if desc := strings.TrimSpace(option.Description); desc != "" {
			line += " - " + desc
		}
		lines = append(lines, fitFooterWidth(line, width))
	}
	return lines
}

func (m Model) renderCodexElicitationBlocks(request codexapp.ElicitationRequest, width int) []string {
	lines := []string{fitFooterWidth("MCP input: "+request.Summary(), width)}
	loginURL := ""
	managedSessionKey := ""
	if snapshot, ok := m.currentCachedCodexSnapshot(); ok {
		loginURL = managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL)
		managedSessionKey = strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
	}
	if request.Mode == codexapp.ElicitationModeURL && strings.TrimSpace(request.URL) != "" {
		lines = append(lines, fitFooterWidth("Requested URL: "+request.URL, width))
		if loginURL != "" && managedSessionKey != "" {
			lines = append(lines, fitFooterWidth("Press O to reveal the managed browser window, then finish the login flow and press Enter when you are done.", width))
		}
	}
	if request.Mode == codexapp.ElicitationModeForm {
		lines = append(lines, fitFooterWidth("Paste JSON or text into the composer, then press Enter to accept.", width))
		if len(request.RequestedSchema) > 0 {
			schemaSummary := strings.TrimSpace(string(request.RequestedSchema))
			lines = append(lines, fitFooterWidth("Requested schema: "+schemaSummary, width))
		}
	}
	return lines
}

func (m Model) renderCodexCurrentBrowserPageBlocks(snapshot codexapp.Snapshot, width int) []string {
	if snapshot.Closed || snapshot.Busy || snapshot.BusyExternal {
		return nil
	}
	pageURL := managedBrowserCurrentPageURL(snapshot)
	if pageURL == "" || strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
		return nil
	}
	lines := []string{fitFooterWidth(m.managedBrowserCurrentPageLabel(snapshot)+pageURL, width)}
	if hint := m.managedBrowserCurrentPageHint(snapshot); hint != "" {
		lines = append(lines, fitFooterWidth(hint, width))
	}
	return lines
}

func (m Model) codexBrowserPolicyMismatch(snapshot codexapp.Snapshot) bool {
	currentPolicy := m.currentPlaywrightPolicy()
	sessionPolicy := snapshot.BrowserActivity.Policy.Normalize()
	if currentPolicy != sessionPolicy {
		return true
	}
	return embeddedProvider(snapshot) == codexapp.ProviderCodex &&
		!currentPolicy.UsesLegacyLaunchBehavior() &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == ""
}

func (m Model) codexBrowserReconnectLines(snapshot codexapp.Snapshot) []string {
	if !m.codexBrowserPolicyMismatch(snapshot) {
		return nil
	}
	currentPolicy := m.currentPlaywrightPolicy()
	currentLabel := settingsBrowserAutomationOptionLabel(settingsBrowserAutomationValue(currentPolicy), currentPolicy)
	newCommand := embeddedNewCommand(embeddedProvider(snapshot))
	if embeddedProvider(snapshot) == codexapp.ProviderCodex &&
		!currentPolicy.UsesLegacyLaunchBehavior() &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
		lines := []string{
			"Managed browser controls are not attached to this session yet.",
			"Current browser setting: " + currentLabel + ".",
		}
		if newCommand != "" {
			lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior, or "+newCommand+" for a fresh session.")
		} else {
			lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior.")
		}
		return lines
	}
	sessionPolicy := snapshot.BrowserActivity.Policy.Normalize()
	sessionLabel := settingsBrowserAutomationOptionLabel(settingsBrowserAutomationValue(sessionPolicy), sessionPolicy)
	lines := []string{
		"Session browser setting: " + sessionLabel + ".",
		"Current browser setting: " + currentLabel + ".",
	}
	if newCommand != "" {
		lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior, or "+newCommand+" for a fresh session.")
	} else {
		lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior.")
	}
	return lines
}

func (m Model) codexBrowserReconnectStatus(snapshot codexapp.Snapshot) string {
	lines := m.codexBrowserReconnectLines(snapshot)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, " ")
}

func (m Model) renderCodexBanner(snapshot codexapp.Snapshot, width int) string {
	provider := embeddedProvider(snapshot)
	parts := []string{provider.Label()}
	if projectName := strings.TrimSpace(filepath.Base(snapshot.ProjectPath)); projectName != "" && projectName != "." {
		parts = append(parts, projectName)
	}
	if snapshot.BusyExternal {
		parts = append(parts, "Read-only")
	}
	if m.codexDenseExpanded {
		parts = append(parts, "Blocks expanded")
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(strings.Join(parts, " | "))
	actions := renderFooterActionList(
		footerNavAction("Alt+Down", "picker"),
		footerNavAction("Alt+[", "prev"),
		footerNavAction("Alt+]", "next"),
		footerLowAction("Alt+L", "blocks"),
	)
	overlay := ""
	contentWidth := width
	if snapshot.Preset == codexcli.PresetYolo && !snapshot.Closed {
		overlay = "  " + detailDangerStyle.Render("YOLO MODE")
		if overlayWidth := lipgloss.Width(overlay); overlayWidth >= width {
			return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
		} else if width > 0 {
			contentWidth = width - overlayWidth
		}
	}
	line := renderFooterLine(contentWidth, title, actions)
	banner := lipgloss.PlaceHorizontal(max(0, contentWidth), lipgloss.Left, line)
	if overlay != "" {
		return banner + overlay
	}
	return banner
}

func overlayCodexBannerRight(base, overlay string, width int) string {
	if overlay == "" {
		return base
	}
	if width <= 0 {
		width = max(lipgloss.Width(base), lipgloss.Width(overlay))
	}
	if lipgloss.Width(base) < width {
		base = lipgloss.PlaceHorizontal(width, lipgloss.Left, base)
	}
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth >= width {
		return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
	}
	left := width - overlayWidth
	prefix := ansi.Cut(base, 0, left)
	suffix := ansi.Cut(base, left+overlayWidth, width)
	return prefix + overlay + suffix
}

func (m Model) renderCodexBusyElsewhereNotice(snapshot codexapp.Snapshot, width int) string {
	label := embeddedProvider(snapshot).Label()
	message := strings.TrimSpace(snapshot.LastSystemNotice)
	if message == "" {
		sessionID := shortID(snapshot.ThreadID)
		if sessionID == "" {
			sessionID = "this session"
		}
		message = fmt.Sprintf("Embedded %s session %s is already active in another process, so embedded controls are read-only until it finishes.", label, sessionID)
	}
	return renderCodexMessageBlock("Read-only", message, lipgloss.Color("221"), lipgloss.Color("252"), max(24, width-4))
}

func (m Model) renderCodexSessionMeta(snapshot codexapp.Snapshot, width int) string {
	segments := []string{}
	model := strings.TrimSpace(snapshot.Model)
	reasoning := strings.TrimSpace(snapshot.ReasoningEffort)
	showPendingAsCurrent := codexSnapshotShowsPendingModelAsCurrent(snapshot)
	if showPendingAsCurrent {
		model = strings.TrimSpace(snapshot.PendingModel)
		reasoning = firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), reasoning)
	}
	if model != "" {
		segments = append(segments, renderFooterMeta("Model")+" "+renderFooterStatus(model))
	}
	if reasoning != "" {
		segments = append(segments, renderFooterMeta("Reasoning")+" "+renderFooterStatus(reasoning))
	}
	if context := codexSnapshotContextLeftLabel(snapshot); context != "" {
		segments = append(segments, renderFooterMeta("Context")+" "+renderFooterStatus(context))
	}
	if nextModel := strings.TrimSpace(snapshot.PendingModel); nextModel != "" && !showPendingAsCurrent {
		nextReasoning := firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), strings.TrimSpace(snapshot.ReasoningEffort))
		next := nextModel
		if nextReasoning != "" {
			next += " / " + nextReasoning
		}
		segments = append(segments, renderFooterMeta("Next")+" "+renderFooterUsage(next))
	}
	if len(segments) == 0 {
		return ""
	}
	return renderFooterLine(width, segments...)
}

func codexSnapshotShowsPendingModelAsCurrent(snapshot codexapp.Snapshot) bool {
	if strings.TrimSpace(snapshot.PendingModel) == "" || snapshot.Busy || snapshot.BusyExternal || snapshot.Closed {
		return false
	}
	for _, entry := range snapshot.Entries {
		switch entry.Kind {
		case codexapp.TranscriptSystem, codexapp.TranscriptStatus:
			continue
		default:
			return false
		}
	}
	return true
}

func codexSnapshotContextLeftLabel(snapshot codexapp.Snapshot) string {
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.ModelContextWindow <= 0 {
		return ""
	}
	return fmt.Sprintf("%d%% left (%s tok)", snapshot.TokenUsage.ContextLeftPercent(), formatInt64(snapshot.TokenUsage.ContextLeftTokens()))
}

func firstNonEmptyCodexLabel(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (m Model) renderCodexFooter(snapshot codexapp.Snapshot, width int) string {
	status := renderCodexFooterStatus(snapshot, m.currentTime(), m.spinnerFrame)

	var actions []footerAction
	switch {
	case snapshot.PendingToolInput != nil:
		actions = append(actions,
			footerPrimaryAction("Enter", "answer"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
		)
		state := m.toolAnswerStateFor(m.codexVisibleProject, snapshot.PendingToolInput)
		if state.QuestionIndex >= 0 && state.QuestionIndex < len(snapshot.PendingToolInput.Questions) {
			if len(snapshot.PendingToolInput.Questions[state.QuestionIndex].Options) > 0 {
				actions = append(actions, footerNavAction("1-9", "choose"))
			}
		}
		if len(snapshot.PendingToolInput.Questions) > 1 {
			actions = append(actions, footerNavAction("Tab", "next"))
		}
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeForm:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerNavAction("Alt+Enter", "newline"),
		}
	case snapshot.PendingElicitation != nil &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, snapshot.PendingElicitation.Mode, snapshot.PendingElicitation.URL) != "":
		actions = []footerAction{
			footerPrimaryAction("O", "show browser"),
			footerPrimaryAction("Enter", "done/accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
		}
	case snapshot.PendingElicitation != nil:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
		}
	case m.codexSlashActive():
		actions = []footerAction{
			footerPrimaryAction("Enter", "run"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Tab", "complete"),
			footerNavAction("Shift+Tab", "previous"),
			footerNavAction("Alt+Enter", "newline"),
		}
	case snapshot.BusyExternal:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
		if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
			actions = append(actions, footerNavAction(cmd, "session"))
		}
	case snapshot.Phase == codexapp.SessionPhaseReconciling:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
	case snapshot.Phase == codexapp.SessionPhaseStalled:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("/reconnect", "recover"),
		}
	case snapshot.Phase == codexapp.SessionPhaseFinishing:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
	case m.codexInputSelectionActive():
		actions = []footerAction{
			footerPrimaryAction("Space", "mark"),
			footerExitAction("Esc", "cancel"),
			footerNavAction("arrows", "move"),
		}
	case snapshot.Busy:
		actions = []footerAction{
			footerPrimaryAction("Enter", "steer"),
			footerExitAction("Ctrl+C", "interrupt"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("Ctrl+V", "image"),
			footerLowAction("Alt+S", "select"),
		}
	case snapshot.Closed:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerLowAction("PgUp/PgDn", "scroll"),
		}
	default:
		actions = []footerAction{
			footerPrimaryAction("Enter", "send"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("Ctrl+V", "image"),
			footerLowAction("Alt+S", "select"),
		}
		if managedBrowserCurrentPageURL(snapshot) != "" && strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" {
			actions = append(actions, footerNavAction("Ctrl+O", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
	}
	if mismatchStatus := m.codexBrowserReconnectStatus(snapshot); mismatchStatus != "" && !snapshot.Closed && !snapshot.BusyExternal {
		actions = append(actions, footerNavAction("/reconnect", "apply browser"))
		if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
			actions = append(actions, footerLowAction(cmd, "fresh"))
		}
	}

	segments := []string{}
	if status != "" {
		segments = append(segments, status)
	}
	segments = append(segments, renderFooterActionList(actions...))
	return renderFooterLine(width, segments...)
}

func (m Model) managedBrowserCurrentPageLabel(snapshot codexapp.Snapshot) string {
	sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
	if state, ok := m.cachedManagedBrowserState(sessionKey); ok && state.RevealSupported && !state.Hidden {
		return "Managed browser page: "
	}
	return "Background browser page: "
}

func (m Model) managedBrowserCurrentPageHint(snapshot codexapp.Snapshot) string {
	sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
	if sessionKey == "" {
		return ""
	}
	if state, ok := m.cachedManagedBrowserState(sessionKey); ok {
		if !state.RevealSupported || !state.Hidden {
			return ""
		}
	}
	return "Press Ctrl+O to reveal the managed browser window for this same session."
}

func (m Model) managedBrowserCurrentPageFooterLabel(snapshot codexapp.Snapshot) string {
	sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
	if state, ok := m.cachedManagedBrowserState(sessionKey); ok && state.RevealSupported && !state.Hidden {
		return "focus browser"
	}
	return "show browser"
}

func compactNonEmptyStrings(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

var (
	codexFinishingFooterPalette   = []lipgloss.Color{lipgloss.Color("214"), lipgloss.Color("220"), lipgloss.Color("229"), lipgloss.Color("221")}
	codexReconcilingFooterPalette = []lipgloss.Color{lipgloss.Color("220"), lipgloss.Color("229"), lipgloss.Color("214")}
)

const codexBusyGradientLoopFrames = 25.0

func renderCodexFooterStatus(snapshot codexapp.Snapshot, now time.Time, spinnerFrame int) string {
	status := codexFooterStatus(snapshot, now)
	switch {
	case status == "Working", status == "Working elsewhere", strings.HasPrefix(status, "Working "):
		return renderCodexAnimatedBusyFooterStatus(status, spinnerFrame)
	case strings.HasPrefix(status, "Finishing "):
		timer := strings.TrimPrefix(status, "Finishing ")
		return renderCodexAnimatedFooterLabel("Finishing", spinnerFrame, codexFinishingFooterPalette) + " " +
			renderCodexAnimatedFooterTimer(timer, spinnerFrame, lipgloss.Color("221"))
	case status == "Finishing":
		return renderCodexAnimatedFooterLabel("Finishing", spinnerFrame, codexFinishingFooterPalette)
	case status == "Rechecking turn status":
		return renderCodexAnimatedFooterText(status, spinnerFrame, codexReconcilingFooterPalette)
	default:
		return renderFooterStatus(status)
	}
}

// Render a wrapped grayscale wave across the full busy label so the gradient
// stays continuous from the end of the phrase back to the beginning.
func renderCodexAnimatedBusyFooterStatus(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) == 1 {
		return lipgloss.NewStyle().Bold(true).Foreground(renderCodexBusyGrayColor(228)).Render(text)
	}

	phase := codexBusyGradientPhase(spinnerFrame)
	count := float64(len(runes))
	var out strings.Builder
	for i, r := range runes {
		position := (float64(i) + 0.5) / count
		gray := codexBusyGradientGrayLevel(position, phase)
		out.WriteString(lipgloss.NewStyle().Bold(true).Foreground(renderCodexBusyGrayColor(gray)).Render(string(r)))
	}
	return out.String()
}

func codexBusyGradientPhase(spinnerFrame int) float64 {
	phase := math.Mod(float64(spinnerFrame)/codexBusyGradientLoopFrames, 1.0)
	if phase < 0 {
		phase += 1
	}
	return phase
}

func codexBusyGradientGrayLevel(position, phase float64) int {
	position = math.Mod(position, 1)
	if position < 0 {
		position += 1
	}
	theta := 2 * math.Pi * (position - phase)
	wave := 0.5 + 0.5*math.Cos(theta)
	contrast := math.Pow(wave, 0.92)
	gray := 124 + contrast*(244-124)
	return int(math.Round(gray))
}

func renderCodexBusyGrayColor(level int) lipgloss.Color {
	if level < 0 {
		level = 0
	}
	if level > 255 {
		level = 255
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", level, level, level))
}

func renderCodexAnimatedFooterLabel(label string, spinnerFrame int, palette []lipgloss.Color) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if len(palette) == 0 {
		return renderFooterStatus(label)
	}
	shift := (spinnerFrame / 3) % len(palette)
	var out strings.Builder
	for i, r := range label {
		style := lipgloss.NewStyle().Bold(true).Foreground(palette[(i+shift)%len(palette)])
		out.WriteString(style.Render(string(r)))
	}
	return out.String()
}

func renderCodexAnimatedFooterText(text string, spinnerFrame int, palette []lipgloss.Color) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(palette) == 0 {
		return renderFooterStatus(text)
	}
	color := palette[(spinnerFrame/4)%len(palette)]
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(text)
}

func renderCodexAnimatedFooterTimer(text string, spinnerFrame int, accent lipgloss.Color) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	palette := []lipgloss.Color{accent, lipgloss.Color("252"), accent, lipgloss.Color("153")}
	return lipgloss.NewStyle().Bold(true).Foreground(palette[(spinnerFrame/4)%len(palette)]).Render(text)
}

func (m Model) renderCodexTranscriptContent(width int) string {
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return "Embedded session unavailable"
	}
	return m.renderCodexTranscriptContentFromSnapshot(snapshot, width)
}

func (m Model) renderCodexTranscriptContentFromSnapshot(snapshot codexapp.Snapshot, width int) string {
	if rendered := m.renderCodexTranscriptEntries(snapshot, width); strings.TrimSpace(rendered) != "" {
		return rendered
	}
	if snapshot.Closed {
		return embeddedProvider(snapshot).Label() + " session closed."
	}
	if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
		return "[system] " + sanitizeCodexRenderedText(notice)
	}
	return "Type a prompt and press Enter."
}

func normalizedCodexStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "", "Codex session ready", "OpenCode session ready":
		return ""
	case "Codex turn complete", "Codex turn completed", "OpenCode turn complete", "OpenCode turn completed", "Turn completed":
		return "Turn completed"
	default:
		for _, prefix := range []string{"Codex turn ", "OpenCode turn "} {
			if strings.HasPrefix(status, prefix) {
				return "Turn " + strings.TrimSpace(strings.TrimPrefix(status, prefix))
			}
		}
		return status
	}
}

func codexStatusIsCompacting(status string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), "compacting conversation history")
}

func (m *Model) refreshCodexSubmitSnapshot(projectPath string, snapshot codexapp.Snapshot) (codexapp.Snapshot, bool, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || !codexSnapshotNeedsSubmitRefresh(snapshot) {
		return snapshot, true, false
	}
	return m.refreshCodexSnapshot(projectPath)
}

func codexSnapshotNeedsSubmitRefresh(snapshot codexapp.Snapshot) bool {
	if snapshot.BusyExternal {
		return true
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseFinishing, codexapp.SessionPhaseReconciling, codexapp.SessionPhaseStalled:
		return true
	default:
		return false
	}
}

func codexSnapshotCanSteer(snapshot codexapp.Snapshot) bool {
	switch snapshot.Phase {
	case "", codexapp.SessionPhaseRunning:
		return snapshot.Busy && !snapshot.BusyExternal
	default:
		return false
	}
}

func codexFooterStatus(snapshot codexapp.Snapshot, now time.Time) string {
	switch snapshot.Phase {
	case codexapp.SessionPhaseReconciling:
		if codexStatusIsCompacting(snapshot.Status) {
			return "Compacting conversation"
		}
		return "Rechecking turn status"
	case codexapp.SessionPhaseStalled:
		return "Stalled; use /reconnect"
	case codexapp.SessionPhaseFinishing:
		if !snapshot.BusySince.IsZero() {
			return "Finishing " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Finishing"
	case codexapp.SessionPhaseExternal:
		if !snapshot.BusySince.IsZero() {
			return "Working elsewhere " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working elsewhere"
	}
	if snapshot.Busy {
		if !snapshot.BusySince.IsZero() {
			return "Working " + formatRunningDuration(now.Sub(snapshot.BusySince))
		}
		return "Working"
	}
	return normalizedCodexStatus(snapshot.Status)
}

func formatRunningDuration(d time.Duration) string {
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

func (m Model) renderCodexTranscriptEntries(snapshot codexapp.Snapshot, width int) string {
	if len(snapshot.Entries) == 0 {
		snapshot.Entries = parseLegacyCodexTranscript(snapshot.Transcript)
	}
	if len(snapshot.Entries) == 0 {
		return ""
	}
	entries := snapshot.Entries
	if snapshot.Provider.Normalized() == codexapp.ProviderOpenCode {
		entries = collapseOpenCodeToolRuns(entries, m.codexDenseExpanded)
		entries = collapseOpenCodeLargeCodeBlocks(entries, m.codexDenseExpanded)
		entries = collapseOpenCodeMassiveEntries(entries, m.codexDenseExpanded)
	}
	if width <= 0 {
		width = 80
	}
	contentWidth := max(18, width-4)
	blocks := make([]string, 0, len(entries)*2)
	var previousKind codexapp.TranscriptKind
	hasPrevious := false
	// Track consecutive reasoning entries to merge into one compact indicator
	reasoningLineCount := 0
	flushReasoning := func() {
		if reasoningLineCount == 0 {
			return
		}
		block := renderReasoningIndicator(reasoningLineCount, contentWidth)
		if hasPrevious {
			blocks = append(blocks, codexTranscriptEntrySeparator(previousKind, codexapp.TranscriptReasoning))
		}
		blocks = append(blocks, block)
		previousKind = codexapp.TranscriptReasoning
		hasPrevious = true
		reasoningLineCount = 0
	}
	for _, entry := range entries {
		if m.hideReasoningSections && !m.codexDenseExpanded && entry.Kind == codexapp.TranscriptReasoning {
			// Accumulate reasoning lines for compact indicator
			text := strings.TrimSpace(entry.Text)
			if text != "" {
				reasoningLineCount += len(strings.Split(text, "\n"))
			}
			continue
		}
		// Flush any pending reasoning indicator before a non-reasoning entry
		flushReasoning()
		block := renderCodexTranscriptEntry(entry, contentWidth, m.codexDenseExpanded)
		if strings.TrimSpace(block) != "" {
			if hasPrevious {
				blocks = append(blocks, codexTranscriptEntrySeparator(previousKind, entry.Kind))
			}
			blocks = append(blocks, block)
			previousKind = entry.Kind
			hasPrevious = true
		}
	}
	// Flush trailing reasoning (model still thinking)
	flushReasoning()
	return strings.Join(blocks, "")
}

func renderCodexTranscriptEntry(entry codexapp.TranscriptEntry, width int, expanded bool) string {
	text := strings.TrimSpace(sanitizeCodexRenderedText(entry.Text))
	if text == "" {
		return ""
	}
	switch entry.Kind {
	case codexapp.TranscriptUser:
		if dt := strings.TrimSpace(entry.DisplayText); dt != "" {
			text = dt
		}
		divider := lipgloss.NewStyle().
			Foreground(lipgloss.Color("238")).
			Render(strings.Repeat("─", max(0, width)))
		return divider + "\n" + renderCodexUserMessageBlock(text, width)
	case codexapp.TranscriptAgent:
		return renderCodexMessageBlock("", text, lipgloss.Color("120"), lipgloss.Color("252"), width)
	case codexapp.TranscriptPlan:
		return renderCodexMessageBlock("Plan", text, lipgloss.Color("214"), lipgloss.Color("252"), width)
	case codexapp.TranscriptReasoning:
		return renderReasoningBlock(text, width)
	case codexapp.TranscriptCommand:
		return renderCodexDenseBlock("Command", text, lipgloss.Color("111"), width, expanded)
	case codexapp.TranscriptFileChange:
		return renderCodexDenseBlock("File changes", text, lipgloss.Color("179"), width, expanded)
	case codexapp.TranscriptTool:
		return renderCodexToolLine(text, width)
	case codexapp.TranscriptError:
		return renderCodexMessageBlock("Error", text, lipgloss.Color("203"), lipgloss.Color("252"), width)
	case codexapp.TranscriptStatus:
		return renderCodexStatusBlock(text, width)
	case codexapp.TranscriptSystem:
		return renderCodexMessageBlock("System", text, lipgloss.Color("244"), lipgloss.Color("246"), width)
	default:
		return renderCodexMessageBlock("", text, lipgloss.Color("244"), lipgloss.Color("252"), width)
	}
}

func codexTranscriptEntrySeparator(previous, current codexapp.TranscriptKind) string {
	// Tight separator (single newline) for entries that are part of the same action flow
	switch {
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptCommand:
		return "\n"
	case previous == codexapp.TranscriptCommand && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptFileChange:
		return "\n"
	case previous == codexapp.TranscriptFileChange && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptCommand && current == codexapp.TranscriptFileChange:
		return "\n"
	case previous == codexapp.TranscriptFileChange && current == codexapp.TranscriptCommand:
		return "\n"
	case previous == codexapp.TranscriptReasoning && current == codexapp.TranscriptReasoning:
		return "\n"
	default:
		return "\n\n"
	}
}

func compactCodexToolTranscriptText(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " | ")
}

// parsedToolCall holds the decomposed parts of a tool transcript entry.
type parsedToolCall struct {
	ToolName string // e.g. "bash", "read", "write", "grep"
	Status   string // e.g. "completed", "running", ""
	Summary  string // description or command
	Prefix   string // e.g. "Tool", "MCP tool", "Web search"
}

// parseToolTranscriptText extracts tool name, status, and summary from tool text.
func parseToolTranscriptText(text string) parsedToolCall {
	text = strings.TrimSpace(text)

	// Handle collapsed summary lines ("Tool activity: ...")
	if strings.HasPrefix(text, "Tool activity") {
		return parsedToolCall{Prefix: "Tool", ToolName: "activity", Summary: strings.TrimPrefix(text, "Tool activity: ")}
	}

	// "Web search: query"
	if strings.HasPrefix(text, "Web search: ") {
		return parsedToolCall{Prefix: "Web", ToolName: "search", Summary: strings.TrimPrefix(text, "Web search: ")}
	}

	// "Viewed image: path"
	if strings.HasPrefix(text, "Viewed image: ") {
		return parsedToolCall{Prefix: "Tool", ToolName: "image", Summary: strings.TrimPrefix(text, "Viewed image: ")}
	}

	// "Image generation [status]\nresult"
	if strings.HasPrefix(text, "Image generation") {
		return parsedToolCall{Prefix: "Tool", ToolName: "image_gen", Summary: text}
	}

	// "MCP tool server/tool [status]"
	if strings.HasPrefix(text, "MCP tool ") {
		rest := strings.TrimPrefix(text, "MCP tool ")
		name, status := "", ""
		if idx := strings.Index(rest, " ["); idx >= 0 {
			name = rest[:idx]
			end := strings.IndexByte(rest[idx+2:], ']')
			if end >= 0 {
				status = rest[idx+2 : idx+2+end]
			}
		} else {
			name = rest
		}
		return parsedToolCall{Prefix: "MCP", ToolName: name, Status: status}
	}

	// "Tool <name> [status]" (dynamic tool calls)
	if strings.HasPrefix(text, "Tool ") && strings.Contains(text, " [") {
		rest := strings.TrimPrefix(text, "Tool ")
		if idx := strings.Index(rest, " ["); idx >= 0 {
			name := rest[:idx]
			end := strings.IndexByte(rest[idx+2:], ']')
			status := ""
			if end >= 0 {
				status = rest[idx+2 : idx+2+end]
			}
			return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status}
		}
	}

	// "Tool <name> <status>: <summary>" or "Tool <name>: <summary>" or "Tool <name> <status>" or "Tool <name>"
	if strings.HasPrefix(text, "Tool ") {
		rest := strings.TrimPrefix(text, "Tool ")
		// Try "name status: summary"
		if colonIdx := strings.Index(rest, ": "); colonIdx >= 0 {
			before := rest[:colonIdx]
			summary := rest[colonIdx+2:]
			parts := strings.SplitN(before, " ", 2)
			name := parts[0]
			status := ""
			if len(parts) > 1 {
				status = parts[1]
			}
			return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status, Summary: summary}
		}
		// Try "name status" or just "name"
		parts := strings.SplitN(rest, " ", 2)
		name := parts[0]
		status := ""
		if len(parts) > 1 {
			status = parts[1]
		}
		return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status}
	}

	return parsedToolCall{Summary: text}
}

// toolCategoryColor returns accent color and symbol for a tool name.
func toolCategoryColor(toolName string) (accent lipgloss.Color, symbol string) {
	lower := strings.ToLower(toolName)
	switch {
	case lower == "bash" || lower == "shell" || lower == "command" || lower == "execute":
		return lipgloss.Color("111"), "$" // blue
	case lower == "read" || lower == "cat" || lower == "view":
		return lipgloss.Color("179"), "→" // yellow/amber
	case lower == "write" || lower == "edit" || lower == "patch" || lower == "apply_diff":
		return lipgloss.Color("120"), "+" // green
	case lower == "grep" || lower == "search" || lower == "find" || lower == "glob" || lower == "rg":
		return lipgloss.Color("81"), "?" // cyan
	case strings.Contains(lower, "/"):
		return lipgloss.Color("141"), "◆" // purple for MCP (server/tool format)
	case lower == "image" || lower == "image_gen":
		return lipgloss.Color("179"), "◻" // amber
	case lower == "search":
		return lipgloss.Color("214"), "⊕" // orange for web
	case lower == "activity":
		return lipgloss.Color("244"), "…" // gray for collapsed summaries
	default:
		return lipgloss.Color("141"), "•" // purple default
	}
}

// renderCodexToolLine renders a tool transcript entry with structured styling.
func renderCodexToolLine(text string, width int) string {
	compacted := compactCodexToolTranscriptText(text)
	parsed := parseToolTranscriptText(compacted)

	accent, symbol := toolCategoryColor(parsed.ToolName)

	// Build the styled line
	var parts []string

	// Symbol + tool name (bold)
	nameStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	if parsed.ToolName != "" {
		parts = append(parts, nameStyle.Render(symbol+" "+parsed.ToolName))
	} else {
		parts = append(parts, nameStyle.Render(symbol+" tool"))
	}

	// Status (dimmed, skip "completed" as it's noise)
	if parsed.Status != "" && parsed.Status != "completed" && parsed.Status != "call completed" {
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true)
		parts = append(parts, statusStyle.Render("["+parsed.Status+"]"))
	}

	// Summary (lighter color)
	if parsed.Summary != "" {
		summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		summary := parsed.Summary
		// Truncate long summaries to fit width (leave room for name + status)
		usedWidth := len(parsed.ToolName) + 4 // symbol + spaces + margin
		maxSummary := width - usedWidth - 4
		if maxSummary > 10 && len(summary) > maxSummary {
			// Preserve "+N more ..." suffix if present
			if moreIdx := strings.LastIndex(summary, " | +"); moreIdx >= 0 {
				suffix := summary[moreIdx+3:] // e.g. "+3 more tool updates"
				suffixStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(suffix)
				trimmed := summary[:moreIdx]
				maxTrimmed := maxSummary - len(suffix) - 5
				if maxTrimmed > 10 && len(trimmed) > maxTrimmed {
					trimmed = trimmed[:maxTrimmed-1] + "…"
				}
				parts = append(parts, summaryStyle.Render(trimmed), suffixStyled)
			} else {
				summary = summary[:maxSummary-1] + "…"
				parts = append(parts, summaryStyle.Render(summary))
			}
		} else {
			parts = append(parts, summaryStyle.Render(summary))
		}
	}

	body := strings.Join(parts, " ")
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Width(width).
		Render(body)
}

func collapseOpenCodeToolRuns(entries []codexapp.TranscriptEntry, expanded bool) []codexapp.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]codexapp.TranscriptEntry, 0, len(entries))
	toolRunStart := -1
	agentRunStart := -1
	flushTools := func(end int) {
		if toolRunStart < 0 || end <= toolRunStart {
			return
		}
		out = append(out, summarizeOpenCodeToolRun(entries[toolRunStart:end]))
		toolRunStart = -1
	}
	flushAgents := func(end int) {
		if agentRunStart < 0 || end <= agentRunStart {
			return
		}
		run := entries[agentRunStart:end]
		if expanded {
			out = append(out, run...)
		} else {
			parts := make([]string, 0, len(run))
			for _, entry := range run {
				parts = append(parts, strings.TrimSpace(entry.Text))
			}
			if collapsedText, ok := collapseOpenCodeLargeCodeBlock(strings.Join(parts, "\n")); ok {
				out = append(out, codexapp.TranscriptEntry{
					Kind: codexapp.TranscriptAgent,
					Text: collapsedText,
				})
			} else {
				out = append(out, run...)
			}
		}
		agentRunStart = -1
	}
	for i, entry := range entries {
		switch entry.Kind {
		case codexapp.TranscriptTool:
			flushAgents(i)
			if toolRunStart < 0 {
				toolRunStart = i
			}
		case codexapp.TranscriptAgent:
			flushTools(i)
			if agentRunStart < 0 {
				agentRunStart = i
			}
		default:
			flushTools(i)
			flushAgents(i)
			out = append(out, entry)
		}
	}
	flushTools(len(entries))
	flushAgents(len(entries))
	return out
}

func collapseOpenCodeLargeCodeBlocks(entries []codexapp.TranscriptEntry, expanded bool) []codexapp.TranscriptEntry {
	if expanded || len(entries) == 0 {
		return entries
	}
	out := make([]codexapp.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != codexapp.TranscriptAgent {
			out = append(out, entry)
			continue
		}
		toolText, ok := collapseOpenCodeLargeCodeBlock(entry.Text)
		if !ok {
			out = append(out, entry)
			continue
		}
		out = append(out, codexapp.TranscriptEntry{
			Kind: entry.Kind,
			Text: toolText,
		})
	}
	return out
}

func collapseOpenCodeLargeCodeBlock(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= openCodeCollapsedAgentCodeLineLimit {
		return "", false
	}
	inCodeFence := false
	foundCodeFence := false
	codeLineCount := 0
	previewLines := make([]string, 0, openCodeAgentCodePreviewLines)
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			foundCodeFence = true
			inCodeFence = !inCodeFence
			continue
		}
		if !inCodeFence {
			if foundCodeFence {
				continue
			}
			if isLikelyCodeLine(line) {
				codeLineCount++
				if len(previewLines) < openCodeAgentCodePreviewLines {
					previewLines = append(previewLines, line)
				}
			}
			continue
		}
		codeLineCount++
		if len(previewLines) < openCodeAgentCodePreviewLines {
			previewLines = append(previewLines, line)
		}
	}
	if !foundCodeFence {
		if !looksLikeCodeBlock(lines) {
			return "", false
		}
		if codeLineCount == 0 {
			codeLineCount = len(lines)
			previewLines = make([]string, 0, openCodeAgentCodePreviewLines)
			for _, line := range lines {
				if len(previewLines) >= openCodeAgentCodePreviewLines {
					break
				}
				previewLines = append(previewLines, line)
			}
		}
	}
	if codeLineCount <= openCodeCollapsedAgentCodeLineLimit {
		return "", false
	}
	totalCodeLines := codeLineCount
	shownPreview := len(previewLines)
	hiddenLines := totalCodeLines - shownPreview
	if shownPreview > 0 {
		return fmt.Sprintf("Assistant answer includes a long code block (%d lines, %d shown, %d hidden). Alt+L expands the full output.\n\nPreview:\n%s", totalCodeLines, shownPreview, hiddenLines, truncateText(strings.Join(previewLines, "\n"), openCodeCollapsedCodePreviewMaxText)), true
	}
	return fmt.Sprintf("Assistant answer includes a long code block (%d lines). Alt+L expands the full output.", totalCodeLines), true
}

func looksLikeCodeBlock(lines []string) bool {
	if len(lines) <= openCodeCollapsedAgentCodeLineLimit {
		return false
	}
	codeLike := 0
	total := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		if isLikelyCodeLine(line) {
			codeLike++
		}
	}
	if total == 0 {
		return false
	}
	return codeLike*100/total >= openCodeCollapsedAgentPreviewRatio
}

func isLikelyCodeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "{") {
		return true
	}
	if strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "let ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "if ") {
		return true
	}
	if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "}") {
		return true
	}
	if strings.ContainsAny(trimmed, "{}();[]<>+=-*/!?:") {
		return true
	}
	return false
}

// collapseOpenCodeMassiveEntries caps oversized entries and detects repetitive content.
// Applied after code-block collapsing as a safety net for verbose/broken model output.
func collapseOpenCodeMassiveEntries(entries []codexapp.TranscriptEntry, expanded bool) []codexapp.TranscriptEntry {
	if expanded || len(entries) == 0 {
		return entries
	}
	out := make([]codexapp.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		switch entry.Kind {
		case codexapp.TranscriptAgent, codexapp.TranscriptReasoning:
			text := strings.TrimSpace(entry.Text)
			lines := strings.Split(text, "\n")

			maxLines := openCodeMaxEntryLines
			previewLines := openCodeMaxEntryPreviewLines
			kindLabel := "output"
			if entry.Kind == codexapp.TranscriptReasoning {
				maxLines = openCodeMaxReasoningLines
				previewLines = openCodeMaxReasoningPreview
				kindLabel = "reasoning"
			}

			// Check for repetitive content first (catches it even under the line cap)
			if repIdx, repCount := detectRepetitiveContent(lines); repIdx >= 0 {
				kept := lines[:repIdx]
				omitted := len(lines) - repIdx
				summary := fmt.Sprintf("\n[Repetitive %s detected: %d similar blocks omitted (%d lines). Alt+L expands.]",
					kindLabel, repCount, omitted)
				out = append(out, codexapp.TranscriptEntry{
					Kind: entry.Kind,
					Text: strings.Join(kept, "\n") + summary,
				})
				continue
			}

			// Apply line cap
			if len(lines) > maxLines {
				preview := strings.Join(lines[:previewLines], "\n")
				summary := fmt.Sprintf("%s\n\n[Long %s truncated: %d lines total, %d shown. Alt+L expands the full output.]",
					preview, kindLabel, len(lines), previewLines)
				out = append(out, codexapp.TranscriptEntry{
					Kind: entry.Kind,
					Text: summary,
				})
				continue
			}

			out = append(out, entry)
		default:
			out = append(out, entry)
		}
	}
	return out
}

// detectRepetitiveContent looks for repeated blocks of lines using a sliding window.
// Returns the line index where repetition starts and how many repeated blocks were found,
// or (-1, 0) if no significant repetition is detected.
func detectRepetitiveContent(lines []string) (startIdx int, repeatCount int) {
	if len(lines) < openCodeRepetitionWindowLines*openCodeRepetitionThreshold {
		return -1, 0
	}
	// Try window sizes from the configured size down to 3
	for windowSize := openCodeRepetitionWindowLines; windowSize >= 3; windowSize-- {
		for start := 0; start+windowSize*(openCodeRepetitionThreshold+1) <= len(lines); start++ {
			window := normalizeWindowLines(lines[start : start+windowSize])
			matches := 0
			pos := start + windowSize
			for pos+windowSize <= len(lines) {
				candidate := normalizeWindowLines(lines[pos : pos+windowSize])
				if window == candidate {
					matches++
					pos += windowSize
				} else {
					break
				}
			}
			if matches >= openCodeRepetitionThreshold {
				// Keep the first occurrence, report where repeats start
				return start + windowSize, matches
			}
		}
	}
	return -1, 0
}

func normalizeWindowLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(strings.TrimSpace(line))
		b.WriteByte('\n')
	}
	return b.String()
}

// toolEntrySummary extracts the meaningful summary from a tool entry,
// stripping the redundant "Tool <name> <status>:" prefix so that joined
// entries don't repeat it on every item.
func toolEntrySummary(entry codexapp.TranscriptEntry) string {
	compacted := strings.TrimSpace(compactCodexToolTranscriptText(entry.Text))
	if compacted == "" {
		return ""
	}
	parsed := parseToolTranscriptText(compacted)
	if parsed.Summary != "" {
		return parsed.Summary
	}
	// Fallback: use the compacted text if no summary was extracted
	return compacted
}

// toolEntryCommonName returns the dominant tool name from a set of entries,
// used to set the prefix once for a collapsed group.
func toolEntryCommonName(entries []codexapp.TranscriptEntry) string {
	counts := map[string]int{}
	for _, entry := range entries {
		parsed := parseToolTranscriptText(strings.TrimSpace(compactCodexToolTranscriptText(entry.Text)))
		if parsed.ToolName != "" {
			counts[parsed.ToolName]++
		}
	}
	best, bestCount := "", 0
	for name, count := range counts {
		if count > bestCount {
			best, bestCount = name, count
		}
	}
	return best
}

func summarizeOpenCodeToolRun(entries []codexapp.TranscriptEntry) codexapp.TranscriptEntry {
	if len(entries) == 0 {
		return codexapp.TranscriptEntry{}
	}
	if len(entries) <= maxOpenCodeCollapsedToolRun {
		return codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptTool,
			Text: joinOpenCodeToolRun(entries),
		}
	}
	previews := make([]string, 0, openCodeToolPreviewCount)
	for _, entry := range entries {
		summary := toolEntrySummary(entry)
		if summary == "" {
			continue
		}
		previews = append(previews, summary)
		if len(previews) >= openCodeToolPreviewCount {
			break
		}
	}
	if len(previews) == 0 {
		return codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptTool,
			Text: fmt.Sprintf("Tool activity: %d updates", len(entries)),
		}
	}
	toolName := toolEntryCommonName(entries)
	prefix := "Tool activity"
	if toolName != "" {
		prefix = "Tool " + toolName
	}
	text := prefix + ": " + strings.Join(previews, " | ")
	remaining := len(entries) - len(previews)
	if remaining > 0 {
		text += fmt.Sprintf(" | +%d more tool updates", remaining)
	}
	return codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptTool,
		Text: truncateText(text, openCodeCollapsedToolPreviewMaxText),
	}
}

func joinOpenCodeToolRun(entries []codexapp.TranscriptEntry) string {
	// If all entries share the same tool name, use it as a single prefix
	commonName := toolEntryCommonName(entries)
	summaries := make([]string, 0, len(entries))
	for _, entry := range entries {
		summary := toolEntrySummary(entry)
		if summary == "" {
			continue
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		return ""
	}
	if commonName != "" {
		return "Tool " + commonName + ": " + strings.Join(summaries, " | ")
	}
	return "Tool: " + strings.Join(summaries, " | ")
}

func compactCodexUserTranscriptText(text string) string {
	return text
}

func isCodexTranscriptAttachmentLine(line string) bool {
	return strings.HasPrefix(line, "[attached ") || strings.HasPrefix(line, "[attachment]")
}

func sanitizeCodexRenderedText(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = ansi.Strip(text)

	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case unicode.IsControl(r):
			continue
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func parseLegacyCodexTranscript(transcript string) []codexapp.TranscriptEntry {
	blocks := splitLegacyCodexTranscriptBlocks(transcript)
	if len(blocks) == 0 {
		return nil
	}
	entries := make([]codexapp.TranscriptEntry, 0, len(blocks))
	for _, block := range blocks {
		kind, text := parseLegacyCodexTranscriptBlock(block)
		if strings.TrimSpace(text) == "" {
			continue
		}
		entries = append(entries, codexapp.TranscriptEntry{Kind: kind, Text: text})
	}
	return entries
}

func splitLegacyCodexTranscriptBlocks(transcript string) []string {
	lines := strings.Split(strings.TrimSpace(transcript), "\n")
	blocks := make([]string, 0, len(lines))
	current := make([]string, 0, 4)
	flush := func() {
		if len(current) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(current, "\n"))
		current = current[:0]
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

func parseLegacyCodexTranscriptBlock(block string) (codexapp.TranscriptKind, string) {
	switch {
	case legacyTranscriptBlockHasPrefix(block, "You: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "You: ")
		return codexapp.TranscriptUser, text
	case legacyTranscriptBlockHasPrefix(block, "Codex: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Codex: ")
		return codexapp.TranscriptAgent, text
	case legacyTranscriptBlockHasPrefix(block, "Plan: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Plan: ")
		return codexapp.TranscriptPlan, text
	case legacyTranscriptBlockHasPrefix(block, "Reasoning: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Reasoning: ")
		return codexapp.TranscriptReasoning, text
	case legacyTranscriptBlockHasPrefix(block, "[status] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[status] ")
		return codexapp.TranscriptStatus, text
	case legacyTranscriptBlockHasPrefix(block, "[system] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[system] ")
		return codexapp.TranscriptSystem, text
	case legacyTranscriptBlockHasPrefix(block, "[error] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[error] ")
		return codexapp.TranscriptError, text
	default:
		return codexapp.TranscriptOther, strings.TrimSpace(block)
	}
}

func legacyTranscriptBlockHasPrefix(block, prefix string) bool {
	_, ok := trimLegacyCodexTranscriptPrefix(block, prefix)
	return ok
}

func trimLegacyCodexTranscriptPrefix(block, prefix string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], prefix) {
		return "", false
	}
	lines[0] = strings.TrimPrefix(lines[0], prefix)
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}

type codexStatusBlockData struct {
	ThreadID           string
	ProjectPath        string
	CWD                string
	Model              string
	ModelProvider      string
	ReasoningEffort    string
	Agent              string
	ServiceTier        string
	Approval           string
	Sandbox            string
	Network            string
	WritableRoots      string
	ContextTokens      int64
	TotalTokens        int64
	ModelContextWindow int64
	ContextUsedPercent int
	HasContextPercent  bool
	LastTurnTokens     int64
	UsageWindows       []codexStatusUsageWindow
}

type codexStatusUsageWindow struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    time.Time
}

type codexStatusUsageGroup struct {
	Limit   string
	Plan    string
	Windows []codexStatusUsageWindow
}

func renderCodexStatusBlock(body string, width int) string {
	status, ok := parseCodexStatusBlock(body)
	if !ok {
		return renderCodexMessageBlock("Status", body, lipgloss.Color("81"), lipgloss.Color("252"), width)
	}
	contentWidth := max(20, width-2)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	groupStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("153"))

	lines := []string{titleStyle.Render("Status")}
	lines = append(lines, renderCodexStatusSummaryRows(status, contentWidth)...)

	groups := groupCodexStatusUsageWindows(status.UsageWindows)
	if len(groups) > 0 {
		lines = append(lines, "")
		lines = append(lines, sectionStyle.Render("Usage left"))
		for index, group := range groups {
			if index > 0 {
				lines = append(lines, "")
			}
			title := group.Limit
			if strings.TrimSpace(group.Plan) != "" {
				title += " (" + group.Plan + ")"
			}
			lines = append(lines, groupStyle.Render(title))
			for _, window := range group.Windows {
				lines = append(lines, renderCodexStatusUsageRow(window, contentWidth))
			}
		}
	}

	footerRows := renderCodexStatusFooterRows(status)
	if len(footerRows) > 0 {
		lines = append(lines, "")
		lines = append(lines, footerRows...)
	}

	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("81")).
		PaddingLeft(0).
		Render(strings.Join(lines, "\n"))
}

func parseCodexStatusBlock(body string) (codexStatusBlockData, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return codexStatusBlockData{}, false
	}
	switch strings.TrimSpace(lines[0]) {
	case "Embedded Codex status", "Embedded OpenCode status":
	default:
		return codexStatusBlockData{}, false
	}
	status := codexStatusBlockData{}
	for _, rawLine := range lines[1:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "usage window:") {
			window, ok := parseCodexStatusUsageWindow(strings.TrimSpace(strings.TrimPrefix(line, "usage window:")))
			if ok {
				status.UsageWindows = append(status.UsageWindows, window)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "thread":
			status.ThreadID = value
		case "project":
			status.ProjectPath = value
		case "cwd":
			status.CWD = value
		case "model":
			status.Model = value
		case "model provider":
			status.ModelProvider = value
		case "reasoning effort":
			status.ReasoningEffort = value
		case "agent":
			status.Agent = value
		case "service tier":
			status.ServiceTier = value
		case "approval":
			status.Approval = value
		case "sandbox":
			status.Sandbox = value
		case "network":
			status.Network = value
		case "writable roots":
			status.WritableRoots = value
		case "total tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.TotalTokens = parsed
			}
		case "context tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ContextTokens = parsed
			}
		case "model context window":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.ModelContextWindow = parsed
			}
		case "context used percent":
			if parsed, err := strconv.Atoi(value); err == nil {
				status.ContextUsedPercent = clampCodexStatusPercent(parsed)
				status.HasContextPercent = true
			}
		case "last turn tokens":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				status.LastTurnTokens = parsed
			}
		}
	}
	return status, true
}

func parseCodexStatusUsageWindow(spec string) (codexStatusUsageWindow, bool) {
	window := codexStatusUsageWindow{}
	hasLimit := false
	hasWindow := false
	hasLeft := false
	for _, rawPart := range strings.Split(spec, ";") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "limit":
			window.Limit = value
			hasLimit = value != ""
		case "plan":
			window.Plan = value
		case "window":
			window.Window = value
			hasWindow = value != ""
		case "left":
			if parsed, err := strconv.Atoi(value); err == nil {
				window.LeftPercent = clampCodexStatusPercent(parsed)
				hasLeft = true
			}
		case "resetsat":
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
				window.ResetsAt = time.Unix(parsed, 0).Local()
			}
		}
	}
	if !hasLimit || !hasWindow || !hasLeft {
		return codexStatusUsageWindow{}, false
	}
	return window, true
}

func renderCodexStatusSummaryRows(status codexStatusBlockData, width int) []string {
	labelWidth := 11
	rows := make([]string, 0, 6)
	modelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	reasoningStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	valueStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	if status.Model != "" {
		value := modelStyle.Render(status.Model)
		extras := make([]string, 0, 2)
		if status.ModelProvider != "" {
			extras = append(extras, status.ModelProvider)
		}
		if status.ServiceTier != "" && status.Agent == "" {
			if strings.HasPrefix(status.ServiceTier, "agent ") {
				extras = append(extras, status.ServiceTier)
			} else {
				extras = append(extras, "tier "+status.ServiceTier)
			}
		}
		if len(extras) > 0 {
			value += " " + mutedStyle.Render("("+strings.Join(extras, ", ")+")")
		}
		rows = append(rows, renderCodexStatusField("Model", value, labelWidth))
	}
	if status.ReasoningEffort != "" {
		rows = append(rows, renderCodexStatusField("Reasoning", reasoningStyle.Render(status.ReasoningEffort), labelWidth))
	}
	if status.Agent != "" {
		rows = append(rows, renderCodexStatusField("Agent", valueStyle.Render(status.Agent), labelWidth))
	}
	directory := status.CWD
	if strings.TrimSpace(directory) == "" {
		directory = status.ProjectPath
	}
	if directory != "" {
		rows = append(rows, renderCodexStatusField("Directory", valueStyle.Render(directory), labelWidth))
	}
	contextTokens := status.ContextTokens
	if contextTokens <= 0 {
		contextTokens = status.TotalTokens
	}
	if contextTokens > 0 {
		contextValue := valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(contextTokens)))
		if status.ModelContextWindow > 0 {
			details := fmt.Sprintf("of %s", formatInt64(status.ModelContextWindow))
			if status.HasContextPercent {
				details += fmt.Sprintf(" (%d%% used)", status.ContextUsedPercent)
			}
			contextValue += " " + mutedStyle.Render(details)
		}
		rows = append(rows, renderCodexStatusField("Context", contextValue, labelWidth))
	}
	if status.LastTurnTokens > 0 {
		rows = append(rows, renderCodexStatusField("Last turn", valueStyle.Render(fmt.Sprintf("%s tokens", formatInt64(status.LastTurnTokens))), labelWidth))
	}
	return rows
}

func renderCodexStatusFooterRows(status codexStatusBlockData) []string {
	labelWidth := 11
	mutedValueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	rows := make([]string, 0, 2)

	accessParts := make([]string, 0, 3)
	if status.Approval != "" {
		accessParts = append(accessParts, status.Approval)
	}
	if status.Sandbox != "" {
		accessParts = append(accessParts, status.Sandbox)
	}
	if status.Network != "" {
		accessParts = append(accessParts, "network "+status.Network)
	}
	if len(accessParts) > 0 {
		rows = append(rows, renderCodexStatusField("Access", mutedValueStyle.Render(strings.Join(accessParts, " | ")), labelWidth))
	}
	if status.WritableRoots != "" {
		rows = append(rows, renderCodexStatusField("Writable", mutedValueStyle.Render(status.WritableRoots), labelWidth))
	}
	if status.ThreadID != "" {
		rows = append(rows, renderCodexStatusField("Session", mutedValueStyle.Render(status.ThreadID), labelWidth))
	}
	return rows
}

func groupCodexStatusUsageWindows(windows []codexStatusUsageWindow) []codexStatusUsageGroup {
	groups := make([]codexStatusUsageGroup, 0, len(windows))
	indexByKey := make(map[string]int)
	for _, window := range windows {
		key := strings.ToLower(window.Limit) + "|" + strings.ToLower(window.Plan)
		index, ok := indexByKey[key]
		if !ok {
			index = len(groups)
			indexByKey[key] = index
			groups = append(groups, codexStatusUsageGroup{
				Limit: window.Limit,
				Plan:  window.Plan,
			})
		}
		groups[index].Windows = append(groups[index].Windows, window)
	}
	return groups
}

func renderCodexStatusUsageRow(window codexStatusUsageWindow, width int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(12)
	resetStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	percentStyle := codexStatusUsagePercentStyle(window.LeftPercent)
	label := codexStatusWindowTitle(window.Window)
	bar := renderCodexStatusProgressBar(window.LeftPercent, codexStatusProgressBarWidth(width))
	leftText := percentStyle.Render(fmt.Sprintf("%3d%% left", window.LeftPercent))
	base := labelStyle.Render(label) + " " + bar + " " + leftText
	resetText := formatCodexStatusReset(window.ResetsAt)
	if resetText == "" {
		return base
	}
	resetRendered := resetStyle.Render(resetText)
	if lipgloss.Width(base+" "+resetRendered) <= width {
		return base + " " + resetRendered
	}
	return base + "\n" + lipgloss.NewStyle().MarginLeft(13).Foreground(lipgloss.Color("244")).Render(resetText)
}

func renderCodexStatusProgressBar(leftPercent, width int) string {
	if width <= 0 {
		width = 10
	}
	filled := (clampCodexStatusPercent(leftPercent)*width + 50) / 100
	if filled > width {
		filled = width
	}
	empty := width - filled
	fillStyle := codexStatusUsagePercentStyle(leftPercent)
	frameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	return frameStyle.Render("[") +
		fillStyle.Render(strings.Repeat("=", filled)) +
		emptyStyle.Render(strings.Repeat("-", empty)) +
		frameStyle.Render("]")
}

func renderCodexStatusField(label, value string, labelWidth int) string {
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(labelWidth)
	return labelStyle.Render(label+":") + " " + value
}

func codexStatusProgressBarWidth(width int) int {
	switch {
	case width >= 84:
		return 22
	case width >= 66:
		return 18
	case width >= 52:
		return 14
	default:
		return 10
	}
}

func codexStatusUsagePercentStyle(leftPercent int) lipgloss.Style {
	switch {
	case leftPercent >= 75:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	case leftPercent >= 40:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	}
}

func codexStatusWindowTitle(window string) string {
	window = strings.TrimSpace(window)
	switch strings.ToLower(window) {
	case "":
		return "Limit"
	case "weekly":
		return "Weekly limit"
	}
	return window + " limit"
}

func formatCodexStatusReset(reset time.Time) string {
	if reset.IsZero() {
		return ""
	}
	now := time.Now().In(reset.Location())
	if sameCodexStatusDay(now, reset) {
		return "resets " + reset.Format("15:04")
	}
	if now.Year() == reset.Year() {
		return "resets " + reset.Format("15:04 on 2 Jan")
	}
	return "resets " + reset.Format("15:04 on 2 Jan 2006")
}

func sameCodexStatusDay(left, right time.Time) bool {
	left = left.In(right.Location())
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}

func clampCodexStatusPercent(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

func formatInt64(value int64) string {
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	if len(digits) <= 3 {
		return sign + digits
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3)
	for index, r := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return sign + out.String()
}

func renderCodexMessageBlock(label, body string, accent, bodyColor lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle(label, body, accent, bodyColor, width, false)
}

func renderCodexCompactTranscriptLine(body string, accent lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle("", body, accent, accent, width, false)
}

func renderCodexUserMessageBlock(body string, width int) string {
	return renderCodexMessageBlockWithStyle("", body, lipgloss.Color("81"), lipgloss.Color("252"), width, true)
}

func renderCodexMessageBlockWithStyle(label, body string, accent, bodyColor lipgloss.Color, width int, shaded bool) string {
	paddingRight := 0
	style := lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1)
	if shaded {
		paddingRight = 1
		style = style.PaddingRight(1).Background(codexComposerShellColor)
	}
	contentWidth := max(10, width-2-paddingRight)
	lines := []string{}
	if strings.TrimSpace(label) != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label))
	}
	lines = append(lines, renderCodexBody(body, bodyColor, contentWidth))
	return style.Render(strings.Join(lines, "\n"))
}

var reasoningBackgroundColor = lipgloss.Color("235")

func renderReasoningBlock(body string, width int) string {
	contentWidth := max(10, width-4)
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("180")).Faint(true).Render("Reasoning")
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(contentWidth)
	wrappedBody := bodyStyle.Render(renderCodexBody(body, lipgloss.Color("252"), contentWidth))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("180")).
		PaddingLeft(0).
		PaddingRight(1).
		Background(reasoningBackgroundColor).
		Render(label + "\n" + wrappedBody)
}

// renderReasoningIndicator renders a compact single-line indicator for hidden
// reasoning content instead of showing nothing (which causes visible content flashes
// as reasoning entries appear and disappear during streaming).
func renderReasoningIndicator(lineCount int, width int) string {
	accent := lipgloss.Color("180")
	label := lipgloss.NewStyle().Foreground(accent).Faint(true).Render("Thinking…")
	plural := "lines"
	if lineCount == 1 {
		plural = "line"
	}
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(
		fmt.Sprintf(" (%d %s, Alt+L expands)", lineCount, plural))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Width(width).
		Render(label + detail)
}

func renderCodexComposer(input textarea.Model, width int) string {
	if width <= 0 {
		width = input.Width() + 4
	}
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(codexComposerShellColor).
		Foreground(lipgloss.Color("252")).
		Render(input.View())
}

func renderCodexMonospaceBlock(label, body string, accent lipgloss.Color, width int) string {
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label)
	renderedLines := make([]string, 0, len(strings.Split(body, "\n")))
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "$ "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(line))
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(line))
		case strings.HasPrefix(line, "@@"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render(line))
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "+"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(line))
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "-"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(line))
		case strings.HasPrefix(line, "# "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case strings.HasPrefix(line, "[command ") && !strings.Contains(line, "exit 0]"):
			// Non-zero exit — render as warning
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render(line))
		case strings.HasPrefix(line, "[command "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case strings.HasPrefix(line, "[file changes "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true).Render(line))
		default:
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(line))
		}
	}
	bodyBlock := lipgloss.NewStyle().Width(contentWidth).Render(strings.Join(renderedLines, "\n"))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Render(title + "\n" + bodyBlock)
}

func renderCodexDenseBlock(label, body string, accent lipgloss.Color, width int, expanded bool) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return ""
	}
	// In collapsed mode, strip low-value noise from command blocks
	if !expanded {
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Hide successful exit lines — failure is the interesting case
			if trimmed == "[command completed, exit 0]" {
				continue
			}
			// Dim cwd comments but keep them only in expanded mode
			if strings.HasPrefix(trimmed, "# cwd:") {
				continue
			}
			filtered = append(filtered, line)
		}
		lines = filtered
	}
	if len(lines) == 0 {
		return ""
	}
	const compactLimit = 8
	hidden := 0
	if !expanded && len(lines) > compactLimit {
		hidden = len(lines) - compactLimit
		lines = lines[:compactLimit]
	}
	title := label
	if hidden > 0 {
		title = fmt.Sprintf("%s (%d more lines hidden; Alt+L expands)", label, hidden)
	}
	return renderCodexMonospaceBlock(title, strings.Join(lines, "\n"), accent, width)
}

func renderCodexBody(body string, color lipgloss.Color, width int) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	fenceLanguage := ""
	fenceLines := []string{}
	tableRows := []string{}

	flushFence := func() {
		if len(fenceLines) == 0 {
			return
		}
		highlighted := syntaxHighlightBlock(strings.Join(fenceLines, "\n"), fenceLanguage, "", syntaxHighlightOptions{
			DefaultColor: lipgloss.Color("180"),
		})
		out = append(out, strings.Split(highlighted, "\n")...)
		fenceLines = nil
	}
	flushTable := func() {
		if len(tableRows) == 0 {
			return
		}
		out = append(out, renderCodexMarkdownTable(tableRows, color, width)...)
		tableRows = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flushTable()
			if inFence {
				flushFence()
				inFence = false
				fenceLanguage = ""
			} else {
				inFence = true
				fenceLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case inFence:
			fenceLines = append(fenceLines, line)
		case isMarkdownTableRow(trimmed):
			tableRows = append(tableRows, trimmed)
		default:
			flushTable()
			switch {
			case strings.HasPrefix(trimmed, "[attached image]"):
				out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true)))
			case strings.HasPrefix(trimmed, "### "):
				out = append(out, renderCodexInlineMarkdown(strings.TrimPrefix(trimmed, "### "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "## "):
				out = append(out, renderCodexInlineMarkdown(strings.TrimPrefix(trimmed, "## "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "# "):
				out = append(out, renderCodexInlineMarkdown(strings.TrimPrefix(trimmed, "# "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "> "):
				out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)))
			case isMarkdownHorizontalRule(trimmed):
				rule := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true).Render(strings.Repeat("─", min(width, 40)))
				out = append(out, rule)
			case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
				out = append(out, renderCodexInlineMarkdown("• "+strings.TrimSpace(trimmed[2:]), lipgloss.NewStyle().Foreground(lipgloss.Color("151"))))
			case isMarkdownNumberedListItem(trimmed):
				num, content := parseMarkdownNumberedListItem(trimmed)
				numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
				out = append(out, numStyle.Render(num+".")+renderCodexInlineMarkdown(" "+content, lipgloss.NewStyle().Foreground(lipgloss.Color("151"))))
			default:
				out = append(out, renderCodexInlineMarkdown(line, lipgloss.NewStyle().Foreground(color)))
			}
		}
	}
	if inFence {
		flushFence()
	}
	flushTable()
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 3
}

func isMarkdownTableSeparator(line string) bool {
	if !isMarkdownTableRow(line) {
		return false
	}
	inner := strings.Trim(line, "|")
	for _, cell := range strings.Split(inner, "|") {
		cell = strings.TrimSpace(cell)
		cleaned := strings.Trim(cell, ":-")
		if cleaned != "" {
			return false
		}
	}
	return true
}

func renderCodexMarkdownTable(rows []string, color lipgloss.Color, maxWidth int) []string {
	if len(rows) == 0 {
		return nil
	}
	// Parse all rows into cells
	parsed := make([][]string, 0, len(rows))
	separatorIdxs := map[int]bool{}
	for i, row := range rows {
		if isMarkdownTableSeparator(row) {
			separatorIdxs[i] = true
			parsed = append(parsed, nil)
			continue
		}
		inner := strings.Trim(strings.TrimSpace(row), "|")
		cells := strings.Split(inner, "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		parsed = append(parsed, cells)
	}

	// Compute column widths
	numCols := 0
	for _, cells := range parsed {
		if len(cells) > numCols {
			numCols = len(cells)
		}
	}
	if numCols == 0 {
		return nil
	}
	colWidths := make([]int, numCols)
	for _, cells := range parsed {
		for j, cell := range cells {
			if len(cell) > colWidths[j] {
				colWidths[j] = len(cell)
			}
		}
	}

	// Cap column widths so table fits within maxWidth (account for separators: " | " between cols + "| " prefix + " |" suffix)
	tableOverhead := 2 + numCols*3 - 1 // "| " + " | " * (n-1) + " |"
	totalWidth := tableOverhead
	for _, w := range colWidths {
		totalWidth += w
	}
	if totalWidth > maxWidth && maxWidth > tableOverhead+numCols {
		available := maxWidth - tableOverhead
		for i, w := range colWidths {
			remaining := numCols - i
			maxCol := available / remaining
			if maxCol < 1 {
				maxCol = 1
			}
			if w > maxCol {
				colWidths[i] = maxCol
			}
			available -= colWidths[i]
			if available < 0 {
				available = 0
			}
		}
	}
	for i, w := range colWidths {
		if w < 1 {
			colWidths[i] = 1
		}
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	cellStyle := lipgloss.NewStyle().Foreground(color)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	out := make([]string, 0, len(rows))
	for i, cells := range parsed {
		if separatorIdxs[i] {
			// Render separator line
			parts := make([]string, numCols)
			for j := range parts {
				parts[j] = strings.Repeat("─", colWidths[j])
			}
			out = append(out, borderStyle.Render("├─"+strings.Join(parts, "─┼─")+"─┤"))
			continue
		}
		// Render data row
		isHeader := i == 0 && len(parsed) > 1 && separatorIdxs[1]
		parts := make([]string, numCols)
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(cells) {
				cell = cells[j]
			}
			// Truncate if needed
			w := colWidths[j]
			if len(cell) > w {
				if w > 1 {
					cell = cell[:w-1] + "…"
				} else {
					cell = "…"
				}
			}
			pad := w - len(cell)
			if pad < 0 {
				pad = 0
			}
			padded := cell + strings.Repeat(" ", pad)
			if isHeader {
				parts[j] = headerStyle.Render(padded)
			} else {
				parts[j] = cellStyle.Render(padded)
			}
		}
		sep := borderStyle.Render(" │ ")
		out = append(out, borderStyle.Render("│ ")+strings.Join(parts, sep)+borderStyle.Render(" │"))
	}
	return out
}

func isMarkdownHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	// Must be only dashes, asterisks, or underscores (with optional spaces)
	cleaned := strings.ReplaceAll(line, " ", "")
	if len(cleaned) < 3 {
		return false
	}
	ch := cleaned[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for _, r := range cleaned {
		if byte(r) != ch {
			return false
		}
	}
	return true
}

func isMarkdownNumberedListItem(line string) bool {
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i < len(line)-1 && line[i+1] == ' ' {
			return true
		}
		return false
	}
	return false
}

func parseMarkdownNumberedListItem(line string) (num, content string) {
	dotIdx := strings.IndexByte(line, '.')
	if dotIdx < 0 {
		return "", line
	}
	return line[:dotIdx], strings.TrimSpace(line[dotIdx+1:])
}

func renderCodexInlineMarkdown(text string, style lipgloss.Style) string {
	if text == "" {
		return style.Render(text)
	}
	codeStyle := style.Copy().
		Foreground(lipgloss.Color("223")).
		Background(lipgloss.Color("236"))
	var out strings.Builder
	remaining := text
	for len(remaining) > 0 {
		// Find the earliest markdown marker: **, *, [, or `
		boldIdx := strings.Index(remaining, "**")
		italicIdx := -1
		linkIdx := strings.IndexByte(remaining, '[')
		codeIdx := strings.IndexByte(remaining, '`')

		// Find standalone * (italic) that is not part of **
		for i := 0; i < len(remaining); i++ {
			if remaining[i] == '*' {
				if i+1 < len(remaining) && remaining[i+1] == '*' {
					i++ // skip **
					continue
				}
				italicIdx = i
				break
			}
		}

		// Find earliest marker
		earliest := -1
		for _, idx := range []int{boldIdx, italicIdx, linkIdx, codeIdx} {
			if idx >= 0 && (earliest < 0 || idx < earliest) {
				earliest = idx
			}
		}
		if earliest < 0 {
			out.WriteString(style.Render(remaining))
			break
		}

		// Render text before the marker
		if earliest > 0 {
			out.WriteString(style.Render(remaining[:earliest]))
		}

		// Process the marker
		switch {
		case boldIdx == earliest:
			// Look for closing **
			close := strings.Index(remaining[earliest+2:], "**")
			if close < 0 || close == 0 {
				out.WriteString(style.Render("**"))
				remaining = remaining[earliest+2:]
				continue
			}
			inner := remaining[earliest+2 : earliest+2+close]
			out.WriteString(style.Copy().Bold(true).Render(inner))
			remaining = remaining[earliest+2+close+2:]

		case italicIdx == earliest:
			// Look for closing * (not **)
			rest := remaining[earliest+1:]
			close := -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == '*' {
					if i+1 < len(rest) && rest[i+1] == '*' {
						i++ // skip **
						continue
					}
					close = i
					break
				}
			}
			if close <= 0 {
				out.WriteString(style.Render("*"))
				remaining = remaining[earliest+1:]
				continue
			}
			inner := rest[:close]
			out.WriteString(style.Copy().Italic(true).Render(inner))
			remaining = rest[close+1:]

		case linkIdx == earliest:
			label, target, consumed, ok := parseCodexMarkdownLink(remaining[earliest:])
			if !ok {
				out.WriteString(style.Render("["))
				remaining = remaining[earliest+1:]
				continue
			}
			out.WriteString(renderCodexHyperlink(label, target, style))
			remaining = remaining[earliest+consumed:]

		case codeIdx == earliest:
			rest := remaining[earliest+1:]
			close := strings.IndexByte(rest, '`')
			if close <= 0 {
				out.WriteString(style.Render("`"))
				remaining = rest
				continue
			}
			inner := rest[:close]
			out.WriteString(codeStyle.Render(inner))
			remaining = rest[close+1:]
		}
	}
	return out.String()
}

func parseCodexMarkdownLink(text string) (label, target string, consumed int, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", 0, false
	}
	closeLabel := strings.Index(text, "](")
	if closeLabel <= 1 {
		return "", "", 0, false
	}
	closeTarget := strings.IndexByte(text[closeLabel+2:], ')')
	if closeTarget < 0 {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	target = text[closeLabel+2 : closeLabel+2+closeTarget]
	if strings.TrimSpace(label) == "" || strings.TrimSpace(target) == "" {
		return "", "", 0, false
	}
	return label, target, closeLabel + 3 + closeTarget, true
}

func renderCodexHyperlink(label, target string, style lipgloss.Style) string {
	target = codexHyperlinkTarget(target)
	linkStyle := style.Copy().Foreground(lipgloss.Color("111")).Underline(true)
	renderedLabel := linkStyle.Render(label)
	if target == "" {
		return renderedLabel
	}
	return ansi.SetHyperlink(target) + renderedLabel + ansi.ResetHyperlink()
}

func codexHyperlinkTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") {
		path := target
		fragment := ""
		if before, after, found := strings.Cut(path, "#"); found {
			path = before
			fragment = after
		}
		return (&url.URL{Scheme: "file", Path: path, Fragment: fragment}).String()
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return target
	}
	return parsed.String()
}

func numericOptionSelection(key string) (int, bool) {
	if len(key) != 1 {
		return 0, false
	}
	if key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	return int(key[0] - '1'), true
}

func encodeElicitationComposerInput(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func countRenderedBlockLines(blocks []string) int {
	total := 0
	for _, block := range blocks {
		if block == "" {
			total++
			continue
		}
		total += strings.Count(block, "\n") + 1
	}
	return total
}

// codexViewportScreenTop returns the screen Y where the viewport content area
// begins (banner line + top border of the framed pane).
const codexViewportScreenTop = 2

// finalizeCodexSelection copies the selected text to the clipboard and
// clears the dragging state. It is called on mouse release and also as a
// fallback when a release event is missed (e.g. released over a non-tracked
// area like the banner).
func (m *Model) finalizeCodexSelection() {
	m.codexSelection.dragging = false
	if m.codexSelection.hasRange() {
		text := cleanCopiedText(m.codexSelection.extractText(m.codexTranscriptCache.rendered))
		if text != "" {
			if err := clipboardTextWriter(text); err == nil {
				m.status = "Copied selection to clipboard"
			} else {
				m.reportError("Selection copy failed", err, m.codexVisibleProject)
			}
		}
	}
}

// handleCodexMouseSelection processes left-button press/drag/release for text
// selection in the codex viewport. Returns (cmd, true) if the event was
// consumed, or (nil, false) to let the viewport handle it (e.g. scroll wheel).
func (m *Model) handleCodexMouseSelection(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch msg.Action {
	case tea.MouseActionPress:
		// If we're still dragging from a previous cycle (missed release),
		// finalize that selection first.
		if m.codexSelection.dragging {
			m.finalizeCodexSelection()
		}
		if msg.Button != tea.MouseButtonLeft {
			m.codexSelection = textSelection{}
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if !ok {
			m.codexSelection = textSelection{}
			return nil, false
		}
		m.codexSelection = textSelection{
			anchorRow:  row,
			anchorCol:  col,
			currentRow: row,
			currentCol: col,
			dragging:   true,
		}
		return nil, true

	case tea.MouseActionMotion:
		if !m.codexSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if ok {
			m.codexSelection.currentRow = row
			m.codexSelection.currentCol = col
		}
		// Always consume motion during drag to prevent fallthrough from
		// clearing the selection when the mouse leaves the viewport area.
		return nil, true

	case tea.MouseActionRelease:
		if !m.codexSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexMouseToContent(msg.X, msg.Y)
		if ok {
			m.codexSelection.currentRow = row
			m.codexSelection.currentCol = col
		}
		m.finalizeCodexSelection()
		return nil, true
	}
	return nil, false
}

// codexMouseToContent converts screen mouse coordinates to content row/col.
func (m *Model) codexMouseToContent(screenX, screenY int) (row, col int, ok bool) {
	visLine := screenY - codexViewportScreenTop
	if visLine < 0 || visLine >= m.codexViewport.Height {
		return 0, 0, false
	}
	contentRow := visLine + m.codexViewport.YOffset
	if contentRow >= m.codexViewport.TotalLineCount() {
		return 0, 0, false
	}
	col = max(0, screenX)
	return contentRow, col, true
}
