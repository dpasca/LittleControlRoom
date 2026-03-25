package codexapp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type claudeCodeSession struct {
	projectPath string
	notify      func()

	mu             sync.Mutex
	claudeHome     string
	sessionFile    string
	sessionID      string
	started        bool
	closed         bool
	busyExternal   bool
	lastActivityAt time.Time
	model          string
	entries        []TranscriptEntry
	lastFileSize   int64
}

func newClaudeCodeSession(req LaunchRequest, notify func()) (Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	claudeHome := filepath.Join(home, ".claude")

	s := &claudeCodeSession{
		projectPath: req.ProjectPath,
		notify:      notify,
		claudeHome:  claudeHome,
	}

	sessionFile, sessionID, ok := s.findLatestSession()
	if !ok {
		return nil, fmt.Errorf("no Claude Code session found for %s", req.ProjectPath)
	}
	s.sessionFile = sessionFile
	s.sessionID = sessionID
	s.started = true

	s.loadTranscript()
	s.refreshActive()

	return s, nil
}

func (s *claudeCodeSession) ProjectPath() string {
	return s.projectPath
}

func (s *claudeCodeSession) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	phase := SessionPhaseIdle
	switch {
	case s.closed:
		phase = SessionPhaseClosed
	case s.busyExternal:
		phase = SessionPhaseExternal
	}

	return Snapshot{
		Provider:       ProviderClaudeCode,
		ProjectPath:    s.projectPath,
		ThreadID:       s.sessionID,
		Phase:          phase,
		Started:        s.started,
		Busy:           s.busyExternal,
		BusyExternal:   s.busyExternal,
		Closed:         s.closed,
		Entries:        append([]TranscriptEntry(nil), s.entries...),
		LastActivityAt: s.lastActivityAt,
		Model:          s.model,
		Status:         "Claude Code (read-only)",
		LastSystemNotice: "Claude Code sessions are read-only. Use a terminal to interact with Claude Code.",
	}
}

func (s *claudeCodeSession) Submit(_ string) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) SubmitInput(_ Submission) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) ShowStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadTranscript()
	s.refreshActive()
	s.notify()
	return nil
}

func (s *claudeCodeSession) ListModels() ([]ModelOption, error) {
	return nil, nil
}

func (s *claudeCodeSession) StageModelOverride(_, _ string) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) Interrupt() error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) RespondApproval(_ ApprovalDecision) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) RespondToolInput(_ map[string][]string) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) RespondElicitation(_ ElicitationDecision, _ json.RawMessage) error {
	return fmt.Errorf("Claude Code sessions are read-only")
}

func (s *claudeCodeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// RefreshBusyElsewhere implements busyElsewhereRefresher.
func (s *claudeCodeSession) RefreshBusyElsewhere() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadTranscript()
	s.refreshActive()
	s.notify()
	return nil
}

// ReconcileBusyState implements busyReconciler.
func (s *claudeCodeSession) ReconcileBusyState() error {
	return s.RefreshBusyElsewhere()
}

func (s *claudeCodeSession) findLatestSession() (path string, sessionID string, ok bool) {
	projectsDir := filepath.Join(s.claudeHome, "projects")
	encodedPath := encodeCCProjectPath(s.projectPath)
	projectDir := filepath.Join(projectsDir, encodedPath)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", "", false
	}

	var bestPath string
	var bestMod time.Time
	var bestID string

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestMod) {
			bestPath = filepath.Join(projectDir, e.Name())
			bestMod = info.ModTime()
			bestID = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	if bestPath == "" {
		return "", "", false
	}
	return bestPath, bestID, true
}

