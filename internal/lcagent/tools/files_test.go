package tools

import (
	"fmt"
	"os"
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

	binary := files.Read("image.bin", 1, 20)
	if binary.Success || !binary.Binary || !strings.Contains(binary.Error, "binary file suppressed") {
		t.Fatalf("binary read = %#v, want suppressed failure", binary)
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
