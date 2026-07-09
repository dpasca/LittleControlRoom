package tui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"image"
	"image/color"
	"image/png"
	"lcroom/internal/aibackend"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestViewWithCommitPreviewRespectsHeight(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryCompleted,
				Summary:  "Work appears complete for now.",
			},
		},
		commitPreview: &service.CommitPreview{
			Intent:      service.GitActionFinish,
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Branch:      "master",
			StageMode:   service.GitStageAllChanges,
			Message:     "Ship current repo changes",
			Included: []service.CommitFile{
				{Code: "M", Summary: "README.md"},
				{Code: "??", Summary: "notes.txt"},
			},
			DiffSummary: "2 files changed, 3 insertions(+), 1 deletion(-)",
			CanPush:     true,
		},
		width:  100,
		height: 24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Commit Preview - demo (master)") {
		t.Fatalf("View() missing commit preview: %q", rendered)
	}
	if !strings.Contains(rendered, "Changes") || !strings.Contains(rendered, "Ship current repo changes") {
		t.Fatalf("View() missing commit preview details: %q", rendered)
	}
	if strings.Contains(rendered, "Source:") || strings.Contains(rendered, "Diff stat") || strings.Contains(rendered, "Included changes") {
		t.Fatalf("View() should use the simplified commit preview labels: %q", rendered)
	}
	if strings.Contains(rendered, "Push:") {
		t.Fatalf("View() should not render a push row anymore: %q", rendered)
	}
	if !strings.Contains(rendered, "2 files changed, 3 insertions(+), 1 deletion(-)") {
		t.Fatalf("View() missing merged change summary: %q", rendered)
	}
	if strings.Contains(rendered, "Selected project: demo") {
		t.Fatalf("View() should fold selected project into the commit preview title: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("View() should fold branch metadata into the commit preview title: %q", rendered)
	}
	if !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "commit") || !strings.Contains(rendered, "Alt+Enter") || !strings.Contains(rendered, "commit & push") || !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("View() missing commit dialog actions: %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	messageLine := -1
	stageLine := -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "Message:") && strings.Contains(line, "Ship current repo changes"):
			messageLine = i
		case strings.Contains(line, "Stage: stage all current changes"):
			stageLine = i
		}
	}
	if messageLine < 0 || stageLine < 0 {
		t.Fatalf("View() missing inline commit message or stage line: %q", rendered)
	}
	if !(messageLine < stageLine) {
		t.Fatalf("View() should show the commit message before stage metadata: %q", rendered)
	}
	if stageLine != messageLine+1 {
		t.Fatalf("View() should show stage immediately after the inline commit message line: %q", rendered)
	}
	if !strings.Contains(rendered, "Repo: clean") {
		t.Fatalf("View() should preserve the detail-pane context under the commit preview: %q", rendered)
	}
	if !strings.Contains(rendered, "Output") && !strings.Contains(rendered, "Standby. Use /run or /run-edit.") {
		t.Fatalf("View() should preserve background list and detail context under the commit preview: %q", rendered)
	}
}

func TestViewWithCommitPreviewShowsRecommendedUntrackedFiles(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:      service.GitActionCommit,
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Branch:      "master",
			StageMode:   service.GitStageStagedOnly,
			Message:     "Update repo",
			Included: []service.CommitFile{
				{Code: "M", Summary: "README.md"},
				{Code: "??", Summary: "notes.txt"},
			},
			SelectedUntracked: []service.CommitFile{
				{Code: "??", Summary: "notes.txt"},
			},
			Excluded: []service.CommitFile{
				{Code: "??", Summary: "scratch.txt"},
			},
			DiffSummary: "2 files changed, 3 insertions(+), 1 deletion(-)",
			CanPush:     false,
			Warnings: []string{
				"Will also stage 1 AI-recommended untracked file before commit.",
			},
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 50))
	if !strings.Contains(rendered, "Stage: commit staged changes plus 1 recommended untracked file") {
		t.Fatalf("renderCommitPreviewContent() should mention recommended untracked staging: %q", rendered)
	}
	if !strings.Contains(rendered, "notes.txt") || !strings.Contains(rendered, "scratch.txt") {
		t.Fatalf("renderCommitPreviewContent() should show included and left-out files: %q", rendered)
	}
	if !strings.Contains(rendered, "Will also stage 1 AI-recommended untracked file before commit.") {
		t.Fatalf("renderCommitPreviewContent() should show the AI staging warning: %q", rendered)
	}
	if !strings.Contains(rendered, "Alt+Enter") || !strings.Contains(rendered, "push unavailable") {
		t.Fatalf("renderCommitPreviewContent() should show disabled push action when push is unavailable: %q", rendered)
	}
}

func TestRenderCommitPreviewContentShowsLoadingPlaceholder(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:        service.GitActionCommit,
			ProjectName:   "demo",
			ProjectPath:   "/tmp/demo",
			StageMode:     service.GitStageStagedOnly,
			Message:       "Generating commit message...",
			LatestSummary: "Recent AI/backend setup work",
		},
		commitPreviewRefreshing: true,
		width:                   100,
		height:                  24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 50))
	if !strings.Contains(rendered, "Generating commit message...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading message placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "Inspecting repo changes...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading changes placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "Building commit preview...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading footer hint: %q", rendered)
	}
	if strings.Contains(rendered, "Enter commit") {
		t.Fatalf("renderCommitPreviewContent() should hide commit actions while loading: %q", rendered)
	}
}

func TestCommitPreviewEnterCommitsWithoutPush(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     true,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.commitApplying {
		t.Fatalf("enter should start applying the commit")
	}
	if got.pendingGitSummary("/tmp/demo") != "Committing..." {
		t.Fatalf("pending git summary = %q, want %q", got.pendingGitSummary("/tmp/demo"), "Committing...")
	}
	if got.status != "Committing..." {
		t.Fatalf("status = %q, want %q", got.status, "Committing...")
	}
	if cmd == nil {
		t.Fatalf("enter should return an apply command")
	}
}

func TestCommitPreviewRefreshingBlocksCommitActions(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Generating commit message...",
		},
		commitPreviewRefreshing: true,
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.commitApplying {
		t.Fatalf("enter should stay blocked while the preview is still refreshing")
	}
	if cmd != nil {
		t.Fatalf("enter should not return an apply command while refreshing")
	}
}

func TestCommitPreviewRefreshingAllowsEscCancel(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Generating commit message...",
		},
		commitPreviewRefreshing: true,
		commitPreviewRequestID:  3,
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Preparing commit preview...",
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("Esc should close a refreshing commit preview")
	}
	if got.commitPreviewRefreshing {
		t.Fatalf("Esc should clear refreshing state")
	}
	if got.commitPreviewRequestID != 4 {
		t.Fatalf("request id = %d, want old async result invalidated", got.commitPreviewRequestID)
	}
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary should clear when canceling refresh")
	}
	if got.status != "Commit preview canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
	if cmd != nil {
		t.Fatalf("Esc cancel should not return a command")
	}
}

func TestCommitPreviewMsgIgnoresStaleCanceledRequest(t *testing.T) {
	m := Model{
		commitPreviewRequestID: 4,
	}

	updated, cmd := m.Update(commitPreviewMsg{
		requestID:   3,
		projectPath: "/tmp/demo",
		preview: service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Late result",
		},
	})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("stale commit preview result should not reopen the dialog")
	}
	if cmd != nil {
		t.Fatalf("stale commit preview result should not return a command")
	}
}

func TestCommitPreviewAltEnterCommitsAndPushes(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     true,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if !got.commitApplying {
		t.Fatalf("alt+enter should start applying the commit")
	}
	if got.pendingGitSummary("/tmp/demo") != "Committing and pushing..." {
		t.Fatalf("pending git summary = %q, want %q", got.pendingGitSummary("/tmp/demo"), "Committing and pushing...")
	}
	if got.status != "Committing and pushing..." {
		t.Fatalf("status = %q, want %q", got.status, "Committing and pushing...")
	}
	if cmd == nil {
		t.Fatalf("alt+enter should return an apply command")
	}
}

func TestCommitPreviewAltEnterStaysBlockedWithoutPush(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     false,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if got.commitApplying {
		t.Fatalf("alt+enter should not start applying when push is unavailable")
	}
	if got.status != "Commit & push is unavailable for this repo" {
		t.Fatalf("status = %q, want %q", got.status, "Commit & push is unavailable for this repo")
	}
	if cmd != nil {
		t.Fatalf("alt+enter should not return an apply command when push is unavailable")
	}
}

func TestDispatchCommandPushMarksPendingGitOperation(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPush})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "Pushing..." {
		t.Fatalf("pending git summary = %q, want push marker", got.pendingGitSummary("/tmp/demo"))
	}
	if got.status != "Pushing..." {
		t.Fatalf("status = %q, want push-in-flight status", got.status)
	}
	if cmd == nil {
		t.Fatalf("/push should schedule async work")
	}
}

