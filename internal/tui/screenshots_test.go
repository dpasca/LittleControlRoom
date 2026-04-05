package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"

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
	if !strings.Contains(rendered, "background:#000000") {
		t.Fatalf("html should render against a true black background: %q", rendered)
	}
	if !strings.Contains(rendered, "<title>Demo</title>") {
		t.Fatalf("html should include the document title: %q", rendered)
	}
	if strings.Contains(rendered, "titlebar") || strings.Contains(rendered, "dot-close") {
		t.Fatalf("html should not include fake window chrome: %q", rendered)
	}
	if !strings.Contains(rendered, "class=\"shell-wrap\"") {
		t.Fatalf("html should render the borderless shell wrapper: %q", rendered)
	}
	if !strings.Contains(rendered, "Iosevka") {
		t.Fatalf("html should prefer the Iosevka font stack: %q", rendered)
	}
}

func TestRenderTerminalHTMLDocumentIncludesTrueColorEscapes(t *testing.T) {
	t.Parallel()

	rendered, _, _ := renderTerminalHTMLDocument("Demo", "\x1b[38;2;76;52;56mhello \x1b[48;2;40;66;53mworld\x1b[0m", 20, 4)
	if !strings.Contains(rendered, "#4c3438") {
		t.Fatalf("html should preserve truecolor foreground escapes: %q", rendered)
	}
	if !strings.Contains(rendered, "background:#284235") {
		t.Fatalf("html should preserve truecolor background escapes: %q", rendered)
	}
}

