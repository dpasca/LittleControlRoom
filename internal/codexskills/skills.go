package codexskills

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexstate"
)

type SourceKind string

const (
	SourceUser   SourceKind = "user"
	SourceSystem SourceKind = "system"
	SourcePlugin SourceKind = "plugin"
)

type Inventory struct {
	CodexHome string
	ScannedAt time.Time
	Skills    []Skill
}

type Skill struct {
	Name           string
	InvocationName string
	DirectoryName  string
	Description    string
	Path           string
	Dir            string
	Source         SourceKind
	SourceLabel    string
	PluginName     string
	PluginVersion  string
	ModifiedAt     time.Time
	SizeBytes      int64
	SymlinkTarget  string
	ContentHash    string
	Attention      []string
}

func LoadInventory(ctx context.Context, codexHome string, scannedAt time.Time) (Inventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if scannedAt.IsZero() {
		scannedAt = time.Now()
	}
	home, err := effectiveCodexHome(codexHome)
	if err != nil {
		return Inventory{}, err
	}
	inv := Inventory{CodexHome: home, ScannedAt: scannedAt}
	if err := ctx.Err(); err != nil {
		return inv, err
	}

	skillsRoot := filepath.Join(home, "skills")
	userSkills, err := loadSkillSet(ctx, skillsRoot, SourceUser, "", "")
	if err != nil {
		return inv, err
	}
	inv.Skills = append(inv.Skills, userSkills...)

	systemSkills, err := loadSkillSet(ctx, filepath.Join(skillsRoot, ".system"), SourceSystem, "", "")
	if err != nil {
		return inv, err
	}
	inv.Skills = append(inv.Skills, systemSkills...)

	pluginSkills, err := loadPluginSkills(ctx, filepath.Join(home, "plugins", "cache"), home)
	if err != nil {
		return inv, err
	}
	inv.Skills = append(inv.Skills, pluginSkills...)

	annotateInventory(&inv)
	sort.SliceStable(inv.Skills, func(i, j int) bool {
		left := strings.ToLower(inv.Skills[i].InvocationName)
		right := strings.ToLower(inv.Skills[j].InvocationName)
		if left != right {
			return left < right
		}
		if inv.Skills[i].Source != inv.Skills[j].Source {
			return sourceRank(inv.Skills[i].Source) < sourceRank(inv.Skills[j].Source)
		}
		return inv.Skills[i].Path < inv.Skills[j].Path
	})
	return inv, nil
}

func CountBySource(inv Inventory, source SourceKind) int {
	count := 0
	for _, skill := range inv.Skills {
		if skill.Source == source {
			count++
		}
	}
	return count
}

func AttentionSkills(inv Inventory) []Skill {
	out := make([]Skill, 0)
	for _, skill := range inv.Skills {
		if len(skill.Attention) > 0 {
			out = append(out, skill)
		}
	}
	return out
}

func PrimaryAttention(skill Skill) string {
	if len(skill.Attention) == 0 {
		return ""
	}
	return strings.TrimSpace(skill.Attention[0])
}

func FormatInventoryReport(inv Inventory, limit int) string {
	if limit <= 0 {
		limit = 40
	}
	lines := []string{
		fmt.Sprintf("Codex skills inventory: %d skill(s) in %s.", len(inv.Skills), inv.CodexHome),
		fmt.Sprintf("Sources: %d user, %d system, %d plugin.", CountBySource(inv, SourceUser), CountBySource(inv, SourceSystem), CountBySource(inv, SourcePlugin)),
	}
	attention := AttentionSkills(inv)
	if len(attention) > 0 {
		lines = append(lines, fmt.Sprintf("Review suggested for %d skill(s):", len(attention)))
		for i, skill := range attention {
			if i >= limit {
				lines = append(lines, fmt.Sprintf("- +%d more", len(attention)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("- %s [%s]: %s", skill.InvocationName, skill.SourceLabel, strings.Join(skill.Attention, "; ")))
			lines = append(lines, "  "+skill.Path)
		}
	} else {
		lines = append(lines, "No duplicate or metadata issues found.")
	}

	lines = append(lines, "Installed skills:")
	for i, skill := range inv.Skills {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("- +%d more", len(inv.Skills)-i))
			break
		}
		summary := skill.Description
		if summary == "" {
			summary = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", skill.InvocationName, skill.SourceLabel, clip(summary, 180)))
	}
	return strings.Join(lines, "\n")
}

func loadSkillSet(ctx context.Context, root string, source SourceKind, pluginName, pluginVersion string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills directory %s: %w", root, err)
	}
	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dirName := strings.TrimSpace(entry.Name())
		if dirName == "" {
			continue
		}
		if source == SourceUser && (dirName == ".system" || strings.HasPrefix(dirName, ".")) {
			continue
		}
		if source == SourceSystem && strings.HasPrefix(dirName, ".") {
			continue
		}
		skillPath := filepath.Join(root, dirName, "SKILL.md")
		skill, err := loadSkillFile(skillPath, source, pluginName, pluginVersion, dirName)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func loadPluginSkills(ctx context.Context, cacheRoot, codexHome string) ([]Skill, error) {
	if _, err := os.Stat(cacheRoot); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat plugin cache %s: %w", cacheRoot, err)
	}
	skills := []Skill{}
	err := filepath.WalkDir(cacheRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		rel, err := filepath.Rel(codexHome, path)
		if err != nil {
			return nil
		}
		parts := splitPath(rel)
		if len(parts) < 8 || parts[0] != "plugins" || parts[1] != "cache" || parts[len(parts)-3] != "skills" {
			return nil
		}
		pluginName := parts[3]
		pluginVersion := parts[4]
		dirName := parts[len(parts)-2]
		skill, err := loadSkillFile(path, SourcePlugin, pluginName, pluginVersion, dirName)
		if err != nil {
			return err
		}
		skills = append(skills, skill)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk plugin skills %s: %w", cacheRoot, err)
	}
	return skills, nil
}

