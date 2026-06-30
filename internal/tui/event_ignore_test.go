package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBusClassificationFailureAddsErrorLogEntry(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 5, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status": "failed",
			"stage":  "waiting_for_model",
			"model":  "mlx-community/Qwen3.5-9B-MLX-4bit",
			"error":  "connection refused",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification update should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment failed (use /errors)" {
		t.Fatalf("status = %q, want assessment failure hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment failed")
	}
	if entry.Message != "classification stage waiting for model: model mlx-community/Qwen3.5-9B-MLX-4bit: connection refused" {
		t.Fatalf("error log message = %q", entry.Message)
	}
	if entry.RootCause != "connection refused" {
		t.Fatalf("error log root cause = %q, want %q", entry.RootCause, "connection refused")
	}
	if len(entry.Context) != 2 || entry.Context[0] != "classification stage waiting for model" || entry.Context[1] != "model mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("error log context = %#v", entry.Context)
	}
}

func TestBusClassificationTimeoutUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 20, 22, 32, 5, 0, time.FixedZone("CST", 8*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom--todo-we-v-ebeen-working-a-lot-on-this-project",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 20, 22, 32, 5, 0, time.FixedZone("CST", 8*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "context deadline exceeded",
			"error_kind":      "timeout",
			"error_diagnosis": "request timed out while contacting the model; network connectivity or provider availability may be degraded",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification timeout should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment timed out (use /errors)" {
		t.Fatalf("status = %q, want timeout-specific assessment hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment timed out" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment timed out")
	}
	if entry.RootCause != "context deadline exceeded" {
		t.Fatalf("error log root cause = %q, want %q", entry.RootCause, "context deadline exceeded")
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "request timed out while contacting the model; network connectivity or provider availability may be degraded" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusClassificationConnectionFailureUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 5, 2, 15, 25, 47, 0, time.FixedZone("JST", 9*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 5, 2, 15, 25, 47, 0, time.FixedZone("JST", 9*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "Reconnecting... 5/5",
			"error_kind":      "connection_failed",
			"error_diagnosis": "could not reach the model; network connectivity or provider availability may be degraded",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification connection failure should continue waiting on the bus")
	}
	if got.status != "Assessment connection failed (use /errors)" {
		t.Fatalf("status = %q, want connection-specific assessment hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment connection failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment connection failed")
	}
	if entry.RootCause != "Reconnecting... 5/5" {
		t.Fatalf("error log root cause = %q, want reconnect detail", entry.RootCause)
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "could not reach the model; network connectivity or provider availability may be degraded" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusInsufficientBalanceBackgroundErrorsAreDeduped(t *testing.T) {
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	m := Model{
		nowFn: func() time.Time {
			return now
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
	}
	errText := "todo worktree suggester failed: insufficient balance"

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		At:          now,
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"action":  "todo_worktree_suggestion_failed",
			"todo_id": "1",
			"error":   errText,
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("first event should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Status != aiBalanceErrorStatus {
		t.Fatalf("error log status = %q, want %q", got.errorLogEntries[0].Status, aiBalanceErrorStatus)
	}
	if got.status != aiBalanceErrorStatus+" (use /errors)" {
		t.Fatalf("status = %q, want visible balance alert", got.status)
	}

	now = now.Add(time.Minute)
	updated, _ = got.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		At:          now,
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"action":  "todo_worktree_suggestion_failed",
			"todo_id": "2",
			"error":   errText,
		},
	}))
	got = updated.(Model)
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("duplicate balance error should be suppressed, got %#v", got.errorLogEntries)
	}
	if got.status != aiBalanceErrorStatus+" (use /errors)" {
		t.Fatalf("status = %q, want balance alert retained after duplicate", got.status)
	}
}

func TestBusClassificationOpenFileLimitUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 22, 17, 38, 58, 0, time.FixedZone("CST", 8*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 22, 17, 38, 58, 0, time.FixedZone("CST", 8*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "error creating thread: Fatal error: Failed to initialize session: Too many open files (os error 24)",
			"error_kind":      "open_file_limit",
			"error_diagnosis": "local open-file limit was reached while assessing the latest session; too many helper processes or open files may already be active",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification open-file-limit failure should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment hit open-file limit (use /errors)" {
		t.Fatalf("status = %q, want open-file-limit assessment hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment hit open-file limit" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment hit open-file limit")
	}
	if entry.RootCause != "error creating thread: Fatal error: Failed to initialize session: Too many open files (os error 24)" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "local open-file limit was reached while assessing the latest session; too many helper processes or open files may already be active" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusClassificationBackendUnavailableUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 29, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"error":           "session classifier unavailable: Codex assessment backend is not ready",
			"error_kind":      "backend_unavailable",
			"error_diagnosis": "AI assessment backend is not configured or not ready; open /setup to select a working backend",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification backend-unavailable failure should continue waiting on the bus")
	}
	if got.status != "Assessment backend unavailable (use /errors)" {
		t.Fatalf("status = %q, want backend-unavailable assessment hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Status != "Assessment backend unavailable" {
		t.Fatalf("error log status = %q", got.errorLogEntries[0].Status)
	}
}

func TestBusTodoSuggestionFailureAddsErrorLogEntry(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		At:          time.Date(2026, 4, 5, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"action": "todo_worktree_suggestion_failed",
			"model":  "mlx-community/Qwen3.5-9B-MLX-4bit",
			"error":  "EOF while reading response body",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("todo suggestion failure should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "TODO worktree suggestion failed (use /errors)" {
		t.Fatalf("status = %q, want TODO suggestion failure hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "TODO worktree suggestion failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "TODO worktree suggestion failed")
	}
	if entry.RootCause != "EOF while reading response body" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if len(entry.Context) != 1 || entry.Context[0] != "model mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("error log context = %#v", entry.Context)
	}
}

func TestActionChangesProjectStructure(t *testing.T) {
	for _, action := range []string{"archive_project", "unarchive_project", "forget_project", "remove_worktree", "scratch_task_archived", "scratch_task_deleted"} {
		if !actionChangesProjectStructure(action) {
			t.Fatalf("actionChangesProjectStructure(%q) = false, want true", action)
		}
	}
	for _, action := range []string{"toggle_pin", "git_push", "todo_worktree_suggestion_failed", ""} {
		if actionChangesProjectStructure(action) {
			t.Fatalf("actionChangesProjectStructure(%q) = true, want false", action)
		}
	}
}

func TestBusRemoveWorktreeRefreshTargetsRootDetail(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Name: childPath,
				Path: childPath,
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		ProjectPath: childPath,
		Payload: map[string]string{
			"action":    "remove_worktree",
			"root_path": rootPath,
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("remove-worktree bus event should keep waiting on the bus and queue a refresh")
	}
	if !got.projectsReloadInFlight {
		t.Fatalf("remove-worktree bus event should queue a project list reload")
	}
	if !got.detailReloadInFlight[rootPath] {
		t.Fatalf("detail reload should target root path %q, got %#v", rootPath, got.detailReloadInFlight)
	}
	if got.detailReloadInFlight[childPath] {
		t.Fatalf("detail reload should not target removed worktree path %q", childPath)
	}
}

func TestDispatchRemoveCommandStoresIgnoredPathAndHidesOnlySelectedProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	for _, state := range []model.ProjectState{
		{
			Path:           "/tmp/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 20,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/worktrees/a1/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 15,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/visible-demo",
			Name:           "visible-demo",
			AttentionScore: 10,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("upsert project %s: %v", state.Path, err)
		}
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 3 || m.projects[0].Name != "projects_control_center" {
		t.Fatalf("initial projects = %#v, want removable candidate first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open confirmation before scheduling work")
	}
	if got.projectRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the project removal confirmation")
	}
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusKeep {
		t.Fatalf("default removal confirmation selection = %d, want keep", got.projectRemoveConfirm.Selected)
	}
	rendered := ansi.Strip(got.renderProjectRemoveConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "only this exact project path") || !strings.Contains(rendered, "does not delete files") {
		t.Fatalf("project removal confirmation should explain files are kept, got %q", rendered)
	}

	updated, _ = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusRemove {
		t.Fatalf("tab should move project removal focus to remove")
	}

	updated, cmd = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("project removal confirmation should return a removal command")
	}
	if got.projectRemoveConfirm != nil {
		t.Fatalf("project removal confirmation should close while the background action runs")
	}

	rawMsg := cmd()
	afterAction, reloadCmd := got.Update(rawMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("remove action should trigger a project reload")
	}
	projectsMsg := reloadCmd()
	finalModel, _ := reloaded.Update(projectsMsg)
	saved := finalModel.(Model)
	if len(saved.projects) != 2 || saved.projects[0].Path != "/tmp/worktrees/a1/projects_control_center" || saved.projects[1].Name != "visible-demo" {
		t.Fatalf("visible projects after /remove = %#v, want same-name worktree plus visible-demo", saved.projects)
	}
	if saved.status != `Removed "projects_control_center" from list` {
		t.Fatalf("status = %q, want removal confirmation", saved.status)
	}

	ignoredNames, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignoredNames) != 0 {
		t.Fatalf("ignored names = %#v, want none for path-specific remove", ignoredNames)
	}

	ignored, err := st.ListIgnoredProjects(ctx)
	if err != nil {
		t.Fatalf("list ignored projects: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Scope != model.ProjectIgnoreScopePath || ignored[0].Path != "/tmp/projects_control_center" {
		t.Fatalf("ignored projects = %#v, want exact path /tmp/projects_control_center", ignored)
	}
}

