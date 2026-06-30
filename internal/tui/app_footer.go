package tui

import (
	"errors"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/uistyle"
	"path/filepath"
	"strings"
)

func (m Model) renderClassificationSummary() string {
	counts := m.classificationCounts()

	parts := []string{
		classificationStyle(model.ClassificationCompleted).Render(fmt.Sprintf("OK=%d", counts.done)),
		classificationStyle(model.ClassificationRunning).Render(fmt.Sprintf("RUN=%d", counts.running)),
		classificationStyle(model.ClassificationPending).Render(fmt.Sprintf("Q=%d", counts.queued)),
		classificationStyle(model.ClassificationFailed).Render(fmt.Sprintf("ERR=%d", counts.failed)),
	}
	return strings.Join(parts, " ")
}

type classificationSummary struct {
	done    int
	queued  int
	running int
	failed  int
}

func (m Model) classificationCounts() classificationSummary {
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}

	counts := classificationSummary{}
	for _, project := range projects {
		switch project.LatestSessionClassification {
		case model.ClassificationCompleted:
			counts.done++
		case model.ClassificationPending:
			counts.queued++
		case model.ClassificationRunning:
			counts.running++
		case model.ClassificationFailed:
			counts.failed++
		}
	}
	return counts
}

func (m Model) classificationFailureCount() int {
	return m.classificationCounts().failed
}

func (m Model) footerAssessmentAlertLabel() string {
	failed := m.classificationFailureCount()
	switch failed {
	case 0:
		return ""
	case 1:
		return "1 assessment error"
	default:
		return fmt.Sprintf("%d assessment errors", failed)
	}
}

func (m Model) renderFooterAssessmentSegment() string {
	text := m.footerAssessmentAlertLabel()
	if text == "" {
		return ""
	}
	return renderFooterAlert(text)
}

