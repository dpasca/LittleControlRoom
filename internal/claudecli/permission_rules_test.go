package claudecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditPermissionRulesPreservesSettingsAndExactRules(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".claude")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.local.json")
	original := `{
 // Keep this explanation.
 "hooks": {"PreToolUse": []},
 "permissions": {"deny": ["Read(.env)"], "additionalDirectories": ["../shared"]},
 "custom": 9007199254740993
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := "Bash(huggingface-cli upload:*)"
	for i := 0; i < 2; i++ {
		if err := EditPermissionRule(project, "allow", rule, false); err != nil {
			t.Fatal(err)
		}
	}
	rules, err := ReadPermissionRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules["allow"]) != 1 || rules["allow"][0] != rule || len(rules["deny"]) != 1 {
		t.Fatalf("rules = %#v", rules)
	}
	raw, _ := os.ReadFile(path)
	for _, preserved := range []string{"Keep this explanation", "PreToolUse", "../shared", "9007199254740993"} {
		if !strings.Contains(string(raw), preserved) {
			t.Fatalf("lost %q: %s", preserved, raw)
		}
	}
	if err := EditPermissionRule(project, "allow", rule, true); err != nil {
		t.Fatal(err)
	}
	rules, err = ReadPermissionRules(path)
	if err != nil || len(rules["allow"]) != 0 {
		t.Fatalf("remove: %#v, %v", rules, err)
	}
	if err := EditPermissionRule(project, "allow", rule, true); err == nil {
		t.Fatal("missing removal should fail")
	}
}

func TestEditPermissionRulesRejectsInvalidSettingsWithoutClobbering(t *testing.T) {
	for _, original := range []string{`{broken`, `null`, `{"permissions":null}`, `{"permissions":{"allow":"Bash"}}`, `{"permissions":{"deny":[42]}}`, `{"permissions":{"allow":[null]}}`} {
		t.Run(original, func(t *testing.T) {
			project := t.TempDir()
			dir := filepath.Join(project, ".claude")
			os.Mkdir(dir, 0o700)
			path := filepath.Join(dir, "settings.local.json")
			os.WriteFile(path, []byte(original), 0o600)
			if err := EditPermissionRule(project, "allow", "Bash(go test:*)", false); err == nil {
				t.Fatal("expected error")
			}
			raw, _ := os.ReadFile(path)
			if string(raw) != original {
				t.Fatal("settings overwritten")
			}
		})
	}
}

func TestEditPermissionRulesCreatesLocalFileAndRejectsSymlinks(t *testing.T) {
	project := t.TempDir()
	if err := EditPermissionRule(project, "ask", "Bash(git push:*)", false); err != nil {
		t.Fatal(err)
	}
	rules, err := ReadPermissionRules(filepath.Join(project, ".claude", "settings.local.json"))
	if err != nil || len(rules["ask"]) != 1 {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}
	other := t.TempDir()
	if err := os.Symlink(filepath.Join(project, ".claude"), filepath.Join(other, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := EditPermissionRule(other, "allow", "Bash", false); err == nil {
		t.Fatal("symlink should be refused")
	}
}

func TestValidatePermissionRule(t *testing.T) {
	for _, rule := range []string{"Bash", "Bash(huggingface-cli upload:*)", "Bash(echo $(pwd))", "mcp__server__*", "Read(./.env)"} {
		if err := ValidatePermissionRule(rule); err != nil {
			t.Fatalf("%q: %v", rule, err)
		}
	}
	for _, rule := range []string{"", "bash command", "Bash(", "Bash()", "Bash)"} {
		if err := ValidatePermissionRule(rule); err == nil {
			t.Fatalf("accepted %q", rule)
		}
	}
}
