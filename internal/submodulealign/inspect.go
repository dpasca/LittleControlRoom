package submodulealign

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lcroom/internal/gitlock"
)

// operationMarkers are entries in a worktree's administrative directory that
// mean an interrupted merge, rebase, or similar operation owns the checkout.
var operationMarkers = []string{
	"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD", "BISECT_LOG",
	"rebase-merge", "rebase-apply", "sequencer",
}

const maxListedPaths = 5

// Inspect gathers and checks every precondition without modifying anything. It
// returns a *Refusal for a failed precondition. A target missing from the shared
// object store is a refusal unless FetchIfMissing is set, in which case the plan
// reports NeedsFetch with unknown ancestry.
func Inspect(ctx context.Context, req Request) (Plan, error) {
	req, err := NormalizeRequest(req)
	if err != nil {
		return Plan{}, refuse(RefusalInvalidRequest, "%v", err)
	}
	plan := Plan{ParentPath: req.ParentPath, SubmodulePath: req.SubmodulePath}

	if err := inspectParent(ctx, req, &plan); err != nil {
		return Plan{}, err
	}
	if err := inspectLinkedWorktree(ctx, req, &plan); err != nil {
		return Plan{}, err
	}
	if err := inspectCheckoutState(ctx, &plan); err != nil {
		return Plan{}, err
	}
	snapshot, err := configSnapshot(ctx, plan.CheckoutPath)
	if err != nil {
		return Plan{}, err
	}
	plan.configSnapshot = snapshot
	if err := inspectTarget(ctx, req, &plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func inspectParent(ctx context.Context, req Request, plan *Plan) error {
	top, err := git(ctx, req.ParentPath, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if !top.ok() || !samePath(top.line(), req.ParentPath) {
		return refuse(RefusalParentNotCheckout, "parent_path %s is not the root of a Git checkout", req.ParentPath)
	}

	var headOID, indexOID string
	var headFound, indexFound bool
	tree, err := git(ctx, req.ParentPath, true, "ls-tree", "-z", "HEAD", "--", req.SubmodulePath)
	if err != nil {
		return err
	}
	if tree.ok() {
		for _, entry := range strings.Split(tree.stdout, "\x00") {
			meta, name, found := strings.Cut(entry, "\t")
			fields := strings.Fields(meta)
			if !found || name != req.SubmodulePath || len(fields) != 3 {
				continue
			}
			if fields[0] != "160000" {
				return refuse(RefusalNotGitlink, "%s is not a submodule gitlink in the parent's HEAD (mode %s)", req.SubmodulePath, fields[0])
			}
			headOID, headFound = fields[2], true
		}
	}
	staged, err := git(ctx, req.ParentPath, true, "ls-files", "--stage", "-z", "--", req.SubmodulePath)
	if err != nil {
		return err
	}
	if !staged.ok() {
		return refuse(RefusalParentNotCheckout, "cannot read the parent index: %s", staged.failure())
	}
	for _, entry := range strings.Split(staged.stdout, "\x00") {
		meta, name, found := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !found || name != req.SubmodulePath || len(fields) != 3 {
			continue
		}
		if fields[2] != "0" {
			return refuse(RefusalGitlinkUnmerged, "the gitlink for %s is unmerged in the parent; resolve the merge first", req.SubmodulePath)
		}
		if fields[0] != "160000" {
			return refuse(RefusalNotGitlink, "%s is not a submodule gitlink in the parent's index (mode %s)", req.SubmodulePath, fields[0])
		}
		indexOID, indexFound = fields[1], true
	}
	switch {
	case !headFound && !indexFound:
		return refuse(RefusalNotGitlink, "%s is not a submodule gitlink in the parent's HEAD or index", req.SubmodulePath)
	case !indexFound:
		return refuse(RefusalNotGitlink, "the gitlink for %s is staged for removal in the parent", req.SubmodulePath)
	case !headFound || indexOID != headOID:
		plan.PinnedCommit, plan.TargetSource = indexOID, TargetIndexGitlink
	default:
		plan.PinnedCommit, plan.TargetSource = headOID, TargetHeadGitlink
	}
	plan.TargetCommit = plan.PinnedCommit
	plan.TargetIsPinned = true
	if req.TargetCommit != "" && req.TargetCommit != plan.PinnedCommit {
		plan.TargetCommit, plan.TargetSource, plan.TargetIsPinned = req.TargetCommit, TargetExplicit, false
		plan.Warnings = append(plan.Warnings, "the target differs from the gitlink the parent pins; the parent will report this submodule as modified until its gitlink is updated")
	}

	checkout := filepath.Join(req.ParentPath, filepath.FromSlash(req.SubmodulePath))
	if !pathInside(req.ParentPath, checkout) {
		return refuse(RefusalInvalidRequest, "submodule_path resolves outside parent_path")
	}
	plan.CheckoutPath = checkout
	return nil
}

// inspectLinkedWorktree proves the checkout is a registered linked worktree of
// a shared submodule repository, with reciprocal pointers on both sides.
func inspectLinkedWorktree(ctx context.Context, req Request, plan *Plan) error {
	checkout := plan.CheckoutPath
	info, err := os.Lstat(checkout)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return refuse(RefusalNotInitialized, "submodule checkout %s does not exist", checkout)
		}
		return fmt.Errorf("stat %s: %w", checkout, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return refuse(RefusalNotInitialized, "submodule checkout %s is not a plain directory", checkout)
	}
	dotGit := filepath.Join(checkout, ".git")
	dotInfo, err := os.Lstat(dotGit)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return refuse(RefusalNotInitialized, "submodule %s is not initialized (no .git entry)", req.SubmodulePath)
		}
		return fmt.Errorf("stat %s: %w", dotGit, err)
	}
	if dotInfo.IsDir() {
		return refuse(RefusalNotLinkedWorktree, "%s has a .git directory, so it is a standalone clone and not a linked worktree; this control only aligns reused linked submodule worktrees", checkout)
	}
	if !dotInfo.Mode().IsRegular() || dotInfo.Size() > 4096 {
		return refuse(RefusalNotLinkedWorktree, "%s is not a gitdir pointer file", dotGit)
	}
	pointer, err := readPointer(dotGit, "gitdir:", checkout)
	if err != nil {
		return refuse(RefusalPointersMismatch, "%v", err)
	}
	admin := filepath.Clean(pointer)
	if filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return refuse(RefusalNotLinkedWorktree, "%s points at %s, which is not under <repository>/worktrees/; the canonical submodule checkout is never aligned by this control", dotGit, admin)
	}
	if adminInfo, err := os.Stat(admin); err != nil || !adminInfo.IsDir() {
		return refuse(RefusalPointersMismatch, "the worktree administrative directory %s named by %s does not exist", admin, dotGit)
	}
	common := filepath.Dir(filepath.Dir(admin))
	plan.AdminDir, plan.CommonDir = admin, common

	commondir, err := readPointer(filepath.Join(admin, "commondir"), "", admin)
	if err != nil || !samePath(commondir, common) {
		return refuse(RefusalPointersMismatch, "%s/commondir does not point back at %s", admin, common)
	}
	back, err := readPointer(filepath.Join(admin, "gitdir"), "", admin)
	if err != nil || !samePath(back, dotGit) {
		return refuse(RefusalPointersMismatch, "%s/gitdir does not point back at %s (found %q)", admin, dotGit, back)
	}
	registered, err := countRegistrations(common, dotGit)
	if err != nil {
		return err
	}
	if registered != 1 {
		return refuse(RefusalPointersMismatch, "%s is registered by %d worktree records under %s/worktrees; expected exactly one", dotGit, registered, common)
	}

	resolved, err := git(ctx, checkout, true, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(resolved.stdout), "\n")
	if !resolved.ok() || len(lines) != 3 {
		return refuse(RefusalWrongToplevel, "git cannot resolve %s as a work tree: %s", checkout, resolved.failure())
	}
	if !samePath(lines[0], checkout) {
		return refuse(RefusalWrongToplevel, "git resolves the top level of %s as %s; read the submodule-worktrees knowledge topic before repairing this", checkout, lines[0])
	}
	if !samePath(lines[1], admin) {
		return refuse(RefusalPointersMismatch, "git resolves the git directory of %s as %s, not %s", checkout, lines[1], admin)
	}
	gitCommon := lines[2]
	if !filepath.IsAbs(gitCommon) {
		gitCommon = filepath.Join(checkout, gitCommon)
	}
	if !samePath(gitCommon, common) {
		return refuse(RefusalPointersMismatch, "git resolves the common directory of %s as %s, not %s", checkout, gitCommon, common)
	}
	parentCommon, err := git(ctx, req.ParentPath, true, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if parentCommon.ok() {
		value := parentCommon.line()
		if !filepath.IsAbs(value) {
			value = filepath.Join(req.ParentPath, value)
		}
		if samePath(value, common) {
			return refuse(RefusalNotLinkedWorktree, "%s shares the parent's own repository, not a submodule repository", checkout)
		}
	}
	return nil
}

