package projectrun

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestPathCompletionBuildsDirectoryAndExecutableSuggestions(t *testing.T) {
	t.Parallel()

	rootQuery, ok := ParsePathCompletion("./too", len([]rune("./too")))
	if !ok {
		t.Fatal("ParsePathCompletion() should recognize a project-relative path")
	}
	if rootQuery.Directory != "." || rootQuery.NamePrefix != "too" || !rootQuery.FirstWord {
		t.Fatalf("root query = %#v", rootQuery)
	}
	if !rootQuery.Explicit {
		t.Fatal("./ path should be explicit")
	}
	rootSuggestions := rootQuery.Suggestions([]PathCompletionEntry{
		{Name: "tools", Directory: true},
		{Name: "todo.txt"},
	})
	if got := suggestionCommands(rootSuggestions); !slices.Equal(got, []string{"./tools/"}) {
		t.Fatalf("root suggestions = %#v, want ./tools/", got)
	}

	fileQuery, ok := ParsePathCompletion("./tools/bu", len([]rune("./tools/bu")))
	if !ok {
		t.Fatal("ParsePathCompletion() should recognize a nested project path")
	}
	if fileQuery.Directory != "tools" || fileQuery.NamePrefix != "bu" || !fileQuery.FirstWord {
		t.Fatalf("file query = %#v", fileQuery)
	}
	fileSuggestions := fileQuery.Suggestions([]PathCompletionEntry{
		{Name: "build and run.sh", Executable: true},
		{Name: "build-notes.txt"},
	})
	if got := suggestionCommands(fileSuggestions); !slices.Equal(got, []string{`./tools/build\ and\ run.sh`}) {
		t.Fatalf("file suggestions = %#v", got)
	}
}

func TestPathCompletionStartsFromBareProjectDirectoryPrefix(t *testing.T) {
	t.Parallel()

	rootQuery, ok := ParsePathCompletion("too", len([]rune("too")))
	if !ok {
		t.Fatal("ParsePathCompletion() should consider a bare first word as a root path prefix")
	}
	if rootQuery.Directory != "." || rootQuery.NamePrefix != "too" || !rootQuery.FirstWord || rootQuery.Explicit {
		t.Fatalf("bare root query = %#v", rootQuery)
	}
	rootSuggestions := rootQuery.Suggestions([]PathCompletionEntry{
		{Name: "tools", Directory: true},
		{Name: "todo.txt"},
		{Name: "tool.sh", Executable: true},
	})
	if got := suggestionCommands(rootSuggestions); !slices.Equal(got, []string{"tools/"}) {
		t.Fatalf("bare root suggestions = %#v, want tools/", got)
	}

	fileQuery, ok := ParsePathCompletion("tools/bu", len([]rune("tools/bu")))
	if !ok || !fileQuery.Explicit {
		t.Fatalf("nested bare path should have explicit path intent: %#v, ok=%v", fileQuery, ok)
	}
	fileSuggestions := fileQuery.Suggestions([]PathCompletionEntry{
		{Name: "build and run.sh", Executable: true},
	})
	if got := suggestionCommands(fileSuggestions); !slices.Equal(got, []string{`tools/build\ and\ run.sh`}) {
		t.Fatalf("bare nested suggestions = %#v", got)
	}
}

func TestPathCompletionSupportsArgumentsAndOpenQuotes(t *testing.T) {
	t.Parallel()

	query, ok := ParsePathCompletion(`bash './tools/bu`, len([]rune(`bash './tools/bu`)))
	if !ok {
		t.Fatal("ParsePathCompletion() should recognize an open quoted argument")
	}
	if query.Directory != "tools" || query.NamePrefix != "bu" || query.FirstWord {
		t.Fatalf("quoted argument query = %#v", query)
	}

	suggestions := query.Suggestions([]PathCompletionEntry{
		{Name: "build and run.sh"},
	})
	if got := suggestionCommands(suggestions); !slices.Equal(got, []string{`bash './tools/build and run.sh'`}) {
		t.Fatalf("quoted suggestions = %#v", got)
	}

	boundaryQuery, ok := ParsePathCompletion("echo ready && ./to", len([]rune("echo ready && ./to")))
	if !ok || !boundaryQuery.FirstWord {
		t.Fatalf("path after command boundary should be a first word: %#v, ok=%v", boundaryQuery, ok)
	}

	bareArgumentQuery, ok := ParsePathCompletion("bash tools/bu", len([]rune("bash tools/bu")))
	if !ok || !bareArgumentQuery.Explicit || bareArgumentQuery.FirstWord {
		t.Fatalf("bare nested argument should be an explicit path: %#v, ok=%v", bareArgumentQuery, ok)
	}
}

func TestPathCompletionRejectsOutsideAndMidlinePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		cursor  int
	}{
		{name: "parent", command: "./../secret", cursor: len([]rune("./../secret"))},
		{name: "nested parent", command: "./tools/../../secret", cursor: len([]rune("./tools/../../secret"))},
		{name: "absolute", command: "/tmp/secret", cursor: len([]rune("/tmp/secret"))},
		{name: "not path", command: "pnpm dev", cursor: len([]rune("pnpm dev"))},
		{name: "cursor in middle", command: "./tools/run.sh", cursor: 4},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if query, ok := ParsePathCompletion(test.command, test.cursor); ok {
				t.Fatalf("ParsePathCompletion() = %#v, true; want no query", query)
			}
		})
	}
}

func TestReadPathCompletionEntriesStaysWithinProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tools := filepath.Join(root, "tools")
	if err := os.MkdirAll(filepath.Join(tools, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	executableName := "run.sh"
	if runtime.GOOS == "windows" {
		executableName = "run.cmd"
	}
	if err := os.WriteFile(filepath.Join(tools, executableName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tools, "notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	outside := t.TempDir()
	outsideLink := filepath.Join(root, "outside")
	if err := os.Symlink(outside, outsideLink); err != nil && runtime.GOOS != "windows" {
		t.Fatalf("symlink outside project: %v", err)
	}

	entries, err := ReadPathCompletionEntries(root, "tools")
	if err != nil {
		t.Fatalf("ReadPathCompletionEntries() error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		if entry.Name == executableName && !entry.Executable {
			t.Fatalf("%s should be marked executable", executableName)
		}
	}
	for _, want := range []string{"nested", "notes.txt", executableName} {
		if !slices.Contains(names, want) {
			t.Fatalf("entries = %#v, missing %q", entries, want)
		}
	}

	if _, err := ReadPathCompletionEntries(root, "../"); err == nil {
		t.Fatal("ReadPathCompletionEntries() should reject a parent directory")
	}
	if _, err := os.Lstat(outsideLink); err == nil {
		rootEntries, err := ReadPathCompletionEntries(root, ".")
		if err != nil {
			t.Fatalf("read root entries: %v", err)
		}
		for _, entry := range rootEntries {
			if entry.Name == "outside" {
				t.Fatalf("outside symlink should not be offered: %#v", rootEntries)
			}
		}
		if _, err := ReadPathCompletionEntries(root, "outside"); err == nil {
			t.Fatal("ReadPathCompletionEntries() should reject a symlink outside the project")
		}
	}
}
