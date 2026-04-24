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
					"total tokens: 12345",
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