// readPointer returns the path in a one-line pointer file. A relative path is
// resolved against base. prefix, when set, must lead the line.
func readPointer(file, prefix, base string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", file, err)
	}
	if info.Size() > 4096 {
		return "", fmt.Errorf("%s is unexpectedly large", file)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", file, err)
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	if prefix != "" {
		if !strings.HasPrefix(strings.ToLower(line), prefix) {
			return "", fmt.Errorf("%s does not contain a %s pointer", file, strings.TrimSuffix(prefix, ":"))
		}
		line = strings.TrimSpace(line[len(prefix):])
	}
	if line == "" {
		return "", fmt.Errorf("%s contains an empty pointer", file)
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(base, line)
	}
	return filepath.Clean(line), nil
}

func countRegistrations(common, dotGit string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return 0, fmt.Errorf("read %s/worktrees: %w", common, err)
	}
	count := 0
	for _, entry := range entries {
		admin := filepath.Join(common, "worktrees", entry.Name())
		back, err := readPointer(filepath.Join(admin, "gitdir"), "", admin)
		if err == nil && samePath(back, dotGit) {
			count++
		}
	}
	return count, nil
}

// inspectCheckoutState refuses unless HEAD is detached, no operation is in
// progress, and the checkout has no staged, unstaged, or untracked changes.
func inspectCheckoutState(ctx context.Context, plan *Plan) error {
	checkout := plan.CheckoutPath
	if branch, err := git(ctx, checkout, true, "symbolic-ref", "--quiet", "HEAD"); err != nil {
		return err
	} else if branch.ok() {
		return refuse(RefusalBranchCheckedOut, "%s has branch %s checked out; only detached reused worktrees are aligned", checkout, strings.TrimPrefix(branch.line(), "refs/heads/"))
	}
	for _, marker := range operationMarkers {
		if _, err := os.Lstat(filepath.Join(plan.AdminDir, marker)); err == nil {
			return refuse(RefusalOperationInProgress, "%s has an operation in progress (%s); finish or abort it first", checkout, marker)
		}
	}
	if err := gitlock.CheckIndexLock(ctx, checkout); err != nil {
		var lockErr gitlock.IndexLockError
		if errors.As(err, &lockErr) {
			return refuse(RefusalIndexLocked, "%v", err)
		}
		return err
	}
	head, err := gitOK(ctx, checkout, true, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	plan.CurrentHead = head

	status, err := git(ctx, checkout, true, "status", "--porcelain=v2", "--untracked-files=all")
	if err != nil {
		return err
	}
	if !status.ok() {
		return refuse(RefusalDirty, "cannot read the status of %s: %s", checkout, status.failure())
	}
	if reason := describeDirty(status.stdout); reason != "" {
		return refuse(RefusalDirty, "submodule checkout %s is not clean: %s", checkout, reason)
	}
	return nil
}

// describeDirty summarizes `status --porcelain=v2` output; empty means clean.
func describeDirty(porcelain string) string {
	var staged, unstaged, untracked, unmerged int
	var paths []string
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 3 {
			continue
		}
		var path string
		switch line[0] {
		case '1':
			if fields := strings.SplitN(line, " ", 9); len(fields) == 9 {
				path = fields[8]
			}
			if line[2] != '.' {
				staged++
			}
			if line[3] != '.' {
				unstaged++
			}
		case '2':
			if fields := strings.SplitN(line, " ", 10); len(fields) == 10 {
				path, _, _ = strings.Cut(fields[9], "\t")
			}
			if line[2] != '.' {
				staged++
			}
			if line[3] != '.' {
				unstaged++
			}
		case 'u':
			if fields := strings.SplitN(line, " ", 11); len(fields) == 11 {
				path = fields[10]
			}
			unmerged++
		case '?':
			path = line[2:]
			untracked++
		default:
			continue
		}
		if len(paths) < maxListedPaths && path != "" {
			paths = append(paths, path)
		}
	}
	if staged+unstaged+untracked+unmerged == 0 {
		return ""
	}
	var parts []string
	for _, p := range []struct {
		n    int
		name string
	}{{staged, "staged"}, {unstaged, "unstaged"}, {untracked, "untracked"}, {unmerged, "unmerged"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.name))
		}
	}
	return fmt.Sprintf("%s (e.g. %s)", strings.Join(parts, ", "), strings.Join(paths, ", "))
}

