package tui

import (
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type fakeHistoryCodexSession struct {
	*fakeCodexSession
	loadCalls int
	loadFn    func(*fakeHistoryCodexSession)
}

func (s *fakeHistoryCodexSession) LoadOlderHistory() error {
	s.loadCalls++
	if s.loadFn != nil {
		s.loadFn(s)
	}
	return nil
}

func TestCodexHistoryPageLoadsAsyncAndPreservesVisibleTurn(t *testing.T) {
	projectPath := "/tmp/history-demo"
	recentEntries := []codexapp.TranscriptEntry{
		{ItemID: "history-note", Kind: codexapp.TranscriptSystem, Text: "Older transcript turns are available."},
		{ItemID: "user-recent", TurnID: "turn-recent", Kind: codexapp.TranscriptUser, Text: "recent question"},
		{ItemID: "agent-recent", TurnID: "turn-recent", Kind: codexapp.TranscriptAgent, Text: "recent answer\nline two\nline three"},
	}
	session := &fakeHistoryCodexSession{fakeCodexSession: &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:           codexapp.ProviderCodex,
			ProjectPath:        projectPath,
			Started:            true,
			HistoryHasMore:     true,
			TranscriptRevision: 1,
			Entries:            recentEntries,
		},
	}}
	session.loadFn = func(s *fakeHistoryCodexSession) {
		s.snapshot.Entries = []codexapp.TranscriptEntry{
			{ItemID: "history-note", Kind: codexapp.TranscriptSystem, Text: "Historical tool details are summarized."},
			{ItemID: "user-old", TurnID: "turn-old", Kind: codexapp.TranscriptUser, Text: "older question"},
			{ItemID: "agent-old", TurnID: "turn-old", Kind: codexapp.TranscriptAgent, Text: "older answer\nolder line two\nolder line three"},
			{ItemID: "user-recent", TurnID: "turn-recent", Kind: codexapp.TranscriptUser, Text: "recent question"},
			{ItemID: "agent-recent", TurnID: "turn-recent", Kind: codexapp.TranscriptAgent, Text: "recent answer\nline two\nline three"},
		}
		s.snapshot.HistoryHasMore = false
		s.snapshot.TranscriptRevision++
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(80, 5),
		width:               80,
		height:              12,
	}
	m.ensureCodexRuntime()
	m.storeCodexSnapshot(projectPath, session.Snapshot())
	m.syncCodexViewport(true)
	m.codexViewport.GotoTop()

	cmd := m.maybeRequestOlderCodexHistoryAtViewportTop()
	if cmd == nil {
		t.Fatal("expected an asynchronous older-history command")
	}
	if !m.codexHistoryLoadPending(projectPath) {
		t.Fatal("history load should be marked pending before provider I/O starts")
	}
	msg, ok := cmd().(codexHistoryPageLoadedMsg)
	if !ok {
		t.Fatalf("history command returned %T, want codexHistoryPageLoadedMsg", cmd())
	}
	updated, renderCmd := m.applyCodexHistoryPageLoadedMsg(msg)
	m = normalizeUpdateModel(updated)
	if renderCmd == nil {
		t.Fatal("loaded history should schedule transcript rendering off the UI thread")
	}
	renderMsg, ok := renderCmd().(codexTranscriptRenderedMsg)
	if !ok {
		t.Fatalf("render command returned %T, want codexTranscriptRenderedMsg", renderCmd())
	}
	updated, _ = m.applyCodexTranscriptRenderedMsg(renderMsg)
	m = normalizeUpdateModel(updated)

	if session.loadCalls != 1 {
		t.Fatalf("history load calls = %d, want 1", session.loadCalls)
	}
	if m.codexHistoryLoadPending(projectPath) {
		t.Fatal("history viewport restore should clear after the new page is rendered")
	}
	if m.codexViewport.YOffset <= 0 {
		t.Fatalf("viewport offset = %d, want it shifted to keep the previously visible recent turn stable", m.codexViewport.YOffset)
	}
	if rendered := ansi.Strip(m.codexTranscriptCache.rendered); !strings.Contains(rendered, "older question") || !strings.Contains(rendered, "recent question") {
		t.Fatalf("rendered paginated transcript missing turns: %q", rendered)
	}
}

