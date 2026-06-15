package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/lcagent/policy"
)

type TextEditor struct {
	Workspace policy.Workspace
}

type ReplaceTextSpec struct {
	Path                 string
	OldText              string
	NewText              string
	ExpectedReplacements int
}

type ReplaceLinesSpec struct {
	Path              string
	StartLine         int
	EndLine           int
	NewText           string
	ExpectedFirstLine string
	ExpectedLastLine  string
}

type CreateFileSpec struct {
	Path    string
	Content string
}

type ReplaceFileSpec struct {
	Path           string
	Content        string
	ExpectedSHA256 string
}

const (
	replaceTextMaxLines = 80
	replaceTextMaxBytes = 12000
)

func (e TextEditor) CreateFile(spec CreateFileSpec) ToolResult {
	if err := e.Workspace.AllowEdit("create_file"); err != nil {
		return failureResult(err)
	}
	rel := cleanEditPath(spec.Path)
	if rel == "" {
		return ToolResult{Success: false, Error: "path is required"}
	}
	if strings.ContainsRune(spec.Content, '\x00') {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary content suppressed: %s", rel), Binary: true}
	}
	path, err := e.Workspace.Resolve(rel)
	if err != nil {
		return failureResult(err)
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
		}
		return ToolResult{Success: false, Error: fmt.Sprintf("create_file target already exists: %s; use replace_file with expected_sha256 for whole-file rewrites", rel)}
	} else if !os.IsNotExist(err) {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if err := os.WriteFile(path, []byte(spec.Content), 0o644); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	summary := fileWritePatchSummary(rel, "add", countReplacementLines(spec.Content), 0)
	diffSummary := formatPatchSummary(summary)
	return ToolResult{
		Success:      true,
		Output:       "file created\nsha256: " + fileSHA256Hex([]byte(spec.Content)) + "\n\n" + diffSummary,
		FilesTouched: []string{rel},
		DiffSummary:  diffSummary,
		PatchSummary: summary,
	}
}

func (e TextEditor) ReplaceFile(spec ReplaceFileSpec) ToolResult {
	if err := e.Workspace.AllowEdit("replace_file"); err != nil {
		return failureResult(err)
	}
	rel := cleanEditPath(spec.Path)
	if rel == "" {
		return ToolResult{Success: false, Error: "path is required"}
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(spec.ExpectedSHA256))
	if !isSHA256Hex(expectedSHA) {
		return textEditFailureResult("replace_file", rel, "expected_sha256 is required and must be a 64-character hex SHA-256 from the latest read_file output", "call read_file on the target file and copy its sha256 value before retrying", []ReadSuggestion{{Path: rel, Offset: 1, Limit: 80, Reason: "refresh file hash before whole-file replacement"}})
	}
	if strings.ContainsRune(spec.Content, '\x00') {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary content suppressed: %s", rel), Binary: true}
	}
	path, err := e.Workspace.Resolve(rel)
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if bytes.IndexByte(body, 0) >= 0 {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", rel), Binary: true}
	}
	currentSHA := fileSHA256Hex(body)
	if !strings.EqualFold(currentSHA, expectedSHA) {
		message := fmt.Sprintf("replace_file sha256 guard mismatch in %s; expected %s, current %s", rel, expectedSHA, currentSHA)
		return textEditFailureResult("replace_file", rel, message, "call read_file on the target file and copy its current sha256 value before retrying", []ReadSuggestion{{Path: rel, Offset: 1, Limit: 80, Reason: "refresh file hash before whole-file replacement"}})
	}
	if err := os.WriteFile(path, []byte(spec.Content), info.Mode().Perm()); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	newSHA := fileSHA256Hex([]byte(spec.Content))
	summary := fileWritePatchSummary(rel, "replace", countReplacementLines(spec.Content), countReplacementLines(string(body)))
	diffSummary := formatPatchSummary(summary)
	return ToolResult{
		Success:      true,
		Output:       fmt.Sprintf("file replaced\nold_sha256: %s\nsha256: %s\n\n%s", currentSHA, newSHA, diffSummary),
		FilesTouched: []string{rel},
		DiffSummary:  diffSummary,
		PatchSummary: summary,
	}
}

