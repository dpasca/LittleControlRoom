package codex

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"

	_ "modernc.org/sqlite"
)

type Detector struct {
	codexHome        string
	includeArchived  bool
	errorWindow      time.Duration
	completionWindow time.Duration
	errorTailBytes   int64

	mu         sync.Mutex
	cache      map[string]cachedParse
	errorCache map[string]cachedError
	turnCache  map[string]cachedTurnState
}

type cachedParse struct {
	modTimeUnix int64
	result      parseResult
}

type parseResult struct {
	sessionID     string
	cwd           string
	format        string
	startedAt     time.Time
	lastEventAt   time.Time
	errorCount    int
	turnKnown     bool
	turnDone      bool
	turnStartedAt time.Time
	artifacts     []model.ArtifactEvidence
}

type cachedError struct {
	modTimeUnix int64
	count       int
}

type cachedTurnState struct {
	modTimeUnix int64
	state       turnLifecycleState
}

type turnLifecycleState struct {
	known     bool
	completed bool
	startedAt time.Time
}

type turnLifecycleEvent struct {
	completed bool
	timestamp time.Time
}

func New(codexHome string) *Detector {
	return &Detector{
		codexHome:        codexHome,
		includeArchived:  true,
		errorWindow:      48 * time.Hour,
		completionWindow: 48 * time.Hour,
		errorTailBytes:   512 * 1024,
		cache:            map[string]cachedParse{},
		errorCache:       map[string]cachedError{},
		turnCache:        map[string]cachedTurnState{},
	}
}

func (d *Detector) Name() string {
	return "codex"
}

