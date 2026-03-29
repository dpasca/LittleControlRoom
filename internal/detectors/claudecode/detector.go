package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

type Detector struct {
	claudeHome     string
	errorTailBytes int64

	mu    sync.Mutex
	cache map[string]cachedParse
}

type cachedParse struct {
	modTimeUnix int64
	result      parseResult
}

type parseResult struct {
	sessionID   string
	cwd         string
	startedAt   time.Time
	lastEventAt time.Time
	turnDone    bool
	turnKnown   bool
}

func New(claudeHome string) *Detector {
	return &Detector{
		claudeHome:     claudeHome,
		errorTailBytes: 512 * 1024,
		cache:          map[string]cachedParse{},
	}
}

func (d *Detector) Name() string {
	return "claude_code"
}

func (d *Detector) Detect(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	results := map[string]*model.DetectorProjectActivity{}

	// Phase 1: Scan active sessions from PID files for live-activity signal.
	activeSessions := d.collectActiveSessions()

	// Phase 2: Scan session JSONL files under ~/.claude/projects/
	files, err := d.collectSessionFiles()
	if err != nil {
		return nil, err
	}

	seenFiles := map[string]struct{}{}
	for _, f := range files {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		seenFiles[f.path] = struct{}{}

		parsed, err := d.parseWithCache(f.path)
		if err != nil {
			continue
		}
		if parsed.cwd == "" {
			continue
		}
		cwd := filepath.Clean(parsed.cwd)
		if !scope.Allows(cwd) {
			continue
		}

		entry, ok := results[cwd]
		if !ok {
			entry = &model.DetectorProjectActivity{
				ProjectPath: cwd,
				Source:      d.Name(),
			}
			results[cwd] = entry
		}

		// If an active PID session targets this cwd + sessionID, mark turn as in-progress.
		turnDone := parsed.turnDone
		turnKnown := parsed.turnKnown
		if active, ok := activeSessions[parsed.sessionID]; ok && active.cwd == cwd {
			turnKnown = true
			turnDone = false
		}

		session := model.SessionEvidence{
			SessionID:            parsed.sessionID,
			ProjectPath:          cwd,
			DetectedProjectPath:  cwd,
			SessionFile:          f.path,
			Format:               "claude_code",
			StartedAt:            parsed.startedAt,
			LastEventAt:          parsed.lastEventAt,
			LatestTurnStateKnown: turnKnown,
			LatestTurnCompleted:  turnDone,
		}
		entry.Sessions = append(entry.Sessions, session)
		entry.Artifacts = append(entry.Artifacts, model.ArtifactEvidence{
			Path:      f.path,
			Kind:      "claude_code_session_jsonl",
			UpdatedAt: f.modTime,
			Note:      "Claude Code session JSONL",
		})
		if parsed.lastEventAt.After(entry.LastActivity) {
			entry.LastActivity = parsed.lastEventAt
		}
	}

	d.pruneCache(seenFiles)

	for _, entry := range results {
		sort.Slice(entry.Sessions, func(i, j int) bool {
			return entry.Sessions[i].LastEventAt.After(entry.Sessions[j].LastEventAt)
		})
		dedupeArtifacts(entry)
	}

	return results, nil
}

type activeSession struct {
	pid       int
	sessionID string
	cwd       string
	startedAt time.Time
}

