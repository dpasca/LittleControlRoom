package tui

import "lcroom/internal/codexapp"

func (m Model) renderCodexMessageDelivery(snapshot codexapp.Snapshot, width int) []string {
	var rows []string
	now := m.currentTime()
	if status, quiet := snapshot.ParentActivitySummary(now); status != "" && snapshot.Busy {
		style := embeddedSidebarMutedStyle
		if quiet {
			style = detailWarningStyle
		}
		rows = append(rows, style.Render(fitLine(status, width)))
	}
	if summary, warning := snapshot.MessageDeliverySummary(now); summary != "" {
		style := detailValueStyle
		if warning {
			style = detailWarningStyle
		}
		rows = append(rows, style.Render(fitLine(summary, width)))
		shown := 0
		for _, delivery := range snapshot.MessageDeliveries {
			if !delivery.Waiting() {
				continue
			}
			rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine(delivery.State+": "+delivery.Preview, width)))
			shown++
			if shown == 2 {
				break
			}
		}
	}
	return rows
}
