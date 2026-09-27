package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lcroom/internal/codexapp"
)

func TestCodexArtifactPickerRejectsSlashCommandMentions(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			projectPath := t.TempDir()
			snapshot := codexapp.Snapshot{
				Provider: provider, ProjectPath: projectPath, Busy: true,
				Entries: []codexapp.TranscriptEntry{{
					Kind: codexapp.TranscriptAgent,
					Text: "Game commands: `/auto`, `/apc`, `/lift`, `/dlc`.\n/auto\n/apc\n/dev/null\nDiscard output with `/dev/null`.",
				}},
			}
			m := Model{codexVisibleProject: projectPath}
			m.storeCodexSnapshot(projectPath, snapshot)
			updated, cmd := m.openCodexArtifactPicker(snapshot)
			got := normalizeUpdateModel(updated)
			if cmd == nil || got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 0 {
				t.Fatalf("unverified mentions must stay out of the picker while scanning: %#v", got.codexArtifactPicker)
			}
			// Typing while the scan is pending must keep the dialog and its filter.
			updated, _ = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
			got = normalizeUpdateModel(updated)
			if got.codexArtifactPicker == nil || got.codexArtifactPicker.Filter != "a" {
				t.Fatal("typing dismissed the scanning picker")
			}
			got = drainCmdMsgs(got, cmd)
			if got.codexArtifactPicker != nil || got.status != "No openable links in this embedded transcript" {
				t.Fatalf("command-only transcript should settle without a picker: %#v, %q", got.codexArtifactPicker, got.status)
			}
			updated, cmd = got.openCodexArtifactPicker(snapshot)
			got = normalizeUpdateModel(updated)
			if got.codexArtifactPicker != nil || cmd != nil {
				t.Fatal("reopening resurrected rejected paths from the visible transcript")
			}
		})
	}
}

func TestCodexArtifactPickerConfirmsFilesAndPreservesExplicitLinks(t *testing.T) {
	projectPath := t.TempDir()
	filePath := filepath.Join(projectPath, "auto")
	if err := os.WriteFile(filePath, []byte("real extensionless file"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(projectPath, "missing.txt")
	fileURLPath := filepath.Join(projectPath, "explicit.txt")
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join([]string{
				"Commands: `/auto` and `/apc`.",
				"File: `" + filePath + "`.",
				"Directory: `" + projectPath + "`.",
				"Missing: `" + missingPath + "`.",
				"Explicit: [missing](" + missingPath + ") and `file://" + fileURLPath + "`.",
				"Web: [docs](https://example.com/auto).",
			}, "\n"),
		}},
	}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 3 || cmd == nil {
		t.Fatalf("explicit links should be available while inferred paths are checked: %#v", got.codexArtifactPicker)
	}
	got = drainCmdMsgs(got, cmd)
	want := map[string]bool{filePath: true, projectPath: true, missingPath: true, fileURLPath: true, "https://example.com/auto": true}
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != len(want) {
		t.Fatalf("confirmed picker = %#v, want %d targets", got.codexArtifactPicker, len(want))
	}
	for _, target := range got.codexArtifactPicker.Targets {
		if !want[target.Path] {
			t.Fatalf("unexpected or repeated target: %#v", target)
		}
		delete(want, target.Path)
	}
}

func TestCodexArtifactScanConfirmsRelativePathsAfterLaterChunkEvidence(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	relativePath := "exports/clip.mp4"
	absolutePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := make([]codexapp.TranscriptEntry, codexArtifactLinkScanEntryBudget)
	entries[0] = codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: "Video: `" + relativePath + "`."}
	entries = append(entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptCommand, Text: absolutePath})
	snapshot := codexapp.Snapshot{ProjectPath: projectPath, Entries: entries}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	updated, nextCmd := m.Update(cmd())
	got := normalizeUpdateModel(updated)
	if targets := got.cachedProgressiveCodexOpenTargets(snapshot); len(targets) != 0 || nextCmd == nil {
		t.Fatalf("missing guessed path must wait for the next chunk: %#v", targets)
	}
	got = drainCmdMsgs(got, nextCmd)
	targets := got.cachedProgressiveCodexOpenTargets(snapshot)
	if len(targets) != 1 || targets[0].Path != absolutePath {
		t.Fatalf("later evidence should confirm the actual video: %#v", targets)
	}
}

func TestCodexArtifactScanRetriesMissingPathsOnTranscriptRevision(t *testing.T) {
	projectPath := t.TempDir()
	path := filepath.Join(projectPath, "output.txt")
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath, TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptAgent, Text: "Output: `" + path + "`."}},
	}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	got := drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
	if targets := got.cachedProgressiveCodexOpenTargets(snapshot); len(targets) != 0 {
		t.Fatalf("missing file was listed: %#v", targets)
	}
	if err := os.WriteFile(path, []byte("output"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot.TranscriptRevision++
	snapshot.Entries = append(snapshot.Entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: "Done."})
	got.storeCodexSnapshot(projectPath, snapshot)
	got = drainCmdMsgs(got, got.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
	if targets := got.cachedProgressiveCodexOpenTargets(snapshot); len(targets) != 1 || targets[0].Path != path {
		t.Fatalf("new file was not confirmed on the next revision: %#v", targets)
	}
}
