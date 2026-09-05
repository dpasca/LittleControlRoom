package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type skillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func readSkill(path string) (skillMetadata, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return skillMetadata{}, nil, err
	}
	defer file.Close()
	raw, err := readBounded(file, 256<<10)
	if err != nil {
		return skillMetadata{}, nil, err
	}
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return skillMetadata{}, raw, fmt.Errorf("missing skill frontmatter")
	}
	end := bytes.Index(append(normalized[4:len(normalized):len(normalized)], '\n'), []byte("\n---\n"))
	if end < 0 {
		return skillMetadata{}, raw, fmt.Errorf("missing skill frontmatter terminator")
	}
	var metadata skillMetadata
	if err := yaml.Unmarshal(normalized[4:4+end], &metadata); err != nil {
		return skillMetadata{}, raw, fmt.Errorf("invalid skill metadata")
	}
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	if metadata.Name == "" || metadata.Description == "" {
		return metadata, raw, fmt.Errorf("skill needs a name and description")
	}
	return metadata, raw, nil
}

func (s *scan) skillRoot(scope string) string {
	t := s.inventory.Target
	root := s.roots.HomeDir
	if scope == "project" {
		root = t.ProjectPath
	}
	switch t.Provider {
	case "claude_code":
		return filepath.Join(root, ".claude", "skills")
	case "opencode":
		if scope == "user" {
			return filepath.Join(s.roots.OpenCodeConfigRoot, "skills")
		}
		return filepath.Join(root, ".opencode", "skills")
	default:
		return filepath.Join(root, ".agents", "skills")
	}
}

func (s *scan) loadSkills(ctx context.Context) error {
	t := s.inventory.Target
	type root struct {
		path, scope, source string
		shared              bool
	}
	roots := []root{}
	for _, scope := range s.scopes() {
		roots = append(roots, root{s.skillRoot(scope), scope, "native", t.Provider == "codex" || t.Provider == "lcagent"})
		base := s.roots.HomeDir
		if scope == "project" {
			base = t.ProjectPath
		}
		if t.Provider == "opencode" {
			roots = append(roots, root{filepath.Join(base, ".claude", "skills"), scope, "shared", true}, root{filepath.Join(base, ".agents", "skills"), scope, "shared", true})
		}
	}
	if t.Provider == "codex" || t.Provider == "lcagent" {
		roots = append(roots, root{filepath.Join(s.roots.CodexHome, "skills"), "user", "native", true}, root{filepath.Join(s.roots.CodexHome, "skills", ".system"), "user", "system", true})
	}
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root.path] {
			continue
		}
		seen[root.path] = true
		files, err := os.ReadDir(root.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			s.inventory.Warnings = append(s.inventory.Warnings, "Cannot inspect skills directory "+root.path)
			continue
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if strings.HasPrefix(file.Name(), ".") {
				continue
			}
			path := filepath.Join(root.path, file.Name(), "SKILL.md")
			metadata, raw, err := readSkill(path)
			if os.IsNotExist(err) {
				continue
			}
			s.fingerprints = append(s.fingerprints, path+"\x00"+digest(raw))
			name := metadata.Name
			if name == "" {
				name = file.Name()
			}
			entry := Entry{Kind: "skill", Name: name, Description: metadata.Description, Provider: t.Provider, Scope: root.scope, Source: root.source, Path: path, ConfigPath: s.configPath(root.scope, "skill"), Enabled: true, State: "discovered", Shared: root.shared}
			if err != nil {
				entry.State = "invalid"
				entry.Detail = "Invalid or missing skill metadata"
			} else if root.source != "system" {
				entry.Actions = []string{"set_enabled", "remove"}
			}
			entry.Enabled = s.skillEnabled(entry)
			if !entry.Enabled {
				entry.State = "disabled"
			}
			if root.shared {
				entry.Detail = strings.TrimSpace(entry.Detail + " Skill files may also be used by other agents; removal affects every reader of this folder.")
			}
			s.inventory.Entries = append(s.inventory.Entries, entry)
		}
	}
	return nil
}

