package sessionclassify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestExtractClaudeCodeTranscriptHidesGeneratedLocalCommandRecords(t *testing.T) {
	sessionFile := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"previous-answer","message":{"role":"assistant","content":[{"type":"text","text":"Choose one of the open items."}]}}`,
		`{"type":"user","isMeta":true,"uuid":"command-caveat","parentUuid":"previous-answer","message":{"role":"user","content":"<local-command-caveat>generated records follow</local-command-caveat>"}}`,
		`{"type":"user","uuid":"command-name","parentUuid":"command-caveat","message":{"role":"user","content":"<command-name>/model</command-name>"}}`,
		`{"type":"user","uuid":"command-output","parentUuid":"command-name","message":{"role":"user","content":"<local-command-stdout>Set model to Opus 5</local-command-stdout>"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"escaped-prompt","parentUuid":"command-output","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"continued-prompt","parentUuid":"command-output","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"assistant","uuid":"answer","parentUuid":"continued-prompt","message":{"role":"assistant","content":[{"type":"text","text":"Config plumbing is the most important."}]}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	items, err := extractClaudeCodeTranscript(context.Background(), sessionFile)
	if err != nil {
		t.Fatalf("extractClaudeCodeTranscript() error = %v", err)
	}
	if got, want := len(items), 3; got != want {
		t.Fatalf("transcript item count = %d, want %d: %#v", got, want, items)
	}
	for _, item := range items {
		if strings.Contains(item.Text, "<command-") || strings.Contains(item.Text, "<local-command-") {
			t.Fatalf("generated local command XML leaked into classifier transcript: %#v", item)
		}
	}
	if items[1].Role != "user" || items[1].Text != "which one you think is more improtant at this point" {
		t.Fatalf("user prompt item = %#v", items[1])
	}
}

func TestExtractSnapshotClaudeCodeRecoversRecentConversationBeforeImage(t *testing.T) {
	t.Parallel()

	imageResult := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}}`, strings.Repeat("A", 2*codexTailBytes))
	for _, leadingImage := range []bool{false, true} {
		t.Run(fmt.Sprintf("leading_image=%t", leadingImage), func(t *testing.T) {
			lines := []string{}
			if leadingImage {
				lines = append(lines, imageResult)
			}
			for i := 0; i < maxTranscriptItems; i++ {
				lines = append(lines,
					fmt.Sprintf(`{"type":"user","promptSource":"sdk","message":{"content":"Previous request %d"}}`, i),
					fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":"Previous response %d"}]}}`, i),
				)
			}
			lines = append(lines,
				`{"type":"user","promptSource":"sdk","message":{"content":"Inspect the latest screenshot."}}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"Checking the updated cockpit."}]}}`,
				`{"type":"user","isMeta":true,"uuid":"command","message":{"content":"Generated command context"}}`,
				`{"type":"user","uuid":"command-output","parentUuid":"command","message":{"content":"Generated command output"}}`,
				`{"type":"user","isCompactSummary":true,"message":{"content":"Generated compact summary"}}`,
				imageResult,
				`{"type":"attachment"}`,
				`{"type":"last-prompt"}`,
			)
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			classification := model.SessionClassification{
				SessionID:     "image-session",
				SessionFormat: "claude_code",
				SessionFile:   path,
			}
			snapshot, err := ExtractSnapshot(context.Background(), classification, model.SessionEvidence{}, GitStatusSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			items := snapshot.Transcript
			if len(items) != maxTranscriptItems {
				t.Fatalf("recovered %d items, want %d", len(items), maxTranscriptItems)
			}
			if items[len(items)-2].Text != "Inspect the latest screenshot." || items[len(items)-1].Text != "Checking the updated cockpit." {
				t.Fatalf("did not retain the most recent conversation: %#v", items)
			}
			for _, item := range items {
				if strings.Contains(item.Text, "Generated") || strings.Contains(item.Text, "AAAA") {
					t.Fatalf("non-conversational record leaked: %#v", item)
				}
			}

			preview, err := ExtractPreview(context.Background(), model.SessionEvidence{Format: "claude_code", SessionFile: path})
			if err != nil || preview.Summary != "Checking the updated cockpit." {
				t.Fatalf("preview = %#v, error = %v", preview, err)
			}

			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"The new layout is verified."}]}}` + "\n")
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("append assistant update: %v, close: %v", writeErr, closeErr)
			}
			snapshot, err = ExtractSnapshot(context.Background(), classification, model.SessionEvidence{}, GitStatusSnapshot{})
			if err != nil || len(snapshot.Transcript) != 1 || snapshot.Transcript[0].Text != "The new layout is verified." {
				t.Fatalf("new tail conversation = %#v, error = %v", snapshot.Transcript, err)
			}
		})
	}
}

func TestExtractClaudeCodeTranscriptWithoutConversation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := `{"type":"user","isMeta":true,"message":{"content":"Startup settings"}}` + "\n" +
		`{"type":"user","isCompactSummary":true,"message":{"content":"Generated context"}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"Tool output"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractSnapshot(context.Background(), model.SessionClassification{
		SessionFormat: "claude_code",
		SessionFile:   path,
	}, model.SessionEvidence{}, GitStatusSnapshot{})
	if err == nil || err.Error() != "no conversational transcript found" {
		t.Fatalf("error = %v, want missing conversation", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := extractClaudeCodeTranscript(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
}

func TestExtractCodexTurnLifecycleEventTreatsTurnAbortedAsCompleted(t *testing.T) {
	line := `{"timestamp":"2026-03-05T09:04:12.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`
	event, ok := extractCodexTurnLifecycleEvent(line)
	if !ok || !event.completed {
		t.Fatalf("extractCodexTurnLifecycleEvent(turn_aborted) = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("turn_aborted timestamp = zero, want parsed timestamp")
	}
}

func TestDetectCodexTurnLifecycleTreatsTurnAbortedAsCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:00.238Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"turn_aborted\",\"reason\":\"interrupted\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectCodexTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectCodexTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectCodexTurnLifecycle() = %#v, want known=true completed=true", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero for completed/aborted turn", state.startedAt)
	}
}

func TestDetectCodexTurnLifecycleIgnoresControlOnlyTaskStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.001Z\",\"type\":\"turn_context\",\"payload\":{\"turn_id\":\"turn_control\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.002Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"model switch\"}]}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.003Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectCodexTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectCodexTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectCodexTurnLifecycle() = %#v, want latest stable completed state", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero after control-only task start is ignored", state.startedAt)
	}
}

