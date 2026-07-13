package control

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	FeatureSendPrompt                         = "send_prompt"
	FeatureResume                             = "resume"
	FeatureForceNew                           = "force_new"
	FeatureCreateTask                         = "create_task"
	FeatureContinueTask                       = "continue_task"
	FeatureCloseTask                          = "close_task"
	FeatureArchiveTask                        = "archive_task"
	FeatureAddTodo                            = "add_todo"
	FeatureCreateTodoWorktreeAndStartEngineer = "create_todo_worktree_and_start_engineer"
	FeatureCompleteTodo                       = "complete_todo"
	FeatureUpdateSettings                     = "update_settings"
	FeaturePrepareCommit                      = "prepare_commit"
	FeatureApprovalResponse                   = "approval_response"
	FeatureReview                             = "review"
	FeatureCompact                            = "compact"
)

const (
	HostEffectMayRevealEngineerSession = "may_reveal_engineer_session"
	HostEffectMayCreateTaskWorkspace   = "may_create_task_workspace"
	HostEffectMaySetProjectArchive     = "may_set_project_archive_state"
	HostEffectMayCreateProjectTodo     = "may_create_project_todo"
	HostEffectMayCreateProjectWorktree = "may_create_project_worktree"
	HostEffectMayCompleteProjectTodo   = "may_complete_project_todo"
	HostEffectMayUpdateSettings        = "may_update_settings"
	HostEffectMayPrepareGitCommit      = "may_prepare_git_commit_preview"
)

type AgentTaskKind string

const (
	AgentTaskKindAgent    AgentTaskKind = "agent"
	AgentTaskKindSubagent AgentTaskKind = "subagent"
)

func AgentTaskKindValues() []AgentTaskKind {
	return []AgentTaskKind{
		AgentTaskKindAgent,
		AgentTaskKindSubagent,
	}
}

func AgentTaskKindStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, AgentTaskKindValues()...)
}

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

func AgentTaskCloseStatusValues() []AgentTaskCloseStatus {
	return []AgentTaskCloseStatus{
		AgentTaskCloseCompleted,
		AgentTaskCloseArchived,
		AgentTaskCloseWaiting,
	}
}

func AgentTaskCloseStatusStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, AgentTaskCloseStatusValues()...)
}

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

type ProjectArchiveAction string

const (
	ProjectArchiveActionArchive   ProjectArchiveAction = "archive"
	ProjectArchiveActionUnarchive ProjectArchiveAction = "unarchive"
)

func ProjectArchiveActionValues() []ProjectArchiveAction {
	return []ProjectArchiveAction{
		ProjectArchiveActionArchive,
		ProjectArchiveActionUnarchive,
	}
}

func ProjectArchiveActionStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, ProjectArchiveActionValues()...)
}

func NormalizeProjectArchiveAction(value string) ProjectArchiveAction {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ProjectArchiveActionArchive), "archived":
		return ProjectArchiveActionArchive
	case string(ProjectArchiveActionUnarchive), "restore", "active":
		return ProjectArchiveActionUnarchive
	default:
		return ""
	}
}

func (a ProjectArchiveAction) Normalized() ProjectArchiveAction {
	return NormalizeProjectArchiveAction(string(a))
}

