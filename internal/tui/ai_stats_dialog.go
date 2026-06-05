package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/config"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const aiStatsFailedProjectLimit = 5

func (m *Model) openAIStatsDialog() tea.Cmd {
	m.showAIStats = true
	m.showPerf = false
	m.showHelp = false
	m.err = nil
	m.status = "AI stats open. Press c to copy, r to refresh, or Esc to close"
	return m.refreshSetupSnapshotCmd(false)
}

func (m *Model) closeAIStatsDialog(status string) {
	m.showAIStats = false
	if status != "" {
		m.status = status
	}
}

func (m Model) updateAIStatsMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "c":
		return m, m.copyAIStatsToClipboard()
	case "r":
		m.status = "Refreshing AI backend status..."
		return m, m.refreshSetupSnapshotCmd(false)
	case "esc", "enter", "?":
		m.closeAIStatsDialog("AI stats closed")
	}
	return m, nil
}

func (m *Model) copyAIStatsToClipboard() tea.Cmd {
	if err := clipboardTextWriter(m.formatAIStatsCopyText()); err != nil {
		m.reportError("AI stats copy failed", err, "")
		return nil
	}
	m.err = nil
	m.status = "Copied AI stats to clipboard"
	return nil
}

func (m Model) formatAIStatsCopyText() string {
	return strings.TrimSpace(ansi.Strip(m.renderAIStatsContent(100)))
}

func (m Model) renderAIStats(bodyW int) string {
	panelWidth := min(bodyW, min(max(60, bodyW-12), 96))
	panelInnerWidth := max(32, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderAIStatsContent(panelInnerWidth))
}

