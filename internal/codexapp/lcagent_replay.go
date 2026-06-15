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
	"lcroom/internal/browserctl"
	lcagentcore "lcroom/internal/lcagent"
	lcrmodel "lcroom/internal/model"
)

const lcagentReplayMaxDepth = 20

type lcagentReplay struct {
	sessionID                string
	threadID                 string
	parentID                 string
	projectPath              string
	model                    string
	modelProvider            string
	criticModel              string
	criticModelProvider      string
	startedAt                time.Time
	lastActivityAt           time.Time
	lastError                string
	criticActive             bool
	criticReviews            int
	criticConsultations      int
	criticConsultConcerns    int
	criticConcerns           int
	criticLeadRevisions      int
	criticFollowupDrafts     int
	criticLastStatus         string
	criticLastSummary        string
	suggestedInputDraftID    string
	suggestedInputDraft      string
	tokenUsage               *threadTokenUsage
	browserActivity          browserctl.SessionActivity
	managedBrowserSessionKey string
	currentBrowserPageURL    string
	currentBrowserPageStale  bool
	entries                  []TranscriptEntry
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
	if sessionID != "" {
		if info, ok, err := lcagentcore.LoadThreadStateInfo(dataDir, sessionID, projectPath); err != nil {
			return nil, err
		} else if ok {
			return loadLCAgentThreadReplay(dataDir, info)
		}
	} else {
		if info, ok, err := lcagentcore.LatestThreadStateInfo(dataDir, projectPath); err != nil {
			return nil, err
		} else if ok {
			return loadLCAgentThreadReplay(dataDir, info)
		}
	}
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
	if replay.threadID == "" {
		replay.threadID = replay.sessionID
	}
	if projectPath != "" && replay.projectPath != "" && !sameCleanPath(projectPath, replay.projectPath) {
		return nil, fmt.Errorf("LCAgent session %s belongs to %s, not %s", firstNonEmpty(replay.sessionID, sessionID), replay.projectPath, projectPath)
	}
	if replay.threadID != "" && replay.threadID != replay.sessionID {
		if info, ok, err := lcagentcore.LoadThreadStateInfo(dataDir, replay.threadID, projectPath); err != nil {
			return nil, err
		} else if ok {
			return loadLCAgentThreadReplay(dataDir, info)
		}
	}
	replay = loadLCAgentReplayChain(dataDir, replay, projectPath)
	return replay, nil
}

