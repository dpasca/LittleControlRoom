package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/events"
	"lcroom/internal/scanner"
	"lcroom/internal/worktreerecovery"
)

type WorktreeRecovery struct {
	ProjectPath   string
	RootPath      string
	Location      string
	Phase         string
	RetainedBytes int64
	Verified      bool
}

func (s *Service) ListWorktreeRecoveries(ctx context.Context) ([]WorktreeRecovery, error) {
	entries, err := os.ReadDir(s.recoveryBase())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var results []WorktreeRecovery
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(s.recoveryBase(), entry.Name())
		j, err := worktreerecovery.Load(dir)
		if err != nil {
			return nil, err
		}
		if j.Phase == "purged" {
			continue
		}
		results = append(results, recoveryStatus(j))
	}
	return results, nil
}

// WorktreeRecoveryStatus reads the durable receipt. Listing or reporting a
// completed action must not rehash the entire recovery. Review, restore, purge,
// and resumed removal still independently verify it before acting.
func (s *Service) WorktreeRecoveryStatus(ctx context.Context, path string) (*WorktreeRecovery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j, err := worktreerecovery.Load(worktreerecovery.Location(s.recoveryBase(), path))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := recoveryStatus(j)
	return &r, nil
}

func recoveryStatus(j *worktreerecovery.Journal) WorktreeRecovery {
	return WorktreeRecovery{ProjectPath: j.Original, RootPath: j.Root, Location: j.Directory, Phase: j.Phase, RetainedBytes: j.RetainedBytes, Verified: !j.Verified.IsZero() && j.Phase != "purged"}
}

func (s *Service) recoveryResumeCandidate(ctx context.Context, path string) (StaleWorktreeCleanupCandidate, bool, error) {
	j, err := worktreerecovery.Load(worktreerecovery.Location(s.recoveryBase(), path))
	if os.IsNotExist(err) {
		return StaleWorktreeCleanupCandidate{}, false, nil
	}
	if err != nil {
		return StaleWorktreeCleanupCandidate{}, false, nil
	}
	if j.Phase != "relocating" && j.Phase != "relocated" && j.Phase != "discarding_duplicate" && j.Phase != "promoting" && j.Phase != "promoted" && j.Phase != "removed" {
		return StaleWorktreeCleanupCandidate{}, false, nil
	}
	summary, err := s.store.GetProjectSummary(ctx, path, true)
	if err != nil {
		return StaleWorktreeCleanupCandidate{}, false, nil
	}
	if summary.Pinned {
		return StaleWorktreeCleanupCandidate{}, false, fmt.Errorf("worktree is pinned")
	}
	if j.Phase == "removed" && !summary.PresentOnDisk {
		return StaleWorktreeCleanupCandidate{}, false, nil
	}
	return StaleWorktreeCleanupCandidate{ProjectPath: path, RootProjectPath: j.Root, ProjectName: filepath.Base(path), RecoveryResume: true}, true, nil
}

func recoveryPathKey(path string) string { return filepath.Base(worktreerecovery.Location("", path)) }

