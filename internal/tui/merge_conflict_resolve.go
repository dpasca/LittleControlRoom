package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
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

type mergeConflictResolverOpenedMsg struct {
	projectPath      string
	provider         codexapp.Provider
	snapshot         codexapp.Snapshot
	reused           bool
	restartIntentKey string
	restartWarmup    bool
	err              error
}

type mergeConflictResolverUpdateMsg struct {
	projectPath string
	snapshot    codexapp.Snapshot
	found       bool
	terminal    bool
	closeErr    error
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
	provider := m.preferredEmbeddedProviderForProject(project)
	return m.launchParallelMergeConflictResolver(project, provider, mergeConflictResolvePrompt(project))
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
	return m.launchParallelMergeConflictResolver(project, provider, gitlinkConflictResolvePrompt(parent, target))
}

func (m Model) launchParallelMergeConflictResolver(project model.ProjectSummary, provider codexapp.Provider, prompt string) (tea.Model, tea.Cmd) {
	return m.launchParallelMergeConflictResolverWithOptions(project, provider, embeddedLaunchOptions{
		forceNew: true,
		prompt:   strings.TrimSpace(prompt),
		reveal:   false,
	}, "")
}

func (m Model) resumeParallelMergeConflictResolver(project model.ProjectSummary, choice suspendedTurnResumeChoice) (tea.Model, tea.Cmd) {
	intent := codexapp.RestartIntent{
		Provider:    choice.Provider,
		ProjectPath: choice.ProjectPath,
		SessionID:   choice.SessionID,
		Parallel:    true,
	}
	return m.launchParallelMergeConflictResolverWithOptions(project, choice.Provider, embeddedLaunchOptions{
		resumeID:                choice.SessionID,
		prompt:                  suspendedTurnContinuationPromptForChoice(choice),
		reveal:                  false,
		continueInterruptedTurn: choice.CapturedOnQuit,
		interruptedTurnID:       choice.ActiveTurnID,
		restartWarmup:           true,
	}, intent.Key())
}

func (m Model) launchParallelMergeConflictResolverWithOptions(project model.ProjectSummary, provider codexapp.Provider, options embeddedLaunchOptions, restartIntentKey string) (tea.Model, tea.Cmd) {
	provider = provider.Normalized()
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	options.prompt = strings.TrimSpace(options.prompt)
	options.reveal = false
	req := m.embeddedLaunchRequest(project, provider, options)
	if options.forceNew {
		req.ResumeID = ""
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.ensureCodexRuntime()
	req = m.enrichEmbeddedLaunchRequest(req)
	manager := m.codexManager
	m.err = nil
	m.rememberEmbeddedProvider(provider)
	m.status = "Starting background " + provider.Label() + " conflict resolver..."
	return m, func() tea.Msg {
		if manager == nil {
			return mergeConflictResolverOpenedMsg{
				projectPath:      project.Path,
				provider:         provider,
				restartIntentKey: restartIntentKey,
				restartWarmup:    options.restartWarmup,
				err:              errors.New(provider.Label() + " manager unavailable"),
			}
		}
		if provider == codexapp.ProviderLCAgent {
			if err := codexapp.CheckLCAgentProviderAccess(context.Background(), req); err != nil {
				return mergeConflictResolverOpenedMsg{
					projectPath:      project.Path,
					provider:         provider,
					restartIntentKey: restartIntentKey,
					restartWarmup:    options.restartWarmup,
					err:              err,
				}
			}
		}
		attemptLimit := 1
		if req.ForceNew {
			attemptLimit = maxForceNewEmbeddedOpenAttempts
		}
		var (
			session codexapp.Session
			reused  bool
			err     error
		)
		for attempt := 1; attempt <= attemptLimit; attempt++ {
			session, reused, err = manager.OpenParallel(req)
			if !shouldRetryFreshEmbeddedOpenError(req, err) || attempt == attemptLimit {
				break
			}
		}
		if err != nil {
			return mergeConflictResolverOpenedMsg{
				projectPath:      project.Path,
				provider:         provider,
				restartIntentKey: restartIntentKey,
				restartWarmup:    options.restartWarmup,
				err:              err,
			}
		}
		snapshot := session.Snapshot()
		if options.continueInterruptedTurn && snapshot.BusyExternal {
			_ = manager.CloseParallelProject(project.Path)
			return mergeConflictResolverOpenedMsg{
				projectPath:      project.Path,
				provider:         provider,
				snapshot:         snapshot,
				restartIntentKey: restartIntentKey,
				restartWarmup:    options.restartWarmup,
				err:              errors.New("saved background resolver is active in another process"),
			}
		}
		return mergeConflictResolverOpenedMsg{
			projectPath:      project.Path,
			provider:         provider,
			snapshot:         snapshot,
			reused:           reused,
			restartIntentKey: restartIntentKey,
			restartWarmup:    options.restartWarmup,
		}
	}
}

func (m Model) applyMergeConflictResolverOpenedMsg(msg mergeConflictResolverOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if msg.restartWarmup {
			m.settleParallelRestartWarmup(msg.projectPath, false)
		}
		m.status = "Background conflict resolver failed: " + msg.err.Error()
		m.reportError("Background conflict resolver failed", msg.err, msg.projectPath)
		return m, nil
	}
	restartAckCmd := tea.Cmd(nil)
	if strings.TrimSpace(msg.restartIntentKey) != "" && !msg.snapshot.BusyExternal {
		restartAckCmd = m.acknowledgeRestartIntentsCmd([]string{msg.restartIntentKey})
	}
	if m.codexManager != nil {
		if _, live := m.codexManager.ParallelSession(msg.projectPath); !live {
			// A very short resolver can finish and be detached before its open
			// result is applied. Keep the terminal status in that race.
			if msg.restartWarmup {
				m.settleParallelRestartWarmup(msg.projectPath, !msg.snapshot.BusyExternal)
			}
			return m, restartAckCmd
		}
	}
	m.err = nil
	if msg.reused {
		m.status = "Background " + msg.provider.Label() + " conflict resolver is already running"
	} else {
		m.status = "Resolving merge conflicts in the background with " + msg.provider.Label()
	}
	if msg.restartWarmup {
		m.settleParallelRestartWarmup(msg.projectPath, !msg.snapshot.BusyExternal)
	}
	return m, batchCmds(restartAckCmd, m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath)))
}

