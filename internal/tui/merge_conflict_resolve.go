package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

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
	ownerProjectPath string
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

type mergeConflictResolverPhase uint8

const (
	mergeConflictResolverStarting mergeConflictResolverPhase = iota + 1
	mergeConflictResolverRunning
	mergeConflictResolverChecking
	mergeConflictResolverNeedsAttention
	mergeConflictResolverFailed
	mergeConflictResolverRefreshFailed
	mergeConflictResolverConflictsRemain
	mergeConflictResolverResolved
)

// mergeConflictResolverState is the UI-owned, non-blocking projection of a
// background resolver. Render helpers read this cache instead of reaching into
// a live provider session from Bubble Tea's Update/View path.
type mergeConflictResolverState struct {
	OwnerProjectPath   string
	SessionProjectPath string
	Provider           codexapp.Provider
	Phase              mergeConflictResolverPhase
	SessionID          string
	StartedAt          time.Time
	UpdatedAt          time.Time
	Detail             string
}

func (state mergeConflictResolverState) active() bool {
	return state.Phase == mergeConflictResolverStarting || state.Phase == mergeConflictResolverRunning
}

func (state mergeConflictResolverState) provider() codexapp.Provider {
	provider := state.Provider.Normalized()
	if provider == "" {
		return codexapp.ProviderCodex
	}
	return provider
}

func (state mergeConflictResolverState) elapsed(now time.Time) string {
	if state.StartedAt.IsZero() || now.IsZero() || now.Before(state.StartedAt) {
		return ""
	}
	return formatRunningDuration(now.Sub(state.StartedAt))
}

func (state mergeConflictResolverState) summary(now time.Time) string {
	detail := strings.TrimSpace(state.Detail)
	switch state.Phase {
	case mergeConflictResolverStarting:
		return "Starting background conflict resolver"
	case mergeConflictResolverRunning:
		if detail == "" {
			detail = "Resolving merge conflicts"
		}
		if elapsed := state.elapsed(now); elapsed != "" {
			detail += " (" + elapsed + ")"
		}
		return detail
	case mergeConflictResolverChecking:
		return "Resolver finished; refreshing Git status"
	case mergeConflictResolverNeedsAttention:
		if detail == "" {
			return "Resolver needs input"
		}
		return "Resolver needs input: " + detail
	case mergeConflictResolverFailed:
		if detail == "" {
			return "Background resolver failed"
		}
		return "Resolver failed: " + detail
	case mergeConflictResolverRefreshFailed:
		if detail == "" {
			return "Resolver finished; Git status refresh failed"
		}
		return "Resolver finished; Git status refresh failed: " + detail
	case mergeConflictResolverConflictsRemain:
		return "Resolver finished; Git still reports unmerged files"
	case mergeConflictResolverResolved:
		return "Conflicts resolved in the background"
	default:
		return ""
	}
}

func (state mergeConflictResolverState) commandStatus(now time.Time) string {
	providerLabel := state.provider().Label()
	switch state.Phase {
	case mergeConflictResolverStarting:
		return "Background " + providerLabel + " conflict resolver is starting; progress is shown on the project row"
	case mergeConflictResolverRunning:
		status := "Background " + providerLabel + " conflict resolver is still working"
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			status += ": " + detail
		}
		if elapsed := state.elapsed(now); elapsed != "" {
			status += " (" + elapsed + ")"
		}
		return status
	default:
		return state.summary(now)
	}
}

func (state mergeConflictResolverState) repoLabel() string {
	switch state.Phase {
	case mergeConflictResolverStarting:
		return "resolver starting"
	case mergeConflictResolverRunning:
		return "resolver working"
	case mergeConflictResolverChecking:
		return "resolver checking"
	case mergeConflictResolverNeedsAttention:
		return "resolver needs input"
	case mergeConflictResolverFailed:
		return "resolver failed"
	case mergeConflictResolverRefreshFailed:
		return "resolver done; Git status unknown"
	case mergeConflictResolverConflictsRemain:
		return "resolver finished; conflicts remain"
	case mergeConflictResolverResolved:
		return "resolver finished"
	default:
		return ""
	}
}

