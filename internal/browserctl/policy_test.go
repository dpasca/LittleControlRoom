package browserctl

import (
	"strings"
	"testing"
)

func TestDefaultPolicyUsesManagedOnlyWhenNeededBehavior(t *testing.T) {
	policy := DefaultPolicy()
	if policy.UsesLegacyLaunchBehavior() {
		t.Fatalf("DefaultPolicy() should default to managed behavior")
	}
	if policy.ManagementMode != ManagementModeManaged {
		t.Fatalf("DefaultPolicy().ManagementMode = %s, want %s", policy.ManagementMode, ManagementModeManaged)
	}
	if policy.DefaultBrowserMode != BrowserModeHeadless {
		t.Fatalf("DefaultPolicy().DefaultBrowserMode = %s, want %s", policy.DefaultBrowserMode, BrowserModeHeadless)
	}
	if policy.LoginMode != LoginModePromote {
		t.Fatalf("DefaultPolicy().LoginMode = %s, want %s", policy.LoginMode, LoginModePromote)
	}
	if policy.IsolationScope != IsolationScopeTask {
		t.Fatalf("DefaultPolicy().IsolationScope = %s, want %s", policy.IsolationScope, IsolationScopeTask)
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	got := (Policy{}).Normalize()
	want := DefaultPolicy()
	if got != want {
		t.Fatalf("Policy{}.Normalize() = %#v, want %#v", got, want)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	policy := DefaultPolicy()
	policy.ManagementMode = "surprise"
	if err := policy.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid mode error")
	}
}

func TestAppendEnvOverridesExistingValues(t *testing.T) {
	base := []string{
		"LCR_PLAYWRIGHT_MANAGEMENT_MODE=legacy",
		"PATH=/bin",
	}
	got := AppendEnv(base, "codex", Policy{
		ManagementMode:     ManagementModeManaged,
		DefaultBrowserMode: BrowserModeHeaded,
		LoginMode:          LoginModePromote,
		IsolationScope:     IsolationScopeProject,
	})
	text := strings.Join(got, "\n")
	for _, want := range []string{
		"LCR_PLAYWRIGHT_MANAGEMENT_MODE=managed",
		"LCR_PLAYWRIGHT_DEFAULT_BROWSER_MODE=headed",
		"LCR_PLAYWRIGHT_LOGIN_MODE=promote",
		"LCR_PLAYWRIGHT_ISOLATION_SCOPE=project",
		"LCR_EMBEDDED_PROVIDER=codex",
		"PATH=/bin",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("AppendEnv() missing %q in %q", want, text)
		}
	}
	if strings.Count(text, "LCR_PLAYWRIGHT_MANAGEMENT_MODE=") != 1 {
		t.Fatalf("AppendEnv() should only keep one management mode entry, got %q", text)
	}
}
