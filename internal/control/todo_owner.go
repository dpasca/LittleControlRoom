package control

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type TodoRepairOwnerInput struct {
	RequestID         string `json:"request_id,omitempty"`
	TodoID            int64  `json:"todo_id"`
	ExpectedOwnerPath string `json:"expected_owner_path"`
	RepositoryPath    string `json:"repository_path"`
	ExpectedRevision  string `json:"expected_revision,omitempty"`
}

func TodoRepairOwnerCapability() Capability {
	return Capability{
		Name: CapabilityTodoRepairOwner, Domain: CapabilityDomainTodo, Scope: AuthorityScopeHost,
		Description: "Repair one incorrectly checkout-owned TODO to its repository. Retains all metadata, attachments, work links and provenance. Host-only, confirmed, same-repository operation; stale input refuses and exact retries are audited no-ops.",
		Risk:        RiskWrite, Confirmation: ConfirmationRequired, RequiresHost: true, Async: true,
		HostEffects: []string{"may_repair_single_todo_owner"},
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"request_id":          map[string]any{"type": "string"},
				"todo_id":             map[string]any{"type": "integer", "minimum": 1},
				"expected_owner_path": map[string]any{"type": "string", "description": "Exact current incorrect checkout owner; stale input refuses."},
				"repository_path":     map[string]any{"type": "string", "description": "Exact loaded primary repository owner, verified against live Git membership."},
				"expected_revision":   map[string]any{"type": "string", "description": "Host binds the reviewed TODO, attachments, launch journal and plan snapshot when omitted. Changes before execution refuse."},
			},
			"required": []string{"todo_id", "expected_owner_path", "repository_path"},
		},
		OutputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"status": map[string]any{"type": "string"}},
			"required":   []string{"status"},
		},
	}
}

func validateTodoRepairOwnerInvocation(inv Invocation) (Invocation, error) {
	var input TodoRepairOwnerInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, err
	}
	if input.RequestID != "" && inv.RequestID != "" && input.RequestID != inv.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch")
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	input.ExpectedOwnerPath = filepath.Clean(strings.TrimSpace(input.ExpectedOwnerPath))
	input.RepositoryPath = filepath.Clean(strings.TrimSpace(input.RepositoryPath))
	if input.TodoID <= 0 || !filepath.IsAbs(input.ExpectedOwnerPath) || !filepath.IsAbs(input.RepositoryPath) || input.ExpectedOwnerPath == input.RepositoryPath {
		return Invocation{}, fmt.Errorf("todo_id and distinct absolute expected_owner_path and repository_path are required")
	}
	inv.RequestID = input.RequestID
	var err error
	inv.Args, err = json.Marshal(input)
	return inv, err
}
