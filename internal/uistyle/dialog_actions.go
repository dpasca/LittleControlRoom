package uistyle

import "github.com/charmbracelet/lipgloss"

type DialogActionTone int

const (
	DialogActionPrimary DialogActionTone = iota
	DialogActionNavigate
	DialogActionSecondary
	DialogActionCancel
	DialogActionDisabled
)

var (
	DialogActionPrimaryKeyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("42")).Bold(true).Padding(0, 1)
	DialogActionPrimaryTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true)
	DialogActionNavigateKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("81")).Bold(true).Padding(0, 1)
	DialogActionNavigateTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true)
	DialogActionSecondaryKeyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	DialogActionSecondaryTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true)
	DialogActionCancelKeyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
	DialogActionCancelTextStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Bold(true)
	DialogActionDisabledKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238")).Bold(true).Padding(0, 1)
	DialogActionDisabledTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func DialogActionStyles(tone DialogActionTone) (lipgloss.Style, lipgloss.Style) {
	switch tone {
	case DialogActionPrimary:
		return DialogActionPrimaryKeyStyle, DialogActionPrimaryTextStyle
	case DialogActionNavigate:
		return DialogActionNavigateKeyStyle, DialogActionNavigateTextStyle
	case DialogActionSecondary:
		return DialogActionSecondaryKeyStyle, DialogActionSecondaryTextStyle
	case DialogActionCancel:
		return DialogActionCancelKeyStyle, DialogActionCancelTextStyle
	case DialogActionDisabled:
		return DialogActionDisabledKeyStyle, DialogActionDisabledTextStyle
	default:
		return DialogActionNavigateKeyStyle, DialogActionNavigateTextStyle
	}
}

func RenderDialogAction(key, label string, keyStyle, labelStyle, fillStyle lipgloss.Style) string {
	return lipgloss.JoinHorizontal(lipgloss.Left, keyStyle.Render(key), fillStyle.Render(" "), labelStyle.Render(label))
}

func RenderDialogActionTone(key, label string, tone DialogActionTone, fillStyle lipgloss.Style) string {
	keyStyle, labelStyle := DialogActionStyles(tone)
	return RenderDialogAction(key, label, keyStyle, labelStyle, fillStyle)
}
