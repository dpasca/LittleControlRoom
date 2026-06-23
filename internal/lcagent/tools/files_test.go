package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lcroom/internal/lcagent/policy"
)

func TestFileToolsReadListAndSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha\nbeta needle\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.txt"), []byte("needle in docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	read := files.Read("README.md", 2, 1)
	if !read.Success {
		t.Fatalf("read failed: %s", read.Error)
	}
	if !strings.Contains(read.Output, "lines: 2-2") || !strings.Contains(read.Output, "2 | beta needle") {
		t.Fatalf("read output = %q", read.Output)
	}
	if !strings.Contains(read.Output, "sha256: ") || !strings.Contains(read.Output, fileSHA256Hex([]byte("alpha\nbeta needle\ngamma\n"))) {
		t.Fatalf("read output missing sha256:\n%s", read.Output)
	}
	if !strings.Contains(read.Output, "total_lines: 3") || !strings.Contains(read.Output, "has_more: true") || !strings.Contains(read.Output, "next_offset: 3") {
		t.Fatalf("read metadata missing:\n%s", read.Output)
	}

	list := files.List(".", "*.md", 20)
	if !list.Success {
		t.Fatalf("list failed: %s", list.Error)
	}
	if !strings.Contains(list.Output, "README.md") || strings.Contains(list.Output, "note.txt") {
		t.Fatalf("list output = %q", list.Output)
	}

	search := files.Search("needle", ".", "*.txt", 20)
	if !search.Success {
		t.Fatalf("search failed: %s", search.Error)
	}
	if !strings.Contains(search.Output, "docs/note.txt:1: needle in docs") || strings.Contains(search.Output, "README.md") {
		t.Fatalf("search output = %q", search.Output)
	}
	if !strings.Contains(search.Output, "match_type: literal_substring_case_insensitive") {
		t.Fatalf("search output missing match type:\n%s", search.Output)
	}

	searchWithContext := files.SearchContext("needle", "README.md", "", 20, 1, 1)
	if !searchWithContext.Success {
		t.Fatalf("search with context failed: %s", searchWithContext.Error)
	}
	for _, want := range []string{"README.md:2: beta needle", "  1 | alpha", "> 2 | beta needle", "  3 | gamma"} {
		if !strings.Contains(searchWithContext.Output, want) {
			t.Fatalf("search context missing %q:\n%s", want, searchWithContext.Output)
		}
	}

	compactSearch := files.SearchContextWithOptions("needle", "README.md", "", 20, 2, 2, SearchOptions{
		OutputMode: "compact",
		Intent:     "find the smallest relevant match list",
	})
	if !compactSearch.Success {
		t.Fatalf("compact search failed: %s", compactSearch.Error)
	}
	for _, want := range []string{"intent: find the smallest relevant match list", "output_mode: compact", "README.md:2: beta needle"} {
		if !strings.Contains(compactSearch.Output, want) {
			t.Fatalf("compact search missing %q:\n%s", want, compactSearch.Output)
		}
	}
	if strings.Contains(compactSearch.Output, "  1 | alpha") || strings.Contains(compactSearch.Output, "> 2 | beta needle") {
		t.Fatalf("compact search should suppress context:\n%s", compactSearch.Output)
	}

	binary := files.Read("image.bin", 1, 20)
	if binary.Success || !binary.Binary || !strings.Contains(binary.Error, "binary file suppressed") {
		t.Fatalf("binary read = %#v, want suppressed failure", binary)
	}
}

