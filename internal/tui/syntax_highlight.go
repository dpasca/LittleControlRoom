package tui

import (
	"path/filepath"
	"strings"

	chroma "github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
)

type syntaxHighlightOptions struct {
	DefaultColor    lipgloss.Color
	BackgroundColor lipgloss.Color
}

func syntaxHighlightBlock(text, languageHint, filename string, opts syntaxHighlightOptions) string {
	if text == "" {
		return ""
	}
	lexer := syntaxHighlightLexer(languageHint, filename, text)
	if lexer == nil {
		return syntaxHighlightPlainText(text, opts)
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
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
	hint := strings.TrimSpace(strings.TrimPrefix(languageHint, "."))
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
	name := strings.TrimSpace(filepath.Base(filename))
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
		return style.Foreground(lipgloss.Color("244")).Italic(true)
	case tokenType.InCategory(chroma.Keyword):
		return style.Foreground(lipgloss.Color("81")).Bold(true)
	case tokenType == chroma.NameFunction || tokenType == chroma.NameFunctionMagic:
		return style.Foreground(lipgloss.Color("117")).Bold(true)
	case tokenType == chroma.NameClass || tokenType == chroma.NameNamespace || tokenType == chroma.NameTag || tokenType == chroma.NameDecorator:
		return style.Foreground(lipgloss.Color("117"))
	case tokenType.InSubCategory(chroma.NameBuiltin) || tokenType == chroma.NameBuiltin:
		return style.Foreground(lipgloss.Color("141"))
	case tokenType.InCategory(chroma.LiteralString):
		return style.Foreground(lipgloss.Color("120"))
	case tokenType.InCategory(chroma.LiteralNumber):
		return style.Foreground(lipgloss.Color("179"))
	case tokenType.InCategory(chroma.Operator):
		return style.Foreground(lipgloss.Color("215"))
	case tokenType == chroma.Punctuation || tokenType.InCategory(chroma.Punctuation):
		return style.Foreground(lipgloss.Color("252"))
	case tokenType.InCategory(chroma.Generic):
		return style.Foreground(lipgloss.Color("221"))
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
