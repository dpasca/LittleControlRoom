package projectrun

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxNestedSuggestionDepth = 3

var skippedNestedSuggestionDirs = map[string]struct{}{
	".git":         {},
	".next":        {},
	".turbo":       {},
	".yarn":        {},
	"build":        {},
	"builds":       {},
	"coverage":     {},
	"dist":         {},
	"library":      {},
	"logs":         {},
	"node_modules": {},
	"obj":          {},
	"recordings":   {},
	"saves":        {},
	"temp":         {},
	"tmp":          {},
	"usersettings": {},
	"vendor":       {},
}

type Suggestion struct {
	Command string
	Reason  string
}

func Suggest(projectPath string) (Suggestion, error) {
	candidates, err := Candidates(projectPath)
	if err != nil {
		return Suggestion{}, err
	}
	if len(candidates) == 0 {
		return Suggestion{}, nil
	}
	return candidates[0], nil
}

func Candidates(projectPath string) ([]Suggestion, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil, fmt.Errorf("project path is required")
	}

	candidates, err := candidatesAtPath(projectPath)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		return dedupeSuggestions(candidates), nil
	}

	candidates, err = nestedCandidates(projectPath)
	if err != nil {
		return nil, err
	}
	return dedupeSuggestions(candidates), nil
}

func candidatesAtPath(projectPath string) ([]Suggestion, error) {
	candidates := []Suggestion{}
	if suggestion, ok := suggestBinDev(projectPath); ok {
		candidates = append(candidates, suggestion)
	}
	if suggestion, ok, err := suggestPackageScript(projectPath); err != nil {
		return nil, err
	} else if ok {
		candidates = append(candidates, suggestion)
	}
	if suggestion, ok, err := suggestMakeTarget(projectPath); err != nil {
		return nil, err
	} else if ok {
		candidates = append(candidates, suggestion)
	}
	if suggestion, ok, err := suggestJustTarget(projectPath); err != nil {
		return nil, err
	} else if ok {
		candidates = append(candidates, suggestion)
	}
	if suggestion, ok := suggestGoEntrypoint(projectPath); ok {
		candidates = append(candidates, suggestion)
	}
	if suggestion, ok := suggestUnityEditor(projectPath); ok {
		candidates = append(candidates, suggestion)
	}

	return candidates, nil
}

