package tools

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"lcroom/internal/lcagent/policy"
)

const (
	defaultReadLineLimit    = 200
	maxReadLineLimit        = 1000
	defaultListEntryLimit   = 200
	maxListEntryLimit       = 1000
	defaultSearchMaxMatch   = 25
	maxSearchMaxMatch       = 100
	maxSearchContextLines   = 8
	defaultOutlineFileLimit = 30
	maxOutlineFileLimit     = 80
	maxModuleOutlineChars   = 24000
	defaultScoutFileLimit   = 12
	maxScoutFileLimit       = 40
	defaultScoutLineLimit   = 120
	fileScannerInitialSize  = 64 * 1024
	fileScannerMaxToken     = 1024 * 1024
)

type FileProfile string

const (
	FileProfileBalanced FileProfile = "balanced"
	FileProfileGenerous FileProfile = "generous"
)

type FileLimits struct {
	DefaultReadLineLimit    int `json:"default_read_line_limit"`
	MaxReadLineLimit        int `json:"max_read_line_limit"`
	DefaultListEntryLimit   int `json:"default_list_entry_limit"`
	MaxListEntryLimit       int `json:"max_list_entry_limit"`
	DefaultSearchMaxMatch   int `json:"default_search_max_match"`
	MaxSearchMaxMatch       int `json:"max_search_max_match"`
	MaxSearchContextLines   int `json:"max_search_context_lines"`
	DefaultOutlineFileLimit int `json:"default_outline_file_limit"`
	MaxOutlineFileLimit     int `json:"max_outline_file_limit"`
	MaxModuleOutlineChars   int `json:"max_module_outline_chars"`
}

type FileTools struct {
	Workspace policy.Workspace
	Limits    FileLimits
}

type ListOptions struct {
	IncludeHidden bool
}

type SearchOptions struct {
	IncludeHidden bool
	OutputMode    string
	Intent        string
}

type ModuleOutlineOptions struct {
	IncludeHidden bool
}

type ScoutPackOptions struct {
	FileGlob        string
	MaxFiles        int
	MaxLinesPerFile int
	IncludeHidden   bool
	Question        string
}

func ParseFileProfile(raw string) (FileProfile, error) {
	switch FileProfile(strings.ToLower(strings.TrimSpace(raw))) {
	case "", FileProfileBalanced:
		return FileProfileBalanced, nil
	case FileProfileGenerous:
		return FileProfileGenerous, nil
	default:
		return "", fmt.Errorf("unknown tool profile %q (expected %q or %q)", raw, FileProfileBalanced, FileProfileGenerous)
	}
}

func FileLimitsForProfile(profile FileProfile) FileLimits {
	switch profile {
	case FileProfileGenerous:
		return GenerousFileLimits()
	default:
		return BalancedFileLimits()
	}
}

func BalancedFileLimits() FileLimits {
	return FileLimits{
		DefaultReadLineLimit:    defaultReadLineLimit,
		MaxReadLineLimit:        maxReadLineLimit,
		DefaultListEntryLimit:   defaultListEntryLimit,
		MaxListEntryLimit:       maxListEntryLimit,
		DefaultSearchMaxMatch:   defaultSearchMaxMatch,
		MaxSearchMaxMatch:       maxSearchMaxMatch,
		MaxSearchContextLines:   maxSearchContextLines,
		DefaultOutlineFileLimit: defaultOutlineFileLimit,
		MaxOutlineFileLimit:     maxOutlineFileLimit,
		MaxModuleOutlineChars:   maxModuleOutlineChars,
	}
}

func GenerousFileLimits() FileLimits {
	return FileLimits{
		DefaultReadLineLimit:    400,
		MaxReadLineLimit:        2500,
		DefaultListEntryLimit:   400,
		MaxListEntryLimit:       2000,
		DefaultSearchMaxMatch:   50,
		MaxSearchMaxMatch:       250,
		MaxSearchContextLines:   16,
		DefaultOutlineFileLimit: 60,
		MaxOutlineFileLimit:     160,
		MaxModuleOutlineChars:   48000,
	}
}

func (t FileTools) limits() FileLimits {
	return t.Limits.withDefaults()
}

