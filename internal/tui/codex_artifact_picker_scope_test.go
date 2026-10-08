package tui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func TestCodexArtifactPickerLatestReplyAndMediaFilters(t *testing.T) {
	project := t.TempDir()
	video := filepath.Join(project, "review", "after.mp4")
	imagePath := filepath.Join(project, "review", "after.png")
	writeCodexArtifactTestFiles(t, video)
	entries := make([]codexapp.TranscriptEntry, codexArtifactLinkScanEntryBudget+1)
	for i := range entries {
		entries[i] = codexapp.TranscriptEntry{Kind: codexapp.TranscriptCommand, Text: fmt.Sprintf("[old notes](old-%d.md)", i)}
	}
	entries = append(entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: "[Handoff](review/README.md)\n[After video](" + video + ")\n[After image](" + imagePath + ")"})
	snapshot := codexapp.Snapshot{ProjectPath: project, Entries: entries}
	m := Model{codexVisibleProject: project, codexViewport: viewport.New(100, 2)}
	m.storeCodexSnapshot(project, snapshot)
	rendered := m.renderAndCacheCodexTranscript(project, snapshot, 100)
	m.codexViewport.SetContent(rendered)
	m.codexViewport.GotoTop()
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	m = normalizeUpdateModel(updated)
	if cmd == nil || !m.codexArtifactPicker.ShowLatest || codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 3 {
		t.Fatalf("latest reply must be available before the history scan: %#v", m.codexArtifactPicker)
	}
	panel := ansi.Strip(m.renderCodexArtifactPickerContent(100, 40))
	for _, want := range []string{"Latest reply 3", "Images 1", "Videos 1", "Scanning transcript links", "after.mp4"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("picker missing %q:\n%s", want, panel)
		}
	}
	m = drainCmdMsgs(m, cmd)
	if codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 3 || len(m.codexArtifactPicker.Targets) != len(entries)+2 {
		t.Fatalf("scan changed the latest scope or lost history: %#v", m.codexArtifactPicker)
	}
	if panel := ansi.Strip(m.renderCodexArtifactPickerContent(100, 40)); strings.Contains(panel, "Scanning transcript") {
		t.Fatalf("completed scan still looks pending:\n%s", panel)
	}
	for i := 0; i < 2; i++ {
		updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRight})
		m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	}
	updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("after")})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if target, ok := m.currentCodexArtifactTarget(); !ok || target.Path != video || codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 1 {
		t.Fatalf("video filter selected %#v, ok=%t", target, ok)
	}
	updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if m.codexArtifactPicker.ShowLatest || m.codexArtifactPicker.Filter != "after" || m.codexArtifactPicker.TypeFilter != codexArtifactPickerVideos {
		t.Fatal("changing scope must preserve text and media filters")
	}
	updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if target, ok := m.currentCodexArtifactTarget(); !ok || target.Path != video {
		t.Fatalf("clearing filters jumped away from the reviewed file: %#v", target)
	}
	oldOpener := externalPathOpener
	opened := ""
	externalPathOpener = func(path string) error { opened = path; return nil }
	t.Cleanup(func() { externalPathOpener = oldOpener })
	_, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("opening the selected video did not queue a command")
	}
	cmd()
	if opened != video {
		t.Fatalf("opened %q, want %q", opened, video)
	}
}

func TestCodexArtifactPickerEmptyLatestReplyAndFilterRecovery(t *testing.T) {
	for _, latest := range []string{"Finished.", "The missing file is `missing.png`."} {
		t.Run(latest, func(t *testing.T) {
			project := t.TempDir()
			snapshot := codexapp.Snapshot{ProjectPath: project, Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptAgent, Text: "[Earlier video](clip.mp4)"},
				{Kind: codexapp.TranscriptAgent, Text: latest},
			}}
			m := Model{codexVisibleProject: project}
			m.storeCodexSnapshot(project, snapshot)
			updated, cmd := m.openCodexArtifactPicker(snapshot)
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			if m.codexArtifactPicker.ShowLatest || codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 1 {
				t.Fatal("a reply without openable files should fall back to all transcript links")
			}
			updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyTab})
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			if _, ok := m.currentCodexArtifactTarget(); ok {
				t.Fatal("empty latest scope selected a hidden history target")
			}
			if panel := ansi.Strip(m.renderCodexArtifactPickerContent(100, 30)); !strings.Contains(panel, "Tab shows all transcript") {
				t.Fatalf("empty scope needs recovery guidance:\n%s", panel)
			}
			updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyTab})
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRight})
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			if codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 0 {
				t.Fatal("image-only filter should exclude the video")
			}
			updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyCtrlL})
			m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
			if codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 1 {
				t.Fatal("clearing filters did not restore the video")
			}
		})
	}
}

