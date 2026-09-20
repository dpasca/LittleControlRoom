package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"lcroom/internal/model"
)

type AgentTaskSubmitResultInput struct {
	RequestID string `json:"request_id,omitempty"`
	TaskID    string `json:"task_id"`
	RunID     int64  `json:"run_id"`
	model.AgentTaskResultPayload
}
type AgentTaskReviewResultInput struct {
	RequestID string                 `json:"request_id,omitempty"`
	TaskID    string                 `json:"task_id"`
	Revision  int64                  `json:"revision"`
	Decision  string                 `json:"decision"`
	Summary   string                 `json:"summary"`
	Checks    []model.AgentTaskCheck `json:"checks,omitempty"`
}

func validateAgentTaskResultInvocation(inv Invocation) (Invocation, error) {
	if inv.Capability == CapabilityAgentTaskSubmitResult {
		var input AgentTaskSubmitResultInput
		if err := decodeStrictJSON(inv.Args, &input); err != nil {
			return Invocation{}, err
		}
		if input.TaskID == "" || input.RunID < 1 {
			return Invocation{}, fmt.Errorf("task_id and positive run_id are required")
		}
		if err := ValidateTaskResult(input.AgentTaskResultPayload); err != nil {
			return Invocation{}, err
		}
		input.RequestID = inv.RequestID
		inv.Args, _ = json.Marshal(input)
	} else {
		var input AgentTaskReviewResultInput
		if err := decodeStrictJSON(inv.Args, &input); err != nil {
			return Invocation{}, err
		}
		if input.TaskID == "" || input.Revision < 1 {
			return Invocation{}, fmt.Errorf("task_id and positive revision are required")
		}
		if input.Decision != "accept" && input.Decision != "changes_requested" {
			return Invocation{}, fmt.Errorf("decision must be accept or changes_requested")
		}
		if strings.TrimSpace(input.Summary) == "" || len(input.Summary) > 2000 {
			return Invocation{}, fmt.Errorf("review summary must contain 1–2000 bytes")
		}
		if err := validateTaskChecks(input.Checks); err != nil {
			return Invocation{}, err
		}
		input.RequestID = inv.RequestID
		inv.Args, _ = json.Marshal(input)
	}
	return inv, nil
}

func ValidateTaskResult(result model.AgentTaskResultPayload) error {
	if result.Outcome != "ready_for_review" && result.Outcome != "blocked" && result.Outcome != "failed" {
		return fmt.Errorf("unsupported result outcome")
	}
	if strings.TrimSpace(result.Summary) == "" || len(result.Summary) > 2000 {
		return fmt.Errorf("result summary must contain 1–2000 bytes")
	}
	if len(result.Criteria) > 20 || len(result.ChangedFiles) > 100 || len(result.Risks) > 20 || len(result.Questions) > 20 || len(result.BaseRevision) > 200 {
		return fmt.Errorf("result exceeds evidence limits")
	}
	for _, criterion := range result.Criteria {
		if criterion.Criterion == "" || len(criterion.Criterion) > 500 || len(criterion.Detail) > 1000 {
			return fmt.Errorf("invalid criterion evidence length")
		}
		if criterion.Outcome != "met" && criterion.Outcome != "unmet" && criterion.Outcome != "unknown" {
			return fmt.Errorf("criterion outcome must be met, unmet or unknown")
		}
	}
	for _, values := range [][]string{result.ChangedFiles, result.Risks, result.Questions} {
		for _, value := range values {
			if len(value) > 1000 {
				return fmt.Errorf("result evidence item exceeds 1000 bytes")
			}
		}
	}
	return validateTaskChecks(result.Checks)
}
func validateTaskChecks(checks []model.AgentTaskCheck) error {
	if len(checks) > 20 {
		return fmt.Errorf("at most 20 checks are allowed")
	}
	for _, check := range checks {
		if check.Command == "" || check.CWD == "" || len(check.Command) > 1000 || len(check.CWD) > 1000 || len(check.Evidence) > 2000 {
			return fmt.Errorf("check requires bounded command, cwd and evidence")
		}
		switch check.Outcome {
		case "passed":
			if check.ExitCode == nil || *check.ExitCode != 0 {
				return fmt.Errorf("passed check requires exit_code 0")
			}
		case "failed":
			if check.ExitCode == nil || *check.ExitCode == 0 {
				return fmt.Errorf("failed check requires a nonzero exit_code")
			}
		case "not_run":
			if check.ExitCode != nil {
				return fmt.Errorf("not_run check cannot have an exit_code")
			}
		default:
			return fmt.Errorf("check outcome must be passed, failed or not_run")
		}
	}
	return nil
}
func taskResultCheckSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"command": map[string]any{"type": "string", "maxLength": 1000}, "cwd": map[string]any{"type": "string", "maxLength": 1000}, "outcome": map[string]any{"type": "string", "enum": []string{"passed", "failed", "not_run"}}, "exit_code": map[string]any{"type": "integer"}, "evidence": map[string]any{"type": "string", "maxLength": 2000}}, "required": []string{"command", "cwd", "outcome"}}}
}
func AgentTaskResultCapability(review bool) Capability {
	name, description := CapabilityAgentTaskSubmitResult, "Submit bounded worker claims for the current structured task run. Only the host-bound worker can submit. No turn is started; end your turn after submission so the host can hand off to the caller."
	properties := map[string]any{"request_id": map[string]any{"type": "string"}, "task_id": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string", "maxLength": 2000}, "checks": taskResultCheckSchema()}
	required := []string{"task_id", "run_id", "outcome", "summary"}
	if review {
		name, description = CapabilityAgentTaskReviewResult, "Record acceptance or requested changes for the exact current result revision. Only the originating caller or explicit operator review can act. Does not launch corrections, commit, or delete anything."
		properties["revision"] = map[string]any{"type": "integer", "minimum": 1}
		properties["decision"] = map[string]any{"type": "string", "enum": []string{"accept", "changes_requested"}}
		required = []string{"task_id", "revision", "decision", "summary"}
	} else {
		properties["run_id"] = map[string]any{"type": "integer", "minimum": 1}
		properties["outcome"] = map[string]any{"type": "string", "enum": []string{"ready_for_review", "blocked", "failed"}}
		properties["base_revision"] = map[string]any{"type": "string", "maxLength": 200}
		for _, field := range []string{"changed_files", "risks", "questions"} {
			limit := 20
			if field == "changed_files" {
				limit = 100
			}
			properties[field] = map[string]any{"type": "array", "maxItems": limit, "items": map[string]any{"type": "string", "maxLength": 1000}}
		}
		properties["criteria"] = map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"criterion": map[string]any{"type": "string", "maxLength": 500}, "outcome": map[string]any{"type": "string", "enum": []string{"met", "unmet", "unknown"}}, "detail": map[string]any{"type": "string", "maxLength": 1000}}, "required": []string{"criterion", "outcome"}}}
	}
	return Capability{Name: name, Description: description, Domain: CapabilityDomainTask, Scope: AuthorityScopeProject, Confirmation: ConfirmationNone, Risk: RiskWrite, InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}, OutputSchema: map[string]any{"type": "object"}}
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
