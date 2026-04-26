package tui

import (
	"strings"

	"lcroom/internal/aibackend"
	"lcroom/internal/config"

	"github.com/charmbracelet/lipgloss"
)

type inferenceStatusCard struct {
	Title       string
	Value       string
	State       string
	StateStyle  lipgloss.Style
	Detail      string
	DetailStyle lipgloss.Style
	Selected    bool
}

func (m Model) renderInferenceStatusCards(width int) string {
	settings := m.currentSettingsBaseline()
	cards := []inferenceStatusCard{
		m.projectReportsStatusCard(settings),
		m.bossChatStatusCard(settings),
	}
	if width < 70 {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			renderInferenceStatusCard(cards[0], width),
			renderInferenceStatusCard(cards[1], width),
		)
	}
	gap := "  "
	cardWidth := max(26, (width-lipgloss.Width(gap))/2)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		renderInferenceStatusCard(cards[0], cardWidth),
		gap,
		renderInferenceStatusCard(cards[1], cardWidth),
	)
}

func (m Model) renderCompactInferenceSetupSummary(width int) string {
	settings := m.currentSettingsBaseline()
	projectCard := m.projectReportsStatusCard(settings)
	bossCard := m.bossChatStatusCard(settings)
	summary := "AI setup: Project reports use " + projectCard.Value + " (" + strings.ToLower(projectCard.State) + "); Boss chat uses " + bossCard.Value + " (" + strings.ToLower(bossCard.State) + ")."
	if relationship := bossChatRelationshipSummary(settings); relationship != "" {
		summary += " " + relationship
	}
	summary += " Run /setup to change roles."
	return commandPaletteHintStyle.Render(lipgloss.NewStyle().Width(width).Render(summary))
}

func bossChatRelationshipSummary(settings config.EditableSettings) string {
	switch settings.BossChatBackend {
	case config.AIBackendOpenAIAPI:
		if settings.AIBackend == config.AIBackendOpenAIAPI {
			return "Both use the saved OpenAI API key."
		}
		return "Boss chat uses the saved OpenAI API key; project reports stay separate."
	case config.AIBackendDisabled:
		return "Boss chat is off; project reports can still run."
	default:
		return ""
	}
}

func (m Model) projectReportsStatusCard(settings config.EditableSettings) inferenceStatusCard {
	backend := settings.AIBackend
	status, known := m.inferenceBackendStatus(backend, settings)
	state, stateStyle := inferenceStateForBackend(backend, status, known)
	value := backend.Label()
	detail := strings.TrimSpace(status.Detail)
	if status.LoginHint != "" && !status.Ready {
		detail = strings.TrimSpace(status.LoginHint)
	}
	if backend == config.AIBackendUnset {
		detail = "Choose a backend in /setup for summaries, classifications, TODOs, and commit help."
	}
	if backend == config.AIBackendDisabled {
		detail = "Project reports and commit help are off."
	}
	return inferenceStatusCard{
		Title:       "Project reports",
		Value:       value,
		State:       state,
		StateStyle:  stateStyle,
		Detail:      detail,
		DetailStyle: commandPaletteHintStyle,
	}
}

func (m Model) bossChatStatusCard(settings config.EditableSettings) inferenceStatusCard {
	backend := settings.BossChatBackend
	status, known := m.inferenceBackendStatus(backend, settings)
	state, stateStyle := inferenceStateForBackend(backend, status, known)
	value := backend.Label()
	detail := strings.TrimSpace(status.Detail)
	if backend == config.AIBackendUnset {
		value = "Auto"
		state = "setup"
		stateStyle = detailWarningStyle
		detail = "Save an OpenAI API key to enable boss chat, or set boss chat off."
	}
	if backend == config.AIBackendDisabled {
		detail = "High-level chat is off; project reports can still run."
	}
	if backend == config.AIBackendOpenAIAPI {
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			state = "setup"
			stateStyle = detailWarningStyle
			detail = "Needs a saved OpenAI API key."
		} else if settings.AIBackend == config.AIBackendOpenAIAPI {
			detail = "Uses the same saved OpenAI API key as project reports."
		} else {
			detail = "Uses the saved OpenAI API key; project reports stay separate."
		}
	}
	return inferenceStatusCard{
		Title:       "Boss chat",
		Value:       value,
		State:       state,
		StateStyle:  stateStyle,
		Detail:      detail,
		DetailStyle: commandPaletteHintStyle,
	}
}