func TestDispatchCommandPullMarksPendingGitOperation(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPull})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "Pulling..." {
		t.Fatalf("pending git summary = %q, want pull marker", got.pendingGitSummary("/tmp/demo"))
	}
	if got.status != "Pulling..." {
		t.Fatalf("status = %q, want pull-in-flight status", got.status)
	}
	if cmd == nil {
		t.Fatalf("/pull should schedule async work")
	}
}

func TestCommitPreviewDOpensDiffView(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("pressing d should close the commit preview and open diff mode")
	}
	if got.diffView == nil {
		t.Fatalf("pressing d should open diff mode")
	}
	if got.status != "Preparing diff view..." {
		t.Fatalf("status = %q, want diff preparing status", got.status)
	}
	if cmd == nil {
		t.Fatalf("pressing d should return a diff load command")
	}
}

func TestCommitPreviewNoChangesOpensGitStatusDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.NoChangesToCommitError{
			ProjectPath: "/tmp/quickgame_30",
			ProjectName: "quickgame_30",
			Branch:      "master",
			Ahead:       4,
			CanPush:     true,
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("no-changes dialog should not immediately return another command")
	}
	if got.gitStatusDialog == nil {
		t.Fatalf("expected no-changes commit result to open the git status dialog")
	}
	if got.err != nil {
		t.Fatalf("no-changes dialog should clear the generic error, got %v", got.err)
	}
	if got.status != "Nothing new to commit. Enter push 4 existing commits, Esc cancel" {
		t.Fatalf("status = %q", got.status)
	}

	rendered := ansi.Strip(got.renderGitStatusDialogContent(72))
	if !strings.Contains(rendered, "Nothing To Commit - quickgame_30 (master)") {
		t.Fatalf("rendered dialog should identify the project and empty-commit state: %q", rendered)
	}
	if !strings.Contains(rendered, "ahead of upstream by 4 commit(s)") {
		t.Fatalf("rendered dialog should show ahead status: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("rendered dialog should fold branch metadata into the title: %q", rendered)
	}
	if !strings.Contains(rendered, "push 4 existing commits") {
		t.Fatalf("rendered dialog should offer pushing existing commits: %q", rendered)
	}
}