func loadLCAgentThreadReplay(dataDir string, info lcagentcore.ThreadStateInfo) (*lcagentReplay, error) {
	threadID := strings.TrimSpace(info.ThreadID)
	if threadID == "" {
		return nil, nil
	}
	paths, err := findLCAgentSessionFilesByThread(dataDir, threadID)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 && strings.TrimSpace(info.LastRunID) != "" {
		path, err := findLCAgentSessionFile(dataDir, info.LastRunID)
		if err != nil {
			return nil, err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	combined := &lcagentReplay{
		sessionID:      strings.TrimSpace(info.LastRunID),
		threadID:       threadID,
		projectPath:    strings.TrimSpace(info.ProjectPath),
		lastActivityAt: info.UpdatedAt,
	}
	for _, path := range paths {
		replay, err := parseLCAgentReplayFile(path)
		if err != nil || replay == nil {
			continue
		}
		if replay.threadID == "" {
			replay.threadID = replay.sessionID
		}
		if replay.threadID != threadID {
			continue
		}
		if replay.projectPath != "" {
			combined.projectPath = replay.projectPath
		}
		if replay.model != "" {
			combined.model = replay.model
		}
		if replay.modelProvider != "" {
			combined.modelProvider = replay.modelProvider
		}
		if replay.startedAt.Before(combined.startedAt) || combined.startedAt.IsZero() {
			combined.startedAt = replay.startedAt
		}
		if replay.lastActivityAt.After(combined.lastActivityAt) {
			combined.lastActivityAt = replay.lastActivityAt
		}
		if replay.sessionID != "" {
			combined.sessionID = replay.sessionID
		}
		if replay.lastError != "" {
			combined.lastError = replay.lastError
		}
		combined.browserActivity = replay.browserActivity
		if replay.managedBrowserSessionKey != "" {
			combined.managedBrowserSessionKey = replay.managedBrowserSessionKey
		}
		if replay.currentBrowserPageURL != "" {
			combined.currentBrowserPageURL = replay.currentBrowserPageURL
			combined.currentBrowserPageStale = replay.currentBrowserPageStale
		}
		combined.entries = appendReplayEntries(combined.entries, replay.entries)
		combined.tokenUsage = mergeReplayTokenUsage(combined.tokenUsage, replay.tokenUsage)
		mergeReplayCriticState(combined, replay)
	}
	return combined, nil
}

func loadLCAgentReplayChain(dataDir string, replay *lcagentReplay, projectPath string) *lcagentReplay {
	if replay == nil {
		return nil
	}
	seen := map[string]struct{}{}
	if replay.sessionID != "" {
		seen[replay.sessionID] = struct{}{}
	}
	parents := make([]*lcagentReplay, 0)
	parentID := strings.TrimSpace(replay.parentID)
	for depth := 0; parentID != "" && depth < lcagentReplayMaxDepth; depth++ {
		if _, ok := seen[parentID]; ok {
			break
		}
		seen[parentID] = struct{}{}
		path, err := findLCAgentSessionFile(dataDir, parentID)
		if err != nil || path == "" {
			break
		}
		parent, err := parseLCAgentReplayFile(path)
		if err != nil || parent == nil {
			break
		}
		if projectPath != "" && parent.projectPath != "" && !sameCleanPath(projectPath, parent.projectPath) {
			break
		}
		parents = append(parents, parent)
		parentID = strings.TrimSpace(parent.parentID)
	}
	if len(parents) == 0 {
		return replay
	}
	combined := *replay
	combined.entries = nil
	combined.tokenUsage = nil
	for i := len(parents) - 1; i >= 0; i-- {
		combined.entries = appendReplayEntries(combined.entries, parents[i].entries)
		combined.tokenUsage = mergeReplayTokenUsage(combined.tokenUsage, parents[i].tokenUsage)
	}
	combined.entries = appendReplayEntries(combined.entries, replay.entries)
	combined.tokenUsage = mergeReplayTokenUsage(combined.tokenUsage, replay.tokenUsage)
	return &combined
}

func appendReplayEntries(out []TranscriptEntry, entries []TranscriptEntry) []TranscriptEntry {
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		entry.Text = text
		entry.ItemID = fmt.Sprintf("lcagent-replay-%d", len(out)+1)
		out = append(out, entry)
	}
	return out
}

func mergeReplayTokenUsage(total, next *threadTokenUsage) *threadTokenUsage {
	if next == nil {
		return total
	}
	out := cloneThreadTokenUsage(total)
	if out == nil {
		out = &threadTokenUsage{}
	}
	out.Last = next.Last
	out.Total.CachedInputTokens += next.Total.CachedInputTokens
	out.Total.InputTokens += next.Total.InputTokens
	out.Total.OutputTokens += next.Total.OutputTokens
	out.Total.ReasoningOutputTokens += next.Total.ReasoningOutputTokens
	out.Total.TotalTokens += next.Total.TotalTokens
	if next.ModelContextWindow != nil {
		copied := *next.ModelContextWindow
		out.ModelContextWindow = &copied
	}
	return out
}

func mergeReplayCriticState(total, next *lcagentReplay) {
	if total == nil || next == nil {
		return
	}
	if next.criticModel != "" {
		total.criticModel = next.criticModel
	}
	if next.criticModelProvider != "" {
		total.criticModelProvider = next.criticModelProvider
	}
	total.criticActive = next.criticActive
	total.criticReviews += next.criticReviews
	total.criticConsultations += next.criticConsultations
	total.criticConsultConcerns += next.criticConsultConcerns
	total.criticConcerns += next.criticConcerns
	total.criticLeadRevisions += next.criticLeadRevisions
	total.criticFollowupDrafts += next.criticFollowupDrafts
	if next.criticLastStatus != "" {
		total.criticLastStatus = next.criticLastStatus
		total.criticLastSummary = next.criticLastSummary
	}
	if next.suggestedInputDraftID != "" || next.suggestedInputDraft != "" {
		total.suggestedInputDraftID = next.suggestedInputDraftID
		total.suggestedInputDraft = next.suggestedInputDraft
	}
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

func findLCAgentSessionFilesByThread(dataDir, threadID string) ([]string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, nil
	}
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	type candidate struct {
		path string
		at   time.Time
	}
	var candidates []candidate
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		replay, err := parseLCAgentReplayFile(path)
		if err != nil || replay == nil {
			return nil
		}
		if firstNonEmpty(replay.threadID, replay.sessionID) == threadID {
			at := replay.startedAt
			if at.IsZero() {
				at = replay.lastActivityAt
			}
			candidates = append(candidates, candidate{path: path, at: at})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].path < candidates[j].path
		}
		if candidates[i].at.IsZero() {
			return true
		}
		if candidates[j].at.IsZero() {
			return false
		}
		return candidates[i].at.Before(candidates[j].at)
	})
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	return paths, nil
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
			replay.threadID = firstNonEmpty(rawJSONString(event["thread_id"]), replay.threadID)
			replay.parentID = firstNonEmpty(rawJSONString(event["parent_session_id"]), replay.parentID)
			replay.projectPath = rawJSONString(event["cwd"])
			replay.startedAt = rawJSONTime(event["started_at"])
			replay.model = rawJSONString(event["model"])
			replay.modelProvider = rawJSONString(event["provider"])
			if replay.lastActivityAt.IsZero() {
				replay.lastActivityAt = replay.startedAt
			}
		case "model_request_started", "model_request_progress":
			replay.upsertEntry(lcagentModelRequestItemID(event), TranscriptStatus, lcagentModelRequestText(event))
		case "model_response":
			modelName := rawJSONString(event["model"])
			if modelName != "" {
				replay.model = modelName
			}
			if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
				replay.addTokenUsage(usage)
			}
			replay.upsertExistingEntry(lcagentModelRequestItemID(event), TranscriptStatus, lcagentModelResponseText(event))
		case "critic_profile":
			if rawJSONBool(event["enabled"]) {
				replay.criticModel = rawJSONString(event["model"])
				replay.criticModelProvider = rawJSONString(event["provider"])
			}
		case "critic_review_started":
			replay.criticActive = true
			replay.criticLastStatus = "reviewing"
			replay.criticLastSummary = ""
		case "critic_model_response":
			modelName := rawJSONString(event["model"])
			if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
				replay.addTokenUsage(usage)
			}
		case "critic_model_response_invalid":
			replay.applyCriticInvalidModelResponse(event)
		case "critic_review_retry":
			replay.applyCriticReviewRetry(event)
		case "critic_review_result":
			replay.applyCriticReviewResult(event)
		case "critic_review_failed":
			replay.applyCriticReviewFailed(event)
		case "critic_lead_feedback":
			replay.applyCriticLeadFeedback(event)
		case "critic_consult_started":
			replay.applyCriticConsultStarted(event)
		case "critic_consult_result":
			replay.applyCriticConsultResult(event)
		case "critic_consult_failed":
			replay.applyCriticConsultFailed(event)
		case "user_message":
			replay.appendEntry(TranscriptUser, rawJSONString(event["message"]))
		case "tool_call":
			tool := rawJSONString(event["tool"])
			replay.appendEntry(TranscriptTool, lcagentToolCallText(tool, event["args"]))
		case "tool_result":
			tool := rawJSONString(event["tool"])
			replay.appendEntry(TranscriptTool, lcagentToolResultText(tool, event["result"]))
		case "browser_activity_started":
			replay.browserActivity = lcagentReplayBrowserActivity(event, replay.browserActivity, browserctl.SessionActivityStateActive)
		case "browser_activity_finished":
			replay.browserActivity = lcagentReplayBrowserActivity(event, replay.browserActivity, browserctl.SessionActivityStateIdle)
		case "browser_waiting_for_user":
			replay.browserActivity = lcagentReplayBrowserActivity(event, replay.browserActivity, browserctl.SessionActivityStateWaitingForUser)
			replay.observeBrowserPage(event)
			replay.appendEntry(TranscriptStatus, lcagentBrowserWaitingText(event))
		case "browser_page":
			replay.observeBrowserPage(event)
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
		case "provider_failure":
			if !rawJSONBool(event["retrying"]) {
				replay.upsertExistingEntry(lcagentModelRequestItemID(event), TranscriptStatus, lcagentModelRequestFailureText(event))
			}
			replay.appendEntry(TranscriptError, lcagentProviderFailureText(event))
		case "provider_retry":
			replay.appendEntry(TranscriptStatus, lcagentProviderRetryText(event))
		case "provider_retry_succeeded":
			replay.appendEntry(TranscriptStatus, lcagentProviderRetrySucceededText(event))
		case "verification_summary":
			status := rawJSONString(event["status"])
			message := rawJSONString(event["message"])
			if message == "" && status != "" {
				message = "Verification status: " + status
			}
			replay.appendEntry(TranscriptStatus, message)
		case "continuation":
			replay.parentID = firstNonEmpty(rawJSONString(event["parent_session_id"]), replay.parentID)
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
	if replay.threadID == "" {
		replay.threadID = replay.sessionID
	}
	return replay, nil
}