func footerSupplementSegments(rawSegments ...string) []string {
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func (m Model) renderFooter(width int) string {
	usageSegment := m.renderFooterUsageSegment(m.footerUsageLabel())
	browserSegment := m.renderFooterBrowserAttentionSegment()
	processSegment := m.renderFooterProcessWarningSegment()
	runtimeSegment := m.renderFooterRuntimeSegment()
	assessmentSegment := ""
	if !m.errorLogVisible {
		assessmentSegment = m.renderFooterAssessmentSegment()
	}
	filterSegment := m.renderFooterProjectFilterSegment()
	supplementSegments := footerSupplementSegments(filterSegment, runtimeSegment, processSegment, browserSegment, assessmentSegment, usageSegment)
	if m.diffView != nil {
		diffSegments := append([]string{renderDiffFooter(width, *m.diffView, usageSegment)}, footerSupplementSegments(filterSegment, runtimeSegment, processSegment, browserSegment, assessmentSegment, "")...)
		return renderFooterLine(width, diffSegments...)
	}
	if m.gitStatusDialog != nil {
		label := gitStatusDialogReadyStatus(*m.gitStatusDialog)
		if m.gitStatusApplying {
			label = "Applying git action..."
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.commitPreview != nil {
		label := commitPreviewReadyStatus(*m.commitPreview)
		if m.commitApplying {
			label = "Applying git action..."
		} else if m.commitPreviewRefreshing {
			label = "Refreshing commit preview..."
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.newProjectDialog != nil {
		label := "New project: Enter create/add, Space toggle git, Alt+1..3 recent, Esc cancel"
		if len(m.newProjectDialog.PathInput.MatchedSuggestions()) > 0 {
			label = "New project: Enter create/add, Right complete path, Alt+1..8 pick, Space git, Esc cancel"
		}
		if m.newProjectDialog.Submitting {
			label = "New project: applying..."
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.newTaskDialog != nil {
		label := "New task: ↑↓/j/k choose agent, Enter create, Esc cancel"
		if m.newTaskDialog.Submitting {
			label = "New task: creating..."
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.projectFilterDialog != nil {
		label := "Project filter: type to narrow, Enter keep, Esc close"
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.categoryDialog != nil {
		label := "Categories: ↑↓ choose, Enter select, Esc close"
		if m.categoryDialog.Mode == categoryDialogModeCreate {
			label = "Categories: type name, Enter create, Esc back"
		} else if m.categoryDialog.Mode == categoryDialogModeMove || m.categoryDialog.Mode == categoryDialogModeRemove {
			label = "Categories: ↑↓ choose, Enter apply, Esc back"
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.errorLogVisible {
		return m.renderModalFooter(width, "Error log: ↑↓ select, Enter/c copy, t ask engineer, Esc close", supplementSegments...)
	}
	if m.cpuDialog != nil {
		return m.renderModalFooter(width, "CPU inspector: ↑↓ select, Space mark, a ask scoped, A ask all, r refresh, Esc close", supplementSegments...)
	}
	if m.portsDialog != nil {
		return m.renderModalFooter(width, "Ports inspector: ↑↓ select, s stop external, r refresh, Esc close", supplementSegments...)
	}
	if m.processDialog != nil {
		return m.renderModalFooter(width, "Process inspector: ↑↓ select, r refresh, Esc close", supplementSegments...)
	}
	if m.skillsDialog != nil {
		return m.renderModalFooter(width, "Codex skills: ↑↓ select, c copy path, r refresh, Esc close", supplementSegments...)
	}
	if m.commandMode {
		return m.renderModalFooter(width, "Command palette open", supplementSegments...)
	}
	if m.bossSetupPrompt != nil {
		return m.renderModalFooter(width, "Boss chat setup: Enter choose, Tab switch, Esc cancel", supplementSegments...)
	}
	if m.setupMode {
		if m.setupReviewMode {
			return m.renderModalFooter(width, "Setup save: Enter save, Esc back", supplementSegments...)
		}
		if m.setupConfigMode {
			return m.renderModalFooter(width, "Setup configuration: type to edit, Tab field, Enter continue, Esc back", supplementSegments...)
		}
		return m.renderModalFooter(width, "Setup: ↑↓ provider, Enter next, Esc back/close", supplementSegments...)
	}
	if m.settingsMode {
		return m.renderModalFooter(width, "Settings: ctrl+s save, Tab next, Esc cancel", supplementSegments...)
	}
	if m.showPerf {
		return m.renderModalFooter(width, "Performance: c copy, Esc close", supplementSegments...)
	}
	if m.showAIStats {
		return m.renderModalFooter(width, "AI stats: Esc close", supplementSegments...)
	}
	if m.todoPendingLaunchDialog != nil {
		if m.todoPendingLaunchDialog.AllowAbort {
			return m.renderModalFooter(width, "Preparing worktree: Enter choose, Tab switch, Esc close", supplementSegments...)
		}
		return m.renderModalFooter(width, "Preparing worktree: Enter OK, Esc close", supplementSegments...)
	}
	if m.worktreeMergeConfirm != nil {
		if m.worktreeMergeConfirm.Busy {
			return m.renderModalFooter(width, "Merge worktree: waiting for actions to finish", supplementSegments...)
		}
		label := "Merge worktree: Space toggle, Tab navigate, Enter choose, Esc cancel"
		if !worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
			label = "Merge blocked: adjust options or fix repo state, Space toggle, Esc cancel"
		}
		return m.renderModalFooter(width, label, supplementSegments...)
	}
	if m.worktreePostMerge != nil {
		if m.worktreePostMerge.Busy {
			return m.renderModalFooter(width, "Merged worktree: waiting for removal to finish", supplementSegments...)
		}
		return m.renderModalFooter(width, "Merged worktree: Enter remove, Tab keep, Esc keep", supplementSegments...)
	}
	if m.worktreeRemoveConfirm != nil {
		if m.worktreeRemoveConfirm.Busy {
			return m.renderModalFooter(width, "Remove worktree: waiting for git to finish", supplementSegments...)
		}
		return m.renderModalFooter(width, "Remove worktree: Enter remove, Tab switch, Esc cancel", supplementSegments...)
	}
	if m.projectRemoveConfirm != nil {
		if m.projectRemoveConfirm.Submitting {
			return m.renderModalFooter(width, "Remove project: waiting for the list update", supplementSegments...)
		}
		return m.renderModalFooter(width, "Remove project: Enter choose, Tab switch, Esc cancel", supplementSegments...)
	}
	if m.agentTaskAction != nil {
		if m.agentTaskAction.Submitting {
			return m.renderModalFooter(width, "Agent task: waiting for archive", supplementSegments...)
		}
		return m.renderModalFooter(width, "Agent task: Enter choose, Tab switch, Esc cancel", supplementSegments...)
	}
	baseSegments := append([]string{
		compactFooterBase(width, m.focusedPane, m.detailViewport.ScrollPercent(), m.runtimeViewport.ScrollPercent(), m.hasHiddenCodexSession(), m.currentEmbeddedLaunchLabel(), m.worktreeFooterActions(width)),
	}, supplementSegments...)
	return renderFooterLine(width, baseSegments...)
}

func (m Model) renderCommandPalette(bodyW int) string {
	panelWidth := min(bodyW, min(max(48, bodyW-10), 84))
	panelInnerWidth := max(24, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCommandPaletteContent(panelInnerWidth))
}

func (m Model) renderCommandPaletteOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCommandPalette(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCommitPreview(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-12), 96))
	panelInnerWidth := max(28, panelWidth-4)
	// Reserve space for panel border (2) and vertical centering margin.
	maxContentHeight := max(8, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCommitPreviewContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderCommitPreviewOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCommitPreview(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderGitStatusDialog(bodyW int) string {
	panelWidth := min(bodyW, min(max(54, bodyW-12), 96))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderGitStatusDialogContent(panelInnerWidth))
}

func (m Model) renderGitStatusDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderGitStatusDialog(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderGitStatusDialogContent(width int) string {
	if m.gitStatusDialog == nil {
		return ""
	}
	dialog := *m.gitStatusDialog

	lines := []string{
		renderDialogHeader(dialog.Title, dialog.ProjectName, dialog.Branch, width),
		"",
		commitPreviewLine("Status", dialog.Status),
	}
	if strings.TrimSpace(dialog.RemoteStatus) != "" {
		lines = append(lines, commitPreviewLine("Remote", dialog.RemoteStatus))
	}

	if len(dialog.Warnings) > 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Next"))
		for _, warning := range dialog.Warnings {
			lines = append(lines, detailWarningStyle.Render("- "+warning))
		}
	}

	lines = append(lines, "")
	if m.gitStatusApplying {
		lines = append(lines, commandPaletteHintStyle.Render("Applying git action..."))
	} else {
		lines = append(lines, renderGitStatusDialogActions(dialog))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderCommitPreviewContent(width, maxHeight int) string {
	if m.commitPreview == nil {
		return ""
	}
	preview := *m.commitPreview
	placeholder := commitPreviewHasPlaceholderState(preview)

	// --- Fixed lines (always shown) ---
	lines := []string{
		renderDialogHeader("Commit Preview", preview.ProjectName, preview.Branch, width),
		"",
		renderCommitPreviewMessageInline(preview.Message, width),
		commitPreviewLine("Stage", stageModeLabel(preview.StageMode, len(preview.SelectedUntracked))),
	}

	if strings.TrimSpace(preview.LatestSummary) != "" {
		lines = append(lines, commitPreviewLine("Context", preview.LatestSummary))
	}
	if aiStatus := commitPreviewAIStatusText(preview); aiStatus != "" {
		lines = append(lines, commitPreviewLine("AI", aiStatus))
	}

	// --- Footer (always shown) ---
	var footer []string
	footer = append(footer, "")
	if m.commitApplying {
		footer = append(footer, commandPaletteHintStyle.Render("Applying git action..."))
	} else if m.commitPreviewRefreshing {
		hint := "Refreshing commit preview... Esc cancel"
		if placeholder {
			hint = "Building commit preview... Esc cancel"
		}
		footer = append(footer, commandPaletteHintStyle.Render(hint))
	} else {
		footer = append(footer, renderCommitPreviewActions(preview.CanPush))
	}

	// Budget = maxHeight minus fixed header and footer lines.
	budget := maxHeight - len(lines) - len(footer)

	// --- Optional sections, added in priority order with budget checks ---

	// Changes section (highest priority after message).
	if budget > 2 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Changes"))
		budget -= 2
		if placeholder {
			lines = append(lines, detailMutedStyle.Render("- Inspecting repo changes..."))
			budget--
		} else if budget >= 3 {
			// Show individual files when there's room.
			fileLimit := min(6, budget-1) // reserve 1 for diff summary
			fileLines := renderCommitPreviewFiles(preview.Included, fileLimit, width)
			lines = append(lines, fileLines...)
			budget -= len(fileLines)
		}
		if !placeholder && strings.TrimSpace(preview.DiffSummary) != "" && budget > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(strings.TrimSpace(preview.DiffSummary)))
			budget--
		}
	}

	// Left-out files (lower priority — dropped first when tight).
	if !placeholder && len(preview.Excluded) > 0 && budget > 3 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Left out"))
		budget -= 2
		fileLimit := min(4, budget)
		fileLines := renderCommitPreviewFiles(preview.Excluded, fileLimit, width)
		lines = append(lines, fileLines...)
		budget -= len(fileLines)
	}

	// TODO completions — always show at least a summary line so the user
	// knows TODOs will be marked done on commit.
	if !placeholder && len(m.commitTodoCompletions) > 0 {
		selectedCount := len(selectedCommitTodoIDs(m.commitTodoCompletions))
		if budget > 3 {
			// Full view: individual items with checkboxes.
			lines = append(lines, "")
			lines = append(lines, commandPaletteTitleStyle.Render("TODOs addressed"))
			budget -= 2
			todoLimit := min(len(m.commitTodoCompletions), budget-1) // reserve 1 for hint
			todoLines := renderCommitTodoCompletions(m.commitTodoCompletions, m.commitTodoSelected, width, todoLimit)
			lines = append(lines, todoLines...)
			budget -= len(todoLines)
		} else if budget > 0 {
			// Collapsed: single summary line.
			summary := fmt.Sprintf("TODOs: %d will be marked done (↑↓/Space to review)", selectedCount)
			if selectedCount == 0 {
				summary = fmt.Sprintf("TODOs: %d suggested, none selected", len(m.commitTodoCompletions))
			}
			lines = append(lines, commitPreviewInfoStyle.Render(summary))
			budget--
		}
	}

	// Warnings (compact: collapse to count when very tight).
	if len(preview.Warnings) > 0 && budget > 2 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Warnings"))
		budget -= 2
		warnLimit := min(len(preview.Warnings), max(1, budget))
		for i := 0; i < warnLimit; i++ {
			lines = append(lines, detailWarningStyle.Render("- "+preview.Warnings[i]))
			budget--
		}
		if warnLimit < len(preview.Warnings) {
			lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("+ %d more", len(preview.Warnings)-warnLimit)))
			budget--
		}
	}

	lines = append(lines, footer...)
	return strings.Join(lines, "\n")
}

func commitPreviewHasPlaceholderState(preview service.CommitPreview) bool {
	return len(preview.Included) == 0 &&
		len(preview.Excluded) == 0 &&
		len(preview.SelectedUntracked) == 0 &&
		strings.TrimSpace(preview.DiffSummary) == "" &&
		strings.TrimSpace(preview.DiffStat) == ""
}

func commitPreviewLine(label, value string) string {
	return detailLabelStyle.Render(label+":") + " " + commitPreviewInfoStyle.Render(value)
}

func renderDialogHeader(title, projectName, branch string, width int) string {
	titleWidth := ansi.StringWidth(title)
	if width <= titleWidth {
		return commandPaletteTitleStyle.Render(truncateText(title, max(1, width)))
	}

	projectName = strings.TrimSpace(projectName)
	branch = strings.TrimSpace(branch)
	if projectName == "" && branch == "" {
		return commandPaletteTitleStyle.Render(title)
	}

	suffixPlain := ""
	switch {
	case projectName != "" && branch != "":
		suffixPlain = fmt.Sprintf("%s (%s)", projectName, branch)
	case projectName != "":
		suffixPlain = projectName
	case branch != "":
		suffixPlain = fmt.Sprintf("(%s)", branch)
	}
	if suffixPlain == "" {
		return commandPaletteTitleStyle.Render(title)
	}

	separator := " - "
	if titleWidth+ansi.StringWidth(separator)+ansi.StringWidth(suffixPlain) > width {
		return commandPaletteTitleStyle.Render(title) + commitPreviewInfoStyle.Render(separator+truncateText(suffixPlain, max(1, width-titleWidth-ansi.StringWidth(separator))))
	}

	parts := []string{
		commandPaletteTitleStyle.Render(title),
		commitPreviewInfoStyle.Render(separator),
	}
	if projectName != "" {
		parts = append(parts, dialogProjectTitleStyle.Render(projectName))
	}
	if branch != "" {
		branchText := "(" + branch + ")"
		if projectName != "" {
			branchText = " " + branchText
		}
		parts = append(parts, commitPreviewInfoStyle.Render(branchText))
	}
	return strings.Join(parts, "")
}

func renderCommitPreviewMessageInline(value string, width int) string {
	body := strings.TrimSpace(value)
	if body == "" {
		body = "(empty)"
	}
	label := detailLabelStyle.Render("Message:")
	labelWidth := ansi.StringWidth(label) + 1 // +1 for space
	messageStyle := lipgloss.NewStyle().
		Width(max(12, width-labelWidth)).
		Foreground(lipgloss.Color("229")).
		Bold(true)
	return label + " " + messageStyle.Render(body)
}

func renderCommitPreviewFiles(files []service.CommitFile, limit, width int) []string {
	if len(files) == 0 {
		return []string{detailMutedStyle.Render("- none")}
	}
	maxWidth := max(12, width-6)
	lines := make([]string, 0, min(limit, len(files))+1)
	for _, file := range files[:min(limit, len(files))] {
		row := commitPreviewValueStyle.Render(file.Code) + " " + commitPreviewInfoStyle.Render(truncateText(file.Summary, maxWidth))
		lines = append(lines, row)
	}
	if len(files) > limit {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("+ %d more", len(files)-limit)))
	}
	return lines
}

func stageModeLabel(mode service.GitStageMode, selectedUntracked int) string {
	switch mode {
	case service.GitStageAllChanges:
		return "stage all current changes"
	case service.GitStageStagedOnly:
		if selectedUntracked == 1 {
			return "commit staged changes plus 1 recommended untracked file"
		}
		if selectedUntracked > 1 {
			return fmt.Sprintf("commit staged changes plus %d recommended untracked files", selectedUntracked)
		}
		return "commit staged changes only"
	default:
		return "commit staged changes only"
	}
}

func commitPreviewReadyStatus(preview service.CommitPreview) string {
	prefix := "Commit preview ready."
	if aiStatus := commitPreviewAIStatusText(preview); aiStatus != "" {
		if strings.Contains(aiStatus, "balance insufficient") {
			prefix = "Commit preview ready with AI balance issue (use /errors)."
		} else {
			prefix = "Commit preview ready with AI fallback (use /errors)."
		}
	}
	canPush := preview.CanPush
	if canPush {
		return prefix + " Enter commit, Alt+Enter commit & push, d diff, Esc cancel"
	}
	return prefix + " Enter commit, Alt+Enter unavailable, d diff, Esc cancel"
}

func commitPreviewAIStatusText(preview service.CommitPreview) string {
	errText := strings.TrimSpace(preview.CommitMessageError)
	if errText == "" {
		return ""
	}
	if llm.IsInsufficientBalanceError(errors.New(errText)) {
		return "AI provider balance insufficient; fallback subject used; /errors has details"
	}
	return "AI failed; fallback subject used; /errors has details"
}

func gitStatusDialogFromNoChanges(err service.NoChangesToCommitError) gitStatusDialog {
	projectName := strings.TrimSpace(err.ProjectName)
	if projectName == "" && strings.TrimSpace(err.ProjectPath) != "" {
		projectName = filepath.Base(err.ProjectPath)
	}
	if projectName == "" {
		projectName = "(unknown project)"
	}

	branch := strings.TrimSpace(err.Branch)
	if branch == "" {
		branch = "(detached)"
	}

	dialog := gitStatusDialog{
		Title:       "Nothing To Commit",
		ProjectPath: err.ProjectPath,
		ProjectName: projectName,
		Branch:      branch,
		Status:      "Working tree is clean.",
		CanPush:     err.CanPush,
		Ahead:       err.Ahead,
	}

	switch {
	case err.Ahead > 0:
		dialog.RemoteStatus = fmt.Sprintf("ahead of upstream by %d commit(s)", err.Ahead)
	case err.Behind > 0:
		dialog.RemoteStatus = fmt.Sprintf("behind upstream by %d commit(s)", err.Behind)
	}

	if err.Ahead > 0 && err.CanPush {
		if err.Ahead == 1 {
			dialog.Warnings = append(dialog.Warnings, "This branch already has 1 local commit ready to push.")
		} else {
			dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("This branch already has %d local commits ready to push.", err.Ahead))
		}
	} else if warning := strings.TrimSpace(err.PushWarning); warning != "" {
		dialog.Warnings = append(dialog.Warnings, warning)
	}

	return dialog
}

func gitStatusDialogReadyStatus(dialog gitStatusDialog) string {
	if ready := strings.TrimSpace(dialog.ReadyStatus); ready != "" {
		return ready
	}
	if dialog.CanPush {
		if dialog.Ahead == 1 {
			return "Nothing new to commit. Enter push 1 existing commit, Esc cancel"
		}
		return fmt.Sprintf("Nothing new to commit. Enter push %d existing commits, Esc cancel", max(1, dialog.Ahead))
	}
	return "Nothing new to commit. Enter close, Esc close"
}

func gitStatusDialogDismissStatus(dialog gitStatusDialog) string {
	if dismiss := strings.TrimSpace(dialog.DismissStatus); dismiss != "" {
		return dismiss
	}
	if dialog.CanPush {
		return "Nothing new to commit. Use /push to send existing commits."
	}
	return "No changes to commit"
}

func gitStatusDialogFromSubmoduleAttention(err service.SubmoduleAttentionError, intent service.GitActionIntent, message string) gitStatusDialog {
	projectName := strings.TrimSpace(err.ProjectName)
	if projectName == "" && strings.TrimSpace(err.ProjectPath) != "" {
		projectName = filepath.Base(err.ProjectPath)
	}
	if projectName == "" {
		projectName = "(unknown project)"
	}

	branch := strings.TrimSpace(err.Branch)
	if branch == "" {
		branch = "(detached)"
	}

	dialog := gitStatusDialog{
		Title:             "Submodule Attention",
		ProjectPath:       err.ProjectPath,
		ProjectName:       projectName,
		Branch:            branch,
		Status:            submoduleAttentionStatusLine(err),
		ReadyStatus:       "Submodules need attention. Enter resolve & continue, Esc close",
		DismissStatus:     "Submodules still need attention",
		ResolveSubmodules: true,
		CommitIntent:      intent,
		CommitMessage:     message,
	}

	if len(err.DirtySubmodules) == 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("Submodule %s has local changes inside it; resolve & continue will commit those changes and push that submodule.", err.DirtySubmodules[0]))
	} else if len(err.DirtySubmodules) > 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("These submodules have local changes inside them; resolve & continue will commit and push them: %s.", strings.Join(err.DirtySubmodules, ", ")))
	}
	if len(err.UnpushedSubmodules) == 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("Submodule %s has local commits that are not pushed to upstream; resolve & continue will push that submodule.", err.UnpushedSubmodules[0]))
	} else if len(err.UnpushedSubmodules) > 1 {
		dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("These submodules have local commits that are not pushed to upstream; resolve & continue will push them: %s.", strings.Join(err.UnpushedSubmodules, ", ")))
	}
	if len(dialog.Warnings) == 0 {
		if len(err.Submodules) == 1 {
			dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("Submodule %s needs attention before committing the parent repo.", err.Submodules[0]))
		} else if len(err.Submodules) > 1 {
			dialog.Warnings = append(dialog.Warnings, fmt.Sprintf("These submodules need attention before committing the parent repo: %s.", strings.Join(err.Submodules, ", ")))
		}
	}
	dialog.Warnings = append(dialog.Warnings, "Fresh worktree creation can fail until required submodule commits are available from the submodule remote.")
	if warning := strings.TrimSpace(err.PushWarning); warning != "" {
		dialog.Warnings = append(dialog.Warnings, warning)
	}
	return dialog
}