func (d *Detector) collectActiveSessions() map[string]activeSession {
	result := map[string]activeSession{}
	dir := filepath.Join(d.claudeHome, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			StartedAt int64  `json:"startedAt"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.PID <= 0 || s.SessionID == "" || s.CWD == "" {
			continue
		}
		// Check if PID is still alive.
		if err := syscall.Kill(s.PID, 0); err != nil {
			continue
		}
		startedAt := time.Time{}
		if s.StartedAt > 0 {
			startedAt = time.UnixMilli(s.StartedAt)
		}
		result[s.SessionID] = activeSession{
			pid:       s.PID,
			sessionID: s.SessionID,
			cwd:       filepath.Clean(s.CWD),
			startedAt: startedAt,
		}
	}
	return result
}

type sessionFile struct {
	path    string
	modTime time.Time
}

func (d *Detector) collectSessionFiles() ([]sessionFile, error) {
	projectsDir := filepath.Join(d.claudeHome, "projects")
	if _, err := os.Stat(projectsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var files []sessionFile
	err := filepath.WalkDir(projectsDir, func(path string, dir fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if dir.IsDir() {
			// Only walk one level of subdirectories under projects/.
			rel, _ := filepath.Rel(projectsDir, path)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) > 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := dir.Info()
		if err != nil {
			return nil
		}
		files = append(files, sessionFile{path: path, modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	return files, nil
}

func (d *Detector) parseWithCache(path string) (parseResult, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return parseResult{}, err
	}
	modUnix := stat.ModTime().Unix()

	d.mu.Lock()
	cached, ok := d.cache[path]
	d.mu.Unlock()

	if ok && cached.modTimeUnix == modUnix {
		return cached.result, nil
	}

	parsed, err := parseSessionFile(path, stat.ModTime())
	if err != nil {
		return parseResult{}, err
	}

	d.mu.Lock()
	d.cache[path] = cachedParse{modTimeUnix: modUnix, result: parsed}
	d.mu.Unlock()

	return parsed, nil
}

func parseSessionFile(path string, modTime time.Time) (parseResult, error) {
	res := parseResult{
		lastEventAt: modTime,
	}

	file, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	lastType := ""
	for sc.Scan() {
		line := sc.Text()
		var entry ccEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if res.sessionID == "" && entry.SessionID != "" {
			res.sessionID = entry.SessionID
		}
		if res.cwd == "" && entry.CWD != "" {
			res.cwd = entry.CWD
		}
		if res.startedAt.IsZero() && entry.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				res.startedAt = t
			}
		}
		if entry.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				if t.After(res.lastEventAt) {
					res.lastEventAt = t
				}
			}
		}

		lastType = entry.Type
	}

	// Heuristic: if the last entry is a "system" with subtype "turn_duration", the turn is done.
	// If the last entry is "assistant" or "progress", the turn might still be running.
	switch lastType {
	case "system":
		res.turnKnown = true
		res.turnDone = true
	case "user":
		// User just sent a message; turn hasn't started or just completed.
		res.turnKnown = true
		res.turnDone = true
	case "assistant", "progress":
		res.turnKnown = true
		res.turnDone = false
	}

	return res, nil
}

type ccEntry struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Timestamp string `json:"timestamp"`
	Subtype   string `json:"subtype"`
}

func (d *Detector) pruneCache(live map[string]struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for path := range d.cache {
		if _, ok := live[path]; !ok {
			delete(d.cache, path)
		}
	}
}

func dedupeArtifacts(a *model.DetectorProjectActivity) {
	seen := map[string]struct{}{}
	out := make([]model.ArtifactEvidence, 0, len(a.Artifacts))
	for _, artifact := range a.Artifacts {
		key := artifact.Kind + "|" + artifact.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, artifact)
	}
	a.Artifacts = out
}

// encodeCCProjectPath converts an absolute path to the directory name format
// used by Claude Code under ~/.claude/projects/. For example,
// "/Users/davide/dev/repos/Foo" becomes "-Users-davide-dev-repos-Foo".
func encodeCCProjectPath(projectPath string) string {
	cleaned := filepath.Clean(projectPath)
	return strings.ReplaceAll(cleaned, "/", "-")
}

// SessionFileForProject returns the most recently modified Claude Code session
// JSONL file for the given project path, along with its session ID.
// This is used by the Claude Code session viewer and embedded Claude resume path.
func (d *Detector) SessionFileForProject(projectPath string) (path string, sessionID string, ok bool) {
	projectPath = filepath.Clean(projectPath)
	files, err := d.collectSessionFiles()
	if err != nil {
		return "", "", false
	}

	type candidate struct {
		path      string
		sessionID string
		modTime   time.Time
	}
	var best *candidate

	for _, f := range files {
		parsed, err := d.parseWithCache(f.path)
		if err != nil {
			continue
		}
		cwd := filepath.Clean(parsed.cwd)
		if cwd != projectPath {
			continue
		}
		if best == nil || f.modTime.After(best.modTime) {
			best = &candidate{
				path:      f.path,
				sessionID: parsed.sessionID,
				modTime:   f.modTime,
			}
		}
	}

	if best == nil {
		return "", "", false
	}
	return best.path, best.sessionID, true
}

// IsSessionActive checks whether a Claude Code session with the given
// session ID is currently running (has a live PID).
func (d *Detector) IsSessionActive(sessionID string) bool {
	active := d.collectActiveSessions()
	_, ok := active[sessionID]
	return ok
}

// ReadTranscript reads a Claude Code session JSONL file and returns the
// transcript entries suitable for display, along with metadata.
func ReadTranscript(path string) (entries []model.ClaudeCodeTranscriptEntry, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return readTranscriptFrom(file)
}

func readTranscriptFrom(r io.Reader) ([]model.ClaudeCodeTranscriptEntry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var entries []model.ClaudeCodeTranscriptEntry
	for sc.Scan() {
		line := sc.Text()
		entry, ok := parseTranscriptLine(line)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, sc.Err()
}

func parseTranscriptLine(line string) (model.ClaudeCodeTranscriptEntry, bool) {
	var raw struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		IsMeta    bool   `json:"isMeta"`
		UUID      string `json:"uuid"`
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		CWD       string `json:"cwd"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Model   string          `json:"model"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return model.ClaudeCodeTranscriptEntry{}, false
	}

	// Skip meta entries (command outputs, caveats), progress updates,
	// file-history-snapshot, and system/turn_duration entries.
	if raw.IsMeta {
		return model.ClaudeCodeTranscriptEntry{}, false
	}

	ts := time.Time{}
	if raw.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
			ts = t
		}
	}

	switch raw.Type {
	case "user":
		text := extractTextContent(raw.Message.Content)
		if strings.TrimSpace(text) == "" {
			return model.ClaudeCodeTranscriptEntry{}, false
		}
		return model.ClaudeCodeTranscriptEntry{
			UUID:      raw.UUID,
			Kind:      "user",
			Text:      text,
			Timestamp: ts,
		}, true

	case "assistant":
		text, tools := extractAssistantContent(raw.Message.Content)
		if strings.TrimSpace(text) == "" && len(tools) == 0 {
			return model.ClaudeCodeTranscriptEntry{}, false
		}
		entry := model.ClaudeCodeTranscriptEntry{
			UUID:      raw.UUID,
			Kind:      "assistant",
			Text:      text,
			Model:     raw.Message.Model,
			Timestamp: ts,
		}
		if len(tools) > 0 {
			entry.ToolUses = tools
		}
		return entry, true

	case "tool_result":
		return model.ClaudeCodeTranscriptEntry{}, false

	case "system":
		if raw.Subtype == "turn_duration" {
			return model.ClaudeCodeTranscriptEntry{}, false
		}
		return model.ClaudeCodeTranscriptEntry{}, false

	default:
		return model.ClaudeCodeTranscriptEntry{}, false
	}
}

func extractTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// Try as plain string first.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	// Must be an array — shouldn't happen for user messages, but handle it.
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

func extractAssistantContent(content json.RawMessage) (text string, tools []model.ClaudeCodeToolUse) {
	if len(content) == 0 {
		return "", nil
	}
	var blocks []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Name  string `json:"name"`
		ID    string `json:"id"`
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
			tu := model.ClaudeCodeToolUse{
				ID:   b.ID,
				Name: b.Name,
			}
			// Extract a brief summary from input for display.
			tu.Summary = toolUseSummary(b.Name, b.Input)
			tools = append(tools, tu)
		}
	}
	return strings.Join(textParts, "\n"), tools
}

func toolUseSummary(name string, input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	switch name {
	case "Read":
		return extractStringField(fields, "file_path")
	case "Edit":
		return extractStringField(fields, "file_path")
	case "Write":
		return extractStringField(fields, "file_path")
	case "Bash":
		cmd := extractStringField(fields, "command")
		if len(cmd) > 120 {
			return cmd[:120] + "..."
		}
		return cmd
	case "Glob":
		return extractStringField(fields, "pattern")
	case "Grep":
		return extractStringField(fields, "pattern")
	case "Agent":
		return extractStringField(fields, "description")
	default:
		return ""
	}
}

func extractStringField(fields map[string]json.RawMessage, key string) string {
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