func (r *lcagentReplay) applyCriticInvalidModelResponse(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	modelName := rawJSONString(event["model"])
	if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
		r.addTokenUsage(usage)
	}
	attempt := rawJSONInt(event["attempt"])
	text := "LCAgent critic returned invalid structured output"
	if attempt > 0 {
		text += fmt.Sprintf(" on attempt %d", attempt)
	}
	if rawJSONBool(event["retrying"]) {
		text += "; retrying"
	}
	r.criticLastStatus = "invalid_json"
	r.criticLastSummary = strings.TrimSpace(firstNonEmpty(rawJSONString(event["message"]), text))
	r.appendEntry(TranscriptStatus, text)
}

func (r *lcagentReplay) applyCriticReviewRetry(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	message := firstNonEmpty(rawJSONString(event["message"]), "LCAgent critic retrying")
	r.criticLastStatus = "retrying"
	r.criticLastSummary = strings.TrimSpace(message)
	r.appendEntry(TranscriptStatus, message)
}

func (r *lcagentReplay) applyCriticReviewResult(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	status := normalizeLCAgentCriticReviewStatus(rawJSONString(event["status"]))
	summary := strings.TrimSpace(rawJSONString(event["summary"]))
	text := lcagentCriticReviewResultText(status, summary)
	r.criticActive = false
	r.criticReviews++
	r.criticLastStatus = firstNonEmpty(status, "complete")
	r.criticLastSummary = summary
	if status != "" && status != "clean" {
		r.criticConcerns++
	}
	proposed := strings.TrimSpace(firstNonEmpty(rawJSONString(event["proposed_user_message"]), rawJSONString(event["human_prompt"])))
	if proposed != "" && status == "needs_followup" {
		packetHash := strings.TrimSpace(rawJSONString(event["packet_hash"]))
		if packetHash == "" {
			packetHash = strings.TrimSpace(rawJSONString(event["session_id"]))
		}
		r.suggestedInputDraftID = firstNonEmpty(packetHash, fmt.Sprintf("critic-%d", len(r.entries)+1))
		r.suggestedInputDraft = proposed
		r.criticFollowupDrafts++
	}
	r.appendEntry(TranscriptStatus, text)
}