func (state mergeConflictResolverState) detailText(now time.Time) string {
	providerLabel := state.provider().Label()
	sessionSuffix := ""
	if sessionID := shortID(strings.TrimSpace(state.SessionID)); sessionID != "" {
		sessionSuffix = " · session " + sessionID
	}
	switch state.Phase {
	case mergeConflictResolverStarting:
		return providerLabel + " resolver is starting in the background" + sessionSuffix
	case mergeConflictResolverRunning:
		text := providerLabel + " resolver is working in the background"
		if elapsed := state.elapsed(now); elapsed != "" {
			text += " (" + elapsed + ")"
		}
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			text += " · " + detail
		}
		return text + sessionSuffix
	case mergeConflictResolverChecking:
		return providerLabel + " resolver finished; refreshing the repository's Git state" + sessionSuffix
	case mergeConflictResolverNeedsAttention:
		text := providerLabel + " resolver needs input"
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			text += " · " + detail
		}
		if sessionSuffix != "" {
			text += sessionSuffix + " · open it from /sessions"
		}
		return text
	case mergeConflictResolverFailed:
		text := providerLabel + " resolver failed"
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			text += " · " + detail
		}
		return text + sessionSuffix
	case mergeConflictResolverRefreshFailed:
		text := providerLabel + " resolver finished, but Little Control Room could not refresh Git status"
		if detail := strings.TrimSpace(state.Detail); detail != "" {
			text += " · " + detail
		}
		return text + sessionSuffix
	case mergeConflictResolverConflictsRemain:
		return providerLabel + " resolver finished, but Git still reports unmerged files" + sessionSuffix + " · run /resolve to retry"
	case mergeConflictResolverResolved:
		return providerLabel + " resolver finished and Git no longer reports unmerged files" + sessionSuffix
	default:
		return ""
	}
}

func (m *Model) ensureMergeConflictResolverState() {
	if m.mergeConflictResolvers == nil {
		m.mergeConflictResolvers = make(map[string]mergeConflictResolverState)
	}
}

func (m Model) mergeConflictResolverForProject(projectPath string) (mergeConflictResolverState, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || len(m.mergeConflictResolvers) == 0 {
		return mergeConflictResolverState{}, false
	}
	state, ok := m.mergeConflictResolvers[projectPath]
	return state, ok
}

func (m Model) mergeConflictResolverOwnerForSession(sessionProjectPath string) string {
	sessionProjectPath = normalizeProjectPath(sessionProjectPath)
	if sessionProjectPath == "" {
		return ""
	}
	for ownerPath, state := range m.mergeConflictResolvers {
		if normalizeProjectPath(state.SessionProjectPath) == sessionProjectPath {
			return ownerPath
		}
	}
	return sessionProjectPath
}

func (m *Model) markMergeConflictResolverStarting(ownerProjectPath, sessionProjectPath string, provider codexapp.Provider) {
	m.ensureMergeConflictResolverState()
	ownerProjectPath = normalizeProjectPath(ownerProjectPath)
	sessionProjectPath = normalizeProjectPath(sessionProjectPath)
	if ownerProjectPath == "" {
		ownerProjectPath = sessionProjectPath
	}
	if sessionProjectPath == "" {
		sessionProjectPath = ownerProjectPath
	}
	if ownerProjectPath == "" {
		return
	}
	if existing, ok := m.mergeConflictResolvers[ownerProjectPath]; ok &&
		existing.active() &&
		normalizeProjectPath(existing.SessionProjectPath) == sessionProjectPath {
		return
	}
	now := m.currentTime()
	m.mergeConflictResolvers[ownerProjectPath] = mergeConflictResolverState{
		OwnerProjectPath:   ownerProjectPath,
		SessionProjectPath: sessionProjectPath,
		Provider:           provider.Normalized(),
		Phase:              mergeConflictResolverStarting,
		StartedAt:          now,
		UpdatedAt:          now,
	}
}

