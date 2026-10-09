// Package submodulealign moves one reused linked submodule worktree to a target
// commit with `git checkout --detach`, without touching shared Git
// configuration, the canonical submodule checkout, or sibling worktrees.
//
// The package is UI-agnostic. Inspect is read-only and reports every
// precondition as a typed Refusal; Align re-inspects, optionally fetches, moves
// HEAD, and verifies the result.
package submodulealign

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Request identifies one submodule worktree and the commit to move it to.
type Request struct {
	// ParentPath is the absolute root of the parent checkout whose gitlink pins
	// the submodule.
	ParentPath string `json:"parent_path"`
	// SubmodulePath is the slash-separated path of the submodule relative to
	// ParentPath.
	SubmodulePath string `json:"submodule_path"`
	// TargetCommit is an optional full object id. Empty means the gitlink the
	// parent currently pins (the index entry when staged, otherwise HEAD).
	TargetCommit string `json:"target_commit,omitempty"`
	// FetchIfMissing allows fetching from the submodule's configured remotes
	// when the target commit is absent from the shared object store.
	FetchIfMissing bool `json:"fetch_if_missing,omitempty"`
}

// NormalizeRequest validates syntax only. It never touches the filesystem, so
// control validation can use it without running Git.
func NormalizeRequest(req Request) (Request, error) {
	req.ParentPath = strings.TrimSpace(req.ParentPath)
	if req.ParentPath == "" || !filepath.IsAbs(req.ParentPath) {
		return Request{}, fmt.Errorf("parent_path must be an absolute checkout path")
	}
	req.ParentPath = filepath.Clean(req.ParentPath)
	if req.ParentPath == string(filepath.Separator) {
		return Request{}, fmt.Errorf("parent_path must be an absolute checkout path")
	}
	sub, err := normalizeSubmodulePath(req.SubmodulePath)
	if err != nil {
		return Request{}, err
	}
	req.SubmodulePath = sub
	target, err := normalizeCommit(req.TargetCommit)
	if err != nil {
		return Request{}, err
	}
	req.TargetCommit = target
	return req, nil
}

func normalizeSubmodulePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("submodule_path is required")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("submodule_path contains a NUL byte")
	}
	slashed := filepath.ToSlash(raw)
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(raw) {
		return "", fmt.Errorf("submodule_path must be relative to parent_path")
	}
	clean := path.Clean(slashed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("submodule_path must stay inside parent_path")
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("submodule_path must not contain a .git component")
		}
	}
	return clean, nil
}

func normalizeCommit(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if len(raw) != 40 && len(raw) != 64 {
		return "", fmt.Errorf("target_commit must be a full 40 or 64 character object id")
	}
	for _, r := range raw {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("target_commit must be a hexadecimal object id")
		}
	}
	return raw, nil
}
