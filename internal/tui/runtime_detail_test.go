package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/brand"
	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderDetailContentKeepsRuntimeInSeparatePane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(72))
	if strings.Contains(rendered, "Run cmd:") || strings.Contains(rendered, "Runtime:") {
		t.Fatalf("renderDetailContent() should leave runtime fields to the runtime pane: %q", rendered)
	}
}

func TestRuntimePaneShowsRuntimeOutputAndActions(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.Start(projectrun.StartRequest{
		ProjectPath: dir,
		Command:     "printf 'ready on http://127.0.0.1:4310/\\nwarming up\\n'; sleep 2",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForRuntimeSnapshot(t, manager, dir, func(snapshot projectrun.Snapshot) bool {
		return len(snapshot.RecentOutput) >= 2 && len(snapshot.AnnouncedURLs) >= 1
	})

	m := Model{
		width:  100,
		height: 28,
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected:         0,
		visibility:       visibilityAllFolders,
		runtimeManager:   manager,
		runtimeSnapshots: make(map[string]projectrun.Snapshot),
	}
	cmd := m.openRuntimeInspectorForSelection()
	if cmd == nil {
		t.Fatalf("openRuntimeInspectorForSelection() should queue a runtime cache refresh")
	}
	updated, followup := m.update(cmd())
	m = updated.(Model)
	if followup != nil {
		t.Fatalf("runtime pane refresh should not queue a follow-up without an in-flight refresh")
	}

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Runtime - demo") {
		t.Fatalf("View() should show the runtime pane title: %q", rendered)
	}
	if !strings.Contains(rendered, "ready on http://127.0.0.1:4310/") || !strings.Contains(rendered, "warming up") {
		t.Fatalf("View() should show runtime output in the runtime pane: %q", rendered)
	}
	if !strings.Contains(rendered, "Open URL") || !strings.Contains(rendered, "Restart") || !strings.Contains(rendered, "Stop") {
		t.Fatalf("View() should show runtime pane actions: %q", rendered)
	}
	if !strings.Contains(rendered, "Focus: runtime") {
		t.Fatalf("View() should show runtime focus in the footer: %q", rendered)
	}
}

func TestRuntimeSnapshotRefreshKeepsPrimaryAndProcessList(t *testing.T) {
	projectPath := "/tmp/demo"
	now := time.Now()
	snapshots := []projectrun.Snapshot{
		{
			ID:          "default",
			Default:     true,
			ProjectPath: projectPath,
			Command:     "pnpm dev",
			Running:     false,
			StartedAt:   now.Add(-2 * time.Minute),
		},
		{
			ID:          "rt_1",
			Name:        "frontend",
			ProjectPath: projectPath,
			Command:     "pnpm dev",
			Running:     true,
			StartedAt:   now.Add(-time.Minute),
		},
		{
			ID:          "rt_2",
			Name:        "emulators",
			ProjectPath: projectPath,
			Command:     "firebase emulators:start",
			Running:     true,
			StartedAt:   now,
		},
	}

	m := Model{
		runtimeSnapshots:        cloneRuntimeSnapshots(snapshots),
		runtimeProcessSnapshots: cloneRuntimeProcessSnapshots(snapshots),
	}
	if got := m.projectRuntimeSnapshot(projectPath); got.ID != "rt_2" {
		t.Fatalf("primary runtime snapshot ID = %q, want newest running process", got.ID)
	}
	if got := m.runningRuntimeCount(); got != 2 {
		t.Fatalf("runningRuntimeCount() = %d, want 2", got)
	}
	if got := m.projectRuntimeSnapshots(projectPath); len(got) != 3 {
		t.Fatalf("projectRuntimeSnapshots() len = %d, want 3: %+v", len(got), got)
	}
}

func TestRuntimePaneSwitchesBetweenManagedProcesses(t *testing.T) {
	projectPath := "/tmp/demo"
	now := time.Now()
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          projectPath,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          projectPath,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected: 0,
		runtimeProcessSnapshots: []projectrun.Snapshot{
			{
				ID:           "rt_1",
				Name:         "frontend",
				ProjectPath:  projectPath,
				Command:      "pnpm dev",
				Running:      true,
				StartedAt:    now.Add(-time.Minute),
				RecentOutput: []string{"frontend ready"},
			},
			{
				ID:           "rt_2",
				Name:         "emulators",
				ProjectPath:  projectPath,
				Command:      "firebase emulators:start",
				Running:      true,
				StartedAt:    now,
				RecentOutput: []string{"emulator ready"},
			},
		},
		runtimeSnapshots:       map[string]projectrun.Snapshot{},
		runtimeProcessSelected: make(map[string]string),
		focusedPane:            focusRuntime,
		runtimeViewport:        viewport.New(60, 5),
	}
	m.runtimeSnapshots = cloneRuntimeSnapshots(m.runtimeProcessSnapshots)

	m.syncRuntimeViewport(true)
	rendered := ansi.Strip(m.renderRuntimePanel(80, 14))
	if !strings.Contains(rendered, "Process") || !strings.Contains(rendered, "rt_2 emulators") || !strings.Contains(rendered, "emulator ready") {
		t.Fatalf("runtime pane should show newest running process by default: %q", rendered)
	}

	m.selectRuntimeProcess(1)
	m.syncRuntimeViewport(true)
	rendered = ansi.Strip(m.renderRuntimePanel(80, 14))
	if !strings.Contains(rendered, "rt_1 frontend") || !strings.Contains(rendered, "frontend ready") {
		t.Fatalf("runtime pane should switch to the next process: %q", rendered)
	}
}

