package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/viewportnav"
)

var (
	dialogButtonStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	dialogButtonSelectedStyle = dialogSelectedRowStyle.Padding(0, 1)
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

// allowLongDialogTextarea drops the bubbles textarea default 99-row cap.
// The cap makes Enter a silent no-op once a value grows past 99 lines, which
// is easy to reach by pasting a long block into a dialog editor. Character
// limits still bound how much text a dialog accepts.
func allowLongDialogTextarea(input *textarea.Model) {
	if input == nil {
		return
	}
	input.MaxHeight = 0
}

// pageDialogTextarea moves the textarea cursor by roughly one visible page so
// long values can be navigated with PageUp/PageDown. Direction is negative for
// up and positive for down. Cursor moves are fed through the textarea so it
// keeps wrapped lines and its own viewport in sync.
func pageDialogTextarea(input *textarea.Model, direction int) tea.Cmd {
	if input == nil || direction == 0 {
		return nil
	}
	step := viewportnav.PageStep(input.Height())
	key := tea.KeyMsg{Type: tea.KeyUp}
	if direction > 0 {
		key = tea.KeyMsg{Type: tea.KeyDown}
	}
	var cmd tea.Cmd
	for i := 0; i < step; i++ {
		*input, cmd = input.Update(key)
	}
	return cmd
}
