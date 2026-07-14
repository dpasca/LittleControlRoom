package codexapp

import (
	"strings"
	"testing"
)

func TestActivityPreviewKeepsRecentSummaryEntriesBounded(t *testing.T) {
	entries := []TranscriptEntry{
		{Kind: TranscriptSystem, Text: "Started a new embedded Codex session."},
		{Kind: TranscriptUser, Text: "Fix the lazy summary."},
		{Kind: TranscriptTool, Text: "large tool output"},
		{Kind: TranscriptAgent, Text: "The updated summary is already available."},
		{Kind: TranscriptStatus, Text: strings.Repeat("x", maxActivityPreviewEntryBytes+100)},
	}

	preview := activityPreviewFromEntries(entries)
	if len(preview) != 3 {
		t.Fatalf("activity preview entries = %#v, want system, agent, and status", preview)
	}
	if preview[0].Kind != TranscriptSystem || preview[1].Kind != TranscriptAgent || preview[2].Kind != TranscriptStatus {
		t.Fatalf("activity preview order/kinds = %#v", preview)
	}
	if got := len(preview[2].Text); got > maxActivityPreviewEntryBytes {
		t.Fatalf("bounded activity text bytes = %d, want <= %d", got, maxActivityPreviewEntryBytes)
	}
}

func TestStateSnapshotCarriesActivityPreviewWithoutFullTranscript(t *testing.T) {
	session := &appServerSession{
		started: true,
		entries: []transcriptEntry{
			{Kind: TranscriptSystem, Text: "Started a new embedded Codex session."},
			{Kind: TranscriptAgent, Text: "The main row can show this immediately."},
		},
	}

	snapshot := session.stateSnapshotLocked()
	if len(snapshot.Entries) != 0 || snapshot.Transcript != "" {
		t.Fatalf("lightweight snapshot copied full transcript: entries=%#v transcript=%q", snapshot.Entries, snapshot.Transcript)
	}
	if len(snapshot.ActivityPreview) != 2 || snapshot.ActivityPreview[1].Text != "The main row can show this immediately." {
		t.Fatalf("activity preview = %#v", snapshot.ActivityPreview)
	}
}
