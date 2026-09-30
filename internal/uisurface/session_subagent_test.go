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
