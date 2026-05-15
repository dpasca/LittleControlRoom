package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/lcagent/policy"
)

type PatchApplier struct {
	Workspace policy.Workspace
}

const applyPatchFormatHint = "expected Codex apply_patch format, for example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch"

func (a PatchApplier) Apply(patch string) ToolResult {
	if err := a.Workspace.AllowPatch(); err != nil {
		return failureResult(err)
	}
	ops, err := parseApplyPatch(patch)
	if err != nil {
		return patchFailureResult("parse", "", err, nil, nil)
	}
	touched := make([]string, 0, len(ops))
	for _, op := range ops {
		if _, err := a.Workspace.Resolve(op.Path); err != nil {
			return failureResult(err)
		}
		touched = append(touched, filepath.Clean(op.Path))
	}
	summary, err := a.summarizeOps(ops)
	if err != nil {
		return patchFailureResult("summarize", "", err, touched, nil)
	}
	for _, op := range ops {
		if err := a.applyOp(op); err != nil {
			return patchFailureResult("apply", op.Path, err, touched, a.patchFailureReadSuggestions(op, err))
		}
	}
	diffSummary := formatPatchSummary(summary)
	return ToolResult{
		Success:      true,
		Output:       "patch applied\n\n" + diffSummary,
		FilesTouched: uniqueStrings(touched),
		DiffSummary:  diffSummary,
		PatchSummary: summary,
	}
}

func patchFailureResult(stage, path string, err error, touched []string, suggestedReads []ReadSuggestion) ToolResult {
	message := strings.TrimSpace(err.Error())
	suggestedReads = cleanReadSuggestions(suggestedReads)
	hint := patchRecoveryHint(stage, path, message, suggestedReads)
	full := message
	if hint != "" {
		full += "; " + hint
	}
	return ToolResult{
		Success:      false,
		Error:        full,
		FilesTouched: uniqueStrings(touched),
		PatchFailure: &PatchFailure{
			Stage:          stage,
			Path:           cleanPatchFailurePath(path),
			Message:        message,
			Hint:           hint,
			SuggestedReads: suggestedReads,
		},
	}
}

func cleanPatchFailurePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func patchRecoveryHint(stage, path, message string, suggestedReads []ReadSuggestion) string {
	switch stage {
	case "parse":
		return applyPatchFormatHint
	case "summarize":
		return "re-read the target path and retry with a patch that matches the current workspace state"
	case "apply":
		lower := strings.ToLower(message)
		switch {
		case strings.Contains(lower, "hunk context not found"):
			if len(suggestedReads) > 0 {
				return "call " + formatReadSuggestionCall(suggestedReads[0]) + " to refresh exact current lines, then retry with a smaller hunk that preserves unchanged context"
			}
			target := strings.TrimSpace(path)
			if target == "" {
				target = "the target file"
			}
			return "re-read exact current lines around " + target + " and retry with a smaller hunk that preserves unchanged context"
		case strings.Contains(lower, "already exists"):
			return "use *** Update File for an existing file, or choose a new path for *** Add File"
		case strings.Contains(lower, "no such file") || strings.Contains(lower, "cannot find"):
			return "confirm the workspace-relative path exists before updating or deleting it"
		default:
			return "re-read the affected file and retry with exact current context"
		}
	default:
		return ""
	}
}

func (a PatchApplier) patchFailureReadSuggestions(op patchOp, err error) []ReadSuggestion {
	if op.Kind != "update" {
		return nil
	}
	var hunkErr *patchHunkApplyError
	if !errors.As(err, &hunkErr) {
		return nil
	}
	target, resolveErr := a.Workspace.Resolve(op.Path)
	if resolveErr != nil {
		return nil
	}
	lines, binary, readErr := readTextFileLines(target)
	if readErr != nil || binary {
		return nil
	}
	offset, limit := suggestedReadRangeForHunk(lines, hunkErr.Hunk)
	if limit <= 0 {
		return nil
	}
	reason := "refresh current context for failed patch hunk"
	if hunkErr.Index >= 0 {
		reason = fmt.Sprintf("refresh current context for failed patch hunk %d", hunkErr.Index+1)
	}
	return []ReadSuggestion{{
		Path:   cleanPatchFailurePath(op.Path),
		Offset: offset,
		Limit:  limit,
		Reason: reason,
	}}
}

