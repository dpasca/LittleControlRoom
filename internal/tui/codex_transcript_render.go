package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"lcroom/internal/codexapp"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) renderCodexTranscriptEntries(snapshot codexapp.Snapshot, width int) string {
	rendered, _ := m.renderCodexTranscriptEntriesWithLinks(snapshot, width)
	return rendered
}

type codexTranscriptRenderOptions struct {
	fullHistory bool
}

type codexTranscriptLinkSpan struct {
	Target    codexArtifactOpenTarget
	StartLine int
	EndLine   int
}

func (m Model) renderCodexTranscriptEntriesWithLinks(snapshot codexapp.Snapshot, width int) (string, []codexTranscriptLinkSpan) {
	return m.renderCodexTranscriptEntriesWithLinksOptions(snapshot, width, codexTranscriptRenderOptions{})
}

func (m Model) renderCodexTranscriptEntriesWithLinksOptions(snapshot codexapp.Snapshot, width int, options codexTranscriptRenderOptions) (string, []codexTranscriptLinkSpan) {
	entries := codexTranscriptEntriesFromSnapshot(snapshot)
	if len(entries) == 0 {
		return "", nil
	}
	blockMode := m.codexDenseBlockMode.normalized()
	if !options.fullHistory {
		entries = limitCodexTranscriptEntriesForLiveView(entries, blockMode)
	}
	if snapshot.Provider.Normalized() == codexapp.ProviderOpenCode {
		entries = collapseOpenCodeToolRuns(entries, blockMode.full())
	}
	entries = collapseMassiveTranscriptEntries(entries, blockMode.full())
	if width <= 0 {
		width = 80
	}
	contentWidth := max(18, width-4)
	blocks := make([]string, 0, len(entries)*2)
	links := make([]codexTranscriptLinkSpan, 0)
	lineIndex := 0
	var previousKind codexapp.TranscriptKind
	hasPrevious := false
	lastGeneratedImageIndex := lastGeneratedImageEntryIndex(entries)
	// Track consecutive reasoning entries to merge into one compact indicator
	reasoningLineCount := 0
	flushReasoning := func() {
		if reasoningLineCount == 0 {
			return
		}
		block := renderReasoningIndicator(reasoningLineCount, contentWidth)
		if hasPrevious {
			separator := codexTranscriptEntrySeparator(previousKind, codexapp.TranscriptReasoning)
			blocks = append(blocks, separator)
			lineIndex += strings.Count(separator, "\n")
		}
		blocks = append(blocks, block)
		lineIndex += strings.Count(block, "\n")
		previousKind = codexapp.TranscriptReasoning
		hasPrevious = true
		reasoningLineCount = 0
	}
	for index, entry := range entries {
		if m.hideReasoningSections && !blockMode.full() && entry.Kind == codexapp.TranscriptReasoning {
			// Accumulate reasoning lines for compact indicator
			text := strings.TrimSpace(entry.Text)
			if text != "" {
				reasoningLineCount += len(strings.Split(text, "\n"))
			}
			continue
		}
		// Flush any pending reasoning indicator before a non-reasoning entry
		flushReasoning()
		block := renderCodexTranscriptEntryWithOptions(entry, contentWidth, blockMode, codexTranscriptEntryRenderOptions{
			latestGeneratedImage: index == lastGeneratedImageIndex,
		})
		if strings.TrimSpace(block) != "" {
			if hasPrevious {
				separator := codexTranscriptEntrySeparator(previousKind, entry.Kind)
				blocks = append(blocks, separator)
				lineIndex += strings.Count(separator, "\n")
			}
			startLine := lineIndex
			blocks = append(blocks, block)
			endLine := startLine + strings.Count(block, "\n") + 1
			lineIndex += strings.Count(block, "\n")
			for _, target := range codexOpenTargetsFromTranscriptEntry(entry) {
				links = append(links, codexTranscriptLinkSpan{
					Target:    target,
					StartLine: startLine,
					EndLine:   endLine,
				})
			}
			previousKind = entry.Kind
			hasPrevious = true
		}
	}
	// Flush trailing reasoning (model still thinking)
	flushReasoning()
	return strings.Join(blocks, ""), links
}