func TestFileToolsTreatsUTF8BoundaryAsText(t *testing.T) {
	root := t.TempDir()
	// Put the first byte of a multi-byte rune exactly at the 4096-byte binary
	// sniff boundary. The file is valid UTF-8, so it must remain readable.
	source := strings.Repeat("a", 4095) + "┌ decorative comment\n\nint main() {\n    return 0;\n}\n"
	if err := os.WriteFile(filepath.Join(root, "skate.cpp"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	read := files.Read("skate.cpp", 1, 10)
	if !read.Success || read.Binary {
		t.Fatalf("read = %#v, want valid UTF-8 source text", read)
	}
	search := files.Search("int main", ".", "*.cpp", 10)
	if !search.Success || !strings.Contains(search.Output, "skate.cpp:3: int main()") {
		t.Fatalf("search = %#v", search)
	}
	outline := files.Outline("skate.cpp")
	if !outline.Success || !strings.Contains(outline.Output, "type: c/c++") {
		t.Fatalf("outline = %#v", outline)
	}
}

func TestFileToolsListGlobCaseSensitivity(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tools", "render_sprites")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte("# Handoff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	exact := files.List(".", "**/handoff.md", 20)
	if !exact.Success {
		t.Fatalf("exact list failed: %s", exact.Error)
	}
	if strings.Contains(exact.Output, "Tools/render_sprites/HANDOFF.md") {
		t.Fatalf("case-sensitive glob should not match different case:\n%s", exact.Output)
	}
	if !strings.Contains(exact.Output, "match_type: glob_case_sensitive") {
		t.Fatalf("case-sensitive list missing match type:\n%s", exact.Output)
	}

	caseSensitive := false
	forgiving := files.ListWithOptions(".", "**/handoff.md", 20, ListOptions{CaseSensitive: &caseSensitive})
	if !forgiving.Success {
		t.Fatalf("case-insensitive list failed: %s", forgiving.Error)
	}
	for _, want := range []string{"match_type: glob_case_insensitive", "Tools/render_sprites/HANDOFF.md"} {
		if !strings.Contains(forgiving.Output, want) {
			t.Fatalf("case-insensitive list missing %q:\n%s", want, forgiving.Output)
		}
	}
}

func TestFileToolsScoutPackReadsBoundedGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "tui", "app.go"), []byte("package tui\n\nfunc updateCodexMode() {}\nfunc other() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "tui", "note.md"), []byte("# note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	pack := files.ScoutPack("internal/tui", ScoutPackOptions{
		FileGlob:        "*.go",
		MaxFiles:        5,
		MaxLinesPerFile: 3,
		Question:        "find embedded Enter handling",
	})
	if !pack.Success {
		t.Fatalf("ScoutPack failed: %s", pack.Error)
	}
	for _, want := range []string{"scout_pack: true", "question: find embedded Enter handling", "## internal/tui/app.go", "3 | func updateCodexMode()"} {
		if !strings.Contains(pack.Output, want) {
			t.Fatalf("ScoutPack missing %q:\n%s", want, pack.Output)
		}
	}
	if strings.Contains(pack.Output, "note.md") || strings.Contains(pack.Output, "func other") {
		t.Fatalf("ScoutPack should obey glob and line bounds:\n%s", pack.Output)
	}
}

func TestFileToolsHonorsCustomReadLimits(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&body, "line %02d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{
		Workspace: w,
		Limits: FileLimits{
			DefaultReadLineLimit: 3,
			MaxReadLineLimit:     5,
		},
	}

	readDefault := files.Read("big.txt", 1, 0)
	if !readDefault.Success {
		t.Fatalf("read default failed: %s", readDefault.Error)
	}
	if !strings.Contains(readDefault.Output, "lines: 1-3") || !strings.Contains(readDefault.Output, "read truncated after 3 lines") {
		t.Fatalf("default read did not honor custom limit:\n%s", readDefault.Output)
	}

	readCapped := files.Read("big.txt", 1, 100)
	if !readCapped.Success {
		t.Fatalf("read capped failed: %s", readCapped.Error)
	}
	if !strings.Contains(readCapped.Output, "lines: 1-5") || !strings.Contains(readCapped.Output, "read truncated after 5 lines") {
		t.Fatalf("capped read did not honor custom max:\n%s", readCapped.Output)
	}
}

