package control

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	FeatureSendPrompt       = "send_prompt"
	FeatureResume           = "resume"
	FeatureForceNew         = "force_new"
	FeatureCreateTask       = "create_task"
	FeatureContinueTask     = "continue_task"
	FeatureCloseTask        = "close_task"
	FeatureApprovalResponse = "approval_response"
	FeatureReview           = "review"
	FeatureCompact          = "compact"
)

const (
	HostEffectMayRevealEngineerSession = "may_reveal_engineer_session"
	HostEffectMayCreateTaskWorkspace   = "may_create_task_workspace"
)

type AgentTaskKind string

const (
	AgentTaskKindAgent    AgentTaskKind = "agent"
	AgentTaskKindSubagent AgentTaskKind = "subagent"
)

func NormalizeAgentTaskKind(value string) AgentTaskKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(AgentTaskKindAgent):
		return AgentTaskKindAgent
	case string(AgentTaskKindSubagent):
		return AgentTaskKindSubagent
	default:
		return ""
	}
}

func (k AgentTaskKind) Normalized() AgentTaskKind {
	return NormalizeAgentTaskKind(string(k))
}

type AgentTaskCloseStatus string

const (
	AgentTaskCloseCompleted AgentTaskCloseStatus = "completed"
	AgentTaskCloseArchived  AgentTaskCloseStatus = "archived"
	AgentTaskCloseWaiting   AgentTaskCloseStatus = "waiting"
)

func NormalizeAgentTaskCloseStatus(value string) AgentTaskCloseStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(AgentTaskCloseCompleted):
		return AgentTaskCloseCompleted
	case string(AgentTaskCloseArchived):
		return AgentTaskCloseArchived
	case string(AgentTaskCloseWaiting):
		return AgentTaskCloseWaiting
	default:
		return ""
	}
}

func (s AgentTaskCloseStatus) Normalized() AgentTaskCloseStatus {
	return NormalizeAgentTaskCloseStatus(string(s))
}

type EngineerSendPromptInput struct {
	RequestID   string      `json:"request_id,omitempty"`
	ProjectPath string      `json:"project_path"`
	ProjectName string      `json:"project_name"`
	Provider    Provider    `json:"provider"`
	SessionMode SessionMode `json:"session_mode"`
	Prompt      string      `json:"prompt"`
	Reveal      bool        `json:"reveal"`
}

type EngineerSendPromptResult struct {
	Provider    Provider `json:"provider"`
	ProjectPath string   `json:"project_path"`
	SessionID   string   `json:"session_id"`
	Reused      bool     `json:"reused"`
	PromptSent  bool     `json:"prompt_sent"`
	Revealed    bool     `json:"revealed"`
	Status      string   `json:"status"`
}

type AgentTaskCreateInput struct {
	RequestID    string        `json:"request_id,omitempty"`
	Title        string        `json:"title"`
	Kind         AgentTaskKind `json:"kind"`
	ParentTaskID string        `json:"parent_task_id"`
	Prompt       string        `json:"prompt"`
	Provider     Provider      `json:"provider"`
	Reveal       bool          `json:"reveal"`
	Capabilities []string      `json:"capabilities"`
	Resources    []ResourceRef `json:"resources"`
}

type AgentTaskContinueInput struct {
	RequestID   string      `json:"request_id,omitempty"`
	TaskID      string      `json:"task_id"`
	Prompt      string      `json:"prompt"`
	Provider    Provider    `json:"provider"`
	SessionMode SessionMode `json:"session_mode"`
	Reveal      bool        `json:"reveal"`
}

type AgentTaskCloseInput struct {
	RequestID    string               `json:"request_id,omitempty"`
	TaskID       string               `json:"task_id"`
	Status       AgentTaskCloseStatus `json:"status"`
	Summary      string               `json:"summary"`
	CloseSession bool                 `json:"close_session"`
}

func Capabilities() []Capability {
	return []Capability{
		EngineerSendPromptCapability(),
		AgentTaskCreateCapability(),
		AgentTaskContinueCapability(),
		AgentTaskCloseCapability(),
	}
}

