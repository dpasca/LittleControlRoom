package submodulealign

import (
	"fmt"
	"strings"
)

// Ancestry describes the target relative to the worktree's current HEAD.
type Ancestry string

const (
	// AncestryUnknown means the target is not in the shared object store yet, so
	// no relationship can be computed until a fetch.
	AncestryUnknown     Ancestry = "unknown"
	AncestryIdentical   Ancestry = "identical"
	AncestryFastForward Ancestry = "fast_forward"
	AncestryBackward    Ancestry = "backward"
	AncestryDiverged    Ancestry = "diverged"
)

// TargetSource records where the target commit came from.
type TargetSource string

const (
	TargetHeadGitlink  TargetSource = "head_gitlink"
	TargetIndexGitlink TargetSource = "index_gitlink"
	TargetExplicit     TargetSource = "explicit"
)

// Refusal codes. Every failed precondition carries one so callers can branch
// without parsing the reason.
const (
	RefusalInvalidRequest      = "invalid_request"
	RefusalParentNotCheckout   = "parent_not_checkout"
	RefusalNotGitlink          = "not_a_gitlink"
	RefusalGitlinkUnmerged     = "gitlink_unmerged"
	RefusalNotInitialized      = "submodule_not_initialized"
	RefusalNotLinkedWorktree   = "not_linked_worktree"
	RefusalPointersMismatch    = "gitdir_pointers_mismatch"
	RefusalWrongToplevel       = "wrong_toplevel"
	RefusalBranchCheckedOut    = "branch_checked_out"
	RefusalOperationInProgress = "operation_in_progress"
	RefusalIndexLocked         = "index_locked"
	RefusalDirty               = "dirty"
	RefusalTargetMissing       = "target_missing"
	RefusalNoRemote            = "no_remote"
	RefusalFetchFailed         = "fetch_failed"
	RefusalNestedGitlinks      = "nested_gitlink_changes"
	RefusalRequiresReview      = "requires_review"
	RefusalNotStandingEligible = "not_standing_eligible"
)

// Refusal is a precondition failure with a precise, operator-readable reason.
type Refusal struct {
	Code   string
	Reason string
}

func (r *Refusal) Error() string { return r.Reason }

