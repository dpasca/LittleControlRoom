package tui

import (
	"fmt"
	"strings"
)

type MobileServerStatus struct {
	URL           string
	ListenAddress string
	Error         string
}

func (m *Model) SetMobileServerStatus(status MobileServerStatus) {
	if m == nil {
		return
	}
	status.URL = strings.TrimSpace(status.URL)
	status.ListenAddress = strings.TrimSpace(status.ListenAddress)
	status.Error = strings.TrimSpace(status.Error)
	m.mobileServerStatus = status
}

func (m Model) mobileServerStatusMessage() string {
	status := m.mobileServerStatus
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
		return "Mobile client available at " + status.URL
	}
	return "Mobile client is not running in this mode"
}

func (m Model) renderMobileServerStatusNotice() string {
	if strings.TrimSpace(m.mobileServerStatus.Error) == "" {
		return ""
	}
	return topStatusDangerBadgeStyle.Render(m.mobileServerStatusMessage())
}
