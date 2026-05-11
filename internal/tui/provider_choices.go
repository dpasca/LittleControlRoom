package tui

import (
	"strings"

	"lcroom/internal/aibackend"
	"lcroom/internal/config"

	"github.com/charmbracelet/lipgloss"
)

type providerChoiceRole string

const (
	providerChoiceRoleProjectReports providerChoiceRole = "project-reports"
	providerChoiceRoleBossChat       providerChoiceRole = "boss-chat"
)

type providerChoice struct {
	Value       config.AIBackend
	Label       string
	Summary     string
	Description string
	State       string
	StateStyle  lipgloss.Style
	Detail      string
	NextStep    string
}

func (m Model) providerChoices(role providerChoiceRole, settings config.EditableSettings) []providerChoice {
	switch role {
	case providerChoiceRoleBossChat:
		return m.bossChatProviderChoices(settings)
	default:
		return m.projectReportsProviderChoices(settings)
	}
}

func (m Model) projectReportsProviderChoices(settings config.EditableSettings) []providerChoice {
	specs := []providerChoice{
		{
			Value:       config.AIBackendCodex,
			Label:       "Codex",
			Summary:     "Use your local Codex CLI installation for project analysis.",
			Description: "Requires Codex to be installed and authenticated. No API key is stored by Little Control Room.",
		},
		{
			Value:       config.AIBackendOpenCode,
			Label:       "OpenCode",
			Summary:     "Use your local OpenCode installation for project analysis.",
			Description: "Requires OpenCode to be installed and authenticated. No API key is stored by Little Control Room.",
		},
		{
			Value:       config.AIBackendClaude,
			Label:       "Claude Code",
			Summary:     "Use your local Claude Code installation for project analysis.",
			Description: "Requires Claude Code to be installed and authenticated. Background tasks default to Haiku to keep usage lighter.",
		},
		{
			Value:       config.AIBackendMLX,
			Label:       "MLX",
			Summary:     "Use a local MLX OpenAI-compatible endpoint for project analysis.",
			Description: "Requires a local MLX server running at the configured endpoint. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOllama,
			Label:       "Ollama",
			Summary:     "Use a local Ollama OpenAI-compatible endpoint for project analysis.",
			Description: "Requires a local Ollama server running at the configured endpoint. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Use a direct OpenAI API key for project analysis.",
			Description: "Requires an OpenAI API key to be saved. This is the most predictable setup if you do not have Codex, OpenCode, or Claude Code installed.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Disabled",
			Summary:     "Turn off AI-powered project analysis.",
			Description: "Little Control Room keeps working, but summaries, classifications, and commit help stay off.",
		},
	}
	for i := range specs {
		status, known := m.inferenceBackendStatus(specs[i].Value, settings)
		specs[i].State, specs[i].StateStyle = inferenceStateForBackend(specs[i].Value, status, known)
		specs[i].Detail = providerChoiceStatusDetail(status, "Availability will refresh in the background.")
		specs[i].NextStep = projectReportsProviderNextStep(specs[i].Value, status, known)
	}
	return specs
}

func (m Model) bossChatProviderChoices(settings config.EditableSettings) []providerChoice {
	specs := []providerChoice{
		{
			Value:       config.AIBackendUnset,
			Label:       "Auto",
			Summary:     "Use OpenAI API automatically when a saved API key exists.",
			Description: "This keeps boss chat low-friction without forcing a separate backend choice. If no OpenAI API key is saved, boss chat stays unconfigured.",
		},
		{
			Value:       config.AIBackendOpenAIAPI,
			Label:       "OpenAI API",
			Summary:     "Use direct OpenAI API inference for the high-level /boss conversation.",
			Description: "Project reports can still use Codex, OpenCode, Claude Code, MLX, Ollama, or another backend. Boss chat only shares the saved API key.",
		},
		{
			Value:       config.AIBackendMLX,
			Label:       "MLX",
			Summary:     "Use your local MLX OpenAI-compatible endpoint for boss chat.",
			Description: "Uses the MLX endpoint, API key, and model fields. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendOllama,
			Label:       "Ollama",
			Summary:     "Use your local Ollama OpenAI-compatible endpoint for boss chat.",
			Description: "Uses the Ollama endpoint, API key, and model fields. Leave the model blank to auto-use the first discovered local model.",
		},
		{
			Value:       config.AIBackendDisabled,
			Label:       "Off",
			Summary:     "Turn off boss chat inference.",
			Description: "The classic TUI and project-report inference keep working. This only disables the high-level chat assistant.",
		},
	}
	for i := range specs {
		optionSettings := settings
		optionSettings.BossChatBackend = specs[i].Value
		card := m.bossChatStatusCard(optionSettings)
		specs[i].State = card.State
		specs[i].StateStyle = card.StateStyle
		specs[i].Detail = strings.TrimSpace(card.Detail)
		specs[i].NextStep = bossChatProviderNextStep(specs[i], settings)
	}
	return specs
}