func objectList(value any) []map[string]any {
	if list, ok := value.([]map[string]any); ok {
		return list
	}
	list, _ := value.([]any)
	out := []map[string]any{}
	for _, v := range list {
		if item, ok := v.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func (s *scan) skillEnabled(entry Entry) bool {
	enabled := true
	for _, scope := range s.scopes() {
		doc := s.readDocument(s.configPath(scope, "skill"))
		switch entry.Provider {
		case "codex":
			for _, setting := range objectList(objectAt(doc.values, "skills")["config"]) {
				if textAt(setting, "path") == entry.Path {
					enabled = boolAt(setting, "enabled", true)
				}
			}
		case "claude_code":
			if mode := textAt(objectAt(doc.values, "skillOverrides"), entry.Name); mode != "" {
				enabled = mode != "off"
			}
		case "opencode":
			if mode := textAt(objectAt(doc.values, "permission", "skill"), entry.Name); mode != "" {
				enabled = mode != "deny"
			}
		case "lcagent":
			enabled = boolAt(objectAt(doc.values, "skills"), entry.Path, enabled)
		}
	}
	return enabled
}

func (s *scan) setSkillEnabled(entry Entry, enabled bool) (Result, error) {
	path := s.configPath(s.inventory.Target.Scope, "skill")
	keys := []string{}
	var value any = enabled
	switch entry.Provider {
	case "codex":
		doc := s.readDocument(path)
		settings := objectList(objectAt(doc.values, "skills")["config"])
		found := false
		for _, setting := range settings {
			if textAt(setting, "path") == entry.Path {
				setting["enabled"] = enabled
				found = true
			}
		}
		if !found {
			settings = append(settings, map[string]any{"path": entry.Path, "enabled": enabled})
		}
		keys = []string{"skills", "config"}
		value = settings
	case "claude_code":
		keys = []string{"skillOverrides", entry.Name}
		value = "off"
		if enabled {
			value = "on"
		}
	case "opencode":
		keys = []string{"permission", "skill", entry.Name}
		value = "deny"
		if enabled {
			value = "allow"
		}
	default:
		keys = []string{"skills", entry.Path}
	}
	backup, err := s.edit(path, keys, value, false)
	if err != nil {
		return Result{}, err
	}
	return changedResult(fmt.Sprintf("Skill %s enabled=%t for %s (%s scope).", entry.Name, enabled, entry.Provider, s.inventory.Target.Scope), path, backup), nil
}

func (s *scan) installSkill(ctx context.Context, change Change) (Result, error) {
	source := change.SourcePath
	commit := ""
	if change.GitURL != "" {
		if err := os.MkdirAll(s.roots.DataDir, 0o700); err != nil {
			return Result{}, err
		}
		temp, err := os.MkdirTemp(s.roots.DataDir, "integration-fetch-*")
		if err != nil {
			return Result{}, err
		}
		defer os.RemoveAll(temp)
		source = filepath.Join(temp, "repository")
		if _, err := s.run(ctx, "git", []string{"-c", "core.hooksPath=/dev/null", "clone", "--no-checkout", "--", change.GitURL, source}, temp); err != nil {
			return Result{}, fmt.Errorf("could not fetch skill repository: %w", err)
		}
		if _, err := s.run(ctx, "git", []string{"-c", "core.hooksPath=/dev/null", "checkout", "--detach", change.GitRef}, source); err != nil {
			return Result{}, fmt.Errorf("could not check out the requested skill revision: %w", err)
		}
		raw, err := s.run(ctx, "git", []string{"rev-parse", "HEAD"}, source)
		if err != nil {
			return Result{}, err
		}
		commit = strings.TrimSpace(string(raw))
	}
	// Canonicalize the root (macOS /var is itself a symlink), but reject
	// symlinks inside the requested subdirectory, including intermediate ones.
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return Result{}, fmt.Errorf("cannot resolve skill source directory")
	}
	for _, part := range strings.Split(filepath.Clean(change.Subdirectory), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		source = filepath.Join(source, part)
		info, err := os.Lstat(source)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("skill subdirectory must be a real directory within the source")
		}
	}
	metadata, _, err := readSkill(filepath.Join(source, "SKILL.md"))
	if err != nil {
		return Result{}, fmt.Errorf("source must contain a valid SKILL.md")
	}
	if !validName(metadata.Name) {
		return Result{}, fmt.Errorf("skill name is not a safe directory name")
	}
	root := s.skillRoot(change.Scope)
	destination := filepath.Join(root, metadata.Name)
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, fmt.Errorf("skill destination already exists; existing files were preserved")
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".lcr-skill-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	if err := copySkill(ctx, source, stage); err != nil {
		return Result{}, err
	}
	provenance, _ := json.MarshalIndent(map[string]string{"source_path": change.SourcePath, "git_url": change.GitURL, "git_commit": commit, "subdirectory": change.Subdirectory}, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, ".lcr-source.json"), provenance, 0o600); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, fmt.Errorf("skill destination appeared during installation; preserved it")
	}
	if err := os.Rename(stage, destination); err != nil {
		return Result{}, err
	}
	return changedResult("Installed skill "+metadata.Name+" for "+change.Provider+" ("+change.Scope+" scope).", destination, ""), nil
}

func copySkill(ctx context.Context, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill source must be a directory, not a symlink")
	}
	var size int64
	count := 0
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		if rel == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains a symlink; provide a self-contained skill directory")
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains a non-regular file")
		}
		count++
		if size+info.Size() > 32<<20 || count > 2000 {
			return fmt.Errorf("skill exceeds the 32 MiB / 2000 file installation limit")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, err := readBounded(file, (32<<20)-size)
		file.Close()
		if err != nil {
			return err
		}
		size += int64(len(raw))
		return os.WriteFile(target, raw, info.Mode().Perm()&0o755)
	})
}

func (s *scan) removeSkill(entry Entry) (Result, error) {
	directory := filepath.Dir(entry.Path)
	backupRoot := filepath.Join(s.roots.DataDir, "integration-backups")
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return Result{}, err
	}
	backup := filepath.Join(backupRoot, entry.ID+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Rename(directory, backup); err != nil {
		return Result{}, fmt.Errorf("could not move skill to recovery storage; original files were preserved: %w", err)
	}
	return changedResult("Removed skill "+entry.Name+"; its directory is recoverable from "+backup+".", directory, backup), nil
}

// SkillVisibility is consumed by LCAgent's native skill catalog. It is separate
// from Codex configuration, so a Codex-specific toggle does not affect LCAgent.
func SkillVisibility(dataDir, projectPath string) (map[string]bool, error) {
	m := New(Options{DataDir: dataDir})
	roots, err := m.roots()
	if err != nil {
		return nil, err
	}
	s := &scan{roots: roots, docs: map[string]*document{}, inventory: Inventory{Target: Target{Provider: "lcagent", Scope: "project", ProjectPath: projectPath}}}
	visibility := map[string]bool{}
	for _, scope := range s.scopes() {
		doc := s.readDocument(s.configPath(scope, "skill"))
		if doc.err != nil {
			return nil, doc.err
		}
		for path, value := range objectAt(doc.values, "skills") {
			if enabled, ok := value.(bool); ok {
				visibility[path] = enabled
			}
		}
	}
	return visibility, nil
}