func submoduleAttentionStatusLine(err service.SubmoduleAttentionError) string {
	dirtyCount := len(err.DirtySubmodules)
	unpushedCount := len(err.UnpushedSubmodules)
	switch {
	case dirtyCount > 0 && unpushedCount > 0:
		return fmt.Sprintf("%d dirty and %d unpushed submodule(s) need attention.", dirtyCount, unpushedCount)
	case dirtyCount > 0:
		return fmt.Sprintf("%d dirty submodule(s) need attention.", dirtyCount)
	case unpushedCount > 0:
		return fmt.Sprintf("%d submodule(s) have local commits to push.", unpushedCount)
	default:
		return "Submodules need attention before the parent repo can commit."
	}
}

func gitStatusDialogFromSubmoduleResolved(err service.SubmoduleResolvedNoParentChangesError) gitStatusDialog {
	projectName := strings.TrimSpace(err.ProjectName)
	if projectName == "" && strings.TrimSpace(err.ProjectPath) != "" {
		projectName = filepath.Base(err.ProjectPath)
	}
	if projectName == "" {
		projectName = "(unknown project)"
	}

	branch := strings.TrimSpace(err.Branch)
	if branch == "" {
		branch = "(detached)"
	}

	dialog := gitStatusDialog{
		Title:         "Submodules Resolved",
		ProjectPath:   err.ProjectPath,
		ProjectName:   projectName,
		Branch:        branch,
		Status:        "Parent repo has no new commit to prepare.",
		ReadyStatus:   "Submodules resolved. Enter close, Esc close",
		DismissStatus: "Submodules resolved; no parent commit needed",
	}
	if summary := strings.TrimSpace(err.Summary); summary != "" {
		dialog.Warnings = append(dialog.Warnings, summary)
	}
	return dialog
}

