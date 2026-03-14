package sessionclassify

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

	"lcroom/internal/model"
	"lcroom/internal/opencodesqlite"
)

const (
	maxTranscriptItems = 8
	maxTranscriptBytes = 800
	codexTailBytes     = 1024 * 1024
	maxPreviewBytes    = 160
	previewHeadBytes   = 64 * 1024
	previewItemLimit   = 6
)

type SessionSnapshot struct {
	ProjectPath          string            `json:"project_path"`
	SessionID            string            `json:"session_id"`
	SessionFormat        string            `json:"session_format"`
	LastEventAt          string            `json:"last_event_at"`
	LatestTurnStateKnown bool              `json:"latest_turn_state_known"`
	LatestTurnCompleted  bool              `json:"latest_turn_completed"`
	GitStatus            GitStatusSnapshot `json:"git_status,omitempty"`
	Transcript           []TranscriptItem  `json:"transcript"`
}

type SessionPreview struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type GitStatusSnapshot struct {
	WorktreeDirty bool   `json:"worktree_dirty"`
	RemoteStatus  string `json:"remote_status,omitempty"`
	AheadCount    int    `json:"ahead_count,omitempty"`
	BehindCount   int    `json:"behind_count,omitempty"`
}

type TranscriptItem struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func ExtractSnapshot(ctx context.Context, classification model.SessionClassification, session model.SessionEvidence, gitStatus GitStatusSnapshot) (SessionSnapshot, error) {
	recoveredSession := session
	if strings.TrimSpace(recoveredSession.SessionFile) == "" {
		recoveredSession.SessionFile = classification.SessionFile
	}
	if strings.TrimSpace(recoveredSession.Format) == "" {
		recoveredSession.Format = classification.SessionFormat
	}
	_ = RecoverSessionTurnState(&recoveredSession)
	snapshot := SessionSnapshot{
		ProjectPath:          classification.ProjectPath,
		SessionID:            classification.SessionID,
		SessionFormat:        classification.SessionFormat,
		LastEventAt:          classification.SourceUpdatedAt.UTC().Format(time.RFC3339),
		LatestTurnStateKnown: recoveredSession.LatestTurnStateKnown,
		LatestTurnCompleted:  recoveredSession.LatestTurnCompleted,
		GitStatus:            gitStatus,
	}

	var (
		items []TranscriptItem
		err   error
	)
	switch classification.SessionFormat {
	case "modern", "legacy":
		items, err = extractCodexTranscript(classification.SessionFile)
	case "opencode_db":
		items, err = extractOpenCodeTranscript(ctx, classification.SessionFile)
	default:
		err = fmt.Errorf("unsupported session format: %s", classification.SessionFormat)
	}
	if err != nil {
		return SessionSnapshot{}, err
	}
	if len(items) == 0 {
		return SessionSnapshot{}, errors.New("no conversational transcript found")
	}
	snapshot.Transcript = items
	return snapshot, nil
}

// RecoverSessionTurnState backfills latest-turn lifecycle from the session artifact
// when the fast detector omitted it, which commonly happens on older Codex sessions.
func RecoverSessionTurnState(session *model.SessionEvidence) error {
	if session == nil || session.LatestTurnStateKnown || strings.TrimSpace(session.SessionFile) == "" {
		return nil
	}

	switch session.Format {
	case "modern", "legacy":
		state, err := detectCodexTurnLifecycle(session.SessionFile)
		if err != nil {
			return err
		}
		if !state.known {
			return nil
		}
		session.LatestTurnStateKnown = true
		session.LatestTurnCompleted = state.completed
		session.LatestTurnStartedAt = state.startedAt
	default:
	}

	return nil
}

func ExtractPreview(ctx context.Context, session model.SessionEvidence) (SessionPreview, error) {
	switch session.Format {
	case "modern", "legacy":
		return extractCodexPreview(session.SessionFile)
	case "opencode_db":
		return extractOpenCodePreview(ctx, session.SessionFile)
	default:
		return SessionPreview{}, fmt.Errorf("unsupported session format: %s", session.Format)
	}
}

func PreviewFromTranscript(items []TranscriptItem) SessionPreview {
	return previewFromTranscript(items)
}

func extractCodexPreview(path string) (SessionPreview, error) {
	headItems, err := extractCodexHeadTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	tailItems, err := extractCodexTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	items := append(headItems, tailItems...)
	if len(items) == 0 {
		return SessionPreview{}, errors.New("no conversational transcript found")
	}
	return previewFromTranscript(items), nil
}

func extractCodexHeadTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readHeadLines(path, previewHeadBytes)
	if err != nil {
		return nil, err
	}
	items := make([]TranscriptItem, 0, previewItemLimit)
	for _, line := range lines {
		item, ok := extractCodexTranscriptItem(line)
		if !ok {
			continue
		}
		items = append(items, item)
		if len(items) >= previewItemLimit {
			break
		}
	}
	return finalizeTranscript(items), nil
}

func readHeadLines(path string, maxBytes int64) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer file.Close()

	scanner := newSessionScanner(file)
	lines := []string{}
	var bytesRead int64
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		bytesRead += int64(len(line)) + 1
		if bytesRead >= maxBytes {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}
	return lines, nil
}

func extractOpenCodePreview(ctx context.Context, sessionRef string) (SessionPreview, error) {
	dbPath, sessionID, err := parseOpenCodeSessionRef(sessionRef)
	if err != nil {
		return SessionPreview{}, err
	}

	db, err := opencodesqlite.Open(dbPath)
	if err != nil {
		return SessionPreview{}, fmt.Errorf("open opencode sqlite: %w", err)
	}
	defer db.Close()

	headItems, err := extractOpenCodeTranscriptItems(ctx, db, sessionID, false, previewItemLimit)
	if err != nil {
		return SessionPreview{}, err
	}
	tailItems, err := extractOpenCodeTranscriptItems(ctx, db, sessionID, true, previewItemLimit)
	if err != nil {
		return SessionPreview{}, err
	}
	items := append(headItems, tailItems...)
	if len(items) == 0 {
		return SessionPreview{}, errors.New("no conversational transcript found")
	}
	return previewFromTranscript(items), nil
}

func NewGitStatusSnapshot(repoDirty bool, repoSyncStatus model.RepoSyncStatus, repoAheadCount, repoBehindCount int) GitStatusSnapshot {
	snapshot := GitStatusSnapshot{
		WorktreeDirty: repoDirty,
		RemoteStatus:  string(repoSyncStatus),
	}
	switch repoSyncStatus {
	case model.RepoSyncAhead:
		snapshot.AheadCount = repoAheadCount
	case model.RepoSyncBehind:
		snapshot.BehindCount = repoBehindCount
	case model.RepoSyncDiverged:
		snapshot.AheadCount = repoAheadCount
		snapshot.BehindCount = repoBehindCount
	}
	return snapshot
}

func extractCodexTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readTailLines(path, codexTailBytes)
	if err != nil {
		return nil, err
	}

	items := make([]TranscriptItem, 0, len(lines))
	for _, line := range lines {
		if item, ok := extractCodexTranscriptItem(line); ok {
			items = append(items, item)
		}
	}
	return finalizeTranscript(items), nil
}

type codexTurnLifecycle struct {
	known     bool
	completed bool
	startedAt time.Time
}

func detectCodexTurnLifecycle(path string) (codexTurnLifecycle, error) {
	lines, err := readTailLines(path, codexTailBytes)
	if err != nil {
		return codexTurnLifecycle{}, err
	}
	state := codexTurnLifecycle{}
	for _, line := range lines {
		event, ok := extractCodexTurnLifecycleEvent(line)
		if !ok {
			continue
		}
		state.known = true
		state.completed = event.completed
		if event.completed {
			state.startedAt = time.Time{}
			continue
		}
		state.startedAt = event.timestamp
	}
	return state, nil
}

func readTailLines(path string, maxBytes int64) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat session file: %w", err)
	}

	offset := int64(0)
	if stat.Size() > maxBytes {
		offset = stat.Size() - maxBytes
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek session file: %w", err)
	}

	scanner := newSessionScanner(file)

	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}
	return lines, nil
}

func newSessionScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return scanner
}

