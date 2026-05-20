package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultRepoOverviewFileLimit = 120
	maxRepoOverviewFileLimit     = 500
	repoOverviewGitTimeout       = 2 * time.Second
)

type RepoOverviewOptions struct {
	IncludeHidden bool
	MaxFiles      int
}

type repoGitInfo struct {
	Present          bool
	Root             string
	Branch           string
	TrackedFiles     []string
	UntrackedFiles   []string
	ModifiedCount    int
	UntrackedCount   int
	RenamedCount     int
	DeletedCount     int
	DirtyKnown       bool
	TrackedKnown     bool
	DetectionMessage string
}

func (t FileTools) RepoOverview(path string, opts RepoOverviewOptions) ToolResult {
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	root := target
	rootRel := rel
	if !info.IsDir() {
		root = filepath.Dir(target)
		rootRel = t.relative(root)
	}
	opts.MaxFiles = clampInt(opts.MaxFiles, defaultRepoOverviewFileLimit, maxRepoOverviewFileLimit)
	hiddenRoot := !opts.IncludeHidden && rootRel != "." && pathHasDefaultHiddenSegment(rootRel)

	git := t.repoGitInfo(root)
	files, source, hiddenDirs := t.repoOverviewFiles(root, rootRel, git, opts, hiddenRoot)
	topCounts := repoTopLevelCounts(files, rootRel)
	extCounts := repoExtensionCounts(files)
	important := repoImportantFiles(files)
	hints := repoProjectHints(important, extCounts)

	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", rootRel)
	fmt.Fprintf(&b, "source: %s\n", source)
	if git.Present {
		fmt.Fprintf(&b, "git: true\n")
		fmt.Fprintf(&b, "git_root: %s\n", t.relative(git.Root))
		if git.Branch != "" {
			fmt.Fprintf(&b, "branch: %s\n", git.Branch)
		}
		if git.TrackedKnown {
			fmt.Fprintf(&b, "tracked_files: %d\n", len(git.TrackedFiles))
		}
		if git.DirtyKnown {
			dirty := git.ModifiedCount > 0 || git.UntrackedCount > 0 || git.RenamedCount > 0 || git.DeletedCount > 0
			fmt.Fprintf(&b, "dirty: %t\n", dirty)
			fmt.Fprintf(&b, "modified_files: %d\n", git.ModifiedCount)
			fmt.Fprintf(&b, "untracked_files: %d\n", git.UntrackedCount)
			fmt.Fprintf(&b, "renamed_files: %d\n", git.RenamedCount)
			fmt.Fprintf(&b, "deleted_files: %d\n", git.DeletedCount)
		}
	} else {
		fmt.Fprintf(&b, "git: false\n")
		if git.DetectionMessage != "" {
			fmt.Fprintf(&b, "git_note: %s\n", git.DetectionMessage)
		}
	}
	if len(hiddenDirs) > 0 {
		fmt.Fprintf(&b, "hidden_dirs_skipped: %d (%s)\n", len(hiddenDirs), strings.Join(trimStrings(hiddenDirs, 12), ", "))
		fmt.Fprintf(&b, "hidden_note: set include_hidden=true to include hidden/generated directories.\n")
	}
	b.WriteByte('\n')

	b.WriteString("tree:\n")
	treeLines := t.repoOverviewTree(root, rootRel, opts.IncludeHidden, hiddenRoot, 80)
	if len(treeLines) == 0 {
		b.WriteString("- empty\n")
	} else {
		for _, line := range treeLines {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}

	writeRepoOverviewSection(&b, "top_level_counts", topCounts, 20)
	writeRepoOverviewSection(&b, "extension_counts", extCounts, 20)
	writeStringSection(&b, "important_files", important, 40)
	writeStringSection(&b, "project_hints", hints, 20)
	writeStringSection(&b, "suggested_next", repoSuggestedNext(rootRel, important, hints, hiddenDirs), 20)

	sample := repoRepresentativeFiles(files, important, opts.MaxFiles)
	writeStringSection(&b, "file_sample", sample, opts.MaxFiles)
	if len(files) > len(sample) {
		fmt.Fprintf(&b, "\n--- file sample truncated after %d of %d files ---\n", len(sample), len(files))
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: len(files) > len(sample)}
}

func (t FileTools) repoGitInfo(dir string) repoGitInfo {
	root, err := runGitRead(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return repoGitInfo{DetectionMessage: strings.TrimSpace(err.Error())}
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return repoGitInfo{DetectionMessage: "git root unavailable"}
	}
	info := repoGitInfo{Present: true, Root: root}
	if branch, err := runGitRead(root, "branch", "--show-current"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}
	if info.Branch == "" {
		if head, err := runGitRead(root, "rev-parse", "--short", "HEAD"); err == nil {
			head = strings.TrimSpace(head)
			if head != "" {
				info.Branch = "detached@" + head
			}
		}
	}
	if tracked, err := runGitRead(root, "ls-files", "-z"); err == nil {
		info.TrackedFiles = splitNUL(tracked)
		info.TrackedKnown = true
	}
	if status, err := runGitRead(root, "status", "--porcelain=v1", "-z"); err == nil {
		info.DirtyKnown = true
		for _, entry := range gitStatusEntries(status) {
			if len(entry) < 2 {
				continue
			}
			code := entry[:2]
			switch {
			case strings.HasPrefix(code, "??"):
				info.UntrackedCount++
				if file := strings.TrimSpace(strings.TrimPrefix(entry[2:], " ")); file != "" {
					info.UntrackedFiles = append(info.UntrackedFiles, filepath.ToSlash(file))
				}
			case strings.Contains(code, "R"):
				info.RenamedCount++
			case strings.Contains(code, "D"):
				info.DeletedCount++
			default:
				info.ModifiedCount++
			}
		}
	}
	return info
}

func (t FileTools) repoOverviewFiles(root, rootRel string, git repoGitInfo, opts RepoOverviewOptions, hiddenRoot bool) ([]string, string, []string) {
	if hiddenRoot {
		return nil, "hidden placeholder", []string{strings.TrimSuffix(rootRel, "/") + "/"}
	}
	if git.Present && git.TrackedKnown {
		files := make([]string, 0, len(git.TrackedFiles)+len(git.UntrackedFiles))
		seen := map[string]bool{}
		for _, file := range git.TrackedFiles {
			display := t.gitFileToWorkspaceRel(git.Root, file)
			if !repoFileUnderRoot(display, rootRel) {
				continue
			}
			addRepoOverviewFile(&files, seen, display)
		}
		source := "git ls-files"
		for _, file := range git.UntrackedFiles {
			if !opts.IncludeHidden && pathHasDefaultHiddenSegment(file) {
				continue
			}
			display := t.gitFileToWorkspaceRel(git.Root, file)
			if !repoFileUnderRoot(display, rootRel) {
				continue
			}
			addRepoOverviewFile(&files, seen, display)
			source = "git ls-files + untracked"
		}
		sort.Strings(files)
		return files, source, t.repoOverviewHiddenDirs(root, opts.IncludeHidden, 80)
	}
	files, hiddenDirs := t.repoOverviewWalkFiles(root, opts.IncludeHidden)
	sort.Strings(files)
	return files, "filesystem", hiddenDirs
}

func addRepoOverviewFile(files *[]string, seen map[string]bool, file string) {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if file == "" || seen[file] {
		return
	}
	seen[file] = true
	*files = append(*files, file)
}

func (t FileTools) repoOverviewWalkFiles(root string, includeHidden bool) ([]string, []string) {
	files := []string{}
	hiddenDirs := []string{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == root || entry == nil {
			return nil
		}
		if entry.IsDir() {
			if !includeHidden && defaultHiddenDir(entry.Name()) {
				hiddenDirs = append(hiddenDirs, strings.TrimSuffix(t.relative(path), "/")+"/")
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		files = append(files, t.relative(path))
		return nil
	})
	return files, hiddenDirs
}

func (t FileTools) repoOverviewHiddenDirs(root string, includeHidden bool, maxDirs int) []string {
	if includeHidden || maxDirs <= 0 {
		return nil
	}
	hiddenDirs := []string{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == root || entry == nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if defaultHiddenDir(entry.Name()) {
			hiddenDirs = append(hiddenDirs, strings.TrimSuffix(t.relative(path), "/")+"/")
			return filepath.SkipDir
		}
		if len(hiddenDirs) >= maxDirs {
			return filepath.SkipDir
		}
		return nil
	})
	return hiddenDirs
}

func (t FileTools) repoOverviewTree(root, rootRel string, includeHidden, hiddenRoot bool, maxEntries int) []string {
	if hiddenRoot {
		return []string{strings.TrimSuffix(rootRel, "/") + "/ [hidden by default; set include_hidden=true to descend]"}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	lines := []string{}
	for _, entry := range entries {
		name := entry.Name()
		display := name
		if entry.IsDir() {
			display += "/"
		}
		if entry.IsDir() && !includeHidden && defaultHiddenDir(name) {
			display += " [hidden by default; set include_hidden=true to descend]"
		}
		lines = append(lines, display)
	}
	sort.Strings(lines)
	if len(lines) > maxEntries {
		return append(lines[:maxEntries], fmt.Sprintf("... +%d more", len(lines)-maxEntries))
	}
	return lines
}

func (t FileTools) gitFileToWorkspaceRel(gitRoot, gitRel string) string {
	if relRoot := t.relative(gitRoot); relRoot != "." {
		return filepath.ToSlash(filepath.Join(relRoot, gitRel))
	}
	return filepath.ToSlash(gitRel)
}

func runGitRead(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoOverviewGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func splitNUL(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, filepath.ToSlash(part))
		}
	}
	return out
}

