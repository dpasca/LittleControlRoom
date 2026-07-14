package tui

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
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

func TestDispatchResolveStartsFreshEngineerForMergeConflict(t *testing.T) {
	projectPath := "/tmp/resolve-conflict"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "resolve-thread",
				Started:  true,
				Status:   "Codex session ready",
			},
		}, nil
	})
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
		t.Fatalf("dispatchCommand(/resolve) cmd = nil, want fresh engineer launch")
	}
	if got.codexPendingOpen == nil || !got.codexPendingOpen.newSession {
		t.Fatalf("codexPendingOpen = %#v, want fresh pending open", got.codexPendingOpen)
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.projectPath == "" {
		t.Fatalf("command messages = %#v, want codexSessionOpenedMsg", msgs)
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderCodex {
		t.Fatalf("request provider = %q, want Codex", requests[0].Provider)
	}
	if !requests[0].ForceNew {
		t.Fatalf("request ForceNew = false, want true")
	}
	for _, want := range []string{
		"Resolve the current Git merge conflicts",
		"Current branch: feat/conflict",
		"Inspect `git status --short`",
		"Do not commit, push, abort",
		"remaining `git status --short` state",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("/resolve prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
}

func TestResolveGitlinkConflictTargetStartsFreshEngineerInSubmoduleWorktree(t *testing.T) {
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
		t.Fatalf("gitlink resolve target cmd = nil, want fresh engineer launch")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != worktreePath || !got.codexPendingOpen.newSession {
		t.Fatalf("codexPendingOpen = %#v, want fresh pending open for submodule worktree", got.codexPendingOpen)
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			break
		}
	}
	if opened.projectPath != worktreePath {
		t.Fatalf("opened project path = %q, want %q (msgs %#v)", opened.projectPath, worktreePath, msgs)
	}
	if opened.err != nil {
		t.Fatalf("codexSessionOpenedMsg.err = %v", opened.err)
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
		"Resolve the submodule content conflicts",
		"Parent repo: " + parentPath,
		"Submodule path: assets_src",
		"Submodule merge worktree: " + worktreePath,
		"Submodule merge branch: lcroom/master/assets_src-merge-aaaa-bbbb",
		"commit the submodule merge on its current branch and push that branch upstream",
		"Stage the parent repo gitlink",
		"Do not commit the parent repo merge",
		"remaining `git status --short` state",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("/resolve gitlink prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
}

func TestDispatchResolveBlocksWhileSameEngineerTurnActive(t *testing.T) {
	projectPath := "/tmp/resolve-active"
	liveSession := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			ThreadID: "active-thread",
			Started:  true,
			Busy:     true,
			Status:   "Codex is working...",
		},
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return liveSession, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "resolve-active",
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindResolve})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/resolve) cmd = %#v, want nil while active engineer turn blocks launch", cmd)
	}
	if got.status != "Resolve blocked: another engineer session is open" {
		t.Fatalf("status = %q, want compact resolve refusal", got.status)
	}
	if got.actionNoticeDialog == nil {
		t.Fatal("active engineer refusal should open a notice dialog")
	}
	if !strings.Contains(got.actionNoticeDialog.Summary, "Another engineer session is already open") {
		t.Fatalf("notice summary = %q, want concise engineer-session explanation", got.actionNoticeDialog.Summary)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the existing active session request", len(requests))
	}
	if len(liveSession.submitted) != 0 {
		t.Fatalf("active session received submissions: %#v", liveSession.submitted)
	}
}

func TestDispatchResolveShowsIdleSessionBlockInNoticeDialog(t *testing.T) {
	projectPath := "/tmp/resolve-idle"
	liveSession := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			ThreadID: "idle-thread",
			Started:  true,
			Status:   "Codex turn completed",
		},
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return liveSession, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "resolve-idle",
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindResolve})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/resolve) cmd = %#v, want nil while idle session blocks fresh launch", cmd)
	}
	if got.status != "Resolve blocked: another engineer session is open" {
		t.Fatalf("status = %q, want compact resolve refusal", got.status)
	}
	if strings.Contains(got.status, "An idle turn") {
		t.Fatalf("top status retained the long explanation: %q", got.status)
	}
	if got.actionNoticeDialog == nil {
		t.Fatal("idle engineer refusal should open a notice dialog")
	}
	rendered := ansi.Strip(got.renderActionNoticeDialogContent(72))
	normalizedRendered := strings.Join(strings.Fields(rendered), " ")
	for _, want := range []string{
		"Resolve blocked",
		"resolve-idle",
		"Another engineer session is already open",
		"Do this first",
		"Open the existing engineer session",
		"More detail",
		"/resolve opens a new session",
		"idle may not mean finished",
		"ask it to resolve the conflict",
		"Enter/Esc",
	} {
		if !strings.Contains(normalizedRendered, want) {
			t.Fatalf("notice dialog missing %q:\n%s", want, rendered)
		}
	}
	summaryIndex := strings.Index(normalizedRendered, "Another engineer session is already open")
	actionIndex := strings.Index(normalizedRendered, "Do this first")
	detailIndex := strings.Index(normalizedRendered, "More detail")
	if summaryIndex < 0 || actionIndex <= summaryIndex || detailIndex <= actionIndex {
		t.Fatalf("notice hierarchy should be summary, first action, then detail:\n%s", rendered)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the existing idle session request", len(requests))
	}
	narrowOverlay := ansi.Strip(got.renderActionNoticeDialogOverlay("", 60, 20))
	if width := lipgloss.Width(narrowOverlay); width != 60 {
		t.Fatalf("narrow notice width = %d, want 60", width)
	}
	if height := lipgloss.Height(narrowOverlay); height != 20 {
		t.Fatalf("narrow notice height = %d, want 20", height)
	}
	normalizedNarrowOverlay := strings.Join(strings.Fields(narrowOverlay), " ")
	for _, want := range []string{
		"Another engineer session is already",
		"/resolve cannot start",
		"Do this first",
		"Open the existing engineer session",
		"More detail",
		"Enter/Esc",
	} {
		if !strings.Contains(normalizedNarrowOverlay, want) {
			t.Fatalf("narrow notice dialog missing %q:\n%s", want, narrowOverlay)
		}
	}
	if !strings.Contains(narrowOverlay, "Resolve blocked") {
		t.Fatalf("narrow notice lost its title:\n%s", narrowOverlay)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.actionNoticeDialog != nil {
		t.Fatal("Esc should close the resolve notice dialog")
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