func extractCodexTranscriptItem(line string) (TranscriptItem, bool) {
	var top struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   []codexTextPart `json:"content"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return TranscriptItem{}, false
	}

	switch top.Type {
	case "message":
		text := extractCodexMessageText(top.Content)
		if top.Role == "" || text == "" {
			return TranscriptItem{}, false
		}
		return TranscriptItem{Role: top.Role, Text: text}, true
	case "response_item":
		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content []codexTextPart `json:"content"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil {
			return TranscriptItem{}, false
		}
		if payload.Type != "message" {
			return TranscriptItem{}, false
		}
		text := extractCodexMessageText(payload.Content)
		if payload.Role == "" || text == "" {
			return TranscriptItem{}, false
		}
		return TranscriptItem{Role: payload.Role, Text: text}, true
	case "event_msg":
		var payload struct {
			Type             string `json:"type"`
			Message          string `json:"message"`
			LastAgentMessage string `json:"last_agent_message"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil {
			return TranscriptItem{}, false
		}
		switch payload.Type {
		case "user_message":
			text := sanitizeTranscriptText(payload.Message)
			if text == "" {
				return TranscriptItem{}, false
			}
			return TranscriptItem{Role: "user", Text: text}, true
		case "agent_message":
			text := sanitizeTranscriptText(payload.Message)
			if text == "" {
				return TranscriptItem{}, false
			}
			return TranscriptItem{Role: "assistant", Text: text}, true
		case "task_complete":
			text := sanitizeTranscriptText(payload.LastAgentMessage)
			if text == "" {
				return TranscriptItem{}, false
			}
			return TranscriptItem{Role: "assistant", Text: text}, true
		default:
			return TranscriptItem{}, false
		}
	default:
		return TranscriptItem{}, false
	}
}

type codexTurnLifecycleEvent struct {
	completed bool
	timestamp time.Time
}

func extractCodexTurnLifecycleEvent(line string) (codexTurnLifecycleEvent, bool) {
	var top struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return codexTurnLifecycleEvent{}, false
	}
	if top.Type != "event_msg" {
		return codexTurnLifecycleEvent{}, false
	}
	timestamp := time.Time{}
	if parsed, err := time.Parse(time.RFC3339, top.Timestamp); err == nil {
		timestamp = parsed
	}
	switch top.Payload.Type {
	case "task_started":
		return codexTurnLifecycleEvent{completed: false, timestamp: timestamp}, true
	case "task_complete":
		return codexTurnLifecycleEvent{completed: true, timestamp: timestamp}, true
	default:
		return codexTurnLifecycleEvent{}, false
	}
}

type codexTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func extractCodexMessageText(parts []codexTextPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			if text := sanitizeTranscriptText(part.Text); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return sanitizeTranscriptText(strings.Join(texts, "\n\n"))
}

func extractOpenCodeTranscript(ctx context.Context, sessionRef string) ([]TranscriptItem, error) {
	dbPath, sessionID, err := parseOpenCodeSessionRef(sessionRef)
	if err != nil {
		return nil, err
	}

	db, err := opencodesqlite.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open opencode sqlite: %w", err)
	}
	defer db.Close()
	return extractOpenCodeTranscriptItems(ctx, db, sessionID, true, 12)
}

func extractOpenCodeTranscriptItems(ctx context.Context, db *sql.DB, sessionID string, newest bool, limit int) ([]TranscriptItem, error) {
	ordering := "ASC"
	cteName := "first_messages"
	if newest {
		ordering = "DESC"
		cteName = "recent_messages"
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		WITH %s AS (
			SELECT id, time_created, data
			FROM message
			WHERE session_id = ?
			ORDER BY time_created %s
			LIMIT %d
		)
		SELECT rm.id, rm.time_created, rm.data, p.time_created, p.data
		FROM %s rm
		LEFT JOIN part p ON p.message_id = rm.id
		ORDER BY rm.time_created ASC, p.time_created ASC
	`, cteName, ordering, limit, cteName), sessionID)
	if err != nil {
		return nil, fmt.Errorf("query opencode transcript: %w", err)
	}
	defer rows.Close()

	type messageEntry struct {
		ID   string
		Role string
		Text []string
	}

	order := []string{}
	entries := map[string]*messageEntry{}

	for rows.Next() {
		var (
			messageID      string
			messageCreated int64
			messageData    string
			partCreated    sql.NullInt64
			partData       sql.NullString
		)
		if err := rows.Scan(&messageID, &messageCreated, &messageData, &partCreated, &partData); err != nil {
			return nil, fmt.Errorf("scan opencode transcript: %w", err)
		}

		entry, ok := entries[messageID]
		if !ok {
			entry = &messageEntry{ID: messageID, Role: parseOpenCodeRole(messageData)}
			entries[messageID] = entry
			order = append(order, messageID)
		}
		if !partData.Valid {
			continue
		}
		if text := parseOpenCodePartText(partData.String); text != "" {
			entry.Text = append(entry.Text, text)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read opencode transcript: %w", err)
	}

	items := make([]TranscriptItem, 0, len(order))
	for _, id := range order {
		entry := entries[id]
		text := sanitizeTranscriptText(strings.Join(entry.Text, "\n\n"))
		if entry.Role == "" || text == "" {
			continue
		}
		items = append(items, TranscriptItem{
			Role: entry.Role,
			Text: text,
		})
	}
	return finalizeTranscript(items), nil
}

