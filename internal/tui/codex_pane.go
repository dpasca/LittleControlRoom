package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/codexslash"

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
	codexDenseBlockPreviewLines   = 5
	codexTranscriptLiveEntryLimit = 180
	codexTranscriptLiveLineLimit  = 1600
	codexTranscriptLiveByteLimit  = 256 * 1024
	codexCacheMissEntryLimit      = 24
	codexCacheMissLineLimit       = 240
	codexCacheMissByteLimit       = 32 * 1024
)

type codexDenseBlockMode int

const (
	codexDenseBlockSummary codexDenseBlockMode = iota
	codexDenseBlockPreview
	codexDenseBlockFull
)

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

type codexStateSnapshooter interface {
	StateSnapshot() codexapp.Snapshot
}

func stateSnapshotForCodexSession(session codexapp.Session) (codexapp.Snapshot, bool) {
	if session == nil {
		return codexapp.Snapshot{}, false
	}
	state, ok := session.(codexStateSnapshooter)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	return state.StateSnapshot(), true
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
		cached.ManagedBrowserSessionKey = state.ManagedBrowserSessionKey
		cached.Status = state.Status
		cached.LastError = state.LastError
		cached.LastSystemNotice = state.LastSystemNotice
		cached.LastActivityAt = state.LastActivityAt
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

func (m Model) codexTranscriptCacheMatches(projectPath string, width int) bool {
	projectPath = strings.TrimSpace(projectPath)
	return projectPath != "" &&
		m.codexTranscriptCache.projectPath == projectPath &&
		m.codexTranscriptCache.width == width &&
		m.codexTranscriptCache.denseBlockMode == m.codexDenseBlockMode.normalized() &&
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
		rendered, links = m.renderCodexTranscriptContentFromSnapshotWithLinks(snapshot, width)
	})
	m.codexTranscriptCache = codexTranscriptRenderCache{
		projectPath:    strings.TrimSpace(projectPath),
		width:          width,
		denseBlockMode: m.codexDenseBlockMode.normalized(),
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

	if m.codexInputCopyDialog != nil {
		return m.updateCodexInputCopyDialogMode(msg)
	}

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
		m.codexDenseBlockMode = m.codexDenseBlockMode.next()
		m.status = m.codexDenseBlockMode.statusText()
		m.syncCodexViewport(false)
		return m, nil
	case "alt+o":
		return m.openCodexArtifactPicker(snapshot)
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
	case "alt+c":
		if snapshot.PendingApproval != nil || snapshot.Closed {
			return m, nil
		}
		m.openCodexInputCopyDialog()
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

	if m.codexInputSelectionActive() {
		return m.updateCodexInputSelectionMode(msg)
	}

	if snapshot.PendingToolInput != nil {
		return m.updateCodexToolInputMode(snapshot, msg)
	}

	if snapshot.PendingElicitation != nil {
		return m.updateCodexElicitationMode(snapshot, msg)
	}

	// Clear mouse selection on any keypress.
	m.codexComposerSelection = textSelection{}

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
			if snapshot.Closed && (inv.Kind == codexslash.KindModel || inv.Kind == codexslash.KindStatus || inv.Kind == codexslash.KindCompact || inv.Kind == codexslash.KindReview) {
				m.status = label + " session is closed. Use /resume, /new, or /reconnect to reopen it."
				return m, nil
			}
			switch inv.Kind {
			case codexslash.KindNew:
				m.status = "Starting a new embedded " + label + " session..."
				m.beginNewCodexPendingOpen(m.codexVisibleProject, embeddedProvider(snapshot))
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
			case codexslash.KindReview:
				m.status = "Starting embedded " + label + " review..."
				return m, m.reviewVisibleCodexSessionCmd()
			case codexslash.KindBoss:
				return m.openBossModeOrSetupPrompt()
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
		m.status = "Use Alt+C to copy or select input"
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
	stickToBottom := resetToBottom || m.codexViewport.AtBottom()
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
	// Keep the latest reply visible when lower assistant blocks appear or disappear
	// while the transcript is already pinned to the end.
	if stickToBottom {
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
	case codexTranscriptCacheMissCanRender(snapshot):
		transcript.SetContent(m.renderCodexTranscriptContentFromSnapshot(snapshot, transcript.Width))
	default:
		transcript.SetContent(renderCodexTranscriptCacheMissContent(snapshot))
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
	headline := "Opening embedded " + label + " session..."
	detail := "Waiting for the requested embedded session to come online."
	footerStatus := "Opening embedded " + label + " session"
	if m.codexPendingOpen != nil && m.codexPendingOpen.newSession {
		headline = "Starting a new embedded " + label + " session..."
		detail = "Preparing the new embedded session."
		footerStatus = "Starting a new embedded " + label + " session"
	}
	spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(label + " | " + projectName)
	bodyHeight := max(3, height-6)
	body := m.renderHFramedPane(strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(spinner + " " + headline),
		"",
		fitFooterWidth("Project: "+projectPath, max(24, width-4)),
		fitFooterWidth(detail, max(24, width-4)),
	}, "\n"), width, bodyHeight, true)
	footer := renderFooterLine(width, renderFooterStatus(spinner+" "+footerStatus))
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
	return managedBrowserFlowSupported(embeddedProvider(snapshot)) &&
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
	if managedBrowserFlowSupported(embeddedProvider(snapshot)) &&
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
	if blockMode := m.codexDenseBlockMode.bannerText(); blockMode != "" {
		parts = append(parts, blockMode)
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
			footerLowAction("Alt+C", "copy/select"),
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
		if managedBrowserCurrentPageURL(snapshot) != "" && strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" {
			actions = append(actions, footerNavAction("Ctrl+O", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeForm:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("Ctrl+C", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy/select"),
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
			footerLowAction("Alt+C", "copy/select"),
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
			footerLowAction("Alt+C", "copy/select"),
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
			footerLowAction("Alt+C", "copy/select"),
		}
		if managedBrowserCurrentPageURL(snapshot) != "" && strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" {
			actions = append(actions, footerNavAction("Ctrl+O", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
	}
	if len(codexArtifactOpenTargets(snapshot)) > 0 && codexArtifactPickerAllowed(snapshot) {
		actions = append(actions, footerNavAction("Alt+O", "links"))
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
	rendered, _ := m.renderCodexTranscriptContentFromSnapshotWithLinks(snapshot, width)
	return rendered
}

func (m Model) renderCodexTranscriptContentFromSnapshotWithLinks(snapshot codexapp.Snapshot, width int) (string, []codexTranscriptLinkSpan) {
	if rendered, links := m.renderCodexTranscriptEntriesWithLinks(snapshot, width); strings.TrimSpace(rendered) != "" {
		return rendered, links
	}
	if snapshot.Closed {
		return embeddedProvider(snapshot).Label() + " session closed.", nil
	}
	if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
		return "[system] " + sanitizeCodexRenderedText(notice), nil
	}
	return "Type a prompt and press Enter.", nil
}

func renderCodexTranscriptCacheMissContent(snapshot codexapp.Snapshot) string {
	if snapshot.Closed {
		return embeddedProvider(snapshot).Label() + " session closed."
	}
	if len(snapshot.Entries) == 0 && strings.TrimSpace(snapshot.Transcript) == "" {
		if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
			return "[system] " + sanitizeCodexRenderedText(notice)
		}
		return "Type a prompt and press Enter."
	}
	return "Transcript is updating..."
}

func codexTranscriptCacheMissCanRender(snapshot codexapp.Snapshot) bool {
	if len(snapshot.Entries) > 0 {
		if len(snapshot.Entries) > codexCacheMissEntryLimit {
			return false
		}
		lines := 0
		bytes := 0
		for _, entry := range snapshot.Entries {
			bytes += codexTranscriptEntryApproxByteCount(entry)
			if bytes > codexCacheMissByteLimit {
				return false
			}
			lines += codexTranscriptEntryApproxLineCount(entry)
			if lines > codexCacheMissLineLimit {
				return false
			}
		}
		return true
	}
	transcript := strings.TrimSpace(snapshot.Transcript)
	if transcript == "" {
		return true
	}
	if len(transcript) > codexCacheMissByteLimit {
		return false
	}
	return transcriptApproxLineCount(transcript) <= codexCacheMissLineLimit
}

type codexArtifactOpenTarget struct {
	Kind        string
	Label       string
	Path        string
	PreviewData []byte
}

type codexArtifactPickerState struct {
	ProjectPath     string
	Title           string
	Hint            string
	Targets         []codexArtifactOpenTarget
	Selected        int
	PreviewSeq      int64
	PreviewRequests map[string]int64
	PreviewData     map[string][]byte
	PreviewErrors   map[string]string
}

func (m Model) openCodexArtifactPicker(snapshot codexapp.Snapshot) (tea.Model, tea.Cmd) {
	targets := m.visibleCodexOpenTargets(snapshot)
	if len(targets) == 0 {
		m.status = "No openable links visible in this embedded transcript"
		return m, nil
	}
	m.codexArtifactPicker = &codexArtifactPickerState{
		ProjectPath:     strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject)),
		Title:           "Open Links",
		Hint:            "Links visible in this embedded transcript. Enter opens with the system app or browser.",
		Targets:         targets,
		Selected:        len(targets) - 1,
		PreviewRequests: make(map[string]int64),
		PreviewData:     make(map[string][]byte),
		PreviewErrors:   make(map[string]string),
	}
	m.status = "Link picker open"
	return m, m.codexArtifactPickerPreviewCmd()
}

func (m Model) updateCodexArtifactPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.codexArtifactPicker
	if picker == nil {
		return m, nil
	}
	if len(picker.Targets) == 0 {
		m.closeCodexArtifactPicker("No openable links visible in this embedded transcript")
		return m, nil
	}
	if picker.Selected < 0 {
		picker.Selected = 0
	}
	if picker.Selected >= len(picker.Targets) {
		picker.Selected = len(picker.Targets) - 1
	}
	switch msg.String() {
	case "esc":
		m.closeCodexArtifactPicker("Link picker closed")
		return m, nil
	case "up", "k":
		picker.Selected = max(0, picker.Selected-1)
		return m, m.codexArtifactPickerPreviewCmd()
	case "down", "j":
		picker.Selected = min(len(picker.Targets)-1, picker.Selected+1)
		return m, m.codexArtifactPickerPreviewCmd()
	case "pgup", "ctrl+u":
		picker.Selected = max(0, picker.Selected-5)
		return m, m.codexArtifactPickerPreviewCmd()
	case "pgdown", "ctrl+d":
		picker.Selected = min(len(picker.Targets)-1, picker.Selected+5)
		return m, m.codexArtifactPickerPreviewCmd()
	case "home":
		picker.Selected = 0
		return m, m.codexArtifactPickerPreviewCmd()
	case "end":
		picker.Selected = len(picker.Targets) - 1
		return m, m.codexArtifactPickerPreviewCmd()
	case "enter", "alt+o":
		target := picker.Targets[picker.Selected]
		m.closeCodexArtifactPicker("Opening " + codexArtifactTargetDisplay(target))
		return m, m.openCodexLinkTargetCmd(target)
	}
	return m, nil
}