func TestEmbeddedSidebarRecentTurnsSelectAndJump(t *testing.T) {
	projectPath := "/tmp/recent-turns-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.height = 14
	entries := make([]codexapp.TranscriptEntry, 0, 12)
	for i := 1; i <= 6; i++ {
		turnID := fmt.Sprintf("turn-%d", i)
		entries = append(entries,
			codexapp.TranscriptEntry{ItemID: fmt.Sprintf("user-%d", i), TurnID: turnID, Kind: codexapp.TranscriptUser, Text: fmt.Sprintf("question %d with a useful label", i)},
			codexapp.TranscriptEntry{ItemID: fmt.Sprintf("agent-%d", i), TurnID: turnID, Kind: codexapp.TranscriptAgent, Text: fmt.Sprintf("answer %d\nmore detail\nfinal detail", i)},
		)
	}
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Entries = entries
	m.storeCodexSnapshot(projectPath, snapshot)
	m.syncCodexViewport(true)

	section := ansi.Strip(strings.Join(m.renderEmbeddedSidebarRecentTurnsSection(snapshot, 38), "\n"))
	if !strings.Contains(section, "Recent Turns") || !strings.Contains(section, "question 6") || !strings.Contains(section, "question 2") {
		t.Fatalf("recent-turn section missing newest five turns:\n%s", section)
	}
	if strings.Contains(section, "question 1") {
		t.Fatalf("recent-turn section should cap itself at five rows:\n%s", section)
	}

	m.codexPanelFocus = embeddedCodexFocusSidebar
	m.codexSidebarSelected = embeddedCodexSidebarRecentTurns
	m.codexSidebarTurnSelected = 4
	anchor, ok := codexTranscriptTurnAnchorByID(m.codexTranscriptCache.turnAnchors, "turn-2")
	if !ok {
		t.Fatal("rendered transcript missing turn-2 anchor")
	}
	updated, _ := m.updateCodexSidebarMode(snapshot, tea.KeyMsg{Type: tea.KeyEnter})
	m = normalizeUpdateModel(updated)
	if m.codexPanelFocus != embeddedCodexFocusMain {
		t.Fatalf("panel focus = %q, want main transcript after jump", m.codexPanelFocus)
	}
	wantOffset := min(anchor.Line, max(0, m.codexViewport.TotalLineCount()-m.codexViewport.Height))
	if m.codexViewport.YOffset != wantOffset {
		t.Fatalf("viewport offset = %d, want turn anchor %d (clamped to %d)", m.codexViewport.YOffset, anchor.Line, wantOffset)
	}
}

func TestCodexTranscriptUpdateDoesNotPinReaderAndShowsCatchUpIndicator(t *testing.T) {
	projectPath := "/tmp/read-while-streaming"
	entries := make([]codexapp.TranscriptEntry, 0, 18)
	for i := 0; i < 18; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			ItemID: fmt.Sprintf("agent-%d", i),
			TurnID: fmt.Sprintf("turn-%d", i),
			Kind:   codexapp.TranscriptAgent,
			Text:   fmt.Sprintf("answer %d\nline two\nline three", i),
		})
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(80, 6),
		width:               80,
		height:              14,
	}
	m.ensureCodexRuntime()
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: projectPath, Started: true, TranscriptRevision: 1, Entries: entries}
	m.storeCodexSnapshot(projectPath, snapshot)
	m.syncCodexViewport(true)
	maxOffset := max(0, m.codexViewport.TotalLineCount()-m.codexViewport.Height)
	m.codexViewport.SetYOffset(max(1, maxOffset-5))
	before := m.codexViewport.YOffset

	snapshot.TranscriptRevision++
	snapshot.Entries = append(snapshot.Entries, codexapp.TranscriptEntry{ItemID: "agent-new", TurnID: "turn-new", Kind: codexapp.TranscriptAgent, Text: "new streamed answer\nkeeps arriving"})
	m.storeCodexSnapshot(projectPath, snapshot)
	m.syncCodexViewport(false)
	if m.codexViewport.YOffset != before {
		t.Fatalf("streaming update moved reader from offset %d to %d", before, m.codexViewport.YOffset)
	}
	rendered := ansi.Strip(m.renderCodexMainView(snapshot, 80, 14))
	if !strings.Contains(rendered, "More recent conversation below") {
		t.Fatalf("scrolled transcript missing catch-up indicator:\n%s", rendered)
	}

	m.codexViewport.GotoBottom()
	rendered = ansi.Strip(m.renderCodexMainView(snapshot, 80, 14))
	if strings.Contains(rendered, "More recent conversation below") {
		t.Fatalf("bottom-pinned transcript should hide catch-up indicator:\n%s", rendered)
	}
}
