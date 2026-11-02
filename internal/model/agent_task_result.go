package model

import (
	"strings"
	"time"
)

// Worker evidence is a claim. Host checkout evidence stays in Repository.
type AgentTaskCheck struct {
	Command  string `json:"command"`
	CWD      string `json:"cwd"`
	Outcome  string `json:"outcome"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}
type AgentTaskCriterion struct {
	Criterion string `json:"criterion"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
}
type AgentTaskResultPayload struct {
	Outcome      string               `json:"outcome"`
	Summary      string               `json:"summary"`
	Criteria     []AgentTaskCriterion `json:"criteria,omitempty"`
	Checks       []AgentTaskCheck     `json:"checks,omitempty"`
	BaseRevision string               `json:"base_revision,omitempty"`
	ChangedFiles []string             `json:"changed_files,omitempty"`
	Risks        []string             `json:"risks,omitempty"`
	Questions    []string             `json:"questions,omitempty"`
}
type AgentTaskResult struct {
	AgentTaskResultPayload
	TaskID      string    `json:"task_id"`
	Revision    int64     `json:"revision"`
	SubmittedAt time.Time `json:"submitted_at"`
}
type AgentTaskReview struct {
	Revision   int64            `json:"revision"`
	Decision   string           `json:"decision"`
	Summary    string           `json:"summary"`
	Checks     []AgentTaskCheck `json:"checks,omitempty"`
	Reviewer   string           `json:"reviewer"`
	ReviewedAt time.Time        `json:"reviewed_at"`
}
type AgentTaskWorkflow struct {
	Enabled       bool             `json:"enabled,omitempty"`
	CallerStopped bool             `json:"caller_stopped,omitempty"`
	RunID         int64            `json:"run_id,omitempty"`
	WorkerKey     string           `json:"worker_key,omitempty"`
	Phase         string           `json:"phase,omitempty"`
	Handoff       bool             `json:"handoff,omitempty"`
	Result        *AgentTaskResult `json:"result,omitempty"`
	Review        *AgentTaskReview `json:"review,omitempty"`
	// Supervision is the operator's bounded correction grant, agreed once when
	// the task is confirmed. It survives new runs; it is never widened by a
	// worker, a caller or a callback prompt.
	MaxCorrections     int  `json:"max_corrections,omitempty"`
	CorrectionsUsed    int  `json:"corrections_used,omitempty"`
	SupervisionRevoked bool `json:"supervision_revoked,omitempty"`
}

// CorrectionsRemaining reports the unused part of the grant. A revoked or
// exhausted grant returns zero, which returns corrections to ordinary
// confirmation rather than blocking them.
func (w AgentTaskWorkflow) CorrectionsRemaining() int {
	if !w.Enabled || w.SupervisionRevoked || w.MaxCorrections <= w.CorrectionsUsed {
		return 0
	}
	return w.MaxCorrections - w.CorrectionsUsed
}

// These fields must come from host-bound tool context, never from model input.
type AgentTaskActor struct {
	ProjectPath string
	Provider    SessionSource
	SessionKey  string
	Operator    bool
}

// AgentTaskDeliveryWaitingPrefix marks a result_delivery_error that explains why
// a still-pending callback has not reached its caller yet (recipient busy,
// unavailable, waiting for idle), as opposed to a terminal delivery failure.
const AgentTaskDeliveryWaitingPrefix = "waiting: "

// ResultDeliveryState names where the caller callback stands without making
// readers infer it from several nullable lifecycle fields.
func (t AgentTask) ResultDeliveryState() string {
	deliveryError := strings.TrimSpace(t.ResultDeliveryError)
	switch {
	case !t.ResultConsumedAt.IsZero():
		return "consumed"
	case strings.HasPrefix(deliveryError, AgentTaskDeliveryWaitingPrefix):
		return "waiting"
	case deliveryError != "":
		return "failed"
	case !t.ResultDeliveredAt.IsZero():
		return "delivered"
	case strings.TrimSpace(t.ResultMessageID) != "":
		return "queued"
	case !t.ResultReadyAt.IsZero() || t.Workflow.Phase == "unclassified":
		return "no_callback"
	default:
		return "pending"
	}
}

// ReviewState reports whether the task is waiting on its caller: "awaiting_review"
// for a submitted result nobody has reviewed, "unclassified" for a worker that
// ended without any structured result, "" otherwise.
func (t AgentTask) ReviewState() string {
	w := t.Workflow
	if !w.Enabled {
		if !t.ResultReadyAt.IsZero() && t.ResultConsumedAt.IsZero() && t.Status != AgentTaskStatusArchived {
			return "awaiting_review"
		}
		return ""
	}
	if !w.Handoff || w.Review != nil || w.CallerStopped {
		return ""
	}
	switch w.Phase {
	case "awaiting_review", "blocked", "failed":
		if w.Result != nil {
			return "awaiting_review"
		}
	case "unclassified":
		return "unclassified"
	}
	return ""
}
