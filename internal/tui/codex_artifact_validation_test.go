package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"lcroom/internal/codexapp"
)

func TestCodexArtifactPickerKeepsFinalVideosAfterEarlierRelativeLinks(t *testing.T) {
	for _, mode := range []string{"same chunk", "later chunk", "streaming revision"} {
		t.Run(mode, func(t *testing.T) {
			projectPath := t.TempDir()
			folder := filepath.Join(projectPath, "build", "review-pending", "camera")
			names := []string{"debris-before.mp4", "debris-after.mp4", "missile-before.mp4", "missile-after.mp4"}
			var relativeLinks, finalLinks []string
			finalLinks = append(finalLinks, "[Complete handoff]("+filepath.Join(folder, "README.md")+")")
			for _, name := range names {
				relativeLinks = append(relativeLinks, "[old clip]("+name+")")
				finalLinks = append(finalLinks, "[review clip]("+filepath.Join(folder, name)+")")
			}
			snapshot := codexapp.Snapshot{
				ProjectPath: projectPath, TranscriptRevision: 1,
				Entries: []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptCommand, Text: strings.Join(relativeLinks, "\n")}},
			}
			if mode == "later chunk" {
				for len(snapshot.Entries) < codexArtifactLinkScanEntryBudget {
					snapshot.Entries = append(snapshot.Entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: "Still working."})
				}
			}
			m := Model{codexVisibleProject: projectPath, codexViewport: viewport.New(100, 30)}
			if mode == "streaming revision" {
				m.storeCodexSnapshot(projectPath, snapshot)
				m = drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
				snapshot.TranscriptRevision++
			}
			finalEntry := len(snapshot.Entries)
			snapshot.Entries = append(snapshot.Entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: strings.Join(finalLinks, "\n")})
			m.storeCodexSnapshot(projectPath, snapshot)
			m = drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
			updated, cmd := m.openCodexArtifactPicker(snapshot)
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			picker := m.codexArtifactPicker
			if picker == nil || len(picker.Targets) != 9 {
				t.Fatalf("picker = %#v, want four earlier clips followed by the handoff and four final clips", picker)
			}
			for i, name := range names {
				target := picker.Targets[len(picker.Targets)-len(names)+i]
				if target.Path != filepath.Join(folder, name) || target.Kind != "video" || target.Label != "review clip" || target.sourceEntry != finalEntry {
					t.Fatalf("final video %d = %#v", i, target)
				}
			}
			if picker.Selected != len(picker.Targets)-1 {
				t.Fatalf("selection = %d, want the latest video", picker.Selected)
			}
			panel := ansi.Strip(m.renderCodexArtifactPickerContent(120, 50))
			for _, name := range names {
				if !strings.Contains(panel, name) {
					t.Fatalf("final video %q missing from picker panel:\n%s", name, panel)
				}
			}
		})
	}
}

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

func TestCodexArtifactPickerListsTrailingSlashProjectDirectories(t *testing.T) {
	projectPath := t.TempDir()
	dirPath := filepath.Join(projectPath, "build", "terrain-review", "before-after")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Look at: `build/terrain-review/before-after/`, with the original on the left. " +
				"Not `build/missing-review/`.",
		}},
	}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	got := drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("picker = %#v, want only the existing directory", got.codexArtifactPicker)
	}
	if target := got.codexArtifactPicker.Targets[0]; target.Path != dirPath {
		t.Fatalf("target = %#v, want %q", target, dirPath)
	}
}

func scanCodexArtifactTargetsForTest(t *testing.T, projectPath string, entries []codexapp.TranscriptEntry) []codexArtifactOpenTarget {
	t.Helper()
	snapshot := codexapp.Snapshot{ProjectPath: projectPath, Entries: entries}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	got := drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
	return got.cachedProgressiveCodexOpenTargets(snapshot)
}

func writeCodexArtifactTestFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexArtifactScanResolvesBareNamesInTheFolderTheMessageNames(t *testing.T) {
	projectPath := t.TempDir()
	clipDir := filepath.Join(projectPath, "build", "review-clips", "fe-1b")
	clip := filepath.Join(clipDir, "title-backdrop.mp4")
	nearReadme := filepath.Join(clipDir, "README.md")
	rootReadme := filepath.Join(projectPath, "README.md")
	writeCodexArtifactTestFiles(t, clip, nearReadme, rootReadme)

	targets := scanCodexArtifactTargetsForTest(t, projectPath, []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Clips are in build/review-clips/fe-1b/\n\n| title-backdrop.mp4 │ the new backdrop |\n\nREADME.md there lists what to check. Node.js and e.g. v1.2 are not files.",
	}})
	got := make(map[string]bool)
	for _, target := range targets {
		got[target.Path] = true
	}
	want := map[string]bool{clipDir: true, clip: true, nearReadme: true}
	if len(got) != len(want) {
		t.Fatalf("targets = %#v, want %v", targets, want)
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("missing %s in %#v", path, targets)
		}
	}
	if got[rootReadme] {
		t.Fatalf("bare README.md resolved to the project root instead of the named folder: %#v", targets)
	}
}

func TestCodexArtifactScanFallsBackToTheProjectRootForBareNames(t *testing.T) {
	projectPath := t.TempDir()
	rootNotes := filepath.Join(projectPath, "NOTES.md")
	writeCodexArtifactTestFiles(t, rootNotes)

	targets := scanCodexArtifactTargetsForTest(t, projectPath, []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "See `NOTES.md` and also NOTES.md again; `missing.md` does not exist.",
	}})
	if len(targets) == 0 {
		t.Fatal("bare root file was not found")
	}
	for _, target := range targets {
		if target.Path != rootNotes {
			t.Fatalf("unexpected target %#v", target)
		}
	}
}

func TestCodexArtifactScanDropsAmbiguousBareNames(t *testing.T) {
	projectPath := t.TempDir()
	first := filepath.Join(projectPath, "a", "notes.md")
	second := filepath.Join(projectPath, "b", "notes.md")
	writeCodexArtifactTestFiles(t, first, second)

	targets := scanCodexArtifactTargetsForTest(t, projectPath, []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Compare a/ and b/ — notes.md differs.",
	}})
	for _, target := range targets {
		if filepath.Base(target.Path) == "notes.md" {
			t.Fatalf("ambiguous bare name resolved: %#v", target)
		}
	}
	if len(targets) != 2 {
		t.Fatalf("both folders should still be listed: %#v", targets)
	}
}

func TestCodexArtifactScanResolvesBareNamesFromRecentlyEditedFolders(t *testing.T) {
	projectPath := t.TempDir()
	source := filepath.Join(projectPath, "src", "app", "launch.cpp")
	other := filepath.Join(projectPath, "docs", "launch.cpp.md")
	writeCodexArtifactTestFiles(t, source, other)

	targets := scanCodexArtifactTargetsForTest(t, projectPath, []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptTool, ToolName: "Edit", ToolPath: source},
		{Kind: codexapp.TranscriptAgent, Text: "Updated `launch.cpp` to center the camera."},
	})
	found := false
	for _, target := range targets {
		if target.Path == source && target.Label == "launch.cpp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("launch.cpp should resolve to the edited folder: %#v", targets)
	}
}

func TestCodexArtifactScanLeavesCommandOutputAloneForBareNames(t *testing.T) {
	projectPath := t.TempDir()
	writeCodexArtifactTestFiles(t, filepath.Join(projectPath, "main.go"))

	targets := scanCodexArtifactTargetsForTest(t, projectPath, []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptCommand, CommandText: "ls", Text: "$ ls\nmain.go\n",
	}})
	if len(targets) != 0 {
		t.Fatalf("shell listings must not flood the picker: %#v", targets)
	}
}

func TestCodexArtifactPickerExcludesLinksFromMultilineCommandInput(t *testing.T) {
	projectPath := t.TempDir()
	audioPath := filepath.Join(projectPath, "build", "review-pending", "menu-music", "military-r2", "loop.wav")
	writeCodexArtifactTestFiles(t, audioPath, filepath.Join(projectPath, "preview.wav"))
	command := "python3 - <<'PY'\ntext = '''[preview](preview.wav)\n[provenance](provenance.json)'''\nprint('Wrote README')\nPY"
	snapshot := codexapp.Snapshot{ProjectPath: projectPath, Entries: []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptCommand, CommandText: command, Text: "$ " + command + "\nWrote README\n[command completed, exit 0]"},
		{Kind: codexapp.TranscriptAgent, Text: "Listen: [loop](" + audioPath + ")."},
	}}
	m := Model{codexVisibleProject: projectPath}
	m.storeCodexSnapshot(projectPath, snapshot)
	m = drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(projectPath, snapshot))
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if m.codexArtifactPicker == nil || len(m.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("script links leaked into picker: %#v", m.codexArtifactPicker)
	}
	target := m.codexArtifactPicker.Targets[0]
	if target.Path != audioPath || codexArtifactTargetFolder(target, projectPath, "") != "build/review-pending/menu-music/military-r2/" {
		t.Fatalf("audio target lost its folder: %#v", target)
	}
	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error { opened = path; return nil }
	t.Cleanup(func() { externalPathOpener = oldOpener })
	_, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no open command")
	}
	cmd()
	if opened != audioPath {
		t.Fatalf("opened %q, want %q", opened, audioPath)
	}
}

