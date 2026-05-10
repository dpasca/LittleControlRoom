package lcagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

func TestDetectorParsesSessionLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	project := t.TempDir()
	sessionDir := filepath.Join(dataDir, "lcagent", "sessions", "2026", "05", "09")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "lca_demo.jsonl")
	content := `{"type":"session_meta","id":"lca_demo","started_at":"2026-05-09T01:00:00Z","cwd":"` + filepath.ToSlash(project) + `","auto":"low","model":"scripted"}
{"type":"user_message","event_id":"evt_1","timestamp":"2026-05-09T01:00:01Z","session_id":"lca_demo","message":"hi"}
{"type":"tool_result","event_id":"evt_2","timestamp":"2026-05-09T01:00:02Z","session_id":"lca_demo","tool":"run_command","result":{"success":false,"error":"boom"}}
{"type":"turn_complete","event_id":"evt_3","timestamp":"2026-05-09T01:00:03Z","session_id":"lca_demo","summary":"done"}
`
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := New(dataDir).Detect(context.Background(), scanner.NewPathScope([]string{project}, nil))
	if err != nil {
		t.Fatal(err)
	}
	entry := results[project]
	if entry == nil {
		t.Fatalf("missing project entry, results=%#v", results)
	}
	if len(entry.Sessions) != 1 {
		t.Fatalf("sessions len = %d", len(entry.Sessions))
	}
	session := entry.Sessions[0]
	if session.Source != model.SessionSourceLCAgent {
		t.Fatalf("source = %q", session.Source)
	}
	if session.SessionID != "lcagent:lca_demo" || session.RawSessionID != "lca_demo" {
		t.Fatalf("session identity = %q raw %q", session.SessionID, session.RawSessionID)
	}
	if !session.LatestTurnStateKnown || !session.LatestTurnCompleted {
		t.Fatalf("turn state known=%v completed=%v", session.LatestTurnStateKnown, session.LatestTurnCompleted)
	}
	if session.ErrorCount != 1 || entry.ErrorCount != 1 {
		t.Fatalf("error counts session=%d entry=%d", session.ErrorCount, entry.ErrorCount)
	}
	if len(entry.Artifacts) != 1 || entry.Artifacts[0].Kind != "lcagent_session_jsonl" {
		t.Fatalf("artifacts = %#v", entry.Artifacts)
	}
}

func TestDetectorIgnoresOutOfScopeSessions(t *testing.T) {
	dataDir := t.TempDir()
	project := t.TempDir()
	other := t.TempDir()
	sessionDir := filepath.Join(dataDir, "lcagent", "sessions", "2026", "05", "09")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "lca_other.jsonl"), []byte(`{"type":"session_meta","id":"lca_other","started_at":"2026-05-09T01:00:00Z","cwd":"`+filepath.ToSlash(other)+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := New(dataDir).Detect(context.Background(), scanner.NewPathScope([]string{project}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want empty", results)
	}
}
