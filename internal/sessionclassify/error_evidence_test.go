package sessionclassify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/model"
)

func TestSnapshotPreservesProviderFailureAfterPromiseToContinue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := `{"type":"event_msg","payload":{"type":"user_message","message":"Convert to particles."}}
{"type":"event_msg","payload":{"type":"agent_message","message":"I am continuing with the particle implementation."}}
`
	classification := model.SessionClassification{ProjectPath: "/project", SessionID: "session", SessionFile: path, SessionFormat: "modern"}
	write := func(body string) SessionSnapshot {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		snapshot, err := ExtractSnapshot(context.Background(), classification, model.SessionEvidence{}, GitStatusSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := write(body)
	after := write(body + `{"type":"event_msg","payload":{"type":"error","message":"Bad Request","codex_error_info":"other"}}
{"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}
`)
	if len(after.Transcript) != 4 || after.Transcript[2].Role != "error" || !strings.Contains(after.Transcript[2].Text, "Bad Request") || after.Transcript[3].Role != "status" {
		t.Fatalf("failure evidence lost: %+v", after.Transcript)
	}
	if SnapshotHashForSnapshot(before) == SnapshotHashForSnapshot(after) {
		t.Fatal("provider failure must invalidate the earlier follow-up assessment")
	}
	if !after.LatestTurnStateKnown || !after.LatestTurnCompleted {
		t.Fatal("aborted turn must be settled, not still working")
	}
}