func TestRuntimePaneListsMultipleLocalListeners(t *testing.T) {
	projectPath := "/tmp/okmain"
	project := model.ProjectSummary{
		Name:          "okmain",
		Path:          projectPath,
		PresentOnDisk: true,
	}
	m := Model{
		width:                  100,
		height:                 28,
		projects:               []model.ProjectSummary{project},
		allProjects:            []model.ProjectSummary{project},
		selected:               0,
		visibility:             visibilityAllFolders,
		runtimeSnapshots:       map[string]projectrun.Snapshot{},
		runtimeProcessSelected: make(map[string]string),
		processReports: map[string]procinspect.ProjectReport{
			projectPath: {
				ProjectPath: projectPath,
				Instances: []procinspect.ProjectInstance{
					{Process: procinspect.Process{PID: 15448, PGID: 15448, Command: "node tune.mjs", Ports: []int{9878}}, ProjectPath: projectPath},
					{Process: procinspect.Process{PID: 18471, PGID: 18471, Command: "node tune.mjs", Ports: []int{9877}}, ProjectPath: projectPath},
					{Process: procinspect.Process{PID: 63523, PGID: 63523, Command: "node tune.mjs", Ports: []int{9879}}, ProjectPath: projectPath},
				},
			},
		},
	}

	rendered := ansi.Strip(m.renderRuntimePanel(100, 18))
	for _, want := range []string{
		"Local listeners",
		"tune.mjs pid 18471 on 9877",
		"tune.mjs pid 15448 on 9878",
		"tune.mjs pid 63523 on 9879",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderRuntimePanel() missing %q:\n%s", want, rendered)
		}
	}
}

func TestStopRuntimeOpensExternalProcessConfirmation(t *testing.T) {
	projectPath := "/tmp/demo"
	m := modelWithExternalProcess(projectPath, 4321, 4017)

	updated, cmd := m.handleStopRuntime(m.projects[0])
	if cmd != nil {
		t.Fatalf("handleStopRuntime() command = %v, want nil before confirmation", cmd)
	}
	got := updated.(Model)
	if got.externalStopConfirm == nil {
		t.Fatalf("external stop confirmation was not opened")
	}
	if got.externalStopConfirm.Selected != externalProcessStopConfirmFocusKeep {
		t.Fatalf("default external stop selection = %d, want keep", got.externalStopConfirm.Selected)
	}

	rendered := ansi.Strip(got.renderExternalProcessStopConfirmOverlay("", 100, 24))
	for _, want := range []string{"Stop External Process", "PID", "4321", "Ports", "4017", "python3 -m http.server 4017"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("external stop confirmation missing %q: %q", want, rendered)
		}
	}
}

