package tui

import (
	"context"
	"errors"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/aibackend"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompactUsageLabel(t *testing.T) {
	if got := compactUsageLabel(model.LLMSessionUsage{}); got != "cost off" {
		t.Fatalf("compactUsageLabel(disabled) = %q, want %q", got, "cost off")
	}

	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "gpt-5-mini",
		Totals: model.LLMUsage{
			InputTokens:  345,
			OutputTokens: 538,
		},
	}
	if got := compactUsageLabel(usage); got != "cost $0.0012" {
		t.Fatalf("compactUsageLabel(enabled) = %q, want %q", got, "cost $0.0012")
	}
}

func TestCompactUsageLabelFallsBackToModelAndTokensWhenCostUnknown(t *testing.T) {
	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "xiaomi/mimo-v2.5-pro",
		Totals: model.LLMUsage{
			InputTokens:       12_500,
			OutputTokens:      345,
			CachedInputTokens: 2_000,
			ReasoningTokens:   67,
		},
	}

	want := "mimo-v2.5-pro i12k o345 c2.0k r67"
	if got := compactUsageLabel(usage); got != want {
		t.Fatalf("compactUsageLabel(unknown cost) = %q, want %q", got, want)
	}
}

func TestCompactUnknownCostUsageLabelUsesTotalWhenOnlyTotalTokensAvailable(t *testing.T) {
	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "mimo-v2.5-pro",
		Totals: model.LLMUsage{
			TotalTokens: 18_250,
		},
	}

	want := "mimo-v2.5-pro t18k"
	if got := compactUsageLabel(usage); got != want {
		t.Fatalf("compactUsageLabel(total-only unknown cost) = %q, want %q", got, want)
	}
}

func TestCompactUsageModelLabelKeepsLastProviderSegment(t *testing.T) {
	if got := compactUsageModelLabel("openrouter/x-ai/grok-4-latest"); got != "grok-4-latest" {
		t.Fatalf("compactUsageModelLabel() = %q, want %q", got, "grok-4-latest")
	}
}

func TestCompactLocalUsageLabel(t *testing.T) {
	if got := compactLocalUsageLabel("Codex", model.LLMSessionUsage{}); got != "Codex ready" {
		t.Fatalf("compactLocalUsageLabel(ready) = %q, want %q", got, "Codex ready")
	}

	usage := model.LLMSessionUsage{Started: 1, Completed: 1}
	if got := compactLocalUsageLabel("Codex", usage); got != "Codex 1 call" {
		t.Fatalf("compactLocalUsageLabel(one call) = %q, want %q", got, "Codex 1 call")
	}
}

func TestLLMUsageTotalsIncreasedIncludesTokenCounters(t *testing.T) {
	previous := model.LLMUsage{
		InputTokens:      10,
		OutputTokens:     5,
		EstimatedCostUSD: 0,
	}
	if llmUsageTotalsIncreased(previous, previous) {
		t.Fatalf("same usage totals should not count as increased")
	}

	current := previous
	current.InputTokens = 11
	if !llmUsageTotalsIncreased(current, previous) {
		t.Fatalf("input token increase should count as increased usage")
	}

	current = previous
	current.OutputTokens = 6
	if !llmUsageTotalsIncreased(current, previous) {
		t.Fatalf("output token increase should count as increased usage")
	}
}

func TestAIStatsShowsCostOnlyForOpenAIAPI(t *testing.T) {
	if aiStatsShowsCost(config.AIBackendCodex) {
		t.Fatalf("aiStatsShowsCost(codex) = true, want false")
	}
	if !aiStatsShowsCost(config.AIBackendOpenAIAPI) {
		t.Fatalf("aiStatsShowsCost(openai_api) = false, want true")
	}
}

func TestAIStatsCostValue(t *testing.T) {
	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "gpt-5-mini",
		Totals: model.LLMUsage{
			InputTokens:  345,
			OutputTokens: 538,
		},
	}

	if got := ansi.Strip(aiStatsCostValue(usage)); got != "$0.0012" {
		t.Fatalf("aiStatsCostValue() = %q, want %q", got, "$0.0012")
	}
}

func TestAIStatsBillingValueMarksLocalProviderMode(t *testing.T) {
	if got := ansi.Strip(aiStatsBillingValue(config.AIBackendOpenCode)); got != "local provider mode" {
		t.Fatalf("aiStatsBillingValue(opencode) = %q, want %q", got, "local provider mode")
	}
}