type EngineerSendPromptInput struct {
	RequestID   string      `json:"request_id,omitempty"`
	ProjectPath string      `json:"project_path"`
	ProjectName string      `json:"project_name"`
	Provider    Provider    `json:"provider"`
	SessionMode SessionMode `json:"session_mode"`
	Prompt      string      `json:"prompt"`
	TodoID      int64       `json:"todo_id,omitempty"`
	TodoLabel   string      `json:"todo_label,omitempty"`
	TodoText    string      `json:"todo_text,omitempty"`
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
	TodoID      int64    `json:"todo_id"`
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

type ScratchTaskArchiveInput struct {
	RequestID   string `json:"request_id,omitempty"`
	ProjectPath string `json:"project_path"`
	ProjectName string `json:"project_name"`
}

type ProjectArchiveInput struct {
	RequestID   string               `json:"request_id,omitempty"`
	ProjectPath string               `json:"project_path"`
	ProjectName string               `json:"project_name"`
	Action      ProjectArchiveAction `json:"action"`
	Resources   []ResourceRef        `json:"resources,omitempty"`
}

type TodoAddInput struct {
	RequestID   string `json:"request_id,omitempty"`
	ProjectPath string `json:"project_path"`
	ProjectName string `json:"project_name"`
	Text        string `json:"text"`
}

type TodoCreateWorktreeAndStartEngineerInput struct {
	RequestID    string   `json:"request_id,omitempty"`
	ProjectPath  string   `json:"project_path"`
	ProjectName  string   `json:"project_name"`
	TodoText     string   `json:"todo_text"`
	Prompt       string   `json:"prompt"`
	Provider     Provider `json:"provider"`
	Reveal       bool     `json:"reveal"`
	TodoID       int64    `json:"todo_id,omitempty"`
	TodoLabel    string   `json:"todo_label,omitempty"`
	WorktreePath string   `json:"worktree_path,omitempty"`
}

type TodoCompleteInput struct {
	RequestID   string `json:"request_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	TodoID      int64  `json:"todo_id"`
	TodoLabel   string `json:"todo_label,omitempty"`
	TodoText    string `json:"todo_text,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
}

type SettingsField string

const (
	SettingsFieldIncludePaths           SettingsField = "include_paths"
	SettingsFieldExcludePaths           SettingsField = "exclude_paths"
	SettingsFieldExcludeProjectPatterns SettingsField = "exclude_project_patterns"
	SettingsFieldPrivacyMode            SettingsField = "privacy_mode"
	SettingsFieldHideReasoningSections  SettingsField = "hide_reasoning_sections"
	SettingsFieldCodexLaunchPreset      SettingsField = "codex_launch_preset"
)

func SettingsFieldValues() []SettingsField {
	return []SettingsField{
		SettingsFieldIncludePaths,
		SettingsFieldExcludePaths,
		SettingsFieldExcludeProjectPatterns,
		SettingsFieldPrivacyMode,
		SettingsFieldHideReasoningSections,
		SettingsFieldCodexLaunchPreset,
	}
}

func SettingsFieldStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, SettingsFieldValues()...)
}

func NormalizeSettingsField(value string) SettingsField {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case string(SettingsFieldIncludePaths):
		return SettingsFieldIncludePaths
	case string(SettingsFieldExcludePaths):
		return SettingsFieldExcludePaths
	case string(SettingsFieldExcludeProjectPatterns):
		return SettingsFieldExcludeProjectPatterns
	case string(SettingsFieldPrivacyMode):
		return SettingsFieldPrivacyMode
	case string(SettingsFieldHideReasoningSections):
		return SettingsFieldHideReasoningSections
	case string(SettingsFieldCodexLaunchPreset):
		return SettingsFieldCodexLaunchPreset
	default:
		return ""
	}
}

func (f SettingsField) Normalized() SettingsField {
	return NormalizeSettingsField(string(f))
}

type SettingsUpdateOperation string

const (
	SettingsUpdateSet          SettingsUpdateOperation = "set"
	SettingsUpdateAppendUnique SettingsUpdateOperation = "append_unique"
	SettingsUpdateRemove       SettingsUpdateOperation = "remove"
)

func SettingsUpdateOperationValues() []SettingsUpdateOperation {
	return []SettingsUpdateOperation{
		SettingsUpdateSet,
		SettingsUpdateAppendUnique,
		SettingsUpdateRemove,
	}
}

func SettingsUpdateOperationStrings(includeEmpty bool) []string {
	return stringValues(includeEmpty, SettingsUpdateOperationValues()...)
}

func NormalizeSettingsUpdateOperation(value string) SettingsUpdateOperation {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case string(SettingsUpdateSet):
		return SettingsUpdateSet
	case string(SettingsUpdateAppendUnique), "append", "add", "add_unique":
		return SettingsUpdateAppendUnique
	case string(SettingsUpdateRemove), "delete":
		return SettingsUpdateRemove
	default:
		return ""
	}
}

