package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lcroom/internal/model"
	"strings"
	"time"
)

func (s *Store) SetSnooze(ctx context.Context, path string, until *time.Time) error {
	var v any
	if until != nil {
		v = until.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET snoozed_until = ?, updated_at = ? WHERE path = ?`, v, time.Now().Unix(), path)
	return err
}

func (s *Store) AddTodo(ctx context.Context, projectPath, text string) (model.TodoItem, error) {
	return s.AddTodoWithAttachments(ctx, projectPath, text, nil)
}

func (s *Store) AddTodoWithAttachments(ctx context.Context, projectPath, text string, attachments []model.TodoAttachment) (model.TodoItem, error) {
	if projectPath == "" {
		return model.TodoItem{}, fmt.Errorf("project path is required")
	}
	if strings.TrimSpace(text) == "" {
		return model.TodoItem{}, fmt.Errorf("todo text is required")
	}
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.TodoItem{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE project_todos
		SET position = position + 1
		WHERE project_path = ? AND done = 0
	`, projectPath); err != nil {
		return model.TodoItem{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO project_todos(project_path, text, done, position, created_at, updated_at)
		VALUES (?, ?, 0, 0, ?, ?)
	`, projectPath, text, now.Unix(), now.Unix())
	if err != nil {
		return model.TodoItem{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.TodoItem{}, err
	}
	if err := replaceTodoAttachmentsTx(ctx, tx, id, attachments, now); err != nil {
		return model.TodoItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE path = ?`, now.Unix(), projectPath); err != nil {
		return model.TodoItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.TodoItem{}, err
	}
	return model.TodoItem{
		ID:          id,
		ProjectPath: projectPath,
		Text:        text,
		Attachments: normalizeTodoAttachmentsForSave(attachments),
		Position:    0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Store) QueueTodoWorktreeSuggestion(ctx context.Context, todoID int64) (bool, error) {
	if todoID <= 0 {
		return false, fmt.Errorf("todo id is required")
	}
	todo, err := s.GetTodo(ctx, todoID)
	if err != nil {
		return false, err
	}
	if todo.Done {
		return false, nil
	}

	now := time.Now()
	textHash := hashTodoText(todo.Text)
	existing, err := s.GetTodoWorktreeSuggestion(ctx, todoID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && existing.TodoTextHash == textHash {
		switch existing.Status {
		case model.TodoWorktreeSuggestionQueued,
			model.TodoWorktreeSuggestionRunning,
			model.TodoWorktreeSuggestionReady,
			model.TodoWorktreeSuggestionFailed:
			return false, nil
		}
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO todo_worktree_suggestions(
			todo_id, status, todo_text_hash, branch_name, worktree_suffix, kind, reason,
			confidence, model, last_error, updated_at
		)
		VALUES(?, ?, ?, '', '', '', '', 0, '', '', ?)
		ON CONFLICT(todo_id) DO UPDATE SET
			status = excluded.status,
			todo_text_hash = excluded.todo_text_hash,
			branch_name = '',
			worktree_suffix = '',
			kind = '',
			reason = '',
			confidence = 0,
			model = '',
			last_error = '',
			updated_at = excluded.updated_at
	`, todoID, string(model.TodoWorktreeSuggestionQueued), textHash, now.Unix())
	return err == nil, err
}

func (s *Store) ForceQueueTodoWorktreeSuggestion(ctx context.Context, todoID int64) (bool, error) {
	if todoID <= 0 {
		return false, fmt.Errorf("todo id is required")
	}
	todo, err := s.GetTodo(ctx, todoID)
	if err != nil {
		return false, err
	}
	if todo.Done {
		return false, nil
	}

	// Force-queued suggestions are expected soon (newly saved TODO / open dialog /
	// retry now), so make them immediately claimable regardless of debounce.
	readyAt := time.Unix(0, 0)
	textHash := hashTodoText(todo.Text)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO todo_worktree_suggestions(
			todo_id, status, todo_text_hash, branch_name, worktree_suffix, kind, reason,
			confidence, model, last_error, updated_at
		)
		VALUES(?, ?, ?, '', '', '', '', 0, '', '', ?)
		ON CONFLICT(todo_id) DO UPDATE SET
			status = excluded.status,
			todo_text_hash = excluded.todo_text_hash,
			branch_name = '',
			worktree_suffix = '',
			kind = '',
			reason = '',
			confidence = 0,
			model = '',
			last_error = '',
			updated_at = excluded.updated_at
	`, todoID, string(model.TodoWorktreeSuggestionQueued), textHash, readyAt.Unix())
	return err == nil, err
}

func (s *Store) QueueOpenTodoWorktreeSuggestions(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM project_todos
		WHERE done = 0
		ORDER BY updated_at ASC, id ASC
	`)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, 16)
	for rows.Next() {
		var todoID int64
		if err := rows.Scan(&todoID); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, todoID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	queued := 0
	for _, todoID := range ids {
		changed, err := s.QueueTodoWorktreeSuggestion(ctx, todoID)
		if err != nil {
			return queued, err
		}
		if changed {
			queued++
		}
	}
	return queued, nil
}

func scanTodoWorktreeSuggestionRow(scanner interface {
	Scan(dest ...any) error
}) (model.TodoWorktreeSuggestion, error) {
	var (
		suggestion model.TodoWorktreeSuggestion
		status     string
		updatedAt  int64
	)
	if err := scanner.Scan(
		&suggestion.TodoID,
		&suggestion.ProjectPath,
		&suggestion.TodoText,
		&status,
		&suggestion.TodoTextHash,
		&suggestion.BranchName,
		&suggestion.WorktreeSuffix,
		&suggestion.Kind,
		&suggestion.Reason,
		&suggestion.Confidence,
		&suggestion.Model,
		&suggestion.LastError,
		&updatedAt,
	); err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	suggestion.Status = model.TodoWorktreeSuggestionStatus(strings.TrimSpace(status))
	suggestion.TodoText = strings.TrimSpace(suggestion.TodoText)
	suggestion.TodoTextHash = strings.TrimSpace(suggestion.TodoTextHash)
	suggestion.BranchName = strings.TrimSpace(suggestion.BranchName)
	suggestion.WorktreeSuffix = strings.TrimSpace(suggestion.WorktreeSuffix)
	suggestion.Kind = strings.TrimSpace(suggestion.Kind)
	suggestion.Reason = strings.TrimSpace(suggestion.Reason)
	suggestion.Model = strings.TrimSpace(suggestion.Model)
	suggestion.LastError = strings.TrimSpace(suggestion.LastError)
	suggestion.UpdatedAt = time.Unix(updatedAt, 0)
	return suggestion, nil
}

func (s *Store) GetTodoWorktreeSuggestion(ctx context.Context, todoID int64) (model.TodoWorktreeSuggestion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			tws.todo_id, pt.project_path, pt.text, tws.status, tws.todo_text_hash, tws.branch_name,
			tws.worktree_suffix, tws.kind, tws.reason, tws.confidence, tws.model, tws.last_error, tws.updated_at
		FROM todo_worktree_suggestions tws
		JOIN project_todos pt ON pt.id = tws.todo_id
		WHERE tws.todo_id = ?
	`, todoID)
	return scanTodoWorktreeSuggestionRow(row)
}

func (s *Store) DeleteTodoWorktreeSuggestion(ctx context.Context, todoID int64) error {
	if todoID <= 0 {
		return fmt.Errorf("todo id is required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM todo_worktree_suggestions WHERE todo_id = ?`, todoID)
	return err
}

func (s *Store) ClaimNextQueuedTodoWorktreeSuggestion(ctx context.Context, debounce, staleAfter time.Duration) (model.TodoWorktreeSuggestion, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := time.Now()
	if staleAfter > 0 {
		_, err = conn.ExecContext(ctx, `
			UPDATE todo_worktree_suggestions
			SET status = ?, updated_at = ?
			WHERE status = ? AND updated_at < ?
		`, string(model.TodoWorktreeSuggestionQueued), now.Unix(), string(model.TodoWorktreeSuggestionRunning), now.Add(-staleAfter).Unix())
		if err != nil {
			return model.TodoWorktreeSuggestion{}, err
		}
	}

	row := conn.QueryRowContext(ctx, `
		SELECT
			tws.todo_id, pt.project_path, pt.text, tws.status, tws.todo_text_hash, tws.branch_name,
			tws.worktree_suffix, tws.kind, tws.reason, tws.confidence, tws.model, tws.last_error, tws.updated_at
		FROM todo_worktree_suggestions tws
		JOIN project_todos pt ON pt.id = tws.todo_id
		JOIN projects p ON p.path = pt.project_path
		WHERE tws.status = ?
		  AND pt.done = 0
		  AND p.in_scope = 1
		  AND p.archived = 0
		  AND tws.updated_at <= ?
		ORDER BY p.attention_score DESC, pt.updated_at ASC, tws.updated_at ASC
		LIMIT 1
	`, string(model.TodoWorktreeSuggestionQueued), now.Add(-debounce).Unix())

	suggestion, err := scanTodoWorktreeSuggestionRow(row)
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}

	result, err := conn.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?, updated_at = ?, last_error = ''
		WHERE todo_id = ? AND status = ? AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionRunning), now.Unix(), suggestion.TodoID, string(model.TodoWorktreeSuggestionQueued), suggestion.UpdatedAt.Unix())
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	if rowsAffected != 1 {
		return model.TodoWorktreeSuggestion{}, sql.ErrNoRows
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	committed = true
	suggestion.Status = model.TodoWorktreeSuggestionRunning
	suggestion.UpdatedAt = now
	return suggestion, nil
}

func (s *Store) CompleteTodoWorktreeSuggestion(ctx context.Context, suggestion model.TodoWorktreeSuggestion) (bool, error) {
	if suggestion.TodoID <= 0 {
		return false, errors.New("todo worktree suggestion requires todo_id")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?,
			todo_text_hash = ?,
			branch_name = ?,
			worktree_suffix = ?,
			kind = ?,
			reason = ?,
			confidence = ?,
			model = ?,
			last_error = '',
			updated_at = ?
		WHERE todo_id = ?
		  AND status = ?
		  AND todo_text_hash = ?
		  AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionReady), strings.TrimSpace(suggestion.TodoTextHash), strings.TrimSpace(suggestion.BranchName),
		strings.TrimSpace(suggestion.WorktreeSuffix), strings.TrimSpace(suggestion.Kind), strings.TrimSpace(suggestion.Reason),
		suggestion.Confidence, strings.TrimSpace(suggestion.Model), now.Unix(), suggestion.TodoID,
		string(model.TodoWorktreeSuggestionRunning), strings.TrimSpace(suggestion.TodoTextHash), suggestion.UpdatedAt.Unix())
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected == 1, nil
}

func (s *Store) FailTodoWorktreeSuggestion(ctx context.Context, suggestion model.TodoWorktreeSuggestion, lastError string) (bool, error) {
	if suggestion.TodoID <= 0 {
		return false, errors.New("todo worktree suggestion requires todo_id")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?, last_error = ?, updated_at = ?
		WHERE todo_id = ?
		  AND status = ?
		  AND todo_text_hash = ?
		  AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionFailed), strings.TrimSpace(lastError), time.Now().Unix(), suggestion.TodoID,
		string(model.TodoWorktreeSuggestionRunning), strings.TrimSpace(suggestion.TodoTextHash), suggestion.UpdatedAt.Unix())
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected == 1, nil
}

