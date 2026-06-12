package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"strings"
	"time"
)

const (
	maxForceNewEmbeddedOpenAttempts     = 3
	maxOpenCodeCollapsedToolRun         = 5
	openCodeToolPreviewCount            = 3
	openCodeCollapsedToolPreviewMaxText = 180
	// Massive output caps apply only to non-answer internals. Assistant answers are
	// transcript content and should not have their tail clipped in collapsed mode.
	openCodeRepetitionWindowLines = 6   // sliding window size for repetition detection
	openCodeRepetitionThreshold   = 4   // consecutive repeated windows to trigger collapse
	openCodeMaxReasoningLines     = 120 // reasoning blocks get a tighter cap
	openCodeMaxReasoningPreview   = 12  // preview lines for reasoning
	codexDenseBlockPreviewLines   = 5
	codexTranscriptLiveEntryLimit = 480
	codexTranscriptLiveLineLimit  = 6000
	codexTranscriptLiveByteLimit  = 768 * 1024
	codexCacheMissEntryLimit      = 24
	codexCacheMissLineLimit       = 240
	codexCacheMissByteLimit       = 32 * 1024
)

var (
	codexBannerProjectStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	codexBannerMetaStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true)
	codexBannerSeparatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

type codexDenseBlockMode int

const (
	codexDenseBlockSummary codexDenseBlockMode = iota
	codexDenseBlockPreview
	codexDenseBlockFull
)

type embeddedPermissionSession interface {
	ShowPermissions() error
	SetPermissionLevel(level string) error
}

func (mode codexDenseBlockMode) normalized() codexDenseBlockMode {
	switch mode {
	case codexDenseBlockPreview, codexDenseBlockFull:
		return mode
	default:
		return codexDenseBlockSummary
	}
}

func (mode codexDenseBlockMode) full() bool {
	return mode.normalized() == codexDenseBlockFull
}

func (mode codexDenseBlockMode) next() codexDenseBlockMode {
	switch mode.normalized() {
	case codexDenseBlockSummary:
		return codexDenseBlockPreview
	case codexDenseBlockPreview:
		return codexDenseBlockFull
	default:
		return codexDenseBlockSummary
	}
}

func (mode codexDenseBlockMode) statusText() string {
	switch mode.normalized() {
	case codexDenseBlockPreview:
		return "Showing short transcript block previews"
	case codexDenseBlockFull:
		return "Showing full transcript blocks"
	default:
		return "Hiding transcript block output"
	}
}

func (mode codexDenseBlockMode) bannerText() string {
	switch mode.normalized() {
	case codexDenseBlockPreview:
		return "Blocks preview"
	case codexDenseBlockFull:
		return "Blocks full"
	default:
		return ""
	}
}

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
	if m.embeddedSidebarDiffs == nil {
		m.embeddedSidebarDiffs = make(map[string]embeddedSidebarDiffState)
	}
	if m.embeddedSidebarDiffAutoAt == nil {
		m.embeddedSidebarDiffAutoAt = make(map[string]time.Time)
	}
	if m.codexClosedHandled == nil {
		m.codexClosedHandled = make(map[string]struct{})
	}
	if m.codexToolAnswers == nil {
		m.codexToolAnswers = make(map[string]codexToolAnswerState)
	}
	if m.codexLCAgentStatusVisible == nil {
		m.codexLCAgentStatusVisible = make(map[string]struct{})
	}
	if m.codexSnapshots == nil {
		m.codexSnapshots = make(map[string]codexapp.Snapshot)
	}
	if m.codexTranscriptRev == nil {
		m.codexTranscriptRev = make(map[string]uint64)
	}
	if m.codexTranscriptFullHistory == nil {
		m.codexTranscriptFullHistory = make(map[string]struct{})
	}
	if m.codexArtifactLinkScans == nil {
		m.codexArtifactLinkScans = make(map[string]codexArtifactLinkScanState)
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
	case codexapp.ProviderLCAgent:
		return "/lcagent-new"
	default:
		return "/codex-new"
	}
}

func (m *Model) beginCodexPendingOpen(projectPath string, provider codexapp.Provider) {
	m.beginCodexPendingOpenWithVisibility(projectPath, provider, true)
}

