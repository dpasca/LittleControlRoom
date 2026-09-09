package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

// The receipt is written before the first Git mutation. In particular, detached
// child HEADs must survive removal of their administrative worktree directories.
type worktreeRemovalPlan struct {
	ResolvedPath string
	RootPath     string
	Path         string
	Commit       string
	Branch       string
	Children     []ownedRemovalChild
	Bytes        int64
	Entries      []residualWorktreeEntry `json:"-"`
}

type ownedRemovalChild struct {
	Path       string
	Repository string
	Commit     string
	Branch     string
	GitDir     string
}

// RetainedWorktreeError distinguishes disk residue from a completed removal.
// Bytes is a logical size, not allocated disk usage; inspection errors are
// included in Reason so an incomplete size is never presented as authoritative.
type RetainedWorktreeError struct {
	Path                 string
	Bytes                int64
	Reason               string
	RegistrationRetained bool
}

func (e *RetainedWorktreeError) Error() string {
	next := "Open the orphaned worktree inspection to review cleanup"
	if e.RegistrationRetained {
		next = "The outer registration is retained or unverified; inspect the folder and retry removal"
	}
	return fmt.Sprintf("Worktree removal incomplete: retained %s (%d logical bytes). %s. %s; stop any processes using this path before retrying.", e.Path, e.Bytes, e.Reason, next)
}