func renderGitStatusDialogActions(dialog gitStatusDialog) string {
	if dialog.ResolveSubmodules {
		actions := []string{
			renderDialogAction("Enter", "resolve & continue", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		}
		return strings.Join(actions, "   ")
	}
	primaryLabel := "close"
	keyStyle := commitActionKeyStyle
	textStyle := commitActionTextStyle
	if dialog.CanPush {
		if dialog.Ahead == 1 {
			primaryLabel = "push 1 existing commit"
		} else {
			primaryLabel = fmt.Sprintf("push %d existing commits", max(1, dialog.Ahead))
		}
		keyStyle = pushActionKeyStyle
		textStyle = pushActionTextStyle
	}
	actions := []string{
		renderDialogAction("Enter", primaryLabel, keyStyle, textStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}

func buildCommitTodoItems(suggested []service.TodoCompletion) []commitTodoItem {
	if len(suggested) == 0 {
		return nil
	}
	items := make([]commitTodoItem, len(suggested))
	for i, s := range suggested {
		items[i] = commitTodoItem{ID: s.ID, Text: s.Text, Selected: true}
	}
	return items
}

func selectedCommitTodoIDs(items []commitTodoItem) []int64 {
	var ids []int64
	for _, item := range items {
		if item.Selected {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func renderCommitTodoCompletions(items []commitTodoItem, selected, width, limit int) []string {
	if limit <= 0 {
		limit = len(items)
	}
	visible := min(limit, len(items))
	maxTextWidth := max(12, width-8)
	lines := make([]string, 0, visible+2)
	for i := 0; i < visible; i++ {
		item := items[i]
		checkbox := "[ ] "
		if item.Selected {
			checkbox = "[x] "
		}
		text := truncateText(item.Text, maxTextWidth)
		row := checkbox + text
		if i == selected {
			row = commitPreviewInfoStyle.Bold(true).Render(row)
		} else if item.Selected {
			row = commitPreviewInfoStyle.Render(row)
		} else {
			row = detailMutedStyle.Render(row)
		}
		lines = append(lines, row)
	}
	if visible < len(items) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("+ %d more", len(items)-visible)))
	}
	lines = append(lines, commandPaletteHintStyle.Render("Space toggle, ↑↓ navigate"))
	return lines
}

func renderCommitPreviewActions(canPush bool) string {
	actions := []string{renderDialogAction("Enter", "commit", commitActionKeyStyle, commitActionTextStyle)}
	if canPush {
		actions = append(actions, renderDialogAction("Alt+Enter", "commit & push", pushActionKeyStyle, pushActionTextStyle))
	} else {
		actions = append(actions, renderDialogAction("Alt+Enter", "push unavailable", disabledActionKeyStyle, disabledActionTextStyle))
	}
	actions = append(actions, renderDialogAction("d", "diff", navigateActionKeyStyle, navigateActionTextStyle))
	actions = append(actions, renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle))
	return strings.Join(actions, "   ")
}

func renderDialogAction(key, label string, keyStyle, labelStyle lipgloss.Style) string {
	return uistyle.RenderDialogAction(key, label, keyStyle, labelStyle, dialogPanelFillStyle)
}

func renderCommandPaletteActions() string {
	actions := []string{
		renderDialogAction("Enter", "run", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "complete", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Up/Down", "choose", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}

func (m Model) renderCommandPaletteContent(width int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Command Palette"),
	}

	if p, ok := m.selectedProject(); ok {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: "+p.Name))
	} else {
		lines = append(lines, commandPaletteHintStyle.Render("Selected project: none"))
	}

	lines = append(lines, "")
	input := m.commandInput
	input.Width = max(12, width-2)
	lines = append(lines, input.View())
	lines = append(lines, renderCommandPaletteActions())
	lines = append(lines, "")
	lines = append(lines, commandPaletteTitleStyle.Render("Suggestions"))

	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No matching commands. Try /help or /refresh."))
	} else {
		start, end := m.commandSuggestionWindow(len(suggestions))
		if start > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
		}
		for i := start; i < end; i++ {
			row := m.renderCommandSuggestionRow(suggestions[i], i == m.commandSelected, width)
			lines = append(lines, row)
		}
		if end < len(suggestions) {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(suggestions)-end)))
		}
	}

	if selected, ok := m.selectedCommandSuggestion(); ok && strings.TrimSpace(selected.Summary) != "" {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("About"))
		lines = append(lines, commandPaletteHintStyle.Render(selected.Summary))
	}

	return strings.Join(lines, "\n")
}