func TestAIStatsBillingNoticeClarifiesLocalBackends(t *testing.T) {
	got := aiStatsBillingNotice(config.AIBackendClaude)
	if !strings.Contains(got, "local provider path") {
		t.Fatalf("local backend notice should mention local provider billing semantics, got %q", got)
	}
	if !strings.Contains(got, "estimated API-key spend") {
		t.Fatalf("local backend notice should explain how to see API-key cost, got %q", got)
	}
}

func TestFooterUsageLabelShowsLocalBackendActivity(t *testing.T) {
	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	if got := m.footerUsageLabel(); got != "Codex ready" {
		t.Fatalf("footerUsageLabel() = %q, want %q", got, "Codex ready")
	}
}

func TestFooterUsageLabelUsesConfiguredLocalBackendBeforeSetupCheck(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{settingsBaseline: &settings}
	if got := m.footerUsageLabel(); got != "Codex ready" {
		t.Fatalf("footerUsageLabel() = %q, want %q", got, "Codex ready")
	}
}

func TestFooterUsageLabelShowsUnavailableBackend(t *testing.T) {
	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	if got := m.footerUsageLabel(); got != "AI unavailable" {
		t.Fatalf("footerUsageLabel() = %q, want AI unavailable", got)
	}
}

func TestRenderTopStatusLineShowsUnavailableBackendNotice(t *testing.T) {
	m := Model{
		status:       "Ready",
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	rendered := ansi.Strip(m.renderTopStatusLine(160))
	if !strings.Contains(rendered, "AI unavailable (use /setup)") {
		t.Fatalf("top status line missing backend warning: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key") {
		t.Fatalf("top status line should keep the warning generic, got %q", rendered)
	}
}

func TestRenderTopStatusLinePulsesActionRequiredWarning(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Stop the runtime before merging this worktree back"}

	warnA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	warnB := m.renderTopStatusLine(160)

	if ansi.Strip(warnA) != ansi.Strip(warnB) {
		t.Fatalf("warning pulse should preserve banner text, got %q vs %q", ansi.Strip(warnA), ansi.Strip(warnB))
	}
	if warnA == warnB {
		t.Fatalf("action-required warning should animate across spinner frames")
	}
}

func TestRenderTopStatusLineSettlesEmbeddedSessionAttentionWarning(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	m := Model{
		status: "Close the embedded agent session before merging this worktree back.",
		nowFn: func() time.Time {
			return now
		},
	}
	m.markTopStatusAttentionPulse(m.status)

	warnA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	warnB := m.renderTopStatusLine(160)

	if ansi.Strip(warnA) != ansi.Strip(warnB) {
		t.Fatalf("embedded-session warning pulse should preserve banner text, got %q vs %q", ansi.Strip(warnA), ansi.Strip(warnB))
	}
	if warnA == warnB {
		t.Fatalf("embedded-session warning should pulse during its short attention window")
	}

	now = now.Add(topStatusAttentionPulseDuration + time.Millisecond)
	m.spinnerFrame = 0
	settledA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	settledB := m.renderTopStatusLine(160)

	if ansi.Strip(settledA) != ansi.Strip(settledB) {
		t.Fatalf("settled embedded-session warning should preserve banner text, got %q vs %q", ansi.Strip(settledA), ansi.Strip(settledB))
	}
	if settledA != settledB {
		t.Fatalf("embedded-session warning should stop pulsing after the attention window")
	}
}

func TestRenderTopStatusLinePulsesPinnedTodoResumeBlockWarning(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Another embedded Claude Code session is open for this TODO lane. Finish or close it before opening TODO #42's pinned session."}

	warnA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	warnB := m.renderTopStatusLine(160)

	if ansi.Strip(warnA) != ansi.Strip(warnB) {
		t.Fatalf("pinned TODO warning pulse should preserve banner text, got %q vs %q", ansi.Strip(warnA), ansi.Strip(warnB))
	}
	if warnA == warnB {
		t.Fatalf("pinned TODO warning should animate across spinner frames")
	}
}

func TestRenderTopStatusLinePulsesErrorsAsDanger(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		status: "Scan failed",
		err:    errors.New("boom"),
	}

	errA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	errB := m.renderTopStatusLine(160)

	if ansi.Strip(errA) != ansi.Strip(errB) {
		t.Fatalf("danger pulse should preserve banner text, got %q vs %q", ansi.Strip(errA), ansi.Strip(errB))
	}
	if errA == errB {
		t.Fatalf("error banner should animate across spinner frames")
	}
}

func TestRenderTopStatusLineKeepsRecoveryProgressNeutral(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Scanning and retrying failed assessments..."}

	statusA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	statusB := m.renderTopStatusLine(160)

	if ansi.Strip(statusA) != ansi.Strip(statusB) {
		t.Fatalf("recovery progress should preserve banner text, got %q vs %q", ansi.Strip(statusA), ansi.Strip(statusB))
	}
	if statusA != statusB {
		t.Fatalf("recovery progress should not animate like a warning or error")
	}
}

func TestRenderTopStatusLineKeepsClipboardConfirmationNeutral(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Copied error details to clipboard"}

	statusA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	statusB := m.renderTopStatusLine(160)

	if ansi.Strip(statusA) != ansi.Strip(statusB) {
		t.Fatalf("clipboard confirmation should preserve banner text, got %q vs %q", ansi.Strip(statusA), ansi.Strip(statusB))
	}
	if statusA != statusB {
		t.Fatalf("clipboard confirmation should not animate like a danger banner")
	}
}

func TestRenderTopStatusLineShowsNavigationHintsInsteadOfAICounts(t *testing.T) {
	m := Model{status: "Ready"}

	rendered := ansi.Strip(m.renderTopStatusLine(160))
	if !strings.Contains(rendered, "f filter") || !strings.Contains(rendered, "/ command") || !strings.Contains(rendered, "` chat") {
		t.Fatalf("top status line should surface navigation hints, got %q", rendered)
	}
	if strings.Contains(rendered, "Tab switch") {
		t.Fatalf("top status line should leave pane switching to the footer, got %q", rendered)
	}
	if strings.Contains(rendered, "OK=") || strings.Contains(rendered, "RUN=") || strings.Contains(rendered, "ERR=") {
		t.Fatalf("top status line should no longer include AI classification counters, got %q", rendered)
	}
}

func TestRenderTopStatusLineShowsCPUUsageAtRight(t *testing.T) {
	m := Model{
		status: "Ready",
		cpuSnapshot: procinspect.CPUSnapshot{
			TotalCPU:  132.6,
			ScannedAt: time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC),
			Processes: []procinspect.CPUProcess{{
				Process: procinspect.Process{PID: 42, CPU: 82.3, Command: "/opt/homebrew/bin/node server.js"},
			}},
		},
	}

	rendered := strings.TrimRight(ansi.Strip(m.renderTopStatusLine(80)), " ")
	if !strings.Contains(rendered, "Ready") {
		t.Fatalf("top status line missing left status: %q", rendered)
	}
	if !strings.HasSuffix(rendered, "CPU 133% server.js 82%") {
		t.Fatalf("top status line should pin CPU summary to the right, got %q", rendered)
	}
}

