package projectrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

	return dedupeSuggestions(candidates), nil
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