func (m Model) commandSuggestionWindow(total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}

	limit := min(5, total)
	start := 0
	if m.commandSelected >= limit {
		start = m.commandSelected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func (m Model) renderCommandSuggestionRow(s commands.Suggestion, selected bool, width int) string {
	left := s.Display
	if left == "" {
		left = s.Insert
	}
	right := strings.TrimSpace(s.Summary)
	maxLeft := max(12, min(28, width/3))
	left = truncateText(left, maxLeft)
	if right != "" {
		right = truncateText(right, max(12, width-maxLeft-7))
	}

	marker := " "
	if selected {
		marker = ">"
	}
	row := marker + " " + left
	if right != "" {
		row += "  " + right
	}
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	row = marker + " " + commandPalettePickStyle.Render(left)
	if right != "" {
		row += "  " + commandPaletteRowStyle.Render(right)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func commandVisibilityMode(mode commands.ViewMode) projectVisibilityMode {
	switch mode {
	case commands.ViewAll:
		return visibilityAllFolders
	default:
		return visibilityAIFolders
	}
}

// overlayBlock keeps the existing panes visible around the modal instead of
// replacing the whole body with a centered popup.
func overlayBlock(base, overlay string, width, height, left, top int) string {
	baseLines := blockLines(base, width, height)
	overlayLines := blockLines(overlay, lipgloss.Width(overlay), lipgloss.Height(overlay))
	overlayWidth := lipgloss.Width(overlay)

	for row, overlayLine := range overlayLines {
		target := top + row
		if target < 0 || target >= len(baseLines) {
			continue
		}
		baseLine := baseLines[target]
		prefix := ansi.Cut(baseLine, 0, left)
		suffix := ansi.Cut(baseLine, left+overlayWidth, width)
		baseLines[target] = prefix + overlayLine + suffix
	}

	return strings.Join(baseLines, "\n")
}

func blockLines(block string, width, height int) []string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	filled := lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, block)
	lines := strings.Split(filled, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Left, line)
	}
	return lines
}

