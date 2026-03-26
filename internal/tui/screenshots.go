package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/scanner"
	"lcroom/internal/service"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type ScreenshotAsset struct {
	Name   string
	Title  string
	ANSI   string
	HTML   string
	Width  int
	Height int
}

type ScreenshotReport struct {
	Assets                []ScreenshotAsset
	MatchedProjects       []string
	MissingProjectFilters []string
	Warnings              []string
}

func GenerateScreenshots(ctx context.Context, svc *service.Service, cfg config.ScreenshotConfig) (ScreenshotReport, error) {
	if svc == nil {
		return ScreenshotReport{}, fmt.Errorf("service required")
	}

	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	}()

	data, err := loadScreenshotData(ctx, svc, cfg)
	if err != nil {
		return ScreenshotReport{}, err
	}
	projects := data.projects

	filtered, missingFilters := filterScreenshotProjects(projects, cfg.ProjectFilters)
	if len(filtered) == 0 {
		return ScreenshotReport{}, fmt.Errorf("no visible projects matched the screenshot filters")
	}

	selectedProject, selectedWarning := resolveScreenshotProject(filtered, cfg.SelectedProject)
	liveProject, liveWarning := resolveScreenshotProject(filtered, cfg.LiveCodexProject)
	if strings.TrimSpace(liveProject.Path) == "" {
		liveProject = selectedProject
	}

	now := screenshotReferenceTime(filtered)
	listSnapshots := screenshotListLiveCodexSnapshots(filtered, now)
	runtimeProject := selectedProject
	runtimeWarning := ""
	if filter := strings.TrimSpace(cfg.LiveRuntimeProject); filter != "" {
		if project, ok := findScreenshotProject(filtered, filter); ok {
			runtimeProject = project
		} else {
			runtimeWarning = fmt.Sprintf("Screenshot runtime project %q was not found; using %s instead.", filter, screenshotProjectLabel(selectedProject))
		}
	}
	runtimeSnapshots := screenshotRuntimeSnapshots(runtimeProject, now)
	dashboardProject := screenshotDashboardSelectionProject(filtered, selectedProject, liveProject, runtimeProject)

	assets := make([]ScreenshotAsset, 0, 6)

	mainModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, dashboardProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	if len(listSnapshots) > 0 {
		mainModel.codexManager = newScreenshotCodexManager(listSnapshots)
	}
	assets = append(assets, screenshotAsset("main-panel", "Main Panel", mainModel.View(), cfg))

	runtimeModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, runtimeProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	runtimeModel.codexManager = newScreenshotCodexManager(cloneScreenshotSnapshots(listSnapshots))
	runtimeModel.runtimeSnapshots = cloneScreenshotRuntimeSnapshots(runtimeSnapshots)
	_ = runtimeModel.openRuntimeInspectorForSelection()
	assets = append(assets, screenshotAsset("main-panel-live-runtime", "Main Panel With Live Runtime", runtimeModel.View(), cfg))

	codexModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, liveProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	embeddedSnapshots := cloneScreenshotSnapshots(listSnapshots)
	embeddedSnapshots[liveProject.Path] = screenshotEmbeddedCodexSnapshot(liveProject, now)
	codexModel.codexManager = newScreenshotCodexManager(embeddedSnapshots)
	codexModel.codexVisibleProject = liveProject.Path
	codexModel.codexHiddenProject = liveProject.Path
	codexModel.syncCodexViewport(false)
	codexModel.syncCodexComposerSize()
	assets = append(assets, screenshotAsset("codex-embedded", "Embedded Codex Session", codexModel.View(), cfg))

	diffModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, selectedProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	diffModel.diffView = screenshotDiffView(selectedProject)
	diffModel.syncDiffView(true)
	diffModel.status = diffViewReadyStatus(*diffModel.diffView)
	assets = append(assets, screenshotAsset("diff-view", "Diff View", diffModel.View(), cfg))

	imageDiffModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, selectedProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	imageDiffModel.diffView = screenshotImageDiffView(selectedProject)
	imageDiffModel.syncDiffView(true)
	imageDiffModel.status = diffViewReadyStatus(*imageDiffModel.diffView)
	assets = append(assets, screenshotAsset("diff-view-image", "Image Diff View", imageDiffModel.View(), cfg))

	commitModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, selectedProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	commitModel.commitPreview = screenshotCommitPreview(selectedProject)
	commitModel.status = commitPreviewReadyStatus(commitModel.commitPreview.CanPush)
	assets = append(assets, screenshotAsset("commit-preview", "Commit Preview", commitModel.View(), cfg))

	todoProject := screenshotProjectWithMostTodos(ctx, svc, data, filtered, selectedProject)
	todoModel, err := buildScreenshotDashboardModel(ctx, svc, data, filtered, todoProject.Path, cfg, now)
	if err != nil {
		return ScreenshotReport{}, err
	}
	todoModel.todoDialog = &todoDialogState{
		ProjectPath: todoProject.Path,
		ProjectName: noteProjectTitle(todoProject.Path, todoProject.Name),
		Selected:    1,
	}
	todoModel.syncTodoDialogSelection()
	todoModel.status = "TODO list open. Enter starts selected item; a adds, e edits, space toggles"
	assets = append(assets, screenshotAsset("todo-dialog", "TODO Dialog", todoModel.View(), cfg))

	report := ScreenshotReport{
		Assets:                assets,
		MatchedProjects:       screenshotProjectLabels(filtered),
		MissingProjectFilters: missingFilters,
	}
	if selectedWarning != "" {
		report.Warnings = append(report.Warnings, selectedWarning)
	}
	if liveWarning != "" {
		report.Warnings = append(report.Warnings, liveWarning)
	}
	if runtimeWarning != "" {
		report.Warnings = append(report.Warnings, runtimeWarning)
	}
	return report, nil
}

func loadScreenshotData(ctx context.Context, svc *service.Service, cfg config.ScreenshotConfig) (screenshotDataSet, error) {
	if cfg.DemoData {
		return screenshotDemoDataSet(), nil
	}

	projects, err := svc.Store().ListProjects(ctx, false)
	if err != nil {
		return screenshotDataSet{}, fmt.Errorf("list projects: %w", err)
	}

	sanitizedProjects := make([]model.ProjectSummary, 0, len(projects))
	details := make(map[string]model.ProjectDetail, len(projects))
	for _, project := range projects {
		detail, err := svc.Store().GetProjectDetail(ctx, project.Path, 20)
		if err != nil {
			return screenshotDataSet{}, fmt.Errorf("load screenshot detail %s: %w", project.Path, err)
		}
		sanitizedDetail := sanitizeScreenshotProjectDetail(detail)
		sanitizedProjects = append(sanitizedProjects, sanitizedDetail.Summary)
		details[sanitizedDetail.Summary.Path] = sanitizedDetail
	}

	return screenshotDataSet{
		projects: sanitizedProjects,
		details:  details,
	}, nil
}

func screenshotAsset(name, title, rendered string, cfg config.ScreenshotConfig) ScreenshotAsset {
	html, width, height := renderTerminalHTMLDocument(title, rendered, cfg.TerminalWidth, cfg.TerminalHeight)
	return ScreenshotAsset{
		Name:   name,
		Title:  title,
		ANSI:   rendered,
		HTML:   html,
		Width:  width,
		Height: height,
	}
}

