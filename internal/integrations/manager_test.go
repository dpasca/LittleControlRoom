package integrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	return New(Options{HomeDir: t.TempDir(), Run: func(context.Context, string, []string, string, []string) ([]byte, error) {
		t.Error("unexpected native installer invocation")
		return nil, nil
	}})
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func inventoryFor(t *testing.T, m *Manager, target Target) Inventory {
	t.Helper()
	inv, err := m.Inventory(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func findEntry(t *testing.T, inv Inventory, kind, name, scope string) Entry {
	t.Helper()
	for _, entry := range inv.Entries {
		if entry.Kind == kind && entry.Name == name && entry.Scope == scope {
			return entry
		}
	}
	t.Fatalf("missing %s %s (%s): %#v", kind, name, scope, inv.Entries)
	return Entry{}
}

func entryChange(inv Inventory, entry Entry, action string) Change {
	return Change{Target: inv.Target, Action: action, ExpectedRevision: inv.Revision, EntryID: entry.ID, EntryName: entry.Name}
}

func TestMCPLifecyclePreservesNativeConfiguration(t *testing.T) {
	for _, provider := range []string{"codex", "claude_code", "opencode"} {
		for _, scope := range []string{"user", "project"} {
			t.Run(provider+"/"+scope, func(t *testing.T) {
				m := testManager(t)
				target := Target{Provider: provider, Scope: scope}
				if scope == "project" {
					target.ProjectPath = t.TempDir()
				}
				s, err := m.scan(t.Context(), target)
				if err != nil {
					t.Fatal(err)
				}
				path := s.configPath(scope, "mcp")
				original := "{\n// keep this comment\n\"otherSetting\": {\"secret\": \"do-not-disclose\"},\n}\n"
				if provider == "codex" {
					original = "# keep this comment\nmodel = 'test-model'\n[otherSetting]\nsecret = 'do-not-disclose'\n"
				}
				writeFixture(t, path, original)
				inv := inventoryFor(t, m, target)
				change := Change{Target: target, Action: "add_mcp", ExpectedRevision: inv.Revision, MCP: &MCPConfig{Name: "example", Command: []string{"example-server", "--private-argument=do-not-disclose"}, EnvVars: []string{"EXAMPLE_TOKEN"}}}
				result, err := m.Apply(t.Context(), change)
				if err != nil {
					t.Fatal(err)
				}
				if !result.ReconnectRequired || len(result.BackupPaths) != 1 || readFixture(t, result.BackupPaths[0]) != original {
					t.Fatalf("missing exact recovery/activation: %#v", result)
				}
				updated := readFixture(t, path)
				if !strings.Contains(updated, "keep this comment") || !strings.Contains(updated, "do-not-disclose") {
					t.Fatalf("unrelated configuration lost: %s", updated)
				}
				inv = inventoryFor(t, m, target)
				raw, _ := json.Marshal(inv)
				if strings.Contains(string(raw), "do-not-disclose") {
					t.Fatalf("inventory leaked configuration secret: %s", raw)
				}
				entry := findEntry(t, inv, "mcp", "example", scope)
				if !entry.Enabled || entry.State != "configured" {
					t.Fatalf("unexpected entry: %#v", entry)
				}
				if _, err := m.Apply(t.Context(), change); err == nil {
					t.Fatal("stale revision accepted")
				}
				if entry.Can("set_enabled") {
					toggle := entryChange(inv, entry, "set_enabled")
					disabled := false
					toggle.Enabled = &disabled
					if _, err := m.Apply(t.Context(), toggle); err != nil {
						t.Fatal(err)
					}
					inv = inventoryFor(t, m, target)
					entry = findEntry(t, inv, "mcp", "example", scope)
					if entry.Enabled || entry.State != "disabled" {
						t.Fatalf("toggle not reflected: %#v", entry)
					}
				}
				if _, err := m.Apply(t.Context(), entryChange(inv, entry, "remove")); err != nil {
					t.Fatal(err)
				}
				for _, entry := range inventoryFor(t, m, target).Entries {
					if entry.Kind == "mcp" && entry.Name == "example" {
						t.Fatal("MCP entry remained after removal")
					}
				}
			})
		}
	}
}

func TestConfigurationEditsPreserveOtherGroups(t *testing.T) {
	for _, raw := range []string{
		"model = 'test'\n[mcp_servers.old]\ncommand = 'old'\n[profiles.fast]\nmodel = 'other'\n",
		"mcp_servers.old.command = 'old'\nmodel = 'test'\n",
		"mcp_servers = { old = {command = 'old'} }\nmodel = 'test'\n",
		"[[skills.config]]\npath = '/tmp/SKILL.md'\nenabled = false\n[mcp_servers.old]\ncommand = '''a\nlong\ncommand'''\n[other]\nvalue = 3\n",
	} {
		updated, err := patchTOML([]byte(raw), []string{"mcp_servers", "new"}, map[string]any{"command": "new"}, false)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		before, after := map[string]any{}, map[string]any{}
		if err := toml.Unmarshal([]byte(raw), &before); err != nil {
			t.Fatal(err)
		}
		if err := toml.Unmarshal(updated, &after); err != nil {
			t.Fatal(err)
		}
		delete(objectAt(after, "mcp_servers"), "new")
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("unrelated TOML changed: %s", updated)
		}
	}
	raw := []byte("{ // retain\n\"permission\": {\"bash\": \"ask\"}, \"other\": true, }\n")
	updated, err := patchJSON(raw, []string{"permission", "skill", "skill/name~1"}, "deny", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "// retain") {
		t.Fatalf("JSONC comment lost: %s", updated)
	}
	standard, err := hujson.Standardize(updated)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err := json.Unmarshal(standard, &value); err != nil {
		t.Fatal(err)
	}
	if textAt(objectAt(value, "permission", "skill"), "skill/name~1") != "deny" || textAt(objectAt(value, "permission"), "bash") != "ask" {
		t.Fatalf("bad JSON pointer edit: %s", updated)
	}
}

func TestReplaceFilePreservesSymlinkAndRejectsChangedSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	link := filepath.Join(dir, "overlay.json")
	writeFixture(t, path, "original")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	backup, err := replaceFile(link, []byte("original"), []byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("configuration symlink replaced")
	}
	if readFixture(t, path) != "updated" || readFixture(t, backup) != "original" {
		t.Fatal("wrong update or backup")
	}
	if _, err := replaceFile(link, []byte("original"), []byte("clobber")); err == nil {
		t.Fatal("concurrent edit was overwritten")
	}
	if readFixture(t, path) != "updated" {
		t.Fatal("original changed after conflict")
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("backup permissions are not private")
	}
}

