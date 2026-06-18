package tui

import (
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"image/color"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisibleCodexViewShowsBannerAndYoloWarning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex session ready",
			Model:           "gpt-5-codex",
			ReasoningEffort: "high",
			Transcript:      "Codex: hello",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if !strings.Contains(rendered, "Codex | demo") {
		t.Fatalf("embedded Codex view should use a compact Codex banner: %q", rendered)
	}
	if !strings.Contains(rendered, "YOLO MODE") {
		t.Fatalf("embedded Codex view should show YOLO warning: %q", rendered)
	}
	if !strings.Contains(rendered, "hello") {
		t.Fatalf("embedded Codex view should render transcript: %q", rendered)
	}
	if strings.Contains(rendered, "Codex: hello") {
		t.Fatalf("embedded Codex view should not repeat the sender label in fallback transcript rendering: %q", rendered)
	}
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) == 0 {
		t.Fatalf("rendered view should have lines")
	}
	if len(lines) != m.height {
		t.Fatalf("embedded Codex view line count = %d, want %d; render was %q", len(lines), m.height, ansi.Strip(rendered))
	}
	if strings.Contains(lines[0], "Little Control Room - Control Center for AI Tasks") {
		t.Fatalf("embedded Codex banner should omit the full app title: %q", lines[0])
	}
	if !strings.Contains(lines[0], "YOLO MODE") {
		t.Fatalf("YOLO warning should live on the top banner: %q", lines[0])
	}
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "YOLO MODE") {
		t.Fatalf("YOLO warning should be overlaid at the banner's right edge: %q", lines[0])
	}
	if !strings.Contains(lines[0], " YOLO MODE") {
		t.Fatalf("YOLO warning should keep a small spacer from the banner text: %q", lines[0])
	}
	if strings.Count(ansi.Strip(rendered), "YOLO MODE") != 1 {
		t.Fatalf("YOLO warning should appear exactly once: %q", ansi.Strip(rendered))
	}
	if strings.Contains(lines[0], "Alt+Down picker") || strings.Contains(lines[0], "Alt+[ prev") || strings.Contains(lines[0], "Alt+] next") {
		t.Fatalf("embedded Codex view should omit obsolete picker/session shortcuts from the banner line: %q", rendered)
	}
	if !strings.Contains(lines[0], "Alt+L blocks") {
		t.Fatalf("embedded Codex view should keep block controls on the banner line: %q", rendered)
	}
	if len(lines) > 1 && strings.Contains(lines[1], "Alt+Down picker") {
		t.Fatalf("embedded Codex view should not spend a separate row on banner shortcuts: %q", rendered)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("embedded Codex view line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
}

func TestVisibleOpenCodeViewShowsBannerAndYoloWarning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:         codexapp.ProviderOpenCode,
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Status:           "OpenCode session ready",
			Model:            "openai/gpt-5.4",
			ReasoningEffort:  "high",
			Transcript:       "OpenCode: hello",
			LastSystemNotice: "Started a new embedded OpenCode session ses_demo.",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if !strings.Contains(rendered, "OpenCode | demo") {
		t.Fatalf("embedded OpenCode view should use a compact OpenCode banner: %q", rendered)
	}
	if !strings.Contains(rendered, "YOLO MODE") {
		t.Fatalf("embedded OpenCode view should show YOLO warning: %q", rendered)
	}
}

func TestVisibleLCAgentViewShowsPermissionBadgeInBanner(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:        codexapp.ProviderLCAgent,
			Started:         true,
			Status:          "LCAgent session ready",
			Model:           "deepseek-chat",
			PermissionLevel: "low",
			Transcript:      "LCAgent: hello",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		t.Fatalf("rendered view should have lines")
	}
	if !strings.Contains(lines[0], "PERM LOW") {
		t.Fatalf("LCAgent permission should live on the top banner: %q", lines[0])
	}
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "PERM LOW") {
		t.Fatalf("LCAgent permission badge should be overlaid at the banner's right edge: %q", lines[0])
	}
	if strings.Contains(rendered, "Perm Low") {
		t.Fatalf("LCAgent permission should not be repeated beside the model meta: %q", rendered)
	}
}

func TestCodexLowerBlocksOmitSidebarSessionMeta(t *testing.T) {
	tokenBudget := int64(5000)
	m := Model{
		codexInput: newCodexTextarea(),
		nowFn:      func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) },
	}
	rendered := ansi.Strip(strings.Join(m.codexLowerBlocks(codexapp.Snapshot{
		Model:            "gpt-5-codex",
		ReasoningEffort:  "high",
		PendingModel:     "gpt-5",
		PendingReasoning: "medium",
		Goal: &codexapp.ThreadGoal{
			Objective:   "ship the goal indicator",
			Status:      codexapp.ThreadGoalStatusActive,
			TokenBudget: &tokenBudget,
			TokensUsed:  1200,
		},
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "Continue from the last breakpoint"},
		},
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
	}, 180), "\n"))

	for _, unwanted := range []string{"Model gpt-5-codex", "Reasoning high", "Context max 200,000 tok", "Tok i10k", "Goal active", "Next gpt-5 / medium"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("codex lower blocks should omit sidebar session meta %q: %q", unwanted, rendered)
		}
	}
}

func TestCodexSnapshotGoalLabelShowsGoalStates(t *testing.T) {
	budget := int64(100)
	tests := []struct {
		name string
		goal *codexapp.ThreadGoal
		want string
	}{
		{
			name: "none",
			goal: nil,
			want: "",
		},
		{
			name: "active",
			goal: &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusActive},
			want: "active",
		},
		{
			name: "budget limited",
			goal: &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusBudgetLimited, TokenBudget: &budget, TokensUsed: 101},
			want: "budget-limited 101/100 tok",
		},
		{
			name: "blocked",
			goal: &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusBlocked},
			want: "blocked",
		},
		{
			name: "complete",
			goal: &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusComplete},
			want: "complete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexSnapshotGoalLabel(codexapp.Snapshot{Goal: tt.goal}); got != tt.want {
				t.Fatalf("codexSnapshotGoalLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVisibleCodexViewShowsBusyElsewhereWarningBlock(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Busy:             true,
			BusyExternal:     true,
			ThreadID:         "019cccc3abcdef",
			LastSystemNotice: "Resumed embedded Codex session 019cccc3. It is already active in another Codex process, so embedded controls are read-only until it finishes.",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptAgent, Text: "Still waiting on the other run."},
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if !strings.Contains(rendered, "Read-only") {
		t.Fatalf("busy-elsewhere view should show a read-only warning block: %q", rendered)
	}
	if !strings.Contains(rendered, "Resumed embedded Codex session 019cccc3") {
		t.Fatalf("busy-elsewhere view should surface the resume warning prominently: %q", rendered)
	}
}

func TestVisibleCodexViewShowsCompactingStateInsteadOfBusyElsewhere(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Busy:    true,
			Status:  "Compacting conversation history...",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptSystem, Text: "Compacting conversation history..."},
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "Read-only") {
		t.Fatalf("compacting view should not show a read-only warning: %q", rendered)
	}
	if strings.Contains(rendered, "Working elsewhere") {
		t.Fatalf("compacting view should not look like an external busy session: %q", rendered)
	}
	if !strings.Contains(rendered, "Compacting conversation") {
		t.Fatalf("compacting view should show an explicit compaction footer: %q", rendered)
	}
}

func TestVisibleCodexViewUsesPromptInsteadOfPlaceholder(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "Message Codex") {
		t.Fatalf("embedded Codex composer should not render the old placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "> ") {
		t.Fatalf("embedded Codex composer should render a prompt marker when empty: %q", rendered)
	}
}

func TestVisibleCodexViewUsesStructuredTranscriptEntries(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: "summarize this repo"},
				{Kind: codexapp.TranscriptAgent, Text: "Here is a quick summary."},
				{Kind: codexapp.TranscriptCommand, Text: "$ git status --short\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"},
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "summarize this repo") {
		t.Fatalf("structured transcript should show a user block: %q", rendered)
	}
	if !strings.Contains(rendered, "Here is a quick summary.") {
		t.Fatalf("structured transcript should show an agent block: %q", rendered)
	}
	if !strings.Contains(rendered, "Command") || !strings.Contains(rendered, "$ git status --short") {
		t.Fatalf("structured transcript should render command blocks: %q", rendered)
	}
}

func TestVisibleCodexViewStripsTerminalEscapeSequencesFromCommandOutput(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{
					Kind: codexapp.TranscriptCommand,
					Text: "$ make tui\n\x1b[?1049h\x1b[2J\x1b[HLittle Control Room\n\x1b[?1049l[command completed, exit 0]",
				},
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		codexDenseBlockMode: codexDenseBlockPreview,
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.renderCodexView()
	if strings.Contains(rendered, "\x1b[?1049h") || strings.Contains(rendered, "\x1b[2J") {
		t.Fatalf("embedded Codex view should strip nested terminal control sequences from transcript output: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Little Control Room") {
		t.Fatalf("embedded Codex view should preserve readable command output after stripping terminal escapes: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesWrapLongAgentMessagesWithoutSenderLabels(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "This is a deliberately long Codex reply that should wrap across multiple lines inside the embedded transcript pane instead of being truncated off the edge.",
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 28))
	if strings.Contains(rendered, "Codex:") {
		t.Fatalf("structured transcript should not repeat the agent sender label: %q", rendered)
	}

	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("long agent message should wrap to multiple lines: %q", rendered)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 28 {
			t.Fatalf("wrapped line width = %d, want <= 28: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexTranscriptEntryHighlightsUserEchoBlock(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	userRendered := renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptUser,
		Text: "summarize this repo",
	}, 36, codexDenseBlockSummary)
	if !strings.Contains(userRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("user transcript entry should reuse the composer background color: %q", userRendered)
	}

	agentRendered := renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptAgent,
		Text: "Here is a quick summary.",
	}, 36, codexDenseBlockSummary)
	if strings.Contains(agentRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("agent transcript entry should not inherit the user echo background: %q", agentRendered)
	}
}

