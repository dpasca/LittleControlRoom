package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
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

func TestRenderAgentTaskDetailShowsCorrectionBoundary(t *testing.T) {
	rejected := model.AgentTask{
		ID:            "task-2",
		Title:         "Delegated fix",
		WorkspacePath: "/tmp/tasks/task-2",
		Repository:    model.AgentTaskRepository{Write: true, Root: "/tmp/repo", State: "released", HandoffFingerprint: "abc123"},
		Workflow: model.AgentTaskWorkflow{
			Enabled: true,
			RunID:   2,
			Phase:   "changes_requested",
			Review:  &model.AgentTaskReview{Revision: 2, Decision: "changes_requested", Summary: "Missing the failing case"},
		},
	}
	m := Model{}
	rendered := ansi.Strip(m.renderAgentTaskDetailContent(rejected, 110))
	if !strings.Contains(rendered, "Correction baseline:") || !strings.Contains(rendered, "reviewed revision 2") {
		t.Fatalf("rejected result hid its correction boundary: %q", rendered)
	}

	captured := rejected
	captured.Repository.CorrectionBaseline, captured.Repository.CorrectionRevision = "def456", 2
	rendered = ansi.Strip(m.renderAgentTaskDetailContent(captured, 110))
	if !strings.Contains(rendered, "captured for revision 2") {
		t.Fatalf("captured boundary not shown: %q", rendered)
	}

	// An accepted revision authorizes no correction, so it must claim none.
	accepted := rejected
	accepted.Workflow.Phase = "completed"
	accepted.Workflow.Review = &model.AgentTaskReview{Revision: 2, Decision: "accept", Summary: "Verified"}
	if rendered = ansi.Strip(m.renderAgentTaskDetailContent(accepted, 110)); strings.Contains(rendered, "Correction baseline:") {
		t.Fatalf("accepted result advertised a correction boundary: %q", rendered)
	}
}

func rejectedCaptureTask() model.AgentTask {
	return model.AgentTask{
		ID:            "task-3",
		Title:         "Delegated fix",
		WorkspacePath: "/tmp/tasks/task-3",
		Kind:          model.AgentTaskKindAgent,
		Repository:    model.AgentTaskRepository{Write: true, Root: "/tmp/repo", State: "blocked", Error: "checkout changed since revision 1 was reviewed"},
		Workflow: model.AgentTaskWorkflow{
			Enabled: true,
			RunID:   1,
			Phase:   "changes_requested",
			Review:  &model.AgentTaskReview{Revision: 1, Decision: "changes_requested", Summary: "Missing the failing case"},
		},
	}
}

func TestAgentTaskActionOffersCorrectionCaptureOnlyWhenAuthorized(t *testing.T) {
	task := rejectedCaptureTask()
	if !agentTaskOffersCorrectionCapture(task) {
		t.Fatal("rejected revision did not offer a capture")
	}
	held := task
	held.Repository.State = "held"
	if agentTaskOffersCorrectionCapture(held) {
		t.Fatal("offered a capture while the worker held ownership")
	}
	accepted := task
	accepted.Workflow.Review = &model.AgentTaskReview{Revision: 1, Decision: "accept", Summary: "Verified"}
	if agentTaskOffersCorrectionCapture(accepted) {
		t.Fatal("offered a capture for an accepted result")
	}
	stale := task
	stale.Workflow.RunID = 2
	if agentTaskOffersCorrectionCapture(stale) {
		t.Fatal("offered a capture against a superseded revision")
	}
	plain := task
	plain.Repository.Write = false
	if agentTaskOffersCorrectionCapture(plain) {
		t.Fatal("offered a capture without managed write ownership")
	}
}