func (m *Model) beginNewCodexPendingOpen(projectPath string, provider codexapp.Provider) {
	m.beginNewCodexPendingOpenWithVisibility(projectPath, provider, true)
}

func (m *Model) beginCodexPendingOpenWithVisibility(projectPath string, provider codexapp.Provider, showWhilePending bool) {
	m.beginCodexPendingOpenWithOptions(projectPath, provider, showWhilePending, false, true)
}

func (m *Model) beginNewCodexPendingOpenWithVisibility(projectPath string, provider codexapp.Provider, showWhilePending bool) {
	m.beginCodexPendingOpenWithOptions(projectPath, provider, showWhilePending, true, true)
}

func (m *Model) beginCodexPendingOpenWithVisibilityAndReveal(projectPath string, provider codexapp.Provider, showWhilePending, revealOnOpen bool) {
	m.beginCodexPendingOpenWithOptions(projectPath, provider, showWhilePending, false, revealOnOpen)
}

func (m *Model) beginNewCodexPendingOpenWithVisibilityAndReveal(projectPath string, provider codexapp.Provider, showWhilePending, revealOnOpen bool) {
	m.beginCodexPendingOpenWithOptions(projectPath, provider, showWhilePending, true, revealOnOpen)
}

func (m *Model) beginCodexPendingOpenWithOptions(projectPath string, provider codexapp.Provider, showWhilePending, newSession, revealOnOpen bool) {
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
		newSession:       newSession,
		hideOnOpen:       !revealOnOpen,
	}
}

func (m Model) revealPendingEmbeddedOpenOnSuccess(projectPath string) bool {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || m.codexPendingOpen == nil {
		return true
	}
	if normalizeProjectPath(m.codexPendingOpen.projectPath) != projectPath {
		return true
	}
	return !m.codexPendingOpen.hideOnOpen
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
	linkScanCmd := tea.Cmd(nil)
	if reveal {
		if cached, ok := m.codexCachedSnapshot(projectPath); ok {
			linkScanCmd = m.maybeStartCodexArtifactLinkScan(projectPath, cached)
		}
	}
	sidebarCmd := tea.Cmd(nil)
	if reveal {
		sidebarCmd = m.refreshEmbeddedSidebarCmd(projectPath)
	}
	return batchCmds(m.markProjectSessionSeen(projectPath), asyncCmd, linkScanCmd, sidebarCmd)
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

type codexTryStateSnapshooter interface {
	TryStateSnapshot() (codexapp.Snapshot, bool)
}

func stateSnapshotForCodexSession(session codexapp.Session) (codexapp.Snapshot, bool) {
	if session == nil {
		return codexapp.Snapshot{}, false
	}
	if state, ok := session.(codexTryStateSnapshooter); ok {
		return state.TryStateSnapshot()
	}
	return codexapp.Snapshot{}, false
}

func (m Model) cachedLiveCodexSnapshot(projectPath string) (codexapp.Snapshot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	cached, hasCached := m.codexCachedSnapshot(projectPath)
	if state, ok := stateSnapshotForCodexSession(session); ok {
		if state.Closed || !state.Started {
			return codexapp.Snapshot{}, false
		}
		if !hasCached {
			cached = state
			hasCached = true
		}
		cached.Closed = state.Closed
		cached.Started = state.Started
		cached.Busy = state.Busy
		cached.BusyExternal = state.BusyExternal
		cached.BusySince = state.BusySince
		cached.LastBusyActivityAt = state.LastBusyActivityAt
		cached.Phase = state.Phase
		cached.ActiveTurnID = state.ActiveTurnID
		cached.PendingApproval = state.PendingApproval
		cached.PendingToolInput = state.PendingToolInput
		cached.PendingElicitation = state.PendingElicitation
		cached.BrowserActivity = state.BrowserActivity
		cached.CurrentBrowserPageURL = state.CurrentBrowserPageURL
		cached.CurrentBrowserPageStale = state.CurrentBrowserPageStale
		cached.ManagedBrowserSessionKey = state.ManagedBrowserSessionKey
		cached.Status = state.Status
		cached.LastError = state.LastError
		cached.LastSystemNotice = state.LastSystemNotice
		cached.LastActivityAt = state.LastActivityAt
		cached.Goal = cloneCodexThreadGoal(state.Goal)
		if strings.TrimSpace(state.ThreadID) != "" {
			cached.ThreadID = state.ThreadID
		}
		if state.Provider.Normalized() != "" {
			cached.Provider = state.Provider
		}
	}
	if !hasCached {
		return codexapp.Snapshot{}, false
	}
	if !cached.Started || cached.Closed {
		return codexapp.Snapshot{}, false
	}
	return cached, true
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
	m.maybeApplyCodexSuggestedInputDraft(projectPath, snapshot)
}

func (m *Model) maybeApplyCodexSuggestedInputDraft(projectPath string, snapshot codexapp.Snapshot) {
	projectPath = strings.TrimSpace(projectPath)
	draftID := strings.TrimSpace(snapshot.SuggestedInputDraftID)
	text := strings.TrimSpace(snapshot.SuggestedInputDraft)
	if projectPath == "" || draftID == "" || text == "" {
		return
	}
	if m.codexSuggestedDraftsApplied == nil {
		m.codexSuggestedDraftsApplied = make(map[string]string)
	}
	if m.codexSuggestedDraftsApplied[projectPath] == draftID {
		return
	}
	if !m.currentCodexDraftFor(projectPath).Empty() {
		return
	}
	m.codexSuggestedDraftsApplied[projectPath] = draftID
	m.restoreCodexDraft(projectPath, codexDraft{Text: text})
	if strings.TrimSpace(m.codexVisibleProject) == projectPath {
		m.status = "LCAgent critic drafted a follow-up for review."
		m.syncCodexComposerSize()
	}
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
	delete(m.codexTranscriptFullHistory, projectPath)
	delete(m.codexLCAgentStatusVisible, projectPath)
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
	delete(m.codexArtifactLinkScans, projectPath)
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
		if left[i].ItemID != right[i].ItemID ||
			left[i].Kind != right[i].Kind ||
			left[i].Text != right[i].Text ||
			left[i].DisplayText != right[i].DisplayText ||
			!codexGeneratedImageArtifactsEqual(left[i].GeneratedImage, right[i].GeneratedImage) {
			return false
		}
	}
	return true
}