func TestRenderCodexTranscriptEntryCompactsToolCallsToSingleLine(t *testing.T) {
	rendered := ansi.Strip(renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptTool,
		Text: "Tool call completed: read README.md\nusing rg --files",
	}, 90, codexDenseBlockSummary))

	// New structured rendering shows tool name bold + summary
	if !strings.Contains(rendered, "call") {
		t.Fatalf("tool transcript entry should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "read README.md") {
		t.Fatalf("tool transcript entry should show summary: %q", rendered)
	}
	if strings.Count(rendered, "\n") > 1 {
		t.Fatalf("tool transcript entry should render compactly: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntryRendersPlanLabelOnOwnLine(t *testing.T) {
	rendered := ansi.Strip(renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptPlan,
		Text: "[x] Inspect renderer\n[>] Add plan status styling\n[ ] Run verification",
	}, 64, codexDenseBlockSummary))

	lines := strings.Split(rendered, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		if text := strings.TrimSpace(line); text != "" {
			trimmed = append(trimmed, text)
		}
	}
	wantLines := []string{
		"Plan",
		"[x] Inspect renderer",
		"[>] Add plan status styling",
		"[ ] Run verification",
	}
	for i, want := range wantLines {
		if i >= len(trimmed) || trimmed[i] != want {
			t.Fatalf("plan line %d = %q, want %q; rendered:\n%s", i, trimmed, want, rendered)
		}
	}
	if strings.Contains(rendered, "Plan [x]") {
		t.Fatalf("plan label should not share the first item line:\n%s", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsConsecutiveToolCallsDense(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: read README.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: scan internal/tui"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "\n\n") {
		t.Fatalf("consecutive tool transcript entries should not be separated by a blank line: %q", rendered)
	}
	if strings.Count(rendered, "\n") != 1 {
		t.Fatalf("consecutive tool transcript entries should render as one line each: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeToolRuns(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: read STATUS.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect codex_pane.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect app_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect opencode_session.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect opencode_session_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: prepare patch"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	// Collapsed run should show the common tool name once, not repeat "Tool bash completed:" per entry
	if !strings.Contains(rendered, "bash") {
		t.Fatalf("collapsed OpenCode tool run should show the common tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "+3 more tool updates") {
		t.Fatalf("collapsed OpenCode tool run should mention omitted updates: %q", rendered)
	}
	if strings.Contains(rendered, "inspect opencode_session.go") {
		t.Fatalf("collapsed OpenCode tool run should omit later repetitive updates: %q", rendered)
	}
	// Should NOT repeat "Tool bash completed:" for each entry
	if strings.Count(rendered, "bash completed") > 0 {
		t.Fatalf("collapsed OpenCode tool run should strip redundant prefixes: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocks(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("long assistant code block should not collapse: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("long assistant code block should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocksWithoutFence(t *testing.T) {
	codeLines := make([]string, 0, 120)
	for i := 1; i <= 120; i++ {
		codeLines = append(codeLines, "if (i == "+fmt.Sprintf("%d", i)+") { const shake = scene.cameras.main.shake(0, 0); }")
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(codeLines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("long non-fenced OpenCode code block should not collapse: %q", rendered)
	}
	if !strings.Contains(rendered, "i == 120") {
		t.Fatalf("long non-fenced assistant code should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeConsecutiveAgentChunks(t *testing.T) {
	chunk := make([]string, 0, 16)
	for i := 1; i <= 16; i++ {
		chunk = append(chunk, fmt.Sprintf("if (cond_%d) { const shake = scene.cameras.main.shake(0, 0); }", i))
	}
	entries := make([]codexapp.TranscriptEntry, 0, 20)
	for i := 0; i < 10; i++ {
		entries = append(entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: strings.Join(chunk, "\n")})
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries:  entries,
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("consecutive OpenCode agent chunks should not collapse to summary: %q", rendered)
	}
	if count := strings.Count(rendered, "cond_"); count != 160 {
		t.Fatalf("assistant chunks should preserve all code lines, got %d cond_ markers: %q", count, rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocksWithDenseMode(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}
	m := Model{codexDenseBlockMode: codexDenseBlockFull}
	rendered := ansi.Strip(m.renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("dense-expanded OpenCode should keep the full assistant output: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("expanded output should include the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsCodexAgentCodeBlocksUncollapsed(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("Codex should not use OpenCode-only code collapse behavior: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("Codex transcript should keep full code line text: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsCodexToolRunsUncollapsed(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: read STATUS.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect codex_pane.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect app_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect opencode_session.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect opencode_session_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: prepare patch"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Tool activity:") {
		t.Fatalf("Codex tool runs should keep the existing dense rendering: %q", rendered)
	}
	if !strings.Contains(rendered, "inspect opencode_session.go") {
		t.Fatalf("Codex tool runs should still show individual updates: %q", rendered)
	}
}

func TestRenderCodexToolLineShowsToolNameAndSummary(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Tool bash completed: Run focused service tests", 80))
	if !strings.Contains(rendered, "bash") {
		t.Fatalf("tool line should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "Run focused service tests") {
		t.Fatalf("tool line should show summary: %q", rendered)
	}
	// "completed" status should be suppressed (noise)
	if strings.Contains(rendered, "completed") {
		t.Fatalf("tool line should suppress 'completed' status: %q", rendered)
	}
}

func TestRenderCodexToolLineShowsNonCompletedStatus(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Tool bash running: long operation", 80))
	if !strings.Contains(rendered, "running") {
		t.Fatalf("tool line should show non-completed status: %q", rendered)
	}
}

func TestRenderCodexToolLineParsesWebSearch(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Web search: golang concurrency patterns", 80))
	if !strings.Contains(rendered, "search") {
		t.Fatalf("web search should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "golang concurrency patterns") {
		t.Fatalf("web search should show query: %q", rendered)
	}
}

func TestRenderCodexToolLineParsesMCPTool(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("MCP tool myserver/query [completed]", 80))
	if !strings.Contains(rendered, "myserver/query") {
		t.Fatalf("MCP tool should show server/tool name: %q", rendered)
	}
}

func TestRenderCodexBodyRendersNumberedList(t *testing.T) {
	body := "Steps:\n1. First item\n2. Second item\n3. Third item"
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "1.") || !strings.Contains(rendered, "First item") {
		t.Fatalf("numbered list should render items: %q", rendered)
	}
	if !strings.Contains(rendered, "3.") || !strings.Contains(rendered, "Third item") {
		t.Fatalf("numbered list should render all items: %q", rendered)
	}
}

func TestRenderCodexBodyRendersHorizontalRule(t *testing.T) {
	body := "Above\n---\nBelow"
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "─") {
		t.Fatalf("horizontal rule should render as box-drawing line: %q", rendered)
	}
	if !strings.Contains(rendered, "Above") || !strings.Contains(rendered, "Below") {
		t.Fatalf("content around horizontal rule should be preserved: %q", rendered)
	}
}

func TestRenderCodexDenseBlockHidesSuccessfulExitInCollapsedMode(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockPreview))
	if strings.Contains(rendered, "exit 0") {
		t.Fatalf("collapsed command block should hide successful exit: %q", rendered)
	}
	if strings.Contains(rendered, "# cwd:") {
		t.Fatalf("collapsed command block should hide cwd comment: %q", rendered)
	}
	if !strings.Contains(rendered, "git status") {
		t.Fatalf("collapsed command block should keep the command: %q", rendered)
	}
	if !strings.Contains(rendered, "README.md") {
		t.Fatalf("collapsed command block should keep command output: %q", rendered)
	}
}

func TestRenderCodexDenseBlockHidesOutputByDefault(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n?? notes.txt\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockSummary))
	if !strings.Contains(rendered, "$ git status") {
		t.Fatalf("summary command block should keep the command: %q", rendered)
	}
	for _, hidden := range []string{"README.md", "notes.txt", "exit 0", "# cwd:"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("summary command block should hide %q: %q", hidden, rendered)
		}
	}
	if !strings.Contains(rendered, "2 lines hidden") || !strings.Contains(rendered, "Alt+L previews") {
		t.Fatalf("summary command block should mention hidden previewable lines: %q", rendered)
	}
	if !strings.Contains(rendered, "Command (2 lines hidden; Alt+L previews) -> $ git status") {
		t.Fatalf("summary command block should keep the title and command on one line: %q", rendered)
	}
}

func TestRenderCodexDenseBlockTruncatesLongCollapsedCommandInlineSummary(t *testing.T) {
	paths := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		paths = append(paths, fmt.Sprintf("captures/player-sprite-prototype/20260608/rb00-root-180-axis-neg-current/sprite-%02d.png", i))
	}
	command := `$ /bin/zsh -lc "magick montage ` + strings.Join(paths, " ") + ` -tile 6x5 out.png"`
	body := command + "\nfirst output line\nsecond output line\n[command completed, exit 0]"

	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 90, codexDenseBlockSummary))
	lines := strings.Split(strings.TrimSpace(rendered), "\n")
	if len(lines) != 1 {
		t.Fatalf("summary command block should keep long commands to one display line, got %d lines: %q", len(lines), rendered)
	}
	if !strings.Contains(rendered, `$ /bin/zsh -lc`) {
		t.Fatalf("summary command block should keep the command prefix: %q", rendered)
	}
	if !strings.Contains(rendered, "...") {
		t.Fatalf("summary command block should mark truncated commands: %q", rendered)
	}
	if strings.Contains(rendered, "sprite-29.png") {
		t.Fatalf("summary command block should truncate the far tail of long commands: %q", rendered)
	}
	if width := ansi.StringWidth(lines[0]); width > 90 {
		t.Fatalf("summary command block line width = %d, want <= 90: %q", width, lines[0])
	}

	full := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 90, codexDenseBlockFull))
	if !strings.Contains(full, "sprite-29.png") {
		t.Fatalf("expanded command block should keep the full command: %q", full)
	}
}

func TestRenderCodexDenseBlockPreviewShowsFiveOutputLines(t *testing.T) {
	outputLines := make([]string, 0, 7)
	for i := 1; i <= 7; i++ {
		outputLines = append(outputLines, fmt.Sprintf("output line %d", i))
	}
	body := "$ demo\n" + strings.Join(outputLines, "\n") + "\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockPreview))
	if !strings.Contains(rendered, "output line 5") {
		t.Fatalf("preview command block should show the fifth output line: %q", rendered)
	}
	if strings.Contains(rendered, "output line 6") {
		t.Fatalf("preview command block should hide output past five lines: %q", rendered)
	}
	if !strings.Contains(rendered, "2 lines hidden") || !strings.Contains(rendered, "Alt+L expands") {
		t.Fatalf("preview command block should mention remaining hidden lines: %q", rendered)
	}
	if !strings.Contains(rendered, "Command (2 lines hidden; Alt+L expands) -> $ demo") {
		t.Fatalf("preview command block should keep the title and command on one line: %q", rendered)
	}
}

func TestRenderCodexDenseBlockKeepsFailedExitInCollapsedMode(t *testing.T) {
	body := "$ make test\nerror: tests failed\n[command completed, exit 1]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockSummary))
	if !strings.Contains(rendered, "exit 1") {
		t.Fatalf("collapsed command block should keep non-zero exit: %q", rendered)
	}
}

func TestRenderCodexDenseBlockShowsAllInExpandedMode(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockFull))
	if !strings.Contains(rendered, "exit 0") {
		t.Fatalf("expanded command block should show exit status: %q", rendered)
	}
	if !strings.Contains(rendered, "# cwd:") {
		t.Fatalf("expanded command block should show cwd comment: %q", rendered)
	}
}

func TestCodexTranscriptEntrySeparatorTightensToolCommandTransitions(t *testing.T) {
	// tool→command should be tight
	sep := codexTranscriptEntrySeparator(codexapp.TranscriptTool, codexapp.TranscriptCommand)
	if sep != "\n" {
		t.Fatalf("tool→command should use tight separator, got %q", sep)
	}
	// command→tool should be tight
	sep = codexTranscriptEntrySeparator(codexapp.TranscriptCommand, codexapp.TranscriptTool)
	if sep != "\n" {
		t.Fatalf("command→tool should use tight separator, got %q", sep)
	}
	// command→command should be tight
	sep = codexTranscriptEntrySeparator(codexapp.TranscriptCommand, codexapp.TranscriptCommand)
	if sep != "\n" {
		t.Fatalf("command→command should use tight separator, got %q", sep)
	}
	// agent→tool should still be double
	sep = codexTranscriptEntrySeparator(codexapp.TranscriptAgent, codexapp.TranscriptTool)
	if sep != "\n\n" {
		t.Fatalf("agent→tool should use standard separator, got %q", sep)
	}
}

func TestReasoningIndicatorShownWhenHidden(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptAgent, Text: "Let me think about this..."},
			{Kind: codexapp.TranscriptReasoning, Text: "Step 1: analyze the problem\nStep 2: consider options\nStep 3: pick the best one"},
			{Kind: codexapp.TranscriptAgent, Text: "Here is my answer."},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "Thinking") {
		t.Fatalf("hidden reasoning should show compact indicator: %q", rendered)
	}
	if !strings.Contains(rendered, "3 lines") {
		t.Fatalf("reasoning indicator should show line count: %q", rendered)
	}
	// Should NOT show the actual reasoning text
	if strings.Contains(rendered, "Step 1") {
		t.Fatalf("hidden reasoning should not show reasoning content: %q", rendered)
	}
	// Agent messages should still be visible
	if !strings.Contains(rendered, "Here is my answer") {
		t.Fatalf("agent messages should still be visible around reasoning indicator: %q", rendered)
	}
}

func TestReasoningIndicatorMergesConsecutiveEntries(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "First thought\nSecond thought"},
			{Kind: codexapp.TranscriptReasoning, Text: "Third thought\nFourth thought\nFifth thought"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "5 lines") {
		t.Fatalf("consecutive reasoning entries should merge into one indicator with total lines: %q", rendered)
	}
	// Should only have one "Thinking" indicator, not two
	if strings.Count(rendered, "Thinking") != 1 {
		t.Fatalf("should have exactly one thinking indicator for consecutive reasoning: %q", rendered)
	}
}

