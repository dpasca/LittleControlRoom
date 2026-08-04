package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexCleanupDialogState struct {
	Audit        service.CodexCleanupAudit
	Selected     int
	Chosen       map[string]bool
	Loading      bool
	Confirming   bool
	Deleting     bool
	Finished     bool
	Queue        []service.CodexCleanupWorktreeGroup
	QueueIndex   int
	Results      []codexCleanupDeleteResult
	ErrorMessage string
}

type codexCleanupDeleteResult struct {
	Group  service.CodexCleanupWorktreeGroup
	Result service.DeleteCodexCleanupWorktreeResult
	Err    error
}

type codexCleanupAuditMsg struct {
	audit service.CodexCleanupAudit
	err   error
}

type codexCleanupDeleteMsg struct {
	group  service.CodexCleanupWorktreeGroup
	result service.DeleteCodexCleanupWorktreeResult
	err    error
}

func (m Model) openCodexCleanup() (tea.Model, tea.Cmd) {
	m.codexCleanup = &codexCleanupDialogState{
		Chosen:  make(map[string]bool),
		Loading: true,
	}
	m.status = "Auditing Codex session storage..."
	return m, m.loadCodexCleanupAuditCmd()
}

func (m Model) loadCodexCleanupAuditCmd() tea.Cmd {
	svc := m.svc
	manager := m.codexManager
	cachedLoadedThreadIDs := m.cachedLoadedCodexThreadIDs()
	return func() tea.Msg {
		if svc == nil {
			return codexCleanupAuditMsg{err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		loadedThreadIDs := append(cachedLoadedThreadIDs, codexapp.LoadedThreadIDs(manager)...)
		audit, err := svc.AuditCodexSessionStorage(ctx, service.CodexCleanupAuditOptions{
			LoadedThreadIDs: loadedThreadIDs,
		})
		err = timeoutActionError(err, tuiProjectActionTimeout, "auditing Codex session storage")
		return codexCleanupAuditMsg{audit: audit, err: err}
	}
}

func (m Model) deleteCodexCleanupGroupCmd(group service.CodexCleanupWorktreeGroup) tea.Cmd {
	svc := m.svc
	manager := m.codexManager
	cachedLoadedThreadIDs := m.cachedLoadedCodexThreadIDs()
	rootThreadIDs := make([]string, 0, len(group.Threads))
	for _, thread := range group.Threads {
		rootThreadIDs = append(rootThreadIDs, thread.ID)
	}
	return func() tea.Msg {
		if svc == nil {
			return codexCleanupDeleteMsg{group: group, err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		loadedThreadIDs := append(cachedLoadedThreadIDs, codexapp.LoadedThreadIDs(manager)...)
		result, err := svc.DeleteCodexCleanupWorktree(ctx, service.DeleteCodexCleanupWorktreeRequest{
			WorktreePath:    group.WorktreePath,
			RootThreadIDs:   rootThreadIDs,
			Revision:        group.Revision,
			LoadedThreadIDs: loadedThreadIDs,
		})
		err = timeoutActionError(err, tuiGitActionTimeout, "permanently deleting Codex sessions")
		return codexCleanupDeleteMsg{group: group, result: result, err: err}
	}
}

func (m Model) cachedLoadedCodexThreadIDs() []string {
	set := make(map[string]struct{})
	for _, snapshot := range m.codexSnapshots {
		if snapshot.Provider.Normalized() != codexapp.ProviderCodex || snapshot.Closed {
			continue
		}
		if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
			set[threadID] = struct{}{}
		}
	}
	for _, state := range m.mergeConflictResolvers {
		if state.Provider.Normalized() != codexapp.ProviderCodex || !state.active() {
			continue
		}
		if threadID := strings.TrimSpace(state.SessionID); threadID != "" {
			set[threadID] = struct{}{}
		}
	}
	threadIDs := make([]string, 0, len(set))
	for threadID := range set {
		threadIDs = append(threadIDs, threadID)
	}
	sort.Strings(threadIDs)
	return threadIDs
}

func (m Model) applyCodexCleanupAudit(msg codexCleanupAuditMsg) (tea.Model, tea.Cmd) {
	dialog := m.codexCleanup
	if dialog == nil || !dialog.Loading {
		return m, nil
	}
	dialog.Loading = false
	if msg.err != nil {
		dialog.ErrorMessage = msg.err.Error()
		m.reportError("Codex cleanup audit failed", msg.err, "")
		return m, nil
	}
	dialog.Audit = msg.audit
	dialog.Chosen = make(map[string]bool)
	dialog.Selected = 0
	dialog.ErrorMessage = ""
	m.err = nil
	if len(msg.audit.Groups) == 0 {
		m.status = fmt.Sprintf("Codex cleanup audit complete: no eligible sessions (%d excluded by safeguards)", msg.audit.Excluded.Total())
		return m, nil
	}
	m.status = fmt.Sprintf(
		"Codex cleanup audit: %d worktree%s, %d root thread%s, %s recoverable",
		len(msg.audit.Groups), pluralSuffix(len(msg.audit.Groups)),
		msg.audit.EligibleRootThreads, pluralSuffix(msg.audit.EligibleRootThreads),
		formatUpdateBytes(msg.audit.RecoverableBytes),
	)
	return m, nil
}

func (m Model) applyCodexCleanupDelete(msg codexCleanupDeleteMsg) (tea.Model, tea.Cmd) {
	dialog := m.codexCleanup
	if dialog == nil || !dialog.Deleting || dialog.QueueIndex >= len(dialog.Queue) {
		return m, nil
	}
	expected := dialog.Queue[dialog.QueueIndex]
	if normalizeProjectPath(expected.WorktreePath) != normalizeProjectPath(msg.group.WorktreePath) {
		return m, nil
	}
	dialog.Results = append(dialog.Results, codexCleanupDeleteResult{Group: msg.group, Result: msg.result, Err: msg.err})
	dialog.QueueIndex++
	if msg.err != nil {
		dialog.Deleting = false
		dialog.Finished = true
		dialog.ErrorMessage = msg.err.Error()
		m.reportError("Codex cleanup stopped", msg.err, msg.group.WorktreePath)
		return m, nil
	}
	if dialog.QueueIndex < len(dialog.Queue) {
		next := dialog.Queue[dialog.QueueIndex]
		m.status = fmt.Sprintf("Codex cleanup %d/%d verified; deleting %s...", dialog.QueueIndex, len(dialog.Queue), filepath.Base(next.WorktreePath))
		return m, m.deleteCodexCleanupGroupCmd(next)
	}

	dialog.Deleting = false
	dialog.Finished = true
	reclaimed, allVerified := codexCleanupVerifiedTotal(dialog.Results)
	if allVerified {
		m.status = fmt.Sprintf("Codex cleanup complete: %d worktree%s, %s reclaimed and verified", len(dialog.Results), pluralSuffix(len(dialog.Results)), formatUpdateBytes(reclaimed))
	} else {
		m.status = fmt.Sprintf("Codex cleanup finished: %s verified reclaimed; review verification details", formatUpdateBytes(reclaimed))
	}
	return m, nil
}

func (m Model) updateCodexCleanupMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.codexCleanup
	if dialog == nil {
		return m, nil
	}
	if dialog.Deleting {
		if msg.String() == "esc" {
			m.status = "Codex deletion is already in progress and cannot be canceled safely"
		}
		return m, nil
	}
	if dialog.Loading {
		if msg.String() == "esc" {
			m.codexCleanup = nil
			m.status = "Codex cleanup audit closed; no sessions were deleted"
		}
		return m, nil
	}
	if dialog.Finished {
		if msg.String() == "esc" || msg.String() == "enter" {
			m.codexCleanup = nil
			m.status = "Codex cleanup report closed"
		}
		return m, nil
	}
	if dialog.Confirming {
		switch msg.String() {
		case "esc":
			dialog.Confirming = false
			m.status = "Permanent deletion canceled; selection is unchanged"
		case "d", "D":
			dialog.Queue = selectedCodexCleanupGroups(dialog)
			if len(dialog.Queue) == 0 {
				dialog.Confirming = false
				m.status = "Select at least one worktree before deleting"
				return m, nil
			}
			dialog.Confirming = false
			dialog.Deleting = true
			dialog.QueueIndex = 0
			dialog.Results = nil
			dialog.ErrorMessage = ""
			first := dialog.Queue[0]
			m.status = fmt.Sprintf("Codex cleanup 0/%d; deleting %s...", len(dialog.Queue), filepath.Base(first.WorktreePath))
			return m, m.deleteCodexCleanupGroupCmd(first)
		}
		return m, nil
	}

	groups := dialog.Audit.Groups
	switch msg.String() {
	case "esc":
		m.codexCleanup = nil
		m.status = "Codex cleanup closed; no sessions were deleted"
	case "r":
		dialog.Loading = true
		dialog.ErrorMessage = ""
		dialog.Chosen = make(map[string]bool)
		m.status = "Refreshing Codex session storage audit..."
		return m, m.loadCodexCleanupAuditCmd()
	case "up", "k":
		if len(groups) > 0 {
			dialog.Selected = max(0, dialog.Selected-1)
		}
	case "down", "j":
		if len(groups) > 0 {
			dialog.Selected = min(len(groups)-1, dialog.Selected+1)
		}
	case "pgup", "ctrl+u":
		if len(groups) > 0 {
			dialog.Selected = max(0, dialog.Selected-5)
		}
	case "pgdown", "ctrl+d":
		if len(groups) > 0 {
			dialog.Selected = min(len(groups)-1, dialog.Selected+5)
		}
	case "home", "g":
		dialog.Selected = 0
	case "end", "G":
		if len(groups) > 0 {
			dialog.Selected = len(groups) - 1
		}
	case " ":
		if len(groups) > 0 {
			index := max(0, min(dialog.Selected, len(groups)-1))
			path := groups[index].WorktreePath
			dialog.Chosen[path] = !dialog.Chosen[path]
		}
	case "enter":
		if len(selectedCodexCleanupGroups(dialog)) == 0 {
			m.status = "Select one or more worktrees with Space; nothing was deleted"
			return m, nil
		}
		dialog.Confirming = true
		m.status = "Review the permanent-deletion warning; press D only if the selection is correct"
	}
	return m, nil
}

func selectedCodexCleanupGroups(dialog *codexCleanupDialogState) []service.CodexCleanupWorktreeGroup {
	if dialog == nil {
		return nil
	}
	groups := make([]service.CodexCleanupWorktreeGroup, 0)
	for _, group := range dialog.Audit.Groups {
		if dialog.Chosen[group.WorktreePath] {
			groups = append(groups, group)
		}
	}
	return groups
}

func renderCodexCleanupOverlay(body string, bodyW, bodyH int, dialog *codexCleanupDialogState, spinnerFrame int, now time.Time) string {
	if dialog == nil {
		return body
	}
	panelW := min(max(72, bodyW-12), 112)
	panelInnerW := max(36, panelW-4)
	content := renderCodexCleanupContent(dialog, panelInnerW, bodyH, spinnerFrame, now)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexCleanupOverlay(body string, bodyW, bodyH int) string {
	return renderCodexCleanupOverlay(body, bodyW, bodyH, m.codexCleanup, m.spinnerFrame, m.currentTime())
}

func renderCodexCleanupContent(dialog *codexCleanupDialogState, width, bodyH, spinnerFrame int, now time.Time) string {
	lines := []string{commandPaletteTitleStyle.Render("Clean Codex session storage")}
	if dialog.Loading {
		lines = append(lines,
			commandPaletteHintStyle.Render("Read-only audit: matching missing Codex working directories to LCR-deleted linked worktrees."),
			"",
			commandPaletteHintStyle.Render(spinnerFrames[spinnerFrame%len(spinnerFrames)]+" Checking pins, activity, lineage, descendants, paths, and rollout sizes..."),
			"",
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		)
		return strings.Join(lines, "\n")
	}
	if dialog.Finished {
		return renderCodexCleanupResults(dialog, width, bodyH)
	}
	if dialog.Deleting {
		return renderCodexCleanupProgress(dialog, width, spinnerFrame)
	}
	if dialog.Confirming {
		return renderCodexCleanupConfirmation(dialog, width)
	}

	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width,
		fmt.Sprintf("Only unpinned, unloaded Codex trees inactive for at least %d days and tied to LCR worktrees missing for at least %d days are selectable. External-volume and uncertain trees are excluded.",
			int(service.CodexCleanupRecentWindow/(24*time.Hour)), int(service.CodexCleanupDeletedWorktreeGrace/(24*time.Hour))))...)
	lines = append(lines, "")
	if dialog.ErrorMessage != "" {
		lines = append(lines, detailDangerStyle.Render("Audit failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, dialog.ErrorMessage)...)
		lines = append(lines, "", renderDialogAction("R", "retry", navigateActionKeyStyle, navigateActionTextStyle)+"   "+renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
		return strings.Join(lines, "\n")
	}

	audit := dialog.Audit
	lines = append(lines, detailField("Audit", fmt.Sprintf("%d threads scanned · %d missing cwd · %d safeguards excluded", audit.ScannedThreads, audit.MissingCWDThreads, audit.Excluded.Total())))
	if len(audit.Groups) == 0 {
		lines = append(lines,
			"",
			detailMutedStyle.Render("No Codex session trees are safely eligible for deletion."),
			cleanupExclusionSummary(audit.Excluded),
			"",
			renderDialogAction("R", "audit again", navigateActionKeyStyle, navigateActionTextStyle)+"   "+renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		)
		return strings.Join(lines, "\n")
	}

	selected := selectedCodexCleanupGroups(dialog)
	selectedBytes, selectedRoots, selectedDescendants := codexCleanupGroupTotals(selected)
	lines = append(lines,
		detailField("Eligible", fmt.Sprintf("%d worktree%s · %d root%s + %d descendant%s · %s recoverable", len(audit.Groups), pluralSuffix(len(audit.Groups)), audit.EligibleRootThreads, pluralSuffix(audit.EligibleRootThreads), audit.EligibleDescendants, pluralSuffix(audit.EligibleDescendants), formatUpdateBytes(audit.RecoverableBytes))),
		detailField("Selected", fmt.Sprintf("%d worktree%s · %d root%s + %d descendant%s · %s", len(selected), pluralSuffix(len(selected)), selectedRoots, pluralSuffix(selectedRoots), selectedDescendants, pluralSuffix(selectedDescendants), formatUpdateBytes(selectedBytes))),
		"",
	)

	start, end := cleanupGroupWindow(dialog.Selected, len(audit.Groups), bodyH)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more worktrees", start)))
	}
	for index := start; index < end; index++ {
		group := audit.Groups[index]
		mark := "[ ]"
		if dialog.Chosen[group.WorktreePath] {
			mark = "[x]"
		}
		row := fmt.Sprintf("%s %s · %s old · %d root%s + %d %s · %s",
			mark,
			firstNonEmptyString(group.WorktreeName, filepath.Base(group.WorktreePath)),
			formatCleanupAge(now, group.LastActivity),
			group.RootThreadCount, pluralSuffix(group.RootThreadCount),
			group.DescendantCount, cleanupChildLabel(group.DescendantCount),
			formatUpdateBytes(group.RecoverableBytes),
		)
		style := commandPaletteRowStyle
		if index == dialog.Selected {
			style = commandPaletteSelectStyle
		}
		lines = append(lines, style.Render(truncateText(row, width)))
	}
	if end < len(audit.Groups) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more worktrees", len(audit.Groups)-end)))
	}

	group := audit.Groups[max(0, min(dialog.Selected, len(audit.Groups)-1))]
	lines = append(lines, "", detailField("Path", group.WorktreePath))
	lines = append(lines, detailField("Age", fmt.Sprintf("last Codex activity %s · worktree missing %s", formatCleanupAge(now, group.LastActivity), formatCleanupAge(now, group.MissingSince))))
	gitLine := firstNonEmptyString(group.Branch, "branch unknown")
	if group.ParentBranch != "" {
		gitLine += " · parent " + group.ParentBranch
	}
	lines = append(lines, detailField("Git", gitLine), detailField("Reason", group.Reason))
	for index, thread := range group.Threads {
		if index >= 3 {
			lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("  +%d more root threads", len(group.Threads)-index)))
			break
		}
		git := firstNonEmptyString(thread.GitBranch, group.Branch, "branch unknown")
		if thread.GitSHA != "" {
			git += " @ " + shortID(thread.GitSHA)
		}
		threadLine := fmt.Sprintf("  %s · %s old · %s · %d %s · %s",
			shortID(thread.ID), formatCleanupAge(now, thread.LastActivity), git,
			thread.DescendantCount, cleanupChildLabel(thread.DescendantCount), formatUpdateBytes(thread.RecoverableBytes))
		lines = append(lines, detailMutedStyle.Render(truncateText(threadLine, width)))
	}
	lines = append(lines,
		"",
		renderDialogAction("Space", "select", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Enter", "review deletion", commitActionKeyStyle, commitActionTextStyle)+"   "+
			renderDialogAction("R", "audit again", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return clampDialogContent(strings.Join(lines, "\n"), max(12, bodyH-4), 5, detailMutedStyle.Render("… details clipped to fit terminal …"))
}

func renderCodexCleanupConfirmation(dialog *codexCleanupDialogState, width int) string {
	groups := selectedCodexCleanupGroups(dialog)
	bytes, roots, descendants := codexCleanupGroupTotals(groups)
	lines := []string{
		commandPaletteTitleStyle.Render("Permanent Codex deletion"),
		"",
		detailDangerStyle.Render("WARNING: THIS CANNOT BE UNDONE"),
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width,
		fmt.Sprintf("Codex app-server will permanently delete %d selected worktree group%s: %d root thread%s and %d spawned descendant%s. Conversation rollouts will not be recoverable from LCR.",
			len(groups), pluralSuffix(len(groups)), roots, pluralSuffix(roots), descendants, pluralSuffix(descendants)))...)
	lines = append(lines,
		"",
		detailField("Recoverable disk size", formatUpdateBytes(bytes)),
		detailMutedStyle.Render("LCR will repeat the full safety audit before each worktree. Any changed preview is rejected."),
		"",
	)
	shownGroups := min(len(groups), 6)
	for index := 0; index < shownGroups; index++ {
		lines = append(lines, detailDangerStyle.Render("- "+groups[index].WorktreePath))
	}
	if shownGroups < len(groups) {
		lines = append(lines, detailDangerStyle.Render(fmt.Sprintf("- +%d more selected worktrees", len(groups)-shownGroups)))
	}
	lines = append(lines,
		"",
		renderDialogAction("D", "PERMANENTLY DELETE", commitActionKeyStyle, commitActionTextStyle)+"   "+
			renderDialogAction("Esc", "go back", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}

func renderCodexCleanupProgress(dialog *codexCleanupDialogState, width, spinnerFrame int) string {
	current := service.CodexCleanupWorktreeGroup{}
	if dialog.QueueIndex < len(dialog.Queue) {
		current = dialog.Queue[dialog.QueueIndex]
	}
	reclaimed, _ := codexCleanupVerifiedTotal(dialog.Results)
	lines := []string{
		commandPaletteTitleStyle.Render("Deleting Codex session storage"),
		"",
		commandPaletteHintStyle.Render(spinnerFrames[spinnerFrame%len(spinnerFrames)] + fmt.Sprintf(" Worktree %d of %d", dialog.QueueIndex+1, len(dialog.Queue))),
		detailField("Current", firstNonEmptyString(current.WorktreePath, "verifying final result")),
		detailField("Verified reclaimed", formatUpdateBytes(reclaimed)),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, "Deletion is permanent. LCR is waiting for app-server and verifying that its thread rows and rollout files are gone.")...)
	lines = append(lines, "", detailMutedStyle.Render("Repeated input is disabled while deletion is in flight."))
	return strings.Join(lines, "\n")
}

func renderCodexCleanupResults(dialog *codexCleanupDialogState, width, bodyH int) string {
	reclaimed, allVerified := codexCleanupVerifiedTotal(dialog.Results)
	title := "Codex cleanup report"
	lines := []string{commandPaletteTitleStyle.Render(title), ""}
	if len(dialog.Results) == 0 {
		lines = append(lines, detailWarningStyle.Render("No sessions were deleted."))
	} else if allVerified && dialog.ErrorMessage == "" {
		lines = append(lines, detailValueStyle.Render(fmt.Sprintf("Verified reclaimed space: %s", formatUpdateBytes(reclaimed))))
	} else {
		lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("Verified reclaimed space so far: %s", formatUpdateBytes(reclaimed))))
	}
	lines = append(lines, "")
	for _, item := range dialog.Results {
		status := fmt.Sprintf("%s · %d roots + %d descendants · %s verified",
			filepath.Base(item.Group.WorktreePath), item.Result.DeletedRootThreads, item.Result.DeletedDescendants,
			formatUpdateBytes(item.Result.VerifiedReclaimedBytes))
		if item.Result.Verified {
			lines = append(lines, detailValueStyle.Render("✓ "+status))
		} else {
			lines = append(lines, detailWarningStyle.Render("! "+status))
		}
		if item.Err != nil {
			lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, "  "+item.Err.Error())...)
		}
	}
	if dialog.ErrorMessage != "" && len(dialog.Results) == 0 {
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.ErrorMessage)...)
	}
	lines = append(lines, "", renderDialogAction("Enter/Esc", "close report", cancelActionKeyStyle, cancelActionTextStyle))
	return clampDialogContent(strings.Join(lines, "\n"), max(10, bodyH-4), 3, detailMutedStyle.Render("… earlier results clipped …"))
}