func TestClaudeRevisionIgnoresUsageMetadataButGuardsMCP(t *testing.T) {
	m := testManager(t)
	path := filepath.Join(m.opts.HomeDir, ".claude.json")
	target := Target{Provider: "claude_code", Scope: "project", ProjectPath: t.TempDir()}
	writeFixture(t, path, `{"numStartups":1,"mcpServers":{"example":{"command":"example"}}}`)
	first := inventoryFor(t, m, target)
	writeFixture(t, path, `{"numStartups":2,"mcpServers":{"example":{"command":"example"}}}`)
	second := inventoryFor(t, m, target)
	if first.Revision != second.Revision {
		t.Fatal("unrelated Claude usage metadata invalidated the revision")
	}
	entry := findEntry(t, second, "mcp", "example", "user")
	if !entry.Can("set_enabled") || entry.Can("remove") {
		t.Fatalf("wrong inherited actions: %#v", entry)
	}
	change := entryChange(second, entry, "set_enabled")
	disabled := false
	change.Enabled = &disabled
	if _, err := m.Apply(t.Context(), change); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, path), `"numStartups": 2`) {
		t.Fatal("new usage metadata lost")
	}
	third := inventoryFor(t, m, target)
	if third.Revision == second.Revision || findEntry(t, third, "mcp", "example", "user").Enabled {
		t.Fatal("project toggle missing from revision or state")
	}
	if !findEntry(t, inventoryFor(t, m, Target{Provider: "claude_code", Scope: "user"}), "mcp", "example", "user").Enabled {
		t.Fatal("project toggle disabled the server globally")
	}
}

func TestSkillLifecycleAcrossProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude_code", "opencode", "lcagent"} {
		t.Run(provider, func(t *testing.T) {
			m := testManager(t)
			source := t.TempDir()
			writeFixture(t, filepath.Join(source, "SKILL.md"), "---\nname: example\ndescription: >-\n  A multiline\n  description.\n---\nInstructions.\n")
			writeFixture(t, filepath.Join(source, "scripts", "helper.sh"), "not executed during install")
			target := Target{Provider: provider, Scope: "project", ProjectPath: t.TempDir()}
			inv := inventoryFor(t, m, target)
			install := Change{Target: target, Action: "install_skill", ExpectedRevision: inv.Revision, SourcePath: source}
			if _, err := m.Apply(t.Context(), install); err != nil {
				t.Fatal(err)
			}
			inv = inventoryFor(t, m, target)
			entry := findEntry(t, inv, "skill", "example", "project")
			if entry.Description != "A multiline description." {
				t.Fatalf("YAML description: %q", entry.Description)
			}
			install.ExpectedRevision = inv.Revision
			if _, err := m.Apply(t.Context(), install); err == nil {
				t.Fatal("existing skill overwritten")
			}
			disabled := false
			change := entryChange(inv, entry, "set_enabled")
			change.Enabled = &disabled
			if _, err := m.Apply(t.Context(), change); err != nil {
				t.Fatal(err)
			}
			inv = inventoryFor(t, m, target)
			entry = findEntry(t, inv, "skill", "example", "project")
			if entry.Enabled {
				t.Fatal("skill visibility not updated")
			}
			result, err := m.Apply(t.Context(), entryChange(inv, entry, "remove"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(entry.Path); !os.IsNotExist(err) {
				t.Fatal("skill not removed from native root")
			}
			if len(result.BackupPaths) != 1 || readFixture(t, filepath.Join(result.BackupPaths[0], "scripts", "helper.sh")) != "not executed during install" {
				t.Fatal("skill not recoverable")
			}
		})
	}
}

func TestSkillRejectsEscapingSubdirectoryAndSymlinks(t *testing.T) {
	m := testManager(t)
	source, outside := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(outside, "nested", "SKILL.md"), "---\nname: example\ndescription: example\n---\n")
	if err := os.Symlink(outside, filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	target := Target{Provider: "codex", Scope: "user"}
	inv := inventoryFor(t, m, target)
	change := Change{Target: target, Action: "install_skill", ExpectedRevision: inv.Revision, SourcePath: source, Subdirectory: "escape/nested"}
	if _, err := m.Apply(t.Context(), change); err == nil {
		t.Fatal("escaping symlink accepted")
	}
	writeFixture(t, filepath.Join(source, "SKILL.md"), "---\nname: example\ndescription: example\n---\n")
	change.Subdirectory = ""
	if _, err := m.Apply(t.Context(), change); err == nil {
		t.Fatal("symlink packaged into skill")
	}
	bad := filepath.Join(t.TempDir(), "SKILL.md")
	writeFixture(t, bad, "---\nname: example\ndescription: example\n---invalid\n")
	if _, _, err := readSkill(bad); err == nil {
		t.Fatal("invalid frontmatter delimiter accepted")
	}
}