func (s *Service) recoveryBase() string {
	p := filepath.Join(s.cfg.DataDir, "worktree-recoveries")
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// ReviewWorktreeRecovery re-verifies on a worker, not the TUI render path.
func (s *Service) ReviewWorktreeRecovery(ctx context.Context, path string) (*WorktreeRecovery, error) {
	dir := worktreerecovery.Location(s.recoveryBase(), path)
	j, err := worktreerecovery.Load(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bytes, err := worktreerecovery.Storage(dir)
	if err != nil {
		return nil, err
	}
	r := &WorktreeRecovery{ProjectPath: j.Original, RootPath: j.Root, Location: dir, Phase: j.Phase, RetainedBytes: bytes}
	if j.Phase == "purged" {
		return r, nil
	}
	err = j.Verify(ctx)
	r.Verified = err == nil
	return r, err
}

func (s *Service) RestoreWorktreeRecovery(ctx context.Context, path, destination string) error {
	unlock, err := worktreerecovery.Lock(s.recoveryBase(), path)
	if err != nil {
		return err
	}
	defer unlock()
	j, err := worktreerecovery.Load(worktreerecovery.Location(s.recoveryBase(), path))
	if err != nil {
		return err
	}
	return j.Restore(ctx, destination)
}

func (s *Service) PurgeWorktreeRecovery(ctx context.Context, path, confirmation string) error {
	unlock, err := worktreerecovery.Lock(s.recoveryBase(), path)
	if err != nil {
		return err
	}
	defer unlock()
	j, err := worktreerecovery.Load(worktreerecovery.Location(s.recoveryBase(), path))
	if err != nil {
		return err
	}
	if err := checkRemovalProcesses(ctx, j.Directory); err != nil {
		return err
	}
	return j.Purge(ctx, confirmation)
}

// recoveryNeeded inventories the entire removal tree instead of aborting at
// the first dependency. All nested repositories use local preservation; there
// is no credential/network prerequisite and no cache-name exemption.
func recoveryNeeded(ctx context.Context, path string) (bool, error) {
	found := false
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == path {
			return nil
		}
		if d.Name() == ".git" {
			// The selected worktree's own pointer is expected. Nested pointer
			// files also represent repositories with administrative history.
			if filepath.Dir(p) != path {
				found = true
			}
			if d.IsDir() {
				found = true
				return filepath.SkipDir
			}
			if d.Type()&os.ModeSymlink != 0 {
				found = true
			}
			return nil
		}
		if d.IsDir() {
			if _, err := os.Lstat(filepath.Join(p, "HEAD")); err == nil {
				if _, err := os.Lstat(filepath.Join(p, "objects")); err == nil {
					found = true
					return filepath.SkipDir
				}
			}
		}
		return nil
	})
	if err == nil && !found {
		ignored, gitErr := cloneGitOutput(ctx, path, "", "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
		if gitErr != nil {
			return false, gitErr
		}
		found = ignored != ""
	}
	return found, err
}

// WorktreeConsumerScanCanceledError means cancellation stopped read-only consumer
// inspection before recovery could relocate or remove the checkout.
type WorktreeConsumerScanCanceledError struct{}

func (*WorktreeConsumerScanCanceledError) Error() string {
	return "cleanup canceled while checking external consumers; checkout left untouched"
}

func (*WorktreeConsumerScanCanceledError) Unwrap() error { return context.Canceled }

// Resolve only roots and actual references, never every ordinary file visited.
func recoveryConsumerPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func recoveryConsumerRoots(roots []string) []string {
	unique := make(map[string]bool, len(roots))
	for _, root := range roots {
		// Keep the leaf link itself for consumer detection as well as its
		// resolved directory for traversal. WalkDir does not follow symlinks.
		unique[filepath.Join(recoveryConsumerPath(filepath.Dir(root)), filepath.Base(root))] = true
		unique[recoveryConsumerPath(root)] = true
	}
	ordered := make([]string, 0, len(unique))
	for root := range unique {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	result := make([]string, 0, len(ordered))
	for _, root := range ordered {
		covered := false
		for _, parent := range result {
			if removalPathWithin(root, parent) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, root)
		}
	}
	return result
}

// Inspect Git metadata of known projects and sibling repositories. Searching
// every working file under their parent directories made each approved removal
// depend on unrelated build caches and cloud folders. Git alternates have no
// reverse index; unknown repositories outside this inventory remain outside
// the scope of this check.
func (s *Service) checkRecoveryConsumers(ctx context.Context, path string) error {
	var candidates []string
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		candidates = append(candidates, filepath.Join(filepath.Dir(path), entry.Name()))
	}
	projects, err := s.store.ListProjects(ctx, true)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.PresentOnDisk {
			candidates = append(candidates, p.Path)
		}
	}
	s.publishWorktreeRemovalProgress(path, "Checking external consumers: locating Git metadata...")
	roots, err := recoveryConsumerMetadataRoots(ctx, path, s.recoveryBase(), candidates)
	if err != nil {
		return err
	}
	return inspectRecoveryConsumersWithProgress(ctx, path, s.recoveryBase(), roots, func(current string, entries int64) {
		s.publishWorktreeRemovalProgress(path, fmt.Sprintf("Checking external consumers: %d entries · %s", entries, current))
	})
}

