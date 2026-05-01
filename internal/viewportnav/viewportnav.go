package viewportnav

import "github.com/charmbracelet/bubbles/viewport"

const (
	pageStepNumerator   = 4
	pageStepDenominator = 5
)

func PageStep(height int) int {
	if height <= 0 {
		return 0
	}
	step := height * pageStepNumerator / pageStepDenominator
	if step < 1 {
		return 1
	}
	return step
}

func PageUp(m *viewport.Model) []string {
	if m == nil {
		return nil
	}
	return m.ScrollUp(PageStep(m.Height))
}

func PageDown(m *viewport.Model) []string {
	if m == nil {
		return nil
	}
	return m.ScrollDown(PageStep(m.Height))
}