func (d *Detector) Detect(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	if fastResults, used, err := d.detectFromStateDB(ctx, scope); err == nil && used {
		// Supplement the DB results with any session files on disk that aren't
		// already covered. Embedded Codex sessions launched by the app create
		// rollout JSONL files but may not write to state_5.sqlite.
		d.mergeOrphanSessionFiles(ctx, scope, fastResults)
		return fastResults, nil
	}

	files, err := d.collectSessionFiles()
	if err != nil {
		return nil, err
	}

	results := map[string]*model.DetectorProjectActivity{}
	seenFiles := map[string]struct{}{}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		seenFiles[f] = struct{}{}
		parsed, err := d.parseWithCache(f)
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

		session := model.SessionEvidence{
			SessionID:            parsed.sessionID,
			ProjectPath:          cwd,
			DetectedProjectPath:  cwd,
			SessionFile:          f,
			Format:               parsed.format,
			StartedAt:            parsed.startedAt,
			LastEventAt:          parsed.lastEventAt,
			ErrorCount:           parsed.errorCount,
			LatestTurnStartedAt:  parsed.turnStartedAt,
			LatestTurnStateKnown: parsed.turnKnown,
			LatestTurnCompleted:  parsed.turnDone,
		}
		entry.Sessions = append(entry.Sessions, session)
		entry.Artifacts = append(entry.Artifacts, parsed.artifacts...)
		entry.ErrorCount += parsed.errorCount
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

// mergeOrphanSessionFiles finds session JSONL files on disk that aren't
// referenced by any session in the DB results and merges them in. This is
// cheap because it only parses files whose paths are not already known.
func (d *Detector) mergeOrphanSessionFiles(ctx context.Context, scope scanner.PathScope, results map[string]*model.DetectorProjectActivity) {
	knownFiles := map[string]struct{}{}
	for _, entry := range results {
		for _, s := range entry.Sessions {
			if s.SessionFile != "" {
				knownFiles[s.SessionFile] = struct{}{}
			}
		}
	}

	files, err := d.collectSessionFiles()
	if err != nil {
		return
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, known := knownFiles[f]; known {
			continue
		}
		parsed, err := d.parseWithCache(f)
		if err != nil {
			continue
		}
		if parsed.cwd == "" || parsed.sessionID == "" {
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

		session := model.SessionEvidence{
			SessionID:            parsed.sessionID,
			ProjectPath:          cwd,
			DetectedProjectPath:  cwd,
			SessionFile:          f,
			Format:               parsed.format,
			StartedAt:            parsed.startedAt,
			LastEventAt:          parsed.lastEventAt,
			ErrorCount:           parsed.errorCount,
			LatestTurnStartedAt:  parsed.turnStartedAt,
			LatestTurnStateKnown: parsed.turnKnown,
			LatestTurnCompleted:  parsed.turnDone,
		}
		entry.Sessions = append(entry.Sessions, session)
		entry.Artifacts = append(entry.Artifacts, parsed.artifacts...)
		entry.ErrorCount += parsed.errorCount
		if parsed.lastEventAt.After(entry.LastActivity) {
			entry.LastActivity = parsed.lastEventAt
		}
	}

	for _, entry := range results {
		sort.Slice(entry.Sessions, func(i, j int) bool {
			return entry.Sessions[i].LastEventAt.After(entry.Sessions[j].LastEventAt)
		})
	}
}

func (d *Detector) detectFromStateDB(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, bool, error) {
	dbPath := filepath.Join(d.codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(ctx, `
		SELECT id, cwd, created_at, updated_at, rollout_path
		FROM threads
	`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	results := map[string]*model.DetectorProjectActivity{}
	now := time.Now()
	used := false

	for rows.Next() {
		var (
			id        string
			cwd       string
			createdAt int64
			updatedAt int64
			rollout   string
		)
		if err := rows.Scan(&id, &cwd, &createdAt, &updatedAt, &rollout); err != nil {
			continue
		}
		if cwd == "" {
			continue
		}
		projectPath := filepath.Clean(cwd)
		if !scope.Allows(projectPath) {
			continue
		}

		used = true
		entry, ok := results[projectPath]
		if !ok {
			entry = &model.DetectorProjectActivity{
				ProjectPath: projectPath,
				Source:      d.Name(),
			}
			results[projectPath] = entry
		}

		updatedTime := time.Unix(updatedAt, 0)
		startedTime := time.Unix(createdAt, 0)

		// Use the rollout file's modification time if it's more recent than
		// the threads table updated_at, since Codex appends to the JSONL file
		// during active sessions but may not update the DB timestamp as often.
		lastEventAt := updatedTime
		if rollout != "" {
			if stat, err := os.Stat(rollout); err == nil {
				if modTime := stat.ModTime(); modTime.After(lastEventAt) {
					lastEventAt = modTime
				}
			}
		}

		errorCount := 0
		if rollout != "" && now.Sub(lastEventAt) <= d.errorWindow {
			errorCount = d.countErrorsWithCache(rollout)
		}
		turnState := turnLifecycleState{}
		if rollout != "" && now.Sub(lastEventAt) <= d.completionWindow {
			turnState = d.detectTurnStateWithCache(rollout)
		}

		artifactPath := dbPath
		artifactKind := "codex_threads_sqlite"
		artifactNote := "state_5.sqlite threads table"
		if rollout != "" {
			artifactPath = rollout
			artifactKind = "codex_session_jsonl"
			artifactNote = "rollout path from state_5.sqlite threads"
		}
		artifactUpdated := lastEventAt
		if stat, err := os.Stat(artifactPath); err == nil {
			if modTime := stat.ModTime(); modTime.After(artifactUpdated) {
				artifactUpdated = modTime
			}
		}

		session := model.SessionEvidence{
			SessionID:            id,
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			SessionFile:          rollout,
			Format:               "modern",
			StartedAt:            startedTime,
			LastEventAt:          lastEventAt,
			ErrorCount:           errorCount,
			LatestTurnStartedAt:  turnState.startedAt,
			LatestTurnStateKnown: turnState.known,
			LatestTurnCompleted:  turnState.completed,
		}
		entry.Sessions = append(entry.Sessions, session)
		entry.Artifacts = append(entry.Artifacts, model.ArtifactEvidence{
			Path:      artifactPath,
			Kind:      artifactKind,
			UpdatedAt: artifactUpdated,
			Note:      artifactNote,
		})
		entry.ErrorCount += errorCount
		if lastEventAt.After(entry.LastActivity) {
			entry.LastActivity = lastEventAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, used, err
	}

	for _, entry := range results {
		sort.Slice(entry.Sessions, func(i, j int) bool {
			return entry.Sessions[i].LastEventAt.After(entry.Sessions[j].LastEventAt)
		})
		dedupeArtifacts(entry)
	}

	return results, used, nil
}

func (d *Detector) collectSessionFiles() ([]string, error) {
	var roots []string
	sessionsDir := filepath.Join(d.codexHome, "sessions")
	roots = append(roots, sessionsDir)
	if d.includeArchived {
		roots = append(roots, filepath.Join(d.codexHome, "archived_sessions"))
	}

	var files []string
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		err := filepath.WalkDir(root, func(path string, dir fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if dir.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".jsonl" {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(files)
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

func (d *Detector) countErrorsWithCache(path string) int {
	stat, err := os.Stat(path)
	if err != nil {
		return 0
	}
	modUnix := stat.ModTime().Unix()

	d.mu.Lock()
	cached, ok := d.errorCache[path]
	d.mu.Unlock()
	if ok && cached.modTimeUnix == modUnix {
		return cached.count
	}

	count := d.countErrorsFromTail(path)

	d.mu.Lock()
	d.errorCache[path] = cachedError{modTimeUnix: modUnix, count: count}
	d.mu.Unlock()
	return count
}

func (d *Detector) detectTurnStateWithCache(path string) turnLifecycleState {
	stat, err := os.Stat(path)
	if err != nil {
		return turnLifecycleState{}
	}
	modUnix := stat.ModTime().Unix()

	d.mu.Lock()
	cached, ok := d.turnCache[path]
	d.mu.Unlock()
	if ok && cached.modTimeUnix == modUnix {
		return cached.state
	}

	state := d.detectTurnStateFromTail(path)

	d.mu.Lock()
	d.turnCache[path] = cachedTurnState{modTimeUnix: modUnix, state: state}
	d.mu.Unlock()
	return state
}

func (d *Detector) countErrorsFromTail(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return 0
	}
	size := stat.Size()
	offset := int64(0)
	if size > d.errorTailBytes {
		offset = size - d.errorTailBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	count := 0
	for sc.Scan() {
		line := sc.Text()
		if output, ok := extractFunctionOutput(line); ok {
			count += countNonZeroExitCodes(output)
		}
	}
	return count
}

func (d *Detector) detectTurnStateFromTail(path string) turnLifecycleState {
	f, err := os.Open(path)
	if err != nil {
		return turnLifecycleState{}
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return turnLifecycleState{}
	}
	size := stat.Size()
	offset := int64(0)
	if size > d.errorTailBytes {
		offset = size - d.errorTailBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return turnLifecycleState{}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	state := turnLifecycleState{}
	provisionalTurnStart := time.Time{}
	for sc.Scan() {
		line := sc.Text()
		if event, ok := extractTurnLifecycle(line); ok {
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
		if !provisionalTurnStart.IsZero() && isMeaningfulTurnActivityLine(line) {
			state.known = true
			state.completed = false
			state.startedAt = provisionalTurnStart
		}
	}
	return state
}

func parseSessionFile(path string, modTime time.Time) (parseResult, error) {
	res := parseResult{
		lastEventAt: modTime,
		artifacts: []model.ArtifactEvidence{{
			Path:      path,
			Kind:      "codex_session_jsonl",
			UpdatedAt: modTime,
			Note:      "session log file mtime used for activity",
		}},
	}

	file, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	lineNo := 0
	provisionalTurnStart := time.Time{}
	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		if lineNo == 1 {
			format := detectFormat(line)
			res.format = format
			switch format {
			case "modern":
				meta, parseErr := parseModernMeta(line)
				if parseErr == nil {
					res.sessionID = meta.ID
					res.cwd = meta.CWD
					res.startedAt = meta.Timestamp
				}
			case "legacy":
				meta, parseErr := parseLegacyMeta(line)
				if parseErr == nil {
					res.sessionID = meta.ID
					res.startedAt = meta.Timestamp
				}
			}
		}

		if res.cwd == "" {
			if cwd := extractLegacyCWD(line); cwd != "" {
				res.cwd = cwd
			}
		}

		if output, ok := extractFunctionOutput(line); ok {
			res.errorCount += countNonZeroExitCodes(output)
		}
		if event, ok := extractTurnLifecycle(line); ok {
			if event.completed {
				res.turnKnown = true
				res.turnDone = true
				res.turnStartedAt = time.Time{}
			} else {
				provisionalTurnStart = event.timestamp
			}
			if event.completed {
				provisionalTurnStart = time.Time{}
			}
			continue
		}
		if !provisionalTurnStart.IsZero() && isMeaningfulTurnActivityLine(line) {
			res.turnKnown = true
			res.turnDone = false
			res.turnStartedAt = provisionalTurnStart
		}
	}
	if err := scanner.Err(); err != nil {
		return res, err
	}

	return res, nil
}

func (d *Detector) pruneCache(live map[string]struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for path := range d.cache {
		if _, ok := live[path]; !ok {
			delete(d.cache, path)
		}
	}
	for path := range d.errorCache {
		if _, ok := live[path]; !ok {
			delete(d.errorCache, path)
		}
	}
	for path := range d.turnCache {
		if _, ok := live[path]; !ok {
			delete(d.turnCache, path)
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

type modernMeta struct {
	ID        string
	CWD       string
	Timestamp time.Time
}

type modernLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID        string `json:"id"`
		CWD       string `json:"cwd"`
		Timestamp string `json:"timestamp"`
	} `json:"payload"`
}

func parseModernMeta(line string) (modernMeta, error) {
	var v modernLine
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return modernMeta{}, err
	}
	if v.Type != "session_meta" {
		return modernMeta{}, fmt.Errorf("not a modern session_meta line")
	}
	t, err := time.Parse(time.RFC3339, v.Payload.Timestamp)
	if err != nil {
		t = time.Time{}
	}
	return modernMeta{ID: v.Payload.ID, CWD: v.Payload.CWD, Timestamp: t}, nil
}

type legacyMeta struct {
	ID        string
	Timestamp time.Time
}

type legacyLine struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

func parseLegacyMeta(line string) (legacyMeta, error) {
	var v legacyLine
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return legacyMeta{}, err
	}
	if v.ID == "" {
		return legacyMeta{}, fmt.Errorf("not a legacy session line")
	}
	t, err := time.Parse(time.RFC3339, v.Timestamp)
	if err != nil {
		t = time.Time{}
	}
	return legacyMeta{ID: v.ID, Timestamp: t}, nil
}

func detectFormat(firstLine string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(firstLine), &raw); err != nil {
		return "unknown"
	}
	if t, ok := raw["type"].(string); ok && t == "session_meta" {
		return "modern"
	}
	if _, ok := raw["id"]; ok {
		return "legacy"
	}
	return "unknown"
}

func extractFunctionOutput(line string) (string, bool) {
	var top struct {
		Type    string `json:"type"`
		Output  string `json:"output"`
		Payload struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return "", false
	}

	if top.Type == "function_call_output" && top.Output != "" {
		return top.Output, true
	}
	if top.Type == "response_item" && top.Payload.Type == "function_call_output" && top.Payload.Output != "" {
		return top.Payload.Output, true
	}
	return "", false
}

func extractTurnLifecycle(line string) (turnLifecycleEvent, bool) {
	var top struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &top); err != nil {
		return turnLifecycleEvent{}, false
	}
	if top.Type != "event_msg" {
		return turnLifecycleEvent{}, false
	}
	timestamp := time.Time{}
	if parsed, err := time.Parse(time.RFC3339, top.Timestamp); err == nil {
		timestamp = parsed
	}
	switch top.Payload.Type {
	case "task_started":
		return turnLifecycleEvent{completed: false, timestamp: timestamp}, true
	case "task_complete", "turn_aborted":
		return turnLifecycleEvent{completed: true, timestamp: timestamp}, true
	default:
		return turnLifecycleEvent{}, false
	}
}

func isMeaningfulTurnActivityLine(line string) bool {
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

func extractLegacyCWD(prefix string) string {
	marker := "Current working directory:"
	idx := strings.Index(prefix, marker)
	if idx == -1 {
		return ""
	}
	rest := prefix[idx+len(marker):]
	end := len(rest)
	for _, sep := range []string{"\\n", "\n", "\"", "\r"} {
		if i := strings.Index(rest, sep); i >= 0 && i < end {
			end = i
		}
	}
	cwd := strings.TrimSpace(rest[:end])
	cwd = strings.Trim(cwd, "\"')(")
	return cwd
}

func countNonZeroExitCodes(s string) int {
	needle := "Process exited with code "
	count := 0
	for {
		idx := strings.Index(s, needle)
		if idx == -1 {
			break
		}
		s = s[idx+len(needle):]
		i := 0
		for i < len(s) && s[i] == ' ' {
			i++
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i {
			if n, err := strconv.Atoi(s[i:j]); err == nil && n > 0 {
				count++
			}
			s = s[j:]
		}
	}
	return count
}