// configSnapshot records the origins and values of the two settings this
// control must never change. It is read-only.
func configSnapshot(ctx context.Context, checkout string) (string, error) {
	var parts []string
	for _, key := range []string{"core.worktree", "extensions.worktreeConfig"} {
		result, err := git(ctx, checkout, true, "config", "--show-origin", "--show-scope", "--get-all", key)
		if err != nil {
			return "", err
		}
		if result.code > 1 {
			return "", fmt.Errorf("read %s in %s: %s", key, checkout, result.failure())
		}
		parts = append(parts, key+"\n"+strings.TrimSpace(result.stdout))
	}
	return strings.Join(parts, "\n--\n"), nil
}

func inspectTarget(ctx context.Context, req Request, plan *Plan) error {
	checkout := plan.CheckoutPath
	exists, err := git(ctx, checkout, true, "cat-file", "-e", plan.TargetCommit+"^{commit}")
	if err != nil {
		return err
	}
	plan.TargetPresent = exists.ok()
	if !plan.TargetPresent {
		if !req.FetchIfMissing {
			return refuse(RefusalTargetMissing, "commit %s is not in the shared object store %s; propose again with fetch_if_missing=true, or fetch it into the submodule first", short(plan.TargetCommit), plan.CommonDir)
		}
		plan.NeedsFetch = true
		plan.Ancestry = AncestryUnknown
		plan.Warnings = append(plan.Warnings, "the target commit is missing and will be fetched from the repository's remotes before the checkout")
		return nil
	}

	if plan.CurrentHead == plan.TargetCommit {
		plan.Ancestry = AncestryIdentical
	} else {
		forward, err := isAncestor(ctx, checkout, plan.CurrentHead, plan.TargetCommit)
		if err != nil {
			return err
		}
		backward := false
		if !forward {
			if backward, err = isAncestor(ctx, checkout, plan.TargetCommit, plan.CurrentHead); err != nil {
				return err
			}
		}
		switch {
		case forward:
			plan.Ancestry = AncestryFastForward
		case backward:
			plan.Ancestry = AncestryBackward
		default:
			plan.Ancestry = AncestryDiverged
		}
		counts, err := gitOK(ctx, checkout, true, "rev-list", "--left-right", "--count", plan.CurrentHead+"..."+plan.TargetCommit)
		if err != nil {
			return err
		}
		if fields := strings.Fields(counts); len(fields) == 2 {
			plan.TargetBehind, _ = strconv.Atoi(fields[0])
			plan.TargetAhead, _ = strconv.Atoi(fields[1])
		}
		if err := checkNestedGitlinks(ctx, plan); err != nil {
			return err
		}
	}

	plan.HeadOnRefs = true
	switch plan.Ancestry {
	case AncestryBackward, AncestryDiverged:
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("the move is %s", strings.ReplaceAll(string(plan.Ancestry), "_", "-")))
		onRefs, err := gitOK(ctx, checkout, true, "for-each-ref", "--contains", plan.CurrentHead, "--count=1", "--format=%(refname)")
		if err != nil {
			return err
		}
		if onRefs == "" {
			plan.HeadOnRefs = false
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("current HEAD %s is reachable from no ref; after the move it is recoverable only through the reflog", short(plan.CurrentHead)))
		}
	}
	plan.StandingEligible = plan.TargetIsPinned && !plan.NeedsFetch &&
		(plan.Ancestry == AncestryFastForward || plan.Ancestry == AncestryIdentical)
	return nil
}

func isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	result, err := git(ctx, dir, true, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	switch result.code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("merge-base --is-ancestor failed in %s: %s", dir, result.failure())
	}
}

// checkNestedGitlinks refuses when the move would change a nested submodule's
// pinned commit. A plain checkout leaves nested submodules where they are, so
// the submodule would be left reporting drift.
func checkNestedGitlinks(ctx context.Context, plan *Plan) error {
	diff, err := git(ctx, plan.CheckoutPath, true, "diff-tree", "-r", "--no-renames", "--raw", plan.CurrentHead, plan.TargetCommit)
	if err != nil {
		return err
	}
	if !diff.ok() {
		return fmt.Errorf("compare %s and %s: %s", short(plan.CurrentHead), short(plan.TargetCommit), diff.failure())
	}
	var nested []string
	for _, line := range strings.Split(diff.stdout, "\n") {
		meta, name, found := strings.Cut(line, "\t")
		fields := strings.Fields(strings.TrimPrefix(meta, ":"))
		if !found || len(fields) < 2 || !strings.HasPrefix(meta, ":") {
			continue
		}
		if fields[0] == "160000" || fields[1] == "160000" {
			nested = append(nested, name)
		}
	}
	if len(nested) == 0 {
		return nil
	}
	if len(nested) > maxListedPaths {
		nested = append(nested[:maxListedPaths], "...")
	}
	return refuse(RefusalNestedGitlinks, "moving %s from %s to %s changes nested submodule pins (%s); a plain checkout would leave them behind, so align those separately",
		plan.SubmodulePath, short(plan.CurrentHead), short(plan.TargetCommit), strings.Join(nested, ", "))
}
