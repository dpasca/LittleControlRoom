package integrations

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s *scan) loadPlugins(ctx context.Context) {
	t := s.inventory.Target
	switch t.Provider {
	case "codex":
		doc := s.readDocument(s.configPath("user", "plugin"))
		configured := objectAt(doc.values, "plugins")
		for _, selector := range sortedKeys(configured) {
			if !validPluginSelector(selector) {
				continue
			}
			settings, _ := configured[selector].(map[string]any)
			enabled := boolAt(settings, "enabled", true)
			state := "configured"
			if !enabled {
				state = "disabled"
			}
			s.inventory.Entries = append(s.inventory.Entries, Entry{Kind: "plugin", Name: selector, Provider: t.Provider, Scope: "user", Source: "native", ConfigPath: doc.path, Enabled: enabled, State: state, Actions: []string{"set_enabled", "remove"}, Detail: "Native plugin configuration; bundled connections may need authentication"})
		}
		cache := filepath.Join(s.roots.CodexHome, "plugins", "cache")
		marketplaces, _ := os.ReadDir(cache)
		for _, market := range marketplaces {
			if ctx.Err() != nil {
				return
			}
			if !market.IsDir() {
				continue
			}
			plugins, _ := os.ReadDir(filepath.Join(cache, market.Name()))
			for _, plugin := range plugins {
				if !plugin.IsDir() {
					continue
				}
				selector := plugin.Name() + "@" + market.Name()
				if !validPluginSelector(selector) {
					continue
				}
				versions, _ := os.ReadDir(filepath.Join(cache, market.Name(), plugin.Name()))
				for _, version := range versions {
					if !version.IsDir() {
						continue
					}
					root := filepath.Join(cache, market.Name(), plugin.Name(), version.Name())
					manifest := s.readDocument(filepath.Join(root, ".codex-plugin", "plugin.json"))
					if len(manifest.raw) == 0 {
						continue
					}
					_, configuredHere := configured[selector]
					if !configuredHere {
						s.inventory.Entries = append(s.inventory.Entries, Entry{Kind: "plugin", Name: selector, Provider: t.Provider, Scope: "user", Source: "cache", Path: root, State: "cached", Detail: "Cached bundle " + version.Name() + "; cache presence does not establish installation or enablement"})
					}
					s.appendBundledSkills(ctx, root, selector, "user")
				}
			}
		}
	case "claude_code":
		doc := s.readDocument(filepath.Join(s.roots.HomeDir, ".claude", "plugins", "installed_plugins.json"))
		for _, selector := range sortedKeys(objectAt(doc.values, "plugins")) {
			if !validPluginSelector(selector) {
				continue
			}
			for _, install := range objectList(objectAt(doc.values, "plugins")[selector]) {
				scope := textAt(install, "scope")
				nativeScope := scope
				if scope != "user" && scope != "project" && scope != "local" {
					continue
				}
				if scope != "user" {
					if t.Scope != "project" || filepath.Clean(textAt(install, "projectPath")) != t.ProjectPath {
						continue
					}
					scope = "project"
				}
				path := s.configPath(scope, "plugin")
				if nativeScope == "project" {
					path = filepath.Join(t.ProjectPath, ".claude", "settings.json")
				}
				enabled := boolAt(objectAt(s.readDocument(path).values, "enabledPlugins"), selector, true)
				state := "installed"
				if !enabled {
					state = "disabled"
				}
				s.inventory.Entries = append(s.inventory.Entries, Entry{Kind: "plugin", Name: selector, Provider: t.Provider, Scope: scope, NativeScope: nativeScope, Source: "native", Path: textAt(install, "installPath"), ConfigPath: path, Enabled: enabled, State: state, Actions: []string{"set_enabled", "remove"}})
				s.appendBundledSkills(ctx, textAt(install, "installPath"), selector, scope)
			}
		}
	case "opencode":
		for _, scope := range s.scopes() {
			path := s.configPath(scope, "plugin")
			doc := s.readDocument(path)
			for _, name := range stringList(doc.values["plugin"]) {
				if strings.Contains(name, "://") {
					name = "plugin URL (details in native configuration)"
				}
				s.inventory.Entries = append(s.inventory.Entries, Entry{Kind: "plugin", Name: name, Provider: t.Provider, Scope: scope, Source: "native", ConfigPath: path, Enabled: true, State: "configured", Detail: "OpenCode JavaScript plugin; manage its package in native OpenCode configuration"})
			}
		}
	}
}