func (m *Model) updateMergeConflictResolverSnapshot(sessionProjectPath string, snapshot codexapp.Snapshot, terminal bool) mergeConflictResolverState {
	m.ensureMergeConflictResolverState()
	sessionProjectPath = normalizeProjectPath(sessionProjectPath)
	ownerProjectPath := m.mergeConflictResolverOwnerForSession(sessionProjectPath)
	if ownerProjectPath == "" {
		ownerProjectPath = sessionProjectPath
	}
	state := m.mergeConflictResolvers[ownerProjectPath]
	state.OwnerProjectPath = ownerProjectPath
	state.SessionProjectPath = sessionProjectPath
	if provider := embeddedProvider(snapshot).Normalized(); provider != "" {
		state.Provider = provider
	}
	if state.Provider.Normalized() == "" {
		state.Provider = codexapp.ProviderCodex
	}
	if sessionID := strings.TrimSpace(snapshot.ThreadID); sessionID != "" {
		state.SessionID = sessionID
	}
	now := m.currentTime()
	if state.StartedAt.IsZero() {
		state.StartedAt = snapshot.BusySince
		if state.StartedAt.IsZero() {
			state.StartedAt = now
		}
	}
	state.UpdatedAt = embeddedSnapshotActivityAt(snapshot)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}
	if detail := strings.TrimSpace(liveEngineerSnapshotDetail(snapshot)); detail != "" {
		state.Detail = detail
	}

	switch {
	case !terminal:
		state.Phase = mergeConflictResolverRunning
	case snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil:
		state.Phase = mergeConflictResolverNeedsAttention
	case strings.TrimSpace(snapshot.LastError) != "":
		state.Phase = mergeConflictResolverFailed
		state.Detail = strings.TrimSpace(snapshot.LastError)
	default:
		state.Phase = mergeConflictResolverChecking
	}
	m.mergeConflictResolvers[ownerProjectPath] = state
	return state
}

func (m *Model) failMergeConflictResolver(ownerProjectPath, sessionProjectPath string, provider codexapp.Provider, err error) {
	m.markMergeConflictResolverStarting(ownerProjectPath, sessionProjectPath, provider)
	ownerProjectPath = m.mergeConflictResolverOwnerForSession(sessionProjectPath)
	state, ok := m.mergeConflictResolvers[ownerProjectPath]
	if !ok {
		return
	}
	state.Phase = mergeConflictResolverFailed
	state.UpdatedAt = m.currentTime()
	if err != nil {
		state.Detail = strings.TrimSpace(err.Error())
	}
	m.mergeConflictResolvers[ownerProjectPath] = state
}

func (m *Model) failMergeConflictResolverRefresh(projectPath string, err error) {
	projectPath = normalizeProjectPath(projectPath)
	state, ok := m.mergeConflictResolvers[projectPath]
	if !ok || state.Phase != mergeConflictResolverChecking {
		return
	}
	state.Phase = mergeConflictResolverRefreshFailed
	state.UpdatedAt = m.currentTime()
	if err != nil {
		state.Detail = strings.TrimSpace(err.Error())
	}
	m.mergeConflictResolvers[projectPath] = state
}