func (s *Store) GetTodo(ctx context.Context, todoID int64) (model.TodoItem, error) {
	if todoID <= 0 {
		return model.TodoItem{}, fmt.Errorf("todo id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, project_path, text, done, position, created_at, updated_at, completed_at,
			work_provider, work_project_path, work_session_id, work_claimed_at, work_state, work_state_at
		FROM project_todos
		WHERE id = ?
	`, todoID)
	var (
		item         model.TodoItem
		done         int
		createdAt    int64
		updatedAt    int64
		completedAt  sql.NullInt64
		workProvider sql.NullString
		workProject  sql.NullString
		workSession  sql.NullString
		workClaimed  sql.NullInt64
		workState    sql.NullString
		workStateAt  sql.NullInt64
	)
	if err := row.Scan(
		&item.ID, &item.ProjectPath, &item.Text, &done, &item.Position, &createdAt, &updatedAt, &completedAt,
		&workProvider, &workProject, &workSession, &workClaimed, &workState, &workStateAt,
	); err != nil {
		return model.TodoItem{}, err
	}
	item.Done = done != 0
	item.CreatedAt = time.Unix(createdAt, 0)
	item.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		item.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	item.WorkProvider = model.NormalizeSessionSource(model.SessionSource(strings.TrimSpace(workProvider.String)))
	item.WorkProjectPath = strings.TrimSpace(workProject.String)
	item.WorkSessionID = strings.TrimSpace(workSession.String)
	item.WorkState = model.NormalizeTodoWorkState(model.TodoWorkState(strings.TrimSpace(workState.String)))
	if workClaimed.Valid {
		item.WorkClaimedAt = time.Unix(workClaimed.Int64, 0)
	}
	if workStateAt.Valid {
		item.WorkStateAt = time.Unix(workStateAt.Int64, 0)
	}
	attachments, err := s.ListTodoAttachments(ctx, item.ID)
	if err != nil {
		return model.TodoItem{}, err
	}
	item.Attachments = attachments
	return item, nil
}

func (s *Store) UpdateTodo(ctx context.Context, id int64, text string) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("todo text is required")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET text = ?, updated_at = ?
		WHERE id = ?
	`, text, now.Unix(), id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateTodoWithAttachments(ctx context.Context, id int64, text string, attachments []model.TodoAttachment) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("todo text is required")
	}
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE project_todos
		SET text = ?, updated_at = ?
		WHERE id = ?
	`, text, now.Unix(), id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	if err := replaceTodoAttachmentsTx(ctx, tx, id, attachments, now); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeTodoAttachmentsForSave(in []model.TodoAttachment) []model.TodoAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.TodoAttachment, 0, len(in))
	for _, attachment := range in {
		path := strings.TrimSpace(attachment.Path)
		if path == "" {
			continue
		}
		kind := model.TodoAttachmentKind(strings.TrimSpace(string(attachment.Kind)))
		if kind == "" {
			kind = model.TodoAttachmentLocalImage
		}
		out = append(out, model.TodoAttachment{
			ID:        attachment.ID,
			TodoID:    attachment.TodoID,
			Kind:      kind,
			Path:      path,
			Position:  len(out),
			CreatedAt: attachment.CreatedAt,
		})
	}
	return out
}

func replaceTodoAttachmentsTx(ctx context.Context, tx *sql.Tx, todoID int64, attachments []model.TodoAttachment, now time.Time) error {
	if todoID <= 0 {
		return fmt.Errorf("todo id is required")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_attachments WHERE todo_id = ?`, todoID); err != nil {
		return err
	}
	for i, attachment := range normalizeTodoAttachmentsForSave(attachments) {
		createdAt := attachment.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO todo_attachments(todo_id, kind, path, position, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, todoID, string(attachment.Kind), attachment.Path, i, createdAt.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListTodoAttachments(ctx context.Context, todoID int64) ([]model.TodoAttachment, error) {
	if todoID <= 0 {
		return nil, nil
	}
	byTodoID, err := s.listTodoAttachmentsForIDs(ctx, []int64{todoID})
	if err != nil {
		return nil, err
	}
	return byTodoID[todoID], nil
}

func (s *Store) listTodoAttachmentsForIDs(ctx context.Context, ids []int64) (map[int64][]model.TodoAttachment, error) {
	out := make(map[int64][]model.TodoAttachment, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(args) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, todo_id, kind, path, position, created_at
		FROM todo_attachments
		WHERE todo_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY todo_id ASC, position ASC, id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			attachment model.TodoAttachment
			kind       string
			createdAt  int64
		)
		if err := rows.Scan(&attachment.ID, &attachment.TodoID, &kind, &attachment.Path, &attachment.Position, &createdAt); err != nil {
			return nil, err
		}
		attachment.Kind = model.TodoAttachmentKind(strings.TrimSpace(kind))
		if attachment.Kind == "" {
			attachment.Kind = model.TodoAttachmentLocalImage
		}
		attachment.Path = strings.TrimSpace(attachment.Path)
		attachment.CreatedAt = time.Unix(createdAt, 0)
		if attachment.Path == "" {
			continue
		}
		out[attachment.TodoID] = append(out[attachment.TodoID], attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for todoID, attachments := range out {
		for i := range attachments {
			attachments[i].Position = i
		}
		out[todoID] = attachments
	}
	return out, nil
}

func todoIDs(items []model.TodoItem) []int64 {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item.ID > 0 {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (s *Store) ToggleTodoDone(ctx context.Context, id int64, done bool) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	now := time.Now()
	var (
		result sql.Result
		err    error
	)
	if done {
		result, err = s.db.ExecContext(ctx, `
			UPDATE project_todos
			SET done = 1,
				completed_at = ?,
				updated_at = ?,
				work_provider = '',
				work_project_path = '',
				work_session_id = '',
				work_claimed_at = NULL,
				work_state = '',
				work_state_at = NULL
			WHERE id = ?
		`, now.Unix(), now.Unix(), id)
	} else {
		result, err = s.db.ExecContext(ctx, `
			UPDATE project_todos
			SET done = 0,
				completed_at = NULL,
				updated_at = ?
			WHERE id = ?
		`, now.Unix(), id)
	}
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) AttachTodoWorkSession(ctx context.Context, todoID int64, workProjectPath string, provider model.SessionSource, sessionID string, state model.TodoWorkState, at time.Time) error {
	if todoID <= 0 {
		return fmt.Errorf("todo id is required")
	}
	workProjectPath = strings.TrimSpace(workProjectPath)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("todo work session id is required")
	}
	provider = model.NormalizeSessionSource(provider)
	if provider == "" {
		provider = model.SessionSourceUnknown
	}
	state = model.NormalizeTodoWorkState(state)
	if state == "" {
		state = model.TodoWorkStateWorking
	}
	if at.IsZero() {
		at = time.Now()
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET work_provider = ?,
			work_project_path = ?,
			work_session_id = ?,
			work_claimed_at = ?,
			work_state = ?,
			work_state_at = ?,
			updated_at = ?
		WHERE id = ?
		  AND done = 0
	`, string(provider), workProjectPath, sessionID, at.Unix(), string(state), at.Unix(), now.Unix(), todoID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateTodoWorkStateForSession(ctx context.Context, workProjectPath string, provider model.SessionSource, sessionID string, state model.TodoWorkState, at time.Time) (int, error) {
	workProjectPath = strings.TrimSpace(workProjectPath)
	sessionID = strings.TrimSpace(sessionID)
	if workProjectPath == "" || sessionID == "" {
		return 0, nil
	}
	provider = model.NormalizeSessionSource(provider)
	if provider == "" {
		provider = model.SessionSourceUnknown
	}
	state = model.NormalizeTodoWorkState(state)
	if state == "" {
		state = model.TodoWorkStateIdle
	}
	if at.IsZero() {
		at = time.Now()
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET work_state = ?,
			work_state_at = ?,
			updated_at = ?
		WHERE work_provider = ?
		  AND work_session_id = ?
		  AND done = 0
		  AND (work_state <> ? OR work_state_at IS NULL OR work_state_at < ?)
		  AND (work_state_at IS NULL OR work_state_at <= ?)
		  AND (work_project_path = ? OR (work_project_path = '' AND project_path = ?))
	`, string(state), at.Unix(), now.Unix(), string(provider), sessionID, string(state), at.Unix(), at.Unix(), workProjectPath, workProjectPath)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rowsAffected), nil
}

func (s *Store) ClearTodoWorkForProjectPath(ctx context.Context, workProjectPath string) (int, error) {
	workProjectPath = strings.TrimSpace(workProjectPath)
	if workProjectPath == "" {
		return 0, nil
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET work_provider = '',
			work_project_path = '',
			work_session_id = '',
			work_claimed_at = NULL,
			work_state = '',
			work_state_at = NULL,
			updated_at = ?
		WHERE done = 0
		  AND work_project_path = ?
		  AND (
			work_provider <> ''
			OR work_session_id <> ''
			OR work_claimed_at IS NOT NULL
			OR work_state <> ''
			OR work_state_at IS NOT NULL
		  )
	`, now.Unix(), workProjectPath)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rowsAffected), nil
}

func (s *Store) TodoProjectPathsForWorkSession(ctx context.Context, workProjectPath string, provider model.SessionSource, sessionID string) ([]string, error) {
	workProjectPath = strings.TrimSpace(workProjectPath)
	sessionID = strings.TrimSpace(sessionID)
	if workProjectPath == "" || sessionID == "" {
		return nil, nil
	}
	provider = model.NormalizeSessionSource(provider)
	if provider == "" {
		provider = model.SessionSourceUnknown
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT project_path
		FROM project_todos
		WHERE work_provider = ?
		  AND work_session_id = ?
		  AND done = 0
		  AND (work_project_path = ? OR (work_project_path = '' AND project_path = ?))
		ORDER BY project_path ASC
	`, string(provider), sessionID, workProjectPath, workProjectPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		if strings.TrimSpace(path) != "" {
			paths = append(paths, strings.TrimSpace(path))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) DeleteTodo(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM project_todos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteDoneTodos(ctx context.Context, projectPath string) (int, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return 0, fmt.Errorf("project path is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM project_todos WHERE project_path = ? AND done = 1`, projectPath)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rowsAffected > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE path = ?`, time.Now().Unix(), projectPath); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(rowsAffected), nil
}