func (s *scan) appendBundledSkills(ctx context.Context, root, plugin, scope string) {
	if root == "" {
		return
	}
	entries, _ := os.ReadDir(filepath.Join(root, "skills"))
	for _, item := range entries {
		if ctx.Err() != nil {
			return
		}
		if strings.HasPrefix(item.Name(), ".") {
			continue
		}
		path := filepath.Join(root, "skills", item.Name(), "SKILL.md")
		meta, raw, err := readSkill(path)
		if err != nil {
			continue
		}
		s.fingerprints = append(s.fingerprints, path+"\x00"+digest(raw))
		s.inventory.Entries = append(s.inventory.Entries, Entry{Kind: "skill", Name: strings.Split(plugin, "@")[0] + ":" + meta.Name, Description: meta.Description, Provider: s.inventory.Target.Provider, Scope: scope, Source: "plugin", Path: path, State: "bundled", Detail: "Provided by " + plugin + "; manage the plugin as a bundle. Availability in the session is unverified."})
	}
}

func validPluginSelector(selector string) bool {
	parts := strings.Split(selector, "@")
	return len(parts) == 2 && validName(parts[0]) && validName(parts[1])
}

func (s *scan) setPluginEnabled(entry Entry, enabled bool) (Result, error) {
	keys := []string{"plugins", entry.Name, "enabled"}
	if entry.Provider == "claude_code" {
		keys = []string{"enabledPlugins", entry.Name}
	}
	backup, err := s.edit(entry.ConfigPath, keys, enabled, false)
	if err != nil {
		return Result{}, err
	}
	return changedResult(fmt.Sprintf("Plugin %s enabled=%t for %s.", entry.Name, enabled, entry.Provider), entry.ConfigPath, backup), nil
}

func (s *scan) installPlugin(ctx context.Context, change Change) (Result, error) {
	scope := change.Scope
	if change.Provider == "claude_code" && scope == "project" {
		scope = "local"
	}
	command, args := pluginCommand(change.Provider, "install", change.Plugin, scope)
	if _, err := s.run(ctx, command, args, s.commandCWD()); err != nil {
		return Result{}, fmt.Errorf("native plugin installation did not complete: %w; inspect the native plugin manager for connection or sign-in requirements", err)
	}
	return Result{Status: "Native plugin installer completed for " + change.Plugin + ". Bundled connectors may still need sign-in; verify availability after reconnecting.", Activation: ActivationNotice, ReconnectRequired: true}, nil
}

func (s *scan) removePlugin(ctx context.Context, entry Entry) (Result, error) {
	scope := entry.Scope
	if entry.NativeScope != "" {
		scope = entry.NativeScope
	}
	command, args := pluginCommand(entry.Provider, "remove", entry.Name, scope)
	if _, err := s.run(ctx, command, args, s.commandCWD()); err != nil {
		return Result{}, fmt.Errorf("native plugin removal did not complete: %w", err)
	}
	return Result{Status: "Removed plugin " + entry.Name + " through its native manager. Reinstall it from its marketplace to restore it; connected service accounts are managed separately.", Activation: ActivationNotice, ReconnectRequired: true}, nil
}

func pluginCommand(provider, action, selector, scope string) (string, []string) {
	if provider == "codex" {
		verb := "add"
		if action == "remove" {
			verb = "remove"
		}
		return "codex", []string{"plugin", verb, selector, "--json"}
	}
	verb := "install"
	if action == "remove" {
		verb = "uninstall"
	}
	args := []string{"plugin", verb, selector, "--scope", scope}
	if action == "remove" {
		args = append(args, "--keep-data")
	}
	return "claude", args
}

func (s *scan) commandCWD() string {
	if s.inventory.Target.ProjectPath != "" {
		return s.inventory.Target.ProjectPath
	}
	return s.roots.HomeDir
}

func (s *scan) run(ctx context.Context, command string, args []string, cwd string) ([]byte, error) {
	env := []string{}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "CODEX_HOME=") && !strings.HasPrefix(item, "GIT_TERMINAL_PROMPT=") {
			env = append(env, item)
		}
	}
	env = append(env, "CODEX_HOME="+s.roots.CodexHome, "GIT_TERMINAL_PROMPT=0")
	if s.manager.opts.Run != nil {
		return s.manager.opts.Run(ctx, command, args, cwd, env)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	configureProbeProcess(cmd)
	defer stopProbeProcess(cmd)
	output := &limitedBuffer{limit: 2 << 20}
	cmd.Stdout = output
	// Native installers may print tokens or connection details. Never route raw
	// output into a transcript or an error receipt.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s command failed (check that the installed CLI supports this operation)", command)
	}
	return output.data, nil
}

type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		b.data = append(b.data, p[:min(remaining, n)]...)
	}
	return n, nil
}
