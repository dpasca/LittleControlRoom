package gitops

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCodexCommitMessageClientLive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LCROOM_RUN_LIVE_CODEX_HELPER_TEST")) == "" {
		t.Skip("set LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 to run against a real local Codex install")
	}

	client := NewCodexCommitMessageClientWithUsageTracker(nil)
	if client == nil {
		t.Fatalf("NewCodexCommitMessageClientWithUsageTracker() = nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	input := CommitMessageInput{
		Intent:      "commit",
		ProjectName: "LittleControlRoom",
		StageMode:   "staged",
		IncludedFiles: []string{
			"internal/tui/app.go",
		},
		DiffStat: " 1 file changed, 10 insertions(+), 2 deletions(-)",
		Patch: `diff --git a/internal/tui/app.go b/internal/tui/app.go
index abcdef0..1234567 100644
--- a/internal/tui/app.go
+++ b/internal/tui/app.go
@@ -100,6 +100,14 @@
 func renderBanner() string {
-	return "Loading"
+	return "Generating commit message..."
 }`,
	}

	raw, err := client.runJSONSchemaPrompt(
		ctx,
		commitMessageInstructions,
		"Draft a git commit subject for this coding task snapshot:\n\n"+`{
  "intent": "commit",
  "project_name": "LittleControlRoom",
  "stage_mode": "staged",
  "included_files": ["internal/tui/app.go"],
  "diff_stat": " 1 file changed, 10 insertions(+), 2 deletions(-)",
  "patch": "diff --git a/internal/tui/app.go b/internal/tui/app.go\nindex abcdef0..1234567 100644\n--- a/internal/tui/app.go\n+++ b/internal/tui/app.go\n@@ -100,6 +100,14 @@\n func renderBanner() string {\n-    return \\\"Loading\\\"\n+    return \\\"Generating commit message...\\\"\n }"
}`,
		"git_commit_message",
		map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"message": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"message"},
		},
	)
	if err != nil {
		t.Fatalf("client.runJSONSchemaPrompt() error = %v", err)
	}
	t.Logf("raw output: %q", raw.OutputText)

	suggestion, err := client.Suggest(ctx, input)
	if err != nil {
		t.Fatalf("client.Suggest() error = %v", err)
	}
	if strings.TrimSpace(suggestion.Message) == "" {
		t.Fatalf("client.Suggest() returned empty message")
	}
	t.Logf("commit suggestion: %s", suggestion.Message)
	t.Logf("model: %s", suggestion.Model)
}