func (m Model) waitMergeConflictResolverUpdateCmd() tea.Cmd {
	manager := m.codexManager
	if manager == nil || manager.ParallelUpdates() == nil {
		return nil
	}
	return func() tea.Msg {
		projectPath, ok := <-manager.ParallelUpdates()
		if !ok {
			return nil
		}
		session, found := manager.ParallelSession(projectPath)
		if !found {
			return mergeConflictResolverUpdateMsg{projectPath: projectPath}
		}
		snapshot := session.Snapshot()
		terminal := snapshot.Closed ||
			snapshot.PendingApproval != nil ||
			snapshot.PendingToolInput != nil ||
			snapshot.PendingElicitation != nil ||
			(snapshot.Started && !embeddedSessionBlocksProviderSwitch(snapshot))
		var closeErr error
		if terminal {
			closeErr = manager.CloseParallelProject(projectPath)
		}
		return mergeConflictResolverUpdateMsg{
			projectPath: projectPath,
			snapshot:    snapshot,
			found:       true,
			terminal:    terminal,
			closeErr:    closeErr,
		}
	}
}

func (m Model) applyMergeConflictResolverUpdateMsg(msg mergeConflictResolverUpdateMsg) (tea.Model, tea.Cmd) {
	if m.codexManager != nil {
		m.codexManager.AckParallelUpdate(msg.projectPath)
	}
	waitCmd := m.waitMergeConflictResolverUpdateCmd()
	if msg.closeErr != nil {
		m.reportError("Background conflict resolver cleanup failed", msg.closeErr, msg.projectPath)
		return m, waitCmd
	}
	if !msg.found || !msg.terminal {
		return m, waitCmd
	}

	provider := embeddedProvider(msg.snapshot)
	projectName := filepath.Base(strings.TrimSpace(msg.projectPath))
	if project, ok := m.projectSummaryByPathAllProjects(msg.projectPath); ok {
		projectName = projectNameForPicker(project, msg.projectPath)
	}
	sessionID := shortID(msg.snapshot.ThreadID)
	if msg.snapshot.PendingApproval != nil || msg.snapshot.PendingToolInput != nil || msg.snapshot.PendingElicitation != nil {
		action := "Open the resolver from /sessions and continue it."
		if sessionID != "" {
			action = "Open resolver session " + sessionID + " from /sessions and continue it."
		}
		m.status = "Background conflict resolver needs attention"
		m.openActionNoticeDialog(
			"Resolver needs attention",
			projectName,
			"The background "+provider.Label()+" resolver paused for input and was detached safely.",
			action,
			"Its conversation is preserved in the normal session history. If another engineer session is open for this project, finish or close that session before reopening the resolver.",
		)
		return m, batchCmds(waitCmd, m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath)))
	}
	if lastErr := strings.TrimSpace(msg.snapshot.LastError); lastErr != "" {
		err := errors.New(lastErr)
		m.status = "Background conflict resolver failed: " + lastErr
		m.reportError("Background conflict resolver failed", err, msg.projectPath)
	} else {
		m.err = nil
		m.status = "Background " + provider.Label() + " conflict resolver finished"
	}
	return m, batchCmds(waitCmd, m.requestProjectInvalidationCmd(invalidateProjectData(msg.projectPath)))
}

func (m Model) mergeConflictResolveTargetProject() (model.ProjectSummary, bool) {
	if projectPath := strings.TrimSpace(m.codexVisibleProject); projectPath != "" {
		if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
			return project, true
		}
	}
	return m.selectedProject()
}

func mergeConflictResolvePrompt(project model.ProjectSummary) string {
	location := "repo"
	if project.WorktreeKind == model.WorktreeKindLinked {
		location = "linked worktree"
	}

	return strings.Join([]string{
		"Resolve the current Git merge conflicts in this " + location + ".",
		"Preserve the intended changes from both sides and keep the current Git operation and branch intact.",
		"If there are no major problems, run focused verification and commit the resolution. Do not push. Report any blocker instead of forcing a questionable resolution.",
	}, "\n")
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
		"Resolve the Git conflicts in this submodule merge worktree.",
		"Parent repo: " + parentPath,
		"Submodule path: " + strings.TrimSpace(target.SubmodulePath),
		"Submodule merge worktree: " + strings.TrimSpace(target.WorktreePath),
		"Submodule merge branch: " + strings.TrimSpace(target.Branch),
	}
	lines = append(lines,
		"Preserve the intended changes from both sides and keep both Git operations and branches intact.",
		"If there are no major problems, run focused verification, commit the submodule merge on its current branch, push that branch, and stage the parent repo gitlink at the resulting commit.",
		"Do not commit the parent repo merge. Report any blocker instead of forcing a questionable resolution.",
	)
	return strings.Join(lines, "\n")
}