func codexTranscriptEntriesFromSnapshot(snapshot codexapp.Snapshot) []codexapp.TranscriptEntry {
	if len(snapshot.Entries) > 0 {
		return snapshot.Entries
	}
	return parseLegacyCodexTranscript(snapshot.Transcript)
}

func renderCodexTranscriptEntry(entry codexapp.TranscriptEntry, width int, blockMode codexDenseBlockMode) string {
	return renderCodexTranscriptEntryWithOptions(entry, width, blockMode, codexTranscriptEntryRenderOptions{})
}

type codexTranscriptEntryRenderOptions struct {
	latestGeneratedImage bool
}

func renderCodexTranscriptEntryWithOptions(entry codexapp.TranscriptEntry, width int, blockMode codexDenseBlockMode, options codexTranscriptEntryRenderOptions) string {
	if entry.GeneratedImage != nil {
		return renderCodexGeneratedImageBlock(entry, width, options.latestGeneratedImage)
	}
	text := strings.TrimSpace(sanitizeCodexRenderedText(entry.Text))
	if text == "" {
		return ""
	}
	switch entry.Kind {
	case codexapp.TranscriptUser:
		if dt := strings.TrimSpace(entry.DisplayText); dt != "" {
			text = dt
		}
		divider := lipgloss.NewStyle().
			Foreground(lipgloss.Color("238")).
			Render(strings.Repeat("─", max(0, width)))
		return divider + "\n" + renderCodexUserMessageBlock(text, width)
	case codexapp.TranscriptAgent:
		return renderCodexMessageBlock("", text, lipgloss.Color("120"), lipgloss.Color("252"), width)
	case codexapp.TranscriptPlan:
		return renderCodexMessageBlock("Plan", text, lipgloss.Color("214"), lipgloss.Color("252"), width)
	case codexapp.TranscriptReasoning:
		return renderReasoningBlock(text, width)
	case codexapp.TranscriptCommand:
		return renderCodexDenseBlock("Command", text, lipgloss.Color("111"), width, blockMode)
	case codexapp.TranscriptFileChange:
		return renderCodexDenseBlock("File changes", text, lipgloss.Color("179"), width, blockMode)
	case codexapp.TranscriptTool:
		return renderCodexToolLine(text, width)
	case codexapp.TranscriptError:
		return renderCodexMessageBlock("Error", text, lipgloss.Color("203"), lipgloss.Color("252"), width)
	case codexapp.TranscriptStatus:
		return renderCodexStatusBlock(text, width)
	case codexapp.TranscriptSystem:
		return renderCodexMessageBlock("System", text, lipgloss.Color("244"), lipgloss.Color("246"), width)
	default:
		return renderCodexMessageBlock("", text, lipgloss.Color("244"), lipgloss.Color("252"), width)
	}
}

func renderCodexGeneratedImageBlock(entry codexapp.TranscriptEntry, width int, latest bool) string {
	image := entry.GeneratedImage
	if image == nil {
		return ""
	}
	accent := lipgloss.Color("179")
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Generated image")
	meta := generatedImageMetaText(image)
	if meta != "" {
		title += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(meta)
	}
	lines := []string{title}
	if path := strings.TrimSpace(image.Path); path != "" {
		lines = append(lines, renderGeneratedImageFileLine(path, contentWidth))
	} else if sourcePath := strings.TrimSpace(image.SourcePath); sourcePath != "" {
		lines = append(lines, renderGeneratedImageFileLine(sourcePath, contentWidth))
	}
	if latest {
		lines = append(lines, renderGeneratedImageOpenHint(contentWidth))
	}
	if preview := renderANSIImagePreview(image.PreviewData, contentWidth, 16); strings.TrimSpace(preview) != "" {
		lines = append(lines, preview)
	}
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Width(width).
		Render(strings.Join(lines, "\n"))
}

