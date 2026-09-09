package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const staleWorktreeCleanupSuccessStatusPrefix = "Stale worktree cleanup finished successfully:"

type staleWorktreeCleanupDialogState struct {
	Audit        service.StaleWorktreeCleanupAudit
	Selected     int
	Chosen       map[string]bool
	Loading      bool
	Removing     bool
	Finished     bool
	Queue        []service.StaleWorktreeCleanupCandidate
	QueueIndex   int
	Results      []staleWorktreeCleanupResult
	LiveExcluded int
	ErrorMessage string
}

type staleWorktreeCleanupResult struct {
	Candidate     service.StaleWorktreeCleanupCandidate
	Finalize      service.FinalizeMergedWorktreeResult
	ClosedSession bool
	SkippedReason string
	Err           error
}

type staleWorktreeCleanupAuditMsg struct {
	audit service.StaleWorktreeCleanupAudit
	err   error
}

type staleWorktreeCleanupRevalidateMsg struct {
	candidate service.StaleWorktreeCleanupCandidate
	reason    string
	err       error
}

type staleWorktreeCleanupRemoveMsg struct {
	result staleWorktreeCleanupResult
}

func (m Model) openStaleWorktreeCleanup() (tea.Model, tea.Cmd) {
	m.staleWorktreeCleanup = &staleWorktreeCleanupDialogState{
		Chosen:  make(map[string]bool),
		Loading: true,
	}
	m.status = "Auditing stale worktrees..."
	return m, m.loadStaleWorktreeCleanupAuditCmd()
}