func TestNativePluginCommandsAndCatalog(t *testing.T) {
	m := testManager(t)
	m.opts.Run = func(_ context.Context, command string, args []string, cwd string, env []string) ([]byte, error) {
		if command != "codex" || cwd != m.opts.HomeDir || !containsString(env, "CODEX_HOME="+filepath.Join(m.opts.HomeDir, ".codex")) {
			t.Fatalf("wrong native launch: %s %v %s", command, args, cwd)
		}
		if reflect.DeepEqual(args, []string{"plugin", "list", "--available", "--json"}) {
			return []byte(`{"installed":[{"name":"example","marketplaceName":"curated","installed":true,"enabled":true}],"available":[{"name":"example","marketplaceName":"curated"},{"name":"another","marketplaceName":"curated"}]}`), nil
		}
		if !reflect.DeepEqual(args, []string{"plugin", "add", "example@curated", "--json"}) {
			t.Fatalf("unexpected plugin args: %v", args)
		}
		return []byte(`{"secret":"must never enter a receipt"}`), nil
	}
	items, truncated, err := m.Catalog(t.Context(), "codex", "plugin", "example", 20)
	if err != nil || truncated || len(items) != 1 || !items[0].Installed || items[0].Plugin != "example@curated" {
		t.Fatalf("catalog: %#v %v", items, err)
	}
	target := Target{Provider: "codex", Scope: "user"}
	inv := inventoryFor(t, m, target)
	result, err := m.Apply(t.Context(), Change{Target: target, Action: "install_plugin", ExpectedRevision: inv.Revision, Plugin: "example@curated"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("installer output leaked")
	}
	command, args := pluginCommand("claude_code", "remove", "example@curated", "project")
	if command != "claude" || !reflect.DeepEqual(args, []string{"plugin", "uninstall", "example@curated", "--scope", "project", "--keep-data"}) {
		t.Fatalf("Claude removal: %s %v", command, args)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestValidateChangeRejectsAmbiguityAndReservedTargets(t *testing.T) {
	base := Change{Target: Target{Provider: "codex", Scope: "user"}, Action: "add_mcp", ExpectedRevision: strings.Repeat("a", 64), MCP: &MCPConfig{Name: "example", Command: []string{"example"}}}
	if _, err := ValidateChange(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Change){
		"managed":              func(c *Change) { c.MCP.Name = "lcr_runtime" },
		"both transports":      func(c *Change) { c.MCP.URL = "https://example.com" },
		"mixed operations":     func(c *Change) { c.Plugin = "plugin@catalog" },
		"irrelevant headers":   func(c *Change) { c.MCP.HeaderEnv = map[string]string{"Authorization": "TOKEN"} },
		"literal secret":       func(c *Change) { c.MCP.EnvVars = []string{"TOKEN=secret"} },
		"unsupported provider": func(c *Change) { c.Provider = "lcagent" },
		"project required":     func(c *Change) { c.Scope = "project" },
		"mixed scope":          func(c *Change) { c.ProjectPath = "/a/project" },
		"secret URL":           func(c *Change) { c.MCP.Command = nil; c.MCP.URL = "https://secret@example.com/mcp" },
		"URL query":            func(c *Change) { c.MCP.Command = nil; c.MCP.URL = "https://example.com/mcp?token=secret" },
	} {
		t.Run(name, func(t *testing.T) {
			change := base
			mcp := *base.MCP
			change.MCP = &mcp
			mutate(&change)
			if _, err := ValidateChange(change); err == nil {
				t.Fatal("invalid change accepted")
			}
		})
	}
}

func TestNativeRootsResolveLaunchOverlays(t *testing.T) {
	m := testManager(t)
	nativeCodex := filepath.Join(m.opts.HomeDir, ".codex")
	writeFixture(t, filepath.Join(nativeCodex, "config.toml"), "model = 'example'\n")
	overlay := t.TempDir()
	if err := os.Symlink(filepath.Join(nativeCodex, "config.toml"), filepath.Join(overlay, "config.toml")); err != nil {
		t.Fatal(err)
	}
	m.opts.CodexHome = overlay
	opencodeOverlay := filepath.Join(t.TempDir(), "lcroom-opencode-config-test", "opencode")
	nativeOpenCode := filepath.Join(m.opts.HomeDir, ".config", "opencode")
	writeFixture(t, filepath.Join(opencodeOverlay, ".lcr-source-root"), nativeOpenCode)
	m.opts.OpenCodeConfigRoot = opencodeOverlay
	roots, err := m.roots()
	if err != nil {
		t.Fatal(err)
	}
	if roots.CodexHome != nativeCodex || roots.OpenCodeConfigRoot != nativeOpenCode {
		t.Fatalf("overlay mistaken for native configuration: %#v", roots)
	}
	if err := os.Remove(filepath.Join(opencodeOverlay, ".lcr-source-root")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.roots(); err == nil {
		t.Fatal("unresolvable legacy overlay accepted")
	}
	if err := os.Symlink(filepath.Join(nativeOpenCode, "opencode.json"), filepath.Join(opencodeOverlay, "opencode.json")); err != nil {
		t.Fatal(err)
	}
	roots, err = m.roots()
	if err != nil || roots.OpenCodeConfigRoot != nativeOpenCode {
		t.Fatalf("legacy overlay: %#v %v", roots, err)
	}
}

func TestPluginCacheSkipsNonDirectoryVersionMarkers(t *testing.T) {
	m := testManager(t)
	root := filepath.Join(m.opts.HomeDir, ".codex", "plugins", "cache", "curated", "example")
	writeFixture(t, filepath.Join(root, "last-updated"), "metadata")
	writeFixture(t, filepath.Join(root, "1.0", ".codex-plugin", "plugin.json"), `{"name":"example"}`)
	inv := inventoryFor(t, m, Target{Provider: "codex", Scope: "user"})
	entry := findEntry(t, inv, "plugin", "example@curated", "user")
	if entry.State != "cached" || len(entry.Actions) != 0 {
		t.Fatalf("cache implies installed state: %#v", entry)
	}
	for _, warning := range inv.Warnings {
		if strings.Contains(warning, "cannot read") {
			t.Fatalf("cache marker generated a spurious error: %s", warning)
		}
	}
}
