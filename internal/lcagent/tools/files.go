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
	defaultSearchMaxMatch   = 50
	maxSearchMaxMatch       = 200
	maxSearchContextLines   = 8
	defaultOutlineFileLimit = 30
	maxOutlineFileLimit     = 80
	maxModuleOutlineChars   = 24000
	fileScannerInitialSize  = 64 * 1024
	fileScannerMaxToken     = 1024 * 1024
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
	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	limit = clampInt(limit, defaultReadLineLimit, maxReadLineLimit)
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
	return t.SearchContext(query, path, fileGlob, maxMatches, 0, 0)
}

func (t FileTools) SearchContext(query, path, fileGlob string, maxMatches, contextBefore, contextAfter int) ToolResult {
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
	contextBefore = clampNonNegative(contextBefore, maxSearchContextLines)
	contextAfter = clampNonNegative(contextAfter, maxSearchContextLines)
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
		fileMatches, err := searchTextFile(path, display, query, maxMatches-len(matches), contextBefore, contextAfter)
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
		return ToolResult{Success: false, Error: err.Error()}
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	return outlineFile(target, rel)
}

func (t FileTools) ModuleOutline(path, fileGlob string, maxFiles int) ToolResult {
	target, rel, err := t.resolve(defaultPath(path))
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	info, err := os.Stat(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if !info.IsDir() {
		return t.Outline(rel)
	}

	maxFiles = clampInt(maxFiles, defaultOutlineFileLimit, maxOutlineFileLimit)
	fileGlob = strings.TrimSpace(fileGlob)
	candidates := []string{}
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == target || entry == nil {
			return nil
		}
		if entry.IsDir() {
			if shouldSkipOutlineDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		display := t.relative(path)
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
	if truncated {
		fmt.Fprintf(&b, "file_limit_truncated: true\n")
	}
	b.WriteByte('\n')

	for _, candidate := range candidates {
		section := outlineFile(candidate, t.relative(candidate))
		if !section.Success {
			continue
		}
		next := "\n---\n" + strings.TrimSpace(section.Output) + "\n"
		if b.Len()+len(next) > maxModuleOutlineChars {
			truncated = true
			fmt.Fprintf(&b, "\n--- module outline truncated after %d chars ---\n", maxModuleOutlineChars)
			break
		}
		b.WriteString(next)
	}
	if len(candidates) == 0 {
		b.WriteString("No supported .go, .md, or .markdown files found.\n")
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
	default:
		return ToolResult{Success: false, Error: fmt.Sprintf("outline unsupported for %s", display)}
	}
}

func outlineSupported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".markdown":
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
