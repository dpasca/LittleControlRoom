package codexapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const reconnectDiagnosticDetails = `"codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":null}},"additionalDetails":"stream disconnected before completion: websocket closed by server before response.completed"`

func reconnectErrorNotification(attempt, total int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"error":{"message":"Reconnecting... %d/%d",%s}}`, attempt, total, reconnectDiagnosticDetails))
}

func TestSummarizeCodexErrorHidesProviderDiagnostics(t *testing.T) {
	raw := strings.Join([]string{
		"Reconnecting... 2/5",
		`codexErrorInfo: {"responseStreamDisconnected":{"httpStatusCode":null}}`,
		`additionalDetails: "stream disconnected before completion: websocket closed by server before response.completed"`,
	}, "\n")
	summary := summarizeCodexError(raw)
	if want := "Connection to Codex dropped (response stream disconnected). Retrying, attempt 2 of 5."; summary.Display != want {
		t.Fatalf("display = %q, want %q", summary.Display, want)
	}
	if summary.Family != "reconnect|response stream disconnected" {
		t.Fatalf("family = %q, want a stable reconnect family", summary.Family)
	}
	if summary.Attempt != 2 || summary.Total != 5 {
		t.Fatalf("attempt = %d/%d, want 2/5", summary.Attempt, summary.Total)
	}
	if label := codexRetryStatusLabel(raw); label != "Codex reconnecting (2/5)" {
		t.Fatalf("status label = %q, want a compact reconnect label", label)
	}

	last := summarizeCodexError(strings.Replace(raw, "2/5", "5/5", 1))
	if want := "Connection to Codex dropped (response stream disconnected). Last retry attempt (5 of 5)."; last.Display != want {
		t.Fatalf("final attempt display = %q, want %q", last.Display, want)
	}
	if last.Family != summary.Family {
		t.Fatalf("family changed between attempts: %q vs %q", last.Family, summary.Family)
	}
}

func TestSummarizeCodexErrorLeavesPlainMessagesAlone(t *testing.T) {
	if got := summarizeCodexError("Codex exited before the turn completed"); got.Display != "" || got.Family != "" {
		t.Fatalf("plain message should render as-is, got %#v", got)
	}
	raw := "Bad Request\ncodexErrorInfo: \"BadRequest\"\nadditionalDetails: \"unsupported image detail\""
	summary := summarizeCodexError(raw)
	if summary.Display != "Bad Request" {
		t.Fatalf("display = %q, want the human message without the raw payload", summary.Display)
	}
	if summary.Family != "" {
		t.Fatalf("family = %q, want no folding for a one-off failure", summary.Family)
	}
	if label := codexRetryStatusLabel(raw); label != "" {
		t.Fatalf("status label = %q, want no reconnect label", label)
	}
}

func TestHumanizeCodexErrorInfoKey(t *testing.T) {
	for key, want := range map[string]string{
		"responseStreamDisconnected": "response stream disconnected",
		"httpConnectionFailed":       "http connection failed",
		"BadRequest":                 "bad request",
		"usage_limit_reached":        "usage limit reached",
		"":                           "",
	} {
		if got := humanizeCodexErrorInfoKey(key); got != want {
			t.Fatalf("humanize(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestCodexReconnectAttemptsShareOneTranscriptEntry(t *testing.T) {
	s := &appServerSession{notify: func() {}}
	for attempt := 2; attempt <= 4; attempt++ {
		s.handleNotification("error", reconnectErrorNotification(attempt, 5))
	}
	errorEntries := make([]transcriptEntry, 0, 4)
	for _, entry := range s.entries {
		if entry.Kind == TranscriptError {
			errorEntries = append(errorEntries, entry)
		}
	}
	if len(errorEntries) != 1 {
		t.Fatalf("error entries = %d, want the retries folded into one line", len(errorEntries))
	}
	entry := errorEntries[0]
	if !strings.Contains(entry.Text, "Reconnecting... 4/5") || !strings.Contains(entry.Text, "responseStreamDisconnected") {
		t.Fatalf("raw diagnostics lost from the folded entry: %q", entry.Text)
	}
	if want := "Connection to Codex dropped (response stream disconnected). Retrying, attempt 4 of 5."; entry.DisplayText != want {
		t.Fatalf("display = %q, want %q", entry.DisplayText, want)
	}
	if s.status != "Codex reconnecting (4/5)" {
		t.Fatalf("status = %q, want a compact reconnect label", s.status)
	}

	s.handleNotification("error", json.RawMessage(`{"error":{"message":"Bad Request","codexErrorInfo":"BadRequest","additionalDetails":"unsupported image detail"}}`))
	if got := len(s.entries); got != 2 {
		t.Fatalf("entries = %d, want an unrelated failure to stay separate", got)
	}
}

func TestCodexReconnectEntryResolvesWhenTheTurnFinishes(t *testing.T) {
	s := &appServerSession{notify: func() {}}
	s.handleNotification("error", reconnectErrorNotification(3, 5))
	s.handleNotification("turn/completed", json.RawMessage(`{"turn":{"id":"turn-1","status":"completed"}}`))
	if len(s.entries) != 1 {
		t.Fatalf("entries = %d, want the single reconnect line", len(s.entries))
	}
	if want := "Connection to Codex dropped (response stream disconnected). Recovered after 3 attempts."; s.entries[0].DisplayText != want {
		t.Fatalf("display = %q, want %q", s.entries[0].DisplayText, want)
	}
	if !strings.Contains(s.entries[0].Text, "Reconnecting... 3/5") {
		t.Fatalf("raw diagnostics lost after recovery: %q", s.entries[0].Text)
	}

	failed := &appServerSession{notify: func() {}}
	failed.handleNotification("error", reconnectErrorNotification(5, 5))
	failed.handleNotification("turn/completed", json.RawMessage(`{"turn":{"id":"turn-2","status":"failed"}}`))
	if strings.Contains(failed.entries[0].DisplayText, "Recovered") {
		t.Fatalf("a failed turn must not claim recovery: %q", failed.entries[0].DisplayText)
	}
}

func TestSummarizeCodexErrorIgnoresDistantAttemptCounters(t *testing.T) {
	raw := "Reconnecting embedded session failed\nthe provider rejected 3/5 of the queued items\ncodexErrorInfo: {\"httpConnectionFailed\":{\"httpStatusCode\":500}}"
	if got := summarizeCodexError(raw); got.Family != "" {
		t.Fatalf("family = %q, want no retry folding for an unrelated counter", got.Family)
	}
}
