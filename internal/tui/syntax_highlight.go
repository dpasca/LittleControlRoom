package tui

import (
	"path/filepath"
	"strings"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
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

type syntaxHighlightPlan struct {
	lexer chroma.Lexer
}

func syntaxHighlightBlock(text, languageHint, filename string, opts syntaxHighlightOptions) string {
	return newSyntaxHighlightPlan(languageHint, filename, text).Render(text, opts)
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
