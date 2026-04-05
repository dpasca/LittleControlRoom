package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type footerActionTone int

const (
	footerTonePrimary footerActionTone = iota
	footerToneNavigate
	footerToneExit
	footerToneHide
	footerToneLow
)

type footerAction struct {
	key   string
	label string
	tone  footerActionTone
}

var (
	footerMetaStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	footerStatusStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	footerAlertStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	footerUsageStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	footerPrimaryKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("42")).Bold(true)
	footerPrimaryLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true)
	footerNavigateKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("81")).Bold(true)
	footerNavigateLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("153"))
	footerExitKeyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true)
	footerExitLabelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Bold(true)
	footerHideKeyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true)
	footerHideLabelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
	footerLowKeyStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238")).Bold(true)
	footerLowLabelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func footerPrimaryAction(key, label string) footerAction {
	return footerAction{key: key, label: label, tone: footerTonePrimary}
}

func footerNavAction(key, label string) footerAction {
	return footerAction{key: key, label: label, tone: footerToneNavigate}
}

func footerExitAction(key, label string) footerAction {
	return footerAction{key: key, label: label, tone: footerToneExit}
}

func footerHideAction(key, label string) footerAction {
	return footerAction{key: key, label: label, tone: footerToneHide}
}

func footerLowAction(key, label string) footerAction {
	return footerAction{key: key, label: label, tone: footerToneLow}
}

func renderFooterMeta(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return footerMetaStyle.Render(text)
}

func renderFooterStatus(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return footerStatusStyle.Render(text)
}

func renderFooterAlert(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return footerAlertStyle.Render(text)
}

func renderFooterUsage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return footerUsageStyle.Render(text)
}

func renderFooterActionList(actions ...footerAction) string {
	if len(actions) == 0 {
		return ""
	}
	segments := make([]string, 0, len(actions))
	for _, action := range actions {
		if rendered := action.render(); rendered != "" {
			segments = append(segments, rendered)
		}
	}
	return strings.Join(segments, "  ")
}

func joinFooterSegments(segments ...string) string {
	filtered := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(ansi.Strip(segment)) == "" {
			continue
		}
		filtered = append(filtered, segment)
	}
	return strings.Join(filtered, "  ")
}

func renderFooterLine(width int, segments ...string) string {
	line := joinFooterSegments(segments...)
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "...")
}

func (a footerAction) render() string {
	key := strings.TrimSpace(a.key)
	label := strings.TrimSpace(a.label)
	if key == "" && label == "" {
		return ""
	}

	keyStyle := footerLowKeyStyle
	labelStyle := footerLowLabelStyle
	switch a.tone {
	case footerTonePrimary:
		keyStyle = footerPrimaryKeyStyle
		labelStyle = footerPrimaryLabelStyle
	case footerToneNavigate:
		keyStyle = footerNavigateKeyStyle
		labelStyle = footerNavigateLabelStyle
	case footerToneExit:
		keyStyle = footerExitKeyStyle
		labelStyle = footerExitLabelStyle
	case footerToneHide:
		keyStyle = footerHideKeyStyle
		labelStyle = footerHideLabelStyle
	}

	if key == "" {
		return labelStyle.Render(label)
	}
	keyRendered := keyStyle.Render(key)
	if label == "" {
		return keyRendered
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, keyRendered, " ", labelStyle.Render(label))
}
