package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	repositoryIntegrityAskEngineer = iota
	repositoryIntegrityRepair
	repositoryIntegrityUseCurrent
	repositoryIntegrityKeep
	repositoryIntegrityOptionCount

	repositoryIntegrityActionTimeout = 90 * time.Second
)

type repositoryIntegrityDialogState struct {
	State       model.RepositoryIntegrityState
	Selected    int
	Busy        bool
	BusyMessage string
}

type repositoryIntegrityActionKind string

const (
	repositoryIntegrityActionAcknowledge repositoryIntegrityActionKind = "acknowledge"
	repositoryIntegrityActionUseCurrent  repositoryIntegrityActionKind = "use_current"
	repositoryIntegrityActionRepair      repositoryIntegrityActionKind = "repair"
	repositoryIntegrityActionEngineer    repositoryIntegrityActionKind = "engineer"
)

type repositoryIntegrityActionMsg struct {
	Action   repositoryIntegrityActionKind
	State    model.RepositoryIntegrityState
	Task     model.AgentTask
	Provider codexapp.Provider
	Repair   service.RepositoryIntegrityRepairResult
	Err      error
}

func (m Model) repositoryIntegrityStateForProject(projectPath string) (model.RepositoryIntegrityState, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return model.RepositoryIntegrityState{}, false
	}
	if project, ok := m.projectSummaryByPath(projectPath); ok {
		rootPath := normalizeProjectPath(projectWorktreeRootPath(project))
		if state, found := m.repositoryIntegrityByRoot[rootPath]; found {
			return state, true
		}
	}
	for _, state := range m.repositoryIntegrityByRoot {
		if state.ContainsProject(projectPath) {
			return state, true
		}
	}
	return model.RepositoryIntegrityState{}, false
}

func (m *Model) openRepositoryIntegrityDialogForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	state, ok := m.repositoryIntegrityStateForProject(project.Path)
	if !ok || !state.Displaced {
		m.status = "No repository root integrity incident for the selected project"
		return nil
	}
	if block := m.repositoryIntegrityLiveRepairBlockReason(state); block != "" {
		state.CanRepair = false
		state.RepairBlockReason = block
	}
	m.repositoryIntegrityDialog = &repositoryIntegrityDialogState{
		State:    state,
		Selected: repositoryIntegrityKeep,
	}
	m.status = "Repository integrity warning open"
	return nil
}

func (m Model) repositoryIntegrityLiveRepairBlockReason(state model.RepositoryIntegrityState) string {
	for _, member := range state.Members {
		path := normalizeProjectPath(member.Path)
		if path == "" {
			continue
		}
		if m.projectHasLiveCodexSession(path) {
			return fmt.Sprintf("close the embedded engineer session for %s before repairing the repository family", firstNonEmptyTrimmed(member.Name, filepath.Base(path)))
		}
		if snapshot := m.projectRuntimeSnapshot(path); snapshot.Running {
			return fmt.Sprintf("stop the managed runtime for %s before repairing the repository family", firstNonEmptyTrimmed(member.Name, filepath.Base(path)))
		}
	}
	return ""
}

func (m Model) updateRepositoryIntegrityDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.repositoryIntegrityDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Busy {
		if msg.String() == "esc" {
			m.status = "Repository integrity action is already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.repositoryIntegrityDialog = nil
		m.status = "Repository integrity warning left active"
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		dialog.Selected = (dialog.Selected - 1 + repositoryIntegrityOptionCount) % repositoryIntegrityOptionCount
		return m, nil
	case "right", "l", "down", "j", "tab":
		dialog.Selected = (dialog.Selected + 1) % repositoryIntegrityOptionCount
		return m, nil
	case "enter":
		state := dialog.State
		switch dialog.Selected {
		case repositoryIntegrityAskEngineer:
			dialog.Busy = true
			dialog.BusyMessage = "Creating a fresh engineer task..."
			m.status = dialog.BusyMessage
			provider := m.repositoryIntegrityEngineerProvider(state)
			return m, m.createRepositoryIntegrityEngineerTaskCmd(state, provider)
		case repositoryIntegrityRepair:
			if !state.CanRepair {
				m.status = "Safe repair unavailable: " + firstNonEmptyTrimmed(state.RepairBlockReason, "the safety checks did not pass")
				return m, nil
			}
			if block := m.repositoryIntegrityLiveRepairBlockReason(state); block != "" {
				dialog.State.CanRepair = false
				dialog.State.RepairBlockReason = block
				m.status = "Safe repair unavailable: " + block
				return m, nil
			}
			dialog.Busy = true
			dialog.BusyMessage = fmt.Sprintf("Moving %s into a linked worktree and restoring %s...", state.ActualBranch, state.ExpectedBranch)
			m.status = dialog.BusyMessage
			return m, m.repairRepositoryIntegrityCmd(state)
		case repositoryIntegrityUseCurrent:
			dialog.Busy = true
			dialog.BusyMessage = "Updating the expected root branch..."
			m.status = dialog.BusyMessage
			return m, m.setRepositoryIntegrityExpectedBranchCmd(state)
		default:
			dialog.Busy = true
			dialog.BusyMessage = "Acknowledging this exact repository state..."
			m.status = dialog.BusyMessage
			return m, m.acknowledgeRepositoryIntegrityCmd(state)
		}
	}
	return m, nil
}