func (m *Model) reconcileMergeConflictResolverProject(project model.ProjectSummary) {
	projectPath := normalizeProjectPath(project.Path)
	state, ok := m.mergeConflictResolvers[projectPath]
	if !ok {
		return
	}
	switch state.Phase {
	case mergeConflictResolverChecking, mergeConflictResolverRefreshFailed:
		if project.RepoConflict {
			state.Phase = mergeConflictResolverConflictsRemain
		} else {
			state.Phase = mergeConflictResolverResolved
		}
		state.UpdatedAt = m.currentTime()
		m.mergeConflictResolvers[projectPath] = state
	case mergeConflictResolverConflictsRemain:
		if !project.RepoConflict {
			state.Phase = mergeConflictResolverResolved
			state.UpdatedAt = m.currentTime()
			m.mergeConflictResolvers[projectPath] = state
		}
	case mergeConflictResolverResolved:
		if project.RepoConflict {
			// A later conflict is a new incident; do not attribute it to the
			// already-finished resolver.
			delete(m.mergeConflictResolvers, projectPath)
		}
	}
}

func (m *Model) reconcileMergeConflictResolverProjects() {
	if len(m.mergeConflictResolvers) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(m.allProjects)+len(m.archivedProjects)+len(m.projects))
	for _, projects := range [][]model.ProjectSummary{m.allProjects, m.archivedProjects, m.projects} {
		for _, project := range projects {
			path := normalizeProjectPath(project.Path)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			m.reconcileMergeConflictResolverProject(project)
		}
	}
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
	if resolver, active := m.mergeConflictResolverForProject(project.Path); active &&
		(resolver.active() || resolver.Phase == mergeConflictResolverChecking) {
		m.status = resolver.commandStatus(m.currentTime())
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
	return m.launchParallelMergeConflictResolverForOwner(parent.Path, project, provider, gitlinkConflictResolvePrompt(parent, target))
}

func (m Model) launchParallelMergeConflictResolver(project model.ProjectSummary, provider codexapp.Provider, prompt string) (tea.Model, tea.Cmd) {
	return m.launchParallelMergeConflictResolverForOwner(project.Path, project, provider, prompt)
}