func buildScreenshotDashboardModel(ctx context.Context, svc *service.Service, data screenshotDataSet, projects []model.ProjectSummary, selectedPath string, cfg config.ScreenshotConfig, now time.Time) (Model, error) {
	m := New(ctx, svc)
	m.loading = false
	m.err = nil
	m.width = cfg.TerminalWidth
	m.height = cfg.TerminalHeight
	m.nowFn = func() time.Time { return now }
	m.excludeProjectPatterns = nil
	m.allProjects = append([]model.ProjectSummary(nil), projects...)
	m.visibility = visibilityAIFolders
	m.rebuildProjectList(selectedPath)
	if len(m.projects) == 0 {
		m.visibility = visibilityAllFolders
		m.rebuildProjectList(selectedPath)
	}
	if len(m.projects) == 0 {
		return Model{}, fmt.Errorf("no screenshot projects remained after visibility filtering")
	}
	if idx := m.indexByPath(selectedPath); idx >= 0 {
		m.selected = idx
	}
	project, ok := m.selectedProject()
	if !ok {
		return Model{}, fmt.Errorf("selected screenshot project unavailable")
	}
	detail, err := data.projectDetail(ctx, svc, project.Path, 20)
	if err != nil {
		return Model{}, fmt.Errorf("load project detail %s: %w", project.Path, err)
	}
	m.detail = detail
	m.status = fmt.Sprintf("Loaded %d projects (%s, %s)", len(m.projects), m.sortMode, visibilityLabel(m.visibility))
	m.syncDetailViewport(false)
	return m, nil
}

func screenshotProjectWithMostTodos(ctx context.Context, svc *service.Service, data screenshotDataSet, projects []model.ProjectSummary, fallback model.ProjectSummary) model.ProjectSummary {
	best := fallback
	bestCount := 0
	for _, p := range projects {
		detail, err := data.projectDetail(ctx, svc, p.Path, 1)
		if err != nil {
			continue
		}
		if len(detail.Todos) > bestCount {
			bestCount = len(detail.Todos)
			best = p
		}
	}
	return best
}

func (d screenshotDataSet) projectDetail(ctx context.Context, svc *service.Service, path string, eventLimit int) (model.ProjectDetail, error) {
	if d.details != nil {
		detail, ok := d.details[path]
		if !ok {
			return model.ProjectDetail{}, fmt.Errorf("demo project detail not found: %s", path)
		}
		return detail, nil
	}
	return svc.Store().GetProjectDetail(ctx, path, eventLimit)
}

func sanitizeScreenshotProjectDetail(detail model.ProjectDetail) model.ProjectDetail {
	detail.Summary = sanitizeScreenshotProjectSummary(detail.Summary)
	for i := range detail.Reasons {
		detail.Reasons[i].Text = sanitizeScreenshotText(detail.Reasons[i].Text)
	}
	for i := range detail.Sessions {
		detail.Sessions[i] = sanitizeScreenshotSession(detail.Sessions[i])
	}
	for i := range detail.Artifacts {
		detail.Artifacts[i].Path = sanitizeScreenshotPath(detail.Artifacts[i].Path)
		detail.Artifacts[i].Note = sanitizeScreenshotText(detail.Artifacts[i].Note)
	}
	for i := range detail.Todos {
		detail.Todos[i].ProjectPath = sanitizeScreenshotPath(detail.Todos[i].ProjectPath)
	}
	for i := range detail.RecentEvents {
		detail.RecentEvents[i].ProjectPath = sanitizeScreenshotPath(detail.RecentEvents[i].ProjectPath)
		detail.RecentEvents[i].Payload = sanitizeScreenshotText(detail.RecentEvents[i].Payload)
	}
	if detail.LatestSessionClassification != nil {
		classification := *detail.LatestSessionClassification
		classification.ProjectPath = sanitizeScreenshotPath(classification.ProjectPath)
		classification.SessionFile = sanitizeScreenshotPath(classification.SessionFile)
		classification.Summary = sanitizeScreenshotText(classification.Summary)
		classification.LastError = sanitizeScreenshotText(classification.LastError)
		detail.LatestSessionClassification = normalizeScreenshotClassificationDetail(&classification)
	}
	return detail
}

func sanitizeScreenshotProjectSummary(summary model.ProjectSummary) model.ProjectSummary {
	summary.Path = sanitizeScreenshotPath(summary.Path)
	summary.Note = sanitizeScreenshotText(summary.Note)
	summary.MovedFromPath = sanitizeScreenshotPath(summary.MovedFromPath)
	summary.LatestSessionDetectedProjectPath = sanitizeScreenshotPath(summary.LatestSessionDetectedProjectPath)
	summary.LatestSessionSummary = sanitizeScreenshotText(summary.LatestSessionSummary)
	summary.LatestCompletedSessionSummary = sanitizeScreenshotText(summary.LatestCompletedSessionSummary)
	return normalizeScreenshotClassificationSummary(summary)
}

func normalizeScreenshotClassificationSummary(summary model.ProjectSummary) model.ProjectSummary {
	if summary.LatestSessionClassification == model.ClassificationCompleted {
		return summary
	}
	summary.LatestSessionClassification = ""
	summary.LatestSessionClassificationStage = ""
	summary.LatestSessionClassificationType = model.SessionCategoryUnknown
	summary.LatestSessionClassificationStageStartedAt = time.Time{}
	summary.LatestSessionClassificationUpdatedAt = time.Time{}
	return summary
}

func normalizeScreenshotClassificationDetail(classification *model.SessionClassification) *model.SessionClassification {
	if classification == nil {
		return nil
	}
	if classification.Status != model.ClassificationCompleted {
		return nil
	}
	return classification
}

func sanitizeScreenshotSession(session model.SessionEvidence) model.SessionEvidence {
	session.ProjectPath = sanitizeScreenshotPath(session.ProjectPath)
	session.DetectedProjectPath = sanitizeScreenshotPath(session.DetectedProjectPath)
	session.SessionFile = sanitizeScreenshotPath(session.SessionFile)
	return session
}

func sanitizeScreenshotPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return sanitizeScreenshotText(path)
}

func sanitizeScreenshotText(text string) string {
	replacements := []struct {
		from string
		to   string
	}{
		{from: "/Users/davide/dev/repos/", to: "/workspaces/repos/"},
		{from: "/Users/davide/dev/repos", to: "/workspaces/repos"},
		{from: "/Users/davide/dev/poncle_repos/", to: "/workspaces/poncle_repos/"},
		{from: "/Users/davide/dev/poncle_repos", to: "/workspaces/poncle_repos"},
		{from: "/Users/davide/projects_control_center/", to: "/workspaces/projects_control_center/"},
		{from: "/Users/davide/projects_control_center", to: "/workspaces/projects_control_center"},
		{from: "/Users/davide/.codex/", to: "/workspaces/.codex/"},
		{from: "/Users/davide/.codex", to: "/workspaces/.codex"},
		{from: "/Users/davide/.local/share/opencode/", to: "/workspaces/.local/share/opencode/"},
		{from: "/Users/davide/.local/share/opencode", to: "/workspaces/.local/share/opencode"},
		{from: "/Users/davide/.little-control-room/", to: "/workspaces/.little-control-room/"},
		{from: "/Users/davide/.little-control-room", to: "/workspaces/.little-control-room"},
		{from: "/Users/davide/", to: "/workspaces/"},
		{from: "/Users/davide", to: "/workspaces"},
	}
	for _, replacement := range replacements {
		text = strings.ReplaceAll(text, replacement.from, replacement.to)
	}
	return text
}