func (m Model) repositoryIntegrityEngineerProvider(state model.RepositoryIntegrityState) codexapp.Provider {
	if project, ok := m.projectSummaryByPath(state.RootPath); ok {
		if provider := m.preferredEmbeddedProviderForProject(project).Normalized(); provider != "" {
			return provider
		}
	}
	return codexapp.ProviderCodex
}

func (m Model) acknowledgeRepositoryIntegrityCmd(state model.RepositoryIntegrityState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(repositoryIntegrityActionTimeout)
		defer cancel()
		err := m.svc.AcknowledgeRepositoryIntegrity(ctx, state.RootPath, state.Fingerprint)
		err = timeoutActionError(err, repositoryIntegrityActionTimeout, "acknowledging the repository integrity warning")
		return repositoryIntegrityActionMsg{Action: repositoryIntegrityActionAcknowledge, State: state, Err: err}
	}
}

func (m Model) setRepositoryIntegrityExpectedBranchCmd(state model.RepositoryIntegrityState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(repositoryIntegrityActionTimeout)
		defer cancel()
		err := m.svc.SetRepositoryRootExpectedBranch(ctx, state.RootPath, state.ActualBranch)
		err = timeoutActionError(err, repositoryIntegrityActionTimeout, "updating the expected repository root branch")
		return repositoryIntegrityActionMsg{Action: repositoryIntegrityActionUseCurrent, State: state, Err: err}
	}
}

func (m Model) repairRepositoryIntegrityCmd(state model.RepositoryIntegrityState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(repositoryIntegrityActionTimeout)
		defer cancel()
		result, err := m.svc.RepairRepositoryRoot(ctx, service.RepositoryIntegrityRepairRequest{RootPath: state.RootPath})
		err = timeoutActionError(err, repositoryIntegrityActionTimeout, "repairing the repository root")
		return repositoryIntegrityActionMsg{Action: repositoryIntegrityActionRepair, State: state, Repair: result, Err: err}
	}
}

func (m Model) createRepositoryIntegrityEngineerTaskCmd(state model.RepositoryIntegrityState, provider codexapp.Provider) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(repositoryIntegrityActionTimeout)
		defer cancel()
		task, err := m.svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
			Title:        repositoryIntegrityEngineerTaskTitle(state),
			Kind:         model.AgentTaskKindAgent,
			Capabilities: []string{"repository.integrity.explain", "repository.integrity.repair"},
			Resources: []model.AgentTaskResource{{
				Kind:        model.AgentTaskResourceProject,
				ProjectPath: state.RootPath,
				Label:       "repository root integrity incident",
			}},
		})
		err = timeoutActionError(err, repositoryIntegrityActionTimeout, "creating a repository integrity engineer task")
		return repositoryIntegrityActionMsg{Action: repositoryIntegrityActionEngineer, State: state, Task: task, Provider: provider, Err: err}
	}
}