func TestCompactFooterBaseSplitsGlobalActionsFromTopStatus(t *testing.T) {
	rendered := ansi.Strip(compactFooterBase(160, focusProjects, 0, 0, false, "Session", nil))
	if strings.Contains(rendered, "f filter") || strings.Contains(rendered, "/ command") {
		t.Fatalf("normal footer should not repeat top global actions, got %q", rendered)
	}
	if !strings.Contains(rendered, "Tab switch") {
		t.Fatalf("normal footer should keep pane switching guidance, got %q", rendered)
	}
	if !strings.Contains(rendered, "/chat chat") {
		t.Fatalf("normal footer should advertise Chat, got %q", rendered)
	}
	if strings.Contains(rendered, "? help") {
		t.Fatalf("normal footer should not advertise the removed help dialog, got %q", rendered)
	}
}

func TestRenderTopStatusLineShowsMergeConflictBadge(t *testing.T) {
	m := Model{
		status: "Ready",
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
			RepoConflict:  true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderTopStatusLine(180))
	if !strings.Contains(rendered, "MERGE CONFLICT") {
		t.Fatalf("top status line missing merge conflict badge: %q", rendered)
	}
	if !strings.Contains(rendered, "selected repo has unmerged files") {
		t.Fatalf("top status line missing merge conflict summary: %q", rendered)
	}
	if !strings.Contains(rendered, "use /resolve") {
		t.Fatalf("top status line missing /resolve guidance: %q", rendered)
	}
}