func gitStatusEntries(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\x00")
	entries := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := strings.TrimSpace(parts[i])
		if entry == "" {
			continue
		}
		entries = append(entries, entry)
		if len(entry) >= 2 && (strings.Contains(entry[:2], "R") || strings.Contains(entry[:2], "C")) {
			i++
		}
	}
	return entries
}

func repoFileUnderRoot(file, root string) bool {
	root = filepath.ToSlash(strings.TrimSpace(root))
	file = filepath.ToSlash(strings.TrimSpace(file))
	if root == "" || root == "." {
		return true
	}
	return file == root || strings.HasPrefix(file, strings.TrimSuffix(root, "/")+"/")
}

func repoTopLevelCounts(files []string, root string) []string {
	counts := map[string]int{}
	root = filepath.ToSlash(strings.TrimSpace(root))
	prefix := ""
	if root != "" && root != "." {
		prefix = strings.TrimSuffix(root, "/") + "/"
	}
	for _, file := range files {
		rel := strings.TrimPrefix(filepath.ToSlash(file), prefix)
		part := rel
		if i := strings.Index(part, "/"); i >= 0 {
			part = part[:i] + "/"
		}
		if part != "" {
			counts[part]++
		}
	}
	return sortedCountLines(counts)
}

func repoExtensionCounts(files []string) []string {
	counts := map[string]int{}
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if ext == "" {
			ext = "[no extension]"
		}
		counts[ext]++
	}
	return sortedCountLines(counts)
}