func retainedDirectoryBytes(ctx context.Context, path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// Reject symlink ancestors, including substitution of a parent directory after
// inspection. macOS's system /var alias is resolved once at the entry boundary.
func removalPathIdentity(path string) (os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("unsafe removal path %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if resolved != filepath.Join(parent, filepath.Base(path)) {
		return nil, fmt.Errorf("removal path is a symbolic link: %s", path)
	}
	return os.Lstat(path)
}

func removalGitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("inspect %s: git %s: %w", repo, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func inspectOwnedRemovalChild(ctx context.Context, root, parent, path string, tree map[string]residualGitTreeEntry) (ownedRemovalChild, error) {
	pointer := filepath.Join(path, ".git")
	info, err := os.Lstat(pointer)
	if err != nil {
		return ownedRemovalChild{}, err
	}
	if !info.Mode().IsRegular() {
		return ownedRemovalChild{}, fmt.Errorf("nested worktree metadata requires review: %s", pointer)
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return ownedRemovalChild{}, err
	}
	if tree[filepath.ToSlash(rel)].Mode != "160000" {
		return ownedRemovalChild{}, fmt.Errorf("unrelated nested repository at %s; no preserved parent gitlink", path)
	}
	repo := filepath.Join(root, rel)
	common, err := removalGitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ownedRemovalChild{}, err
	}
	modules, err := gitPath(ctx, root, "modules")
	if err != nil {
		return ownedRemovalChild{}, err
	}
	modules, err = filepath.EvalSymlinks(modules)
	if err != nil {
		return ownedRemovalChild{}, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return ownedRemovalChild{}, err
	}
	if !removalPathWithin(common, modules) {
		return ownedRemovalChild{}, fmt.Errorf("nested repository object store is outside the parent's modules directory: %s", path)
	}
	actual, err := removalGitOutput(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ownedRemovalChild{}, err
	}
	if !samePath(actual, common) {
		return ownedRemovalChild{}, fmt.Errorf("unrelated nested repository at %s", path)
	}
	gitDir, err := removalGitOutput(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ownedRemovalChild{}, err
	}
	if !samePath(filepath.Dir(gitDir), filepath.Join(common, "worktrees")) {
		return ownedRemovalChild{}, fmt.Errorf("nested checkout is not an owned linked worktree: %s", path)
	}
	backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil || !samePath(strings.TrimSpace(string(backlink)), filepath.Join(path, ".git")) {
		return ownedRemovalChild{}, fmt.Errorf("nested worktree pointer identity changed: %s", path)
	}
	registrations, err := scanner.ListGitWorktrees(ctx, repo)
	if err != nil {
		return ownedRemovalChild{}, err
	}
	for _, registration := range registrations {
		if !samePath(registration.Path, path) {
			continue
		}
		if registration.IsMain || registration.LockedReason != "" || registration.PrunableReason != "" {
			return ownedRemovalChild{}, fmt.Errorf("nested worktree is primary, locked or prunable: %s", path)
		}
		status, err := removalGitOutput(ctx, path, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
		if err != nil {
			return ownedRemovalChild{}, err
		}
		if status != "" {
			return ownedRemovalChild{}, fmt.Errorf("nested worktree has dirty or untracked work: %s", path)
		}
		return ownedRemovalChild{Path: path, Repository: repo, Commit: registration.Head, Branch: registration.Branch, GitDir: gitDir}, nil
	}
	return ownedRemovalChild{}, fmt.Errorf("nested worktree has no exact registration: %s", path)
}

func removalPathWithin(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// inspectRemovalPlan never infers ownership from a directory name. A gitlink
// from the preserved commit, the root's shared module store, and both directions
// of the child's registration must agree. Even force removal uses this check.
func inspectRemovalPlan(ctx context.Context, root, path, commit string, verifyFiles bool) (worktreeRemovalPlan, error) {
	plan := worktreeRemovalPlan{RootPath: root, Path: path, Commit: commit}
	identity, err := removalPathIdentity(path)
	if err != nil {
		return plan, err
	}
	if !identity.IsDir() || samePath(root, path) || removalPathWithin(root, path) {
		return plan, fmt.Errorf("unsafe worktree directory %s", path)
	}
	plan.ResolvedPath, err = filepath.EvalSymlinks(path)
	if err != nil {
		return plan, err
	}
	tree, err := readResidualGitTree(ctx, root, commit)
	if err != nil {
		return plan, err
	}
	hash, err := residualObjectHash(commit)
	if err != nil {
		return plan, err
	}
	var untrackedPaths []string
	err = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode().Type() != entry.Type() {
			return fmt.Errorf("worktree entry changed during inspection: %s", current)
		}
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if isSymlink && entry.Name() == ".git" {
			return fmt.Errorf("symbolic link in Git metadata requires review: %s", current)
		}
		if current != path && entry.IsDir() {
			if _, err := os.Lstat(filepath.Join(current, "HEAD")); err == nil {
				if info, err := os.Lstat(filepath.Join(current, "objects")); err == nil && info.IsDir() {
					return fmt.Errorf("possible nested bare repository requires review: %s", current)
				}
			}
			if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
				child, err := inspectOwnedRemovalChild(ctx, root, path, current, tree)
				if err != nil {
					return err
				}
				// Nested Git metadata needs its own ownership review; Git
				// status alone can hide it. WalkDir never follows symlinks.
				err = filepath.WalkDir(current, func(p string, e os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					if e.Name() == ".git" && (e.Type()&os.ModeSymlink != 0 || p != filepath.Join(current, ".git")) {
						return fmt.Errorf("nested metadata requires review: %s", p)
					}
					return nil
				})
				if err != nil {
					return err
				}
				plan.Children = append(plan.Children, child)
				return filepath.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		plan.Entries = append(plan.Entries, residualWorktreeEntry{Path: current, Info: info})
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() && !isSymlink {
			return fmt.Errorf("special file requires review: %s", current)
		}
		// Links are leaf entries. Neither inspection nor deletion follows
		// their targets, including external directories and dangling links.
		if info.Mode().IsRegular() {
			plan.Bytes += info.Size()
		}
		rel, _ := filepath.Rel(path, current)
		if !verifyFiles || rel == ".git" || (filepath.Base(rel) == ".DS_Store" && !isSymlink) {
			return nil
		}
		tracked, exists := tree[filepath.ToSlash(rel)]
		if !exists {
			// Only the separately confirmed cleanup accepts ignored output.
			// Untracked source is never covered by that confirmation.
			untrackedPaths = append(untrackedPaths, filepath.ToSlash(rel))
			return nil
		}
		mode := "100644"
		if isSymlink {
			mode = "120000"
		} else if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		if tracked.Type != "blob" || tracked.Mode != mode {
			return fmt.Errorf("tracked file mode changed: %s", current)
		}
		object, err := hashResidualGitBlob(ctx, current, info, hash)
		if err != nil {
			return err
		}
		if object != tracked.Object {
			return fmt.Errorf("tracked file differs from preserved commit: %s", current)
		}
		return nil
	})
	if err != nil {
		return plan, err
	}
	if len(untrackedPaths) != 0 {
		// One Git call for the entire artifact inventory, including paths
		// containing whitespace/newlines. Large build trees must not spawn
		// one process per file during an asynchronous cleanup inspection.
		cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
		cmd.Stdin = strings.NewReader(strings.Join(untrackedPaths, "\x00") + "\x00")
		out, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			return plan, fmt.Errorf("verify ignored residue: %w", err)
		}
		ignored := make(map[string]bool, len(untrackedPaths))
		for _, path := range strings.Split(string(out), "\x00") {
			ignored[path] = true
		}
		for _, rel := range untrackedPaths {
			if !ignored[rel] {
				return plan, fmt.Errorf("untracked or unverifiable file requires review: %s", filepath.Join(path, filepath.FromSlash(rel)))
			}
		}
	}
	if err := verifyRemovalChildRegistrations(ctx, plan, tree, false); err != nil {
		return plan, err
	}
	return plan, nil
}

// Include registrations whose checkout pointer has already disappeared; a
// filesystem walk alone cannot find those interrupted child removals.
func verifyRemovalChildRegistrations(ctx context.Context, plan worktreeRemovalPlan, tree map[string]residualGitTreeEntry, removed bool) error {
	for rel, entry := range tree {
		if entry.Mode != "160000" {
			continue
		}
		repo := filepath.Join(plan.RootPath, filepath.FromSlash(rel))
		if _, err := os.Lstat(filepath.Join(repo, ".git")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		registrations, err := scanner.ListGitWorktrees(ctx, repo)
		if err != nil {
			return err
		}
		for _, registration := range registrations {
			// Resolve the parent (which may have a /var alias), even after
			// the leaf has disappeared, before testing path containment.
			parent := plan.ResolvedPath
			if !removalPathWithin(registration.Path, parent) && !removalPathWithin(registration.Path, plan.Path) {
				continue
			}
			owned := false
			for _, child := range plan.Children {
				owned = owned || samePath(child.Path, registration.Path)
			}
			if removed || !owned {
				return fmt.Errorf("nested registration still requires cleanup: %s (%s)", registration.Path, registration.Head)
			}
		}
	}
	return nil
}

// Git prune acts on all expired administrative directories, not just the
// selected path. Never let that sweep erase private submodule object stores.
func checkPrunableWorktreeStores(ctx context.Context, root string) error {
	adminRoot, err := gitPath(ctx, root, "worktrees")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(adminRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		modules := filepath.Join(adminRoot, entry.Name(), "modules")
		if _, err := os.Lstat(modules); !os.IsNotExist(err) {
			return fmt.Errorf("pruning requires manual review: private submodule object store may remain at %s", modules)
		}
	}
	return nil
}

func (s *Service) saveRemovalReceipt(ctx context.Context, plan worktreeRemovalPlan) error {
	payload, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return s.store.AddEvent(ctx, model.StoredEvent{At: time.Now(), ProjectPath: plan.Path, Type: "worktree_removal_started", Payload: string(payload)})
}

func (s *Service) retainedRemovalCommit(ctx context.Context, path, root string, summary model.ProjectSummary) string {
	if s.store != nil {
		if events, err := s.store.ListRecentEventsByTypeForProject(ctx, "worktree_removal_started", path, 100); err == nil {
			for _, event := range events {
				if event.Type != "worktree_removal_started" {
					continue
				}
				var plan worktreeRemovalPlan
				if json.Unmarshal([]byte(event.Payload), &plan) == nil && samePath(plan.RootPath, root) && plan.Commit != "" {
					return plan.Commit
				}
			}
		}
	}
	commits, _, _ := residualWorktreeCommitCandidates(ctx, root, summary, "")
	if len(commits) != 0 {
		return commits[0]
	}
	return ""
}

func removeOwnedChildren(ctx context.Context, plan worktreeRemovalPlan) error {
	tree, err := readResidualGitTree(ctx, plan.RootPath, plan.Commit)
	if err != nil {
		return err
	}
	for _, child := range plan.Children {
		if err := validateRemovalPlanDirectory(plan); err != nil {
			return err
		}
		current, err := inspectOwnedRemovalChild(ctx, plan.RootPath, plan.Path, child.Path, tree)
		if err != nil {
			return err
		}
		if current != child {
			return fmt.Errorf("nested worktree changed since inspection: %s", child.Path)
		}
		if err := gitWorktreeRemove(ctx, child.Repository, child.Path, false); err != nil {
			return err
		}
		registrations, err := scanner.ListGitWorktrees(ctx, child.Repository)
		if err != nil {
			return err
		}
		for _, registration := range registrations {
			if samePath(registration.Path, child.Path) {
				return fmt.Errorf("nested registration remains: %s", child.Path)
			}
		}
		if _, err := os.Lstat(child.Path); !os.IsNotExist(err) {
			return fmt.Errorf("nested path remains after removal: %s", child.Path)
		}
	}
	return verifyRemovalChildRegistrations(ctx, plan, tree, true)
}

func validateRemovalPlanDirectory(plan worktreeRemovalPlan) error {
	resolved, err := filepath.EvalSymlinks(plan.Path)
	if err != nil || resolved != plan.ResolvedPath {
		return fmt.Errorf("worktree path identity changed: %s", plan.Path)
	}
	for _, entry := range plan.Entries {
		if !entry.Info.IsDir() {
			continue
		}
		info, err := os.Lstat(entry.Path)
		if err != nil || !sameResidualFileSnapshot(entry.Info, info) {
			return fmt.Errorf("worktree directory identity changed: %s", entry.Path)
		}
	}
	return nil
}

// An open cwd can recreate output just as an open writable descriptor can.
// Do not kill shared services (for example adb); report them for user review.
func checkRemovalProcesses(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-Fpn", "+D", path)
	out, err := cmd.Output()
	if len(out) != 0 {
		return fmt.Errorf("processes still use %s: %s", path, strings.TrimSpace(string(out)))
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify processes using %s: %w", path, err)
	}
	return nil
}

func (s *Service) recordRetainedRemoval(ctx context.Context, path string, cause error, orphaned bool) error {
	size, sizeErr := retainedDirectoryBytes(ctx, path)
	reason := cause.Error()
	if sizeErr != nil {
		reason += "; size is incomplete: " + sizeErr.Error()
	}
	retained := &RetainedWorktreeError{Path: path, Bytes: size, Reason: reason, RegistrationRetained: !orphaned}
	if s.store != nil {
		// Use a short independent context so a cancelled Git operation still
		// leaves a durable, visible partial result.
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var forgetErr error
		if orphaned {
			forgetErr = s.store.SetForgotten(persistCtx, path, true)
		}
		err := errors.Join(forgetErr, s.store.SetProjectPresence(persistCtx, path, true),
			s.store.AddEvent(persistCtx, model.StoredEvent{At: time.Now(), ProjectPath: path, Type: "worktree_removal_incomplete", Payload: retained.Error()}))
		s.forgetProjectState(path)
		if err != nil {
			return errors.Join(retained, err)
		}
	}
	return retained
}

func retainedSizeLabel(bytes int64) string {
	return strconv.FormatInt(bytes, 10) + " bytes (logical size)"
}
