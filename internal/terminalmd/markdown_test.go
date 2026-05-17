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

func TestRenderBodyDecodesPercentEscapedLocalMarkdownPaths(t *testing.T) {
	t.Parallel()

	path := "/tmp/Family Room/jun_it_citizenship/Italian B1 certificate.pdf"
	encodedPath := strings.ReplaceAll(path, " ", "%20")
	rendered := RenderBody("Open [Italian B1 certificate]("+encodedPath+").", lipgloss.Color("252"), 120)
	if !strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("rendered link should target the decoded local path %q: %q", path, rendered)
	}
	if strings.Contains(rendered, ansi.SetHyperlink(encodedPath)) || strings.Contains(rendered, "%20") {
		t.Fatalf("rendered local link should not keep percent-escaped spaces: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Open Italian B1 certificate.") {
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

func TestRenderBodyWrapsMarkdownTableCellsWithoutTruncation(t *testing.T) {
	t.Parallel()

	longValue := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"
	table := "| Name | Value |\n| --- | --- |\n| item | " + longValue + " |"
	rendered := ansi.Strip(RenderBody(table, lipgloss.Color("252"), 40))
	if strings.Contains(rendered, "…") || strings.Contains(rendered, "...") {
		t.Fatalf("table cells should wrap instead of truncating: %q", rendered)
	}
	compact := markdownTableReadableText(rendered)
	if !strings.Contains(compact, longValue) {
		t.Fatalf("wrapped table should preserve full cell contents, got %q from %q", compact, rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 40 {
			t.Fatalf("wrapped table line width = %d, want <= 40: %q", width, line)
		}
	}
}

func TestExtractOpenLinksUsesOpenableLocalPath(t *testing.T) {
	t.Parallel()

	path := "/tmp/demo/manager.go:107"
	links := ExtractOpenLinks("Changed [manager.go](" + path + ") and [docs](https://example.com/docs).")
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2: %#v", len(links), links)
	}
	if links[0].Kind != "source" || links[0].Label != "manager.go" || links[0].OpenPath != "/tmp/demo/manager.go" {
		t.Fatalf("local link = %#v, want source manager.go with line suffix stripped", links[0])
	}
	if links[1].Kind != "url" || links[1].OpenPath != "https://example.com/docs" {
		t.Fatalf("external link = %#v, want openable URL", links[1])
	}
}

func TestExtractOpenLinksDecodesPercentEscapedLocalPath(t *testing.T) {
	t.Parallel()

	path := "/tmp/Family Room/jun_it_citizenship/Italian B1 certificate.pdf"
	encodedPath := strings.ReplaceAll(path, " ", "%20")
	links := ExtractOpenLinks("Open [Italian B1 certificate](" + encodedPath + ").")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1: %#v", len(links), links)
	}
	if links[0].Kind != "pdf" || links[0].Label != "Italian B1 certificate" || links[0].OpenPath != path {
		t.Fatalf("local link = %#v, want decoded PDF path %q", links[0], path)
	}
}

func markdownTableReadableText(text string) string {
	replacer := strings.NewReplacer(
		"\n", "",
		"\t", "",
		" ", "",
		"│", "",
		"├", "",
		"┤", "",
		"─", "",
		"┼", "",
	)
	return replacer.Replace(text)
}
