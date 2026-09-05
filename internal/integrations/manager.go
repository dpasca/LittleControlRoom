package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lcroom/internal/codexstate"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

type Options struct {
	HomeDir            string
	CodexHome          string
	OpenCodeConfigRoot string
	DataDir            string
	// Run is injectable so tests never install software or use a real account.
	Run func(context.Context, string, []string, string, []string) ([]byte, error)
}

type Manager struct{ opts Options }

var mutationMu sync.Mutex

func New(options Options) *Manager { return &Manager{opts: options} }

func (m *Manager) roots() (Options, error) {
	o := m.opts
	if o.HomeDir == "" {
		var err error
		o.HomeDir, err = os.UserHomeDir()
		if err != nil {
			return o, err
		}
	}
	if o.CodexHome == "" && m.opts.HomeDir == "" {
		o.CodexHome = os.Getenv("CODEX_HOME")
	}
	if o.CodexHome == "" {
		o.CodexHome = filepath.Join(o.HomeDir, ".codex")
	}
	o.CodexHome = codexstate.ResolveHomeRoot(o.CodexHome)
	if o.OpenCodeConfigRoot == "" {
		root := ""
		if m.opts.HomeDir == "" {
			root = os.Getenv("XDG_CONFIG_HOME")
		}
		if root == "" {
			root = filepath.Join(o.HomeDir, ".config")
		}
		o.OpenCodeConfigRoot = filepath.Join(root, "opencode")
	}
	if strings.HasPrefix(filepath.Base(filepath.Dir(o.OpenCodeConfigRoot)), "lcroom-opencode-config-") {
		root, err := resolveOpenCodeOverlay(o.OpenCodeConfigRoot)
		if err != nil {
			return o, err
		}
		o.OpenCodeConfigRoot = root
	}
	if o.DataDir == "" {
		o.DataDir = filepath.Join(o.HomeDir, ".little-control-room")
	}
	return o, nil
}

func resolveOpenCodeOverlay(root string) (string, error) {
	if raw, err := readConfigBytes(filepath.Join(root, ".lcr-source-root")); err == nil && len(raw) < 4096 && filepath.IsAbs(strings.TrimSpace(string(raw))) {
		return filepath.Clean(strings.TrimSpace(string(raw))), nil
	}
	// Older overlays predate the marker but mirror native config as symlinks.
	for _, name := range []string{"opencode.jsonc", "opencode.json", "package.json"} {
		path := filepath.Join(root, name)
		if target, err := os.Readlink(path); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			return filepath.Dir(filepath.Clean(target)), nil
		}
	}
	return "", fmt.Errorf("cannot resolve the native OpenCode configuration behind this older LCR overlay; reconnect the engineer")
}

type document struct {
	path   string
	raw    []byte
	values map[string]any
	err    error
}
type scan struct {
	inventory    Inventory
	docs         map[string]*document
	fingerprints []string
	manager      *Manager
	roots        Options
}

func (m *Manager) Inventory(ctx context.Context, target Target) (Inventory, error) {
	s, err := m.scan(ctx, target)
	if err != nil {
		return Inventory{}, err
	}
	return s.inventory, nil
}

