package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestClaudeParentPaneShowsChildProgressWithoutSidebar(t *testing.T) {
	now := time.Now()
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, Phase: codexapp.SessionPhaseRunning,
		Subagents: []claudeartifact.SubagentProgress{
			{ID: "perf", Description: "Phone profiling", LatestAction: "Bash: Compare graphics tiers", UpdatedAt: now.Add(-time.Minute)},
			{ID: "icon", Description: "Icon review", Completed: true, UpdatedAt: now.Add(-4 * time.Hour)},
		},
	}
	m := Model{codexInput: newCodexTextarea()}
	for _, width := range []int{60, 120} {
		text := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, width), "\n"))
		for _, want := range []string{"1 active", "1 completed", "Phone profiling", "Icon review", "Compare graphics tiers", "ago"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d missing %q:\n%s", width, want, text)
			}
		}
		for _, line := range m.renderCodexSubagentProgress(snapshot, width) {
			if ansi.StringWidth(line) > width {
				t.Fatalf("panel overflows at width %d: %q", width, line)
			}
		}
	}
	if footer := codexFooterStatus(snapshot, now); footer != "Working · 1 subagent active" {
		t.Fatalf("footer hides children or the parent's working state: %q", footer)
	}
	snapshot.Subagents[0].UpdatedAt = now.Add(-time.Hour)
	text := ansi.Strip(strings.Join(m.renderCodexSubagentProgress(snapshot, 100), "\n"))
	if !strings.Contains(text, "no recent activity") || strings.Contains(text, "1 active") {
		t.Fatalf("silent child still claims active: %s", text)
	}
	snapshot.PendingApproval = &codexapp.ApprovalRequest{}
	if codexFooterStatus(snapshot, now) != "Waiting for approval" {
		t.Fatal("child progress hid approval")
	}
	snapshot.SubagentProgressError = "Subagent activity unavailable: cannot read logs"
	text = ansi.Strip(strings.Join(m.renderCodexSubagentProgress(snapshot, 100), "\n"))
	if !strings.Contains(text, "unavailable") {
		t.Fatalf("hidden failure: %s", text)
	}
}

func TestClaudeParentPaneCollapsesCompletedChildren(t *testing.T) {
	now := time.Now()
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, Phase: codexapp.SessionPhaseRunning,
		BusySince: now.Add(-3 * time.Hour),
		Subagents: []claudeartifact.SubagentProgress{
			{ID: "a", Description: "Draft crew manual corpus", AgentType: "general-purpose", LatestAction: "SubagentHandback", Completed: true, UpdatedAt: now.Add(-5 * time.Hour)},
			{ID: "b", Description: "Survey voice verification harness", LatestAction: "SubagentHandback", Completed: true, UpdatedAt: now.Add(-6 * time.Hour)},
			{ID: "c", Description: "Survey intercom strip", Completed: true, UpdatedAt: now.Add(-6 * time.Hour)},
			{ID: "d", Description: "Survey palette catalog", Completed: true, UpdatedAt: now.Add(-6 * time.Hour)},
		},
	}
	m := Model{codexInput: newCodexTextarea(), nowFn: func() time.Time { return now }}
	rows := m.renderCodexSubagentProgress(snapshot, 80)
	text := ansi.Strip(strings.Join(rows, "\n"))
	if len(rows) != 1 || !strings.Contains(text, "4 completed: Draft crew manual corpus") || !strings.Contains(text, "+") || strings.Contains(text, "SubagentHandback") {
		t.Fatalf("finished children should collapse to one receipt line:\n%s", text)
	}
	if footer := codexFooterStatus(snapshot, now); footer != "Working 3:00:00" {
		t.Fatalf("finished children replaced the working timer: %q", footer)
	}

	snapshot.Subagents[3] = claudeartifact.SubagentProgress{ID: "d", Description: "Survey palette catalog", AgentType: "Explore", LatestAction: "Read: src/menu.cpp", UpdatedAt: now.Add(-30 * time.Second)}
	text = ansi.Strip(strings.Join(m.renderCodexSubagentProgress(snapshot, 120), "\n"))
	for _, want := range []string{"Subagents: 1 active · 3 completed", "● Survey palette catalog (Explore) · Read: src/menu.cpp · 30s ago", "✓ 3 completed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if footer := codexFooterStatus(snapshot, now); footer != "Working 3:00:00 · 1 subagent active" {
		t.Fatalf("footer = %q", footer)
	}
}

func TestDashboardShowsRunningSubagentsForLiveSession(t *testing.T) {
	now := time.Now()
	project := model.ProjectSummary{Path: "/tmp/demo", Name: "demo", PresentOnDisk: true}
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Started: true, Busy: true, Phase: codexapp.SessionPhaseRunning,
		BusySince: now.Add(-time.Hour),
		Entries:   []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "Launched two surveys in parallel."}},
		Subagents: []claudeartifact.SubagentProgress{
			{ID: "a", Description: "Survey palette catalog", AgentType: "Explore", LatestAction: "Bash: Grep menu ids", UpdatedAt: now.Add(-time.Minute)},
			{ID: "b", Description: "Draft crew manual corpus", Completed: true, UpdatedAt: now.Add(-time.Hour)},
		},
	}
	m := Model{
		projects:                     []model.ProjectSummary{project},
		renderCachedSessionStateOnly: true,
		nowFn:                        func() time.Time { return now },
		codexSnapshots:               map[string]codexapp.Snapshot{project.Path: snapshot},
	}
	summary, ok := m.projectLiveEngineerAssessmentSummary(project, now)
	if !ok || !strings.HasPrefix(summary, "1 subagent active · Launched two surveys") {
		t.Fatalf("dashboard row hides running subagents: %q", summary)
	}
	detail := strings.Join(strings.Fields(ansi.Strip(renderProjectDetailSurface(m.buildProjectDetailSurface(project, model.ProjectDetail{}), 120))), " ")
	for _, want := range []string{"Subagents: 1 subagent active · 1 completed", "Survey palette catalog (Explore) · Bash: Grep menu ids · 1m ago"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail pane missing %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, "Draft crew manual corpus") {
		t.Fatalf("detail pane lists completed children individually:\n%s", detail)
	}

	snapshot.Subagents[0].Completed = true
	m.codexSnapshots[project.Path] = snapshot
	if summary, _ := m.projectLiveEngineerAssessmentSummary(project, now); strings.Contains(summary, "subagent") {
		t.Fatalf("finished children still read as running: %q", summary)
	}
	snapshot.Subagents[0].Completed = false
	snapshot.PendingApproval = &codexapp.ApprovalRequest{}
	m.codexSnapshots[project.Path] = snapshot
	if summary, _ := m.projectLiveEngineerAssessmentSummary(project, now); !strings.HasPrefix(summary, "Waiting for approval") {
		t.Fatalf("subagent count hid a pending approval: %q", summary)
	}
}

func TestOverlaySnapshotCopiesChildProgress(t *testing.T) {
	state := codexapp.Snapshot{Subagents: []claudeartifact.SubagentProgress{{ID: "child", Description: "Task"}}, SubagentProgressError: "read failed"}
	cached := overlayCodexSnapshotState(codexapp.Snapshot{}, state)
	state.Subagents[0].Description = "changed"
	if cached.Subagents[0].Description != "Task" || cached.SubagentProgressError != "read failed" {
		t.Fatalf("overlay lost or aliased activity: %#v", cached)
	}
}