func TestReasoningShownFullyWhenNotHidden(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "Detailed reasoning step here"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: false}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "Thinking") {
		t.Fatalf("non-hidden reasoning should not show compact indicator: %q", rendered)
	}
	if !strings.Contains(rendered, "Detailed reasoning step here") {
		t.Fatalf("non-hidden reasoning should show full content: %q", rendered)
	}
}

func TestReasoningExpandedWithAltL(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "Detailed reasoning step here"},
		},
	}

	// hideReasoningSections=true but full block mode (Alt+L twice) should show full reasoning
	rendered := ansi.Strip((Model{hideReasoningSections: true, codexDenseBlockMode: codexDenseBlockFull}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "Thinking") {
		t.Fatalf("Alt+L should bypass reasoning hiding and show full content: %q", rendered)
	}
	if !strings.Contains(rendered, "Detailed reasoning step here") {
		t.Fatalf("Alt+L should reveal full reasoning content: %q", rendered)
	}
}

func TestReasoningIndicatorSingularLine(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "One line of thought"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "1 line,") {
		t.Fatalf("single-line reasoning should use singular 'line': %q", rendered)
	}
	if strings.Contains(rendered, "1 lines") {
		t.Fatalf("should not say '1 lines': %q", rendered)
	}
}

func TestDefaultConfigHidesReasoningSections(t *testing.T) {
	cfg := config.Default()
	if !cfg.HideReasoningSections {
		t.Fatal("HideReasoningSections should default to true")
	}
}

