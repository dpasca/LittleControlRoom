package lcagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
)

func TestParseResumeContextFileReadsLargeModelContextSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.jsonl")
	started := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	largeContent := strings.Repeat("large context line\n", 300000)
	messages := []modeladapter.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: largeContent},
		{Role: "assistant", Content: "done"},
	}
	events := []map[string]any{
		{
			"type":       "session_meta",
			"id":         "lca_large_snapshot",
			"cwd":        dir,
			"started_at": started.Format(time.RFC3339Nano),
		},
		{
			"type":          modelContextSnapshotType,
			"session_id":    "lca_large_snapshot",
			"timestamp":     started.Add(time.Second).Format(time.RFC3339Nano),
			"source":        "final_response",
			"message_count": len(messages),
			"approx_chars":  messagesApproxChars(messages),
			"messages":      messages,
		},
	}
	var data strings.Builder
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(data.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := parseResumeContextFile(path)
	if err != nil {
		t.Fatalf("parseResumeContextFile() error = %v", err)
	}
	if !ctx.hasExactMessages() {
		t.Fatal("hasExactMessages() = false, want true")
	}
	if ctx.ExactMessageCount != len(messages) {
		t.Fatalf("ExactMessageCount = %d, want %d", ctx.ExactMessageCount, len(messages))
	}
	if got := ctx.ExactMessages[1].Content; got != largeContent {
		t.Fatalf("large message length = %d, want %d", len(got), len(largeContent))
	}
}

func TestParseResumeContextFileCorrectsLegacyVerificationStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-status.jsonl")
	events := []map[string]any{
		{
			"type":       "session_meta",
			"id":         "lca_legacy_status",
			"cwd":        dir,
			"started_at": time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		},
		{
			"type":                "turn_complete",
			"summary":             "old final summary claimed checks passed",
			"files_changed":       []string{"frontend/src/pages/index.tsx"},
			"verification":        []string{"pnpm run lint passed", "pnpm run build passed"},
			"verification_status": "failed",
			"actual_checks": []map[string]any{
				{"command": "pnpm run lint", "status": "failed", "success": false},
				{"command": "pnpm run build", "status": "failed", "success": false},
				{"command": "cd " + filepath.Join(dir, "frontend") + " && pwd && ls package.json && pnpm run lint", "status": "passed", "success": true},
				{"command": "cd " + filepath.Join(dir, "frontend") + " && pnpm run build", "status": "passed", "success": true},
			},
		},
	}
	var data strings.Builder
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(data.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, err := parseResumeContextFile(path)
	if err != nil {
		t.Fatalf("parseResumeContextFile() error = %v", err)
	}
	if ctx.VerificationStatus != "verified" {
		t.Fatalf("VerificationStatus = %q, want verified", ctx.VerificationStatus)
	}
	if pending := ctx.pendingVerificationStatus(); pending != "" {
		t.Fatalf("pendingVerificationStatus() = %q, want empty", pending)
	}
}
