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
				Instances: []procinspect.ProjectInstance{{
					Process:     procinspect.Process{PID: 2468, PGID: 2468, Command: "vite --host 127.0.0.1", Ports: []int{4017}},
					ProjectPath: projectPath,
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
		"vite pid 2468",
		"4017",
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

func TestEmbeddedSidebarShowsConditionalSessionBrowserAndSummary(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.allProjects = []model.ProjectSummary{{
		Name:                            "demo",
		Path:                            projectPath,
		LatestSessionFormat:             "codex_jsonl",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:            "Use the dashboard summary here.",
	}}
	tokenBudget := int64(5000)
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Model = "gpt-5-codex"
	snapshot.ReasoningEffort = "high"
	snapshot.PendingModel = "gpt-5"
	snapshot.PendingReasoning = "medium"
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
		"Model gpt-5-codex",
		"Reasoning high",
		"Next gpt-5 / medium",
		"Context 6% of 200k",
		"Tokens i10k c0% o2.0k",
		"Goal active 1,200/5,000 tok",
		"ship conditional sidebar sections",
		"Browser",
		"State waiting",
		"Source playwright/browser_click",
		"Page example.com/login",
		"Summary",
		"Use the dashboard summary here.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("conditional sidebar missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Recent Activity") ||
		strings.Contains(rendered, "Conversation history compacted") ||
		strings.Contains(rendered, "$ make test") ||
		strings.Contains(rendered, "Please make the sidebar useful") ||
		strings.Contains(rendered, "Ready to work") {
		t.Fatalf("sidebar summary should not show transcript activity rows:\n%s", rendered)
	}
}

func TestEmbeddedSidebarSummaryWrapsProjectListSummary(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.allProjects = []model.ProjectSummary{{
		Name:                            "demo",
		Path:                            projectPath,
		LatestSessionFormat:             "codex_jsonl",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:            "This summary mirrors the dashboard assessment text and wraps cleanly inside the sidebar without ellipses.",
	}}

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarSummarySection(testEmbeddedSidebarSnapshot(projectPath), 32), "\n"))
	if !strings.Contains(rendered, "Summary") ||
		!strings.Contains(rendered, "dashboard assessment text") ||
		!strings.Contains(rendered, "without ellipses") {
		t.Fatalf("wrapped sidebar summary missing expected text:\n%s", rendered)
	}
	if strings.Contains(rendered, "...") {
		t.Fatalf("sidebar summary should wrap instead of ellipsizing:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 32 {
			t.Fatalf("wrapped sidebar summary line width = %d, want <= 32: %q\n%s", width, line, rendered)
		}
	}
}

func TestEmbeddedSidebarLiveSummaryDropsLargeCodeBlock(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.allProjects = []model.ProjectSummary{{
		Name:                "demo",
		Path:                projectPath,
		LatestSessionFormat: "codex_jsonl",
	}}
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Busy = true
	snapshot.BusySince = m.currentTime().Add(-2 * time.Minute)
	snapshot.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Generated the helper for review:\n```go\n" + strings.Repeat("func generatedSidebarLeak() string { return \"too much code\" }\n", 40) + "```\n",
	}}
	m.codexSnapshots[projectPath] = snapshot

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarSummarySection(snapshot, 42), "\n"))
	if !strings.Contains(rendered, "Generated the helper for review.") {
		t.Fatalf("sidebar summary missing prose:\n%s", rendered)
	}
	for _, unwanted := range []string{"generatedSidebarLeak", "too much code", "```"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("sidebar summary leaked %q:\n%s", unwanted, rendered)
		}
	}
}

func TestEmbeddedSidebarLiveSummaryDoesNotUseCommandOutput(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	m.allProjects = []model.ProjectSummary{{
		Name:                "demo",
		Path:                projectPath,
		LatestSessionFormat: "codex_jsonl",
	}}
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Busy = true
	snapshot.BusySince = m.currentTime().Add(-3 * time.Minute)
	snapshot.Status = "Codex is working..."
	snapshot.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptCommand,
		Text: "$ /bin/zsh -lc \"sed -n '560,900p' src/FractalSpaceScene.cpp\"\n" +
			"# cwd: /Users/davide/dev/repos/romaexe_intros--bjung-alternative-scenes\n" +
			strings.Repeat("Vec2 pa {}; | Vec2 pb {}; | const auto drawRadius = radius * depthMul;\n", 40),
	}}
	m.codexSnapshots[projectPath] = snapshot

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarSummarySection(snapshot, 42), "\n"))
	if !strings.Contains(rendered, "Work in progress") {
		t.Fatalf("command-only live summary should use active fallback:\n%s", rendered)
	}
	for _, unwanted := range []string{"Running /bin/zsh", "FractalSpaceScene.cpp", "Vec2 pa", "drawRadius"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("sidebar summary leaked command output %q:\n%s", unwanted, rendered)
		}
	}
}

