package control

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type WorktreeRemoveInput struct {
	RequestID    string `json:"request_id,omitempty"`
	WorktreePath string `json:"worktree_path"`
}

type WorktreeRemoveResult struct {
	WorktreePath      string `json:"worktree_path"`
	RootPath          string `json:"root_path"`
	WorktreeRemoved   bool   `json:"worktree_removed"`
	IdleSessionClosed bool   `json:"idle_session_closed"`
}

func WorktreeRemoveCapability() Capability {
	return Capability{
		Name:         CapabilityWorktreeRemove,
		Domain:       CapabilityDomainWorktree,
		Scope:        AuthorityScopePortfolio,
		Description:  "Permanently delete one tracked linked worktree and all its contents after operator confirmation. Removes its Git registrations, clears LCR worktree/TODO work state, closes an idle embedded session, and refreshes the TUI immediately on completion. Preserves branches, conversation history, and TODO completion state. Active engineers and runtimes are not stopped and may recreate files. Use project queries to discover the exact path first.",
		Risk:         RiskDestructive,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		Async:        true,
		HostEffects:  []string{"may_delete_worktree_directory", "may_remove_worktree_registrations", "may_clear_todo_work_state", "may_close_idle_engineer_session"},
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"request_id": map[string]any{"type": "string"},
				"worktree_path": map[string]any{
					"type":        "string",
					"minLength":   1,
					"description": "Exact absolute path of a tracked linked worktree, including a missing checkout whose LCR/Git records need cleanup. Primary checkouts cannot be removed.",
				},
			},
			"required": []string{"worktree_path"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"status": map[string]any{"type": "string"},
				"worktree": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"worktree_path":       map[string]any{"type": "string"},
						"root_path":           map[string]any{"type": "string"},
						"worktree_removed":    map[string]any{"type": "boolean", "description": "True only after directory, Git registration, and LCR cleanup succeed."},
						"idle_session_closed": map[string]any{"type": "boolean"},
					},
					"required": []string{"worktree_path", "root_path", "worktree_removed", "idle_session_closed"},
				},
			},
			"required": []string{"status"},
		},
	}
}

func validateWorktreeRemoveInvocation(inv Invocation) (Invocation, error) {
	var input WorktreeRemoveInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityWorktreeRemove, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityWorktreeRemove)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	input.WorktreePath = filepath.Clean(strings.TrimSpace(input.WorktreePath))
	if !filepath.IsAbs(input.WorktreePath) || input.WorktreePath == string(filepath.Separator) {
		return Invocation{}, fmt.Errorf("worktree_path must be an absolute linked worktree path")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Invocation{}, err
	}
	inv.RequestID = input.RequestID
	inv.Args = payload
	return inv, nil
}