func TestCommitPreviewNoChangesRefreshesProjectStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             projectPath,
		Name:             "repo",
		PresentOnDisk:    true,
		WorktreeKind:     model.WorktreeKindMain,
		WorktreeRootPath: projectPath,
		InScope:          true,
		RepoBranch:       "master",
		RepoDirty:        true,
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.NoChangesToCommitError{
			ProjectPath: projectPath,
			ProjectName: "repo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("no-changes commit result should refresh project status")
	}
	msg := cmd()
	refreshMsg, ok := msg.(projectStatusRefreshedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want projectStatusRefreshedMsg", msg)
	}
	if refreshMsg.err != nil {
		t.Fatalf("refresh err = %v", refreshMsg.err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected refresh to clear stale dirty state")
	}

	updated, cmd = got.Update(refreshMsg)
	got = updated.(Model)
	if cmd == nil {
		t.Fatal("refresh completion should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestPrepareCommitPreviewCmdRefreshesStaleProjectStatusBeforeNoChangesDialog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	linkedPath := filepath.Join(root, "repo--linked")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	oldLinkedUpdatedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	if err := os.MkdirAll(linkedPath, 0o755); err != nil {
		t.Fatalf("create linked path: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             linkedPath,
		Name:             "repo--linked",
		PresentOnDisk:    true,
		WorktreeKind:     model.WorktreeKindLinked,
		WorktreeRootPath: projectPath,
		InScope:          true,
		RepoBranch:       "stale-linked",
		RepoDirty:        true,
		UpdatedAt:        oldLinkedUpdatedAt,
	}); err != nil {
		t.Fatalf("seed linked project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.prepareCommitPreviewCmd(projectPath, service.GitActionCommit, "")
	if cmd == nil {
		t.Fatal("prepareCommitPreviewCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", raw)
	}
	if msg.err == nil {
		t.Fatal("expected no-changes commit preview error")
	}
	if !msg.refreshedProjectState {
		t.Fatal("expected no-changes preview to refresh stale project state")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after prepare refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected prepare command to clear stale dirty state")
	}
	linkedDetail, err := st.GetProjectDetail(ctx, linkedPath, 20)
	if err != nil {
		t.Fatalf("get linked detail after prepare refresh: %v", err)
	}
	if linkedDetail.Summary.RepoBranch != "stale-linked" || !linkedDetail.Summary.RepoDirty {
		t.Fatalf("prepare no-changes refresh should not cascade into linked worktrees; linked summary = %#v", linkedDetail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if got.gitStatusDialog == nil {
		t.Fatalf("expected no-changes commit result to open the git status dialog")
	}
	if reloadCmd == nil {
		t.Fatal("no-changes commit result should invalidate project data immediately")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestCommitPreviewSubmoduleAttentionOpensGitStatusDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.SubmoduleAttentionError{
			ProjectPath:     "/tmp/fractalmech",
			ProjectName:     "FractalMech",
			Branch:          "master",
			Submodules:      []string{"assets_src"},
			DirtySubmodules: []string{"assets_src"},
		},
		intent:  service.GitActionCommit,
		message: "Update parent after assets refresh",
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("submodule-attention dialog should not immediately return another command")
	}
	if got.gitStatusDialog == nil {
		t.Fatalf("expected submodule-attention result to open the git status dialog")
	}
	if got.err != nil {
		t.Fatalf("submodule-attention dialog should clear the generic error, got %v", got.err)
	}
	if got.status != "Submodules need attention. Enter resolve & continue, Esc close" {
		t.Fatalf("status = %q", got.status)
	}

	rendered := ansi.Strip(got.renderGitStatusDialogContent(72))
	if !strings.Contains(rendered, "Submodule Attention - FractalMech (master)") {
		t.Fatalf("rendered dialog should identify the project and submodule state: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("rendered dialog should fold branch metadata into the title: %q", rendered)
	}
	if !strings.Contains(rendered, "assets_src") || !strings.Contains(rendered, "resolve & continue") {
		t.Fatalf("rendered dialog should explain the submodule follow-up: %q", rendered)
	}
}

func TestCommitPreviewSubmoduleResolvedNoParentCommitOpensGitStatusDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.SubmoduleResolvedNoParentChangesError{
			ProjectPath: "/tmp/fractalmech",
			ProjectName: "FractalMech",
			Branch:      "master",
			Summary:     "Pushed existing commits from submodule assets_src at abc123; no parent commit was needed for that submodule.",
		},
	})
	got := updated.(Model)

	_ = cmd
	if got.gitStatusDialog == nil {
		t.Fatalf("expected resolved submodule result to open the git status dialog")
	}
	if got.status != "Submodules resolved. Enter close, Esc close" {
		t.Fatalf("status = %q", got.status)
	}
	rendered := ansi.Strip(got.renderGitStatusDialogContent(72))
	if !strings.Contains(rendered, "Submodules Resolved - FractalMech (master)") {
		t.Fatalf("rendered dialog should identify resolved submodules: %q", rendered)
	}
	if !strings.Contains(rendered, "Pushed existing commits from submodule assets_src") {
		t.Fatalf("rendered dialog should include pushed submodule summary: %q", rendered)
	}
}

func TestGitStatusDialogEnterPushesExistingCommits(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath: "/tmp/demo",
			CanPush:     true,
			Ahead:       4,
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.gitStatusApplying {
		t.Fatalf("enter should start pushing when the dialog offers a push")
	}
	if got.pendingGitSummary("/tmp/demo") != "Pushing existing commits..." {
		t.Fatalf("pending git summary = %q, want push-in-flight marker", got.pendingGitSummary("/tmp/demo"))
	}
	if got.status != "Pushing existing commits..." {
		t.Fatalf("status = %q, want %q", got.status, "Pushing existing commits...")
	}
	if cmd == nil {
		t.Fatalf("enter should return a push command when the dialog offers a push")
	}
}

func TestPushCmdRefreshesProjectStatusAndTargetsProjectReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", "--bare", remotePath)
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")
	runTUITestGit(t, projectPath, "remote", "add", "origin", remotePath)
	runTUITestGit(t, projectPath, "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "ship")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "repo",
		PresentOnDisk:  true,
		InScope:        true,
		RepoBranch:     "master",
		RepoSyncStatus: model.RepoSyncAhead,
		RepoAheadCount: 1,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.pushCmd(projectPath)
	if cmd == nil {
		t.Fatal("pushCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("push action err = %v", msg.err)
	}
	if msg.status != "Pushed latest commits" {
		t.Fatalf("status = %q, want push success message", msg.status)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after push refresh: %v", err)
	}
	if detail.Summary.RepoAheadCount != 0 {
		t.Fatalf("expected ahead count to clear after push refresh, got %#v", detail.Summary)
	}
	if detail.Summary.RepoSyncStatus != model.RepoSyncSynced {
		t.Fatalf("expected synced repo status after push refresh, got %#v", detail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if reloadCmd == nil {
		t.Fatal("push action should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestPushCmdTreatsRemoteAheadRejectionAsWarning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	seedPath := filepath.Join(root, "seed")
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", "--bare", remotePath)
	runTUITestGit(t, "", "init", seedPath)
	runTUITestGit(t, seedPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, seedPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed README.md: %v", err)
	}
	runTUITestGit(t, seedPath, "add", "README.md")
	runTUITestGit(t, seedPath, "commit", "-m", "initial commit")
	branch := runTUITestGit(t, seedPath, "branch", "--show-current")
	runTUITestGit(t, seedPath, "remote", "add", "origin", remotePath)
	runTUITestGit(t, seedPath, "push", "-u", "origin", branch)
	runTUITestGit(t, root, "clone", remotePath, projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nlocal\n"), 0o644); err != nil {
		t.Fatalf("update local README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "local update")

	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\nremote\n"), 0o644); err != nil {
		t.Fatalf("update seed README.md: %v", err)
	}
	runTUITestGit(t, seedPath, "add", "README.md")
	runTUITestGit(t, seedPath, "commit", "-m", "remote update")
	runTUITestGit(t, seedPath, "push")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "repo",
		PresentOnDisk:  true,
		InScope:        true,
		RepoBranch:     branch,
		RepoSyncStatus: model.RepoSyncAhead,
		RepoAheadCount: 1,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	raw := m.pushCmd(projectPath)()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("push action err = %v", msg.err)
	}
	if !strings.HasPrefix(msg.status, "Pull first:") {
		t.Fatalf("status = %q, want pull-first warning", msg.status)
	}

	updated, _ := m.Update(msg)
	got := updated.(Model)
	if got.status != msg.status {
		t.Fatalf("status = %q, want warning status %q", got.status, msg.status)
	}
	if len(got.errorLogEntries) != 0 {
		t.Fatalf("remote-ahead push warning should not enter error log, got %#v", got.errorLogEntries)
	}
	if severity := topStatusSeverityForMessage(got.status, got.err); severity != topStatusSeverityWarning {
		t.Fatalf("top status severity = %v, want warning", severity)
	}
}

func TestPullCmdRefreshesProjectStatusAndTargetsProjectReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	seedPath := filepath.Join(root, "seed")
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", "--bare", remotePath)
	runTUITestGit(t, "", "init", seedPath)
	runTUITestGit(t, seedPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, seedPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed README.md: %v", err)
	}
	runTUITestGit(t, seedPath, "add", "README.md")
	runTUITestGit(t, seedPath, "commit", "-m", "initial commit")
	branch := runTUITestGit(t, seedPath, "branch", "--show-current")
	runTUITestGit(t, seedPath, "remote", "add", "origin", remotePath)
	runTUITestGit(t, seedPath, "push", "-u", "origin", branch)
	runTUITestGit(t, root, "clone", remotePath, projectPath)

	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\nremote\n"), 0o644); err != nil {
		t.Fatalf("update seed README.md: %v", err)
	}
	runTUITestGit(t, seedPath, "add", "README.md")
	runTUITestGit(t, seedPath, "commit", "-m", "remote update")
	runTUITestGit(t, seedPath, "push")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:            projectPath,
		Name:            "repo",
		PresentOnDisk:   true,
		InScope:         true,
		RepoBranch:      branch,
		RepoSyncStatus:  model.RepoSyncBehind,
		RepoBehindCount: 1,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.pullCmd(projectPath)
	if cmd == nil {
		t.Fatal("pullCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("pull action err = %v", msg.err)
	}
	if msg.status != "Pull complete" {
		t.Fatalf("status = %q, want pull success message", msg.status)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after pull refresh: %v", err)
	}
	if detail.Summary.RepoBehindCount != 0 {
		t.Fatalf("expected behind count to clear after pull refresh, got %#v", detail.Summary)
	}
	if detail.Summary.RepoSyncStatus != model.RepoSyncSynced {
		t.Fatalf("expected synced repo status after pull refresh, got %#v", detail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if reloadCmd == nil {
		t.Fatal("pull action should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestApplyCommitPreviewCmdRefreshesProjectStatusAndTargetsProjectReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.applyCommitPreviewCmd(service.CommitPreview{
		ProjectPath: projectPath,
		ProjectName: "repo",
		Branch:      "master",
		StageMode:   service.GitStageAllChanges,
		Message:     "ship",
	}, false)
	if cmd == nil {
		t.Fatal("applyCommitPreviewCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("commit action err = %v", msg.err)
	}
	if msg.status == "" {
		t.Fatal("status should describe commit result")
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after commit refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected clean repo status after commit refresh, got %#v", detail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if reloadCmd == nil {
		t.Fatal("commit action should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestGitStatusDialogEnterClosesWhenPushUnavailable(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath: "/tmp/demo",
			CanPush:     false,
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.gitStatusDialog != nil {
		t.Fatalf("enter should close the dialog when no push action is available")
	}
	if got.status != "No changes to commit" {
		t.Fatalf("status = %q, want %q", got.status, "No changes to commit")
	}
	if cmd != nil {
		t.Fatalf("enter should not return a command when the dialog only closes")
	}
}

func TestGitStatusDialogEnterClosesWithCustomDismissStatus(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath:   "/tmp/demo",
			CanPush:       false,
			DismissStatus: "Submodule changes still need attention",
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.gitStatusDialog != nil {
		t.Fatalf("enter should close the dialog when no push action is available")
	}
	if got.status != "Submodule changes still need attention" {
		t.Fatalf("status = %q, want custom dismiss status", got.status)
	}
	if cmd != nil {
		t.Fatalf("enter should not return a command when the dialog only closes")
	}
}

func TestGitStatusDialogEnterResolvesSubmodulesWhenAvailable(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath:       "/tmp/demo",
			ResolveSubmodules: true,
			CommitIntent:      service.GitActionCommit,
			CommitMessage:     "Update parent",
			DismissStatus:     "Submodule changes still need attention",
			ReadyStatus:       "Submodules need attention. Enter resolve & continue, Esc close",
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.gitStatusApplying {
		t.Fatalf("enter should start resolving submodules when the dialog offers it")
	}
	if got.status != "Resolving submodule commits..." {
		t.Fatalf("status = %q, want resolving status", got.status)
	}
	if cmd == nil {
		t.Fatalf("enter should return a resolve command when the dialog offers it")
	}
}

func TestCommandPaletteScrollsSelectedSuggestionIntoView(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	selectedIndex := 0
	suggestions := commands.Suggestions("/")
	for i, suggestion := range suggestions {
		if suggestion.Display == "/sessions on|off|toggle" {
			selectedIndex = i
			break
		}
	}

	m := Model{
		commandMode:     true,
		commandInput:    input,
		commandSelected: selectedIndex,
		width:           100,
		height:          24,
	}
	m.syncCommandSelection()

	rendered := m.renderCommandPaletteContent(70)
	if !strings.Contains(rendered, "/sessions on|off|toggle") {
		t.Fatalf("rendered palette should include the selected suggestion once it scrolls into view: %q", rendered)
	}
	if !strings.Contains(rendered, "↑ ") {
		t.Fatalf("rendered palette should show that earlier suggestions exist: %q", rendered)
	}
	if !strings.Contains(rendered, "↓ ") {
		t.Fatalf("rendered palette should show that later suggestions exist: %q", rendered)
	}
	if strings.Contains(rendered, "/help  Open the help panel") {
		t.Fatalf("rendered palette should scroll past the initial suggestions when selection moves later: %q", rendered)
	}
}

func TestDispatchSessionCommandOpensEmbeddedSessionPicker(t *testing.T) {
	now := time.Date(2026, 5, 9, 11, 30, 0, 0, time.UTC)
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider:       codexapp.ProviderLCAgent,
				ProjectPath:    "/tmp/demo",
				ThreadID:       "lca_demo",
				Started:        true,
				LastActivityAt: now,
				Status:         "Loaded LCAgent session lca_demo from disk",
			},
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindSession, Canonical: "/session"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/session) cmd = %#v, want nil", cmd)
	}
	if !got.codexPickerVisible {
		t.Fatalf("/session should open the embedded session picker")
	}
	if got.codexPickerTitle != "Embedded Sessions" {
		t.Fatalf("picker title = %q", got.codexPickerTitle)
	}
	if got.status != "Embedded session picker open" {
		t.Fatalf("status = %q, want picker-open status", got.status)
	}
	if len(got.codexPickerChoices) != 1 || got.codexPickerChoices[0].Provider != codexapp.ProviderLCAgent {
		t.Fatalf("picker choices = %#v, want one LCAgent choice", got.codexPickerChoices)
	}
}

func TestHelpPanelLinesStayMinimal(t *testing.T) {
	lines := helpPanelLines()
	joined := ansi.Strip(strings.Join(lines, "\n"))

	if len(lines) > 20 {
		t.Fatalf("helpPanelLines() should stay compact, got %d lines: %q", len(lines), joined)
	}
	if !strings.Contains(joined, "slash-command palette") {
		t.Fatalf("helpPanelLines() should explain the slash-command palette: %q", joined)
	}
	if !strings.Contains(joined, "Help Chat") {
		t.Fatalf("helpPanelLines() should advertise the active Help Chat shortcut: %q", joined)
	}
	if !strings.Contains(joined, "/wt merge|remove|prune") {
		t.Fatalf("helpPanelLines() should include concrete worktree slash-command examples: %q", joined)
	}
	if !strings.Contains(joined, "/setup, /ai, /perf, /errors, /codex, /todo") || !strings.Contains(joined, "/commit, /diff, or /run") {
		t.Fatalf("helpPanelLines() should include concrete slash-command examples: %q", joined)
	}
	if !strings.Contains(joined, "interrupt busy session") {
		t.Fatalf("helpPanelLines() should keep the session interrupt hint: %q", joined)
	}
	if !strings.Contains(joined, "b  boss") || !strings.Contains(joined, "t  todo") || !strings.Contains(joined, "o  sort") || !strings.Contains(joined, "p  pin") || !strings.Contains(joined, "ctrl+v  image") {
		t.Fatalf("helpPanelLines() should show the reordered quick actions: %q", joined)
	}
	if !strings.Contains(joined, "AGENT") || !strings.Contains(joined, "RUN") {
		t.Fatalf("helpPanelLines() missing colored legend content: %q", joined)
	}
	if strings.Contains(joined, "/settings /new-project /open /run-edit") {
		t.Fatalf("helpPanelLines() should not grow back into a slash-command inventory: %q", joined)
	}
	if strings.Contains(joined, "Runtime pane shows command, ports, URL, logs") {
		t.Fatalf("helpPanelLines() should omit verbose runtime detail: %q", joined)
	}
}

func TestRenderHelpPanelOmitsVerboseLegacyHints(t *testing.T) {
	m := Model{}

	rendered := ansi.Strip(m.renderHelpPanel(80))
	if strings.Contains(rendered, "f   forget missing") {
		t.Fatalf("renderHelpPanel() should not advertise forget while it is unavailable: %q", rendered)
	}
	if strings.Contains(rendered, "Runtime pane shows command, ports, URL, logs") {
		t.Fatalf("renderHelpPanel() should not include verbose runtime prose: %q", rendered)
	}
	if !strings.Contains(rendered, "slash-command palette") {
		t.Fatalf("renderHelpPanel() should explain the slash-command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "ctrl+v") || !strings.Contains(rendered, "image") {
		t.Fatalf("renderHelpPanel() should keep the paste hint: %q", rendered)
	}
}

func TestViewWithHelpOverlayPreservesBackground(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		selected: 0,
		showHelp: true,
		width:    100,
		height:   24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Help") || !strings.Contains(rendered, "slash-command palette") {
		t.Fatalf("View() should show the help overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "[Main") || !strings.Contains(rendered, "Summary") || !strings.Contains(rendered, "Path:") {
		t.Fatalf("View() should preserve the dashboard behind the help overlay: %q", rendered)
	}
}

func TestBacktickOpensAndHidesHelpChat(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"
	m := Model{
		ctx:              context.Background(),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'`'}})
	got := updated.(Model)
	if !got.helpChatMode {
		t.Fatalf("backtick should open help chat")
	}
	if got.bossMode {
		t.Fatalf("backtick should open the help overlay, not full Boss mode")
	}
	if got.bossModelActive {
		t.Fatalf("backtick should not initialize the full Boss model")
	}
	if !got.helpChatModelActive {
		t.Fatalf("backtick should initialize the help chat model")
	}
	if got.showHelp {
		t.Fatalf("backtick should not open the static quick help panel")
	}
	if cmd == nil {
		t.Fatalf("opening help chat should return the embedded boss init command")
	}
	rendered := ansi.Strip(got.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() with help chat line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Help Chat") {
		t.Fatalf("help chat overlay should render the Help Chat modal: %q", rendered)
	}
	for _, unwanted := range []string{"Boss Chat", "Boss Desk", "Boss Log"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("help chat overlay should render a frameless core chat, not %q: %q", unwanted, rendered)
		}
	}
	for _, unwanted := range []string{"Tab chat/flow", "Flow"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("help chat overlay should not expose Boss transcript controls %q: %q", unwanted, rendered)
		}
	}
	for _, unwanted := range []string{"LLM help", "LLM"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("help chat overlay should not render the old right-side LLM label %q: %q", unwanted, rendered)
		}
	}
	for _, want := range []string{"/new clear", "Ctrl+L clear"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("help chat overlay should advertise %q: %q", want, rendered)
		}
	}

	updated, cmd = got.updateHelpChatModeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'`'}})
	got = updated.(Model)
	if got.helpChatMode {
		t.Fatalf("backtick should hide help chat when it is active")
	}
	if cmd != nil {
		t.Fatalf("hiding help chat with backtick should not return a command")
	}
}

func TestQuestionMarkStillOpensQuickHelpPanel(t *testing.T) {
	m := Model{}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	got := updated.(Model)
	if !got.showHelp {
		t.Fatalf("question mark should open the static quick help panel")
	}
	if got.helpChatMode {
		t.Fatalf("question mark should not open active Help Chat")
	}
	if cmd != nil {
		t.Fatalf("question mark help toggle should not return a command")
	}
}

func TestDispatchAICommandOpensAIStatsDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindAIStats})
	got := updated.(Model)
	if !got.showAIStats {
		t.Fatalf("dispatchCommand(/ai) should open the AI stats dialog")
	}
	if got.status != "AI stats open. Press c to copy, r to refresh, or Esc to close" {
		t.Fatalf("status = %q, want AI stats open status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/ai) should refresh backend status")
	}
}

