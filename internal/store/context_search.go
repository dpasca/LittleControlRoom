package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/model"
	"lcroom/internal/opencodesqlite"
)

const (
	contextSearchDefaultLimit            = 8
	contextSearchMaxLimit                = 24
	contextSearchMaxTerms                = 10
	contextSearchSessionCandidateLimit   = 120
	contextSearchCachedSessionDocLimit   = 1000
	contextSearchJSONLTailBytes          = 2 * 1024 * 1024
	contextSearchMaxSessionTextBytes     = 64 * 1024
	contextSearchMaxTranscriptItemRunes  = 1200
	contextSearchMaxProjectDocItemRunes  = 1000
	contextSearchOpenCodeRecentMsgLimit  = 120
	contextSearchScannerInitialBuf       = 64 * 1024
	contextSearchScannerMaxTokenCapacity = 16 * 1024 * 1024
)

type contextSearchDoc struct {
	Source      string
	ProjectPath string
	ProjectName string
	SessionID   string
	Title       string
	Body        string
	UpdatedAt   time.Time
}

type contextSessionCandidate struct {
	SessionID            string
	Source               model.SessionSource
	RawSessionID         string
	ProjectPath          string
	ProjectName          string
	SessionFile          string
	SessionFormat        string
	SnapshotHash         string
	SourceUpdatedAt      time.Time
	LatestTurnStateKnown bool
	LatestTurnCompleted  bool
}

type contextTranscriptItem struct {
	Role string
	Text string
}

func (s *Store) SearchContext(ctx context.Context, req model.ContextSearchRequest) ([]model.ContextSearchResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, nil
	}
	match := contextSearchMatch(query)
	if match == "" {
		return nil, nil
	}
	if err := s.refreshContextSearchIndex(ctx, req.IncludeHistorical); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = contextSearchDefaultLimit
	}
	if limit > contextSearchMaxLimit {
		limit = contextSearchMaxLimit
	}
	return s.queryContextSearchIndex(ctx, match, req.ProjectPath, limit)
}

