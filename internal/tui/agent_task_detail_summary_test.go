package tui

import (
	"errors"
	"strings"
	"testing"

	"lcroom/internal/model"
	"lcroom/internal/service"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderAgentTaskDetailCondensesLongSummary(t *testing.T) {
	cleanup := staleWorktreeRecoveryError{Result: staleWorktreeCleanupResult{
		Candidate: service.StaleWorktreeCleanupCandidate{
			ProjectPath:     "/tmp/repo--lane",
			RootProjectPath: "/tmp/repo",
			Branch:          "feature/lane",
			ParentBranch:    "master",
		},
		Err: errors.New("unlink /tmp/repo--lane/vendor/.git: directory not empty"),
	}}
	task := model.AgentTask{
		ID:            "task-1",
		Title:         "Resolve cleanup blocker for repo--lane",
		WorkspacePath: "/tmp/tasks/task-1",
		Summary:       staleWorktreeRecoveryPrompt(cleanup),
	}

	m := Model{}
	rendered := ansi.Strip(m.renderAgentTaskDetailContent(task, 110))
	summaryLines := 0
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "Summary:") || (summaryLines > 0 && strings.HasPrefix(line, "         ")) {
			summaryLines++
			continue
		}
		if summaryLines > 0 {
			break
		}
	}
	if summaryLines == 0 {
		t.Fatalf("renderAgentTaskDetailContent() rendered no summary: %q", rendered)
	}
	if summaryLines > agentTaskDetailSummaryMaxLines {
		t.Fatalf("summary occupies %d lines, want <= %d: %q", summaryLines, agentTaskDetailSummaryMaxLines, rendered)
	}
	if !strings.Contains(rendered, "Resolve the blocker that stopped") {
		t.Fatalf("renderAgentTaskDetailContent() dropped the opening summary sentence: %q", rendered)
	}
	if strings.Contains(rendered, "refs-only Git bundle") {
		t.Fatalf("renderAgentTaskDetailContent() kept the full engineer prompt: %q", rendered)
	}
	for _, want := range []string{"Path:", "Kind:", "Status:", "Task ID:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderAgentTaskDetailContent() missing %q after the summary: %q", want, rendered)
		}
	}
}

func TestAgentTaskDetailSummaryKeepsShortSummary(t *testing.T) {
	short := "Nested repository may contain work that isn't backed up."
	if got := agentTaskDetailSummary(short); got != short {
		t.Fatalf("agentTaskDetailSummary(%q) = %q, want unchanged", short, got)
	}
	if got := agentTaskDetailSummary("   "); got != "" {
		t.Fatalf("agentTaskDetailSummary(blank) = %q, want empty", got)
	}
}

func TestAgentTaskDetailSummaryTruncatesSingleLongSentence(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("engineer kept working on the blocker ", 40))
	got := agentTaskDetailSummary(long)
	// compactEngineerNoticeText cuts just under the limit, then appends "...".
	if len(got) > agentTaskDetailSummaryCharLimit+3 {
		t.Fatalf("agentTaskDetailSummary() length = %d, want <= %d: %q", len(got), agentTaskDetailSummaryCharLimit+3, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("agentTaskDetailSummary() = %q, want a truncation marker", got)
	}
}
