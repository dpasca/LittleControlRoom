package submodulealign

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/gitlock"
)

// Options narrows how Align runs.
type Options struct {
	// RequireStandingEligibility makes Align refuse unless the plan is a clean
	// fast-forward (or no-op) to the pinned gitlink that needs no fetch. Callers
	// set it when the operation was authorized by a scoped standing permission
	// rather than by an operator reviewing this exact plan.
	RequireStandingEligibility bool
}

// Align moves the linked submodule worktree to the target commit with
// `git checkout --detach`. It never runs `git submodule update` or `sync`, never
// writes Git configuration, and only changes this worktree's HEAD, index, and
// files. Fetching, when needed and allowed, adds objects and remote-tracking
// refs to the shared repository but changes no checkout.
func Align(ctx context.Context, req Request, opts Options) (Result, error) {
	plan, err := Inspect(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if opts.RequireStandingEligibility && !plan.StandingEligible {
		return Result{}, refuse(RefusalNotStandingEligible, "this alignment no longer qualifies for a standing permission (%s); propose it again for explicit confirmation", plan.IneligibleReason())
	}
	result := Result{
		ParentPath: plan.ParentPath, SubmodulePath: plan.SubmodulePath, CheckoutPath: plan.CheckoutPath,
		PreviousHead: plan.CurrentHead, TargetCommit: plan.TargetCommit, TargetSource: plan.TargetSource,
	}

	if plan.NeedsFetch {
		if err := fetchTarget(ctx, plan); err != nil {
			return result, err
		}
		result.Fetched = true
		// The operator confirmed without knowing the relationship. Anything but a
		// fast-forward must be proposed again so it can be reviewed.
		reviewedPin := plan.PinnedCommit
		if plan, err = Inspect(ctx, req); err != nil {
			return result, err
		}
		if plan.PinnedCommit != reviewedPin {
			return result, refuse(RefusalRequiresReview, "the parent's pinned gitlink changed from %s to %s while fetching; propose it again to review the new target",
				short(reviewedPin), short(plan.PinnedCommit))
		}
		if plan.Ancestry != AncestryFastForward && plan.Ancestry != AncestryIdentical {
			return result, refuse(RefusalRequiresReview, "after fetching, %s is %s relative to the current HEAD %s, and that was not visible when this was confirmed; propose it again to review the relationship",
				short(plan.TargetCommit), strings.ReplaceAll(string(plan.Ancestry), "_", "-"), short(plan.CurrentHead))
		}
		result.PreviousHead = plan.CurrentHead
	}
	result.Ancestry = plan.Ancestry

	if plan.CurrentHead != plan.TargetCommit {
		if err := gitlock.CheckIndexLock(ctx, plan.CheckoutPath); err != nil {
			return result, refuse(RefusalIndexLocked, "%v", err)
		}
		// --no-recurse-submodules keeps a user's submodule.recurse setting from
		// updating nested submodules; the explicit -- ends revision parsing.
		out, err := git(ctx, plan.CheckoutPath, false, "-c", "advice.detachedHead=false",
			"checkout", "--detach", "--no-recurse-submodules", plan.TargetCommit, "--")
		if err != nil {
			return result, err
		}
		if !out.ok() {
			return result, fmt.Errorf("git checkout --detach %s failed in %s: %s", short(plan.TargetCommit), plan.CheckoutPath, out.failure())
		}
		result.Changed = true
	}

	verification, err := verify(ctx, plan)
	result.Verification = verification
	result.Head = headOrEmpty(ctx, plan.CheckoutPath)
	if err != nil {
		return result, err
	}
	if problems := verification.problems(); len(problems) > 0 {
		return result, &VerificationError{Result: result, Problems: problems}
	}
	return result, nil
}

func headOrEmpty(ctx context.Context, checkout string) string {
	head, err := gitOK(ctx, checkout, true, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return head
}

// fetchTarget retrieves a missing commit from the repository's remotes. Remotes
// are the only source: the canonical checkout shares this object store, so it
// cannot hold anything this worktree lacks. Automatic maintenance is disabled
// per invocation (-c is not persisted) so a fetch never repacks shared objects.
func fetchTarget(ctx context.Context, plan Plan) error {
	remotes, err := gitOK(ctx, plan.CheckoutPath, true, "remote")
	if err != nil {
		return err
	}
	names := strings.Fields(remotes)
	if len(names) == 0 {
		return refuse(RefusalNoRemote, "commit %s is missing and %s has no remote to fetch it from", short(plan.TargetCommit), plan.CheckoutPath)
	}
	// Prefer origin, then the rest in Git's order.
	ordered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "-") {
			continue // never let a remote name parse as an option
		}
		if name == "origin" {
			ordered = append([]string{name}, ordered...)
		} else {
			ordered = append(ordered, name)
		}
	}
	base := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--no-recurse-submodules", "--quiet"}
	var lastFailure string
	for _, remote := range ordered {
		result, err := git(ctx, plan.CheckoutPath, false, append(append([]string{}, base...), "--no-tags", "--", remote, plan.TargetCommit)...)
		if err != nil {
			return err
		}
		if result.ok() && commitPresent(ctx, plan) {
			return nil
		}
		lastFailure = result.failure()
	}
	// Some servers refuse a fetch by object id; fall back to every remote's refs.
	result, err := git(ctx, plan.CheckoutPath, false, append(append([]string{}, base...), "--all", "--tags")...)
	if err != nil {
		return err
	}
	if !result.ok() {
		lastFailure = result.failure()
	}
	if !commitPresent(ctx, plan) {
		return refuse(RefusalFetchFailed, "commit %s is still missing after fetching from %s%s", short(plan.TargetCommit), strings.Join(ordered, ", "), failureSuffix(lastFailure))
	}
	return nil
}

