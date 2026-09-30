package uisurface

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
)

func TestMobileSessionSummaryUsesChildActivityWhenParentIsQuiet(t *testing.T) {
	now := time.Now()
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true,
		Entries:   []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "Starting phone profiling"}},
		Subagents: []claudeartifact.SubagentProgress{{ID: "worker", Description: "Phone profiling", LatestAction: "Bash: Compare graphics tiers", UpdatedAt: now}},
	}
	item := BuildLiveEngineerSession(snapshot, now)
	if !strings.Contains(item.Summary, "1 active") || !strings.Contains(item.Summary, "Compare graphics tiers") {
		t.Fatalf("mobile hid child progress: %#v", item)
	}
	item = BuildLiveEngineerSession(snapshot, now.Add(time.Hour))
	if !strings.Contains(item.Summary, "without recent activity") {
		t.Fatalf("mobile hid silence: %#v", item)
	}
}

func TestMobileSessionSurfacesDelayedInputIndependentlyOfChildActivity(t *testing.T) {
	now := time.Now()
	s := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, LastActivityAt: now,
		ParentStatus: "Parent working", ParentActivityAt: now.Add(-time.Hour),
		Subagents:         []claudeartifact.SubagentProgress{{ID: "worker", UpdatedAt: now}},
		MessageDeliveries: []codexapp.MessageDeliverySnapshot{{ID: "message", State: "queued", SubmittedAt: now.Add(-3 * time.Minute)}},
	}
	view := BuildLiveEngineerSessionDetail(s, now)
	if !strings.Contains(view.Session.Summary, "parent has not started") {
		t.Fatalf("missing input delay: %s", view.Session.Summary)
	}
	found := false
	for _, field := range view.Instruments {
		if field.Label == "Message delivery" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing delivery instrument")
	}
}
