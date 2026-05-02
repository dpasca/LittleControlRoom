package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

func (s *Store) CreateAgentTask(ctx context.Context, input model.CreateAgentTaskInput) (model.AgentTask, error) {
	normalized, err := normalizeCreateAgentTaskInput(input)
	if err != nil {
		return model.AgentTask{}, err
	}
	if normalized.ID == "" {
		normalized.ID, err = newAgentTaskID()
		if err != nil {
			return model.AgentTask{}, err
		}
	}

	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AgentTask{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_tasks(
			id, parent_task_id, title, kind, status, summary, capabilities, provider, session_id, workspace_path,
			expires_at, created_at, last_touched_at, completed_at, archived_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, normalized.ID, normalized.ParentTaskID, normalized.Title, string(normalized.Kind), string(normalized.Status), normalized.Summary,
		encodeAgentTaskCapabilities(normalized.Capabilities), string(normalized.Provider), normalized.SessionID, normalized.WorkspacePath, nullableTimeUnixValue(normalized.ExpiresAt),
		now.Unix(), now.Unix(), nullableTimeUnixValue(time.Time{}), nullableTimeUnixValue(time.Time{}), now.Unix())
	if err != nil {
		return model.AgentTask{}, fmt.Errorf("create agent task: %w", err)
	}
	if err := insertAgentTaskResources(ctx, tx, normalized.ID, normalized.Resources, now); err != nil {
		return model.AgentTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AgentTask{}, err
	}
	return s.GetAgentTask(ctx, normalized.ID)
}

func (s *Store) GetAgentTask(ctx context.Context, id string) (model.AgentTask, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.AgentTask{}, errors.New("agent task id is required")
	}
	task, err := scanAgentTask(s.db.QueryRowContext(ctx, `
		SELECT
			id, parent_task_id, title, kind, status, summary, capabilities, provider, session_id, workspace_path,
			expires_at, created_at, last_touched_at, completed_at, archived_at, updated_at
		FROM agent_tasks
		WHERE id = ?
	`, id))
	if err != nil {
		return model.AgentTask{}, err
	}
	resources, err := s.listAgentTaskResources(ctx, []string{id})
	if err != nil {
		return model.AgentTask{}, err
	}
	task.Resources = resources[id]
	return task, nil
}

func (s *Store) ListAgentTasks(ctx context.Context, filter model.AgentTaskFilter) ([]model.AgentTask, error) {
	where := []string{}
	args := []any{}
	if kind := model.NormalizeAgentTaskKind(filter.Kind); filter.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(kind))
	}
	if len(filter.Statuses) > 0 {
		parts := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			parts = append(parts, "?")
			args = append(args, string(model.NormalizeAgentTaskStatus(status)))
		}
		where = append(where, "status IN ("+strings.Join(parts, ", ")+")")
	} else if !filter.IncludeArchived {
		where = append(where, "status != ?")
		args = append(args, string(model.AgentTaskStatusArchived))
	}

	query := `
		SELECT
			id, parent_task_id, title, kind, status, summary, capabilities, provider, session_id, workspace_path,
			expires_at, created_at, last_touched_at, completed_at, archived_at, updated_at
		FROM agent_tasks
	`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY last_touched_at DESC, created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent tasks: %w", err)
	}
	defer rows.Close()

	tasks := []model.AgentTask{}
	taskIDs := []string{}
	for rows.Next() {
		task, err := scanAgentTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent task: %w", err)
		}
		tasks = append(tasks, task)
		taskIDs = append(taskIDs, task.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agent tasks: %w", err)
	}
	resources, err := s.listAgentTaskResources(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tasks[i].Resources = resources[tasks[i].ID]
	}
	return tasks, nil
}

