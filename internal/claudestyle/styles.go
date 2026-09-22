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

// Verbosity is how much prose a style is expected to produce. It drives the
// at-a-glance colour in the UI, where the point is to notice a verbose setting
// before spending a turn on it.
type Verbosity string

const (
	// VerbosityConcise is proven concise: the style declares it, or its name
	// and description clearly say so.
	VerbosityConcise Verbosity = "concise"
	// VerbosityVerbose covers Claude Code's unconstrained default, styles that
	// declare themselves verbose, and styles that say so in their text.
	VerbosityVerbose Verbosity = "verbose"
)

// VerbosityKey is the optional frontmatter field a style file can set to
// "concise" or "verbose" to state its intent instead of relying on inference.
const VerbosityKey = "verbosity"

// Option is one selectable output style.
type Option struct {
	Name        string
	Description string
	Source      Source
	Path        string
	// Verbosity is declared by the style file when it sets a verbosity field,
	// and inferred from its name and description otherwise.
	Verbosity Verbosity
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
		Verbosity:   VerbosityVerbose,
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
		name, description, declared := parseFrontMatter(string(data))
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
			Verbosity:   resolveVerbosity(declared, name, description),
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

// conciseWords and verboseWords classify a style that does not declare its own
// verbosity. They match the vocabulary style authors actually use in a name or
// one-line description.
var (
	conciseWords = []string{
		"terse", "concise", "brief", "short", "minimal", "compact",
		"succinct", "laconic", "no-nonsense", "straight",
	}
	verboseWords = []string{
		"verbose", "explanatory", "explain", "detailed", "detail", "thorough",
		"learning", "teaching", "tutorial", "educational", "insight",
		"comprehensive", "elaborate", "narrate", "walkthrough",
	}
)

// resolveVerbosity prefers the style's own declaration, then infers from its
// name and description. Anything still unproven is reported verbose: the
// colour exists to warn, so an unrecognised style should not look safe.
func resolveVerbosity(declared, name, description string) Verbosity {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case string(VerbosityConcise), "terse", "short", "brief", "low":
		return VerbosityConcise
	case string(VerbosityVerbose), "long", "high", "explanatory":
		return VerbosityVerbose
	}
	haystack := strings.ToLower(name + " " + description)
	// A verbose marker wins over a concise one: a style described as "brief
	// but detailed explanations" is the kind worth flagging.
	for _, word := range verboseWords {
		if strings.Contains(haystack, word) {
			return VerbosityVerbose
		}
	}
	for _, word := range conciseWords {
		if strings.Contains(haystack, word) {
			return VerbosityConcise
		}
	}
	return VerbosityVerbose
}

// VerbosityOf reports the verbosity for a style name, defaulting to verbose
// for an unknown name so an unrecognised value never reads as safe.
func VerbosityOf(options []Option, name string) Verbosity {
	if IsDefault(name) {
		return VerbosityVerbose
	}
	if option, _, ok := Resolve(options, name); ok {
		return option.Verbosity
	}
	return VerbosityVerbose
}

func parseFrontMatter(markdown string) (name, description, verbosity string) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", ""
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
		if key != "name" && key != "description" && key != VerbosityKey {
			continue
		}
		values[key] = trimFrontMatterValue(value)
	}
	return strings.TrimSpace(values["name"]),
		strings.TrimSpace(values["description"]),
		strings.TrimSpace(values[VerbosityKey])
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
