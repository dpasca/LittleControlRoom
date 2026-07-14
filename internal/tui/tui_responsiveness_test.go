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
