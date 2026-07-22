package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type worktreeRestoreDialogState struct {
	RootPath     string
	RootName     string
	Candidates   []service.RestorableWorktreeSession
	Selected     int
	Loading      bool
	Busy         bool
	ErrorMessage string
}

func (m Model) openWorktreeRestoreForSelection() (tea.Model, tea.Cmd) {
	row, project, ok := m.selectedProjectRow()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	rootPath := strings.TrimSpace(row.RootPath)
	if rootPath == "" {
		rootPath = projectWorktreeRootPath(project)
	}
	rootPath = normalizeProjectPath(rootPath)
	if rootPath == "" {
		m.status = "Worktree recovery requires a repository family"
		return m, nil
	}
	rootProject := m.pickerProjectSummary(rootPath)
	m.worktreeRestore = &worktreeRestoreDialogState{
		RootPath: rootPath,
		RootName: projectNameForPicker(rootProject, rootPath),
		Loading:  true,
	}
	m.worktreeMergeConfirm = nil
	m.worktreePostMerge = nil
	m.worktreeRemoveConfirm = nil
	m.status = "Loading deleted-worktree Codex sessions..."
	return m, m.loadWorktreeRestoreCandidatesCmd(rootPath)
}

func (m Model) loadWorktreeRestoreCandidatesCmd(rootPath string) tea.Cmd {
	svc := m.svc
	rootPath = normalizeProjectPath(rootPath)
	return func() tea.Msg {
		if svc == nil {
			return worktreeRestoreCandidatesMsg{rootPath: rootPath, err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		candidates, err := svc.ListRestorableWorktreeSessions(ctx, rootPath)
		err = timeoutActionError(err, tuiProjectActionTimeout, "loading deleted-worktree Codex sessions")
		return worktreeRestoreCandidatesMsg{rootPath: rootPath, candidates: candidates, err: err}
	}
}

func (m Model) restoreWorktreeSessionCmd(rootPath, sessionID string) tea.Cmd {
	svc := m.svc
	rootPath = normalizeProjectPath(rootPath)
	sessionID = strings.TrimSpace(sessionID)
	return func() tea.Msg {
		if svc == nil {
			return worktreeRestoreActionMsg{rootPath: rootPath, err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		result, err := svc.RestoreWorktreeSession(ctx, service.RestoreWorktreeSessionRequest{
			ProjectPath: rootPath,
			SessionID:   sessionID,
		})
		err = timeoutActionError(err, tuiGitActionTimeout, "restoring the deleted worktree")
		return worktreeRestoreActionMsg{rootPath: rootPath, result: result, err: err}
	}
}

func (m Model) applyWorktreeRestoreCandidates(msg worktreeRestoreCandidatesMsg) (tea.Model, tea.Cmd) {
	dialog := m.worktreeRestore
	if dialog == nil || normalizeProjectPath(dialog.RootPath) != normalizeProjectPath(msg.rootPath) {
		return m, nil
	}
	dialog.Loading = false
	if msg.err != nil {
		dialog.ErrorMessage = msg.err.Error()
		m.reportError("Worktree recovery scan failed", msg.err, dialog.RootPath)
		return m, nil
	}
	dialog.ErrorMessage = ""
	dialog.Candidates = append([]service.RestorableWorktreeSession(nil), msg.candidates...)
	if len(dialog.Candidates) == 0 {
		m.worktreeRestore = nil
		m.err = nil
		m.status = "No deleted-worktree Codex sessions found for this repository"
		return m, nil
	}
	dialog.Selected = 0
	for index, candidate := range dialog.Candidates {
		if candidate.Ready {
			dialog.Selected = index
			break
		}
	}
	m.err = nil
	suffix := "s"
	if len(dialog.Candidates) == 1 {
		suffix = ""
	}
	m.status = fmt.Sprintf("Found %d deleted-worktree Codex session%s", len(dialog.Candidates), suffix)
	return m, nil
}

func (m Model) applyWorktreeRestoreAction(msg worktreeRestoreActionMsg) (tea.Model, tea.Cmd) {
	dialog := m.worktreeRestore
	if dialog == nil || normalizeProjectPath(dialog.RootPath) != normalizeProjectPath(msg.rootPath) {
		return m, nil
	}
	if msg.err != nil {
		if msg.result.WorktreeCreated {
			m.worktreeRestore = nil
			m.preferredSelectPath = strings.TrimSpace(msg.result.WorktreePath)
			m.reportError("Worktree restore incomplete", msg.err, msg.result.WorktreePath)
			return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.result.WorktreePath))
		}
		dialog.Busy = false
		dialog.ErrorMessage = msg.err.Error()
		m.reportError("Worktree restore failed", msg.err, dialog.RootPath)
		return m, nil
	}

	m.worktreeRestore = nil
	m.err = nil
	m.preferredSelectPath = strings.TrimSpace(msg.result.WorktreePath)
	project := model.ProjectSummary{
		Path:             msg.result.WorktreePath,
		PresentOnDisk:    true,
		WorktreeRootPath: msg.result.RootProjectPath,
		WorktreeKind:     model.WorktreeKindLinked,
	}
	req := m.embeddedLaunchRequest(project, codexapp.ProviderCodex, embeddedLaunchOptions{resumeID: msg.result.SessionID})
	req.WorkspaceContract = codexapp.WorkspaceContract{
		AssignedPath:       msg.result.WorktreePath,
		RepositoryRootPath: msg.result.RootProjectPath,
		ExpectedRootBranch: msg.result.ParentBranch,
	}
	if err := req.Validate(); err != nil {
		m.reportError("Restored Codex session could not open", err, msg.result.WorktreePath)
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.result.WorktreePath))
	}
	m.ensureCodexRuntime()
	m.rememberEmbeddedProvider(codexapp.ProviderCodex)
	m.beginCodexPendingOpenWithVisibilityAndReveal(msg.result.WorktreePath, codexapp.ProviderCodex, true, true)
	m.status = fmt.Sprintf("Restored %s; resuming Codex session %s...", filepath.Base(msg.result.WorktreePath), shortID(msg.result.SessionID))
	return m, batchCmds(
		m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.result.WorktreePath)),
		m.openCodexSessionCmdWithVisibility(req, true),
	)
}

func (m Model) updateWorktreeRestoreMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.worktreeRestore
	if dialog == nil {
		return m, nil
	}
	if dialog.Busy {
		if msg.String() == "esc" {
			m.status = "Worktree restoration is still in progress"
		}
		return m, nil
	}
	if dialog.Loading {
		if msg.String() == "esc" {
			m.worktreeRestore = nil
			m.status = "Worktree recovery closed"
		}
		return m, nil
	}
	if len(dialog.Candidates) == 0 {
		if msg.String() == "esc" || msg.String() == "enter" {
			m.worktreeRestore = nil
			m.status = "Worktree recovery closed"
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.worktreeRestore = nil
		m.status = "Worktree recovery closed"
	case "up", "k":
		dialog.Selected = max(0, dialog.Selected-1)
	case "down", "j":
		dialog.Selected = min(len(dialog.Candidates)-1, dialog.Selected+1)
	case "pgup", "ctrl+u":
		dialog.Selected = max(0, dialog.Selected-5)
	case "pgdown", "ctrl+d":
		dialog.Selected = min(len(dialog.Candidates)-1, dialog.Selected+5)
	case "home", "g":
		dialog.Selected = 0
	case "end", "G":
		dialog.Selected = len(dialog.Candidates) - 1
	case "enter":
		candidate, ok := selectedWorktreeRestoreCandidate(dialog)
		if !ok {
			return m, nil
		}
		if !candidate.Ready {
			m.status = firstNonEmptyString(candidate.BlockReason, "That saved session cannot be restored safely")
			return m, nil
		}
		dialog.Busy = true
		dialog.ErrorMessage = ""
		m.status = "Recreating " + filepath.Base(candidate.WorktreePath) + " and restoring its Codex session..."
		return m, m.restoreWorktreeSessionCmd(dialog.RootPath, candidate.SessionID)
	}
	return m, nil
}

func selectedWorktreeRestoreCandidate(dialog *worktreeRestoreDialogState) (service.RestorableWorktreeSession, bool) {
	if dialog == nil || len(dialog.Candidates) == 0 {
		return service.RestorableWorktreeSession{}, false
	}
	index := max(0, min(dialog.Selected, len(dialog.Candidates)-1))
	return dialog.Candidates[index], true
}

