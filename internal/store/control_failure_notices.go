package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// A caller that ended its turn waiting for success still needs the failure.
// Persist the receipt in the same transaction as the terminal operation so a
// restart cannot lose it. The existing exact-session outbox handles delivery;
// this is a report-only receipt, never a retry or a success continuation.
func recordControlFailureNotice(ctx context.Context, tx *sql.Tx, operation control.Operation, problem string, now time.Time) error {
	if !control.IsExternalOperationID(operation.ID) || !operation.ResumeOnSuccess || operation.Capability == control.CapabilityEngineerSendPrompt {
		return nil // Engineer messages already have their own delivery receipts.
	}
	prompt := fmt.Sprintf("LCR operation %s (%s) failed.\nError: %s\nYou ended your turn waiting for this operation. Automatic success continuation will not run. Report this failure; do not retry or continue later write or external-action steps from the same workflow through other tools. Wait for new user instructions. This failure receipt grants no additional permissions. Do not send an acknowledgment-only reply.", operation.ID, operation.Capability, problem)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engineer_dispatches(
			origin_operation_id, worker_project_path, worker_provider, worker_session_id,
			caller_project_path, caller_provider, caller_session_key, caller_session_id,
			reply_state, reply_seq, reply_prompt, created_at, updated_at
		)
		SELECT ?, o.project_path, o.provider, '', o.project_path, o.provider, o.session_key,
			COALESCE(NULLIF(c.session_id, ''), b.provider_session_id, ''), ?, 1, ?, ?, ?
		FROM control_operations o
		LEFT JOIN control_continuations c ON c.operation_id = o.id
		LEFT JOIN engineer_session_bindings b ON b.project_path = o.project_path
			AND b.provider = o.provider AND b.control_session_key = o.session_key
		WHERE o.id = ? AND o.session_key <> '' AND o.project_path <> ''
			AND o.provider IN ('codex', 'opencode', 'claude_code', 'lcagent')
		ON CONFLICT(origin_operation_id) DO NOTHING
	`, "control-operation-notice:"+operation.ID+":failed", string(model.EngineerDispatchReplyReady), prompt, now.Unix(), now.Unix(), operation.ID)
	return err
}