func TestUpdateAIStatsModeCopiesDetailsToClipboard(t *testing.T) {
	prevWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = prevWriter
	})

	m := Model{
		showAIStats:  true,
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendDeepSeek,
			DeepSeek: aibackend.Status{
				Backend: config.AIBackendDeepSeek,
				Label:   "DeepSeek",
				Ready:   true,
				Detail:  "Saved DeepSeek API key ready.",
			},
		},
	}

	updated, cmd := m.updateAIStatsMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("copy should happen synchronously")
	}
	if !strings.Contains(copied, "AI Stats - DeepSeek") || !strings.Contains(copied, "Selected: DeepSeek") {
		t.Fatalf("copied AI stats missing backend details: %q", copied)
	}
	if got.status != "Copied AI stats to clipboard" {
		t.Fatalf("status = %q, want copied confirmation", got.status)
	}
}

func TestDispatchPerfCommandOpensPerfDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPerf})
	got := updated.(Model)
	if !got.showPerf {
		t.Fatalf("dispatchCommand(/perf) should open the performance dialog")
	}
	if got.status != "Performance open. Press c to copy or Esc to close" {
		t.Fatalf("status = %q, want performance open status", got.status)
	}
	if cmd != nil {
		t.Fatalf("dispatchCommand(/perf) should not return an async command")
	}
}

func TestRenderAIStatsOverlayPreservesBackground(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationFailed,
			LatestSessionClassificationType:  model.SessionCategoryBlocked,
			LatestSessionSummary:             "Classifier failed on the latest session.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			PresentOnDisk:               true,
			LatestSessionClassification: model.ClassificationFailed,
		}},
		showAIStats: true,
		width:       100,
		height:      24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "AI Stats") || !strings.Contains(rendered, "Assessment Attention") || !strings.Contains(rendered, "demo") {
		t.Fatalf("View() should show the AI stats overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "Little Control Room") || !strings.Contains(rendered, "╭────╭") {
		t.Fatalf("View() should keep the dashboard visible around the AI stats overlay: %q", rendered)
	}
}

func TestRenderPerfOverlayPreservesBackground(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		showPerf: true,
		width:    100,
		height:   24,
		aiLatencyRecent: []aiLatencySample{{
			Name:        "Model apply",
			ProjectPath: "/tmp/demo",
			Detail:      "Codex gpt-5.4 high",
			Result:      "ok",
			Duration:    420 * time.Millisecond,
		}},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Performance") || !strings.Contains(rendered, "Latency") || !strings.Contains(rendered, "Model apply") {
		t.Fatalf("View() should show the performance overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "Little Control Room") || !strings.Contains(rendered, "Repo: clean") || !strings.Contains(rendered, "TODOs: none") {
		t.Fatalf("View() should keep the dashboard visible around the performance overlay: %q", rendered)
	}
}

