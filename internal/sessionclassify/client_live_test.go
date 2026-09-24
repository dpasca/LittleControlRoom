package sessionclassify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"lcroom/internal/llm"
	"lcroom/internal/model"
)

func TestCodexClassifierPostMergeFollowupsLive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LCROOM_RUN_LIVE_CODEX_HELPER_TEST")) == "" {
		t.Skip("set LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 to test post-merge assessment semantics")
	}
	client := NewCodexClientWithUsageTrackerInDataDir(t.TempDir(), nil)
	for _, tc := range []struct {
		name, request, handoff string
		publication            WorktreePublicationStatus
		want                   model.SessionCategory
	}{
		{"merge_done", "Implement the fix and merge it.", "Implementation and tests are complete. Please commit and merge the work into master.", WorktreePublicationUnknown, model.SessionCategoryCompleted},
		{"push_done", "Implement the fix, merge and push master.", "Implementation and tests are complete. Please commit, merge into master, and push master.", WorktreePublicationPublished, model.SessionCategoryCompleted},
		{"push_pending", "Implement the fix, merge and push master.", "Implementation and tests are complete. Please commit, merge into master, and push master.", WorktreePublicationPending, model.SessionCategoryNeedsFollowUp},
		{"push_unknown", "Implement the fix, merge and push master.", "Implementation and tests are complete. Please commit, merge into master, and push master.", WorktreePublicationUnknown, model.SessionCategoryNeedsFollowUp},
		{"restart_pending", "Implement the fix, merge it, and rebuild and restart LCR for me.", "Fix validated. Commit and merge the changes, then rebuild and restart LCR to activate the fix.", WorktreePublicationPublished, model.SessionCategoryNeedsFollowUp},
		{"verification_pending", "Implement the controls, merge and push, then launch the game and manually verify them.", "Changes and automated tests are complete. Merge and push, then launch the game and manually verify the new controls.", WorktreePublicationPublished, model.SessionCategoryNeedsFollowUp},
		{"restart_reminder", "Fix session selection and run the tests.", "Session-selection fix implemented; tests and TUI smoke pass. Rebuild and restart LCR to activate the fix; the running app has not been updated.", WorktreePublicationPublished, model.SessionCategoryCompleted},
		{"visual_check_reminder", "Improve the Start TODO dialog's model and reasoning display.", "Full model and reasoning now shown per row; automated tests pass. Still needs a live make tui visual check after rebuild/restart.", WorktreePublicationPublished, model.SessionCategoryCompleted},
		{"optional_polish", "Implement the activity picker.", "Activity picker implemented, tests pass. If you want, I can add search next. Shall I do that?", WorktreePublicationPublished, model.SessionCategoryCompleted},
		{"known_defect", "Implement the activity picker and update its shortcut labels.", "Picker implemented and tests pass, but block titles still incorrectly say Alt+L expands. Those labels still need fixing.", WorktreePublicationPublished, model.SessionCategoryNeedsFollowUp},
		{"verification_failed", "Fix the layout gap.", "Fix merged and tests pass, but live TUI verification still reproduces the gap. The layout needs another correction.", WorktreePublicationPublished, model.SessionCategoryNeedsFollowUp},
		{"required_approval", "Implement the fix and deploy it after I approve the production release.", "Fix implemented and tests pass. Ready for production deployment; please approve before I deploy.", WorktreePublicationPublished, model.SessionCategoryWaitingForUser},
		{"provider_blocker", "Fix the model selector and restore AI worktree naming.", "Model selector fixed and tested, but AI worktree naming remains blocked by exhausted API credits.", WorktreePublicationPublished, model.SessionCategoryBlocked},
		{"source_push_pending", "Implement the fix, merge and publish master, and push this feature branch too.", "Implementation and tests complete. Master is published; the separate feature-branch push still needs doing.", WorktreePublicationPublished, model.SessionCategoryNeedsFollowUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			result, err := client.Classify(ctx, SessionSnapshot{
				ProjectPath: "/tmp/post-merge-eval", SessionID: "post-merge-eval", SessionFormat: "modern",
				LatestTurnStateKnown: true, LatestTurnCompleted: true,
				GitStatus: GitStatusSnapshot{RemoteStatus: "no_upstream", Integration: &WorktreeIntegrationSnapshot{
					TargetBranch: "master", MergeStatus: model.WorktreeMergeStatusMerged,
					PublicationStatus: tc.publication,
				}},
				Transcript: []TranscriptItem{
					{Role: "user", Text: tc.request},
					{Role: "assistant", Text: tc.handoff},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %s", result.Category, result.Summary)
			if result.Category != tc.want {
				t.Fatalf("category = %s, want %s", result.Category, tc.want)
			}
		})
	}
}

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
