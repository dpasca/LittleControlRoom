package terminalmd

import (
	"net/url"
	"path/filepath"
	"strings"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	syntaxHighlightMaxBytes = 12 * 1024
	syntaxHighlightMaxLines = 240
)

type syntaxHighlightOptions struct {
	DefaultColor    lipgloss.Color
	BackgroundColor lipgloss.Color
	NoItalic        bool
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
			if len(cell) > colWidths[j] {
				colWidths[j] = len(cell)
			}
		}
	}

	tableOverhead := 2 + numCols*3 - 1
	totalWidth := tableOverhead
	for _, w := range colWidths {
		totalWidth += w
	}
	if totalWidth > maxWidth && maxWidth > tableOverhead+numCols {
		available := maxWidth - tableOverhead
		for i, w := range colWidths {
			remaining := numCols - i
			maxCol := available / remaining
			if maxCol < 1 {
				maxCol = 1
			}
			if w > maxCol {
				colWidths[i] = maxCol
			}
			available -= colWidths[i]
			if available < 0 {
				available = 0
			}
		}
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
		parts := make([]string, numCols)
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(cells) {
				cell = cells[j]
			}
			w := colWidths[j]
			if len(cell) > w {
				if w > 1 {
					cell = cell[:w-1] + "…"
				} else {
					cell = "…"
				}
			}
			pad := w - len(cell)
			if pad < 0 {
				pad = 0
			}
			padded := cell + strings.Repeat(" ", pad)
			if isHeader {
				parts[j] = headerStyle.Render(padded)
			} else {
				parts[j] = cellStyle.Render(padded)
			}
		}
		sep := borderStyle.Render(" │ ")
		out = append(out, borderStyle.Render("│ ")+strings.Join(parts, sep)+borderStyle.Render(" │"))
	}
	return out
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
		Foreground(lipgloss.Color("223")).
		Background(lipgloss.Color("236"))
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
	closeLabel := strings.Index(text, "](")
	if closeLabel <= 1 {
		return "", "", 0, false
	}
	closeTarget := strings.IndexByte(text[closeLabel+2:], ')')
	if closeTarget < 0 {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	target = text[closeLabel+2 : closeLabel+2+closeTarget]
	if strings.TrimSpace(label) == "" || strings.TrimSpace(target) == "" {
		return "", "", 0, false
	}
	return label, target, closeLabel + 3 + closeTarget, true
}

func renderHyperlink(label, target string, style lipgloss.Style) string {
	target = hyperlinkTarget(target)
	linkStyle := style.Copy().Foreground(lipgloss.Color("111")).Underline(true)
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
	if strings.HasPrefix(target, "/") {
		path := target
		fragment := ""
		if before, after, found := strings.Cut(path, "#"); found {
			path = before
			fragment = after
		}
		return (&url.URL{Scheme: "file", Path: path, Fragment: fragment}).String()
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return target
	}
	return parsed.String()
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