func (m Model) visibleCodexOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	width := m.codexViewport.Width
	if width <= 0 {
		width = m.width
	}
	if width <= 0 {
		width = 80
	}
	if projectPath == "" || !m.codexTranscriptCacheMatches(projectPath, width) {
		_ = m.renderAndCacheCodexTranscript(projectPath, snapshot, width)
	}
	links := m.codexTranscriptCache.links
	if len(links) == 0 {
		return nil
	}
	if m.codexViewport.Height <= 0 {
		return codexTargetsFromLinkSpans(links)
	}
	startLine := max(0, m.codexViewport.YOffset)
	endLine := startLine + max(1, m.codexViewport.Height)
	targets := make([]codexArtifactOpenTarget, 0, len(links))
	for _, link := range links {
		if link.EndLine <= startLine || link.StartLine >= endLine {
			continue
		}
		targets = append(targets, link.Target)
	}
	return targets
}

func codexTargetsFromLinkSpans(links []codexTranscriptLinkSpan) []codexArtifactOpenTarget {
	targets := make([]codexArtifactOpenTarget, 0, len(links))
	for _, link := range links {
		targets = append(targets, link.Target)
	}
	return targets
}

func (m *Model) closeCodexArtifactPicker(status string) {
	m.codexArtifactPicker = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func codexArtifactPickerAllowed(snapshot codexapp.Snapshot) bool {
	return snapshot.PendingApproval == nil && snapshot.PendingToolInput == nil && snapshot.PendingElicitation == nil
}

func (m *Model) codexArtifactPickerPreviewCmd() tea.Cmd {
	picker := m.codexArtifactPicker
	if picker == nil {
		return nil
	}
	target, ok := m.currentCodexArtifactTarget()
	if !ok {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if strings.TrimSpace(target.Kind) != "image" || path == "" || len(target.PreviewData) > 0 {
		return nil
	}
	if len(picker.PreviewData[path]) > 0 || strings.TrimSpace(picker.PreviewErrors[path]) != "" {
		return nil
	}
	if picker.PreviewRequests == nil {
		picker.PreviewRequests = make(map[string]int64)
	}
	if picker.PreviewRequests[path] > 0 {
		return nil
	}
	picker.PreviewSeq++
	seq := picker.PreviewSeq
	picker.PreviewRequests[path] = seq
	projectPath := strings.TrimSpace(picker.ProjectPath)
	return loadCodexArtifactPreviewCmd(projectPath, path, seq)
}

const maxCodexArtifactPreviewBytes = 25 * 1024 * 1024

func loadCodexArtifactPreviewCmd(projectPath, path string, seq int64) tea.Cmd {
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("inspect preview: %w", err)}
		}
		if info.IsDir() {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("path is a directory")}
		}
		if info.Size() > maxCodexArtifactPreviewBytes {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("image is too large for preview")}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, err: fmt.Errorf("read preview: %w", err)}
		}
		return codexArtifactPreviewMsg{projectPath: projectPath, path: path, seq: seq, data: data}
	}
}