func TestDetectLCAgentTurnLifecycleTreatsTurnAbortedAsCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lcagent.jsonl")
	content := "" +
		"{\"type\":\"user_message\",\"timestamp\":\"2026-06-13T06:20:00Z\",\"message\":\"build the game\"}\n" +
		"{\"type\":\"turn_aborted\",\"timestamp\":\"2026-06-13T06:21:00Z\",\"reason\":\"interrupted\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectLCAgentTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectLCAgentTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectLCAgentTurnLifecycle() = %#v, want known=true completed=true", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero for aborted turn", state.startedAt)
	}
}

func TestRecoverSessionTurnStateRefreshesStaleLCAgentEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lcagent.jsonl")
	userAt := time.Date(2026, 6, 13, 6, 20, 0, 0, time.UTC)
	content := "" +
		"{\"type\":\"user_message\",\"timestamp\":\"" + userAt.Format(time.RFC3339Nano) + "\",\"message\":\"build the game\"}\n" +
		"{\"type\":\"turn_aborted\",\"timestamp\":\"2026-06-13T06:21:00Z\",\"reason\":\"interrupted\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := model.SessionEvidence{
		SessionFile:          path,
		Format:               "lcagent_jsonl",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		LatestTurnStartedAt:  userAt,
	}
	if err := RecoverSessionTurnState(&session); err != nil {
		t.Fatalf("RecoverSessionTurnState() error = %v", err)
	}
	if !session.LatestTurnStateKnown || !session.LatestTurnCompleted {
		t.Fatalf("recovered state known=%v completed=%v, want settled aborted turn", session.LatestTurnStateKnown, session.LatestTurnCompleted)
	}
	if !session.LatestTurnStartedAt.IsZero() {
		t.Fatalf("LatestTurnStartedAt = %v, want zero after abort", session.LatestTurnStartedAt)
	}
}
