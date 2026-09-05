// Package integrations manages agent-native skills, MCP configuration and plugin
// installations. All I/O belongs on workers; the TUI consumes Inventory snapshots.
package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Target struct {
	Provider    string `json:"provider"`
	Scope       string `json:"scope"`
	ProjectPath string `json:"project_path,omitempty"`
}

type Entry struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Provider    string   `json:"provider"`
	Scope       string   `json:"scope"`
	Source      string   `json:"source"`
	NativeScope string   `json:"native_scope,omitempty"`
	Path        string   `json:"path,omitempty"`
	ConfigPath  string   `json:"config_path,omitempty"`
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state"`
	Detail      string   `json:"detail,omitempty"`
	Actions     []string `json:"actions"`
	Environment []string `json:"environment_names,omitempty"`
	Shared      bool     `json:"shared,omitempty"`
}

type Inventory struct {
	Target     Target    `json:"target"`
	Revision   string    `json:"revision"`
	ScannedAt  time.Time `json:"scanned_at"`
	Entries    []Entry   `json:"entries"`
	Warnings   []string  `json:"warnings"`
	Activation string    `json:"activation"`
}

type MCPConfig struct {
	Name      string            `json:"name"`
	Command   []string          `json:"command,omitempty"`
	URL       string            `json:"url,omitempty"`
	EnvVars   []string          `json:"env_vars,omitempty"`
	HeaderEnv map[string]string `json:"header_env,omitempty"`
}

// Change is deliberately typed rather than accepting arbitrary native config.
// Environment/header values are references to existing environment variables.
type Change struct {
	Target
	Action           string     `json:"action"`
	ExpectedRevision string     `json:"expected_revision"`
	EntryID          string     `json:"entry_id,omitempty"`
	EntryName        string     `json:"entry_name,omitempty"`
	Enabled          *bool      `json:"enabled,omitempty"`
	SourcePath       string     `json:"source_path,omitempty"`
	GitURL           string     `json:"git_url,omitempty"`
	GitRef           string     `json:"git_ref,omitempty"`
	Subdirectory     string     `json:"subdirectory,omitempty"`
	Plugin           string     `json:"plugin,omitempty"`
	MCP              *MCPConfig `json:"mcp,omitempty"`
}

type Result struct {
	Status            string   `json:"status"`
	ChangedPaths      []string `json:"changed_paths,omitempty"`
	BackupPaths       []string `json:"backup_paths,omitempty"`
	Activation        string   `json:"activation"`
	ReconnectRequired bool     `json:"reconnect_required"`
	Check             *Check   `json:"check,omitempty"`
}

