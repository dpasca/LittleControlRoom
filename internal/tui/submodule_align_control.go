package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/submodulealign"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	submoduleAlignPendingSummary = "Aligning submodule worktree..."
	// tuiSubmoduleAlignTimeout leaves room for a fetch from a slow remote.
	tuiSubmoduleAlignTimeout = 10 * time.Minute
	// submoduleAlignInspectTimeout bounds the read-only inspection shown in the
	// confirmation dialog.
	submoduleAlignInspectTimeout = 30 * time.Second
)

// externalControlPreflight is the live Git inspection behind a submodule
// alignment proposal. It is computed off the UI thread when the proposal loads,
// because both the operator-visible ancestry and the eligibility of a saved
// permission depend on repository state that arguments alone cannot express.
type externalControlPreflight struct {
	// applies is false for every capability that has no live-state preflight.
	applies bool
	// preview is the plain-text plan shown in the dialog.
	preview string
	// refusal is set when a precondition fails now; no dialog is shown.
	refusal error
	// standingEligible is true only for a clean fast-forward to the pinned
	// gitlink that needs no fetch.
	standingEligible bool
	// standingNote explains why a saved permission cannot be used or offered.
	standingNote string
}

// allowsStandingPermission reports whether saved permissions may be consulted.
func (p externalControlPreflight) allowsStandingPermission() bool {
	return !p.applies || (p.refusal == nil && p.standingEligible)
}

func inspectSubmoduleAlignProposal(ctx context.Context, operation control.Operation) externalControlPreflight {
	if operation.Capability != control.CapabilityGitSubmoduleAlign {
		return externalControlPreflight{}
	}
	out := externalControlPreflight{applies: true}
	invocation, err := control.ValidateInvocation(operation.Invocation)
	if err != nil {
		out.refusal = err
		return out
	}
	var input control.GitSubmoduleAlignInput
	if err := json.Unmarshal(invocation.Args, &input); err != nil {
		out.refusal = err
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, submoduleAlignInspectTimeout)
	defer cancel()
	plan, err := submodulealign.Inspect(ctx, input.Request())
	if err != nil {
		out.refusal = err
		return out
	}
	out.preview = plan.Preview()
	out.standingEligible = plan.StandingEligible
	if !plan.StandingEligible {
		out.standingNote = "only a clean fast-forward to the pinned gitlink that needs no fetch qualifies, and here " + plan.IneligibleReason()
	}
	return out
}

func (m Model) executeGitSubmoduleAlignControl(inv control.Invocation, input control.GitSubmoduleAlignInput) controlInvocationOutcome {
	if m.pendingGitSummary(input.ParentPath) != "" {
		err := fmt.Errorf("a git action is already running for %s", input.ParentPath)
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	m.setPendingGitSummary(input.ParentPath, submoduleAlignPendingSummary)
	m.status = submoduleAlignPendingSummary
	return controlInvocationOutcome{model: m, cmd: m.alignSubmoduleControlCmd(inv, input)}
}

type submoduleAlignActionMsg struct {
	parentPath string
	result     *control.GitSubmoduleAlignResult
	status     string
	err        error
}

func (m Model) alignSubmoduleControlCmd(inv control.Invocation, input control.GitSubmoduleAlignInput) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiSubmoduleAlignTimeout)
		defer cancel()
		msg := submoduleAlignActionMsg{parentPath: input.ParentPath, status: "Submodule alignment failed"}
		opts := submodulealign.Options{}
		// A saved permission authorizes only what it could have authorized when
		// it was offered, so anything it covered is held to that standard again.
		if control.IsExternalOperationID(inv.RequestID) {
			if svc == nil || svc.Store() == nil {
				msg.err = errors.New("service store unavailable; cannot tell how this alignment was authorized")
				return msg
			}
			operation, err := svc.Store().GetControlOperation(ctx, inv.RequestID)
			if err != nil {
				msg.err = fmt.Errorf("cannot tell how this alignment was authorized: %w", err)
				return msg
			}
			opts.RequireStandingEligibility = operation.ConfirmationBy == control.ConfirmationScopedPermission
		}
		result, err := submodulealign.Align(ctx, input.Request(), opts)
		if result.CheckoutPath != "" {
			converted := control.NewGitSubmoduleAlignResult(result)
			msg.result = &converted
		}
		var refusal *submodulealign.Refusal
		switch {
		case err == nil:
			msg.status = submoduleAlignStatus(result)
		case errors.As(err, &refusal):
			msg.err = fmt.Errorf("refused (%s): %s", refusal.Code, refusal.Reason)
			msg.status = "Submodule alignment refused: " + refusal.Reason
		default:
			msg.err = timeoutActionError(err, tuiSubmoduleAlignTimeout, "aligning the submodule worktree")
		}
		return msg
	}
}

func submoduleAlignStatus(result submodulealign.Result) string {
	name := result.SubmodulePath
	if !result.Changed {
		return fmt.Sprintf("Submodule %s was already at %s", name, shortCommit(result.Head))
	}
	status := fmt.Sprintf("Aligned submodule %s from %s to %s (%s)", name, shortCommit(result.PreviousHead), shortCommit(result.Head), strings.ReplaceAll(string(result.Ancestry), "_", "-"))
	if result.Fetched {
		status += " after fetching"
	}
	return status
}

func shortCommit(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

func (m Model) applySubmoduleAlignAction(msg submoduleAlignActionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.clearPendingGitSummary(msg.parentPath)
		m.reportError("Submodule alignment failed", msg.err, msg.parentPath)
		if msg.status != "" {
			m.status = msg.status
		}
	} else {
		m.expirePendingGitSummaryOnRefresh(msg.parentPath)
		m.err = nil
		m.status = msg.status
	}
	// The submodule's HEAD moved or was found stale, so the parent's repository
	// status must be recomputed either way.
	return m, m.requestProjectInvalidationCmd(invalidateProjectScan(msg.parentPath, false))
}
