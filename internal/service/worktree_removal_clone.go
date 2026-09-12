package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Ignored clones are disposable only when their entire object inventory is
// reachable from refs currently advertised by an upstream outside the removal
// tree. Remote-tracking refs alone are not proof: they can contain local work.
// This also covers bare package caches and clones borrowing from those caches.
type removalClone struct {
	Path     string
	Head     string
	Refs     string
	Upstream string
	Entries  []residualWorktreeEntry `json:"-"`
}

func cloneGitOutput(ctx context.Context, repo, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "--no-replace-objects", "-c", "core.fsmonitor=false", "-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		// Do not echo credential-bearing remote URLs or credential-helper output.
		return "", fmt.Errorf("Git verification failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func inspectRemovalCloneIntoPlan(ctx context.Context, plan *worktreeRemovalPlan, path string, tree map[string]residualGitTreeEntry) error {
	clone, err := inspectRemovalClone(ctx, *plan, path, tree)
	if err != nil {
		return fmt.Errorf("nested repository %s cannot be safely removed: %w; preserve local work or restore upstream access, then retry cleanup", path, err)
	}
	plan.Clones = append(plan.Clones, clone)
	plan.Entries = append(plan.Entries, clone.Entries...)
	for _, entry := range clone.Entries {
		if entry.Info.Mode().IsRegular() {
			plan.Bytes += entry.Info.Size()
		}
	}
	return filepath.SkipDir
}

func inspectRemovalClone(ctx context.Context, plan worktreeRemovalPlan, path string, tree map[string]residualGitTreeEntry) (removalClone, error) {
	clone := removalClone{Path: path}
	rel, err := filepath.Rel(plan.Path, path)
	if err != nil || !removalPathWithin(path, plan.Path) {
		return clone, fmt.Errorf("repository is outside the selected worktree")
	}
	rel = filepath.ToSlash(rel)
	for tracked := range tree {
		if tracked == rel || strings.HasPrefix(tracked, rel+"/") {
			return clone, fmt.Errorf("repository contains preserved parent source or a submodule gitlink")
		}
	}
	ignored, err := cloneGitOutput(ctx, plan.RootPath, rel+"/\x00", "check-ignore", "--no-index", "-z", "--stdin")
	if err != nil || ignored != rel+"/\x00" {
		return clone, fmt.Errorf("repository directory is not ignored by the parent repository")
	}
	bare, err := cloneGitOutput(ctx, path, "", "rev-parse", "--is-bare-repository")
	if err != nil {
		return clone, err
	}
	metadata := path
	if bare == "false" {
		metadata = filepath.Join(path, ".git")
		top, err := cloneGitOutput(ctx, path, "", "rev-parse", "--show-toplevel")
		if err != nil || !samePath(top, path) {
			return clone, fmt.Errorf("checkout root does not match its directory")
		}
	} else if bare != "true" {
		return clone, fmt.Errorf("repository kind is unknown")
	}
	gitDir, err := cloneGitOutput(ctx, path, "", "rev-parse", "--absolute-git-dir")
	if err != nil || !samePath(gitDir, metadata) {
		return clone, fmt.Errorf("Git metadata is not self-contained")
	}
	common, err := cloneGitOutput(ctx, path, "", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !samePath(common, metadata) {
		return clone, fmt.Errorf("Git metadata is shared with another worktree")
	}
	if entries, err := os.ReadDir(filepath.Join(metadata, "worktrees")); !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
		return clone, fmt.Errorf("repository has linked worktree registrations")
	}
	// Snapshot before Git reads so concurrent writes during verification also
	// invalidate the plan. Never follow a symlink inside administrative data.
	clone.Entries, err = snapshotRemovalClone(ctx, path, metadata)
	if err != nil {
		return clone, err
	}
	if bare == "false" {
		status, err := cloneGitOutput(ctx, path, "", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
		if err != nil || status != "" {
			return clone, fmt.Errorf("checkout has modified, untracked, ignored, or unreadable files")
		}
		files, err := cloneGitOutput(ctx, path, "", "ls-files", "-v", "-z")
		if err != nil {
			return clone, err
		}
		for _, file := range strings.Split(files, "\x00") {
			if file != "" && !strings.HasPrefix(file, "H ") {
				return clone, fmt.Errorf("index flags can hide local changes")
			}
		}
	}
	clone.Head, err = cloneGitOutput(ctx, path, "", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return clone, err
	}
	clone.Refs, err = cloneGitOutput(ctx, path, "", "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return clone, err
	}
	if _, err := cloneGitOutput(ctx, path, "", "fsck", "--connectivity-only", "--no-dangling"); err != nil {
		return clone, fmt.Errorf("repository integrity could not be verified: %w", err)
	}
	sourceRepo, upstream, err := removalCloneUpstream(ctx, plan, path)
	if err != nil {
		return clone, err
	}
	clone.Upstream = upstream
	if parsed, err := url.Parse(upstream); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		// Removal receipts must not retain credentials from a remote URL.
		parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
		clone.Upstream = parsed.String()
	}
	key := sourceRepo + "\x00" + upstream
	advertised, cached := plan.upstreamRefs[key]
	if !cached {
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		advertised, err = cloneGitOutput(probeCtx, sourceRepo, "", "ls-remote", "origin")
		if err != nil {
			return clone, fmt.Errorf("cannot verify upstream refs: %w", err)
		}
		if plan.upstreamRefs != nil {
			plan.upstreamRefs[key] = advertised
		}
	}
	objects, err := cloneGitOutput(ctx, path, "", "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	if err != nil {
		return clone, err
	}
	inventory := make(map[string]bool)
	for _, object := range strings.Fields(objects) {
		inventory[object] = true
	}
	var tips []string
	for _, line := range strings.Split(advertised, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && inventory[fields[0]] {
			tips = append(tips, fields[0])
		}
	}
	if len(tips) == 0 {
		return clone, fmt.Errorf("no upstream ref can establish object recoverability")
	}
	reachable, err := cloneGitOutput(ctx, path, strings.Join(tips, "\n")+"\n", "rev-list", "--objects", "--no-object-names", "--stdin")
	if err != nil {
		return clone, err
	}
	for _, object := range strings.Fields(reachable) {
		delete(inventory, object)
	}
	if len(inventory) != 0 {
		return clone, fmt.Errorf("%d Git objects are not verifiably recoverable from upstream (including possible local commits, stashes or reflog history)", len(inventory))
	}
	return clone, nil
}

// Follow an in-tree package cache's origin until the source survives cleanup.
// Resolve insteadOf rewrites before checking containment, including file URLs.
func removalCloneUpstream(ctx context.Context, plan worktreeRemovalPlan, repo string) (string, string, error) {
	seen := make(map[string]bool)
	for len(seen) < 16 {
		if seen[repo] {
			break
		}
		seen[repo] = true
		origin, err := cloneGitOutput(ctx, repo, "", "remote", "get-url", "origin")
		if err != nil || origin == "" {
			return "", "", fmt.Errorf("no verifiable origin remote")
		}
		local := origin
		if strings.HasPrefix(origin, "file://") {
			u, err := url.Parse(origin)
			if err != nil || (u.Host != "" && u.Host != "localhost") {
				return "", "", fmt.Errorf("unverifiable file remote")
			}
			local = u.Path
		} else if strings.Contains(origin, ":") && !filepath.IsAbs(origin) {
			return repo, origin, nil
		}
		if !filepath.IsAbs(local) {
			local = filepath.Join(repo, local)
		}
		local, err = filepath.EvalSymlinks(local)
		if err != nil {
			return "", "", fmt.Errorf("local upstream is unavailable")
		}
		if !samePath(local, plan.ResolvedPath) && !removalPathWithin(local, plan.ResolvedPath) {
			common, err := cloneGitOutput(ctx, local, "", "rev-parse", "--path-format=absolute", "--git-common-dir")
			if err != nil {
				return "", "", fmt.Errorf("local upstream metadata is unavailable")
			}
			adminRoot, err := gitPath(ctx, plan.RootPath, "worktrees")
			if err != nil {
				return "", "", err
			}
			if resolved, err := filepath.EvalSymlinks(adminRoot); err == nil {
				adminRoot = resolved
			}
			objects, err := filepath.EvalSymlinks(filepath.Join(common, "objects"))
			if err != nil || samePath(objects, plan.ResolvedPath) || removalPathWithin(objects, plan.ResolvedPath) || removalPathWithin(objects, adminRoot) {
				return "", "", fmt.Errorf("local upstream depends on the selected worktree's object store")
			}
			if _, err := os.Lstat(filepath.Join(objects, "info", "alternates")); !os.IsNotExist(err) {
				return "", "", fmt.Errorf("local upstream borrows objects; independent recovery cannot be verified")
			}
			return repo, local, nil
		}
		repo = local
	}
	return "", "", fmt.Errorf("origin chain does not lead outside the selected worktree")
}

func snapshotRemovalClone(ctx context.Context, path, metadata string) ([]residualWorktreeEntry, error) {
	var entries []residualWorktreeEntry
	err := filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		inMetadata := current == metadata || removalPathWithin(current, metadata)
		if inMetadata {
			if info.Mode()&os.ModeSymlink != 0 || strings.HasSuffix(entry.Name(), ".lock") {
				return fmt.Errorf("Git metadata contains a symlink or active lock: %s", current)
			}
			rel, _ := filepath.Rel(metadata, current)
			top := strings.Split(filepath.ToSlash(rel), "/")[0]
			switch top {
			case ".", "HEAD", "config", "description", "index", "packed-refs", "shallow", "FETCH_HEAD", "ORIG_HEAD", "COMMIT_EDITMSG", "logs", "objects", "refs", "branches", "worktrees":
			case "hooks":
				if !info.IsDir() && !strings.HasSuffix(entry.Name(), ".sample") {
					return fmt.Errorf("custom Git hook requires review: %s", current)
				}
			case "info":
				if rel != "info" && rel != filepath.Join("info", "exclude") && rel != filepath.Join("info", "refs") {
					return fmt.Errorf("custom Git info requires review: %s", current)
				}
			default:
				return fmt.Errorf("additional Git metadata requires review: %s", current)
			}
		} else if current != path && entry.Name() == ".git" {
			return fmt.Errorf("additional nested repository requires review: %s", current)
		}
		if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("special file requires review: %s", current)
		}
		entries = append(entries, residualWorktreeEntry{Path: current, Info: info})
		return nil
	})
	return entries, err
}

func validateRemovalClones(ctx context.Context, plan worktreeRemovalPlan) error {
	if len(plan.Clones) == 0 {
		return nil
	}
	if err := validateRemovalPlanDirectory(plan); err != nil {
		return err
	}
	for _, clone := range plan.Clones {
		expected := make(map[string]os.FileInfo, len(clone.Entries))
		for _, entry := range clone.Entries {
			expected[entry.Path] = entry.Info
		}
		err := filepath.WalkDir(clone.Path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil || !sameResidualFileSnapshot(expected[path], info) {
				return fmt.Errorf("nested repository changed since inspection: %s", path)
			}
			delete(expected, path)
			return nil
		})
		if err != nil {
			return err
		}
		if len(expected) != 0 {
			return fmt.Errorf("nested repository entries disappeared since inspection: %s", clone.Path)
		}
	}
	return nil
}