func (l FileLimits) withDefaults() FileLimits {
	defaults := BalancedFileLimits()
	if l.DefaultReadLineLimit <= 0 {
		l.DefaultReadLineLimit = defaults.DefaultReadLineLimit
	}
	if l.MaxReadLineLimit <= 0 {
		l.MaxReadLineLimit = defaults.MaxReadLineLimit
	}
	if l.DefaultListEntryLimit <= 0 {
		l.DefaultListEntryLimit = defaults.DefaultListEntryLimit
	}
	if l.MaxListEntryLimit <= 0 {
		l.MaxListEntryLimit = defaults.MaxListEntryLimit
	}
	if l.DefaultSearchMaxMatch <= 0 {
		l.DefaultSearchMaxMatch = defaults.DefaultSearchMaxMatch
	}
	if l.MaxSearchMaxMatch <= 0 {
		l.MaxSearchMaxMatch = defaults.MaxSearchMaxMatch
	}
	if l.MaxSearchContextLines <= 0 {
		l.MaxSearchContextLines = defaults.MaxSearchContextLines
	}
	if l.DefaultOutlineFileLimit <= 0 {
		l.DefaultOutlineFileLimit = defaults.DefaultOutlineFileLimit
	}
	if l.MaxOutlineFileLimit <= 0 {
		l.MaxOutlineFileLimit = defaults.MaxOutlineFileLimit
	}
	if l.MaxModuleOutlineChars <= 0 {
		l.MaxModuleOutlineChars = defaults.MaxModuleOutlineChars
	}
	if l.MaxReadLineLimit < l.DefaultReadLineLimit {
		l.MaxReadLineLimit = l.DefaultReadLineLimit
	}
	if l.MaxListEntryLimit < l.DefaultListEntryLimit {
		l.MaxListEntryLimit = l.DefaultListEntryLimit
	}
	if l.MaxSearchMaxMatch < l.DefaultSearchMaxMatch {
		l.MaxSearchMaxMatch = l.DefaultSearchMaxMatch
	}
	if l.MaxOutlineFileLimit < l.DefaultOutlineFileLimit {
		l.MaxOutlineFileLimit = l.DefaultOutlineFileLimit
	}
	return l
}

func (t FileTools) Read(path string, offset, limit int) ToolResult {
	limits := t.limits()
	target, rel, err := t.resolve(path)
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	limit = clampInt(limit, limits.DefaultReadLineLimit, limits.MaxReadLineLimit)
	lines, totalLines, binary, err := readTextFileRange(target, startLine, limit)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if binary {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", rel), Binary: true}
	}

	endLine := startLine + len(lines) - 1
	hasMore := len(lines) > 0 && endLine < totalLines
	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n", rel)
	fmt.Fprintf(&b, "total_lines: %d\n", totalLines)
	fmt.Fprintf(&b, "has_more: %t\n", hasMore)
	if hasMore {
		fmt.Fprintf(&b, "next_offset: %d\n", endLine+1)
	}
	if len(lines) == 0 {
		fmt.Fprintf(&b, "lines: none from %d\n", startLine)
	} else {
		fmt.Fprintf(&b, "lines: %d-%d\n\n", startLine, endLine)
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteByte('\n')
	}
	if hasMore {
		fmt.Fprintf(&b, "\n--- read truncated after %d lines; continue with next_offset %d ---\n", limit, endLine+1)
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: hasMore}
}

func (t FileTools) List(path, glob string, maxEntries int) ToolResult {
	return t.ListWithOptions(path, glob, maxEntries, ListOptions{})
}

func (t FileTools) ListWithOptions(path, glob string, maxEntries int, opts ListOptions) ToolResult {
	limits := t.limits()
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	maxEntries = clampInt(maxEntries, limits.DefaultListEntryLimit, limits.MaxListEntryLimit)
	glob = strings.TrimSpace(glob)

	entries := []string{}
	hiddenDirs := []string{}
	truncated := false
	addEntry := func(path string, entry fs.DirEntry) {
		display := t.displayPath(path)
		if display == "." {
			display = rel
		}
		if entry != nil && entry.IsDir() && display != "." {
			display += "/"
		}
		if glob != "" && !fileGlobMatches(glob, display) {
			return
		}
		if len(entries) >= maxEntries {
			truncated = true
			return
		}
		entries = append(entries, display)
	}
	addHiddenDir := func(path string, entry fs.DirEntry) {
		display := t.displayPath(path)
		if entry != nil && entry.IsDir() && display != "." {
			display += "/"
		}
		if glob != "" && !fileGlobMatches(glob, display) {
			return
		}
		if len(entries) >= maxEntries {
			truncated = true
			return
		}
		hiddenDirs = append(hiddenDirs, display)
		entries = append(entries, display+" [hidden by default; set include_hidden=true to descend]")
	}

	if !opts.IncludeHidden && rel != "." && pathHasDefaultHiddenSegment(rel) {
		addHiddenDir(target, nil)
	} else if !info.IsDir() {
		addEntry(target, nil)
	} else {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == target {
				return nil
			}
			if truncated {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !opts.IncludeHidden && entry != nil && entry.IsDir() && defaultHiddenDir(entry.Name()) {
				addHiddenDir(path, entry)
				return filepath.SkipDir
			}
			addEntry(path, entry)
			return nil
		})
		if err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
	}
	sort.Strings(entries)

	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", rel)
	if glob != "" {
		fmt.Fprintf(&b, "glob: %s\n", glob)
	}
	fmt.Fprintf(&b, "entries: %d\n\n", len(entries))
	if len(hiddenDirs) > 0 {
		fmt.Fprintf(&b, "hidden_dirs: %d (%s)\n", len(hiddenDirs), strings.Join(trimStrings(hiddenDirs, 12), ", "))
		b.WriteString("hidden_note: hidden directories are placeholders; set include_hidden=true to list their contents.\n\n")
	}
	b.WriteString(strings.Join(entries, "\n"))
	if len(entries) > 0 {
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "\n--- list truncated after %d entries ---\n", maxEntries)
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: truncated}
}

