package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/appfs"
	lcrmodel "lcroom/internal/model"
)

type lcagentReplay struct {
	sessionID      string
	projectPath    string
	model          string
	modelProvider  string
	startedAt      time.Time
	lastActivityAt time.Time
	lastError      string
	tokenUsage     *threadTokenUsage
	entries        []TranscriptEntry
}

func loadLCAgentReplay(req LaunchRequest) (*lcagentReplay, error) {
	if req.ForceNew {
		return nil, nil
	}
	dataDir := strings.TrimSpace(req.AppDataDir)
	if dataDir == "" {
		dataDir = appfs.DefaultDataDir()
	}
	projectPath := strings.TrimSpace(req.ProjectPath)
	sessionID := strings.TrimSpace(req.ResumeID)
	var path string
	var err error
	if sessionID != "" {
		path, err = findLCAgentSessionFile(dataDir, sessionID)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, fmt.Errorf("LCAgent session artifact not found: %s", sessionID)
		}
	} else {
		path, err = findLatestLCAgentSessionFile(dataDir, projectPath)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, nil
		}
	}
	replay, err := parseLCAgentReplayFile(path)
	if err != nil {
		return nil, err
	}
	if replay.sessionID == "" {
		replay.sessionID = sessionID
	}
	if projectPath != "" && replay.projectPath != "" && !sameCleanPath(projectPath, replay.projectPath) {
		return nil, fmt.Errorf("LCAgent session %s belongs to %s, not %s", firstNonEmpty(replay.sessionID, sessionID), replay.projectPath, projectPath)
	}
	return replay, nil
}

func findLCAgentSessionFile(dataDir, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", nil
	}
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	want := sessionID + ".jsonl"
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if entry.Name() == want {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

func findLatestLCAgentSessionFile(dataDir, projectPath string) (string, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", nil
	}
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var latestPath string
	var latestAt time.Time
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, path := range files {
		replay, err := parseLCAgentReplayFile(path)
		if err != nil || !sameCleanPath(projectPath, replay.projectPath) {
			continue
		}
		at := replay.lastActivityAt
		if at.IsZero() {
			at = replay.startedAt
		}
		if at.IsZero() {
			if info, statErr := os.Stat(path); statErr == nil {
				at = info.ModTime()
			}
		}
		if latestPath == "" || at.After(latestAt) || (at.Equal(latestAt) && path > latestPath) {
			latestPath = path
			latestAt = at
		}
	}
	return latestPath, nil
}

