package tui

import (
	"lcroom/internal/codexapp"
	"strings"
	"testing"
)

func TestCodexErrorBlockPrefersSummaryUntilBlocksAreExpanded(t *testing.T) {
	entry := codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptError,
		Text: strings.Join([]string{
			"Reconnecting... 4/5",
			`codexErrorInfo: {"responseStreamDisconnected":{"httpStatusCode":null}}`,
			`additionalDetails: "stream disconnected before completion: websocket closed by server before response.completed"`,
		}, "\n"),
		DisplayText: "Connection to Codex dropped (response stream disconnected). Retrying, attempt 4 of 5.",
	}

	summary := renderCodexTranscriptEntry(entry, 80, codexDenseBlockSummary)
	if !strings.Contains(summary, "Connection to Codex dropped") || !strings.Contains(summary, "attempt 4 of 5.") {
		t.Fatalf("summary block missing the readable line:\n%s", summary)
	}
	if strings.Contains(summary, "codexErrorInfo") || strings.Contains(summary, "responseStreamDisconnected") {
		t.Fatalf("summary block leaked raw provider diagnostics:\n%s", summary)
	}
	if !strings.Contains(summary, "Alt+L") {
		t.Fatalf("summary block should point at the expanded view:\n%s", summary)
	}

	full := renderCodexTranscriptEntry(entry, 80, codexDenseBlockFull)
	if !strings.Contains(full, "responseStreamDisconnected") || !strings.Contains(full, "stream disconnected before completion") {
		t.Fatalf("expanded block dropped the raw diagnostics:\n%s", full)
	}
}

func TestCodexErrorBlockKeepsPlainErrorsUnchanged(t *testing.T) {
	entry := codexapp.TranscriptEntry{Kind: codexapp.TranscriptError, Text: "Codex exited before the turn completed"}
	block := renderCodexTranscriptEntry(entry, 80, codexDenseBlockSummary)
	if !strings.Contains(block, "Codex exited before the turn completed") {
		t.Fatalf("plain error text changed:\n%s", block)
	}
	if strings.Contains(block, "Alt+L") {
		t.Fatalf("no detail is hidden, so no expand hint belongs here:\n%s", block)
	}
}
