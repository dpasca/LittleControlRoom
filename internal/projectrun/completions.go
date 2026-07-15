package projectrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var preferredRuntimeNames = []string{"dev", "run", "start", "serve", "preview"}

// Completions returns project-derived commands suitable for completing the
// runtime command editor. Unlike Suggest, it includes non-default commands
// such as every package script and detected Make/Just target. Suggest remains
// deliberately conservative about choosing a command without user input.
func Completions(projectPath string) ([]Suggestion, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil, fmt.Errorf("project path is required")
	}

	completions, err := completionsAtPath(projectPath)
	if err != nil {
		return nil, err
	}
	if len(completions) > 0 {
		return dedupeSuggestions(completions), nil
	}

	completions, err = nestedCandidatesWith(projectPath, completionsAtPath)
	if err != nil {
		return nil, err
	}
	return dedupeSuggestions(completions), nil
}

func completionsAtPath(projectPath string) ([]Suggestion, error) {
	defaults, err := candidatesAtPath(projectPath)
	if err != nil {
		return nil, err
	}
	completions := append([]Suggestion(nil), defaults...)

	packageScripts, err := packageScriptCompletions(projectPath)
	if err != nil {
		return nil, err
	}
	completions = append(completions, packageScripts...)

	makeTargets, err := makeTargetCompletions(projectPath)
	if err != nil {
		return nil, err
	}
	completions = append(completions, makeTargets...)

	justTargets, err := justTargetCompletions(projectPath)
	if err != nil {
		return nil, err
	}
	completions = append(completions, justTargets...)
	completions = append(completions, goEntrypointCompletions(projectPath)...)

	return dedupeSuggestions(completions), nil
}

func packageScriptCompletions(projectPath string) ([]Suggestion, error) {
	raw, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read package.json: %w", err)
	}

	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil || len(manifest.Scripts) == 0 {
		return nil, nil
	}

	manager := detectPackageManager(projectPath, manifest.PackageManager)
	names := make([]string, 0, len(manifest.Scripts))
	for name, command := range manifest.Scripts {
		if strings.TrimSpace(name) != "" && strings.TrimSpace(command) != "" {
			names = append(names, name)
		}
	}
	names = preferredNamesFirst(names)

	out := make([]Suggestion, 0, len(names))
	for _, name := range names {
		command := packageManagerCommand(manager, name)
		reason := fmt.Sprintf("Found package.json script %q.", name)
		if delegatedScript, ok := delegatedPackageScript(manifest.Scripts, name); ok {
			command = packageManagerCommand(manager, delegatedScript)
			reason = fmt.Sprintf("Found package.json script %q delegating to %q.", name, delegatedScript)
		}
		out = append(out, Suggestion{Command: command, Reason: reason})
	}
	return dedupeSuggestions(out), nil
}

func makeTargetCompletions(projectPath string) ([]Suggestion, error) {
	for _, fileName := range []string{"GNUmakefile", "Makefile", "makefile"} {
		raw, err := os.ReadFile(filepath.Join(projectPath, fileName))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", fileName, err)
		}
		names := makeTargetNames(raw)
		out := make([]Suggestion, 0, len(names))
		for _, name := range names {
			out = append(out, Suggestion{
				Command: "make " + name,
				Reason:  fmt.Sprintf("Found %q target in %s.", name, fileName),
			})
		}
		return out, nil
	}
	return nil, nil
}

func makeTargetNames(raw []byte) []string {
	var phony []string
	var explicit []string
	for _, line := range logicalDefinitionLines(string(raw)) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(line, "\t") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon <= 0 || (colon+1 < len(trimmed) && trimmed[colon+1] == '=') {
			continue
		}
		left := strings.TrimSpace(trimmed[:colon])
		right := stripDefinitionComment(trimmed[colon+1:])
		if left == ".PHONY" {
			for _, name := range strings.Fields(right) {
				if isSimpleCompletionName(name) {
					phony = append(phony, name)
				}
			}
			continue
		}
		for _, name := range strings.Fields(left) {
			if isSimpleCompletionName(name) {
				explicit = append(explicit, name)
			}
		}
	}
	if len(phony) > 0 {
		return preferredNamesFirst(phony)
	}
	return preferredNamesFirst(explicit)
}

func justTargetCompletions(projectPath string) ([]Suggestion, error) {
	for _, fileName := range []string{"justfile", ".justfile"} {
		raw, err := os.ReadFile(filepath.Join(projectPath, fileName))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", fileName, err)
		}
		names := justTargetNames(raw)
		out := make([]Suggestion, 0, len(names))
		for _, name := range names {
			out = append(out, Suggestion{
				Command: "just " + name,
				Reason:  fmt.Sprintf("Found %q recipe in %s.", name, fileName),
			})
		}
		return out, nil
	}
	return nil, nil
}

func justTargetNames(raw []byte) []string {
	var names []string
	for _, line := range logicalDefinitionLines(string(raw)) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon <= 0 || (colon+1 < len(trimmed) && trimmed[colon+1] == '=') {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(trimmed[:colon]))
		if len(fields) > 0 && isSimpleCompletionName(fields[0]) {
			names = append(names, fields[0])
		}
	}
	return preferredNamesFirst(names)
}

func goEntrypointCompletions(projectPath string) []Suggestion {
	if _, err := os.Stat(filepath.Join(projectPath, "go.mod")); err != nil {
		return nil
	}

	out := []Suggestion{}
	if info, err := os.Stat(filepath.Join(projectPath, "main.go")); err == nil && !info.IsDir() {
		out = append(out, Suggestion{Command: "go run .", Reason: "Found main.go in the project root."})
	}

	entries, err := os.ReadDir(filepath.Join(projectPath, "cmd"))
	if err != nil {
		return out
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		out = append(out, Suggestion{
			Command: "go run ./cmd/" + dir,
			Reason:  fmt.Sprintf("Found Go entrypoint under cmd/%s.", dir),
		})
	}
	return out
}

func logicalDefinitionLines(raw string) []string {
	physical := strings.Split(raw, "\n")
	logical := make([]string, 0, len(physical))
	for i := 0; i < len(physical); i++ {
		line := strings.TrimSuffix(physical[i], "\r")
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			logical = append(logical, line)
			continue
		}
		for strings.HasSuffix(strings.TrimSpace(line), "\\") && i+1 < len(physical) {
			line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\")) + " " + strings.TrimSpace(physical[i+1])
			i++
		}
		logical = append(logical, line)
	}
	return logical
}

func stripDefinitionComment(value string) string {
	if index := strings.Index(value, "#"); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}

func isSimpleCompletionName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	return !strings.ContainsAny(name, "$%*?[]{}=:/\\")
}

func preferredNamesFirst(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	available := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			available[name] = struct{}{}
		}
	}

	out := make([]string, 0, len(available))
	for _, preferred := range preferredRuntimeNames {
		if _, ok := available[preferred]; ok {
			out = append(out, preferred)
			seen[preferred] = struct{}{}
		}
	}
	rest := make([]string, 0, len(available)-len(out))
	for name := range available {
		if _, ok := seen[name]; !ok {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
