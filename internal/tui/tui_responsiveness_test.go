package tui

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"
)

func TestRenderProjectListUsesDeliveredSnapshotWithoutReadingLiveSession(t *testing.T) {
	projectPath := "/tmp/render-cache"
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    projectPath,
			Started:        true,
			Busy:           true,
			ActiveTurnID:   "live-turn",
			BusySince:      time.Now().Add(-time.Minute),
			LastActivityAt: time.Now(),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	project := model.ProjectSummary{
		Path:          projectPath,
		Name:          "render-cache",
		PresentOnDisk: true,
	}
	m := Model{
		allProjects:  []model.ProjectSummary{project},
		projects:     []model.ProjectSummary{project},
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:       codexapp.ProviderCodex,
				ProjectPath:    projectPath,
				Started:        true,
				Busy:           true,
				ActiveTurnID:   "cached-turn",
				BusySince:      time.Now().Add(-time.Minute),
				LastActivityAt: time.Now(),
			},
		},
	}
	beforeSnapshot := session.snapshotCalls
	beforeTrySnapshot := session.trySnapshotCalls
	beforeState := session.stateSnapshotCalls
	beforeTryState := session.tryStateSnapshotCalls

	_ = m.renderProjectList(120, 5)

	if session.snapshotCalls != beforeSnapshot || session.trySnapshotCalls != beforeTrySnapshot ||
		session.stateSnapshotCalls != beforeState || session.tryStateSnapshotCalls != beforeTryState {
		t.Fatalf("render read live session: snapshot=%d/%d try=%d/%d state=%d/%d tryState=%d/%d",
			beforeSnapshot, session.snapshotCalls,
			beforeTrySnapshot, session.trySnapshotCalls,
			beforeState, session.stateSnapshotCalls,
			beforeTryState, session.tryStateSnapshotCalls)
	}
}

func TestSelectedDetailReloadOnlyRunsForLatestSelection(t *testing.T) {
	projects := []model.ProjectSummary{
		{Path: "/tmp/first", Name: "first"},
		{Path: "/tmp/second", Name: "second"},
	}
	m := Model{projects: projects, selected: 0}
	if cmd := m.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("first selection should schedule a debounced detail request")
	}
	firstSeq := m.selectedDetailRequestSeq
	m.selected = 1
	if cmd := m.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("second selection should schedule a debounced detail request")
	}
	secondSeq := m.selectedDetailRequestSeq

	staleModel, staleCmd := m.Update(selectedDetailReloadMsg{path: projects[0].Path, seq: firstSeq})
	stale := normalizeUpdateModel(staleModel)
	if staleCmd != nil || len(stale.detailReloadInFlight) != 0 {
		t.Fatalf("stale selection started detail work: cmd=%v inFlight=%#v", staleCmd != nil, stale.detailReloadInFlight)
	}

	latestModel, latestCmd := stale.Update(selectedDetailReloadMsg{path: projects[1].Path, seq: secondSeq})
	latest := normalizeUpdateModel(latestModel)
	if latestCmd == nil || !latest.detailReloadInFlight[projects[1].Path] {
		t.Fatalf("latest selection did not start detail work: cmd=%v inFlight=%#v", latestCmd != nil, latest.detailReloadInFlight)
	}
	if latest.selectedDetailInFlight != projects[1].Path {
		t.Fatalf("selected detail in flight = %q, want %q", latest.selectedDetailInFlight, projects[1].Path)
	}
}