func codexGeneratedImageArtifactsEqual(left, right *codexapp.GeneratedImageArtifact) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID &&
		left.Path == right.Path &&
		left.SourcePath == right.SourcePath &&
		left.Width == right.Width &&
		left.Height == right.Height &&
		left.ByteSize == right.ByteSize &&
		bytes.Equal(left.PreviewData, right.PreviewData)
}

func (m Model) codexTranscriptRevision(projectPath string) uint64 {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return 0
	}
	return m.codexTranscriptRev[projectPath]
}

func (m Model) codexTranscriptFullHistoryLoaded(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || len(m.codexTranscriptFullHistory) == 0 {
		return false
	}
	_, ok := m.codexTranscriptFullHistory[projectPath]
	return ok
}

func (m *Model) loadFullCodexTranscriptHistory(projectPath string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexTranscriptFullHistory == nil {
		m.codexTranscriptFullHistory = make(map[string]struct{})
	}
	m.codexTranscriptFullHistory[projectPath] = struct{}{}
	m.resetCodexTranscriptCaches(projectPath)
}

func (m Model) isCodexLCAgentStatusVisible(projectPath string) bool {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || len(m.codexLCAgentStatusVisible) == 0 {
		return false
	}
	_, ok := m.codexLCAgentStatusVisible[projectPath]
	return ok
}

func (m *Model) setCodexLCAgentStatusVisible(projectPath string, visible bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return
	}
	if m.codexLCAgentStatusVisible == nil {
		m.codexLCAgentStatusVisible = make(map[string]struct{})
	}
	if visible {
		m.codexLCAgentStatusVisible[projectPath] = struct{}{}
		return
	}
	delete(m.codexLCAgentStatusVisible, projectPath)
}

