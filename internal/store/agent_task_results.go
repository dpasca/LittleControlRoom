package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func encodeTaskWorkflow(value model.AgentTaskWorkflow) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Store) workflowTransaction(ctx context.Context, task model.AgentTask, next model.AgentTaskWorkflow, status model.AgentTaskStatus, extra func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET workflow_json=?,status=?,updated_at=? WHERE id=? AND workflow_json=?`, encodeTaskWorkflow(next), string(status), time.Now().Unix(), task.ID, encodeTaskWorkflow(task.Workflow))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("task run changed; reload the task before retrying")
	}
	if extra != nil {
		if err := extra(tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) BeginStructuredTaskRun(ctx context.Context, task model.AgentTask, key string, active bool) error {
	if !task.Workflow.Enabled {
		return nil
	}
	if key == "" {
		return errors.New("structured task requires a host control identity")
	}
	if active {
		if task.Workflow.WorkerKey != key || task.Workflow.Phase != "working" {
			return errors.New("finish the current task turn before beginning another result run")
		}
		return nil
	}
	// A new run resets result metadata but never the operator's correction grant.
	next := model.AgentTaskWorkflow{Enabled: true, RunID: task.Workflow.RunID + 1, WorkerKey: key, Phase: "working",
		MaxCorrections: task.Workflow.MaxCorrections, CorrectionsUsed: task.Workflow.CorrectionsUsed, SupervisionRevoked: task.Workflow.SupervisionRevoked}
	return s.workflowTransaction(ctx, task, next, model.AgentTaskStatusActive, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE engineer_messages SET state='failed',last_error='Superseded by a new task run' WHERE agent_task_id=? AND state IN ('queued','delivering')`, task.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET result_ready_at=NULL,result_message_id='',result_delivered_at=NULL,result_delivery_error='',result_consumed_at=NULL,result_consumed_by='',completed_at=NULL WHERE id=?`, task.ID)
		return err
	})
}

func (s *Store) SubmitAgentTaskResult(ctx context.Context, actor model.AgentTaskActor, input control.AgentTaskSubmitResultInput) (model.AgentTask, error) {
	if err := control.ValidateTaskResult(input.AgentTaskResultPayload); err != nil {
		return model.AgentTask{}, err
	}
	task, err := s.GetAgentTask(ctx, input.TaskID)
	if err != nil {
		return task, err
	}
	workflow := task.Workflow
	if !workflow.Enabled || input.RunID < 1 || workflow.RunID != input.RunID {
		return task, errors.New("result targets a stale or non-structured task run")
	}
	if actor.Operator || actor.SessionKey == "" || filepath.Clean(actor.ProjectPath) != filepath.Clean(task.WorkspacePath) || actor.Provider != task.Provider {
		return task, errors.New("only the exact worker may submit this result")
	}
	keyMatches := actor.SessionKey == workflow.WorkerKey
	// LCAgent native controls use its logical thread; that identity arrives after
	// the fresh launch key and is bound by the host's session record.
	if actor.Provider == model.SessionSourceLCAgent && task.SessionID != "" && actor.SessionKey == task.SessionID {
		keyMatches = true
	}
	if !keyMatches {
		return task, errors.New("result belongs to another worker session")
	}
	if workflow.Result != nil {
		if reflect.DeepEqual(workflow.Result.AgentTaskResultPayload, input.AgentTaskResultPayload) {
			return task, nil
		}
		return task, errors.New("this run already has an immutable result; continue into a new run for corrections")
	}
	if workflow.Phase != "working" && workflow.Phase != "unclassified" && workflow.Phase != "canceled" {
		return task, errors.New("task is not accepting a result")
	}
	result := model.AgentTaskResult{AgentTaskResultPayload: input.AgentTaskResultPayload, TaskID: task.ID, Revision: workflow.RunID, SubmittedAt: time.Now().UTC()}
	workflow.Result = &result
	if workflow.Phase != "canceled" {
		workflow.Phase = "submitted"
	}
	err = s.workflowTransaction(ctx, task, workflow, task.Status, func(tx *sql.Tx) error {
		raw, _ := json.Marshal(result)
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_task_results(task_id,revision,result_json) VALUES(?,?,?)`, task.ID, result.Revision, string(raw))
		return err
	})
	if err != nil {
		return task, err
	}
	return s.GetAgentTask(ctx, task.ID)
}

