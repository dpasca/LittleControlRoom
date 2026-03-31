package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type attentionDialogFocus int

const (
	attentionDialogFocusPrimary attentionDialogFocus = iota
	attentionDialogFocusDismiss
)

type attentionDialogState struct {
	Title           string
	ProjectName     string
	ProjectPath     string
	Message         string
	Hint            string
	PrimaryLabel    string
	PrimaryProvider codexapp.Provider
	Selected        attentionDialogFocus
}

func (m *Model) showAttentionDialog(dialog attentionDialogState) {
	if strings.TrimSpace(dialog.ProjectName) == "" {
		dialog.ProjectName = projectNameForPicker(model.ProjectSummary{Name: dialog.ProjectName}, dialog.ProjectPath)
	}
	dialog.PrimaryProvider = dialog.PrimaryProvider.Normalized()
	dialog.Selected = attentionDialogFocusDismiss
	if dialog.PrimaryLabel == "" || dialog.PrimaryProvider == "" {
		dialog.Selected = attentionDialogFocusDismiss
	}
	m.attentionDialog = &dialog
	switch {
	case strings.TrimSpace(dialog.Message) != "":
		m.status = dialog.Message
	case strings.TrimSpace(dialog.Title) != "":
		m.status = dialog.Title
	}
}

func (m *Model) dismissAttentionDialog() {
	m.attentionDialog = nil
}

func (m Model) attentionDialogSessionActionLabel(project model.ProjectSummary, provider codexapp.Provider) string {
	provider = provider.Normalized()
	if provider == "" {
		return ""
	}
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok && embeddedProvider(snapshot) == provider {
		return "Open " + provider.Label()
	}
	if strings.TrimSpace(m.selectedProjectSessionID(project, provider)) != "" {
		return "Resume " + provider.Label()
	}
	return ""
}

func (m Model) updateAttentionDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.attentionDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.dismissAttentionDialog()
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if dialog.PrimaryLabel == "" || dialog.PrimaryProvider == "" {
			return m, nil
		}
		if dialog.Selected == attentionDialogFocusPrimary {
			dialog.Selected = attentionDialogFocusDismiss
		} else {
			dialog.Selected = attentionDialogFocusPrimary
		}
		return m, nil
	case "enter":
		if dialog.Selected == attentionDialogFocusPrimary && dialog.PrimaryLabel != "" && dialog.PrimaryProvider != "" {
			provider := dialog.PrimaryProvider
			m.dismissAttentionDialog()
			return m.launchEmbeddedForSelection(provider, false, "")
		}
		m.dismissAttentionDialog()
		return m, nil
	case "ctrl+c":
		m.dismissAttentionDialog()
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) renderAttentionDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderAttentionDialogPanel(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderAttentionDialogPanel(bodyW int) string {
	panelW := min(bodyW, min(max(56, bodyW-18), 86))
	panelInnerW := max(28, panelW-4)
	return lipgloss.NewStyle().
		Width(panelW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("178")).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(m.renderAttentionDialogContent(panelInnerW), panelInnerW))
}

func (m Model) renderAttentionDialogContent(width int) string {
	dialog := m.attentionDialog
	if dialog == nil {
		return ""
	}

	lines := []string{
		renderDialogHeader(dialog.Title, dialog.ProjectName, "", width),
	}
	if strings.TrimSpace(dialog.ProjectPath) != "" {
		lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(displayPathWithHomeTilde(dialog.ProjectPath), max(20, width-6)))))
	}
	lines = append(lines, "", detailWarningStyle.Render("Action needed"))
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, dialog.Message)...)
	if strings.TrimSpace(dialog.Hint) != "" {
		lines = append(lines, "")
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, dialog.Hint)...)
	}

	buttons := renderNoteDialogButton("Keep", true)
	if dialog.PrimaryLabel != "" && dialog.PrimaryProvider != "" {
		buttons = lipgloss.JoinHorizontal(
			lipgloss.Left,
			renderNoteDialogButton(dialog.PrimaryLabel, dialog.Selected == attentionDialogFocusPrimary),
			" ",
			renderNoteDialogButton("Keep", dialog.Selected == attentionDialogFocusDismiss),
		)
	}
	lines = append(lines, "", buttons)

	actionLabel := "dismiss"
	if dialog.PrimaryLabel != "" && dialog.PrimaryProvider != "" {
		actionLabel = "use highlighted"
		lines = append(lines, renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle))
	}
	lines = append(lines,
		renderDialogAction("Enter", actionLabel, commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}

func (m *Model) showSessionBlockedAttentionDialog(project model.ProjectSummary, title, message, retryText string, provider codexapp.Provider) {
	actionLabel := m.attentionDialogSessionActionLabel(project, provider)
	hint := fmt.Sprintf("Finish or close the embedded session, then %s.", retryText)
	if actionLabel != "" {
		hint = fmt.Sprintf("%s to finish or close that session, then %s.", actionLabel, retryText)
	}
	m.showAttentionDialog(attentionDialogState{
		Title:           title,
		ProjectName:     projectNameForPicker(project, project.Path),
		ProjectPath:     project.Path,
		Message:         message,
		Hint:            hint,
		PrimaryLabel:    actionLabel,
		PrimaryProvider: provider,
	})
}
