package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/x/ansi"
)

func narrativeClaudeFixture() []codexapp.TranscriptEntry {
	clipsEdit := "python3 - <<'EOF'\nimport re\np='src/animation/actions/clips.ts'\ns=open(p).read()\ns=s.replace('a','b')\nopen(p,'w').write(s)\nEOF"
	bash := func(command, output string, failed bool) []codexapp.TranscriptEntry {
		summary := command
		if len(summary) > 120 {
			summary = summary[:120] + "..."
		}
		return []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Bash: " + summary, ToolName: "Bash"},
			{Kind: codexapp.TranscriptCommand, Text: "$ " + command + "\n" + output, CommandText: command, Failed: failed},
		}
	}
	entries := []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptUser, Text: "bring the cast to life"},
		{Kind: codexapp.TranscriptAgent, Text: "Now wiring the layers into the existing idle clips."},
		{Kind: codexapp.TranscriptTool, Text: "Write: /repo/src/animation/actions/idleLayers.ts", ToolName: "Write", ToolPath: "/repo/src/animation/actions/idleLayers.ts"},
	}
	entries = append(entries, bash(clipsEdit, "[command completed]", false)...)
	entries = append(entries, bash(clipsEdit, "[command completed]", false)...)
	entries = append(entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: "Writing deterministic tests for the idle."})
	entries = append(entries, bash("sed -n 1,30p tests/avatar/avatarInstance.test.ts", "import x", false)...)
	entries = append(entries, bash(`grep -rn "\.evaluate(" src/app | head -30`, "Exit code 1", true)...)
	entries = append(entries, bash("pnpm exec vitest run tests/animation/idleLayers.test.ts 2>&1 | tail -30", "      Tests  2 failed | 4 passed (6)", true)...)
	entries = append(entries, bash("./scripts/deploy.sh --dry-run", "Exit code 3\nnope", true)...)
	entries = append(entries, bash("mkdir -p /tmp/castlife && cat > /tmp/castlife/probe.test.ts <<'EOF'\nimport { test } from 'vitest';\nEOF", "[command completed]", false)...)
	entries = append(entries,
		codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "TodoWrite", ToolName: "TodoWrite"},
		codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Bash: go test ./internal/tui", ToolName: "Bash"},
	)
	return entries
}

func TestNarrativeModeFoldsToolTrafficIntoActivityLines(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: "/repo",
		Busy:        true,
		Entries:     narrativeClaudeFixture(),
	}
	rendered, _ := renderCodexTranscriptEntriesWithLinksConfigured(snapshot, 120, codexTranscriptRenderOptions{
		blockMode:                codexDenseBlockNarrative,
		blockModeSet:             true,
		hideReasoningSections:    true,
		hideReasoningSectionsSet: true,
		lcagentStatusVisibleSet:  true,
	})
	plain := ansi.Strip(rendered)
	t.Logf("\n%s", plain)

	for _, want := range []string{
		"✎ idleLayers.ts, clips.ts ×2",
		"▶ vitest idleLayers ✗ 2 failed, 4 passed",
		`? searched "\.evaluate("`,
		"→ read avatarInstance.test.ts",
		"~ 1 scratch probe",
		"✗ deploy.sh failed · exit 3",
		"● running go test tui…",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrative render missing %q:\n%s", want, plain)
		}
	}
	for _, unwanted := range []string{"python3 - <<", "Bash:", "Command", "TodoWrite", "grep -rn", "failed · exit 1"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("narrative render should hide %q:\n%s", unwanted, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		if strings.Contains(line, "Now wiring the layers") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "✎ idleLayers.ts") {
				t.Fatalf("activity should sit directly under its prose:\n%s", plain)
			}
		}
	}
}

func TestNarrativeModeOnlyShowsRunningLineForBusyTail(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderClaudeCode,
		Entries:  narrativeClaudeFixture(),
	}
	rendered, _ := renderCodexTranscriptEntriesWithLinksConfigured(snapshot, 120, codexTranscriptRenderOptions{
		blockMode:               codexDenseBlockNarrative,
		blockModeSet:            true,
		lcagentStatusVisibleSet: true,
	})
	plain := ansi.Strip(rendered)
	if strings.Contains(plain, "●") {
		t.Fatalf("idle session should not show a live activity line:\n%s", plain)
	}
	if !strings.Contains(plain, "▶ go test tui") {
		t.Fatalf("idle session should fold the unfinished call into the step summary:\n%s", plain)
	}
}

func TestNarrativeModeKeepsEditedFileLinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: "/repo",
		Entries:     narrativeClaudeFixture(),
	}
	_, links := renderCodexTranscriptEntriesWithLinksConfigured(snapshot, 120, codexTranscriptRenderOptions{
		blockMode:               codexDenseBlockNarrative,
		blockModeSet:            true,
		lcagentStatusVisibleSet: true,
	})
	found := false
	for _, link := range links {
		if strings.HasSuffix(link.Target.Path, "idleLayers.ts") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Alt+O links should still include files the engineer wrote: %#v", links)
	}
}
