package sessionclassify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"lcroom/internal/llm"
)

func TestCodexClassifierClientLive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LCROOM_RUN_LIVE_CODEX_HELPER_TEST")) == "" {
		t.Skip("set LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 to run against a real local Codex install")
	}

	client := NewCodexClientWithUsageTracker(nil)
	if client == nil {
		t.Fatalf("NewCodexClientWithUsageTracker() = nil")
	}

	snapshot := SessionSnapshot{
		ProjectPath:          "/tmp/demo",
		SessionID:            "ses_demo",
		SessionFormat:        "modern",
		LastEventAt:          time.Now().UTC().Format(time.RFC3339),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		GitStatus: GitStatusSnapshot{
			WorktreeDirty: true,
			RemoteStatus:  "ahead",
			AheadCount:    1,
		},
		Transcript: []TranscriptItem{
			{Role: "user", Text: "Please make the commit dialog open immediately while the message is generating."},
			{Role: "assistant", Text: "Implemented the immediate-open commit dialog and I’m running tests now."},
			{Role: "assistant", Text: "Everything passed and the commit dialog now shows loading state right away."},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	raw, err := client.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           client.ModelName(),
		SystemText:      sessionClassificationInstructions,
		UserText:        "Classify this latest coding-session snapshot:\n\n" + `{"project_path":"/tmp/demo","session_id":"ses_demo","session_format":"modern","latest_turn_state_known":true,"latest_turn_completed":true,"git_status":{"worktree_dirty":true,"remote_status":"ahead","ahead_count":1},"transcript":[{"role":"user","text":"Please make the commit dialog open immediately while the message is generating."},{"role":"assistant","text":"Implemented the immediate-open commit dialog and I’m running tests now."},{"role":"assistant","text":"Everything passed and the commit dialog now shows loading state right away."}]}`,
		SchemaName:      "session_state_classification",
		Schema:          sessionClassificationSchema(),
		ReasoningEffort: classifierPrimaryReasoningEffort,
	})
	if err != nil {
		t.Fatalf("responsesClient().RunJSONSchema() error = %v", err)
	}
	t.Logf("raw output: %q", raw.OutputText)

	result, err := client.Classify(ctx, snapshot)
	if err != nil {
		t.Fatalf("client.Classify() error = %v", err)
	}
	if strings.TrimSpace(result.Summary) == "" {
		t.Fatalf("client.Classify() returned empty summary")
	}
	if strings.TrimSpace(string(result.Category)) == "" {
		t.Fatalf("client.Classify() returned empty category")
	}
	t.Logf("category: %s", result.Category)
	t.Logf("summary: %s", result.Summary)
	t.Logf("model: %s", result.Model)
}
