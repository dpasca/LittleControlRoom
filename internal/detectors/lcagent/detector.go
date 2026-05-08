package lcagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

type Detector struct {
	dataDir string
}

type parseResult struct {
	sessionID     string
	cwd           string
	startedAt     time.Time
	lastEventAt   time.Time
	errorCount    int
	turnKnown     bool
	turnDone      bool
	turnStartedAt time.Time
}

func New(dataDir string) *Detector {
	return &Detector{dataDir: dataDir}
}

func (d *Detector) Name() string {
	return "lcagent"
}

func (d *Detector) Detect(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	files, err := d.collectSessionFiles()
	if err != nil {
		return nil, err
	}
	results := map[string]*model.DetectorProjectActivity{}
	for _, path := range files {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		parsed, err := parseSessionFile(path)
		if err != nil || strings.TrimSpace(parsed.cwd) == "" {
			continue
		}
		cwd := filepath.Clean(parsed.cwd)
		if !scope.Allows(cwd) {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr == nil && parsed.lastEventAt.IsZero() {
			parsed.lastEventAt = info.ModTime()
		}

		entry, ok := results[cwd]
		if !ok {
			entry = &model.DetectorProjectActivity{
				ProjectPath: cwd,
				Source:      d.Name(),
			}
			results[cwd] = entry
		}
		session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
			Source:               model.SessionSourceLCAgent,
			SessionID:            parsed.sessionID,
			ProjectPath:          cwd,
			DetectedProjectPath:  cwd,
			SessionFile:          path,
			Format:               "lcagent_jsonl",
			StartedAt:            parsed.startedAt,
			LastEventAt:          parsed.lastEventAt,
			ErrorCount:           parsed.errorCount,
			LatestTurnStartedAt:  parsed.turnStartedAt,
			LatestTurnStateKnown: parsed.turnKnown,
			LatestTurnCompleted:  parsed.turnDone,
		})
		entry.Sessions = append(entry.Sessions, session)
		updatedAt := parsed.lastEventAt
		if statErr == nil && info.ModTime().After(updatedAt) {
			updatedAt = info.ModTime()
		}
		entry.Artifacts = append(entry.Artifacts, model.ArtifactEvidence{
			Path:      path,
			Kind:      "lcagent_session_jsonl",
			UpdatedAt: updatedAt,
			Note:      "LCAgent session JSONL",
		})
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
	return results, nil
}

func (d *Detector) collectSessionFiles() ([]string, error) {
	root := filepath.Join(d.dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func parseSessionFile(path string) (parseResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return parseResult{}, err
	}
	defer file.Close()

	var result parseResult
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		eventType := rawString(event["type"])
		if eventType == "" {
			continue
		}
		at := rawTime(event["timestamp"])
		if at.IsZero() {
			at = rawTime(event["started_at"])
		}
		if !at.IsZero() && at.After(result.lastEventAt) {
			result.lastEventAt = at
		}
		switch eventType {
		case "session_meta":
			result.sessionID = rawString(event["id"])
			result.cwd = rawString(event["cwd"])
			result.startedAt = rawTime(event["started_at"])
			if result.lastEventAt.IsZero() {
				result.lastEventAt = result.startedAt
			}
		case "user_message":
			result.turnKnown = true
			result.turnDone = false
			if !at.IsZero() {
				result.turnStartedAt = at
			}
		case "turn_complete":
			result.turnKnown = true
			result.turnDone = true
		case "turn_aborted":
			result.turnKnown = true
			result.turnDone = false
			result.errorCount++
		case "tool_result":
			if toolResultFailed(event["result"]) {
				result.errorCount++
			}
		case "permission_denied":
			result.errorCount++
		}
	}
	return result, scanner.Err()
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func rawTime(raw json.RawMessage) time.Time {
	value := rawString(raw)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func toolResultFailed(raw json.RawMessage) bool {
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	return !result.Success
}