func TestRenderTerminalHTMLDocumentUsesPixelCellsForBlockGlyphRuns(t *testing.T) {
	t.Parallel()

	rendered, _, _ := renderTerminalHTMLDocument("Demo", "\x1b[38;2;16;32;48;48;2;4;8;12m▀█▄\x1b[0m", 3, 1)
	if !strings.Contains(rendered, `class="pixel-run"`) {
		t.Fatalf("html should render block glyph runs through pixel cells: %q", rendered)
	}
	for _, want := range []string{
		`background:linear-gradient(to bottom,#102030 0 50%,#04080c 50% 100%)`,
		`background:#102030`,
		`background:linear-gradient(to bottom,#04080c 0 50%,#102030 50% 100%)`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("html missing pixel-cell style %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, ">▀█▄<") {
		t.Fatalf("html should not rely on raw block glyph text for pixel-art runs: %q", rendered)
	}
}

func TestTerminalLineBackgroundRequiresMatchingEdges(t *testing.T) {
	t.Parallel()

	shellBG := "#303030"
	defaultFG := "#d7dbe6"
	defaultBG := "#000000"

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

func TestScreenshotRuntimeSnapshotRendersRuntimePane(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.Local)
	project := model.ProjectSummary{
		Name:          "FractalMech",
		Path:          "/tmp/FractalMech",
		PresentOnDisk: true,
		RunCommand:    "pnpm dev",
	}

	m := Model{
		width:  112,
		height: 31,
		projects: []model.ProjectSummary{
			project,
		},
		selected:         0,
		focusedPane:      focusRuntime,
		runtimeSnapshots: map[string]projectrun.Snapshot{project.Path: screenshotLiveRuntimeSnapshot(project, now)},
		nowFn: func() time.Time {
			return now
		},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"Runtime - FractalMech",
		"Run cmd: pnpm dev",
		"URL: http://127.0.0.1:3000/ (ports: 3000)",
		"serving FractalMech assets",
		"watching for changes...",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestScreenshotSetupRendersBackendChoicesAndHaikuHint(t *testing.T) {
	t.Parallel()

	m := Model{
		width:          112,
		height:         31,
		homeDir:        "/Users/davide",
		setupMode:      true,
		setupChecked:   true,
		setupSelected:  mSetupSelectionForTest(config.AIBackendClaude),
		setupModelTier: config.ModelTierCheap,
		setupSnapshot:  screenshotSetupSnapshot(),
		status:         "Choose how Little Control Room should run AI summaries, classifications, and commit help.",
		projects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
		},
		allProjects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
		},
	}
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.OpenCodeModelTier = string(config.ModelTierCheap)
	m.settingsBaseline = &settings
	m.detail = model.ProjectDetail{
		Summary: model.ProjectSummary{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"Setup",
		"Config: ~/.little-control-room/config.toml",
		"Codex",
		"OpenCode",
		"Claude Code",
		"OpenAI API key",
		"Disabled",
		"Haiku",
		"active",
		"ready",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestScreenshotSettingsRendersLocalBackendFields(t *testing.T) {
	t.Parallel()

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX
	settings.MLXBaseURL = "http://127.0.0.1:8080/v1"
	settings.MLXAPIKey = "mlx"
	settings.OllamaBaseURL = "http://127.0.0.1:11434/v1"
	settings.OllamaAPIKey = "ollama"

	m := Model{
		width:        112,
		height:       31,
		homeDir:      "/Users/davide",
		settingsMode: true,
		status:       "Editing settings. Enter to save, Esc to cancel",
		projects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
		},
		allProjects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
		},
	}
	m.settingsFields = newSettingsFields(settings)
	m.settingsSelected = settingsFieldMLXBaseURL
	m.settingsBaseline = &settings
	m.detail = model.ProjectDetail{
		Summary: model.ProjectSummary{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", PresentOnDisk: true},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"Settings",
		"AI backend: MLX",
		"MLX base URL",
		"MLX API key",
		"Ollama base URL",
		"Ollama API key",
		"http://127.0.0.1:8080/v1",
		"http://127.0.0.1:11434/v1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("settings screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestSanitizeScreenshotProjectSummaryHidesQueuedAssessmentState(t *testing.T) {
	t.Parallel()

	summary := sanitizeScreenshotProjectSummary(model.ProjectSummary{
		Name:                             "LittleControlRoom",
		Path:                             "/Users/davide/dev/repos/LittleControlRoom",
		LatestSessionFormat:              "modern",
		LatestSessionClassification:      model.ClassificationPending,
		LatestSessionClassificationStage: model.ClassificationStageQueued,
		LatestSessionClassificationType:  model.SessionCategoryInProgress,
	})

	if summary.LatestSessionClassification != "" {
		t.Fatalf("LatestSessionClassification = %q, want empty", summary.LatestSessionClassification)
	}
	if got := projectAssessmentLabelAt(summary, time.Time{}); got == "queued" {
		t.Fatalf("projectAssessmentLabelAt() = %q, want non-queued label", got)
	}
}

func TestScreenshotDashboardSelectionProjectPrefersStableAssessment(t *testing.T) {
	t.Parallel()

	preferred := model.ProjectSummary{
		Name:                        "LittleControlRoom",
		Path:                        "/tmp/LittleControlRoom",
		LatestSessionClassification: model.ClassificationPending,
	}
	live := model.ProjectSummary{
		Name:                            "FractalMech",
		Path:                            "/tmp/FractalMech",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
	}
	runtime := live
	candidate := model.ProjectSummary{
		Name:                            "okmain",
		Path:                            "/tmp/okmain",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
	}

	got := screenshotDashboardSelectionProject([]model.ProjectSummary{preferred, live, candidate}, preferred, live, runtime)
	if got.Path != candidate.Path {
		t.Fatalf("dashboard selection = %q, want %q", got.Path, candidate.Path)
	}
}

func TestScreenshotDiffViewFixtureRendersSelectedPatch(t *testing.T) {
	t.Parallel()

	project := model.ProjectSummary{
		Name: "LittleControlRoom",
		Path: "/tmp/LittleControlRoom",
	}

	m := Model{
		diffView: screenshotDiffView(project),
		width:    112,
		height:   31,
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"Staged (1)",
		"Unstaged (3)",
		"internal/tui/diff_view.go",
		"Before",
		"After",
		"func diffModeLabel",
		"side-by-side",
		"Alt+Up",
		"stage",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("diff screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestScreenshotImageDiffViewFixtureRendersImagePreview(t *testing.T) {
	t.Parallel()

	project := model.ProjectSummary{
		Name: "LittleControlRoom",
		Path: "/tmp/LittleControlRoom",
	}

	m := Model{
		diffView: screenshotImageDiffView(project),
		width:    112,
		height:   31,
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"assets/sprites/bunker_guard.png",
		"HEAD image",
		"Working tree image",
		"FractalMech-style bunker sprite pass",
		"Alt+Up",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("image diff screenshot render missing %q: %q", want, rendered)
		}
	}
}

func TestScreenshotImageDiffFixturePrefersSiblingFractalMechJets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projectPath := filepath.Join(root, "LittleControlRoom")
	fractalPath := filepath.Join(root, "FractalMech", "public", "assets", "sprites", "enemies")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project path: %v", err)
	}
	if err := os.MkdirAll(fractalPath, 0o755); err != nil {
		t.Fatalf("mkdir fractal path: %v", err)
	}

	oldPNG := mustScreenshotPNG(color.RGBA{R: 180, G: 190, B: 200, A: 255})
	newPNG := mustScreenshotPNG(color.RGBA{R: 120, G: 140, B: 160, A: 255})
	oldPath := filepath.Join(fractalPath, "jet_f15_gray_camo.png")
	newPath := filepath.Join(fractalPath, "jet_f16_gray_camo.png")
	if err := os.WriteFile(oldPath, oldPNG, 0o644); err != nil {
		t.Fatalf("write old jet png: %v", err)
	}
	if err := os.WriteFile(newPath, newPNG, 0o644); err != nil {
		t.Fatalf("write new jet png: %v", err)
	}

	path, body, note, oldImage, newImage := screenshotImageDiffFixture(model.ProjectSummary{
		Name: "LittleControlRoom",
		Path: projectPath,
	})

	if path != "public/assets/sprites/enemies/jet_f15_gray_camo.png" {
		t.Fatalf("fixture path = %q, want jet sprite path", path)
	}
	if !strings.Contains(body, "F-15") || !strings.Contains(body, "F-16") {
		t.Fatalf("fixture body should describe the jet comparison: %q", body)
	}
	if !strings.Contains(note, "sibling FractalMech jet sprite pair") {
		t.Fatalf("fixture note should mention sibling jet sprites: %q", note)
	}
	if !bytes.Equal(oldImage, oldPNG) || !bytes.Equal(newImage, newPNG) {
		t.Fatalf("fixture should load the sibling jet png bytes")
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

func mustScreenshotPNG(fill color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func TestSanitizeScreenshotProjectDetailRewritesLocalPaths(t *testing.T) {
	t.Parallel()

	detail := model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:                             "/Users/davide/dev/repos/LittleControlRoom",
			MovedFromPath:                    "/Users/davide/dev/repos/BatonDeck",
			LatestSessionDetectedProjectPath: "/Users/davide/dev/repos/LittleControlRoom",
			LatestSessionSummary:             "Path check: /Users/davide/dev/repos/LittleControlRoom",
			LatestCompletedSessionSummary:    "Completed: /Users/davide/dev/repos/LittleControlRoom looks healthy",
		},
		Reasons: []model.AttentionReason{
			{Text: "Recent activity in /Users/davide/dev/poncle_repos/quickgame_03"},
		},
		Sessions: []model.SessionEvidence{{
			ProjectPath:         "/Users/davide/dev/repos/LittleControlRoom",
			DetectedProjectPath: "/Users/davide/dev/repos/LittleControlRoom",
			SessionFile:         "/Users/davide/.codex/sessions/demo.jsonl",
		}},
		Artifacts: []model.ArtifactEvidence{{
			Path: "/Users/davide/.local/share/opencode/opencode.db",
			Note: "artifact under /Users/davide/.local/share/opencode/opencode.db",
		}},
		RecentEvents: []model.StoredEvent{{
			ProjectPath: "/Users/davide/dev/repos/LittleControlRoom",
			Payload:     "payload /Users/davide/dev/repos/LittleControlRoom",
		}},
		LatestSessionClassification: &model.SessionClassification{
			Status:      model.ClassificationCompleted,
			ProjectPath: "/Users/davide/dev/repos/LittleControlRoom",
			SessionFile: "/Users/davide/.codex/sessions/demo.jsonl",
			Summary:     "classification /Users/davide/dev/repos/LittleControlRoom",
			LastError:   "error /Users/davide/.little-control-room/config.toml",
		},
	}

	got := sanitizeScreenshotProjectDetail(detail)
	for _, candidate := range []string{
		got.Summary.Path,
		got.Summary.MovedFromPath,
		got.Summary.LatestSessionDetectedProjectPath,
		got.Summary.LatestSessionSummary,
		got.Summary.LatestCompletedSessionSummary,
		got.Reasons[0].Text,
		got.Sessions[0].ProjectPath,
		got.Sessions[0].DetectedProjectPath,
		got.Sessions[0].SessionFile,
		got.Artifacts[0].Path,
		got.Artifacts[0].Note,
		got.RecentEvents[0].ProjectPath,
		got.RecentEvents[0].Payload,
		got.LatestSessionClassification.ProjectPath,
		got.LatestSessionClassification.SessionFile,
		got.LatestSessionClassification.Summary,
		got.LatestSessionClassification.LastError,
	} {
		if strings.Contains(candidate, "/Users/davide") {
			t.Fatalf("sanitizeScreenshotProjectDetail() left a local path behind: %q", candidate)
		}
	}
	if got.Summary.Path != "/workspaces/repos/LittleControlRoom" {
		t.Fatalf("summary path = %q", got.Summary.Path)
	}
	if got.Reasons[0].Text != "Recent activity in /workspaces/poncle_repos/quickgame_03" {
		t.Fatalf("reason text = %q", got.Reasons[0].Text)
	}
	if got.Sessions[0].SessionFile != "/workspaces/.codex/sessions/demo.jsonl" {
		t.Fatalf("session file = %q", got.Sessions[0].SessionFile)
	}
}