func (t FileTools) Search(query, path, fileGlob string, maxMatches int) ToolResult {
	return t.SearchContext(query, path, fileGlob, maxMatches, 0, 0)
}

func (t FileTools) SearchContext(query, path, fileGlob string, maxMatches, contextBefore, contextAfter int) ToolResult {
	return t.SearchContextWithOptions(query, path, fileGlob, maxMatches, contextBefore, contextAfter, SearchOptions{})
}

func (t FileTools) SearchContextWithOptions(query, path, fileGlob string, maxMatches, contextBefore, contextAfter int, opts SearchOptions) ToolResult {
	limits := t.limits()
	query = strings.TrimSpace(query)
	if query == "" {
		return ToolResult{Success: false, Error: "query is required"}
	}
	outputMode := normalizeSearchOutputMode(opts.OutputMode)
	intent := strings.TrimSpace(opts.Intent)
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	maxMatches = clampInt(maxMatches, limits.DefaultSearchMaxMatch, limits.MaxSearchMaxMatch)
	contextBefore = clampNonNegative(contextBefore, limits.MaxSearchContextLines)
	contextAfter = clampNonNegative(contextAfter, limits.MaxSearchContextLines)
	if outputMode == "compact" {
		contextBefore = 0
		contextAfter = 0
	}
	fileGlob = strings.TrimSpace(fileGlob)

	matches := []string{}
	hiddenDirs := []string{}
	truncated := false
	searchFile := func(path string) {
		if truncated {
			return
		}
		display := t.displayPath(path)
		if fileGlob != "" && !fileGlobMatches(fileGlob, display) {
			return
		}
		fileMatches, err := searchTextFile(path, display, query, maxMatches-len(matches), contextBefore, contextAfter)
		if err != nil {
			return
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= maxMatches {
			truncated = true
		}
	}
	addHiddenDir := func(path string, entry fs.DirEntry) {
		display := t.displayPath(path)
		if entry == nil || entry.IsDir() {
			display = strings.TrimSuffix(display, "/") + "/"
		}
		hiddenDirs = append(hiddenDirs, display)
	}

	if !opts.IncludeHidden && rel != "." && pathHasDefaultHiddenSegment(rel) {
		addHiddenDir(target, nil)
	} else if !info.IsDir() {
		searchFile(target)
	} else {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == target || entry == nil {
				return nil
			}
			if entry.IsDir() {
				if !opts.IncludeHidden && defaultHiddenDir(entry.Name()) {
					addHiddenDir(path, entry)
					return filepath.SkipDir
				}
				return nil
			}
			searchFile(path)
			return nil
		})
		if err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "query: %s\n", query)
	if intent != "" {
		fmt.Fprintf(&b, "intent: %s\n", intent)
	}
	fmt.Fprintf(&b, "output_mode: %s\n", outputMode)
	fmt.Fprintf(&b, "match_type: literal_substring_case_insensitive\n")
	fmt.Fprintf(&b, "path: %s\n", rel)
	if fileGlob != "" {
		fmt.Fprintf(&b, "file_glob: %s\n", fileGlob)
	}
	if len(hiddenDirs) > 0 {
		fmt.Fprintf(&b, "hidden_dirs_skipped: %d (%s)\n", len(hiddenDirs), strings.Join(trimStrings(hiddenDirs, 12), ", "))
		fmt.Fprintf(&b, "hidden_note: set include_hidden=true to search hidden/generated directories.\n")
	}
	if contextBefore > 0 || contextAfter > 0 {
		fmt.Fprintf(&b, "context_before: %d\n", contextBefore)
		fmt.Fprintf(&b, "context_after: %d\n", contextAfter)
	}
	fmt.Fprintf(&b, "matches: %d\n\n", len(matches))
	b.WriteString(strings.Join(matches, "\n"))
	if len(matches) > 0 {
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "\n--- search truncated after %d matches ---\n", maxMatches)
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: truncated}
}

func (t FileTools) Outline(path string) ToolResult {
	target, rel, err := t.resolve(path)
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s; use module_outline for directories", rel)}
	}
	return outlineFile(target, rel)
}

func (t FileTools) ModuleOutline(path, fileGlob string, maxFiles int) ToolResult {
	return t.ModuleOutlineWithOptions(path, fileGlob, maxFiles, ModuleOutlineOptions{})
}

