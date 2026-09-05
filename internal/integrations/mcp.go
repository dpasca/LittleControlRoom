package integrations

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

func (s *scan) mcpKeys() []string {
	switch s.inventory.Target.Provider {
	case "codex":
		return []string{"mcp_servers"}
	case "opencode":
		return []string{"mcp"}
	default:
		return []string{"mcpServers"}
	}
}

func (s *scan) loadMCP() {
	t := s.inventory.Target
	if t.Provider == "lcagent" {
		s.inventory.Warnings = append(s.inventory.Warnings, "LCAgent exposes LCR controls and its managed browser natively; general configurable MCP servers are not supported yet.")
		return
	}
	for _, scope := range s.scopes() {
		path := s.configPath(scope, "mcp")
		doc := s.readDocument(path)
		s.appendMCPEntries(path, scope, objectAt(doc.values, s.mcpKeys()...), false)
	}
	if t.Provider == "claude_code" && t.Scope == "project" {
		path := s.configPath("user", "mcp")
		doc := s.readDocument(path)
		s.appendMCPEntries(path, "project", objectAt(doc.values, "projects", t.ProjectPath, "mcpServers"), true)
	}
}

func (s *scan) appendMCPEntries(path, scope string, servers map[string]any, local bool) {
	t := s.inventory.Target
	for _, name := range sortedKeys(servers) {
		config, ok := servers[name].(map[string]any)
		if !ok {
			continue
		}
		e := Entry{Kind: "mcp", Name: name, Provider: t.Provider, Scope: scope, Source: "native", ConfigPath: path, Enabled: boolAt(config, "enabled", true), State: "configured", Actions: []string{"remove", "check_mcp", "set_enabled"}}
		if local {
			e.Source = "native_local"
		}
		command := textAt(config, "command")
		if args := stringList(config["command"]); len(args) > 0 {
			command = args[0]
		}
		if command != "" {
			e.Detail = "Local server: " + filepath.Base(command)
		} else if raw := textAt(config, "url"); raw != "" {
			u, err := url.Parse(raw)
			if err == nil {
				e.Detail = "Remote server: " + u.Hostname()
			} else {
				e.State = "invalid"
			}
		}
		e.Environment = append(e.Environment, stringList(config["env_vars"])...)
		for _, key := range []string{"env", "environment"} {
			for name := range objectAt(config, key) {
				e.Environment = append(e.Environment, name)
			}
		}
		for _, value := range objectAt(config, "env_http_headers") {
			if ref, ok := value.(string); ok && validEnvName(ref) {
				e.Environment = append(e.Environment, ref)
			}
		}
		for _, value := range objectAt(config, "headers") {
			if value, ok := value.(string); ok {
				if ref, ok := environmentReference(value); ok {
					e.Environment = append(e.Environment, ref)
				}
			}
		}
		sort.Strings(e.Environment)
		if t.Provider == "claude_code" {
			if t.Scope == "user" {
				e.Actions = []string{"remove", "check_mcp"}
				e.Detail += "; Claude server toggles apply per project"
			} else {
				doc := s.readDocument(s.configPath("user", "mcp"))
				for _, disabled := range stringList(objectAt(doc.values, "projects", t.ProjectPath)["disabledMcpServers"]) {
					if disabled == name {
						e.Enabled = false
					}
				}
			}
		}
		if name == "playwright" || name == "lcr_runtime" {
			e.Actions = []string{}
			e.Detail += "; reserved for LCR's managed integration"
		}
		if !e.Enabled {
			e.State = "disabled"
		}
		e.Detail += "; running-session connection has not been verified"
		s.inventory.Entries = append(s.inventory.Entries, e)
	}
}

