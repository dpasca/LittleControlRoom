package tui

import (
	"testing"

	"lcroom/internal/model"
)

func TestDemoRecordingPrivateForSelectedPrivateCategory(t *testing.T) {
	t.Parallel()

	m := Model{
		archiveMode:        projectArchiveCategory,
		selectedCategoryID: "private",
		projectCategories: []model.ProjectCategory{
			{ID: "private", Name: "Private", Private: true},
		},
	}
	if !m.DemoRecordingPrivate() {
		t.Fatal("selected private category was not masked")
	}

	m.selectedCategoryID = "public"
	m.projectCategories = append(m.projectCategories, model.ProjectCategory{ID: "public", Name: "Public"})
	if m.DemoRecordingPrivate() {
		t.Fatal("selected public category was masked")
	}
}

func TestDemoRecordingPrivateForVisibleEmbeddedPrivateProject(t *testing.T) {
	t.Parallel()

	privatePath := "/tmp/private-project"
	publicPath := "/tmp/public-project"
	m := Model{
		archiveMode:         projectArchiveMain,
		codexVisibleProject: privatePath,
		allProjects: []model.ProjectSummary{
			{Path: privatePath, Name: "Private project", CategoryID: "private", CategoryPrivate: true},
			{Path: publicPath, Name: "Public project"},
		},
	}
	if !m.DemoRecordingPrivate() {
		t.Fatal("visible embedded private project was not masked")
	}

	m.codexVisibleProject = publicPath
	if m.DemoRecordingPrivate() {
		t.Fatal("visible embedded public project was masked")
	}
}

func TestDemoRecordingPrivateForVisiblePendingPrivateProject(t *testing.T) {
	t.Parallel()

	privatePath := "/tmp/private-project"
	m := Model{
		archiveMode: projectArchiveMain,
		allProjects: []model.ProjectSummary{
			{Path: privatePath, Name: "Private project", CategoryPrivate: true},
		},
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      privatePath,
			showWhilePending: true,
		},
	}
	if !m.DemoRecordingPrivate() {
		t.Fatal("visible pending private project was not masked")
	}

	m.codexPendingOpen.showWhilePending = false
	if m.DemoRecordingPrivate() {
		t.Fatal("background pending private project was masked despite not being visible")
	}
}

func TestDemoRecordingPrivateInheritsPrivateWorktreeRoot(t *testing.T) {
	t.Parallel()

	rootPath := "/tmp/private-root"
	worktreePath := "/tmp/private-root--task"
	m := Model{
		archiveMode:         projectArchiveMain,
		codexVisibleProject: worktreePath,
		allProjects: []model.ProjectSummary{
			{Path: rootPath, Name: "Private root", CategoryPrivate: true},
			{Path: worktreePath, Name: "Task worktree", WorktreeRootPath: rootPath},
		},
	}
	if !m.DemoRecordingPrivate() {
		t.Fatal("embedded worktree of a private root was not masked")
	}
}
