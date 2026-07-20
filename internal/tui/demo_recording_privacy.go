package tui

import "strings"

// DemoRecordingPrivate reports whether the currently rendered LCR surface can
// expose a project category that the user marked private. The demo recorder
// consumes this state after rendering and substitutes a fixed mask in the saved
// recording; it does not change what the local operator sees.
func (m Model) DemoRecordingPrivate() bool {
	if m.archiveMode == projectArchiveCategory {
		if category, ok := m.projectCategoryByID(m.selectedCategoryID); ok && category.Private {
			return true
		}
	}

	projectPath := strings.TrimSpace(m.codexVisibleProject)
	if m.codexPendingOpenVisible() {
		projectPath = m.codexPendingOpenProject()
	}
	return m.demoRecordingProjectPrivate(projectPath)
}

func (m Model) demoRecordingProjectPrivate(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	project, ok := m.projectSummaryByPathAllProjects(projectPath)
	if !ok {
		return false
	}
	if project.CategoryPrivate {
		return true
	}
	if category, found := m.projectCategoryByID(project.CategoryID); found && category.Private {
		return true
	}

	rootPath := strings.TrimSpace(project.WorktreeRootPath)
	if rootPath == "" || normalizeProjectPath(rootPath) == normalizeProjectPath(projectPath) {
		return false
	}
	root, found := m.projectSummaryByPathAllProjects(rootPath)
	if !found {
		return false
	}
	if root.CategoryPrivate {
		return true
	}
	category, found := m.projectCategoryByID(root.CategoryID)
	return found && category.Private
}