func (s *Store) FinalizeStructuredTask(ctx context.Context, task model.AgentTask) (model.AgentTask, error) {
	workflow := task.Workflow
	if !workflow.Enabled {
		return task, errors.New("structured workflow required")
	}
	if workflow.Phase != "working" && workflow.Phase != "submitted" && workflow.Phase != "canceled" {
		return task, nil
	}
	workflow.Handoff = true
	summary := "Worker stopped without a structured result; inspect its transcript."
	var readyAt any
	if workflow.Phase != "canceled" {
		if workflow.Result == nil {
			workflow.Phase = "unclassified"
		} else {
			workflow.Phase = workflow.Result.Outcome
			if workflow.Phase == "ready_for_review" {
				workflow.Phase = "awaiting_review"
			}
			summary = workflow.Result.Summary
			readyAt = time.Now().Unix()
		}
	}
	err := s.workflowTransaction(ctx, task, workflow, model.AgentTaskStatusWaiting, func(tx *sql.Tx) error {
		if workflow.Result != nil {
			evidence, err := json.Marshal(task.Repository)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_task_result_handoffs(task_id,revision,repository_json) VALUES(?,?,?)`, task.ID, workflow.RunID, string(evidence)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET summary=?,result_ready_at=? WHERE id=?`, summary, readyAt, task.ID)
		return err
	})
	if err != nil {
		return task, err
	}
	return s.GetAgentTask(ctx, task.ID)
}

func (s *Store) taskCallerAuthorized(ctx context.Context, task model.AgentTask, actor model.AgentTaskActor) bool {
	if actor.Operator {
		return true
	}
	if actor.SessionKey == "" || actor.Provider != task.OriginProvider || filepath.Clean(actor.ProjectPath) != filepath.Clean(firstNonEmptyString(task.OriginWorktreePath, task.OriginProjectPath)) {
		return false
	}
	if task.OriginSessionKey != "" && actor.SessionKey == task.OriginSessionKey {
		return true
	}
	if task.OriginSessionID == "" {
		return false
	}
	if actor.Provider == model.SessionSourceLCAgent && actor.SessionKey == task.OriginSessionID {
		return true
	}
	bound, err := s.ResolveEngineerSession(ctx, actor.ProjectPath, actor.Provider, actor.SessionKey)
	return err == nil && bound != "" && bound == task.OriginSessionID
}