func (t FileTools) ModuleOutlineWithOptions(path, fileGlob string, maxFiles int, opts ModuleOutlineOptions) ToolResult {
	limits := t.limits()
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if !info.IsDir() {
		return t.Outline(rel)
	}

	maxFiles = clampInt(maxFiles, limits.DefaultOutlineFileLimit, limits.MaxOutlineFileLimit)
	fileGlob = strings.TrimSpace(fileGlob)
	candidates := []string{}
	hiddenDirs := []string{}
	addHiddenDir := func(path string) {
		display := strings.TrimSuffix(t.displayPath(path), "/") + "/"
		hiddenDirs = append(hiddenDirs, display)
	}
	if !opts.IncludeHidden && rel != "." && pathHasDefaultHiddenSegment(rel) {
		addHiddenDir(target)
	} else {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == target || entry == nil {
				return nil
			}
			if entry.IsDir() {
				if !opts.IncludeHidden && defaultHiddenDir(entry.Name()) {
					addHiddenDir(path)
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			display := t.displayPath(path)
			if !outlineSupported(display) {
				return nil
			}
			if fileGlob != "" && !fileGlobMatches(fileGlob, display) {
				return nil
			}
			candidates = append(candidates, path)
			return nil
		})
		if err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
	}
	sort.Strings(candidates)

	truncated := false
	if len(candidates) > maxFiles {
		candidates = candidates[:maxFiles]
		truncated = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", rel)
	if fileGlob != "" {
		fmt.Fprintf(&b, "file_glob: %s\n", fileGlob)
	}
	fmt.Fprintf(&b, "files: %d\n", len(candidates))
	fmt.Fprintf(&b, "max_files: %d\n", maxFiles)
	if len(hiddenDirs) > 0 {
		fmt.Fprintf(&b, "hidden_dirs_skipped: %d (%s)\n", len(hiddenDirs), strings.Join(trimStrings(hiddenDirs, 12), ", "))
		fmt.Fprintf(&b, "hidden_note: set include_hidden=true to outline hidden/generated directories.\n")
	}
	if truncated {
		fmt.Fprintf(&b, "file_limit_truncated: true\n")
	}
	b.WriteByte('\n')

	for _, candidate := range candidates {
		section := outlineFile(candidate, t.displayPath(candidate))
		if !section.Success {
			continue
		}
		next := "\n---\n" + strings.TrimSpace(section.Output) + "\n"
		if b.Len()+len(next) > limits.MaxModuleOutlineChars {
			truncated = true
			fmt.Fprintf(&b, "\n--- module outline truncated after %d chars ---\n", limits.MaxModuleOutlineChars)
			break
		}
		b.WriteString(next)
	}
	if len(candidates) == 0 {
		b.WriteString("No supported source or Markdown files found.\n")
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: truncated}
}

func (t FileTools) ScoutPack(path string, opts ScoutPackOptions) ToolResult {
	limits := t.limits()
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	fileGlob := strings.TrimSpace(opts.FileGlob)
	maxFiles := clampInt(opts.MaxFiles, defaultScoutFileLimit, maxScoutFileLimit)
	maxLines := clampInt(opts.MaxLinesPerFile, defaultScoutLineLimit, limits.MaxReadLineLimit)
	if maxLines > limits.MaxReadLineLimit {
		maxLines = limits.MaxReadLineLimit
	}

	candidates := []string{}
	hiddenDirs := []string{}
	truncated := false
	addCandidate := func(path string) {
		if truncated {
			return
		}
		display := t.displayPath(path)
		if fileGlob != "" && !fileGlobMatches(fileGlob, display) {
			return
		}
		candidates = append(candidates, path)
		if len(candidates) >= maxFiles {
			truncated = true
		}
	}
	addHiddenDir := func(path string, entry fs.DirEntry) {
		display := t.displayPath(path)
		if entry == nil || entry.IsDir() {
			display = strings.TrimSuffix(display, "/") + "/"
		}
		hiddenDirs = append(hiddenDirs, display)
	}

	if !opts.IncludeHidden && rel != "." && pathHasDefaultHiddenSegment(rel) {
		addHiddenDir(target, nil)
	} else if !info.IsDir() {
		addCandidate(target)
	} else {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == target || entry == nil {
				return nil
			}
			if entry.IsDir() {
				if !opts.IncludeHidden && defaultHiddenDir(entry.Name()) {
					addHiddenDir(path, entry)
					return filepath.SkipDir
				}
				return nil
			}
			addCandidate(path)
			if truncated {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
	}
	sort.Strings(candidates)

	var b strings.Builder
	fmt.Fprintf(&b, "scout_pack: true\n")
	fmt.Fprintf(&b, "path: %s\n", rel)
	if fileGlob != "" {
		fmt.Fprintf(&b, "file_glob: %s\n", fileGlob)
	}
	if question := strings.TrimSpace(opts.Question); question != "" {
		fmt.Fprintf(&b, "question: %s\n", question)
	}
	fmt.Fprintf(&b, "files: %d\n", len(candidates))
	fmt.Fprintf(&b, "max_lines_per_file: %d\n", maxLines)
	if len(hiddenDirs) > 0 {
		fmt.Fprintf(&b, "hidden_dirs_skipped: %d (%s)\n", len(hiddenDirs), strings.Join(trimStrings(hiddenDirs, 12), ", "))
		fmt.Fprintf(&b, "hidden_note: set include_hidden=true only if hidden/generated contents are directly relevant.\n")
	}
	if truncated {
		fmt.Fprintf(&b, "truncated: true\n")
	}
	b.WriteByte('\n')

	for _, candidate := range candidates {
		display := t.displayPath(candidate)
		lines, totalLines, binary, err := readTextFileRange(candidate, 1, maxLines)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "## %s\nread_error: %s\n\n", display, err.Error())
			continue
		case binary:
			fmt.Fprintf(&b, "## %s\nbinary: true\n\n", display)
			continue
		}
		hasMore := len(lines) > 0 && len(lines) < totalLines
		fmt.Fprintf(&b, "## %s\n", display)
		fmt.Fprintf(&b, "total_lines: %d\n", totalLines)
		fmt.Fprintf(&b, "included_lines: 1-%d\n\n", min(len(lines), totalLines))
		if len(lines) > 0 {
			b.WriteString(strings.Join(lines, "\n"))
			b.WriteByte('\n')
		}
		if hasMore {
			fmt.Fprintf(&b, "--- file truncated after %d lines; use read_file path=%q offset=%d ---\n", maxLines, display, maxLines+1)
			truncated = true
		}
		b.WriteByte('\n')
	}
	if len(candidates) == 0 {
		b.WriteString("No matching readable files found.\n")
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: truncated}
}

