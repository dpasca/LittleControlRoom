package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/scanner"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func testEmbeddedSidebarModel(projectPath string) Model {
	input := newCodexTextarea()
	return Model{
		width:                     118,
		height:                    28,
		nowFn:                     func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) },
		codexVisibleProject:       projectPath,
		codexHiddenProject:        projectPath,
		codexInput:                input,
		codexViewport:             viewport.New(0, 0),
		codexDrafts:               make(map[string]codexDraft),
		codexSnapshots:            map[string]codexapp.Snapshot{projectPath: testEmbeddedSidebarSnapshot(projectPath)},
		codexTranscriptRev:        map[string]uint64{projectPath: 1},
		codexClosedHandled:        make(map[string]struct{}),
		codexToolAnswers:          make(map[string]codexToolAnswerState),
		codexLCAgentStatusVisible: make(map[string]struct{}),
		codexArtifactLinkScans:    make(map[string]codexArtifactLinkScanState),
		runtimeSnapshots: map[string]projectrun.Snapshot{
			projectPath: {
				ID:          "default",
				Default:     true,
				ProjectPath: projectPath,
				Command:     "npm run dev",
				PID:         4321,
				Running:     true,
				StartedAt:   time.Date(2026, 6, 1, 11, 55, 0, 0, time.UTC),
				Ports:       []int{3000},
			},
		},
		runtimeProcessSnapshots: []projectrun.Snapshot{{
			ID:          "default",
			Default:     true,
			ProjectPath: projectPath,
			Command:     "npm run dev",
			PID:         4321,
			Running:     true,
			StartedAt:   time.Date(2026, 6, 1, 11, 55, 0, 0, time.UTC),
			Ports:       []int{3000},
		}},
		processReports: map[string]procinspect.ProjectReport{
			projectPath: {
				ProjectPath: projectPath,
				Findings: []procinspect.Finding{{
					Process: procinspect.Process{PID: 9876, CPU: 64, Command: "node server.js", Ports: []int{5173}},
					Reasons: []string{"orphaned under PID 1", "high CPU"},
				}},
			},
		},
		embeddedSidebarDiffs: map[string]embeddedSidebarDiffState{
			projectPath: {
				ProjectPath: projectPath,
				Preview: &service.DiffPreview{
					ProjectPath: projectPath,
					ProjectName: "demo",
					Branch:      "feat/sidebar",
					Summary:     "2 files changed",
					Files: []service.DiffFilePreview{{
						Path:    "app.go",
						Summary: "app.go",
						Kind:    scanner.GitChangeModified,
					}},
				},
			},
		},
	}
}

func testEmbeddedSidebarSnapshot(projectPath string) codexapp.Snapshot {
	return codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		ProjectPath:              projectPath,
		ThreadID:                 "thread-sidebar",
		Started:                  true,
		ManagedBrowserSessionKey: "managed-sidebar",
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Ready to work.",
		}},
	}
}

func TestRenderCodexViewShowsEmbeddedSidebarSections(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	for _, want := range []string{
		"Active Processes",
		"Diff Summary",
		"npm run dev",
		"node 64%",
		"2 files changed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered sidebar missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "AI Engineer") {
		t.Fatalf("sidebar should not render the redundant title:\n%s", rendered)
	}
}

func TestEmbeddedSidebarDiffUsesVisibleProjectWhenSnapshotPathDiffers(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	snapshotPath := "/tmp/lcr-sidebar-demo-from-snapshot"
	m := testEmbeddedSidebarModel(projectPath)
	m.allProjects = []model.ProjectSummary{
		{Name: "demo", Path: projectPath, RepoDirty: true, PresentOnDisk: true},
		{Name: "snapshot-demo", Path: snapshotPath, RepoDirty: true, PresentOnDisk: true},
	}

	rendered := ansi.Strip(m.renderEmbeddedCodexSidebar(testEmbeddedSidebarSnapshot(snapshotPath), 46, 28))
	if !strings.Contains(rendered, "2 files changed") {
		t.Fatalf("sidebar should use the visible project's cached diff, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Dirty worktree") {
		t.Fatalf("sidebar should not fall back to the snapshot path dirty label, got:\n%s", rendered)
	}
}

func TestEmbeddedSidebarOmitsOptionalSectionsWithoutState(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)

	rendered := ansi.Strip(m.renderEmbeddedCodexSidebar(testEmbeddedSidebarSnapshot(projectPath), 40, 28))
	for _, absent := range []string{
		"AI Engineer",
		"Session",
		"Browser",
		"Recent Activity",
		"Goal",
		"Ready to work",
	} {
		if strings.Contains(rendered, absent) {
			t.Fatalf("sidebar should omit optional %q without relevant state:\n%s", absent, rendered)
		}
	}
}

