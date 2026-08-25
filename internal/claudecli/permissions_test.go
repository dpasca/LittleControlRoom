package claudecli

import "testing"

func TestParsePermissionMode(t *testing.T) {
	tests := map[string]PermissionMode{
		"":                  PermissionModeAuto,
		"auto":              PermissionModeAuto,
		"bypassPermissions": PermissionModeBypassPermissions,
		"bypass":            PermissionModeBypassPermissions,
		"yolo":              PermissionModeBypassPermissions,
		"accept-edits":      PermissionModeAcceptEdits,
		"full_auto":         PermissionModeAcceptEdits,
		"manual":            PermissionModeManual,
		"default":           PermissionModeManual,
		"safe":              PermissionModeManual,
		"dont ask":          PermissionModeDontAsk,
		"plan":              PermissionModePlan,
	}

	for raw, want := range tests {
		got, err := ParsePermissionMode(raw)
		if err != nil {
			t.Fatalf("ParsePermissionMode(%q) error = %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParsePermissionMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParsePermissionModeRejectsUnknownValue(t *testing.T) {
	if _, err := ParsePermissionMode("reckless"); err == nil {
		t.Fatal("ParsePermissionMode() should reject an unknown mode")
	}
}

func TestDefaultPermissionModeIsAuto(t *testing.T) {
	if got := DefaultPermissionMode(); got != PermissionModeAuto {
		t.Fatalf("DefaultPermissionMode() = %q, want %q", got, PermissionModeAuto)
	}
}

func TestPermissionModeUsesApprovalBridge(t *testing.T) {
	for _, mode := range []PermissionMode{PermissionModeAuto, PermissionModeAcceptEdits, PermissionModeManual, PermissionModePlan} {
		if !mode.UsesApprovalBridge() {
			t.Fatalf("%q should use the approval bridge", mode)
		}
	}
	for _, mode := range []PermissionMode{PermissionModeBypassPermissions, PermissionModeDontAsk} {
		if mode.UsesApprovalBridge() {
			t.Fatalf("%q should not use the approval bridge", mode)
		}
	}
}