func (m Model) codexTranscriptCacheMatches(projectPath string, width int) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" &&
		m.codexTranscriptCache.projectPath == projectPath &&
		m.codexTranscriptCache.width == width &&
		m.codexTranscriptCache.denseBlockMode == m.codexDenseBlockMode.normalized() &&
		m.codexTranscriptCache.fullHistory == m.codexTranscriptFullHistoryLoaded(projectPath) &&
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
	links := []codexTranscriptLinkSpan(nil)
	m.measureAISyncLatency("Embedded transcript render", projectPath, embeddedProvider(snapshot).Label(), func() {
		rendered, links = m.renderCodexTranscriptContentFromSnapshotWithLinksForProject(projectPath, snapshot, width)
	})
	m.codexTranscriptCache = codexTranscriptRenderCache{
		projectPath:    strings.TrimSpace(projectPath),
		width:          width,
		denseBlockMode: m.codexDenseBlockMode.normalized(),
		fullHistory:    m.codexTranscriptFullHistoryLoaded(projectPath),
		transcriptRev:  m.codexTranscriptRevision(projectPath),
		rendered:       rendered,
		links:          links,
	}
	return rendered
}

func (m Model) codexViewportContentMatches(projectPath string, width int) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" &&
		m.codexViewportContent.projectPath == projectPath &&
		m.codexViewportContent.width == width &&
		m.codexViewportContent.denseBlockMode == m.codexDenseBlockMode.normalized() &&
		m.codexViewportContent.fullHistory == m.codexTranscriptFullHistoryLoaded(projectPath) &&
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
		projectPath:    projectPath,
		width:          width,
		denseBlockMode: m.codexDenseBlockMode.normalized(),
		fullHistory:    m.codexTranscriptFullHistoryLoaded(projectPath),
		transcriptRev:  m.codexTranscriptRevision(projectPath),
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
	return m.openCodexSessionCmdWithVisibility(req, true)
}

