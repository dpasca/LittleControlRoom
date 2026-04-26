package tui

import (
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type bossSetupPromptSelection int

const (
	bossSetupPromptOpenSetup bossSetupPromptSelection = iota
	bossSetupPromptCancel
)

type bossSetupPromptState struct {
	Selected bossSetupPromptSelection
	Reason   string
}

func (m Model) openBossModeOrSetupPrompt() (tea.Model, tea.Cmd) {
	if m.bossChatConfigured() {
		return m.openBossMode()
	}
	m.openBossSetupPrompt()
	return m, nil
}

func (m Model) bossChatConfigured() bool {
	settings := m.currentSettingsBaseline()
	switch settings.BossChatBackend {
	case config.AIBackendOpenAIAPI:
		return strings.TrimSpace(settings.OpenAIAPIKey) != ""
	case config.AIBackendMLX:
		return strings.TrimSpace(settings.MLXBaseURL) != "" || config.AIBackendMLX.DefaultOpenAICompatibleBaseURL() != ""
	case config.AIBackendOllama:
		return strings.TrimSpace(settings.OllamaBaseURL) != "" || config.AIBackendOllama.DefaultOpenAICompatibleBaseURL() != ""
	default:
		return false
	}
}

func (m *Model) openBossSetupPrompt() {
	m.bossSetupPrompt = &bossSetupPromptState{
		Selected: bossSetupPromptOpenSetup,
		Reason:   m.bossSetupPromptReason(),
	}
	m.status = "Boss chat needs setup before it can open."
}

func (m *Model) closeBossSetupPrompt(status string) {
	m.bossSetupPrompt = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) bossSetupPromptReason() string {
	settings := m.currentSettingsBaseline()
	switch {
	case settings.BossChatBackend == config.AIBackendDisabled:
		return "Boss chat is currently turned off."
	case settings.BossChatBackend == config.AIBackendMLX:
		return "Boss chat is set to MLX, but the local endpoint still needs setup."
	case settings.BossChatBackend == config.AIBackendOllama:
		return "Boss chat is set to Ollama, but the local endpoint still needs setup."
	case settings.BossChatBackend == config.AIBackendOpenAIAPI && strings.TrimSpace(settings.OpenAIAPIKey) == "":
		return "Boss chat is set to OpenAI API, but needs a saved OpenAI API key before it can start."
	case settings.BossChatBackend == config.AIBackendUnset:
		return "Boss chat is not configured yet. Open /setup to choose a boss chat backend."
	case settings.BossChatBackend != config.AIBackendOpenAIAPI:
		return "Boss chat is not connected to a supported direct chat backend yet."
	default:
		return "Boss chat needs one quick setup step before it can start."
	}
}

func (m Model) updateBossSetupPromptMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.bossSetupPrompt == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeBossSetupPrompt("Boss mode canceled. Run /setup anytime to configure boss chat.")
		return m, nil
	case "tab", "shift+tab", "left", "right", "up", "down":
		if m.bossSetupPrompt.Selected == bossSetupPromptOpenSetup {
			m.bossSetupPrompt.Selected = bossSetupPromptCancel
		} else {
			m.bossSetupPrompt.Selected = bossSetupPromptOpenSetup
		}
		return m, nil
	case "c", "n":
		m.closeBossSetupPrompt("Boss mode canceled. Run /setup anytime to configure boss chat.")
		return m, nil
	case "s", "o":
		return m.openSetupFromBossSetupPrompt()
	case "enter":
		if m.bossSetupPrompt.Selected == bossSetupPromptOpenSetup {
			return m.openSetupFromBossSetupPrompt()
		}
		m.closeBossSetupPrompt("Boss mode canceled. Run /setup anytime to configure boss chat.")
		return m, nil
	}
	return m, nil
}

func (m Model) openSetupFromBossSetupPrompt() (tea.Model, tea.Cmd) {
	m.bossSetupPrompt = nil
	cmd := m.openSetupModeForBossChat()
	return m, cmd
}

func (m *Model) openSetupModeForBossChat() tea.Cmd {
	cmd := m.openSetupMode()
	m.setupFocusedRole = setupRoleBossChat
	settings := m.currentSettingsBaseline()
	if current := settings.BossChatBackend; current == config.AIBackendMLX || current == config.AIBackendOllama {
		m.setupBossSelected = m.setupBossSelectionForBackend(current)
	} else if projectBackend := settings.AIBackend; projectBackend == config.AIBackendMLX || projectBackend == config.AIBackendOllama {
		m.setupBossSelected = m.setupBossSelectionForBackend(projectBackend)
	} else {
		m.setupBossSelected = m.setupBossSelectionForBackend(config.AIBackendOpenAIAPI)
	}
	m.status = "Configure boss chat here, then run /boss again."
	return cmd
}

func (m Model) renderBossSetupPromptOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderBossSetupPromptPanel(bodyW)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(1, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderBossSetupPromptPanel(bodyW int) string {
	panelWidth := min(max(54, bodyW-10), 78)
	panelInnerWidth := max(20, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderBossSetupPromptContent(panelInnerWidth))
}

func (m Model) renderBossSetupPromptContent(width int) string {
	prompt := m.bossSetupPrompt
	if prompt == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("Boss Chat Setup", "", "", width),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, prompt.Reason)...)
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Project reports can stay on their current backend. This only configures the high-level /boss conversation.")...)
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderDialogButton("Open /setup", prompt.Selected == bossSetupPromptOpenSetup),
		renderDialogButton("Cancel", prompt.Selected == bossSetupPromptCancel),
	}, "  "))
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   "))
	return strings.Join(lines, "\n")
}