func TestDispatchRemoveCommandForMissingProjectMarksItForgotten(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	missingPath := filepath.Join(t.TempDir(), "missing-demo")
	visiblePath := filepath.Join(t.TempDir(), "visible-demo")
	for _, state := range []model.ProjectState{
		{
			Path:           missingPath,
			Name:           "missing-demo",
			AttentionScore: 20,
			PresentOnDisk:  false,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           visiblePath,
			Name:           "visible-demo",
			AttentionScore: 10,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("upsert project %s: %v", state.Path, err)
		}
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 2 || m.projects[0].Name != "missing-demo" {
		t.Fatalf("initial projects = %#v, want missing project first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open confirmation before removing missing projects")
	}
	if got.projectRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the project removal confirmation")
	}
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusKeep {
		t.Fatalf("default removal confirmation selection = %d, want keep", got.projectRemoveConfirm.Selected)
	}
	rendered := ansi.Strip(got.renderProjectRemoveConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "stale dashboard entry") {
		t.Fatalf("missing project removal confirmation should explain stale entry removal, got %q", rendered)
	}

	updated, _ = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusRemove {
		t.Fatalf("tab should move project removal focus to remove")
	}

	updated, cmd = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("project removal confirmation should return a removal command")
	}
	if got.projectRemoveConfirm != nil {
		t.Fatalf("missing-project removal confirmation should close while the background action runs")
	}

	rawMsg := cmd()
	afterAction, reloadCmd := got.Update(rawMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("remove action should trigger a project reload")
	}
	if reloaded.status != "Removed from list" {
		t.Fatalf("status = %q, want missing-project removal confirmation", reloaded.status)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects after removal: %v", err)
	}
	if len(visibleProjects) != 1 || visibleProjects[0].Path != visiblePath {
		t.Fatalf("visible projects after removing missing project = %#v, want only visible-demo", visibleProjects)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored names after missing-project removal = %#v, want none", ignored)
	}

	detail, err := st.GetProjectDetail(ctx, missingPath, 1)
	if err != nil {
		t.Fatalf("get missing project detail: %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("missing project summary = %#v, want forgotten=true", detail.Summary)
	}
}