func (o SettingsUpdateOperation) Normalized() SettingsUpdateOperation {
	return NormalizeSettingsUpdateOperation(string(o))
}

type SettingsChange struct {
	Field     SettingsField           `json:"field"`
	Operation SettingsUpdateOperation `json:"operation"`
	Value     string                  `json:"value"`
	Values    []string                `json:"values"`
	BoolValue bool                    `json:"bool_value"`
}

type SettingsUpdateInput struct {
	RequestID string           `json:"request_id,omitempty"`
	Changes   []SettingsChange `json:"changes"`
}

type GitPrepareCommitInput struct {
	RequestID       string `json:"request_id,omitempty"`
	ProjectPath     string `json:"project_path"`
	ProjectName     string `json:"project_name"`
	Message         string `json:"message,omitempty"`
	PushAfterCommit bool   `json:"push_after_commit"`
}

func Capabilities() []Capability {
	return []Capability{
		EngineerSendPromptCapability(),
		AgentTaskCreateCapability(),
		AgentTaskContinueCapability(),
		AgentTaskCloseCapability(),
		ProjectArchiveCapability(),
		ScratchTaskArchiveCapability(),
		TodoAddCapability(),
		TodoCreateWorktreeAndStartEngineerCapability(),
		TodoCompleteCapability(),
		SettingsUpdateCapability(),
		GitPrepareCommitCapability(),
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
	case CapabilityProjectArchive:
		return ProjectArchiveCapability(), true
	case CapabilityScratchTaskArchive:
		return ScratchTaskArchiveCapability(), true
	case CapabilityTodoAdd:
		return TodoAddCapability(), true
	case CapabilityTodoCreateWorktreeAndStartEngineer:
		return TodoCreateWorktreeAndStartEngineerCapability(), true
	case CapabilityTodoComplete:
		return TodoCompleteCapability(), true
	case CapabilitySettingsUpdate:
		return SettingsUpdateCapability(), true
	case CapabilityGitPrepareCommit:
		return GitPrepareCommitCapability(), true
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
			{
				ID:        ProviderLCAgent,
				Available: true,
				Reason:    "experimental",
				Features:  []string{FeatureSendPrompt, FeatureResume, FeatureForceNew},
			},
		},
	}
}

func AgentTaskCreateCapability() Capability {
	return Capability{
		Name:         CapabilityAgentTaskCreate,
		Description:  "Create a delegated agent task and optionally start an embedded engineer session in its workspace.",
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
		Description:  "Continue an existing delegated agent task, reusing its workspace and engineer session when possible.",
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
		Description:  "Mark a delegated agent task completed, waiting, or archived.",
		InputSchema:  agentTaskCloseInputSchema(),
		OutputSchema: agentTaskOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
	}
}

func ProjectArchiveCapability() Capability {
	return Capability{
		Name:         CapabilityProjectArchive,
		Description:  "Move an in-scope regular loaded project between the Active and Archived project-list tabs without touching its files or scope. Repository roots move their linked worktrees with them.",
		InputSchema:  projectArchiveInputSchema(),
		OutputSchema: projectArchiveOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMaySetProjectArchive},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureArchiveTask},
		}},
	}
}

func ScratchTaskArchiveCapability() Capability {
	return Capability{
		Name:         CapabilityScratchTaskArchive,
		Description:  "Archive a scratch task project so it leaves the active dashboard but remains recoverable on disk.",
		InputSchema:  scratchTaskArchiveInputSchema(),
		OutputSchema: scratchTaskArchiveOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureArchiveTask},
		}},
	}
}

func TodoAddCapability() Capability {
	return Capability{
		Name:         CapabilityTodoAdd,
		Description:  "Add a project TODO item to the selected or named loaded project backlog.",
		InputSchema:  todoAddInputSchema(),
		OutputSchema: todoAddOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayCreateProjectTodo},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureAddTodo},
		}},
	}
}

