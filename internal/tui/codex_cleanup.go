package tui

import (
	"context"
	"errors"
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

const codexCleanupDeleteTimeout = 30 * time.Minute
const codexCleanupAuditTimeout = 2 * time.Minute

func formatCodexCleanupBytes(size int64) string {
	if size < 0 {
		size = 0
	}
	if size >= 1024*1024*1024 {
		return fmt.Sprintf("%.1f GiB", float64(size)/(1024*1024*1024))
	}
	return formatUpdateBytes(size)
}

type codexCleanupDialogState struct {
	Category        service.CodexCleanupCategory
	InactiveDays    int
	Focus           codexCleanupFocus
	Dropdown        bool
	OptionIndex     int
	ReviewDelete    bool
	AuditGeneration int
	AuditCancel     context.CancelFunc
	Audit           service.CodexCleanupAudit
	Selected        int
	ShowRetained    bool
	RetainedIndex   int
	SortMode        int
	Chosen          map[string]bool
	Loading         bool
	Confirming      bool
	Deleting        bool
	Backgrounded    bool
	Finished        bool
	Aborted         bool
	Queue           []service.CodexCleanupWorktreeGroup
	QueueIndex      int
	Results         []codexCleanupDeleteResult
	Cancel          context.CancelFunc
	CancelRequested bool
	ErrorMessage    string
}

type codexCleanupDeleteResult struct {
	Group    service.CodexCleanupWorktreeGroup
	Result   service.DeleteCodexCleanupWorktreeResult
	Canceled bool
	Err      error
}

type codexCleanupAuditMsg struct {
	owner      *codexCleanupDialogState
	generation int
	audit      service.CodexCleanupAudit
	err        error
}

type codexCleanupDeleteMsg struct {
	group  service.CodexCleanupWorktreeGroup
	result service.DeleteCodexCleanupWorktreeResult
	err    error
}

func (m Model) openCodexCleanup() (tea.Model, tea.Cmd) {
	if dialog := m.codexCleanup; dialog != nil {
		m.filterCodexCleanupForPrivacy()
		dialog.Backgrounded = false
		switch {
		case dialog.Deleting && dialog.CancelRequested:
			m.status = "Codex cleanup is stopping; waiting for post-delete verification"
		case dialog.Deleting:
			m.status = fmt.Sprintf("Codex cleanup %d/%d in progress; B hides it, Esc aborts", dialog.QueueIndex+1, len(dialog.Queue))
		case dialog.Finished && dialog.Aborted:
			m.status = "Codex cleanup aborted; report open"
		case dialog.Finished:
			m.status = "Codex cleanup report open"
		}
		return m, nil
	}
	m.codexCleanup = &codexCleanupDialogState{
		InactiveDays: 7,
		Focus:        cleanupFocusCategory,
		Chosen:       make(map[string]bool),
		Loading:      true,
	}
	m.status = "Auditing Codex session storage..."
	return m, m.loadCodexCleanupAuditCmd()
}

func (m Model) codexCleanupVisible() bool {
	return m.codexCleanup != nil && !m.codexCleanup.Backgrounded
}

func (m Model) renderFooterCodexCleanupSegment() string {
	dialog := m.codexCleanup
	if dialog == nil || !dialog.Backgrounded {
		return ""
	}
	switch {
	case dialog.Deleting && dialog.CancelRequested:
		return renderFooterAlert("Codex GC stopping · /codex-gc")
	case dialog.Deleting:
		return renderFooterStatus(fmt.Sprintf("Codex GC %d/%d · /codex-gc", dialog.QueueIndex+1, len(dialog.Queue)))
	case dialog.Finished && dialog.Aborted:
		return renderFooterAlert("Codex GC stopped · /codex-gc report")
	case dialog.Finished:
		return renderFooterStatus("Codex GC report · /codex-gc")
	default:
		return ""
	}
}

func (m Model) loadCodexCleanupAuditCmd() tea.Cmd {
	svc := m.svc
	dialog := m.codexCleanup
	days := 7
	generation := 0
	category := service.CodexCleanupOrphaned
	ctx, cancel := m.actionContext(codexCleanupAuditTimeout)
	if dialog != nil {
		category = dialog.Category
		if category == service.CodexCleanupStale {
			days = max(7, dialog.InactiveDays)
		}
		if dialog.AuditCancel != nil {
			dialog.AuditCancel()
		}
		dialog.AuditCancel = cancel
		dialog.AuditGeneration++
		generation = dialog.AuditGeneration
	}
	manager := m.codexManager
	cachedLoadedThreadIDs := m.cachedLoadedCodexThreadIDs()
	return func() tea.Msg {
		defer cancel()
		if svc == nil {
			return codexCleanupAuditMsg{owner: dialog, generation: generation, err: fmt.Errorf("service unavailable")}
		}
		loadedThreadIDs := append(cachedLoadedThreadIDs, codexapp.LoadedThreadIDs(manager)...)
		audit, err := svc.AuditCodexSessionStorage(ctx, service.CodexCleanupAuditOptions{
			Category:        category,
			InactiveDays:    days,
			LoadedThreadIDs: loadedThreadIDs,
		})
		err = timeoutActionError(err, codexCleanupAuditTimeout, "auditing Codex session storage")
		return codexCleanupAuditMsg{owner: dialog, generation: generation, audit: audit, err: err}
	}
}

func (m *Model) startCodexCleanupGroupDelete(group service.CodexCleanupWorktreeGroup) tea.Cmd {
	svc := m.svc
	manager := m.codexManager
	cachedLoadedThreadIDs := m.cachedLoadedCodexThreadIDs()
	rootThreadIDs := make([]string, 0, len(group.Threads))
	for _, thread := range group.Threads {
		rootThreadIDs = append(rootThreadIDs, thread.ID)
	}
	ctx, cancel := m.actionContext(codexCleanupDeleteTimeout)
	if m.codexCleanup != nil {
		m.codexCleanup.Cancel = cancel
	}
	return func() tea.Msg {
		defer cancel()
		if svc == nil {
			return codexCleanupDeleteMsg{group: group, err: fmt.Errorf("service unavailable")}
		}
		loadedThreadIDs := append(append([]string(nil), cachedLoadedThreadIDs...), codexapp.LoadedThreadIDs(manager)...)
		result, err := svc.DeleteCodexCleanupWorktree(ctx, service.DeleteCodexCleanupWorktreeRequest{
			CurrentLoadedThreadIDs: func() []string { return codexapp.LoadedThreadIDs(manager) },
			Category:               group.Category,
			InactiveDays:           group.InactiveDays,
			WorktreePath:           group.WorktreePath,
			RootProjectPath:        group.RootProjectPath,
			RootThreadIDs:          rootThreadIDs,
			Revision:               group.Revision,
			LoadedThreadIDs:        loadedThreadIDs,
		})
		err = timeoutActionError(err, codexCleanupDeleteTimeout, "permanently deleting Codex sessions")
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
	if msg.owner != nil && (msg.owner != dialog || msg.generation != dialog.AuditGeneration) {
		return m, nil
	}
	dialog.Loading = false
	dialog.AuditCancel = nil
	if msg.err != nil {
		dialog.ErrorMessage = msg.err.Error()
		dialog.Focus = cleanupFocusRefresh
		m.reportError("Codex cleanup audit failed", msg.err, "")
		return m, nil
	}
	dialog.Audit = msg.audit
	dialog.Audit.Groups = append([]service.CodexCleanupWorktreeGroup(nil), msg.audit.Groups...)
	m.filterCodexCleanupForPrivacy()
	sortCodexCleanupGroups(dialog)
	dialog.Chosen = make(map[string]bool)
	dialog.Selected = 0
	dialog.RetainedIndex = 0
	dialog.ErrorMessage = ""
	m.err = nil
	if len(dialog.Audit.Groups) == 0 {
		m.status = fmt.Sprintf("Codex cleanup audit complete: no eligible sessions (%d excluded by safeguards)", msg.audit.Excluded.Total())
		return m, nil
	}
	m.status = fmt.Sprintf(
		"Codex cleanup audit: %d project group%s, %d root thread%s, %s recoverable",
		len(dialog.Audit.Groups), pluralSuffix(len(dialog.Audit.Groups)),
		dialog.Audit.EligibleRootThreads, pluralSuffix(dialog.Audit.EligibleRootThreads),
		formatCodexCleanupBytes(dialog.Audit.RecoverableBytes),
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
	if dialog.Cancel != nil {
		dialog.Cancel()
		dialog.Cancel = nil
	}
	canceled := dialog.CancelRequested || errors.Is(msg.err, context.Canceled)
	dialog.Results = append(dialog.Results, codexCleanupDeleteResult{
		Group:    msg.group,
		Result:   msg.result,
		Canceled: canceled,
		Err:      msg.err,
	})
	dialog.QueueIndex++
	if canceled {
		dialog.CancelRequested = false
		dialog.Deleting = false
		dialog.Finished = true
		dialog.Aborted = true
		dialog.ErrorMessage = ""
		m.err = nil
		reclaimed, _ := codexCleanupVerifiedTotal(dialog.Results)
		verified := codexCleanupVerifiedGroupCount(dialog.Results)
		remaining := max(0, len(dialog.Queue)-dialog.QueueIndex)
		m.status = fmt.Sprintf("Codex cleanup aborted: %d group%s fully verified, %d not started, %s reclaimed", verified, pluralSuffix(verified), remaining, formatCodexCleanupBytes(reclaimed))
		if dialog.Backgrounded {
			m.status += "; /codex-gc opens the report"
		}
		return m, nil
	}
	if msg.err != nil {
		dialog.Deleting = false
		dialog.Finished = true
		dialog.ErrorMessage = msg.err.Error()
		if m.codexCleanupGroupPrivate(msg.group) {
			m.status = "Codex cleanup stopped for a private project"
		} else {
			m.reportError("Codex cleanup stopped", msg.err, msg.group.WorktreePath)
		}
		return m, nil
	}
	if dialog.QueueIndex < len(dialog.Queue) {
		next := dialog.Queue[dialog.QueueIndex]
		m.status = fmt.Sprintf("Codex cleanup %d/%d verified; deleting %s...", dialog.QueueIndex, len(dialog.Queue), m.codexCleanupGroupLabel(next))
		if dialog.Backgrounded {
			m.status += " (background; /codex-gc to view or abort)"
		}
		return m, m.startCodexCleanupGroupDelete(next)
	}

	dialog.Deleting = false
	dialog.Finished = true
	reclaimed, allVerified := codexCleanupVerifiedTotal(dialog.Results)
	if allVerified {
		m.status = fmt.Sprintf("Codex cleanup complete: %d project group%s, %s reclaimed and verified", len(dialog.Results), pluralSuffix(len(dialog.Results)), formatCodexCleanupBytes(reclaimed))
	} else {
		m.status = fmt.Sprintf("Codex cleanup finished: %s verified reclaimed; review verification details", formatCodexCleanupBytes(reclaimed))
	}
	if dialog.Backgrounded {
		m.status += "; /codex-gc opens the report"
	}
	return m, nil
}

func (m Model) updateCodexCleanupMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.codexCleanup
	if dialog == nil {
		return m, nil
	}
	if dialog.Deleting {
		switch msg.String() {
		case "b", "B":
			dialog.Backgrounded = true
			m.status = fmt.Sprintf("Codex cleanup continues in background (%d/%d); /codex-gc reopens progress or abort controls", dialog.QueueIndex+1, len(dialog.Queue))
		case "esc":
			if !dialog.CancelRequested {
				dialog.CancelRequested = true
				if dialog.Cancel != nil {
					dialog.Cancel()
				}
				m.status = "Aborting Codex cleanup; waiting for the active request to stop and verification to finish"
			}
		}
		return m, nil
	}
	if dialog.Loading {
		if msg.String() == "esc" {
			if dialog.AuditCancel != nil {
				dialog.AuditCancel()
			}
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
		case "tab", "shift+tab", "left", "right":
			dialog.ReviewDelete = !dialog.ReviewDelete
		case "esc":
			dialog.Confirming = false
		case "enter", " ":
			if !dialog.ReviewDelete {
				dialog.Confirming = false
				return m, nil
			}
			dialog.Queue = selectedCodexCleanupGroups(dialog)
			if len(dialog.Queue) == 0 {
				dialog.Confirming = false
				return m, nil
			}
			dialog.Confirming = false
			dialog.Deleting = true
			dialog.Backgrounded = false
			dialog.Aborted = false
			dialog.CancelRequested = false
			dialog.QueueIndex = 0
			dialog.Results = nil
			dialog.ErrorMessage = ""
			return m, m.startCodexCleanupGroupDelete(dialog.Queue[0])
		}
		return m, nil
	}
	return m.updateCodexCleanupForm(msg)
}
func allCodexCleanupGroupsSelected(dialog *codexCleanupDialogState) bool {
	if dialog == nil || len(dialog.Audit.Groups) == 0 {
		return false
	}
	for _, group := range dialog.Audit.Groups {
		if !dialog.Chosen[group.WorktreePath] {
			return false
		}
	}
	return true
}

func codexCleanupSortLabel(mode int) string {
	switch mode {
	case 1:
		return "oldest first"
	case 2:
		return "name"
	default:
		return "largest first"
	}
}

func sortCodexCleanupGroups(dialog *codexCleanupDialogState) {
	groups := dialog.Audit.Groups
	focusedPath := ""
	if dialog.Selected >= 0 && dialog.Selected < len(groups) {
		focusedPath = groups[dialog.Selected].WorktreePath
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		switch dialog.SortMode {
		case 0:
			if a.RecoverableBytes != b.RecoverableBytes {
				return a.RecoverableBytes > b.RecoverableBytes
			}
		case 1:
			if !a.LastActivity.Equal(b.LastActivity) {
				return a.LastActivity.Before(b.LastActivity)
			}
		}
		if a.WorktreeName != b.WorktreeName {
			return a.WorktreeName < b.WorktreeName
		}
		return a.WorktreePath < b.WorktreePath
	})
	for index, group := range groups {
		if group.WorktreePath == focusedPath {
			dialog.Selected = index
			break
		}
	}
}

func codexCleanupSelectAllLabel(dialog *codexCleanupDialogState) string {
	if allCodexCleanupGroupsSelected(dialog) {
		return "clear all"
	}
	return "select all"
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
	panelW := min(bodyW, min(max(64, bodyW-8), 112))
	panelInnerW := max(1, panelW-4)
	content := renderCodexCleanupContent(dialog, panelInnerW, bodyH, spinnerFrame, now)
	panel := renderDialogPanel(panelW-2, panelInnerW, content)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexCleanupOverlay(body string, bodyW, bodyH int) string {
	return renderCodexCleanupOverlay(body, bodyW, bodyH, m.codexCleanupPrivacyView(), m.spinnerFrame, m.currentTime())
}

func renderCodexCleanupContent(dialog *codexCleanupDialogState, width, bodyH, spinnerFrame int, now time.Time) string {
	if dialog.Finished {
		return renderCodexCleanupResults(dialog, width, bodyH)
	}
	if dialog.Deleting {
		return renderCodexCleanupProgress(dialog, width, spinnerFrame)
	}
	return buildCodexCleanupView(dialog, width, bodyH, spinnerFrame, now).text()
}

func renderCodexCleanupConfirmation(dialog *codexCleanupDialogState, width int) string {
	return buildCodexCleanupReview(dialog, width, 40).text()
}
func renderCodexCleanupProgress(dialog *codexCleanupDialogState, width, spinnerFrame int) string {
	current := service.CodexCleanupWorktreeGroup{}
	if dialog.QueueIndex < len(dialog.Queue) {
		current = dialog.Queue[dialog.QueueIndex]
	}
	reclaimed, _ := codexCleanupVerifiedTotal(dialog.Results)
	title := "Deleting Codex session storage"
	progress := spinnerFrames[spinnerFrame%len(spinnerFrames)] + fmt.Sprintf(" Project group %d of %d", dialog.QueueIndex+1, len(dialog.Queue))
	if dialog.CancelRequested {
		title = "Aborting Codex session cleanup"
		progress = spinnerFrames[spinnerFrame%len(spinnerFrames)] + " Stopping the active app-server request"
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		"",
		commandPaletteHintStyle.Render(progress),
		detailField("Current", firstNonEmptyString(current.WorktreePath, "verifying final result")),
		detailField("Verified reclaimed", formatCodexCleanupBytes(reclaimed)),
		"",
	}
	if dialog.CancelRequested {
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, "The active delete request is being canceled. LCR will still verify what was already removed, and no queued project group will start afterward.")...)
		lines = append(lines, "", renderDialogAction("b", "hide to background", navigateActionKeyStyle, navigateActionTextStyle))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, "Deletion is permanent. It is running off the UI path while LCR waits for app-server and verifies that thread rows and rollout files are gone.")...)
	lines = append(lines,
		"",
		renderDialogAction("b", "hide to background", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Esc", "abort remaining", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}

func renderCodexCleanupResults(dialog *codexCleanupDialogState, width, bodyH int) string {
	reclaimed, allVerified := codexCleanupVerifiedTotal(dialog.Results)
	title := "Codex cleanup report"
	lines := []string{commandPaletteTitleStyle.Render(title), ""}
	if dialog.Aborted {
		lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("Cleanup aborted. Verified reclaimed before stop: %s", formatCodexCleanupBytes(reclaimed))))
		remaining := max(0, len(dialog.Queue)-dialog.QueueIndex)
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("%d queued project group%s did not start. Run /codex-gc again to audit anything that remains.", remaining, pluralSuffix(remaining))))
	} else if len(dialog.Results) == 0 {
		lines = append(lines, detailWarningStyle.Render("No sessions were deleted."))
	} else if allVerified && dialog.ErrorMessage == "" {
		lines = append(lines, detailValueStyle.Render(fmt.Sprintf("Verified reclaimed space: %s", formatCodexCleanupBytes(reclaimed))))
	} else {
		lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("Verified reclaimed space so far: %s", formatCodexCleanupBytes(reclaimed))))
	}
	lines = append(lines, "")
	for _, item := range dialog.Results {
		detail := fmt.Sprintf("%d root%s + %d descendant%s · %s verified",
			item.Result.DeletedRootThreads, pluralSuffix(item.Result.DeletedRootThreads),
			item.Result.DeletedDescendants, pluralSuffix(item.Result.DeletedDescendants),
			formatCodexCleanupBytes(item.Result.VerifiedReclaimedBytes))
		statusWidth := max(1, width-2)
		status := codexCleanupResultStatus(filepath.Base(item.Group.WorktreePath), detail, statusWidth)
		if item.Result.Verified {
			lines = append(lines, detailValueStyle.Render("✓ "+status))
		} else if item.Canceled {
			status = codexCleanupResultStatus(filepath.Base(item.Group.WorktreePath), detail+" · stopped before full group completion", statusWidth)
			lines = append(lines, detailWarningStyle.Render("• "+status))
		} else {
			lines = append(lines, detailWarningStyle.Render("! "+status))
		}
		if item.Err != nil && (!item.Canceled || !item.Result.Verified) {
			lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, "  "+item.Err.Error())...)
		}
	}
	if dialog.ErrorMessage != "" && len(dialog.Results) == 0 {
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.ErrorMessage)...)
	}
	lines = append(lines, "", renderDialogAction("Enter/Esc", "close report", cancelActionKeyStyle, cancelActionTextStyle))
	return clampDialogContent(strings.Join(lines, "\n"), max(10, bodyH-4), 3, detailMutedStyle.Render("… earlier results clipped …"))
}

func codexCleanupResultStatus(name, detail string, width int) string {
	if width <= 0 {
		return ""
	}
	suffix := " · " + detail
	suffixWidth := lipgloss.Width(suffix)
	if suffixWidth >= width {
		return cleanupCellText(detail, width)
	}
	return cleanupCellText(name, width-suffixWidth) + suffix
}

func codexCleanupVerifiedTotal(results []codexCleanupDeleteResult) (int64, bool) {
	allVerified := len(results) > 0
	var total int64
	for _, item := range results {
		total += item.Result.VerifiedReclaimedBytes
		if !item.Result.Verified || (item.Err != nil && !item.Canceled) {
			allVerified = false
		}
	}
	return total, allVerified
}

func codexCleanupVerifiedGroupCount(results []codexCleanupDeleteResult) int {
	count := 0
	for _, item := range results {
		if item.Result.Verified {
			count++
		}
	}
	return count
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
	visible := max(1, min(total, max(2, bodyH-28)))
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