func sortedCountLines(counts map[string]int) []string {
	type item struct {
		Name  string
		Count int
	}
	items := make([]item, 0, len(counts))
	for name, count := range counts {
		items = append(items, item{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprintf("%s: %d", item.Name, item.Count))
	}
	return out
}

func repoImportantFiles(files []string) []string {
	important := []string{}
	seen := map[string]bool{}
	for _, file := range files {
		if repoImportantFile(file) && !seen[file] {
			seen[file] = true
			important = append(important, file)
		}
	}
	sort.Strings(important)
	return important
}

func repoImportantFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	slashPath := filepath.ToSlash(path)
	switch base {
	case "readme", "readme.md", "agents.md", "package.json", "pnpm-lock.yaml", "package-lock.json", "yarn.lock", "bun.lock", "bun.lockb", "go.mod", "go.work", "cargo.toml", "cargo.lock", "pyproject.toml", "requirements.txt", "setup.py", "uv.lock", "poetry.lock", "cmakelists.txt", "makefile", "justfile", "taskfile.yml", "taskfile.yaml", "dockerfile", "docker-compose.yml", "docker-compose.yaml", "firebase.json", "tsconfig.json", "tailwind.config.ts", "tailwind.config.js", "vite.config.ts", "vite.config.js", "next.config.ts", "next.config.js", "next.config.mjs", "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "package.swift", ".gitlab-ci.yml":
		return true
	}
	if (strings.HasPrefix(slashPath, ".github/workflows/") || strings.Contains(slashPath, "/.github/workflows/")) && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")) {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sln", ".csproj", ".xcodeproj", ".uproject":
		return true
	default:
		return false
	}
}

