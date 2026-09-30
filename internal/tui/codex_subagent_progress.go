package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
)

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
	rows := []string{detailLabelStyle.Render(fitLine(snapshot.SubagentActivitySummary(now), width))}
	limit := min(3, len(snapshot.Subagents))
	for _, child := range snapshot.Subagents[:limit] {
		state := child.State(now)
		age := ""
		if !child.UpdatedAt.IsZero() {
			age = " · " + formatRunningDuration(now.Sub(child.UpdatedAt)) + " ago"
		}
		label := truncateText(child.Description, max(8, width/2))
		style := detailValueStyle
		if state != "active" && state != "completed" {
			style = detailWarningStyle
		}
		rows = append(rows, style.Render(fitLine(label+" · "+state+age, width)))
		if action := strings.TrimSpace(child.LatestAction); action != "" {
			rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine("  Last action: "+action, width)))
		}
	}
	if remaining := len(snapshot.Subagents) - limit; remaining > 0 {
		rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine(fmt.Sprintf("+%d more subagents", remaining), width)))
	}
	return rows
}