func recoveryConsumerMetadataRoots(ctx context.Context, path, recoveryBase string, candidates []string) ([]string, error) {
	path, recoveryBase = recoveryConsumerPath(path), recoveryConsumerPath(recoveryBase)
	var roots, metadata []string
	seen := map[string]bool{}
	outside := func(p string) bool {
		return !removalPathWithin(p, path) && !removalPathWithin(p, recoveryBase)
	}
	readPointer := func(p, prefix string) (string, error) {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if !strings.HasPrefix(value, prefix) || value == prefix {
			return "", nil
		}
		value = strings.TrimPrefix(value, prefix)
		if !filepath.IsAbs(value) {
			value = filepath.Join(filepath.Dir(p), value)
		}
		return recoveryConsumerPath(value), nil
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, recoveryConsumerContextError(err)
		}
		// Keep a leaf alias visible even when its target is the removal tree.
		candidate = filepath.Join(recoveryConsumerPath(filepath.Dir(candidate)), filepath.Base(candidate))
		if seen[candidate] || !outside(candidate) {
			continue
		}
		seen[candidate] = true
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target := recoveryConsumerPath(candidate)
			if removalPathWithin(target, path) {
				roots = append(roots, candidate)
			}
			candidate = target
			if !outside(candidate) {
				continue
			}
		}
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		marker := filepath.Join(candidate, ".git")
		gitInfo, err := os.Stat(marker)
		switch {
		case err == nil && gitInfo.IsDir():
			roots = append(roots, marker)
			metadata = append(metadata, recoveryConsumerPath(marker))
		case err == nil && gitInfo.Mode().IsRegular():
			roots = append(roots, marker)
			target, err := readPointer(marker, "gitdir: ")
			if err != nil {
				return nil, err
			}
			if target != "" {
				metadata = append(metadata, target)
			}
		case err != nil && !os.IsNotExist(err):
			return nil, err
		default:
			// Bare repositories have no .git marker.
			if _, err := os.Stat(filepath.Join(candidate, "HEAD")); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, err
			}
			if objects, err := os.Stat(filepath.Join(candidate, "objects")); err == nil && objects.IsDir() {
				metadata = append(metadata, candidate)
			} else if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	seen = map[string]bool{}
	for n := 0; n < len(metadata); n++ {
		if err := ctx.Err(); err != nil {
			return nil, recoveryConsumerContextError(err)
		}
		gitDir := metadata[n]
		if seen[gitDir] || !outside(gitDir) {
			continue
		}
		seen[gitDir] = true
		roots = append(roots, gitDir)
		common, err := readPointer(filepath.Join(gitDir, "commondir"), "")
		if err != nil {
			return nil, err
		}
		if common != "" {
			metadata = append(metadata, common)
		}
	}
	return roots, nil
}

func recoveryConsumerContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &WorktreeConsumerScanCanceledError{}
	}
	return err
}

func inspectRecoveryConsumers(ctx context.Context, path, recoveryBase string, roots []string) error {
	return inspectRecoveryConsumersWithProgress(ctx, path, recoveryBase, roots, nil)
}

func (s *Service) publishWorktreeRemovalProgress(path, detail string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{Type: events.WorktreeRemovalProgress, At: time.Now(), ProjectPath: path, Payload: map[string]string{"detail": detail}})
}

