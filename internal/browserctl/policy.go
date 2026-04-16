package browserctl

import (
	"fmt"
	"strings"
)

type ManagementMode string

const (
	ManagementModeLegacy  ManagementMode = "legacy"
	ManagementModeObserve ManagementMode = "observe"
	ManagementModeManaged ManagementMode = "managed"
)

type BrowserMode string

const (
	BrowserModeHeadless BrowserMode = "headless"
	BrowserModeHeaded   BrowserMode = "headed"
)

type LoginMode string

const (
	LoginModeManual  LoginMode = "manual"
	LoginModePromote LoginMode = "promote"
)

type IsolationScope string

const (
	IsolationScopeTask    IsolationScope = "task"
	IsolationScopeProject IsolationScope = "project"
)

type Policy struct {
	ManagementMode     ManagementMode
	DefaultBrowserMode BrowserMode
	LoginMode          LoginMode
	IsolationScope     IsolationScope
}

func DefaultPolicy() Policy {
	return Policy{
		ManagementMode:     ManagementModeLegacy,
		DefaultBrowserMode: BrowserModeHeadless,
		LoginMode:          LoginModeManual,
		IsolationScope:     IsolationScopeTask,
	}
}

func ParseManagementMode(raw string) (ManagementMode, error) {
	switch normalize(raw) {
	case "", string(ManagementModeLegacy):
		return ManagementModeLegacy, nil
	case string(ManagementModeObserve):
		return ManagementModeObserve, nil
	case string(ManagementModeManaged):
		return ManagementModeManaged, nil
	default:
		return "", fmt.Errorf("playwright management mode must be one of: legacy, observe, managed")
	}
}

func ParseBrowserMode(raw string) (BrowserMode, error) {
	switch normalize(raw) {
	case "", string(BrowserModeHeadless):
		return BrowserModeHeadless, nil
	case string(BrowserModeHeaded):
		return BrowserModeHeaded, nil
	default:
		return "", fmt.Errorf("playwright default browser mode must be one of: headless, headed")
	}
}

func ParseLoginMode(raw string) (LoginMode, error) {
	switch normalize(raw) {
	case "", string(LoginModeManual):
		return LoginModeManual, nil
	case string(LoginModePromote):
		return LoginModePromote, nil
	default:
		return "", fmt.Errorf("playwright login mode must be one of: manual, promote")
	}
}

func ParseIsolationScope(raw string) (IsolationScope, error) {
	switch normalize(raw) {
	case "", string(IsolationScopeTask):
		return IsolationScopeTask, nil
	case string(IsolationScopeProject):
		return IsolationScopeProject, nil
	default:
		return "", fmt.Errorf("playwright isolation scope must be one of: task, project")
	}
}

func (p Policy) Normalize() Policy {
	normalized := DefaultPolicy()
	if value, err := ParseManagementMode(string(p.ManagementMode)); err == nil {
		normalized.ManagementMode = value
	}
	if value, err := ParseBrowserMode(string(p.DefaultBrowserMode)); err == nil {
		normalized.DefaultBrowserMode = value
	}
	if value, err := ParseLoginMode(string(p.LoginMode)); err == nil {
		normalized.LoginMode = value
	}
	if value, err := ParseIsolationScope(string(p.IsolationScope)); err == nil {
		normalized.IsolationScope = value
	}
	return normalized
}

func (p Policy) Validate() error {
	if _, err := ParseManagementMode(string(p.ManagementMode)); err != nil {
		return err
	}
	if _, err := ParseBrowserMode(string(p.DefaultBrowserMode)); err != nil {
		return err
	}
	if _, err := ParseLoginMode(string(p.LoginMode)); err != nil {
		return err
	}
	if _, err := ParseIsolationScope(string(p.IsolationScope)); err != nil {
		return err
	}
	return nil
}

func (p Policy) UsesLegacyLaunchBehavior() bool {
	return p.Normalize().ManagementMode == ManagementModeLegacy
}

func (p Policy) Summary() string {
	normalized := p.Normalize()
	if normalized.ManagementMode == ManagementModeLegacy {
		return "Legacy provider-owned Playwright behavior."
	}
	return fmt.Sprintf(
		"%s mode, %s browser default, %s login handling, %s isolation.",
		normalized.ManagementMode,
		normalized.DefaultBrowserMode,
		normalized.LoginMode,
		normalized.IsolationScope,
	)
}

func AppendEnv(base []string, provider string, policy Policy) []string {
	normalized := policy.Normalize()
	out := append([]string(nil), base...)
	out = withOverride(out, "LCR_PLAYWRIGHT_MANAGEMENT_MODE", string(normalized.ManagementMode))
	out = withOverride(out, "LCR_PLAYWRIGHT_DEFAULT_BROWSER_MODE", string(normalized.DefaultBrowserMode))
	out = withOverride(out, "LCR_PLAYWRIGHT_LOGIN_MODE", string(normalized.LoginMode))
	out = withOverride(out, "LCR_PLAYWRIGHT_ISOLATION_SCOPE", string(normalized.IsolationScope))
	if trimmedProvider := strings.TrimSpace(provider); trimmedProvider != "" {
		out = withOverride(out, "LCR_EMBEDDED_PROVIDER", trimmedProvider)
	}
	return out
}

func normalize(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	return trimmed
}

func withOverride(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, key+"="+value)
	return out
}