func TestSelectedDetailReloadKeepsOnlyLatestProjectQueued(t *testing.T) {
	projects := []model.ProjectSummary{
		{Path: "/tmp/first", Name: "first"},
		{Path: "/tmp/second", Name: "second"},
		{Path: "/tmp/third", Name: "third"},
	}
	m := Model{projects: projects, selected: 0}

	if cmd := m.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("first selection should schedule a debounced detail request")
	}
	firstSeq := m.selectedDetailRequestSeq
	firstModel, firstCmd := m.Update(selectedDetailReloadMsg{path: projects[0].Path, seq: firstSeq})
	first := normalizeUpdateModel(firstModel)
	if firstCmd == nil || first.selectedDetailInFlight != projects[0].Path {
		t.Fatalf("first detail load did not start: cmd=%v selectedInFlight=%q", firstCmd != nil, first.selectedDetailInFlight)
	}

	first.selected = 1
	if cmd := first.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("second selection should schedule a debounced detail request")
	}
	secondSeq := first.selectedDetailRequestSeq
	secondModel, secondCmd := first.Update(selectedDetailReloadMsg{path: projects[1].Path, seq: secondSeq})
	second := normalizeUpdateModel(secondModel)
	if secondCmd != nil {
		t.Fatal("second selection should wait for the active selected-detail load")
	}
	if second.selectedDetailQueuedPath != projects[1].Path || second.selectedDetailQueuedSeq != secondSeq {
		t.Fatalf("selected detail queue = (%q, %d), want (%q, %d)",
			second.selectedDetailQueuedPath, second.selectedDetailQueuedSeq, projects[1].Path, secondSeq)
	}

	second.selected = 2
	if cmd := second.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("third selection should schedule a debounced detail request")
	}
	thirdSeq := second.selectedDetailRequestSeq
	thirdModel, thirdCmd := second.Update(selectedDetailReloadMsg{path: projects[2].Path, seq: thirdSeq})
	third := normalizeUpdateModel(thirdModel)
	if thirdCmd != nil {
		t.Fatal("third selection should replace the queued selection, not start another load")
	}
	if third.selectedDetailQueuedPath != projects[2].Path || third.selectedDetailQueuedSeq != thirdSeq {
		t.Fatalf("selected detail queue = (%q, %d), want latest (%q, %d)",
			third.selectedDetailQueuedPath, third.selectedDetailQueuedSeq, projects[2].Path, thirdSeq)
	}
	if len(third.detailReloadInFlight) != 1 || !third.detailReloadInFlight[projects[0].Path] {
		t.Fatalf("detail reloads = %#v, want only the first project in flight", third.detailReloadInFlight)
	}
	if len(third.detailReloadQueued) != 0 {
		t.Fatalf("generic per-project queue should stay empty, got %#v", third.detailReloadQueued)
	}

	completedModel, followUp := third.Update(detailMsg{
		path:   projects[0].Path,
		detail: model.ProjectDetail{Summary: projects[0]},
	})
	completed := normalizeUpdateModel(completedModel)
	if followUp == nil {
		t.Fatal("completing the first load should start the latest queued selection")
	}
	if completed.selectedDetailInFlight != projects[2].Path {
		t.Fatalf("selected detail in flight = %q, want latest %q", completed.selectedDetailInFlight, projects[2].Path)
	}
	if completed.selectedDetailQueuedPath != "" || completed.selectedDetailQueuedSeq != 0 {
		t.Fatalf("selected detail queue was not cleared: path=%q seq=%d",
			completed.selectedDetailQueuedPath, completed.selectedDetailQueuedSeq)
	}
	if len(completed.detailReloadInFlight) != 1 || !completed.detailReloadInFlight[projects[2].Path] {
		t.Fatalf("detail reloads = %#v, want only the latest project in flight", completed.detailReloadInFlight)
	}
}

func TestSelectedDetailReloadAttachesToExplicitRefreshWithoutQueuingDuplicate(t *testing.T) {
	project := model.ProjectSummary{Path: "/tmp/current", Name: "current"}
	m := Model{
		projects:             []model.ProjectSummary{project},
		selected:             0,
		detailReloadInFlight: map[string]bool{project.Path: true},
	}
	if cmd := m.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("selection should schedule a debounced detail request")
	}
	seq := m.selectedDetailRequestSeq

	attachedModel, cmd := m.Update(selectedDetailReloadMsg{path: project.Path, seq: seq})
	attached := normalizeUpdateModel(attachedModel)
	if cmd != nil {
		t.Fatal("selected detail should attach to the explicit refresh already in flight")
	}
	if attached.selectedDetailInFlight != project.Path {
		t.Fatalf("selected detail in flight = %q, want %q", attached.selectedDetailInFlight, project.Path)
	}
	if attached.detailReloadQueued[project.Path] {
		t.Fatal("attaching selected detail should not queue a duplicate per-project refresh")
	}

	completedModel, followUp := attached.Update(detailMsg{
		path:   project.Path,
		detail: model.ProjectDetail{Summary: project},
	})
	completed := normalizeUpdateModel(completedModel)
	if followUp != nil {
		t.Fatal("attached selected-detail completion should not schedule duplicate work")
	}
	if completed.selectedDetailInFlight != "" {
		t.Fatalf("selected detail still marked in flight for %q", completed.selectedDetailInFlight)
	}
	if len(completed.detailReloadInFlight) != 0 {
		t.Fatalf("detail reloads still in flight: %#v", completed.detailReloadInFlight)
	}
}

