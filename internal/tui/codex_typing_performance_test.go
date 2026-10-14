package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
)

func TestEngineerTypingAndViewDoNotReadLiveSession(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			const path = "/tmp/lcr-typing"
			m := testEmbeddedSidebarModel(path)
			snapshot := m.codexSnapshots[path]
			snapshot.Provider = provider
			snapshot.Busy = true
			snapshot.BrowserActivity.State = browserctl.SessionActivityStateActive
			snapshot.CurrentBrowserPageURL = "https://example.com"
			m.codexSnapshots[path] = snapshot
			session := &fakeCodexSession{projectPath: path, snapshot: snapshot}
			m.codexManager = codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) {
				return session, nil
			})
			if _, _, err := m.codexManager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: provider}); err != nil {
				t.Fatal(err)
			}
			m.syncEmbeddedSessionIdleProtection()
			m.codexInput.Focus()
			m.syncCodexComposerSize()
			m.syncCodexViewport(true)
			session.snapshotCalls, session.trySnapshotCalls = 0, 0
			session.stateSnapshotCalls, session.tryStateSnapshotCalls = 0, 0
			for _, key := range []tea.KeyMsg{
				{Type: tea.KeyRunes, Runes: []rune("x")},
				{Type: tea.KeyBackspace},
				{Type: tea.KeyRunes, Runes: []rune("/")},
				{Type: tea.KeyBackspace},
				{Type: tea.KeyRunes, Runes: []rune("still responsive")},
			} {
				updated, _ := m.Update(key)
				m = normalizeUpdateModel(updated)
				_ = m.View()
			}
			if got := m.codexInput.Value(); got != "still responsive" {
				t.Fatalf("draft = %q", got)
			}
			if !strings.Contains(ansi.Strip(m.View()), "still responsive") {
				t.Fatal("typed draft is not visible")
			}
			for i, action := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionMotion} {
				updated, _ := m.Update(tea.MouseMsg{X: 5 + i, Y: codexViewportScreenTop, Button: tea.MouseButtonLeft, Action: action})
				m = normalizeUpdateModel(updated)
				_ = m.View()
			}
			if !m.codexSelection.dragging {
				t.Fatal("transcript mouse selection did not start")
			}
			if session.snapshotCalls != 0 || session.trySnapshotCalls != 0 || session.stateSnapshotCalls != 0 || session.tryStateSnapshotCalls != 0 {
				t.Fatalf("typing/render read live session: snapshot=%d try=%d state=%d tryState=%d",
					session.snapshotCalls, session.trySnapshotCalls, session.stateSnapshotCalls, session.tryStateSnapshotCalls)
			}
		})
	}
}

func TestCodexBannerLinkAvailabilityUsesConfirmedCurrentCaches(t *testing.T) {
	const path = "/tmp/lcr-link-cache"
	explicit := codexArtifactOpenTarget{Kind: "file", Path: path + "/report.md"}
	unverified := explicit
	unverified.inferredLocalPath = true
	verified := unverified
	verified.verifiedPath = verified.Path
	for _, tc := range []struct {
		name      string
		targets   []codexArtifactOpenTarget
		complete  bool
		stale     bool
		visible   bool
		offscreen bool
		want      bool
	}{
		{name: "empty"},
		{name: "scanned explicit", targets: []codexArtifactOpenTarget{explicit}, want: true},
		{name: "scanned verified", targets: []codexArtifactOpenTarget{verified}, want: true},
		{name: "unverified", targets: []codexArtifactOpenTarget{unverified}},
		{name: "stale scan", targets: []codexArtifactOpenTarget{explicit}, stale: true},
		{name: "visible before scan", visible: true, want: true},
		{name: "offscreen before scan", visible: true, offscreen: true},
		{name: "completed empty scan", visible: true, complete: true},
		{name: "completed rejected paths", targets: []codexArtifactOpenTarget{unverified}, complete: true, visible: true},
		{name: "stale completed scan", stale: true, complete: true, visible: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testEmbeddedSidebarModel(path)
			snapshot := m.codexSnapshots[path]
			if tc.visible {
				snapshot.Entries = []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "See [report](" + explicit.Path + ")."}}
			}
			m.storeCodexSnapshot(path, snapshot)
			m.codexViewport.Width, m.codexViewport.Height = 100, 30
			m.renderAndCacheCodexTranscript(path, snapshot, 100)
			if tc.offscreen {
				m.codexViewport.YOffset = 1000
			}
			rev := m.codexTranscriptRevision(path)
			if tc.stale {
				rev++
			}
			m.codexArtifactLinkScans[path] = codexArtifactLinkScanState{transcriptRev: rev, targets: tc.targets, complete: tc.complete}
			if got := m.hasCachedCodexOpenTargets(snapshot); got != tc.want {
				t.Fatalf("link availability = %t, want %t", got, tc.want)
			}
			if allocs := testing.AllocsPerRun(20, func() { _ = m.hasCachedCodexOpenTargets(snapshot) }); allocs != 0 {
				t.Fatalf("banner link check allocated %.0f objects; it must not rebuild the picker", allocs)
			}
		})
	}
}