func TestDispatchIgnoreCommandStoresIgnoredNameAndHidesProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	for _, state := range []model.ProjectState{
		{
			Path:           "/tmp/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 20,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/worktrees/a1/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 15,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/visible-demo",
			Name:           "visible-demo",
			AttentionScore: 10,
			InScope:        true,
			UpdatedAt:      now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("upsert project %s: %v", state.Path, err)
		}
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 3 || m.projects[0].Name != "projects_control_center" {
		t.Fatalf("initial projects = %#v, want ignored candidate first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnore})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/ignore) should return an ignore command")
	}

	actionMsg := cmd()
	afterAction, reloadCmd := got.Update(actionMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("ignore action should trigger a project reload")
	}
	projectsMsg := reloadCmd()
	finalModel, _ := reloaded.Update(projectsMsg)
	saved := finalModel.(Model)
	if len(saved.projects) != 1 || saved.projects[0].Name != "visible-demo" {
		t.Fatalf("visible projects after /ignore = %#v, want only visible-demo", saved.projects)
	}
	if saved.status != `Ignored "projects_control_center"` {
		t.Fatalf("status = %q, want ignore confirmation", saved.status)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Name != "projects_control_center" {
		t.Fatalf("ignored names = %#v, want projects_control_center", ignored)
	}
}

func TestIgnoredPickerListsAndRestoresIgnoredNames(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.SetIgnoredProjectName(ctx, "projects_control_center", true); err != nil {
		t.Fatalf("seed ignored project name: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnored})
	got := updated.(Model)
	if !got.ignoredPickerVisible || !got.ignoredPickerLoading {
		t.Fatalf("/ignored should open the ignored picker in loading state")
	}
	if cmd == nil {
		t.Fatalf("/ignored should load ignored project names")
	}

	loadedModel, _ := got.Update(cmd())
	loaded := loadedModel.(Model)
	if !loaded.ignoredPickerVisible || loaded.ignoredPickerLoading {
		t.Fatalf("ignored picker should be visible after loading")
	}
	if len(loaded.ignoredPickerItems) != 1 || loaded.ignoredPickerItems[0].Name != "projects_control_center" {
		t.Fatalf("ignored picker items = %#v, want projects_control_center", loaded.ignoredPickerItems)
	}

	nextModel, unignoreCmd := loaded.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if unignoreCmd == nil {
		t.Fatalf("enter in ignored picker should trigger restore")
	}
	if !next.ignoredPickerLoading {
		t.Fatalf("ignored picker should return to loading while restoring")
	}

	restoredModel, _ := next.Update(unignoreCmd())
	restored := restoredModel.(Model)
	if restored.status != `Restored "projects_control_center"` {
		t.Fatalf("status = %q, want restore confirmation", restored.status)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names after restore: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored names after restore = %#v, want none", ignored)
	}
}

func TestIgnoredPickerListsAndRestoresIgnoredPaths(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/projects_control_center"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "projects_control_center",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := st.SetIgnoredProjectPath(ctx, projectPath, true); err != nil {
		t.Fatalf("seed ignored project path: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnored})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("/ignored should load ignored projects")
	}

	loadedModel, _ := got.Update(cmd())
	loaded := loadedModel.(Model)
	if len(loaded.ignoredPickerItems) != 1 || loaded.ignoredPickerItems[0].Scope != model.ProjectIgnoreScopePath || loaded.ignoredPickerItems[0].Path != projectPath {
		t.Fatalf("ignored picker items = %#v, want exact path", loaded.ignoredPickerItems)
	}

	nextModel, restoreCmd := loaded.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if restoreCmd == nil {
		t.Fatalf("enter in ignored picker should restore the path")
	}
	if !next.ignoredPickerLoading {
		t.Fatalf("ignored picker should return to loading while restoring")
	}

	restoredModel, _ := next.Update(restoreCmd())
	restored := restoredModel.(Model)
	if restored.status != `Restored "/tmp/projects_control_center"` {
		t.Fatalf("status = %q, want restore confirmation", restored.status)
	}

	ignored, err := st.ListIgnoredProjects(ctx)
	if err != nil {
		t.Fatalf("list ignored projects after restore: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored projects after restore = %#v, want none", ignored)
	}
}