func renderGeneratedImageOpenHint(width int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("111")).
		Width(max(10, width)).
		Render("Alt+O artifact picker")
}

func renderGeneratedImageFileLine(path string, width int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		Width(max(10, width)).
		Render("File: " + filepath.Base(path))
}

func lastGeneratedImageEntryIndex(entries []codexapp.TranscriptEntry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].GeneratedImage != nil {
			return i
		}
	}
	return -1
}

func generatedImageMetaText(image *codexapp.GeneratedImageArtifact) string {
	if image == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if image.Width > 0 && image.Height > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", image.Width, image.Height))
	}
	if image.ByteSize > 0 {
		parts = append(parts, formatGeneratedImageBytes(image.ByteSize))
	}
	return strings.Join(parts, " ")
}

func formatGeneratedImageBytes(size int64) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(1024))
	case size == 1:
		return "1 byte"
	case size > 1:
		return fmt.Sprintf("%d bytes", size)
	default:
		return ""
	}
}

func codexTranscriptEntrySeparator(previous, current codexapp.TranscriptKind) string {
	// Tight separator (single newline) for entries that are part of the same action flow
	switch {
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptCommand:
		return "\n"
	case previous == codexapp.TranscriptCommand && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptTool && current == codexapp.TranscriptFileChange:
		return "\n"
	case previous == codexapp.TranscriptFileChange && current == codexapp.TranscriptTool:
		return "\n"
	case previous == codexapp.TranscriptCommand && current == codexapp.TranscriptFileChange:
		return "\n"
	case previous == codexapp.TranscriptFileChange && current == codexapp.TranscriptCommand:
		return "\n"
	case previous == codexapp.TranscriptReasoning && current == codexapp.TranscriptReasoning:
		return "\n"
	default:
		return "\n\n"
	}
}

func compactCodexToolTranscriptText(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " | ")
}

// parsedToolCall holds the decomposed parts of a tool transcript entry.
type parsedToolCall struct {
	ToolName string // e.g. "bash", "read", "write", "grep"
	Status   string // e.g. "completed", "running", ""
	Summary  string // description or command
	Prefix   string // e.g. "Tool", "MCP tool", "Web search"
}

// parseToolTranscriptText extracts tool name, status, and summary from tool text.
func parseToolTranscriptText(text string) parsedToolCall {
	text = strings.TrimSpace(text)

	// Handle collapsed summary lines ("Tool activity: ...")
	if strings.HasPrefix(text, "Tool activity") {
		return parsedToolCall{Prefix: "Tool", ToolName: "activity", Summary: strings.TrimPrefix(text, "Tool activity: ")}
	}

	// "Web search: query"
	if strings.HasPrefix(text, "Web search: ") {
		return parsedToolCall{Prefix: "Web", ToolName: "search", Summary: strings.TrimPrefix(text, "Web search: ")}
	}

	// "Viewed image: path"
	if strings.HasPrefix(text, "Viewed image: ") {
		return parsedToolCall{Prefix: "Tool", ToolName: "image", Summary: strings.TrimPrefix(text, "Viewed image: ")}
	}

	// "Image generation [status]\nresult"
	if strings.HasPrefix(text, "Image generation") {
		return parsedToolCall{Prefix: "Tool", ToolName: "image_gen", Summary: text}
	}

	// "MCP tool server/tool [status]"
	if strings.HasPrefix(text, "MCP tool ") {
		rest := strings.TrimPrefix(text, "MCP tool ")
		name, status := "", ""
		if idx := strings.Index(rest, " ["); idx >= 0 {
			name = rest[:idx]
			end := strings.IndexByte(rest[idx+2:], ']')
			if end >= 0 {
				status = rest[idx+2 : idx+2+end]
			}
		} else {
			name = rest
		}
		return parsedToolCall{Prefix: "MCP", ToolName: name, Status: status}
	}

	// "Tool <name> [status]" (dynamic tool calls)
	if strings.HasPrefix(text, "Tool ") && strings.Contains(text, " [") {
		rest := strings.TrimPrefix(text, "Tool ")
		if idx := strings.Index(rest, " ["); idx >= 0 {
			name := rest[:idx]
			end := strings.IndexByte(rest[idx+2:], ']')
			status := ""
			if end >= 0 {
				status = rest[idx+2 : idx+2+end]
			}
			return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status}
		}
	}

	// "Tool <name> <status>: <summary>" or "Tool <name>: <summary>" or "Tool <name> <status>" or "Tool <name>"
	if strings.HasPrefix(text, "Tool ") {
		rest := strings.TrimPrefix(text, "Tool ")
		// Try "name status: summary"
		if colonIdx := strings.Index(rest, ": "); colonIdx >= 0 {
			before := rest[:colonIdx]
			summary := rest[colonIdx+2:]
			parts := strings.SplitN(before, " ", 2)
			name := parts[0]
			status := ""
			if len(parts) > 1 {
				status = parts[1]
			}
			return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status, Summary: summary}
		}
		// Try "name status" or just "name"
		parts := strings.SplitN(rest, " ", 2)
		name := parts[0]
		status := ""
		if len(parts) > 1 {
			status = parts[1]
		}
		return parsedToolCall{Prefix: "Tool", ToolName: name, Status: status}
	}

	return parsedToolCall{Summary: text}
}