func (m Model) currentUsage() model.LLMSessionUsage {
	if m.svc == nil {
		return model.LLMSessionUsage{}
	}
	return m.svc.SessionUsage()
}

func (m Model) footerUsageLabel() string {
	if !m.setupChecked {
		switch backend := m.currentSettingsBaseline().AIBackend; backend {
		case config.AIBackendDisabled:
			return "AI disabled"
		default:
			if backend.UsesLocalProviderPath() {
				return compactLocalUsageLabel(backend.Label(), m.currentUsage())
			}
			return compactUsageLabel(m.currentUsage())
		}
	}
	switch status := m.setupSnapshot.SelectedStatus(); {
	case m.setupSnapshot.NeedsSetup():
		return "AI setup"
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return "AI disabled"
	case m.setupSnapshot.Selected != config.AIBackendUnset && !status.Ready:
		return "AI unavailable"
	default:
		if m.setupSnapshot.Selected.UsesLocalProviderPath() {
			return compactLocalUsageLabel(m.setupSnapshot.Selected.Label(), m.currentUsage())
		}
		return compactUsageLabel(m.currentUsage())
	}
}

func (m Model) aiBackendStatusNotice() string {
	if !m.setupChecked {
		switch m.currentSettingsBaseline().AIBackend {
		case config.AIBackendDisabled:
			return "AI disabled"
		default:
			return ""
		}
	}
	switch status := m.setupSnapshot.SelectedStatus(); {
	case m.setupSnapshot.NeedsSetup():
		return "Use /setup to enable AI"
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return "AI disabled"
	case m.setupSnapshot.Selected != config.AIBackendUnset && !status.Ready:
		return "AI unavailable (use /setup)"
	default:
		return ""
	}
}

