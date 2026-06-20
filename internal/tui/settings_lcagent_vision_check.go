package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func settingsFieldCanCheckLCAgentVision(index int) bool {
	return index == settingsFieldLCAgentProvider ||
		index == settingsFieldLCAgentModel ||
		index == settingsFieldLCAgentVisionProvider ||
		index == settingsFieldLCAgentVisionModel
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
	checkMain := fieldIndex == settingsFieldLCAgentProvider || fieldIndex == settingsFieldLCAgentModel
	visionProvider := settingsLCAgentVisionProviderValue(settings.LCAgentVisionProvider)
	if visionProvider == "auto" {
		checkMain = true
	}
	if checkMain {
		visionProvider = "main"
	}
	if strings.EqualFold(visionProvider, "off") {
		m.status = "LCAgent vision provider is off. Choose a provider before checking image input."
		return m, nil
	}
	req := m.lcagentLaunchRequestFromSettings(m.currentSelectedProjectPath(), settings)
	req.LCAgentVisionProvider = visionProvider
	if visionProvider == "main" {
		req.LCAgentVisionModel = ""
	}
	m.settingsLCAgentVisionCheckInFlight = true
	m.status = "Checking LCAgent vision image input..."
	return m, settingsLCAgentVisionCheckCmd(req, checkMain, settingsLCAgentMainProvider(settings), settingsLCAgentMainModel(settings))
}

func settingsLCAgentVisionCheckCmd(req codexapp.LaunchRequest, checkedMain bool, mainProvider string, mainModel string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		result, err := codexapp.CheckLCAgentVisionAccess(ctx, req)
		return settingsLCAgentVisionCheckMsg{
			result:       result,
			err:          err,
			checkedMain:  checkedMain,
			mainProvider: strings.TrimSpace(mainProvider),
			mainModel:    strings.TrimSpace(mainModel),
		}
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
		if !msg.checkedMain || strings.TrimSpace(msg.mainProvider) == "" || strings.TrimSpace(msg.mainModel) == "" {
			return m, nil
		}
		settings := m.currentSettingsBaseline()
		if strings.EqualFold(strings.TrimSpace(settings.LCAgentMainVisionProvider), strings.TrimSpace(msg.mainProvider)) &&
			strings.EqualFold(strings.TrimSpace(settings.LCAgentMainVisionModel), strings.TrimSpace(msg.mainModel)) {
			settings.LCAgentMainVisionProvider = ""
			settings.LCAgentMainVisionModel = ""
			saved := cloneEditableSettings(settings)
			m.settingsBaseline = &saved
			m.status += " Auto image analysis will stay off for this Main Model."
			return m, settingsLCAgentVisionCapabilitySaveCmd(m.currentWritableConfigPath(), settings)
		}
		return m, nil
	}
	m.status = fmt.Sprintf("LCAgent vision verified image input for %s / %s. Reply: %s", provider, model, truncateText(response, 140))
	if !msg.checkedMain || strings.TrimSpace(msg.mainProvider) == "" || strings.TrimSpace(msg.mainModel) == "" {
		return m, nil
	}
	settings := m.currentSettingsBaseline()
	settings.LCAgentMainVisionProvider = strings.TrimSpace(msg.mainProvider)
	settings.LCAgentMainVisionModel = strings.TrimSpace(msg.mainModel)
	saved := cloneEditableSettings(settings)
	m.settingsBaseline = &saved
	m.status += " Auto image analysis can now use the Main Model."
	return m, settingsLCAgentVisionCapabilitySaveCmd(m.currentWritableConfigPath(), settings)
}

func settingsLCAgentVisionCapabilitySaveCmd(path string, settings config.EditableSettings) tea.Cmd {
	settings = config.NormalizeEditableSettings(settings)
	return func() tea.Msg {
		err := config.SaveEditableSettings(path, settings)
		return settingsLCAgentVisionCapabilitySavedMsg{settings: settings, path: path, err: err}
	}
}