func (m *Model) openCodexSessionCmdWithVisibility(req codexapp.LaunchRequest, revealOnOpen bool) tea.Cmd {
	req = m.applyEmbeddedModelPreference(req)
	manager := m.codexManager
	provider := req.Provider.Normalized()
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	if provider == codexapp.ProviderLCAgent {
		req.LCAgentPreflightAccess = true
		if req.RuntimeManager == nil {
			req.RuntimeManager = m.runtimeManager
		}
		if strings.TrimSpace(req.LCAgentPath) == "" {
			req.LCAgentPath = m.lcagentPath()
		}
		if strings.TrimSpace(req.LCAgentEnvFile) == "" {
			req.LCAgentEnvFile = m.lcagentEnvFile()
		}
		if strings.TrimSpace(req.LCAgentOpenAIAPIKey) == "" {
			req.LCAgentOpenAIAPIKey = m.openAIAPIKey()
		}
		if strings.TrimSpace(req.LCAgentOpenRouterAPIKey) == "" {
			req.LCAgentOpenRouterAPIKey = m.openRouterAPIKey()
		}
		if strings.TrimSpace(req.LCAgentDeepSeekAPIKey) == "" {
			req.LCAgentDeepSeekAPIKey = m.deepSeekAPIKey()
		}
		if strings.TrimSpace(req.LCAgentMoonshotAPIKey) == "" {
			req.LCAgentMoonshotAPIKey = m.moonshotAPIKey()
		}
		if strings.TrimSpace(req.LCAgentXiaomiAPIKey) == "" {
			req.LCAgentXiaomiAPIKey = m.xiaomiAPIKey()
		}
		if strings.TrimSpace(req.LCAgentXiaomiBaseURL) == "" {
			req.LCAgentXiaomiBaseURL = m.xiaomiBaseURL()
		}
		if strings.TrimSpace(req.LCAgentRoutePreset) == "" {
			req.LCAgentRoutePreset = m.lcagentRoutePreset()
		}
		if strings.TrimSpace(req.LCAgentProvider) == "" {
			req.LCAgentProvider = m.lcagentProvider()
		}
		if strings.TrimSpace(req.LCAgentAuto) == "" {
			req.LCAgentAuto = m.lcagentAuto()
		}
		if !req.LCAgentAdminWrite {
			req.LCAgentAdminWrite = m.lcagentAdminWrite()
		}
		if strings.TrimSpace(req.LCAgentToolProfile) == "" {
			req.LCAgentToolProfile = m.lcagentToolProfile()
		}
		if strings.TrimSpace(req.LCAgentContextProfile) == "" {
			req.LCAgentContextProfile = m.lcagentContextProfile()
		}
		if req.LCAgentRequestTimeout <= 0 {
			req.LCAgentRequestTimeout = m.lcagentRequestTimeout()
		}
		if strings.TrimSpace(req.LCAgentUtilityProvider) == "" {
			req.LCAgentUtilityProvider = m.lcagentUtilityProvider()
		}
		if strings.TrimSpace(req.LCAgentUtilityModel) == "" {
			req.LCAgentUtilityModel = m.lcagentUtilityModel()
		}
		if strings.TrimSpace(req.LCAgentCriticProvider) == "" {
			req.LCAgentCriticProvider = m.lcagentCriticProvider()
		}
		if strings.TrimSpace(req.LCAgentCriticModel) == "" {
			req.LCAgentCriticModel = m.lcagentCriticModel()
		}
		if strings.TrimSpace(req.LCAgentWebSearchBackend) == "" {
			req.LCAgentWebSearchBackend = m.lcagentWebSearchBackend()
		}
		if strings.TrimSpace(req.LCAgentWebSearchAPIKey) == "" {
			req.LCAgentWebSearchAPIKey = m.lcagentWebSearchAPIKey()
		}
		if strings.TrimSpace(req.LCAgentWebSearchEngineID) == "" {
			req.LCAgentWebSearchEngineID = m.lcagentWebSearchEngineID()
		}
		if strings.TrimSpace(req.LCAgentWebSearchURL) == "" {
			req.LCAgentWebSearchURL = m.lcagentWebSearchURL()
		}
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
		if provider == codexapp.ProviderLCAgent && strings.TrimSpace(req.Prompt) != "" {
			if err := codexapp.CheckLCAgentProviderAccess(context.Background(), req); err != nil {
				return codexSessionOpenedMsg{
					projectPath:  req.ProjectPath,
					perfOpID:     perfOpID,
					perfDuration: time.Since(startedAt),
					err:          err,
				}
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
		visibleStatus := embeddedSessionOpenStatus(req, threadIDsToAvoid, reused, snapshot, true)
		backgroundStatus := embeddedSessionOpenStatus(req, threadIDsToAvoid, reused, snapshot, false)
		status := visibleStatus
		if !revealOnOpen {
			status = backgroundStatus
		}
		renamedTask, renameErr := m.maybeAutoRenameScratchTaskFromPrompt(req.ProjectPath, req.Prompt)
		return codexSessionOpenedMsg{
			projectPath:      req.ProjectPath,
			snapshot:         snapshot,
			status:           status,
			visibleStatus:    visibleStatus,
			backgroundStatus: backgroundStatus,
			renamedTask:      renamedTask,
			renameErr:        renameErr,
			perfOpID:         perfOpID,
			perfDuration:     time.Since(startedAt),
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

func embeddedSessionOpenStatus(req codexapp.LaunchRequest, threadIDsToAvoid map[string]struct{}, reused bool, snapshot codexapp.Snapshot, revealOnOpen bool) string {
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
		return embeddedSessionOpenedStatus("Prompt sent to "+freshSessionLabel, revealOnOpen)
	case req.ForceNew:
		return embeddedSessionOpenedStatus("Fresh embedded "+label+" "+sessionLabel+" opened", revealOnOpen)
	case snapshot.BusyExternal && strings.TrimSpace(req.Prompt) != "":
		if cmd := embeddedNewCommand(provider); cmd != "" {
			return label + " is already active in another process. Prompt was not sent; use " + cmd + " for a separate session."
		}
		return label + " is already active in another process. Prompt was not sent; this is a read-only view."
	case snapshot.BusyExternal:
		return label + " is already active in another process. Embedded view is read-only until it finishes."
	case strings.TrimSpace(req.Prompt) != "":
		return embeddedSessionOpenedStatus("Prompt sent to embedded "+label, revealOnOpen)
	case reused:
		return embeddedSessionOpenedStatus("Embedded "+label+" session reopened", revealOnOpen)
	default:
		return embeddedSessionOpenedStatus("Embedded "+label+" session opened", revealOnOpen)
	}
}

func embeddedSessionOpenedStatus(action string, revealOnOpen bool) string {
	if revealOnOpen {
		return action + ". Alt+Up hides it."
	}
	return action + " in the background."
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
	queueSteer := false
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		steer = codexSnapshotCanSteer(snapshot)
		queueSteer = codexSnapshotQueuesBusyInput(snapshot)
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, func() tea.Msg {
		return codexActionMsg{projectPath: projectPath, restoreDraft: draft, err: errors.New("embedded session unavailable")}
	}, func(session codexapp.Session) tea.Msg {
		if err := session.SubmitInput(submission); err != nil {
			return codexActionMsg{projectPath: projectPath, restoreDraft: draft, err: err}
		}
		renamedTask, renameErr := m.maybeAutoRenameScratchTaskFromPrompt(projectPath, submission.TranscriptDisplayText())
		status := "Prompt sent to " + label
		if steer {
			status = "Steer sent to " + label
		} else if queueSteer {
			status = "Steer queued for " + label
		}
		return codexActionMsg{projectPath: projectPath, status: status, refreshView: queueSteer, renamedTask: renamedTask, renameErr: renameErr}
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
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " status added to the transcript", refreshView: true}
	})
}

func (m Model) showVisibleCodexPermissionsCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		permissionSession, ok := session.(embeddedPermissionSession)
		if !ok {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("%s does not support in-session permission changes", label)}
		}
		if err := permissionSession.ShowPermissions(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " permissions added to the transcript", refreshView: true}
	})
}