func (m Model) renderAIBackendStatusNotice() string {
	notice := m.aiBackendStatusNotice()
	if notice == "" {
		return ""
	}
	switch {
	case m.setupSnapshot.NeedsSetup():
		return topStatusSetupBadgeStyle.Render(notice)
	case m.setupSnapshot.Selected == config.AIBackendDisabled:
		return detailMutedStyle.Render(notice)
	default:
		return topStatusWarningBadgeStyle.Render(notice)
	}
}

func compactUsageLabel(usage model.LLMSessionUsage) string {
	if !usage.Enabled {
		return "cost off"
	}
	estimatedCostUSD, ok := estimatedUsageCostUSD(usage)
	if !ok {
		if fallback := compactUnknownCostUsageLabel(usage); fallback != "" {
			return fallback
		}
		return "usage unknown"
	}
	return "cost " + formatEstimatedCostUSD(estimatedCostUSD)
}

func compactUnknownCostUsageLabel(usage model.LLMSessionUsage) string {
	modelLabel := compactUsageModelLabel(usage.Model)
	tokens := compactUsageTokenLabel(usage.Totals)
	if modelLabel != "" && tokens != "" {
		return modelLabel + " " + tokens
	}
	if tokens != "" {
		return "tokens " + tokens
	}
	if usage.Running > 0 && modelLabel != "" {
		return modelLabel + " running"
	}
	if modelLabel != "" {
		return "model " + modelLabel
	}
	if usage.Running > 0 {
		return "AI running"
	}
	callCount := usage.Completed + usage.Failed
	if callCount <= 0 {
		callCount = usage.Started
	}
	if callCount == 1 {
		return "AI 1 call"
	}
	if callCount > 1 {
		return fmt.Sprintf("AI %d calls", callCount)
	}
	return ""
}

func compactUsageTokenLabel(usage model.LLMUsage) string {
	includeTotal := usage.InputTokens == 0 && usage.OutputTokens == 0
	return uistyle.FormatCompactTokenUsage(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CachedInputTokens,
		usage.ReasoningTokens,
		usage.TotalTokens,
		includeTotal,
	)
}

func compactUsageModelLabel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}
	if slash := strings.LastIndex(modelName, "/"); slash >= 0 {
		modelName = modelName[slash+1:]
	}
	if modelName == "" {
		return ""
	}
	return truncateText(modelName, 16)
}

func compactLocalUsageLabel(providerLabel string, usage model.LLMSessionUsage) string {
	providerLabel = strings.TrimSpace(providerLabel)
	if providerLabel == "" {
		providerLabel = "AI"
	}
	if usage.Running > 0 {
		return providerLabel + " running"
	}
	callCount := usage.Completed + usage.Failed
	if callCount <= 0 {
		callCount = usage.Started
	}
	if callCount <= 0 {
		return providerLabel + " ready"
	}
	if callCount == 1 {
		return providerLabel + " 1 call"
	}
	return fmt.Sprintf("%s %d calls", providerLabel, callCount)
}

func (m Model) renderFooterUsageSegment(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch text {
	case "AI setup":
		return topStatusSetupBadgeStyle.Render(text)
	case "AI unavailable":
		return topStatusWarningBadgeStyle.Render(text)
	case "AI disabled":
		return detailMutedStyle.Render(text)
	}
	if !m.usagePulseUntil.After(m.currentTime()) {
		return renderFooterUsage(text)
	}
	if m.spinnerFrame%2 == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("186")).Bold(true).Render(text)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("59")).Bold(true).Render(text)
}