func TestEmbeddedSidebarRecentTurnsPreservesFirstPromptAndOrder(t *testing.T) {
	snapshot := codexapp.Snapshot{Entries: []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptUser, TurnID: "old", Text: "older prompt"},
		{Kind: codexapp.TranscriptUser, TurnID: "one", Text: "first prompt", DisplayText: "display\n prompt"},
		{Kind: codexapp.TranscriptAgent, TurnID: "one", Text: "reply"},
		{Kind: codexapp.TranscriptUser, TurnID: "two", Text: "second prompt"},
		{Kind: codexapp.TranscriptUser, TurnID: "one", Text: "follow-up in same turn"},
		{Kind: codexapp.TranscriptUser, Text: "no turn"},
	}}
	turns := embeddedSidebarRecentTurns(snapshot, 2)
	if len(turns) != 2 || turns[0].TurnID != "two" || turns[0].Label != "second prompt" || turns[1].TurnID != "one" || turns[1].Label != "display prompt" {
		t.Fatalf("recent turns = %#v", turns)
	}
	if got := embeddedSidebarRecentTurns(codexapp.Snapshot{}, 5); len(got) != 0 {
		t.Fatalf("empty transcript returned %#v", got)
	}
	if got := embeddedSidebarRecentTurns(snapshot, 0); len(got) != 0 {
		t.Fatalf("zero limit returned %#v", got)
	}
}

func benchmarkEngineerTypingModel(turns int) Model {
	const path = "/tmp/lcr-typing-benchmark"
	m := testEmbeddedSidebarModel(path)
	m.width, m.height = 160, 48
	snapshot := m.codexSnapshots[path]
	for i := 0; i < turns; i++ {
		turnID := fmt.Sprintf("turn-%d", i)
		snapshot.Entries = append(snapshot.Entries,
			codexapp.TranscriptEntry{Kind: codexapp.TranscriptUser, TurnID: turnID, Text: "Please investigate the input latency and verify the fix."},
			codexapp.TranscriptEntry{Kind: codexapp.TranscriptCommand, TurnID: turnID, Text: strings.Repeat("build output line\n", 1000)},
			codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, TurnID: turnID, Text: "Implemented the change and checked the results."},
		)
	}
	m.codexSnapshots[path] = snapshot
	m.codexInput.Focus()
	m.syncCodexComposerSize()
	m.renderAndCacheCodexTranscript(path, snapshot, m.embeddedCodexMainWidth())
	m.syncCodexViewport(true)
	return m
}

func BenchmarkEngineerTypingAndView(b *testing.B) {
	for _, turns := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("turns_%d", turns), func(b *testing.B) {
			m := benchmarkEngineerTypingModel(turns)
			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
			backspace := tea.KeyMsg{Type: tea.KeyBackspace}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msg := key
				if i%2 != 0 {
					msg = backspace
				}
				updated, _ := m.Update(msg)
				m = normalizeUpdateModel(updated)
				_ = m.View()
			}
		})
	}
}

func BenchmarkEngineerArtifactHistoryInteraction(b *testing.B) {
	for _, action := range []string{"typing", "selection"} {
		b.Run(action, func(b *testing.B) {
			m := benchmarkEngineerTypingModel(1000)
			path := m.codexVisibleProject
			targets := make([]codexArtifactOpenTarget, 0, 1100)
			for i := 0; i < 550; i++ {
				targets = append(targets,
					codexArtifactOpenTarget{Kind: "file", Path: fmt.Sprintf("%s/docs/review-%d.md", path, i), implicitProjectRelative: true},
					codexArtifactOpenTarget{Kind: "file", Path: fmt.Sprintf("/tmp/reports/review-%d/docs/review-%d.md", i, i)},
				)
			}
			m.codexArtifactLinkScans[path] = codexArtifactLinkScanState{transcriptRev: m.codexTranscriptRevision(path), targets: targets}
			m.codexSelection = textSelection{anchorRow: m.codexViewport.YOffset, anchorCol: 0, currentRow: m.codexViewport.YOffset, dragging: true}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
				if i%2 != 0 {
					msg = tea.KeyMsg{Type: tea.KeyBackspace}
				}
				if action == "selection" {
					msg = tea.MouseMsg{X: i%50 + 1, Y: codexViewportScreenTop + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
				}
				next, _ := m.Update(msg)
				m = normalizeUpdateModel(next)
				_ = m.View()
			}
		})
	}
}
