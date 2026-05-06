package bossrun

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
)

const (
	GoalKindAgentTaskCleanup = "agent_task_cleanup"

	GoalStatusWaitingForApproval = "waiting_for_approval"
	GoalStatusRunning            = "running"
	GoalStatusCompleted          = "completed"
	GoalStatusFailed             = "failed"

	PlanStepObserve  PlanStepKind = "observe"
	PlanStepClassify PlanStepKind = "classify"
	PlanStepSelect   PlanStepKind = "select"
	PlanStepPropose  PlanStepKind = "propose"
	PlanStepAct      PlanStepKind = "act"
	PlanStepDelegate PlanStepKind = "delegate"
	PlanStepAwait    PlanStepKind = "await"
	PlanStepVerify   PlanStepKind = "verify"
	PlanStepBranch   PlanStepKind = "branch"
	PlanStepReport   PlanStepKind = "report"
)

type PlanStepKind string

type GoalRun struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Objective       string    `json:"objective"`
	SuccessCriteria string    `json:"success_criteria"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
}

type Plan struct {
	Version int        `json:"version"`
	Steps   []PlanStep `json:"steps"`
}

type PlanStep struct {
	ID         string                 `json:"id"`
	Kind       PlanStepKind           `json:"kind"`
	Title      string                 `json:"title"`
	Capability control.CapabilityName `json:"capability"`
	Resources  []control.ResourceRef  `json:"resources"`
	Evidence   string                 `json:"evidence"`
	Confidence float64                `json:"confidence"`
}

type AuthorityGrant struct {
	Summary              string                   `json:"summary"`
	AllowedCapabilities  []control.CapabilityName `json:"allowed_capabilities"`
	Resources            []control.ResourceRef    `json:"resources"`
	ForbiddenSideEffects []string                 `json:"forbidden_side_effects"`
	MaxRisk              control.RiskLevel        `json:"max_risk"`
}

type GoalProposal struct {
	Run              GoalRun               `json:"run"`
	Plan             Plan                  `json:"plan"`
	Authority        AuthorityGrant        `json:"authority"`
	Preview          string                `json:"preview"`
	ArchiveResources []control.ResourceRef `json:"archive_resources"`
	KeepResources    []control.ResourceRef `json:"keep_resources"`
	ReviewResources  []control.ResourceRef `json:"review_resources"`
}

type TaskFailure struct {
	TaskID string `json:"task_id"`
	Error  string `json:"error"`
}

type TraceEntry struct {
	StepID     string                 `json:"step_id"`
	Capability control.CapabilityName `json:"capability"`
	ResourceID string                 `json:"resource_id"`
	Status     string                 `json:"status"`
	Summary    string                 `json:"summary"`
	At         time.Time              `json:"at,omitempty"`
}

type GoalResult struct {
	RunID           string        `json:"run_id"`
	Kind            string        `json:"kind"`
	Summary         string        `json:"summary"`
	ArchivedTaskIDs []string      `json:"archived_task_ids"`
	KeptTaskIDs     []string      `json:"kept_task_ids"`
	ReviewTaskIDs   []string      `json:"review_task_ids"`
	FailedTasks     []TaskFailure `json:"failed_tasks"`
	Verified        bool          `json:"verified"`
	Trace           []TraceEntry  `json:"trace"`
}

type GoalRecord struct {
	Proposal    GoalProposal `json:"proposal"`
	Result      GoalResult   `json:"result"`
	Error       string       `json:"error"`
	Trace       []TraceEntry `json:"trace"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	CompletedAt time.Time    `json:"completed_at,omitempty"`
}