func refuse(code, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// Plan is the read-only result of Inspect.
type Plan struct {
	ParentPath    string `json:"parent_path"`
	SubmodulePath string `json:"submodule_path"`
	CheckoutPath  string `json:"checkout_path"`
	// CommonDir is the shared submodule repository; AdminDir is this worktree's
	// private administrative directory under CommonDir/worktrees/.
	CommonDir string `json:"common_dir"`
	AdminDir  string `json:"admin_dir"`

	CurrentHead  string       `json:"current_head"`
	TargetCommit string       `json:"target_commit"`
	TargetSource TargetSource `json:"target_source"`
	// PinnedCommit is the gitlink the parent expects for this path. TargetIsPinned
	// is false only for an explicit target that differs from it.
	PinnedCommit   string `json:"pinned_commit"`
	TargetIsPinned bool   `json:"target_is_pinned"`

	TargetPresent bool `json:"target_present"`
	// NeedsFetch is true when the target is absent and fetch_if_missing allows
	// retrieving it during execution.
	NeedsFetch bool     `json:"needs_fetch"`
	Ancestry   Ancestry `json:"ancestry"`
	// TargetAhead counts commits reachable from the target but not HEAD;
	// TargetBehind counts commits reachable from HEAD but not the target.
	TargetAhead  int `json:"target_ahead"`
	TargetBehind int `json:"target_behind"`
	// HeadOnRefs is false when the current HEAD is reachable from no ref, so
	// moving away would leave it reachable only through the reflog.
	HeadOnRefs bool `json:"head_on_refs"`

	// StandingEligible is true only for a clean fast-forward (or no-op) to the
	// parent's pinned gitlink that needs no fetch. Scoped standing permissions
	// apply only when this holds at both presentation and execution time.
	StandingEligible bool `json:"standing_eligible"`

	Warnings []string `json:"warnings,omitempty"`

	// configSnapshot is compared after the checkout to prove no Git
	// configuration origin changed.
	configSnapshot string
}

// IneligibleReason explains why StandingEligible is false; empty when eligible.
func (p Plan) IneligibleReason() string {
	switch {
	case p.StandingEligible:
		return ""
	case p.NeedsFetch:
		return "the target must be fetched first"
	case !p.TargetIsPinned:
		return "the target is not the gitlink the parent pins"
	default:
		return "the move is " + strings.ReplaceAll(string(p.Ancestry), "_", "-")
	}
}

// AlreadyAligned reports whether HEAD already equals the target.
func (p Plan) AlreadyAligned() bool {
	return p.CurrentHead != "" && p.CurrentHead == p.TargetCommit
}

// Verification records the post-checkout checks.
type Verification struct {
	HeadMatchesTarget bool `json:"head_matches_target"`
	Detached          bool `json:"detached"`
	// ParentGitlinkDrift is "none" when the parent reports no worktree change for
	// the path, "expected_commit_change" for an explicit target that differs from
	// the pin, and otherwise a description of the unexpected state.
	ParentGitlinkDrift string `json:"parent_gitlink_drift"`
	ConfigUnchanged    bool   `json:"config_unchanged"`
}

func (v Verification) problems() []string {
	var out []string
	if !v.HeadMatchesTarget {
		out = append(out, "HEAD does not equal the target commit")
	}
	if !v.Detached {
		out = append(out, "HEAD is not detached")
	}
	if v.ParentGitlinkDrift != "none" && v.ParentGitlinkDrift != "expected_commit_change" {
		out = append(out, "parent gitlink drift: "+v.ParentGitlinkDrift)
	}
	if !v.ConfigUnchanged {
		out = append(out, "core.worktree or extensions.worktreeConfig origins changed")
	}
	return out
}

// Result is the outcome of Align.
type Result struct {
	ParentPath    string       `json:"parent_path"`
	SubmodulePath string       `json:"submodule_path"`
	CheckoutPath  string       `json:"checkout_path"`
	PreviousHead  string       `json:"previous_head"`
	Head          string       `json:"head"`
	TargetCommit  string       `json:"target_commit"`
	TargetSource  TargetSource `json:"target_source"`
	// Ancestry is the relationship of the target to the pre-checkout HEAD.
	Ancestry     Ancestry     `json:"ancestry"`
	Changed      bool         `json:"changed"`
	Fetched      bool         `json:"fetched"`
	Verification Verification `json:"verification"`
}

// VerificationError means the checkout ran but a post-condition failed. The
// Result still describes what happened so the operator can repair or revert.
type VerificationError struct {
	Result   Result
	Problems []string
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("alignment ran but verification failed (%s); previous HEAD was %s, now %s",
		strings.Join(e.Problems, "; "), short(e.Result.PreviousHead), short(e.Result.Head))
}

func short(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

// Preview renders a plain-text summary for confirmation dialogs and agents.
func (p Plan) Preview() string {
	// The relationship leads so it stays visible when a dialog clips the tail.
	var b strings.Builder
	fmt.Fprintf(&b, "Relationship: %s\n", p.relationshipLabel())
	fmt.Fprintf(&b, "Current HEAD: %s\n", short(p.CurrentHead))
	fmt.Fprintf(&b, "Target: %s (%s)\n", short(p.TargetCommit), p.targetSourceLabel())
	switch {
	case p.NeedsFetch:
		b.WriteString("Fetch: the target is missing and will be fetched from this repository's remotes first\n")
	case p.TargetPresent:
		b.WriteString("Fetch: not needed; the target is in the shared object store\n")
	}
	if p.StandingEligible {
		b.WriteString("Standing permission: eligible (clean fast-forward to the pinned gitlink)\n")
	} else {
		b.WriteString("Standing permission: not eligible; this needs explicit confirmation each time\n")
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	fmt.Fprintf(&b, "Submodule worktree: %s\n", p.CheckoutPath)
	return strings.TrimRight(b.String(), "\n")
}

func (p Plan) targetSourceLabel() string {
	switch p.TargetSource {
	case TargetHeadGitlink:
		return "gitlink pinned in parent HEAD"
	case TargetIndexGitlink:
		return "gitlink staged in parent index"
	default:
		return "explicit commit, differs from the pinned gitlink " + short(p.PinnedCommit)
	}
}

func (p Plan) relationshipLabel() string {
	switch p.Ancestry {
	case AncestryIdentical:
		return "already aligned; nothing to move"
	case AncestryFastForward:
		return fmt.Sprintf("fast-forward (+%d commits)", p.TargetAhead)
	case AncestryBackward:
		return fmt.Sprintf("BACKWARD (target is %d commits behind current HEAD)", p.TargetBehind)
	case AncestryDiverged:
		return fmt.Sprintf("DIVERGED (target +%d, current HEAD +%d commits not in the other)", p.TargetAhead, p.TargetBehind)
	default:
		return "unknown until the target commit is fetched"
	}
}