func (m Model) applyRepositoryIntegrityActionMsg(msg repositoryIntegrityActionMsg) (tea.Model, tea.Cmd) {
	if m.repositoryIntegrityDialog != nil {
		m.repositoryIntegrityDialog.Busy = false
		m.repositoryIntegrityDialog.BusyMessage = ""
	}
	if msg.Err != nil {
		m.reportError("Repository integrity action failed", msg.Err, msg.State.RootPath)
		return m, nil
	}
	switch msg.Action {
	case repositoryIntegrityActionAcknowledge:
		m.repositoryIntegrityDialog = nil
		m.status = "Repository warning acknowledged for this exact checkout state"
		return m, m.requestProjectsReloadCmd()
	case repositoryIntegrityActionUseCurrent:
		m.repositoryIntegrityDialog = nil
		m.status = fmt.Sprintf("Repository root policy now expects %s", msg.State.ActualBranch)
		return m, m.requestProjectsReloadCmd()
	case repositoryIntegrityActionRepair:
		m.repositoryIntegrityDialog = nil
		m.preferredSelectPath = msg.Repair.WorktreePath
		m.status = fmt.Sprintf("Root restored to %s; %s moved to %s", msg.Repair.RestoredBranch, msg.Repair.MovedBranch, msg.Repair.WorktreePath)
		if warning := strings.TrimSpace(msg.Repair.PreparationWarning); warning != "" {
			m.status += "; worktree preparation needs attention"
			m.appendBackgroundErrorLogEntry("Worktree preparation incomplete", fmt.Errorf("%s", warning), msg.Repair.WorktreePath)
		}
		return m, m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.Repair.WorktreePath))
	case repositoryIntegrityActionEngineer:
		m.upsertOpenAgentTask(msg.Task)
		project, err := projectSummaryForAgentTask(msg.Task)
		if err != nil {
			m.reportError("Repository integrity engineer task failed", err, msg.State.RootPath)
			return m, nil
		}
		prompt := m.agentTaskLaunchPromptWithRuntimeContext(msg.Task, repositoryIntegrityEngineerPrompt(msg.State))
		updated, cmd := m.launchEmbeddedForProjectWithOptions(project, msg.Provider, embeddedLaunchOptions{
			forceNew: true,
			prompt:   prompt,
			reveal:   true,
		})
		m = normalizeUpdateModel(updated)
		m.repositoryIntegrityDialog = nil
		if cmd == nil {
			if strings.TrimSpace(m.status) == "" {
				m.status = "Created repository integrity engineer task " + msg.Task.ID
			}
			return m, nil
		}
		return m, m.agentTaskLaunchTrackingCmd(msg.Task, cmd, bossAgentTaskHandoffStatus(msg.Task))
	default:
		return m, nil
	}
}

func repositoryIntegrityEngineerTaskTitle(state model.RepositoryIntegrityState) string {
	name := firstNonEmptyTrimmed(state.RootName, filepath.Base(state.RootPath), "repository")
	return "Investigate root checkout for " + name
}