func TestFileToolsAllowAbsoluteInspectionOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	note := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(note, []byte("needle outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}
	outsideDisplay := filepath.ToSlash(filepath.Clean(outside))
	noteDisplay := filepath.ToSlash(filepath.Clean(note))

	read := files.Read(note, 1, 20)
	if !read.Success {
		t.Fatalf("absolute read failed: %s", read.Error)
	}
	if !strings.Contains(read.Output, "file: "+noteDisplay) || strings.Contains(read.Output, "../") {
		t.Fatalf("absolute read used wrong display path:\n%s", read.Output)
	}

	list := files.List(outside, "", 20)
	if !list.Success {
		t.Fatalf("absolute list failed: %s", list.Error)
	}
	if !strings.Contains(list.Output, "path: "+outsideDisplay) || !strings.Contains(list.Output, noteDisplay) {
		t.Fatalf("absolute list output =\n%s", list.Output)
	}

	search := files.Search("needle", outside, "", 20)
	if !search.Success {
		t.Fatalf("absolute search failed: %s", search.Error)
	}
	if !strings.Contains(search.Output, noteDisplay+":1: needle outside") {
		t.Fatalf("absolute search output =\n%s", search.Output)
	}

	overview := files.RepoOverview(outside, RepoOverviewOptions{MaxFiles: 20})
	if !overview.Success {
		t.Fatalf("absolute repo overview failed: %s", overview.Error)
	}
	if !strings.Contains(overview.Output, "path: "+outsideDisplay) || !strings.Contains(overview.Output, noteDisplay) {
		t.Fatalf("absolute repo overview output =\n%s", overview.Output)
	}
}

func TestFileToolsListAndSearchReportHiddenDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", ".venv", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("needle hidden git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".venv", "note.py"), []byte("needle hidden venv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.py"), []byte("needle visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	list := files.List(".", "", 20)
	if !list.Success {
		t.Fatalf("list failed: %s", list.Error)
	}
	for _, want := range []string{".git/ [hidden by default", ".venv/ [hidden by default", "hidden_dirs: 2", "src/"} {
		if !strings.Contains(list.Output, want) {
			t.Fatalf("list missing %q:\n%s", want, list.Output)
		}
	}
	if strings.Contains(list.Output, ".git/HEAD") || strings.Contains(list.Output, ".venv/note.py") {
		t.Fatalf("list descended into hidden dirs by default:\n%s", list.Output)
	}

	search := files.SearchContext("needle", ".", "", 20, 0, 0)
	if !search.Success {
		t.Fatalf("search failed: %s", search.Error)
	}
	if !strings.Contains(search.Output, "src/app.py:1: needle visible") || !strings.Contains(search.Output, "hidden_dirs_skipped: 2") {
		t.Fatalf("search did not report visible match and skipped dirs:\n%s", search.Output)
	}
	if strings.Contains(search.Output, ".git/HEAD") || strings.Contains(search.Output, ".venv/note.py") {
		t.Fatalf("search descended into hidden dirs by default:\n%s", search.Output)
	}

	searchHidden := files.SearchContextWithOptions("needle", ".", "", 20, 0, 0, SearchOptions{IncludeHidden: true})
	if !searchHidden.Success {
		t.Fatalf("search hidden failed: %s", searchHidden.Error)
	}
	for _, want := range []string{".git/HEAD:1: needle hidden git", ".venv/note.py:1: needle hidden venv", "src/app.py:1: needle visible"} {
		if !strings.Contains(searchHidden.Output, want) {
			t.Fatalf("search hidden missing %q:\n%s", want, searchHidden.Output)
		}
	}
}

func TestFileToolsSearchDeniesBroadHomeWithoutGlob(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	result := files.SearchContext("needle", ".", "", 20, 0, 0)
	if result.Success || !strings.Contains(result.Error, "broad home-directory search requires a narrower path or file_glob") {
		t.Fatalf("broad home search result = %#v", result)
	}

	withGlob := files.SearchContext("needle", ".", "*.txt", 20, 0, 0)
	if !withGlob.Success || !strings.Contains(withGlob.Output, "note.txt:1: needle") {
		t.Fatalf("home search with glob = %#v\n%s", withGlob, withGlob.Output)
	}
}

func TestFileToolsSearchStopsAtTraversalBudget(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.txt", i)), []byte("haystack\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{
		Workspace: w,
		Limits: FileLimits{
			DefaultSearchFileBudget:   2,
			MaxSearchFileBudget:       2,
			DefaultSearchTimeBudgetMS: 1000,
			MaxSearchTimeBudgetMS:     1000,
		},
	}

	result := files.SearchContext("needle", ".", "*.txt", 20, 0, 0)
	if !result.Success || !result.Truncated {
		t.Fatalf("budgeted search result = %#v\n%s", result, result.Output)
	}
	for _, want := range []string{"files_seen: 2", "files_searched: 2", "search stopped: file budget reached after 2 files"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("budgeted search missing %q:\n%s", want, result.Output)
		}
	}
}

