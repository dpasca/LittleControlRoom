package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CapabilityName string

const (
	CapabilityEngineerSendPrompt                 CapabilityName = "engineer.send_prompt"
	CapabilityAgentTaskCreate                    CapabilityName = "agent_task.create"
	CapabilityAgentTaskContinue                  CapabilityName = "agent_task.continue"
	CapabilityAgentTaskClose                     CapabilityName = "agent_task.close"
	CapabilityProjectCreateAndStartEngineer      CapabilityName = "project.create_and_start_engineer"
	CapabilityProjectSetCategory                 CapabilityName = "project.set_category"
	CapabilityProjectArchive                     CapabilityName = "project.set_archive_state"
	CapabilityScratchTaskArchive                 CapabilityName = "scratch_task.archive"
	CapabilityTodoAdd                            CapabilityName = "todo.add"
	CapabilityTodoCreateWorktreeAndStartEngineer CapabilityName = "todo.create_worktree_and_start_engineer"
	CapabilityTodoComplete                       CapabilityName = "todo.complete"
	CapabilitySettingsUpdate                     CapabilityName = "settings.update"
	CapabilityGitPrepareCommit                   CapabilityName = "git.prepare_commit"
)

func CapabilityNameValues() []CapabilityName {
	return []CapabilityName{
		CapabilityEngineerSendPrompt,
		CapabilityAgentTaskCreate,
		CapabilityAgentTaskContinue,
		CapabilityAgentTaskClose,
		CapabilityProjectCreateAndStartEngineer,
		CapabilityProjectSetCategory,
		CapabilityProjectArchive,
		CapabilityScratchTaskArchive,
		CapabilityTodoAdd,
		CapabilityTodoCreateWorktreeAndStartEngineer,
		CapabilityTodoComplete,
		CapabilitySettingsUpdate,
		CapabilityGitPrepareCommit,
	}
}

func CapabilityNameStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, CapabilityNameValues()...)
}

type CapabilityDomain string

const (
	CapabilityDomainEngineer CapabilityDomain = "engineer"
	CapabilityDomainTask     CapabilityDomain = "agent_task"
	CapabilityDomainProject  CapabilityDomain = "project"
	CapabilityDomainTodo     CapabilityDomain = "todo"
	CapabilityDomainSettings CapabilityDomain = "settings"
	CapabilityDomainGit      CapabilityDomain = "git"
)

func CapabilityDomainValues() []CapabilityDomain {
	return []CapabilityDomain{
		CapabilityDomainEngineer,
		CapabilityDomainTask,
		CapabilityDomainProject,
		CapabilityDomainTodo,
		CapabilityDomainSettings,
		CapabilityDomainGit,
	}
}

func CapabilityDomainStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, CapabilityDomainValues()...)
}

func NormalizeCapabilityDomain(value string) CapabilityDomain {
	switch CapabilityDomain(strings.ToLower(strings.TrimSpace(value))) {
	case CapabilityDomainEngineer:
		return CapabilityDomainEngineer
	case CapabilityDomainTask:
		return CapabilityDomainTask
	case CapabilityDomainProject:
		return CapabilityDomainProject
	case CapabilityDomainTodo:
		return CapabilityDomainTodo
	case CapabilityDomainSettings:
		return CapabilityDomainSettings
	case CapabilityDomainGit:
		return CapabilityDomainGit
	default:
		return ""
	}
}

type AuthorityScope string

const (
	AuthorityScopeProject   AuthorityScope = "project"
	AuthorityScopePortfolio AuthorityScope = "portfolio"
	AuthorityScopeHost      AuthorityScope = "host"
)

func AuthorityScopeValues() []AuthorityScope {
	return []AuthorityScope{
		AuthorityScopeProject,
		AuthorityScopePortfolio,
		AuthorityScopeHost,
	}
}

func AuthorityScopeStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, AuthorityScopeValues()...)
}

func NormalizeAuthorityScope(value string) AuthorityScope {
	switch AuthorityScope(strings.ToLower(strings.TrimSpace(value))) {
	case AuthorityScopeProject:
		return AuthorityScopeProject
	case AuthorityScopePortfolio:
		return AuthorityScopePortfolio
	case AuthorityScopeHost:
		return AuthorityScopeHost
	default:
		return ""
	}
}

func AuthorityAllows(available, required AuthorityScope) bool {
	rank := func(scope AuthorityScope) int {
		switch NormalizeAuthorityScope(string(scope)) {
		case AuthorityScopeProject:
			return 1
		case AuthorityScopePortfolio:
			return 2
		case AuthorityScopeHost:
			return 3
		default:
			return 0
		}
	}
	return rank(available) >= rank(required) && rank(required) > 0
}

type Provider string

const (
	ProviderAuto       Provider = "auto"
	ProviderCodex      Provider = "codex"
	ProviderOpenCode   Provider = "opencode"
	ProviderClaudeCode Provider = "claude_code"
	ProviderLCAgent    Provider = "lcagent"
)

func ProviderValues() []Provider {
	return []Provider{
		ProviderAuto,
		ProviderCodex,
		ProviderOpenCode,
		ProviderClaudeCode,
		ProviderLCAgent,
	}
}

func EngineerProviderValues() []Provider {
	return []Provider{
		ProviderCodex,
		ProviderOpenCode,
		ProviderClaudeCode,
		ProviderLCAgent,
	}
}

func ProviderStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, ProviderValues()...)
}

func EngineerProviderStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, EngineerProviderValues()...)
}

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
	case string(ProviderLCAgent), "lc-agent", "lc_agent":
		return ProviderLCAgent
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
	case ProviderLCAgent:
		return "LCAgent"
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

func SessionModeValues() []SessionMode {
	return []SessionMode{
		SessionModeResumeOrNew,
		SessionModeNew,
	}
}

func SessionModeStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, SessionModeValues()...)
}

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

func RiskLevelValues() []RiskLevel {
	return []RiskLevel{
		RiskRead,
		RiskWrite,
		RiskExternal,
		RiskDestructive,
	}
}

func RiskLevelStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, RiskLevelValues()...)
}

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
	Domain       CapabilityDomain     `json:"domain"`
	Scope        AuthorityScope       `json:"scope"`
	Description  string               `json:"description"`
	InputSchema  map[string]any       `json:"input_schema,omitempty"`
	OutputSchema map[string]any       `json:"output_schema,omitempty"`
	Risk         RiskLevel            `json:"risk"`
	Confirmation ConfirmationPolicy   `json:"confirmation"`
	RequiresHost bool                 `json:"requires_host"`
	Async        bool                 `json:"async"`
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
	case CapabilityProjectCreateAndStartEngineer:
		return validateProjectCreateAndStartEngineerInvocation(inv)
	case CapabilityProjectSetCategory:
		return validateProjectSetCategoryInvocation(inv)
	case CapabilityProjectArchive:
		return validateProjectArchiveInvocation(inv)
	case CapabilityScratchTaskArchive:
		return validateScratchTaskArchiveInvocation(inv)
	case CapabilityTodoAdd:
		return validateTodoAddInvocation(inv)
	case CapabilityTodoCreateWorktreeAndStartEngineer:
		return validateTodoCreateWorktreeAndStartEngineerInvocation(inv)
	case CapabilityTodoComplete:
		return validateTodoCompleteInvocation(inv)
	case CapabilitySettingsUpdate:
		return validateSettingsUpdateInvocation(inv)
	case CapabilityGitPrepareCommit:
		return validateGitPrepareCommitInvocation(inv)
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

func ResourceKindValues() []ResourceKind {
	return []ResourceKind{
		ResourceProject,
		ResourceEngineerSession,
		ResourceTodo,
		ResourceAgentTask,
		ResourceProcess,
		ResourcePort,
		ResourceFile,
	}
}

func ResourceKindStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, ResourceKindValues()...)
}

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

func stringValues[T ~string](includeEmpty bool, values ...T) []string {
	out := make([]string, 0, len(values)+1)
	if includeEmpty {
		out = append(out, "")
	}
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

type Operation struct {
	ID              string          `json:"id"`
	ClientRequestID string          `json:"client_request_id,omitempty"`
	Capability      CapabilityName  `json:"capability"`
	Status          OperationStatus `json:"status"`
	Invocation      Invocation      `json:"invocation"`
	Resources       []ResourceRef   `json:"resources,omitempty"`
	Source          string          `json:"source,omitempty"`
	Provider        string          `json:"provider,omitempty"`
	SessionKey      string          `json:"session_key,omitempty"`
	ProjectPath     string          `json:"project_path,omitempty"`
	RequestedBy     string          `json:"requested_by,omitempty"`
	Confirmed       bool            `json:"confirmed"`
	ConfirmationBy  string          `json:"confirmation_by,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	StartedAt       time.Time       `json:"started_at,omitempty"`
	CompletedAt     time.Time       `json:"completed_at,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
}

type EngineerMessageState string

const (
	EngineerMessageQueued     EngineerMessageState = "queued"
	EngineerMessageDelivering EngineerMessageState = "delivering"
	EngineerMessageDelivered  EngineerMessageState = "delivered"
	EngineerMessageFailed     EngineerMessageState = "failed"
)

func (s EngineerMessageState) Terminal() bool {
	switch s {
	case EngineerMessageDelivered, EngineerMessageFailed:
		return true
	default:
		return false
	}
}

type EngineerMessage struct {
	ID                       string               `json:"id"`
	OperationID              string               `json:"operation_id,omitempty"`
	AgentTaskID              string               `json:"agent_task_id,omitempty"`
	ProjectPath              string               `json:"project_path"`
	Provider                 Provider             `json:"provider"`
	SessionMode              SessionMode          `json:"session_mode"`
	RequestedTargetSessionID string               `json:"requested_target_session_id,omitempty"`
	TargetSessionID          string               `json:"target_session_id,omitempty"`
	Prompt                   string               `json:"prompt"`
	Reveal                   bool                 `json:"reveal"`
	TodoID                   int64                `json:"todo_id,omitempty"`
	TodoLabel                string               `json:"todo_label,omitempty"`
	TodoText                 string               `json:"todo_text,omitempty"`
	State                    EngineerMessageState `json:"state"`
	AttemptCount             int                  `json:"attempt_count"`
	LastError                string               `json:"last_error,omitempty"`
	CreatedAt                time.Time            `json:"created_at"`
	UpdatedAt                time.Time            `json:"updated_at"`
	DeliveredAt              time.Time            `json:"delivered_at,omitempty"`
}

type EngineerMessageReceipt struct {
	MessageID       string               `json:"message_id"`
	State           EngineerMessageState `json:"state"`
	Provider        Provider             `json:"provider"`
	ProjectPath     string               `json:"project_path"`
	TargetSessionID string               `json:"target_session_id,omitempty"`
	Status          string               `json:"status"`
}