func filterScreenshotProjects(projects []model.ProjectSummary, filters []string) ([]model.ProjectSummary, []string) {
	if len(filters) == 0 {
		return append([]model.ProjectSummary(nil), projects...), nil
	}

	filtered := make([]model.ProjectSummary, 0, len(filters))
	seen := map[string]struct{}{}
	missing := []string{}
	for _, filter := range filters {
		found := false
		for _, project := range projects {
			if !screenshotProjectMatches(project, filter) {
				continue
			}
			found = true
			if _, ok := seen[project.Path]; ok {
				continue
			}
			seen[project.Path] = struct{}{}
			filtered = append(filtered, project)
		}
		if !found {
			missing = append(missing, filter)
		}
	}
	return filtered, missing
}

func resolveScreenshotProject(projects []model.ProjectSummary, filter string) (model.ProjectSummary, string) {
	if len(projects) == 0 {
		return model.ProjectSummary{}, ""
	}
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return projects[0], ""
	}
	for _, project := range projects {
		if screenshotProjectMatches(project, filter) {
			return project, ""
		}
	}
	return projects[0], fmt.Sprintf("Screenshot project %q was not found; using %s instead.", filter, screenshotProjectLabel(projects[0]))
}

func screenshotProjectMatches(project model.ProjectSummary, filter string) bool {
	filter = normalizeScreenshotProjectToken(filter)
	if filter == "" {
		return false
	}

	candidates := []string{
		project.Name,
		filepath.Base(filepath.Clean(project.Path)),
	}
	for _, candidate := range candidates {
		if normalizeScreenshotProjectToken(candidate) == filter {
			return true
		}
		if screenshotProjectAcronym(candidate) == filter {
			return true
		}
	}
	return false
}

func normalizeScreenshotProjectToken(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func screenshotProjectAcronym(value string) string {
	words := screenshotProjectWords(value)
	if len(words) == 0 {
		return ""
	}
	var out strings.Builder
	for _, word := range words {
		r, _ := utf8.DecodeRuneInString(word)
		if r != utf8.RuneError && r != 0 {
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

func screenshotProjectWords(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	words := []string{}
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		words = append(words, string(current))
		current = current[:0]
	}

	var prev rune
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if len(current) > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			current = append(current, r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return words
}

func screenshotReferenceTime(projects []model.ProjectSummary) time.Time {
	latest := time.Time{}
	for _, project := range projects {
		if project.LastActivity.After(latest) {
			latest = project.LastActivity
		}
	}
	if latest.IsZero() {
		return time.Now()
	}
	return latest.Add(5 * time.Minute)
}

func screenshotProjectLabels(projects []model.ProjectSummary) []string {
	out := make([]string, 0, len(projects))
	for _, project := range projects {
		out = append(out, screenshotProjectLabel(project))
	}
	return out
}

func screenshotProjectLabel(project model.ProjectSummary) string {
	if strings.TrimSpace(project.Name) != "" {
		return project.Name
	}
	return filepath.Base(filepath.Clean(project.Path))
}

type screenshotCodexSession struct {
	projectPath string
	snapshot    codexapp.Snapshot
}

func (s *screenshotCodexSession) ProjectPath() string {
	return s.projectPath
}

func (s *screenshotCodexSession) Snapshot() codexapp.Snapshot {
	snapshot := s.snapshot
	snapshot.ProjectPath = s.projectPath
	return snapshot
}

func (s *screenshotCodexSession) Submit(prompt string) error {
	return nil
}

func (s *screenshotCodexSession) SubmitInput(input codexapp.Submission) error {
	return nil
}

func (s *screenshotCodexSession) ShowStatus() error {
	return nil
}

func (s *screenshotCodexSession) Interrupt() error {
	return nil
}

func (s *screenshotCodexSession) ListModels() ([]codexapp.ModelOption, error) {
	return nil, nil
}

func (s *screenshotCodexSession) StageModelOverride(model, reasoningEffort string) error {
	s.snapshot.PendingModel = model
	s.snapshot.PendingReasoning = reasoningEffort
	return nil
}

func (s *screenshotCodexSession) RespondApproval(decision codexapp.ApprovalDecision) error {
	return nil
}

func (s *screenshotCodexSession) RespondToolInput(answers map[string][]string) error {
	return nil
}

func (s *screenshotCodexSession) RespondElicitation(decision codexapp.ElicitationDecision, content json.RawMessage) error {
	return nil
}

func (s *screenshotCodexSession) Close() error {
	s.snapshot.Closed = true
	return nil
}

func newScreenshotCodexManager(snapshots map[string]codexapp.Snapshot) *codexapp.Manager {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		snapshot, ok := snapshots[strings.TrimSpace(req.ProjectPath)]
		if !ok {
			snapshot = codexapp.Snapshot{Preset: req.Preset}
		}
		if snapshot.Preset == "" {
			snapshot.Preset = req.Preset
		}
		return &screenshotCodexSession{projectPath: req.ProjectPath, snapshot: snapshot}, nil
	})
	for projectPath, snapshot := range snapshots {
		_, _, _ = manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      snapshot.Preset,
		})
	}
	return manager
}

func screenshotLiveCodexSnapshot(project model.ProjectSummary, now time.Time) codexapp.Snapshot {
	return codexapp.Snapshot{
		ThreadID:       "thread-live",
		Preset:         codexcli.PresetYolo,
		Started:        true,
		Busy:           true,
		BusySince:      now.Add(-48 * time.Second),
		Status:         "Working",
		LastActivityAt: now,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptStatus, Text: fmt.Sprintf("Embedded Codex ready in %s", screenshotProjectLabel(project))},
		},
	}
}

func screenshotEmbeddedCodexSnapshot(project model.ProjectSummary, now time.Time) codexapp.Snapshot {
	projectName := screenshotProjectLabel(project)
	return codexapp.Snapshot{
		ThreadID:        "thread-screenshot",
		Preset:          codexcli.PresetYolo,
		Started:         true,
		Status:          "Codex turn completed",
		LastActivityAt:  now,
		Model:           "gpt-5.4",
		ReasoningEffort: "xhigh",
		TokenUsage: &codexapp.TokenUsageSnapshot{
			Last: codexapp.TokenUsageBreakdown{
				InputTokens:           10000,
				OutputTokens:          2345,
				ReasoningOutputTokens: 345,
				TotalTokens:           12345,
			},
			Total: codexapp.TokenUsageBreakdown{
				TotalTokens: 12345,
			},
			ModelContextWindow: 200000,
		},
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "set up reproducible screenshots for this app"},
			{Kind: codexapp.TranscriptPlan, Text: "1. Add a local screenshot config for safe projects.\n2. Render fixed-size TUI scenarios at 112x31.\n3. Export PNG assets for docs and thumbnails."},
			{Kind: codexapp.TranscriptCommand, Text: "$ rg --files internal/tui docs Makefile\ninternal/tui/app.go\ninternal/tui/codex_pane.go\ninternal/tui/screenshots.go\ndocs/reference.md\nMakefile\n[command completed, exit 0]"},
			{Kind: codexapp.TranscriptFileChange, Text: "A internal/config/screenshot_config.go\nA internal/tui/screenshots.go\nM internal/cli/run.go\nM Makefile\nA docs/screenshots.example.toml"},
			{Kind: codexapp.TranscriptAgent, Text: fmt.Sprintf("Added a screenshots command for %s with a local allowlist config, live dashboard capture, and browser-rendered PNG output in docs/screenshots.", projectName)},
			{Kind: codexapp.TranscriptSystem, Text: "Tip: Alt+Up hides the pane while the CX badge stays lit in the project list."},
		},
		Transcript: "",
	}
}

