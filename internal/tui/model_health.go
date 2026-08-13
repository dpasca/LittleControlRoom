package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/config"
)

// modelHealthFooterLabel summarizes provider/model divergences for the footer.
//
// A misconfigured model used to be invisible until the feature that needed it
// returned a raw provider error, so the footer carries a standing indicator
// instead: the condition is persistent, not an event, and it should stay
// visible until it is actually fixed.
func modelHealthFooterLabel(mismatches []config.ModelMismatch) string {
	if len(mismatches) == 0 {
		return ""
	}
	if len(mismatches) == 1 {
		return "MODEL MISMATCH " + mismatches[0].Field
	}
	return fmt.Sprintf("MODEL MISMATCH %d fields", len(mismatches))
}

// modelHealthDetail renders the full explanation for the settings surface.
func modelHealthDetail(mismatches []config.ModelMismatch) string {
	if len(mismatches) == 0 {
		return ""
	}
	lines := make([]string, 0, len(mismatches))
	for _, mismatch := range mismatches {
		lines = append(lines, mismatch.Detail())
	}
	return strings.Join(lines, "\n")
}

func (m Model) currentModelMismatches() []config.ModelMismatch {
	baseline := m.currentSettingsBaseline()
	return baseline.ModelMismatches()
}

func (m Model) renderFooterModelHealthSegment() string {
	return renderFooterAlert(modelHealthFooterLabel(m.currentModelMismatches()))
}