func repositoryIntegrityEngineerPrompt(state model.RepositoryIntegrityState) string {
	lines := []string{
		"Investigate this repository-root integrity incident. Start in investigation-only mode: do not mutate files, branches, worktrees, Git metadata, or LCR state.",
		"",
		"Explain the likely cause, the risk, and the safest repair. Before making any change, present the exact plan and ask the user for explicit confirmation.",
		"",
		"Trusted incident snapshot:",
		"- Repository root: " + state.RootPath,
		"- Expected root branch: " + state.ExpectedBranch,
		"- Current root branch: " + state.ActualBranch,
		fmt.Sprintf("- Root dirty: %t", state.RootDirty),
		fmt.Sprintf("- Root conflict: %t", state.RootConflict),
	}
	if state.SuggestedWorktreePath != "" {
		lines = append(lines, "- Proposed linked worktree for the current branch: "+state.SuggestedWorktreePath)
	}
	if state.RepairBlockReason != "" {
		lines = append(lines, "- Automated repair blocker: "+state.RepairBlockReason)
	}
	if len(state.Members) > 0 {
		lines = append(lines, "", "Known repository family:")
		for _, member := range state.Members {
			lines = append(lines, fmt.Sprintf("- %s | branch=%s | kind=%s | dirty=%t | conflict=%t", member.Path, member.Branch, member.WorktreeKind, member.Dirty, member.Conflict))
		}
	}
	if len(state.RecentExcursions) > 0 {
		lines = append(lines, "", "Recent commands that crossed from an assigned worktree into the canonical root:")
		for _, excursion := range state.RecentExcursions {
			lines = append(lines, fmt.Sprintf("- %s | cwd=%s | command=%s", excursion.At.Format(time.RFC3339), excursion.CWD, singleLineStatusText(excursion.Command)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderRepositoryIntegrityDialogOverlay(body string, bodyW, bodyH int) string {
	dialog := m.repositoryIntegrityDialog
	if dialog == nil {
		return body
	}
	panelW := min(bodyW, min(max(68, bodyW-12), 108))
	panelInnerW := max(36, panelW-4)
	content := m.renderRepositoryIntegrityDialogContent(*dialog, panelInnerW)
	content = clampDialogContent(
		content,
		max(12, bodyH-4),
		5,
		dialogOverflowHintLine(panelInnerW, "... more incident details"),
	)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderRepositoryIntegrityDialogContent(dialog repositoryIntegrityDialogState, width int) string {
	state := dialog.State
	lines := []string{
		commandPaletteTitleStyle.Render("Repository Root Integrity"),
		"",
		detailWarningStyle.Render("The canonical root is not on its expected branch."),
		"",
		detailLabelStyle.Render("Root:") + " " + detailMutedStyle.Render(m.displayPathWithHomeTilde(state.RootPath)),
		detailLabelStyle.Render("Expected:") + " " + detailValueStyle.Render(state.ExpectedBranch),
		detailLabelStyle.Render("Current:") + " " + detailWarningStyle.Render(state.ActualBranch),
		detailLabelStyle.Render("Evidence:") + " " + detailMutedStyle.Render(repositoryIntegrityEvidenceLabel(state.ExpectedBranchSource)),
	}
	if state.RootDirty || state.RootConflict {
		lines = append(lines, detailDangerStyle.Render(fmt.Sprintf("Root state: dirty=%t, conflict=%t", state.RootDirty, state.RootConflict)))
	}
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Ask Engineer investigates. Use Current changes policy. Keep acknowledges only this exact state.")...)
	lines = append(lines, "", detailSectionStyle.Render("Safe response"))
	if state.CanRepair {
		target := m.displayPathWithHomeTilde(state.SuggestedWorktreePath)
		if width < 84 {
			target = filepath.Base(state.SuggestedWorktreePath) + " (a sibling worktree)"
		}
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, fmt.Sprintf("Restore %s here and move %s to %s. No branch is renamed or deleted.", state.ExpectedBranch, state.ActualBranch, target))...)
	} else {
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, "Automatic repair is unavailable: "+firstNonEmptyTrimmed(state.RepairBlockReason, "the safety checks did not pass"))...)
	}
	if len(state.RecentExcursions) > 0 {
		lines = append(lines, "", detailSectionStyle.Render("Recent workspace crossing"))
		excursion := state.RecentExcursions[0]
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, fmt.Sprintf("A managed session assigned elsewhere ran a command from %s at %s.", m.displayPathWithHomeTilde(excursion.CWD), excursion.At.Format("2006-01-02 15:04")))...)
	}
	lines = append(lines, "")
	if dialog.Busy {
		lines = append(lines, detailValueStyle.Render(todoDialogWaitingLabel(m.spinnerFrame)+" "+dialog.BusyMessage))
		return strings.Join(lines, "\n")
	}
	buttons := []string{
		renderDialogButton("Ask Engineer", dialog.Selected == repositoryIntegrityAskEngineer),
	}
	if state.CanRepair {
		buttons = append(buttons, renderDialogButton("Repair Safely", dialog.Selected == repositoryIntegrityRepair))
	} else if dialog.Selected == repositoryIntegrityRepair {
		buttons = append(buttons, dialogButtonSelectedStyle.Render("Repair Unavailable"))
	} else {
		buttons = append(buttons, disabledActionTextStyle.Render("[Repair Unavailable]"))
	}
	buttons = append(buttons,
		renderDialogButton("Use Current", dialog.Selected == repositoryIntegrityUseCurrent),
		renderDialogButton("Keep", dialog.Selected == repositoryIntegrityKeep),
	)
	lines = append(lines, strings.Join(buttons, " "))
	lines = append(lines, "", renderHelpPanelActionRow(
		renderDialogAction("Tab/←→", "choose", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "confirm", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "leave warning active", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
}

func repositoryIntegrityEvidenceLabel(source string) string {
	switch strings.TrimSpace(source) {
	case "worktree_creation":
		return "saved before LCR created a linked worktree"
	case "linked_worktree_parent":
		return "unanimous linked-worktree parent branch"
	case "origin_default":
		return "origin/HEAD"
	case "user":
		return "explicit user choice"
	default:
		return firstNonEmptyTrimmed(source, "saved policy")
	}
}

func (m Model) repositoryIntegrityIncidentCount() int {
	count := 0
	for _, state := range m.repositoryIntegrityByRoot {
		if state.NeedsAttention() {
			count++
		}
	}
	return count
}

func (m Model) renderFooterRepositoryIntegritySegment() string {
	count := m.repositoryIntegrityIncidentCount()
	if count == 0 {
		return ""
	}
	if count == 1 {
		return renderFooterAlert("1 root checkout warning")
	}
	return renderFooterAlert(fmt.Sprintf("%d root checkout warnings", count))
}