func TodoCreateWorktreeAndStartEngineerCapability() Capability {
	return Capability{
		Name:         CapabilityTodoCreateWorktreeAndStartEngineer,
		Description:  "Create a tracked project TODO, prepare a dedicated Git worktree, and start an engineer session there.",
		InputSchema:  todoCreateWorktreeAndStartEngineerInputSchema(),
		OutputSchema: todoCreateWorktreeAndStartEngineerOutputSchema(),
		Risk:         RiskExternal,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects: []string{
			HostEffectMayCreateProjectTodo,
			HostEffectMayCreateProjectWorktree,
			HostEffectMayRevealEngineerSession,
		},
		Providers: EngineerSendPromptCapability().Providers,
	}
}

func TodoCompleteCapability() Capability {
	return Capability{
		Name:         CapabilityTodoComplete,
		Description:  "Mark an existing project TODO item complete after user confirmation.",
		InputSchema:  todoCompleteInputSchema(),
		OutputSchema: todoCompleteOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayCompleteProjectTodo},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureCompleteTodo},
		}},
	}
}

func SettingsUpdateCapability() Capability {
	return Capability{
		Name:         CapabilitySettingsUpdate,
		Description:  "Apply confirmed Little Control Room settings changes without delegating through a project engineer session.",
		InputSchema:  settingsUpdateInputSchema(),
		OutputSchema: settingsUpdateOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayUpdateSettings},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeatureUpdateSettings},
		}},
	}
}