func (m Model) renderAIStatsOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderAIStats(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderAIStatsContent(width int) string {
	backend, backendStatus := m.aiStatsBackendStatus()
	usage := m.currentUsage()
	failedProjects, totalFailed := m.aiStatsFailedProjects(aiStatsFailedProjectLimit)

	lines := []string{
		renderDialogHeader("AI Stats", backend.Label(), "", width),
		commandPaletteHintStyle.Render("Internal assessment and usage snapshot."),
		"",
		detailSectionStyle.Render("Assessments"),
		detailField("Status", m.renderClassificationSummary()),
		detailField("Calls", aiStatsCallsValue(usage)),
	}
	if aiStatsShowsCost(backend) {
		lines = append(lines, detailField("Cost", aiStatsCostValue(usage)))
	} else {
		lines = append(lines, detailField("Billing", aiStatsBillingValue(backend)))
	}
	lines = append(lines, detailField("Tokens", aiStatsTokensValue(usage)))
	if billingNotice := aiStatsBillingNotice(backend); billingNotice != "" {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, billingNotice)...)
	}
	if modelValue := aiStatsModelValue(usage); modelValue != "" {
		lines = append(lines, detailField("Model", modelValue))
	}
	if speedValue := aiStatsSpeedValue(usage); speedValue != "" {
		lines = append(lines, detailField("Speed", speedValue))
	}
	if activityValue := aiStatsActivityValue(usage); activityValue != "" {
		lines = append(lines, detailField("Recent", activityValue))
	}

	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("Backend"))
	lines = append(lines, detailField("Selected", aiStatsBackendLabelValue(backend, backendStatus)))
	if detail := strings.TrimSpace(backendStatus.Detail); detail != "" {
		lines = append(lines, detailField("State", aiStatsBackendDetailValue(backend, backendStatus)))
	}
	if contextValue := aiStatsContextValue(backendStatus); contextValue != "" {
		lines = append(lines, detailField("Context", contextValue))
	}
	if warning := strings.TrimSpace(backendStatus.ContextWarning); warning != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, warning)...)
	}
	if notice := m.renderAIBackendStatusNotice(); notice != "" {
		lines = append(lines, detailField("Notice", notice))
	}
	if hint := strings.TrimSpace(backendStatus.LoginHint); hint != "" && !backendStatus.Ready {
		lines = append(lines, detailField("Fix", commandPaletteHintStyle.Render(hint)))
	}

	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("Assessment Attention"))
	if totalFailed == 0 {
		lines = append(lines, detailField("Check", detailValueStyle.Render("none right now")))
	} else {
		lines = append(lines, detailField("Check", detailDangerStyle.Render(fmt.Sprintf("%d project(s) need attention", totalFailed))))
		lines = append(lines, commandPaletteHintStyle.Render("These are project assessment states, not /errors entries."))
		for _, projectName := range failedProjects {
			lines = append(lines, detailDangerStyle.Render("- "+truncateText(projectName, max(12, width-4))))
		}
		if totalFailed > len(failedProjects) {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("+ %d more", totalFailed-len(failedProjects))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, commandPaletteHintStyle.Render("Open /ai whenever you want these internal counters again."))
	lines = append(lines, "")
	lines = append(lines, renderHelpPanelActionRow(
		renderDialogAction("c", "copy", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
}

func (m Model) aiStatsBackendStatus() (config.AIBackend, aibackend.Status) {
	if m.setupChecked {
		return m.setupSnapshot.Selected, m.setupSnapshot.SelectedStatus()
	}

	backend := m.currentSettingsBaseline().AIBackend
	switch backend {
	case config.AIBackendDisabled:
		return backend, aibackend.Status{
			Backend: backend,
			Label:   backend.Label(),
			Ready:   true,
			Detail:  "AI features disabled by choice.",
		}
	case config.AIBackendUnset:
		return backend, aibackend.Status{
			Backend: backend,
			Label:   backend.Label(),
			Detail:  "Run /setup to pick and check an AI backend.",
		}
	default:
		return backend, aibackend.Status{
			Backend: backend,
			Label:   backend.Label(),
			Detail:  "Backend status check pending. Press r to refresh it now.",
		}
	}
}

func aiStatsBackendLabelValue(backend config.AIBackend, status aibackend.Status) string {
	switch {
	case backend == config.AIBackendDisabled:
		return detailMutedStyle.Render(status.Label)
	case backend == config.AIBackendUnset:
		return detailWarningStyle.Render(status.Label)
	case status.Ready:
		return detailValueStyle.Render(status.Label)
	default:
		return detailWarningStyle.Render(status.Label)
	}
}

func aiStatsBackendDetailValue(backend config.AIBackend, status aibackend.Status) string {
	detail := strings.TrimSpace(status.Detail)
	if detail == "" {
		return ""
	}
	switch {
	case backend == config.AIBackendDisabled:
		return detailMutedStyle.Render(detail)
	case backend == config.AIBackendUnset, !status.Ready:
		return detailWarningStyle.Render(detail)
	default:
		return detailValueStyle.Render(detail)
	}
}

func aiStatsCallsValue(usage model.LLMSessionUsage) string {
	parts := []string{fmt.Sprintf("started %d", usage.Started)}
	if usage.Running > 0 {
		parts = append(parts, fmt.Sprintf("running %d", usage.Running))
	}
	parts = append(parts, fmt.Sprintf("ok %d", usage.Completed))
	parts = append(parts, fmt.Sprintf("failed %d", usage.Failed))
	return detailValueStyle.Render(strings.Join(parts, " | "))
}

func aiStatsShowsCost(backend config.AIBackend) bool {
	return backend == config.AIBackendOpenAIAPI
}

func aiStatsCostValue(usage model.LLMSessionUsage) string {
	if !usage.Enabled {
		return detailMutedStyle.Render("off")
	}
	if estimatedCostUSD, ok := estimatedUsageCostUSD(usage); ok {
		return detailValueStyle.Render(formatEstimatedCostUSD(estimatedCostUSD))
	}
	return detailWarningStyle.Render("unknown")
}

func aiStatsBillingValue(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendDisabled:
		return detailMutedStyle.Render("disabled")
	case config.AIBackendUnset:
		return detailWarningStyle.Render("not configured")
	default:
		if backend.UsesLocalProviderPath() {
			return detailMutedStyle.Render("local provider mode")
		}
		return detailMutedStyle.Render("not available")
	}
}

func aiStatsBillingNotice(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return "These numbers are still estimates. Your OpenAI dashboard is the billing source of truth."
	default:
		if backend.UsesLocalProviderPath() {
			return fmt.Sprintf("%s is running through its local provider path here, so Little Control Room only shows calls and tokens. Switch to the OpenAI API backend in /setup if you want estimated API-key spend.", backend.Label())
		}
		return ""
	}
}

func aiStatsTokensValue(usage model.LLMSessionUsage) string {
	totals := usage.Totals
	if totals.InputTokens == 0 && totals.OutputTokens == 0 && totals.TotalTokens == 0 && totals.CachedInputTokens == 0 && totals.ReasoningTokens == 0 {
		return detailMutedStyle.Render("none yet")
	}

	parts := make([]string, 0, 5)
	if totals.InputTokens > 0 {
		parts = append(parts, "in "+formatTokenCount(totals.InputTokens))
	}
	if totals.OutputTokens > 0 {
		parts = append(parts, "out "+formatTokenCount(totals.OutputTokens))
	}
	if totals.TotalTokens > 0 {
		parts = append(parts, "total "+formatTokenCount(totals.TotalTokens))
	}
	if totals.CachedInputTokens > 0 {
		parts = append(parts, "cached "+formatTokenCount(totals.CachedInputTokens))
	}
	if totals.ReasoningTokens > 0 {
		parts = append(parts, "reason "+formatTokenCount(totals.ReasoningTokens))
	}
	return detailValueStyle.Render(strings.Join(parts, " | "))
}

func aiStatsModelValue(usage model.LLMSessionUsage) string {
	if modelName := strings.TrimSpace(usage.Model); modelName != "" {
		return detailValueStyle.Render(modelName)
	}
	return ""
}

func aiStatsSpeedValue(usage model.LLMSessionUsage) string {
	parts := make([]string, 0, 3)
	if usage.LastOutputTokensPerSecond > 0 {
		label := "last"
		if usage.LastOutputEvalDuration > 0 {
			label = "decode last"
		}
		parts = append(parts, fmt.Sprintf("%s %.1f tok/s", label, usage.LastOutputTokensPerSecond))
	}
	if usage.AverageOutputTokensPerSecond > 0 && usage.Completed > 1 {
		label := "avg"
		if usage.Totals.OutputEvalDuration > 0 {
			label = "decode avg"
		}
		parts = append(parts, fmt.Sprintf("%s %.1f tok/s", label, usage.AverageOutputTokensPerSecond))
	}
	if usage.LastRequestDuration > 0 {
		parts = append(parts, "request last "+usage.LastRequestDuration.Round(time.Millisecond).String())
	}
	if len(parts) == 0 {
		return ""
	}
	return detailValueStyle.Render(strings.Join(parts, " | "))
}

func aiStatsActivityValue(usage model.LLMSessionUsage) string {
	parts := make([]string, 0, 2)
	if !usage.LastStartedAt.IsZero() {
		parts = append(parts, "start "+usage.LastStartedAt.Format(timeFieldFormat))
	}
	if !usage.LastFinishedAt.IsZero() {
		parts = append(parts, "finish "+usage.LastFinishedAt.Format(timeFieldFormat))
	}
	if len(parts) == 0 {
		return ""
	}
	return detailValueStyle.Render(strings.Join(parts, " | "))
}

func aiStatsContextValue(status aibackend.Status) string {
	detail := strings.TrimSpace(status.ContextDetail)
	if detail != "" {
		return detailValueStyle.Render(detail)
	}
	if status.ContextWindow > 0 {
		return detailValueStyle.Render(formatTokenCount(status.ContextWindow) + " tokens")
	}
	return ""
}

func (m Model) aiStatsFailedProjects(limit int) ([]string, int) {
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}

	failed := make([]string, 0, min(limit, len(projects)))
	total := 0
	for _, project := range projects {
		if project.LatestSessionClassification != model.ClassificationFailed {
			continue
		}
		total++
		if limit <= 0 || len(failed) >= limit {
			continue
		}
		failed = append(failed, projectTitle(project.Path, project.Name))
	}
	return failed, total
}
