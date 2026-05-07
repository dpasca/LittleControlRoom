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

func TestSuggestUsesSingleNestedPackageScript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "package.json"), []byte(`{"scripts":{"dev":"vite"},"packageManager":"pnpm@9.0.0"}`), 0o644); err != nil {
		t.Fatalf("write nested package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "cd src && pnpm dev" {
		t.Fatalf("suggested command = %q, want %q", suggestion.Command, "cd src && pnpm dev")
	}
	if suggestion.Reason != `Found package.json script "dev" under src.` {
		t.Fatalf("suggested reason = %q", suggestion.Reason)
	}
}

func TestSuggestUsesUnityProjectAtRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeUnityProject(t, root, "6000.3.14f1")

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != unityEditorCommand() {
		t.Fatalf("suggested command = %q, want %q", suggestion.Command, unityEditorCommand())
	}
	if suggestion.Reason != "Found Unity project version 6000.3.14f1." {
		t.Fatalf("suggested reason = %q", suggestion.Reason)
	}
}

func TestSuggestUsesSingleNestedUnityProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeUnityProject(t, filepath.Join(root, "Leaf"), "6000.3.14f1")

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	wantCommand := "cd Leaf && " + unityEditorCommand()
	if suggestion.Command != wantCommand {
		t.Fatalf("suggested command = %q, want %q", suggestion.Command, wantCommand)
	}
	if suggestion.Reason != "Found Unity project version 6000.3.14f1 under Leaf." {
		t.Fatalf("suggested reason = %q", suggestion.Reason)
	}
}

func TestSuggestDoesNotUseBareUnityVersionFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ProjectSettings"), 0o755); err != nil {
		t.Fatalf("mkdir ProjectSettings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.3.14f1\n"), 0o644); err != nil {
		t.Fatalf("write ProjectVersion.txt: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "" {
		t.Fatalf("suggested command = %q, want empty command", suggestion.Command)
	}
}

func TestSuggestUsesUnityProjectWithoutPackagesDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeUnityProject(t, root, "2022.3.62f1")
	if err := os.RemoveAll(filepath.Join(root, "Packages")); err != nil {
		t.Fatalf("remove Packages: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != unityEditorCommand() {
		t.Fatalf("suggested command = %q, want %q", suggestion.Command, unityEditorCommand())
	}
	if suggestion.Reason != "Found Unity project version 2022.3.62f1." {
		t.Fatalf("suggested reason = %q", suggestion.Reason)
	}
}

func TestSuggestPrefersRootCandidateOverNestedCandidate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatalf("write root package.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "package.json"), []byte(`{"scripts":{"dev":"vite"},"packageManager":"pnpm@9.0.0"}`), 0o644); err != nil {
		t.Fatalf("write nested package.json: %v", err)
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "npm run dev" {
		t.Fatalf("suggested command = %q, want %q", suggestion.Command, "npm run dev")
	}
}

func TestSuggestDoesNotGuessAcrossMultipleNestedCandidates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, relPath := range []string{"apps/web", "apps/docs"} {
		if err := os.MkdirAll(filepath.Join(root, relPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relPath, err)
		}
		if err := os.WriteFile(filepath.Join(root, relPath, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
			t.Fatalf("write %s package.json: %v", relPath, err)
		}
	}

	suggestion, err := Suggest(root)
	if err != nil {
		t.Fatalf("Suggest() error = %v", err)
	}
	if suggestion.Command != "" {
		t.Fatalf("suggested command = %q, want empty command", suggestion.Command)
	}
}

func writeUnityProject(t *testing.T, projectPath, version string) {
	t.Helper()
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(projectPath, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	versionFile := filepath.Join(projectPath, "ProjectSettings", "ProjectVersion.txt")
	raw := []byte("m_EditorVersion: " + version + "\nm_EditorVersionWithRevision: " + version + " (test)\n")
	if err := os.WriteFile(versionFile, raw, 0o644); err != nil {
		t.Fatalf("write ProjectVersion.txt: %v", err)
	}
}