// toolCategoryColor returns accent color and symbol for a tool name.
func toolCategoryColor(toolName string) (accent lipgloss.Color, symbol string) {
	lower := strings.ToLower(toolName)
	switch {
	case lower == "bash" || lower == "shell" || lower == "command" || lower == "execute":
		return lipgloss.Color("111"), "$" // blue
	case lower == "read" || lower == "cat" || lower == "view":
		return lipgloss.Color("179"), "→" // yellow/amber
	case lower == "write" || lower == "edit" || lower == "patch" || lower == "apply_diff":
		return lipgloss.Color("120"), "+" // green
	case lower == "grep" || lower == "search" || lower == "find" || lower == "glob" || lower == "rg":
		return lipgloss.Color("81"), "?" // cyan
	case strings.Contains(lower, "/"):
		return lipgloss.Color("141"), "◆" // purple for MCP (server/tool format)
	case lower == "image" || lower == "image_gen":
		return lipgloss.Color("179"), "◻" // amber
	case lower == "search":
		return lipgloss.Color("214"), "⊕" // orange for web
	case lower == "activity":
		return lipgloss.Color("244"), "…" // gray for collapsed summaries
	default:
		return lipgloss.Color("141"), "•" // purple default
	}
}

// renderCodexToolLine renders a tool transcript entry with structured styling.
func renderCodexToolLine(text string, width int) string {
	compacted := compactCodexToolTranscriptText(text)
	parsed := parseToolTranscriptText(compacted)

	accent, symbol := toolCategoryColor(parsed.ToolName)

	// Build the styled line
	var parts []string

	// Symbol + tool name (bold)
	nameStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	if parsed.ToolName != "" {
		parts = append(parts, nameStyle.Render(symbol+" "+parsed.ToolName))
	} else {
		parts = append(parts, nameStyle.Render(symbol+" tool"))
	}

	// Status (dimmed, skip "completed" as it's noise)
	if parsed.Status != "" && parsed.Status != "completed" && parsed.Status != "call completed" {
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true)
		parts = append(parts, statusStyle.Render("["+parsed.Status+"]"))
	}

	// Summary (lighter color)
	if parsed.Summary != "" {
		summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		summary := parsed.Summary
		// Truncate long summaries to fit width (leave room for name + status)
		usedWidth := len(parsed.ToolName) + 4 // symbol + spaces + margin
		maxSummary := width - usedWidth - 4
		if maxSummary > 10 && len(summary) > maxSummary {
			// Preserve "+N more ..." suffix if present
			if moreIdx := strings.LastIndex(summary, " | +"); moreIdx >= 0 {
				suffix := summary[moreIdx+3:] // e.g. "+3 more tool updates"
				suffixStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(suffix)
				trimmed := summary[:moreIdx]
				maxTrimmed := maxSummary - len(suffix) - 5
				if maxTrimmed > 10 && len(trimmed) > maxTrimmed {
					trimmed = trimmed[:maxTrimmed-1] + "…"
				}
				parts = append(parts, summaryStyle.Render(trimmed), suffixStyled)
			} else {
				summary = summary[:maxSummary-1] + "…"
				parts = append(parts, summaryStyle.Render(summary))
			}
		} else {
			parts = append(parts, summaryStyle.Render(summary))
		}
	}

	body := strings.Join(parts, " ")
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Width(width).
		Render(body)
}