func TestFileToolsRepoOverviewFilesystemReportsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".venv", "build", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.py"), []byte("def main():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".venv", "hidden.py"), []byte("hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "build", "bundle.js"), []byte("hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	overview := files.RepoOverview(".", RepoOverviewOptions{MaxFiles: 20})
	if !overview.Success {
		t.Fatalf("repo overview failed: %s", overview.Error)
	}
	for _, want := range []string{"path: .", "source: filesystem", "git: false", ".venv/ [hidden by default", "build/ [hidden by default", "hidden_dirs_skipped: 2", "README.md", "src/app.py", "Python"} {
		if !strings.Contains(overview.Output, want) {
			t.Fatalf("repo overview missing %q:\n%s", want, overview.Output)
		}
	}
	if strings.Contains(overview.Output, ".venv/hidden.py") || strings.Contains(overview.Output, "build/bundle.js") {
		t.Fatalf("repo overview descended into hidden dirs by default:\n%s", overview.Output)
	}

	hiddenPlaceholder := files.RepoOverview(".venv", RepoOverviewOptions{})
	if !hiddenPlaceholder.Success {
		t.Fatalf("hidden repo overview failed: %s", hiddenPlaceholder.Error)
	}
	if !strings.Contains(hiddenPlaceholder.Output, "source: hidden placeholder") || !strings.Contains(hiddenPlaceholder.Output, ".venv/ [hidden by default") || strings.Contains(hiddenPlaceholder.Output, "hidden.py") {
		t.Fatalf("hidden placeholder output =\n%s", hiddenPlaceholder.Output)
	}

	withHidden := files.RepoOverview(".", RepoOverviewOptions{IncludeHidden: true, MaxFiles: 20})
	if !withHidden.Success {
		t.Fatalf("repo overview include hidden failed: %s", withHidden.Error)
	}
	for _, want := range []string{".venv/hidden.py", "build/bundle.js"} {
		if !strings.Contains(withHidden.Output, want) {
			t.Fatalf("repo overview include hidden missing %q:\n%s", want, withHidden.Output)
		}
	}
}

func TestFileToolsRepoOverviewGitIncludesTrackedAndUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	for _, dir := range []string{".github/workflows", ".venv", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	filesToWrite := map[string]string{
		"README.md":                "# Demo\n",
		"package.json":             `{"scripts":{"test":"echo ok"}}` + "\n",
		".github/workflows/ci.yml": "name: ci\n",
		"src/lib.rs":               "pub fn demo() {}\n",
		"src/main.cpp":             "int main() { return 0; }\n",
		"notes.txt":                "untracked visible\n",
		".venv/hidden.py":          "untracked hidden\n",
	}
	for name, body := range filesToWrite {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitForTest(t, root, "init")
	runGitForTest(t, root, "add", "README.md", "package.json", ".github/workflows/ci.yml", "src/lib.rs", "src/main.cpp")

	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	overview := FileTools{Workspace: w}.RepoOverview(".", RepoOverviewOptions{MaxFiles: 30})
	if !overview.Success {
		t.Fatalf("repo overview failed: %s", overview.Error)
	}
	for _, want := range []string{"git: true", "source: git ls-files + untracked", "tracked_files: 5", "dirty: true", ".git/ [hidden by default", ".venv/ [hidden by default", "hidden_dirs_skipped:", "README.md", "package.json", ".github/workflows/ci.yml", "src/lib.rs", "src/main.cpp", "notes.txt", "Node/JavaScript package", "Rust", "C/C++"} {
		if !strings.Contains(overview.Output, want) {
			t.Fatalf("repo overview missing %q:\n%s", want, overview.Output)
		}
	}
	if strings.Contains(overview.Output, ".venv/hidden.py") {
		t.Fatalf("repo overview included hidden untracked file by default:\n%s", overview.Output)
	}
}

func TestTextEditorReplaceText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    "README.md",
		OldText: "old\n",
		NewText: "new\n",
	})
	if !result.Success {
		t.Fatalf("replace failed: %s", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\nkeep\n" {
		t.Fatalf("README = %q", data)
	}
	if got := strings.Join(result.FilesTouched, ","); got != "README.md" {
		t.Fatalf("FilesTouched = %v", result.FilesTouched)
	}
	if result.PatchSummary == nil || result.PatchSummary.TotalAddedLines != 1 || result.PatchSummary.TotalDeletedLines != 1 {
		t.Fatalf("PatchSummary = %#v", result.PatchSummary)
	}
	if !strings.Contains(result.DiffSummary, "README.md: replace +1 -1") {
		t.Fatalf("diff summary = %q", result.DiffSummary)
	}
}