func TestRenderCodexBodyRendersBoldAndItalic(t *testing.T) {
	rendered := ansi.Strip(renderCodexBody("This is **bold** and *italic* text.", lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "bold") {
		t.Fatalf("bold text should be preserved: %q", rendered)
	}
	if !strings.Contains(rendered, "italic") {
		t.Fatalf("italic text should be preserved: %q", rendered)
	}
	// The ** and * delimiters should be stripped
	if strings.Contains(rendered, "**") {
		t.Fatalf("bold markers should be stripped: %q", rendered)
	}
}

func TestRenderCodexBodyRendersInlineCodeWithoutBackticks(t *testing.T) {
	body := "This workflow is scheduled twice a day (`0 0` and `0 1`) and maps to `02:00 Europe/Rome`."
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 52))
	normalized := strings.Join(strings.Fields(rendered), " ")

	for _, want := range []string{"0 0", "0 1", "02:00 Europe/Rome"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("inline code content should be preserved, missing %q in %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "`") {
		t.Fatalf("inline code markers should be stripped: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 52 {
			t.Fatalf("wrapped line width = %d, want <= 52: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexBodyRendersMarkdownTable(t *testing.T) {
	table := "| Name | Value | Status |\n| --- | --- | --- |\n| foo | 42 | ok |\n| bar | 99 | err |"
	rendered := ansi.Strip(renderCodexBody(table, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "Name") || !strings.Contains(rendered, "foo") {
		t.Fatalf("table should render cell contents: %q", rendered)
	}
	if !strings.Contains(rendered, "│") {
		t.Fatalf("table should use box-drawing separators: %q", rendered)
	}
	if !strings.Contains(rendered, "─") {
		t.Fatalf("table should render horizontal separator: %q", rendered)
	}
}

func TestRenderCodexBodyWrapsMarkdownTableCellsWithoutTruncation(t *testing.T) {
	longValue := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"
	table := "| Name | Value |\n| --- | --- |\n| item | " + longValue + " |"
	rendered := ansi.Strip(renderCodexBody(table, lipgloss.Color("252"), 40))
	if strings.Contains(rendered, "…") || strings.Contains(rendered, "...") {
		t.Fatalf("table cells should wrap instead of truncating: %q", rendered)
	}
	compact := renderedMarkdownTableReadableText(rendered)
	if !strings.Contains(compact, longValue) {
		t.Fatalf("wrapped table should preserve full cell contents, got %q from %q", compact, rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 40 {
			t.Fatalf("wrapped table line width = %d, want <= 40: %q", width, line)
		}
	}
}

func TestRenderCodexTranscriptEntriesKeepsMassiveAgentOutput(t *testing.T) {
	lines := make([]string, 1200)
	for i := range lines {
		lines[i] = fmt.Sprintf("This is output line %d with some content.", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("assistant output should not be bottom-clipped: %q", rendered)
	}
	if !strings.Contains(rendered, "This is output line 1199 with some content.") {
		t.Fatalf("assistant output should preserve the final line: %q", rendered)
	}
}

func renderedMarkdownTableReadableText(text string) string {
	replacer := strings.NewReplacer(
		"\n", "",
		"\t", "",
		" ", "",
		"│", "",
		"├", "",
		"┤", "",
		"─", "",
		"┼", "",
	)
	return replacer.Replace(text)
}

func TestRenderCodexTranscriptEntriesKeepsLongReadableMarkdownAgentOutput(t *testing.T) {
	lines := []string{
		"That is probably much more model-native than a custom graph API with bespoke object types. LLMs already understand:",
		"",
		"```bash",
		"ls",
		"cat",
		"grep",
		"find",
		"mkdir",
		"cp",
		"mv",
		"touch",
		"sed",
		"jq",
		"git",
		"```",
		"",
	}
	for i := len(lines); i < 217; i++ {
		lines = append(lines, fmt.Sprintf("Readable explanation line %d.", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("readable assistant Markdown should not be treated as dense output: %q", rendered)
	}
	if !strings.Contains(rendered, "Readable explanation line 216.") {
		t.Fatalf("readable assistant Markdown should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesMassiveReasoningOutput(t *testing.T) {
	lines := make([]string, 150)
	for i := range lines {
		lines[i] = fmt.Sprintf("Thinking step %d...", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptReasoning,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if !strings.Contains(rendered, "Long reasoning truncated") {
		t.Fatalf("massive reasoning output should be truncated: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesRepetitiveContent(t *testing.T) {
	// Simulate GLM-5 style repetitive output
	lines := make([]string, 0, 100)
	lines = append(lines, "Here is the solution:")
	block := []string{
		"Step 1: Initialize the variable",
		"Step 2: Loop through items",
		"Step 3: Process each item",
		"Step 4: Return the result",
		"Step 5: Clean up resources",
		"Step 6: Log completion",
	}
	for i := 0; i < 10; i++ {
		lines = append(lines, block...)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if !strings.Contains(rendered, "Repetitive") {
		t.Fatalf("repetitive content should be detected and collapsed: %q", rendered)
	}
	if !strings.Contains(rendered, "similar blocks omitted") {
		t.Fatalf("repetitive collapse should mention omitted blocks: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesConsecutiveDuplicateAssistantMessages(t *testing.T) {
	repeated := "Still paused, as requested. I won't resume or touch the worktree until you explicitly tell me to."
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptAgent, Text: repeated},
			{Kind: codexapp.TranscriptAgent, Text: repeated},
			{Kind: codexapp.TranscriptAgent, Text: repeated},
			{Kind: codexapp.TranscriptAgent, Text: "Different final answer"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if count := strings.Count(rendered, repeated); count != 1 {
		t.Fatalf("duplicate assistant message rendered %d times, want 1:\n%s", count, rendered)
	}
	if !strings.Contains(rendered, "Assistant message repeated 2 more times") {
		t.Fatalf("rendered transcript should summarize collapsed duplicates:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Different final answer") {
		t.Fatalf("rendered transcript should keep following non-duplicate answer:\n%s", rendered)
	}
}

func TestRenderCodexTranscriptEntriesExpandsConsecutiveDuplicateAssistantMessagesWithDenseMode(t *testing.T) {
	repeated := "Still paused, as requested."
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptAgent, Text: repeated},
			{Kind: codexapp.TranscriptAgent, Text: repeated},
		},
	}

	rendered := ansi.Strip((Model{codexDenseBlockMode: codexDenseBlockFull}).renderCodexTranscriptEntries(snapshot, 120))
	if count := strings.Count(rendered, repeated); count != 2 {
		t.Fatalf("dense-expanded transcript rendered duplicate assistant message %d times, want 2:\n%s", count, rendered)
	}
	if strings.Contains(rendered, "message repeated") {
		t.Fatalf("dense-expanded transcript should not collapse duplicates:\n%s", rendered)
	}
}

func TestRenderCodexTranscriptEntriesExpandsMassiveOutputWithDenseMode(t *testing.T) {
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = fmt.Sprintf("This is output line %d with some content.", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{codexDenseBlockMode: codexDenseBlockFull}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("dense-expanded mode should show full output: %q", rendered[:200])
	}
	if !strings.Contains(rendered, "output line 249") {
		t.Fatalf("dense-expanded mode should include all lines: %q", rendered[len(rendered)-200:])
	}
}

func TestRenderCodexTranscriptEntriesParsesLegacyTranscriptWithoutSenderLabels(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Transcript: "You: summarize this repo\n\nCodex: Here is a deliberately long answer that should wrap inside the pane without repeating the sender name on screen.",
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 30))
	normalized := strings.Join(strings.Fields(rendered), " ")
	if !strings.Contains(normalized, "summarize this repo") || !strings.Contains(normalized, "Here is a deliberately long answer") {
		t.Fatalf("legacy transcript fallback should still render the conversation text: %q", rendered)
	}
	if strings.Contains(rendered, "You:") || strings.Contains(rendered, "Codex:") {
		t.Fatalf("legacy transcript fallback should hide sender labels: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 30 {
			t.Fatalf("legacy wrapped line width = %d, want <= 30: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersLocalMarkdownLinksAsArtifacts(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [README](/tmp/demo/README.md).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/README.md")) {
		t.Fatalf("rendered transcript should not rely on terminal hyperlinks for local markdown artifacts: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[README](/tmp/demo/README.md)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "See README (README.md) Alt+O.") {
		t.Fatalf("rendered transcript should preserve the local markdown artifact in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesResolvesRelativeMarkdownArtifactLinksAgainstProjectPath(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "proj_leaf")
	siblingPath := filepath.Join(root, "proj_leaf_bulk")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := os.MkdirAll(siblingPath, 0o755); err != nil {
		t.Fatalf("create sibling dir: %v", err)
	}
	artifactPath := filepath.Join(siblingPath, "tmp_export_ui_integration_map_2026-06-02.html")
	if err := os.WriteFile(artifactPath, []byte("<!doctype html>\n"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [integration map](../proj_leaf_bulk/tmp_export_ui_integration_map_2026-06-02.html).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 140)
	if strings.Contains(rendered, ansi.SetHyperlink(artifactPath)) {
		t.Fatalf("relative local artifact links should not rely on terminal hyperlinks: %q", rendered)
	}
	if strings.Contains(rendered, "../proj_leaf_bulk") {
		t.Fatalf("rendered transcript should not expose unresolved relative artifact paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[integration map](") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	want := "Open integration map (tmp_export_ui_integration_map_2026-06-02.html) Alt+O."
	if !strings.Contains(stripped, want) {
		t.Fatalf("rendered transcript missing resolved artifact hint %q: %q", want, stripped)
	}
}

func TestCodexLinkExtractionIgnoresHiddenDenseCommandOutput(t *testing.T) {
	hiddenOutput := strings.Repeat("[not a markdown link\n", 2000) + "[hidden](https://hidden.example/docs)"
	entry := codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptCommand,
		Text: "$ generate-large-output\n" + hiddenOutput,
	}

	if scanText, ok := codexTranscriptEntryLinkScanText(entry, codexDenseBlockSummary); ok || scanText != "" {
		t.Fatalf("command entries should not participate in link scanning: text=%q ok=%t", scanText, ok)
	}
	if targets := codexOpenTargetsFromTranscriptEntryForBlockMode(entry, codexDenseBlockSummary); len(targets) != 0 {
		t.Fatalf("hidden command links should not be discoverable in summary mode: %#v", targets)
	}
}

func TestCodexProgressiveLinkScanFindsHiddenDenseCommandOutput(t *testing.T) {
	projectPath := "/tmp/demo"
	hiddenOutput := strings.Repeat("[not a markdown link\n", 2000) + "[hidden](https://hidden.example/docs)"
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptCommand,
				Text: "$ generate-large-output\n" + hiddenOutput,
			},
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexViewport:       viewport.New(80, 4),
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	if cmd == nil {
		t.Fatalf("progressive link scan should start for transcript entries")
	}
	got := drainCmdMsgs(m, cmd)
	targets := got.cachedProgressiveCodexOpenTargets(snapshot)
	if len(targets) != 1 {
		t.Fatalf("progressive targets = %#v, want one hidden URL", targets)
	}
	if targets[0].Kind != "url" || targets[0].Path != "https://hidden.example/docs" {
		t.Fatalf("hidden target = %#v, want hidden URL", targets[0])
	}

	updated, cmd := got.openCodexArtifactPicker(snapshot)
	if cmd != nil {
		t.Fatalf("cached hidden target should open picker without a command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("hidden link picker state = %#v, want one progressive target", got.codexArtifactPicker)
	}
}

func TestCodexProgressiveLinkScanDefersPassiveBusyStreamingScan(t *testing.T) {
	projectPath := "/tmp/demo"
	snapshot := codexapp.Snapshot{
		ProjectPath:        projectPath,
		Busy:               true,
		TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptCommand,
				Text: "$ generate-large-output\nOpen [hidden](https://hidden.example/docs).",
			},
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexViewport:       viewport.New(80, 4),
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	if cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot); cmd != nil {
		t.Fatalf("passive busy streaming scan should defer until the transcript settles")
	}
	if cmd := m.maybeStartCodexArtifactLinkScanForPicker(projectPath, snapshot); cmd == nil {
		t.Fatalf("explicit picker scan should still run while the transcript is busy")
	}
}

func TestCodexProgressiveLinkScanResolvesRelativeMarkdownArtifactLinks(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "proj_leaf")
	siblingPath := filepath.Join(root, "proj_leaf_bulk")
	if err := os.MkdirAll(siblingPath, 0o755); err != nil {
		t.Fatalf("create sibling dir: %v", err)
	}
	artifactPath := filepath.Join(siblingPath, "tmp_export_ui_integration_map_2026-06-02.html")
	if err := os.WriteFile(artifactPath, []byte("<!doctype html>\n"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptCommand,
				Text: "$ export-map\nOpen [integration map](../proj_leaf_bulk/tmp_export_ui_integration_map_2026-06-02.html).",
			},
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexViewport:       viewport.New(100, 4),
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	if cmd == nil {
		t.Fatalf("progressive link scan should start for transcript entries")
	}
	got := drainCmdMsgs(m, cmd)
	targets := got.cachedProgressiveCodexOpenTargets(snapshot)
	if len(targets) != 1 {
		t.Fatalf("progressive targets = %#v, want one relative artifact", targets)
	}
	if targets[0].Kind != "html" || targets[0].Path != artifactPath {
		t.Fatalf("relative progressive target = %#v, want html path %q", targets[0], artifactPath)
	}
}

func TestCodexProgressiveLinkScanIgnoresCppLambdaCaptures(t *testing.T) {
	projectPath := t.TempDir()
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptCommand,
				Text: strings.Join([]string{
					"$ sed -n '1,80p' src/App.cpp",
					"auto at = [&](float x, float z, float lift=0.f) { return Vec3{x, lift, z}; };",
					"auto control = [this](DjControlId id, float lift=0.f) { return djControl(id, lift); };",
					"See [notes](docs/plan.md).",
				}, "\n"),
			},
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexViewport:       viewport.New(100, 4),
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	if cmd == nil {
		t.Fatalf("progressive link scan should start for transcript entries")
	}
	got := drainCmdMsgs(m, cmd)
	targets := got.cachedProgressiveCodexOpenTargets(snapshot)
	if len(targets) != 1 {
		t.Fatalf("progressive targets = %#v, want only the real relative markdown link", targets)
	}
	wantPath := filepath.Join(projectPath, "docs", "plan.md")
	if targets[0].Kind != "doc" || targets[0].Label != "notes" || targets[0].Path != wantPath {
		t.Fatalf("progressive target = %#v, want notes doc path %q", targets[0], wantPath)
	}
}

func TestCodexMarkdownLinkParserBoundsMalformedBrackets(t *testing.T) {
	longLabel := "[" + strings.Repeat("x", codexMarkdownLinkLabelScanLimit+1) + "](https://example.com/docs)"
	if _, _, _, ok := parseCodexMarkdownLink(longLabel); ok {
		t.Fatalf("over-limit markdown labels should not parse")
	}

	label, target, consumed, ok := parseCodexMarkdownLink("[docs](https://example.com/docs)")
	if !ok {
		t.Fatalf("valid markdown link did not parse")
	}
	if label != "docs" || target != "https://example.com/docs" || consumed != len("[docs](https://example.com/docs)") {
		t.Fatalf("parsed link = (%q, %q, %d), want docs/example/full length", label, target, consumed)
	}
}

func TestRenderCodexTranscriptEntriesUnwrapsAngleBracketLocalMarkdownLinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [notes](</tmp/lcroom mockups/notes.md>).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/lcroom mockups/notes.md")) {
		t.Fatalf("rendered transcript should not rely on terminal hyperlinks for local markdown artifacts: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	if strings.Contains(rendered, "%3C") || strings.Contains(rendered, "%3E") || strings.Contains(rendered, "%20") {
		t.Fatalf("rendered transcript should not include encoded markdown target angle brackets: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[notes](</tmp/lcroom mockups/notes.md>)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "Open notes (notes.md) Alt+O.") {
		t.Fatalf("rendered transcript should preserve the compact local artifact label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLocalLineSuffixInRawPathHyperlink(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Changed [manager.go](/tmp/demo/manager.go:107).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/manager.go:107")) {
		t.Fatalf("rendered transcript should not use line-suffixed local paths as terminal hyperlink targets: %q", rendered)
	}
	if !strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/manager.go")) {
		t.Fatalf("rendered transcript should use the openable local file as the terminal hyperlink target: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Changed manager.go.") {
		t.Fatalf("rendered transcript should preserve the compact local link label in the visible text: %q", stripped)
	}
}

func TestCodexLinkPickerOpensLineSuffixedLocalMarkdownLinksAsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sourcefile.go")
	if err := os.WriteFile(path, []byte("package demo\n"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Changed [sourcefile.go](" + path + ":232).",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 4),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("link picker state = %#v, want one source target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "source" || target.Path != path {
		t.Fatalf("source target = %#v, want kind source path %q", target, path)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on source link should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("source open command returned nil")
	}
	if opened != path {
		t.Fatalf("source picker opened %q, want clean file path %q", opened, path)
	}
	_ = updated
}

func TestCodexLinkPickerOpensRelativeMarkdownArtifactLinksAgainstProjectPath(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "proj_leaf")
	siblingPath := filepath.Join(root, "proj_leaf_bulk")
	if err := os.MkdirAll(siblingPath, 0o755); err != nil {
		t.Fatalf("create sibling dir: %v", err)
	}
	artifactPath := filepath.Join(siblingPath, "tmp_export_ui_integration_map_2026-06-02.html")
	if err := os.WriteFile(artifactPath, []byte("<!doctype html>\n"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [integration map](../proj_leaf_bulk/tmp_export_ui_integration_map_2026-06-02.html).",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: snapshot,
		},
		codexViewport: viewport.New(140, 4),
	}
	rendered := m.renderAndCacheCodexTranscript(projectPath, snapshot, 140)
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("link picker state = %#v, want one html target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "html" || target.Path != artifactPath {
		t.Fatalf("HTML target = %#v, want kind html path %q", target, artifactPath)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on relative artifact should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("relative artifact open command returned nil")
	}
	if opened != artifactPath {
		t.Fatalf("relative artifact picker opened %q, want %q", opened, artifactPath)
	}
	_ = updated
}

func TestCodexLinkPickerListsVisibleRelativeInlineCodeArtifactPaths(t *testing.T) {
	projectPath := t.TempDir()
	oldRel := "data/bjung/storyboard_openai/debug/scene02_current_order/current_order_contact.png"
	visibleRel := "data/bjung/storyboard_openai/debug/scene02_current_order/interleaved_order_contact.png"
	oldPath := filepath.Join(projectPath, filepath.FromSlash(oldRel))
	visiblePath := filepath.Join(projectPath, filepath.FromSlash(visibleRel))
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Current runtime order:\n`" + oldRel + "`",
			},
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Possible interleaved order:\n`" + visibleRel + "`",
			},
		},
	}

	m := Model{
		codexVisibleProject: projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: snapshot,
		},
		codexViewport: viewport.New(140, 1),
	}
	rendered := m.renderAndCacheCodexTranscript(projectPath, snapshot, 140)
	m.codexViewport.SetContent(rendered)
	m.codexViewport.SetYOffset(3)

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("visible inline-code path picker state = %#v, want exactly one target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "image" || target.Path != visiblePath {
		t.Fatalf("visible inline-code target = %#v, want image path %q", target, visiblePath)
	}
	if target.Path == oldPath {
		t.Fatalf("off-screen inline-code path should not be listed: %#v", target)
	}
}

func TestCodexInlineCodePathScanIgnoresFencesAndBareCodeTokens(t *testing.T) {
	projectPath := t.TempDir()
	rel := "data/bjung/storyboard_openai/debug/scene02_current_order/current_order_contact.png"
	text := strings.Join([]string{
		"Model `gpt-5.4` ran `go test ./...`.",
		"```text",
		"data/bjung/storyboard_openai/debug/scene02_current_order/hidden_order_contact.png",
		"```",
		"Current runtime order: `" + rel + "`",
	}, "\n")

	targets := codexArtifactOpenTargetsFromMarkdownInProject(text, projectPath)
	if len(targets) != 1 {
		t.Fatalf("inline code path targets = %#v, want exactly one real path", targets)
	}
	wantPath := filepath.Join(projectPath, filepath.FromSlash(rel))
	if targets[0].Kind != "image" || targets[0].Path != wantPath {
		t.Fatalf("inline code path target = %#v, want image path %q", targets[0], wantPath)
	}
}

func TestCodexLinkPickerOpensPercentEscapedLocalMarkdownLinksAsFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Family Room", "jun_it_citizenship")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	path := filepath.Join(dir, "Italian B1 certificate.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	encodedPath := strings.ReplaceAll(path, " ", "%20")
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [Italian B1 certificate](" + encodedPath + ").",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(100, 4),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 100)
	if strings.Contains(rendered, "%20") {
		t.Fatalf("rendered transcript should not expose percent-escaped local path spaces: %q", rendered)
	}
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("link picker state = %#v, want one PDF target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "pdf" || target.Path != path {
		t.Fatalf("PDF target = %#v, want kind pdf path %q", target, path)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on PDF link should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("PDF open command returned nil")
	}
	if opened != path {
		t.Fatalf("PDF picker opened %q, want decoded file path %q", opened, path)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesRendersFileURLMarkdownLinksAsRawPathHyperlinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [notes](file://localhost/tmp/demo/notes.txt).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if !strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/notes.txt")) {
		t.Fatalf("rendered transcript should convert local file URLs into raw path hyperlink targets: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not preserve file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Open notes.") {
		t.Fatalf("rendered transcript should preserve the compact local link label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesDoesNotUseTerminalHyperlinksForLocalImageMarkdownLinks(t *testing.T) {
	path := "/tmp/demo/image.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [image](file://localhost/tmp/demo/image.png).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("local image markdown links should not rely on terminal hyperlinks: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) {
		t.Fatalf("rendered transcript should not expose long raw image paths that terminals split: %q", stripped)
	}
	if !strings.Contains(stripped, "Open image (image.png) Alt+O.") {
		t.Fatalf("rendered transcript should show the image label and filename: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesAdvertisesOpenShortcutBesideEachLocalArtifactLink(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "New preview: [boss-cabin-game.png](/tmp/demo/boss-cabin-game.png)\nAll previews: [index.html](/tmp/demo/index.html)",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{
		"New preview: boss-cabin-game.png Alt+O",
		"All previews: index.html Alt+O",
	} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered transcript missing %q: %q", want, stripped)
		}
	}
	if count := strings.Count(stripped, "Alt+O"); count != 2 {
		t.Fatalf("local artifact shortcut hint count = %d, want 2 in transcript: %q", count, stripped)
	}
}

func TestCodexArtifactPickerOpensFolderNamedReadmeLinksAsDirectory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "LittleControlRoom-art-lab")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	readme := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(readme, []byte("# Art lab\n"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Created a sibling art workspace here:\n[LittleControlRoom-art-lab](" + readme + ")",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(readme)) {
		t.Fatalf("folder README links should not rely on terminal hyperlinks: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "README.md") {
		t.Fatalf("folder README link should render as the workspace directory, not the README target: %q", stripped)
	}
	if !strings.Contains(stripped, "LittleControlRoom-art-lab Alt+O") {
		t.Fatalf("folder README link should advertise the artifact picker: %q", stripped)
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("directory artifact should not queue a preview command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("directory artifact picker state = %#v, want one target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "dir" || target.Path != workspace {
		t.Fatalf("directory artifact target = %#v, want kind dir path %q", target, workspace)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "DIR", "LittleControlRoom-art-lab"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("directory artifact picker missing %q: %q", want, overlay)
		}
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on directory artifact should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("directory open command returned nil")
	}
	if opened != workspace {
		t.Fatalf("directory picker opened %q, want %q", opened, workspace)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesRendersGeneratedImagePreview(t *testing.T) {
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	path := "/tmp/demo/generated_images/ig_demo.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + path,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        path,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("generated image block should not rely on terminal hyperlinks for local image opening: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("generated image block should include an ANSI image preview: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) {
		t.Fatalf("generated image block should not expose long raw paths that terminals split: %q", stripped)
	}
	for _, want := range []string{"Generated image", "4x4", "File: ig_demo.png", "Alt+O artifact picker"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("generated image block missing %q: %q", want, stripped)
		}
	}
}

func TestRenderCodexTranscriptEntriesAdvertisesOpenShortcutOnlyOnLatestGeneratedImage(t *testing.T) {
	firstPath := "/tmp/demo/generated_images/first.png"
	secondPath := "/tmp/demo/generated_images/second.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					Path:  firstPath,
					Width: 4,
				},
			},
			{
				Kind: codexapp.TranscriptTool,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					Path:  secondPath,
					Width: 4,
				},
			},
		},
	}

	stripped := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	if strings.Count(stripped, "Alt+O artifact picker") != 1 {
		t.Fatalf("latest artifact shortcut hint count = %d, transcript: %q", strings.Count(stripped, "Alt+O artifact picker"), stripped)
	}
	firstHint := strings.Index(stripped, "File: first.png")
	secondHint := strings.Index(stripped, "File: second.png")
	openHint := strings.Index(stripped, "Alt+O artifact picker")
	if firstHint < 0 || secondHint < 0 || openHint < 0 || openHint < secondHint || openHint < firstHint {
		t.Fatalf("shortcut hint should be attached to the latest generated image: %q", stripped)
	}
}

func TestGeneratedImageOpenActionsUseSystemOpen(t *testing.T) {
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	path := filepath.Join(t.TempDir(), "ig_demo.png")
	if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + path,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        path,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 20),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should show the artifact picker")
	}
	if got.status != "Link picker open" {
		t.Fatalf("status = %q, want picker status", got.status)
	}
	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter from artifact picker should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("artifact picker command returned nil message")
	}
	if opened != path {
		t.Fatalf("artifact picker opened %q, want %q", opened, path)
	}
}