func failureSuffix(text string) string {
	if text == "" {
		return ""
	}
	return ": " + text
}

func commitPresent(ctx context.Context, plan Plan) bool {
	result, err := git(ctx, plan.CheckoutPath, true, "cat-file", "-e", plan.TargetCommit+"^{commit}")
	return err == nil && result.ok()
}

// verify checks the post-conditions the control promises.
func verify(ctx context.Context, plan Plan) (Verification, error) {
	var v Verification
	head, err := gitOK(ctx, plan.CheckoutPath, true, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return v, err
	}
	v.HeadMatchesTarget = head == plan.TargetCommit
	branch, err := git(ctx, plan.CheckoutPath, true, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return v, err
	}
	v.Detached = !branch.ok()

	after, err := configSnapshot(ctx, plan.CheckoutPath)
	if err != nil {
		return v, err
	}
	v.ConfigUnchanged = after == plan.configSnapshot

	status, err := git(ctx, plan.ParentPath, true, "status", "--porcelain=v2", "--untracked-files=no", "--", plan.SubmodulePath)
	if err != nil {
		return v, err
	}
	if !status.ok() {
		return v, fmt.Errorf("read parent status in %s: %s", plan.ParentPath, status.failure())
	}
	v.ParentGitlinkDrift = classifyDrift(status.stdout, plan.TargetIsPinned)
	return v, nil
}

// classifyDrift inspects the parent's status for the submodule path. Staged
// differences (column X) are a gitlink the operator already pinned; only a
// worktree-versus-index difference (column Y or the submodule field) is drift.
func classifyDrift(porcelain string, targetIsPinned bool) string {
	var drift []string
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 || (line[0] != '1' && line[0] != '2' && line[0] != 'u') {
			continue
		}
		fields := strings.Fields(line)
		if line[0] == 'u' || len(fields) < 3 {
			drift = append(drift, "unmerged entry")
			continue
		}
		if line[3] == '.' && fields[2] == "S..." {
			continue
		}
		drift = append(drift, fmt.Sprintf("worktree %c, submodule %s", line[3], fields[2]))
	}
	switch {
	case len(drift) == 0:
		return "none"
	case !targetIsPinned && len(drift) == 1 && strings.HasSuffix(drift[0], "submodule SC.."):
		return "expected_commit_change"
	default:
		return strings.Join(drift, "; ")
	}
}