func screenshotListLiveCodexSnapshots(projects []model.ProjectSummary, now time.Time) map[string]codexapp.Snapshot {
	type timerSpec struct {
		filter   string
		duration time.Duration
	}

	specs := []timerSpec{
		{filter: "LCR", duration: 18*time.Minute + 42*time.Second},
		{filter: "FractalMech", duration: 11*time.Minute + 8*time.Second},
		{filter: "okmain", duration: 24*time.Minute + 17*time.Second},
		{filter: "local-llm-lab", duration: 7*time.Minute + 41*time.Second},
		{filter: screenshotDemoBusyProject, duration: 24*time.Minute + 17*time.Second},
		{filter: screenshotDemoFollowupProject, duration: 11*time.Minute + 8*time.Second},
		{filter: "platform-api", duration: 7*time.Minute + 41*time.Second},
	}

	snapshots := make(map[string]codexapp.Snapshot, 4)
	for _, spec := range specs {
		project, ok := findScreenshotProject(projects, spec.filter)
		if !ok {
			continue
		}
		if _, exists := snapshots[project.Path]; exists {
			continue
		}
		snapshots[project.Path] = codexapp.Snapshot{
			ThreadID:       fmt.Sprintf("thread-%s-live", normalizeScreenshotProjectToken(project.Name)),
			Preset:         codexcli.PresetYolo,
			Started:        true,
			Busy:           true,
			BusySince:      now.Add(-spec.duration),
			Status:         "Working",
			LastActivityAt: now,
		}
		if len(snapshots) >= 4 {
			break
		}
	}
	if len(snapshots) >= 4 {
		return snapshots
	}
	for _, project := range screenshotRecentActiveProjects(projects) {
		if _, exists := snapshots[project.Path]; exists {
			continue
		}
		duration := 5*time.Minute + time.Duration(len(snapshots))*6*time.Minute + 19*time.Second
		snapshots[project.Path] = codexapp.Snapshot{
			ThreadID:       fmt.Sprintf("thread-%s-live", normalizeScreenshotProjectToken(project.Name)),
			Preset:         codexcli.PresetYolo,
			Started:        true,
			Busy:           true,
			BusySince:      now.Add(-duration),
			Status:         "Working",
			LastActivityAt: now,
		}
		if len(snapshots) >= 4 {
			break
		}
	}
	return snapshots
}

func cloneScreenshotSnapshots(src map[string]codexapp.Snapshot) map[string]codexapp.Snapshot {
	if len(src) == 0 {
		return map[string]codexapp.Snapshot{}
	}
	dst := make(map[string]codexapp.Snapshot, len(src))
	for path, snapshot := range src {
		dst[path] = snapshot
	}
	return dst
}

func screenshotRuntimeSnapshots(project model.ProjectSummary, now time.Time) map[string]projectrun.Snapshot {
	projectPath := strings.TrimSpace(project.Path)
	if projectPath == "" {
		return map[string]projectrun.Snapshot{}
	}
	return map[string]projectrun.Snapshot{
		projectPath: screenshotLiveRuntimeSnapshot(project, now),
	}
}

func cloneScreenshotRuntimeSnapshots(src map[string]projectrun.Snapshot) map[string]projectrun.Snapshot {
	if len(src) == 0 {
		return map[string]projectrun.Snapshot{}
	}
	dst := make(map[string]projectrun.Snapshot, len(src))
	for path, snapshot := range src {
		dst[path] = snapshot
	}
	return dst
}

func screenshotLiveRuntimeSnapshot(project model.ProjectSummary, now time.Time) projectrun.Snapshot {
	command := strings.TrimSpace(project.RunCommand)
	if command == "" {
		command = "pnpm dev"
	}
	return projectrun.Snapshot{
		ProjectPath:   strings.TrimSpace(project.Path),
		Command:       command,
		Running:       true,
		StartedAt:     now.Add(-11*time.Minute - 24*time.Second),
		Ports:         []int{3000},
		AnnouncedURLs: []string{"http://127.0.0.1:3000/"},
		RecentOutput: []string{
			"$ " + command,
			"ready on http://127.0.0.1:3000/",
			fmt.Sprintf("serving %s assets", screenshotProjectLabel(project)),
			"watching for changes...",
		},
	}
}

func findScreenshotProject(projects []model.ProjectSummary, filter string) (model.ProjectSummary, bool) {
	for _, project := range projects {
		if screenshotProjectMatches(project, filter) {
			return project, true
		}
	}
	return model.ProjectSummary{}, false
}

func screenshotAlternateSelectionProject(projects []model.ProjectSummary, liveProject model.ProjectSummary) model.ProjectSummary {
	if project, ok := findScreenshotProject(projects, screenshotDemoFollowupProject); ok && project.Path != liveProject.Path {
		return project
	}
	for _, project := range projects {
		if project.Path != liveProject.Path {
			return project
		}
	}
	return liveProject
}

func screenshotDashboardSelectionProject(projects []model.ProjectSummary, preferred, liveProject, runtimeProject model.ProjectSummary) model.ProjectSummary {
	if screenshotProjectHasShowcaseAssessment(preferred) {
		return preferred
	}
	for _, project := range projects {
		if project.Path == runtimeProject.Path || project.Path == liveProject.Path {
			continue
		}
		if screenshotProjectHasShowcaseAssessment(project) {
			return project
		}
	}
	for _, project := range projects {
		if project.Path == runtimeProject.Path {
			continue
		}
		if screenshotProjectHasShowcaseAssessment(project) {
			return project
		}
	}
	if fallback := screenshotAlternateSelectionProject(projects, liveProject); strings.TrimSpace(fallback.Path) != "" {
		return fallback
	}
	return preferred
}

func screenshotProjectHasShowcaseAssessment(project model.ProjectSummary) bool {
	_, _, ok := assessmentStatusLabel(project, false)
	return ok
}

