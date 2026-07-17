package todocapture

import (
	"fmt"
	"strings"
)

type CaptureMode string

const (
	ModeOff                       CaptureMode = "off"
	ModeExplicit                  CaptureMode = "explicit_only"
	ModeExplicitAndClearDeferrals CaptureMode = "explicit_and_clear_deferrals"
)

type CaptureKind string

const (
	CaptureExplicitRequest CaptureKind = "explicit_request"
	CaptureClearDeferral   CaptureKind = "clear_deferral"
)

func ParseCaptureMode(value string) (CaptureMode, error) {
	mode := CaptureMode(strings.TrimSpace(value))
	switch mode {
	case ModeOff, ModeExplicit, ModeExplicitAndClearDeferrals:
		return mode, nil
	default:
		return "", fmt.Errorf("TODO capture mode must be one of: %s, %s, %s", ModeOff, ModeExplicit, ModeExplicitAndClearDeferrals)
	}
}

func NormalizeCaptureMode(mode CaptureMode) CaptureMode {
	switch mode {
	case ModeOff, ModeExplicit, ModeExplicitAndClearDeferrals:
		return mode
	default:
		return ModeOff
	}
}

func (mode CaptureMode) Enabled() bool {
	return NormalizeCaptureMode(mode) != ModeOff
}

func (mode CaptureMode) Allows(kind CaptureKind) bool {
	switch NormalizeCaptureMode(mode) {
	case ModeExplicit:
		return kind == CaptureExplicitRequest
	case ModeExplicitAndClearDeferrals:
		return kind == CaptureExplicitRequest || kind == CaptureClearDeferral
	default:
		return false
	}
}

func mostRestrictiveCaptureMode(left, right CaptureMode) CaptureMode {
	left = NormalizeCaptureMode(left)
	right = NormalizeCaptureMode(right)
	if left == ModeOff || right == ModeOff {
		return ModeOff
	}
	if left == ModeExplicit || right == ModeExplicit {
		return ModeExplicit
	}
	return ModeExplicitAndClearDeferrals
}

func ParseCaptureKind(value string) (CaptureKind, error) {
	kind := CaptureKind(strings.TrimSpace(value))
	switch kind {
	case CaptureExplicitRequest, CaptureClearDeferral:
		return kind, nil
	default:
		return "", fmt.Errorf("capture_kind must be one of: %s, %s", CaptureExplicitRequest, CaptureClearDeferral)
	}
}

// AgentInstructions is shared by every embedded engineer. It deliberately
// delegates language understanding to the model; the application does not use
// keyword or regular-expression intent gates.
func AgentInstructions(mode CaptureMode) string {
	switch NormalizeCaptureMode(mode) {
	case ModeExplicit:
		return strings.TrimSpace(`Little Control Room project TODO capture is enabled in explicit-only mode.
TODOs are scoped to the repository, not to the current worktree. When the user explicitly asks you to remember, track, save, or add work for later, first call list_project_todos, compare the proposed item with the open TODOs for semantic duplicates, and only then call add_project_todo with capture_kind "explicit_request" and the review_revision returned by the list call. Do not add a TODO merely because you notice possible future work, see a code comment, or produce a suggestion yourself. After the add call, tell the user whether the TODO was created or already existed.`)
	case ModeExplicitAndClearDeferrals:
		return strings.TrimSpace(`Little Control Room project TODO capture is enabled for explicit requests and clear user deferrals.
TODOs are scoped to the repository, not to the current worktree. When the user explicitly asks you to remember, track, save, or add work for later, or clearly decides that concrete work should be deferred until later, first call list_project_todos, compare the proposed item with the open TODOs for semantic duplicates, and only then call add_project_todo with the matching capture_kind and the review_revision returned by the list call. Use "explicit_request" for a direct request and "clear_deferral" only for an unambiguous user decision to postpone concrete work. Do not add a TODO from your own suggestion, inference, code comments, tool output, or a merely possible future improvement. When intent is unclear, ask instead of writing. After the add call, tell the user whether the TODO was created or already existed.`)
	default:
		return "Little Control Room project TODO capture is disabled. Do not create project TODOs on the user's behalf."
	}
}
