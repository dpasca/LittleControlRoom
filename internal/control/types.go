package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CapabilityName string

const (
	CapabilityEngineerSendPrompt CapabilityName = "engineer.send_prompt"
	CapabilityAgentTaskCreate    CapabilityName = "agent_task.create"
	CapabilityAgentTaskContinue  CapabilityName = "agent_task.continue"
	CapabilityAgentTaskClose     CapabilityName = "agent_task.close"
	CapabilityScratchTaskArchive CapabilityName = "scratch_task.archive"
)

type Provider string

const (
	ProviderAuto       Provider = "auto"
	ProviderCodex      Provider = "codex"
	ProviderOpenCode   Provider = "opencode"
	ProviderClaudeCode Provider = "claude_code"
)

func NormalizeProvider(value string) Provider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ProviderAuto):
		return ProviderAuto
	case string(ProviderCodex):
		return ProviderCodex
	case string(ProviderOpenCode), "open-code", "open_code":
		return ProviderOpenCode
	case string(ProviderClaudeCode), "claude-code", "claude":
		return ProviderClaudeCode
	default:
		return ""
	}
}

func (p Provider) Normalized() Provider {
	return NormalizeProvider(string(p))
}

func (p Provider) Valid() bool {
	return p.Normalized() != ""
}

func (p Provider) Label() string {
	switch p.Normalized() {
	case ProviderAuto:
		return "auto"
	case ProviderOpenCode:
		return "OpenCode"
	case ProviderClaudeCode:
		return "Claude Code"
	case ProviderCodex:
		return "Codex"
	default:
		return ""
	}
}

type SessionMode string

const (
	SessionModeResumeOrNew SessionMode = "resume_or_new"
	SessionModeNew         SessionMode = "new"
)

func NormalizeSessionMode(value string) SessionMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(SessionModeResumeOrNew), "resume", "resume-or-new":
		return SessionModeResumeOrNew
	case string(SessionModeNew), "fresh", "force_new", "force-new":
		return SessionModeNew
	default:
		return ""
	}
}

func (m SessionMode) Normalized() SessionMode {
	return NormalizeSessionMode(string(m))
}

type RiskLevel string

const (
	RiskRead        RiskLevel = "read"
	RiskWrite       RiskLevel = "write"
	RiskExternal    RiskLevel = "external"
	RiskDestructive RiskLevel = "destructive"
)

type ConfirmationPolicy string

const (
	ConfirmationNone     ConfirmationPolicy = "none"
	ConfirmationRequired ConfirmationPolicy = "required"
)

type OperationStatus string

const (
	OperationProposed               OperationStatus = "proposed"
	OperationWaitingForConfirmation OperationStatus = "waiting_for_confirmation"
	OperationRunning                OperationStatus = "running"
	OperationCompleted              OperationStatus = "completed"
	OperationFailed                 OperationStatus = "failed"
	OperationCanceled               OperationStatus = "canceled"
)

type ProviderCapability struct {
	ID        Provider `json:"id"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Features  []string `json:"features,omitempty"`
}

type Capability struct {
	Name         CapabilityName       `json:"name"`
	Description  string               `json:"description"`
	InputSchema  map[string]any       `json:"input_schema,omitempty"`
	OutputSchema map[string]any       `json:"output_schema,omitempty"`
	Risk         RiskLevel            `json:"risk"`
	Confirmation ConfirmationPolicy   `json:"confirmation"`
	RequiresHost bool                 `json:"requires_host"`
	HostEffects  []string             `json:"host_effects,omitempty"`
	Providers    []ProviderCapability `json:"providers,omitempty"`
}

type Invocation struct {
	RequestID  string          `json:"request_id,omitempty"`
	Capability CapabilityName  `json:"capability"`
	Args       json.RawMessage `json:"args,omitempty"`
}

func ValidateInvocation(inv Invocation) (Invocation, error) {
	inv.RequestID = strings.TrimSpace(inv.RequestID)
	inv.Capability = CapabilityName(strings.TrimSpace(string(inv.Capability)))
	switch inv.Capability {
	case CapabilityEngineerSendPrompt:
		return validateEngineerSendPromptInvocation(inv)
	case CapabilityAgentTaskCreate:
		return validateAgentTaskCreateInvocation(inv)
	case CapabilityAgentTaskContinue:
		return validateAgentTaskContinueInvocation(inv)
	case CapabilityAgentTaskClose:
		return validateAgentTaskCloseInvocation(inv)
	case CapabilityScratchTaskArchive:
		return validateScratchTaskArchiveInvocation(inv)
	case "":
		return Invocation{}, fmt.Errorf("capability is required")
	default:
		return Invocation{}, fmt.Errorf("unsupported control capability: %s", inv.Capability)
	}
}

type ResourceKind string

const (
	ResourceProject         ResourceKind = "project"
	ResourceEngineerSession ResourceKind = "engineer_session"
	ResourceTodo            ResourceKind = "todo"
	ResourceAgentTask       ResourceKind = "agent_task"
	ResourceProcess         ResourceKind = "process"
	ResourcePort            ResourceKind = "port"
	ResourceFile            ResourceKind = "file"
)

type ResourceRef struct {
	Kind        ResourceKind `json:"kind"`
	ID          string       `json:"id,omitempty"`
	Path        string       `json:"path,omitempty"`
	ProjectPath string       `json:"project_path,omitempty"`
	Provider    Provider     `json:"provider,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
	TodoID      int64        `json:"todo_id,omitempty"`
	PID         int          `json:"pid,omitempty"`
	Port        int          `json:"port,omitempty"`
	Label       string       `json:"label,omitempty"`
}

type Operation struct {
	ID             string          `json:"id"`
	Capability     CapabilityName  `json:"capability"`
	Status         OperationStatus `json:"status"`
	Invocation     Invocation      `json:"invocation"`
	Resources      []ResourceRef   `json:"resources,omitempty"`
	RequestedBy    string          `json:"requested_by,omitempty"`
	Confirmed      bool            `json:"confirmed"`
	ConfirmationBy string          `json:"confirmation_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      time.Time       `json:"started_at,omitempty"`
	CompletedAt    time.Time       `json:"completed_at,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
}
