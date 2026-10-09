package control

import (
	"encoding/json"
	"path/filepath"
)

const ConfirmationScopedPermission = "scoped_permission"

// ControlPermission is operator-owned. Matching uses validated typed arguments,
// never prompt text, risk labels, or agent-supplied assertions of permission.
type ControlPermission struct {
	ID          int64          `json:"id"`
	Origin      string         `json:"origin"`
	Provider    string         `json:"provider"`
	SessionKey  string         `json:"session_key,omitempty"`
	Capability  CapabilityName `json:"capability"`
	Target      string         `json:"target"`
	Constraints string         `json:"constraints"`
	Limit       int            `json:"limit"`
	Used        int            `json:"used"`
}

// PermissionForOperation describes the smallest reusable permission for a
// request. Only the fields explicitly removed below may vary. New capability
// fields therefore narrow grants by default rather than silently widening them.
func PermissionForOperation(op Operation) (ControlPermission, bool) {
	if !filepath.IsAbs(op.ProjectPath) || op.SessionKey == "" || op.Provider == "" {
		return ControlPermission{}, false
	}
	inv, err := ValidateInvocation(op.Invocation)
	if err != nil {
		return ControlPermission{}, false
	}
	var args map[string]any
	if json.Unmarshal(inv.Args, &args) != nil {
		return ControlPermission{}, false
	}
	permission := ControlPermission{Origin: op.ProjectPath, Provider: op.Provider, Capability: inv.Capability}
	remove := []string{"request_id", "reveal"}
	switch inv.Capability {
	case CapabilityEngineerSendPrompt:
		var input EngineerSendPromptInput
		_ = json.Unmarshal(inv.Args, &input)
		if input.SessionMode != SessionModeResumeOrNew || input.TargetSessionID == "" || input.Provider == ProviderAuto || input.TodoID != 0 || input.EngineerModelSelection != (EngineerModelSelection{}) {
			return ControlPermission{}, false
		}
		permission.Target = input.ProjectPath
		remove = append(remove, "prompt", "target_session_id", "todo_text", "todo_label")
	case CapabilityTodoAdd, CapabilityTodoComplete, CapabilityProjectSetCategory:
		permission.Target, _ = args["project_path"].(string)
		remove = append(remove, "text", "todo_id", "todo_text", "todo_label", "evidence")
	case CapabilityTodoCreateWorktreeAndStartEngineer:
		var input TodoCreateWorktreeAndStartEngineerInput
		_ = json.Unmarshal(inv.Args, &input)
		if input.Provider == ProviderAuto || input.Model == "" || input.SelectModel {
			return ControlPermission{}, false
		}
		permission.Target = input.ProjectPath
		permission.Limit = 3
		remove = append(remove, "prompt", "todo_id", "todo_text", "todo_label")
	case CapabilityAgentTaskCreate:
		var input AgentTaskCreateInput
		_ = json.Unmarshal(inv.Args, &input)
		if input.Provider == ProviderAuto || input.Model == "" || input.SelectModel {
			return ControlPermission{}, false
		}
		permission.Target = op.ProjectPath
		permission.Limit = 3
		remove = append(remove, "prompt", "title")
	case CapabilityGitSubmoduleAlign:
		// The host additionally requires a clean fast-forward to the pinned
		// gitlink that needs no fetch, both when offering and when using a grant,
		// so the target and fetch choice cannot widen what the grant covers.
		var input GitSubmoduleAlignInput
		_ = json.Unmarshal(inv.Args, &input)
		permission.Target = input.ParentPath
		remove = append(remove, "target_commit", "fetch_if_missing")
	case CapabilityAgentTaskClose:
		var input AgentTaskCloseInput
		_ = json.Unmarshal(inv.Args, &input)
		if input.Status == AgentTaskCloseArchived || input.CloseSession {
			return ControlPermission{}, false
		}
		permission.Target = input.TaskID
		remove = append(remove, "summary")
	default:
		return ControlPermission{}, false
	}
	if permission.Target == "" {
		return ControlPermission{}, false
	}
	if inv.Capability != CapabilityAgentTaskClose && !filepath.IsAbs(permission.Target) {
		return ControlPermission{}, false
	}
	for _, key := range remove {
		delete(args, key)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return ControlPermission{}, false
	}
	permission.Constraints = string(encoded)
	return permission, true
}