func TestDispatchResolveStartsParallelBackgroundResolverWithoutReplacingEngineer(t *testing.T) {
	projectPath := "/tmp/resolve-conflict"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := "existing-thread"
		if strings.TrimSpace(req.Prompt) != "" {
			threadID = "resolve-thread"
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: threadID,
				Started:  true,
				Busy:     strings.TrimSpace(req.Prompt) != "",
				Status:   "Codex session ready",
			},
		}, nil
	})
	interactive, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderCodex,
	})
	if err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "resolve-conflict",
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
			RepoBranch:    "feat/conflict",
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindResolve})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/resolve) cmd = nil, want background resolver launch")
	}
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want no foreground session transition", got.codexPendingOpen)
	}

	msgs := collectCmdMsgs(cmd)
	var (
		opened    mergeConflictResolverOpenedMsg
		hasOpened bool
	)
	for _, msg := range msgs {
		if candidate, ok := msg.(mergeConflictResolverOpenedMsg); ok {
			opened = candidate
			hasOpened = true
			break
		}
	}
	if !hasOpened || opened.projectPath != projectPath {
		t.Fatalf("command messages = %#v, want mergeConflictResolverOpenedMsg for %q", msgs, projectPath)
	}
	if opened.err != nil {
		t.Fatalf("mergeConflictResolverOpenedMsg.err = %v", opened.err)
	}
	if opened.reused {
		t.Fatal("fresh background resolver unexpectedly reused")
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want existing engineer plus resolver", len(requests))
	}
	resolveRequest := requests[1]
	if resolveRequest.Provider != codexapp.ProviderCodex {
		t.Fatalf("resolver provider = %q, want Codex", resolveRequest.Provider)
	}
	if !resolveRequest.ForceNew {
		t.Fatalf("request ForceNew = false, want true")
	}
	for _, want := range []string{
		"Resolve the current Git merge conflicts",
		"Preserve the intended changes from both sides",
		"If there are no major problems",
		"commit the resolution",
		"Do not push",
	} {
		if !strings.Contains(resolveRequest.Prompt, want) {
			t.Fatalf("/resolve prompt missing %q:\n%s", want, resolveRequest.Prompt)
		}
	}
	for _, unwanted := range []string{"Current branch:", "Instructions:", "Chat", "Do not commit"} {
		if strings.Contains(resolveRequest.Prompt, unwanted) {
			t.Fatalf("/resolve prompt retained %q:\n%s", unwanted, resolveRequest.Prompt)
		}
	}
	if interactive.Snapshot().Closed {
		t.Fatal("/resolve closed the existing engineer session")
	}
	if current, ok := manager.Session(projectPath); !ok || current != interactive {
		t.Fatalf("interactive session = (%#v, %v), want original session", current, ok)
	}
	if parallel, ok := manager.ParallelSession(projectPath); !ok || parallel == interactive {
		t.Fatalf("parallel session = (%#v, %v), want a distinct resolver", parallel, ok)
	}

	applied, _ := got.applyMergeConflictResolverOpenedMsg(opened)
	visible := applied.(Model)
	resolver, ok := visible.mergeConflictResolverForProject(projectPath)
	if !ok || resolver.Phase != mergeConflictResolverRunning {
		t.Fatalf("resolver view state = (%#v, %v), want running", resolver, ok)
	}
	topStatus := ansi.Strip(visible.renderTopStatusLine(180))
	if !strings.Contains(topStatus, "RESOLVING") || strings.Contains(topStatus, "use /resolve") {
		t.Fatalf("top status should replace stale conflict guidance with resolver state: %q", topStatus)
	}
	projectRow := ansi.Strip(visible.renderProjectList(160, 6))
	for _, want := range []string{"resolve", "CX resolve"} {
		if !strings.Contains(projectRow, want) {
			t.Fatalf("project row missing resolver signal %q:\n%s", want, projectRow)
		}
	}
	detail := strings.Join(strings.Fields(ansi.Strip(renderProjectDetailSurface(visible.buildProjectDetailSurface(visible.projects[0], model.ProjectDetail{}), 100))), " ")
	for _, want := range []string{"Resolver:", "working in the background", "Run /resolve again to see its latest status"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("project detail missing resolver feedback %q:\n%s", want, detail)
		}
	}
}