func TestTextEditorCreateFileWritesExactContent(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	content := "first\nsecond\n"
	result := TextEditor{Workspace: w}.CreateFile(CreateFileSpec{
		Path:    "docs/new.txt",
		Content: content,
	})
	if !result.Success {
		t.Fatalf("create_file failed: %s", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("new file content = %q", data)
	}
	if got := strings.Join(result.FilesTouched, ","); got != "docs/new.txt" {
		t.Fatalf("FilesTouched = %v", result.FilesTouched)
	}
	if result.PatchSummary == nil || result.PatchSummary.TotalAddedLines != 2 || result.PatchSummary.TotalDeletedLines != 0 {
		t.Fatalf("PatchSummary = %#v", result.PatchSummary)
	}
	if !strings.Contains(result.Output, fileSHA256Hex([]byte(content))) {
		t.Fatalf("create_file output missing sha256:\n%s", result.Output)
	}
}

func TestTextEditorCreateFileRejectsExistingTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.CreateFile(CreateFileSpec{
		Path:    "README.md",
		Content: "new\n",
	})
	if result.Success {
		t.Fatal("create_file succeeded for existing file, want failure")
	}
	if !strings.Contains(result.Error, "target already exists") || !strings.Contains(result.Error, "replace_file") {
		t.Fatalf("error = %q", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("existing file changed: %q", data)
	}
}

func TestTextEditorReplaceFileRequiresExpectedSHA256(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}

	missing := TextEditor{Workspace: w}.ReplaceFile(ReplaceFileSpec{
		Path:    "README.md",
		Content: "new\n",
	})
	if missing.Success || !strings.Contains(missing.Error, "expected_sha256") {
		t.Fatalf("missing guard result = %#v", missing)
	}

	stale := TextEditor{Workspace: w}.ReplaceFile(ReplaceFileSpec{
		Path:           "README.md",
		Content:        "new\n",
		ExpectedSHA256: strings.Repeat("0", 64),
	})
	if stale.Success {
		t.Fatal("replace_file succeeded with stale sha, want failure")
	}
	if !strings.Contains(stale.Error, "sha256 guard mismatch") || stale.PatchFailure == nil || stale.PatchFailure.Stage != "replace_file" {
		t.Fatalf("stale guard result = %#v", stale)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("file changed despite stale guard: %q", data)
	}
}

func TestTextEditorReplaceFileRewritesWithHashGuard(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	old := "old\nkeep\n"
	if err := os.WriteFile(path, []byte(old), 0o640); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	newBody := "new\nbody\n"
	result := TextEditor{Workspace: w}.ReplaceFile(ReplaceFileSpec{
		Path:           "README.md",
		Content:        newBody,
		ExpectedSHA256: fileSHA256Hex([]byte(old)),
	})
	if !result.Success {
		t.Fatalf("replace_file failed: %s", result.Error)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newBody {
		t.Fatalf("README = %q", data)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %#o, want 0640", got)
	}
	if got := strings.Join(result.FilesTouched, ","); got != "README.md" {
		t.Fatalf("FilesTouched = %v", result.FilesTouched)
	}
	if result.PatchSummary == nil || result.PatchSummary.TotalAddedLines != 2 || result.PatchSummary.TotalDeletedLines != 2 {
		t.Fatalf("PatchSummary = %#v", result.PatchSummary)
	}
	if !strings.Contains(result.Output, fileSHA256Hex([]byte(newBody))) || !strings.Contains(result.Output, fileSHA256Hex([]byte(old))) {
		t.Fatalf("replace_file output missing hashes:\n%s", result.Output)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestTextEditorReplaceTextRequiresExpectedOccurrences(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    "README.md",
		OldText: "same\n",
		NewText: "changed\n",
	})
	if result.Success {
		t.Fatal("replace succeeded with duplicate old_text, want failure")
	}
	if !strings.Contains(result.Error, "found 2 occurrences") || !strings.Contains(result.Error, "expected 1") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestTextEditorReplaceTextRejectsHugeSpan(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < replaceTextMaxLines+1; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    "README.md",
		OldText: body.String(),
		NewText: "short\n",
	})
	if result.Success {
		t.Fatal("replace_text succeeded for huge span, want guidance failure")
	}
	if !strings.Contains(result.Error, "too large") || !strings.Contains(result.Error, "replace_lines") {
		t.Fatalf("error = %q", result.Error)
	}
	if result.PatchFailure == nil || result.PatchFailure.Stage != "replace_text" {
		t.Fatalf("PatchFailure = %#v", result.PatchFailure)
	}
}