func TestRenderAIStatsContentHidesLocalBackendCost(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	classifier := &usageSnapshotClassifier{
		usage: model.LLMSessionUsage{
			Enabled: true,
			Model:   "gpt-5-mini",
			Totals: model.LLMUsage{
				InputTokens:  345,
				OutputTokens: 538,
			},
		},
	}

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex
	svc := service.New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	m := New(ctx, svc)
	m.setupChecked = true
	m.setupSnapshot = aibackend.Snapshot{
		Selected: config.AIBackendCodex,
		Codex: aibackend.Status{
			Backend:       config.AIBackendCodex,
			Label:         "Codex",
			Installed:     true,
			Authenticated: true,
			Ready:         true,
			Detail:        "Logged in with ChatGPT.",
		},
	}

	rendered := ansi.Strip(m.renderAIStatsContent(76))
	if strings.Contains(rendered, "Cost:") {
		t.Fatalf("renderAIStatsContent() should hide cost for local backends: %q", rendered)
	}
	if !strings.Contains(rendered, "Billing: local provider mode") {
		t.Fatalf("renderAIStatsContent() should show local provider billing mode: %q", rendered)
	}
	if !strings.Contains(rendered, "Codex is running through its local provider path here") {
		t.Fatalf("renderAIStatsContent() should explain local backend billing semantics: %q", rendered)
	}
	if strings.Contains(rendered, "Latency") {
		t.Fatalf("renderAIStatsContent() should keep performance details out of AI stats: %q", rendered)
	}
}

func TestRenderAIStatsContentShowsLocalContextAndSpeed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	classifier := &usageSnapshotClassifier{
		usage: model.LLMSessionUsage{
			Enabled:                      true,
			Model:                        "gemma4:12b-mlx",
			Completed:                    2,
			LastRequestDuration:          1500 * time.Millisecond,
			LastOutputEvalDuration:       800 * time.Millisecond,
			LastOutputTokensPerSecond:    12.5,
			AverageOutputTokensPerSecond: 10.0,
			Totals: model.LLMUsage{
				InputTokens:        512,
				OutputTokens:       30,
				TotalTokens:        542,
				OutputEvalDuration: 3 * time.Second,
			},
		},
	}

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOllama
	svc := service.New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	m := New(ctx, svc)
	m.setupChecked = true
	m.setupSnapshot = aibackend.Snapshot{
		Selected: config.AIBackendOllama,
		Ollama: aibackend.Status{
			Backend:       config.AIBackendOllama,
			Label:         "Ollama",
			Installed:     true,
			Authenticated: true,
			Ready:         true,
			Detail:        "Ollama ready at http://127.0.0.1:11434/v1 (using gemma4:12b-mlx)",
			ContextWindow: 131072,
			ContextDetail: "max context 131.1k tokens | 13.0B | nvfp4",
		},
	}

	rendered := ansi.Strip(m.renderAIStatsContent(90))
	for _, want := range []string{
		"Speed: decode last 12.5 tok/s | decode avg 10.0 tok/s | request last 1.5s",
		"Context: max context 131.1k tokens | 13.0B | nvfp4",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderAIStatsContent() missing %q: %q", want, rendered)
		}
	}
}

func TestRenderPerfContentShowsLatencySection(t *testing.T) {
	now := time.Date(2026, time.April, 2, 17, 30, 0, 0, time.UTC)
	m := Model{
		aiLatencyInFlight: map[int64]aiLatencyOp{
			1: {
				ID:          1,
				Name:        "Embedded open",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex",
				StartedAt:   now.Add(-3200 * time.Millisecond),
			},
		},
		aiLatencyRecent: []aiLatencySample{
			{
				Name:        "Model apply",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex gpt-5.4 high",
				Result:      "ok",
				Duration:    420 * time.Millisecond,
			},
			{
				Name:        "Embedded viewport",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex",
				Result:      "ok",
				Duration:    2300 * time.Millisecond,
			},
		},
		nowFn: func() time.Time { return now },
	}

	rendered := ansi.Strip(m.renderPerfContent(76))
	for _, want := range []string{
		"Performance",
		"Latency",
		"In flight: 1 operation(s)",
		"Embedded open",
		"Model apply",
		"Embedded viewport",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderPerfContent() missing %q: %q", want, rendered)
		}
	}
}

func TestSectionToggleDevKeysAreNoLongerBound(t *testing.T) {
	m := Model{
		showSessions: true,
		showEvents:   true,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("x should no longer trigger a command")
	}
	if !got.showSessions || !got.showEvents {
		t.Fatalf("x should no longer toggle section visibility")
	}

	updated, cmd = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("e should no longer trigger a command")
	}
	if !got.showSessions || !got.showEvents {
		t.Fatalf("e should no longer toggle section visibility")
	}
}

func TestDispatchDiffCommandOpensDiffView(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindDiff})
	got := updated.(Model)
	if got.diffView == nil {
		t.Fatalf("dispatchCommand(/diff) should open the diff view")
	}
	if got.status != "Preparing diff view..." {
		t.Fatalf("status = %q, want preparing diff status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/diff) should return a load command")
	}
}

func TestDispatchCommitCommandOpensLoadingPreviewImmediately(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                 "demo",
			Path:                 "/tmp/demo",
			PresentOnDisk:        true,
			LatestSessionSummary: "Recent backend setup cleanup",
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCommit})
	got := updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("dispatchCommand(/commit) should open the commit preview shell immediately")
	}
	if !got.commitPreviewRefreshing {
		t.Fatalf("dispatchCommand(/commit) should mark the preview as refreshing")
	}
	if got.commitPreview.Message != "Generating commit message..." {
		t.Fatalf("loading preview message = %q, want generating placeholder", got.commitPreview.Message)
	}
	if got.commitPreview.ProjectName != "demo" {
		t.Fatalf("loading preview should use the selected project name, got %q", got.commitPreview.ProjectName)
	}
	if got.status != "Preparing commit preview..." {
		t.Fatalf("status = %q, want preparing commit preview", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/commit) should still return the async preview command")
	}
}

func TestActionMsgClearsPendingGitSummary(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		commitApplying: true,
	}

	updated, cmd := m.Update(actionMsg{
		projectPath:            "/tmp/demo",
		status:                 "Committed abc12345",
		clearPendingGitSummary: true,
	})
	got := updated.(Model)
	// On success, the pending summary stays alive until the next project
	// list refresh so the spinner keeps animating instead of flashing "!".
	if got.pendingGitSummary("/tmp/demo") == "" {
		t.Fatalf("pending git summary should survive until project refresh")
	}
	if !got.pendingGitSummaryExpireNext["/tmp/demo"] {
		t.Fatalf("pending git summary should be marked for expiry on next refresh")
	}
	if got.status != "Committed abc12345" {
		t.Fatalf("status = %q, want %q", got.status, "Committed abc12345")
	}
	if cmd == nil {
		t.Fatalf("actionMsg should trigger follow-up refresh commands")
	}

	// Simulate project list refresh — pending summary should now be cleared.
	updated2, _ := got.Update(projectsMsg{})
	got2 := updated2.(Model)
	if got2.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared after project refresh", got2.pendingGitSummary("/tmp/demo"))
	}
}

func TestProjectSummaryMsgClearsExpiredPendingGitSummary(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Pushing...",
		},
		pendingGitSummaryExpireNext: map[string]bool{
			"/tmp/demo": true,
		},
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
	}

	updated, _ := m.Update(projectSummaryMsg{
		path:  "/tmp/demo",
		found: true,
		summary: model.ProjectSummary{
			Path: "/tmp/demo",
			Name: "demo",
		},
	})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared after targeted summary refresh", got.pendingGitSummary("/tmp/demo"))
	}
	if got.pendingGitSummaryExpireNext != nil {
		if got.pendingGitSummaryExpireNext["/tmp/demo"] {
			t.Fatalf("pending git summary expiry should be consumed for refreshed project")
		}
	}
}

func TestActionMsgErrorClearsPendingGitSummaryImmediately(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		commitApplying: true,
	}

	updated, _ := m.Update(actionMsg{
		projectPath:            "/tmp/demo",
		status:                 "Commit failed",
		clearPendingGitSummary: true,
		err:                    fmt.Errorf("git error"),
	})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared on error", got.pendingGitSummary("/tmp/demo"))
	}
}

func TestTogglePinCmdReturnsTargetedProjectRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/pin-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "pin-demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	cmd := m.togglePinCmd(projectPath)
	if cmd == nil {
		t.Fatal("togglePinCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want actionMsg", raw)
	}
	if msg.projectPath != projectPath {
		t.Fatalf("projectPath = %q, want %q", msg.projectPath, projectPath)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}
}

func TestSnoozeCmdReturnsTargetedProjectRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/snooze-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "snooze-demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	cmd := m.snoozeCmd(projectPath, time.Hour)
	if cmd == nil {
		t.Fatal("snoozeCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want actionMsg", raw)
	}
	if msg.projectPath != projectPath {
		t.Fatalf("projectPath = %q, want %q", msg.projectPath, projectPath)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}
}

