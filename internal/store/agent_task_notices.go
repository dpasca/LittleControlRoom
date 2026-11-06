package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

// TakePendingAgentTaskNotices claims compact notices once per durable callback.
// This does not acknowledge, consume or deliver the full callback.
func (s *Store) TakePendingAgentTaskNotices(ctx context.Context, path string, provider model.SessionSource, key string) ([]map[string]any, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	sessionID, err := s.ResolveEngineerSession(ctx, path, provider, key)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, t.id, t.title, t.result_ready_at, t.workflow_json
 FROM engineer_messages m JOIN agent_tasks t ON t.id = m.agent_task_id AND t.result_message_id = m.id
 WHERE m.state = 'queued' AND m.project_path = ? AND m.provider = ?
 AND ((m.target_session_id <> '' AND m.target_session_id = ?) OR
 (m.target_session_id = '' AND t.origin_session_key = ?))
 AND NOT EXISTS (SELECT 1 FROM engineer_session_successions ss WHERE ss.project_path = m.project_path AND ss.provider = m.provider AND ss.session_id = m.target_session_id)
 AND NOT EXISTS (SELECT 1 FROM agent_task_result_notices n WHERE n.message_id = m.id)
 ORDER BY t.result_ready_at, m.id LIMIT 10`, filepath.Clean(path), string(provider), sessionID, key)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		message, id, title, workflow string
		ready                        int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.message, &c.id, &c.title, &c.ready, &c.workflow); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var notices []map[string]any
	for _, c := range candidates {
		result, err := s.db.ExecContext(ctx, `INSERT INTO agent_task_result_notices(message_id, noticed_at)
 SELECT m.id, ? FROM engineer_messages m JOIN agent_tasks t ON t.result_message_id = m.id
 WHERE m.id = ? AND m.state = 'queued'
 AND NOT EXISTS (SELECT 1 FROM engineer_session_successions ss WHERE ss.project_path = m.project_path AND ss.provider = m.provider AND ss.session_id = m.target_session_id)
 ON CONFLICT DO NOTHING`, time.Now().Unix(), c.message)
		if err != nil {
			return nil, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		workflow := decodeAgentTaskNoticeWorkflow(c.workflow)
		outcome := "unclassified"
		if workflow.Result != nil {
			outcome = string(workflow.Result.Outcome)
		}
		notices = append(notices, map[string]any{"message_id": c.message, "task_id": c.id, "title": c.title, "outcome": outcome, "ready_at": time.Unix(c.ready, 0).UTC().Format(time.RFC3339), "result_revision": workflow.RunID, "review_instruction": "Inspect work.agent_task_get for this task and result_revision. Independently verify the work; if a structured result exists, record agent_task.review_result (accept or changes_requested). Full callback remains queued for idle delivery. Continue authorized work; do not send an acknowledgment-only reply."})
	}
	return notices, nil
}

func decodeAgentTaskNoticeWorkflow(raw string) model.AgentTaskWorkflow {
	var workflow model.AgentTaskWorkflow
	_ = json.Unmarshal([]byte(raw), &workflow)
	return workflow
}
