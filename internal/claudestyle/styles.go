// Package claudestyle discovers the Claude Code output styles available on
// disk so Little Control Room can display and select them without shelling out
// to the Claude CLI.
//
// Claude Code resolves an output style by the `name` field in a style file's
// frontmatter, not by the file name, and the comparison is case-sensitive.
// Verified against Claude Code 2.1.278: a file `zzprobe.md` declaring
// `name: ProbeStyleName` applies for "ProbeStyleName" and is silently ignored
// for "zzprobe" and "probestylename". The CLI does not reject an unknown name;
// it echoes the requested value back in its `init` event and applies nothing,
// so callers must validate a name against Discover before using it.
package claudestyle

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultName is Claude Code's built-in style, meaning "no style file".
const DefaultName = "default"

// StylesDirName is the per-root directory Claude Code reads style files from.
const StylesDirName = "output-styles"

// Source records where a style file was found. Project styles take precedence
// over user styles with the same name, matching Claude Code's general
// project-over-user settings precedence.
type Source string

const (
	SourceBuiltIn Source = "built-in"
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Option is one selectable output style.
type Option struct {
	Name        string
	Description string
	Source      Source
	Path        string
}

// Label renders a short source-qualified description for pickers and status
// text.
func (o Option) Label() string {
	if o.Source == SourceBuiltIn {
		return o.Name
	}
	return o.Name + " (" + string(o.Source) + ")"
}

// Discover returns the built-in default style followed by the user and project
// styles readable from disk, sorted by name within each source. An unreadable
// or missing directory contributes nothing rather than failing the whole scan,
// because a missing output-styles directory is the normal case.
func Discover(claudeHome, projectPath string) []Option {
	options := []Option{{
		Name:        DefaultName,
		Description: "Claude Code's standard responses",
		Source:      SourceBuiltIn,
	}}
	byName := map[string]int{DefaultName: 0}

	for _, scan := range []struct {
		root   string
		source Source
	}{
		{root: strings.TrimSpace(claudeHome), source: SourceUser},
		{root: filepath.Join(strings.TrimSpace(projectPath), ".claude"), source: SourceProject},
	} {
		if strings.TrimSpace(scan.root) == "" {
			continue
		}
		for _, option := range loadStyleDir(filepath.Join(scan.root, StylesDirName), scan.source) {
			if index, ok := byName[option.Name]; ok {
				// A project style shadows a user style of the same name.
				options[index] = option
				continue
			}
			byName[option.Name] = len(options)
			options = append(options, option)
		}
	}
	return options
}

func loadStyleDir(dir string, source Source) []Option {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	options := make([]Option, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name, description := parseFrontMatter(string(data))
		if name == "" {
			// Without a frontmatter name Claude Code cannot address the style,
			// so listing it would offer a selection that silently does nothing.
			continue
		}
		options = append(options, Option{
			Name:        name,
			Description: description,
			Source:      source,
			Path:        path,
		})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options
}

// Find returns the option matching name exactly. Matching is case-sensitive
// because Claude Code's own lookup is.
func Find(options []Option, name string) (Option, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Option{}, false
	}
	for _, option := range options {
		if option.Name == name {
			return option, true
		}
	}
	return Option{}, false
}

// Resolve maps typed input to a style, accepting any casing and returning the
// exact stored name. Claude Code matches style names case-sensitively and
// silently applies nothing for a wrong-case name, so resolving here is what
// makes `/style terse` work instead of quietly doing nothing.
//
// The ambiguous return reports the rare case of two styles whose names differ
// only by case: picking one arbitrarily would apply a style the user did not
// ask for, so the caller must ask which.
func Resolve(options []Option, name string) (match Option, ambiguous []Option, ok bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Option{}, nil, false
	}
	// An exact match always wins, so a deliberate exact name is never treated
	// as ambiguous against a differently-cased sibling.
	if exact, found := Find(options, name); found {
		return exact, nil, true
	}
	matches := []Option{}
	for _, option := range options {
		if strings.EqualFold(option.Name, name) {
			matches = append(matches, option)
		}
	}
	switch len(matches) {
	case 0:
		return Option{}, nil, false
	case 1:
		return matches[0], nil, true
	default:
		return Option{}, matches, false
	}
}

// Names returns every discovered style name in listing order.
func Names(options []Option) []string {
	names := make([]string, 0, len(options))
	for _, option := range options {
		names = append(names, option.Name)
	}
	return names
}

// IsDefault reports whether name selects Claude Code's built-in behavior. An
// empty name means the same thing: LCR sends no override.
func IsDefault(name string) bool {
	name = strings.TrimSpace(name)
	return name == "" || name == DefaultName
}

func parseFrontMatter(markdown string) (name, description string) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", ""
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
		if key != "name" && key != "description" {
			continue
		}
		values[key] = trimFrontMatterValue(value)
	}
	return strings.TrimSpace(values["name"]), strings.TrimSpace(values["description"])
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
