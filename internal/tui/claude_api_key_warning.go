package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const anthropicAPIKeyEnvironmentVariable = "ANTHROPIC_API_KEY"

var errClaudeAPIKeyLaunchCanceled = errors.New("Claude Code launch canceled after API billing warning")

type claudeAPIKeyWarningFocus uint8

const (
	claudeAPIKeyWarningFocusContinue claudeAPIKeyWarningFocus = iota
	claudeAPIKeyWarningFocusCancel
)

type claudeAPIKeyWarningRequestedMsg struct {
	projectPath string
	launchCmd   tea.Cmd
	cancelCmd   tea.Cmd
}

type claudeAPIKeyWarningPendingLaunch struct {
	launchCmd tea.Cmd
	cancelCmd tea.Cmd
}

type claudeAPIKeyWarningDialogState struct {
	ProjectName string
	ProjectPath string
	Selected    claudeAPIKeyWarningFocus
	Pending     []claudeAPIKeyWarningPendingLaunch
}

func anthropicAPIKeyPresentInEnvironment() bool {
	return strings.TrimSpace(os.Getenv(anthropicAPIKeyEnvironmentVariable)) != ""
}

func (m Model) anthropicAPIKeyPresent() bool {
	if m.anthropicAPIKeyPresentFn == nil {
		return false
	}
	return m.anthropicAPIKeyPresentFn()
}

// deferClaudeLaunchForAPIKeyWarning keeps the provider process unopened until
// the warning request has been applied and explicitly acknowledged in the TUI.
func (m Model) deferClaudeLaunchForAPIKeyWarning(provider codexapp.Provider, projectPath string, launchCmd, cancelCmd tea.Cmd) tea.Cmd {
	if launchCmd == nil ||
		provider.Normalized() != codexapp.ProviderClaudeCode ||
		m.claudeAPIKeyWarningAcknowledged ||
		!m.anthropicAPIKeyPresent() {
		return launchCmd
	}
	return func() tea.Msg {
		return claudeAPIKeyWarningRequestedMsg{
			projectPath: strings.TrimSpace(projectPath),
			launchCmd:   launchCmd,
			cancelCmd:   cancelCmd,
		}
	}
}

// mapDeferredClaudeLaunchCommand preserves command decorators such as control
// receipts and TODO/session tracking when the underlying launch is postponed.
func mapDeferredClaudeLaunchCommand(cmd tea.Cmd, mapResult func(tea.Msg) tea.Msg) tea.Cmd {
	if cmd == nil || mapResult == nil {
		return cmd
	}
	return func() tea.Msg {
		msg := cmd()
		request, ok := msg.(claudeAPIKeyWarningRequestedMsg)
		if !ok {
			return mapResult(msg)
		}
		request.launchCmd = mapDeferredClaudeLaunchCommand(request.launchCmd, mapResult)
		request.cancelCmd = mapDeferredClaudeLaunchCommand(request.cancelCmd, mapResult)
		return request
	}
}

func (m Model) applyClaudeAPIKeyWarningRequested(msg claudeAPIKeyWarningRequestedMsg) (tea.Model, tea.Cmd) {
	if msg.launchCmd == nil {
		return m, nil
	}
	if m.claudeAPIKeyWarningAcknowledged || !m.anthropicAPIKeyPresent() {
		return m, msg.launchCmd
	}
	if m.claudeAPIKeyWarning == nil {
		projectPath := strings.TrimSpace(msg.projectPath)
		m.claudeAPIKeyWarning = &claudeAPIKeyWarningDialogState{
			ProjectName: projectTitle(projectPath, ""),
			ProjectPath: projectPath,
			Selected:    claudeAPIKeyWarningFocusCancel,
		}
	}
	m.claudeAPIKeyWarning.Pending = append(
		m.claudeAPIKeyWarning.Pending,
		claudeAPIKeyWarningPendingLaunch{
			launchCmd: msg.launchCmd,
			cancelCmd: msg.cancelCmd,
		},
	)
	m.status = "Claude Code launch needs API billing acknowledgement"
	m.markTopStatusAttentionPulse(m.status)
	return m, nil
}