func suggestedReadRangeForHunk(lines []string, hunk patchHunk) (int, int) {
	if len(lines) == 0 {
		return 1, 0
	}
	const contextLines = 20
	const minimumWindow = 40
	const maximumWindow = 120
	bestStart := bestApproximateHunkStart(lines, hunk.Old)
	if bestStart < 0 {
		bestStart = 0
	}
	start := bestStart - contextLines
	if start < 0 {
		start = 0
	}
	window := len(hunk.Old) + 2*contextLines
	if window < minimumWindow {
		window = minimumWindow
	}
	if window > maximumWindow {
		window = maximumWindow
	}
	if start+window > len(lines) {
		window = len(lines) - start
	}
	if window <= 0 {
		return 1, 0
	}
	return start + 1, window
}

func bestApproximateHunkStart(lines, old []string) int {
	bestStart := -1
	bestScore := 0
	for start := range lines {
		score := 0
		for i, oldLine := range old {
			lineIndex := start + i
			if lineIndex >= len(lines) {
				break
			}
			if oldLine == lines[lineIndex] {
				score += 2
				continue
			}
			if strings.TrimSpace(oldLine) != "" && strings.TrimSpace(oldLine) == strings.TrimSpace(lines[lineIndex]) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestStart = start
		}
	}
	if bestScore > 0 {
		return bestStart
	}
	for _, oldLine := range old {
		if strings.TrimSpace(oldLine) == "" {
			continue
		}
		for i, line := range lines {
			if line == oldLine || strings.TrimSpace(line) == strings.TrimSpace(oldLine) {
				return i
			}
		}
	}
	return -1
}

func cleanReadSuggestions(suggestions []ReadSuggestion) []ReadSuggestion {
	if len(suggestions) == 0 {
		return nil
	}
	out := make([]ReadSuggestion, 0, len(suggestions))
	seen := map[string]struct{}{}
	for _, suggestion := range suggestions {
		suggestion.Path = cleanPatchFailurePath(suggestion.Path)
		if suggestion.Path == "" || suggestion.Offset <= 0 || suggestion.Limit <= 0 {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", suggestion.Path, suggestion.Offset, suggestion.Limit)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, suggestion)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func formatReadSuggestionCall(suggestion ReadSuggestion) string {
	return fmt.Sprintf(`read_file {"path":%q,"offset":%d,"limit":%d}`, suggestion.Path, suggestion.Offset, suggestion.Limit)
}

type patchOp struct {
	Kind  string
	Path  string
	Lines []string
	Hunks []patchHunk
}

type patchHunk struct {
	Old          []string
	New          []string
	AddedLines   int
	DeletedLines int
}

type patchHunkApplyError struct {
	Index int
	Hunk  patchHunk
}

func (e *patchHunkApplyError) Error() string {
	return "hunk context not found"
}

func parseApplyPatch(patch string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("patch must start with *** Begin Patch")
	}
	var ops []patchOp
	for i := 1; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "*** End Patch":
			return ops, nil
		case strings.HasPrefix(line, "*** Add File: "):
			op := patchOp{Kind: "add", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("add file line must start with + for %s", op.Path)
				}
				op.Lines = append(op.Lines, strings.TrimPrefix(lines[i], "+"))
				i++
			}
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOp{Kind: "delete", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op := patchOp{Kind: "update", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if strings.HasPrefix(lines[i], "@@") {
					i++
					continue
				}
				var h patchHunk
				for i < len(lines) && !strings.HasPrefix(lines[i], "@@") && !strings.HasPrefix(lines[i], "*** ") {
					if lines[i] == "" && i == len(lines)-1 {
						break
					}
					if len(lines[i]) == 0 {
						return nil, fmt.Errorf("malformed patch line in %s", op.Path)
					}
					prefix, text := lines[i][0], lines[i][1:]
					switch prefix {
					case ' ':
						h.Old = append(h.Old, text)
						h.New = append(h.New, text)
					case '-':
						h.Old = append(h.Old, text)
						h.DeletedLines++
					case '+':
						h.New = append(h.New, text)
						h.AddedLines++
					default:
						return nil, fmt.Errorf("malformed patch line in %s", op.Path)
					}
					i++
				}
				if len(h.Old) > 0 || len(h.New) > 0 {
					op.Hunks = append(op.Hunks, h)
				}
			}
			if len(op.Hunks) == 0 {
				return nil, fmt.Errorf("update patch has no hunks for %s", op.Path)
			}
			ops = append(ops, op)
		case strings.TrimSpace(line) == "":
			i++
		default:
			return nil, fmt.Errorf("unexpected patch line: %s", line)
		}
	}
	return nil, fmt.Errorf("patch missing *** End Patch")
}

