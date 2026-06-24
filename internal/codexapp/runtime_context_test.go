package codexapp

import (
	"strings"
	"testing"

	"lcroom/internal/projectrun"
)

func TestAugmentSubmissionWithRuntimeContextKeepsDisplayTextClean(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.StartManaged(projectrun.StartRequest{
		ProjectPath:   dir,
		Command:       "sleep 30",
		Name:          "dev-server",
		CreateNew:     true,
		ReuseMatching: true,
	})
	if err != nil {
		t.Fatalf("StartManaged() error = %v", err)
	}

	got := augmentSubmissionWithRuntimeContext(Submission{Text: "open the app"}, manager, dir)
	if !strings.Contains(got.Text, "Little Control Room runtime context") {
		t.Fatalf("augmented text missing runtime context:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, `command "sleep 30"`) {
		t.Fatalf("augmented text missing managed command:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "User request:\nopen the app") {
		t.Fatalf("augmented text missing original request:\n%s", got.Text)
	}
	if got.TranscriptDisplayText() != "open the app" {
		t.Fatalf("TranscriptDisplayText() = %q, want original user text", got.TranscriptDisplayText())
	}
}