func (r *lcagentReplay) applyCriticReviewFailed(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	message := firstNonEmpty(rawJSONString(event["message"]), "critic review failed")
	prefix := "LCAgent critic unavailable: "
	if strings.EqualFold(rawJSONString(event["failure_kind"]), "invalid_json") {
		prefix = "LCAgent critic invalid structured output: "
	}
	r.criticActive = false
	r.criticLastStatus = "failed"
	r.criticLastSummary = strings.TrimSpace(message)
	r.appendEntry(TranscriptStatus, prefix+message)
}

func (r *lcagentReplay) applyCriticLeadFeedback(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	text := lcagentCriticLeadFeedbackText(event)
	r.criticLeadRevisions++
	r.criticLastStatus = "lead revision"
	r.criticLastSummary = strings.TrimSpace(text)
	r.appendEntry(TranscriptStatus, text)
}

func (r *lcagentReplay) applyCriticConsultStarted(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	text := "LCAgent critic consulting"
	if question := rawJSONString(event["question"]); question != "" {
		text += ": " + lcagentCondenseStatusText(question, 160)
	}
	r.criticActive = true
	r.criticLastStatus = "consulting"
	r.criticLastSummary = ""
	r.appendEntry(TranscriptStatus, text)
}

func (r *lcagentReplay) applyCriticConsultResult(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	status := normalizeLCAgentCriticReviewStatus(rawJSONString(event["status"]))
	summary := strings.TrimSpace(rawJSONString(event["summary"]))
	modelName := rawJSONString(event["model"])
	if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
		r.addTokenUsage(usage)
	}
	text := lcagentCriticConsultResultText(status, summary)
	r.criticActive = false
	r.criticConsultations++
	r.criticLastStatus = firstNonEmpty(status, "consulted")
	r.criticLastSummary = summary
	if status != "" && status != "clean" {
		r.criticConsultConcerns++
	}
	r.appendEntry(TranscriptStatus, text)
}

