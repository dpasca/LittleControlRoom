package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// The dispatch reply outbox also carries delivery notices. Its unique origin
// prevents duplicate notices across polls and restarts, and its caller binding
// prevents a replacement conversation from receiving someone else's report.
func recordEngineerMessageNotice(ctx context.Context, tx *sql.Tx, message control.EngineerMessage, kind, status string, now time.Time) error {
	if !control.IsExternalOperationID(message.OperationID) {
		return nil // Never generate notices about callback messages.
	}
	prompt := fmt.Sprintf("LCR engineer message %s (operation %s) delivery notice.\nDestination: %s (%s).\n%s", message.ID, message.OperationID, message.ProjectPath, message.Provider, status)
	if !message.ExpiresAt.IsZero() {
		prompt += "\nDelivery deadline: " + message.ExpiresAt.Format(time.RFC3339)
	}
	if kind == "no-current-session" {
		prompt += fmt.Sprintf("\nNo engineer session is open. You can offer to start one: propose engineer.send_prompt for project_path %s with provider %s, session_mode new and an explicit startup prompt, without follow_current_session. The original message stays queued until a session opens or its deadline expires; do not resend it.", message.ProjectPath, message.Provider)
	}
	prompt += "\nThis is a delivery status notice, not a worker completion. Continue independent authorized work; do not send an acknowledgment-only reply."
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engineer_dispatches(
			origin_operation_id, worker_project_path, worker_provider, worker_session_id,
			caller_project_path, caller_provider, caller_session_key, caller_session_id,
			reply_state, reply_seq, reply_prompt, created_at, updated_at
		)
		SELECT ?, ?, ?, ?, o.project_path, o.provider, o.session_key,
			COALESCE(b.provider_session_id, ''), ?, 1, ?, ?, ?
		FROM control_operations o
		LEFT JOIN engineer_session_bindings b ON b.project_path = o.project_path
			AND b.provider = o.provider AND b.control_session_key = o.session_key
		WHERE o.id = ? AND o.capability = ? AND o.session_key <> '' AND o.project_path <> ''
			AND o.provider IN ('codex', 'opencode', 'claude_code', 'lcagent')
		ON CONFLICT(origin_operation_id) DO NOTHING
	`, "engineer-message-notice:"+message.ID+":"+kind, message.ProjectPath, string(message.Provider), message.TargetSessionID,
		string(model.EngineerDispatchReplyReady), prompt, now.Unix(), now.Unix(), message.OperationID, string(control.CapabilityEngineerSendPrompt))
	return err
}

// ListReadyEngineerDispatches returns routable outbox entries. Unknown caller
// identities cannot starve reports whose exact return address is available.
func (s *Store) ListReadyEngineerDispatches(ctx context.Context, limit int) ([]model.EngineerDispatch, error) {
	rows, err := s.db.QueryContext(ctx, engineerDispatchSelect+`
		WHERE reply_state = ? AND (caller_session_id <> '' OR EXISTS (
			SELECT 1 FROM engineer_session_bindings b
			WHERE b.project_path = engineer_dispatches.caller_project_path
				AND b.provider = engineer_dispatches.caller_provider
				AND b.control_session_key = engineer_dispatches.caller_session_key))
		ORDER BY updated_at, id LIMIT ?`, string(model.EngineerDispatchReplyReady), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EngineerDispatch
	for rows.Next() {
		dispatch, err := scanEngineerDispatch(rows)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(dispatch.ReplyPrompt) != "" {
			out = append(out, dispatch)
		}
	}
	return out, rows.Err()
}