func TestCodexArtifactPickerOpensSelectedImageTargets(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "mockup.png")
	secondPath := filepath.Join(dir, "generated.png")
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [GPT Image Mockup](" + firstPath + ").",
			},
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + secondPath,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        secondPath,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 20),
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should show the artifact picker")
	}
	if got.codexArtifactPicker.Selected != 1 {
		t.Fatalf("picker selected = %d, want latest image index 1", got.codexArtifactPicker.Selected)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "mockup.png", "generated.png", "Enter/Alt+O", "open", "Esc", "close"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("artifact picker missing %q: %q", want, overlay)
		}
	}
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("artifact picker should render the selected generated image preview")
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyUp})
	if cmd == nil {
		t.Fatalf("moving to a path-only image should queue a preview load")
	}
	rawPreviewMsg := cmd()
	previewMsg, ok := rawPreviewMsg.(codexArtifactPreviewMsg)
	if !ok {
		t.Fatalf("preview command returned %T, want codexArtifactPreviewMsg", rawPreviewMsg)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || got.codexArtifactPicker.Selected != 0 {
		t.Fatalf("picker selected after up = %#v, want index 0", got.codexArtifactPicker)
	}
	updated, cmd = got.Update(previewMsg)
	if cmd != nil {
		t.Fatalf("applying preview should not queue a command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("artifact picker should render loaded markdown image preview")
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on selected image should queue an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("artifact open command returned nil message")
	}
	if opened != firstPath {
		t.Fatalf("Enter opened %q, want selected path %q", opened, firstPath)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker != nil {
		t.Fatalf("picker should close after opening an image")
	}
}

func TestCodexArtifactPickerKeepsTranscriptOrderAndRepetitions(t *testing.T) {
	dir := t.TempDir()
	alphaPath := filepath.Join(dir, "alpha.txt")
	betaPath := filepath.Join(dir, "beta.txt")
	for _, path := range []string{alphaPath, betaPath} {
		if err := os.WriteFile(path, []byte("demo\n"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "First [Alpha](" + alphaPath + ").",
			},
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Then [Beta](" + betaPath + ").",
			},
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Updated [Alpha latest](" + alphaPath + ").",
			},
		},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 3 {
		t.Fatalf("artifact picker targets = %#v, want three transcript-order targets", got.codexArtifactPicker)
	}
	targets := got.codexArtifactPicker.Targets
	if targets[0].Path != alphaPath || targets[1].Path != betaPath || targets[2].Path != alphaPath {
		t.Fatalf("target order = [%q, %q, %q], want alpha, beta, alpha by transcript appearance", targets[0].Path, targets[1].Path, targets[2].Path)
	}
	if targets[0].Label != "Alpha" || targets[2].Label != "Alpha latest" {
		t.Fatalf("duplicate labels = [%q, %q], want original and latest labels", targets[0].Label, targets[2].Label)
	}
	if got.codexArtifactPicker.Selected != 2 {
		t.Fatalf("picker selected = %d, want latest target selected", got.codexArtifactPicker.Selected)
	}
}

func TestCodexArtifactPickerFiltersByFuzzyFileName(t *testing.T) {
	dir := t.TempDir()
	alphaPath := filepath.Join(dir, "AlphaComponent.tsx")
	betaPath := filepath.Join(dir, "beta_notes.md")
	for _, path := range []string{alphaPath, betaPath} {
		if err := os.WriteFile(path, []byte("demo\n"), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "First [Alpha source](" + alphaPath + "). Then [Beta notes](" + betaPath + ").",
			},
		},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	for _, r := range []rune("acx") {
		updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			t.Fatalf("typing filter should not queue a command, got %T", cmd)
		}
		got = normalizeUpdateModel(updated)
	}
	if got.codexArtifactPicker == nil || got.codexArtifactPicker.Filter != "acx" {
		t.Fatalf("picker filter = %#v, want acx", got.codexArtifactPicker)
	}
	selected, ok := got.currentCodexArtifactTarget()
	if !ok || selected.Path != alphaPath {
		t.Fatalf("selected filtered target = %#v, want %q", selected, alphaPath)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(100, 24))
	if !strings.Contains(overlay, "AlphaComponent.tsx") || strings.Contains(overlay, "beta_notes.md") {
		t.Fatalf("filtered overlay = %q, want only AlphaComponent.tsx", overlay)
	}
}