func TestTextEditorReplaceLinesDeletesRangeWithGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("keep-a\nold-a\nold-b\nkeep-b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceLines(ReplaceLinesSpec{
		Path:              "README.md",
		StartLine:         2,
		EndLine:           3,
		NewText:           "",
		ExpectedFirstLine: "old-a",
		ExpectedLastLine:  "old-b",
	})
	if !result.Success {
		t.Fatalf("replace_lines failed: %s", result.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep-a\nkeep-b\n" {
		t.Fatalf("README = %q", data)
	}
	if result.PatchSummary == nil || result.PatchSummary.TotalAddedLines != 0 || result.PatchSummary.TotalDeletedLines != 2 {
		t.Fatalf("PatchSummary = %#v", result.PatchSummary)
	}
}

func TestTextEditorReplaceLinesRejectsStaleGuard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("keep\ncurrent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceLines(ReplaceLinesSpec{
		Path:              "README.md",
		StartLine:         2,
		EndLine:           2,
		NewText:           "new\n",
		ExpectedFirstLine: "stale",
	})
	if result.Success {
		t.Fatal("replace_lines succeeded with stale guard, want failure")
	}
	if !strings.Contains(result.Error, "first-line guard mismatch") {
		t.Fatalf("error = %q", result.Error)
	}
	if result.PatchFailure == nil || result.PatchFailure.Stage != "replace_lines" || len(result.PatchFailure.SuggestedReads) != 1 {
		t.Fatalf("PatchFailure = %#v", result.PatchFailure)
	}
}

func TestTextEditorReplaceTextDeniesAutoOff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    "README.md",
		OldText: "old\n",
		NewText: "new\n",
	})
	if result.Success {
		t.Fatal("replace succeeded with auto off, want denial")
	}
	if !result.Denied || !strings.Contains(result.DenialReason, "replace_text denied with --auto off") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
	}
}

func TestTextEditorReplaceTextDeniesAbsolutePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    target,
		OldText: "old\n",
		NewText: "new\n",
	})
	if result.Success {
		t.Fatal("replace_text absolute path succeeded, want denial")
	}
	if !result.Denied || !strings.Contains(result.DenialReason, "--admin-write") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("outside file changed: %q", data)
	}
}

func TestTextEditorAdminWriteAllowsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	w.AdminWrite = true
	result := TextEditor{Workspace: w}.ReplaceText(ReplaceTextSpec{
		Path:    target,
		OldText: "old\n",
		NewText: "new\n",
	})
	if !result.Success {
		t.Fatalf("replace_text failed: %s", result.Error)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("outside file = %q", data)
	}
}