func CapabilityByName(name CapabilityName) (Capability, bool) {
	switch CapabilityName(strings.TrimSpace(string(name))) {
	case CapabilityEngineerSendPrompt:
		return EngineerSendPromptCapability(), true
	case CapabilityAgentTaskCreate:
		return AgentTaskCreateCapability(), true
	case CapabilityAgentTaskContinue:
		return AgentTaskContinueCapability(), true
	case CapabilityAgentTaskClose:
		return AgentTaskCloseCapability(), true
	default:
		return Capability{}, false
	}
}

func EngineerSendPromptCapability() Capability {
	return Capability{
		Name:         CapabilityEngineerSendPrompt,
		Description:  "Send a prompt to an embedded engineer session for a project.",
		InputSchema:  engineerSendPromptInputSchema(),
		OutputSchema: engineerSendPromptOutputSchema(),
		Risk:         RiskExternal,
		Confirmation: ConfirmationRequired,
		RequiresHost: false,
		HostEffects:  []string{HostEffectMayRevealEngineerSession},
		Providers: []ProviderCapability{
			{
				ID:        ProviderCodex,
				Available: true,
				Features:  []string{FeatureSendPrompt, FeatureResume, FeatureForceNew, FeatureApprovalResponse, FeatureReview, FeatureCompact},
			},
			{
				ID:        ProviderOpenCode,
				Available: true,
				Features:  []string{FeatureSendPrompt, FeatureResume, FeatureForceNew, FeatureApprovalResponse},
			},
			{
				ID:        ProviderClaudeCode,
				Available: false,
				Reason:    "disabled",
				Features:  []string{FeatureSendPrompt, FeatureResume, FeatureForceNew},
			},
		},
	}
}

func AgentTaskCreateCapability() Capability {
	return Capability{
		Name:         CapabilityAgentTaskCreate,
		Description:  "Create a generic Boss-owned agent task and optionally start an embedded engineer session in its workspace.",
		InputSchema:  agentTaskCreateInputSchema(),
		OutputSchema: agentTaskOutputSchema(),
		Risk:         RiskExternal,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayCreateTaskWorkspace, HostEffectMayRevealEngineerSession},
		Providers:    EngineerSendPromptCapability().Providers,
	}
}

func AgentTaskContinueCapability() Capability {
	return Capability{
		Name:         CapabilityAgentTaskContinue,
		Description:  "Continue an existing Boss-owned agent task, reusing its workspace and engineer session when possible.",
		InputSchema:  agentTaskContinueInputSchema(),
		OutputSchema: agentTaskOutputSchema(),
		Risk:         RiskExternal,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayRevealEngineerSession},
		Providers:    EngineerSendPromptCapability().Providers,
	}
}

func AgentTaskCloseCapability() Capability {
	return Capability{
		Name:         CapabilityAgentTaskClose,
		Description:  "Mark a Boss-owned agent task completed, waiting, or archived.",
		InputSchema:  agentTaskCloseInputSchema(),
		OutputSchema: agentTaskOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
	}
}

func NormalizeEngineerSendPromptInput(input EngineerSendPromptInput) (EngineerSendPromptInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.Provider = input.Provider.Normalized()
	if input.Provider == "" {
		return EngineerSendPromptInput{}, fmt.Errorf("unsupported engineer provider: %s", input.Provider)
	}
	input.SessionMode = input.SessionMode.Normalized()
	if input.SessionMode == "" {
		return EngineerSendPromptInput{}, fmt.Errorf("unsupported engineer session mode: %s", input.SessionMode)
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.ProjectPath == "" && input.ProjectName == "" {
		return EngineerSendPromptInput{}, fmt.Errorf("project_path or project_name is required")
	}
	if input.Prompt == "" {
		return EngineerSendPromptInput{}, fmt.Errorf("prompt is required")
	}
	return input, nil
}

func NormalizeAgentTaskCreateInput(input AgentTaskCreateInput) (AgentTaskCreateInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return AgentTaskCreateInput{}, fmt.Errorf("agent task title is required")
	}
	input.Kind = input.Kind.Normalized()
	if input.Kind == "" {
		return AgentTaskCreateInput{}, fmt.Errorf("unsupported agent task kind: %s", input.Kind)
	}
	input.ParentTaskID = strings.TrimSpace(input.ParentTaskID)
	if input.Kind == AgentTaskKindSubagent && input.ParentTaskID == "" {
		return AgentTaskCreateInput{}, fmt.Errorf("parent_task_id is required for subagent tasks")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.Provider = input.Provider.Normalized()
	if input.Provider == "" {
		return AgentTaskCreateInput{}, fmt.Errorf("unsupported engineer provider: %s", input.Provider)
	}
	input.Capabilities = normalizeCapabilityList(input.Capabilities)
	input.Resources = normalizeResourceRefs(input.Resources)
	return input, nil
}

func NormalizeAgentTaskContinueInput(input AgentTaskContinueInput) (AgentTaskContinueInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return AgentTaskContinueInput{}, fmt.Errorf("agent task id is required")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return AgentTaskContinueInput{}, fmt.Errorf("prompt is required")
	}
	input.Provider = input.Provider.Normalized()
	if input.Provider == "" {
		return AgentTaskContinueInput{}, fmt.Errorf("unsupported engineer provider: %s", input.Provider)
	}
	input.SessionMode = input.SessionMode.Normalized()
	if input.SessionMode == "" {
		return AgentTaskContinueInput{}, fmt.Errorf("unsupported engineer session mode: %s", input.SessionMode)
	}
	return input, nil
}