func repoProjectHints(important []string, extCounts []string) []string {
	hints := []string{}
	hasImportant := func(name string) bool {
		for _, file := range important {
			if strings.EqualFold(filepath.Base(file), name) {
				return true
			}
		}
		return false
	}
	hasExt := func(ext string) bool {
		for _, line := range extCounts {
			if strings.HasPrefix(line, ext+": ") {
				return true
			}
		}
		return false
	}
	if hasImportant("next.config.ts") || hasImportant("next.config.js") || hasImportant("next.config.mjs") {
		hints = append(hints, "Next.js")
	}
	if hasImportant("package.json") {
		hints = append(hints, "Node/JavaScript package")
	}
	if hasImportant("go.mod") || hasExt(".go") {
		hints = append(hints, "Go")
	}
	if hasImportant("Cargo.toml") || hasExt(".rs") {
		hints = append(hints, "Rust")
	}
	if hasImportant("CMakeLists.txt") || hasExt(".c") || hasExt(".cc") || hasExt(".cpp") || hasExt(".cxx") || hasExt(".h") || hasExt(".hpp") {
		hints = append(hints, "C/C++")
	}
	if hasExt(".cs") || hasExt(".csproj") || hasExt(".sln") {
		hints = append(hints, ".NET/C#")
	}
	if hasImportant("pyproject.toml") || hasImportant("requirements.txt") || hasExt(".py") {
		hints = append(hints, "Python")
	}
	if hasImportant("pom.xml") || hasImportant("build.gradle") || hasExt(".java") {
		hints = append(hints, "Java/JVM")
	}
	if hasImportant("build.gradle.kts") || hasExt(".kt") {
		hints = append(hints, "Kotlin")
	}
	if hasImportant("Package.swift") || hasExt(".swift") {
		hints = append(hints, "Swift")
	}
	if len(hints) == 0 {
		hints = append(hints, "No common framework manifest detected")
	}
	return hints
}

func repoSuggestedNext(rootRel string, important, hints, hiddenDirs []string) []string {
	suggestions := []string{}
	for _, file := range trimStrings(important, 5) {
		suggestions = append(suggestions, fmt.Sprintf("read_file %s", file))
	}
	if rootRel == "" {
		rootRel = "."
	}
	suggestions = append(suggestions, fmt.Sprintf("module_outline %s", rootRel))
	for _, hint := range hints {
		switch hint {
		case "Node/JavaScript package", "Next.js":
			suggestions = append(suggestions, "read_file package.json or the nearest package.json")
		case "C/C++":
			suggestions = append(suggestions, "module_outline with file_glob=*.cpp or inspect CMakeLists.txt")
		case "Rust":
			suggestions = append(suggestions, "read_file Cargo.toml")
		case "Go":
			suggestions = append(suggestions, "read_file go.mod")
		}
	}
	if len(hiddenDirs) > 0 {
		suggestions = append(suggestions, "repeat repo_overview/list_files with include_hidden=true only if skipped directories are directly relevant")
	}
	return uniqueRepoOverviewStrings(suggestions)
}

func repoRepresentativeFiles(files, important []string, maxFiles int) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(file string) {
		if file == "" || seen[file] || len(out) >= maxFiles {
			return
		}
		seen[file] = true
		out = append(out, file)
	}
	for _, file := range important {
		add(file)
	}
	for _, file := range files {
		if outlineSupported(file) {
			add(file)
		}
	}
	for _, file := range files {
		add(file)
	}
	return out
}

func writeRepoOverviewSection(b *strings.Builder, title string, values []string, limit int) {
	b.WriteByte('\n')
	b.WriteString(title)
	b.WriteString(":\n")
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, value := range trimStrings(values, limit) {
		fmt.Fprintf(b, "- %s\n", value)
	}
}

func writeStringSection(b *strings.Builder, title string, values []string, limit int) {
	writeRepoOverviewSection(b, title, values, limit)
}

func uniqueRepoOverviewStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