func TestEmbeddedSidebarShowsConditionalSessionBrowserAndActivity(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	tokenBudget := int64(5000)
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.TokenUsage = &codexapp.TokenUsageSnapshot{
		Total: codexapp.TokenUsageBreakdown{
			InputTokens:  10000,
			OutputTokens: 2000,
		},
		ModelContextWindow: 200000,
	}
	snapshot.Goal = &codexapp.ThreadGoal{
		Objective:   "ship conditional sidebar sections",
		Status:      codexapp.ThreadGoalStatusActive,
		TokenBudget: &tokenBudget,
		TokensUsed:  1200,
	}
	snapshot.BrowserActivity = browserctl.SessionActivity{
		Policy:     settingsAutomaticPlaywrightPolicy,
		State:      browserctl.SessionActivityStateWaitingForUser,
		ServerName: "playwright",
		ToolName:   "browser_click",
	}
	snapshot.CurrentBrowserPageURL = "https://example.com/login?state=demo"
	snapshot.Entries = []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptUser, Text: "Please make the sidebar useful."},
		{Kind: codexapp.TranscriptAgent, Text: "Ready to work."},
		{Kind: codexapp.TranscriptTool, Text: "Bash: make test"},
		{Kind: codexapp.TranscriptCommand, Text: "$ make test\nok"},
		{Kind: codexapp.TranscriptStatus, Text: "Conversation history compacted"},
	}

	rendered := ansi.Strip(m.renderEmbeddedCodexSidebar(snapshot, 46, 40))
	for _, want := range []string{
		"Session",
		"Context 6% of 200k",
		"Tokens i10k c0% o2.0k",
		"Goal active 1,200/5,000 tok",
		"ship conditional sidebar sections",
		"Browser",
		"State waiting",
		"Source playwright/browser_click",
		"Page example.com/login",
		"Recent Activity",
		"note Conversation history compacted",
		"cmd $ make test",
		"tool Bash: make test",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("conditional sidebar missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Please make the sidebar useful") || strings.Contains(rendered, "Ready to work") {
		t.Fatalf("recent activity should skip user and agent chatter:\n%s", rendered)
	}
}

func TestFinishCodexPendingOpenRefreshesSidebarDiffWhenRevealed(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.ctx = context.Background()
	m.svc = &service.Service{}
	delete(m.embeddedSidebarDiffs, projectPath)

	cmd := m.finishCodexPendingOpen(projectPath, testEmbeddedSidebarSnapshot(projectPath), true, true)
	if cmd == nil {
		t.Fatalf("finishCodexPendingOpen should return a refresh command when revealing the session")
	}
	state, ok := m.embeddedSidebarDiffState(projectPath)
	if !ok {
		t.Fatalf("sidebar diff state was not initialized")
	}
	if !state.Loading {
		t.Fatalf("sidebar diff state should be loading after revealed pending open: %#v", state)
	}
}

func TestVisibleBusyEmbeddedSidebarDiffRefreshIsThrottled(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	m := testEmbeddedSidebarModel(projectPath)
	m.ctx = context.Background()
	m.svc = &service.Service{}
	m.nowFn = func() time.Time { return now }
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Busy = true
	m.codexSnapshots[projectPath] = snapshot
	delete(m.embeddedSidebarDiffs, projectPath)

	cmd := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd()
	if cmd == nil {
		t.Fatalf("visible busy session should request sidebar diff refresh")
	}
	state, ok := m.embeddedSidebarDiffState(projectPath)
	if !ok || !state.Loading {
		t.Fatalf("sidebar diff state should be loading after auto refresh: %#v", state)
	}
	if next := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd(); next != nil {
		t.Fatalf("in-flight sidebar diff refresh should not queue another command")
	}

	state.Loading = false
	m.embeddedSidebarDiffs[projectPath] = state
	now = now.Add(embeddedSidebarDiffAutoInterval - time.Millisecond)
	if next := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd(); next != nil {
		t.Fatalf("sidebar diff refresh should be throttled before the interval")
	}

	now = now.Add(time.Millisecond)
	if next := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd(); next == nil {
		t.Fatalf("sidebar diff refresh should run again after the throttle interval")
	}
}