func TestRuntimePaneShowsExternalListenerAndStopConfirms(t *testing.T) {
	projectPath := "/tmp/demo"
	m := modelWithExternalProcess(projectPath, 4321, 4017)
	m.focusedPane = focusRuntime
	m.runtimeViewport = viewport.New(80, 5)

	m.syncRuntimeViewport(true)
	rendered := ansi.Strip(m.renderRuntimePanel(80, 14))
	for _, want := range []string{"local listener pid 4321", "External listener output is not captured", "Open URL", "Restart", "Stop"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime pane missing %q: %q", want, rendered)
		}
	}

	actions := m.runtimePanelActions(projectPath)
	if len(actions) != 3 {
		t.Fatalf("runtime actions len = %d, want 3", len(actions))
	}
	if actions[1].Enabled {
		t.Fatalf("restart should be disabled for external listeners")
	}
	if !actions[2].Enabled {
		t.Fatalf("stop should be enabled for external listener with PID")
	}

	m.runtimeActionSelected = 2
	cmd := m.activateRuntimePaneAction()
	if cmd != nil {
		t.Fatalf("external runtime stop action should wait for confirmation, got command")
	}
	if m.externalStopConfirm == nil || m.externalStopConfirm.PID != 4321 {
		t.Fatalf("external stop confirmation = %#v, want PID 4321", m.externalStopConfirm)
	}
}

func TestExternalProcessStopConfirmInvokesTerminatorAfterStopSelection(t *testing.T) {
	oldTerminator := externalProcessTerminator
	defer func() { externalProcessTerminator = oldTerminator }()

	calledPID := 0
	externalProcessTerminator = func(pid int) error {
		calledPID = pid
		return nil
	}

	m := modelWithExternalProcess("/tmp/demo", 4321, 4017)
	m.externalStopConfirm = &externalProcessStopConfirmState{
		ProjectPath: "/tmp/demo",
		ProjectName: "demo",
		PID:         4321,
		Ports:       []int{4017},
		Selected:    externalProcessStopConfirmFocusKeep,
	}

	updated, cmd := m.updateExternalProcessStopConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("keep selection should not stop external process")
	}
	if calledPID != 0 {
		t.Fatalf("terminator called for keep selection with PID %d", calledPID)
	}
	if got.externalStopConfirm != nil {
		t.Fatalf("keep selection should close confirmation")
	}

	m.externalStopConfirm = &externalProcessStopConfirmState{
		ProjectPath: "/tmp/demo",
		ProjectName: "demo",
		PID:         4321,
		Ports:       []int{4017},
		Selected:    externalProcessStopConfirmFocusStop,
	}
	updated, cmd = m.updateExternalProcessStopConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("stop selection should return a stop command")
	}
	if got.externalStopConfirm == nil || !got.externalStopConfirm.Submitting {
		t.Fatalf("confirmation should be marked submitting before stop result")
	}

	updated, _ = got.update(cmd())
	got = updated.(Model)
	if calledPID != 4321 {
		t.Fatalf("terminator PID = %d, want 4321", calledPID)
	}
	if got.externalStopConfirm != nil {
		t.Fatalf("successful stop should close confirmation")
	}
	if !strings.Contains(got.status, "Requested stop for external process PID 4321") {
		t.Fatalf("status = %q, want external stop status", got.status)
	}
}

