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
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("replace_text found %d occurrences of old_text in %s, expected %d; re-read the target range and provide an exact unique old_text span or adjust expected_replacements", count, rel, expected),
		}
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