func TestCodexArtifactPickerRowsUseFixedColumns(t *testing.T) {
	layout := newCodexArtifactPickerRowLayout(100)
	first := ansi.Strip(renderCodexArtifactPickerRow(codexArtifactOpenTarget{
		Kind:  "doc",
		Label: "short",
		Path:  "/tmp/short.md",
	}, false, 100, layout))
	second := ansi.Strip(renderCodexArtifactPickerRow(codexArtifactOpenTarget{
		Kind:  "html",
		Label: "long",
		Path:  "/tmp/a/much/longer/path/with/a/dashboard.html",
	}, false, 100, layout))

	firstType := strings.Index(first, "DOC")
	secondType := strings.Index(second, "HTML")
	if firstType < 0 || secondType < 0 || firstType != secondType {
		t.Fatalf("type columns not aligned:\n%q\n%q", first, second)
	}
	firstPath := strings.Index(first, "/tmp")
	secondPath := strings.Index(second, "/tmp")
	if firstPath < 0 || secondPath < 0 || firstPath != secondPath {
		t.Fatalf("path columns not aligned:\n%q\n%q", first, second)
	}
}

func TestCodexArtifactPickerOpensContainingFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brief.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [brief](" + path + ").",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should show the artifact picker")
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Alt+F", "folder"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("artifact picker missing containing-folder action %q: %q", want, overlay)
		}
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if cmd == nil {
		t.Fatalf("Alt+F on selected artifact should queue a containing-folder open command")
	}
	rawMsg := cmd()
	openMsg, ok := rawMsg.(browserOpenMsg)
	if !ok {
		t.Fatalf("folder open command returned %T, want browserOpenMsg", rawMsg)
	}
	if openMsg.err != nil {
		t.Fatalf("folder open command error = %v", openMsg.err)
	}
	if openMsg.status != "Opened containing folder" {
		t.Fatalf("folder open status = %q, want success", openMsg.status)
	}
	if opened != dir {
		t.Fatalf("folder picker opened %q, want %q", opened, dir)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker != nil {
		t.Fatalf("picker should close after opening a containing folder")
	}
}

func TestCodexArtifactPickerLoadsPreviewForPathOnlyImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mockup.png")
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [GPT Image Mockup](" + path + ").",
			},
		},
	}
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd == nil {
		t.Fatalf("Alt+O should queue a preview load for a path-only image")
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should open artifact picker")
	}
	if preview := ansi.Strip(got.renderCodexArtifactPicker(80, 24)); !strings.Contains(preview, "Loading preview") {
		t.Fatalf("picker should show loading preview while path image loads: %q", preview)
	}

	rawPreviewMsg := cmd()
	msg, ok := rawPreviewMsg.(codexArtifactPreviewMsg)
	if !ok {
		t.Fatalf("preview command returned %T, want codexArtifactPreviewMsg", rawPreviewMsg)
	}
	updated, cmd = got.Update(msg)
	if cmd != nil {
		t.Fatalf("preview message should not queue command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("picker should render loaded path image preview")
	}
}

func TestCodexArtifactPickerListsPDFMarkdownLinksWithoutPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [brief](" + path + ").",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("PDF artifact should not queue a preview command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("PDF artifact picker state = %#v, want one target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "pdf" || target.Path != path {
		t.Fatalf("PDF artifact target = %#v, want kind pdf path %q", target, path)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "PDF", filepath.Base(path), "Path:"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("PDF artifact picker missing %q: %q", want, overlay)
		}
	}
	if strings.Contains(overlay, "│ Preview") {
		t.Fatalf("PDF artifact picker should not render a preview section: %q", overlay)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on PDF artifact should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("PDF open command returned nil")
	}
	if opened != path {
		t.Fatalf("PDF picker opened %q, want %q", opened, path)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesKeepsHTTPSMarkdownLinksClickable(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [docs](https://example.com/docs).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if !strings.Contains(rendered, ansi.SetHyperlink("https://example.com/docs")) {
		t.Fatalf("rendered transcript should include an https hyperlink escape sequence: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[docs](https://example.com/docs)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "See docs.") {
		t.Fatalf("rendered transcript should preserve the external link label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesRendersMarkdownInsideTableCells(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: strings.Join([]string{
					"| Kind | Output | Notes |",
					"| --- | --- | --- |",
					"| Web | [docs](https://example.com/docs) | **ready** and *polished* with `code` |",
					"| Local | [README](/tmp/demo/README.md) | plain |",
				}, "\n"),
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 160)
	if !strings.Contains(rendered, ansi.SetHyperlink("https://example.com/docs")) {
		t.Fatalf("table cell markdown link should render as a clickable hyperlink: %q", rendered)
	}
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/README.md")) {
		t.Fatalf("local markdown artifact table link should use the artifact hint instead of a terminal hyperlink: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	for _, unwanted := range []string{
		"[docs](https://example.com/docs)",
		"https://example.com/docs",
		"[README](/tmp/demo/README.md)",
		"**ready**",
		"*polished*",
		"`code`",
	} {
		if strings.Contains(stripped, unwanted) {
			t.Fatalf("rendered table should hide markdown syntax %q:\n%s", unwanted, stripped)
		}
	}
	for _, want := range []string{"docs", "README (README.md) Alt+O", "ready", "polished", "code"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, stripped)
		}
	}
}

func TestCodexLinkPickerListsOnlyVisibleTranscriptLinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Hidden [old docs](https://hidden.example/docs).",
			},
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Visible [new docs](https://visible.example/docs).",
			},
		},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 1),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)
	m.codexViewport.SetYOffset(2)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("visible link picker state = %#v, want exactly one visible link", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "url" || target.Path != "https://visible.example/docs" {
		t.Fatalf("visible link target = %#v, want visible URL", target)
	}

	openedURL := ""
	oldBrowserOpener := externalBrowserOpener
	externalBrowserOpener = func(rawURL string) error {
		openedURL = rawURL
		return nil
	}
	t.Cleanup(func() { externalBrowserOpener = oldBrowserOpener })

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on visible URL should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("visible URL open command returned nil")
	}
	if openedURL != "https://visible.example/docs" {
		t.Fatalf("visible URL opened %q, want visible URL", openedURL)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesHighlightsFencedCodeBlocks(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	goSnapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Use this helper:\n```go\nfunc main() {\n    if err != nil {\n        return err\n    }\n}\n```",
		}},
	}
	textSnapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Use this helper:\n```text\nfunc main() {\n    if err != nil {\n        return err\n    }\n}\n```",
		}},
	}

	goRendered := (Model{}).renderCodexTranscriptEntries(goSnapshot, 80)
	textRendered := (Model{}).renderCodexTranscriptEntries(textSnapshot, 80)

	for _, rendered := range []string{goRendered, textRendered} {
		stripped := ansi.Strip(rendered)
		if !strings.Contains(stripped, "func main() {") || !strings.Contains(stripped, "return err") {
			t.Fatalf("fenced-code rendering should preserve the visible code text: %q", stripped)
		}
	}
	if !strings.Contains(goRendered, "\x1b[") {
		t.Fatalf("Go fenced block should include ANSI styling: %q", goRendered)
	}
	if goRendered == textRendered {
		t.Fatalf("language-tagged fenced block should render differently from plain-text fenced block")
	}
}

func TestSourceStyleDimsNonLiveOpenCodeBadge(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	nonLive := sourceStyle("opencode_db", false).Render("OC")
	live := sourceStyle("opencode_db", true).Render("OC")

	if ansi.Strip(nonLive) != "OC" || ansi.Strip(live) != "OC" {
		t.Fatalf("source badge text should stay visible: non-live=%q live=%q", ansi.Strip(nonLive), ansi.Strip(live))
	}
	if nonLive == live {
		t.Fatalf("non-live OpenCode badge should render differently from a live badge: non-live=%q live=%q", nonLive, live)
	}
}

func TestSourceStyleRecognizesLCAgentBadge(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	nonLive := sourceStyle("lcagent_jsonl", false).Render("LA")
	live := sourceStyle("lcagent_jsonl", true).Render("LA")

	if sourceTag("lcagent_jsonl") != "LA" {
		t.Fatalf("sourceTag(lcagent_jsonl) = %q, want LA", sourceTag("lcagent_jsonl"))
	}
	if sourceLabel("lcagent_jsonl") != "LCAgent" {
		t.Fatalf("sourceLabel(lcagent_jsonl) = %q, want LCAgent", sourceLabel("lcagent_jsonl"))
	}
	if ansi.Strip(nonLive) != "LA" || ansi.Strip(live) != "LA" {
		t.Fatalf("source badge text should stay visible: non-live=%q live=%q", ansi.Strip(nonLive), ansi.Strip(live))
	}
	if nonLive == live {
		t.Fatalf("non-live LCAgent badge should render differently from a live badge: non-live=%q live=%q", nonLive, live)
	}
}