func (m Model) loadStaleWorktreeCleanupAuditCmd() tea.Cmd {
	svc := m.svc
	now := m.currentTime()
	return func() tea.Msg {
		if svc == nil {
			return staleWorktreeCleanupAuditMsg{err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		audit, err := svc.AuditStaleWorktreeCleanup(ctx, now)
		err = timeoutActionError(err, tuiProjectActionTimeout, "auditing stale worktrees")
		return staleWorktreeCleanupAuditMsg{audit: audit, err: err}
	}
}

func (m Model) applyStaleWorktreeCleanupAudit(msg staleWorktreeCleanupAuditMsg) (tea.Model, tea.Cmd) {
	dialog := m.staleWorktreeCleanup
	if dialog == nil || !dialog.Loading {
		return m, nil
	}
	dialog.Loading = false
	if msg.err != nil {
		dialog.ErrorMessage = msg.err.Error()
		m.reportError("Stale worktree audit failed", msg.err, "")
		return m, nil
	}

	filtered := msg.audit.Candidates[:0]
	liveExcluded := 0
	for _, candidate := range msg.audit.Candidates {
		if reason, _ := m.staleWorktreeCleanupLiveState(candidate.ProjectPath, candidate.RootProjectPath); reason != "" {
			liveExcluded++
			continue
		}
		filtered = append(filtered, candidate)
	}
	msg.audit.Candidates = filtered
	dialog.Audit = msg.audit
	dialog.Selected = 0
	dialog.LiveExcluded = liveExcluded
	dialog.Chosen = make(map[string]bool, len(filtered))
	for _, candidate := range filtered {
		dialog.Chosen[candidate.ProjectPath] = true
	}
	dialog.ErrorMessage = ""
	m.err = nil
	if len(filtered) == 0 {
		m.status = fmt.Sprintf("Stale worktree audit complete: no eligible worktrees (%d linked checked)", msg.audit.ScannedLinkedWorktrees)
		return m, nil
	}
	m.status = fmt.Sprintf("Stale worktree audit: %d eligible and selected", len(filtered))
	return m, nil
}

func (m Model) staleWorktreeCleanupRevalidateCmd(candidate service.StaleWorktreeCleanupCandidate) tea.Cmd {
	svc := m.svc
	now := m.currentTime()
	return func() tea.Msg {
		if svc == nil {
			return staleWorktreeCleanupRevalidateMsg{
				candidate: candidate,
				err:       fmt.Errorf("service unavailable"),
			}
		}

		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		revalidated, reason, err := svc.RevalidateStaleWorktreeCleanupCandidate(ctx, candidate.ProjectPath, now)
		if err != nil {
			return staleWorktreeCleanupRevalidateMsg{
				candidate: candidate,
				err:       timeoutActionError(err, tuiGitActionTimeout, "revalidating the stale worktree"),
			}
		}
		if reason != "" {
			return staleWorktreeCleanupRevalidateMsg{candidate: candidate, reason: reason}
		}
		return staleWorktreeCleanupRevalidateMsg{candidate: revalidated}
	}
}

func (m Model) applyStaleWorktreeCleanupRevalidate(msg staleWorktreeCleanupRevalidateMsg) (tea.Model, tea.Cmd) {
	dialog := m.staleWorktreeCleanup
	if dialog == nil || !dialog.Removing || dialog.QueueIndex >= len(dialog.Queue) {
		return m, nil
	}
	expected := dialog.Queue[dialog.QueueIndex]
	if normalizeProjectPath(expected.ProjectPath) != normalizeProjectPath(msg.candidate.ProjectPath) {
		return m, nil
	}
	result := staleWorktreeCleanupResult{Candidate: msg.candidate, Err: msg.err, SkippedReason: msg.reason}
	if result.Err != nil || result.SkippedReason != "" {
		return m.applyStaleWorktreeCleanupRemove(staleWorktreeCleanupRemoveMsg{result: result})
	}

	// Re-read the current Model only after the slow Git revalidation returns.
	// Pending actions, external runtimes, and embedded work may have appeared
	// while that command was in flight; a snapshot captured before it began is
	// no longer safe enough to authorize deletion.
	if reason, _ := m.staleWorktreeCleanupLiveState(msg.candidate.ProjectPath, msg.candidate.RootProjectPath); reason != "" {
		result.SkippedReason = reason
		return m.applyStaleWorktreeCleanupRemove(staleWorktreeCleanupRemoveMsg{result: result})
	}

	m.status = fmt.Sprintf("Stale worktree cleanup %d/%d; removing %s...", dialog.QueueIndex, len(dialog.Queue), staleWorktreeCleanupCandidateName(msg.candidate))
	return m, m.staleWorktreeCleanupFinalizeCmd(msg.candidate)
}

func (m Model) staleWorktreeCleanupFinalizeCmd(candidate service.StaleWorktreeCleanupCandidate) tea.Cmd {
	svc := m.svc
	manager := m.codexManager
	runtimeManager := m.runtimeManager
	now := m.currentTime()
	return func() tea.Msg {
		result := staleWorktreeCleanupResult{Candidate: candidate}
		if svc == nil {
			result.Err = fmt.Errorf("service unavailable")
			return staleWorktreeCleanupRemoveMsg{result: result}
		}

		if staleWorktreeManagedRuntimeRunning(runtimeManager, candidate.ProjectPath) {
			result.SkippedReason = "a managed runtime became active"
			return staleWorktreeCleanupRemoveMsg{result: result}
		}
		if session, ok := managerSession(manager, candidate.ProjectPath); ok {
			snapshot := session.Snapshot()
			if reason := staleWorktreeCleanupSessionBlockReason(snapshot, now); reason != "" {
				result.SkippedReason = reason
				return staleWorktreeCleanupRemoveMsg{result: result}
			}
			closed, closeErr := closeIdleEmbeddedSessionForWorktree(manager, candidate.ProjectPath, true)
			result.ClosedSession = closed
			if closeErr != nil {
				result.Err = closeErr
				return staleWorktreeCleanupRemoveMsg{result: result}
			}
		}

		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		result.Finalize, result.Err = svc.FinalizeMergedWorktree(ctx, candidate.ProjectPath, service.FinalizeMergedWorktreeOptions{
			MarkLinkedTodoDone: candidate.LinkedTodoID > 0,
			RemoveWorktree:     true,
		})
		result.Err = timeoutActionError(result.Err, tuiGitActionTimeout, "removing the stale worktree")
		return staleWorktreeCleanupRemoveMsg{result: result}
	}
}

func managerSession(manager *codexapp.Manager, projectPath string) (codexapp.Session, bool) {
	if manager == nil {
		return nil, false
	}
	return manager.Session(projectPath)
}

func staleWorktreeManagedRuntimeRunning(manager *projectrun.Manager, projectPath string) bool {
	if manager == nil {
		return false
	}
	for _, snapshot := range manager.SnapshotsForProject(projectPath) {
		if snapshot.Running {
			return true
		}
	}
	return false
}

func (m Model) applyStaleWorktreeCleanupRemove(msg staleWorktreeCleanupRemoveMsg) (tea.Model, tea.Cmd) {
	dialog := m.staleWorktreeCleanup
	if dialog == nil || !dialog.Removing || dialog.QueueIndex >= len(dialog.Queue) {
		return m, nil
	}
	expected := dialog.Queue[dialog.QueueIndex]
	if normalizeProjectPath(expected.ProjectPath) != normalizeProjectPath(msg.result.Candidate.ProjectPath) {
		return m, nil
	}
	dialog.Results = append(dialog.Results, msg.result)
	dialog.QueueIndex++
	if msg.result.ClosedSession {
		m.dropCodexSnapshot(msg.result.Candidate.ProjectPath)
		if normalizeProjectPath(m.codexVisibleProject) == normalizeProjectPath(msg.result.Candidate.ProjectPath) {
			m.codexVisibleProject = ""
			m.codexInput.Blur()
		}
		if normalizeProjectPath(m.codexHiddenProject) == normalizeProjectPath(msg.result.Candidate.ProjectPath) {
			m.codexHiddenProject = ""
		}
	}
	if msg.result.Finalize.WorktreeRemoved {
		m.applyRemovedProjectLocally(msg.result.Candidate.ProjectPath, msg.result.Candidate.RootProjectPath)
	}
	if msg.result.Err != nil {
		m.appendErrorLogEntry("Stale worktree cleanup failed", msg.result.Err, msg.result.Candidate.ProjectPath)
	}

	if dialog.QueueIndex < len(dialog.Queue) {
		next := dialog.Queue[dialog.QueueIndex]
		m.status = fmt.Sprintf("Stale worktree cleanup %d/%d; checking %s...", dialog.QueueIndex, len(dialog.Queue), staleWorktreeCleanupCandidateName(next))
		return m, m.staleWorktreeCleanupRevalidateCmd(next)
	}

	dialog.Removing = false
	dialog.Finished = true
	removed, skipped, failed := staleWorktreeCleanupResultCounts(dialog.Results)
	m.status = staleWorktreeCleanupFinishedStatus(removed, skipped, failed)
	return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
}

func staleWorktreeCleanupFinishedStatus(removed, skipped, failed int) string {
	if failed == 0 {
		return fmt.Sprintf("%s %d removed, %d skipped", staleWorktreeCleanupSuccessStatusPrefix, removed, skipped)
	}
	return fmt.Sprintf("Stale worktree cleanup finished: %d removed, %d skipped, %d failed", removed, skipped, failed)
}

func (m Model) updateStaleWorktreeCleanupMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.staleWorktreeCleanup
	if dialog == nil {
		return m, nil
	}
	if dialog.Removing {
		if msg.String() == "esc" {
			m.status = "Worktree removal is already in progress; remaining selected items will still be checked"
		}
		return m, nil
	}
	if dialog.Loading {
		if msg.String() == "esc" {
			m.staleWorktreeCleanup = nil
			m.status = "Stale worktree audit closed; no worktrees were removed"
		}
		return m, nil
	}
	if dialog.Finished {
		if msg.String() == "esc" || msg.String() == "enter" {
			m.staleWorktreeCleanup = nil
			m.status = "Stale worktree cleanup report closed"
		}
		return m, nil
	}

	candidates := dialog.Audit.Candidates
	switch msg.String() {
	case "esc":
		m.staleWorktreeCleanup = nil
		m.status = "Stale worktree cleanup canceled; no worktrees were removed"
	case "r":
		dialog.Loading = true
		dialog.ErrorMessage = ""
		dialog.Chosen = make(map[string]bool)
		m.status = "Refreshing stale worktree audit..."
		return m, m.loadStaleWorktreeCleanupAuditCmd()
	case "up", "k":
		if len(candidates) > 0 {
			dialog.Selected = max(0, dialog.Selected-1)
		}
	case "down", "j":
		if len(candidates) > 0 {
			dialog.Selected = min(len(candidates)-1, dialog.Selected+1)
		}
	case "pgup", "ctrl+u":
		if len(candidates) > 0 {
			dialog.Selected = max(0, dialog.Selected-5)
		}
	case "pgdown", "ctrl+d":
		if len(candidates) > 0 {
			dialog.Selected = min(len(candidates)-1, dialog.Selected+5)
		}
	case "home", "g":
		dialog.Selected = 0
	case "end", "G":
		if len(candidates) > 0 {
			dialog.Selected = len(candidates) - 1
		}
	case " ":
		if len(candidates) > 0 {
			index := max(0, min(dialog.Selected, len(candidates)-1))
			path := candidates[index].ProjectPath
			dialog.Chosen[path] = !dialog.Chosen[path]
		}
	case "enter":
		dialog.Queue = selectedStaleWorktreeCleanupCandidates(dialog)
		if len(dialog.Queue) == 0 {
			m.status = "Select at least one stale worktree before removing"
			return m, nil
		}
		dialog.Removing = true
		dialog.QueueIndex = 0
		dialog.Results = nil
		dialog.ErrorMessage = ""
		first := dialog.Queue[0]
		m.status = fmt.Sprintf("Stale worktree cleanup 0/%d; checking %s...", len(dialog.Queue), staleWorktreeCleanupCandidateName(first))
		return m, m.staleWorktreeCleanupRevalidateCmd(first)
	}
	return m, nil
}

func selectedStaleWorktreeCleanupCandidates(dialog *staleWorktreeCleanupDialogState) []service.StaleWorktreeCleanupCandidate {
	if dialog == nil {
		return nil
	}
	selected := make([]service.StaleWorktreeCleanupCandidate, 0, len(dialog.Audit.Candidates))
	for _, candidate := range dialog.Audit.Candidates {
		if dialog.Chosen[candidate.ProjectPath] {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func (m Model) staleWorktreeCleanupLiveState(projectPath, rootPath string) (string, codexapp.Provider) {
	if _, ok := m.pendingGitOperation(projectPath); ok {
		return "a Git action is in progress", ""
	}
	if _, ok := m.pendingGitOperation(rootPath); ok {
		return "a repository-family Git action is in progress", ""
	}
	if resolver, ok := m.mergeConflictResolverForProject(projectPath); ok && resolver.active() {
		return "a merge-conflict engineer is active", ""
	}
	for _, snapshot := range m.projectRuntimeSnapshots(projectPath) {
		if snapshot.Running {
			if snapshot.External {
				return "an external local runtime is active", ""
			}
			return "a managed runtime is active", ""
		}
	}
	if m.codexPendingOpen != nil && normalizeProjectPath(m.codexPendingOpen.projectPath) == normalizeProjectPath(projectPath) {
		return "an engineer session is opening", ""
	}
	if _, ok := m.todoPendingLaunchForProjectPath(projectPath); ok {
		return "a TODO worktree launch is still being prepared", ""
	}
	if snapshot, ok := m.liveCodexSnapshot(projectPath); ok {
		provider := embeddedProvider(snapshot)
		if reason := staleWorktreeCleanupSessionBlockReason(snapshot, m.currentTime()); reason != "" {
			return reason, ""
		}
		return "", provider
	}
	return "", ""
}

func staleWorktreeCleanupSessionBlockReason(snapshot codexapp.Snapshot, now time.Time) string {
	provider := embeddedProvider(snapshot).Label()
	if embeddedSessionBlocksProviderSwitch(snapshot) {
		return "the embedded " + provider + " session is active"
	}
	if snapshot.Goal != nil && snapshot.Goal.Status == codexapp.ThreadGoalStatusActive {
		return "the embedded " + provider + " session has an active goal"
	}
	if len(snapshot.BackgroundTasks) > 0 {
		return "the embedded " + provider + " session has background work"
	}
	switch snapshot.BrowserActivity.Normalize().State {
	case browserctl.SessionActivityStateActive:
		return "the embedded " + provider + " browser task is active"
	case browserctl.SessionActivityStateWaitingForUser:
		return "the embedded " + provider + " browser task is waiting for input"
	}
	if activityAt := embeddedSnapshotActivityAt(snapshot); !activityAt.IsZero() && !activityAt.Before(now.Add(-service.StaleWorktreeCleanupWindow)) {
		return "the embedded " + provider + " session was used within the last 24 hours"
	}
	return ""
}

func (m Model) staleWorktreeCleanupCandidate(project model.ProjectSummary) (service.StaleWorktreeCleanupCandidate, codexapp.Provider, bool) {
	candidate, _, ok := service.EvaluateStaleWorktreeCleanupCandidate(project, m.currentTime())
	if !ok {
		return service.StaleWorktreeCleanupCandidate{}, "", false
	}
	reason, idleProvider := m.staleWorktreeCleanupLiveState(candidate.ProjectPath, candidate.RootProjectPath)
	if reason != "" {
		return service.StaleWorktreeCleanupCandidate{}, "", false
	}
	return candidate, idleProvider, true
}

func (m Model) staleWorktreeCleanupCount(projects []model.ProjectSummary) int {
	count := 0
	for _, project := range projects {
		if _, _, ok := m.staleWorktreeCleanupCandidate(project); ok {
			count++
		}
	}
	return count
}

func staleWorktreeCleanupCandidateName(candidate service.StaleWorktreeCleanupCandidate) string {
	return firstNonEmptyString(strings.TrimSpace(candidate.Branch), strings.TrimSpace(candidate.ProjectName), filepath.Base(candidate.ProjectPath), "worktree")
}

func staleWorktreeCleanupSummary(candidate service.StaleWorktreeCleanupCandidate, idleProvider codexapp.Provider, now time.Time) string {
	parts := []string{"merged", "clean", "idle " + formatCleanupAge(now, candidate.LastActivity)}
	if candidate.NoRecordedSession {
		parts = append(parts, "no recorded session; Git activity")
	}
	if idleProvider != "" {
		parts = append(parts, "idle "+idleProvider.Label()+" open")
	}
	return strings.Join(parts, " · ")
}

func staleWorktreeCleanupDetail(candidate service.StaleWorktreeCleanupCandidate, idleProvider codexapp.Provider, now time.Time) string {
	text := "stale — merged, clean, idle " + formatCleanupAge(now, candidate.LastActivity)
	if candidate.NoRecordedSession {
		text += "; no recorded session; age from Git activity"
	}
	if idleProvider != "" {
		text += "; idle " + idleProvider.Label() + " session will be closed"
	}
	return text
}

func staleWorktreeCleanupStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Faint(true)
}

func staleWorktreeCleanupResultCounts(results []staleWorktreeCleanupResult) (int, int, int) {
	removed := 0
	skipped := 0
	failed := 0
	for _, result := range results {
		switch {
		case result.Finalize.WorktreeRemoved:
			removed++
		case result.SkippedReason != "":
			skipped++
		case result.Err != nil:
			failed++
		default:
			failed++
		}
	}
	return removed, skipped, failed
}

func (m Model) renderStaleWorktreeCleanupOverlay(body string, bodyW, bodyH int) string {
	dialog := m.staleWorktreeCleanup
	if dialog == nil {
		return body
	}
	panelW := min(max(68, bodyW-12), 108)
	panelInnerW := max(34, panelW-4)
	content := m.renderStaleWorktreeCleanupContent(dialog, panelInnerW, bodyH)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderStaleWorktreeCleanupContent(dialog *staleWorktreeCleanupDialogState, width, bodyH int) string {
	lines := []string{commandPaletteTitleStyle.Render("Clean stale worktrees")}
	if dialog.Loading {
		lines = append(lines,
			commandPaletteHintStyle.Render("Read-only audit: finding merged, clean linked worktrees idle for more than 24 hours."),
			"",
			commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Checking worktree and assessment state..."),
			"",
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		)
		return strings.Join(lines, "\n")
	}
	if dialog.Finished {
		return renderStaleWorktreeCleanupResults(dialog, width, bodyH)
	}
	if dialog.Removing {
		return renderStaleWorktreeCleanupProgress(dialog, width, m.spinnerFrame)
	}

	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width,
		"Eligible worktrees are present, unpinned, merged into their recorded parent, conflict-free, clean, and unused for more than 24 hours. Recorded sessions must be assessed done with a completed turn; worktrees without a recorded session use Git activity for their age. Active turns, runtimes, and Git actions are excluded; idle managed sessions close immediately before removal.")...)
	lines = append(lines, "")
	if dialog.ErrorMessage != "" {
		lines = append(lines,
			detailDangerStyle.Render("Audit failed"),
		)
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, dialog.ErrorMessage)...)
		lines = append(lines, "", renderDialogAction("R", "retry", navigateActionKeyStyle, navigateActionTextStyle)+"   "+renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
		return strings.Join(lines, "\n")
	}

	candidates := dialog.Audit.Candidates
	selected := selectedStaleWorktreeCleanupCandidates(dialog)
	lines = append(lines,
		detailField("Audit", fmt.Sprintf("%d linked checked · %d eligible · %d live excluded", dialog.Audit.ScannedLinkedWorktrees, len(candidates), dialog.LiveExcluded)),
		detailField("Selected", fmt.Sprintf("%d worktree%s", len(selected), pluralSuffix(len(selected)))),
		"",
	)
	if len(candidates) == 0 {
		lines = append(lines,
			detailMutedStyle.Render("No worktrees are currently eligible for stale cleanup."),
			"",
			renderDialogAction("R", "audit again", navigateActionKeyStyle, navigateActionTextStyle)+"   "+renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		)
		return strings.Join(lines, "\n")
	}

	start, end := cleanupGroupWindow(dialog.Selected, len(candidates), bodyH)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more worktrees", start)))
	}
	for index := start; index < end; index++ {
		candidate := candidates[index]
		mark := "[ ]"
		if dialog.Chosen[candidate.ProjectPath] {
			mark = "[x]"
		}
		_, idleProvider := m.staleWorktreeCleanupLiveState(candidate.ProjectPath, candidate.RootProjectPath)
		row := fmt.Sprintf("%s %s · %s", mark, staleWorktreeCleanupCandidateName(candidate), staleWorktreeCleanupSummary(candidate, idleProvider, m.currentTime()))
		style := commandPaletteRowStyle
		if index == dialog.Selected {
			style = commandPaletteSelectStyle
		}
		lines = append(lines, style.Render(truncateText(row, width)))
	}
	if end < len(candidates) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more worktrees", len(candidates)-end)))
	}

	candidate := candidates[max(0, min(dialog.Selected, len(candidates)-1))]
	_, idleProvider := m.staleWorktreeCleanupLiveState(candidate.ProjectPath, candidate.RootProjectPath)
	gitLine := staleWorktreeCleanupCandidateName(candidate) + " → " + firstNonEmptyString(candidate.ParentBranch, "parent unknown")
	lines = append(lines,
		"",
		detailField("Path", candidate.ProjectPath),
		detailField("Git", gitLine),
		detailField("Cleanup", staleWorktreeCleanupDetail(candidate, idleProvider, m.currentTime())),
	)
	if candidate.LinkedTodoID > 0 {
		lines = append(lines, detailField("TODO", fmt.Sprintf("#%d will be marked done before removal", candidate.LinkedTodoID)))
	}
	lines = append(lines,
		"",
		detailMutedStyle.Render("Branches and AI conversation history are preserved. Every selected worktree is freshly revalidated; changed candidates are skipped."),
		"",
		renderDialogAction("Space", "toggle", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Enter", "remove selected", commitActionKeyStyle, commitActionTextStyle)+"   "+
			renderDialogAction("R", "audit again", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return clampDialogContent(strings.Join(lines, "\n"), max(12, bodyH-4), 5, detailMutedStyle.Render("… details clipped to fit terminal …"))
}

func renderStaleWorktreeCleanupProgress(dialog *staleWorktreeCleanupDialogState, width, spinnerFrame int) string {
	current := dialog.QueueIndex + 1
	if current > len(dialog.Queue) {
		current = len(dialog.Queue)
	}
	name := "worktree"
	if dialog.QueueIndex < len(dialog.Queue) {
		name = staleWorktreeCleanupCandidateName(dialog.Queue[dialog.QueueIndex])
	}
	removed, skipped, failed := staleWorktreeCleanupResultCounts(dialog.Results)
	lines := []string{
		commandPaletteTitleStyle.Render("Clean stale worktrees"),
		"",
		detailValueStyle.Render(fmt.Sprintf("%s Revalidating and removing %d/%d: %s", spinnerFrames[spinnerFrame%len(spinnerFrames)], current, len(dialog.Queue), name)),
		"",
		detailField("Progress", fmt.Sprintf("%d removed · %d skipped · %d failed", removed, skipped, failed)),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width,
		"Each checkout is rechecked for merge, cleanliness, assessment, activity, runtime, and engineer state. An idle managed session closes only after those checks pass.")...)
	lines = append(lines, "", detailMutedStyle.Render("Removal in progress; the dialog stays locked until every selected item has a result."))
	return strings.Join(lines, "\n")
}

func renderStaleWorktreeCleanupResults(dialog *staleWorktreeCleanupDialogState, width, bodyH int) string {
	removed, skipped, failed := staleWorktreeCleanupResultCounts(dialog.Results)
	lines := []string{
		commandPaletteTitleStyle.Render("Stale worktree cleanup report"),
		"",
		detailField("Result", fmt.Sprintf("%d removed · %d skipped · %d failed", removed, skipped, failed)),
		detailMutedStyle.Render("Git branches and AI conversation history were preserved."),
		"",
	}
	budget := max(2, bodyH-13)
	used := 0
	for index, result := range dialog.Results {
		name := staleWorktreeCleanupCandidateName(result.Candidate)
		var style lipgloss.Style
		var marker, detail string
		switch {
		case result.Finalize.WorktreeRemoved:
			style = classificationCategoryStyle(model.SessionCategoryCompleted)
			marker = "✓"
			detail = "removed"
			if result.ClosedSession {
				detail += "; idle session closed"
			}
			if result.Finalize.LinkedTodoMarkedDone {
				detail += "; linked TODO done"
			}
		case result.SkippedReason != "":
			style = detailWarningStyle
			marker = "-"
			detail = "skipped: " + result.SkippedReason
		case result.Err != nil:
			style = detailDangerStyle
			marker = "!"
			detail = result.Err.Error()
		default:
			style = detailDangerStyle
			marker = "!"
			detail = "removal did not complete"
		}
		block := staleWorktreeCleanupResultBlock(style, width, marker, name, detail)
		if index > 0 && used+len(block) > budget {
			lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("+%d more results", len(dialog.Results)-index)))
			break
		}
		lines = append(lines, block...)
		used += len(block)
	}
	lines = append(lines, "", renderDialogAction("Enter/Esc", "close report", cancelActionKeyStyle, cancelActionTextStyle))
	return clampDialogContent(strings.Join(lines, "\n"), max(10, bodyH-4), 4, detailMutedStyle.Render("… more results hidden …"))
}

// staleWorktreeCleanupResultBlock renders one report entry, keeping it on a
// single line when it fits and otherwise wrapping the detail under an indented
// continuation so long failure messages stay fully readable.
func staleWorktreeCleanupResultBlock(style lipgloss.Style, width int, marker, name, detail string) []string {
	textWidth := max(10, width-2)
	single := marker + " " + name + " · " + detail
	if lipgloss.Width(single) <= textWidth && !strings.ContainsAny(detail, "\r\n") {
		return []string{style.Render(single)}
	}
	out := []string{style.Render(marker + " " + truncateText(name, textWidth))}
	detailWidth := max(8, textWidth-2)
	for _, raw := range strings.Split(strings.ReplaceAll(detail, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		wrapped := lipgloss.NewStyle().Width(detailWidth).Render(trimmed)
		for _, line := range strings.Split(wrapped, "\n") {
			out = append(out, style.Render("  "+strings.TrimRight(line, " ")))
		}
	}
	return out
}