func contextSearchMatch(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]struct{}{}
	terms := make([]string, 0, min(len(fields), contextSearchMaxTerms))
	for _, field := range fields {
		term := strings.ToLower(strings.TrimSpace(field))
		if len([]rune(term)) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, contextSearchClipRunes(term, 48)+"*")
		if len(terms) >= contextSearchMaxTerms {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

func (s *Store) refreshContextSearchIndex(ctx context.Context, includeHistorical bool) error {
	if err := s.refreshContextSessionTextCache(ctx, includeHistorical); err != nil {
		return err
	}

	projectDocs, err := s.contextProjectDocs(ctx, includeHistorical)
	if err != nil {
		return err
	}
	sessionDocs, err := s.contextSessionDocs(ctx, includeHistorical)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM context_search_fts`); err != nil {
		return fmt.Errorf("clear context search index: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO context_search_fts(source, project_path, project_name, session_id, title, body, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare context search index insert: %w", err)
	}
	defer stmt.Close()

	for _, doc := range append(projectDocs, sessionDocs...) {
		if strings.TrimSpace(doc.Title) == "" && strings.TrimSpace(doc.Body) == "" {
			continue
		}
		if _, err := stmt.ExecContext(
			ctx,
			doc.Source,
			doc.ProjectPath,
			doc.ProjectName,
			doc.SessionID,
			doc.Title,
			doc.Body,
			doc.UpdatedAt.Unix(),
		); err != nil {
			return fmt.Errorf("insert context search doc: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) contextProjectDocs(ctx context.Context, includeHistorical bool) ([]contextSearchDoc, error) {
	projects, err := s.ListProjects(ctx, includeHistorical)
	if err != nil {
		return nil, err
	}
	docs := make([]contextSearchDoc, 0, len(projects))
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		detail, err := s.GetProjectDetail(ctx, project.Path, 5)
		if err != nil {
			return nil, err
		}
		docs = append(docs, contextProjectDoc(detail))
	}
	return docs, nil
}

func contextProjectDoc(detail model.ProjectDetail) contextSearchDoc {
	summary := detail.Summary
	name := contextProjectName(summary.Path, summary.Name)
	updatedAt := summary.LastActivity
	updatedAt = laterTime(updatedAt, summary.LatestSessionClassificationUpdatedAt)
	updatedAt = laterTime(updatedAt, summary.LatestCompletedSessionClassificationUpdatedAt)
	updatedAt = laterTime(updatedAt, summary.CreatedAt)

	var b strings.Builder
	contextAppendLine(&b, "Project: "+name)
	contextAppendLine(&b, "Path: "+summary.Path)
	contextAppendLine(&b, "Status: "+string(summary.Status))
	contextAppendLine(&b, fmt.Sprintf("Attention score: %d", summary.AttentionScore))
	if summary.Kind != "" {
		contextAppendLine(&b, "Kind: "+string(summary.Kind))
	}
	if summary.RepoBranch != "" {
		contextAppendLine(&b, "Branch: "+summary.RepoBranch)
	}
	if summary.RepoSyncStatus != "" {
		contextAppendLine(&b, "Repo sync: "+string(summary.RepoSyncStatus))
	}
	if summary.RepoDirty {
		contextAppendLine(&b, "Repo has uncommitted changes")
	}
	if summary.RepoConflict {
		contextAppendLine(&b, "Repo has conflicts")
	}
	if summary.RunCommand != "" {
		contextAppendLine(&b, "Run command: "+summary.RunCommand)
	}
	if summary.MovedFromPath != "" {
		contextAppendLine(&b, "Moved from: "+summary.MovedFromPath)
	}
	if summary.LatestSessionSummary != "" {
		contextAppendLine(&b, "Latest session assessment: "+summary.LatestSessionSummary)
	}
	if summary.LatestCompletedSessionSummary != "" && summary.LatestCompletedSessionSummary != summary.LatestSessionSummary {
		contextAppendLine(&b, "Latest completed assessment: "+summary.LatestCompletedSessionSummary)
	}

	for _, reason := range detail.Reasons {
		contextAppendLine(&b, "Attention reason: "+reason.Text)
	}
	for _, todo := range detail.Todos {
		status := "open"
		if todo.Done {
			status = "done"
		}
		contextAppendLine(&b, fmt.Sprintf("TODO %s #%d: %s", status, todo.ID, todo.Text))
		updatedAt = laterTime(updatedAt, todo.UpdatedAt)
	}
	for _, artifact := range detail.Artifacts {
		contextAppendLine(&b, "Artifact: "+strings.TrimSpace(strings.Join([]string{artifact.Kind, artifact.Path, artifact.Note}, " ")))
		updatedAt = laterTime(updatedAt, artifact.UpdatedAt)
	}
	if detail.LatestSessionClassification != nil {
		c := detail.LatestSessionClassification
		contextAppendLine(&b, "Latest classification status: "+string(c.Status))
		if c.Category != "" {
			contextAppendLine(&b, "Latest classification category: "+string(c.Category))
		}
		if c.Summary != "" {
			contextAppendLine(&b, "Latest classification summary: "+c.Summary)
		}
		updatedAt = laterTime(updatedAt, c.UpdatedAt)
		updatedAt = laterTime(updatedAt, c.SourceUpdatedAt)
	}
	for _, session := range detail.Sessions {
		contextAppendLine(&b, "Session: "+session.ExternalID()+" "+string(session.Source)+" "+session.SessionFile)
		updatedAt = laterTime(updatedAt, session.LastEventAt)
	}
	for _, event := range detail.RecentEvents {
		contextAppendLine(&b, "Recent event: "+event.Type+" "+event.Payload)
		updatedAt = laterTime(updatedAt, event.At)
	}
	if updatedAt.IsZero() {
		updatedAt = time.Unix(0, 0)
	}

	return contextSearchDoc{
		Source:      "project",
		ProjectPath: summary.Path,
		ProjectName: name,
		Title:       strings.TrimSpace(name + " " + summary.Path),
		Body:        b.String(),
		UpdatedAt:   updatedAt,
	}
}

func (s *Store) refreshContextSessionTextCache(ctx context.Context, includeHistorical bool) error {
	candidates, err := s.contextSessionCandidates(ctx, includeHistorical)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		needsRefresh, err := s.contextSessionCacheNeedsRefresh(ctx, candidate)
		if err != nil {
			return err
		}
		if !needsRefresh {
			continue
		}
		text, err := compactContextSessionText(ctx, candidate)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			text = ""
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO context_session_text_cache(
				session_id, source, raw_session_id, project_path, project_name, session_file, session_format,
				snapshot_hash, source_updated_at, latest_turn_state_known, latest_turn_completed, cached_at, text
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
				source=excluded.source,
				raw_session_id=excluded.raw_session_id,
				project_path=excluded.project_path,
				project_name=excluded.project_name,
				session_file=excluded.session_file,
				session_format=excluded.session_format,
				snapshot_hash=excluded.snapshot_hash,
				source_updated_at=excluded.source_updated_at,
				latest_turn_state_known=excluded.latest_turn_state_known,
				latest_turn_completed=excluded.latest_turn_completed,
				cached_at=excluded.cached_at,
				text=excluded.text
		`,
			candidate.SessionID,
			string(candidate.Source),
			candidate.RawSessionID,
			candidate.ProjectPath,
			candidate.ProjectName,
			candidate.SessionFile,
			candidate.SessionFormat,
			candidate.SnapshotHash,
			candidate.SourceUpdatedAt.Unix(),
			boolToInt(candidate.LatestTurnStateKnown),
			boolToInt(candidate.LatestTurnCompleted),
			now,
			text,
		); err != nil {
			return fmt.Errorf("upsert context session text cache: %w", err)
		}
	}
	return nil
}

