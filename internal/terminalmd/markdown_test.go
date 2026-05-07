package terminalmd

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderBodyKeepsLocalMarkdownLinksCompact(t *testing.T) {
	t.Parallel()

	path := "/tmp/demo/manager.go:107"
	rendered := RenderBody("Changed [manager.go]("+path+").", lipgloss.Color("252"), 100)
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("rendered link should not target the line-suffixed path: %q", rendered)
	}
	if !strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/manager.go")) {
		t.Fatalf("rendered link should target the openable local file: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered local link should use the raw local path target: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) || strings.Contains(stripped, "[manager.go]") {
		t.Fatalf("rendered text should hide local markdown target syntax: %q", stripped)
	}
	if !strings.Contains(stripped, "Changed manager.go.") {
		t.Fatalf("rendered text should keep the compact link label: %q", stripped)
	}
}

func TestRenderBodyUnwrapsAngleBracketLocalMarkdownLinks(t *testing.T) {
	t.Parallel()

	path := "/tmp/lcroom mockups/notes.md"
	rendered := RenderBody("Open [notes](<"+path+">).", lipgloss.Color("252"), 100)
	if !strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("rendered link should target the unwrapped local path: %q", rendered)
	}
	if strings.Contains(rendered, "%3C") || strings.Contains(rendered, "%3E") || strings.Contains(rendered, "%20") {
		t.Fatalf("rendered local link should not encode angle markers or spaces: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) || strings.Contains(stripped, "[notes]") {
		t.Fatalf("rendered text should hide angle-bracket target syntax: %q", stripped)
	}
	if !strings.Contains(stripped, "Open notes.") {
		t.Fatalf("rendered text should keep the compact link label: %q", stripped)
	}
}

func TestRenderBodyKeepsExternalMarkdownLinksClickable(t *testing.T) {
	t.Parallel()

	rendered := RenderBody("See [docs](https://example.com/docs).", lipgloss.Color("252"), 100)
	if !strings.Contains(rendered, ansi.SetHyperlink("https://example.com/docs")) {
		t.Fatalf("rendered external link should include a hyperlink escape: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "https://example.com/docs") || strings.Contains(stripped, "[docs]") {
		t.Fatalf("rendered text should hide external markdown target syntax: %q", stripped)
	}
	if !strings.Contains(stripped, "See docs.") {
		t.Fatalf("rendered text should keep the external link label: %q", stripped)
	}
}