func parseOpenCodeSessionRef(sessionRef string) (string, string, error) {
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

func parseOpenCodeRole(messageData string) string {
	var payload struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(messageData), &payload); err != nil {
		return ""
	}
	return payload.Role
}

func parseOpenCodePartText(partData string) string {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(partData), &header); err != nil {
		return ""
	}
	switch header.Type {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		return sanitizeTranscriptText(payload.Text)
	case "reasoning":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		text := sanitizeTranscriptText(payload.Text)
		if text == "" {
			return ""
		}
		return "Reasoning: " + text
	case "tool":
		var payload struct {
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
				Title  string `json:"title"`
				Input  struct {
					Command     string `json:"command"`
					Description string `json:"description"`
				} `json:"input"`
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		toolName := sanitizeTranscriptText(payload.Tool)
		status := sanitizeTranscriptText(payload.State.Status)
		summary := summarizeOpenCodeToolDetail(
			toolName,
			payload.State.Input.Description,
			payload.State.Title,
			payload.State.Input.Command,
		)
		switch {
		case toolName != "" && status != "" && summary != "":
			return fmt.Sprintf("Tool %s %s: %s", toolName, status, summary)
		case toolName != "" && summary != "":
			return fmt.Sprintf("Tool %s: %s", toolName, summary)
		case summary != "":
			return "Tool: " + summary
		case toolName != "" && status != "":
			return fmt.Sprintf("Tool %s %s", toolName, status)
		case toolName != "":
			return "Tool " + toolName
		default:
			return "Tool activity"
		}
	case "patch":
		var payload struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		files := summarizeOpenCodePaths(payload.Files, 3)
		if len(files) == 0 {
			return "Patch applied"
		}
		return "Patch touched " + strings.Join(files, ", ")
	case "file":
		var payload struct {
			Mime     string `json:"mime"`
			Filename string `json:"filename"`
			Source   struct {
				Path string `json:"path"`
			} `json:"source"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		name := firstNonEmptyTranscriptValue(payload.Filename)
		if name == "" {
			name = firstNonEmptyTranscriptPathValue(payload.Source.Path)
		}
		mime := sanitizeTranscriptText(payload.Mime)
		switch {
		case name != "" && mime != "":
			return fmt.Sprintf("Attached file: %s (%s)", name, mime)
		case name != "":
			return "Attached file: " + name
		case mime != "":
			return "Attached file: " + mime
		default:
			return "Attached file"
		}
	case "step-finish":
		var payload struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return ""
		}
		reason := sanitizeTranscriptText(payload.Reason)
		if reason == "tool-calls" {
			return ""
		}
		if reason == "" {
			return "Step finished"
		}
		return "Step finished: " + reason
	case "compaction":
		return "Session compacted"
	default:
		return ""
	}
}

func summarizeOpenCodeToolDetail(toolName string, values ...string) string {
	for _, value := range values {
		text := sanitizeTranscriptText(value)
		if text == "" {
			continue
		}
		if looksLikeOpenCodePathSummary(text) && toolName != "bash" {
			text = sanitizeTranscriptText(filepath.Base(text))
		}
		if text != "" {
			return text
		}
	}
	return ""
}

func looksLikeOpenCodePathSummary(text string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, " ") {
		return false
	}
	return strings.ContainsAny(text, `/\`)
}

func firstNonEmptyTranscriptValue(values ...string) string {
	for _, value := range values {
		text := sanitizeTranscriptText(value)
		if text == "" {
			continue
		}
		if text != "" {
			return text
		}
	}
	return ""
}

func firstNonEmptyTranscriptPathValue(values ...string) string {
	for _, value := range values {
		text := sanitizeTranscriptText(value)
		if text == "" {
			continue
		}
		if strings.ContainsAny(text, `/\`) {
			text = sanitizeTranscriptText(filepath.Base(text))
		}
		if text != "" {
			return text
		}
	}
	return ""
}

func summarizeOpenCodePaths(paths []string, limit int) []string {
	out := make([]string, 0, len(paths))
	total := 0
	for _, path := range paths {
		name := firstNonEmptyTranscriptPathValue(path)
		if name == "" {
			continue
		}
		total++
		if limit <= 0 || len(out) < limit {
			out = append(out, name)
		}
	}
	if limit > 0 && total > len(out) && len(out) > 0 {
		out[len(out)-1] = fmt.Sprintf("%s (+%d more)", out[len(out)-1], total-len(out))
	}
	return out
}

func finalizeTranscript(items []TranscriptItem) []TranscriptItem {
	out := make([]TranscriptItem, 0, len(items))
	for _, item := range items {
		role := strings.TrimSpace(strings.ToLower(item.Role))
		text := sanitizeTranscriptText(item.Text)
		if role == "" || text == "" {
			continue
		}
		clean := TranscriptItem{Role: role, Text: text}
		if len(out) > 0 && out[len(out)-1] == clean {
			continue
		}
		out = append(out, clean)
	}
	if len(out) > maxTranscriptItems {
		out = out[len(out)-maxTranscriptItems:]
	}
	return out
}

func previewFromTranscript(items []TranscriptItem) SessionPreview {
	title := firstPreviewTitle(items)
	summary := lastPreviewSummary(items, title)
	switch {
	case title == "" && summary != "":
		title = summary
	case summary == "" && title != "":
		summary = title
	}
	return SessionPreview{Title: title, Summary: summary}
}

func firstPreviewTitle(items []TranscriptItem) string {
	for _, item := range items {
		if strings.TrimSpace(strings.ToLower(item.Role)) != "user" {
			continue
		}
		text := previewText(item.Text)
		if text == "" {
			continue
		}
		return text
	}
	for _, item := range items {
		text := previewText(item.Text)
		if text == "" {
			continue
		}
		return text
	}
	return ""
}

func lastPreviewSummary(items []TranscriptItem, title string) string {
	title = strings.TrimSpace(title)
	for i := len(items) - 1; i >= 0; i-- {
		if strings.TrimSpace(strings.ToLower(items[i].Role)) != "assistant" {
			continue
		}
		text := previewText(items[i].Text)
		if text == "" || strings.EqualFold(text, title) {
			continue
		}
		return text
	}
	for i := len(items) - 1; i >= 0; i-- {
		text := previewText(items[i].Text)
		if text == "" || strings.EqualFold(text, title) {
			continue
		}
		return text
	}
	return ""
}

func previewText(text string) string {
	text = sanitizeTranscriptText(text)
	lines := strings.Split(text, "\n")
	if previewScaffoldText(lines) {
		return ""
	}
	if line := previewLine(lines, true); line != "" {
		return truncatePreviewLine(line)
	}
	return truncatePreviewLine(previewLine(lines, false))
}

func previewScaffoldText(lines []string) bool {
	if len(lines) == 0 {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(lines[0]))
	switch {
	case lower == "":
		return true
	case strings.HasPrefix(lower, "<environment_context>"):
		return true
	case strings.HasPrefix(lower, "current working directory:"):
		return true
	case strings.HasPrefix(lower, "# agents.md instructions for "):
		return true
	default:
		return false
	}
}

func previewLine(lines []string, skipPresentation bool) string {
	for _, line := range lines {
		line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
		if line == "" {
			continue
		}
		if skipPresentation && previewPresentationLine(line) {
			continue
		}
		return line
	}
	return ""
}

func previewPresentationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return true
	case strings.HasPrefix(trimmed, "#"):
		return true
	case wrappedBy(trimmed, "**", "**"):
		return true
	case wrappedBy(trimmed, "__", "__"):
		return true
	case wrappedBy(trimmed, "*", "*"):
		return true
	case wrappedBy(trimmed, "_", "_"):
		return true
	default:
		return false
	}
}

func wrappedBy(text, prefix, suffix string) bool {
	if len(text) <= len(prefix)+len(suffix) {
		return false
	}
	return strings.HasPrefix(text, prefix) && strings.HasSuffix(text, suffix)
}

func truncatePreviewLine(text string) string {
	if len(text) <= maxPreviewBytes {
		return text
	}
	return text[:maxPreviewBytes-3] + "..."
}

func sanitizeTranscriptText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	text = strings.Join(out, "\n")
	if len(text) <= maxTranscriptBytes {
		return text
	}
	return text[:maxTranscriptBytes-3] + "..."
}