func TestFileToolsOutlineGoAndMarkdown(t *testing.T) {
	root := t.TempDir()
	goBody := `package demo

import (
	"context"
	"fmt"
)

type Runner struct{}

const statusReady = "ready"

func Run(ctx context.Context) error {
	return nil
}

func (r *Runner) String() string {
	return fmt.Sprint(statusReady)
}
`
	if err := os.WriteFile(filepath.Join(root, "demo.go"), []byte(goBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mdBody := "# Title\n\nIntro\n\n## Details\n\nBody\n\n### Deep\n\nMore\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(mdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "mod", "worker.go"), []byte("package mod\n\nfunc Work() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "skip.go"), []byte("package vendor\n\nfunc Skip() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	goOutline := files.Outline("demo.go")
	if !goOutline.Success {
		t.Fatalf("go outline failed: %s", goOutline.Error)
	}
	for _, want := range []string{"type: go", "package: demo", "imports: 2", "type Runner lines", "const statusReady lines", "func Run lines", "method *Runner.String lines"} {
		if !strings.Contains(goOutline.Output, want) {
			t.Fatalf("go outline missing %q:\n%s", want, goOutline.Output)
		}
	}

	mdOutline := files.Outline("README.md")
	if !mdOutline.Success {
		t.Fatalf("markdown outline failed: %s", mdOutline.Error)
	}
	for _, want := range []string{"type: markdown", "h1 lines 1-11: Title", "h2 lines 5-11: Details", "h3 lines 9-11: Deep"} {
		if !strings.Contains(mdOutline.Output, want) {
			t.Fatalf("markdown outline missing %q:\n%s", want, mdOutline.Output)
		}
	}

	moduleOutline := files.ModuleOutline(".", "*.go", 10)
	if !moduleOutline.Success {
		t.Fatalf("module outline failed: %s", moduleOutline.Error)
	}
	for _, want := range []string{"path: .", "files: 2", "file: demo.go", "file: internal/mod/worker.go", "func Work lines"} {
		if !strings.Contains(moduleOutline.Output, want) {
			t.Fatalf("module outline missing %q:\n%s", want, moduleOutline.Output)
		}
	}
	if strings.Contains(moduleOutline.Output, "vendor/skip.go") {
		t.Fatalf("module outline should skip vendor:\n%s", moduleOutline.Output)
	}
}

func TestFileToolsOutlineCommonLanguages(t *testing.T) {
	root := t.TempDir()
	filesToWrite := map[string]string{
		"app.py": `class Worker:
    async def run(self):
        pass

def helper():
    pass
`,
		"view.tsx": `export interface Props {
  title: string
}

export function Page(props: Props) {
  return null
}

export const useThing = () => null
`,
		"lib.rs": `pub struct Runner;

impl Runner {
    pub async fn run(&self) {}
}

pub fn helper() {}
`,
		"demo.cpp": `namespace demo {
class Widget {
public:
  void draw();
};

int add(int left, int right) {
  return left + right;
}
}
`,
		"Service.cs": `public class Service
{
    public async Task RunAsync()
    {
    }
}
`,
	}
	for name, body := range filesToWrite {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}

	cases := []struct {
		path string
		want []string
	}{
		{"app.py", []string{"type: python", "class Worker lines", "async func run lines", "func helper lines"}},
		{"view.tsx", []string{"type: typescript", "interface Props lines", "func Page lines", "func useThing lines"}},
		{"lib.rs", []string{"type: rust", "struct Runner lines", "impl Runner lines", "async fn run lines", "fn helper lines"}},
		{"demo.cpp", []string{"type: c/c++", "namespace demo lines", "class Widget lines", "func add lines"}},
		{"Service.cs", []string{"type: csharp", "class Service lines", "method RunAsync lines"}},
	}
	for _, tt := range cases {
		outline := files.Outline(tt.path)
		if !outline.Success {
			t.Fatalf("%s outline failed: %s", tt.path, outline.Error)
		}
		for _, want := range tt.want {
			if !strings.Contains(outline.Output, want) {
				t.Fatalf("%s outline missing %q:\n%s", tt.path, want, outline.Output)
			}
		}
	}

	moduleOutline := files.ModuleOutline(".", "", 20)
	if !moduleOutline.Success {
		t.Fatalf("module outline failed: %s", moduleOutline.Error)
	}
	for _, want := range []string{"file: app.py", "file: view.tsx", "file: lib.rs", "file: demo.cpp", "file: Service.cs"} {
		if !strings.Contains(moduleOutline.Output, want) {
			t.Fatalf("module outline missing %q:\n%s", want, moduleOutline.Output)
		}
	}
}

func TestFileToolsDenyWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}
	if result := files.Read("../outside.txt", 1, 20); result.Success || !result.Denied {
		t.Fatalf("read outside workspace succeeded: %#v", result)
	}
	if result := files.List("../outside", "", 20); result.Success || !result.Denied {
		t.Fatalf("list outside workspace succeeded: %#v", result)
	}
	if result := files.Search("needle", "../outside", "", 20); result.Success || !result.Denied {
		t.Fatalf("search outside workspace succeeded: %#v", result)
	}
}

func TestFileToolsDenySymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires elevated privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	files := FileTools{Workspace: w}
	if result := files.Read("outside/secret.txt", 1, 20); result.Success || !result.Denied {
		t.Fatalf("read through symlink escape succeeded: %#v", result)
	}
}