func modelWithExternalProcess(projectPath string, pid, port int) Model {
	project := model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
	}
	process := procinspect.Process{
		PID:     pid,
		PPID:    1,
		PGID:    pid,
		Command: "python3 -m http.server 4017",
		CWD:     filepath.Join(projectPath, "_site"),
		Ports:   []int{port},
	}
	return Model{
		width:                  100,
		height:                 28,
		projects:               []model.ProjectSummary{project},
		allProjects:            []model.ProjectSummary{project},
		selected:               0,
		visibility:             visibilityAllFolders,
		runtimeSnapshots:       map[string]projectrun.Snapshot{},
		runtimeProcessSelected: make(map[string]string),
		processReports: map[string]procinspect.ProjectReport{
			projectPath: {
				ProjectPath: projectPath,
				Instances: []procinspect.ProjectInstance{{
					Process:     process,
					ProjectPath: projectPath,
				}},
			},
		},
	}
}

func TestRenderRuntimePaneShowsControlRoomFlairWhenEmpty(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	rendered := m.renderRuntimePanel(41, 10)
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Control Room - demo") {
		t.Fatalf("renderRuntimePanel() should show the control room header: %q", stripped)
	}
	if !strings.Contains(stripped, "Standby. Use /run or /run-edit.") {
		t.Fatalf("renderRuntimePanel() should show the wake-room hint: %q", stripped)
	}
	if strings.Contains(stripped, "Output") {
		t.Fatalf("renderRuntimePanel() should use the dedicated flair layout instead of the generic output box: %q", stripped)
	}
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("renderRuntimePanel() should use truecolor pixel styling for the idle scene: %q", rendered)
	}
	if !strings.Contains(rendered, "\u2580") && !strings.Contains(rendered, "\u2584") && !strings.Contains(rendered, "\u2588") {
		t.Fatalf("renderRuntimePanel() should include pixel block glyphs in the idle scene: %q", rendered)
	}
}

func TestRenderRuntimePaneControlRoomFlairAnimates(t *testing.T) {
	base := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	renderedA := base.renderRuntimePanel(41, 10)
	base.spinnerFrame = 12
	renderedB := base.renderRuntimePanel(41, 10)

	if renderedA == renderedB {
		t.Fatalf("renderRuntimePanel() should animate the control room scene across spinner frames")
	}
	linesA := strings.Split(ansi.Strip(renderedA), "\n")
	linesB := strings.Split(ansi.Strip(renderedB), "\n")
	if len(linesA) != len(linesB) {
		t.Fatalf("control room render line count changed across frames: %d vs %d", len(linesA), len(linesB))
	}
	if linesA[0] != linesB[0] {
		t.Fatalf("control room header should stay stable while animating: %q vs %q", linesA[0], linesB[0])
	}
	if linesA[len(linesA)-1] != linesB[len(linesB)-1] {
		t.Fatalf("control room footer should stay stable while animating: %q vs %q", linesA[len(linesA)-1], linesB[len(linesB)-1])
	}
}

func TestRenderRuntimePaneFallsBackToTextWhenTooNarrowForFlair(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderRuntimePanel(22, 8))
	if !strings.Contains(rendered, "Runtime - demo") {
		t.Fatalf("renderRuntimePanel() should fall back to the standard runtime summary when the pane is too narrow: %q", rendered)
	}
	if !strings.Contains(rendered, "Use /run, /start, or /") {
		t.Fatalf("renderRuntimePanel() should keep the original empty-runtime guidance when flair is unavailable: %q", rendered)
	}
	if strings.Contains(rendered, "Control Room - demo") {
		t.Fatalf("renderRuntimePanel() should not force the control room flair into a cramped pane: %q", rendered)
	}
}

func waitForRuntimeSnapshot(t *testing.T, manager *projectrun.Manager, projectPath string, ready func(projectrun.Snapshot) bool) projectrun.Snapshot {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := projectrun.WaitUntilRunning(ctx, manager, projectPath); err != nil {
		t.Fatalf("WaitUntilRunning() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if ready(snapshot) {
			return snapshot
		}
		time.Sleep(50 * time.Millisecond)
	}

	snapshot, err := manager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("runtime snapshot never reached expected state: %+v", snapshot)
	return projectrun.Snapshot{}
}