func (m Model) applyCodexArtifactPreviewMsg(msg codexArtifactPreviewMsg) (tea.Model, tea.Cmd) {
	picker := m.codexArtifactPicker
	if picker == nil {
		return m, nil
	}
	path := strings.TrimSpace(msg.path)
	if path == "" || strings.TrimSpace(msg.projectPath) != strings.TrimSpace(picker.ProjectPath) {
		return m, nil
	}
	if picker.PreviewRequests == nil || picker.PreviewRequests[path] != msg.seq {
		return m, nil
	}
	delete(picker.PreviewRequests, path)
	if picker.PreviewData == nil {
		picker.PreviewData = make(map[string][]byte)
	}
	if picker.PreviewErrors == nil {
		picker.PreviewErrors = make(map[string]string)
	}
	if msg.err != nil {
		picker.PreviewErrors[path] = strings.TrimSpace(msg.err.Error())
		return m, nil
	}
	delete(picker.PreviewErrors, path)
	picker.PreviewData[path] = append([]byte(nil), msg.data...)
	return m, nil
}

func codexArtifactTargetDisplay(target codexArtifactOpenTarget) string {
	label := strings.TrimSpace(target.Label)
	if strings.TrimSpace(target.Kind) == "url" {
		right := codexArtifactTargetRight(target)
		if label == "" || label == strings.TrimSpace(target.Path) {
			return right
		}
		if right == "" || label == right {
			return label
		}
		return label + " (" + right + ")"
	}
	base := filepath.Base(strings.TrimSpace(target.Path))
	if label == "" || label == base {
		return base
	}
	return label + " (" + base + ")"
}