func TestViewWithDiffScreenUsesFullBody(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		ProjectName: "demo",
		ProjectPath: "/tmp/demo",
		Branch:      "master",
		Summary:     "3 files changed, 12 insertions(+), 1 deletion(-)",
		Files: []service.DiffFilePreview{
			{
				Path:      "pixel.png",
				Summary:   "pixel.png",
				Code:      "M",
				Kind:      scanner.GitChangeModified,
				Unstaged:  true,
				Body:      "Binary image change rendered as ANSI preview.",
				IsImage:   true,
				OldImage:  mustTestPNG(color.RGBA{R: 220, G: 32, B: 32, A: 255}),
				NewImage:  mustTestPNG(color.RGBA{R: 32, G: 120, B: 220, A: 255}),
				Untracked: false,
			},
			{
				Path:      "README.md",
				Summary:   "README.md",
				Code:      "M",
				Kind:      scanner.GitChangeModified,
				Staged:    true,
				Body:      "# Staged\n\ndiff --git a/README.md b/README.md\n+diff screen\n",
				IsImage:   false,
				OldImage:  nil,
				NewImage:  nil,
				Untracked: false,
			},
		},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   24,
		status:   "Preparing diff view...",
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Files") || !strings.Contains(rendered, "README.md") {
		t.Fatalf("View() should render the diff file list: %q", rendered)
	}
	if !strings.Contains(rendered, "Staged (1)") || !strings.Contains(rendered, "Unstaged (1)") {
		t.Fatalf("View() should render staged and unstaged file sections: %q", rendered)
	}
	if !strings.Contains(rendered, "HEAD image") || !strings.Contains(rendered, "Working tree image") {
		t.Fatalf("View() should render image diff labels: %q", rendered)
	}
	if !strings.Contains(rendered, "Alt+Up") || !strings.Contains(rendered, "stage") || !strings.Contains(rendered, "unified") {
		t.Fatalf("View() should render the highlighted diff footer legend: %q", rendered)
	}
	if strings.Contains(rendered, "ATTN  ASSESS") || strings.Contains(rendered, "Attention reasons") {
		t.Fatalf("View() should replace the normal list/detail body when diff is open: %q", rendered)
	}
}

func TestDiffScreenKeepsActionHintsInFooterOnly(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		ProjectName: "demo",
		ProjectPath: "/tmp/demo",
		Branch:      "master",
		Summary:     "1 file changed",
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\ndiff --git a/README.md b/README.md\n+diff screen\n",
		}},
	}

	m := Model{
		diffView: diffState,
		width:    200,
		height:   20,
	}
	m.syncDiffView(true)
	m.status = diffViewReadyStatus(*m.diffView)

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) < 2 {
		t.Fatalf("View() rendered too few lines: %q", strings.Join(lines, "\n"))
	}
	top := lines[0]
	footer := lines[len(lines)-1]
	for _, unexpected := range []string{"Alt+F", "Enter open", "Esc close", "f filter", "b boss"} {
		if strings.Contains(top, unexpected) {
			t.Fatalf("top status should not advertise action hint %q: %q", unexpected, top)
		}
	}
	if !strings.Contains(top, "Diff split") || !strings.Contains(top, "focus: diff") {
		t.Fatalf("top status should keep compact diff state, got %q", top)
	}
	for _, want := range []string{"Enter open", "Alt+F folder", "Esc close"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("diff footer should advertise %q, got %q", want, footer)
		}
	}
}

func TestNewDiffViewStateDefaultsToContentFocus(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	if diffState.focus != diffFocusContent {
		t.Fatalf("new diff focus = %s, want content", diffState.focus)
	}
}

func TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen(t *testing.T) {
	m := Model{
		diffView: newDiffViewState("/tmp/demo", "demo"),
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(diffPreviewMsg{
		err: service.NoDiffChangesError{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("no-diff result should not queue another command")
	}
	if got.diffView == nil {
		t.Fatalf("no-diff result should keep the diff screen open")
	}
	if got.diffView.loading {
		t.Fatalf("no-diff result should stop loading")
	}
	if got.diffView.preview == nil {
		t.Fatalf("no-diff result should keep preview metadata for the empty state")
	}
	if got.status != "Worktree clean" {
		t.Fatalf("status = %q, want clean-worktree status", got.status)
	}

	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Clean worktree") {
		t.Fatalf("clean diff screen should explain the empty state: %q", rendered)
	}
	if !strings.Contains(rendered, "demo has no staged, unstaged, or untracked") || !strings.Contains(rendered, "changes right now") {
		t.Fatalf("clean diff screen should show the no-diff warning in the content pane: %q", rendered)
	}
	if strings.Contains(rendered, "Enter/Tab") || strings.Contains(rendered, "unified") {
		t.Fatalf("clean diff screen should not show interactive diff controls: %q", rendered)
	}
}

func TestDiffPreviewMsgNoChangesRefreshesProjectStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
		diffView: newDiffViewState(projectPath, "repo"),
	}

	updated, cmd := m.Update(diffPreviewMsg{
		err: service.NoDiffChangesError{
			ProjectPath: projectPath,
			ProjectName: "repo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("no-diff result should refresh project status")
	}
	msg := cmd()
	refreshMsg, ok := msg.(projectStatusRefreshedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want projectStatusRefreshedMsg", msg)
	}
	if refreshMsg.err != nil {
		t.Fatalf("refresh err = %v", refreshMsg.err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected refresh to clear stale dirty state")
	}

	updated, cmd = got.Update(refreshMsg)
	got = updated.(Model)
	if cmd == nil {
		t.Fatal("refresh completion should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestRenderDiffFileListSeparatesStagedAndUnstagedSections(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "unstaged.txt",
				Summary:  "unstaged.txt",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
			},
			{
				Path:    "staged.txt",
				Summary: "staged.txt",
				Code:    "M",
				Kind:    scanner.GitChangeModified,
				Staged:  true,
			},
		},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   20,
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.renderDiffFileList(28, 10))
	stagedHeader := strings.Index(rendered, "Staged (1)")
	stagedFile := strings.Index(rendered, "staged.txt")
	unstagedHeader := strings.Index(rendered, "Unstaged (1)")
	unstagedFile := strings.Index(rendered, "unstaged.txt")
	if stagedHeader == -1 || stagedFile == -1 || unstagedHeader == -1 || unstagedFile == -1 {
		t.Fatalf("renderDiffFileList() should include both grouped sections and files: %q", rendered)
	}
	if !(stagedHeader < stagedFile && stagedFile < unstagedHeader && unstagedHeader < unstagedFile) {
		t.Fatalf("renderDiffFileList() should place staged files before the unstaged section: %q", rendered)
	}
}

func TestRenderDiffFileRowSelectedUsesCompactCodeSpacing(t *testing.T) {
	rendered := ansi.Strip(renderDiffFileRow(service.DiffFilePreview{
		Path:     "README.md",
		Summary:  "README.md",
		Code:     "M",
		Kind:     scanner.GitChangeModified,
		Unstaged: true,
	}, true, 28))
	if strings.Contains(rendered, "M   modified") {
		t.Fatalf("selected diff row should not add extra padding before the state label: %q", rendered)
	}
	if !strings.Contains(rendered, "M modified") {
		t.Fatalf("selected diff row should keep the compact code-to-state spacing: %q", rendered)
	}
}

func TestRenderDiffEntryBodyUsesSideBySideColumns(t *testing.T) {
	rendered := ansi.Strip(renderDiffEntryBody(service.DiffFilePreview{
		Path:    "README.md",
		Summary: "README.md",
		Code:    "M",
		Kind:    scanner.GitChangeModified,
		Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
	}, 84, diffRenderModeSideBySide))

	for _, want := range []string{
		"Unstaged",
		"Before",
		"After",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -1,3 +1,3 @@",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderDiffEntryBody() missing %q in side-by-side output: %q", want, rendered)
		}
	}

	foundPair := false
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "-old title") && strings.Contains(line, "+new title") {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("renderDiffEntryBody() should place removed and added lines on the same visual row: %q", rendered)
	}
}

func TestRenderDiffEntryBodyCanUseUnifiedMode(t *testing.T) {
	rendered := ansi.Strip(renderDiffEntryBody(service.DiffFilePreview{
		Path:    "README.md",
		Summary: "README.md",
		Code:    "M",
		Kind:    scanner.GitChangeModified,
		Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
	}, 84, diffRenderModeUnified))

	for _, want := range []string{
		"diff --git a/README.md b/README.md",
		"@@ -1,3 +1,3 @@",
		"-old title",
		"+new title",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unified diff output missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Before") || strings.Contains(rendered, "After") {
		t.Fatalf("unified diff output should not render side-by-side column headers: %q", rendered)
	}
}

func TestSyntaxHighlightLexerUsesFilenameHint(t *testing.T) {
	lexer := syntaxHighlightLexer("", "main.go", "if err != nil {\n    return err\n}")
	if lexer == nil {
		t.Fatalf("expected a lexer for Go source")
	}
	if got := strings.ToLower(lexer.Config().Name); !strings.Contains(got, "go") {
		t.Fatalf("lexer name = %q, want a Go lexer", lexer.Config().Name)
	}
}