func inspectRecoveryConsumersWithProgress(ctx context.Context, path, recoveryBase string, roots []string, progress func(string, int64)) error {
	// The caller owns the removal deadline, including consumer inspection and
	// local recovery. Do not impose a shorter hidden deadline on large scans.
	var entries int64
	lastProgress := time.Time{}
	report := func(current string) {
		if progress == nil || time.Since(lastProgress) < time.Second {
			return
		}
		progress(current, entries)
		lastProgress = time.Now()
	}

	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return &WorktreeConsumerScanCanceledError{}
		}
		return err
	}
	path = recoveryConsumerPath(path)
	recoveryBase = recoveryConsumerPath(recoveryBase)
	var problems []error
	for _, root := range recoveryConsumerRoots(roots) {
		if root == path || removalPathWithin(root, path) || root == recoveryBase || removalPathWithin(root, recoveryBase) {
			continue
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) && len(problems) == 0 {
				return &WorktreeConsumerScanCanceledError{}
			}
			return errors.Join(append(problems, fmt.Errorf("external consumer inspection incomplete at %s: %w", root, err))...)
		}
		report(root)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				// Persisted project presence can be stale, and caches can disappear
				// during traversal. An absent path cannot be a live consumer.
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			entries++
			if d.IsDir() {
				report(p)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == path || p == recoveryBase {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				target, linkErr := filepath.EvalSymlinks(p)
				if linkErr == nil && (target == path || removalPathWithin(target, path)) {
					problems = append(problems, fmt.Errorf("external symbolic-link consumer %s points into removal tree: %s", p, target))
				}
				return nil
			}
			if d.IsDir() {
				// Loose objects and packs contain data, not reverse dependency
				// metadata. Keep objects/info (including alternates) in scope.
				if filepath.Base(filepath.Dir(p)) == "objects" && d.Name() != "info" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() == ".git" || d.Name() == "commondir" {
				data, err := os.ReadFile(p)
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				value := strings.TrimSpace(string(data))
				if d.Name() == ".git" {
					if !strings.HasPrefix(value, "gitdir: ") {
						return nil
					}
					value = strings.TrimPrefix(value, "gitdir: ")
				}
				if !filepath.IsAbs(value) {
					value = filepath.Join(filepath.Dir(p), value)
				}
				if resolved, err := filepath.EvalSymlinks(value); err == nil {
					value = resolved
				}
				if value == path || removalPathWithin(value, path) {
					problems = append(problems, fmt.Errorf("external Git metadata consumer %s depends on %s", p, value))
				}
				return nil
			}
			if d.Name() != "alternates" || filepath.Base(filepath.Dir(p)) != "info" {
				return nil
			}
			data, err := os.ReadFile(p)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line == "" {
					continue
				}
				if !filepath.IsAbs(line) {
					line = filepath.Join(filepath.Dir(filepath.Dir(p)), line)
				}
				if resolved, err := filepath.EvalSymlinks(line); err == nil {
					line = resolved
				}
				if line == path || removalPathWithin(line, path) {
					problems = append(problems, fmt.Errorf("external object consumer %s borrows from %s", p, line))
				}
			}
			return nil
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if errors.Is(ctxErr, context.Canceled) && len(problems) == 0 {
					return &WorktreeConsumerScanCanceledError{}
				}
				return errors.Join(append(problems, fmt.Errorf("external consumer inspection incomplete at %s: %w", root, ctxErr))...)
			}
			problems = append(problems, fmt.Errorf("external consumer inspection incomplete at %s: %w", root, err))
		}
	}
	return errors.Join(problems...)
}