func TestVisibleCodexViewHidesSessionApprovalShortcutForFileChanges(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetSafe,
			Status:  "Waiting for file change approval",
			PendingApproval: &codexapp.ApprovalRequest{
				Kind:      codexapp.ApprovalFileChange,
				GrantRoot: "/tmp/demo",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetSafe,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if strings.Contains(rendered, "A session") {
		t.Fatalf("file change approval should not advertise session-wide approval: %q", rendered)
	}
	if !strings.Contains(rendered, "a accept  d decline  c cancel  Alt+Up hide") {
		t.Fatalf("file change approval footer missing expected keys: %q", rendered)
	}
}

func TestVisibleLCAgentCommandApprovalShowsMediumShortcut(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "Waiting for command approval",
			PendingApproval: &codexapp.ApprovalRequest{
				ID:      "approval-1",
				Kind:    codexapp.ApprovalCommandExecution,
				Command: "pnpm install",
				CWD:     "/tmp/demo",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if !strings.Contains(rendered, "A medium") {
		t.Fatalf("LCAgent approval footer should advertise Medium shortcut: %q", rendered)
	}
	if strings.Contains(rendered, "A session") {
		t.Fatalf("LCAgent approval footer should not use vague session label: %q", rendered)
	}
}

func TestPendingOpenCodexApprovalCanBeAcceptedImmediately(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "Waiting for command approval",
			PendingApproval: &codexapp.ApprovalRequest{
				ID:      "approval-1",
				Kind:    codexapp.ApprovalCommandExecution,
				Command: "pnpm run dev",
				CWD:     "/tmp/demo/frontend",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      "/tmp/demo",
			provider:         codexapp.ProviderLCAgent,
			showWhilePending: true,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got := updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want cleared once approval is pending", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Switching LCAgent to Medium for this run..." {
		t.Fatalf("status = %q, want medium switch status", got.status)
	}

	_ = collectCmdMsgs(cmd)
	if len(session.decisions) != 1 || session.decisions[0] != codexapp.DecisionAcceptForSession {
		t.Fatalf("approval decisions = %#v, want acceptForSession", session.decisions)
	}
}

func TestCodexUpdateRevealsPendingOpenApproval(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "Waiting for command approval",
			PendingApproval: &codexapp.ApprovalRequest{
				ID:      "approval-1",
				Kind:    codexapp.ApprovalCommandExecution,
				Command: "pnpm run dev",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      "/tmp/demo",
			provider:         codexapp.ProviderLCAgent,
			showWhilePending: true,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want cleared once update carries approval", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if snapshot, ok := got.currentCodexSnapshot(); !ok || snapshot.PendingApproval == nil {
		t.Fatalf("currentCodexSnapshot() = (%#v, %v), want pending approval", snapshot, ok)
	}
}

func TestCodexUpdateSettlesPendingOpenWhenSessionStarts(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent running",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      "/tmp/demo",
			provider:         codexapp.ProviderLCAgent,
			showWhilePending: true,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want cleared once session has started", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if snapshot, ok := got.currentCodexSnapshot(); !ok || !snapshot.Started {
		t.Fatalf("currentCodexSnapshot() = (%#v, %v), want started session", snapshot, ok)
	}
}

func TestStoreCodexSnapshotOnlyInvalidatesTranscriptRevisionWhenTranscriptChanges(t *testing.T) {
	m := Model{}
	projectPath := "/tmp/demo"
	base := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "First reply",
		}},
	}

	m.storeCodexSnapshot(projectPath, base)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("initial transcript revision = %d, want 1", got)
	}

	statusOnly := base
	statusOnly.Status = "Codex is working..."
	m.storeCodexSnapshot(projectPath, statusOnly)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("status-only update should not bump transcript revision, got %d", got)
	}

	changed := base
	changed.TranscriptRevision = 2
	changed.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Updated reply",
	}}
	m.storeCodexSnapshot(projectPath, changed)
	if got := m.codexTranscriptRevision(projectPath); got != 2 {
		t.Fatalf("transcript update should bump transcript revision, got %d", got)
	}
}

func TestStoreCodexSnapshotIgnoresNoticeOnlyChangesWhenTranscriptHasEntries(t *testing.T) {
	m := Model{}
	projectPath := "/tmp/demo"
	base := codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		TranscriptRevision: 7,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Existing reply",
		}},
	}

	m.storeCodexSnapshot(projectPath, base)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("initial transcript revision = %d, want 1", got)
	}

	noticeOnly := base
	noticeOnly.LastSystemNotice = "Claude Code will use opus, effort high on the next prompt."
	m.storeCodexSnapshot(projectPath, noticeOnly)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("notice-only update with transcript entries should not bump transcript revision, got %d", got)
	}
}

func TestVisibleCodexViewUsesCachedSnapshotWhileTyping(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: "Existing reply",
			}},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("hello")
	input.Focus()

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)

	callsAfterSync := session.snapshotCalls
	if callsAfterSync == 0 {
		t.Fatalf("expected the initial snapshot refresh to read the session")
	}

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Existing reply") {
		t.Fatalf("View() missing transcript content: %q", rendered)
	}
	if session.snapshotCalls != callsAfterSync {
		t.Fatalf("View() should reuse the cached snapshot after sync; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterSync)
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	got := updated.(Model)
	_ = got.View()
	if session.snapshotCalls != callsAfterSync {
		t.Fatalf("typing should not reread the session snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterSync)
	}
	if got.codexInput.Value() != "hello!" {
		t.Fatalf("codex input = %q, want appended text", got.codexInput.Value())
	}
}

func TestRenderCodexViewDoesNotRenderTranscriptOnCacheMiss(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               80,
		height:              20,
	}
	entries := make([]codexapp.TranscriptEntry, 0, codexCacheMissEntryLimit+1)
	for i := 0; i <= codexCacheMissEntryLimit; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("expensive transcript content %02d should not render from View", i),
		})
	}
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Started:  true,
		Entries:  entries,
	})

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "expensive transcript content") {
		t.Fatalf("renderCodexView() should not render transcript content on cache miss: %q", rendered)
	}
	if !strings.Contains(rendered, "Transcript is updating") {
		t.Fatalf("renderCodexView() should show a small cache-miss placeholder: %q", rendered)
	}
}

func TestSyncCodexViewportDefersHeavyTranscriptRender(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               80,
		height:              20,
	}
	entries := make([]codexapp.TranscriptEntry, 0, codexCacheMissEntryLimit+1)
	for i := 0; i <= codexCacheMissEntryLimit; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("deferred transcript content %02d", i),
		})
	}
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Started:  true,
		Entries:  entries,
	})

	m.syncCodexViewport(true)
	rendered := ansi.Strip(m.codexViewport.View())
	if strings.Contains(rendered, "deferred transcript content") {
		t.Fatalf("syncCodexViewport() should not render heavy transcript content synchronously: %q", rendered)
	}
	if !strings.Contains(rendered, "Transcript is updating") {
		t.Fatalf("syncCodexViewport() should show a cache-miss placeholder: %q", rendered)
	}
	if m.codexTranscriptCache.rendered != "" {
		t.Fatalf("transcript cache should not be populated synchronously on a heavy cache miss")
	}

	cmd := m.requestVisibleCodexTranscriptRenderCmd()
	if cmd == nil {
		t.Fatalf("heavy transcript cache miss should queue a deferred render")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatalf("deferred render messages = %#v, want one message", msgs)
	}
	renderMsg, ok := msgs[0].(codexTranscriptRenderedMsg)
	if !ok {
		t.Fatalf("deferred render message = %T, want codexTranscriptRenderedMsg", msgs[0])
	}
	updated, _ := m.applyCodexTranscriptRenderedMsg(renderMsg)
	got := normalizeUpdateModel(updated)
	rendered = ansi.Strip(got.codexViewport.View())
	if !strings.Contains(rendered, "deferred transcript content 24") {
		t.Fatalf("deferred render should populate the viewport with transcript content: %q", rendered)
	}
	if got.codexTranscriptCache.rendered == "" {
		t.Fatalf("deferred render should populate the transcript cache")
	}
}

func TestStreamingTranscriptRenderCoalescesAndAppliesStaleContent(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(80, 20),
		width:               80,
		height:              20,
	}
	first := streamingHeavyTranscriptSnapshot(projectPath, "first streaming transcript content")
	m.storeCodexSnapshot(projectPath, first)
	m.syncCodexViewport(true)
	if rendered := ansi.Strip(m.codexViewport.View()); !strings.Contains(rendered, "Transcript is updating") {
		t.Fatalf("heavy streaming transcript should start with placeholder before deferred render: %q", rendered)
	}
	firstRenderCmd := m.requestVisibleCodexTranscriptRenderCmd()
	if firstRenderCmd == nil {
		t.Fatalf("heavy streaming transcript should queue first deferred render")
	}

	second := streamingHeavyTranscriptSnapshot(projectPath, "second streaming transcript content")
	second.TranscriptRevision = first.TranscriptRevision + 1
	m.storeCodexSnapshot(projectPath, second)
	if nextRenderCmd := m.requestVisibleCodexTranscriptRenderCmd(); nextRenderCmd != nil {
		t.Fatalf("new streaming revision should not queue another render while compatible render is in flight")
	}

	msgs := collectCmdMsgs(firstRenderCmd)
	if len(msgs) != 1 {
		t.Fatalf("first deferred render messages = %#v, want one", msgs)
	}
	renderMsg, ok := msgs[0].(codexTranscriptRenderedMsg)
	if !ok {
		t.Fatalf("first deferred render message = %T, want codexTranscriptRenderedMsg", msgs[0])
	}
	updated, cmd := m.applyCodexTranscriptRenderedMsg(renderMsg)
	if cmd != nil {
		t.Fatalf("busy stale render should apply without immediately queueing another render")
	}
	got := normalizeUpdateModel(updated)
	rendered := ansi.Strip(got.codexViewport.View())
	if strings.Contains(rendered, "Transcript is updating") {
		t.Fatalf("stale streaming render should replace placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "first streaming transcript content 24") {
		t.Fatalf("stale streaming render should show latest completed render content: %q", rendered)
	}
	view := ansi.Strip(got.renderCodexView())
	if strings.Contains(view, "Transcript is updating") {
		t.Fatalf("render path should keep stale streaming content instead of restoring placeholder: %q", view)
	}
}

