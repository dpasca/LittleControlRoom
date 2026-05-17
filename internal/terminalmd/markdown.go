package terminalmd

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	syntaxHighlightMaxBytes = 12 * 1024
	syntaxHighlightMaxLines = 240

	markdownLinkLabelScanLimit  = 512
	markdownLinkTargetScanLimit = 8192
)

type syntaxHighlightOptions struct {
	DefaultColor    lipgloss.Color
	BackgroundColor lipgloss.Color
	NoItalic        bool
}

// OpenLink describes a Markdown link that can be opened by a terminal UI.
type OpenLink struct {
	Kind     string
	Label    string
	Target   string
	OpenPath string
}

// RenderBody renders lightweight Markdown for terminal transcripts.
func RenderBody(body string, color lipgloss.Color, width int) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	fenceLanguage := ""
	fenceLines := []string{}
	tableRows := []string{}

	flushFence := func() {
		if len(fenceLines) == 0 {
			return
		}
		highlighted := syntaxHighlightBlock(strings.Join(fenceLines, "\n"), fenceLanguage, "", syntaxHighlightOptions{
			DefaultColor: lipgloss.Color("180"),
		})
		out = append(out, strings.Split(highlighted, "\n")...)
		fenceLines = nil
	}
	flushTable := func() {
		if len(tableRows) == 0 {
			return
		}
		out = append(out, renderMarkdownTable(tableRows, color, width)...)
		tableRows = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flushTable()
			if inFence {
				flushFence()
				inFence = false
				fenceLanguage = ""
			} else {
				inFence = true
				fenceLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case inFence:
			fenceLines = append(fenceLines, line)
		case isMarkdownTableRow(trimmed):
			tableRows = append(tableRows, trimmed)
		default:
			flushTable()
			switch {
			case strings.HasPrefix(trimmed, "[attached image]"):
				out = append(out, renderInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true)))
			case strings.HasPrefix(trimmed, "### "):
				out = append(out, renderInlineMarkdown(strings.TrimPrefix(trimmed, "### "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "## "):
				out = append(out, renderInlineMarkdown(strings.TrimPrefix(trimmed, "## "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "# "):
				out = append(out, renderInlineMarkdown(strings.TrimPrefix(trimmed, "# "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)))
			case strings.HasPrefix(trimmed, "> "):
				out = append(out, renderInlineMarkdown(line, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)))
			case isMarkdownHorizontalRule(trimmed):
				rule := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true).Render(strings.Repeat("─", min(width, 40)))
				out = append(out, rule)
			case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
				out = append(out, renderInlineMarkdown("• "+strings.TrimSpace(trimmed[2:]), lipgloss.NewStyle().Foreground(lipgloss.Color("151"))))
			case isMarkdownNumberedListItem(trimmed):
				num, content := parseMarkdownNumberedListItem(trimmed)
				numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
				out = append(out, numStyle.Render(num+".")+renderInlineMarkdown(" "+content, lipgloss.NewStyle().Foreground(lipgloss.Color("151"))))
			default:
				out = append(out, renderInlineMarkdown(line, lipgloss.NewStyle().Foreground(color)))
			}
		}
	}
	if inFence {
		flushFence()
	}
	flushTable()
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 3
}

func isMarkdownTableSeparator(line string) bool {
	if !isMarkdownTableRow(line) {
		return false
	}
	inner := strings.Trim(line, "|")
	for _, cell := range strings.Split(inner, "|") {
		cell = strings.TrimSpace(cell)
		cleaned := strings.Trim(cell, ":-")
		if cleaned != "" {
			return false
		}
	}
	return true
}