func (m Model) renderWorktreeRestoreOverlay(body string, bodyW, bodyH int) string {
	dialog := m.worktreeRestore
	if dialog == nil {
		return body
	}
	panelW := min(max(62, bodyW-18), 100)
	panelInnerW := max(30, panelW-4)
	panel := renderDialogPanel(panelW, panelInnerW, m.renderWorktreeRestoreContent(panelInnerW, bodyH))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderWorktreeRestoreContent(width, bodyH int) string {
	dialog := m.worktreeRestore
	if dialog == nil {
		return ""
	}
	lines := []string{commandPaletteTitleStyle.Render("Restore deleted worktree session")}
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Recreate the original checkout from Git, then resume its globally stored Codex conversation.")...)
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, "This restores committed Git state; files that existed only in the deleted checkout cannot be recovered.")...)
	lines = append(lines,
		"",
		renderDialogAction("Enter", "restore + resume", commitActionKeyStyle, commitActionTextStyle)+"   "+
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	)
	if dialog.Loading {
		lines = append(lines, commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Reading LCR history and Codex's global thread index..."))
		return strings.Join(lines, "\n")
	}
	if len(dialog.Candidates) == 0 {
		if dialog.ErrorMessage != "" {
			lines = append(lines, detailWarningStyle.Render("Recovery scan failed"))
			lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, dialog.ErrorMessage)...)
		} else {
			lines = append(lines, detailMutedStyle.Render("No deleted-worktree Codex sessions found for this repository."))
		}
		return strings.Join(lines, "\n")
	}

	start, end := worktreeRestoreWindow(dialog, bodyH)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for index := start; index < end; index++ {
		lines = append(lines, renderWorktreeRestoreRow(dialog.Candidates[index], index == dialog.Selected, width))
	}
	if end < len(dialog.Candidates) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(dialog.Candidates)-end)))
	}

	if selected, ok := selectedWorktreeRestoreCandidate(dialog); ok {
		lines = append(lines, "", commandPaletteTitleStyle.Render("Recovery details"))
		if summary := strings.TrimSpace(selected.Summary); summary != "" && summary != strings.TrimSpace(selected.Title) {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, truncateText(summary, 280))...)
		}
		lines = append(lines, detailValueStyle.Render(truncateText(selected.WorktreePath, width)))
		meta := "Branch: " + firstNonEmptyString(selected.BranchName, "unknown") + "  Session: " + shortID(selected.SessionID) + "  Last activity: " + formatPickerActivity(selected.LastActivity)
		lines = append(lines, detailMutedStyle.Render(fitFooterWidth(meta, width)))
		switch {
		case !selected.Ready:
			lines = append(lines, detailWarningStyle.Render("Blocked: "+truncateText(selected.BlockReason, max(16, width-9))))
		case selected.RecreateBranch:
			lines = append(lines, detailWarningStyle.Render("The local branch is gone; LCR will recreate it at recorded commit "+shortID(selected.GitSHA)+"."))
		case selected.StaleRegistration:
			lines = append(lines, detailValueStyle.Render("Git still has the missing checkout registered; LCR will safely reuse that exact registration."))
		default:
			lines = append(lines, detailValueStyle.Render("The retained local branch will be checked out again at the original path."))
		}
	}
	if dialog.ErrorMessage != "" {
		lines = append(lines, "", detailWarningStyle.Render("Restore failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, truncateText(dialog.ErrorMessage, 480))...)
	}
	if dialog.Busy {
		lines = append(lines, "", detailValueStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Restoring checkout, metadata, and worktree preparation..."))
	}
	return strings.Join(lines, "\n")
}

func renderWorktreeRestoreRow(candidate service.RestorableWorktreeSession, selected bool, width int) string {
	badge := "READY"
	badgeStyle := detailValueStyle
	if !candidate.Ready {
		badge = "BLOCKED"
		badgeStyle = detailWarningStyle
	}
	badgeCell := badgeStyle.Render(badge)
	right := formatPickerActivity(candidate.LastActivity)
	available := max(16, width-lipgloss.Width(badgeCell)-lipgloss.Width(right)-8)
	label := truncateText(firstNonEmptyString(candidate.Title, candidate.WorktreeName), available)
	row := fmt.Sprintf("  %s  %s  %s", badgeCell, fitStyledWidth(label, available), right)
	if selected {
		row = "> " + strings.TrimPrefix(row, "  ")
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func worktreeRestoreWindow(dialog *worktreeRestoreDialogState, bodyH int) (int, int) {
	if dialog == nil || len(dialog.Candidates) == 0 {
		return 0, 0
	}
	if bodyH <= 0 {
		bodyH = 30
	}
	limit := min(len(dialog.Candidates), max(2, min(8, (bodyH-17)/2)))
	start := 0
	if dialog.Selected >= limit {
		start = dialog.Selected - limit + 1
	}
	start = max(0, min(start, len(dialog.Candidates)-limit))
	return start, start + limit
}