func waitForRuntimeStopped(t *testing.T, manager *projectrun.Manager, projectPath string) projectrun.Snapshot {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if !snapshot.Running {
			return snapshot
		}
		time.Sleep(50 * time.Millisecond)
	}

	snapshot, err := manager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("runtime did not stop: %+v", snapshot)
	return projectrun.Snapshot{}
}

func TestRenderDetailContentShowsTODOSection(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:           "demo",
			Path:           "/tmp/demo",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			OpenTODOCount:  2,
			TotalTODOCount: 2,
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo", OpenTODOCount: 2, TotalTODOCount: 2},
			Todos: []model.TodoItem{
				{ID: 1, ProjectPath: "/tmp/demo", Text: "Line one"},
				{ID: 2, ProjectPath: "/tmp/demo", Text: "Line two"},
			},
		},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(60))
	if !strings.Contains(rendered, "TODO") {
		t.Fatalf("renderDetailContent() should include a TODO section: %q", rendered)
	}
	if !strings.Contains(rendered, "[ ] Line one") || !strings.Contains(rendered, "[ ] Line two") {
		t.Fatalf("renderDetailContent() should render open TODO items: %q", rendered)
	}
}

func TestViewStacksListAndDetailVertically(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
			RepoDirty:                        true,
			RepoSyncStatus:                   model.RepoSyncAhead,
			RepoAheadCount:                   2,
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
		width:  100,
		height: 24,
	}

	rendered := m.View()
	if !strings.Contains(rendered, "╯\n╭") {
		t.Fatalf("View() should stack list and detail panes vertically: %q", rendered)
	}
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Repo: dirty, ahead 2 (master)") {
		t.Fatalf("View() should show combined repo status in the detail pane: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("View() should fold the current branch into the repo line, got %q", rendered)
	}
	if !strings.Contains(rendered, "Runtime - demo") && !strings.Contains(rendered, "Control Room - demo") {
		t.Fatalf("View() should render the runtime pane beside the detail pane: %q", rendered)
	}
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("View() line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
		if strings.HasPrefix(line, "╭") && !strings.HasSuffix(strings.TrimRight(line, " "), "╮") {
			t.Fatalf("View() top border should keep its right edge visible: %q", line)
		}
		if strings.HasPrefix(line, "╰") && !strings.HasSuffix(strings.TrimRight(line, " "), "╯") {
			t.Fatalf("View() bottom border should keep its right edge visible: %q", line)
		}
	}
}

func TestRenderDetailAssessmentOmitsConfidence(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:     model.ClassificationCompleted,
				Category:   model.SessionCategoryNeedsFollowUp,
				Confidence: 0.91,
				Summary:    "A concrete next step still remains.",
			},
		},
	}

	rendered := m.renderDetailContent(80)
	if !strings.Contains(rendered, "followup") {
		t.Fatalf("renderDetailContent() missing formatted category label: %q", rendered)
	}
	if strings.Contains(rendered, "91%") {
		t.Fatalf("renderDetailContent() still shows confidence: %q", rendered)
	}
	if strings.Contains(rendered, "- needs follow-up") {
		t.Fatalf("renderDetailContent() still repeats assessment category in summary section: %q", rendered)
	}
}

func TestRenderDetailSimplifiesStateAndAttention(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		}},
		selected: 0,
	}

	rendered := m.renderDetailContent(80)
	if strings.Contains(rendered, "State:") {
		t.Fatalf("renderDetailContent() still shows State label: %q", rendered)
	}
	if !strings.Contains(rendered, "Assessment:") {
		t.Fatalf("renderDetailContent() missing Assessment label: %q", rendered)
	}
	if !strings.Contains(rendered, "waiting") {
		t.Fatalf("renderDetailContent() missing assessment-based label: %q", rendered)
	}
	if strings.Contains(rendered, "(idle)") {
		t.Fatalf("renderDetailContent() should no longer combine assessment with parenthetical activity: %q", rendered)
	}
	if strings.Contains(rendered, "Activity:") {
		t.Fatalf("renderDetailContent() should hide idle activity noise: %q", rendered)
	}
	if strings.Contains(rendered, "Status:") {
		t.Fatalf("renderDetailContent() should not show a generic Status field: %q", rendered)
	}
	if strings.Contains(rendered, "Attention status:") {
		t.Fatalf("renderDetailContent() still shows separate attention status line: %q", rendered)
	}
	if !strings.Contains(rendered, "Attention:") {
		t.Fatalf("renderDetailContent() missing attention score field: %q", rendered)
	}
}

