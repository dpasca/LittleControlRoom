package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderCodexTranscriptEntriesRendersEmbeddedStatusCard(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded Codex status",
					"model: gpt-5.4",
					"model provider: openai",
					"reasoning effort: high",
					"service tier: auto",
					"cwd: /tmp/demo",
					"input tokens: 12345",
					"cached input tokens: 2000",
					"reasoning tokens: 123",
					"output tokens: 6789",
					"model context window: 200000",
					"context tokens: 12000",
					"context used percent: 6",
					"last turn tokens: 4321",
					"usage window: limit=Codex; plan=Pro; window=5h; left=85; resetsAt=1773027840",
					"usage window: limit=Codex; plan=Pro; window=weekly; left=88; resetsAt=1773200640",
					"usage window: limit=GPT-5.3-Codex-Spark; window=5h; left=100; resetsAt=1773027840",
				}, "\n"),
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	checks := []string{
		"Status",
		"Model:",
		"gpt-5.4",
		"Reasoning:",
		"high",
		"Usage left",
		"Codex (Pro)",
		"5h limit",
		"85% left",
		"Weekly limit",
		"88% left",
		"Context:",
		"12,000 tokens",
		"Tokens:",
		"i12k c16% r123 o6.8k",
		"Last turn:",
		"4,321 tokens",
		"resets ",
	}
	for _, want := range checks {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered embedded status card should include %q: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersOpenCodeStatusCard(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded OpenCode status",
					"model: gpt-5.4",
					"model provider: openai",
					"reasoning effort: high",
					"agent: build",
					"cwd: /tmp/demo",
					"total tokens: 12345",
					"last turn tokens: 4321",
				}, "\n"),
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	for _, want := range []string{"Status", "Model:", "gpt-5.4", "Reasoning:", "high", "Agent:", "build", "Last turn:", "4,321 tokens"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered OpenCode status card should include %q: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesCollapsesLCAgentStatusByDefault(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded LCAgent status",
					"status: running",
					"route preset: default",
					"model: gpt-5.4",
					"reasoning effort: medium",
				}, "\n"),
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	if !strings.Contains(rendered, "Embedded LCAgent status is hidden") {
		t.Fatalf("rendered transcript should show collapsed LCAgent status by default: %q", rendered)
	}
	if strings.Contains(rendered, "status: running") {
		t.Fatalf("collapsed transcript should not include raw LCAgent status lines by default: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesShowsLCAgentStatusWhenVisible(t *testing.T) {
	projectPath := "/tmp/demo"
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded LCAgent status",
					"status: running",
					"route preset: default",
					"permissions: medium",
					"model: gpt-5.4",
					"reasoning effort: medium",
				}, "\n"),
			},
		},
	}
	m := Model{codexLCAgentStatusVisible: map[string]struct{}{projectPath: {}}}

	rendered := ansi.Strip(m.renderCodexTranscriptEntries(snapshot, 80))
	if strings.Contains(rendered, lcAgentStatusCollapsedText) {
		t.Fatalf("rendered transcript should show full LCAgent status when enabled: %q", rendered)
	}
	if !strings.Contains(rendered, "Route:") || !strings.Contains(rendered, "default") {
		t.Fatalf("rendered LCAgent status should include route preset details: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesSuppressesLCAgentBoilerplateStatusByDefault(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "which one would you suggest?"},
			{Kind: codexapp.TranscriptStatus, Text: "Continuing LCAgent thread lct_2b918f1c9d245c160500a0e2."},
			{Kind: codexapp.TranscriptStatus, Text: "Continuing LCAgent from lct_2b918f1c9d245c160500a0e2 [depth 1; reason continue_from; handoff assistant_message; exact replay 100 messages]"},
			{Kind: codexapp.TranscriptStatus, Text: "Loaded exact LCAgent context from lct_2b918f1c9d245c160500a0e2 [depth 1; handoff assistant_message; 100 replay messages]"},
			{Kind: codexapp.TranscriptStatus, Text: "LCAgent web search enabled: exa"},
			{Kind: codexapp.TranscriptStatus, Text: "LCAgent oversized search refinement enabled: deepseek deepseek-v4-flash"},
			{Kind: codexapp.TranscriptAgent, Text: "I would choose the simple option."},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 100))
	for _, hidden := range []string{
		"Continuing LCAgent",
		"Loaded exact LCAgent context",
		"LCAgent web search enabled",
		"LCAgent oversized search refinement enabled",
	} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("rendered transcript should suppress %q by default: %q", hidden, rendered)
		}
	}
	for _, want := range []string{"which one would you suggest?", "I would choose the simple option."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript missing %q: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesShowsLCAgentBoilerplateStatusWhenVisible(t *testing.T) {
	projectPath := "/tmp/demo"
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptStatus, Text: "Continuing LCAgent from lct_2b918f1c9d245c160500a0e2 [depth 1; exact replay 100 messages]"},
			{Kind: codexapp.TranscriptStatus, Text: "LCAgent web search enabled: exa"},
		},
	}
	m := Model{codexLCAgentStatusVisible: map[string]struct{}{projectPath: {}}}

	rendered := ansi.Strip(m.renderCodexTranscriptEntries(snapshot, 100))
	for _, want := range []string{"Continuing LCAgent from lct_2b918f1c9d245c160500a0e2", "LCAgent web search enabled: exa"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript should show %q when status is visible: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersGenericLabelsInline(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptStatus, Text: "Installing dependencies is blocked at low autonomy."},
			{Kind: codexapp.TranscriptError, Text: "Permission denied: corepack enable requires medium autonomy."},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 100))
	for _, want := range []string{
		"Status Installing dependencies is blocked at low autonomy.",
		"Error Permission denied: corepack enable requires medium autonomy.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered generic label should include inline %q:\n%s", want, rendered)
		}
	}
	for _, notWant := range []string{"Status\nInstalling", "Error\nPermission"} {
		if strings.Contains(rendered, notWant) {
			t.Fatalf("rendered generic label should not reserve a label line %q:\n%s", notWant, rendered)
		}
	}
}