func loadSkillFile(path string, source SourceKind, pluginName, pluginVersion, fallbackName string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, err
	}
	name, description := parseSkillFrontMatter(string(data), fallbackName)
	dir := filepath.Dir(path)
	skill := Skill{
		Name:           name,
		InvocationName: invocationName(source, pluginName, name),
		DirectoryName:  fallbackName,
		Description:    description,
		Path:           path,
		Dir:            dir,
		Source:         source,
		SourceLabel:    sourceLabel(source, pluginName),
		PluginName:     pluginName,
		PluginVersion:  pluginVersion,
		ModifiedAt:     info.ModTime(),
		SizeBytes:      info.Size(),
		ContentHash:    contentHash(data),
	}
	if linkInfo, err := os.Lstat(dir); err == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(dir); err == nil {
			skill.SymlinkTarget = target
		}
	}
	if skill.Name == "" {
		skill.Name = fallbackName
		skill.InvocationName = invocationName(source, pluginName, fallbackName)
	}
	if strings.TrimSpace(skill.Description) == "" {
		skill.Attention = append(skill.Attention, "missing frontmatter description")
	}
	if strings.TrimSpace(skill.DirectoryName) != "" && strings.TrimSpace(skill.Name) != "" && skill.Source != SourcePlugin && skill.DirectoryName != skill.Name {
		skill.Attention = append(skill.Attention, "frontmatter name differs from directory name")
	}
	return skill, nil
}

func annotateInventory(inv *Inventory) {
	byInvocation := map[string][]int{}
	for i := range inv.Skills {
		key := strings.ToLower(strings.TrimSpace(inv.Skills[i].InvocationName))
		if key == "" {
			continue
		}
		byInvocation[key] = append(byInvocation[key], i)
	}
	for _, indexes := range byInvocation {
		if len(indexes) <= 1 {
			continue
		}
		var systemIndexes []int
		for _, index := range indexes {
			if inv.Skills[index].Source == SourceSystem {
				systemIndexes = append(systemIndexes, index)
			}
		}
		for _, index := range indexes {
			skill := &inv.Skills[index]
			addAttention(skill, "duplicate invocation name")
			if skill.Source == SourceUser && len(systemIndexes) > 0 {
				addAttention(skill, "possible stale local duplicate of a system skill")
				system := inv.Skills[systemIndexes[0]]
				if !skill.ModifiedAt.IsZero() && !system.ModifiedAt.IsZero() && skill.ModifiedAt.Before(system.ModifiedAt) {
					addAttention(skill, "local file is older than the system skill")
				}
				if skill.ContentHash != "" && system.ContentHash != "" && skill.ContentHash != system.ContentHash {
					addAttention(skill, "content differs from the system skill")
				}
			}
			if skill.Source == SourceSystem {
				for _, other := range indexes {
					if inv.Skills[other].Source == SourceUser {
						addAttention(skill, "duplicated by a local user skill")
						break
					}
				}
			}
		}
	}
}

func addAttention(skill *Skill, text string) {
	text = strings.TrimSpace(text)
	if skill == nil || text == "" {
		return
	}
	for _, existing := range skill.Attention {
		if existing == text {
			return
		}
	}
	skill.Attention = append(skill.Attention, text)
}

func parseSkillFrontMatter(markdown, fallbackName string) (string, string) {
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
		key = strings.TrimSpace(strings.ToLower(key))
		value = trimFrontMatterValue(value)
		if key == "name" || key == "description" {
			values[key] = value
		}
	}
	name := strings.TrimSpace(values["name"])
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	return name, strings.TrimSpace(values["description"])
}

func trimFrontMatterValue(value string) string {
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

func invocationName(source SourceKind, pluginName, name string) string {
	name = strings.TrimSpace(name)
	if source == SourcePlugin {
		pluginName = strings.TrimSpace(pluginName)
		if pluginName != "" && name != "" {
			return pluginName + ":" + name
		}
	}
	return name
}

func sourceLabel(source SourceKind, pluginName string) string {
	switch source {
	case SourceSystem:
		return "system"
	case SourcePlugin:
		pluginName = strings.TrimSpace(pluginName)
		if pluginName == "" {
			return "plugin"
		}
		return "plugin " + pluginName
	default:
		return "user"
	}
}

func sourceRank(source SourceKind) int {
	switch source {
	case SourceUser:
		return 0
	case SourceSystem:
		return 1
	case SourcePlugin:
		return 2
	default:
		return 9
	}
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func splitPath(path string) []string {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		return nil
	}
	return strings.Split(path, string(filepath.Separator))
}

func effectiveCodexHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if envHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); envHome != "" {
			return codexstate.ResolveHomeRoot(envHome), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		path = filepath.Join(home, ".codex")
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return codexstate.ResolveHomeRoot(filepath.Clean(path)), nil
}

func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
