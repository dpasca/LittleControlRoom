package codexapp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Codex reports each retry of a dropped response stream as its own error that
// repeats the same raw provider diagnostics, so an ordinary turn can end up
// with several near-identical JSON blocks in the transcript. A summary keeps
// the operator-facing line short, and the family key lets consecutive attempts
// of one incident share a single transcript entry.
type codexErrorSummary struct {
	Display string // operator-facing text; empty when the raw text already reads well
	Family  string // stable key shared by retries of the same failure
	Attempt int
	Total   int
}

type codexRetryErrorState struct {
	Family  string
	Text    string
	Attempt int
	Total   int
}

// The attempt counter has to sit right after the word on the same line: a
// greedy gap would let an unrelated "3/5" elsewhere in a multi-line message
// masquerade as a retry and fold two different failures together.
var codexReconnectAttemptPattern = regexp.MustCompile(`(?i)\breconnecting\b[^\d\n]{0,8}([0-9]+)\s*/\s*([0-9]+)`)

// summarizeCodexError converts a raw Codex diagnostic into a short line for the
// transcript. The raw text stays the entry's Text so the full-block view and
// exported transcripts keep the provider diagnostics intact.
func summarizeCodexError(raw string) codexErrorSummary {
	message, fields := splitCodexErrorDiagnostics(raw)
	if message == "" {
		return codexErrorSummary{}
	}
	cause := humanizeCodexErrorInfoKey(codexErrorInfoKey(fields["codexErrorInfo"]))
	if match := codexReconnectAttemptPattern.FindStringSubmatch(message); match != nil {
		attempt, _ := strconv.Atoi(match[1])
		total, _ := strconv.Atoi(match[2])
		family := "reconnect"
		if cause != "" {
			family += "|" + cause
		}
		return codexErrorSummary{
			Display: codexRetryDisplayText(cause, attempt, total),
			Family:  family,
			Attempt: attempt,
			Total:   total,
		}
	}
	if len(fields) == 0 {
		// Nothing was hidden, so rendering Text directly stays accurate.
		return codexErrorSummary{}
	}
	return codexErrorSummary{Display: message}
}

func codexRetryDisplayText(cause string, attempt, total int) string {
	switch {
	case attempt > 0 && total > 0 && attempt >= total:
		return codexRetryIncidentPrefix(cause) + fmt.Sprintf(" Last retry attempt (%d of %d).", attempt, total)
	case attempt > 0 && total > 0:
		return codexRetryIncidentPrefix(cause) + fmt.Sprintf(" Retrying, attempt %d of %d.", attempt, total)
	default:
		return codexRetryIncidentPrefix(cause) + " Retrying."
	}
}

func codexRecoveredRetryDisplayText(state codexRetryErrorState) string {
	cause := strings.TrimPrefix(state.Family, "reconnect")
	cause = strings.TrimPrefix(cause, "|")
	prefix := codexRetryIncidentPrefix(cause)
	switch {
	case state.Attempt > 1:
		return prefix + fmt.Sprintf(" Recovered after %d attempts.", state.Attempt)
	case state.Attempt == 1:
		return prefix + " Recovered after 1 attempt."
	default:
		return prefix + " Recovered."
	}
}

func codexRetryIncidentPrefix(cause string) string {
	if cause == "" {
		return "Connection to Codex dropped."
	}
	return "Connection to Codex dropped (" + cause + ")."
}

// splitCodexErrorDiagnostics reverses diagnosticText: the leading human message
// is separated from the structured provider fields appended after it.
func splitCodexErrorDiagnostics(raw string) (string, map[string]string) {
	fields := make(map[string]string, 2)
	messageLines := make([]string, 0, 2)
	for _, line := range strings.Split(raw, "\n") {
		name, value, isField := strings.Cut(line, ": ")
		if isField && (name == "codexErrorInfo" || name == "additionalDetails") {
			fields[name] = strings.TrimSpace(value)
			continue
		}
		messageLines = append(messageLines, line)
	}
	return strings.TrimSpace(strings.Join(messageLines, "\n")), fields
}

// codexErrorInfoKey reduces the provider's error-info payload to a stable tag.
// The payload is either an object keyed by failure kind or a bare string.
func codexErrorInfoKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err == nil {
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return strings.Join(keys, "+")
	}
	var text string
	if err := json.Unmarshal([]byte(trimmed), &text); err == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

// humanizeCodexErrorInfoKey turns a provider tag such as
// "responseStreamDisconnected" into "response stream disconnected" without
// hard-coding a vocabulary that upstream keeps extending.
func humanizeCodexErrorInfoKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	runes := []rune(key)
	var out strings.Builder
	out.Grow(len(runes) + 8)
	for index, r := range runes {
		if r == '_' || r == '-' || r == '+' {
			out.WriteRune(' ')
			continue
		}
		if unicode.IsUpper(r) && index > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if !unicode.IsUpper(previous) || nextIsLower {
				out.WriteRune(' ')
			}
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return strings.Join(strings.Fields(out.String()), " ")
}