func TestCodexSidebarAltSTogglesSidebarAndSession(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	got := normalizeUpdateModel(updated)
	if got.codexPanelFocus != embeddedCodexFocusSidebar {
		t.Fatalf("focus = %q, want sidebar", got.codexPanelFocus)
	}

	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	got = normalizeUpdateModel(updated)
	if got.codexPanelFocus != embeddedCodexFocusMain {
		t.Fatalf("second Alt+S focus = %q, want main session", got.codexPanelFocus)
	}
}

func TestCodexBannerAdvertisesSidebarShortcut(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)

	rendered := ansi.Strip(m.renderCodexBanner(testEmbeddedSidebarSnapshot(projectPath), 118))
	if !strings.Contains(rendered, "Alt+S sidebar") {
		t.Fatalf("banner should advertise sidebar shortcut: %q", rendered)
	}

	m.codexPanelFocus = embeddedCodexFocusSidebar
	rendered = ansi.Strip(m.renderCodexBanner(testEmbeddedSidebarSnapshot(projectPath), 118))
	if !strings.Contains(rendered, "Alt+S session") {
		t.Fatalf("focused sidebar banner should advertise return shortcut: %q", rendered)
	}
}

func TestCodexTerminalSlashOpensSystemTerminal(t *testing.T) {
	projectPath := t.TempDir()

	previousOpener := externalTerminalOpener
	defer func() { externalTerminalOpener = previousOpener }()

	called := ""
	externalTerminalOpener = func(path string) error {
		called = path
		return nil
	}

	m := testEmbeddedSidebarModel(projectPath)
	m.setCodexComposerValue("/terminal", len("/terminal"))
	m.persistVisibleCodexDraft()

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := normalizeUpdateModel(updated)
	if got.status != "Opening project terminal..." {
		t.Fatalf("status = %q, want opening terminal status", got.status)
	}
	if cmd == nil {
		t.Fatalf("/terminal should return a command")
	}

	rawMsg := cmd()
	openMsg, ok := rawMsg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", rawMsg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Opened project terminal" {
		t.Fatalf("browserOpenMsg.status = %q, want terminal success", openMsg.status)
	}
	if called != projectPath {
		t.Fatalf("opened terminal path = %q, want %q", called, projectPath)
	}
}

func TestDiffAskReturnsToEmbeddedEngineerWithPrompt(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.codexVisibleProject = ""
	m.diffView = newDiffViewState(projectPath, "demo")
	m.diffView.returnToCodexProject = projectPath

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := normalizeUpdateModel(updated)
	if got.diffView != nil {
		t.Fatalf("diffView should close when asking engineer")
	}
	if got.codexVisibleProject != projectPath {
		t.Fatalf("codexVisibleProject = %q, want %q", got.codexVisibleProject, projectPath)
	}
	if !strings.Contains(got.codexInput.Value(), "review the current diff") {
		t.Fatalf("composer = %q, want diff review prompt", got.codexInput.Value())
	}
}

func TestDiffEscReturnsToEmbeddedEngineerWithoutPrompt(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.codexVisibleProject = ""
	m.diffView = newDiffViewState(projectPath, "demo")
	m.diffView.returnToCodexProject = projectPath

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := normalizeUpdateModel(updated)
	if got.diffView != nil {
		t.Fatalf("diffView should close on Esc when returning to engineer")
	}
	if got.codexVisibleProject != projectPath {
		t.Fatalf("codexVisibleProject = %q, want %q", got.codexVisibleProject, projectPath)
	}
	if strings.TrimSpace(got.codexInput.Value()) != "" {
		t.Fatalf("composer = %q, want empty composer on Esc", got.codexInput.Value())
	}
	if got.status != "Back to engineer session" {
		t.Fatalf("status = %q, want back-to-session status", got.status)
	}
}