func collapseOpenCodeToolRuns(entries []codexapp.TranscriptEntry, _ bool) []codexapp.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]codexapp.TranscriptEntry, 0, len(entries))
	toolRunStart := -1
	agentRunStart := -1
	flushTools := func(end int) {
		if toolRunStart < 0 || end <= toolRunStart {
			return
		}
		out = append(out, summarizeOpenCodeToolRun(entries[toolRunStart:end]))
		toolRunStart = -1
	}
	flushAgents := func(end int) {
		if agentRunStart < 0 || end <= agentRunStart {
			return
		}
		run := entries[agentRunStart:end]
		out = append(out, run...)
		agentRunStart = -1
	}
	for i, entry := range entries {
		switch entry.Kind {
		case codexapp.TranscriptTool:
			flushAgents(i)
			if toolRunStart < 0 {
				toolRunStart = i
			}
		case codexapp.TranscriptAgent:
			flushTools(i)
			if agentRunStart < 0 {
				agentRunStart = i
			}
		default:
			flushTools(i)
			flushAgents(i)
			out = append(out, entry)
		}
	}
	flushTools(len(entries))
	flushAgents(len(entries))
	return out
}

// collapseMassiveTranscriptEntries caps oversized non-answer entries and detects
// repetitive assistant output. Assistant answers are not line-capped: truncating
// their tail makes resumed sessions lose readable content unless the user knows
// to expand hidden detail.
func collapseMassiveTranscriptEntries(entries []codexapp.TranscriptEntry, expanded bool) []codexapp.TranscriptEntry {
	if expanded || len(entries) == 0 {
		return entries
	}
	out := make([]codexapp.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		switch entry.Kind {
		case codexapp.TranscriptAgent:
			text := strings.TrimSpace(entry.Text)
			lines := strings.Split(text, "\n")

			if repIdx, repCount := detectRepetitiveContent(lines); repIdx >= 0 {
				kept := lines[:repIdx]
				omitted := len(lines) - repIdx
				summary := fmt.Sprintf("\n[Repetitive %s detected: %d similar blocks omitted (%d lines). Alt+L expands.]",
					"output", repCount, omitted)
				out = append(out, codexapp.TranscriptEntry{
					Kind: entry.Kind,
					Text: strings.Join(kept, "\n") + summary,
				})
				continue
			}

			out = append(out, entry)
		case codexapp.TranscriptReasoning:
			text := strings.TrimSpace(entry.Text)
			lines := strings.Split(text, "\n")

			if repIdx, repCount := detectRepetitiveContent(lines); repIdx >= 0 {
				kept := lines[:repIdx]
				omitted := len(lines) - repIdx
				summary := fmt.Sprintf("\n[Repetitive %s detected: %d similar blocks omitted (%d lines). Alt+L expands.]",
					"reasoning", repCount, omitted)
				out = append(out, codexapp.TranscriptEntry{
					Kind: entry.Kind,
					Text: strings.Join(kept, "\n") + summary,
				})
				continue
			}

			if len(lines) > openCodeMaxReasoningLines {
				preview := strings.Join(lines[:openCodeMaxReasoningPreview], "\n")
				summary := fmt.Sprintf("%s\n\n[Long reasoning truncated: %d lines total, %d shown. Alt+L expands the full output.]",
					preview, len(lines), openCodeMaxReasoningPreview)
				out = append(out, codexapp.TranscriptEntry{
					Kind: entry.Kind,
					Text: summary,
				})
				continue
			}
			out = append(out, entry)
		default:
			out = append(out, entry)
		}
	}
	return out
}

