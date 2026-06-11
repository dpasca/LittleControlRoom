package tui

import (
	"strings"
	"testing"
)

func TestSyntaxHighlightPreparedLexerSkipsContentOnlyInference(t *testing.T) {
	lexer := syntaxHighlightPreparedLexer("", "", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	if lexer != nil {
		t.Fatalf("expected nil lexer when no language hint or filename is available")
	}
}

func TestSyntaxHighlightLexerUsesCppFilenameHint(t *testing.T) {
	lexer := syntaxHighlightLexer("", "src/main.cpp", "int main() {\n    return 0;\n}\n")
	if lexer == nil {
		t.Fatalf("expected a lexer for C++ source")
	}
	if got := strings.ToLower(lexer.Config().Name); !strings.Contains(got, "c++") {
		t.Fatalf("lexer name = %q, want a C++ lexer", lexer.Config().Name)
	}
}

func TestSyntaxHighlightPreparedLexerSkipsLargeTypedBlock(t *testing.T) {
	large := strings.Repeat("fmt.Println(\"hello\")\n", syntaxHighlightMaxLines+5)
	lexer := syntaxHighlightPreparedLexer("go", "", large)
	if lexer != nil {
		t.Fatalf("expected nil lexer for oversized highlighted block")
	}
}

func TestSyntaxHighlightBlockFallsBackToPlainTextForLargeTypedBlock(t *testing.T) {
	large := strings.Repeat("fmt.Println(\"hello\")\n", syntaxHighlightMaxLines+5)
	rendered := syntaxHighlightBlock(large, "go", "", syntaxHighlightOptions{})
	if rendered != large {
		t.Fatalf("large highlighted block should fall back to plain text")
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatalf("plain-text fallback should not include ANSI styling: %q", rendered[:min(40, len(rendered))])
	}
}