func (s *Store) ReviewAgentTaskResult(ctx context.Context, actor model.AgentTaskActor, input control.AgentTaskReviewResultInput) (model.AgentTask, error) {
	raw, _ := json.Marshal(input)
	if _, err := control.ValidateInvocation(control.Invocation{Capability: control.CapabilityAgentTaskReviewResult, Args: raw}); err != nil {
		return model.AgentTask{}, err
	}
	task, err := s.GetAgentTask(ctx, input.TaskID)
	if err != nil {
		return task, err
	}
	workflow := task.Workflow
	if !workflow.Enabled || workflow.Result == nil || workflow.RunID != input.Revision {
		return task, errors.New("review targets a stale or missing result revision")
	}
	if !s.taskCallerAuthorized(ctx, task, actor) {
		return task, errors.New("only the originating caller or operator may review this result")
	}
	if workflow.Review != nil {
		old := workflow.Review
		if old.Revision == input.Revision && old.Decision == input.Decision && old.Summary == input.Summary && reflect.DeepEqual(old.Checks, input.Checks) {
			return task, nil
		}
		return task, errors.New("result revision already reviewed")
	}
	if !workflow.Handoff || task.Repository.State == "held" || workflow.Phase == "canceled" || workflow.Phase == "submitted" {
		return task, errors.New("worker handoff is not complete; review cannot proceed")
	}
	if input.Decision == "accept" && workflow.Phase != "awaiting_review" {
		return task, errors.New("only a ready_for_review result can be accepted")
	}
	reviewer := actor.SessionKey
	if actor.Operator {
		reviewer = "operator"
	}
	workflow.Review = &model.AgentTaskReview{Revision: input.Revision, Decision: input.Decision, Summary: input.Summary, Checks: input.Checks, Reviewer: reviewer, ReviewedAt: time.Now().UTC()}
	workflow.Phase = "changes_requested"
	status := model.AgentTaskStatusWaiting
	if input.Decision == "accept" {
		workflow.Phase = "completed"
		status = model.AgentTaskStatusCompleted
	}
	err = s.workflowTransaction(ctx, task, workflow, status, func(tx *sql.Tx) error {
		// Ownership can be reacquired between the initial read and this CAS.
		// Check it in the same write transaction as acceptance.
		var repositoryState string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(repository_json,'$.state'),'') FROM agent_tasks WHERE id=?`, task.ID).Scan(&repositoryState); err != nil {
			return err
		}
		if repositoryState == "held" {
			return errors.New("repository ownership changed; reload before reviewing")
		}
		reviewJSON, _ := json.Marshal(workflow.Review)
		if _, err := tx.ExecContext(ctx, `UPDATE agent_task_results SET review_json=? WHERE task_id=? AND revision=?`, string(reviewJSON), task.ID, input.Revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE engineer_messages SET state='failed',last_error='Result already reviewed' WHERE agent_task_id=? AND state IN ('queued','delivering')`, task.ID); err != nil {
			return err
		}
		var completed any
		if input.Decision == "accept" {
			completed = time.Now().Unix()
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET result_consumed_at=?,result_consumed_by=?,completed_at=? WHERE id=?`, time.Now().Unix(), reviewer, completed, task.ID)
		return err
	})
	if err != nil {
		return task, err
	}
	return s.GetAgentTask(ctx, task.ID)
}

func (s *Store) GetAgentTaskResult(ctx context.Context, taskID string, revision int64) (map[string]any, error) {
	var resultJSON, reviewJSON, repositoryJSON string
	err := s.db.QueryRowContext(ctx, `SELECT r.result_json,r.review_json,COALESCE(h.repository_json,'null') FROM agent_task_results r LEFT JOIN agent_task_result_handoffs h ON h.task_id=r.task_id AND h.revision=r.revision WHERE r.task_id=? AND r.revision=?`, taskID, revision).Scan(&resultJSON, &reviewJSON, &repositoryJSON)
	if err != nil {
		return nil, err
	}
	var result model.AgentTaskResult
	var review model.AgentTaskReview
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(reviewJSON), &review); err != nil {
		return nil, err
	}
	var repository *model.AgentTaskRepository
	if err := json.Unmarshal([]byte(repositoryJSON), &repository); err != nil {
		return nil, err
	}
	return map[string]any{"worker_claims": result, "caller_review": review, "host_repository_evidence": repository}, nil
}

func (s *Store) StopStructuredTask(ctx context.Context, task model.AgentTask, caller bool) error {
	if !task.Workflow.Enabled {
		return nil
	}
	if task.Workflow.Review != nil {
		// The run is already settled, so there is no result lifecycle left to
		// stop. An explicit stop must still end automatic corrections.
		if task.Workflow.CorrectionsRemaining() < 1 {
			return nil
		}
		next := task.Workflow
		next.SupervisionRevoked = true
		return s.workflowTransaction(ctx, task, next, task.Status, nil)
	}
	next := task.Workflow
	next.SupervisionRevoked = true
	if caller {
		next.CallerStopped = true
	} else {
		next.Phase = "canceled"
	}
	return s.workflowTransaction(ctx, task, next, task.Status, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE engineer_messages SET state='failed',last_error='Engineer explicitly stopped; automatic review suppressed' WHERE agent_task_id=? AND state IN ('queued','delivering')`, task.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET result_delivery_error=? WHERE id=?`, fmt.Sprintf("%s stopped; result retained without automatic review", map[bool]string{true: "Caller", false: "Worker"}[caller]), task.ID)
		return err
	})
}

func (s *Store) ValidateTaskReviewDelivery(ctx context.Context, message control.EngineerMessage) error {
	if message.AgentTaskRevision == 0 {
		return nil
	}
	task, err := s.GetAgentTask(ctx, message.AgentTaskID)
	if err != nil {
		return err
	}
	workflow := task.Workflow
	if !workflow.Enabled || workflow.RunID != message.AgentTaskRevision || workflow.Result == nil || !workflow.Handoff || workflow.Review != nil || workflow.CallerStopped || workflow.Phase == "canceled" {
		return errors.New("task result delivery was canceled or superseded")
	}
	if workflow.Phase != "awaiting_review" && workflow.Phase != "blocked" && workflow.Phase != "failed" {
		return errors.New("task result is no longer ready for caller review")
	}
	current, err := s.GetEngineerMessage(ctx, message.ID)
	if err != nil {
		return err
	}
	if current.State != control.EngineerMessageDelivering && current.State != control.EngineerMessageQueued {
		return errors.New("task result message is no longer pending")
	}
	return nil
}