func parseLCAgentReplayFile(path string) (*lcagentReplay, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	replay := &lcagentReplay{}
	if err := forEachLCAgentJSONLEvent(file, func(event map[string]json.RawMessage) {
		eventType := rawJSONString(event["type"])
		if eventType == "" {
			return
		}
		at := rawJSONTime(event["timestamp"])
		if at.IsZero() {
			at = rawJSONTime(event["started_at"])
		}
		if !at.IsZero() && at.After(replay.lastActivityAt) {
			replay.lastActivityAt = at
		}
		switch eventType {
		case "session_meta":
			replay.sessionID = rawJSONString(event["id"])
			replay.projectPath = rawJSONString(event["cwd"])
			replay.startedAt = rawJSONTime(event["started_at"])
			replay.model = rawJSONString(event["model"])
			replay.modelProvider = rawJSONString(event["provider"])
			if replay.lastActivityAt.IsZero() {
				replay.lastActivityAt = replay.startedAt
			}
		case "model_response":
			modelName := rawJSONString(event["model"])
			if modelName != "" {
				replay.model = modelName
			}
			if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
				replay.addTokenUsage(usage)
			}
		case "user_message":
			replay.appendEntry(TranscriptUser, rawJSONString(event["message"]))
		case "tool_call":
			tool := rawJSONString(event["tool"])
			replay.appendEntry(TranscriptTool, lcagentToolCallText(tool, event["args"]))
		case "tool_result":
			tool := rawJSONString(event["tool"])
			replay.appendEntry(TranscriptTool, lcagentToolResultText(tool, event["result"]))
		case "plan_update":
			replay.appendEntry(TranscriptPlan, lcagentPlanText(event["items"]))
		case "assistant_message":
			replay.appendEntry(TranscriptAgent, rawJSONString(event["message"]))
		case "files_touched":
			replay.appendEntry(TranscriptFileChange, lcagentFilesTouchedText(event["files"]))
		case "patch_diff_summary":
			replay.appendEntry(TranscriptFileChange, rawJSONString(event["summary"]))
		case "patch_feedback":
			replay.appendEntry(TranscriptError, lcagentPatchFeedbackText(event))
		case "verification_check":
			replay.appendEntry(TranscriptStatus, lcagentVerificationCheckText(event))
		case "verification_feedback":
			replay.appendEntry(TranscriptStatus, lcagentVerificationFeedbackText(event))
		case "repair_feedback_suppressed":
			replay.appendEntry(TranscriptStatus, lcagentRepairFeedbackSuppressedText(event))
		case "repair_guidance":
			replay.appendEntry(TranscriptStatus, lcagentRepairGuidanceText(event))
		case "verification_summary":
			status := rawJSONString(event["status"])
			message := rawJSONString(event["message"])
			if message == "" && status != "" {
				message = "Verification status: " + status
			}
			replay.appendEntry(TranscriptStatus, message)
		case "continuation":
			replay.appendEntry(TranscriptStatus, lcagentContinuationText(event))
		case "resume_context":
			replay.appendEntry(TranscriptStatus, lcagentResumeContextText(event))
		case "context_compacted":
			replay.appendEntry(TranscriptStatus, lcagentContextCompactedText(event))
		case "turn_complete":
			if text := lcagentTurnCompleteTraceText(event); text != "" {
				replay.appendEntry(TranscriptStatus, text)
			}
		case "turn_aborted":
			reason := firstNonEmpty(rawJSONString(event["reason"]), "LCAgent run aborted")
			replay.lastError = reason
			replay.appendEntry(TranscriptError, reason)
		case "permission_denied":
			reason := firstNonEmpty(rawJSONString(event["reason"]), "LCAgent permission denied")
			replay.lastError = reason
			replay.appendEntry(TranscriptError, reason)
		case "approval_request":
			if request := lcagentApprovalRequestFromEvent(event, replay.sessionID); request != nil {
				replay.appendEntry(TranscriptStatus, "LCAgent requested command approval: "+request.Summary())
			}
		case "approval_resolved":
			replay.appendEntry(TranscriptStatus, lcagentApprovalResolvedText(event))
		}
	}); err != nil {
		return nil, err
	}
	return replay, nil
}

func (r *lcagentReplay) addTokenUsage(usage lcrmodel.LLMUsage) {
	if r == nil || !lcagentUsageTracked(usage) {
		return
	}
	breakdown := lcagentTokenUsageBreakdown(usage)
	if r.tokenUsage == nil {
		r.tokenUsage = &threadTokenUsage{}
	}
	r.tokenUsage.Last = breakdown
	r.tokenUsage.Total.CachedInputTokens += breakdown.CachedInputTokens
	r.tokenUsage.Total.InputTokens += breakdown.InputTokens
	r.tokenUsage.Total.OutputTokens += breakdown.OutputTokens
	r.tokenUsage.Total.ReasoningOutputTokens += breakdown.ReasoningOutputTokens
	r.tokenUsage.Total.TotalTokens += breakdown.TotalTokens
}

func (r *lcagentReplay) appendEntry(kind TranscriptKind, text string) {
	if r == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.entries = append(r.entries, TranscriptEntry{
		ItemID: fmt.Sprintf("lcagent-replay-%d", len(r.entries)+1),
		Kind:   kind,
		Text:   text,
	})
}

func rawJSONTime(raw json.RawMessage) time.Time {
	value := rawJSONString(raw)
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

func sameCleanPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	return canonicalReplayPath(left) == canonicalReplayPath(right)
}

func canonicalReplayPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}
