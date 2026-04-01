package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

var (
	dialogButtonStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	dialogButtonSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true).Padding(0, 1)
	todoListIndicatorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

func renderDialogButton(label string, selected bool) string {
	if selected {
		return dialogButtonSelectedStyle.Render(" " + label + " ")
	}
	return dialogButtonStyle.Render("[" + label + "]")
}

func projectTitle(projectPath, projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName != "" {
		return projectName
	}
	return filepath.Base(filepath.Clean(projectPath))
}

func styleDialogTextarea(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = focused.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	focused.CursorLine = focused.CursorLine.Background(codexComposerCursorLineColor)
	focused.EndOfBuffer = focused.EndOfBuffer.Foreground(lipgloss.Color("238"))
	focused.Placeholder = focused.Placeholder.Foreground(lipgloss.Color("240"))
	focused.Prompt = focused.Prompt.Foreground(lipgloss.Color("81")).Bold(true)
	focused.Text = focused.Text.Foreground(lipgloss.Color("252"))

	blurred := input.BlurredStyle
	blurred.Base = blurred.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	blurred.CursorLine = blurred.CursorLine.Background(codexComposerShellColor)
	blurred.EndOfBuffer = blurred.EndOfBuffer.Foreground(lipgloss.Color("238"))
	blurred.Placeholder = blurred.Placeholder.Foreground(lipgloss.Color("240"))
	blurred.Prompt = blurred.Prompt.Foreground(lipgloss.Color("244")).Bold(true)
	blurred.Text = blurred.Text.Foreground(lipgloss.Color("252"))

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}