func TestRenderDiffEntryBodyHighlightsLargeCppDiffByFilename(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	body := "# Untracked\n\n" + strings.Repeat("+int main() { return 0; }\n", syntaxHighlightMaxLines+5)
	cppRendered := renderDiffEntryBody(service.DiffFilePreview{
		Path:      "src/main.cpp",
		Summary:   "src/main.cpp",
		Code:      "??",
		Kind:      scanner.GitChangeUntracked,
		Untracked: true,
		Body:      body,
	}, 84, diffRenderModeSideBySide)
	textRendered := renderDiffEntryBody(service.DiffFilePreview{
		Path:      "notes.txt",
		Summary:   "notes.txt",
		Code:      "??",
		Kind:      scanner.GitChangeUntracked,
		Untracked: true,
		Body:      body,
	}, 84, diffRenderModeSideBySide)

	if ansi.Strip(cppRendered) != ansi.Strip(textRendered) {
		t.Fatalf("syntax highlighting should preserve visible diff text")
	}
	if cppRendered == textRendered {
		t.Fatalf("large .cpp diff should use filename-based syntax highlighting")
	}
}

func TestDiffModeMovesSelectionAndScrollsContent(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.focus = diffFocusFiles
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "README.md",
				Summary:  "README.md",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body:     "# Unstaged\n\n" + strings.Repeat("+line\n", 8),
			},
			{
				Path:      "notes.txt",
				Summary:   "notes.txt",
				Code:      "??",
				Kind:      scanner.GitChangeUntracked,
				Untracked: true,
				Body:      "# Untracked\n\n" + strings.Repeat("+note\n", 40),
			},
		},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(Model)
	if got.diffView.selected != 1 {
		t.Fatalf("selected index = %d, want 1", got.diffView.selected)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.diffView.focus != diffFocusContent {
		t.Fatalf("focus = %s, want content", got.diffView.focus)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.diffView.selected != 1 {
		t.Fatalf("down in content focus should keep file selection, got %d", got.diffView.selected)
	}
	if got.diffView.contentViewport.YOffset == 0 {
		t.Fatalf("down in content focus should scroll the diff viewport")
	}
}

func TestDiffModeEnterOpensSelectedFile(t *testing.T) {
	projectPath := t.TempDir()
	path := filepath.Join(projectPath, "README.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	diffState := newDiffViewState(projectPath, "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		ProjectPath: projectPath,
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("Enter should queue a file open command")
	}
	if got.diffView == nil {
		t.Fatalf("opening a file should leave the diff view visible")
	}
	if got.diffView.focus != diffFocusContent {
		t.Fatalf("Enter should keep the current diff focus")
	}
	if got.status != "Opening README.md" {
		t.Fatalf("status = %q, want opening status", got.status)
	}

	rawMsg := cmd()
	openMsg, ok := rawMsg.(browserOpenMsg)
	if !ok {
		t.Fatalf("file open command returned %T, want browserOpenMsg", rawMsg)
	}
	if openMsg.err != nil {
		t.Fatalf("file open command error = %v", openMsg.err)
	}
	if openMsg.status != "Opened diff file" {
		t.Fatalf("file open status = %q, want success", openMsg.status)
	}
	if opened != path {
		t.Fatalf("opened path = %q, want %q", opened, path)
	}
}

func TestDiffModeAltFRevealsSelectedFileInFolder(t *testing.T) {
	projectPath := t.TempDir()
	dir := filepath.Join(projectPath, "docs")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	path := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write guide.md: %v", err)
	}
	diffState := newDiffViewState(projectPath, "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		ProjectPath: projectPath,
		Files: []service.DiffFilePreview{{
			Path:     "docs/guide.md",
			Summary:  "docs/guide.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	opened := ""
	oldRevealer := externalPathRevealer
	externalPathRevealer = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathRevealer = oldRevealer })

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("Alt+F should queue a containing-folder open command")
	}
	if got.diffView == nil {
		t.Fatalf("opening a folder should leave the diff view visible")
	}
	if got.status != "Opening folder for docs/guide.md" {
		t.Fatalf("status = %q, want folder opening status", got.status)
	}

	rawMsg := cmd()
	openMsg, ok := rawMsg.(browserOpenMsg)
	if !ok {
		t.Fatalf("folder open command returned %T, want browserOpenMsg", rawMsg)
	}
	if openMsg.err != nil {
		t.Fatalf("folder open command error = %v", openMsg.err)
	}
	if openMsg.status != "Opened containing folder" {
		t.Fatalf("folder open status = %q, want success", openMsg.status)
	}
	if opened != path {
		t.Fatalf("revealed path = %q, want selected file %q", opened, path)
	}
}

func TestDiffViewCachesRenderedEntriesAcrossSelectionChanges(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "main.go",
				Summary:  "main.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/main.go b/main.go
@@ -1,3 +1,4 @@
-func main() {}
+func main() {
+    println("hello")
+}
 `),
			},
			{
				Path:     "diff_view.go",
				Summary:  "diff_view.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/diff_view.go b/diff_view.go
@@ -10,3 +10,5 @@
-return "before"
+if mode == diffRenderModeUnified {
+    return "after"
+}
 `),
			},
		},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	cacheKey0 := diffRenderCacheKey{
		FileIndex: 0,
		Width:     m.diffView.contentViewport.Width,
		Mode:      diffRenderModeSideBySide,
	}
	// Continuous scroll renders all files up front, so both should be cached.
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("initial render cache size = %d, want 2 (continuous scroll caches all files)", got)
	}
	if _, ok := m.diffView.renderCache[cacheKey0]; !ok {
		t.Fatalf("first file should be in the render cache")
	}

	firstContinuous := m.diffView.continuousContent
	m.moveDiffSelectionTo(1)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after selecting second file = %d, want 2", got)
	}

	m.moveDiffSelectionTo(0)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after revisiting first file = %d, want 2", got)
	}
	if m.diffView.continuousContent != firstContinuous {
		t.Fatalf("revisiting a file should reuse the cached continuous content")
	}
}

func TestDiffContinuousContentSeparatesFilesWithRule(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "main.go",
				Summary:  "main.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body:     "# Unstaged\n\n+line\n",
			},
			{
				Path:      "notes.txt",
				Summary:   "notes.txt",
				Code:      "??",
				Kind:      scanner.GitChangeUntracked,
				Untracked: true,
				Body:      "# Untracked\n\n+note\n",
			},
		},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	lines := strings.Split(ansi.Strip(m.diffView.continuousContent), "\n")
	fileHeaderIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "M modified  main.go") {
			fileHeaderIndex = i
			break
		}
	}
	if fileHeaderIndex < 1 {
		t.Fatalf("continuous diff should include a main.go header after a separator: %q", ansi.Strip(m.diffView.continuousContent))
	}
	separator := strings.TrimSpace(lines[fileHeaderIndex-1])
	if separator == "" || strings.Trim(separator, "─") != "" {
		t.Fatalf("line before file header should be a separator rule, got %q", lines[fileHeaderIndex-1])
	}
	if len(m.diffView.continuousOffsets) == 0 || m.diffView.continuousOffsets[0] != fileHeaderIndex-1 {
		t.Fatalf("first file offset = %#v, want separator line %d", m.diffView.continuousOffsets, fileHeaderIndex-1)
	}
}

func TestDiffModeMTogglesRenderMode(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.focus = diffFocusContent
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	if m.diffView.mode != diffRenderModeSideBySide {
		t.Fatalf("default diff mode = %s, want side-by-side", m.diffView.mode)
	}
	if !strings.Contains(ansi.Strip(m.diffView.continuousContent), "Before") {
		t.Fatalf("default diff renderer should start in side-by-side mode: %q", ansi.Strip(m.diffView.continuousContent))
	}

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got := updated.(Model)
	if got.diffView.mode != diffRenderModeUnified {
		t.Fatalf("toggling mode should switch to unified, got %s", got.diffView.mode)
	}
	if !strings.Contains(got.status, "unified") {
		t.Fatalf("status should mention unified mode after toggling: %q", got.status)
	}
	unified := ansi.Strip(got.diffView.continuousContent)
	if strings.Contains(unified, "Before") || strings.Contains(unified, "After") {
		t.Fatalf("unified mode should not show side-by-side column headers: %q", unified)
	}
	if !strings.Contains(unified, "diff --git a/README.md b/README.md") {
		t.Fatalf("unified mode should keep the regular patch text: %q", unified)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if got.diffView.mode != diffRenderModeSideBySide {
		t.Fatalf("second toggle should switch back to side-by-side, got %s", got.diffView.mode)
	}
	if !strings.Contains(ansi.Strip(got.diffView.continuousContent), "Before") {
		t.Fatalf("side-by-side mode should restore the paired columns: %q", ansi.Strip(got.diffView.continuousContent))
	}
}

func TestSyntaxHighlightBlockUsesLanguageHint(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	code := "func main() {\n    if err != nil {\n        return err\n    }\n}\n"
	goRendered := syntaxHighlightBlock(code, "go", "", syntaxHighlightOptions{})
	textRendered := syntaxHighlightBlock(code, "text", "", syntaxHighlightOptions{})

	if ansi.Strip(goRendered) != code || ansi.Strip(textRendered) != code {
		t.Fatalf("syntax highlighting should preserve the visible code text")
	}
	if !strings.Contains(goRendered, "\x1b[") {
		t.Fatalf("language hint should produce ANSI styling: %q", goRendered)
	}
	if goRendered == textRendered {
		t.Fatalf("language hint should render differently from plain text")
	}
	if syntaxHighlightLexer("text", "", code) != nil {
		t.Fatalf("explicit text hint should skip syntax lexing")
	}
}

func TestDiffModeAltUpReturnsToMainList(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("alt+up should close the diff view and return to the main list")
	}
	if got.status != "Focus: project list" {
		t.Fatalf("status = %q, want focus-list status", got.status)
	}
	if cmd != nil {
		t.Fatalf("alt+up should not return another command")
	}
}