func (m Model) inferenceBackendStatus(backend config.AIBackend, settings config.EditableSettings) (aibackend.Status, bool) {
	status := m.setupSnapshot.StatusFor(backend)
	known := inferenceStatusKnown(status)
	if strings.TrimSpace(status.Label) == "" {
		status.Backend = backend
		status.Label = backend.Label()
	}
	switch backend {
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			status.Ready = false
			status.Detail = "No saved OpenAI API key."
			status.LoginHint = "Open /settings and save an OpenAI API key."
			return status, true
		}
		status.Installed = true
		status.Authenticated = true
		status.Ready = true
		if strings.TrimSpace(status.Detail) == "" {
			status.Detail = "Saved OpenAI API key ready."
		}
	case config.AIBackendDisabled:
		status.Ready = true
		if strings.TrimSpace(status.Detail) == "" {
			status.Detail = "Disabled by choice."
		}
	case config.AIBackendUnset:
		if strings.TrimSpace(status.Detail) == "" {
			status.Detail = "Pick a backend to enable AI features."
		}
	default:
		if strings.TrimSpace(status.Detail) == "" {
			status.Detail = "Selected. Run /setup to refresh availability."
		}
	}
	return status, known
}

func inferenceStatusKnown(status aibackend.Status) bool {
	return status.Backend != "" ||
		strings.TrimSpace(status.Label) != "" ||
		strings.TrimSpace(status.Detail) != "" ||
		strings.TrimSpace(status.LoginHint) != "" ||
		strings.TrimSpace(status.Endpoint) != "" ||
		strings.TrimSpace(status.ActiveModel) != "" ||
		status.Installed ||
		status.Authenticated ||
		status.Ready ||
		len(status.Models) > 0
}

func inferenceStateForBackend(backend config.AIBackend, status aibackend.Status, known bool) (string, lipgloss.Style) {
	switch {
	case backend == config.AIBackendDisabled:
		return "off", detailMutedStyle
	case backend == config.AIBackendUnset:
		return "setup", detailWarningStyle
	case !known:
		return "selected", commandPalettePickStyle
	case status.Ready:
		return "ready", footerPrimaryLabelStyle
	case !status.Installed && backend.RequiresCLIInstallHint():
		return "install", detailWarningStyle
	default:
		return "setup", detailWarningStyle
	}
}

func renderInferenceStatusCard(card inferenceStatusCard, width int) string {
	totalWidth := max(26, width)
	innerWidth := max(10, totalWidth-2)
	title := card.Title
	titleStyle := card.TitleStyle()
	if card.Selected {
		titleStyle = titleStyle.Foreground(lipgloss.Color("230"))
	}
	state := card.StateStyle.Render(strings.ToUpper(strings.TrimSpace(card.State)))
	headerWidth := max(8, innerWidth-lipgloss.Width(state)-1)
	header := titleStyle.Render(truncateText(title, headerWidth))
	headerLine := fitFooterWidth(lipgloss.JoinHorizontal(lipgloss.Top, header, " ", state), innerWidth)
	lines := []string{
		headerLine,
		detailValueStyle.Render(fitFooterWidth(strings.TrimSpace(card.Value), innerWidth)),
		card.DetailStyle.Render(fitFooterWidth(strings.TrimSpace(card.Detail), innerWidth)),
	}
	style := inferenceStatusCardStyle
	if card.Selected {
		style = inferenceStatusCardSelectedStyle
	}
	return style.Width(innerWidth).Render(strings.Join(lines, "\n"))
}

func (c inferenceStatusCard) TitleStyle() lipgloss.Style {
	return detailSectionStyle
}

var inferenceStatusCardStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(dialogPanelBorderColor).
	Background(dialogPanelBackground)

var inferenceStatusCardSelectedStyle = inferenceStatusCardStyle.
	Border(lipgloss.ThickBorder()).
	BorderForeground(lipgloss.Color("214")).
	Background(lipgloss.Color("237"))
