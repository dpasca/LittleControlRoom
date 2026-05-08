package tools

import (
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

func (a PatchApplier) Apply(patch string) ToolResult {
	if err := a.Workspace.AllowPatch(); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	ops, err := parseApplyPatch(patch)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	touched := make([]string, 0, len(ops))
	for _, op := range ops {
		if _, err := a.Workspace.Resolve(op.Path); err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
		touched = append(touched, filepath.Clean(op.Path))
	}
	for _, op := range ops {
		if err := a.applyOp(op); err != nil {
			return ToolResult{Success: false, Error: err.Error(), FilesTouched: uniqueStrings(touched)}
		}
	}
	return ToolResult{Success: true, Output: "patch applied", FilesTouched: uniqueStrings(touched)}
}

type patchOp struct {
	Kind  string
	Path  string
	Lines []string
	Hunks []patchHunk
}

type patchHunk struct {
	Old []string
	New []string
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
					case '+':
						h.New = append(h.New, text)
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
	for _, h := range hunks {
		idx := findSequence(lines, h.Old, cursor)
		if idx < 0 {
			return "", fmt.Errorf("hunk context not found")
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