func NormalizeGoalProposal(proposal GoalProposal) (GoalProposal, error) {
	proposal.Run.ID = strings.TrimSpace(proposal.Run.ID)
	proposal.Run.Kind = strings.TrimSpace(strings.ToLower(proposal.Run.Kind))
	proposal.Run.Title = strings.TrimSpace(proposal.Run.Title)
	proposal.Run.Objective = strings.TrimSpace(proposal.Run.Objective)
	proposal.Run.SuccessCriteria = strings.TrimSpace(proposal.Run.SuccessCriteria)
	proposal.Run.Status = strings.TrimSpace(proposal.Run.Status)
	if proposal.Run.Kind == "" {
		proposal.Run.Kind = GoalKindAgentTaskCleanup
	}
	if proposal.Run.Kind != GoalKindAgentTaskCleanup {
		return GoalProposal{}, fmt.Errorf("unsupported goal kind: %s", proposal.Run.Kind)
	}
	if proposal.Run.Title == "" {
		proposal.Run.Title = "Clear stale delegated agent tasks"
	}
	if proposal.Run.Objective == "" {
		proposal.Run.Objective = "Archive delegated agent task records that have served their scope."
	}
	if proposal.Run.SuccessCriteria == "" {
		proposal.Run.SuccessCriteria = "Selected delegated agent tasks are no longer active or waiting after execution."
	}
	if proposal.Run.Status == "" {
		proposal.Run.Status = GoalStatusWaitingForApproval
	}

	proposal.ArchiveResources = normalizeResources(proposal.ArchiveResources)
	proposal.KeepResources = normalizeResources(proposal.KeepResources)
	proposal.ReviewResources = normalizeResources(proposal.ReviewResources)
	proposal.Authority.Resources = normalizeResources(proposal.Authority.Resources)
	if len(proposal.ArchiveResources) == 0 {
		proposal.ArchiveResources = append([]control.ResourceRef(nil), proposal.Authority.Resources...)
	}
	if len(proposal.Authority.Resources) == 0 {
		proposal.Authority.Resources = append([]control.ResourceRef(nil), proposal.ArchiveResources...)
	}
	proposal.Authority.Resources = uniqueResources(proposal.Authority.Resources)
	proposal.ArchiveResources = uniqueResources(proposal.ArchiveResources)
	if len(AgentTaskResourceIDs(proposal.Authority.Resources)) == 0 {
		return GoalProposal{}, errors.New("agent-task cleanup goal needs at least one agent_task resource")
	}

	proposal.Authority.Summary = strings.TrimSpace(proposal.Authority.Summary)
	if proposal.Authority.Summary == "" {
		proposal.Authority.Summary = proposal.Run.Title
	}
	proposal.Authority.AllowedCapabilities = normalizeCapabilities(proposal.Authority.AllowedCapabilities)
	if len(proposal.Authority.AllowedCapabilities) == 0 {
		proposal.Authority.AllowedCapabilities = []control.CapabilityName{control.CapabilityAgentTaskClose}
	}
	if !capabilityAllowed(proposal.Authority.AllowedCapabilities, control.CapabilityAgentTaskClose) {
		return GoalProposal{}, errors.New("agent-task cleanup goal authority must allow agent_task.close")
	}
	proposal.Authority.ForbiddenSideEffects = normalizeStrings(proposal.Authority.ForbiddenSideEffects)
	if len(proposal.Authority.ForbiddenSideEffects) == 0 {
		proposal.Authority.ForbiddenSideEffects = []string{"close live engineer sessions", "delete files or workspaces"}
	}
	switch proposal.Authority.MaxRisk {
	case "", control.RiskWrite:
		proposal.Authority.MaxRisk = control.RiskWrite
	case control.RiskRead:
		return GoalProposal{}, errors.New("agent-task cleanup goal authority must allow write risk")
	case control.RiskExternal, control.RiskDestructive:
	default:
		return GoalProposal{}, fmt.Errorf("unsupported goal risk level: %s", proposal.Authority.MaxRisk)
	}

	proposal.Plan = normalizePlan(proposal.Plan, proposal.Authority.Resources)
	proposal.Preview = strings.TrimSpace(proposal.Preview)
	if proposal.Preview == "" {
		proposal.Preview = FormatGoalProposalPreview(proposal)
	}
	return CloneGoalProposal(proposal), nil
}