func (t FileTools) resolve(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" || filepath.Clean(strings.TrimSpace(path)) == "." {
		return t.Workspace.Root, ".", nil
	}
	target, err := t.Workspace.ResolveRead(path)
	if err != nil {
		return "", "", err
	}
	return target, t.displayPath(target), nil
}

func (t FileTools) relative(path string) string {
	return t.displayPath(path)
}

func (t FileTools) displayPath(path string) string {
	path = filepath.Clean(path)
	rel, err := filepath.Rel(t.Workspace.Root, path)
	if err != nil || rel == "" {
		return filepath.ToSlash(path)
	}
	if rel == "." {
		return "."
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func defaultPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func searchTextFile(path, display, query string, remaining, contextBefore, contextAfter int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	lines, binary, err := readTextFileLines(path)
	if err != nil {
		return nil, err
	}
	if binary {
		return nil, nil
	}

	needle := strings.ToLower(query)
	matches := []string{}
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			matches = append(matches, formatSearchMatch(display, i, lines, contextBefore, contextAfter))
			if len(matches) >= remaining {
				break
			}
		}
	}
	return matches, nil
}

func normalizeSearchOutputMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "compact", "summary":
		return "compact"
	default:
		return "full"
	}
}

func formatSearchMatch(display string, index int, lines []string, contextBefore, contextAfter int) string {
	lineNo := index + 1
	summary := fmt.Sprintf("%s:%d: %s", display, lineNo, strings.TrimSpace(lines[index]))
	if contextBefore == 0 && contextAfter == 0 {
		return summary
	}
	start := index - contextBefore
	if start < 0 {
		start = 0
	}
	end := index + contextAfter
	if end >= len(lines) {
		end = len(lines) - 1
	}
	var b strings.Builder
	b.WriteString(summary)
	for i := start; i <= end; i++ {
		prefix := "  "
		if i == index {
			prefix = "> "
		}
		fmt.Fprintf(&b, "\n%s%d | %s", prefix, i+1, lines[i])
	}
	return b.String()
}

func readTextFileRange(path string, startLine, limit int) ([]string, int, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	binary, err := fileLooksBinary(file)
	if err != nil || binary {
		return nil, 0, binary, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, false, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, fileScannerInitialSize), fileScannerMaxToken)
	lines := []string{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < startLine {
			continue
		}
		if len(lines) < limit {
			lines = append(lines, fmt.Sprintf("%d | %s", lineNo, scanner.Text()))
		}
	}
	return lines, lineNo, false, scanner.Err()
}

func readTextFileLines(path string) ([]string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	binary, err := fileLooksBinary(file)
	if err != nil || binary {
		return nil, binary, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, fileScannerInitialSize), fileScannerMaxToken)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, false, scanner.Err()
}