func limitCodexTranscriptEntriesForLiveView(entries []codexapp.TranscriptEntry, blockMode codexDenseBlockMode) []codexapp.TranscriptEntry {
	if len(entries) == 0 {
		return entries
	}

	start := codexTranscriptLiveViewStart(entries, blockMode)
	if start <= 0 {
		return entries
	}

	hidden := start
	out := make([]codexapp.TranscriptEntry, 0, len(entries)-start+1)
	out = append(out, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptSystem,
		Text: fmt.Sprintf("Older transcript hidden from live view (%d entries). Scroll to this marker to load full history.", hidden),
	})
	out = append(out, entries[start:]...)
	return out
}

func codexTranscriptLiveViewWouldLimit(entries []codexapp.TranscriptEntry, blockMode codexDenseBlockMode) bool {
	return codexTranscriptLiveViewStart(entries, blockMode) > 0
}

func codexTranscriptLiveViewStart(entries []codexapp.TranscriptEntry, blockMode codexDenseBlockMode) int {
	if len(entries) == 0 {
		return 0
	}

	start := len(entries)
	keptEntries := 0
	keptLines := 0
	keptBytes := 0
	for i := len(entries) - 1; i >= 0; i-- {
		entryLines := codexTranscriptLiveEntryApproxLineCount(entries[i], blockMode)
		entryBytes := codexTranscriptLiveEntryApproxByteCount(entries[i], blockMode)
		if keptEntries > 0 && (keptEntries >= codexTranscriptLiveEntryLimit ||
			keptLines+entryLines > codexTranscriptLiveLineLimit ||
			keptBytes+entryBytes > codexTranscriptLiveByteLimit) {
			break
		}
		start = i
		keptEntries++
		keptLines += entryLines
		keptBytes += entryBytes
	}
	return start
}

func codexTranscriptLiveEntryApproxLineCount(entry codexapp.TranscriptEntry, blockMode codexDenseBlockMode) int {
	switch entry.Kind {
	case codexapp.TranscriptCommand, codexapp.TranscriptFileChange:
		if blockMode.full() {
			return codexTranscriptEntryApproxLineCount(entry)
		}
		lines := strings.Split(strings.TrimSpace(entry.Text), "\n")
		visible, hidden := visibleCodexDenseBlockLines(lines, blockMode)
		if hidden > 0 || len(visible) > 0 {
			return max(1, 1+len(visible))
		}
		return 1
	case codexapp.TranscriptTool:
		return max(1, transcriptApproxLineCount(compactCodexToolTranscriptText(entry.Text)))
	default:
		return codexTranscriptEntryApproxLineCount(entry)
	}
}

func codexTranscriptLiveEntryApproxByteCount(entry codexapp.TranscriptEntry, blockMode codexDenseBlockMode) int {
	switch entry.Kind {
	case codexapp.TranscriptCommand, codexapp.TranscriptFileChange:
		if blockMode.full() {
			return codexTranscriptEntryApproxByteCount(entry)
		}
		lines := strings.Split(strings.TrimSpace(entry.Text), "\n")
		visible, hidden := visibleCodexDenseBlockLines(lines, blockMode)
		if hidden > 0 || len(visible) > 0 {
			return len(strings.Join(visible, "\n")) + 96
		}
		return 96
	case codexapp.TranscriptTool:
		return len(compactCodexToolTranscriptText(entry.Text)) + 32
	default:
		return codexTranscriptEntryApproxByteCount(entry)
	}
}