func (r *lcagentReplay) applyCriticConsultFailed(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	message := firstNonEmpty(rawJSONString(event["message"]), "critic consultation failed")
	text := "LCAgent critic consultation failed: " + message
	r.criticActive = false
	r.criticLastStatus = "consult failed"
	r.criticLastSummary = strings.TrimSpace(message)
	r.appendEntry(TranscriptStatus, text)
}

func lcagentCriticReviewResultText(status, summary string) string {
	text := "LCAgent critic review complete"
	if status != "" && status != "clean" {
		text = "LCAgent critic found " + strings.ReplaceAll(status, "_", " ")
	} else if status == "clean" {
		text = "LCAgent critic found no concerns"
	}
	if strings.TrimSpace(summary) != "" {
		text += ": " + strings.TrimSpace(summary)
	}
	return text
}

func lcagentCriticConsultResultText(status, summary string) string {
	text := "LCAgent critic consultation complete"
	if status != "" && status != "clean" {
		text = "LCAgent critic consultation found " + strings.ReplaceAll(status, "_", " ")
	} else if status == "clean" {
		text = "LCAgent critic consultation found no concerns"
	}
	if strings.TrimSpace(summary) != "" {
		text += ": " + strings.TrimSpace(summary)
	}
	return text
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

func lcagentReplayBrowserActivity(event map[string]json.RawMessage, current browserctl.SessionActivity, state browserctl.SessionActivityState) browserctl.SessionActivity {
	activity := current.Normalize()
	activity.State = state
	activity.ServerName = firstNonEmpty(rawJSONString(event["server_name"]), "playwright")
	activity.ToolName = firstNonEmpty(rawJSONString(event["tool"]), rawJSONString(event["tool_name"]), activity.ToolName)
	activity.LastEventAt = rawJSONTime(event["timestamp"])
	return activity.Normalize()
}

func (r *lcagentReplay) observeBrowserPage(event map[string]json.RawMessage) {
	if r == nil {
		return
	}
	url := strings.TrimSpace(rawJSONString(event["url"]))
	if url == "" {
		return
	}
	r.currentBrowserPageURL = url
	fresh := true
	if _, ok := event["fresh"]; ok {
		fresh = rawJSONBool(event["fresh"])
	}
	r.currentBrowserPageStale = !fresh
	if key := strings.TrimSpace(rawJSONString(event["session_key"])); key != "" {
		r.managedBrowserSessionKey = key
	}
}

func (r *lcagentReplay) appendEntry(kind TranscriptKind, text string) {
	r.appendEntryWithID("", kind, text)
}

func (r *lcagentReplay) appendEntryWithID(itemID string, kind TranscriptKind, text string) {
	if r == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.TrimSpace(itemID) == "" {
		itemID = fmt.Sprintf("lcagent-replay-%d", len(r.entries)+1)
	}
	r.entries = append(r.entries, TranscriptEntry{
		ItemID: itemID,
		Kind:   kind,
		Text:   text,
	})
}

func (r *lcagentReplay) upsertEntry(itemID string, kind TranscriptKind, text string) {
	if r.upsertExistingEntry(itemID, kind, text) {
		return
	}
	r.appendEntryWithID(itemID, kind, text)
}

func (r *lcagentReplay) upsertExistingEntry(itemID string, kind TranscriptKind, text string) bool {
	if r == nil {
		return false
	}
	itemID = strings.TrimSpace(itemID)
	text = strings.TrimSpace(text)
	if itemID == "" || text == "" {
		return false
	}
	for index := len(r.entries) - 1; index >= 0; index-- {
		if r.entries[index].ItemID != itemID {
			continue
		}
		r.entries[index].Kind = kind
		r.entries[index].Text = text
		r.entries[index].DisplayText = ""
		r.entries[index].GeneratedImage = nil
		return true
	}
	return false
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