func (m *Model) resolveClaudeAPIKeyWarning(continueLaunch bool) tea.Cmd {
	dialog := m.claudeAPIKeyWarning
	if dialog == nil {
		return nil
	}
	m.claudeAPIKeyWarning = nil

	cmds := make([]tea.Cmd, 0, len(dialog.Pending))
	if continueLaunch {
		m.claudeAPIKeyWarningAcknowledged = true
		m.status = "Claude Code API billing warning acknowledged for this LCR run"
		for _, pending := range dialog.Pending {
			cmds = append(cmds, pending.launchCmd)
		}
	} else {
		m.status = "Claude Code launch canceled"
		for _, pending := range dialog.Pending {
			cmds = append(cmds, pending.cancelCmd)
		}
	}
	return batchCmds(cmds...)
}

func (m Model) updateClaudeAPIKeyWarningMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.claudeAPIKeyWarning
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "left", "h", "right", "l", "tab", "shift+tab":
		if dialog.Selected == claudeAPIKeyWarningFocusContinue {
			dialog.Selected = claudeAPIKeyWarningFocusCancel
		} else {
			dialog.Selected = claudeAPIKeyWarningFocusContinue
		}
		return m, nil
	case "enter":
		return m, m.resolveClaudeAPIKeyWarning(dialog.Selected == claudeAPIKeyWarningFocusContinue)
	case "esc", "q":
		return m, m.resolveClaudeAPIKeyWarning(false)
	case "ctrl+c":
		cancelCmd := m.resolveClaudeAPIKeyWarning(false)
		updated, quitCmd := m.updateNormalMode(msg)
		return updated, batchCmds(cancelCmd, quitCmd)
	}
	return m, nil
}

func (m Model) renderClaudeAPIKeyWarningOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderClaudeAPIKeyWarningPanel(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderClaudeAPIKeyWarningPanel(bodyW int) string {
	panelW := min(bodyW, min(max(62, bodyW-18), 92))
	panelInnerW := max(28, panelW-4)
	return lipgloss.NewStyle().
		Width(panelW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("178")).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(m.renderClaudeAPIKeyWarningContent(panelInnerW), panelInnerW))
}

func (m Model) renderClaudeAPIKeyWarningContent(width int) string {
	dialog := m.claudeAPIKeyWarning
	if dialog == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("Claude Code API billing warning", dialog.ProjectName, "", width),
	}
	if dialog.ProjectPath != "" {
		lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(dialog.ProjectPath), max(20, width-6)))))
	}
	if len(dialog.Pending) > 1 {
		lines = append(lines, detailField("Queued", detailWarningStyle.Render(fmt.Sprintf("%d Claude Code launches", len(dialog.Pending)))))
	}
	lines = append(lines, "", detailWarningStyle.Render("Pay-as-you-go charges may apply"))
	lines = append(lines, renderWrappedDialogTextLines(
		detailWarningStyle,
		width,
		"ANTHROPIC_API_KEY is set in Little Control Room's environment. Claude Code gives that API key priority over a Claude Pro or Max subscription, so this embedded session may incur Anthropic API charges instead of using only your subscription limits.",
	)...)
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(
		commandPaletteHintStyle,
		width,
		"To keep subscription-only usage, cancel, unset ANTHROPIC_API_KEY in the environment that launches LCR, and restart. Continuing acknowledges this warning until LCR restarts.",
	)...)
	lines = append(lines, "", lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderDialogButton("Continue anyway", dialog.Selected == claudeAPIKeyWarningFocusContinue),
		" ",
		renderDialogButton("Cancel launch", dialog.Selected == claudeAPIKeyWarningFocusCancel),
	))
	lines = append(lines,
		renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "use highlighted", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "cancel launch", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}