func codexArtifactTargetRight(target codexArtifactOpenTarget) string {
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return ""
	}
	if strings.TrimSpace(target.Kind) == "url" {
		parsed, err := url.Parse(path)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
		return path
	}
	return filepath.Base(path)
}

func (m Model) currentCodexArtifactTarget() (codexArtifactOpenTarget, bool) {
	picker := m.codexArtifactPicker
	if picker == nil || len(picker.Targets) == 0 {
		return codexArtifactOpenTarget{}, false
	}
	index := picker.Selected
	if index < 0 {
		index = 0
	}
	if index >= len(picker.Targets) {
		index = len(picker.Targets) - 1
	}
	return picker.Targets[index], true
}

func (m Model) renderCodexArtifactPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexArtifactPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, min((bodyH-panelHeight)/5, bodyH-panelHeight))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexArtifactPicker(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(52, bodyW-12), 88))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCodexArtifactPickerContent(panelInnerWidth, bodyH))
}

func (m Model) renderCodexArtifactPickerContent(width, bodyH int) string {
	picker := m.codexArtifactPicker
	if picker == nil {
		return ""
	}
	title := strings.TrimSpace(picker.Title)
	if title == "" {
		title = "Open Links"
	}
	hint := strings.TrimSpace(picker.Hint)
	if hint == "" {
		hint = "Links visible in this embedded transcript."
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		commandPaletteHintStyle.Render(hint),
		"",
		renderDialogAction("Enter/Alt+O", "open", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("↑↓", "select", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}
	if len(picker.Targets) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No links found."))
		return strings.Join(lines, "\n")
	}
	start, end := codexArtifactPickerWindow(picker.Selected, len(picker.Targets), bodyH)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, renderCodexArtifactPickerRow(picker.Targets[i], i == picker.Selected, width))
	}
	if end < len(picker.Targets) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(picker.Targets)-end)))
	}
	if selected, ok := m.currentCodexArtifactTarget(); ok {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Selected"))
		lines = append(lines, detailValueStyle.Render(fitFooterWidth(codexArtifactTargetDisplay(selected), width)))
		if path := strings.TrimSpace(selected.Path); path != "" {
			lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(path, width)))
		}
		if preview := strings.TrimSpace(m.renderCodexArtifactPreview(selected, width, bodyH)); preview != "" {
			lines = append(lines, "")
			lines = append(lines, commandPaletteTitleStyle.Render("Preview"))
			lines = append(lines, preview)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderCodexArtifactPreview(target codexArtifactOpenTarget, width, bodyH int) string {
	if strings.TrimSpace(target.Kind) != "image" {
		return ""
	}
	data := target.PreviewData
	path := strings.TrimSpace(target.Path)
	picker := m.codexArtifactPicker
	if len(data) == 0 && picker != nil && path != "" {
		data = picker.PreviewData[path]
	}
	maxRows := max(3, min(8, bodyH/4))
	if len(data) > 0 {
		return renderANSIImagePreview(data, max(12, width), maxRows)
	}
	if picker != nil && path != "" {
		if errText := strings.TrimSpace(picker.PreviewErrors[path]); errText != "" {
			return commandPaletteHintStyle.Render("Preview unavailable: " + truncateText(errText, max(20, width-22)))
		}
		if picker.PreviewRequests[path] > 0 {
			return commandPaletteHintStyle.Render("Loading preview...")
		}
	}
	return commandPaletteHintStyle.Render("Preview unavailable.")
}

func codexArtifactPickerWindow(selected, total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if bodyH <= 0 {
		bodyH = 30
	}
	limit := min(total, max(3, min(6, bodyH-18)))
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func renderCodexArtifactPickerRow(target codexArtifactOpenTarget, selected bool, width int) string {
	kind := strings.ToUpper(strings.TrimSpace(target.Kind))
	if kind == "" {
		kind = "FILE"
	}
	kind = fixedBadgeSlot(kind, 5)
	label := codexArtifactTargetDisplay(target)
	right := codexArtifactTargetRight(target)
	available := max(12, width-lipgloss.Width(kind)-lipgloss.Width(right)-7)
	row := fmt.Sprintf("  %s  %s  %s", kind, fitStyledWidth(fitFooterWidth(label, available), available), right)
	if selected {
		row = "> " + strings.TrimPrefix(row, "  ")
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func codexArtifactOpenTargets(snapshot codexapp.Snapshot) []codexArtifactOpenTarget {
	targets := make([]codexArtifactOpenTarget, 0)
	for _, entry := range snapshot.Entries {
		targets = append(targets, codexOpenTargetsFromTranscriptEntry(entry)...)
	}
	return targets
}

func codexOpenTargetsFromTranscriptEntry(entry codexapp.TranscriptEntry) []codexArtifactOpenTarget {
	if image := entry.GeneratedImage; image != nil {
		path := strings.TrimSpace(image.Path)
		if path == "" {
			path = strings.TrimSpace(image.SourcePath)
		}
		if path == "" {
			return nil
		}
		return []codexArtifactOpenTarget{{
			Kind:        "image",
			Label:       "Generated image",
			Path:        path,
			PreviewData: append([]byte(nil), image.PreviewData...),
		}}
	}
	text := entry.Text
	if entry.Kind == codexapp.TranscriptUser {
		if displayText := strings.TrimSpace(entry.DisplayText); displayText != "" {
			text = displayText
		}
	}
	return codexArtifactOpenTargetsFromMarkdown(text)
}

func codexArtifactOpenTargetsFromMarkdown(text string) []codexArtifactOpenTarget {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	targets := make([]codexArtifactOpenTarget, 0)
	remaining := text
	for len(remaining) > 0 {
		idx := strings.IndexByte(remaining, '[')
		if idx < 0 {
			return targets
		}
		label, target, consumed, ok := parseCodexMarkdownLink(remaining[idx:])
		if !ok {
			remaining = remaining[idx+1:]
			continue
		}
		if localPath, ok := codexLocalLinkText(target); ok {
			if artifactPath, kind, ok := codexLocalArtifactOpenTarget(label, localPath); ok {
				targets = append(targets, codexArtifactOpenTarget{Kind: kind, Label: label, Path: artifactPath})
			} else if openPath, _ := codexLocalOpenPath(localPath); strings.TrimSpace(openPath) != "" {
				targets = append(targets, codexArtifactOpenTarget{
					Kind:  codexLocalLinkKind(openPath, localPath),
					Label: codexLocalLinkLabel(label, localPath),
					Path:  openPath,
				})
			}
		} else if externalTarget, ok := codexExternalLinkTarget(target); ok {
			targets = append(targets, codexArtifactOpenTarget{Kind: "url", Label: label, Path: externalTarget})
		}
		remaining = remaining[idx+max(1, consumed):]
	}
	return targets
}

func codexExternalLinkTarget(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Scheme == "file" {
		return "", false
	}
	return parsed.String(), true
}

func codexLocalLinkKind(openPath, rawPath string) string {
	if _, location := codexLocalOpenPath(rawPath); location != "" {
		return "source"
	}
	if kind := codexArtifactKindForPath(openPath); kind != "" {
		return kind
	}
	return "file"
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