// recoverAndRemove is entered only after the normal primary/dirty-source and
// process safeguards. Git removes exact registrations after atomic relocation;
// it never gets a chance to recursively delete a newly recreated original path.
func (s *Service) recoverAndRemove(ctx context.Context, root, path string) (bool, error) {
	ctx = worktreerecovery.WithProgress(ctx, func(p worktreerecovery.Progress) {
		s.publishWorktreeRemovalProgress(path, fmt.Sprintf("%s · %d entries · %.1f MiB processed\n%s", p.Stage, p.Entries, float64(p.Bytes)/(1024*1024), p.Path))
	})
	for p := s.recoveryBase(); p != filepath.Dir(p); p = filepath.Dir(p) {
		if appfs.IsManagedInternalPath(p, []string{appfs.InternalWorkspaceRoot(appfs.DefaultDataDir())}) {
			return true, fmt.Errorf("recovery storage must be outside temporary task workspaces")
		}
	}
	directory := worktreerecovery.Location(s.recoveryBase(), path)
	j, loadErr := worktreerecovery.Load(directory)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return true, loadErr
	}
	if loadErr != nil {
		if _, err := os.Lstat(directory); err == nil {
			return true, fmt.Errorf("incomplete recovery journal at %s; source left untouched: %w", directory, loadErr)
		} else if !os.IsNotExist(err) {
			return true, err
		}
	}
	if j == nil {
		s.publishWorktreeRemovalProgress(path, "Inspecting files to preserve...")
		needed, err := recoveryNeeded(ctx, path)
		if err != nil {
			return false, err
		}
		if !needed {
			return false, nil
		}
	}
	unlock, err := worktreerecovery.Lock(s.recoveryBase(), path)
	if err != nil {
		return true, err
	}
	defer unlock()
	fail := func(err error) (bool, error) {
		return true, fmt.Errorf("worktree cleanup blocked; recovery %s: %w", directory, err)
	}
	if _, err := os.Lstat(path); err == nil {
		admin, err := cloneGitOutput(ctx, path, "", "rev-parse", "--absolute-git-dir")
		if err != nil {
			return fail(err)
		}
		parent, err := gitPath(ctx, root, "worktrees")
		if err != nil {
			return fail(err)
		}
		if !samePath(filepath.Dir(admin), parent) {
			return fail(fmt.Errorf("selected checkout does not own an administrative directory in the expected repository"))
		}
		backlink, err := os.ReadFile(filepath.Join(admin, "gitdir"))
		if err != nil {
			return fail(err)
		}
		if !samePath(strings.TrimSpace(string(backlink)), filepath.Join(path, ".git")) {
			return fail(fmt.Errorf("worktree pointer belongs to another checkout; selected directory left untouched"))
		}
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	if j == nil || j.Verified.IsZero() {
		if err := checkRemovalProcesses(ctx, path); err != nil {
			return fail(err)
		}
		registrations, err := scanner.ListGitWorktrees(ctx, root)
		if err != nil {
			return fail(err)
		}
		for _, r := range registrations {
			if samePath(r.Path, path) && (r.IsMain || r.LockedReason != "") {
				return fail(fmt.Errorf("primary or locked worktree: %s", path))
			}
		}
		if err := s.checkRecoveryConsumers(ctx, path); err != nil {
			return fail(err)
		}
		s.publishWorktreeRemovalProgress(path, "Preparing and verifying local recovery...")
		j, err = worktreerecovery.Prepare(ctx, s.recoveryBase(), root, path)
		if err != nil {
			return fail(err)
		}
	}
	receipt := worktreeRemovalPlan{RootPath: root, Path: path, Commit: j.Repositories[0].Head}
	for _, repo := range j.Repositories {
		receipt.Clones = append(receipt.Clones, removalClone{Path: repo.Path, Head: repo.Head, Refs: repo.Refs})
	}
	if err := s.saveRemovalReceipt(ctx, receipt); err != nil {
		return fail(err)
	}
	// The manifest resolves filesystem aliases. Ownership checks must compare
	// child paths in that same namespace (not /var against /private/var).
	root, path = j.Root, j.Original
	// Capture owned submodule registrations before relocation. Foreign linked
	// checkouts still require review and are never silently unregistered.
	var children []ownedRemovalChild
	var childProblems []error
	tree, err := readResidualGitTree(ctx, root, j.Repositories[0].Head)
	if err != nil {
		return fail(err)
	}
	for _, r := range j.Repositories[1:] {
		if samePath(r.GitDir, r.Common) {
			continue
		}
		if j.Phase == "verified" || j.Phase == "relocating" {
			rel, err := filepath.Rel(path, r.Path)
			if err != nil {
				childProblems = append(childProblems, err)
				continue
			}
			configured, err := cloneGitOutput(ctx, r.Path, "", "config", "--file", filepath.Join(r.Common, "config"), "--get", "core.worktree")
			var exit *exec.ExitError
			if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
				childProblems = append(childProblems, err)
				continue
			}
			if configured != "" {
				if !filepath.IsAbs(configured) {
					configured = filepath.Join(r.Common, configured)
				}
				if !samePath(configured, filepath.Join(root, rel)) {
					childProblems = append(childProblems, fmt.Errorf("invalid shared submodule core.worktree for %s; repair the primary submodule metadata before cleanup", r.Path))
					continue
				}
			}
			child, err := inspectOwnedRemovalChild(ctx, root, path, r.Path, tree)
			if err != nil {
				childProblems = append(childProblems, err)
				continue
			}
			children = append(children, child)
		} else {
			rel, _ := filepath.Rel(path, r.Path)
			if tree[filepath.ToSlash(rel)].Mode != "160000" {
				return fail(fmt.Errorf("unrelated nested linked worktree %s", r.Path))
			}
			children = append(children, ownedRemovalChild{Path: r.Path, Repository: filepath.Join(root, rel), GitDir: r.GitDir})
		}
	}
	if err := errors.Join(childProblems...); err != nil {
		return fail(err)
	}
	plan := worktreeRemovalPlan{RootPath: root, Path: path, ResolvedPath: path, Children: children}
	if j.Phase == "verified" || j.Phase == "relocating" {
		if err := verifyRemovalChildRegistrations(ctx, plan, tree, false); err != nil {
			return fail(err)
		}
	}
	if _, err := os.Lstat(path); err == nil {
		if err := checkRemovalProcesses(ctx, path); err != nil {
			return fail(err)
		}
	}
	if err := checkRemovalProcesses(ctx, j.Directory); err != nil {
		return fail(err)
	}
	s.publishWorktreeRemovalProgress(path, "Preserving checkout in local recovery...")
	if err := j.Relocate(ctx); err != nil {
		return fail(err)
	}
	s.publishWorktreeRemovalProgress(path, "Removing preserved worktree registrations...")
	for _, child := range children {
		if err := removeAbsentRecoveryRegistration(ctx, child.Repository, child.Path, child.GitDir, j); err != nil {
			return fail(err)
		}
	}
	if err := removeAbsentRecoveryRegistration(ctx, root, path, j.Repositories[0].GitDir, j); err != nil {
		return fail(err)
	}
	if err := verifyRemovalChildRegistrations(ctx, plan, tree, true); err != nil {
		return fail(err)
	}
	if err := j.Complete(ctx); err != nil {
		return fail(err)
	}
	return true, nil
}