func (s *Store) contextSessionCandidates(ctx context.Context, includeHistorical bool) ([]contextSessionCandidate, error) {
	query := `
		SELECT
			ps.session_id,
			COALESCE(ps.source, ''),
			COALESCE(ps.raw_session_id, ''),
			ps.project_path,
			p.name,
			ps.session_file,
			ps.format,
			COALESCE(ps.snapshot_hash, ''),
			ps.last_event_at,
			COALESCE(ps.latest_turn_state_known, 0),
			COALESCE(ps.latest_turn_completed, 0),
			CASE WHEN COALESCE(ps.latest_turn_state_known, 0) = 1 AND COALESCE(ps.latest_turn_completed, 0) = 0 THEN 0 ELSE 1 END
		FROM project_sessions ps
		JOIN projects p ON p.path = ps.project_path
		WHERE ps.session_file != ''
			AND ` + projectSummaryVisibilityConditions(includeHistorical) + `
		ORDER BY
			CASE WHEN COALESCE(ps.latest_turn_state_known, 0) = 1 AND COALESCE(ps.latest_turn_completed, 0) = 0 THEN 0 ELSE 1 END ASC,
			ps.last_event_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, contextSearchSessionCandidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []contextSessionCandidate{}
	for rows.Next() {
		var (
			candidate       contextSessionCandidate
			source          string
			sourceUpdatedAt int64
			turnKnown       int
			turnCompleted   int
			openPriority    int
		)
		if err := rows.Scan(
			&candidate.SessionID,
			&source,
			&candidate.RawSessionID,
			&candidate.ProjectPath,
			&candidate.ProjectName,
			&candidate.SessionFile,
			&candidate.SessionFormat,
			&candidate.SnapshotHash,
			&sourceUpdatedAt,
			&turnKnown,
			&turnCompleted,
			&openPriority,
		); err != nil {
			return nil, err
		}
		candidate.Source = model.NormalizeSessionSource(model.SessionSource(source))
		candidate.SourceUpdatedAt = time.Unix(sourceUpdatedAt, 0)
		if artifactUpdatedAt := contextSessionArtifactUpdatedAt(candidate.SessionFile, candidate.SessionFormat); artifactUpdatedAt.After(candidate.SourceUpdatedAt) {
			candidate.SourceUpdatedAt = artifactUpdatedAt
		}
		candidate.LatestTurnStateKnown = turnKnown != 0
		candidate.LatestTurnCompleted = turnCompleted != 0
		if candidate.ProjectName == "" {
			candidate.ProjectName = contextProjectName(candidate.ProjectPath, "")
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

func (s *Store) contextSessionCacheNeedsRefresh(ctx context.Context, candidate contextSessionCandidate) (bool, error) {
	var (
		sessionFile     string
		sessionFormat   string
		projectPath     string
		projectName     string
		snapshotHash    string
		sourceUpdatedAt int64
		latestTurnKnown int
		latestTurnDone  int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT session_file, session_format, project_path, project_name, snapshot_hash, source_updated_at, latest_turn_state_known, latest_turn_completed
		FROM context_session_text_cache
		WHERE session_id = ?
	`, candidate.SessionID).Scan(&sessionFile, &sessionFormat, &projectPath, &projectName, &snapshotHash, &sourceUpdatedAt, &latestTurnKnown, &latestTurnDone)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return sessionFile != candidate.SessionFile ||
		sessionFormat != candidate.SessionFormat ||
		projectPath != candidate.ProjectPath ||
		projectName != candidate.ProjectName ||
		snapshotHash != candidate.SnapshotHash ||
		sourceUpdatedAt != candidate.SourceUpdatedAt.Unix() ||
		(latestTurnKnown != 0) != candidate.LatestTurnStateKnown ||
		(latestTurnDone != 0) != candidate.LatestTurnCompleted, nil
}