func GitPrepareCommitCapability() Capability {
	return Capability{
		Name:         CapabilityGitPrepareCommit,
		Description:  "Open the existing project commit preview flow for a loaded project. Preparing the preview never commits or pushes; the operator must confirm in the normal TUI commit dialog.",
		InputSchema:  gitPrepareCommitInputSchema(),
		OutputSchema: gitPrepareCommitOutputSchema(),
		Risk:         RiskWrite,
		Confirmation: ConfirmationRequired,
		RequiresHost: true,
		HostEffects:  []string{HostEffectMayPrepareGitCommit},
		Providers: []ProviderCapability{{
			ID:        ProviderAuto,
			Available: true,
			Features:  []string{FeaturePrepareCommit},
		}},
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
	input.TodoLabel = strings.TrimSpace(input.TodoLabel)
	input.TodoText = strings.TrimSpace(input.TodoText)
	if input.TodoID < 0 {
		return EngineerSendPromptInput{}, fmt.Errorf("todo_id cannot be negative")
	}
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

func NormalizeScratchTaskArchiveInput(input ScratchTaskArchiveInput) (ScratchTaskArchiveInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	if input.ProjectPath == "" && input.ProjectName == "" {
		return ScratchTaskArchiveInput{}, fmt.Errorf("project_path or project_name is required")
	}
	return input, nil
}

func NormalizeProjectArchiveInput(input ProjectArchiveInput) (ProjectArchiveInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.Resources = normalizeResourceRefs(input.Resources)
	rawAction := strings.TrimSpace(string(input.Action))
	input.Action = input.Action.Normalized()
	if input.ProjectPath == "" && input.ProjectName == "" && len(input.Resources) == 0 {
		return ProjectArchiveInput{}, fmt.Errorf("project_path, project_name, or project resources are required")
	}
	if input.Action == "" {
		return ProjectArchiveInput{}, fmt.Errorf("unsupported project archive action: %s", rawAction)
	}
	for _, resource := range input.Resources {
		if resource.Kind != ResourceProject {
			return ProjectArchiveInput{}, fmt.Errorf("project archive resources must use kind=%s", ResourceProject)
		}
		if resource.ProjectPath == "" && resource.Path == "" && resource.Label == "" {
			return ProjectArchiveInput{}, fmt.Errorf("project archive resource needs project_path, path, or label")
		}
	}
	return input, nil
}

func NormalizeTodoAddInput(input TodoAddInput) (TodoAddInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.Text = strings.TrimSpace(input.Text)
	if input.ProjectPath == "" && input.ProjectName == "" {
		return TodoAddInput{}, fmt.Errorf("project_path or project_name is required")
	}
	if input.Text == "" {
		return TodoAddInput{}, fmt.Errorf("todo text is required")
	}
	return input, nil
}

func NormalizeTodoCreateWorktreeAndStartEngineerInput(input TodoCreateWorktreeAndStartEngineerInput) (TodoCreateWorktreeAndStartEngineerInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.TodoText = strings.TrimSpace(input.TodoText)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.TodoLabel = strings.TrimSpace(input.TodoLabel)
	input.WorktreePath = strings.TrimSpace(input.WorktreePath)
	if input.WorktreePath != "" {
		input.WorktreePath = filepath.Clean(input.WorktreePath)
	}
	input.Provider = input.Provider.Normalized()
	if input.ProjectPath == "" && input.ProjectName == "" {
		return TodoCreateWorktreeAndStartEngineerInput{}, fmt.Errorf("project_path or project_name is required")
	}
	if input.TodoText == "" {
		input.TodoText = input.Prompt
	}
	if input.Prompt == "" {
		input.Prompt = input.TodoText
	}
	if input.TodoText == "" {
		return TodoCreateWorktreeAndStartEngineerInput{}, fmt.Errorf("todo text is required")
	}
	if input.Prompt == "" {
		return TodoCreateWorktreeAndStartEngineerInput{}, fmt.Errorf("engineer prompt is required")
	}
	if input.Provider == "" {
		return TodoCreateWorktreeAndStartEngineerInput{}, fmt.Errorf("unsupported engineer provider: %s", input.Provider)
	}
	if input.TodoID < 0 {
		return TodoCreateWorktreeAndStartEngineerInput{}, fmt.Errorf("todo_id cannot be negative")
	}
	return input, nil
}

func NormalizeTodoCompleteInput(input TodoCompleteInput) (TodoCompleteInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.TodoLabel = strings.TrimSpace(input.TodoLabel)
	input.TodoText = strings.TrimSpace(input.TodoText)
	input.Evidence = strings.TrimSpace(input.Evidence)
	if input.TodoID <= 0 {
		return TodoCompleteInput{}, fmt.Errorf("todo_id is required")
	}
	return input, nil
}

func NormalizeSettingsUpdateInput(input SettingsUpdateInput) (SettingsUpdateInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	out := SettingsUpdateInput{
		RequestID: input.RequestID,
		Changes:   make([]SettingsChange, 0, len(input.Changes)),
	}
	for i, change := range input.Changes {
		normalized, err := NormalizeSettingsChange(change)
		if err != nil {
			return SettingsUpdateInput{}, fmt.Errorf("change %d: %w", i+1, err)
		}
		out.Changes = append(out.Changes, normalized)
	}
	if len(out.Changes) == 0 {
		return SettingsUpdateInput{}, fmt.Errorf("at least one settings change is required")
	}
	return out, nil
}

func NormalizeGitPrepareCommitInput(input GitPrepareCommitInput) (GitPrepareCommitInput, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	if input.ProjectPath != "" {
		input.ProjectPath = filepath.Clean(input.ProjectPath)
	}
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.Message = strings.TrimSpace(input.Message)
	if strings.ContainsAny(input.Message, "\r\n") {
		return GitPrepareCommitInput{}, fmt.Errorf("commit message must be one line")
	}
	input.Message = strings.Join(strings.Fields(input.Message), " ")
	if input.ProjectPath == "" && input.ProjectName == "" {
		return GitPrepareCommitInput{}, fmt.Errorf("project_path or project_name is required")
	}
	return input, nil
}

func NormalizeSettingsChange(change SettingsChange) (SettingsChange, error) {
	rawField := strings.TrimSpace(string(change.Field))
	change.Field = change.Field.Normalized()
	if change.Field == "" {
		return SettingsChange{}, fmt.Errorf("unsupported settings field: %s", rawField)
	}
	rawOperation := strings.TrimSpace(string(change.Operation))
	change.Operation = change.Operation.Normalized()
	if change.Operation == "" {
		return SettingsChange{}, fmt.Errorf("unsupported settings operation: %s", rawOperation)
	}
	change.Value = strings.TrimSpace(change.Value)
	values, err := normalizeSettingsValues(change.Values)
	if err != nil {
		return SettingsChange{}, err
	}
	change.Values = values
	switch change.Operation {
	case SettingsUpdateAppendUnique, SettingsUpdateRemove:
		if len(change.Values) == 0 {
			return SettingsChange{}, fmt.Errorf("%s needs at least one value", change.Operation)
		}
	case SettingsUpdateSet:
		switch change.Field {
		case SettingsFieldIncludePaths, SettingsFieldExcludePaths, SettingsFieldExcludeProjectPatterns:
			values, err := normalizeSettingsValues(firstNonEmptySettingsValues(change.Values, change.Value))
			if err != nil {
				return SettingsChange{}, err
			}
			change.Values = values
		default:
			if change.Value == "" && !settingsFieldUsesBoolValue(change.Field) {
				return SettingsChange{}, fmt.Errorf("set needs a value for %s", change.Field)
			}
		}
	}
	return change, nil
}

func normalizeSettingsValues(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("settings values must be one line")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func firstNonEmptySettingsValues(values []string, value string) []string {
	if len(values) > 0 {
		return values
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func settingsFieldUsesBoolValue(field SettingsField) bool {
	switch field {
	case SettingsFieldPrivacyMode, SettingsFieldHideReasoningSections:
		return true
	default:
		return false
	}
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

func validateScratchTaskArchiveInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityScratchTaskArchive)
	}
	var input ScratchTaskArchiveInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityScratchTaskArchive, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityScratchTaskArchive)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeScratchTaskArchiveInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityScratchTaskArchive, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateProjectArchiveInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityProjectArchive)
	}
	var input ProjectArchiveInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityProjectArchive, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityProjectArchive)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeProjectArchiveInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityProjectArchive, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateTodoAddInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityTodoAdd)
	}
	var input TodoAddInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityTodoAdd, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityTodoAdd)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeTodoAddInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityTodoAdd, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateTodoCreateWorktreeAndStartEngineerInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityTodoCreateWorktreeAndStartEngineer)
	}
	var input TodoCreateWorktreeAndStartEngineerInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityTodoCreateWorktreeAndStartEngineer, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityTodoCreateWorktreeAndStartEngineer)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeTodoCreateWorktreeAndStartEngineerInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityTodoCreateWorktreeAndStartEngineer, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateTodoCompleteInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityTodoComplete)
	}
	var input TodoCompleteInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityTodoComplete, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityTodoComplete)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeTodoCompleteInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityTodoComplete, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateSettingsUpdateInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilitySettingsUpdate)
	}
	var input SettingsUpdateInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilitySettingsUpdate, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilitySettingsUpdate)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeSettingsUpdateInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilitySettingsUpdate, err)
	}
	inv.RequestID = normalized.RequestID
	inv.Args = payload
	return inv, nil
}

