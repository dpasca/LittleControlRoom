package codexapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/claudecli"
)

func TestClaudePermissionRulesReportAndEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	project, home := t.TempDir(), t.TempDir()
	s := newOutputStyleSession(t, home, project)
	s.permissionMode = claudecli.PermissionModeAuto
	if err := s.EditPermissionRule("allow", "Bash(huggingface-cli upload:*)", false); err != nil {
		t.Fatal(err)
	}
	if err := s.ShowPermissions(); err != nil {
		t.Fatal(err)
	}
	text := s.entries[len(s.entries)-1].Text
	for _, want := range []string{"Current mode: Auto", "Bash(huggingface-cli upload:*)", "/reconnect", filepath.Join(project, ".claude", "settings.local.json")} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	s.busy = true
	if err := s.EditPermissionRule("deny", "Bash", false); err == nil {
		t.Fatal("busy edit should fail")
	}
	rules, err := claudecli.ReadPermissionRules(filepath.Join(project, ".claude", "settings.local.json"))
	if err != nil || len(rules["deny"]) != 0 {
		t.Fatalf("busy changed rules: %#v %v", rules, err)
	}
	s.busy = false
	s.closed = true
	if err := s.EditPermissionRule("deny", "Bash", false); err == nil {
		t.Fatal("closed edit should fail")
	}
}

func TestClaudePermissionsUsesConfiguredUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"permissions":{"deny":["Read(.env)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newOutputStyleSession(t, t.TempDir(), t.TempDir())
	if err := s.ShowPermissions(); err != nil {
		t.Fatal(err)
	}
	if text := s.entries[len(s.entries)-1].Text; !strings.Contains(text, "Read(.env)") {
		t.Fatal(text)
	}
}
