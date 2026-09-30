package server

import (
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
)

func TestMobileStreamUpdatesWhenOnlyChildActivityChanges(t *testing.T) {
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true,
		Subagents: []claudeartifact.SubagentProgress{{ID: "worker", Description: "Phone profiling", LatestAction: "Bash: Measure phone", UpdatedAt: time.Now()}},
	}
	before := buildMobileLiveStreamRevision(snapshot, false)
	if before != buildMobileLiveStreamRevision(snapshot, false) {
		t.Fatal("unchanged activity should not stream again")
	}
	snapshot.Subagents[0].LatestAction = "Bash: Analyze results"
	if before == buildMobileLiveStreamRevision(snapshot, false) {
		t.Fatal("quiet parent suppressed child update")
	}
	before = buildMobileLiveStreamRevision(snapshot, false)
	snapshot.Subagents[0].Completed = true
	if before == buildMobileLiveStreamRevision(snapshot, false) {
		t.Fatal("child completion did not stream")
	}
}

func TestMobileStreamUpdatesWhenMessageIsAccepted(t *testing.T) {
	s := codexapp.Snapshot{MessageDeliveries: []codexapp.MessageDeliverySnapshot{{ID: "message", State: "queued", SubmittedAt: time.Now()}}}
	before := buildMobileLiveStreamRevision(s, true)
	s.MessageDeliveries[0].State = "started"
	if before == buildMobileLiveStreamRevision(s, true) {
		t.Fatal("delivery acknowledgment did not reach mobile")
	}
}