func nestedCandidates(projectPath string) ([]Suggestion, error) {
	type match struct {
		relPath     string
		suggestions []Suggestion
	}

	matches := []match{}
	err := filepath.WalkDir(projectPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(projectPath, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		if shouldSkipNestedSuggestionDir(entry.Name(), relPath) {
			return filepath.SkipDir
		}
		if nestedSuggestionDepth(relPath) > maxNestedSuggestionDepth {
			return filepath.SkipDir
		}

		suggestions, err := candidatesAtPath(path)
		if err != nil {
			return err
		}
		if len(suggestions) == 0 {
			return nil
		}
		matches = append(matches, match{
			relPath:     filepath.ToSlash(relPath),
			suggestions: suggestions,
		})
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	if len(matches) != 1 {
		return nil, nil
	}

	out := make([]Suggestion, 0, len(matches[0].suggestions))
	for _, suggestion := range matches[0].suggestions {
		out = append(out, Suggestion{
			Command: prefixNestedCommand(matches[0].relPath, suggestion.Command),
			Reason:  prefixNestedReason(matches[0].relPath, suggestion.Reason),
		})
	}
	return out, nil
}

func shouldSkipNestedSuggestionDir(name, relPath string) bool {
	if strings.TrimSpace(relPath) == "" || relPath == "." {
		return false
	}
	_, skip := skippedNestedSuggestionDirs[strings.ToLower(strings.TrimSpace(name))]
	return skip
}

func nestedSuggestionDepth(relPath string) int {
	relPath = filepath.Clean(strings.TrimSpace(relPath))
	if relPath == "" || relPath == "." {
		return 0
	}
	return strings.Count(relPath, string(os.PathSeparator)) + 1
}

func prefixNestedCommand(relPath, command string) string {
	command = strings.TrimSpace(command)
	relPath = strings.TrimSpace(relPath)
	if command == "" || relPath == "" || relPath == "." {
		return command
	}
	return "cd " + shellQuote(relPath) + " && " + command
}

func prefixNestedReason(relPath, reason string) string {
	reason = strings.TrimSpace(strings.TrimSuffix(reason, "."))
	relPath = strings.TrimSpace(relPath)
	if reason == "" {
		if relPath == "" || relPath == "." {
			return ""
		}
		return fmt.Sprintf("Found runnable project files under %s.", relPath)
	}
	if relPath == "" || relPath == "." {
		return reason + "."
	}
	return fmt.Sprintf("%s under %s.", reason, relPath)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\r'\"\\$&;|<>()[]{}*?!#~`") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func suggestBinDev(projectPath string) (Suggestion, bool) {
	path := filepath.Join(projectPath, "bin", "dev")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Suggestion{}, false
	}
	return Suggestion{
		Command: "./bin/dev",
		Reason:  "Found bin/dev in the project root.",
	}, true
}

func suggestPackageScript(projectPath string) (Suggestion, bool, error) {
	raw, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Suggestion{}, false, nil
		}
		return Suggestion{}, false, fmt.Errorf("read package.json: %w", err)
	}
	var manifest struct {
		PackageManager string            `json:"packageManager"`
		Scripts        map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Suggestion{}, false, nil
	}
	if len(manifest.Scripts) == 0 {
		return Suggestion{}, false, nil
	}
	manager := detectPackageManager(projectPath, manifest.PackageManager)
	for _, script := range []string{"dev", "start", "serve", "preview"} {
		if strings.TrimSpace(manifest.Scripts[script]) == "" {
			continue
		}
		return Suggestion{
			Command: packageManagerCommand(manager, script),
			Reason:  fmt.Sprintf("Found package.json script %q.", script),
		}, true, nil
	}
	return Suggestion{}, false, nil
}

func detectPackageManager(projectPath, packageManager string) string {
	packageManager = strings.ToLower(strings.TrimSpace(packageManager))
	switch {
	case strings.HasPrefix(packageManager, "pnpm@"):
		return "pnpm"
	case strings.HasPrefix(packageManager, "yarn@"):
		return "yarn"
	case strings.HasPrefix(packageManager, "bun@"):
		return "bun"
	case strings.HasPrefix(packageManager, "npm@"):
		return "npm"
	}

	for _, candidate := range []struct {
		File    string
		Manager string
	}{
		{File: "pnpm-lock.yaml", Manager: "pnpm"},
		{File: "yarn.lock", Manager: "yarn"},
		{File: "bun.lock", Manager: "bun"},
		{File: "bun.lockb", Manager: "bun"},
		{File: "package-lock.json", Manager: "npm"},
	} {
		if _, err := os.Stat(filepath.Join(projectPath, candidate.File)); err == nil {
			return candidate.Manager
		}
	}
	return "npm"
}

func packageManagerCommand(manager, script string) string {
	switch manager {
	case "pnpm", "yarn", "bun":
		return manager + " " + script
	default:
		return "npm run " + script
	}
}

func suggestMakeTarget(projectPath string) (Suggestion, bool, error) {
	for _, fileName := range []string{"GNUmakefile", "Makefile", "makefile"} {
		path := filepath.Join(projectPath, fileName)
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Suggestion{}, false, fmt.Errorf("read %s: %w", fileName, err)
		}
		if target, ok := preferredTarget(raw); ok {
			return Suggestion{
				Command: "make " + target,
				Reason:  fmt.Sprintf("Found %q target in %s.", target, fileName),
			}, true, nil
		}
	}
	return Suggestion{}, false, nil
}