func codexTranscriptEntryApproxLineCount(entry codexapp.TranscriptEntry) int {
	text := entry.Text
	if strings.TrimSpace(entry.DisplayText) != "" {
		text = entry.DisplayText
	}
	lines := transcriptApproxLineCount(text)
	if entry.GeneratedImage != nil {
		lines += 3
	}
	return max(1, lines)
}

func codexTranscriptEntryApproxByteCount(entry codexapp.TranscriptEntry) int {
	n := len(entry.Text)
	if strings.TrimSpace(entry.DisplayText) != "" {
		n += len(entry.DisplayText)
	}
	if entry.GeneratedImage != nil {
		n += len(entry.GeneratedImage.PreviewData)
	}
	return n
}

func transcriptApproxLineCount(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// detectRepetitiveContent looks for repeated blocks of lines using a sliding window.
// Returns the line index where repetition starts and how many repeated blocks were found,
// or (-1, 0) if no significant repetition is detected.
func detectRepetitiveContent(lines []string) (startIdx int, repeatCount int) {
	if len(lines) < openCodeRepetitionWindowLines*openCodeRepetitionThreshold {
		return -1, 0
	}
	// Try window sizes from the configured size down to 3
	for windowSize := openCodeRepetitionWindowLines; windowSize >= 3; windowSize-- {
		for start := 0; start+windowSize*(openCodeRepetitionThreshold+1) <= len(lines); start++ {
			window := normalizeWindowLines(lines[start : start+windowSize])
			matches := 0
			pos := start + windowSize
			for pos+windowSize <= len(lines) {
				candidate := normalizeWindowLines(lines[pos : pos+windowSize])
				if window == candidate {
					matches++
					pos += windowSize
				} else {
					break
				}
			}
			if matches >= openCodeRepetitionThreshold {
				// Keep the first occurrence, report where repeats start
				return start + windowSize, matches
			}
		}
	}
	return -1, 0
}

func normalizeWindowLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(strings.TrimSpace(line))
		b.WriteByte('\n')
	}
	return b.String()
}

// toolEntrySummary extracts the meaningful summary from a tool entry,
// stripping the redundant "Tool <name> <status>:" prefix so that joined
// entries don't repeat it on every item.
func toolEntrySummary(entry codexapp.TranscriptEntry) string {
	compacted := strings.TrimSpace(compactCodexToolTranscriptText(entry.Text))
	if compacted == "" {
		return ""
	}
	parsed := parseToolTranscriptText(compacted)
	if parsed.Summary != "" {
		return parsed.Summary
	}
	// Fallback: use the compacted text if no summary was extracted
	return compacted
}

// toolEntryCommonName returns the dominant tool name from a set of entries,
// used to set the prefix once for a collapsed group.
func toolEntryCommonName(entries []codexapp.TranscriptEntry) string {
	counts := map[string]int{}
	for _, entry := range entries {
		parsed := parseToolTranscriptText(strings.TrimSpace(compactCodexToolTranscriptText(entry.Text)))
		if parsed.ToolName != "" {
			counts[parsed.ToolName]++
		}
	}
	best, bestCount := "", 0
	for name, count := range counts {
		if count > bestCount {
			best, bestCount = name, count
		}
	}
	return best
}

func summarizeOpenCodeToolRun(entries []codexapp.TranscriptEntry) codexapp.TranscriptEntry {
	if len(entries) == 0 {
		return codexapp.TranscriptEntry{}
	}
	if len(entries) <= maxOpenCodeCollapsedToolRun {
		return codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptTool,
			Text: joinOpenCodeToolRun(entries),
		}
	}
	previews := make([]string, 0, openCodeToolPreviewCount)
	for _, entry := range entries {
		summary := toolEntrySummary(entry)
		if summary == "" {
			continue
		}
		previews = append(previews, summary)
		if len(previews) >= openCodeToolPreviewCount {
			break
		}
	}
	if len(previews) == 0 {
		return codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptTool,
			Text: fmt.Sprintf("Tool activity: %d updates", len(entries)),
		}
	}
	toolName := toolEntryCommonName(entries)
	prefix := "Tool activity"
	if toolName != "" {
		prefix = "Tool " + toolName
	}
	text := prefix + ": " + strings.Join(previews, " | ")
	remaining := len(entries) - len(previews)
	if remaining > 0 {
		text += fmt.Sprintf(" | +%d more tool updates", remaining)
	}
	return codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptTool,
		Text: truncateText(text, openCodeCollapsedToolPreviewMaxText),
	}
}

