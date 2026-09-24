package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/transcriptactivity"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Narrative transcript mode: every run of tool traffic between two pieces of
// engineer prose collapses into intent-level activity lines ("edited 3 files",
// "vitest idleLayers ✓ 3 passed"). Raw commands stay one Alt+L away.

const (
	codexActivityMaxNamedFiles  = 4
	codexActivityMaxNamedLabels = 2
	codexActivityMaxLabelWidth  = 32
)

var (
	codexActivityBorderColor = lipgloss.Color("238")
	codexActivityTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	codexActivityFaintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	codexActivityOKStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	codexActivityFailStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	codexActivityLiveStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
)

// codexNarrativeActivityRunEnd returns the end of the activity run starting at
// start, or start when the entry there is not activity. Hidden reasoning
// between tool calls is absorbed; trailing reasoning is left for the normal
// "Thinking…" indicator so a live turn still shows the model thinking.
func codexNarrativeActivityRunEnd(entries []codexapp.TranscriptEntry, start int, absorbReasoning bool) int {
	end := start
	lastActivity := -1
	for end < len(entries) {
		entry := entries[end]
		switch {
		case entry.GeneratedImage != nil:
		case transcriptactivity.ActivityKind(entry.Kind):
			lastActivity = end
			end++
			continue
		case absorbReasoning && entry.Kind == codexapp.TranscriptReasoning:
			end++
			continue
		}
		break
	}
	if lastActivity < 0 {
		return start
	}
	return lastActivity + 1
}