func TestResolveGitlinkConflictTargetStartsBackgroundResolverInSubmoduleWorktree(t *testing.T) {
	parentPath := "/tmp/resolve-parent"
	worktreePath := "/tmp/lcroom-submodule-merge/assets_src"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:    req.Provider.Normalized(),
				ProjectPath: req.ProjectPath,
				ThreadID:    "gitlink-resolve-thread",
				Started:     true,
				Busy:        true,
				Status:      "Codex session ready",
			},
		}, nil
	})
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          parentPath,
			Name:          "resolve-parent",
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
			RepoBranch:    "master",
		}},
		selected: 0,
	}

	updated, cmd := m.Update(mergeConflictResolveTargetMsg{
		project: m.projects[0],
		target: service.GitlinkConflictResolveTarget{
			ParentRepoPath: parentPath,
			ParentBranch:   "master",
			SubmodulePath:  "assets_src",
			WorktreePath:   worktreePath,
			Branch:         "lcroom/master/assets_src-merge-aaaa-bbbb",
			Base:           "base-sha",
			Ours:           "ours-sha",
			Theirs:         "theirs-sha",
		},
		hasGitlink: true,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("gitlink resolve target cmd = nil, want background resolver launch")
	}
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want no foreground session transition", got.codexPendingOpen)
	}

	msgs := collectCmdMsgs(cmd)
	var (
		opened    mergeConflictResolverOpenedMsg
		hasOpened bool
	)
	for _, msg := range msgs {
		if candidate, ok := msg.(mergeConflictResolverOpenedMsg); ok {
			opened = candidate
			hasOpened = true
			break
		}
	}
	if !hasOpened || opened.projectPath != worktreePath {
		t.Fatalf("opened project path = %q, want %q (msgs %#v)", opened.projectPath, worktreePath, msgs)
	}
	if opened.err != nil {
		t.Fatalf("mergeConflictResolverOpenedMsg.err = %v", opened.err)
	}
	if opened.ownerProjectPath != parentPath {
		t.Fatalf("resolver owner path = %q, want parent project %q", opened.ownerProjectPath, parentPath)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != worktreePath {
		t.Fatalf("request project path = %q, want submodule worktree %q", requests[0].ProjectPath, worktreePath)
	}
	if !requests[0].ForceNew {
		t.Fatalf("request ForceNew = false, want true")
	}
	for _, want := range []string{
		"Resolve the Git conflicts in this submodule merge worktree",
		"Parent repo: " + parentPath,
		"Submodule path: assets_src",
		"Submodule merge worktree: " + worktreePath,
		"Submodule merge branch: lcroom/master/assets_src-merge-aaaa-bbbb",
		"commit the submodule merge on its current branch, push that branch, and stage the parent repo gitlink",
		"Do not commit the parent repo merge",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("/resolve gitlink prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}

	applied, _ := got.applyMergeConflictResolverOpenedMsg(opened)
	visible := applied.(Model)
	resolver, ok := visible.mergeConflictResolverForProject(parentPath)
	if !ok || normalizeProjectPath(resolver.SessionProjectPath) != normalizeProjectPath(worktreePath) {
		t.Fatalf("parent resolver state = (%#v, %v), want submodule worktree session", resolver, ok)
	}
}

func TestDispatchResolveReusesAlreadyRunningBackgroundResolver(t *testing.T) {
	projectPath := "/tmp/resolve-running"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "resolve-thread",
				Started:  true,
				Busy:     true,
				Status:   "Codex is working...",
			},
		}, nil
	})
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "resolve-running",
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
		}},
		selected: 0,
	}

	firstUpdated, firstCmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindResolve})
	first := firstUpdated.(Model)
	firstMsgs := collectCmdMsgs(firstCmd)
	if len(requests) != 1 {
		t.Fatalf("first /resolve launch requests = %d, want 1", len(requests))
	}
	if len(firstMsgs) != 1 {
		t.Fatalf("first /resolve messages = %#v, want one resolver result", firstMsgs)
	}
	firstOpened, ok := firstMsgs[0].(mergeConflictResolverOpenedMsg)
	if !ok {
		t.Fatalf("first /resolve result = %#v, want resolver open", firstMsgs[0])
	}
	applied, _ := first.applyMergeConflictResolverOpenedMsg(firstOpened)
	running := applied.(Model)
	// A targeted Git refresh may clear the conflict marker while the resolver
	// is still verifying or committing. /resolve remains a useful status query.
	running.projects[0].RepoConflict = false

	secondUpdated, secondCmd := running.dispatchCommand(commands.Invocation{Kind: commands.KindResolve})
	if len(requests) != 1 {
		t.Fatalf("repeated /resolve created %d sessions, want 1", len(requests))
	}
	if secondCmd != nil {
		t.Fatal("repeated /resolve should report cached resolver progress without another launch command")
	}
	second := secondUpdated.(Model)
	if !strings.Contains(second.status, "still working") || strings.Contains(second.status, "already running") {
		t.Fatalf("repeated /resolve status should report useful progress, got %q", second.status)
	}
}

