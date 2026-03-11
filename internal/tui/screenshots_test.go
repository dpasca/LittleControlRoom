package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/ansi"
)

func TestScreenshotProjectMatchesSupportsAcronyms(t *testing.T) {
	t.Parallel()

	project := model.ProjectSummary{
		Name: "LittleControlRoom",
		Path: "/tmp/LittleControlRoom",
	}

	if !screenshotProjectMatches(project, "LCR") {
		t.Fatalf("expected LCR to match LittleControlRoom")
	}
	if !screenshotProjectMatches(project, "littlecontrolroom") {
		t.Fatalf("expected normalized project name to match")
	}
	if screenshotProjectMatches(project, "assistantlab") {
		t.Fatalf("unexpected non-matching project filter")
	}
}

func TestRenderTerminalHTMLDocumentIncludesEscapedTextAndColors(t *testing.T) {
	t.Parallel()

	rendered, width, height := renderTerminalHTMLDocument("Demo", "\x1b[38;5;81mhello <world>\x1b[0m", 20, 4)
	if width <= 0 || height <= 0 {
		t.Fatalf("html viewport = %dx%d, want positive values", width, height)
	}
	if !strings.Contains(rendered, "<!doctype html>") {
		t.Fatalf("html should include a document doctype: %q", rendered)
	}
	if !strings.Contains(rendered, "hello &lt;world&gt;") {
		t.Fatalf("html should escape terminal text: %q", rendered)
	}
	if !strings.Contains(rendered, "#5fd7ff") {
		t.Fatalf("html should include the ANSI 256 foreground color: %q", rendered)
	}
	if !strings.Contains(rendered, "<div class=\"title\">Demo</div>") {
		t.Fatalf("html should include the terminal title bar label: %q", rendered)
	}
	if !strings.Contains(rendered, "Iosevka") {
		t.Fatalf("html should prefer the Iosevka font stack: %q", rendered)
	}
}

func TestTerminalLineBackgroundRequiresMatchingEdges(t *testing.T) {
	t.Parallel()

	shellBG := "#303030"
	defaultFG := "#d7dbe6"
	defaultBG := "#151821"

	line := terminalLine{
		{text: "> ", style: terminalTextStyle{bg: shellBG, hasBG: true}},
		{text: "message", style: terminalTextStyle{}},
		{text: " ", style: terminalTextStyle{bg: shellBG, hasBG: true}},
	}
	if got := terminalLineBackground(line, defaultFG, defaultBG); got != shellBG {
		t.Fatalf("expected matching edge background %q, got %q", shellBG, got)
	}

	line = terminalLine{
		{text: "plain ", style: terminalTextStyle{}},
		{text: "badge", style: terminalTextStyle{bg: shellBG, hasBG: true}},
	}
	if got := terminalLineBackground(line, defaultFG, defaultBG); got != "" {
		t.Fatalf("expected no line background when only the middle is shaded, got %q", got)
	}
}

func TestScreenshotEmbeddedCodexSnapshotRendersSessionMeta(t *testing.T) {
	t.Parallel()

	project := model.ProjectSummary{
		Name: "LittleControlRoom",
		Path: "/tmp/LittleControlRoom",
	}
	snapshot := screenshotEmbeddedCodexSnapshot(project, time.Date(2026, time.March, 11, 1, 30, 0, 0, time.Local))
	manager := newScreenshotCodexManager(map[string]codexapp.Snapshot{
		project.Path: snapshot,
	})

	m := Model{
		codexManager:        manager,
		codexVisibleProject: project.Path,
		codexHiddenProject:  project.Path,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               112,
		height:              31,
	}
	m.syncCodexViewport(false)
	m.syncCodexComposerSize()

	rendered := ansi.Strip(m.View())
	for _, want := range []string{"Model", "gpt-5.4", "Reasoning", "xhigh", "Context", "94% left", "188,000 tok"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("embedded screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestScreenshotDemoDataSetUsesSafeFixturePaths(t *testing.T) {
	t.Parallel()

	data := screenshotDemoDataSet()
	if len(data.projects) < 3 {
		t.Fatalf("demo projects = %d, want at least 3", len(data.projects))
	}

	found := map[string]bool{}
	for _, project := range data.projects {
		found[project.Name] = true
		if strings.Contains(project.Path, "/Users/") {
			t.Fatalf("demo project path should not use a local home directory: %q", project.Path)
		}
	}

	for _, want := range []string{
		screenshotDemoPrimaryProject,
		screenshotDemoBusyProject,
		screenshotDemoFollowupProject,
	} {
		if !found[want] {
			t.Fatalf("demo projects missing %q", want)
		}
	}
}