func TestAgentTaskActionCaptureQueuesBaselineCommand(t *testing.T) {
	task := rejectedCaptureTask()
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := Model{
		ctx:            context.Background(),
		svc:            service.New(cfg, st, events.NewBus(), nil),
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		visibility:     visibilityAllFolders,
		agentTaskAction: &agentTaskActionConfirmState{
			TaskID:          task.ID,
			ProjectPath:     task.WorkspacePath,
			TaskTitle:       task.Title,
			Selected:        agentTaskActionFocusKeep,
			Capture:         true,
			CaptureRevision: 1,
		},
	}
	// Keep -> Trash -> Capture, so the destructive action never sits under a
	// single Tab-and-Enter reflex aimed at the capture.
	updated, _ := m.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.agentTaskAction.Selected != agentTaskActionFocusTrash {
		t.Fatalf("first tab focus = %d", got.agentTaskAction.Selected)
	}
	updated, _ = got.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.agentTaskAction.Selected != agentTaskActionFocusCapture {
		t.Fatalf("second tab focus = %d", got.agentTaskAction.Selected)
	}
	overlay := ansi.Strip(got.renderAgentTaskActionOverlay(strings.Repeat("\n", 24), 120, 24))
	for _, want := range []string{"Capture baseline", "Authorize the checkout as it stands now", "correction of revision 1", "Nothing is stashed, reset or committed"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("capture option missing %q: %s", want, overlay)
		}
	}
	if strings.Contains(overlay, "deleted automatically after 7 days") {
		t.Fatalf("capture reused the trash warning: %q", overlay)
	}

	updated, cmd := got.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || got.status != "Capturing correction baseline..." {
		t.Fatalf("capture did not queue work: cmd=%v status=%q", cmd != nil, got.status)
	}
	if !got.agentTaskAction.Submitting {
		t.Fatal("capture left no busy state")
	}
	if _, repeat := got.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyEnter}); repeat != nil {
		t.Fatal("repeat activation queued a second capture")
	}
	msg, ok := cmd().(agentTaskActionMsg)
	if !ok {
		t.Fatal("capture command returned the wrong message")
	}
	if msg.projectPath != task.WorkspacePath || msg.selectPath != task.WorkspacePath {
		t.Fatalf("capture message lost its task: %+v", msg)
	}
	// The fixture checkout does not exist, so this must surface as an error
	// rather than a silent success.
	if msg.err == nil {
		t.Fatal("missing checkout reported success")
	}
}

func TestRenderAgentTaskDetailStatesCorrectionGrantHonestly(t *testing.T) {
	task := rejectedCaptureTask()
	task.Workflow.MaxCorrections = 2
	m := Model{}

	rendered := ansi.Strip(m.renderAgentTaskDetailContent(task, 110))
	if !strings.Contains(rendered, "Correction grant:") || !strings.Contains(rendered, "0 of 2 used") || !strings.Contains(rendered, "2 may run without asking again") {
		t.Fatalf("live grant not shown: %q", rendered)
	}

	spent := task
	spent.Workflow.CorrectionsUsed = 2
	if rendered = ansi.Strip(m.renderAgentTaskDetailContent(spent, 110)); !strings.Contains(rendered, "spent, so further corrections ask for confirmation") {
		t.Fatalf("spent grant not stated: %q", rendered)
	}

	revoked := task
	revoked.Workflow.SupervisionRevoked = true
	if rendered = ansi.Strip(m.renderAgentTaskDetailContent(revoked, 110)); !strings.Contains(rendered, "revoked, so further corrections ask for confirmation") {
		t.Fatalf("revoked grant not stated: %q", rendered)
	}

	// A task with no grant must not imply one exists.
	none := task
	none.Workflow.MaxCorrections = 0
	if rendered = ansi.Strip(m.renderAgentTaskDetailContent(none, 110)); strings.Contains(rendered, "Correction grant:") {
		t.Fatalf("ungranted task advertised a grant: %q", rendered)
	}
}

func TestAgentTaskActionRevokeQueuesSupervisionRevocation(t *testing.T) {
	task := rejectedCaptureTask()
	task.Workflow.MaxCorrections, task.Workflow.CorrectionsUsed = 2, 1
	if !agentTaskOffersSupervisionRevoke(task) {
		t.Fatal("unspent grant offered no revocation")
	}
	spent := task
	spent.Workflow.CorrectionsUsed = 2
	if agentTaskOffersSupervisionRevoke(spent) {
		t.Fatal("offered to revoke a spent grant")
	}

	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := Model{
		ctx:            context.Background(),
		svc:            service.New(cfg, st, events.NewBus(), nil),
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		visibility:     visibilityAllFolders,
		agentTaskAction: &agentTaskActionConfirmState{
			TaskID:          task.ID,
			ProjectPath:     task.WorkspacePath,
			TaskTitle:       task.Title,
			Selected:        agentTaskActionFocusRevoke,
			Capture:         true,
			CaptureRevision: 1,
			Revoke:          true,
			RevokeRemaining: 1,
		},
	}
	overlay := ansi.Strip(m.renderAgentTaskActionOverlay(strings.Repeat("\n", 24), 120, 24))
	for _, want := range []string{"Revoke corrections", "Stop reopening this worker automatically", "1 correction round(s)", "worker, its session and its edits are untouched"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("revoke option missing %q: %s", want, overlay)
		}
	}
	updated, cmd := m.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil || got.status != "Revoking the correction grant..." {
		t.Fatalf("revoke did not queue work: cmd=%v status=%q", cmd != nil, got.status)
	}
	if !got.agentTaskAction.Submitting {
		t.Fatal("revoke left no busy state")
	}
	msg, ok := cmd().(agentTaskActionMsg)
	if !ok {
		t.Fatal("revoke command returned the wrong message")
	}
	// The fixture task is not in this store, so the failure must be explicit.
	if msg.err == nil {
		t.Fatal("revoking an unknown task reported success")
	}
}
