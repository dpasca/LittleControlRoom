package tools

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"lcroom/internal/lcagent/policy"
)

const (
	defaultReadLineLimit   = 200
	maxReadLineLimit       = 1000
	defaultListEntryLimit  = 200
	maxListEntryLimit      = 1000
	defaultSearchMaxMatch  = 50
	maxSearchMaxMatch      = 200
	fileScannerInitialSize = 64 * 1024
	fileScannerMaxToken    = 1024 * 1024
)

type FileTools struct {
	Workspace policy.Workspace
}

func (t FileTools) Read(path string, offset, limit int) ToolResult {
	target, rel, err := t.resolve(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	file, err := os.Open(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	defer file.Close()
	binary, err := fileLooksBinary(file)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if binary {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", rel), Binary: true}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}

	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	limit = clampInt(limit, defaultReadLineLimit, maxReadLineLimit)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, fileScannerInitialSize), fileScannerMaxToken)
	lines := []string{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < startLine {
			continue
		}
		if len(lines) >= limit {
			break
		}
		lines = append(lines, fmt.Sprintf("%d | %s", lineNo, scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}

	endLine := startLine + len(lines) - 1
	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n", rel)
	if len(lines) == 0 {
		fmt.Fprintf(&b, "lines: none from %d\n", startLine)
	} else {
		fmt.Fprintf(&b, "lines: %d-%d\n\n", startLine, endLine)
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteByte('\n')
	}
	truncated := len(lines) == limit
	if truncated {
		fmt.Fprintf(&b, "\n--- read truncated after %d lines ---\n", limit)
	}
	return ToolResult{Success: true, Output: b.String(), Truncated: truncated}
}

func (t FileTools) List(path, glob string, maxEntries int) ToolResult {
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	maxEntries = clampInt(maxEntries, defaultListEntryLimit, maxListEntryLimit)
	glob = strings.TrimSpace(glob)

	entries := []string{}
	truncated := false
	addEntry := func(path string, entry fs.DirEntry) {
		display := t.relative(path)
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

	if !info.IsDir() {
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
	query = strings.TrimSpace(query)
	if query == "" {
		return ToolResult{Success: false, Error: "query is required"}
	}
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	maxMatches = clampInt(maxMatches, defaultSearchMaxMatch, maxSearchMaxMatch)
	fileGlob = strings.TrimSpace(fileGlob)

	matches := []string{}
	truncated := false
	searchFile := func(path string) {
		if truncated {
			return
		}
		display := t.relative(path)
		if fileGlob != "" && !fileGlobMatches(fileGlob, display) {
			return
		}
		fileMatches, err := searchTextFile(path, display, query, maxMatches-len(matches))
		if err != nil {
			return
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= maxMatches {
			truncated = true
		}
	}

	if !info.IsDir() {
		searchFile(target)
	} else {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == target || entry == nil || entry.IsDir() {
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
	fmt.Fprintf(&b, "path: %s\n", rel)
	if fileGlob != "" {
		fmt.Fprintf(&b, "file_glob: %s\n", fileGlob)
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

func (t FileTools) resolve(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" || filepath.Clean(strings.TrimSpace(path)) == "." {
		return t.Workspace.Root, ".", nil
	}
	target, err := t.Workspace.Resolve(path)
	if err != nil {
		return "", "", err
	}
	return target, t.relative(target), nil
}

func (t FileTools) relative(path string) string {
	rel, err := filepath.Rel(t.Workspace.Root, path)
	if err != nil || rel == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func defaultPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func searchTextFile(path, display, query string, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	binary, err := fileLooksBinary(file)
	if err != nil || binary {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	needle := strings.ToLower(query)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, fileScannerInitialSize), fileScannerMaxToken)
	matches := []string{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), needle) {
			matches = append(matches, fmt.Sprintf("%s:%d: %s", display, lineNo, strings.TrimSpace(line)))
			if len(matches) >= remaining {
				break
			}
		}
	}
	return matches, scanner.Err()
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

func clampInt(value, fallback, maxValue int) int {
	if value <= 0 {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
