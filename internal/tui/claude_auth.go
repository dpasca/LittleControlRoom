package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/claudecli"
	"lcroom/internal/codexapp"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type claudeAuthDialogState struct {
	Input  textinput.Model
	Info   claudecli.SubscriptionTokenInfo
	Busy   bool
	Status string
}

type claudeAuthResultMsg struct {
	info   claudecli.SubscriptionTokenInfo
	err    error
	status string
}

type claudeAuthSetupFinishedMsg struct{ err error }

func (m Model) openClaudeAuthDialog() (tea.Model, tea.Cmd) {
	input := textinput.New()
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.CharLimit = 16 * 1024
	input.Placeholder = "Paste a token from claude setup-token"
	focusCmd := input.Focus()
	m.claudeAuthDialog = &claudeAuthDialogState{Input: input, Info: claudecli.SubscriptionTokens().Info(), Busy: true}
	m.commandMode = false
	m.status = "Claude subscription connection"
	return m, tea.Batch(focusCmd, refreshClaudeAuthCmd(""))
}

func refreshClaudeAuthCmd(status string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		manager := claudecli.SubscriptionTokens()
		err := manager.Refresh(ctx)
		return claudeAuthResultMsg{info: manager.Info(), err: err, status: status}
	}
}

func (m Model) applyClaudeAuthResult(msg claudeAuthResultMsg) (tea.Model, tea.Cmd) {
	if m.claudeAuthDialog == nil {
		return m, nil
	}
	dialog := m.claudeAuthDialog
	dialog.Busy = false
	dialog.Info = msg.info
	dialog.Status = msg.status
	if msg.err != nil {
		dialog.Status = msg.err.Error()
	}
	if msg.status != "" && msg.err == nil {
		m.status = msg.status
		return m, m.refreshSetupSnapshotCmd(false)
	}
	return m, nil
}

func (m Model) updateClaudeAuthDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.claudeAuthDialog
	if dialog.Busy {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		dialog.Input.Reset()
		m.claudeAuthDialog = nil
		return m, nil
	case "enter":
		token := strings.TrimSpace(dialog.Input.Value())
		if err := claudecli.ValidateSubscriptionToken(token); err != nil {
			dialog.Status = err.Error()
			return m, nil
		}
		dialog.Busy = true
		dialog.Input.Reset()
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			manager := claudecli.SubscriptionTokens()
			err := manager.Save(ctx, token)
			return claudeAuthResultMsg{info: manager.Info(), err: err, status: "Claude subscription token saved. Reconnect existing Claude sessions before retrying."}
		}
	case "ctrl+d":
		dialog.Busy = true
		dialog.Input.Reset()
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			manager := claudecli.SubscriptionTokens()
			err := manager.Remove(ctx)
			return claudeAuthResultMsg{info: manager.Info(), err: err, status: "Saved token removed. Reconnect Claude to use its normal login or environment credentials."}
		}
	case "ctrl+g":
		if m.svc == nil {
			dialog.Status = "Run `lcroom claude-auth setup` in a terminal, then reopen this dialog."
			return m, nil
		}
		executable, err := os.Executable()
		if err != nil {
			dialog.Status = "Could not locate LCR. Run `lcroom claude-auth setup` in a terminal."
			return m, nil
		}
		cfg := m.svc.Config()
		cmd := exec.Command(executable, "claude-auth", "setup", "--config", cfg.ConfigPath, "--db", cfg.DBPath)
		dialog.Busy = true
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return claudeAuthSetupFinishedMsg{err: err} })
	case "ctrl+r":
		snapshot, ok := m.currentCachedCodexSnapshot()
		if !ok || snapshot.Provider != codexapp.ProviderClaudeCode || !m.codexVisible() {
			dialog.Status = "Open a Claude session and use /reconnect to load the saved token."
			return m, nil
		}
		if snapshot.Busy || snapshot.BusyExternal || snapshot.Compacting || snapshot.ActiveTurnID != "" || snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil {
			dialog.Status = "Claude is busy. Wait for it to finish or stop it before reconnecting."
			return m, nil
		}
		dialog.Input.Reset()
		m.claudeAuthDialog = nil
		m.status = "Reconnecting Claude with the current credentials..."
		return m, m.reconnectVisibleCodexSessionCmd()
	}
	input, cmd := dialog.Input.Update(msg)
	dialog.Input = input
	return m, cmd
}

func (m Model) renderClaudeAuthOverlay(body string, width, height int) string {
	width, height = max(1, width), max(1, height)
	panelWidth := min(width, 86)
	innerWidth := max(1, panelWidth-4)
	dialog := m.claudeAuthDialog
	lines := []string{renderDialogHeader("Claude subscription connection", "", "", innerWidth)}
	appendText := func(value string) {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, innerWidth, value)...)
	}
	state := "Using Claude's normal login unless an environment credential is set."
	if dialog.Info.Saved {
		state = "An LCR subscription token is saved."
	}
	appendText(state)
	if override := claudecli.SubscriptionTokenEnvironmentOverride(); override != "" {
		appendText(override + " is set in the environment and takes precedence over the saved token. Restart LCR after changing it.")
	}
	appendText("Storage: " + dialog.Info.Storage)
	lines = append(lines, "")
	appendText("Generate a one-year token with claude setup-token, then save it here. Uses your Claude subscription. Local MCP tools work; Remote Control, claude.ai connectors and plan usage are unavailable with this token.")
	input := dialog.Input
	input.Width = max(1, innerWidth-2)
	status := ""
	if dialog.Busy {
		status = "Updating Claude connection..."
	} else if dialog.Status != "" {
		status = dialog.Status
	}
	actions := []string{
		renderDialogAction("Enter", "save token", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("ctrl+g", "generate in terminal", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("ctrl+r", "reconnect session", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("ctrl+d", "remove token", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	footer := ansi.Wrap(strings.Join(actions, "   "), innerWidth, " ")
	statusText := strings.Join(renderWrappedDialogTextLines(commandPaletteHintStyle, innerWidth, status), "\n")
	statusText = fitPaneContent(statusText, innerWidth, min(4, lipgloss.Height(statusText)))
	bottom := strings.Join([]string{"", input.View(), statusText, "", footer}, "\n")
	intro := strings.Join(lines, "\n")
	introHeight := min(lipgloss.Height(intro), max(1, height-2-lipgloss.Height(bottom)-1))
	content := fitPaneContent(intro, innerWidth, introHeight) + "\n" + bottom
	panel := lipgloss.NewStyle().Width(panelWidth-2).Border(lipgloss.RoundedBorder()).Padding(0, 1).
		BorderForeground(dialogPanelBorderColor).Background(dialogPanelBackground).Render(fillDialogBlock(content, innerWidth))
	return overlayBlock(body, panel, width, height, max(0, (width-lipgloss.Width(panel))/2), max(0, (height-lipgloss.Height(panel))/2))
}

func (m Model) applyClaudeAuthSetupFinished(msg claudeAuthSetupFinishedMsg) (tea.Model, tea.Cmd) {
	if m.claudeAuthDialog == nil {
		return m, nil
	}
	if msg.err != nil {
		m.claudeAuthDialog.Busy = false
		m.claudeAuthDialog.Status = fmt.Sprintf("Token setup did not complete (%v). The previous saved token was kept.", msg.err)
		return m, nil
	}
	return m, refreshClaudeAuthCmd("Claude subscription token saved. Reconnect existing Claude sessions before retrying.")
}
