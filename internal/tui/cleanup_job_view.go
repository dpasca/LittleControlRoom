package tui

import (
	"fmt"
	"time"
)

// Both cleanup tools expose the same running-job controls and timing language.
func cleanupJobControls(stopping bool) string {
	hide := renderDialogAction("B", "hide to background", navigateActionKeyStyle, navigateActionTextStyle)
	if stopping {
		return hide
	}
	return hide + "   " + renderDialogAction("Esc", "abort remaining", cancelActionKeyStyle, cancelActionTextStyle)
}
func cleanupJobTiming(now, started, updated time.Time) string {
	elapsed := time.Duration(max(0, int(now.Sub(started)/time.Second))) * time.Second
	waiting := time.Duration(max(0, int(now.Sub(updated)/time.Second))) * time.Second
	return fmt.Sprintf("%s elapsed · %s since last update", elapsed, waiting)
}