func goOutline(path, display string) ToolResult {
	lines, binary, err := readTextFileLines(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if binary {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", display), Binary: true}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	imports := make([]string, 0, len(file.Imports))
	for _, imported := range file.Imports {
		name := importPath(imported)
		if imported.Name != nil {
			name = imported.Name.Name + " " + name
		}
		imports = append(imports, name)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n", display)
	fmt.Fprintf(&b, "type: go\n")
	fmt.Fprintf(&b, "total_lines: %d\n", len(lines))
	fmt.Fprintf(&b, "package: %s\n", file.Name.Name)
	fmt.Fprintf(&b, "imports: %d", len(imports))
	if len(imports) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(trimStrings(imports, 20), ", "))
	}
	b.WriteString("\n")
	b.WriteString("symbols:\n")
	symbolCount := 0
	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					writeSymbol(&b, "type", spec.Name.Name, fset, spec.Pos(), spec.End())
					symbolCount++
				case *ast.ValueSpec:
					names := make([]string, 0, len(spec.Names))
					for _, name := range spec.Names {
						names = append(names, name.Name)
					}
					writeSymbol(&b, strings.ToLower(typed.Tok.String()), strings.Join(names, ", "), fset, spec.Pos(), spec.End())
					symbolCount++
				}
			}
		case *ast.FuncDecl:
			kind := "func"
			name := typed.Name.Name
			if receiver := receiverName(typed.Recv); receiver != "" {
				kind = "method"
				name = receiver + "." + name
			}
			writeSymbol(&b, kind, name, fset, typed.Pos(), typed.End())
			symbolCount++
		}
	}
	if symbolCount == 0 {
		b.WriteString("- none\n")
	}
	return ToolResult{Success: true, Output: b.String()}
}

func markdownOutline(path, display string) ToolResult {
	lines, binary, err := readTextFileLines(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if binary {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", display), Binary: true}
	}
	headings := markdownHeadings(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n", display)
	fmt.Fprintf(&b, "type: markdown\n")
	fmt.Fprintf(&b, "total_lines: %d\n", len(lines))
	b.WriteString("headings:\n")
	if len(headings) == 0 {
		b.WriteString("- none\n")
		return ToolResult{Success: true, Output: b.String()}
	}
	for i, heading := range headings {
		endLine := len(lines)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].Level <= heading.Level {
				endLine = headings[j].Line - 1
				break
			}
		}
		fmt.Fprintf(&b, "- h%d lines %d-%d: %s\n", heading.Level, heading.Line, endLine, heading.Title)
	}
	return ToolResult{Success: true, Output: b.String()}
}

type lightweightSymbol struct {
	Kind  string
	Name  string
	Line  int
	Level int
}