func TestStreamingTranscriptSyncKeepsStaleSmallViewportContent(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(80, 12),
		width:               80,
		height:              16,
	}
	first := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		Started:            true,
		Busy:               true,
		TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "first streamed text",
		}},
	}
	m.storeCodexSnapshot(projectPath, first)
	m.syncCodexViewport(true)
	if rendered := ansi.Strip(m.codexViewport.View()); !strings.Contains(rendered, "first streamed text") {
		t.Fatalf("initial streaming content should render once: %q", rendered)
	}

	second := first
	second.TranscriptRevision = 2
	second.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "second streamed text",
	}}
	m.storeCodexSnapshot(projectPath, second)
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.codexViewport.View())
	if !strings.Contains(rendered, "first streamed text") {
		t.Fatalf("busy streaming sync should keep stale viewport content until deferred render lands: %q", rendered)
	}
	if strings.Contains(rendered, "second streamed text") {
		t.Fatalf("busy streaming sync should not synchronously replace small transcript content: %q", rendered)
	}
}

func streamingHeavyTranscriptSnapshot(projectPath, prefix string) codexapp.Snapshot {
	entries := make([]codexapp.TranscriptEntry, 0, codexCacheMissEntryLimit+1)
	for i := 0; i <= codexCacheMissEntryLimit; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("%s %02d", prefix, i),
		})
	}
	return codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		Started:            true,
		Busy:               true,
		TranscriptRevision: 1,
		Entries:            entries,
	}
}

func TestRenderCodexTranscriptEntriesLimitsLiveTail(t *testing.T) {
	entries := make([]codexapp.TranscriptEntry, 0, codexTranscriptLiveEntryLimit+24)
	for i := 0; i < codexTranscriptLiveEntryLimit+24; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("old reply %03d", i),
		})
	}
	entries = append(entries, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptAgent,
		Text: "latest reply survives the live-view cap",
	})

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(codexapp.Snapshot{Entries: entries}, 80))
	if !strings.Contains(rendered, "Older transcript hidden from live view") {
		t.Fatalf("rendered transcript should include an omission marker: %q", rendered)
	}
	if strings.Contains(rendered, "old reply 000") {
		t.Fatalf("rendered transcript should omit the oldest entries: %q", rendered)
	}
	if !strings.Contains(rendered, "latest reply survives the live-view cap") {
		t.Fatalf("rendered transcript should keep the latest entry: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesBudgetsDenseCommandByRenderedSummary(t *testing.T) {
	noisyOutput := make([]string, 0, codexTranscriptLiveLineLimit+16)
	noisyOutput = append(noisyOutput, "$ make test")
	for i := 0; i < codexTranscriptLiveLineLimit+15; i++ {
		noisyOutput = append(noisyOutput, fmt.Sprintf("noisy validation line %04d", i))
	}
	entries := []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptAgent, Text: "important design explanation remains visible"},
		{Kind: codexapp.TranscriptCommand, Text: strings.Join(noisyOutput, "\n")},
		{Kind: codexapp.TranscriptAgent, Text: "final closeout"},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(codexapp.Snapshot{Entries: entries}, 80))
	if strings.Contains(rendered, "Older transcript hidden from live view") {
		t.Fatalf("dense command output should not consume the live history budget: %q", rendered)
	}
	if !strings.Contains(rendered, "important design explanation remains visible") {
		t.Fatalf("rendered transcript should keep the earlier explanation: %q", rendered)
	}
}

func TestCodexViewportLoadsFullHistoryWhenScrolledToHiddenMarker(t *testing.T) {
	projectPath := "/tmp/demo"
	entries := make([]codexapp.TranscriptEntry, 0, codexTranscriptLiveEntryLimit+2)
	for i := 0; i < codexTranscriptLiveEntryLimit+2; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("reply %03d", i),
		})
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               80,
		height:              16,
	}
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: projectPath,
		Started:     true,
		Entries:     entries,
	})
	m.syncCodexViewport(true)

	if strings.Contains(ansi.Strip(m.codexViewport.View()), "reply 000") {
		t.Fatalf("tail-limited viewport should not start with the oldest reply")
	}
	m.codexViewport.GotoTop()
	if !m.maybeLoadFullCodexHistoryAtViewportTop() {
		t.Fatal("expected viewport top to load full transcript history")
	}
	if !m.codexTranscriptFullHistoryLoaded(projectPath) {
		t.Fatal("full transcript history should be marked loaded for the project")
	}
	rendered := ansi.Strip(m.codexViewport.View())
	if !strings.Contains(rendered, "reply 000") {
		t.Fatalf("full history viewport should show the oldest reply after expansion: %q", rendered)
	}
}

func TestRenderCodexFooterPrioritizesSendCloseHideAndDefersDenseBlocks(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexFooter(codexapp.Snapshot{
		Started: true,
		Status:  "Codex session ready",
	}, 140))

	enterIndex := strings.Index(rendered, "Enter send")
	closeIndex := strings.Index(rendered, "ctrl+c close")
	hideIndex := strings.Index(rendered, "Alt+Up hide")
	if enterIndex < 0 || closeIndex < 0 || hideIndex < 0 {
		t.Fatalf("renderCodexFooter() missing expected footer actions: %q", rendered)
	}
	if !(enterIndex < closeIndex && closeIndex < hideIndex) {
		t.Fatalf("renderCodexFooter() order = %q, want Enter send before ctrl+c close before Alt+Up hide", rendered)
	}
	if strings.Contains(rendered, "Esc hide") {
		t.Fatalf("renderCodexFooter() should keep Esc as a silent fallback, not advertise it: %q", rendered)
	}
	for _, hidden := range []string{"Alt+Down picker", "Alt+[ prev", "Alt+] next", "Alt+L blocks", "Alt+S sidebar"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("renderCodexFooter() should promote %q out of the footer: %q", hidden, rendered)
		}
	}
}

func TestRenderCodexFooterShowsGoalClearForActiveGoal(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexFooter(codexapp.Snapshot{
		Started: true,
		Status:  "Codex session ready",
		Goal: &codexapp.ThreadGoal{
			Objective: "stay paused",
			Status:    codexapp.ThreadGoalStatusActive,
		},
	}, 180))

	for _, want := range []string{"/goal clear", "stop goal"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexFooter() missing %q for active goal: %q", want, rendered)
		}
	}
}

func TestRenderCodexFooterAnimatesBusyStatus(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	now := time.Date(2026, 3, 13, 15, 4, 5, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Busy:      true,
		BusySince: now.Add(-12 * time.Minute),
		Status:    "Codex is working...",
	}

	base := Model{
		nowFn: func() time.Time { return now },
	}
	renderedA := base.renderCodexFooter(snapshot, 140)
	base.spinnerFrame = 6
	renderedB := base.renderCodexFooter(snapshot, 140)

	if stripped := ansi.Strip(renderedA); !strings.Contains(stripped, "Working 12:00") {
		t.Fatalf("renderCodexFooter() missing busy status text: %q", stripped)
	}
	if ansi.Strip(renderedA) != ansi.Strip(renderedB) {
		t.Fatalf("busy footer should keep the same visible text while animating: %q vs %q", ansi.Strip(renderedA), ansi.Strip(renderedB))
	}
	if renderedA == renderedB {
		t.Fatalf("busy footer should animate across spinner frames")
	}
	if !strings.Contains(renderedA, "\x1b[") {
		t.Fatalf("busy footer should include ANSI styling while active: %q", renderedA)
	}
	statusSegment := strings.SplitN(renderedA, "  ", 2)[0]
	for _, legacy := range []string{"38;5;81", "38;5;117", "38;5;153", "38;5;178", "38;5;214", "38;5;221"} {
		if strings.Contains(statusSegment, legacy) {
			t.Fatalf("busy footer should use the neutral gray ramp instead of legacy colorful code %q: %q", legacy, statusSegment)
		}
	}
}

func TestCodexBusyGradientWrapsContinuously(t *testing.T) {
	phase := codexBusyGradientPhase(17)
	start := codexBusyGradientGrayLevel(0, phase)
	end := codexBusyGradientGrayLevel(1, phase)

	if math.Abs(float64(start-end)) > 0.0001 {
		t.Fatalf("wrapped busy gradient should match at the seam: start=%d end=%d phase=%v", start, end, phase)
	}
}

func TestSpinnerTickKeepsHighResolutionAnimationFrames(t *testing.T) {
	base := Model{spinnerFrame: len(spinnerFrames) - 1}

	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}
	if next.spinnerFrame != len(spinnerFrames) {
		t.Fatalf("spinnerFrame = %d, want %d so gradients are not limited to spinner glyph count", next.spinnerFrame, len(spinnerFrames))
	}
}

func TestSpinnerTickRecordsUIStallLatency(t *testing.T) {
	now := time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC)
	base := Model{
		codexVisibleProject: "/tmp/demo",
		lastSpinnerTickAt:   now,
		nowFn:               func() time.Time { return now },
	}

	now = now.Add(10 * time.Second)
	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}

	found := false
	for _, sample := range next.aiLatencyRecent {
		if sample.Name != "UI stall" {
			continue
		}
		found = true
		if sample.ProjectPath != "/tmp/demo" {
			t.Fatalf("UI stall project = %q, want /tmp/demo", sample.ProjectPath)
		}
		if sample.Duration != 10*time.Second-spinnerTickInterval {
			t.Fatalf("UI stall duration = %v, want %v", sample.Duration, 10*time.Second-spinnerTickInterval)
		}
		if sample.Result != "event loop blocked" {
			t.Fatalf("UI stall result = %q, want event loop blocked", sample.Result)
		}
	}
	if !found {
		t.Fatalf("spinner stall should record a UI stall sample, got %#v", next.aiLatencyRecent)
	}
}

func TestSpinnerTickDoesNotResyncRuntimeViewport(t *testing.T) {
	base := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:        0,
		spinnerFrame:    0,
		runtimeViewport: viewport.New(20, 5),
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"fresh runtime output"},
			},
		},
		width:  100,
		height: 24,
	}
	base.runtimeViewport.SetContent("stale runtime cache")

	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}

	rendered := ansi.Strip(next.runtimeViewport.View())
	if !strings.Contains(rendered, "stale runtime cache") {
		t.Fatalf("spinnerTick should not resync runtime output on the UI thread, got %q", rendered)
	}
	if strings.Contains(rendered, "fresh runtime output") {
		t.Fatalf("spinnerTick should leave runtime viewport refreshes to runtime snapshot updates, got %q", rendered)
	}
}

func TestRenderCodexBannerPromotesLinksAndBlocks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Started:     true,
		Status:      "Codex session ready",
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "See [README](/tmp/demo/README.md).",
		}},
	}
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexViewport:       viewport.New(140, 4),
		width:               140,
	}
	m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 140)
	rendered := ansi.Strip(m.renderCodexBanner(snapshot, 140))

	for _, expected := range []string{"Codex | demo", "Alt+O links", "Alt+L blocks", "Alt+S sidebar"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("renderCodexBanner() missing %q: %q", expected, rendered)
		}
	}
	for _, obsolete := range []string{"Alt+Down picker", "Alt+[ prev", "Alt+] next"} {
		if strings.Contains(rendered, obsolete) {
			t.Fatalf("renderCodexBanner() should omit obsolete shortcut %q: %q", obsolete, rendered)
		}
	}
}
