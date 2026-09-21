package claudestyle

import (
	"os"
	"path/filepath"
	"testing"
)

func writeStyle(t *testing.T, root, file, body string) {
	t.Helper()
	dir := filepath.Join(root, StylesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

func TestDiscoverReturnsDefaultWhenNoStylesExist(t *testing.T) {
	options := Discover(t.TempDir(), t.TempDir())
	if len(options) != 1 {
		t.Fatalf("expected only the built-in style, got %d: %v", len(options), Names(options))
	}
	if options[0].Name != DefaultName || options[0].Source != SourceBuiltIn {
		t.Fatalf("unexpected built-in option: %+v", options[0])
	}
}

// Claude Code addresses a style by its frontmatter name, so a style whose file
// name differs must still be listed under the frontmatter name.
func TestDiscoverUsesFrontMatterNameNotFileName(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "zzprobe.md", "---\nname: ProbeStyleName\ndescription: probe desc\n---\n\nbody\n")

	option, ok := Find(Discover(home, t.TempDir()), "ProbeStyleName")
	if !ok {
		t.Fatalf("expected the frontmatter name to be discoverable")
	}
	if option.Description != "probe desc" {
		t.Fatalf("description = %q, want %q", option.Description, "probe desc")
	}
	if option.Source != SourceUser {
		t.Fatalf("source = %q, want %q", option.Source, SourceUser)
	}
	if _, ok := Find(Discover(home, t.TempDir()), "zzprobe"); ok {
		t.Fatalf("file name must not be selectable; Claude Code would ignore it")
	}
}

// A style file without a frontmatter name cannot be selected in Claude Code, so
// offering it would be an option that silently does nothing.
func TestDiscoverSkipsStyleWithoutFrontMatterName(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "broken.md", "no frontmatter here\n")
	writeStyle(t, home, "empty-name.md", "---\ndescription: only a description\n---\n")

	if names := Names(Discover(home, t.TempDir())); len(names) != 1 {
		t.Fatalf("expected unnamed styles to be skipped, got %v", names)
	}
}

func TestDiscoverProjectStyleShadowsUserStyleWithSameName(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeStyle(t, home, "terse.md", "---\nname: Terse\ndescription: user copy\n---\n")
	writeStyle(t, filepath.Join(project, ".claude"), "terse.md", "---\nname: Terse\ndescription: project copy\n---\n")

	options := Discover(home, project)
	option, ok := Find(options, "Terse")
	if !ok {
		t.Fatalf("expected Terse to be discoverable, got %v", Names(options))
	}
	if option.Source != SourceProject || option.Description != "project copy" {
		t.Fatalf("project style should shadow the user style, got %+v", option)
	}
	if got := len(options); got != 2 {
		t.Fatalf("expected default plus one merged style, got %d: %v", got, Names(options))
	}
}

// Claude Code's own lookup is case-sensitive, so Find must be too. Resolve is
// the forgiving entry point that turns typed input into the exact stored name
// before it reaches the CLI, which would otherwise accept a wrong-case name
// and apply no style at all.
func TestFindIsCaseSensitiveAndResolveIsNot(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "terse.md", "---\nname: Terse\n---\n")
	options := Discover(home, t.TempDir())

	if _, ok := Find(options, "terse"); ok {
		t.Fatalf("Find must not match a different case")
	}
	option, ambiguous, ok := Resolve(options, "terse")
	if !ok {
		t.Fatalf("Resolve should match a different case, ambiguous=%v", Names(ambiguous))
	}
	if option.Name != "Terse" {
		t.Fatalf("Resolve returned %q, want the exact stored name %q", option.Name, "Terse")
	}
}

func TestResolveRejectsUnknownName(t *testing.T) {
	options := Discover(t.TempDir(), t.TempDir())
	if _, _, ok := Resolve(options, "Nope"); ok {
		t.Fatal("Resolve matched a style that does not exist")
	}
}

// Two styles differing only by case cannot be disambiguated from a loose
// spelling, and guessing would silently apply the wrong one.
func TestResolveReportsCaseOnlyCollisionAsAmbiguous(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "a.md", "---\nname: Terse\n---\n")
	writeStyle(t, home, "b.md", "---\nname: terse\n---\n")
	options := Discover(home, t.TempDir())

	_, ambiguous, ok := Resolve(options, "TERSE")
	if ok {
		t.Fatal("Resolve should not guess between two case-only variants")
	}
	if len(ambiguous) != 2 {
		t.Fatalf("ambiguous = %v, want both candidates", Names(ambiguous))
	}
}

// An exact hit must win outright, so someone who types the precise name never
// gets an ambiguity error caused by a differently-cased sibling.
func TestResolvePrefersExactMatchOverCaseOnlySibling(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "a.md", "---\nname: Terse\n---\n")
	writeStyle(t, home, "b.md", "---\nname: terse\n---\n")
	options := Discover(home, t.TempDir())

	option, _, ok := Resolve(options, "terse")
	if !ok || option.Name != "terse" {
		t.Fatalf("Resolve(%q) = %q ok=%v, want the exact match", "terse", option.Name, ok)
	}
}

func TestIsDefaultTreatsEmptyAsDefault(t *testing.T) {
	for _, name := range []string{"", "   ", DefaultName} {
		if !IsDefault(name) {
			t.Fatalf("IsDefault(%q) = false, want true", name)
		}
	}
	if IsDefault("Terse") {
		t.Fatalf("IsDefault(%q) = true, want false", "Terse")
	}
}

func TestDiscoverIgnoresNonMarkdownEntries(t *testing.T) {
	home := t.TempDir()
	writeStyle(t, home, "notes.txt", "---\nname: NotAStyle\n---\n")
	writeStyle(t, home, "real.md", "---\nname: Real\n---\n")
	if err := os.MkdirAll(filepath.Join(home, StylesDirName, "nested.md"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	names := Names(Discover(home, t.TempDir()))
	if len(names) != 2 || names[1] != "Real" {
		t.Fatalf("expected only the markdown style, got %v", names)
	}
}