func TestRenderDetailShowsActivityWhenItAddsSignal(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusPossiblyStuck,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryInProgress,
		}},
		selected: 0,
	}

	rendered := m.renderDetailContent(80)
	if !strings.Contains(rendered, "Activity:") {
		t.Fatalf("renderDetailContent() should show non-idle activity: %q", rendered)
	}
	if !strings.Contains(rendered, "stuck") {
		t.Fatalf("renderDetailContent() missing non-idle activity value: %q", rendered)
	}
	foundCombinedRow := false
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if strings.Contains(line, "Assessment:") && strings.Contains(line, "Activity:") {
			foundCombinedRow = true
			break
		}
	}
	if !foundCombinedRow {
		t.Fatalf("renderDetailContent() should place assessment and activity on the same row when there is room: %q", rendered)
	}
}

func TestRenderDetailContentShowsRepoConflict(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/repo",
			Name:          "repo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
			RepoBranch:    "feat/worktree-ux",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Conflict:") {
		t.Fatalf("renderDetailContent() missing conflict field: %q", rendered)
	}
	if !strings.Contains(rendered, "Unmerged files are present") {
		t.Fatalf("renderDetailContent() missing conflict explanation: %q", rendered)
	}
	if !strings.Contains(rendered, "Use /resolve") {
		t.Fatalf("renderDetailContent() missing /resolve guidance: %q", rendered)
	}
	if !strings.Contains(rendered, "Repo: conflict") {
		t.Fatalf("renderDetailContent() should surface repo conflict state: %q", rendered)
	}
}

func TestRenderDetailContentOmitsRemoteSyncForLinkedWorktree(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:           "/tmp/repo--lane",
			Name:           "repo--lane",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			WorktreeKind:   model.WorktreeKindLinked,
			RepoBranch:     "feat/worktree-ux",
			RepoSyncStatus: model.RepoSyncNoUpstream,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "no upstream") {
		t.Fatalf("renderDetailContent() should omit remote sync copy for linked worktrees: %q", rendered)
	}
}

func TestRenderDetailShowsAssessmentStageTiming(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 3, 10, 12, 0, 37, 0, time.UTC)
		},
		projects: []model.ProjectSummary{{
			Path:                             "/tmp/demo",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			LatestSessionFormat:              "modern",
			LatestSessionClassification:      model.ClassificationRunning,
			LatestSessionClassificationStage: model.ClassificationStageWaitingForModel,
			LatestSessionClassificationStageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			LatestCompletedSessionClassificationType:  model.SessionCategoryWaitingForUser,
			LatestCompletedSessionSummary:             "Waiting on a design decision before coding resumes.",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:         model.ClassificationRunning,
				Stage:          model.ClassificationStageWaitingForModel,
				StageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Assessment: waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing assessment progress label: %q", rendered)
	}
	if !strings.Contains(rendered, "Summary: assessment waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing assessment progress summary: %q", rendered)
	}
}

