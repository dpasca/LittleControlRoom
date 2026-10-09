package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/submodulealign"
)

const (
	FeatureAlignSubmoduleWorktree = "align_submodule_worktree"

	HostEffectMayMoveSubmoduleWorktreeHead = "may_move_submodule_worktree_head"
	HostEffectMayFetchSubmoduleObjects     = "may_fetch_submodule_objects"
)

// GitSubmoduleAlignInput names one reused linked submodule worktree. The
// parent's pinned gitlink is the default target.
type GitSubmoduleAlignInput struct {
	RequestID      string `json:"request_id,omitempty"`
	ParentPath     string `json:"parent_path"`
	SubmodulePath  string `json:"submodule_path"`
	TargetCommit   string `json:"target_commit,omitempty"`
	FetchIfMissing bool   `json:"fetch_if_missing,omitempty"`
}

// Request converts validated input to the Git-level request.
func (i GitSubmoduleAlignInput) Request() submodulealign.Request {
	return submodulealign.Request{
		ParentPath: i.ParentPath, SubmodulePath: i.SubmodulePath,
		TargetCommit: i.TargetCommit, FetchIfMissing: i.FetchIfMissing,
	}
}

// GitSubmoduleAlignResult is the recorded outcome of a confirmed alignment.
type GitSubmoduleAlignResult struct {
	ParentPath    string `json:"parent_path"`
	SubmodulePath string `json:"submodule_path"`
	CheckoutPath  string `json:"checkout_path"`
	PreviousHead  string `json:"previous_head"`
	Head          string `json:"head"`
	TargetCommit  string `json:"target_commit"`
	TargetSource  string `json:"target_source"`
	Ancestry      string `json:"ancestry"`
	Changed       bool   `json:"changed"`
	Fetched       bool   `json:"fetched"`
	Verification  struct {
		HeadMatchesTarget  bool   `json:"head_matches_target"`
		Detached           bool   `json:"detached"`
		ParentGitlinkDrift string `json:"parent_gitlink_drift"`
		ConfigUnchanged    bool   `json:"config_unchanged"`
	} `json:"verification"`
}

// NewGitSubmoduleAlignResult copies a Git-level result into its recorded form.
func NewGitSubmoduleAlignResult(r submodulealign.Result) GitSubmoduleAlignResult {
	out := GitSubmoduleAlignResult{
		ParentPath: r.ParentPath, SubmodulePath: r.SubmodulePath, CheckoutPath: r.CheckoutPath,
		PreviousHead: r.PreviousHead, Head: r.Head, TargetCommit: r.TargetCommit,
		TargetSource: string(r.TargetSource), Ancestry: string(r.Ancestry),
		Changed: r.Changed, Fetched: r.Fetched,
	}
	out.Verification.HeadMatchesTarget = r.Verification.HeadMatchesTarget
	out.Verification.Detached = r.Verification.Detached
	out.Verification.ParentGitlinkDrift = r.Verification.ParentGitlinkDrift
	out.Verification.ConfigUnchanged = r.Verification.ConfigUnchanged
	return out
}

func GitSubmoduleAlignCapability() Capability {
	return Capability{
		Name:        CapabilityGitSubmoduleAlign,
		Description: "Preferred way to bring a reused linked submodule worktree up to the parent checkout's pinned gitlink after a merge or pull: runs `git -C <submodule> checkout --detach <gitlink>` for that one worktree. The target defaults to the gitlink pinned in the parent's HEAD, or its index when staged. Refuses with a precise reason unless the submodule worktree is clean (staged, unstaged, untracked), detached, a registered linked worktree with reciprocal gitdir pointers, not mid-operation, and has the target commit (or fetch_if_missing allows fetching it from the submodule's remotes into the shared object store). The proposal reports ancestry (fast-forward, backward, or diverged) so the operator can see it. Never runs git submodule update/sync, never writes Git configuration, and never touches the canonical checkout or sibling worktrees; afterwards it verifies HEAD equals the target, the parent shows no gitlink drift, and the core.worktree and extensions.worktreeConfig origins are unchanged. Requires operator confirmation; the operator may save a scoped standing permission that covers only clean fast-forward alignments to the pinned gitlink that need no fetch. A project-scoped caller can align only its own checkout. Read-only diagnosis and pinning a new gitlink in the parent's own worktree need no control.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"request_id": map[string]any{"type": "string"},
				"parent_path": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Absolute root of the parent checkout whose gitlink pins the submodule. A project-scoped caller must pass its own project path.",
				},
				"submodule_path": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Submodule path relative to parent_path, slash-separated, for example asset-source.",
				},
				"target_commit": map[string]any{
					"type": "string", "pattern": "^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$",
					"description": "Optional full object id. Omit to use the parent's pinned gitlink. A commit that differs from the pin leaves the parent reporting the submodule as modified and never qualifies for a standing permission.",
				},
				"fetch_if_missing": map[string]any{
					"type":        "boolean",
					"description": "Allow fetching the target from the submodule's remotes when it is not in the shared object store. Ancestry is unknown until then, and a result that is not a fast-forward is refused for review.",
				},
			},
			"required": []string{"parent_path", "submodule_path"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"status": map[string]any{"type": "string"},
				"submodule_align": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"parent_path":    map[string]any{"type": "string"},
						"submodule_path": map[string]any{"type": "string"},
						"checkout_path":  map[string]any{"type": "string"},
						"previous_head":  map[string]any{"type": "string"},
						"head":           map[string]any{"type": "string"},
						"target_commit":  map[string]any{"type": "string"},
						"target_source":  map[string]any{"type": "string", "enum": []string{"head_gitlink", "index_gitlink", "explicit"}},
						"ancestry":       map[string]any{"type": "string", "enum": []string{"identical", "fast_forward", "backward", "diverged"}},
						"changed":        map[string]any{"type": "boolean"},
						"fetched":        map[string]any{"type": "boolean"},
						"verification": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"head_matches_target":  map[string]any{"type": "boolean"},
								"detached":             map[string]any{"type": "boolean"},
								"parent_gitlink_drift": map[string]any{"type": "string"},
								"config_unchanged":     map[string]any{"type": "boolean"},
							},
							"required": []string{"head_matches_target", "detached", "parent_gitlink_drift", "config_unchanged"},
						},
					},
					"required": []string{"parent_path", "submodule_path", "checkout_path", "previous_head", "head", "target_commit", "target_source", "ancestry", "changed", "fetched", "verification"},
				},
			},
			"required": []string{"status"},
		},
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayMoveSubmoduleWorktreeHead, HostEffectMayFetchSubmoduleObjects},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureAlignSubmoduleWorktree},
		}},
	}
}

func NormalizeGitSubmoduleAlignInput(input GitSubmoduleAlignInput) (GitSubmoduleAlignInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	request, err := submodulealign.NormalizeRequest(input.Request())
	if err != nil {
		return GitSubmoduleAlignInput{}, err
	}
	input.ParentPath, input.SubmodulePath, input.TargetCommit = request.ParentPath, request.SubmodulePath, request.TargetCommit
	return input, nil
}

func validateGitSubmoduleAlignInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityGitSubmoduleAlign)
	}
	var input GitSubmoduleAlignInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityGitSubmoduleAlign, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityGitSubmoduleAlign)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeGitSubmoduleAlignInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityGitSubmoduleAlign, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}