func codexCleanupVerifiedTotal(results []codexCleanupDeleteResult) (int64, bool) {
	allVerified := len(results) > 0
	var total int64
	for _, item := range results {
		total += item.Result.VerifiedReclaimedBytes
		if item.Err != nil || !item.Result.Verified {
			allVerified = false
		}
	}
	return total, allVerified
}

func codexCleanupGroupTotals(groups []service.CodexCleanupWorktreeGroup) (int64, int, int) {
	var bytes int64
	var roots, descendants int
	for _, group := range groups {
		bytes += group.RecoverableBytes
		roots += group.RootThreadCount
		descendants += group.DescendantCount
	}
	return bytes, roots, descendants
}

func cleanupExclusionSummary(excluded service.CodexCleanupExclusions) string {
	parts := make([]string, 0, 6)
	for _, item := range []struct {
		label string
		count int
	}{
		{"pinned", excluded.Pinned},
		{"loaded", excluded.Loaded},
		{"recent", excluded.Recent},
		{"external volume", excluded.ExternalVolume},
		{"no LCR deletion record", excluded.NoLCRRecord},
		{"uncertain", excluded.Uncertain},
	} {
		if item.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", item.count, item.label))
		}
	}
	if len(parts) == 0 {
		return detailMutedStyle.Render("No missing-working-directory Codex roots were found.")
	}
	return detailMutedStyle.Render("Excluded: " + strings.Join(parts, " · "))
}

func cleanupGroupWindow(selected, total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	visible := max(1, min(total, max(2, bodyH-20)))
	selected = max(0, min(selected, total-1))
	start := max(0, selected-visible/2)
	if start+visible > total {
		start = max(0, total-visible)
	}
	return start, min(total, start+visible)
}

func formatCleanupAge(now, at time.Time) string {
	if at.IsZero() {
		return "unknown age"
	}
	age := now.Sub(at)
	if age < 0 {
		age = 0
	}
	days := int(age / (24 * time.Hour))
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(age / time.Hour)
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return "<1h"
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func cleanupChildLabel(count int) string {
	if count == 1 {
		return "child"
	}
	return "children"
}