func validateGitPrepareCommitInvocation(inv Invocation) (Invocation, error) {
	if len(inv.Args) == 0 {
		return Invocation{}, fmt.Errorf("%s args are required", CapabilityGitPrepareCommit)
	}
	var input GitPrepareCommitInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityGitPrepareCommit, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityGitPrepareCommit)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	normalized, err := NormalizeGitPrepareCommitInput(input)
	if err != nil {
		return Invocation{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Invocation{}, fmt.Errorf("encode normalized %s args: %w", CapabilityGitPrepareCommit, err)
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

func resourceRefInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind":         map[string]any{"type": "string", "enum": ResourceKindStrings(false)},
			"id":           map[string]any{"type": "string"},
			"path":         map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"provider":     map[string]any{"type": "string", "enum": ProviderStrings(true)},
			"session_id":   map[string]any{"type": "string"},
			"todo_id":      map[string]any{"type": "integer"},
			"pid":          map[string]any{"type": "integer"},
			"port":         map[string]any{"type": "integer"},
			"label":        map[string]any{"type": "string"},
		},
		"required": []string{"kind", "id", "path", "project_path", "provider", "session_id", "todo_id", "pid", "port", "label"},
	}
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
				"enum": ProviderStrings(false),
			},
			"session_mode": map[string]any{
				"type": "string",
				"enum": SessionModeStrings(false),
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Prompt to send to the engineer session.",
			},
			"todo_id": map[string]any{
				"type":        "integer",
				"description": "Open project TODO id this engineer handoff is meant to address, or 0 when not tied to a TODO.",
			},
			"todo_text": map[string]any{
				"type":        "string",
				"description": "Text of the tracked TODO for display and engineer context, or empty.",
			},
			"todo_label": map[string]any{
				"type":        "string",
				"description": "Short display label for the tracked TODO, or empty.",
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
				"enum": EngineerProviderStrings(false),
			},
			"project_path": map[string]any{"type": "string"},
			"session_id":   map[string]any{"type": "string"},
			"reused":       map[string]any{"type": "boolean"},
			"prompt_sent":  map[string]any{"type": "boolean"},
			"revealed":     map[string]any{"type": "boolean"},
			"status":       map[string]any{"type": "string"},
			"todo_id":      map[string]any{"type": "integer"},
		},
		"required": []string{"provider", "project_path", "session_id", "reused", "prompt_sent", "revealed", "status", "todo_id"},
	}
}

func agentTaskCreateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":     map[string]any{"type": "string"},
			"title":          map[string]any{"type": "string"},
			"kind":           map[string]any{"type": "string", "enum": AgentTaskKindStrings(false)},
			"parent_task_id": map[string]any{"type": "string"},
			"prompt":         map[string]any{"type": "string"},
			"provider": map[string]any{
				"type": "string",
				"enum": ProviderStrings(false),
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
				"enum": ProviderStrings(false),
			},
			"session_mode": map[string]any{
				"type": "string",
				"enum": SessionModeStrings(false),
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
			"status":        map[string]any{"type": "string", "enum": AgentTaskCloseStatusStrings(false)},
			"summary":       map[string]any{"type": "string"},
			"close_session": map[string]any{"type": "boolean"},
		},
		"required": []string{"task_id", "status", "summary", "close_session"},
	}
}

func scratchTaskArchiveInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":   map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "project_name"},
	}
}

func scratchTaskArchiveOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path":  map[string]any{"type": "string"},
			"archived_path": map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "archived_path", "status"},
	}
}

func projectArchiveInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":   map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"action": map[string]any{
				"type": "string",
				"enum": ProjectArchiveActionStrings(false),
			},
			"resources": map[string]any{
				"type":        "array",
				"items":       resourceRefInputSchema(),
				"description": "Optional batch targets. Each resource must have kind=project and project_path, path, or label.",
			},
		},
		"required": []string{"project_path", "project_name", "action", "resources"},
	}
}

func projectArchiveOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path": map[string]any{"type": "string"},
			"action":       map[string]any{"type": "string", "enum": ProjectArchiveActionStrings(false)},
			"archived":     map[string]any{"type": "boolean"},
			"status":       map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "action", "archived", "status"},
	}
}

func todoAddInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":   map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"text": map[string]any{
				"type":        "string",
				"description": "The exact TODO text to add to the project backlog.",
			},
		},
		"required": []string{"project_path", "project_name", "text"},
	}
}

func todoAddOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path": map[string]any{"type": "string"},
			"todo_id":      map[string]any{"type": "integer"},
			"text":         map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "todo_id", "text", "status"},
	}
}

func todoCreateWorktreeAndStartEngineerInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":    map[string]any{"type": "string"},
			"project_path":  map[string]any{"type": "string"},
			"project_name":  map[string]any{"type": "string"},
			"todo_text":     map[string]any{"type": "string"},
			"prompt":        map[string]any{"type": "string"},
			"provider":      map[string]any{"type": "string", "enum": ProviderStrings(false)},
			"reveal":        map[string]any{"type": "boolean"},
			"todo_id":       map[string]any{"type": "integer"},
			"todo_label":    map[string]any{"type": "string"},
			"worktree_path": map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "project_name", "todo_text", "prompt", "provider", "reveal"},
	}
}

func todoCreateWorktreeAndStartEngineerOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path":  map[string]any{"type": "string"},
			"todo_id":       map[string]any{"type": "integer"},
			"worktree_path": map[string]any{"type": "string"},
			"provider":      map[string]any{"type": "string", "enum": ProviderStrings(false)},
			"session_id":    map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "todo_id", "worktree_path", "provider", "session_id", "status"},
	}
}

func todoCompleteInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":   map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"todo_id": map[string]any{
				"type":        "integer",
				"description": "The existing project TODO id to mark complete.",
			},
			"todo_text": map[string]any{
				"type":        "string",
				"description": "Known TODO text for confirmation display, or empty if unknown.",
			},
			"todo_label": map[string]any{
				"type":        "string",
				"description": "Short display label for the TODO, or empty if unknown.",
			},
			"evidence": map[string]any{
				"type":        "string",
				"description": "Brief evidence that the TODO is satisfied, used only for the confirmation preview.",
			},
		},
		"required": []string{"todo_id"},
	}
}

func todoCompleteOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path": map[string]any{"type": "string"},
			"todo_id":      map[string]any{"type": "integer"},
			"completed":    map[string]any{"type": "boolean"},
			"status":       map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "todo_id", "completed", "status"},
	}
}

func settingsUpdateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string"},
			"changes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"field": map[string]any{
							"type":        "string",
							"enum":        SettingsFieldStrings(false),
							"description": "User-facing setting to update.",
						},
						"operation": map[string]any{
							"type":        "string",
							"enum":        SettingsUpdateOperationStrings(false),
							"description": "set replaces a value; append_unique adds missing list values; remove removes list values.",
						},
						"value": map[string]any{
							"type":        "string",
							"description": "Scalar value for set operations; empty for list operations.",
						},
						"values": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "List values for list settings.",
						},
						"bool_value": map[string]any{
							"type":        "boolean",
							"description": "Boolean value for boolean set operations.",
						},
					},
					"required": []string{"field", "operation", "value", "values", "bool_value"},
				},
				"description": "One or more settings changes to apply together after confirmation.",
			},
		},
		"required": []string{"changes"},
	}
}

func settingsUpdateOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"changed":     map[string]any{"type": "boolean"},
			"config_path": map[string]any{"type": "string"},
			"status":      map[string]any{"type": "string"},
		},
		"required": []string{"changed", "config_path", "status"},
	}
}

func gitPrepareCommitInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id":   map[string]any{"type": "string"},
			"project_path": map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"message": map[string]any{
				"type":        "string",
				"description": "Optional one-line commit message seed for the normal commit preview dialog; empty lets LCR generate one.",
			},
			"push_after_commit": map[string]any{
				"type":        "boolean",
				"description": "True when the user asked to commit and push. This prepares the finish/commit-and-push preview only; applying it still requires the normal TUI confirmation.",
			},
		},
		"required": []string{"project_path", "project_name", "message", "push_after_commit"},
	}
}

func gitPrepareCommitOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_path":      map[string]any{"type": "string"},
			"push_after_commit": map[string]any{"type": "boolean"},
			"preview_opened":    map[string]any{"type": "boolean"},
			"operator_confirms": map[string]any{"type": "boolean"},
			"status":            map[string]any{"type": "string"},
		},
		"required": []string{"project_path", "push_after_commit", "preview_opened", "operator_confirms", "status"},
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
				"kind":         map[string]any{"type": "string", "enum": ResourceKindStrings(false)},
				"id":           map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"project_path": map[string]any{"type": "string"},
				"provider":     map[string]any{"type": "string", "enum": ProviderStrings(true)},
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
