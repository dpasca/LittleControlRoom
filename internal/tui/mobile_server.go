package tui

import (
	"fmt"
	"net"
	urlpkg "net/url"
	"strings"

	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type MobileServerStatus struct {
	URL           string
	ListenAddress string
	BoundAddress  string
	PairingCode   string
	AuthRequired  bool
	Disabled      bool
	LANAddresses  []string
	Error         string
}

func (m *Model) SetMobileServerStatus(status MobileServerStatus) {
	if m == nil {
		return
	}
	status.URL = strings.TrimSpace(status.URL)
	status.ListenAddress = strings.TrimSpace(status.ListenAddress)
	status.BoundAddress = strings.TrimSpace(status.BoundAddress)
	status.PairingCode = strings.TrimSpace(status.PairingCode)
	status.Error = strings.TrimSpace(status.Error)
	status.LANAddresses = normalizeMobileLANAddresses(status.LANAddresses)
	m.mobileServerStatus = status
}

func normalizeMobileLANAddresses(addresses []string) []string {
	clean := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		clean = append(clean, address)
	}
	return clean
}

func (m Model) mobileServerStatusMessage() string {
	status := m.mobileServerStatus
	if status.Disabled {
		return "Mobile client is disabled in Settings; lcroom serve can still start it explicitly"
	}
	if status.Error != "" {
		location := status.URL
		if location == "" && status.ListenAddress != "" {
			location = "http://" + status.ListenAddress
		}
		if location != "" {
			return fmt.Sprintf("Mobile client unavailable at %s: %s", location, status.Error)
		}
		return "Mobile client unavailable: " + status.Error
	}
	if status.URL != "" {
		if status.AuthRequired && status.PairingCode != "" {
			return fmt.Sprintf("Mobile client available at %s; pairing code %s", status.URL, status.PairingCode)
		}
		return "Mobile client available at " + status.URL
	}
	return "Mobile client is not running in this mode"
}

func (m *Model) openMobileDialog() {
	if m == nil {
		return
	}
	m.mobileDialogOpen = true
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Mobile access"
}

func (m *Model) closeMobileDialog(status string) {
	if m == nil {
		return
	}
	m.mobileDialogOpen = false
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) updateMobileDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeMobileDialog("Mobile access closed")
		return m, nil
	case "enter", "s":
		m.closeMobileDialog("")
		return m, m.openMobileSettingsMode()
	case "c":
		phoneURL := mobilePrimaryPhoneURL(m.mobileServerStatus)
		if phoneURL == "" {
			m.status = "Phone URL is not available yet; open Mobile Setup to enable LAN access"
			return m, nil
		}
		if err := clipboardTextWriter(phoneURL); err != nil {
			m.status = "Phone URL copy failed: " + err.Error()
			return m, nil
		}
		m.status = "Phone URL copied to clipboard"
		return m, nil
	case "ctrl+c":
		m.closeMobileDialog("")
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) renderMobileDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderMobileDialogPanel(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderMobileDialogPanel(bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(62, bodyW-12), 88))
	panelInnerW := max(32, panelW-4)
	return renderDialogPanel(panelW, panelInnerW, m.renderMobileDialogContent(panelInnerW, bodyH))
}