func CloneGoalProposal(proposal GoalProposal) GoalProposal {
	proposal.Plan.Steps = clonePlanSteps(proposal.Plan.Steps)
	proposal.Authority.AllowedCapabilities = append([]control.CapabilityName(nil), proposal.Authority.AllowedCapabilities...)
	proposal.Authority.Resources = append([]control.ResourceRef(nil), proposal.Authority.Resources...)
	proposal.Authority.ForbiddenSideEffects = append([]string(nil), proposal.Authority.ForbiddenSideEffects...)
	proposal.ArchiveResources = append([]control.ResourceRef(nil), proposal.ArchiveResources...)
	proposal.KeepResources = append([]control.ResourceRef(nil), proposal.KeepResources...)
	proposal.ReviewResources = append([]control.ResourceRef(nil), proposal.ReviewResources...)
	return proposal
}

func AgentTaskResourceIDs(resources []control.ResourceRef) []string {
	ids := make([]string, 0, len(resources))
	seen := map[string]struct{}{}
	for _, resource := range resources {
		if resource.Kind != control.ResourceAgentTask {
			continue
		}
		id := strings.TrimSpace(resource.ID)
		if id == "" {
			id = strings.TrimSpace(resource.Label)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func FormatGoalProposalPreview(proposal GoalProposal) string {
	ids := AgentTaskResourceIDs(proposal.Authority.Resources)
	lines := []string{fmt.Sprintf("Archive %d delegated agent task record%s?", len(ids), pluralSuffix(len(ids)))}
	for _, resource := range proposal.Authority.Resources {
		if resource.Kind != control.ResourceAgentTask {
			continue
		}
		label := strings.TrimSpace(resource.Label)
		id := strings.TrimSpace(resource.ID)
		switch {
		case label != "" && id != "":
			lines = append(lines, fmt.Sprintf("- %s (%s)", label, id))
		case id != "":
			lines = append(lines, "- "+id)
		case label != "":
			lines = append(lines, "- "+label)
		}
	}
	if len(proposal.KeepResources) > 0 {
		lines = append(lines, "", "Keep out of this run:")
		lines = appendResourceLines(lines, proposal.KeepResources)
	}
	if len(proposal.ReviewResources) > 0 {
		lines = append(lines, "", "Needs review instead of automatic archive:")
		lines = appendResourceLines(lines, proposal.ReviewResources)
	}
	lines = append(lines, "", "Allowed action: agent_task.close with archived status.")
	if len(proposal.Authority.ForbiddenSideEffects) > 0 {
		lines = append(lines, "Forbidden side effects: "+strings.Join(proposal.Authority.ForbiddenSideEffects, "; ")+".")
	}
	lines = append(lines, "Verification: refresh agent-task state and confirm the selected records left the active set.")
	lines = append(lines, "", "Enter confirms; Esc cancels.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func FormatGoalResult(result GoalResult) string {
	if result.Summary != "" {
		return strings.TrimSpace(result.Summary)
	}
	archived := len(result.ArchivedTaskIDs)
	failed := len(result.FailedTasks)
	switch {
	case failed == 0 && result.Verified:
		return fmt.Sprintf("Archived %d delegated agent task record%s and verified the selected tasks are out of the active set.", archived, pluralSuffix(archived))
	case failed == 0:
		return fmt.Sprintf("Archived %d delegated agent task record%s, but verification did not confirm every selected task left the active set.", archived, pluralSuffix(archived))
	case archived == 0:
		return fmt.Sprintf("The goal run could not archive the selected delegated agent task records; %d task%s failed.", failed, pluralSuffix(failed))
	default:
		return fmt.Sprintf("Archived %d delegated agent task record%s; %d task%s still need review.", archived, pluralSuffix(archived), failed, pluralSuffix(failed))
	}
}

func normalizePlan(plan Plan, resources []control.ResourceRef) Plan {
	if plan.Version <= 0 {
		plan.Version = 1
	}
	if len(plan.Steps) == 0 {
		plan.Steps = []PlanStep{
			{ID: "select-agent-tasks", Kind: PlanStepSelect, Title: "Select the delegated agent task records to archive", Resources: resources, Confidence: 1},
			{ID: "archive-agent-tasks", Kind: PlanStepAct, Title: "Archive the selected delegated agent task records", Capability: control.CapabilityAgentTaskClose, Resources: resources, Confidence: 1},
			{ID: "verify-active-set", Kind: PlanStepVerify, Title: "Confirm selected task records are no longer active or waiting", Resources: resources, Confidence: 1},
			{ID: "report-result", Kind: PlanStepReport, Title: "Report archived records, failures, and verification", Resources: resources, Confidence: 1},
		}
		return plan
	}
	plan.Steps = clonePlanSteps(plan.Steps)
	for i := range plan.Steps {
		step := &plan.Steps[i]
		step.ID = strings.TrimSpace(step.ID)
		step.Kind = PlanStepKind(strings.TrimSpace(strings.ToLower(string(step.Kind))))
		step.Title = strings.TrimSpace(step.Title)
		step.Capability = control.CapabilityName(strings.TrimSpace(string(step.Capability)))
		step.Resources = normalizeResources(step.Resources)
		step.Evidence = strings.TrimSpace(step.Evidence)
		if step.ID == "" {
			step.ID = fmt.Sprintf("step-%d", i+1)
		}
		if step.Kind == "" {
			step.Kind = PlanStepAct
		}
		if step.Title == "" {
			step.Title = string(step.Kind)
		}
	}
	return plan
}

func normalizeResources(resources []control.ResourceRef) []control.ResourceRef {
	out := make([]control.ResourceRef, 0, len(resources))
	for _, resource := range resources {
		resource.Kind = control.ResourceKind(strings.TrimSpace(string(resource.Kind)))
		resource.ID = strings.TrimSpace(resource.ID)
		resource.Path = strings.TrimSpace(resource.Path)
		resource.ProjectPath = strings.TrimSpace(resource.ProjectPath)
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

func uniqueResources(resources []control.ResourceRef) []control.ResourceRef {
	out := make([]control.ResourceRef, 0, len(resources))
	seen := map[string]struct{}{}
	for _, resource := range resources {
		key := string(resource.Kind) + ":" + strings.TrimSpace(resource.ID) + ":" + strings.TrimSpace(resource.Path) + ":" + strings.TrimSpace(resource.ProjectPath)
		if key == ":::" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, resource)
	}
	return out
}

func normalizeCapabilities(capabilities []control.CapabilityName) []control.CapabilityName {
	out := make([]control.CapabilityName, 0, len(capabilities))
	seen := map[control.CapabilityName]struct{}{}
	for _, capability := range capabilities {
		capability = control.CapabilityName(strings.TrimSpace(string(capability)))
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

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func capabilityAllowed(capabilities []control.CapabilityName, want control.CapabilityName) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func clonePlanSteps(steps []PlanStep) []PlanStep {
	out := append([]PlanStep(nil), steps...)
	for i := range out {
		out[i].Resources = append([]control.ResourceRef(nil), out[i].Resources...)
	}
	return out
}

func appendResourceLines(lines []string, resources []control.ResourceRef) []string {
	for _, resource := range resources {
		label := strings.TrimSpace(resource.Label)
		id := strings.TrimSpace(resource.ID)
		if label != "" && id != "" {
			lines = append(lines, fmt.Sprintf("- %s (%s)", label, id))
		} else if id != "" {
			lines = append(lines, "- "+id)
		} else if label != "" {
			lines = append(lines, "- "+label)
		}
	}
	return lines
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
