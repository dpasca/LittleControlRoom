package skills

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultPromptSkillLimit = 40
	maxSkillBodyBytes       = 256 * 1024
)

type Source string

const (
	SourceProjectAgents Source = "project"
	SourceProjectCodex  Source = "project_codex"
	SourceCodexUser     Source = "codex"
	SourceCodexSystem   Source = "codex_system"
	SourceGlobalAgents  Source = "agents"
)

type Options struct {
	WorkspaceRoot       string
	CodexHome           string
	AgentsHome          string
	IncludeProjectCodex bool
}

type Catalog struct {
	Skills []Skill
	byName map[string]int
}

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Source      Source `json:"source"`
}

type LoadedSkill struct {
	Skill     Skill
	Body      string
	Truncated bool
}

func DefaultOptions(workspaceRoot string) Options {
	return Options{
		WorkspaceRoot: workspaceRoot,
		CodexHome:     defaultCodexHome(),
		AgentsHome:    defaultAgentsHome(),
	}
}

func Discover(ctx context.Context, opts Options) (Catalog, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	roots := []skillRoot{}
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		roots = append(roots, skillRoot{Dir: filepath.Join(opts.WorkspaceRoot, ".agents", "skills"), Source: SourceProjectAgents})
	}
	if opts.IncludeProjectCodex && strings.TrimSpace(opts.WorkspaceRoot) != "" {
		roots = append(roots,
			skillRoot{Dir: filepath.Join(opts.WorkspaceRoot, ".codex", "skills"), Source: SourceProjectCodex},
			skillRoot{Dir: filepath.Join(opts.WorkspaceRoot, ".codex", "skills", ".system"), Source: SourceProjectCodex},
		)
	}
	if strings.TrimSpace(opts.CodexHome) != "" {
		roots = append(roots,
			skillRoot{Dir: filepath.Join(opts.CodexHome, "skills"), Source: SourceCodexUser},
			skillRoot{Dir: filepath.Join(opts.CodexHome, "skills", ".system"), Source: SourceCodexSystem},
		)
	}
	if strings.TrimSpace(opts.AgentsHome) != "" {
		roots = append(roots, skillRoot{Dir: filepath.Join(opts.AgentsHome, "skills"), Source: SourceGlobalAgents})
	}

	catalog := Catalog{byName: map[string]int{}}
	for _, root := range roots {
		skills, err := loadRoot(ctx, root)
		if err != nil {
			return Catalog{}, err
		}
		for _, skill := range skills {
			key := normalizeName(skill.Name)
			if key == "" {
				continue
			}
			if _, exists := catalog.byName[key]; exists {
				continue
			}
			catalog.byName[key] = len(catalog.Skills)
			catalog.Skills = append(catalog.Skills, skill)
		}
	}
	sort.SliceStable(catalog.Skills, func(i, j int) bool {
		return strings.ToLower(catalog.Skills[i].Name) < strings.ToLower(catalog.Skills[j].Name)
	})
	catalog.reindex()
	return catalog, nil
}

func (c Catalog) PromptIndex(limit int) string {
	if len(c.Skills) == 0 {
		return ""
	}
	if limit <= 0 {
		limit = defaultPromptSkillLimit
	}
	lines := []string{
		"Available skills are listed as metadata only. Call load_skill with a skill name before following a skill body.",
	}
	for i, skill := range c.Skills {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("- +%d more skill(s)", len(c.Skills)-i))
			break
		}
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = "no description"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s]: %s", skill.Name, skill.Source, clip(description, 180)))
	}
	return strings.Join(lines, "\n")
}

func (c Catalog) EventSkills(limit int) []Skill {
	if limit <= 0 || limit > len(c.Skills) {
		limit = len(c.Skills)
	}
	out := make([]Skill, limit)
	copy(out, c.Skills[:limit])
	return out
}

func (c Catalog) Load(name string) (LoadedSkill, error) {
	c.reindex()
	key := normalizeName(name)
	if key == "" {
		return LoadedSkill{}, fmt.Errorf("skill name is required")
	}
	index, ok := c.byName[key]
	if !ok {
		return LoadedSkill{}, fmt.Errorf("skill not found: %s", name)
	}
	skill := c.Skills[index]
	file, err := os.Open(skill.Path)
	if err != nil {
		return LoadedSkill{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSkillBodyBytes+1))
	if err != nil {
		return LoadedSkill{}, err
	}
	truncated := len(data) > maxSkillBodyBytes
	if truncated {
		data = data[:maxSkillBodyBytes]
	}
	if !utf8.Valid(data) {
		return LoadedSkill{}, fmt.Errorf("skill body is not valid utf-8: %s", skill.Name)
	}
	return LoadedSkill{Skill: skill, Body: string(data), Truncated: truncated}, nil
}

func (c *Catalog) reindex() {
	if c == nil {
		return
	}
	c.byName = map[string]int{}
	for i, skill := range c.Skills {
		if key := normalizeName(skill.Name); key != "" {
			c.byName[key] = i
		}
	}
}

type skillRoot struct {
	Dir    string
	Source Source
}

func loadRoot(ctx context.Context, root skillRoot) ([]Skill, error) {
	root.Dir = strings.TrimSpace(root.Dir)
	if root.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills directory %s: %w", root.Dir, err)
	}
	out := []Skill{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		dirName := strings.TrimSpace(entry.Name())
		if dirName == "" || strings.HasPrefix(dirName, ".") {
			continue
		}
		skill, err := loadSkillFile(filepath.Join(root.Dir, dirName, "SKILL.md"), root.Source, dirName)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, skill)
	}
	return out, nil
}

func loadSkillFile(path string, source Source, fallbackName string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	name, description := parseFrontMatter(string(data), fallbackName)
	if strings.TrimSpace(name) == "" {
		name = fallbackName
	}
	return Skill{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Path:        path,
		Source:      source,
	}, nil
}

func parseFrontMatter(markdown, fallbackName string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return strings.TrimSpace(fallbackName), ""
	}
	values := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "name" || key == "description" {
			values[key] = trimValue(value)
		}
	}
	name := strings.TrimSpace(values["name"])
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	return name, strings.TrimSpace(values["description"])
}

func defaultCodexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func defaultAgentsHome() string {
	if value := strings.TrimSpace(os.Getenv("AGENTS_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents")
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func trimValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\'`, "'")
	return strings.TrimSpace(value)
}

func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}