func TestRenderDetailWrapsLongSessionSummary(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
			LatestSessionSummary:            "This is a deliberately long session summary that should wrap inside the detail pane instead of clipping off the edge.",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "This is a deliberately long session summary that should wrap inside the detail pane instead of clipping off the edge.",
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(40))
	if !strings.Contains(rendered, "Summary: This is a deliberately long") {
		t.Fatalf("renderDetailContent() missing wrapped summary start: %q", rendered)
	}
	if !strings.Contains(rendered, "         wrap inside the detail pane") {
		t.Fatalf("renderDetailContent() missing wrapped summary continuation: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("wrapped detail line width = %d, want <= 40: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderDetailMissingProjectShowsForgetHint(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   false,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(80))
	if !strings.Contains(rendered, "Use /remove to take this missing folder off the dashboard.") {
		t.Fatalf("renderDetailContent() missing /remove guidance for missing folders: %q", rendered)
	}
}

func TestRenderDetailShowsMissingLinkedWorktreeGuidance(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo--feature",
			Name:                            "demo--feature",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   false,
			WorktreeKind:                    model.WorktreeKindLinked,
			WorktreeRootPath:                "/tmp/demo",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(120))
	if !strings.Contains(rendered, "Use /remove to clean up this missing linked worktree.") || !strings.Contains(rendered, "x and /wt remove still work too.") {
		t.Fatalf("renderDetailContent() missing linked worktree guidance for missing folders: %q", rendered)
	}
}

func TestRenderDetailMergesLastActivityAndSource(t *testing.T) {
	lastActivity := time.Date(2026, 3, 7, 12, 34, 56, 0, time.UTC)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusActive,
			PresentOnDisk:                   true,
			LastActivity:                    lastActivity,
			LatestSessionFormat:             "modern",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryInProgress,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(80))
	if !strings.Contains(rendered, "Last activity: 2026-03-07T12:34:56Z  Codex") {
		t.Fatalf("renderDetailContent() should keep source inline with last activity: %q", rendered)
	}
	if strings.Contains(rendered, "Latest source:") {
		t.Fatalf("renderDetailContent() still shows separate latest source field: %q", rendered)
	}
	if strings.Contains(rendered, "Extras:") {
		t.Fatalf("renderDetailContent() still shows extras field: %q", rendered)
	}
}

func TestRenderDetailShowsRecentMoveMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                             "/new",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			MovedFromPath:                    "/old",
			MovedAt:                          now.Add(-2 * time.Hour),
			LatestSessionDetectedProjectPath: "/old",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Moved from: /old") {
		t.Fatalf("renderDetailContent() should show recent move origin: %q", rendered)
	}
	if !strings.Contains(rendered, "Moved at:") {
		t.Fatalf("renderDetailContent() should show recent move timestamp: %q", rendered)
	}
}

func TestRenderDetailOmitsStaleMoveMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                             "/new",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			MovedFromPath:                    "/old",
			MovedAt:                          now.Add(-48 * time.Hour),
			LatestSessionDetectedProjectPath: "/old",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "Moved from: /old") {
		t.Fatalf("renderDetailContent() should hide stale move origin: %q", rendered)
	}
	if strings.Contains(rendered, "Moved at:") {
		t.Fatalf("renderDetailContent() should hide stale move timestamp: %q", rendered)
	}
}