func (m Model) setVisibleCodexPermissionCmd(level string) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	level = strings.ToLower(strings.TrimSpace(level))
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		permissionSession, ok := session.(embeddedPermissionSession)
		if !ok {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("%s does not support in-session permission changes", label)}
		}
		if err := permissionSession.SetPermissionLevel(level); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " permissions set to " + titleASCII(level), refreshView: true}
	})
}

func titleASCII(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (m Model) showVisibleCodexGoalCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.ShowGoal(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " goal added to the transcript", refreshView: true}
	})
}

func (m Model) setVisibleCodexGoalCmd(objective string, tokenBudget *int64) tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	var budgetCopy *int64
	if tokenBudget != nil {
		copied := *tokenBudget
		budgetCopy = &copied
	}
	objective = strings.TrimSpace(objective)
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.SetGoal(objective, budgetCopy); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " goal set"}
	})
}

func (m Model) pauseVisibleCodexGoalCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.PauseGoal(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " goal paused"}
	})
}

func (m Model) resumeVisibleCodexGoalCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.ResumeGoal(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " goal resumed"}
	})
}

func (m Model) clearVisibleCodexGoalCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	stopping := false
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
		stopping = snapshot.Busy && codexSnapshotGoalCanBeStopped(snapshot)
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.ClearGoal(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		status := "Embedded " + label + " goal cleared"
		if stopping {
			status = "Embedded " + label + " goal stopped"
		}
		return codexActionMsg{projectPath: projectPath, status: status}
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

func (m Model) reviewVisibleCodexSessionCmd() tea.Cmd {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	label := "Codex"
	if snapshot, ok := m.currentCodexSnapshot(); ok {
		label = embeddedProvider(snapshot).Label()
	}
	return m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if err := session.Review(); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		return codexActionMsg{projectPath: projectPath, status: "Embedded " + label + " review started"}
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

func (m Model) hidePendingCodexOpen(projectPath string) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || m.codexPendingOpen == nil {
		return m, nil
	}
	label := m.codexPendingOpenProvider().Label()
	m.codexPendingOpen.showWhilePending = false
	m.codexPendingOpen.hideOnOpen = true
	m.codexHiddenProject = projectPath
	m.syncDetailViewport(false)
	m.status = "Embedded " + label + " session hidden."
	return m.returnToBossModeAfterCodexHidden(m.focusProjectPath(projectPath))
}

func (m Model) returnToBossModeAfterCodexHidden(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.returnToBossModeAfterCodexHide {
		return m, cmd
	}
	m.returnToBossModeAfterCodexHide = false
	updated, bossCmd := m.openBossMode()
	m = normalizeUpdateModel(updated)
	return m, batchCmds(cmd, bossCmd)
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
	m.codexPanelFocus = embeddedCodexFocusMain
	m.codexInput.Blur()
	m.syncDetailViewport(false)
	m.status = label + " hidden."
	return m.returnToBossModeAfterCodexHidden(batchCmds(
		m.focusProjectPath(projectPath),
		m.markProjectSessionSeen(projectPath),
		refreshCmd,
	))
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