func screenshotRecentActiveProjects(projects []model.ProjectSummary) []model.ProjectSummary {
	if len(projects) == 0 {
		return nil
	}
	out := append([]model.ProjectSummary(nil), projects...)
	sort.SliceStable(out, func(i, j int) bool {
		left := out[i]
		right := out[j]
		if !left.LastActivity.Equal(right.LastActivity) {
			return left.LastActivity.After(right.LastActivity)
		}
		if left.AttentionScore != right.AttentionScore {
			return left.AttentionScore > right.AttentionScore
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
	return out
}

func screenshotCommitPreview(project model.ProjectSummary) *service.CommitPreview {
	return &service.CommitPreview{
		Intent:      service.GitActionFinish,
		ProjectPath: project.Path,
		ProjectName: screenshotProjectLabel(project),
		Branch:      "master",
		StageMode:   service.GitStageAllChanges,
		Message:     "Add reproducible screenshot workflow",
		Included: []service.CommitFile{
			{Code: "M", Summary: "internal/cli/run.go"},
			{Code: "A", Summary: "internal/config/screenshot_config.go"},
			{Code: "A", Summary: "internal/tui/screenshots.go"},
			{Code: "M", Summary: "docs/reference.md"},
			{Code: "A", Summary: "docs/screenshots.example.toml"},
		},
		DiffSummary:   "5 files changed, 312 insertions(+), 18 deletions(-)",
		LatestSummary: strings.TrimSpace(project.LatestSessionSummary),
		CanPush:       true,
	}
}

func screenshotDiffView(project model.ProjectSummary) *diffViewState {
	state := newDiffViewState(project.Path, screenshotProjectLabel(project))
	state.loading = false
	state.preview = screenshotDiffPreview(project)
	state.selected = 1
	state.focus = diffFocusContent
	return state
}

func screenshotDiffPreview(project model.ProjectSummary) *service.DiffPreview {
	return &service.DiffPreview{
		ProjectPath: project.Path,
		ProjectName: screenshotProjectLabel(project),
		Branch:      "master",
		Summary:     "4 files changed, 29 insertions(+), 3 deletions(-)",
		Files: []service.DiffFilePreview{
			{
				Path:    "internal/tui/app.go",
				Summary: "internal/tui/app.go",
				Code:    "M",
				Kind:    scanner.GitChangeModified,
				Staged:  true,
				Body: strings.TrimSpace(`# Staged

diff --git a/internal/tui/app.go b/internal/tui/app.go
@@ -1172,0 +1173,4 @@
+if m.diffView != nil {
+	return strings.Join([]string{header, m.renderDiffView(layout.width, layout.height), m.renderFooter(layout.width)}, "\n")
+}
`),
			},
			{
				Path:     "internal/tui/diff_view.go",
				Summary:  "internal/tui/diff_view.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/internal/tui/diff_view.go b/internal/tui/diff_view.go
--- a/internal/tui/diff_view.go
+++ b/internal/tui/diff_view.go
@@ -412,4 +412,9 @@
-func diffModeLabel() string {
-	return "unified"
+func diffModeLabel(mode diffRenderMode) string {
+	if mode == diffRenderModeUnified {
+		return "unified"
+	}
+	return "side-by-side"
 }
`),
			},
			{
				Path:     "README.md",
				Summary:  "README.md",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
@@ -17,0 +18,3 @@
+| Diff View | Commit Preview |
+| --- | --- |
+| [![Little Control Room diff window](docs/screenshots/diff-view.png)](docs/screenshots/diff-view.png) | [![Little Control Room commit preview dialog](docs/screenshots/commit-preview.png)](docs/screenshots/commit-preview.png) |
`),
			},
			{
				Path:      "docs/screenshots/diff-view.png",
				Summary:   "docs/screenshots/diff-view.png",
				Code:      "??",
				Kind:      scanner.GitChangeUntracked,
				Untracked: true,
				Body:      "# Untracked\n\nBinary file preview unavailable.",
			},
		},
	}
}

type terminalTextStyle struct {
	fg        string
	bg        string
	hasFG     bool
	hasBG     bool
	bold      bool
	faint     bool
	italic    bool
	underline bool
	reverse   bool
}

type terminalRun struct {
	text  string
	style terminalTextStyle
}

type terminalLine []terminalRun

const (
	terminalFontFamilyCSS = `'LCR Iosevka','Iosevka Term','Iosevka Fixed',Iosevka,ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace`
)

type terminalFrameLayout struct {
	stagePadding   float64
	shellPaddingX  float64
	shellPaddingY  float64
	lineHeight     float64
	contentBottom  float64
	viewportWidth  int
	viewportHeight int
}

func terminalFrameMetrics(cols, rows int) terminalFrameLayout {
	const (
		stagePadding       = 18.0
		shellPaddingX      = 12.0
		shellPaddingY      = 10.0
		cellWidthEstimate  = 9.3
		lineHeight         = 18.0
		contentBottom      = 18.0
		captureExtraWidth  = 96.0
		captureExtraHeight = 72.0
	)
	contentWidthEstimate := float64(cols) * cellWidthEstimate
	contentHeightEstimate := float64(rows)*lineHeight + contentBottom
	shellWidthEstimate := contentWidthEstimate + shellPaddingX*2
	shellHeightEstimate := contentHeightEstimate + shellPaddingY*2
	return terminalFrameLayout{
		stagePadding:   stagePadding,
		shellPaddingX:  shellPaddingX,
		shellPaddingY:  shellPaddingY,
		lineHeight:     lineHeight,
		contentBottom:  contentBottom,
		viewportWidth:  int(math.Ceil(shellWidthEstimate + stagePadding*2 + captureExtraWidth)),
		viewportHeight: int(math.Ceil(shellHeightEstimate + stagePadding*2 + captureExtraHeight)),
	}
}

func renderTerminalHTMLDocument(title, content string, cols, rows int) (string, int, int) {
	const (
		pageBackground    = "#000000"
		shellBackground   = "#000000"
		shellStroke       = "#154863"
		shellHighlight    = "rgba(95,215,255,0.10)"
		shadowColor       = "rgba(0,0,0,0.22)"
		defaultFG         = "#f6fbff"
		defaultBG         = shellBackground
		shellRadius       = 10.0
		shellBottomAdjust = 1.0
	)

	layout := terminalFrameMetrics(cols, rows)
	lines := parseTerminalANSI(content)
	if len(lines) < rows {
		for len(lines) < rows {
			lines = append(lines, terminalLine{})
		}
	}

	var out strings.Builder
	out.Grow(len(content) * 6)
	out.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"/>`)
	fmt.Fprintf(&out, `<meta name="viewport" content="width=%d,height=%d,initial-scale=1"/>`, layout.viewportWidth, layout.viewportHeight)
	out.WriteString(`<title>`)
	writeEscapedHTML(&out, title)
	out.WriteString(`</title><style>`)
	out.WriteString(embeddedTerminalFontCSS())
	fmt.Fprintf(&out, `:root{--cols:%d;--rows:%d;--shell-pad-x:%.1fpx;--shell-pad-y:%.1fpx;--line-h:%.1fpx;--content-bottom:%.1fpx;}`, cols, rows, layout.shellPaddingX, layout.shellPaddingY, layout.lineHeight, layout.contentBottom)
	fmt.Fprintf(&out, `html,body{margin:0;padding:0;width:%dpx;height:%dpx;overflow:hidden;background:%s;}`, layout.viewportWidth, layout.viewportHeight, pageBackground)
	fmt.Fprintf(&out, `body{font-family:%s;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale;}`, terminalFontFamilyCSS)
	fmt.Fprintf(&out, `.stage{position:relative;width:%dpx;height:%dpx;background:%s;overflow:hidden;}`, layout.viewportWidth, layout.viewportHeight, pageBackground)
	fmt.Fprintf(&out, `.shell-wrap{position:absolute;left:%.1fpx;top:%.1fpx;display:inline-block;}`, layout.stagePadding, layout.stagePadding)
	fmt.Fprintf(&out, `.terminal-shell{display:inline-block;padding:var(--shell-pad-y) var(--shell-pad-x) calc(var(--shell-pad-y) - %.1fpx);box-sizing:border-box;background:%s;border:1px solid %s;border-radius:%.1fpx;box-shadow:0 6px 14px %s,inset 0 1px 0 %s;overflow:hidden;}`, shellBottomAdjust, defaultBG, shellStroke, shellRadius, shadowColor, shellHighlight)
	fmt.Fprintf(&out, `.terminal{display:block;width:calc(var(--cols) * 1ch);min-width:calc(var(--cols) * 1ch);min-height:calc((var(--rows) * var(--line-h)) + var(--content-bottom));padding-bottom:4px;box-sizing:content-box;overflow:hidden;font-family:%s;font-size:14px;line-height:var(--line-h);color:%s;letter-spacing:0;white-space:pre;font-variant-ligatures:none;font-feature-settings:'liga' 0,'calt' 0;font-variant-numeric:tabular-nums;font-synthesis:none;}`, terminalFontFamilyCSS, defaultFG)
	out.WriteString(`.line{display:flex;align-items:stretch;width:100%;line-height:var(--line-h);}`)
	out.WriteString(`.run{display:inline-block;white-space:pre;line-height:inherit;min-height:100%;vertical-align:top;}`)
	out.WriteString(`.pixel-run{display:inline-flex;align-items:stretch;gap:0;height:100%;line-height:0;vertical-align:top;}`)
	out.WriteString(`.pixel-cell{display:inline-block;flex:0 0 1ch;width:1ch;height:100%;}`)
	out.WriteString(`</style></head><body><div class="stage"><div class="shell-wrap">`)
	out.WriteString(renderTerminalHTMLBlock(lines, layout.lineHeight, defaultFG, defaultBG))
	out.WriteString(`</div></div></body></html>`)
	return out.String(), layout.viewportWidth, layout.viewportHeight
}

func renderTerminalHTMLBlock(lines []terminalLine, lineHeight float64, defaultFG, defaultBG string) string {
	var out strings.Builder
	out.Grow(len(lines) * 128)
	fmt.Fprintf(&out, `<div class="terminal-shell"><div class="terminal">`)
	for _, line := range lines {
		lineBG := terminalLineBackground(line, defaultFG, defaultBG)
		fmt.Fprintf(&out, `<div class="line" style="height:%.1fpx;`, lineHeight)
		if lineBG != "" {
			fmt.Fprintf(&out, `background:%s;`, lineBG)
		}
		out.WriteString(`">`)
		if len(line) == 0 {
			out.WriteString(`&#8203;`)
		} else {
			for _, run := range line {
				if run.text == "" {
					continue
				}
				if terminalRunUsesPixelCells(run.text) {
					out.WriteString(renderTerminalPixelRun(run, defaultFG, defaultBG, lineBG))
					continue
				}
				out.WriteString(`<span class="run" style="`)
				out.WriteString(terminalRunCSS(run.style, defaultFG, defaultBG, lineBG))
				out.WriteString(`">`)
				writeEscapedHTML(&out, run.text)
				out.WriteString(`</span>`)
			}
		}
		out.WriteString(`</div>`)
	}
	out.WriteString(`</div></div>`)
	return out.String()
}

func renderTerminalPixelRun(run terminalRun, defaultFG, defaultBG, lineBG string) string {
	fg, _ := effectiveTerminalColors(run.style, defaultFG, defaultBG)
	bg := effectiveTerminalPixelBackground(run.style, defaultBG, lineBG)

	var out strings.Builder
	out.WriteString(`<span class="pixel-run"`)
	if css := terminalPixelRunCSS(run.style); css != "" {
		out.WriteString(` style="`)
		out.WriteString(css)
		out.WriteString(`"`)
	}
	out.WriteString(`>`)
	for _, r := range run.text {
		out.WriteString(`<span class="pixel-cell" style="`)
		out.WriteString(terminalPixelCellCSS(r, fg, bg))
		out.WriteString(`"></span>`)
	}
	out.WriteString(`</span>`)
	return out.String()
}

func terminalRunCSS(style terminalTextStyle, defaultFG, defaultBG, lineBG string) string {
	fg, bg := effectiveTerminalColors(style, defaultFG, defaultBG)
	parts := []string{
		fmt.Sprintf("color:%s", fg),
	}
	if bg != "" && bg != defaultBG && bg != lineBG {
		parts = append(parts, fmt.Sprintf("background:%s", bg))
	}
	if style.bold {
		parts = append(parts, "font-weight:600")
	}
	if style.faint {
		parts = append(parts, "opacity:0.72")
	}
	if style.italic {
		parts = append(parts, "font-style:italic")
	}
	if style.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

func terminalRunUsesPixelCells(text string) bool {
	if text == "" {
		return false
	}
	hasBlock := false
	for _, r := range text {
		switch r {
		case ' ':
			continue
		case '█', '▀', '▄':
			hasBlock = true
		default:
			return false
		}
	}
	return hasBlock
}

func effectiveTerminalPixelBackground(style terminalTextStyle, defaultBG, lineBG string) string {
	if style.hasBG && style.bg != "" {
		return style.bg
	}
	if lineBG != "" {
		return lineBG
	}
	return defaultBG
}

func terminalPixelRunCSS(style terminalTextStyle) string {
	parts := []string{}
	if style.faint {
		parts = append(parts, "opacity:0.72")
	}
	return strings.Join(parts, ";")
}

func terminalPixelCellCSS(r rune, fg, bg string) string {
	switch r {
	case '█':
		return fmt.Sprintf("background:%s", fg)
	case '▀':
		return fmt.Sprintf("background:linear-gradient(to bottom,%s 0 50%%,%s 50%% 100%%)", fg, bg)
	case '▄':
		return fmt.Sprintf("background:linear-gradient(to bottom,%s 0 50%%,%s 50%% 100%%)", bg, fg)
	default:
		return ""
	}
}

var (
	embeddedTerminalFontCSSOnce  sync.Once
	embeddedTerminalFontCSSValue string
)

func embeddedTerminalFontCSS() string {
	embeddedTerminalFontCSSOnce.Do(func() {
		embeddedTerminalFontCSSValue = buildEmbeddedTerminalFontCSS()
	})
	return embeddedTerminalFontCSSValue
}

func buildEmbeddedTerminalFontCSS() string {
	type fontFace struct {
		weight     int
		candidates []string
	}

	faces := []fontFace{
		{
			weight: 400,
			candidates: []string{
				"~/Library/Fonts/IosevkaTermNerdFontMono-Regular.ttf",
				"~/Library/Fonts/IosevkaNerdFont-Regular.ttf",
				"/usr/share/fonts/truetype/iosevka-term/IosevkaTerm-Regular.ttf",
				"/usr/share/fonts/truetype/iosevka/Iosevka-Regular.ttf",
				"/usr/local/share/fonts/IosevkaTerm-Regular.ttf",
				"/usr/local/share/fonts/Iosevka-Regular.ttf",
			},
		},
		{
			weight: 600,
			candidates: []string{
				"~/Library/Fonts/IosevkaTermNerdFontMono-Medium.ttf",
				"~/Library/Fonts/IosevkaNerdFont-SemiBold.ttf",
				"~/Library/Fonts/IosevkaNerdFont-Heavy.ttf",
				"/usr/share/fonts/truetype/iosevka-term/IosevkaTerm-Medium.ttf",
				"/usr/share/fonts/truetype/iosevka/Iosevka-SemiBold.ttf",
				"/usr/local/share/fonts/IosevkaTerm-Medium.ttf",
				"/usr/local/share/fonts/Iosevka-SemiBold.ttf",
			},
		},
	}

	var css strings.Builder
	for _, face := range faces {
		path, ok := firstExistingScreenshotFont(face.candidates...)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		mimeType, format := embeddedFontFormat(path)
		fmt.Fprintf(&css, "@font-face{font-family:'LCR Iosevka';src:url(data:%s;base64,%s) format('%s');font-style:normal;font-weight:%d;font-display:block;}", mimeType, base64.StdEncoding.EncodeToString(data), format, face.weight)
	}
	return css.String()
}

func firstExistingScreenshotFont(candidates ...string) (string, bool) {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate, true
	}
	return "", false
}

func embeddedFontFormat(path string) (string, string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".otf":
		return "font/otf", "opentype"
	case ".woff":
		return "font/woff", "woff"
	case ".woff2":
		return "font/woff2", "woff2"
	default:
		return "font/ttf", "truetype"
	}
}

func terminalLineBackground(line terminalLine, defaultFG, defaultBG string) string {
	if len(line) == 0 {
		return ""
	}
	leftBG := ""
	rightBG := ""
	for _, run := range line {
		if run.text == "" {
			continue
		}
		_, bg := effectiveTerminalColors(run.style, defaultFG, defaultBG)
		leftBG = bg
		break
	}
	for i := len(line) - 1; i >= 0; i-- {
		run := line[i]
		if run.text == "" {
			continue
		}
		_, bg := effectiveTerminalColors(run.style, defaultFG, defaultBG)
		rightBG = bg
		break
	}
	if leftBG == "" || rightBG == "" || leftBG != rightBG || leftBG == defaultBG {
		return ""
	}
	return leftBG
}

func writeEscapedHTML(out *strings.Builder, text string) {
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(text)); err == nil {
		out.WriteString(escaped.String())
	}
}

func effectiveTerminalColors(style terminalTextStyle, defaultFG, defaultBG string) (string, string) {
	fg := defaultFG
	bg := defaultBG
	if style.hasFG && style.fg != "" {
		fg = style.fg
	}
	if style.hasBG && style.bg != "" {
		bg = style.bg
	}
	if style.reverse {
		fg, bg = bg, fg
	}
	return fg, bg
}

func parseTerminalANSI(content string) []terminalLine {
	lines := []terminalLine{{}}
	style := terminalTextStyle{}
	var current strings.Builder
	currentStyle := style

	flush := func() {
		if current.Len() == 0 {
			return
		}
		line := lines[len(lines)-1]
		line = append(line, terminalRun{text: current.String(), style: currentStyle})
		lines[len(lines)-1] = line
		current.Reset()
	}

	for i := 0; i < len(content); {
		switch content[i] {
		case '\n':
			flush()
			lines = append(lines, terminalLine{})
			i++
		case '\r':
			i++
		case 0x1b:
			if i+1 < len(content) && content[i+1] == '[' {
				end := i + 2
				for end < len(content) {
					b := content[end]
					if b >= '@' && b <= '~' {
						break
					}
					end++
				}
				if end < len(content) && content[end] == 'm' {
					flush()
					params := parseSGRParams(content[i+2 : end])
					style = applySGRParams(style, params)
					currentStyle = style
					i = end + 1
					continue
				}
				if end < len(content) {
					i = end + 1
					continue
				}
			}
			i++
		default:
			r, size := utf8.DecodeRuneInString(content[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			current.WriteRune(r)
			i += size
		}
	}
	flush()
	return lines
}

func parseSGRParams(raw string) []int {
	if raw == "" {
		return []int{0}
	}
	parts := strings.Split(raw, ";")
	params := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			params = append(params, 0)
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		params = append(params, value)
	}
	if len(params) == 0 {
		return []int{0}
	}
	return params
}

func applySGRParams(style terminalTextStyle, params []int) terminalTextStyle {
	for i := 0; i < len(params); i++ {
		switch p := params[i]; {
		case p == 0:
			style = terminalTextStyle{}
		case p == 1:
			style.bold = true
		case p == 2:
			style.faint = true
		case p == 3:
			style.italic = true
		case p == 4:
			style.underline = true
		case p == 7:
			style.reverse = true
		case p == 22:
			style.bold = false
			style.faint = false
		case p == 23:
			style.italic = false
		case p == 24:
			style.underline = false
		case p == 27:
			style.reverse = false
		case p >= 30 && p <= 37:
			style.fg = ansi16Color(p - 30)
			style.hasFG = true
		case p == 39:
			style.fg = ""
			style.hasFG = false
		case p >= 40 && p <= 47:
			style.bg = ansi16Color(p - 40)
			style.hasBG = true
		case p == 49:
			style.bg = ""
			style.hasBG = false
		case p >= 90 && p <= 97:
			style.fg = ansi16BrightColor(p - 90)
			style.hasFG = true
		case p >= 100 && p <= 107:
			style.bg = ansi16BrightColor(p - 100)
			style.hasBG = true
		case p == 38 || p == 48:
			isForeground := p == 38
			if i+1 >= len(params) {
				continue
			}
			mode := params[i+1]
			switch mode {
			case 5:
				if i+2 >= len(params) {
					i = len(params)
					break
				}
				color := ansi256Color(params[i+2])
				if isForeground {
					style.fg = color
					style.hasFG = true
				} else {
					style.bg = color
					style.hasBG = true
				}
				i += 2
			case 2:
				if i+4 >= len(params) {
					i = len(params)
					break
				}
				color := fmt.Sprintf("#%02x%02x%02x", clampColor(params[i+2]), clampColor(params[i+3]), clampColor(params[i+4]))
				if isForeground {
					style.fg = color
					style.hasFG = true
				} else {
					style.bg = color
					style.hasBG = true
				}
				i += 4
			default:
				i++
			}
		}
	}

	return style
}

func screenshotImageDiffView(project model.ProjectSummary) *diffViewState {
	state := newDiffViewState(project.Path, screenshotProjectLabel(project))
	state.loading = false
	state.preview = screenshotImageDiffPreview(project)
	state.selected = 1
	state.focus = diffFocusContent
	return state
}

func screenshotImageDiffPreview(project model.ProjectSummary) *service.DiffPreview {
	imagePath, imageBody, noteBody, oldImage, newImage := screenshotImageDiffFixture(project)
	return &service.DiffPreview{
		ProjectPath: project.Path,
		ProjectName: screenshotProjectLabel(project),
		Branch:      "master",
		Summary:     "3 files changed, 12 insertions(+), 1 deletion(-)",
		Files: []service.DiffFilePreview{
			{
				Path:    "internal/tui/diff_view.go",
				Summary: "internal/tui/diff_view.go",
				Code:    "M",
				Kind:    scanner.GitChangeModified,
				Staged:  true,
				Body: strings.TrimSpace(`# Staged

diff --git a/internal/tui/diff_view.go b/internal/tui/diff_view.go
@@ -455,0 +456,3 @@
+if imageBlock := renderDiffImagePreviewSet(file, width); strings.TrimSpace(imageBlock) != "" {
+	blocks = append(blocks, imageBlock)
+}
`),
			},
			{
				Path:     imagePath,
				Summary:  imagePath,
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				IsImage:  true,
				Body:     imageBody,
				OldImage: oldImage,
				NewImage: newImage,
			},
			{
				Path:      "notes/image-diff.txt",
				Summary:   "notes/image-diff.txt",
				Code:      "??",
				Kind:      scanner.GitChangeUntracked,
				Untracked: true,
				Body:      "# Untracked\n\n" + strings.TrimSpace(noteBody),
			},
		},
	}
}

func screenshotImageDiffFixture(project model.ProjectSummary) (path, imageBody, noteBody string, oldImage, newImage []byte) {
	searchRoots := []string{}
	if projectDir := strings.TrimSpace(project.Path); projectDir != "" {
		searchRoots = append(searchRoots, filepath.Dir(projectDir))
	}
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		searchRoots = append(searchRoots, filepath.Dir(wd))
	}

	if oldJet, newJet, ok := screenshotSiblingFractalMechJets(searchRoots...); ok {
		return "public/assets/sprites/enemies/jet_f15_gray_camo.png",
			"FractalMech jet sprite comparison: F-15 on the left, F-16 on the right.",
			`Use the sibling FractalMech jet sprite pair for the docs screenshot when that repo is present next to LittleControlRoom.

Fallback to the built-in bunker sprite pair when the sibling repo is unavailable, so screenshot generation still works elsewhere.`,
			oldJet, newJet
	}

	return "assets/sprites/bunker_guard.png",
		"FractalMech-style bunker sprite pass: intact frame on the left, damaged repaint on the right.",
		`Use a built-in bunker sprite pair for the docs screenshot so the image diff stays deterministic when the sibling ../FractalMech repo is not present.`,
		screenshotBunkerSpritePNG(false), screenshotBunkerSpritePNG(true)
}

func screenshotSiblingFractalMechJets(searchRoots ...string) (oldImage, newImage []byte, ok bool) {
	seen := make(map[string]struct{}, len(searchRoots))
	for _, root := range searchRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}

		oldJetPath := filepath.Join(root, "FractalMech", "public", "assets", "sprites", "enemies", "jet_f15_gray_camo.png")
		newJetPath := filepath.Join(root, "FractalMech", "public", "assets", "sprites", "enemies", "jet_f16_gray_camo.png")
		oldJet, oldErr := os.ReadFile(oldJetPath)
		newJet, newErr := os.ReadFile(newJetPath)
		if oldErr == nil && newErr == nil {
			return oldJet, newJet, true
		}
	}
	return nil, nil, false
}