func (s *Store) contextSessionDocs(ctx context.Context, includeHistorical bool) ([]contextSearchDoc, error) {
	query := `
		SELECT
			c.source,
			c.project_path,
			c.project_name,
			c.session_id,
			c.raw_session_id,
			c.session_format,
			c.source_updated_at,
			c.latest_turn_state_known,
			c.latest_turn_completed,
			c.text,
			COALESCE(sc.status, ''),
			COALESCE(sc.stage, ''),
			COALESCE(sc.category, ''),
			COALESCE(sc.summary, ''),
			COALESCE(sc.last_error, '')
		FROM context_session_text_cache c
		JOIN projects p ON p.path = c.project_path
		LEFT JOIN session_classifications sc ON sc.session_id = c.session_id
		WHERE ` + projectSummaryVisibilityConditions(includeHistorical) + `
		ORDER BY c.source_updated_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, contextSearchCachedSessionDocLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []contextSearchDoc{}
	for rows.Next() {
		var (
			source, projectPath, projectName, sessionID, rawSessionID, format, text string
			status, stage, category, summary, lastError                             string
			sourceUpdatedAt                                                         int64
			turnKnown, turnCompleted                                                int
		)
		if err := rows.Scan(
			&source,
			&projectPath,
			&projectName,
			&sessionID,
			&rawSessionID,
			&format,
			&sourceUpdatedAt,
			&turnKnown,
			&turnCompleted,
			&text,
			&status,
			&stage,
			&category,
			&summary,
			&lastError,
		); err != nil {
			return nil, err
		}

		externalID := model.ExternalSessionID(model.SessionSource(source), format, sessionID, rawSessionID)
		var b strings.Builder
		contextAppendLine(&b, "Session: "+externalID)
		contextAppendLine(&b, "Project: "+contextProjectName(projectPath, projectName))
		contextAppendLine(&b, "Project path: "+projectPath)
		if source != "" {
			contextAppendLine(&b, "Source: "+source)
		}
		if turnKnown != 0 {
			if turnCompleted != 0 {
				contextAppendLine(&b, "Latest turn completed")
			} else {
				contextAppendLine(&b, "Latest turn may still be open")
			}
		}
		if status != "" {
			contextAppendLine(&b, "Assessment status: "+status)
		}
		if stage != "" {
			contextAppendLine(&b, "Assessment stage: "+stage)
		}
		if category != "" {
			contextAppendLine(&b, "Assessment category: "+category)
		}
		if summary != "" {
			contextAppendLine(&b, "Assessment summary: "+summary)
		}
		if lastError != "" {
			contextAppendLine(&b, "Assessment error: "+lastError)
		}
		if text != "" {
			contextAppendLine(&b, "Transcript:")
			contextAppendLine(&b, text)
		}
		if strings.TrimSpace(b.String()) == "" {
			continue
		}
		out = append(out, contextSearchDoc{
			Source:      "session",
			ProjectPath: projectPath,
			ProjectName: contextProjectName(projectPath, projectName),
			SessionID:   sessionID,
			Title:       strings.TrimSpace("session " + externalID + " " + projectName + " " + projectPath),
			Body:        b.String(),
			UpdatedAt:   time.Unix(sourceUpdatedAt, 0),
		})
	}
	return out, rows.Err()
}

func (s *Store) queryContextSearchIndex(ctx context.Context, match, projectPath string, limit int) ([]model.ContextSearchResult, error) {
	query := `
		SELECT
			source,
			project_path,
			project_name,
			session_id,
			title,
			snippet(context_search_fts, 5, '[', ']', '...', 32),
			CAST(updated_at AS INTEGER),
			bm25(context_search_fts) AS score
		FROM context_search_fts
		WHERE context_search_fts MATCH ?
	`
	args := []any{match}
	if projectPath = strings.TrimSpace(projectPath); projectPath != "" {
		query += ` AND project_path = ?`
		args = append(args, filepath.Clean(projectPath))
	}
	query += ` ORDER BY score ASC, CAST(updated_at AS INTEGER) DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ContextSearchResult{}
	for rows.Next() {
		var (
			result    model.ContextSearchResult
			updatedAt int64
		)
		if err := rows.Scan(
			&result.Source,
			&result.ProjectPath,
			&result.ProjectName,
			&result.SessionID,
			&result.Title,
			&result.Snippet,
			&updatedAt,
			&result.Score,
		); err != nil {
			return nil, err
		}
		result.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, result)
	}
	return out, rows.Err()
}