func (s *scan) addMCP(change Change) (Result, error) {
	mcp := change.MCP
	path := s.configPath(change.Scope, "mcp")
	keys := append(s.mcpKeys(), mcp.Name)
	doc := s.readDocument(path)
	if _, exists := objectAt(doc.values, s.mcpKeys()...)[mcp.Name]; exists {
		return Result{}, fmt.Errorf("MCP server %s already exists at this scope; existing configuration was preserved", mcp.Name)
	}
	config := map[string]any{}
	local := len(mcp.Command) > 0
	switch change.Provider {
	case "codex":
		if local {
			config["command"] = mcp.Command[0]
			config["args"] = mcp.Command[1:]
			if len(mcp.EnvVars) > 0 {
				config["env_vars"] = mcp.EnvVars
			}
		} else {
			config["url"] = mcp.URL
			if len(mcp.HeaderEnv) > 0 {
				config["env_http_headers"] = mcp.HeaderEnv
			}
		}
	case "claude_code":
		if local {
			config["type"] = "stdio"
			config["command"] = mcp.Command[0]
			config["args"] = mcp.Command[1:]
			env := map[string]string{}
			for _, name := range mcp.EnvVars {
				env[name] = "${" + name + "}"
			}
			if len(env) > 0 {
				config["env"] = env
			}
		} else {
			config["type"] = "http"
			config["url"] = mcp.URL
			headers := map[string]string{}
			for header, name := range mcp.HeaderEnv {
				headers[header] = "${" + name + "}"
			}
			if len(headers) > 0 {
				config["headers"] = headers
			}
		}
	case "opencode":
		config["enabled"] = true
		if local {
			config["type"] = "local"
			config["command"] = mcp.Command
			env := map[string]string{}
			for _, name := range mcp.EnvVars {
				env[name] = "{env:" + name + "}"
			}
			if len(env) > 0 {
				config["environment"] = env
			}
		} else {
			config["type"] = "remote"
			config["url"] = mcp.URL
			headers := map[string]string{}
			for header, name := range mcp.HeaderEnv {
				headers[header] = "{env:" + name + "}"
			}
			if len(headers) > 0 {
				config["headers"] = headers
			}
		}
	}
	backup, err := s.edit(path, keys, config, false)
	if err != nil {
		return Result{}, err
	}
	return changedResult("Configured MCP server "+mcp.Name+" for "+change.Provider+" ("+change.Scope+" scope).", path, backup), nil
}

func (s *scan) setEnabled(entry Entry, enabled bool) (Result, error) {
	if entry.Kind == "skill" {
		return s.setSkillEnabled(entry, enabled)
	}
	if entry.Kind == "plugin" {
		return s.setPluginEnabled(entry, enabled)
	}
	path := entry.ConfigPath
	keys := append(s.mcpKeys(), entry.Name, "enabled")
	var value any = enabled
	if entry.Provider == "claude_code" {
		path = s.configPath("user", "mcp")
		keys = []string{"projects", s.inventory.Target.ProjectPath, "disabledMcpServers"}
		doc := s.readDocument(path)
		disabled := stringList(objectAt(doc.values, "projects", s.inventory.Target.ProjectPath)["disabledMcpServers"])
		filtered := []string{}
		for _, name := range disabled {
			if name != entry.Name {
				filtered = append(filtered, name)
			}
		}
		if !enabled {
			filtered = append(filtered, entry.Name)
		}
		value = filtered
	}
	backup, err := s.edit(path, keys, value, false)
	if err != nil {
		return Result{}, err
	}
	return changedResult(fmt.Sprintf("MCP server %s enabled=%t for %s (%s scope).", entry.Name, enabled, entry.Provider, s.inventory.Target.Scope), path, backup), nil
}

func (s *scan) remove(ctx context.Context, entry Entry) (Result, error) {
	if entry.Kind == "skill" {
		return s.removeSkill(entry)
	}
	if entry.Kind == "plugin" {
		return s.removePlugin(ctx, entry)
	}
	keys := append(s.mcpKeys(), entry.Name)
	if entry.Source == "native_local" {
		keys = []string{"projects", s.inventory.Target.ProjectPath, "mcpServers", entry.Name}
	}
	backup, err := s.edit(entry.ConfigPath, keys, nil, true)
	if err != nil {
		return Result{}, err
	}
	return changedResult("Removed MCP configuration for "+entry.Name+"; a configuration backup was retained.", entry.ConfigPath, backup), nil
}

func (s *scan) serverConfig(entry Entry) map[string]any {
	doc := s.readDocument(entry.ConfigPath)
	keys := append(s.mcpKeys(), entry.Name)
	if entry.Source == "native_local" {
		keys = []string{"projects", s.inventory.Target.ProjectPath, "mcpServers", entry.Name}
	}
	return objectAt(doc.values, keys...)
}

func environmentReference(value string) (string, bool) {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
		return name, validEnvName(name)
	}
	if strings.HasPrefix(value, "{env:") && strings.HasSuffix(value, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(value, "{env:"), "}")
		return name, validEnvName(name)
	}
	return "", false
}
