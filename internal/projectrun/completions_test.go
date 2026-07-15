package projectrun

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCompletionsIncludeAllPackageScriptsWithoutChangingDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := `{
		"packageManager": "pnpm@9.0.0",
		"scripts": {
			"build": "vite build",
			"dev": "vite",
			"typecheck": "tsc --noEmit"
		}
	}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "pnpm dev" {
		t.Fatalf("default suggestion = %q, want pnpm dev", suggestion.Command)
	}

	completions, err := Completions(root)
	if err != nil {
		t.Fatalf("Completions() error = %v", err)
	}
	commands := suggestionCommands(completions)
	for _, want := range []string{"pnpm dev", "pnpm build", "pnpm typecheck"} {
		if !slices.Contains(commands, want) {
			t.Fatalf("completion commands = %#v, missing %q", commands, want)
		}
	}
}

func TestCompletionsCanIncludeNonRuntimePackageScriptWithoutSuggestingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "" {
		t.Fatalf("default suggestion = %q, want none", suggestion.Command)
	}

	completions, err := Completions(root)
	if err != nil {
		t.Fatalf("Completions() error = %v", err)
	}
	if commands := suggestionCommands(completions); !slices.Contains(commands, "npm run build") {
		t.Fatalf("completion commands = %#v, want npm run build", commands)
	}
}

func TestCompletionsIncludePublicMakeTargets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	makefile := `.PHONY: help test tui tui-parallel serve

help:
	@echo help

test:
	go test ./...

tui:
	go run ./cmd/app

tui-parallel:
	go run ./cmd/app --parallel

serve:
	go run ./cmd/app serve
`
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "make serve" {
		t.Fatalf("default suggestion = %q, want make serve", suggestion.Command)
	}

	completions, err := Completions(root)
	if err != nil {
		t.Fatalf("Completions() error = %v", err)
	}
	commands := suggestionCommands(completions)
	for _, want := range []string{"make serve", "make tui", "make tui-parallel", "make test"} {
		if !slices.Contains(commands, want) {
			t.Fatalf("completion commands = %#v, missing %q", commands, want)
		}
	}
}

func TestCompletionsIncludeMultipleGoEntrypointsWithoutChoosingOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, name := range []string{"api", "worker"} {
		if err := os.MkdirAll(filepath.Join(root, "cmd", name), 0o755); err != nil {
			t.Fatalf("mkdir cmd/%s: %v", name, err)
		}
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "" {
		t.Fatalf("default suggestion = %q, want none", suggestion.Command)
	}

	completions, err := Completions(root)
	if err != nil {
		t.Fatalf("Completions() error = %v", err)
	}
	commands := suggestionCommands(completions)
	for _, want := range []string{"go run ./cmd/api", "go run ./cmd/worker"} {
		if !slices.Contains(commands, want) {
			t.Fatalf("completion commands = %#v, missing %q", commands, want)
		}
	}
}

func suggestionCommands(suggestions []Suggestion) []string {
	commands := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		commands = append(commands, suggestion.Command)
	}
	return commands
}