func TestCodexCommandLinkScanSeparatesInputFromOutput(t *testing.T) {
	command := "python3 - <<'PY'\ntext = '[input](input.wav)'\nPY"
	output := "[output](/tmp/review/loop.wav)"
	for _, tt := range []struct{ name, text, command, want string }{
		{"multiline", "$ " + command + "\n" + output, command, output},
		{"output delta", output, command, output},
		{"shortened input", "$ python3 - <<'PY'\n[... shortened ...]\n[input](input.wav)\nPY", command, ""},
		{"legacy", "$ echo done\n" + output, "", output},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := codexapp.TranscriptEntry{Kind: codexapp.TranscriptCommand, Text: tt.text, CommandText: tt.command}
			if got := codexCommandResultLinkScanText(entry); got != tt.want {
				t.Fatalf("scan text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexArtifactMentionShapes(t *testing.T) {
	for _, accepted := range []string{"README.md", "a/b/clip.mp4", "build/out/", "résumé.pdf", ".github/ci.yml", "main.go."} {
		if _, ok := codexNormalizeMention(accepted); !ok {
			t.Errorf("%q should be a candidate", accepted)
		}
	}
	for _, rejected := range []string{"v1.2", "1.5.3", "e.g", "and/or", "/etc/hosts", "../up.txt", "~/x.txt", "foo", "a//b.txt", ".go", "a b.txt"} {
		if mention, ok := codexNormalizeMention(rejected); ok {
			t.Errorf("%q should not be a candidate, got %q", rejected, mention)
		}
	}
	if _, ok := codexCodeSpanMention("go test ./..."); ok {
		t.Error("commands are not file mentions")
	}
}

func TestCodexArtifactTargetFolderIsRelativeToTheProject(t *testing.T) {
	project := "/Users/me/dev/repos/App--feature-worktree"
	tests := []struct {
		path, want string
	}{
		{project + "/build/review-clips/fe-1b/clip.mp4", "build/review-clips/fe-1b/"},
		{project + "/README.md", "./"},
		{project + "/build/review-clips", "build/"},
		{"/Users/me/dev/repos/App/src/main.go", "~/dev/repos/App/src/"},
		{"/tmp/out.png", "/tmp/"},
	}
	for _, tt := range tests {
		got := codexArtifactTargetFolder(codexArtifactOpenTarget{Path: tt.path}, project, "/Users/me")
		if got != tt.want {
			t.Errorf("folder(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
	if got := shortenPathLeft("build/review-clips/fe-1b/", 12); got != "…lips/fe-1b/" {
		t.Errorf("shortenPathLeft kept the wrong end: %q", got)
	}
}

func TestCodexArtifactPickerFiltersByFolderAndShowsRelativeFolders(t *testing.T) {
	project := "/Users/me/dev/repos/App--feature-worktree-with-a-very-long-name"
	picker := &codexArtifactPickerState{
		ProjectPath: project,
		Targets: []codexArtifactOpenTarget{
			{Kind: "video", Path: project + "/build/review-clips/fe-1b/title-backdrop.mp4"},
			{Kind: "doc", Path: project + "/docs/README.md"},
		},
	}
	picker.Filter = "fe1b"
	if got := codexArtifactPickerFilteredIndexes(picker); len(got) != 1 || got[0] != 0 {
		t.Fatalf("folder filter = %v, want only the clip", got)
	}
	layout := newCodexArtifactPickerRowLayout(100)
	layout.ProjectPath = project
	row := ansi.Strip(renderCodexArtifactPickerRow(picker.Targets[0], false, 100, layout))
	if !strings.Contains(row, "build/review-clips/fe-1b/") || strings.Contains(row, "worktree") {
		t.Fatalf("row should show the project-relative folder: %q", row)
	}
}
