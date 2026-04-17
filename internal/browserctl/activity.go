package browserctl

import (
	"fmt"
	"strings"
	"time"
)

type SessionActivityState string

const (
	SessionActivityStateIdle           SessionActivityState = "idle"
	SessionActivityStateActive         SessionActivityState = "active"
	SessionActivityStateWaitingForUser SessionActivityState = "waiting_for_user"
)

type SessionActivity struct {
	Policy      Policy
	State       SessionActivityState
	ServerName  string
	ToolName    string
	LastEventAt time.Time
}

func DefaultSessionActivity(policy Policy) SessionActivity {
	return SessionActivity{
		Policy: policy.Normalize(),
		State:  SessionActivityStateIdle,
	}
}

func (a SessionActivity) Normalize() SessionActivity {
	normalized := DefaultSessionActivity(a.Policy)
	switch a.State {
	case SessionActivityStateActive, SessionActivityStateWaitingForUser:
		normalized.State = a.State
	}
	normalized.ServerName = strings.TrimSpace(a.ServerName)
	normalized.ToolName = strings.TrimSpace(a.ToolName)
	normalized.LastEventAt = a.LastEventAt
	return normalized
}

func (a SessionActivity) Enabled() bool {
	return !a.Normalize().Policy.UsesLegacyLaunchBehavior()
}

func (a SessionActivity) Live() bool {
	switch a.Normalize().State {
	case SessionActivityStateActive, SessionActivityStateWaitingForUser:
		return true
	default:
		return false
	}
}

func (a SessionActivity) Summary() string {
	normalized := a.Normalize()
	if !normalized.Enabled() {
		return "Compatibility mode; provider-owned browser behavior."
	}
	source := "Playwright"
	if detail := normalized.SourceLabel(); detail != "" {
		source = detail
	}
	switch normalized.State {
	case SessionActivityStateWaitingForUser:
		return source + " is waiting for user input."
	case SessionActivityStateActive:
		return source + " is active."
	default:
		return "No live Playwright activity."
	}
}

func (a SessionActivity) SourceLabel() string {
	normalized := a.Normalize()
	switch {
	case normalized.ServerName != "" && normalized.ToolName != "":
		return fmt.Sprintf("%s/%s", normalized.ServerName, normalized.ToolName)
	case normalized.ServerName != "":
		return normalized.ServerName
	case normalized.ToolName != "":
		return normalized.ToolName
	default:
		return ""
	}
}

func IsPlaywrightToolCall(serverName, toolName string) bool {
	if isPlaywrightServerName(serverName) {
		return true
	}
	return strings.TrimSpace(serverName) == "" && isPlaywrightToolName(toolName)
}

func isPlaywrightServerName(serverName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(serverName))
	if normalized == "" {
		return false
	}
	return normalized == "playwright" ||
		normalized == "mcp__playwright__" ||
		strings.Contains(normalized, "playwright")
}

func isPlaywrightToolName(toolName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	return strings.HasPrefix(normalized, "browser_")
}
