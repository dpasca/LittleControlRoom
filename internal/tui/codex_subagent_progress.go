package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
)

const codexSubagentProgressRunningRows = 4

// This panel uses only the cached session view model. Keep it visible even
// when the sidebar is hidden or the user has scrolled away from the live tail.
func (m Model) renderCodexSubagentProgress(snapshot codexapp.Snapshot, width int) []string {
	if !snapshot.Busy || snapshot.Closed {
		return nil
	}
	if snapshot.SubagentProgressError != "" {
		return []string{detailWarningStyle.Render(fitLine(snapshot.SubagentProgressError, width))}
	}
	if len(snapshot.Subagents) == 0 {
		return nil
	}
	now := m.currentTime()
	running, completed := splitSubagentsByCompletion(snapshot.Subagents, now)
	if len(running) == 0 {
		// Finished children are a receipt, not ongoing work: one muted line.
		return []string{embeddedSidebarMutedStyle.Render(fitLine("Subagents: "+completedSubagentList(completed, width), width))}
	}
	rows := []string{detailLabelStyle.Render(fitLine(snapshot.SubagentActivitySummary(now), width))}
	limit := min(codexSubagentProgressRunningRows, len(running))
	for _, child := range running[:limit] {
		rows = append(rows, renderCodexSubagentRow(child, now, width))
	}
	if remaining := len(running) - limit; remaining > 0 {
		rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine(fmt.Sprintf("  +%d more running", remaining), width)))
	}
	if len(completed) > 0 {
		rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine("  ✓ "+completedSubagentList(completed, width), width)))
	}
	return rows
}

func renderCodexSubagentRow(child claudeartifact.SubagentProgress, now time.Time, width int) string {
	label, detail, active := subagentRowText(child, now)
	marker, style := "●", detailValueStyle
	if !active {
		marker, style = "○", detailWarningStyle
	}
	head := "  " + marker + " " + truncateText(label, max(8, width/2))
	if detail == "" {
		return style.Render(fitLine(head, width))
	}
	tail := " · " + detail
	return fitStyledWidth(style.Render(head)+embeddedSidebarMutedStyle.Render(truncateText(tail, max(1, width-len([]rune(head))))), width)
}

// subagentRowText describes one unfinished child. Silent children name their
// state instead of a last action that may no longer be current.
func subagentRowText(child claudeartifact.SubagentProgress, now time.Time) (label, detail string, active bool) {
	state := child.State(now)
	active = state == "active"
	parts := []string{}
	if !active {
		parts = append(parts, state)
	} else if action := strings.TrimSpace(child.LatestAction); action != "" {
		parts = append(parts, action)
	}
	if !child.UpdatedAt.IsZero() {
		parts = append(parts, formatSubagentAge(now.Sub(child.UpdatedAt))+" ago")
	}
	label = child.Description
	if agentType := strings.TrimSpace(child.AgentType); agentType != "" {
		label += " (" + agentType + ")"
	}
	return label, strings.Join(parts, " · "), active
}

// addProjectSubagentDetail mirrors the session pane on the dashboard so
// delegated work is visible without opening the session.
func addProjectSubagentDetail(surface *projectDetailSurface, snapshot codexapp.Snapshot, now time.Time) {
	if !snapshot.Busy || snapshot.Closed {
		return
	}
	if snapshot.SubagentProgressError != "" {
		surface.WrappedField("Subagents", snapshot.SubagentProgressError, projectDetailToneWarning)
		return
	}
	if len(snapshot.Subagents) == 0 {
		return
	}
	running, completed := splitSubagentsByCompletion(snapshot.Subagents, now)
	parts := []string{}
	if summary := snapshot.RunningSubagentSummary(now); summary != "" {
		parts = append(parts, summary)
	}
	if len(completed) > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", len(completed)))
	}
	tone := projectDetailToneValue
	if len(running) == 0 {
		tone = projectDetailToneMuted
	}
	surface.WrappedField("Subagents", strings.Join(parts, " · "), tone)
	limit := min(codexSubagentProgressRunningRows, len(running))
	for _, child := range running[:limit] {
		label, detail, active := subagentRowText(child, now)
		if detail != "" {
			label += " · " + detail
		}
		childTone := projectDetailToneValue
		if !active {
			childTone = projectDetailToneWarning
		}
		surface.Bullet(label, childTone)
	}
	if remaining := len(running) - limit; remaining > 0 {
		surface.Bullet(fmt.Sprintf("+%d more running", remaining), projectDetailToneMuted)
	}
}

func splitSubagentsByCompletion(children []claudeartifact.SubagentProgress, now time.Time) (running, completed []claudeartifact.SubagentProgress) {
	for _, child := range children {
		if child.State(now) == "completed" {
			completed = append(completed, child)
		} else {
			running = append(running, child)
		}
	}
	return running, completed
}

// completedSubagentList names as many finished children as fit on one line.
func completedSubagentList(completed []claudeartifact.SubagentProgress, width int) string {
	text := fmt.Sprintf("%d completed", len(completed))
	budget := width - len([]rune(text)) - 20
	names := []string{}
	for i, child := range completed {
		name := truncateText(child.Description, 40)
		if budget-len([]rune(name))-2 < 0 {
			if i > 0 {
				names = append(names, fmt.Sprintf("+%d", len(completed)-i))
			}
			break
		}
		budget -= len([]rune(name)) + 2
		names = append(names, name)
	}
	if len(names) == 0 {
		return text
	}
	return text + ": " + strings.Join(names, ", ")
}

func formatSubagentAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
