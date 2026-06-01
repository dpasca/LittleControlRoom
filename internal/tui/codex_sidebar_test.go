package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
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
		Provider:    codexapp.ProviderCodex,
		ProjectPath: projectPath,
		ThreadID:    "thread-sidebar",
		Started:     true,
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
		"AI Engineer",
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