func (m Model) launchParallelMergeConflictResolverForOwner(ownerProjectPath string, project model.ProjectSummary, provider codexapp.Provider, prompt string) (tea.Model, tea.Cmd) {
	return m.launchParallelMergeConflictResolverWithOptions(ownerProjectPath, project, provider, embeddedLaunchOptions{
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
	return m.launchParallelMergeConflictResolverWithOptions(project.Path, project, choice.Provider, embeddedLaunchOptions{
		resumeID:                choice.SessionID,
		prompt:                  suspendedTurnContinuationPromptForChoice(choice),
		reveal:                  false,
		continueInterruptedTurn: choice.CapturedOnQuit,
		interruptedTurnID:       choice.ActiveTurnID,
		restartWarmup:           true,
	}, intent.Key())
}

func (m Model) launchParallelMergeConflictResolverWithOptions(ownerProjectPath string, project model.ProjectSummary, provider codexapp.Provider, options embeddedLaunchOptions, restartIntentKey string) (tea.Model, tea.Cmd) {
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
	m.markMergeConflictResolverStarting(ownerProjectPath, project.Path, provider)
	if state, ok := m.mergeConflictResolverForProject(ownerProjectPath); ok && state.Phase == mergeConflictResolverRunning {
		m.status = state.commandStatus(m.currentTime())
	} else {
		m.status = "Starting background " + provider.Label() + " conflict resolver; progress is shown on the project row"
	}
	return m, func() tea.Msg {
		if manager == nil {
			return mergeConflictResolverOpenedMsg{
				ownerProjectPath: ownerProjectPath,
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
					ownerProjectPath: ownerProjectPath,
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
				ownerProjectPath: ownerProjectPath,
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
				ownerProjectPath: ownerProjectPath,
				projectPath:      project.Path,
				provider:         provider,
				snapshot:         snapshot,
				restartIntentKey: restartIntentKey,
				restartWarmup:    options.restartWarmup,
				err:              errors.New("saved background resolver is active in another process"),
			}
		}
		return mergeConflictResolverOpenedMsg{
			ownerProjectPath: ownerProjectPath,
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
	ownerProjectPath := normalizeProjectPath(msg.ownerProjectPath)
	if ownerProjectPath == "" {
		ownerProjectPath = normalizeProjectPath(msg.projectPath)
	}
	if msg.err != nil {
		if msg.restartWarmup {
			m.settleParallelRestartWarmup(msg.projectPath, false)
		}
		m.failMergeConflictResolver(ownerProjectPath, msg.projectPath, msg.provider, msg.err)
		m.status = "Background conflict resolver failed: " + msg.err.Error()
		m.reportError("Background conflict resolver failed", msg.err, ownerProjectPath)
		return m, nil
	}
	restartAckCmd := tea.Cmd(nil)
	if strings.TrimSpace(msg.restartIntentKey) != "" && !msg.snapshot.BusyExternal {
		restartAckCmd = m.acknowledgeRestartIntentsCmd([]string{msg.restartIntentKey})
	}
	state := m.updateMergeConflictResolverSnapshot(msg.projectPath, msg.snapshot, false)
	if m.codexManager != nil {
		if _, live := m.codexManager.ParallelSession(msg.projectPath); !live {
			// A very short resolver can finish and be detached before its open
			// result is applied. Keep the terminal status in that race.
			if state.active() {
				state = m.updateMergeConflictResolverSnapshot(msg.projectPath, msg.snapshot, true)
			}
			if msg.restartWarmup {
				m.settleParallelRestartWarmup(msg.projectPath, !msg.snapshot.BusyExternal)
			}
			return m, batchCmds(restartAckCmd, m.refreshProjectStatusCmd(ownerProjectPath))
		}
	}
	m.err = nil
	if msg.reused {
		m.status = state.commandStatus(m.currentTime())
	} else {
		m.status = "Background " + msg.provider.Label() + " conflict resolver started; progress is shown on the project row"
	}
	if msg.restartWarmup {
		m.settleParallelRestartWarmup(msg.projectPath, !msg.snapshot.BusyExternal)
	}
	return m, restartAckCmd
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
	ownerProjectPath := m.mergeConflictResolverOwnerForSession(msg.projectPath)
	if msg.closeErr != nil {
		m.reportError("Background conflict resolver cleanup failed", msg.closeErr, ownerProjectPath)
	}
	if !msg.found {
		if state, ok := m.mergeConflictResolverForProject(ownerProjectPath); ok && state.active() {
			err := errors.New("background resolver stopped without a final status")
			m.failMergeConflictResolver(ownerProjectPath, msg.projectPath, state.Provider, err)
			m.status = "Background conflict resolver stopped unexpectedly"
			return m, batchCmds(waitCmd, m.refreshProjectStatusCmd(ownerProjectPath))
		}
		return m, waitCmd
	}
	state := m.updateMergeConflictResolverSnapshot(msg.projectPath, msg.snapshot, msg.terminal)
	ownerProjectPath = state.OwnerProjectPath
	if !msg.terminal {
		return m, waitCmd
	}

	provider := embeddedProvider(msg.snapshot)
	projectName := filepath.Base(strings.TrimSpace(ownerProjectPath))
	if project, ok := m.projectSummaryByPathAllProjects(ownerProjectPath); ok {
		projectName = projectNameForPicker(project, ownerProjectPath)
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
		return m, batchCmds(waitCmd, m.refreshProjectStatusCmd(ownerProjectPath))
	}
	if lastErr := strings.TrimSpace(msg.snapshot.LastError); lastErr != "" {
		err := errors.New(lastErr)
		m.status = "Background conflict resolver failed: " + lastErr
		m.reportError("Background conflict resolver failed", err, ownerProjectPath)
	} else {
		m.err = nil
		m.status = "Background " + provider.Label() + " conflict resolver finished; refreshing Git status"
	}
	return m, batchCmds(waitCmd, m.refreshProjectStatusCmd(ownerProjectPath))
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