func (a PatchApplier) summarizeOps(ops []patchOp) (*PatchSummary, error) {
	summary := &PatchSummary{}
	for _, op := range ops {
		fileSummary := FilePatchSummary{
			Path:      filepath.Clean(op.Path),
			Operation: op.Kind,
		}
		switch op.Kind {
		case "add":
			fileSummary.AddedLines = len(op.Lines)
		case "delete":
			path, err := a.Workspace.Resolve(op.Path)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			lines, _ := splitLines(string(data))
			fileSummary.DeletedLines = len(lines)
		case "update":
			for _, hunk := range op.Hunks {
				fileSummary.AddedLines += hunk.AddedLines
				fileSummary.DeletedLines += hunk.DeletedLines
			}
		}
		summary.Files = append(summary.Files, fileSummary)
		summary.TotalAddedLines += fileSummary.AddedLines
		summary.TotalDeletedLines += fileSummary.DeletedLines
	}
	return summary, nil
}

func formatPatchSummary(summary *PatchSummary) string {
	if summary == nil {
		return "patch diff summary: unavailable"
	}
	var b strings.Builder
	b.WriteString("patch diff summary:\n")
	if len(summary.Files) == 0 {
		b.WriteString("- no files changed\n")
	} else {
		for _, file := range summary.Files {
			fmt.Fprintf(&b, "- %s: %s +%d -%d\n", file.Path, file.Operation, file.AddedLines, file.DeletedLines)
		}
	}
	fmt.Fprintf(&b, "total: +%d -%d", summary.TotalAddedLines, summary.TotalDeletedLines)
	return b.String()
}

func (a PatchApplier) applyOp(op patchOp) error {
	path, err := a.Workspace.Resolve(op.Path)
	if err != nil {
		return err
	}
	switch op.Kind {
	case "add":
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("add file target already exists: %s", op.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strings.Join(op.Lines, "\n")+"\n"), 0o644)
	case "delete":
		return os.Remove(path)
	case "update":
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, err := applyHunks(string(data), op.Hunks)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Path, err)
		}
		return os.WriteFile(path, []byte(updated), 0o644)
	default:
		return fmt.Errorf("unknown patch op: %s", op.Kind)
	}
}

func applyHunks(content string, hunks []patchHunk) (string, error) {
	lines, trailing := splitLines(content)
	cursor := 0
	for hunkIndex, h := range hunks {
		idx := findSequence(lines, h.Old, cursor)
		if idx < 0 {
			return "", &patchHunkApplyError{Index: hunkIndex, Hunk: h}
		}
		replaced := append([]string{}, lines[:idx]...)
		replaced = append(replaced, h.New...)
		replaced = append(replaced, lines[idx+len(h.Old):]...)
		lines = replaced
		cursor = idx + len(h.New)
	}
	out := strings.Join(lines, "\n")
	if trailing || out != "" {
		out += "\n"
	}
	return out, nil
}

func splitLines(content string) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	trailing := strings.HasSuffix(content, "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return []string{""}, trailing
	}
	return strings.Split(content, "\n"), trailing
}

func findSequence(lines, seq []string, start int) int {
	if len(seq) == 0 {
		return start
	}
	for i := start; i+len(seq) <= len(lines); i++ {
		ok := true
		for j := range seq {
			if lines[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