func (s *Store) ListExpiredAgentTasks(ctx context.Context, now time.Time) ([]model.AgentTask, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, parent_task_id, title, kind, status, summary, capabilities, provider, session_id, workspace_path,
			expires_at, created_at, last_touched_at, completed_at, archived_at, updated_at
		FROM agent_tasks
		WHERE status = ? AND expires_at IS NOT NULL AND expires_at <= ?
		ORDER BY expires_at ASC
	`, string(model.AgentTaskStatusArchived), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list expired agent tasks: %w", err)
	}
	defer rows.Close()

	tasks := []model.AgentTask{}
	taskIDs := []string{}
	for rows.Next() {
		task, err := scanAgentTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expired agent task: %w", err)
		}
		tasks = append(tasks, task)
		taskIDs = append(taskIDs, task.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read expired agent tasks: %w", err)
	}
	resources, err := s.listAgentTaskResources(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tasks[i].Resources = resources[tasks[i].ID]
	}
	return tasks, nil
}

func (s *Store) DeleteAgentTask(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("agent task id is required")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_tasks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete agent task: %w", err)
	}
	return nil
}

func (s *Store) UpdateAgentTask(ctx context.Context, input model.UpdateAgentTaskInput) (model.AgentTask, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return model.AgentTask{}, errors.New("agent task id is required")
	}

	now := time.Now()
	set := []string{}
	args := []any{}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return model.AgentTask{}, errors.New("agent task title is required")
		}
		set = append(set, "title = ?")
		args = append(args, title)
	}
	if input.ParentTaskID != nil {
		set = append(set, "parent_task_id = ?")
		args = append(args, strings.TrimSpace(*input.ParentTaskID))
	}
	if input.Status != nil {
		status := model.NormalizeAgentTaskStatus(*input.Status)
		set = append(set, "status = ?")
		args = append(args, string(status))
		switch status {
		case model.AgentTaskStatusCompleted:
			if input.CompletedAt == nil {
				set = append(set, "completed_at = COALESCE(completed_at, ?)")
				args = append(args, now.Unix())
			}
		case model.AgentTaskStatusArchived:
			if input.ArchivedAt == nil {
				set = append(set, "archived_at = COALESCE(archived_at, ?)")
				args = append(args, now.Unix())
			}
		}
	}
	if input.Summary != nil {
		set = append(set, "summary = ?")
		args = append(args, strings.TrimSpace(*input.Summary))
	}
	if input.ReplaceCapabilities {
		set = append(set, "capabilities = ?")
		args = append(args, encodeAgentTaskCapabilities(input.Capabilities))
	}
	if input.Provider != nil {
		set = append(set, "provider = ?")
		args = append(args, string(model.NormalizeSessionSource(*input.Provider)))
	}
	if input.SessionID != nil {
		set = append(set, "session_id = ?")
		args = append(args, strings.TrimSpace(*input.SessionID))
	}
	if input.WorkspacePath != nil {
		set = append(set, "workspace_path = ?")
		args = append(args, cleanOptionalPath(*input.WorkspacePath))
	}
	if input.ExpiresAt != nil {
		set = append(set, "expires_at = ?")
		args = append(args, nullableTimeUnixValue(*input.ExpiresAt))
	}
	if input.CompletedAt != nil {
		set = append(set, "completed_at = ?")
		args = append(args, nullableTimeUnixValue(*input.CompletedAt))
	}
	if input.ArchivedAt != nil {
		set = append(set, "archived_at = ?")
		args = append(args, nullableTimeUnixValue(*input.ArchivedAt))
	}
	if input.Touch {
		set = append(set, "last_touched_at = ?")
		args = append(args, now.Unix())
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AgentTask{}, err
	}
	defer tx.Rollback()

	if len(set) > 0 {
		set = append(set, "updated_at = ?")
		args = append(args, now.Unix(), id)
		if _, err := tx.ExecContext(ctx, "UPDATE agent_tasks SET "+strings.Join(set, ", ")+" WHERE id = ?", args...); err != nil {
			return model.AgentTask{}, fmt.Errorf("update agent task: %w", err)
		}
	}
	if input.ReplaceResources {
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_task_resources WHERE task_id = ?`, id); err != nil {
			return model.AgentTask{}, fmt.Errorf("replace agent task resources: %w", err)
		}
		if err := insertAgentTaskResources(ctx, tx, id, input.Resources, now); err != nil {
			return model.AgentTask{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.AgentTask{}, err
	}
	return s.GetAgentTask(ctx, id)
}

func normalizeCreateAgentTaskInput(input model.CreateAgentTaskInput) (model.CreateAgentTaskInput, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.ParentTaskID = strings.TrimSpace(input.ParentTaskID)
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return model.CreateAgentTaskInput{}, errors.New("agent task title is required")
	}
	input.Kind = model.NormalizeAgentTaskKind(input.Kind)
	input.Status = model.NormalizeAgentTaskStatus(input.Status)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Capabilities = normalizeAgentTaskCapabilities(input.Capabilities)
	input.Provider = model.NormalizeSessionSource(input.Provider)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.WorkspacePath = cleanOptionalPath(input.WorkspacePath)
	return input, nil
}