func TestEmbeddedSidebarAgentTaskSummaryDropsLargeCodeOnlyBlock(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-agent-task"
	m := testEmbeddedSidebarModel(projectPath)
	m.openAgentTasks = []model.AgentTask{{
		ID:            "agt_sidebar_code",
		WorkspacePath: projectPath,
		Status:        model.AgentTaskStatusWaiting,
		Summary:       "```go\n" + strings.Repeat("func generatedSidebarLeak() string { return \"too much code\" }\n", 40) + "```",
	}}

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarSummarySection(testEmbeddedSidebarSnapshot(projectPath), 42), "\n"))
	if !strings.Contains(rendered, "review task") {
		t.Fatalf("code-only agent-task summary should fall back to task status:\n%s", rendered)
	}
	for _, unwanted := range []string{"generatedSidebarLeak", "too much code", "```"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("sidebar summary leaked %q:\n%s", unwanted, rendered)
		}
	}
}

func TestEmbeddedSidebarTreatsFreshPendingModelAsCurrent(t *testing.T) {
	snapshot := testEmbeddedSidebarSnapshot("/tmp/lcr-sidebar-demo")
	snapshot.Model = "gpt-5-codex"
	snapshot.ReasoningEffort = "medium"
	snapshot.PendingModel = "gpt-5.4"
	snapshot.PendingReasoning = "high"
	snapshot.Entries = []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptSystem, Text: "Started a new embedded Codex session 019demo."},
	}

	rendered := ansi.Strip(strings.Join(embeddedSidebarModelRows(snapshot, 46), "\n"))
	for _, want := range []string{"Model gpt-5.4", "Reasoning high"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("sidebar model rows missing %q:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"Next", "gpt-5-codex", "medium"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("fresh pending model should be shown as current, not %q:\n%s", unwanted, rendered)
		}
	}
}

func TestEmbeddedSidebarSkipsNextWhenPendingHasBeenAppliedBeforeOpen(t *testing.T) {
	snapshot := testEmbeddedSidebarSnapshot("/tmp/lcr-sidebar-demo")
	snapshot.Model = "openai/gpt-5"
	snapshot.ReasoningEffort = "high"

	rendered := ansi.Strip(strings.Join(embeddedSidebarModelRows(snapshot, 46), "\n"))
	for _, want := range []string{"Model openai/gpt-5", "Reasoning high"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("sidebar model rows missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Next") {
		t.Fatalf("sidebar model rows should not show pending next without pending state:\n%s", rendered)
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

func TestVisibleBusyEmbeddedSidebarDiffSkipsKnownNonGitProject(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-scratch"
	m := testEmbeddedSidebarModel(projectPath)
	m.ctx = context.Background()
	m.svc = &service.Service{}
	m.allProjects = []model.ProjectSummary{{
		Path:          projectPath,
		Name:          "scratch",
		PresentOnDisk: true,
		WorktreeKind:  model.WorktreeKindNone,
	}}
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	snapshot.Busy = true
	m.codexSnapshots[projectPath] = snapshot
	delete(m.embeddedSidebarDiffs, projectPath)

	if cmd := m.requestVisibleBusyEmbeddedSidebarDiffRefreshCmd(); cmd != nil {
		t.Fatalf("known non-git project should not auto-refresh sidebar diff")
	}
	if state, ok := m.embeddedSidebarDiffState(projectPath); ok && state.Loading {
		t.Fatalf("known non-git project should not enter loading diff state: %#v", state)
	}

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarDiffSection(projectPath, 46), "\n"))
	if !strings.Contains(rendered, "No git repository") {
		t.Fatalf("non-git sidebar diff section should be stable, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "No diff cached yet") || strings.Contains(rendered, "Preparing diff summary") {
		t.Fatalf("non-git sidebar diff section should not flicker through diff states:\n%s", rendered)
	}
}

func TestEmbeddedSidebarNoGitErrorStaysVisibleWhileRetryLoading(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-scratch"
	m := testEmbeddedSidebarModel(projectPath)
	updated, _ := m.applyEmbeddedSidebarDiffPreviewMsg(embeddedSidebarDiffPreviewMsg{
		projectPath: projectPath,
		seq:         1,
		noGit:       true,
		projectName: "scratch",
	})
	got := normalizeUpdateModel(updated)

	state, ok := got.embeddedSidebarDiffState(projectPath)
	if !ok || !state.NoGit {
		t.Fatalf("sidebar diff state = %#v, want no-git state", state)
	}
	state.Loading = true
	got.embeddedSidebarDiffs[projectPath] = state

	rendered := ansi.Strip(strings.Join(got.renderEmbeddedSidebarDiffSection(projectPath, 46), "\n"))
	if !strings.Contains(rendered, "No git repository") {
		t.Fatalf("loading retry should keep no-git state visible, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Preparing diff summary") {
		t.Fatalf("loading retry should not flicker to preparing text:\n%s", rendered)
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
