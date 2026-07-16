package tui

import (
	"strings"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
)

type codexHistoryViewportRestore struct {
	AnchorTurnID    string
	AnchorLineDelta int
	TotalLines      int
	YOffset         int
}

type codexTurnJumpRequest struct {
	ProjectPath string
	TurnID      string
}

type codexHistoryPageLoadedMsg struct {
	projectPath string
	snapshot    codexapp.Snapshot
	err         error
}

type codexOlderHistoryLoader interface {
	LoadOlderHistory() error
}

func (m Model) codexHistoryLoadPending(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || len(m.codexHistoryLoads) == 0 {
		return false
	}
	_, ok := m.codexHistoryLoads[projectPath]
	return ok
}

func (m *Model) maybeRequestOlderCodexHistoryAtViewportTop() tea.Cmd {
	if !m.codexVisible() || m.codexViewport.YOffset > 0 {
		return nil
	}
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if projectPath == "" || m.codexHistoryLoadPending(projectPath) {
		return nil
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok || !snapshot.HistoryHasMore || snapshot.HistoryLoading {
		return nil
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return nil
	}
	loader, ok := session.(codexOlderHistoryLoader)
	if !ok {
		return nil
	}

	restore := codexHistoryViewportRestore{
		TotalLines: m.codexViewport.TotalLineCount(),
		YOffset:    m.codexViewport.YOffset,
	}
	if m.codexTranscriptCacheMatches(projectPath, m.codexViewport.Width) {
		if anchor, ok := nearestCodexTranscriptTurnAnchor(m.codexTranscriptCache.turnAnchors, restore.YOffset); ok {
			restore.AnchorTurnID = anchor.TurnID
			restore.AnchorLineDelta = restore.YOffset - anchor.Line
		}
	}
	if m.codexHistoryLoads == nil {
		m.codexHistoryLoads = make(map[string]codexHistoryViewportRestore)
	}
	m.codexHistoryLoads[projectPath] = restore
	m.loadFullCodexTranscriptHistory(projectPath)
	m.status = "Loading older Codex turns..."
	return func() tea.Msg {
		err := loader.LoadOlderHistory()
		return codexHistoryPageLoadedMsg{
			projectPath: projectPath,
			snapshot:    session.Snapshot(),
			err:         err,
		}
	}
}

func (m Model) applyCodexHistoryPageLoadedMsg(msg codexHistoryPageLoadedMsg) (tea.Model, tea.Cmd) {
	projectPath := strings.TrimSpace(msg.projectPath)
	if projectPath == "" {
		return m, nil
	}
	snapshot := msg.snapshot
	if current, ok := m.codexCachedSnapshot(projectPath); ok && current.TranscriptRevision > snapshot.TranscriptRevision {
		snapshot = current
	}
	if msg.err != nil {
		delete(m.codexHistoryLoads, projectPath)
		m.status = msg.err.Error()
		if snapshot.ProjectPath != "" {
			m.storeCodexSnapshot(projectPath, snapshot)
		}
		return m, nil
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	if snapshot.HistoryHasMore {
		m.status = "Loaded older Codex turns; scroll to the top again for the previous page"
	} else {
		m.status = "Loaded the beginning of the Codex conversation"
	}
	if strings.TrimSpace(m.codexVisibleProject) != projectPath {
		delete(m.codexHistoryLoads, projectPath)
		return m, nil
	}
	width := m.codexViewport.Width
	if width <= 0 {
		width = m.embeddedCodexMainWidth()
	}
	if width <= 0 {
		width = 120
	}
	return m, m.requestCodexTranscriptRenderCmdForced(projectPath, snapshot, width)
}

func nearestCodexTranscriptTurnAnchor(anchors []codexTranscriptTurnAnchor, line int) (codexTranscriptTurnAnchor, bool) {
	if len(anchors) == 0 {
		return codexTranscriptTurnAnchor{}, false
	}
	best := anchors[0]
	bestDistance := absInt(best.Line - line)
	for _, anchor := range anchors[1:] {
		distance := absInt(anchor.Line - line)
		if distance < bestDistance {
			best = anchor
			bestDistance = distance
		}
	}
	return best, true
}

func codexTranscriptTurnAnchorByID(anchors []codexTranscriptTurnAnchor, turnID string) (codexTranscriptTurnAnchor, bool) {
	turnID = strings.TrimSpace(turnID)
	for _, anchor := range anchors {
		if strings.TrimSpace(anchor.TurnID) == turnID {
			return anchor, true
		}
	}
	return codexTranscriptTurnAnchor{}, false
}

func (m Model) jumpToCodexTurn(turn embeddedSidebarRecentTurn) (tea.Model, tea.Cmd) {
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	turnID := strings.TrimSpace(turn.TurnID)
	if projectPath == "" || turnID == "" {
		return m, nil
	}
	m.codexPanelFocus = embeddedCodexFocusMain
	m.codexPendingTurnJump = codexTurnJumpRequest{}
	if m.codexTranscriptCacheMatches(projectPath, m.codexViewport.Width) {
		if anchor, ok := codexTranscriptTurnAnchorByID(m.codexTranscriptCache.turnAnchors, turnID); ok {
			m.codexViewport.SetYOffset(anchor.Line)
			m.status = "Jumped to turn: " + turn.Label
			return m, m.codexInput.Focus()
		}
	}
	m.codexPendingTurnJump = codexTurnJumpRequest{ProjectPath: projectPath, TurnID: turnID}
	m.status = "Locating turn: " + turn.Label
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return m, m.codexInput.Focus()
	}
	width := m.codexViewport.Width
	if width <= 0 {
		width = m.embeddedCodexMainWidth()
	}
	return m, batchCmds(m.codexInput.Focus(), m.requestCodexTranscriptRenderCmdForced(projectPath, snapshot, width))
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