func projectReportsProviderNextStep(backend config.AIBackend, status aibackend.Status, known bool) string {
	switch {
	case backend == config.AIBackendDisabled:
		return "Save to keep background AI off."
	case status.Ready:
		return "Save to use this for project reports."
	case backend == config.AIBackendOpenAIAPI:
		return "Paste and save an OpenAI API key."
	case !known:
		return "Refresh availability, then save if this is the provider you want."
	case !status.Installed && backend.RequiresCLIInstallHint():
		return "Install and sign in to " + backend.Label() + "."
	case status.LoginHint != "":
		return strings.TrimSpace(status.LoginHint)
	default:
		return "Finish setup for " + backend.Label() + ", then refresh."
	}
}

func bossChatProviderNextStep(choice providerChoice, settings config.EditableSettings) string {
	switch choice.Value {
	case config.AIBackendUnset:
		if strings.TrimSpace(settings.OpenAIAPIKey) != "" {
			return "Save to let boss chat use the saved OpenAI API key automatically."
		}
		return "Save an OpenAI API key, choose a local chat backend, or turn boss chat off."
	case config.AIBackendDisabled:
		return "Save to keep boss chat off."
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return "Paste and save an OpenAI API key."
		}
		return "Save to use OpenAI API for boss chat."
	case config.AIBackendMLX, config.AIBackendOllama:
		if choice.State == "ready" {
			return "Save to use this local backend for boss chat."
		}
		return "Start or configure the " + choice.Label + " local endpoint, then refresh."
	default:
		return "Choose a supported boss chat backend."
	}
}

func providerChoiceStatusDetail(status aibackend.Status, fallback string) string {
	detail := strings.TrimSpace(status.Detail)
	if status.LoginHint != "" && !status.Ready {
		detail = strings.TrimSpace(status.LoginHint)
	}
	if detail == "" {
		detail = fallback
	}
	return detail
}

func providerChoiceSelection(options []providerChoice, current config.AIBackend) int {
	for i, option := range options {
		if option.Value == current {
			return i
		}
	}
	return 0
}

func providerChoiceLabel(options []providerChoice, current config.AIBackend, fallback string) string {
	for _, option := range options {
		if option.Value == current {
			return option.Label
		}
	}
	return fallback
}

func renderProviderChoiceStatus(choice providerChoice) string {
	detail := strings.TrimSpace(choice.Detail)
	if detail == "" {
		detail = "Availability will refresh in the background."
	}
	return choice.StateStyle.Render(choice.State) + detailMutedStyle.Render(" - "+detail)
}

func renderProviderChoiceRow(choice providerChoice, selected, current bool, width int) string {
	labelStyle := detailValueStyle.Bold(true)
	if selected {
		labelStyle = labelStyle.Foreground(lipgloss.Color("230"))
	}
	markerStyle := commandPaletteHintStyle
	if selected {
		markerStyle = commandPalettePickStyle
	}
	marker := markerStyle.Render(" ")
	if selected {
		marker = markerStyle.Render("›")
	}
	labelWidth := min(24, max(12, width/2))
	stateWidth := 12
	row := marker + " " +
		labelStyle.Width(labelWidth).Render(truncateText(choice.Label, labelWidth)) +
		choice.StateStyle.Width(stateWidth).Render(truncateText(choice.State, stateWidth))
	if current {
		row += "  " + detailMutedStyle.Render("(current)")
	}
	row = fitFooterWidth(row, width)
	if selected {
		return projectListSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}
