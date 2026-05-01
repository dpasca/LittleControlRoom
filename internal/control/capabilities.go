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
	FeatureApprovalResponse = "approval_response"
	FeatureReview           = "review"
	FeatureCompact          = "compact"
)

const HostEffectMayRevealEngineerSession = "may_reveal_engineer_session"

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

func Capabilities() []Capability {
	return []Capability{EngineerSendPromptCapability()}
}

func CapabilityByName(name CapabilityName) (Capability, bool) {
	switch CapabilityName(strings.TrimSpace(string(name))) {
	case CapabilityEngineerSendPrompt:
		return EngineerSendPromptCapability(), true
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
