package tui

import (
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) resolveMergeConflictsForSelection() (tea.Model, tea.Cmd) {
	project, ok := m.mergeConflictResolveTargetProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	if !project.PresentOnDisk {
		m.status = "Resolve requires a folder present on disk"
		return m, nil
	}
	if !projectUsesRepoUI(project) {
		m.status = "Resolve requires a Git-backed project"
		return m, nil
	}
	if !project.RepoConflict {
		m.status = "No merge conflict detected for the selected project"
		return m, nil
	}

	provider := mergeConflictResolveControlProvider(m.preferredEmbeddedProviderForProject(project))
	outcome := m.executeEngineerSendPromptControlWithOutcome(control.EngineerSendPromptInput{
		ProjectPath: project.Path,
		ProjectName: projectNameForPicker(project, project.Path),
		Provider:    provider,
		SessionMode: control.SessionModeNew,
		Prompt:      mergeConflictResolvePrompt(project),
		Reveal:      true,
	})
	return outcome.model, outcome.cmd
}

func (m Model) mergeConflictResolveTargetProject() (model.ProjectSummary, bool) {
	if projectPath := strings.TrimSpace(m.codexVisibleProject); projectPath != "" {
		if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
			return project, true
		}
	}
	return m.selectedProject()
}

func mergeConflictResolveControlProvider(provider codexapp.Provider) control.Provider {
	switch provider.Normalized() {
	case codexapp.ProviderOpenCode:
		return control.ProviderOpenCode
	case codexapp.ProviderLCAgent:
		return control.ProviderLCAgent
	default:
		return control.ProviderCodex
	}
}

func mergeConflictResolvePrompt(project model.ProjectSummary) string {
	location := "repo"
	if project.WorktreeKind == model.WorktreeKindLinked {
		location = "linked worktree"
	}

	lines := []string{
		"Resolve the current Git merge conflicts in this " + location + ".",
	}
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		lines = append(lines, "Current branch: "+branch)
	}
	if project.WorktreeKind == model.WorktreeKindLinked {
		if target := strings.TrimSpace(project.WorktreeParentBranch); target != "" {
			lines = append(lines, "Parent branch for merge-back: "+target)
		}
	}
	lines = append(lines,
		"",
		"Instructions:",
		"- Inspect `git status --short` and the conflicted files before editing.",
		"- Resolve all unmerged files carefully, preserving the intended changes from both sides.",
		"- Use `git add` only for files you resolved, if needed to clear unmerged state.",
		"- Do not commit, push, abort the in-progress Git operation, or rename branches.",
		"- Run an appropriate focused verification if one is obvious and cheap; otherwise explain what was skipped.",
		"- Finish by reporting the conflicted files handled, verification run, and the remaining `git status --short` state.",
	)
	return strings.Join(lines, "\n")
}