func TestDiffModeEscReturnsCachedCommitPreviewWhenStateMatches(t *testing.T) {
	ctx := context.Background()
	projectPath, svc := newCommitPreviewReturnTestRepo(t, ctx)

	preview, err := svc.PrepareCommit(ctx, projectPath, service.GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	preview.Message = "Cached preview should survive"

	diffState := newDiffViewState(projectPath, "repo")
	diffState.loading = false
	diffState.returnToCommitPreview = &commitPreviewReturnState{
		preview:         preview,
		messageOverride: "",
	}

	m := Model{
		ctx:      ctx,
		svc:      svc,
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("esc should close the diff view before restoring commit preview")
	}
	if got.commitPreview == nil {
		t.Fatalf("esc should restore the cached commit preview shell")
	}
	if !got.commitPreviewRefreshing {
		t.Fatalf("esc should mark the commit preview as refreshing while the hash check runs")
	}
	if got.status != "Refreshing commit preview..." {
		t.Fatalf("status = %q, want refreshing commit preview", got.status)
	}
	if cmd == nil {
		t.Fatalf("esc should return a resume command")
	}

	msg := cmd()
	previewMsg, ok := msg.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", msg)
	}
	if previewMsg.err != nil {
		t.Fatalf("resume commit preview returned error: %v", previewMsg.err)
	}
	if previewMsg.preview.Message != "Cached preview should survive" {
		t.Fatalf("resume should reuse the cached preview when the hash matches, got message %q", previewMsg.preview.Message)
	}

	updated, _ = got.Update(previewMsg)
	got = updated.(Model)
	if got.commitPreviewRefreshing {
		t.Fatalf("commit preview should stop refreshing once the preview is ready")
	}
	if got.commitPreview == nil || got.commitPreview.Message != "Cached preview should survive" {
		t.Fatalf("cached commit preview should be restored, got %#v", got.commitPreview)
	}
}

func TestDiffModeEscRefreshesCommitPreviewWhenStateChanges(t *testing.T) {
	ctx := context.Background()
	projectPath, svc := newCommitPreviewReturnTestRepo(t, ctx)

	preview, err := svc.PrepareCommit(ctx, projectPath, service.GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	preview.Message = "Cached preview should be replaced"

	diffState := newDiffViewState(projectPath, "repo")
	diffState.loading = false
	diffState.returnToCommitPreview = &commitPreviewReturnState{
		preview:         preview,
		messageOverride: "",
	}

	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("keep this too\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "notes.txt")

	m := Model{
		ctx:      ctx,
		svc:      svc,
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("esc should return a resume command")
	}

	msg := cmd()
	previewMsg, ok := msg.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", msg)
	}
	if previewMsg.err != nil {
		t.Fatalf("resume commit preview returned error: %v", previewMsg.err)
	}
	if previewMsg.preview.Message == "Cached preview should be replaced" {
		t.Fatalf("resume should rebuild the commit preview after git state changes")
	}
	if len(previewMsg.preview.Included) != 2 {
		t.Fatalf("refreshed preview should include both staged files, got %#v", previewMsg.preview.Included)
	}

	updated, _ = got.Update(previewMsg)
	got = updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("refreshed commit preview should be restored")
	}
	if got.commitPreview.Message == "Cached preview should be replaced" {
		t.Fatalf("visible commit preview should come from a refresh, got cached message %q", got.commitPreview.Message)
	}
}

func TestDiffModeDashStartsStageToggle(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	got := updated.(Model)
	if !got.diffView.loading {
		t.Fatalf("pressing - should put the diff view into loading mode")
	}
	if got.status != "Staging selected file..." {
		t.Fatalf("status = %q, want staging status", got.status)
	}
	if cmd == nil {
		t.Fatalf("pressing - should return a toggle command")
	}
}

func TestSlashCommandModeTakesPriorityOverDiffView(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(Model)
	if !got.commandMode {
		t.Fatalf("pressing / in diff mode should open command mode")
	}
	if got.diffView == nil {
		t.Fatalf("opening command mode from diff should keep the diff view active until a command replaces it")
	}
	if cmd == nil {
		t.Fatalf("opening command mode should return a blink command")
	}

	got.commandInput.SetValue("/help")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"
	got.settingsBaseline = &settings
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if !got.helpChatMode {
		t.Fatalf("command mode should open Help Chat once /help is submitted")
	}
	if got.showHelp {
		t.Fatalf("/help should not open the static quick help panel")
	}
}

func TestCommitPreviewMsgClosesDiffView(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, _ := m.Update(commitPreviewMsg{
		preview: service.CommitPreview{
			Intent:      service.GitActionCommit,
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Branch:      "master",
			StageMode:   service.GitStageAllChanges,
			DiffStat:    "1 file changed",
			DiffSummary: "README.md",
			Message:     "demo commit",
		},
		projectPath: "/tmp/demo",
		intent:      service.GitActionCommit,
	})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("commit preview should replace the diff view once ready")
	}
	if got.commitPreview == nil {
		t.Fatalf("commit preview should be stored when the preview message arrives")
	}
}

func TestCommitPreviewMsgLogsCommitMessageFallbackError(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
		width:  100,
		height: 24,
	}

	updated, _ := m.Update(commitPreviewMsg{
		preview: service.CommitPreview{
			Intent:             service.GitActionCommit,
			ProjectPath:        "/tmp/demo",
			ProjectName:        "demo",
			Branch:             "master",
			StageMode:          service.GitStageAllChanges,
			StateHash:          "state-1",
			Message:            "Update demo",
			CommitMessageError: "model mlx-community/Qwen3.5-35B-A3B-4bit: EOF",
		},
		projectPath: "/tmp/demo",
		intent:      service.GitActionCommit,
	})
	got := updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("commit preview should be stored when the preview message arrives")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Commit message fallback used" {
		t.Fatalf("error log status = %q", entry.Status)
	}
	if entry.Message != "model mlx-community/Qwen3.5-35B-A3B-4bit: EOF" {
		t.Fatalf("error log message = %q", entry.Message)
	}
	if entry.RootCause != "EOF" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if !strings.Contains(got.status, "AI fallback") || !strings.Contains(got.status, "/errors") {
		t.Fatalf("status = %q, want AI fallback hint with /errors", got.status)
	}
}

func TestCommitPreviewMsgEscalatesInsufficientBalanceFallbackError(t *testing.T) {
	errText := `openai responses api 402 Payment Required: {"error":{"message":"Insufficient credits"}}`
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
		width:  100,
		height: 24,
	}

	updated, _ := m.Update(commitPreviewMsg{
		preview: service.CommitPreview{
			Intent:             service.GitActionCommit,
			ProjectPath:        "/tmp/demo",
			ProjectName:        "demo",
			Branch:             "master",
			StageMode:          service.GitStageAllChanges,
			StateHash:          "state-1",
			Message:            "Update demo",
			CommitMessageError: errText,
		},
		projectPath: "/tmp/demo",
		intent:      service.GitActionCommit,
	})
	got := updated.(Model)
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != aiBalanceErrorStatus {
		t.Fatalf("error log status = %q, want %q", entry.Status, aiBalanceErrorStatus)
	}
	if entry.Message != errText {
		t.Fatalf("error log message = %q", entry.Message)
	}
	if got.status != aiBalanceErrorStatus+" (use /errors)" {
		t.Fatalf("status = %q, want visible balance alert", got.status)
	}
	if aiStatus := commitPreviewAIStatusText(*got.commitPreview); !strings.Contains(aiStatus, "balance insufficient") {
		t.Fatalf("commit preview AI status = %q, want balance-specific inline status", aiStatus)
	}
}

func TestRenderCommitPreviewContentShowsAIFallbackStatusInline(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:             service.GitActionCommit,
			ProjectName:        "demo",
			ProjectPath:        "/tmp/demo",
			Branch:             "master",
			StageMode:          service.GitStageStagedOnly,
			Message:            "Update demo",
			CommitMessageError: "commit assistant not configured for selected AI backend",
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 8))
	if !strings.Contains(rendered, "AI: AI failed; fallback subject used; /errors has details") {
		t.Fatalf("renderCommitPreviewContent() should show inline AI fallback guidance: %q", rendered)
	}
}

func mustTestPNG(fill color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func newCommitPreviewReturnTestRepo(t *testing.T, ctx context.Context) (string, *service.Service) {
	t.Helper()

	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nbase\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	return projectPath, service.New(config.Default(), st, events.NewBus(), nil)
}

func runTUITestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}