func TestBackgroundResolverCompletionAndAttentionAreSurfaced(t *testing.T) {
	projectPath := "/tmp/resolve-updates"
	m := Model{
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "resolve-updates",
		}},
	}

	updated, _ := m.applyMergeConflictResolverUpdateMsg(mergeConflictResolverUpdateMsg{
		projectPath: projectPath,
		found:       true,
		terminal:    true,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			ThreadID: "resolve-complete",
			Started:  true,
		},
	})
	got := updated.(Model)
	if got.status != "Background Codex conflict resolver finished; refreshing Git status" {
		t.Fatalf("completion status = %q", got.status)
	}
	checking, ok := got.mergeConflictResolverForProject(projectPath)
	if !ok || checking.Phase != mergeConflictResolverChecking {
		t.Fatalf("terminal resolver state = (%#v, %v), want checking", checking, ok)
	}
	got.reconcileMergeConflictResolverProject(model.ProjectSummary{Path: projectPath, RepoConflict: false})
	resolved, ok := got.mergeConflictResolverForProject(projectPath)
	if !ok || resolved.Phase != mergeConflictResolverResolved {
		t.Fatalf("refreshed resolver state = (%#v, %v), want resolved", resolved, ok)
	}
	if rendered := ansi.Strip(got.renderTopStatusLine(160)); !strings.Contains(rendered, "RESOLVED") {
		t.Fatalf("top status missing persistent resolver outcome: %q", rendered)
	}

	updated, _ = got.applyMergeConflictResolverUpdateMsg(mergeConflictResolverUpdateMsg{
		projectPath: projectPath,
		found:       true,
		terminal:    true,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			ThreadID: "session123-needs-input",
			Started:  true,
			PendingApproval: &codexapp.ApprovalRequest{
				ID:   "approval-1",
				Kind: codexapp.ApprovalCommandExecution,
			},
		},
	})
	got = updated.(Model)
	if got.status != "Background conflict resolver needs attention" {
		t.Fatalf("attention status = %q", got.status)
	}
	waiting, ok := got.mergeConflictResolverForProject(projectPath)
	if !ok || waiting.Phase != mergeConflictResolverNeedsAttention {
		t.Fatalf("attention resolver state = (%#v, %v), want needs attention", waiting, ok)
	}
	if got.actionNoticeDialog == nil {
		t.Fatal("resolver attention did not open a notice")
	}
	rendered := strings.Join(strings.Fields(ansi.Strip(got.renderActionNoticeDialogContent(76))), " ")
	for _, want := range []string{
		"Resolver needs attention",
		"paused for input",
		"Open resolver session session1",
		"/sessions",
		"finish or close",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("resolver attention notice missing %q:\n%s", want, rendered)
		}
	}
}

func TestBackgroundResolverReconcilesGitConflictOutcome(t *testing.T) {
	projectPath := "/tmp/resolve-reconcile"
	m := Model{}
	m.markMergeConflictResolverStarting(projectPath, projectPath, codexapp.ProviderCodex)
	m.updateMergeConflictResolverSnapshot(projectPath, codexapp.Snapshot{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: projectPath,
		ThreadID:    "resolve-reconcile-session",
		Started:     true,
	}, true)
	m.failMergeConflictResolverRefresh(projectPath, errors.New("refresh timed out"))
	refreshFailed, ok := m.mergeConflictResolverForProject(projectPath)
	if !ok || refreshFailed.Phase != mergeConflictResolverRefreshFailed || !strings.Contains(refreshFailed.summary(time.Time{}), "refresh timed out") {
		t.Fatalf("refresh failure state = (%#v, %v), want explicit Git-status failure", refreshFailed, ok)
	}

	m.reconcileMergeConflictResolverProject(model.ProjectSummary{Path: projectPath, RepoConflict: true})
	remaining, ok := m.mergeConflictResolverForProject(projectPath)
	if !ok || remaining.Phase != mergeConflictResolverConflictsRemain {
		t.Fatalf("conflicted refresh state = (%#v, %v), want conflicts remain", remaining, ok)
	}
	if detail := m.repoConflictDetailText(model.ProjectSummary{Path: projectPath, RepoConflict: true}); !strings.Contains(detail, "finished") || !strings.Contains(detail, "retry") {
		t.Fatalf("remaining-conflict detail should explain the resolver outcome: %q", detail)
	}

	m.reconcileMergeConflictResolverProject(model.ProjectSummary{Path: projectPath, RepoConflict: false})
	resolved, ok := m.mergeConflictResolverForProject(projectPath)
	if !ok || resolved.Phase != mergeConflictResolverResolved {
		t.Fatalf("clean refresh state = (%#v, %v), want resolved", resolved, ok)
	}

	m.reconcileMergeConflictResolverProject(model.ProjectSummary{Path: projectPath, RepoConflict: true})
	if stale, ok := m.mergeConflictResolverForProject(projectPath); ok {
		t.Fatalf("later conflict retained stale completed resolver state: %#v", stale)
	}
}