func (s *claudeCodeSession) loadTranscript() {
	file, err := os.Open(s.sessionFile)
	if err != nil {
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return
	}
	if stat.Size() == s.lastFileSize {
		return
	}

	// Re-read the full file. For very large files we could optimize to
	// only read from lastFileSize, but for now simplicity wins.
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var entries []TranscriptEntry
	lastType := ""
	for sc.Scan() {
		line := sc.Text()
		entry, entryType, ok := parseCCLine(line)
		if ok {
			entries = append(entries, entry)
		}
		if entryType != "" {
			lastType = entryType
		}
	}

	s.entries = entries
	s.lastFileSize = stat.Size()

	// Determine if session looks active based on last entry type.
	switch lastType {
	case "assistant", "progress":
		s.busyExternal = true
	default:
		s.busyExternal = false
	}

	if len(entries) > 0 {
		// Use file mod time as last activity.
		s.lastActivityAt = stat.ModTime()
	}
}

func (s *claudeCodeSession) refreshActive() {
	// Check if any PID session file references our sessionID.
	sessionsDir := filepath.Join(s.claudeHome, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}
	active := false
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var pidSession struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(data, &pidSession); err != nil {
			continue
		}
		if pidSession.SessionID != s.sessionID {
			continue
		}
		if pidSession.PID > 0 && syscall.Kill(pidSession.PID, 0) == nil {
			active = true
			break
		}
	}
	s.busyExternal = active
}

func parseCCLine(line string) (TranscriptEntry, string, bool) {
	var raw struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsMeta  bool   `json:"isMeta"`
		UUID    string `json:"uuid"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Model   string          `json:"model"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return TranscriptEntry{}, "", false
	}

	if raw.IsMeta {
		return TranscriptEntry{}, raw.Type, false
	}

	switch raw.Type {
	case "user":
		text := extractCCTextContent(raw.Message.Content)
		if strings.TrimSpace(text) == "" {
			return TranscriptEntry{}, raw.Type, false
		}
		return TranscriptEntry{
			ItemID: raw.UUID,
			Kind:   TranscriptUser,
			Text:   text,
		}, raw.Type, true

	case "assistant":
		text, toolLines := extractCCAssistantContent(raw.Message.Content)
		combined := text
		if len(toolLines) > 0 {
			if combined != "" {
				combined += "\n"
			}
			combined += strings.Join(toolLines, "\n")
		}
		if strings.TrimSpace(combined) == "" {
			return TranscriptEntry{}, raw.Type, false
		}
		return TranscriptEntry{
			ItemID: raw.UUID,
			Kind:   TranscriptAgent,
			Text:   combined,
		}, raw.Type, true

	case "progress":
		return TranscriptEntry{}, raw.Type, false
	case "system":
		return TranscriptEntry{}, raw.Type, false
	default:
		return TranscriptEntry{}, raw.Type, false
	}
}

func extractCCTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
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
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractCCAssistantContent(content json.RawMessage) (text string, toolLines []string) {
	if len(content) == 0 {
		return "", nil
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", nil
	}
	var textParts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				textParts = append(textParts, b.Text)
			}
		case "tool_use":
			summary := ccToolSummary(b.Name, b.Input)
			if summary != "" {
				toolLines = append(toolLines, fmt.Sprintf("[%s] %s", b.Name, summary))
			} else {
				toolLines = append(toolLines, fmt.Sprintf("[%s]", b.Name))
			}
		}
	}
	return strings.Join(textParts, "\n"), toolLines
}

func ccToolSummary(name string, input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	switch name {
	case "Read", "Edit", "Write":
		return ccExtractString(fields, "file_path")
	case "Bash":
		cmd := ccExtractString(fields, "command")
		if len(cmd) > 120 {
			return cmd[:120] + "..."
		}
		return cmd
	case "Glob", "Grep":
		return ccExtractString(fields, "pattern")
	case "Agent":
		return ccExtractString(fields, "description")
	default:
		return ""
	}
}

func ccExtractString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func encodeCCProjectPath(projectPath string) string {
	// Claude Code encodes project paths by replacing "/" with "-" and stripping
	// the leading slash, e.g. "/Users/davide/dev/repos/Foo" → "-Users-davide-dev-repos-Foo".
	cleaned := filepath.Clean(projectPath)
	return strings.ReplaceAll(cleaned, "/", "-")
}
