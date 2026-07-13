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
	"unicode"

	"lcroom/internal/model"
	"lcroom/internal/opencodesqlite"
	"lcroom/internal/pasteplaceholder"
)

const (
	maxTranscriptItems = 8
	maxTranscriptBytes = 800
	codexTailBytes     = 1024 * 1024
	maxPreviewBytes    = 160
	previewHeadBytes   = 64 * 1024
	previewItemLimit   = 6
	transcriptOmission = " [...] "
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
	Role    string `json:"role"`
	Text    string `json:"text"`
	Visible bool   `json:"-"`
}

func ExtractSnapshot(ctx context.Context, classification model.SessionClassification, session model.SessionEvidence, gitStatus GitStatusSnapshot) (SessionSnapshot, error) {
	classification = model.NormalizeSessionClassificationIdentity(classification)
	recoveredSession := model.NormalizeSessionEvidenceIdentity(session)
	if strings.TrimSpace(recoveredSession.SessionFile) == "" {
		recoveredSession.SessionFile = classification.SessionFile
	}
	if strings.TrimSpace(recoveredSession.Format) == "" {
		recoveredSession.Format = classification.SessionFormat
	}
	_ = RecoverSessionTurnState(&recoveredSession)
	snapshot := SessionSnapshot{
		ProjectPath:          classification.ProjectPath,
		SessionID:            classification.ExternalID(),
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
	case "claude_code":
		items, err = extractClaudeCodeTranscript(classification.SessionFile)
	case "lcagent_jsonl":
		items, err = extractLCAgentTranscript(classification.SessionFile)
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

// RecoverSessionTurnState refreshes latest-turn lifecycle from the session
// artifact when persisted detector metadata is missing or stale.
func RecoverSessionTurnState(session *model.SessionEvidence) error {
	if session == nil || strings.TrimSpace(session.SessionFile) == "" {
		return nil
	}

	var (
		state codexTurnLifecycle
		err   error
	)
	switch session.Format {
	case "modern", "legacy":
		state, err = detectCodexTurnLifecycle(session.SessionFile)
	case "lcagent_jsonl":
		state, err = detectLCAgentTurnLifecycle(session.SessionFile)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if !state.known {
		return nil
	}
	session.LatestTurnStateKnown = true
	session.LatestTurnCompleted = state.completed
	session.LatestTurnStartedAt = state.startedAt

	return nil
}

func ExtractPreview(ctx context.Context, session model.SessionEvidence) (SessionPreview, error) {
	switch session.Format {
	case "modern", "legacy":
		return extractCodexPreview(session.SessionFile)
	case "opencode_db":
		return extractOpenCodePreview(ctx, session.SessionFile)
	case "claude_code":
		return extractClaudeCodePreview(session.SessionFile)
	case "lcagent_jsonl":
		return extractLCAgentPreview(session.SessionFile)
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
	provisionalTurnStart := time.Time{}
	for _, line := range lines {
		event, ok := extractCodexTurnLifecycleEvent(line)
		if ok {
			if event.completed {
				state.known = true
				state.completed = true
				state.startedAt = time.Time{}
				provisionalTurnStart = time.Time{}
			} else {
				provisionalTurnStart = event.timestamp
			}
			continue
		}
		if !provisionalTurnStart.IsZero() && isMeaningfulCodexTurnActivityLine(line) {
			state.known = true
			state.completed = false
			state.startedAt = provisionalTurnStart
		}
	}
	return state, nil
}

func detectLCAgentTurnLifecycle(path string) (codexTurnLifecycle, error) {
	lines, err := readTailLines(path, codexTailBytes)
	if err != nil {
		return codexTurnLifecycle{}, err
	}
	state := codexTurnLifecycle{}
	for _, line := range lines {
		event, ok := extractLCAgentTurnLifecycleEvent(line)
		if !ok {
			continue
		}
		state.known = true
		state.completed = event.completed
		if event.completed {
			state.startedAt = time.Time{}
		} else {
			state.startedAt = event.timestamp
		}
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
		return TranscriptItem{Role: top.Role, Text: text, Visible: true}, true
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
		// Codex response-item user messages are model-context inputs, not
		// user-visible transcript events. Project instructions and environment
		// context can appear here; actual prompts use event_msg/user_message.
		if strings.EqualFold(strings.TrimSpace(payload.Role), "user") {
			return TranscriptItem{}, false
		}
		text := extractCodexMessageText(payload.Content)
		if payload.Role == "" || text == "" {
			return TranscriptItem{}, false
		}
		return TranscriptItem{Role: payload.Role, Text: text, Visible: true}, true
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
			return TranscriptItem{Role: "user", Text: text, Visible: true}, true
		case "agent_message":
			text := sanitizeTranscriptText(payload.Message)
			if text == "" {
				return TranscriptItem{}, false
			}
			return TranscriptItem{Role: "assistant", Text: text, Visible: true}, true
		case "task_complete":
			text := sanitizeTranscriptText(payload.LastAgentMessage)
			if text == "" {
				return TranscriptItem{}, false
			}
			return TranscriptItem{Role: "assistant", Text: text, Visible: true}, true
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
	case "task_complete", "turn_aborted":
		return codexTurnLifecycleEvent{completed: true, timestamp: timestamp}, true
	default:
		return codexTurnLifecycleEvent{}, false
	}
}

func extractLCAgentTurnLifecycleEvent(line string) (codexTurnLifecycleEvent, bool) {
	var event struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return codexTurnLifecycleEvent{}, false
	}
	timestamp := time.Time{}
	if parsed, err := time.Parse(time.RFC3339Nano, event.Timestamp); err == nil {
		timestamp = parsed
	} else if parsed, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
		timestamp = parsed
	}
	switch event.Type {
	case "user_message":
		return codexTurnLifecycleEvent{completed: false, timestamp: timestamp}, true
	case "turn_complete", "turn_aborted":
		return codexTurnLifecycleEvent{completed: true, timestamp: timestamp}, true
	default:
		return codexTurnLifecycleEvent{}, false
	}
}

func isMeaningfulCodexTurnActivityLine(line string) bool {
	var top struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return false
	}

	switch top.Type {
	case "event_msg":
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil {
			return false
		}
		switch payload.Type {
		case "", "task_started", "task_complete", "turn_aborted", "token_count":
			return false
		default:
			return true
		}
	case "response_item":
		var payload struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal(top.Payload, &payload); err != nil {
			return false
		}
		if payload.Type == "" {
			return false
		}
		if payload.Type == "message" {
			role := strings.ToLower(strings.TrimSpace(payload.Role))
			return role != "" && role != "developer" && role != "system"
		}
		return true
	case "message":
		role := strings.ToLower(strings.TrimSpace(top.Role))
		return role != "" && role != "developer" && role != "system"
	default:
		return false
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
		ID         string
		Role       string
		TextParts  []string
		OtherParts []string
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
		kind, text := parseOpenCodePartText(partData.String)
		if text == "" {
			continue
		}
		if kind == "text" {
			entry.TextParts = append(entry.TextParts, text)
		} else {
			entry.OtherParts = append(entry.OtherParts, text)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read opencode transcript: %w", err)
	}

	items := make([]TranscriptItem, 0, len(order))
	for _, id := range order {
		entry := entries[id]
		parts := entry.OtherParts
		if entry.Role == "assistant" && len(entry.TextParts) > 0 {
			parts = entry.TextParts
		} else if len(entry.TextParts) > 0 {
			parts = append(append([]string(nil), entry.TextParts...), entry.OtherParts...)
		}
		text := sanitizeTranscriptText(strings.Join(parts, "\n\n"))
		if entry.Role == "" || text == "" {
			continue
		}
		items = append(items, TranscriptItem{
			Role:    entry.Role,
			Text:    text,
			Visible: entry.Role != "assistant" || len(entry.TextParts) > 0,
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

func parseOpenCodePartText(partData string) (string, string) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(partData), &header); err != nil {
		return "", ""
	}
	switch header.Type {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return "", ""
		}
		return "text", sanitizeTranscriptText(payload.Text)
	case "reasoning":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return "", ""
		}
		text := sanitizeTranscriptText(payload.Text)
		if text == "" {
			return "", ""
		}
		return "reasoning", "Reasoning: " + text
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
			return "", ""
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
			return "tool", fmt.Sprintf("Tool %s %s: %s", toolName, status, summary)
		case toolName != "" && summary != "":
			return "tool", fmt.Sprintf("Tool %s: %s", toolName, summary)
		case summary != "":
			return "tool", "Tool: " + summary
		case toolName != "" && status != "":
			return "tool", fmt.Sprintf("Tool %s %s", toolName, status)
		case toolName != "":
			return "tool", "Tool " + toolName
		default:
			return "tool", "Tool activity"
		}
	case "patch":
		var payload struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return "", ""
		}
		files := summarizeOpenCodePaths(payload.Files, 3)
		if len(files) == 0 {
			return "patch", "Patch applied"
		}
		return "patch", "Patch touched " + strings.Join(files, ", ")
	case "file":
		var payload struct {
			Mime     string `json:"mime"`
			Filename string `json:"filename"`
			Source   struct {
				Path string `json:"path"`
			} `json:"source"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return "", ""
		}
		name := firstNonEmptyTranscriptValue(payload.Filename)
		if name == "" {
			name = firstNonEmptyTranscriptPathValue(payload.Source.Path)
		}
		mime := sanitizeTranscriptText(payload.Mime)
		switch {
		case name != "" && mime != "":
			return "file", fmt.Sprintf("Attached file: %s (%s)", name, mime)
		case name != "":
			return "file", "Attached file: " + name
		case mime != "":
			return "file", "Attached file: " + mime
		default:
			return "file", "Attached file"
		}
	case "step-finish":
		var payload struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(partData), &payload); err != nil {
			return "", ""
		}
		reason := sanitizeTranscriptText(payload.Reason)
		if reason == "tool-calls" {
			return "", ""
		}
		if reason == "" {
			return "step-finish", "Step finished"
		}
		return "step-finish", "Step finished: " + reason
	case "compaction":
		return "compaction", "Session compacted"
	default:
		return "", ""
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
		clean := TranscriptItem{Role: role, Text: text, Visible: item.Visible}
		if len(out) > 0 && out[len(out)-1] == clean {
			continue
		}
		out = append(out, clean)
	}
	out = collapseAssistantRuns(out)
	if len(out) > maxTranscriptItems {
		out = out[len(out)-maxTranscriptItems:]
	}
	return out
}

func collapseAssistantRuns(items []TranscriptItem) []TranscriptItem {
	if len(items) <= 1 {
		return items
	}

	out := make([]TranscriptItem, 0, len(items))
	for i := 0; i < len(items); {
		if items[i].Role != "assistant" {
			out = append(out, items[i])
			i++
			continue
		}

		j := i + 1
		lastVisible := -1
		for j <= len(items) {
			if items[j-1].Visible {
				lastVisible = j - 1
			}
			if j == len(items) || items[j].Role != "assistant" {
				break
			}
			j++
		}

		switch {
		case lastVisible >= 0:
			out = append(out, items[lastVisible])
		default:
			out = append(out, items[j-1])
		}
		i = j
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
	text = sanitizeTranscriptText(pasteplaceholder.Strip(text))
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

func extractClaudeCodeTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readTailLines(path, codexTailBytes)
	if err != nil {
		return nil, err
	}
	items := make([]TranscriptItem, 0, len(lines))
	for _, line := range lines {
		if item, ok := extractClaudeCodeTranscriptItem(line); ok {
			items = append(items, item)
		}
	}
	return finalizeTranscript(items), nil
}

func extractLCAgentTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readTailLines(path, codexTailBytes)
	if err != nil {
		return nil, err
	}
	items := make([]TranscriptItem, 0, len(lines))
	for _, line := range lines {
		if item, ok := extractLCAgentTranscriptItem(line); ok {
			items = append(items, item)
		}
	}
	return finalizeTranscript(items), nil
}

func extractLCAgentPreview(path string) (SessionPreview, error) {
	headItems, err := extractLCAgentHeadTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	tailItems, err := extractLCAgentTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	items := append(headItems, tailItems...)
	if len(items) == 0 {
		return SessionPreview{}, errors.New("no conversational transcript found")
	}
	return previewFromTranscript(items), nil
}

func extractLCAgentHeadTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readHeadLines(path, previewHeadBytes)
	if err != nil {
		return nil, err
	}
	items := make([]TranscriptItem, 0, previewItemLimit)
	for _, line := range lines {
		item, ok := extractLCAgentTranscriptItem(line)
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

type lcagentPlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

func extractLCAgentTranscriptItem(line string) (TranscriptItem, bool) {
	var event struct {
		Type               string            `json:"type"`
		Message            string            `json:"message"`
		Summary            string            `json:"summary"`
		Reason             string            `json:"reason"`
		Tool               string            `json:"tool"`
		Result             json.RawMessage   `json:"result"`
		Items              []lcagentPlanItem `json:"items"`
		Files              []string          `json:"files"`
		FilesChanged       []string          `json:"files_changed"`
		Verification       []string          `json:"verification"`
		Status             string            `json:"status"`
		Command            string            `json:"command"`
		Argv               []string          `json:"argv"`
		CWD                string            `json:"cwd"`
		Path               string            `json:"path"`
		Stage              string            `json:"stage"`
		Kind               string            `json:"kind"`
		Count              int               `json:"count"`
		SourceSessionID    string            `json:"source_session_id"`
		SourcePath         string            `json:"source_path"`
		SourceCWD          string            `json:"source_cwd"`
		FinalOutcome       string            `json:"final_outcome"`
		VerificationStatus string            `json:"verification_status"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return TranscriptItem{}, false
	}

	switch event.Type {
	case "user_message":
		return lcagentTranscriptItem("user", event.Message, true)
	case "assistant_message":
		return lcagentTranscriptItem("assistant", lcagentFinalText(event.Message, event.FinalOutcome, "", event.FilesChanged, event.Verification), true)
	case "final_response_audit":
		return lcagentTranscriptItem("assistant", lcagentFinalText("", event.FinalOutcome, "", nil, nil), true)
	case "turn_complete":
		return lcagentTranscriptItem("assistant", lcagentFinalText(event.Summary, event.FinalOutcome, event.VerificationStatus, event.FilesChanged, event.Verification), true)
	case "turn_aborted":
		return lcagentTranscriptItem("assistant", "turn aborted: "+event.Reason, true)
	case "tool_call":
		return lcagentTranscriptItem("assistant", lcagentToolCallText(event.Tool), false)
	case "tool_result":
		return lcagentTranscriptItem("assistant", lcagentToolResultText(event.Tool, event.Result), false)
	case "plan_update":
		return lcagentTranscriptItem("assistant", lcagentPlanText(event.Items), false)
	case "files_touched":
		return lcagentTranscriptItem("assistant", lcagentFilesText("files touched", event.Files), false)
	case "resume_context":
		return lcagentTranscriptItem("assistant", lcagentResumeContextText(event.SourceSessionID, event.SourcePath, event.SourceCWD, event.Summary), false)
	case "permission_denied":
		return lcagentTranscriptItem("assistant", lcagentPermissionDeniedText(event.Tool, event.Reason), false)
	case "patch_diff_summary":
		return lcagentTranscriptItem("assistant", lcagentPatchDiffSummaryText(event.Summary), false)
	case "patch_feedback":
		return lcagentTranscriptItem("assistant", lcagentPatchFeedbackText(event.Path, event.Stage, event.Message), false)
	case "verification_check":
		return lcagentTranscriptItem("assistant", lcagentVerificationCheckText(event.Command, event.Argv, event.CWD, event.Status), false)
	case "verification_feedback":
		return lcagentTranscriptItem("assistant", lcagentVerificationFeedbackText(event.Command, event.Status, event.Message), false)
	case "repair_feedback_suppressed":
		return lcagentTranscriptItem("assistant", lcagentRepairFeedbackSuppressedText(event.Kind, event.Message, event.Count), false)
	case "verification_summary":
		return lcagentTranscriptItem("assistant", lcagentVerificationSummaryText(event.Status, event.Message), false)
	default:
		return TranscriptItem{}, false
	}
}

func lcagentTranscriptItem(role, text string, visible bool) (TranscriptItem, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	text = sanitizeTranscriptText(text)
	switch role {
	case "user", "assistant":
	default:
		return TranscriptItem{}, false
	}
	if text == "" {
		return TranscriptItem{}, false
	}
	return TranscriptItem{Role: role, Text: text, Visible: visible}, true
}

func lcagentToolCallText(tool string) string {
	tool = sanitizeTranscriptText(tool)
	if tool == "" {
		return ""
	}
	return "tool call: " + tool
}

func lcagentToolResultText(tool string, raw json.RawMessage) string {
	var result struct {
		Success      bool     `json:"success"`
		Error        string   `json:"error"`
		Command      string   `json:"command"`
		CWD          string   `json:"cwd"`
		ExitCode     int      `json:"exit_code"`
		TimedOut     bool     `json:"timed_out"`
		Truncated    bool     `json:"truncated"`
		Binary       bool     `json:"binary"`
		ArtifactPath string   `json:"artifact_path"`
		FilesTouched []string `json:"files_touched"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &result)
	}

	tool = sanitizeTranscriptText(tool)
	status := "succeeded"
	if !result.Success {
		status = "failed"
	}
	parts := []string{"tool result"}
	if tool != "" {
		parts[0] = "tool result: " + tool
	}
	parts = append(parts, status)
	if tool == "run_command" {
		if result.Command = sanitizeTranscriptText(result.Command); result.Command != "" {
			parts = append(parts, "command: "+result.Command)
		}
		if result.CWD = sanitizeTranscriptText(result.CWD); result.CWD != "" {
			parts = append(parts, "cwd: "+result.CWD)
		}
	}
	if result.Error = sanitizeTranscriptText(result.Error); result.Error != "" {
		parts = append(parts, "error: "+result.Error)
	}
	if result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit code: %d", result.ExitCode))
	}
	if result.TimedOut {
		parts = append(parts, "timed out")
	}
	if result.Truncated {
		parts = append(parts, "output truncated")
	}
	if result.Binary {
		parts = append(parts, "binary output")
	}
	if result.ArtifactPath = sanitizeTranscriptText(result.ArtifactPath); result.ArtifactPath != "" {
		parts = append(parts, "artifact: "+result.ArtifactPath)
	}
	if files := lcagentFilesText("files touched", result.FilesTouched); files != "" {
		parts = append(parts, files)
	}
	return strings.Join(parts, "; ")
}

func lcagentPlanText(items []lcagentPlanItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		step := sanitizeTranscriptText(item.Step)
		if step == "" {
			continue
		}
		status := sanitizeTranscriptText(item.Status)
		if status != "" {
			step = status + ": " + step
		}
		parts = append(parts, step)
	}
	if len(parts) == 0 {
		return ""
	}
	return "plan: " + strings.Join(parts, "; ")
}

func lcagentFinalText(summary, outcome, verificationStatus string, filesChanged, verification []string) string {
	parts := []string{}
	if summary = sanitizeTranscriptText(summary); summary != "" {
		parts = append(parts, summary)
	}
	if outcome = sanitizeTranscriptText(outcome); outcome != "" {
		parts = append(parts, "outcome: "+outcome)
	}
	if verificationStatus = sanitizeTranscriptText(verificationStatus); verificationStatus != "" {
		parts = append(parts, "verification status: "+verificationStatus)
	}
	if files := lcagentFilesText("files changed", filesChanged); files != "" {
		parts = append(parts, files)
	}
	if checks := lcagentFilesText("verification", verification); checks != "" {
		parts = append(parts, checks)
	}
	return strings.Join(parts, "; ")
}

func lcagentPermissionDeniedText(tool, reason string) string {
	reason = sanitizeTranscriptText(reason)
	tool = sanitizeTranscriptText(tool)
	if reason == "" && tool != "" {
		reason = tool + " denied"
	}
	if reason == "" {
		reason = "permission denied"
	}
	return "permission denied: " + reason
}

func lcagentResumeContextText(sourceID, sourcePath, sourceCWD, summary string) string {
	parts := []string{"resume context"}
	if sourceID = sanitizeTranscriptText(sourceID); sourceID != "" {
		parts = append(parts, "source session: "+sourceID)
	}
	if sourcePath = sanitizeTranscriptText(sourcePath); sourcePath != "" {
		parts = append(parts, "source artifact: "+sourcePath)
	}
	if sourceCWD = sanitizeTranscriptText(sourceCWD); sourceCWD != "" {
		parts = append(parts, "source workspace: "+sourceCWD)
	}
	if summary = sanitizeTranscriptText(summary); summary != "" {
		parts = append(parts, summary)
	}
	return strings.Join(parts, "; ")
}

func lcagentPatchDiffSummaryText(summary string) string {
	summary = sanitizeTranscriptText(summary)
	if summary == "" {
		return ""
	}
	return "patch diff summary: " + summary
}

func lcagentPatchFeedbackText(path, stage, message string) string {
	message = sanitizeTranscriptText(message)
	if message != "" {
		return message
	}
	path = sanitizeTranscriptText(path)
	stage = lcagentFirstNonEmpty(sanitizeTranscriptText(stage), "apply_patch")
	if path != "" {
		return "patch feedback: " + path + " failed during " + stage
	}
	return "patch feedback: " + stage
}

func lcagentVerificationCheckText(command string, argv []string, cwd string, status string) string {
	command = lcagentFirstNonEmpty(sanitizeTranscriptText(command), sanitizeTranscriptText(strings.Join(argv, " ")), "verification check")
	if cwd = sanitizeTranscriptText(cwd); cwd != "" {
		command += " in " + cwd
	}
	status = lcagentFirstNonEmpty(sanitizeTranscriptText(status), "unknown")
	return "verification " + status + ": " + command
}

func lcagentVerificationFeedbackText(command, status, message string) string {
	message = sanitizeTranscriptText(message)
	if message != "" {
		return message
	}
	command = sanitizeTranscriptText(command)
	status = lcagentFirstNonEmpty(sanitizeTranscriptText(status), "needs attention")
	if command != "" {
		return "verification feedback: " + command + " is " + status
	}
	return "verification feedback: " + status
}

func lcagentRepairFeedbackSuppressedText(kind, message string, count int) string {
	kind = lcagentFirstNonEmpty(sanitizeTranscriptText(kind), "repair")
	message = sanitizeTranscriptText(message)
	if count > 0 && message != "" {
		return fmt.Sprintf("suppressed duplicate %s feedback after %d repeats: %s", kind, count, message)
	}
	if message != "" {
		return "suppressed duplicate " + kind + " feedback: " + message
	}
	return "suppressed duplicate " + kind + " feedback"
}

func lcagentVerificationSummaryText(status, message string) string {
	message = sanitizeTranscriptText(message)
	if message != "" {
		return message
	}
	status = sanitizeTranscriptText(status)
	if status == "" {
		return ""
	}
	return "verification status: " + status
}

func lcagentFilesText(label string, files []string) string {
	cleaned := make([]string, 0, len(files))
	for _, file := range files {
		file = sanitizeTranscriptText(file)
		if file != "" {
			cleaned = append(cleaned, file)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) > 6 {
		hidden := len(cleaned) - 6
		cleaned = append(cleaned[:5], fmt.Sprintf("%s (+%d more)", cleaned[5], hidden))
	}
	return sanitizeTranscriptText(label) + ": " + strings.Join(cleaned, ", ")
}

func lcagentFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func extractClaudeCodePreview(path string) (SessionPreview, error) {
	headItems, err := extractClaudeCodeHeadTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	tailItems, err := extractClaudeCodeTranscript(path)
	if err != nil {
		return SessionPreview{}, err
	}
	items := append(headItems, tailItems...)
	if len(items) == 0 {
		return SessionPreview{}, errors.New("no conversational transcript found")
	}
	return previewFromTranscript(items), nil
}

func extractClaudeCodeHeadTranscript(path string) ([]TranscriptItem, error) {
	lines, err := readHeadLines(path, previewHeadBytes)
	if err != nil {
		return nil, err
	}
	items := make([]TranscriptItem, 0, previewItemLimit)
	for _, line := range lines {
		item, ok := extractClaudeCodeTranscriptItem(line)
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

func extractClaudeCodeTranscriptItem(line string) (TranscriptItem, bool) {
	var raw struct {
		Type    string `json:"type"`
		IsMeta  bool   `json:"isMeta"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return TranscriptItem{}, false
	}
	if raw.IsMeta {
		return TranscriptItem{}, false
	}

	switch raw.Type {
	case "user":
		text := extractClaudeCodeTextContent(raw.Message.Content)
		if text == "" {
			return TranscriptItem{}, false
		}
		return TranscriptItem{Role: "user", Text: text, Visible: true}, true
	case "assistant":
		text := extractClaudeCodeAssistantText(raw.Message.Content)
		if text == "" {
			return TranscriptItem{}, false
		}
		return TranscriptItem{Role: "assistant", Text: text, Visible: true}, true
	default:
		return TranscriptItem{}, false
	}
}

func extractClaudeCodeTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return sanitizeTranscriptText(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			if t := sanitizeTranscriptText(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func extractClaudeCodeAssistantText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := sanitizeTranscriptText(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			summary := ccToolSummaryForClassifier(b.Name, b.Input)
			if summary != "" {
				parts = append(parts, fmt.Sprintf("Tool %s: %s", b.Name, summary))
			} else {
				parts = append(parts, "Tool "+b.Name)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func ccToolSummaryForClassifier(name string, input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	switch name {
	case "Read", "Edit", "Write":
		return ccExtractStringField(fields, "file_path")
	case "Bash":
		cmd := ccExtractStringField(fields, "command")
		if len(cmd) > 120 {
			return cmd[:120] + "..."
		}
		return cmd
	case "Glob", "Grep":
		return ccExtractStringField(fields, "pattern")
	case "Agent":
		return ccExtractStringField(fields, "description")
	default:
		return ""
	}
}

func ccExtractStringField(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return sanitizeTranscriptText(s)
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
	return truncateTranscriptText(text, maxTranscriptBytes)
}

func truncateTranscriptText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	marker := []rune(transcriptOmission)
	if limit <= len(marker)+8 {
		return truncateTextEnd(text, limit, "...")
	}

	budget := limit - len(marker)
	headBudget := (budget * 3) / 5
	if headBudget < 1 {
		headBudget = budget / 2
	}
	tailBudget := budget - headBudget
	headEnd := headBudget
	tailStart := len(runes) - tailBudget
	headEnd, tailStart = adjustTranscriptSplit(runes, headEnd, tailStart)

	head := strings.TrimRightFunc(string(runes[:headEnd]), unicode.IsSpace)
	tail := strings.TrimLeftFunc(string(runes[tailStart:]), unicode.IsSpace)
	if head == "" || tail == "" {
		return truncateTextEnd(text, limit, "...")
	}
	return head + transcriptOmission + tail
}

func adjustTranscriptSplit(runes []rune, headEnd, tailStart int) (int, int) {
	if headEnd <= 0 || tailStart >= len(runes) || headEnd >= tailStart {
		return max(1, min(headEnd, len(runes))), max(0, min(tailStart, len(runes)))
	}

	if splitInsideWord(runes, headEnd) {
		if boundary := lastWhitespaceBefore(runes, headEnd); boundary > 0 {
			headEnd = boundary
		}
	}
	if splitInsideWord(runes, tailStart) {
		if boundary := firstWhitespaceAfter(runes, tailStart); boundary >= 0 && boundary < len(runes) {
			tailStart = boundary
		}
	}
	if headEnd <= 0 || tailStart >= len(runes) || headEnd >= tailStart {
		return max(1, min(headEnd, len(runes))), max(0, min(tailStart, len(runes)))
	}
	return headEnd, tailStart
}

func splitInsideWord(runes []rune, idx int) bool {
	if idx <= 0 || idx >= len(runes) {
		return false
	}
	return transcriptWordRune(runes[idx-1]) && transcriptWordRune(runes[idx])
}

func transcriptWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lastWhitespaceBefore(runes []rune, idx int) int {
	for i := idx - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}

func firstWhitespaceAfter(runes []rune, idx int) int {
	for i := idx; i < len(runes); i++ {
		if unicode.IsSpace(runes[i]) {
			return i + 1
		}
	}
	return -1
}

func truncateTextEnd(text string, limit int, marker string) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	markerRunes := []rune(marker)
	if limit <= len(markerRunes) {
		return string(runes[:limit])
	}
	return string(runes[:limit-len(markerRunes)]) + marker
}