func renderCodexActivityGroup(entries []codexapp.TranscriptEntry, width int, projectPath string, liveTail bool) string {
	summary := transcriptactivity.Summarize(entries, transcriptactivity.Options{LiveTail: liveTail, ProjectPath: projectPath})
	if summary.Empty() {
		return ""
	}
	contentWidth := max(10, width-2)
	lines := packCodexActivitySegments(codexActivitySegments(summary, projectPath), contentWidth)
	for _, failure := range summary.Failures {
		lines = append(lines, truncateCodexInlineText(codexActivityFailureLine(failure, projectPath), contentWidth))
	}
	if summary.Running != nil {
		lines = append(lines, truncateCodexInlineText(codexActivityRunningLine(*summary.Running, projectPath), contentWidth))
	}
	if len(lines) == 0 {
		return ""
	}
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(codexActivityBorderColor).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func codexActivitySegments(summary transcriptactivity.Summary, projectPath string) []string {
	var segments []string
	add := func(symbol string, color lipgloss.Color, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		segments = append(segments, lipgloss.NewStyle().Foreground(color).Bold(true).Render(symbol)+" "+body)
	}

	if len(summary.Edited) > 0 || summary.UnnamedEdits > 0 {
		add("✎", lipgloss.Color("120"), codexActivityEditedText(summary, projectPath))
	}
	for _, run := range summary.Tests {
		add("▶", lipgloss.Color("111"), codexActivityRunText(run))
	}
	for _, run := range summary.Checks {
		add("◇", lipgloss.Color("111"), codexActivityRunText(run))
	}
	if len(summary.Git) > 0 {
		add("±", lipgloss.Color("214"), codexActivityTextStyle.Render(codexActivityLabelList(summary.Git, len(summary.Git), codexActivityMaxNamedLabels)))
	}
	if summary.SearchCount > 0 {
		add("?", lipgloss.Color("81"), codexActivityTextStyle.Render("searched ")+codexActivityFaintStyle.Render(codexActivityLabelList(codexActivitySearchLabels(summary.Searches), summary.SearchCount, 1)))
	}
	if summary.Reads > 0 {
		add("→", lipgloss.Color("179"), codexActivityTextStyle.Render(codexActivityReadText(summary, projectPath)))
	}
	if summary.RunCount > 0 {
		add("$", lipgloss.Color("246"), codexActivityTextStyle.Render("ran ")+codexActivityFaintStyle.Render(codexActivityLabelList(summary.Runs, summary.RunCount, codexActivityMaxNamedLabels)))
	}
	if summary.MCPCount > 0 {
		add("◆", lipgloss.Color("141"), codexActivityFaintStyle.Render(codexActivityLabelList(summary.MCP, summary.MCPCount, codexActivityMaxNamedLabels)))
	}
	if summary.WebCount > 0 {
		body := codexActivityLabelList(nonEmptyStrings(summary.Web), summary.WebCount, codexActivityMaxNamedLabels)
		if body == "" {
			body = codexActivityCount(summary.WebCount, "lookup", "lookups")
		}
		add("⊕", lipgloss.Color("214"), codexActivityTextStyle.Render("web ")+codexActivityFaintStyle.Render(body))
	}
	for _, label := range summary.Delegated {
		add("⇉", lipgloss.Color("141"), codexActivityTextStyle.Render("subagent ")+codexActivityFaintStyle.Render(truncateCodexInlineText(label, codexActivityMaxLabelWidth)))
	}
	if summary.Scratch > 0 {
		add("~", lipgloss.Color("244"), codexActivityFaintStyle.Render(codexActivityCount(summary.Scratch, "scratch probe", "scratch probes")))
	}
	return segments
}

func codexActivityEditedText(summary transcriptactivity.Summary, projectPath string) string {
	paths := make([]string, 0, len(summary.Edited))
	for _, touch := range summary.Edited {
		paths = append(paths, touch.Path)
	}
	names := codexActivityDisplayNames(paths, projectPath)
	parts := make([]string, 0, codexActivityMaxNamedFiles+1)
	for i, touch := range summary.Edited {
		if i >= codexActivityMaxNamedFiles {
			break
		}
		part := codexActivityTextStyle.Render(names[i])
		if touch.Count > 1 {
			part += codexActivityFaintStyle.Render(fmt.Sprintf(" ×%d", touch.Count))
		}
		parts = append(parts, part)
	}
	more := max(0, len(summary.Edited)-codexActivityMaxNamedFiles)
	if more > 0 {
		parts = append(parts, codexActivityFaintStyle.Render(fmt.Sprintf("+%d more", more)))
	}
	if summary.UnnamedEdits > 0 {
		label := codexActivityCount(summary.UnnamedEdits, "file change", "file changes")
		if len(parts) > 0 {
			label = "+" + label
		}
		parts = append(parts, codexActivityFaintStyle.Render(label))
	}
	return strings.Join(parts, codexActivityFaintStyle.Render(", "))
}

func codexActivityReadText(summary transcriptactivity.Summary, projectPath string) string {
	switch len(summary.ReadPaths) {
	case 0:
		return "read " + codexActivityCount(summary.Reads, "file", "files")
	case 1, 2:
		return "read " + strings.Join(codexActivityDisplayNames(summary.ReadPaths, projectPath), ", ")
	default:
		return "read " + codexActivityCount(len(summary.ReadPaths), "file", "files")
	}
}

func codexActivityRunText(run transcriptactivity.Run) string {
	text := codexActivityTextStyle.Render(truncateCodexInlineText(run.Label, codexActivityMaxLabelWidth))
	if run.Times > 1 {
		text += codexActivityFaintStyle.Render(fmt.Sprintf(" ×%d", run.Times))
	}
	switch run.Outcome {
	case transcriptactivity.OutcomeOK:
		text += " " + codexActivityOKStyle.Render(strings.TrimSpace("✓ "+run.Detail))
	case transcriptactivity.OutcomeFailed:
		text += " " + codexActivityFailStyle.Render(strings.TrimSpace("✗ "+run.Detail))
	}
	return text
}

func codexActivityFailureLine(action transcriptactivity.Action, projectPath string) string {
	label := action.Label
	if len(action.Paths) > 0 {
		label = strings.TrimSpace(codexActivityIntentNoun(action.Intent) + " " + strings.Join(codexActivityDisplayNames(action.Paths, projectPath), ", "))
	}
	label = firstNonEmptyString(label, codexActivityIntentNoun(action.Intent))
	text := "✗ " + label + " failed"
	if detail := strings.TrimSpace(action.Detail); detail != "" {
		text += " · " + detail
	}
	return codexActivityFailStyle.Render(text)
}

func codexActivityRunningLine(action transcriptactivity.Action, projectPath string) string {
	return codexActivityLiveStyle.Render("● "+codexActivityRunningVerb(action, projectPath)) + codexActivityFaintStyle.Render("…")
}

func codexActivityRunningVerb(action transcriptactivity.Action, projectPath string) string {
	target := strings.TrimSpace(action.Label)
	if len(action.Paths) > 0 {
		target = strings.Join(codexActivityDisplayNames(action.Paths, projectPath), ", ")
	}
	target = truncateCodexInlineText(target, codexActivityMaxLabelWidth)
	switch action.Intent {
	case transcriptactivity.IntentEdit:
		return strings.TrimSpace("editing " + target)
	case transcriptactivity.IntentRead:
		return strings.TrimSpace("reading " + target)
	case transcriptactivity.IntentSearch:
		return strings.TrimSpace("searching " + target)
	case transcriptactivity.IntentWeb:
		return strings.TrimSpace("fetching " + target)
	case transcriptactivity.IntentDelegate:
		return strings.TrimSpace("subagent " + target)
	case transcriptactivity.IntentScratch:
		return "scratch probe"
	default:
		return strings.TrimSpace("running " + target)
	}
}

func codexActivityIntentNoun(intent transcriptactivity.Intent) string {
	switch intent {
	case transcriptactivity.IntentEdit:
		return "edit"
	case transcriptactivity.IntentGit:
		return "git"
	default:
		return "command"
	}
}

// codexActivityDisplayNames shortens paths to base names, keeping the parent
// directory when two paths would otherwise look identical.
func codexActivityDisplayNames(paths []string, projectPath string) []string {
	names := make([]string, len(paths))
	counts := make(map[string]int, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(strings.TrimRight(p, "/"))
		counts[names[i]]++
	}
	for i, p := range paths {
		if counts[names[i]] < 2 {
			continue
		}
		rel := p
		if projectPath != "" {
			if r, err := filepath.Rel(projectPath, p); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
		}
		if parent := filepath.Base(filepath.Dir(rel)); parent != "." && parent != "/" {
			names[i] = parent + "/" + names[i]
		}
	}
	return names
}

// codexActivitySearchLabels drops grep's alternation escapes, which make
// patterns hard to read at a glance.
func codexActivitySearchLabels(labels []string) []string {
	out := make([]string, len(labels))
	for i, label := range labels {
		out[i] = strings.ReplaceAll(label, `\|`, "|")
	}
	return out
}

func codexActivityLabelList(labels []string, total, maxShown int) string {
	shown := make([]string, 0, maxShown)
	for _, label := range labels {
		if len(shown) >= maxShown {
			break
		}
		shown = append(shown, truncateCodexInlineText(label, codexActivityMaxLabelWidth))
	}
	text := strings.Join(shown, ", ")
	if more := total - len(shown); more > 0 && len(shown) > 0 {
		text += fmt.Sprintf(" +%d", more)
	}
	return text
}

func codexActivityCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// packCodexActivitySegments lays segments out left to right, wrapping between
// segments rather than inside them.
func packCodexActivitySegments(segments []string, width int) []string {
	separator := codexActivityFaintStyle.Render("  ·  ")
	separatorWidth := ansi.StringWidth(separator)
	var lines []string
	current := ""
	currentWidth := 0
	for _, segment := range segments {
		segment = truncateCodexInlineText(segment, width)
		segmentWidth := ansi.StringWidth(segment)
		if current != "" && currentWidth+separatorWidth+segmentWidth > width {
			lines = append(lines, current)
			current, currentWidth = "", 0
		}
		if current != "" {
			current += separator
			currentWidth += separatorWidth
		}
		current += segment
		currentWidth += segmentWidth
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