func renderMarkdownTable(rows []string, color lipgloss.Color, maxWidth int) []string {
	if len(rows) == 0 {
		return nil
	}
	parsed := make([][]string, 0, len(rows))
	separatorIdxs := map[int]bool{}
	for i, row := range rows {
		if isMarkdownTableSeparator(row) {
			separatorIdxs[i] = true
			parsed = append(parsed, nil)
			continue
		}
		inner := strings.Trim(strings.TrimSpace(row), "|")
		cells := strings.Split(inner, "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		parsed = append(parsed, cells)
	}

	numCols := 0
	for _, cells := range parsed {
		if len(cells) > numCols {
			numCols = len(cells)
		}
	}
	if numCols == 0 {
		return nil
	}
	colWidths := make([]int, numCols)
	for _, cells := range parsed {
		for j, cell := range cells {
			if w := ansi.StringWidth(cell); w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	tableOverhead := 2 + numCols*3 - 1
	totalWidth := tableOverhead
	for _, w := range colWidths {
		totalWidth += w
	}
	if totalWidth > maxWidth {
		colWidths = fitMarkdownTableColumnWidths(colWidths, maxWidth, tableOverhead)
	}
	for i, w := range colWidths {
		if w < 1 {
			colWidths[i] = 1
		}
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	cellStyle := lipgloss.NewStyle().Foreground(color)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	out := make([]string, 0, len(rows))
	for i, cells := range parsed {
		if separatorIdxs[i] {
			parts := make([]string, numCols)
			for j := range parts {
				parts[j] = strings.Repeat("─", colWidths[j])
			}
			out = append(out, borderStyle.Render("├─"+strings.Join(parts, "─┼─")+"─┤"))
			continue
		}
		isHeader := i == 0 && len(parsed) > 1 && separatorIdxs[1]
		wrappedCells := make([][]string, numCols)
		rowHeight := 1
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(cells) {
				cell = cells[j]
			}
			wrappedCells[j] = wrapMarkdownTableCell(cell, colWidths[j])
			if len(wrappedCells[j]) > rowHeight {
				rowHeight = len(wrappedCells[j])
			}
		}
		for lineIdx := 0; lineIdx < rowHeight; lineIdx++ {
			parts := make([]string, numCols)
			for j := 0; j < numCols; j++ {
				cellLine := ""
				if lineIdx < len(wrappedCells[j]) {
					cellLine = wrappedCells[j][lineIdx]
				}
				w := colWidths[j]
				pad := w - ansi.StringWidth(cellLine)
				if pad < 0 {
					pad = 0
				}
				padded := cellLine + strings.Repeat(" ", pad)
				if isHeader {
					parts[j] = headerStyle.Render(padded)
				} else {
					parts[j] = cellStyle.Render(padded)
				}
			}
			sep := borderStyle.Render(" │ ")
			out = append(out, borderStyle.Render("│ ")+strings.Join(parts, sep)+borderStyle.Render(" │"))
		}
	}
	return out
}

func fitMarkdownTableColumnWidths(maxWidths []int, maxWidth int, tableOverhead int) []int {
	widths := append([]int(nil), maxWidths...)
	target := maxWidth - tableOverhead
	if target <= 0 {
		target = len(widths)
	}
	if target < len(widths) {
		for i := range widths {
			widths[i] = 1
		}
		return widths
	}

	available := target
	unresolved := make([]bool, len(widths))
	remaining := 0
	for i := range widths {
		unresolved[i] = true
		remaining++
	}
	for remaining > 0 {
		share := available / remaining
		if share < 1 {
			share = 1
		}
		changed := false
		for i, maxWidth := range maxWidths {
			if !unresolved[i] || maxWidth > share {
				continue
			}
			widths[i] = max(1, maxWidth)
			available -= widths[i]
			unresolved[i] = false
			remaining--
			changed = true
		}
		if changed {
			continue
		}
		remainder := available - share*remaining
		for i := range widths {
			if !unresolved[i] {
				continue
			}
			widths[i] = share
			if remainder > 0 {
				widths[i]++
				remainder--
			}
		}
		break
	}
	return widths
}

func wrapMarkdownTableCell(cell string, width int) []string {
	if width <= 0 || cell == "" {
		return []string{cell}
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(cell)
	rawLines := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		lines = append(lines, hardWrapMarkdownTableLine(raw, width)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func hardWrapMarkdownTableLine(line string, width int) []string {
	if width <= 0 || ansi.StringWidth(line) <= width {
		return []string{line}
	}
	lines := []string{}
	remaining := line
	for remaining != "" {
		part := ansi.Truncate(remaining, width, "")
		if part == "" {
			runes := []rune(remaining)
			part = string(runes[:1])
		}
		lines = append(lines, part)
		remaining = strings.TrimPrefix(remaining, part)
	}
	return lines
}

func isMarkdownHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	cleaned := strings.ReplaceAll(line, " ", "")
	if len(cleaned) < 3 {
		return false
	}
	ch := cleaned[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for _, r := range cleaned {
		if byte(r) != ch {
			return false
		}
	}
	return true
}

func isMarkdownNumberedListItem(line string) bool {
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i < len(line)-1 && line[i+1] == ' ' {
			return true
		}
		return false
	}
	return false
}

func parseMarkdownNumberedListItem(line string) (num, content string) {
	dotIdx := strings.IndexByte(line, '.')
	if dotIdx < 0 {
		return "", line
	}
	return line[:dotIdx], strings.TrimSpace(line[dotIdx+1:])
}

func renderInlineMarkdown(text string, style lipgloss.Style) string {
	if text == "" {
		return style.Render(text)
	}
	codeStyle := style.Copy().
		Foreground(lipgloss.Color("223"))
	var out strings.Builder
	remaining := text
	for len(remaining) > 0 {
		boldIdx := strings.Index(remaining, "**")
		italicIdx := -1
		linkIdx := strings.IndexByte(remaining, '[')
		codeIdx := strings.IndexByte(remaining, '`')

		for i := 0; i < len(remaining); i++ {
			if remaining[i] == '*' {
				if i+1 < len(remaining) && remaining[i+1] == '*' {
					i++
					continue
				}
				italicIdx = i
				break
			}
		}

		earliest := -1
		for _, idx := range []int{boldIdx, italicIdx, linkIdx, codeIdx} {
			if idx >= 0 && (earliest < 0 || idx < earliest) {
				earliest = idx
			}
		}
		if earliest < 0 {
			out.WriteString(style.Render(remaining))
			break
		}

		if earliest > 0 {
			out.WriteString(style.Render(remaining[:earliest]))
		}

		switch {
		case boldIdx == earliest:
			close := strings.Index(remaining[earliest+2:], "**")
			if close < 0 || close == 0 {
				out.WriteString(style.Render("**"))
				remaining = remaining[earliest+2:]
				continue
			}
			inner := remaining[earliest+2 : earliest+2+close]
			out.WriteString(style.Copy().Bold(true).Render(inner))
			remaining = remaining[earliest+2+close+2:]

		case italicIdx == earliest:
			rest := remaining[earliest+1:]
			close := -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == '*' {
					if i+1 < len(rest) && rest[i+1] == '*' {
						i++
						continue
					}
					close = i
					break
				}
			}
			if close <= 0 {
				out.WriteString(style.Render("*"))
				remaining = remaining[earliest+1:]
				continue
			}
			inner := rest[:close]
			out.WriteString(style.Copy().Italic(true).Render(inner))
			remaining = rest[close+1:]

		case linkIdx == earliest:
			label, target, consumed, ok := parseMarkdownLink(remaining[earliest:])
			if !ok {
				out.WriteString(style.Render("["))
				remaining = remaining[earliest+1:]
				continue
			}
			out.WriteString(renderHyperlink(label, target, style))
			remaining = remaining[earliest+consumed:]

		case codeIdx == earliest:
			rest := remaining[earliest+1:]
			close := strings.IndexByte(rest, '`')
			if close <= 0 {
				out.WriteString(style.Render("`"))
				remaining = rest
				continue
			}
			inner := rest[:close]
			out.WriteString(codeStyle.Render(inner))
			remaining = rest[close+1:]
		}
	}
	return out.String()
}

func parseMarkdownLink(text string) (label, target string, consumed int, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", 0, false
	}
	closeLabelOffset := boundedIndexByte(text[1:], ']', markdownLinkLabelScanLimit)
	if closeLabelOffset < 0 {
		return "", "", 0, false
	}
	closeLabel := 1 + closeLabelOffset
	if closeLabel <= 1 {
		return "", "", 0, false
	}
	if closeLabel+1 >= len(text) || text[closeLabel+1] != '(' {
		return "", "", 0, false
	}
	targetText := text[closeLabel+2:]
	target, targetConsumed, ok := parseMarkdownLinkTarget(targetText)
	if !ok {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	if strings.TrimSpace(label) == "" || strings.TrimSpace(target) == "" {
		return "", "", 0, false
	}
	return label, target, closeLabel + 2 + targetConsumed, true
}

func parseMarkdownLinkTarget(text string) (target string, consumed int, ok bool) {
	leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	if leading >= len(text) || leading > markdownLinkTargetScanLimit {
		return "", 0, false
	}
	trimmed := text[leading:]
	if strings.HasPrefix(trimmed, "<") {
		closeAngle := boundedIndexByte(trimmed[1:], '>', markdownLinkTargetScanLimit)
		if closeAngle < 0 {
			return "", 0, false
		}
		target = trimmed[1 : 1+closeAngle]
		afterTarget := trimmed[1+closeAngle+1:]
		trailing := len(afterTarget) - len(strings.TrimLeftFunc(afterTarget, unicode.IsSpace))
		afterTarget = afterTarget[trailing:]
		if !strings.HasPrefix(afterTarget, ")") {
			return "", 0, false
		}
		return strings.TrimSpace(target), leading + 1 + closeAngle + 1 + trailing + 1, true
	}
	closeTarget := boundedIndexByte(trimmed, ')', markdownLinkTargetScanLimit)
	if closeTarget < 0 {
		return "", 0, false
	}
	return strings.TrimSpace(trimmed[:closeTarget]), leading + closeTarget + 1, true
}

// ExtractOpenLinks returns Markdown links with normalized system-open targets.
func ExtractOpenLinks(text string) []OpenLink {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	links := make([]OpenLink, 0)
	remaining := text
	for len(remaining) > 0 {
		idx := strings.IndexByte(remaining, '[')
		if idx < 0 {
			return links
		}
		label, target, consumed, ok := parseMarkdownLink(remaining[idx:])
		if !ok {
			remaining = remaining[idx+1:]
			continue
		}
		if localPath, ok := localLinkText(target); ok {
			openPath, _ := localOpenPath(localPath)
			if openPath != "" {
				links = append(links, OpenLink{
					Kind:     localOpenLinkKind(openPath, localPath),
					Label:    localLinkLabel(label, localPath),
					Target:   localPath,
					OpenPath: openPath,
				})
			}
		} else if externalTarget, ok := externalOpenLinkTarget(target); ok {
			links = append(links, OpenLink{
				Kind:     "url",
				Label:    strings.TrimSpace(label),
				Target:   externalTarget,
				OpenPath: externalTarget,
			})
		}
		remaining = remaining[idx+max(1, consumed):]
	}
	return links
}

func boundedIndexByte(text string, c byte, limit int) int {
	if limit > 0 && len(text) > limit {
		text = text[:limit]
	}
	return strings.IndexByte(text, c)
}

func renderHyperlink(label, target string, style lipgloss.Style) string {
	linkStyle := style.Copy().Foreground(lipgloss.Color("111")).Underline(true)
	if localPath, ok := localLinkText(target); ok {
		label = localLinkLabel(label, localPath)
		openPath, _ := localOpenPath(localPath)
		renderedLabel := linkStyle.Render(label)
		if openPath == "" {
			return renderedLabel
		}
		return ansi.SetHyperlink(openPath) + renderedLabel + ansi.ResetHyperlink()
	}
	target = hyperlinkTarget(target)
	renderedLabel := linkStyle.Render(label)
	if target == "" {
		return renderedLabel
	}
	return ansi.SetHyperlink(target) + renderedLabel + ansi.ResetHyperlink()
}

func hyperlinkTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return target
	}
	return parsed.String()
}

func externalOpenLinkTarget(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Scheme == "file" {
		return "", false
	}
	return parsed.String(), true
}

func localLinkText(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	if strings.HasPrefix(target, "/") {
		return localPathText(target, ""), true
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	pathText := localPathText(path, parsed.Fragment)
	if parsed.Host != "" && parsed.Host != "localhost" {
		pathText = parsed.Host + ":" + pathText
	}
	return pathText, path != ""
}

func localPathText(path, fragment string) string {
	if fragment == "" {
		if before, after, found := strings.Cut(path, "#"); found {
			path = before
			fragment = after
		}
	}
	if fragment != "" {
		if isLineFragment(fragment) {
			path += ":" + fragment
		} else {
			path += "#" + fragment
		}
	}
	return path
}

func isLineFragment(text string) bool {
	if text == "" {
		return false
	}
	for i, part := range strings.Split(text, ":") {
		if part == "" || i > 1 {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func localLinkLabel(label, target string) string {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if label == "" || label == target {
		label = filepath.Base(target)
		if label == "." || label == string(filepath.Separator) {
			label = target
		}
	}
	return label
}

func localOpenLinkKind(openPath, rawPath string) string {
	if _, location := localOpenPath(rawPath); location != "" {
		return "source"
	}
	if kind := artifactKindForPath(openPath); kind != "" {
		return kind
	}
	return "file"
}

func localOpenPath(target string) (path, location string) {
	path = strings.TrimSpace(target)
	if path == "" {
		return "", ""
	}
	if before, after, found := strings.Cut(path, "#"); found {
		path = before
		if after != "" {
			location = "#" + after
		}
	}
	path, lineLocation := splitLocalLineSuffix(path)
	if lineLocation != "" {
		location = lineLocation
	}
	path = unescapeLocalOpenPath(path)
	return path, location
}

func unescapeLocalOpenPath(path string) string {
	if !strings.Contains(path, "%") {
		return path
	}
	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return unescaped
}

func splitLocalLineSuffix(path string) (string, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ""
	}
	base, suffix, ok := cutTrailingNumberSuffix(path)
	if !ok {
		return path, ""
	}
	if nextBase, nextSuffix, ok := cutTrailingNumberSuffix(base); ok {
		return nextBase, nextSuffix + suffix
	}
	return base, suffix
}

func cutTrailingNumberSuffix(path string) (base, suffix string, ok bool) {
	idx := strings.LastIndexByte(path, ':')
	if idx <= 0 || idx == len(path)-1 {
		return path, "", false
	}
	tail := path[idx+1:]
	for _, r := range tail {
		if r < '0' || r > '9' {
			return path, "", false
		}
	}
	return path[:idx], path[idx:], true
}

func artifactKindForPath(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".heic", ".heif", ".svg":
		return "image"
	case ".pdf":
		return "pdf"
	case ".csv", ".tsv", ".xlsx", ".xls", ".ods", ".numbers":
		return "sheet"
	case ".doc", ".docx", ".pages", ".rtf", ".md", ".markdown":
		return "doc"
	case ".ppt", ".pptx", ".key":
		return "deck"
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar":
		return "archive"
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm":
		return "video"
	case ".mp3", ".wav", ".m4a", ".aac", ".flac":
		return "audio"
	case ".html", ".htm":
		return "html"
	default:
		return ""
	}
}

func syntaxHighlightBlock(text, languageHint, filename string, opts syntaxHighlightOptions) string {
	return newSyntaxHighlightPlan(languageHint, filename, text).Render(text, opts)
}

type syntaxHighlightPlan struct {
	lexer chroma.Lexer
}

func newSyntaxHighlightPlan(languageHint, filename, sampleText string) syntaxHighlightPlan {
	return syntaxHighlightPlan{
		lexer: syntaxHighlightPreparedLexer(languageHint, filename, sampleText),
	}
}

func (plan syntaxHighlightPlan) Render(text string, opts syntaxHighlightOptions) string {
	return syntaxHighlightWithLexer(text, plan.lexer, opts)
}

func syntaxHighlightPreparedLexer(languageHint, filename, text string) chroma.Lexer {
	if shouldBypassSyntaxHighlighting(languageHint, filename, text) {
		return nil
	}
	lexer := syntaxHighlightLexer(languageHint, filename, text)
	if lexer == nil {
		return nil
	}
	return chroma.Coalesce(lexer)
}

func shouldBypassSyntaxHighlighting(languageHint, filename, text string) bool {
	if syntaxHighlightTextTooLarge(text) {
		return true
	}
	if normalizedSyntaxLanguageHint(languageHint) == "" && normalizedSyntaxFilename(filename) == "" {
		return true
	}
	return false
}

func syntaxHighlightTextTooLarge(text string) bool {
	if len(text) > syntaxHighlightMaxBytes {
		return true
	}
	if 1+strings.Count(text, "\n") > syntaxHighlightMaxLines {
		return true
	}
	return false
}

func normalizedSyntaxLanguageHint(languageHint string) string {
	return strings.TrimSpace(strings.TrimPrefix(languageHint, "."))
}

func normalizedSyntaxFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return ""
	}
	name := strings.TrimSpace(filepath.Base(filename))
	if name == "." {
		return ""
	}
	return name
}

func syntaxHighlightWithLexer(text string, lexer chroma.Lexer, opts syntaxHighlightOptions) string {
	if text == "" {
		return ""
	}
	if lexer == nil {
		return syntaxHighlightPlainText(text, opts)
	}
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return syntaxHighlightPlainText(text, opts)
	}

	var out strings.Builder
	for token := iterator(); token != chroma.EOF; token = iterator() {
		if token.Value == "" {
			continue
		}
		out.WriteString(renderSyntaxToken(token.Value, syntaxTokenStyle(token.Type, opts)))
	}
	return out.String()
}

func syntaxHighlightPlainText(text string, opts syntaxHighlightOptions) string {
	style := lipgloss.NewStyle()
	if opts.DefaultColor != "" {
		style = style.Foreground(opts.DefaultColor)
	}
	if opts.BackgroundColor != "" {
		style = style.Background(opts.BackgroundColor)
	}
	return renderSyntaxToken(text, style)
}

func syntaxHighlightLexer(languageHint, filename, text string) chroma.Lexer {
	hint := normalizedSyntaxLanguageHint(languageHint)
	if hint != "" {
		switch strings.ToLower(hint) {
		case "text", "plain", "plaintext", "txt":
			return nil
		}
		lexer := lexers.Get(strings.ToLower(hint))
		if lexer == nil || lexer == lexers.Fallback {
			return nil
		}
		return lexer
	}
	name := normalizedSyntaxFilename(filename)
	if name != "" {
		if lexer := lexers.Match(name); lexer != nil && lexer != lexers.Fallback {
			return lexer
		}
	}
	if lexer := lexers.Analyse(text); lexer != nil && lexer != lexers.Fallback {
		return lexer
	}
	return nil
}

func syntaxTokenStyle(tokenType chroma.TokenType, opts syntaxHighlightOptions) lipgloss.Style {
	style := lipgloss.NewStyle()
	if opts.DefaultColor != "" {
		style = style.Foreground(opts.DefaultColor)
	}
	if opts.BackgroundColor != "" {
		style = style.Background(opts.BackgroundColor)
	}

	switch {
	case tokenType.InCategory(chroma.Comment):
		s := style.Foreground(lipgloss.Color("#75715e"))
		if !opts.NoItalic {
			s = s.Italic(true)
		}
		return s
	case tokenType.InCategory(chroma.Keyword):
		return style.Foreground(lipgloss.Color("#f92672")).Bold(true)
	case tokenType == chroma.NameFunction || tokenType == chroma.NameFunctionMagic:
		return style.Foreground(lipgloss.Color("#a6e22e")).Bold(true)
	case tokenType == chroma.NameClass || tokenType == chroma.NameNamespace || tokenType == chroma.NameTag || tokenType == chroma.NameDecorator:
		return style.Foreground(lipgloss.Color("#a6e22e"))
	case tokenType.InSubCategory(chroma.NameBuiltin) || tokenType == chroma.NameBuiltin:
		return style.Foreground(lipgloss.Color("#66d9ef"))
	case tokenType.InCategory(chroma.LiteralString):
		return style.Foreground(lipgloss.Color("#e6db74"))
	case tokenType.InCategory(chroma.LiteralNumber):
		return style.Foreground(lipgloss.Color("#be84ff"))
	case tokenType.InCategory(chroma.Operator):
		return style.Foreground(lipgloss.Color("#f92672"))
	case tokenType == chroma.Punctuation || tokenType.InCategory(chroma.Punctuation):
		return style.Foreground(lipgloss.Color("#f8f8f2"))
	case tokenType.InCategory(chroma.Generic):
		return style.Foreground(lipgloss.Color("#fd971f"))
	default:
		return style
	}
}

func renderSyntaxToken(text string, style lipgloss.Style) string {
	if text == "" {
		return ""
	}
	parts := strings.SplitAfter(text, "\n")
	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "\n") {
			out.WriteString(style.Render(strings.TrimSuffix(part, "\n")))
			out.WriteByte('\n')
			continue
		}
		out.WriteString(style.Render(part))
	}
	return out.String()
}
