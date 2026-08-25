package claudecli

import (
	"fmt"
	"strings"
)

type PermissionMode string

const (
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
	PermissionModeAcceptEdits       PermissionMode = "acceptEdits"
	PermissionModeManual            PermissionMode = "manual"
	PermissionModeDontAsk           PermissionMode = "dontAsk"
	PermissionModePlan              PermissionMode = "plan"
)

func DefaultPermissionMode() PermissionMode {
	return PermissionModeAuto
}

func ParsePermissionMode(raw string) (PermissionMode, error) {
	switch normalizePermissionMode(raw) {
	case "", "auto":
		return PermissionModeAuto, nil
	case "bypass", "bypasspermissions", "yolo":
		return PermissionModeBypassPermissions, nil
	case "acceptedits", "fullauto":
		return PermissionModeAcceptEdits, nil
	case "default", "manual", "safe":
		return PermissionModeManual, nil
	case "dontask":
		return PermissionModeDontAsk, nil
	case "plan":
		return PermissionModePlan, nil
	default:
		return "", fmt.Errorf("Claude Code permission mode must be one of: auto, bypassPermissions, acceptEdits, manual, dontAsk, plan")
	}
}

func normalizePermissionMode(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	return normalized
}

func (m PermissionMode) DisplayName() string {
	switch m {
	case PermissionModeAuto:
		return "Auto"
	case PermissionModeBypassPermissions:
		return "Bypass Permissions"
	case PermissionModeAcceptEdits:
		return "Accept Edits"
	case PermissionModeManual:
		return "Manual"
	case PermissionModeDontAsk:
		return "Don't Ask"
	case PermissionModePlan:
		return "Plan"
	default:
		return strings.TrimSpace(string(m))
	}
}

func (m PermissionMode) UsesApprovalBridge() bool {
	return m != PermissionModeBypassPermissions && m != PermissionModeDontAsk
}