func suggestJustTarget(projectPath string) (Suggestion, bool, error) {
	for _, fileName := range []string{"justfile", ".justfile"} {
		path := filepath.Join(projectPath, fileName)
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Suggestion{}, false, fmt.Errorf("read %s: %w", fileName, err)
		}
		if target, ok := preferredTarget(raw); ok {
			return Suggestion{
				Command: "just " + target,
				Reason:  fmt.Sprintf("Found %q recipe in %s.", target, fileName),
			}, true, nil
		}
	}
	return Suggestion{}, false, nil
}

func preferredTarget(raw []byte) (string, bool) {
	for _, preferred := range []string{"dev", "run", "start", "serve"} {
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.HasPrefix(trimmed, preferred) {
				continue
			}
			if len(trimmed) == len(preferred) || trimmed[len(preferred)] != ':' {
				continue
			}
			return preferred, true
		}
	}
	return "", false
}

func suggestGoEntrypoint(projectPath string) (Suggestion, bool) {
	if _, err := os.Stat(filepath.Join(projectPath, "go.mod")); err != nil {
		return Suggestion{}, false
	}
	if info, err := os.Stat(filepath.Join(projectPath, "main.go")); err == nil && !info.IsDir() {
		return Suggestion{
			Command: "go run .",
			Reason:  "Found main.go in the project root.",
		}, true
	}
	cmdDir := filepath.Join(projectPath, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return Suggestion{}, false
	}
	dirs := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirs = append(dirs, entry.Name())
	}
	sort.Strings(dirs)
	if len(dirs) != 1 {
		return Suggestion{}, false
	}
	return Suggestion{
		Command: "go run ./cmd/" + dirs[0],
		Reason:  fmt.Sprintf("Found a single Go entrypoint under cmd/%s.", dirs[0]),
	}, true
}

func suggestUnityEditor(projectPath string) (Suggestion, bool) {
	version, ok := unityProjectVersion(projectPath)
	if !ok {
		return Suggestion{}, false
	}
	return Suggestion{
		Command: unityEditorCommand(),
		Reason:  fmt.Sprintf("Found Unity project version %s.", version),
	}, true
}

func unityProjectVersion(projectPath string) (string, bool) {
	for _, dir := range []string{"Assets", "ProjectSettings"} {
		info, err := os.Stat(filepath.Join(projectPath, dir))
		if err != nil || !info.IsDir() {
			return "", false
		}
	}

	raw, err := os.ReadFile(filepath.Join(projectPath, "ProjectSettings", "ProjectVersion.txt"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "m_EditorVersion:" {
			return fields[1], true
		}
	}
	return "", false
}

func unityEditorCommand() string {
	return strings.Join([]string{
		`UNITY_PROJECT_PATH="$PWD"`,
		`UNITY_VERSION="$(awk '$1=="m_EditorVersion:"{print $2; exit}' ProjectSettings/ProjectVersion.txt)"`,
		`UNITY_BIN="${UNITY_BIN:-/Applications/Unity/Hub/Editor/$UNITY_VERSION/Unity.app/Contents/MacOS/Unity}"`,
		`if [ -x "$UNITY_BIN" ]; then "$UNITY_BIN" -projectPath "$UNITY_PROJECT_PATH" -logFile -; elif command -v Unity >/dev/null 2>&1; then Unity -projectPath "$UNITY_PROJECT_PATH" -logFile -; else echo "Unity executable not found; set UNITY_BIN or install Unity $UNITY_VERSION" >&2; exit 127; fi`,
	}, "; ")
}

func dedupeSuggestions(candidates []Suggestion) []Suggestion {
	seen := map[string]struct{}{}
	out := make([]Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		command := strings.TrimSpace(candidate.Command)
		if command == "" {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		candidate.Command = command
		candidate.Reason = strings.TrimSpace(candidate.Reason)
		out = append(out, candidate)
	}
	return out
}