func removeAbsentRecoveryRegistration(ctx context.Context, root, path, gitDir string, journal *worktreerecovery.Journal) error {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("path recreated after preservation: %s", path)
	}
	worktrees, err := scanner.ListGitWorktrees(ctx, root)
	if err != nil {
		return err
	}
	for _, w := range worktrees {
		if samePath(w.Path, path) {
			if w.IsMain || w.LockedReason != "" {
				return fmt.Errorf("primary or locked worktree: %s", path)
			}
			common, err := gitPath(ctx, root, "worktrees")
			if err != nil {
				return err
			}
			if !samePath(filepath.Dir(gitDir), common) {
				return fmt.Errorf("unexpected worktree administrative directory: %s", gitDir)
			}
			backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
			if err != nil {
				return err
			}
			if !samePath(strings.TrimSpace(string(backlink)), filepath.Join(path, ".git")) {
				return fmt.Errorf("worktree registration changed: %s", gitDir)
			}
			if err := journal.PreserveRegistration(ctx, gitDir); err != nil {
				return err
			}
		}
	}
	worktrees, err = scanner.ListGitWorktrees(ctx, root)
	if err != nil {
		return err
	}
	for _, w := range worktrees {
		if samePath(w.Path, path) {
			return fmt.Errorf("registration still present: %s", path)
		}
	}
	return nil
}