func insertAgentTaskResources(ctx context.Context, tx *sql.Tx, taskID string, resources []model.AgentTaskResource, now time.Time) error {
	for _, resource := range resources {
		resource = normalizeAgentTaskResource(taskID, resource)
		if resource.Kind == "" {
			return errors.New("agent task resource kind is required")
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_task_resources(
				task_id, kind, ref_id, project_path, path, pid, port, provider, session_id, label, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, taskID, string(resource.Kind), resource.RefID, resource.ProjectPath, resource.Path, resource.PID, resource.Port,
			string(resource.Provider), resource.SessionID, resource.Label, now.Unix())
		if err != nil {
			return fmt.Errorf("insert agent task resource: %w", err)
		}
	}
	return nil
}

func normalizeAgentTaskResource(taskID string, resource model.AgentTaskResource) model.AgentTaskResource {
	resource.TaskID = strings.TrimSpace(taskID)
	resource.Kind = model.NormalizeAgentTaskResourceKind(resource.Kind)
	resource.RefID = strings.TrimSpace(resource.RefID)
	resource.ProjectPath = cleanOptionalPath(resource.ProjectPath)
	resource.Path = cleanOptionalPath(resource.Path)
	resource.Provider = model.NormalizeSessionSource(resource.Provider)
	resource.SessionID = strings.TrimSpace(resource.SessionID)
	resource.Label = strings.TrimSpace(resource.Label)
	return resource
}

func scanAgentTask(scanner interface {
	Scan(dest ...any) error
}) (model.AgentTask, error) {
	var (
		task          model.AgentTask
		kind          string
		status        string
		capabilities  string
		provider      string
		expiresAt     sql.NullInt64
		createdAt     int64
		lastTouchedAt int64
		completedAt   sql.NullInt64
		archivedAt    sql.NullInt64
		updatedAt     int64
	)
	if err := scanner.Scan(
		&task.ID,
		&task.ParentTaskID,
		&task.Title,
		&kind,
		&status,
		&task.Summary,
		&capabilities,
		&provider,
		&task.SessionID,
		&task.WorkspacePath,
		&expiresAt,
		&createdAt,
		&lastTouchedAt,
		&completedAt,
		&archivedAt,
		&updatedAt,
	); err != nil {
		return model.AgentTask{}, err
	}
	task.Kind = model.NormalizeAgentTaskKind(model.AgentTaskKind(kind))
	task.Status = model.NormalizeAgentTaskStatus(model.AgentTaskStatus(status))
	task.Capabilities = decodeAgentTaskCapabilities(capabilities)
	task.Provider = model.NormalizeSessionSource(model.SessionSource(provider))
	if expiresAt.Valid {
		task.ExpiresAt = time.Unix(expiresAt.Int64, 0)
	}
	task.CreatedAt = time.Unix(createdAt, 0)
	task.LastTouchedAt = time.Unix(lastTouchedAt, 0)
	if completedAt.Valid {
		task.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	if archivedAt.Valid {
		task.ArchivedAt = time.Unix(archivedAt.Int64, 0)
	}
	task.UpdatedAt = time.Unix(updatedAt, 0)
	return task, nil
}

func (s *Store) listAgentTaskResources(ctx context.Context, taskIDs []string) (map[string][]model.AgentTaskResource, error) {
	out := make(map[string][]model.AgentTaskResource, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(taskIDs))
	args := make([]any, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, taskID)
		out[taskID] = nil
	}
	if len(args) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, kind, ref_id, project_path, path, pid, port, provider, session_id, label, created_at
		FROM agent_task_resources
		WHERE task_id IN (`+strings.Join(placeholders, ", ")+`)
		ORDER BY task_id ASC, id ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent task resources: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		resource, err := scanAgentTaskResource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent task resource: %w", err)
		}
		out[resource.TaskID] = append(out[resource.TaskID], resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agent task resources: %w", err)
	}
	return out, nil
}

func scanAgentTaskResource(scanner interface {
	Scan(dest ...any) error
}) (model.AgentTaskResource, error) {
	var (
		resource  model.AgentTaskResource
		kind      string
		provider  string
		createdAt int64
	)
	if err := scanner.Scan(
		&resource.ID,
		&resource.TaskID,
		&kind,
		&resource.RefID,
		&resource.ProjectPath,
		&resource.Path,
		&resource.PID,
		&resource.Port,
		&provider,
		&resource.SessionID,
		&resource.Label,
		&createdAt,
	); err != nil {
		return model.AgentTaskResource{}, err
	}
	resource.Kind = model.NormalizeAgentTaskResourceKind(model.AgentTaskResourceKind(kind))
	resource.Provider = model.NormalizeSessionSource(model.SessionSource(provider))
	resource.CreatedAt = time.Unix(createdAt, 0)
	return resource, nil
}

func normalizeAgentTaskCapabilities(capabilities []string) []string {
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

func encodeAgentTaskCapabilities(capabilities []string) string {
	encoded, err := json.Marshal(normalizeAgentTaskCapabilities(capabilities))
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeAgentTaskCapabilities(raw string) []string {
	var capabilities []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &capabilities); err != nil {
		return nil
	}
	return normalizeAgentTaskCapabilities(capabilities)
}

func cleanOptionalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func newAgentTaskID() (string, error) {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate agent task id: %w", err)
	}
	return "agt_" + time.Now().UTC().Format("20060102T150405") + "_" + hex.EncodeToString(random), nil
}