func TestRenderAIBackendStatusNoticeUsesWarningBadge(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	rendered := m.renderAIBackendStatusNotice()
	if got := strings.TrimSpace(ansi.Strip(rendered)); got != "AI unavailable (use /setup)" {
		t.Fatalf("renderAIBackendStatusNotice() = %q, want %q", got, "AI unavailable (use /setup)")
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("renderAIBackendStatusNotice() should render a styled badge, got %q", rendered)
	}
}

func TestSetupSnapshotUnavailableKeepsExistingStatus(t *testing.T) {
	m := Model{
		status: "Ready",
	}

	updated, cmd := m.Update(setupSnapshotMsg{
		snapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("setupSnapshotMsg should not return a command")
	}
	if got.status != "Ready" {
		t.Fatalf("status = %q, want existing status to be preserved", got.status)
	}
}

func TestRenderFooterPulsesWhenUsageIncreases(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	classifier := &usageSnapshotClassifier{
		usage: model.LLMSessionUsage{
			Enabled: true,
			Model:   "gpt-5-mini",
			Totals: model.LLMUsage{
				InputTokens:      345_000,
				OutputTokens:     538_000,
				EstimatedCostUSD: 1.16225,
			},
		},
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	m := New(ctx, svc)
	m.nowFn = func() time.Time { return now }
	m.refreshUsagePulse()

	steady := m.renderFooter(160)
	if !strings.Contains(ansi.Strip(steady), "cost $1.16") {
		t.Fatalf("steady footer missing cost label: %q", steady)
	}

	classifier.usage.Totals.InputTokens = 360_000
	classifier.usage.Totals.OutputTokens = 544_000
	classifier.usage.Totals.EstimatedCostUSD = 1.178
	m.spinnerFrame = 0
	m.refreshUsagePulse()
	pulseA := m.renderFooter(160)

	m.spinnerFrame = 1
	pulseB := m.renderFooter(160)

	if ansi.Strip(pulseA) != ansi.Strip(pulseB) {
		t.Fatalf("usage pulse should keep the same visible text: %q vs %q", ansi.Strip(pulseA), ansi.Strip(pulseB))
	}
	if pulseA == pulseB {
		t.Fatalf("usage pulse should animate across spinner frames")
	}
	if pulseA == ansi.Strip(pulseA) {
		t.Fatalf("usage pulse should use ANSI styling: %q", pulseA)
	}

	now = now.Add(usagePulseDuration + 50*time.Millisecond)
	settled := m.renderFooter(160)
	if settled == pulseA || settled == pulseB {
		t.Fatalf("usage pulse should expire after the pulse window")
	}
}

func TestRenderFooterShowsSeparateAssessmentAlertWhenClassificationErrorsExist(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
			},
		},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			LatestSessionClassification: model.ClassificationFailed,
		}},
	}

	m.spinnerFrame = 0
	renderedA := m.renderFooter(160)
	m.spinnerFrame = 1
	renderedB := m.renderFooter(160)

	if ansi.Strip(renderedA) != ansi.Strip(renderedB) {
		t.Fatalf("assessment footer should keep the same visible text: %q vs %q", ansi.Strip(renderedA), ansi.Strip(renderedB))
	}
	if renderedA != renderedB {
		t.Fatalf("assessment footer should stay visually stable across spinner frames")
	}
	if renderedA == ansi.Strip(renderedA) {
		t.Fatalf("assessment footer should use ANSI styling: %q", renderedA)
	}
	if !strings.Contains(ansi.Strip(renderedA), "Codex ready") {
		t.Fatalf("footer should keep the backend status visible, got %q", ansi.Strip(renderedA))
	}
	if !strings.Contains(ansi.Strip(renderedA), "1 assessment error") {
		t.Fatalf("footer should surface assessment failures separately, got %q", ansi.Strip(renderedA))
	}
}