func compactContextSessionText(ctx context.Context, candidate contextSessionCandidate) (string, error) {
	switch strings.TrimSpace(candidate.SessionFormat) {
	case "modern", "legacy":
		return compactContextJSONLTranscript(ctx, candidate.SessionFile, extractContextCodexTranscriptItem)
	case "claude_code":
		return compactContextJSONLTranscript(ctx, candidate.SessionFile, extractContextClaudeTranscriptItem)
	case "opencode_db":
		return compactContextOpenCodeTranscript(ctx, candidate.SessionFile)
	default:
		return "", nil
	}
}

func contextSessionArtifactUpdatedAt(sessionFile, format string) time.Time {
	path := strings.TrimSpace(sessionFile)
	if path == "" {
		return time.Time{}
	}
	if strings.TrimSpace(format) == "opencode_db" {
		if dbPath, _, err := parseContextOpenCodeSessionRef(path); err == nil {
			path = dbPath
		} else if dbPath, _, ok := strings.Cut(path, "#"); ok {
			path = strings.TrimSpace(dbPath)
		}
	}
	if path == "" {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func compactContextJSONLTranscript(ctx context.Context, path string, extract func(string) (contextTranscriptItem, bool)) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	if stat.Size() > contextSearchJSONLTailBytes {
		if _, err := file.Seek(stat.Size()-contextSearchJSONLTailBytes, io.SeekStart); err != nil {
			return "", err
		}
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, contextSearchScannerInitialBuf), contextSearchScannerMaxTokenCapacity)
	lines := []string{}
	totalBytes := 0
	lastLine := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		item, ok := extract(scanner.Text())
		if !ok {
			continue
		}
		line := contextTranscriptLine(item)
		if line == "" || line == lastLine {
			continue
		}
		lastLine = line
		lines = append(lines, line)
		totalBytes += len(line) + 1
		for totalBytes > contextSearchMaxSessionTextBytes && len(lines) > 0 {
			totalBytes -= len(lines[0]) + 1
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func extractContextCodexTranscriptItem(line string) (contextTranscriptItem, bool) {
	var top struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return contextTranscriptItem{}, false
	}

	switch top.Type {
	case "message":
		return contextTranscriptItemFromRaw(top.Role, top.Content)
	case "response_item":
		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil || payload.Type != "message" {
			return contextTranscriptItem{}, false
		}
		return contextTranscriptItemFromRaw(payload.Role, payload.Content)
	case "event_msg":
		var payload struct {
			Type             string `json:"type"`
			Message          string `json:"message"`
			LastAgentMessage string `json:"last_agent_message"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil {
			return contextTranscriptItem{}, false
		}
		switch payload.Type {
		case "user_message":
			return contextTranscriptItemFromText("user", payload.Message)
		case "agent_message":
			return contextTranscriptItemFromText("assistant", payload.Message)
		case "task_complete":
			return contextTranscriptItemFromText("assistant", payload.LastAgentMessage)
		default:
			return contextTranscriptItem{}, false
		}
	default:
		if top.Type == "" && top.Role != "" {
			return contextTranscriptItemFromRaw(top.Role, top.Content)
		}
		return contextTranscriptItem{}, false
	}
}

func extractContextClaudeTranscriptItem(line string) (contextTranscriptItem, bool) {
	var top struct {
		Type    string `json:"type"`
		IsMeta  bool   `json:"isMeta"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return contextTranscriptItem{}, false
	}
	if top.IsMeta || top.Type == "tool_result" || top.Type == "system" {
		return contextTranscriptItem{}, false
	}
	switch top.Type {
	case "user", "assistant":
		role := top.Message.Role
		if role == "" {
			role = top.Type
		}
		return contextTranscriptItemFromRaw(role, top.Message.Content)
	default:
		return contextTranscriptItem{}, false
	}
}

func compactContextOpenCodeTranscript(ctx context.Context, sessionRef string) (string, error) {
	dbPath, sessionID, err := parseContextOpenCodeSessionRef(sessionRef)
	if err != nil {
		return "", err
	}
	db, err := opencodesqlite.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		WITH recent_messages AS (
			SELECT id, time_created, data
			FROM message
			WHERE session_id = ?
			ORDER BY time_created DESC
			LIMIT %d
		)
		SELECT rm.id, rm.data, p.data
		FROM recent_messages rm
		LEFT JOIN part p ON p.message_id = rm.id
		ORDER BY rm.time_created ASC, p.time_created ASC
	`, contextSearchOpenCodeRecentMsgLimit), sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type messageEntry struct {
		Role  string
		Parts []string
	}
	order := []string{}
	entries := map[string]*messageEntry{}
	for rows.Next() {
		var (
			messageID   string
			messageData string
			partData    sql.NullString
		)
		if err := rows.Scan(&messageID, &messageData, &partData); err != nil {
			return "", err
		}
		entry, ok := entries[messageID]
		if !ok {
			entry = &messageEntry{Role: parseContextOpenCodeRole(messageData)}
			entries[messageID] = entry
			order = append(order, messageID)
		}
		if !partData.Valid {
			continue
		}
		if text := parseContextOpenCodeTextPart(partData.String); text != "" {
			entry.Parts = append(entry.Parts, text)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	lines := []string{}
	totalBytes := 0
	lastLine := ""
	for _, id := range order {
		entry := entries[id]
		if !contextAllowedTranscriptRole(entry.Role) {
			continue
		}
		line := contextTranscriptLine(contextTranscriptItem{
			Role: entry.Role,
			Text: strings.Join(entry.Parts, "\n\n"),
		})
		if line == "" || line == lastLine {
			continue
		}
		lastLine = line
		lines = append(lines, line)
		totalBytes += len(line) + 1
		for totalBytes > contextSearchMaxSessionTextBytes && len(lines) > 0 {
			totalBytes -= len(lines[0]) + 1
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n"), nil
}

func parseContextOpenCodeSessionRef(sessionRef string) (string, string, error) {
	dbPath, ref, ok := strings.Cut(sessionRef, "#")
	if !ok {
		return "", "", fmt.Errorf("invalid opencode session ref: %s", sessionRef)
	}
	if !strings.HasPrefix(ref, "session:") {
		return "", "", fmt.Errorf("invalid opencode session ref: %s", sessionRef)
	}
	sessionID := strings.TrimPrefix(ref, "session:")
	if sessionID == "" {
		return "", "", fmt.Errorf("invalid opencode session ref: %s", sessionRef)
	}
	return filepath.Clean(dbPath), sessionID, nil
}

func parseContextOpenCodeRole(messageData string) string {
	var payload struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(messageData), &payload); err != nil {
		return ""
	}
	return payload.Role
}

func parseContextOpenCodeTextPart(partData string) string {
	var payload struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(partData), &payload); err != nil || payload.Type != "text" {
		return ""
	}
	return contextSanitizeText(payload.Text)
}

func contextTranscriptItemFromRaw(role string, raw json.RawMessage) (contextTranscriptItem, bool) {
	return contextTranscriptItemFromText(role, extractContextContentText(raw))
}

func contextTranscriptItemFromText(role, text string) (contextTranscriptItem, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	text = contextSanitizeText(text)
	if !contextAllowedTranscriptRole(role) || text == "" {
		return contextTranscriptItem{}, false
	}
	return contextTranscriptItem{Role: role, Text: text}, true
}

func contextAllowedTranscriptRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user", "assistant":
		return true
	default:
		return false
	}
}

func extractContextContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return contextSanitizeText(asString)
	}

	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			switch block.Type {
			case "text", "input_text", "output_text":
				if text := contextSanitizeText(block.Text); text != "" {
					parts = append(parts, text)
					continue
				}
				if len(block.Content) > 0 {
					if text := extractContextContentText(block.Content); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
		return contextSanitizeText(strings.Join(parts, "\n\n"))
	}

	var object struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		switch object.Type {
		case "", "text", "input_text", "output_text":
			if text := contextSanitizeText(object.Text); text != "" {
				return text
			}
			if len(object.Content) > 0 {
				return extractContextContentText(object.Content)
			}
		}
	}
	return ""
}

func contextTranscriptLine(item contextTranscriptItem) string {
	role := strings.ToLower(strings.TrimSpace(item.Role))
	text := contextSanitizeText(item.Text)
	if !contextAllowedTranscriptRole(role) || text == "" {
		return ""
	}
	return role + ": " + contextSearchClipRunes(text, contextSearchMaxTranscriptItemRunes)
}

func contextAppendLine(b *strings.Builder, line string) {
	if b == nil {
		return
	}
	line = contextSanitizeText(line)
	if line == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(contextSearchClipRunes(line, contextSearchMaxProjectDocItemRunes))
}

func contextSanitizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func contextSearchClipRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func contextProjectName(path, name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "unnamed project"
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return path
	}
	return base
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