func screenshotBunkerSpritePNG(destroyed bool) []byte {
	const (
		width  = 64
		height = 48
	)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	clear := color.RGBA{}
	sky := color.RGBA{R: 18, G: 24, B: 40, A: 255}
	haze := color.RGBA{R: 54, G: 66, B: 86, A: 255}
	ground := color.RGBA{R: 72, G: 60, B: 42, A: 255}
	shadow := color.RGBA{R: 26, G: 23, B: 20, A: 220}
	bunkerBase := color.RGBA{R: 120, G: 126, B: 132, A: 255}
	bunkerLight := color.RGBA{R: 154, G: 162, B: 170, A: 255}
	bunkerDark := color.RGBA{R: 76, G: 82, B: 88, A: 255}
	doorGlow := color.RGBA{R: 255, G: 196, B: 92, A: 255}
	damageGlow := color.RGBA{R: 255, G: 116, B: 54, A: 255}
	smoke := color.RGBA{R: 96, G: 89, B: 84, A: 210}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, clear)
		}
	}

	fillRectRGBA(img, 0, 14, width, 18, sky)
	fillRectRGBA(img, 0, 26, width, 10, haze)
	fillRectRGBA(img, 0, 36, width, 12, ground)
	fillRectRGBA(img, 8, 33, 48, 5, shadow)
	fillRectRGBA(img, 10, 28, 44, 9, bunkerDark)
	fillRectRGBA(img, 14, 18, 36, 12, bunkerBase)
	fillRectRGBA(img, 18, 11, 28, 10, bunkerLight)
	fillRectRGBA(img, 21, 14, 22, 4, bunkerDark)
	fillRectRGBA(img, 16, 22, 8, 2, bunkerLight)
	fillRectRGBA(img, 40, 22, 8, 2, bunkerLight)
	fillRectRGBA(img, 29, 23, 6, 5, bunkerDark)
	fillRectRGBA(img, 30, 24, 4, 4, doorGlow)
	fillRectRGBA(img, 43, 15, 9, 2, bunkerDark)
	fillRectRGBA(img, 50, 13, 4, 4, bunkerLight)
	setPixelRGBA(img, 53, 12, bunkerLight)
	setPixelRGBA(img, 54, 11, bunkerLight)

	if destroyed {
		fillRectRGBA(img, 22, 16, 16, 6, color.RGBA{R: 58, G: 54, B: 58, A: 255})
		fillRectRGBA(img, 30, 24, 4, 4, color.RGBA{R: 30, G: 22, B: 18, A: 255})
		fillRectRGBA(img, 26, 26, 12, 2, color.RGBA{R: 56, G: 42, B: 34, A: 255})
		fillRectRGBA(img, 20, 30, 24, 3, color.RGBA{R: 48, G: 38, B: 30, A: 255})
		fillRectRGBA(img, 27, 21, 10, 2, damageGlow)
		fillRectRGBA(img, 18, 18, 6, 3, smoke)
		fillRectRGBA(img, 23, 13, 7, 4, smoke)
		fillRectRGBA(img, 31, 10, 8, 5, smoke)
		fillRectRGBA(img, 39, 14, 6, 3, smoke)
		setPixelRGBA(img, 41, 28, damageGlow)
		setPixelRGBA(img, 42, 27, damageGlow)
		setPixelRGBA(img, 43, 26, damageGlow)
		for x := 19; x <= 43; x += 3 {
			setPixelRGBA(img, x, 29, bunkerDark)
		}
	} else {
		fillRectRGBA(img, 22, 16, 16, 3, bunkerLight)
		fillRectRGBA(img, 24, 20, 12, 2, bunkerDark)
		fillRectRGBA(img, 19, 28, 26, 2, bunkerBase)
		setPixelRGBA(img, 18, 17, doorGlow)
		setPixelRGBA(img, 46, 17, doorGlow)
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil
	}
	return out.Bytes()
}

