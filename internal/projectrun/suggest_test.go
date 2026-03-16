package projectrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSuggestPrefersBinDev(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "dev"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin/dev: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "./bin/dev" {
		t.Fatalf("suggested command = %q, want ./bin/dev", suggestion.Command)
	}
}

func TestSuggestUsesPnpmForDevScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"dev":"vite"},"packageManager":"pnpm@9.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "pnpm dev" {
		t.Fatalf("suggested command = %q, want pnpm dev", suggestion.Command)
	}
}

func TestSuggestUsesSingleGoCmdEntrypoint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd", "server"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/server: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "go run ./cmd/server" {
		t.Fatalf("suggested command = %q, want go run ./cmd/server", suggestion.Command)
	}
}