func TestViewWithLongDetailRowsRespectsHeight(t *testing.T) {
	movedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	oldPath := "/workspaces/repos/BatonDeck"
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/workspaces/repos/LittleControlRoom",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			LastActivity:                     time.Date(2026, 3, 10, 17, 26, 9, 0, time.FixedZone("JST", 9*60*60)),
			LatestSessionClassification:      model.ClassificationRunning,
			LatestSessionClassificationType:  model.SessionCategoryInProgress,
			LatestSessionSummary:             "Investigation in progress.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: oldPath,
			MovedFromPath:                    oldPath,
			MovedAt:                          movedAt,
		}},
		selected:   0,
		status:     "Loaded 1 projects (attention, AI folders)",
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
		width:      80,
		height:     24,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationRunning,
				Category: model.SessionCategoryInProgress,
				Summary:  "Investigation in progress.",
			},
		},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(lines[0], brand.Name) {
		t.Fatalf("View() should keep the app title visible: %q", lines[0])
	}
	if strings.Contains(lines[0], brand.Subtitle) {
		t.Fatalf("View() should omit the old subtitle from the top line: %q", lines[0])
	}
	if !strings.Contains(lines[0], "Loaded 1 projects") {
		t.Fatalf("View() should merge the status into the top line: %q", lines[0])
	}
	if len(lines) > 1 && strings.Contains(lines[1], "Loaded 1 projects") {
		t.Fatalf("View() should not render a separate second status line anymore: %q", lines[1])
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("View() line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
}

func TestSplitBodyHeightsGrowFocusedPane(t *testing.T) {
	listListHeight, listDetailHeight := splitBodyHeights(20, focusProjects)
	if listListHeight <= listDetailHeight {
		t.Fatalf("projects focus should favor list pane: list=%d detail=%d", listListHeight, listDetailHeight)
	}

	detailListHeight, detailDetailHeight := splitBodyHeights(20, focusDetail)
	if detailDetailHeight <= detailListHeight {
		t.Fatalf("detail focus should favor detail pane: list=%d detail=%d", detailListHeight, detailDetailHeight)
	}

	runtimeListHeight, runtimeBottomHeight := splitBodyHeights(20, focusRuntime)
	if runtimeBottomHeight <= runtimeListHeight {
		t.Fatalf("runtime focus should favor bottom panes: list=%d bottom=%d", runtimeListHeight, runtimeBottomHeight)
	}
}

func TestTabSwitchesFocusAndEscReturnsToList(t *testing.T) {
	m := Model{
		focusedPane: focusProjects,
		width:       100,
		height:      24,
	}
	m.syncDetailViewport(false)

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.focusedPane != focusDetail {
		t.Fatalf("tab should move focus to detail, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.focusedPane != focusRuntime {
		t.Fatalf("second tab should move focus to runtime, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyShiftTab})
	got = updated.(Model)
	if got.focusedPane != focusDetail {
		t.Fatalf("shift+tab should move focus back to detail, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.focusedPane != focusProjects {
		t.Fatalf("esc should return focus to list, got %s", got.focusedPane)
	}
}

func TestSlashOpensCommandMode(t *testing.T) {
	m := Model{
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(Model)
	if !got.commandMode {
		t.Fatalf("slash should open command mode")
	}
	if got.commandInput.Value() != "/" {
		t.Fatalf("command input = %q, want /", got.commandInput.Value())
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Command Palette") {
		t.Fatalf("View() missing command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "Suggestions") {
		t.Fatalf("View() missing command suggestions section: %q", rendered)
	}
}

func TestRuntimeCommandFocusesRuntimePane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected:    0,
		width:       100,
		height:      24,
		focusedPane: focusProjects,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRuntime})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/runtime should focus locally without an async command")
	}
	if got.focusedPane != focusRuntime {
		t.Fatalf("/runtime should focus the runtime pane, got %s", got.focusedPane)
	}
	if got.status != "Focus: runtime pane" {
		t.Fatalf("status = %q, want runtime focus status", got.status)
	}
}

func TestCPUCommandOpensProcessInspector(t *testing.T) {
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

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCPU})
	got := updated.(Model)
	if got.cpuDialog == nil {
		t.Fatalf("/cpu should open the CPU inspector")
	}
	if !got.cpuDialog.Loading {
		t.Fatalf("dialog should start in loading state")
	}
	if cmd == nil {
		t.Fatalf("/cpu should queue an async CPU scan")
	}
}

func TestPortsCommandOpensPortsInspector(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPorts})
	got := updated.(Model)
	if got.portsDialog == nil {
		t.Fatalf("/ports should open the ports inspector")
	}
	if !got.portsDialog.Loading {
		t.Fatalf("dialog should start in loading state")
	}
	if cmd == nil {
		t.Fatalf("/ports should queue an async process scan")
	}
}

func TestQuitKeyStopsManagedRuntimes(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.Start(projectrun.StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForRuntimeSnapshot(t, manager, dir, func(snapshot projectrun.Snapshot) bool {
		return snapshot.Running
	})

	m := Model{runtimeManager: manager}
	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("quit key should return tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit key command should emit tea.QuitMsg")
	}
	_ = updated.(Model)

	snapshot := waitForRuntimeStopped(t, manager, dir)
	if snapshot.Running {
		t.Fatalf("runtime should be stopped after quit: %+v", snapshot)
	}
}
