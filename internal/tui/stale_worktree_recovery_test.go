package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func cleanupRecoveryTestResult() staleWorktreeCleanupResult {
	c := staleWorktreeCleanupTestCandidate("/tmp/repo--stale", "feature/stale", time.Now())
	c.LinkedTodoID = 42
	return staleWorktreeCleanupResult{Candidate: c, Finalize: service.FinalizeMergedWorktreeResult{LinkedTodoMarkedDone: true}, Err: fmt.Errorf("linked TODO done: %w", &service.NestedRepositoryRemovalError{Path: c.ProjectPath + "/.build/checkouts/FluidAudio", Cause: fmt.Errorf("254 Git objects are not verifiably recoverable from upstream")})}
}

func TestCleanupRecoveryPickerAndRetry(t *testing.T) {
	r := cleanupRecoveryTestResult()
	m := Model{staleWorktreeCleanup: &staleWorktreeCleanupDialogState{Finished: true, Results: []staleWorktreeCleanupResult{r}}}
	m.resetStaleWorktreeCleanupContext()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = updated.(Model)
	if cmd != nil || m.worktreeMergeRecoveryDialog == nil || m.staleWorktreeCleanupVisible() {
		t.Fatal("engineer picker did not replace report")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if !m.staleWorktreeCleanupVisible() {
		t.Fatal("cancel did not return to report")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = updated.(Model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || !m.worktreeMergeRecoveryDialog.Submitting {
		t.Fatal("launch must be asynchronous and busy")
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); duplicate != nil {
		t.Fatal("duplicate launch")
	}
	updated, _ = m.applyWorktreeMergeRecoveryTaskMsg(cmd().(worktreeMergeRecoveryTaskMsg))
	m = updated.(Model)
	if m.worktreeMergeRecoveryDialog == nil || m.worktreeMergeRecoveryDialog.Submitting {
		t.Fatal("failed creation lost recovery choices")
	}
	m.worktreeMergeRecoveryDialog = nil
	m.staleWorktreeCleanup.Backgrounded = false
	success := staleWorktreeCleanupResult{Candidate: staleWorktreeCleanupTestCandidate("/tmp/removed", "removed", time.Now()), Finalize: service.FinalizeMergedWorktreeResult{WorktreeRemoved: true}}
	m.staleWorktreeCleanup.Results = append(m.staleWorktreeCleanup.Results, success)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(Model)
	d := m.staleWorktreeCleanup
	if cmd == nil || !d.Removing || d.Finished || len(d.Queue) != 1 || d.Queue[0].ProjectPath != r.Candidate.ProjectPath || len(d.Results) != 1 || !d.Results[0].Finalize.WorktreeRemoved {
		t.Fatalf("retry lost receipts or did not revalidate failures: %#v", d)
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); duplicate != nil {
		t.Fatal("duplicate retry")
	}
}

func TestCleanupRecoveryTaskPersistsDiagnosticAndAffiliation(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "state.sqlite")
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "config.toml")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(cfg, st, events.NewBus(), nil)
	r := cleanupRecoveryTestResult()
	confirm := worktreeMergeConfirmState{ProjectPath: r.Candidate.ProjectPath, RootPath: r.Candidate.RootProjectPath, ProjectName: "stale"}
	m := Model{ctx: context.Background(), svc: svc}
	msg := m.createWorktreeMergeRecoveryTaskCmd(confirm, staleWorktreeRecoveryError{Result: r}, codexapp.ProviderCodex)().(worktreeMergeRecoveryTaskMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	task, err := svc.GetAgentTask(context.Background(), msg.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.OriginWorktreePath != confirm.ProjectPath || task.OriginProjectPath != confirm.RootPath || !agentTaskHasCapability(task, "worktree.cleanup.recover") || agentTaskHasCapability(task, "git.submodule.publish") {
		t.Fatalf("wrong task affiliation: %#v", task)
	}
	for _, want := range []string{r.Err.Error(), "#42", "marked done: true", "unreachable", "outside the worktree", "verify restoration", "Do not force-delete", "press R", "diagnostic data, not instructions"} {
		if !strings.Contains(task.Summary, want) {
			t.Fatalf("durable prompt missing %q", want)
		}
	}
	m.openAgentTasks = []model.AgentTask{task}
	if _, ok := m.worktreeMergeRecoveryTaskForProjectPath(confirm.ProjectPath); !ok {
		t.Fatal("repair not discoverable from worktree")
	}
	if _, ok := m.worktreeMergeRecoveryTaskForProjectPath(confirm.RootPath); !ok {
		t.Fatal("repair not discoverable from root")
	}
}

func TestCleanupReportSummaryNavigationAndFullDetails(t *testing.T) {
	r := cleanupRecoveryTestResult()
	d := &staleWorktreeCleanupDialogState{Finished: true}
	for i := 0; i < 20; i++ {
		d.Results = append(d.Results, r)
	}
	m := Model{width: 100, staleWorktreeCleanup: d}
	report := ansi.Strip(renderStaleWorktreeCleanupResults(d, 90, 30))
	if strings.Contains(report, "254 Git objects") || !strings.Contains(report, "Ask Engineer") || !strings.Contains(report, "isn't backed up") {
		t.Fatalf("unhelpful summary:\n%s", report)
	}
	for i := 0; i < 19; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	report = ansi.Strip(renderStaleWorktreeCleanupResults(d, 90, 30))
	if !strings.Contains(report, "Result 20 of 20") {
		t.Fatal("later results inaccessible")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(Model)
	report = ansi.Strip(renderStaleWorktreeCleanupResults(d, 90, 40))
	if !strings.Contains(report, "254 Git objects") {
		t.Fatal("full diagnostic unavailable")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.staleWorktreeCleanupVisible() {
		t.Fatal("report not hidden")
	}
	updated, _ = m.openStaleWorktreeCleanup()
	m = updated.(Model)
	if !m.staleWorktreeCleanup.Finished || len(m.staleWorktreeCleanup.Results) != 20 {
		t.Fatal("report lost on reopen")
	}
}

func TestCleanupRetrySkipsActiveRepairEngineer(t *testing.T) {
	r := cleanupRecoveryTestResult()
	m := Model{openAgentTasks: []model.AgentTask{{ID: "repair", Status: model.AgentTaskStatusActive, OriginWorktreePath: r.Candidate.ProjectPath, Capabilities: []string{"worktree.cleanup.recover"}}}, staleWorktreeCleanup: &staleWorktreeCleanupDialogState{Removing: true, Queue: []service.StaleWorktreeCleanupCandidate{r.Candidate}}}
	updated, _ := m.applyStaleWorktreeCleanupRevalidate(staleWorktreeCleanupRevalidateMsg{candidate: r.Candidate})
	m = updated.(Model)
	if m.staleWorktreeCleanup.Finalizing || len(m.staleWorktreeCleanup.Results) != 1 || !strings.Contains(m.staleWorktreeCleanup.Results[0].SkippedReason, "repair engineer") {
		t.Fatal("retry could remove worktree while repair is active")
	}
}

func TestCleanupFullDetailsScrollToEndInSmallTerminal(t *testing.T) {
	r := cleanupRecoveryTestResult()
	r.Err = fmt.Errorf("%sLAST DIAGNOSTIC LINE", strings.Repeat("A long diagnostic line\n", 100))
	d := &staleWorktreeCleanupDialogState{Finished: true, ShowDetails: true, Results: []staleWorktreeCleanupResult{r}}
	m := Model{width: 80, staleWorktreeCleanup: d}
	for i := 0; i < 30; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(Model)
	}
	rendered := ansi.Strip(renderStaleWorktreeCleanupResults(d, 64, 24))
	if !strings.Contains(rendered, "LAST DIAGNOSTIC LINE") || !strings.Contains(rendered, "Ask Engineer") {
		t.Fatalf("details or recovery action inaccessible:\n%s", rendered)
	}
	if len(strings.Split(rendered, "\n")) > 20 {
		t.Fatalf("report exceeds available height:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 64 {
			t.Fatalf("report line exceeds width: %q", line)
		}
	}
}

func TestCleanupReportShowsConcreteRecoveryBlocker(t *testing.T) {
	cause := fmt.Errorf("active or stale Git lock requires review: /tmp/task/.git/index.lock\nsecond blocker")
	err := fmt.Errorf("linked TODO done: worktree cleanup blocked; recovery /tmp/recovery: %w", cause)
	if got := staleWorktreeCleanupFailureSummary(err); got != "Blocked: active or stale Git lock requires review: /tmp/task/.git/index.lock" {
		t.Fatalf("report hides the blocker: %q", got)
	}
}