type Check struct {
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	Tools     []string  `json:"tools,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type Reader interface {
	Inventory(context.Context, Target) (Inventory, error)
}

const ActivationNotice = "Configuration on disk is not proof of availability in a running session. Reconnect the affected LCR engineer when idle to apply changes; native agent policy and authentication still apply."

func ValidateTarget(target Target) (Target, error) {
	switch target.Provider {
	case "codex", "claude_code", "opencode", "lcagent":
	default:
		return Target{}, fmt.Errorf("provider must be codex, claude_code, opencode, or lcagent")
	}
	switch target.Scope {
	case "user":
		if target.ProjectPath != "" {
			return Target{}, fmt.Errorf("user scope must omit project_path")
		}
	case "project":
		if !filepath.IsAbs(target.ProjectPath) || filepath.Clean(target.ProjectPath) == string(filepath.Separator) {
			return Target{}, fmt.Errorf("project scope requires an absolute project_path")
		}
		target.ProjectPath = filepath.Clean(target.ProjectPath)
	default:
		return Target{}, fmt.Errorf("scope must be user or project")
	}
	return target, nil
}

func ValidateChange(change Change) (Change, error) {
	target, err := ValidateTarget(change.Target)
	if err != nil {
		return Change{}, err
	}
	change.Target = target
	if len(change.ExpectedRevision) != 64 {
		return Change{}, fmt.Errorf("expected_revision must come from integrations.list")
	}
	if _, err := hex.DecodeString(change.ExpectedRevision); err != nil {
		return Change{}, fmt.Errorf("invalid expected_revision")
	}
	// Reject mixed operations instead of silently ignoring fields in a proposal.
	entryAction := change.Action == "set_enabled" || change.Action == "remove" || change.Action == "check_mcp"
	if !entryAction && (change.EntryID != "" || change.EntryName != "") || change.Action != "set_enabled" && change.Enabled != nil || change.Action != "add_mcp" && change.MCP != nil || change.Action != "install_plugin" && change.Plugin != "" || change.Action != "install_skill" && (change.SourcePath != "" || change.GitURL != "" || change.GitRef != "" || change.Subdirectory != "") {
		return Change{}, fmt.Errorf("proposal contains fields for a different integration action")
	}
	switch change.Action {
	case "install_skill":
		if (change.SourcePath == "") == (change.GitURL == "") {
			return Change{}, fmt.Errorf("provide exactly one source_path or git_url")
		}
		if change.SourcePath != "" && !filepath.IsAbs(change.SourcePath) {
			return Change{}, fmt.Errorf("source_path must be absolute")
		}
		if change.SourcePath != "" && change.GitRef != "" {
			return Change{}, fmt.Errorf("git_ref requires git_url")
		}
		if change.GitURL != "" {
			if err := validateURL(change.GitURL, true); err != nil {
				return Change{}, err
			}
			if change.GitRef == "" || strings.HasPrefix(change.GitRef, "-") || strings.ContainsAny(change.GitRef, "\r\n\x00") {
				return Change{}, fmt.Errorf("git_ref is required and must name a revision")
			}
		}
		if change.Subdirectory != "" && (filepath.IsAbs(change.Subdirectory) || change.Subdirectory == ".." || strings.HasPrefix(filepath.Clean(change.Subdirectory), ".."+string(filepath.Separator))) {
			return Change{}, fmt.Errorf("subdirectory must stay within the source")
		}
	case "add_mcp":
		if change.MCP == nil {
			return Change{}, fmt.Errorf("mcp is required")
		}
		if change.Provider == "lcagent" {
			return Change{}, fmt.Errorf("general MCP connections are not supported by LCAgent yet; choose Codex, Claude Code, or OpenCode")
		}
		if !validName(change.MCP.Name) || change.MCP.Name == "playwright" || change.MCP.Name == "lcr_runtime" {
			return Change{}, fmt.Errorf("choose a server name other than the LCR-managed playwright and lcr_runtime names")
		}
		if (len(change.MCP.Command) == 0) == (change.MCP.URL == "") {
			return Change{}, fmt.Errorf("provide exactly one MCP command array or URL")
		}
		if change.MCP.URL != "" {
			if err := validateURL(change.MCP.URL, false); err != nil {
				return Change{}, err
			}
		}
		for _, arg := range change.MCP.Command {
			if strings.ContainsRune(arg, 0) {
				return Change{}, fmt.Errorf("MCP arguments must not contain NUL")
			}
		}
		if len(change.MCP.Command) > 0 && strings.TrimSpace(change.MCP.Command[0]) == "" {
			return Change{}, fmt.Errorf("MCP executable is required")
		}
		if len(change.MCP.Command) > 64 {
			return Change{}, fmt.Errorf("too many MCP arguments")
		}
		if len(change.MCP.Command) > 0 && len(change.MCP.HeaderEnv) > 0 || change.MCP.URL != "" && len(change.MCP.EnvVars) > 0 {
			return Change{}, fmt.Errorf("env_vars apply to local commands; header_env applies to remote URLs")
		}
		for _, name := range change.MCP.EnvVars {
			if !validEnvName(name) {
				return Change{}, fmt.Errorf("invalid environment variable name")
			}
		}
		for header, name := range change.MCP.HeaderEnv {
			if !validHeaderName(header) || !validEnvName(name) {
				return Change{}, fmt.Errorf("invalid header/environment reference")
			}
		}
	case "set_enabled":
		if change.EntryID == "" || change.EntryName == "" || change.Enabled == nil {
			return Change{}, fmt.Errorf("entry_id, entry_name, and enabled are required")
		}
	case "remove", "check_mcp":
		if change.EntryID == "" || change.EntryName == "" {
			return Change{}, fmt.Errorf("entry_id and entry_name are required")
		}
	case "install_plugin":
		if change.Provider != "codex" && change.Provider != "claude_code" {
			return Change{}, fmt.Errorf("plugin installation is supported for Codex and Claude Code")
		}
		if change.Provider == "codex" && change.Scope != "user" {
			return Change{}, fmt.Errorf("Codex plugin installation is user-scoped")
		}
		parts := strings.Split(change.Plugin, "@")
		if len(parts) != 2 || !validName(parts[0]) || !validName(parts[1]) {
			return Change{}, fmt.Errorf("plugin must be an exact name@marketplace selector")
		}
	default:
		return Change{}, fmt.Errorf("unsupported integration action")
	}
	return change, nil
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > 128 || name[0] == '.' || name[0] == '-' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func validEnvName(name string) bool {
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	switch strings.ToLower(name) {
	case "host", "content-length", "content-type", "accept", "connection", "transfer-encoding", "mcp-session-id", "mcp-protocol-version":
		return false
	}
	return true
}

func validateURL(raw string, git bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return fmt.Errorf("URL must have a host and contain no credentials, query, or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && !git && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return fmt.Errorf("use HTTPS, or loopback HTTP for a local MCP server")
	}
	return nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func entryID(e Entry) string {
	return digest([]byte(strings.Join([]string{e.Provider, e.Scope, e.NativeScope, e.Kind, e.ConfigPath, e.Path, e.Name}, "\x00")))[:24]
}

func (e Entry) Can(action string) bool {
	for _, candidate := range e.Actions {
		if candidate == action {
			return true
		}
	}
	return false
}