func TestSelectedDetailReloadDoesNotRepeatActiveProject(t *testing.T) {
	project := model.ProjectSummary{Path: "/tmp/current", Name: "current"}
	m := Model{projects: []model.ProjectSummary{project}, selected: 0}

	if cmd := m.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("first selection should schedule a debounced detail request")
	}
	firstSeq := m.selectedDetailRequestSeq
	activeModel, activeCmd := m.Update(selectedDetailReloadMsg{path: project.Path, seq: firstSeq})
	active := normalizeUpdateModel(activeModel)
	if activeCmd == nil {
		t.Fatal("first selected-detail load should start")
	}

	if cmd := active.requestSelectedProjectDetailViewCmd(); cmd == nil {
		t.Fatal("repeated selection should schedule its debounce timer")
	}
	repeatedSeq := active.selectedDetailRequestSeq
	queuedModel, queuedCmd := active.Update(selectedDetailReloadMsg{path: project.Path, seq: repeatedSeq})
	queued := normalizeUpdateModel(queuedModel)
	if queuedCmd != nil {
		t.Fatal("repeated selection should use the active detail load")
	}
	if queued.selectedDetailQueuedPath != project.Path {
		t.Fatalf("selected detail queued path = %q, want %q", queued.selectedDetailQueuedPath, project.Path)
	}

	completedModel, followUp := queued.Update(detailMsg{
		path:   project.Path,
		detail: model.ProjectDetail{Summary: project},
	})
	completed := normalizeUpdateModel(completedModel)
	if followUp != nil {
		t.Fatal("active detail result should also fulfill the repeated selection")
	}
	if completed.selectedDetailInFlight != "" || completed.selectedDetailQueuedPath != "" {
		t.Fatalf("selected detail coordinator was not cleared: inFlight=%q queued=%q",
			completed.selectedDetailInFlight, completed.selectedDetailQueuedPath)
	}
	if len(completed.detailReloadInFlight) != 0 || len(completed.detailReloadQueued) != 0 {
		t.Fatalf("generic detail coordinator was not cleared: inFlight=%#v queued=%#v",
			completed.detailReloadInFlight, completed.detailReloadQueued)
	}
}

func TestSpinnerTickAdvancesMarqueeTwoColumns(t *testing.T) {
	m := Model{marqueeOffset: 7}

	nextModel, _ := m.Update(spinnerTickMsg{})
	next := normalizeUpdateModel(nextModel)

	if got, want := next.marqueeOffset, 7+marqueeColumnsPerTick; got != want {
		t.Fatalf("marquee offset = %d, want %d", got, want)
	}
}

func TestSpinnerTickCadenceKeepsBackgroundRefreshesThrottled(t *testing.T) {
	if got, want := spinnerTickInterval, 200*time.Millisecond; got != want {
		t.Fatalf("spinner tick interval = %s, want %s", got, want)
	}

	tests := []struct {
		name       string
		everyTicks int
		want       time.Duration
	}{
		{name: "runtime snapshots", everyTicks: runtimeSnapshotRefreshEveryTicks, want: time.Second},
		{name: "CPU snapshots", everyTicks: cpuSnapshotRefreshEveryTicks, want: 3 * time.Second},
		{name: "process scans", everyTicks: processScanRefreshEveryTicks, want: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := time.Duration(tt.everyTicks) * spinnerTickInterval; got != tt.want {
				t.Fatalf("refresh cadence = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRebuildProjectListIndexesWorktreeFamiliesAndTabs(t *testing.T) {
	root := model.ProjectSummary{Path: "/tmp/root", Name: "root", PresentOnDisk: true}
	child := model.ProjectSummary{
		Path:             "/tmp/root-child",
		Name:             "child",
		PresentOnDisk:    true,
		WorktreeRootPath: root.Path,
		WorktreeKind:     model.WorktreeKindLinked,
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, child},
		visibility:  visibilityAllFolders,
		archiveMode: projectArchiveMain,
		sortMode:    sortByRecent,
	}
	m.rebuildProjectList(root.Path)

	if got := len(m.worktreeFamily(root.Path)); got != 2 {
		t.Fatalf("worktree family size = %d, want 2", got)
	}
	if got := m.projectTabCount(projectTabDescriptor{mode: projectArchiveMain}); got != 2 {
		t.Fatalf("main tab count = %d, want 2", got)
	}
}
