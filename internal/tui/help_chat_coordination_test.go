package tui

import (
	"strings"
	"testing"
)

func TestEngineerCompletionNoticeUsesWorkAttribution(t *testing.T) {
	got := bossEngineerCompletionNotice("Project Task", "The checks passed.")
	if want := "Work on Project Task is ready for review: The checks passed."; got != want {
		t.Fatalf("completion notice = %q, want %q", got, want)
	}
	for _, unwanted := range []string{"Engineer", "Evelyn", " is back from "} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("completion notice retained agent attribution %q: %q", unwanted, got)
		}
	}
}

func TestEngineerCompletionNoticeWithoutSummaryIsPassive(t *testing.T) {
	got := bossEngineerCompletionNotice("Project Task", "")
	if want := "Work on Project Task is ready for review."; got != want {
		t.Fatalf("completion notice = %q, want %q", got, want)
	}
}

func TestHelpChatHandoffCarriesOnlyWorkLabel(t *testing.T) {
	handoff := bossEngineerCompletionHandoff("Project Task")
	if handoff == nil || handoff.ProjectLabel != "Project Task" {
		t.Fatalf("handoff = %#v, want project label only", handoff)
	}
}