func joinOpenCodeToolRun(entries []codexapp.TranscriptEntry) string {
	// If all entries share the same tool name, use it as a single prefix
	commonName := toolEntryCommonName(entries)
	summaries := make([]string, 0, len(entries))
	for _, entry := range entries {
		summary := toolEntrySummary(entry)
		if summary == "" {
			continue
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		return ""
	}
	if commonName != "" {
		return "Tool " + commonName + ": " + strings.Join(summaries, " | ")
	}
	return "Tool: " + strings.Join(summaries, " | ")
}

func compactCodexUserTranscriptText(text string) string {
	return text
}

func isCodexTranscriptAttachmentLine(line string) bool {
	return strings.HasPrefix(line, "[attached ") || strings.HasPrefix(line, "[attachment]")
}

func sanitizeCodexRenderedText(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = ansi.Strip(text)

	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case unicode.IsControl(r):
			continue
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func parseLegacyCodexTranscript(transcript string) []codexapp.TranscriptEntry {
	blocks := splitLegacyCodexTranscriptBlocks(transcript)
	if len(blocks) == 0 {
		return nil
	}
	entries := make([]codexapp.TranscriptEntry, 0, len(blocks))
	for _, block := range blocks {
		kind, text := parseLegacyCodexTranscriptBlock(block)
		if strings.TrimSpace(text) == "" {
			continue
		}
		entries = append(entries, codexapp.TranscriptEntry{Kind: kind, Text: text})
	}
	return entries
}

func splitLegacyCodexTranscriptBlocks(transcript string) []string {
	lines := strings.Split(strings.TrimSpace(transcript), "\n")
	blocks := make([]string, 0, len(lines))
	current := make([]string, 0, 4)
	flush := func() {
		if len(current) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(current, "\n"))
		current = current[:0]
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

func parseLegacyCodexTranscriptBlock(block string) (codexapp.TranscriptKind, string) {
	switch {
	case legacyTranscriptBlockHasPrefix(block, "You: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "You: ")
		return codexapp.TranscriptUser, text
	case legacyTranscriptBlockHasPrefix(block, "Codex: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Codex: ")
		return codexapp.TranscriptAgent, text
	case legacyTranscriptBlockHasPrefix(block, "Plan: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Plan: ")
		return codexapp.TranscriptPlan, text
	case legacyTranscriptBlockHasPrefix(block, "Reasoning: "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "Reasoning: ")
		return codexapp.TranscriptReasoning, text
	case legacyTranscriptBlockHasPrefix(block, "[status] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[status] ")
		return codexapp.TranscriptStatus, text
	case legacyTranscriptBlockHasPrefix(block, "[system] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[system] ")
		return codexapp.TranscriptSystem, text
	case legacyTranscriptBlockHasPrefix(block, "[error] "):
		text, _ := trimLegacyCodexTranscriptPrefix(block, "[error] ")
		return codexapp.TranscriptError, text
	default:
		return codexapp.TranscriptOther, strings.TrimSpace(block)
	}
}

func legacyTranscriptBlockHasPrefix(block, prefix string) bool {
	_, ok := trimLegacyCodexTranscriptPrefix(block, prefix)
	return ok
}

func trimLegacyCodexTranscriptPrefix(block, prefix string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], prefix) {
		return "", false
	}
	lines[0] = strings.TrimPrefix(lines[0], prefix)
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}