func compactFooterBase(width int, focused paneFocus, detailScroll, runtimeScroll float64, hasHiddenCodex bool, launchLabel string, projectActions []footerAction) string {
	if strings.TrimSpace(launchLabel) == "" {
		launchLabel = "Session"
	}
	if focused == focusDetail {
		detailPercent := int(detailScroll * 100)
		switch {
		case width >= 80:
			return joinFooterSegments(
				renderFooterMeta("Focus: detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("PgUp/PgDn", "page"),
					footerNavAction("Tab", "switch"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", detailPercent)),
			)
		case width >= 60:
			return joinFooterSegments(
				renderFooterMeta("Focus: detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("Tab", "switch"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", detailPercent)),
			)
		default:
			return joinFooterSegments(
				renderFooterMeta("Detail"),
				renderFooterActionList(
					footerHideAction("Esc", "list"),
					footerNavAction("/", "cmd"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
			)
		}
	}
	if focused == focusRuntime {
		runtimePercent := int(runtimeScroll * 100)
		switch {
		case width >= 80:
			return joinFooterSegments(
				renderFooterMeta("Focus: runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "action"),
					footerNavAction("Left/Right", "pick"),
					footerNavAction("PgUp/PgDn", "page"),
					footerNavAction("Tab", "switch"),
					footerHideAction("Esc", "list"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", runtimePercent)),
			)
		case width >= 60:
			return joinFooterSegments(
				renderFooterMeta("Focus: runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "action"),
					footerNavAction("L/R", "pick"),
					footerNavAction("Tab", "switch"),
					footerHideAction("Esc", "list"),
					footerLowAction("?", "help"),
					footerExitAction("q", "quit"),
				),
				renderFooterStatus(fmt.Sprintf("%d%%", runtimePercent)),
			)
		default:
			return joinFooterSegments(
				renderFooterMeta("Runtime"),
				renderFooterActionList(
					footerPrimaryAction("Enter", "run"),
					footerNavAction("L/R", "pick"),
					footerHideAction("Esc", "list"),
					footerExitAction("q", "quit"),
				),
			)
		}
	}
	switch {
	case width >= 80:
		if hasHiddenCodex {
			actions := []footerAction{
				footerPrimaryAction("Enter", launchLabel),
			}
			actions = append(actions, projectActions...)
			actions = append(actions,
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			)
			return joinFooterSegments(
				renderFooterActionList(actions...),
			)
		}
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
		}
		actions = append(actions, projectActions...)
		actions = append(actions,
			footerNavAction("Tab", "switch"),
			footerNavAction("t", "TODO"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		)
		return joinFooterSegments(
			renderFooterActionList(actions...),
		)
	case width >= 60:
		if hasHiddenCodex {
			actions := []footerAction{
				footerPrimaryAction("Enter", launchLabel),
			}
			actions = append(actions, projectActions...)
			actions = append(actions,
				footerLowAction("?", "help"),
				footerExitAction("q", "quit"),
			)
			return joinFooterSegments(
				renderFooterActionList(actions...),
			)
		}
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
		}
		actions = append(actions, projectActions...)
		actions = append(actions,
			footerNavAction("Tab", "switch"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		)
		return joinFooterSegments(
			renderFooterActionList(actions...),
		)
	default:
		actions := []footerAction{
			footerPrimaryAction("Enter", launchLabel),
			footerNavAction("/", "cmd"),
			footerLowAction("?", "help"),
			footerExitAction("q", "quit"),
		}
		if hasHiddenCodex {
			actions = []footerAction{
				footerPrimaryAction("Enter", launchLabel),
				footerNavAction("/", "cmd"),
				footerExitAction("q", "quit"),
			}
		}
		return joinFooterSegments(renderFooterActionList(actions...))
	}
}

func formatTokenCount(v int64) string {
	return uistyle.FormatTokenCount(v)
}

func estimatedUsageCostUSD(usage model.LLMSessionUsage) (float64, bool) {
	if usage.Totals.EstimatedCostUSD > 0 {
		return usage.Totals.EstimatedCostUSD, true
	}
	if usage.Totals.InputTokens == 0 && usage.Totals.OutputTokens == 0 && usage.Totals.TotalTokens == 0 {
		return 0, true
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(usage.Model, usage.Totals); ok {
		return estimatedCostUSD, true
	}
	return 0, false
}

func formatEstimatedCostUSD(costUSD float64) string {
	switch {
	case costUSD >= 1:
		return fmt.Sprintf("$%.2f", costUSD)
	case costUSD >= 0.01:
		return fmt.Sprintf("$%.3f", costUSD)
	default:
		return fmt.Sprintf("$%.4f", costUSD)
	}
}

func helpPanelLines() []string {
	return []string{
		detailSectionStyle.Render("Palette"),
		renderHelpPanelActionRow(
			renderDialogAction("/", "open slash-command palette", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Tab", "complete there", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("?", "toggle help", commitActionKeyStyle, commitActionTextStyle),
		),
		commandPaletteHintStyle.Render("Try /setup, /ai, /perf, /errors, /codex, /todo, /skills, /cpu, /ports, /remove, /wt merge|remove|prune, /commit, /diff, or /run."),
		detailSectionStyle.Render("Navigate"),
		renderHelpPanelActionRow(
			renderDialogAction("Tab", "switch pane", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("↑/↓ or j/k", "move", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("PgUp/PgDn", "page", navigateActionKeyStyle, navigateActionTextStyle),
		),
		renderHelpPanelActionRow(
			renderDialogAction("Enter", "open/send", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "back out", cancelActionKeyStyle, cancelActionTextStyle),
		),
		detailSectionStyle.Render("Quick Actions"),
		renderHelpPanelActionRow(
			renderDialogAction("f", "filter", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("a", "cycle tab", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("b", "boss", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("t", "todo", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("o/v", "sort/view", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("p", "pin", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("ctrl+v", "image", pushActionKeyStyle, pushActionTextStyle),
		),
		detailSectionStyle.Render("Compose & Status"),
		renderHelpPanelActionRow(
			renderDialogAction("Alt+Enter", "newline", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("ctrl+c", "interrupt busy session", cancelActionKeyStyle, cancelActionTextStyle),
		),
		detailSectionStyle.Render("Legend"),
		renderHelpPanelLegendLine(),
		renderHelpPanelActionRow(
			renderDialogAction("q", "quit", disabledActionKeyStyle, disabledActionTextStyle),
		),
	}
}

func renderHelpPanelActionRow(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, dialogPanelFillStyle.Render("   "))
}

func renderHelpPanelLegendLine() string {
	legend := []string{
		renderDialogAction("AGENT", "live", detailLabelStyle, detailValueStyle),
		renderDialogAction("TODO", "open", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("RUN", "runtime", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("!", "warning", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(legend, "  ")
}

func renderDialogPanel(panelWidth, panelInnerWidth int, content string) string {
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dialogPanelBorderColor).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(content, panelInnerWidth))
}

func fillDialogBlock(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fillDialogLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fillDialogLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	line = dialogPanelResetReplacer.Replace(line)
	visibleWidth := lipgloss.Width(line)
	line = dialogPanelFillStyle.Render(line)
	if visibleWidth >= width {
		return line
	}
	return line + dialogPanelFillStyle.Render(strings.Repeat(" ", width-visibleWidth))
}

func fitFooterWidth(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	if width <= 3 {
		return ansi.Cut(line, 0, width)
	}
	return ansi.Truncate(line, width, "...")
}