func (e TextEditor) ReplaceText(spec ReplaceTextSpec) ToolResult {
	if err := e.Workspace.AllowEdit("replace_text"); err != nil {
		return failureResult(err)
	}
	rel := filepath.Clean(strings.TrimSpace(spec.Path))
	if rel == "." || rel == "" {
		return ToolResult{Success: false, Error: "path is required"}
	}
	if spec.OldText == "" {
		return ToolResult{Success: false, Error: "old_text is required"}
	}
	if replacementLineCount(spec.OldText) > replaceTextMaxLines || len(spec.OldText) > replaceTextMaxBytes {
		return textEditFailureResult("replace_text", rel, fmt.Sprintf("replace_text old_text is too large for a literal substitution in %s", rel), "use apply_patch, or replace_lines when exact current line numbers are known", nil)
	}
	expected := spec.ExpectedReplacements
	if expected <= 0 {
		expected = 1
	}
	path, err := e.Workspace.Resolve(rel)
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if bytes.IndexByte(body, 0) >= 0 {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", rel), Binary: true}
	}
	oldBytes := []byte(spec.OldText)
	count := bytes.Count(body, oldBytes)
	if count != expected {
		message := fmt.Sprintf("replace_text found %d occurrences of old_text in %s, expected %d", count, rel, expected)
		return textEditFailureResult("replace_text", rel, message, "re-read the target range and provide an exact unique old_text span, use replace_lines when current line numbers are known, or adjust expected_replacements", nil)
	}
	updated := bytes.Replace(body, oldBytes, []byte(spec.NewText), expected)
	if err := os.WriteFile(path, updated, info.Mode().Perm()); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	summary := replaceTextPatchSummary(rel, spec.OldText, spec.NewText, expected)
	diffSummary := formatPatchSummary(summary)
	return ToolResult{
		Success:      true,
		Output:       "text replaced\n\n" + diffSummary,
		FilesTouched: []string{rel},
		DiffSummary:  diffSummary,
		PatchSummary: summary,
	}
}