func TestCodexArtifactPickerLatestInferredMediaWaitsForVerification(t *testing.T) {
	project := t.TempDir()
	video := filepath.Join(project, "review", "clip.mp4")
	writeCodexArtifactTestFiles(t, video)
	snapshot := codexapp.Snapshot{ProjectPath: project, Entries: []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptAgent, Text: "Review `review/clip.mp4` and `review/missing.mp4`."},
	}}
	m := Model{codexVisibleProject: project}
	m.storeCodexSnapshot(project, snapshot)
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	m = normalizeUpdateModel(updated)
	if !m.codexArtifactPicker.ShowLatest || codexArtifactPickerFilteredCount(m.codexArtifactPicker) != 0 {
		t.Fatal("unverified paths should wait in the latest-reply scope")
	}
	updated, _ = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("clip")})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if target, ok := m.currentCodexArtifactTarget(); !ok || target.Path != video || len(m.codexArtifactPicker.LatestTargets) != 1 {
		t.Fatalf("confirmed latest video = %#v, ok=%t", target, ok)
	}
	if m.codexArtifactPicker.Filter != "clip" {
		t.Fatal("background results cleared the user's search")
	}
}

func TestCodexArtifactPickerKeepsLatestOccurrenceWhenSwitchingScope(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "review", "clip.mp4")
	snapshot := codexapp.Snapshot{ProjectPath: project, TranscriptRevision: 1, Entries: []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptCommand, Text: "[Clip](clip.mp4)"},
		{Kind: codexapp.TranscriptAgent, Text: "[Clip](" + path + ")"},
	}}
	m := Model{codexVisibleProject: project}
	m.storeCodexSnapshot(project, snapshot)
	updated, cmd := m.openCodexArtifactPicker(snapshot)
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	if target, ok := m.currentCodexArtifactTarget(); !ok || target.sourceEntry != 1 {
		t.Fatalf("scope switch jumped to an older occurrence: %#v", target)
	}
	updated, cmd = m.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	m = drainCmdMsgs(normalizeUpdateModel(updated), cmd)
	snapshot.TranscriptRevision++
	snapshot.Entries = append([]codexapp.TranscriptEntry(nil), snapshot.Entries...)
	snapshot.Entries[1].Text = "[New clip](review/new.mp4)"
	m.storeCodexSnapshot(project, snapshot)
	m = drainCmdMsgs(m, m.maybeStartCodexArtifactLinkScan(project, snapshot))
	if target, ok := m.currentCodexArtifactTarget(); !ok || target.Path != filepath.Join(project, "review", "new.mp4") || len(m.codexArtifactPicker.LatestTargets) != 1 {
		t.Fatalf("latest-reply scope retained an obsolete link: %#v", target)
	}
}

func TestCodexArtifactPickerMediaLayoutFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {80, 22}, {80, 24}, {100, 32}, {180, 50}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			picker := &codexArtifactPickerState{ProjectPath: "/project", ShowLatest: true}
			for i := 0; i < 12; i++ {
				picker.LatestTargets = append(picker.LatestTargets, codexArtifactOpenTarget{
					Kind: "image", Label: "After capture", Path: fmt.Sprintf("/project/build/review-pending/camera-comparison/frame-%02d.png", i),
					PreviewData: mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255}),
				})
			}
			picker.Targets = picker.LatestTargets
			m := Model{codexArtifactPicker: picker}
			panel := m.renderCodexArtifactPicker(size[0], size[1])
			if lipgloss.Height(panel) > size[1] || lipgloss.Width(panel) > size[0] {
				t.Fatalf("picker is %dx%d, terminal is %dx%d:\n%s", lipgloss.Width(panel), lipgloss.Height(panel), size[0], size[1], ansi.Strip(panel))
			}
			for _, want := range []string{"Latest reply", "Images", "Videos", "frame-00.png", "After capture", "alt+f", "Esc"} {
				if !strings.Contains(ansi.Strip(panel), want) {
					t.Fatalf("missing %q:\n%s", want, ansi.Strip(panel))
				}
			}
		})
	}
}