var (
	pythonClassRe  = regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	pythonDefRe    = regexp.MustCompile(`^\s*(async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	jsClassRe      = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	jsFunctionRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	jsConstFuncRe  = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?(?:function\b|\([^)]*\)\s*=>|[A-Za-z_$][A-Za-z0-9_$]*\s*=>)`)
	jsTypeRe       = regexp.MustCompile(`^\s*(?:export\s+)?(interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	rustFnRe       = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	rustTypeRe     = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait|mod|const|static)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	rustImplRe     = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl(?:\s*<[^>{}]+>)?\s+([^{]+?)\s*\{`)
	cppTypeRe      = regexp.MustCompile(`^\s*(?:template\s*<[^;{}]+>\s*)?(class|struct|union|namespace|enum(?:\s+class)?)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	cppFunctionRe  = regexp.MustCompile(`^\s*(?:template\s*<[^;{}]+>\s*)?(?:inline\s+|static\s+|virtual\s+|constexpr\s+|consteval\s+|friend\s+|extern\s+|explicit\s+)*[A-Za-z_~][A-Za-z0-9_:<>,~*&\s]+\s+([~A-Za-z_][A-Za-z0-9_:~]*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:noexcept(?:\s*\([^)]*\))?\s*)?(?:override\s*)?(?:final\s*)?(?:->\s*[A-Za-z_][A-Za-z0-9_:<>,~*&\s]+)?(?:\{|;)\s*$`)
	csharpTypeRe   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|sealed|abstract|partial|unsafe|new)\s+)*(class|struct|interface|record|enum|delegate)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	csharpMethodRe = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|virtual|override|async|sealed|abstract|partial|extern|unsafe|new)\s+)+[A-Za-z_][A-Za-z0-9_<>,\[\]?.\s]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:where\s+.+)?(?:\{|=>)?\s*$`)
	javaTypeRe     = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|sealed|non-sealed|strictfp)\s+)*(class|interface|enum|record)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	javaMethodRe   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|strictfp|default)\s+)+[A-Za-z_][A-Za-z0-9_<>,\[\]?.\s]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:throws\s+[^{]+)?(?:\{|;)\s*$`)
	kotlinDeclRe   = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+|open\s+|data\s+|sealed\s+|abstract\s+|object\s+|companion\s+)*(class|object|interface|enum\s+class|fun)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	swiftDeclRe    = regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+|open\s+|final\s+|static\s+|class\s+)*(class|struct|enum|protocol|extension|func)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
)

func lightweightSourceOutline(path, display, language string, extract func(string) (string, string, bool)) ToolResult {
	lines, binary, err := readTextFileLines(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if binary {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", display), Binary: true}
	}
	symbols := make([]lightweightSymbol, 0)
	for i, line := range lines {
		kind, name, ok := extract(line)
		if !ok {
			continue
		}
		symbols = append(symbols, lightweightSymbol{
			Kind:  kind,
			Name:  name,
			Line:  i + 1,
			Level: leadingIndent(line),
		})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n", display)
	fmt.Fprintf(&b, "type: %s\n", language)
	fmt.Fprintf(&b, "total_lines: %d\n", len(lines))
	b.WriteString("symbols:\n")
	if len(symbols) == 0 {
		b.WriteString("- none\n")
		return ToolResult{Success: true, Output: b.String()}
	}
	for i, symbol := range symbols {
		endLine := len(lines)
		for j := i + 1; j < len(symbols); j++ {
			if symbols[j].Level <= symbol.Level {
				endLine = symbols[j].Line - 1
				break
			}
		}
		if endLine < symbol.Line {
			endLine = symbol.Line
		}
		fmt.Fprintf(&b, "- %s %s lines %d-%d\n", symbol.Kind, symbol.Name, symbol.Line, endLine)
	}
	return ToolResult{Success: true, Output: b.String()}
}

func pythonOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "python", func(line string) (string, string, bool) {
		if m := pythonClassRe.FindStringSubmatch(line); len(m) == 2 {
			return "class", m[1], true
		}
		if m := pythonDefRe.FindStringSubmatch(line); len(m) == 3 {
			kind := "func"
			if strings.TrimSpace(m[1]) != "" {
				kind = "async func"
			}
			return kind, m[2], true
		}
		return "", "", false
	})
}

func jsOutline(path, display, language string) ToolResult {
	return lightweightSourceOutline(path, display, language, func(line string) (string, string, bool) {
		if m := jsClassRe.FindStringSubmatch(line); len(m) == 2 {
			return "class", m[1], true
		}
		if m := jsFunctionRe.FindStringSubmatch(line); len(m) == 3 {
			kind := "func"
			if strings.TrimSpace(m[1]) != "" {
				kind = "async func"
			}
			return kind, m[2], true
		}
		if m := jsConstFuncRe.FindStringSubmatch(line); len(m) == 2 {
			return "func", m[1], true
		}
		if m := jsTypeRe.FindStringSubmatch(line); len(m) == 3 {
			return m[1], m[2], true
		}
		return "", "", false
	})
}

func rustOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "rust", func(line string) (string, string, bool) {
		if m := rustFnRe.FindStringSubmatch(line); len(m) == 3 {
			kind := "fn"
			if strings.TrimSpace(m[1]) != "" {
				kind = "async fn"
			}
			return kind, m[2], true
		}
		if m := rustTypeRe.FindStringSubmatch(line); len(m) == 3 {
			return m[1], m[2], true
		}
		if m := rustImplRe.FindStringSubmatch(line); len(m) == 2 {
			return "impl", compactSymbolName(m[1]), true
		}
		return "", "", false
	})
}

func cppOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "c/c++", func(line string) (string, string, bool) {
		if m := cppTypeRe.FindStringSubmatch(line); len(m) == 3 {
			return strings.Join(strings.Fields(m[1]), " "), m[2], true
		}
		if m := cppFunctionRe.FindStringSubmatch(line); len(m) == 2 {
			name := compactSymbolName(m[1])
			if sourceControlKeyword(name) {
				return "", "", false
			}
			return "func", name, true
		}
		return "", "", false
	})
}

func csharpOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "csharp", func(line string) (string, string, bool) {
		if m := csharpTypeRe.FindStringSubmatch(line); len(m) == 3 {
			return m[1], m[2], true
		}
		if m := csharpMethodRe.FindStringSubmatch(line); len(m) == 2 {
			name := compactSymbolName(m[1])
			if sourceControlKeyword(name) {
				return "", "", false
			}
			return "method", name, true
		}
		return "", "", false
	})
}

func javaOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "java", func(line string) (string, string, bool) {
		if m := javaTypeRe.FindStringSubmatch(line); len(m) == 3 {
			return m[1], m[2], true
		}
		if m := javaMethodRe.FindStringSubmatch(line); len(m) == 2 {
			name := compactSymbolName(m[1])
			if sourceControlKeyword(name) {
				return "", "", false
			}
			return "method", name, true
		}
		return "", "", false
	})
}

func kotlinOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "kotlin", func(line string) (string, string, bool) {
		if m := kotlinDeclRe.FindStringSubmatch(line); len(m) == 3 {
			return strings.Join(strings.Fields(m[1]), " "), m[2], true
		}
		return "", "", false
	})
}

func swiftOutline(path, display string) ToolResult {
	return lightweightSourceOutline(path, display, "swift", func(line string) (string, string, bool) {
		if m := swiftDeclRe.FindStringSubmatch(line); len(m) == 3 {
			return m[1], m[2], true
		}
		return "", "", false
	})
}

func fileLooksBinary(file *os.File) (bool, error) {
	var sample [4096]byte
	n, err := file.Read(sample[:])
	if err != nil && err != io.EOF {
		return false, err
	}
	data := sample[:n]
	return bytes.IndexByte(data, 0) >= 0 || (len(data) > 0 && !utf8.Valid(data)), nil
}

func fileGlobMatches(glob, path string) bool {
	glob = filepath.ToSlash(strings.TrimSpace(glob))
	path = filepath.ToSlash(strings.TrimSpace(path))
	if glob == "" {
		return true
	}
	if ok, _ := filepath.Match(glob, path); ok {
		return true
	}
	if ok, _ := filepath.Match(glob, filepath.Base(path)); ok {
		return true
	}
	return false
}

func defaultHiddenDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".venv", "venv", "node_modules", "vendor", "dist", "build", "coverage", ".next", "out", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache":
		return true
	default:
		return false
	}
}

func pathHasDefaultHiddenSegment(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, part := range strings.Split(path, "/") {
		if defaultHiddenDir(part) {
			return true
		}
	}
	return false
}

func leadingIndent(line string) int {
	indent := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			indent++
		case '\t':
			indent += 4
		default:
			return indent
		}
	}
	return indent
}

func compactSymbolName(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
}

func sourceControlKeyword(name string) bool {
	switch strings.TrimSpace(name) {
	case "if", "for", "while", "switch", "catch", "return", "sizeof":
		return true
	default:
		return false
	}
}

func clampInt(value, fallback, maxValue int) int {
	if value <= 0 {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampNonNegative(value, maxValue int) int {
	if value < 0 {
		return 0
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func importPath(imported *ast.ImportSpec) string {
	if imported == nil || imported.Path == nil {
		return ""
	}
	if value, err := strconv.Unquote(imported.Path.Value); err == nil {
		return value
	}
	return strings.Trim(imported.Path.Value, `"`)
}

func trimStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	out := append([]string{}, values[:limit]...)
	out = append(out, fmt.Sprintf("+%d more", len(values)-limit))
	return out
}

func writeSymbol(b *strings.Builder, kind, name string, fset *token.FileSet, pos, end token.Pos) {
	startLine := fset.Position(pos).Line
	endLine := fset.Position(end).Line
	fmt.Fprintf(b, "- %s %s lines %d-%d\n", kind, name, startLine, endLine)
}

func outlineFile(path, display string) ToolResult {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goOutline(path, display)
	case ".md", ".markdown":
		return markdownOutline(path, display)
	case ".py", ".pyi":
		return pythonOutline(path, display)
	case ".js", ".jsx", ".mjs", ".cjs":
		return jsOutline(path, display, "javascript")
	case ".ts", ".tsx", ".mts", ".cts":
		return jsOutline(path, display, "typescript")
	case ".rs":
		return rustOutline(path, display)
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp":
		return cppOutline(path, display)
	case ".cs":
		return csharpOutline(path, display)
	case ".java":
		return javaOutline(path, display)
	case ".kt", ".kts":
		return kotlinOutline(path, display)
	case ".swift":
		return swiftOutline(path, display)
	default:
		return ToolResult{Success: false, Error: fmt.Sprintf("outline unsupported for %s", display)}
	}
}

func outlineSupported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".markdown", ".py", ".pyi", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".rs", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".cs", ".java", ".kt", ".kts", ".swift":
		return true
	default:
		return false
	}
}

func shouldSkipOutlineDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", "coverage":
		return true
	default:
		return false
	}
}

func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return exprName(recv.List[0].Type)
}

func exprName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + exprName(typed.X)
	case *ast.SelectorExpr:
		if left := exprName(typed.X); left != "" {
			return left + "." + typed.Sel.Name
		}
		return typed.Sel.Name
	case *ast.IndexExpr:
		return exprName(typed.X)
	case *ast.IndexListExpr:
		return exprName(typed.X)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

type markdownHeading struct {
	Level int
	Line  int
	Title string
}

func markdownHeadings(lines []string) []markdownHeading {
	var headings []markdownHeading
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		level := 0
		for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
			level++
		}
		if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		title := strings.TrimSpace(trimmed[level+1:])
		if title == "" {
			continue
		}
		headings = append(headings, markdownHeading{Level: level, Line: i + 1, Title: title})
	}
	return headings
}