func (m *Manager) scan(ctx context.Context, target Target) (*scan, error) {
	target, err := ValidateTarget(target)
	if err != nil {
		return nil, err
	}
	roots, err := m.roots()
	if err != nil {
		return nil, err
	}
	s := &scan{manager: m, roots: roots, docs: map[string]*document{}, inventory: Inventory{Target: target, ScannedAt: time.Now().UTC(), Entries: []Entry{}, Warnings: []string{}, Activation: ActivationNotice}}
	s.fingerprints = append(s.fingerprints, target.Provider, target.Scope, target.ProjectPath)
	if err := s.loadSkills(ctx); err != nil {
		return nil, err
	}
	s.loadMCP()
	s.loadPlugins(ctx)
	s.inventory.Warnings = append(s.inventory.Warnings, "Inventory covers standard user and selected-project sources, not every ancestor, administrator policy, custom config override, or LCR-injected connection. Native precedence and trust rules still apply.")
	for i := range s.inventory.Entries {
		e := &s.inventory.Entries[i]
		e.ID = entryID(*e)
		if e.Actions == nil {
			e.Actions = []string{}
		}
		if len(e.Name) > 256 {
			e.Name = string([]rune(e.Name)[:min(128, len([]rune(e.Name)))]) + "…"
			e.Actions = []string{}
			e.State = "invalid"
			e.Detail = "Oversized native integration name; inspect it in the native configuration."
		}
		if description := []rune(e.Description); len(description) > 1200 {
			e.Description = string(description[:1200]) + "…"
		}
		if e.Scope != target.Scope {
			if target.Provider == "claude_code" && target.Scope == "project" && e.Kind == "mcp" && e.Can("set_enabled") {
				e.Actions = []string{"set_enabled", "check_mcp"}
				e.Detail = strings.TrimSpace(e.Detail + " Inherited from user scope; toggle applies only to this project. Select user scope to remove it.")
			} else {
				e.Actions = []string{}
				e.Detail = strings.TrimSpace(e.Detail + " Inherited from user scope; select user scope to manage it.")
			}
		}
	}
	sort.Slice(s.inventory.Entries, func(i, j int) bool {
		a, b := s.inventory.Entries[i], s.inventory.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	for _, doc := range s.docs {
		raw := doc.raw
		if target.Provider == "claude_code" && doc.path == filepath.Join(roots.HomeDir, ".claude.json") && doc.err == nil {
			// Claude frequently rewrites unrelated usage/session metadata here.
			// Guard the relevant integration settings; edit still compares the full
			// original document immediately before replacing it.
			relevant := map[string]any{"mcpServers": doc.values["mcpServers"]}
			if target.Scope == "project" {
				project := objectAt(doc.values, "projects", target.ProjectPath)
				relevant["projectMCP"] = project["mcpServers"]
				relevant["disabledMcpServers"] = project["disabledMcpServers"]
			}
			raw, _ = json.Marshal(relevant)
		}
		s.fingerprints = append(s.fingerprints, doc.path+"\x00"+digest(raw))
	}
	sort.Strings(s.fingerprints)
	s.inventory.Revision = digest([]byte(strings.Join(s.fingerprints, "\n")))
	return s, ctx.Err()
}

func (s *scan) readDocument(path string) *document {
	if doc := s.docs[path]; doc != nil {
		return doc
	}
	doc := &document{path: path, values: map[string]any{}}
	s.docs[path] = doc
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc
	}
	if err == nil {
		defer file.Close()
		doc.raw, err = readBounded(file, 4<<20)
	}
	if err != nil {
		doc.err = fmt.Errorf("cannot read integration configuration %s", path)
	} else if len(doc.raw) > 0 {
		if strings.HasSuffix(path, ".toml") {
			err = toml.Unmarshal(doc.raw, &doc.values)
		} else {
			var standard []byte
			// hujson standardizes in place. Keep the exact native bytes for
			// revision checks, comment-preserving edits, and recovery copies.
			standard, err = hujson.Standardize(append([]byte(nil), doc.raw...))
			if err == nil {
				err = json.Unmarshal(standard, &doc.values)
			}
		}
		if err != nil || doc.values == nil {
			doc.err = fmt.Errorf("cannot parse integration configuration %s; repair it before editing", path)
		}
	}
	if doc.err != nil {
		s.inventory.Warnings = append(s.inventory.Warnings, doc.err.Error())
	}
	return doc
}

func (s *scan) configPath(scope, kind string) string {
	t := s.inventory.Target
	project := scope == "project"
	switch t.Provider {
	case "codex":
		if project {
			return filepath.Join(t.ProjectPath, ".codex", "config.toml")
		}
		return filepath.Join(s.roots.CodexHome, "config.toml")
	case "claude_code":
		if kind == "mcp" {
			if project {
				return filepath.Join(t.ProjectPath, ".mcp.json")
			}
			return filepath.Join(s.roots.HomeDir, ".claude.json")
		}
		if project {
			return filepath.Join(t.ProjectPath, ".claude", "settings.local.json")
		}
		return filepath.Join(s.roots.HomeDir, ".claude", "settings.json")
	case "opencode":
		root := s.roots.OpenCodeConfigRoot
		if project {
			root = t.ProjectPath
		}
		path := filepath.Join(root, "opencode.jsonc")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return filepath.Join(root, "opencode.json")
	default:
		if project {
			return filepath.Join(t.ProjectPath, ".lcagent", "integrations.json")
		}
		return filepath.Join(s.roots.DataDir, "lcagent", "integrations.json")
	}
}

func (s *scan) scopes() []string {
	if s.inventory.Target.Scope == "project" {
		return []string{"user", "project"}
	}
	return []string{"user"}
}

func objectAt(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		child, _ := values[key].(map[string]any)
		if child == nil {
			return map[string]any{}
		}
		values = child
	}
	return values
}

func textAt(values map[string]any, key string) string { value, _ := values[key].(string); return value }
func boolAt(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key].(bool)
	if !ok {
		return fallback
	}
	return value
}
func stringList(value any) []string {
	if items, ok := value.([]string); ok {
		return items
	}
	items, _ := value.([]any)
	result := []string{}
	for _, item := range items {
		if str, ok := item.(string); ok {
			result = append(result, str)
		}
	}
	return result
}

func (m *Manager) Apply(ctx context.Context, change Change) (Result, error) {
	change, err := ValidateChange(change)
	if err != nil {
		return Result{}, err
	}
	mutationMu.Lock()
	defer mutationMu.Unlock()
	s, err := m.scan(ctx, change.Target)
	if err != nil {
		return Result{}, err
	}
	if change.ExpectedRevision != s.inventory.Revision {
		return Result{}, fmt.Errorf("integration configuration changed; refresh integrations.list and review a new proposal")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	switch change.Action {
	case "install_skill":
		return s.installSkill(ctx, change)
	case "add_mcp":
		return s.addMCP(change)
	case "install_plugin":
		return s.installPlugin(ctx, change)
	}
	var entry Entry
	found := false
	for _, candidate := range s.inventory.Entries {
		if candidate.ID == change.EntryID {
			entry = candidate
			found = true
			break
		}
	}
	if !found {
		return Result{}, fmt.Errorf("integration is no longer present; refresh the inventory")
	}
	if entry.Name != change.EntryName {
		return Result{}, fmt.Errorf("entry_name does not match the inspected integration")
	}
	if !entry.Can(change.Action) {
		return Result{}, fmt.Errorf("%s is unavailable for this integration at %s scope", change.Action, change.Scope)
	}
	if change.Action == "check_mcp" {
		return s.checkMCP(ctx, entry)
	}
	if change.Action == "set_enabled" {
		return s.setEnabled(entry, *change.Enabled)
	}
	return s.remove(ctx, entry)
}

func changedResult(status, path, backup string) Result {
	r := Result{Status: status, ChangedPaths: []string{path}, Activation: ActivationNotice, ReconnectRequired: true}
	if backup != "" {
		r.BackupPaths = []string{backup}
	}
	return r
}
