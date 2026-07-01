package tui

import (
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

type mergeConflictResolveTargetMsg struct {
	project    model.ProjectSummary
	target     service.GitlinkConflictResolveTarget
	hasGitlink bool
	err        error
}

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
	if m.svc != nil {
		m.status = "Preparing merge conflict resolver..."
		return m, m.resolveMergeConflictTargetCmd(project)
	}

	return m.launchMergeConflictResolver(project)
}

func (m Model) resolveMergeConflictTargetCmd(project model.ProjectSummary) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		target, ok, err := m.svc.GitlinkConflictResolveTarget(ctx, project.Path)
		err = timeoutActionError(err, tuiGitActionTimeout, "finding gitlink conflict resolver target")
		return mergeConflictResolveTargetMsg{
			project:    project,
			target:     target,
			hasGitlink: ok,
			err:        err,
		}
	}
}

func (m Model) applyMergeConflictResolveTargetMsg(msg mergeConflictResolveTargetMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "Resolve target lookup failed: " + msg.err.Error()
		m.err = msg.err
		return m, nil
	}
	if msg.hasGitlink {
		return m.launchGitlinkConflictResolver(msg.project, msg.target)
	}
	return m.launchMergeConflictResolver(msg.project)
}

func (m Model) launchMergeConflictResolver(project model.ProjectSummary) (tea.Model, tea.Cmd) {
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

func (m Model) launchGitlinkConflictResolver(parent model.ProjectSummary, target service.GitlinkConflictResolveTarget) (tea.Model, tea.Cmd) {
	provider := m.preferredEmbeddedProviderForProject(parent)
	project := model.ProjectSummary{
		Path:          target.WorktreePath,
		Name:          gitlinkConflictResolveProjectName(target),
		PresentOnDisk: true,
		RepoBranch:    target.Branch,
		RepoDirty:     true,
		RepoConflict:  true,
	}
	if message, blocked := m.controlFreshSessionBlockedByActiveEngineerTurn(project, provider, "the submodule conflict worktree"); blocked {
		m.status = message
		return m, nil
	}
	return m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: true,
		prompt:   gitlinkConflictResolvePrompt(parent, target),
		reveal:   true,
	})
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

func gitlinkConflictResolveProjectName(target service.GitlinkConflictResolveTarget) string {
	if path := strings.TrimSpace(target.SubmodulePath); path != "" {
		return filepath.Base(filepath.FromSlash(path)) + " merge"
	}
	if path := strings.TrimSpace(target.WorktreePath); path != "" {
		return filepath.Base(path)
	}
	return "submodule merge"
}

func gitlinkConflictResolvePrompt(parent model.ProjectSummary, target service.GitlinkConflictResolveTarget) string {
	parentPath := strings.TrimSpace(target.ParentRepoPath)
	if parentPath == "" {
		parentPath = strings.TrimSpace(parent.Path)
	}
	lines := []string{
		"Resolve the submodule content conflicts for the current parent Git gitlink merge conflict.",
		"",
		"Context:",
		"- Parent repo: " + parentPath,
		"- Submodule path: " + strings.TrimSpace(target.SubmodulePath),
		"- Submodule merge worktree: " + strings.TrimSpace(target.WorktreePath),
		"- Submodule merge branch: " + strings.TrimSpace(target.Branch),
	}
	if branch := strings.TrimSpace(firstNonEmptyTrimmed(target.ParentBranch, parent.RepoBranch)); branch != "" {
		lines = append(lines, "- Parent branch: "+branch)
	}
	if base := strings.TrimSpace(target.Base); base != "" {
		lines = append(lines, "- Base gitlink SHA: "+base)
	}
	if ours := strings.TrimSpace(target.Ours); ours != "" {
		lines = append(lines, "- Ours gitlink SHA: "+ours)
	}
	if theirs := strings.TrimSpace(target.Theirs); theirs != "" {
		lines = append(lines, "- Theirs gitlink SHA: "+theirs)
	}
	lines = append(lines,
		"",
		"Instructions:",
		"- Work only inside the submodule merge worktree for this resolver session.",
		"- Inspect `git status --short` and the conflicted files before editing.",
		"- Resolve all submodule content conflicts carefully, preserving the intended changes from both sides.",
		"- Use `git add` only for files you resolved inside the submodule merge worktree.",
		"- After the submodule conflicts are resolved, commit the submodule merge on its current branch and push that branch upstream.",
		"- Stage the parent repo gitlink to the new submodule merge commit with `git -C <parent repo> update-index --add --cacheinfo 160000,<merge sha>,<submodule path>`.",
		"- Do not commit the parent repo merge, abort either merge, remove the merge worktree, or rename branches.",
		"- Run an appropriate focused verification if one is obvious and cheap; otherwise explain what was skipped.",
		"- Finish by reporting the conflicted files handled, verification run, and the remaining `git status --short` state.",
	)
	return strings.Join(lines, "\n")
}