func (e TextEditor) ReplaceLines(spec ReplaceLinesSpec) ToolResult {
	if err := e.Workspace.AllowEdit("replace_lines"); err != nil {
		return failureResult(err)
	}
	rel := filepath.Clean(strings.TrimSpace(spec.Path))
	if rel == "." || rel == "" {
		return ToolResult{Success: false, Error: "path is required"}
	}
	if spec.StartLine <= 0 || spec.EndLine <= 0 {
		return ToolResult{Success: false, Error: "start_line and end_line must be positive 1-based line numbers"}
	}
	if spec.EndLine < spec.StartLine {
		return ToolResult{Success: false, Error: "end_line must be greater than or equal to start_line"}
	}
	path, err := e.Workspace.Resolve(rel)
	if err != nil {
		return failureResult(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if info.IsDir() {
		return ToolResult{Success: false, Error: fmt.Sprintf("path is a directory: %s", rel)}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if bytes.IndexByte(body, 0) >= 0 {
		return ToolResult{Success: false, Error: fmt.Sprintf("binary file suppressed: %s", rel), Binary: true}
	}
	lineEnding := detectLineEnding(body)
	lines, trailingNewline := splitLogicalLines(string(body), lineEnding)
	if spec.StartLine > len(lines) || spec.EndLine > len(lines) {
		message := fmt.Sprintf("replace_lines range %d-%d is outside %s line count %d", spec.StartLine, spec.EndLine, rel, len(lines))
		return textEditFailureResult("replace_lines", rel, message, "re-read the target range before retrying", nil)
	}
	first := strings.TrimRight(spec.ExpectedFirstLine, "\r")
	if first != "" && lines[spec.StartLine-1] != first {
		message := fmt.Sprintf("replace_lines first-line guard mismatch in %s at line %d; expected %q, found %q", rel, spec.StartLine, first, lines[spec.StartLine-1])
		return textEditFailureResult("replace_lines", rel, message, "re-read the target range before retrying", []ReadSuggestion{replaceLinesReadSuggestion(rel, spec.StartLine, spec.EndLine, len(lines))})
	}
	last := strings.TrimRight(spec.ExpectedLastLine, "\r")
	if last != "" && lines[spec.EndLine-1] != last {
		message := fmt.Sprintf("replace_lines last-line guard mismatch in %s at line %d; expected %q, found %q", rel, spec.EndLine, last, lines[spec.EndLine-1])
		return textEditFailureResult("replace_lines", rel, message, "re-read the target range before retrying", []ReadSuggestion{replaceLinesReadSuggestion(rel, spec.StartLine, spec.EndLine, len(lines))})
	}

	replacement := splitReplacementLines(spec.NewText, lineEnding)
	updatedLines := make([]string, 0, len(lines)-(spec.EndLine-spec.StartLine+1)+len(replacement))
	updatedLines = append(updatedLines, lines[:spec.StartLine-1]...)
	updatedLines = append(updatedLines, replacement...)
	updatedLines = append(updatedLines, lines[spec.EndLine:]...)
	updated := joinLogicalLines(updatedLines, lineEnding, trailingNewline)
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	summary := &PatchSummary{
		Files: []FilePatchSummary{{
			Path:         rel,
			Operation:    "replace",
			AddedLines:   len(replacement),
			DeletedLines: spec.EndLine - spec.StartLine + 1,
		}},
		TotalAddedLines:   len(replacement),
		TotalDeletedLines: spec.EndLine - spec.StartLine + 1,
	}
	diffSummary := formatPatchSummary(summary)
	return ToolResult{
		Success:      true,
		Output:       fmt.Sprintf("lines %d-%d replaced\n\n%s", spec.StartLine, spec.EndLine, diffSummary),
		FilesTouched: []string{rel},
		DiffSummary:  diffSummary,
		PatchSummary: summary,
	}
}

func cleanEditPath(path string) string {
	rel := filepath.Clean(strings.TrimSpace(path))
	if rel == "." || rel == "" {
		return ""
	}
	return filepath.ToSlash(rel)
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}

func fileWritePatchSummary(path, operation string, added, deleted int) *PatchSummary {
	return &PatchSummary{
		Files: []FilePatchSummary{{
			Path:         path,
			Operation:    operation,
			AddedLines:   added,
			DeletedLines: deleted,
		}},
		TotalAddedLines:   added,
		TotalDeletedLines: deleted,
	}
}

func textEditFailureResult(stage, path, message, hint string, suggestedReads []ReadSuggestion) ToolResult {
	message = strings.TrimSpace(message)
	hint = strings.TrimSpace(hint)
	full := message
	if hint != "" {
		full += "; " + hint
	}
	return ToolResult{
		Success: false,
		Error:   full,
		PatchFailure: &PatchFailure{
			Stage:          stage,
			Path:           filepath.Clean(strings.TrimSpace(path)),
			Message:        message,
			Hint:           hint,
			SuggestedReads: cleanReadSuggestions(suggestedReads),
		},
	}
}

func replaceLinesReadSuggestion(path string, startLine, endLine, totalLines int) ReadSuggestion {
	const contextLines = 10
	start := startLine - contextLines
	if start < 1 {
		start = 1
	}
	end := endLine + contextLines
	if end > totalLines {
		end = totalLines
	}
	limit := end - start + 1
	if limit < 1 {
		limit = 1
	}
	return ReadSuggestion{
		Path:   filepath.Clean(strings.TrimSpace(path)),
		Offset: start,
		Limit:  limit,
		Reason: "refresh current context for failed line-range edit",
	}
}

func replaceTextPatchSummary(path, oldText, newText string, replacements int) *PatchSummary {
	if replacements <= 0 {
		replacements = 1
	}
	added := countReplacementLines(newText) * replacements
	deleted := countReplacementLines(oldText) * replacements
	return &PatchSummary{
		Files: []FilePatchSummary{{
			Path:         path,
			Operation:    "replace",
			AddedLines:   added,
			DeletedLines: deleted,
		}},
		TotalAddedLines:   added,
		TotalDeletedLines: deleted,
	}
}

func replacementLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func countReplacementLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

func detectLineEnding(body []byte) string {
	if bytes.Contains(body, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func splitLogicalLines(text, lineEnding string) ([]string, bool) {
	if lineEnding == "\r\n" {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	trailingNewline := strings.HasSuffix(text, "\n")
	if trailingNewline {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" {
		return nil, trailingNewline
	}
	return strings.Split(text, "\n"), trailingNewline
}

func splitReplacementLines(text, lineEnding string) []string {
	if lineEnding == "\r\n" {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func joinLogicalLines(lines []string, lineEnding string, trailingNewline bool) string {
	if len(lines) == 0 {
		return ""
	}
	out := strings.Join(lines, lineEnding)
	if trailingNewline {
		out += lineEnding
	}
	return out
}
