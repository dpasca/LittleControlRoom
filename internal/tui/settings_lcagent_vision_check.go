package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
)

func settingsFieldCanCheckLCAgentVision(index int) bool {
	return index == settingsFieldLCAgentVisionProvider || index == settingsFieldLCAgentVisionModel
}

func (m Model) checkSettingsLCAgentVision() (tea.Model, tea.Cmd) {
	if m.settingsLCAgentVisionCheckInFlight {
		m.status = "LCAgent vision check is already running."
		return m, nil
	}
	fieldIndex := m.settingsSelected
	if m.setupMode && m.setupConfigMode {
		fieldIndex = m.setupSelectedConfigFieldIndex()
	}
	if !settingsFieldCanCheckLCAgentVision(fieldIndex) {
		return m, nil
	}
	settings := m.settingsDraftForInferenceStatus()
	if strings.EqualFold(settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider), "off") {
		m.status = "LCAgent vision provider is off. Choose a provider before checking image input."
		return m, nil
	}
	req := m.lcagentLaunchRequestFromSettings(m.currentSelectedProjectPath(), settings)
	m.settingsLCAgentVisionCheckInFlight = true
	m.status = "Checking LCAgent vision image input..."
	return m, settingsLCAgentVisionCheckCmd(req)
}

func settingsLCAgentVisionCheckCmd(req codexapp.LaunchRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		result, err := codexapp.CheckLCAgentVisionAccess(ctx, req)
		return settingsLCAgentVisionCheckMsg{result: result, err: err}
	}
}

func (m Model) applySettingsLCAgentVisionCheckMsg(msg settingsLCAgentVisionCheckMsg) (tea.Model, tea.Cmd) {
	m.settingsLCAgentVisionCheckInFlight = false
	if msg.err != nil {
		m.reportError("LCAgent vision check failed", msg.err, "")
		m.status = "LCAgent vision check failed: " + truncateText(msg.err.Error(), 180)
		return m, nil
	}
	provider := settingsLCAgentModelPickerProviderLabel(msg.result.Provider)
	model := strings.TrimSpace(msg.result.Model)
	if model == "" {
		model = "selected model"
	}
	response := strings.TrimSpace(msg.result.Response)
	if response == "" {
		response = "no text returned"
	}
	if !msg.result.Verified {
		m.status = fmt.Sprintf("LCAgent vision request completed for %s / %s, but pixel inspection was not verified. Reply: %s", provider, model, truncateText(response, 140))
		return m, nil
	}
	m.status = fmt.Sprintf("LCAgent vision verified image input for %s / %s. Reply: %s", provider, model, truncateText(response, 140))
	return m, nil
}