func fillRectRGBA(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for yy := max(0, y); yy < min(img.Bounds().Dy(), y+h); yy++ {
		for xx := max(0, x); xx < min(img.Bounds().Dx(), x+w); xx++ {
			img.SetRGBA(xx, yy, c)
		}
	}
}

func setPixelRGBA(img *image.RGBA, x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return
	}
	img.SetRGBA(x, y, c)
}

func clampColor(value int) int {
	switch {
	case value < 0:
		return 0
	case value > 255:
		return 255
	default:
		return value
	}
}

func ansi16Color(index int) string {
	base := []string{
		"#1c1c1c",
		"#ff5f5f",
		"#5fff87",
		"#ffd75f",
		"#5fafff",
		"#af87ff",
		"#5fd7ff",
		"#e6edf3",
	}
	if index < 0 || index >= len(base) {
		return "#e6edf3"
	}
	return base[index]
}

func ansi16BrightColor(index int) string {
	bright := []string{
		"#4d4d4d",
		"#ff8f8f",
		"#87ffaf",
		"#ffe68a",
		"#87c7ff",
		"#c4a1ff",
		"#8be9fd",
		"#f8fbff",
	}
	if index < 0 || index >= len(bright) {
		return "#f8fbff"
	}
	return bright[index]
}

func ansi256Color(index int) string {
	if index < 0 {
		index = 0
	}
	if index > 255 {
		index = 255
	}
	if index < 8 {
		return ansi16Color(index)
	}
	if index < 16 {
		return ansi16BrightColor(index - 8)
	}
	if index >= 232 {
		gray := 8 + (index-232)*10
		return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
	}
	index -= 16
	r := index / 36
	g := (index % 36) / 6
	b := index % 6
	levels := []int{0, 95, 135, 175, 215, 255}
	return fmt.Sprintf("#%02x%02x%02x", levels[r], levels[g], levels[b])
}