func (m Model) renderMobileDialogContent(width, bodyH int) string {
	status := m.mobileServerStatus
	state, stateStyle := mobileRuntimeState(status)
	phoneURLs := mobilePhoneURLs(status)
	phoneURL := ""
	if len(phoneURLs) > 0 {
		phoneURL = phoneURLs[0]
	}
	lines := []string{
		renderDialogHeader("Mobile Access", "", "", width),
		"",
		detailField("State", stateStyle.Render(state)),
	}
	if listener := mobileRuntimeListenerLabel(status); listener != "" {
		lines = append(lines, renderWrappedDetailField("Technical listener", detailValueStyle, width, listener))
		lines = append(lines, renderWrappedDetailField("Network note", detailMutedStyle, width, mobileListenAddressNote(listener)))
	}
	if computerURL := mobileComputerURL(status); computerURL != "" {
		lines = append(lines, renderWrappedDetailField("This computer", detailValueStyle, width, computerURL))
	}
	if phoneURL != "" {
		lines = append(lines, renderWrappedDetailField("Phone URL", footerPrimaryLabelStyle, width, phoneURL))
		if len(phoneURLs) > 1 {
			lines = append(lines, renderWrappedDetailField("Other LAN URLs", detailMutedStyle, width, strings.Join(phoneURLs[1:], ", ")))
		}
	} else {
		lines = append(lines, renderWrappedDetailField("Phone URL", detailWarningStyle, width, mobilePhoneUnavailableReason(status)))
	}
	if detected := mobileDetectedLANLabel(status.LANAddresses); detected != "" {
		lines = append(lines, renderWrappedDetailField("Detected LAN", detailMutedStyle, width, detected))
	} else {
		lines = append(lines, renderWrappedDetailField("Detected LAN", detailMutedStyle, width, "No private IPv4 address detected"))
	}
	if suggestion := mobileSuggestedPhoneURL(status); phoneURL == "" && suggestion != "" {
		lines = append(lines, renderWrappedDetailField("After setup", detailValueStyle, width, suggestion))
	}
	lines = append(lines, renderWrappedDetailField("Pairing", mobilePairingStyle(status), width, mobilePairingLabel(status)))

	if nextLaunch := m.mobileNextLaunchLabel(); nextLaunch != "" {
		lines = append(lines, renderWrappedDetailField("Next launch", detailWarningStyle, width, nextLaunch))
	}

	lines = append(lines, "", detailSectionStyle.Render("Next step"))
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, mobileNextStep(status))...)
	if mobileListenerAcceptsLAN(status) && bodyH >= 22 {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Trusted LAN only: pairing authenticates the browser, but plain HTTP is not encrypted.")...)
	}

	lines = append(lines, "", renderDialogButton("Open Mobile Setup", true))
	copyAction := ""
	if phoneURL != "" {
		copyAction = renderDialogAction("c", "copy phone URL", navigateActionKeyStyle, navigateActionTextStyle)
	}
	lines = append(lines, renderHelpPanelActionRow(
		renderDialogAction("Enter", "setup", commitActionKeyStyle, commitActionTextStyle),
		copyAction,
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
}

func (m Model) mobileNextLaunchLabel() string {
	settings := m.currentSettingsBaseline()
	savedAddress := mobileSavedListenAddress(settings)
	if !m.mobileRestartRequiredForSettings(settings) {
		return ""
	}
	if !settings.MobileEnabled {
		return "Disabled; restart required to apply the saved setup"
	}
	return "Enabled at " + savedAddress + "; restart required to apply the saved setup"
}

func (m Model) mobileRestartRequired() bool {
	if m.settingsBaseline != nil {
		return m.mobileRestartRequiredForSettings(*m.settingsBaseline)
	}
	return m.mobileRestartRequiredForSettings(m.currentSettingsBaseline())
}

func (m Model) mobileRestartRequiredForSettings(settings config.EditableSettings) bool {
	runtimeEnabled := !m.mobileServerStatus.Disabled
	return settings.MobileEnabled != runtimeEnabled ||
		mobileSavedListenAddress(settings) != strings.TrimSpace(m.mobileServerStatus.ListenAddress)
}

func mobileSavedListenAddress(settings config.EditableSettings) string {
	address := strings.TrimSpace(settings.MobileListenAddress)
	if address == "" {
		return config.DefaultMobileListenAddress
	}
	return address
}

func mobileRuntimeState(status MobileServerStatus) (string, lipgloss.Style) {
	switch {
	case status.Disabled:
		return "Disabled", detailMutedStyle
	case status.Error != "":
		return "Unavailable - " + status.Error, detailDangerStyle
	case strings.TrimSpace(status.URL) == "":
		return "Not running", detailWarningStyle
	case mobileListenerAcceptsLAN(status):
		if len(mobilePhoneURLs(status)) > 0 {
			return "LAN ready", footerPrimaryLabelStyle
		}
		return "LAN listener; address not detected", detailWarningStyle
	default:
		return "Local only", detailWarningStyle
	}
}

func mobilePhoneUnavailableReason(status MobileServerStatus) string {
	switch {
	case status.Disabled:
		return "Not running; mobile auto-start is disabled"
	case status.Error != "":
		return "Unavailable until the listener error is fixed"
	case strings.TrimSpace(status.URL) == "":
		return "The mobile server is not running"
	case !mobileListenerAcceptsLAN(status):
		return "Not reachable from the LAN; the listener is local only"
	default:
		return "LAN listener is running, but no private IPv4 address was detected"
	}
}

func mobileNextStep(status MobileServerStatus) string {
	phoneURL := mobilePrimaryPhoneURL(status)
	switch {
	case phoneURL != "":
		if status.AuthRequired && strings.TrimSpace(status.PairingCode) != "" {
			return "Connect the phone to this network, open " + phoneURL + ", and enter pairing code " + status.PairingCode + "."
		}
		return "Connect the phone to this network and open " + phoneURL + "."
	case status.Error != "":
		return "Open Mobile Setup to choose another address or port, save, and restart Little Control Room."
	case status.Disabled:
		return "Open Mobile Setup, enable the interface, choose Phones on this LAN, save, and restart Little Control Room."
	case !mobileListenerAcceptsLAN(status):
		return "Open Mobile Setup, choose Phones on this LAN, save, and restart Little Control Room."
	default:
		return "Check that this computer is connected to the same LAN as the phone, then reopen /mobile."
	}
}

func mobilePairingLabel(status MobileServerStatus) string {
	if status.AuthRequired {
		if code := strings.TrimSpace(status.PairingCode); code != "" {
			return "Required - code " + code
		}
		return "Required"
	}
	if mobileRuntimeRunning(status) {
		return "Not required for local-only access"
	}
	return "Starts automatically for a LAN listener"
}

func mobilePairingStyle(status MobileServerStatus) lipgloss.Style {
	if status.AuthRequired && strings.TrimSpace(status.PairingCode) != "" {
		return detailWarningStyle.Bold(true)
	}
	return detailMutedStyle
}

func mobileRuntimeRunning(status MobileServerStatus) bool {
	return !status.Disabled && strings.TrimSpace(status.Error) == "" && strings.TrimSpace(status.URL) != ""
}

func mobileListenerAcceptsLAN(status MobileServerStatus) bool {
	if !mobileRuntimeRunning(status) {
		return false
	}
	host, _ := mobileListenHostPort(status.ListenAddress)
	return mobileWildcardHost(host) || !mobileLoopbackHost(host)
}

func mobilePhoneURLs(status MobileServerStatus) []string {
	if !mobileListenerAcceptsLAN(status) {
		return nil
	}
	host, _ := mobileListenHostPort(status.ListenAddress)
	port := mobileRuntimePort(status)
	if port == "" {
		return nil
	}
	if !mobileWildcardHost(host) {
		return []string{mobileHTTPURL(host, port)}
	}
	urls := make([]string, 0, len(status.LANAddresses))
	for _, address := range status.LANAddresses {
		if address = strings.TrimSpace(address); address != "" {
			urls = append(urls, mobileHTTPURL(address, port))
		}
	}
	return urls
}

func mobilePrimaryPhoneURL(status MobileServerStatus) string {
	if urls := mobilePhoneURLs(status); len(urls) > 0 {
		return urls[0]
	}
	return ""
}

func mobileComputerURL(status MobileServerStatus) string {
	if !mobileRuntimeRunning(status) {
		return ""
	}
	host, _ := mobileListenHostPort(status.ListenAddress)
	if mobileWildcardHost(host) {
		return mobileHTTPURL("127.0.0.1", mobileRuntimePort(status))
	}
	return strings.TrimSpace(status.URL)
}

func mobileSuggestedPhoneURL(status MobileServerStatus) string {
	if len(status.LANAddresses) == 0 {
		return ""
	}
	port := mobileRuntimePort(status)
	if port == "" {
		return ""
	}
	return mobileHTTPURL(status.LANAddresses[0], port)
}

func mobileRuntimePort(status MobileServerStatus) string {
	if rawURL := strings.TrimSpace(status.URL); rawURL != "" {
		if parsed, err := urlpkg.Parse(rawURL); err == nil {
			if _, port := mobileListenHostPort(parsed.Host); port != "" {
				return port
			}
		}
	}
	_, port := mobileListenHostPort(status.ListenAddress)
	return port
}

func mobileRuntimeListenerLabel(status MobileServerStatus) string {
	if bound := strings.TrimSpace(status.BoundAddress); bound != "" {
		return bound
	}
	return strings.TrimSpace(status.ListenAddress)
}

func mobileListenHostPort(address string) (string, string) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(host), strings.TrimSpace(port)
}

func mobileLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mobileWildcardHost(host string) bool {
	host = strings.TrimSpace(host)
	return host == "" || host == "0.0.0.0" || host == "::"
}

func mobileHTTPURL(host, port string) string {
	return "http://" + net.JoinHostPort(strings.TrimSpace(host), strings.TrimSpace(port))
}

func mobileDetectedLANLabel(addresses []string) string {
	addresses = normalizeMobileLANAddresses(addresses)
	if len(addresses) == 0 {
		return ""
	}
	if len(addresses) <= 3 {
		return strings.Join(addresses, ", ")
	}
	return strings.Join(addresses[:3], ", ") + fmt.Sprintf(" (+%d more)", len(addresses)-3)
}

func (m Model) renderMobileTopStatusIndicator(width int) string {
	if width < 72 || strings.TrimSpace(m.mobileServerStatus.ListenAddress) == "" {
		return ""
	}
	status := m.mobileServerStatus
	restartRequired := m.mobileRestartRequired()
	label := "/mobile"
	if width >= 96 {
		switch {
		case status.Error != "":
			label += " ERR"
		case restartRequired:
			label += " RESTART"
		case status.Disabled:
			label += " OFF"
		case !mobileRuntimeRunning(status):
			label += " SETUP"
		case mobileListenerAcceptsLAN(status):
			label += " LAN"
		default:
			label += " SETUP"
		}
	}
	switch {
	case status.Error != "":
		return topStatusDangerBadgeStyle.Render(label)
	case restartRequired:
		return topStatusSetupBadgeStyle.Render(label)
	case status.Disabled:
		return detailMutedStyle.Render(label)
	case !mobileRuntimeRunning(status):
		return topStatusSetupBadgeStyle.Render(label)
	case mobileListenerAcceptsLAN(status):
		return renderFooterUsage(label)
	default:
		return topStatusSetupBadgeStyle.Render(label)
	}
}

func (m Model) renderMobileServerStatusNotice() string {
	if strings.TrimSpace(m.mobileServerStatus.Error) == "" {
		return ""
	}
	return topStatusDangerBadgeStyle.Render(m.mobileServerStatusMessage())
}