func TestRenderFooterHidesAssessmentAlertWhileErrorLogIsOpen(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		errorLogVisible: true,
		setupChecked:    true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
			},
		},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			LatestSessionClassification: model.ClassificationFailed,
		}},
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "Error log:") {
		t.Fatalf("footer should switch to error log guidance, got %q", rendered)
	}
	if strings.Contains(rendered, "assessment error") {
		t.Fatalf("footer should hide assessment alert while error log is open, got %q", rendered)
	}
}

func TestRenderFooterShowsBrowserAttentionAlert(t *testing.T) {
	m := Model{
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider: codexapp.ProviderCodex,
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settingsAutomaticPlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
					ToolName:   "browser_navigate",
				},
				ManagedBrowserSessionKey: "managed-demo",
			},
		},
	}

	rendered := ansi.Strip(m.renderFooter(120))
	if !strings.Contains(rendered, "1 browser wait") {
		t.Fatalf("footer should surface browser attention waits, got %q", rendered)
	}
}

func TestRenderFooterShowsProcessWarningSystemNotice(t *testing.T) {
	m := Model{
		projects:    []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo"}},
		selected:    0,
		allProjects: []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo"}},
		processReports: map[string]procinspect.ProjectReport{
			"/tmp/demo": {
				ProjectPath: "/tmp/demo",
				Findings: []procinspect.Finding{{
					Process: procinspect.Process{PID: 49995, CPU: 98.5},
					Reasons: []string{"orphaned under PID 1", "high CPU 98.5%"},
				}},
			},
		},
	}

	rendered := ansi.Strip(m.renderFooter(220))
	for _, want := range []string{"PIDs 1", "hot1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("footer missing process warning %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "suspicious project process") || strings.Contains(rendered, "Processes:") {
		t.Fatalf("footer process warning should stay compact, got %q", rendered)
	}
}

func TestProcessWarningStatusIsCompact(t *testing.T) {
	status := processWarningStatus(processWarningStats{Total: 6, HighCPU: 2, PortListeners: 1})
	if status != "PIDs 6 hot2 port1; /cpu" {
		t.Fatalf("processWarningStatus() = %q, want compact CPU status", status)
	}
}

func TestGlobalProcessFindingsIncludeAllProjects(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/selected", Name: "selected"},
			{Path: "/tmp/other", Name: "other"},
		},
		processReports: map[string]procinspect.ProjectReport{
			"/tmp/selected": {
				ProjectPath: "/tmp/selected",
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 1, CPU: 1.2},
					ProjectPath: "/tmp/selected",
					Reasons:     []string{"orphaned under PID 1"},
				}},
			},
			"/tmp/other": {
				ProjectPath: "/tmp/other",
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 2, CPU: 99.0},
					ProjectPath: "/tmp/other",
					Reasons:     []string{"high CPU 99.0%"},
				}},
			},
		},
	}

	findings, _ := m.globalProcessFindings()
	if len(findings) != 2 {
		t.Fatalf("global findings len = %d, want 2", len(findings))
	}
	if findings[0].ProjectPath != "/tmp/other" || findings[0].PID != 2 {
		t.Fatalf("first finding = project %q PID %d, want /tmp/other PID 2", findings[0].ProjectPath, findings[0].PID)
	}
}

func TestFooterSupplementSegmentsPrioritizeAssessmentBeforeUsage(t *testing.T) {
	segments := footerSupplementSegments("filter", "1 assessment error", "OpenCode 139 calls")
	if len(segments) != 3 {
		t.Fatalf("segment count = %d, want 3", len(segments))
	}
	if got := ansi.Strip(segments[0]); got != "filter" {
		t.Fatalf("segment 0 = %q, want filter", got)
	}
	if got := ansi.Strip(segments[1]); got != "1 assessment error" {
		t.Fatalf("segment 1 = %q, want assessment alert", got)
	}
	if got := ansi.Strip(segments[2]); got != "OpenCode 139 calls" {
		t.Fatalf("segment 2 = %q, want usage label", got)
	}
}