func NormalizeAgentTaskCloseInput(input AgentTaskCloseInput) (AgentTaskCloseInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return AgentTaskCloseInput{}, fmt.Errorf("agent task id is required")
	}
	input.Status = input.Status.Normalized()
	if input.Status == "" {
		return AgentTaskCloseInput{}, fmt.Errorf("unsupported agent task close status: %s", input.Status)
	}
	input.Summary = strings.TrimSpace(input.Summary)
	return input, nil
}

func validateEngineerSendPromptInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityEngineerSendPrompt)
	}
	var input EngineerSendPromptInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityEngineerSendPrompt, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityEngineerSendPrompt)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeEngineerSendPromptInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityEngineerSendPrompt, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateAgentTaskCreateInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityAgentTaskCreate)
	}
	var input AgentTaskCreateInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityAgentTaskCreate, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityAgentTaskCreate)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeAgentTaskCreateInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityAgentTaskCreate, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateAgentTaskContinueInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityAgentTaskContinue)
	}
	var input AgentTaskContinueInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityAgentTaskContinue, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityAgentTaskContinue)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeAgentTaskContinueInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityAgentTaskContinue, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateAgentTaskCloseInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityAgentTaskClose)
	}
	var input AgentTaskCloseInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityAgentTaskClose, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityAgentTaskClose)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeAgentTaskCloseInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityAgentTaskClose, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func normalizeCapabilityList(capabilities []string) []string {
	out := make([]string, 0, len(capabilities))
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func normalizeResourceRefs(resources []ResourceRef) []ResourceRef {
	out := make([]ResourceRef, 0, len(resources))
	for _, resource := range resources {
		resource.Kind = ResourceKind(strings.TrimSpace(string(resource.Kind)))
		resource.ID = strings.TrimSpace(resource.ID)
		resource.Path = strings.TrimSpace(resource.Path)
		if resource.Path != "" {
			resource.Path = filepath.Clean(resource.Path)
		}
		resource.ProjectPath = strings.TrimSpace(resource.ProjectPath)
		if resource.ProjectPath != "" {
			resource.ProjectPath = filepath.Clean(resource.ProjectPath)
		}
		resource.Provider = resource.Provider.Normalized()
		resource.SessionID = strings.TrimSpace(resource.SessionID)
		resource.Label = strings.TrimSpace(resource.Label)
		if resource.Kind == "" {
			continue
		}
		out = append(out, resource)
	}
	return out
}

func engineerSendPromptInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "Optional stable idempotency key for this request.",
			},
			"project_path": map[string]any{
				"type":        "string",
				"description": "Exact project path, or empty when project_name is supplied for later resolution.",
			},
			"project_name": map[string]any{
				"type":        "string",
				"description": "Project name when path is unavailable, or empty.",
			},
			"provider": map[string]any{
				"type": "string",
				"enum": []string{
					string(ProviderAuto),
					string(ProviderCodex),
					string(ProviderOpenCode),
					string(ProviderClaudeCode),
				},
			},
			"session_mode": map[string]any{
				"type": "string",
				"enum": []string{
					string(SessionModeResumeOrNew),
					string(SessionModeNew),
				},
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Prompt to send to the engineer session.",
			},
			"reveal": map[string]any{
				"type":        "boolean",
				"description": "Whether the host should reveal the engineer session after sending.",
			},
		},
		"required": []string{"project_path", "project_name", "provider", "session_mode", "prompt", "reveal"},
	}
}

func engineerSendPromptOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"provider": map[string]any{
				"type": "string",
				"enum": []string{
					string(ProviderCodex),
					string(ProviderOpenCode),
					string(ProviderClaudeCode),
				},
			},
			"project_path": map[string]any{"type": "string"},
			"session_id":   map[string]any{"type": "string"},
			"reused":       map[string]any{"type": "boolean"},
			"prompt_sent":  map[string]any{"type": "boolean"},
			"revealed":     map[string]any{"type": "boolean"},
			"status":       map[string]any{"type": "string"},
		},
		"required": []string{"provider", "project_path", "session_id", "reused", "prompt_sent", "revealed", "status"},
	}
}

func agentTaskCreateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":     map[string]any{"type": "string"},
			"title":          map[string]any{"type": "string"},
			"kind":           map[string]any{"type": "string", "enum": []string{string(AgentTaskKindAgent), string(AgentTaskKindSubagent)}},
			"parent_task_id": map[string]any{"type": "string"},
			"prompt":         map[string]any{"type": "string"},
			"provider": map[string]any{
				"type": "string",
				"enum": []string{string(ProviderAuto), string(ProviderCodex), string(ProviderOpenCode), string(ProviderClaudeCode)},
			},
			"reveal":       map[string]any{"type": "boolean"},
			"capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"resources":    resourceRefsSchema(),
		},
		"required": []string{"title", "kind", "parent_task_id", "prompt", "provider", "reveal", "capabilities", "resources"},
	}
}

func agentTaskContinueInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"task_id":    map[string]any{"type": "string"},
			"prompt":     map[string]any{"type": "string"},
			"provider": map[string]any{
				"type": "string",
				"enum": []string{string(ProviderAuto), string(ProviderCodex), string(ProviderOpenCode), string(ProviderClaudeCode)},
			},
			"session_mode": map[string]any{
				"type": "string",
				"enum": []string{string(SessionModeResumeOrNew), string(SessionModeNew)},
			},
			"reveal": map[string]any{"type": "boolean"},
		},
		"required": []string{"task_id", "prompt", "provider", "session_mode", "reveal"},
	}
}

func agentTaskCloseInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":    map[string]any{"type": "string"},
			"task_id":       map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string", "enum": []string{string(AgentTaskCloseCompleted), string(AgentTaskCloseArchived), string(AgentTaskCloseWaiting)}},
			"summary":       map[string]any{"type": "string"},
			"close_session": map[string]any{"type": "boolean"},
		},
		"required": []string{"task_id", "status", "summary", "close_session"},
	}
}

func agentTaskOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id":        map[string]any{"type": "string"},
			"status":         map[string]any{"type": "string"},
			"workspace_path": map[string]any{"type": "string"},
			"provider":       map[string]any{"type": "string"},
			"session_id":     map[string]any{"type": "string"},
			"prompt_sent":    map[string]any{"type": "boolean"},
		},
		"required": []string{"task_id", "status", "workspace_path", "provider", "session_id", "prompt_sent"},
	}
}

func resourceRefsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"kind":         map[string]any{"type": "string", "enum": []string{string(ResourceProject), string(ResourceEngineerSession), string(ResourceTodo), string(ResourceAgentTask), string(ResourceProcess), string(ResourcePort), string(ResourceFile)}},
				"id":           map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"project_path": map[string]any{"type": "string"},
				"provider":     map[string]any{"type": "string", "enum": []string{"", string(ProviderAuto), string(ProviderCodex), string(ProviderOpenCode), string(ProviderClaudeCode)}},
				"session_id":   map[string]any{"type": "string"},
				"todo_id":      map[string]any{"type": "integer"},
				"pid":          map[string]any{"type": "integer"},
				"port":         map[string]any{"type": "integer"},
				"label":        map[string]any{"type": "string"},
			},
			"required": []string{"kind", "id", "path", "project_path", "provider", "session_id", "todo_id", "pid", "port", "label"},
		},
	}
}
