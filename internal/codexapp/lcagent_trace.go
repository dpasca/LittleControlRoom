package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"lcroom/internal/lcagent/sessionmetrics"
	lcrmodel "lcroom/internal/model"
)

type LCAgentTrace struct {
	SessionID                 string
	ThreadID                  string
	ArtifactPath              string
	ProjectPath               string
	StartedAt                 time.Time
	LastActivityAt            time.Time
	ResumeSourceSessionID     string
	ResumeSourcePath          string
	ResumeSourceProject       string
	ResumeSourceSummary       string
	ResumeSourceLastAt        time.Time
	ContinuationRootSessionID string
	ContinuationChainDepth    int
	ContinuationReason        string
	ContinuationHandoffSource string
	PendingFiles              []string
	PendingVerification       []string
	PendingStatus             string
	Completed                 bool
	Aborted                   bool
	Summary                   string
	FinalOutcome              string
	FilesChanged              []string
	Verification              []string
	ActualChecks              []LCAgentVerificationCheck
	VerificationStatus        string
	PermissionDenials         []LCAgentPermissionDenial
	PatchDiffSummaries        []string
	PatchFeedback             []string
	VerificationSummaries     []string
	VerificationFeedback      []string
	RepairFeedbackSuppressed  []string
	RepairGuidance            []string
	ModelResponses            int
	TokenUsage                lcrmodel.LLMUsage
	TraceQuality              sessionmetrics.TraceQuality
	Errors                    []string
}

type LCAgentPermissionDenial struct {
	Tool   string
	Reason string
}

type LCAgentVerificationCheck struct {
	Command  string
	Argv     []string
	CWD      string
	Purpose  string
	Status   string
	Success  bool
	ExitCode int
	Error    string
}

func LoadLCAgentTrace(dataDir, sessionID, projectPath string) (LCAgentTrace, error) {
	sessionID = strings.TrimSpace(sessionID)
	projectPath = strings.TrimSpace(projectPath)
	var (
		path string
		err  error
	)
	if sessionID != "" {
		path, err = findLCAgentSessionFile(dataDir, sessionID)
	} else {
		path, err = findLatestLCAgentSessionFile(dataDir, projectPath)
	}
	if err != nil {
		return LCAgentTrace{}, err
	}
	if path == "" {
		if sessionID != "" {
			return LCAgentTrace{}, fmt.Errorf("LCAgent session artifact not found: %s", sessionID)
		}
		return LCAgentTrace{}, errors.New("LCAgent session artifact not found")
	}
	trace, err := ParseLCAgentTraceFile(path)
	if err != nil {
		return LCAgentTrace{}, err
	}
	if trace.SessionID == "" {
		trace.SessionID = sessionID
	}
	if trace.ThreadID == "" {
		trace.ThreadID = trace.SessionID
	}
	if projectPath != "" && trace.ProjectPath != "" && !sameCleanPath(projectPath, trace.ProjectPath) {
		return LCAgentTrace{}, fmt.Errorf("LCAgent session %s belongs to %s, not %s", firstNonEmpty(trace.SessionID, sessionID), trace.ProjectPath, projectPath)
	}
	return trace, nil
}

func ParseLCAgentTraceFile(path string) (LCAgentTrace, error) {
	file, err := os.Open(path)
	if err != nil {
		return LCAgentTrace{}, err
	}
	defer file.Close()

	trace := LCAgentTrace{ArtifactPath: path}
	if err := forEachLCAgentJSONLEvent(file, func(event map[string]json.RawMessage) {
		eventType := rawJSONString(event["type"])
		if eventType == "" {
			return
		}
		trace.observeEventTime(event)
		switch eventType {
		case "session_meta":
			trace.SessionID = rawJSONString(event["id"])
			trace.ThreadID = firstNonEmpty(rawJSONString(event["thread_id"]), trace.ThreadID)
			trace.ProjectPath = rawJSONString(event["cwd"])
			trace.StartedAt = rawJSONTime(event["started_at"])
			trace.ResumeSourceSessionID = firstNonEmpty(rawJSONString(event["parent_session_id"]), trace.ResumeSourceSessionID)
			trace.ContinuationRootSessionID = firstNonEmpty(rawJSONString(event["root_session_id"]), trace.ContinuationRootSessionID)
			if depth := rawJSONInt(event["continuation_depth"]); depth > trace.ContinuationChainDepth {
				trace.ContinuationChainDepth = depth
			}
			trace.ContinuationReason = firstNonEmpty(rawJSONString(event["continuation_reason"]), trace.ContinuationReason)
			trace.ContinuationHandoffSource = firstNonEmpty(rawJSONString(event["handoff_source"]), trace.ContinuationHandoffSource)
			if trace.LastActivityAt.IsZero() {
				trace.LastActivityAt = trace.StartedAt
			}
		case "model_response":
			trace.ModelResponses++
			modelName := rawJSONString(event["model"])
			if usage, ok := lcagentUsageFromModelResponseEvent(event, modelName); ok {
				trace.addTokenUsage(usage)
			}
		case "continuation":
			trace.ResumeSourceSessionID = firstNonEmpty(rawJSONString(event["parent_session_id"]), trace.ResumeSourceSessionID)
			trace.ResumeSourcePath = firstNonEmpty(rawJSONString(event["parent_path"]), trace.ResumeSourcePath)
			trace.ResumeSourceProject = firstNonEmpty(rawJSONString(event["parent_cwd"]), trace.ResumeSourceProject)
			trace.ResumeSourceSummary = firstNonEmpty(rawJSONString(event["parent_summary"]), trace.ResumeSourceSummary)
			trace.ResumeSourceLastAt = firstNonZeroTime(rawJSONTime(event["parent_last_activity"]), trace.ResumeSourceLastAt)
			trace.ContinuationRootSessionID = firstNonEmpty(rawJSONString(event["root_session_id"]), trace.ContinuationRootSessionID)
			if depth := rawJSONInt(event["chain_depth"]); depth > trace.ContinuationChainDepth {
				trace.ContinuationChainDepth = depth
			}
			trace.ContinuationReason = firstNonEmpty(rawJSONString(event["continuation_reason"]), trace.ContinuationReason)
			trace.ContinuationHandoffSource = firstNonEmpty(rawJSONString(event["handoff_source"]), trace.ContinuationHandoffSource)
			trace.PendingFiles = cleanLCAgentStringList(append(trace.PendingFiles, rawJSONStringList(event["pending_files"])...))
			trace.PendingVerification = cleanLCAgentStringList(append(trace.PendingVerification, rawJSONStringList(event["pending_verification"])...))
			trace.PendingStatus = firstNonEmpty(rawJSONString(event["pending_status"]), trace.PendingStatus)
		case "resume_context":
			trace.ResumeSourceSessionID = firstNonEmpty(rawJSONString(event["source_session_id"]), rawJSONString(event["parent_session_id"]), trace.ResumeSourceSessionID)
			trace.ResumeSourcePath = firstNonEmpty(rawJSONString(event["source_path"]), trace.ResumeSourcePath)
			trace.ResumeSourceProject = firstNonEmpty(rawJSONString(event["source_cwd"]), trace.ResumeSourceProject)
			trace.ResumeSourceSummary = firstNonEmpty(rawJSONString(event["summary"]), trace.ResumeSourceSummary)
			trace.ResumeSourceLastAt = firstNonZeroTime(rawJSONTime(event["source_last_activity"]), trace.ResumeSourceLastAt)
			trace.ContinuationRootSessionID = firstNonEmpty(rawJSONString(event["root_session_id"]), trace.ContinuationRootSessionID)
			if depth := rawJSONInt(event["chain_depth"]); depth > trace.ContinuationChainDepth {
				trace.ContinuationChainDepth = depth
			}
			trace.ContinuationReason = firstNonEmpty(rawJSONString(event["continuation_reason"]), trace.ContinuationReason)
			trace.ContinuationHandoffSource = firstNonEmpty(rawJSONString(event["handoff_source"]), trace.ContinuationHandoffSource)
			trace.PendingFiles = cleanLCAgentStringList(append(trace.PendingFiles, rawJSONStringList(event["pending_files"])...))
			trace.PendingVerification = cleanLCAgentStringList(append(trace.PendingVerification, rawJSONStringList(event["pending_verification"])...))
			trace.PendingStatus = firstNonEmpty(rawJSONString(event["pending_status"]), trace.PendingStatus)
		case "permission_denied":
			trace.PermissionDenials = append(trace.PermissionDenials, LCAgentPermissionDenial{
				Tool:   rawJSONString(event["tool"]),
				Reason: firstNonEmpty(rawJSONString(event["reason"]), "LCAgent permission denied"),
			})
		case "patch_diff_summary":
			if summary := rawJSONString(event["summary"]); summary != "" {
				trace.PatchDiffSummaries = append(trace.PatchDiffSummaries, summary)
			}
		case "patch_feedback":
			if message := rawJSONString(event["message"]); message != "" {
				trace.PatchFeedback = append(trace.PatchFeedback, message)
			}
		case "verification_check":
			trace.ActualChecks = append(trace.ActualChecks, lcagentVerificationCheckFromEvent(event))
		case "verification_feedback":
			if message := rawJSONString(event["message"]); message != "" {
				trace.VerificationFeedback = append(trace.VerificationFeedback, message)
			}
		case "repair_feedback_suppressed":
			if message := lcagentRepairFeedbackSuppressedText(event); message != "" {
				trace.RepairFeedbackSuppressed = append(trace.RepairFeedbackSuppressed, message)
			}
		case "repair_guidance":
			if message := lcagentRepairGuidanceText(event); message != "" {
				trace.RepairGuidance = append(trace.RepairGuidance, message)
			}
		case "verification_summary":
			status := rawJSONString(event["status"])
			actualChecks := lcagentVerificationChecksFromRaw(event["actual_checks"])
			if len(trace.ActualChecks) == 0 {
				trace.ActualChecks = append(trace.ActualChecks, actualChecks...)
			}
			if len(actualChecks) == 0 {
				actualChecks = trace.ActualChecks
			}
			if corrected := correctedLCAgentVerificationStatus(status, rawJSONStringList(event["verification_checks"]), actualChecks); corrected != "" {
				trace.VerificationStatus = corrected
			} else if status != "" {
				trace.VerificationStatus = status
			}
			if message := rawJSONString(event["message"]); message != "" {
				trace.VerificationSummaries = append(trace.VerificationSummaries, message)
			}
		case "assistant_message":
			if trace.Summary == "" {
				trace.Summary = rawJSONString(event["message"])
			}
			trace.FinalOutcome = firstNonEmpty(rawJSONString(event["final_outcome"]), trace.FinalOutcome)
		case "final_response_audit":
			trace.FinalOutcome = firstNonEmpty(rawJSONString(event["final_outcome"]), trace.FinalOutcome)
		case "turn_complete":
			trace.Completed = true
			trace.Summary = firstNonEmpty(rawJSONString(event["summary"]), trace.Summary)
			trace.FinalOutcome = firstNonEmpty(rawJSONString(event["final_outcome"]), trace.FinalOutcome)
			trace.FilesChanged = rawJSONStringList(event["files_changed"])
			trace.Verification = rawJSONStringList(event["verification"])
			status := firstNonEmpty(rawJSONString(event["verification_status"]), trace.VerificationStatus)
			actualChecks := lcagentVerificationChecksFromRaw(event["actual_checks"])
			if len(trace.ActualChecks) == 0 {
				trace.ActualChecks = append(trace.ActualChecks, actualChecks...)
			}
			if len(actualChecks) == 0 {
				actualChecks = trace.ActualChecks
			}
			trace.VerificationStatus = firstNonEmpty(correctedLCAgentVerificationStatus(status, trace.Verification, actualChecks), status)
		case "turn_aborted":
			trace.Aborted = true
			reason := firstNonEmpty(rawJSONString(event["reason"]), "LCAgent run aborted")
			trace.Errors = append(trace.Errors, reason)
		}
	}); err != nil {
		return LCAgentTrace{}, err
	}
	if summary, err := sessionmetrics.AnalyzeFiles([]string{path}); err == nil {
		trace.TraceQuality = summary.TraceQuality
	}
	if trace.ThreadID == "" {
		trace.ThreadID = trace.SessionID
	}
	return trace, nil
}

func (t LCAgentTrace) Verified() bool {
	status := strings.ToLower(strings.TrimSpace(t.VerificationStatus))
	return t.Completed && !t.Aborted && (status == "verified" || status == "reported")
}

func (t LCAgentTrace) CompletionError() error {
	switch {
	case t.Aborted:
		return fmt.Errorf("LCAgent run aborted: %s", firstNonEmpty(strings.Join(t.Errors, "; "), "unknown reason"))
	case !t.Completed:
		return errors.New("LCAgent run did not record turn_complete")
	case !t.Verified():
		status := firstNonEmpty(t.VerificationStatus, "unknown")
		return fmt.Errorf("LCAgent verification status was %s", status)
	default:
		return nil
	}
}

func (t LCAgentTrace) CompactSummary() string {
	parts := []string{}
	if id := strings.TrimSpace(t.SessionID); id != "" {
		parts = append(parts, "session "+id)
	}
	if t.Completed {
		parts = append(parts, "completed")
	}
	if t.Aborted {
		parts = append(parts, "aborted")
	}
	if source := strings.TrimSpace(t.ResumeSourceSessionID); source != "" {
		parts = append(parts, "continued from "+source)
	}
	if t.ContinuationChainDepth > 0 {
		parts = append(parts, fmt.Sprintf("continuation depth %d", t.ContinuationChainDepth))
	}
	if status := strings.TrimSpace(t.VerificationStatus); status != "" {
		parts = append(parts, "verification "+status)
	}
	if outcome := strings.TrimSpace(t.FinalOutcome); outcome != "" {
		parts = append(parts, "outcome "+outcome)
	}
	if pending := strings.TrimSpace(t.PendingStatus); pending != "" {
		parts = append(parts, "pending verification "+pending)
	}
	if len(t.PendingFiles) > 0 {
		parts = append(parts, fmt.Sprintf("%d pending file%s", len(t.PendingFiles), pluralSuffix(len(t.PendingFiles))))
	}
	if len(t.FilesChanged) > 0 {
		parts = append(parts, fmt.Sprintf("%d file%s changed", len(t.FilesChanged), pluralSuffix(len(t.FilesChanged))))
	}
	if len(t.ActualChecks) > 0 {
		checks := t.ActualCheckSummaries()
		parts = append(parts, fmt.Sprintf("%d verification check%s: %s", len(t.ActualChecks), pluralSuffix(len(t.ActualChecks)), strings.Join(limitStrings(checks, 2), "; ")))
	}
	if len(t.VerificationFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("%d verification feedback item%s", len(t.VerificationFeedback), pluralSuffix(len(t.VerificationFeedback))))
	}
	if len(t.RepairFeedbackSuppressed) > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicate repair feedback item%s suppressed", len(t.RepairFeedbackSuppressed), pluralSuffix(len(t.RepairFeedbackSuppressed))))
	}
	if len(t.RepairGuidance) > 0 {
		parts = append(parts, fmt.Sprintf("%d repair guidance item%s", len(t.RepairGuidance), pluralSuffix(len(t.RepairGuidance))))
	}
	if len(t.PermissionDenials) > 0 {
		parts = append(parts, fmt.Sprintf("%d denial%s", len(t.PermissionDenials), pluralSuffix(len(t.PermissionDenials))))
	}
	if len(t.PatchFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("%d patch feedback item%s", len(t.PatchFeedback), pluralSuffix(len(t.PatchFeedback))))
	}
	if len(t.PatchDiffSummaries) > 0 {
		parts = append(parts, fmt.Sprintf("%d patch diff summar%s", len(t.PatchDiffSummaries), pluralY(len(t.PatchDiffSummaries))))
	}
	if usageSummary := t.TokenUsageSummary(); usageSummary != "" {
		parts = append(parts, usageSummary)
	}
	if summary := strings.TrimSpace(t.Summary); summary != "" {
		parts = append(parts, summary)
	}
	return strings.Join(parts, "; ")
}

func (t LCAgentTrace) TraceQualitySummary() string {
	parts := []string{}
	if quality := t.TraceQualitySummaryLabel(); quality != "" {
		parts = append(parts, quality)
	}
	if status := strings.TrimSpace(t.VerificationStatus); status != "" {
		parts = append(parts, "verification "+status)
	}
	if outcome := strings.TrimSpace(t.FinalOutcome); outcome != "" {
		parts = append(parts, "outcome "+outcome)
	}
	if source := strings.TrimSpace(t.ResumeSourceSessionID); source != "" {
		parts = append(parts, "continuation: "+source)
	}
	if t.ContinuationChainDepth > 0 {
		parts = append(parts, fmt.Sprintf("continuation depth: %d", t.ContinuationChainDepth))
	}
	if source := strings.TrimSpace(t.ContinuationHandoffSource); source != "" {
		parts = append(parts, "handoff source: "+source)
	}
	if len(t.PendingFiles) > 0 {
		parts = append(parts, "pending files: "+strings.Join(limitStrings(t.PendingFiles, 4), ", "))
	}
	if checks := t.ActualCheckSummaries(); len(checks) > 0 {
		parts = append(parts, "actual checks: "+strings.Join(limitStrings(checks, 3), "; "))
	}
	if len(t.FilesChanged) > 0 {
		parts = append(parts, "files changed: "+strings.Join(limitStrings(t.FilesChanged, 4), ", "))
	}
	if len(t.PermissionDenials) > 0 {
		parts = append(parts, fmt.Sprintf("denials: %d", len(t.PermissionDenials)))
	}
	if len(t.PatchFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("patch feedback: %d", len(t.PatchFeedback)))
	}
	if len(t.VerificationFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("verification feedback: %d", len(t.VerificationFeedback)))
	}
	if len(t.RepairFeedbackSuppressed) > 0 {
		parts = append(parts, fmt.Sprintf("duplicate repair feedback suppressed: %d", len(t.RepairFeedbackSuppressed)))
	}
	if len(t.RepairGuidance) > 0 {
		parts = append(parts, fmt.Sprintf("repair guidance: %d", len(t.RepairGuidance)))
	}
	if len(t.PatchDiffSummaries) > 0 {
		parts = append(parts, fmt.Sprintf("patch summaries: %d", len(t.PatchDiffSummaries)))
	}
	if usageSummary := t.TokenUsageSummary(); usageSummary != "" {
		parts = append(parts, usageSummary)
	}
	return strings.Join(parts, "; ")
}

func (t LCAgentTrace) TraceQualitySummaryLabel() string {
	grade := strings.TrimSpace(t.TraceQuality.Grade)
	if grade == "" && t.TraceQuality.Score == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("trace quality: %d", t.TraceQuality.Score)}
	if grade != "" {
		parts[0] += "/" + grade
	}
	if t.TraceQuality.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("tool failures: %d", t.TraceQuality.ToolFailures))
	}
	if t.TraceQuality.ProviderFailures > 0 {
		parts = append(parts, fmt.Sprintf("provider failures: %d", t.TraceQuality.ProviderFailures))
	}
	if t.TraceQuality.ProviderRetries > 0 {
		parts = append(parts, fmt.Sprintf("provider retries: %d", t.TraceQuality.ProviderRetries))
	}
	if t.TraceQuality.RepairEvents > 0 {
		parts = append(parts, fmt.Sprintf("repair events: %d", t.TraceQuality.RepairEvents))
	}
	return strings.Join(parts, ", ")
}

func (t LCAgentTrace) ActualCheckSummaries() []string {
	out := make([]string, 0, len(t.ActualChecks))
	for _, check := range t.ActualChecks {
		text := formatLCAgentVerificationCheck(check)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func (t LCAgentTrace) TokenUsageSummary() string {
	if !lcagentUsageTracked(t.TokenUsage) {
		return ""
	}
	parts := []string{}
	total := t.TokenUsage.TotalTokens
	if total == 0 && (t.TokenUsage.InputTokens != 0 || t.TokenUsage.OutputTokens != 0) {
		total = t.TokenUsage.InputTokens + t.TokenUsage.OutputTokens
	}
	if total > 0 {
		parts = append(parts, fmt.Sprintf("tokens: %d", total))
	}
	if t.TokenUsage.CachedInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("cached: %d", t.TokenUsage.CachedInputTokens))
	}
	if t.TokenUsage.ReasoningTokens > 0 {
		parts = append(parts, fmt.Sprintf("reasoning: %d", t.TokenUsage.ReasoningTokens))
	}
	if t.TokenUsage.EstimatedCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("cost: $%.4f", t.TokenUsage.EstimatedCostUSD))
	}
	return strings.Join(parts, ", ")
}

func (t *LCAgentTrace) addTokenUsage(usage lcrmodel.LLMUsage) {
	t.TokenUsage.InputTokens += usage.InputTokens
	t.TokenUsage.OutputTokens += usage.OutputTokens
	t.TokenUsage.TotalTokens += usage.TotalTokens
	t.TokenUsage.CachedInputTokens += usage.CachedInputTokens
	t.TokenUsage.ReasoningTokens += usage.ReasoningTokens
	t.TokenUsage.EstimatedCostUSD += usage.EstimatedCostUSD
}

func (t *LCAgentTrace) observeEventTime(event map[string]json.RawMessage) {
	if t == nil {
		return
	}
	at := rawJSONTime(event["timestamp"])
	if at.IsZero() {
		at = rawJSONTime(event["started_at"])
	}
	if !at.IsZero() && (t.LastActivityAt.IsZero() || at.After(t.LastActivityAt)) {
		t.LastActivityAt = at
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func rawJSONStringList(raw json.RawMessage) []string {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return cleanLCAgentStringList(values)
}

func cleanLCAgentStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func lcagentTurnCompleteTraceText(event map[string]json.RawMessage) string {
	status := firstNonEmpty(rawJSONString(event["verification_status"]), "unknown")
	files := rawJSONStringList(event["files_changed"])
	verification := rawJSONStringList(event["verification"])
	parts := []string{"Trace: verification " + status}
	if len(files) > 0 {
		parts = append(parts, "files "+strings.Join(limitStrings(files, 4), ", "))
	}
	if len(verification) > 0 {
		parts = append(parts, "checks "+strings.Join(limitStrings(verification, 3), ", "))
	}
	actualChecks := lcagentVerificationChecksFromRaw(event["actual_checks"])
	if len(actualChecks) > 0 {
		var checkText []string
		for _, check := range actualChecks {
			if text := formatLCAgentVerificationCheck(check); text != "" {
				checkText = append(checkText, text)
			}
		}
		parts = append(parts, "actual "+strings.Join(limitStrings(checkText, 3), "; "))
	}
	return strings.Join(parts, "; ")
}

func lcagentVerificationCheckFromEvent(event map[string]json.RawMessage) LCAgentVerificationCheck {
	return LCAgentVerificationCheck{
		Command:  rawJSONString(event["command"]),
		Argv:     rawJSONStringList(event["argv"]),
		CWD:      rawJSONString(event["cwd"]),
		Purpose:  rawJSONString(event["purpose"]),
		Status:   firstNonEmpty(rawJSONString(event["status"]), "unknown"),
		Success:  rawJSONBool(event["success"]),
		ExitCode: rawJSONInt(event["exit_code"]),
		Error:    rawJSONString(event["error"]),
	}
}

func lcagentVerificationChecksFromRaw(raw json.RawMessage) []LCAgentVerificationCheck {
	var checks []LCAgentVerificationCheck
	if err := json.Unmarshal(raw, &checks); err != nil {
		return nil
	}
	out := checks[:0]
	for _, check := range checks {
		check.Command = strings.TrimSpace(check.Command)
		check.Argv = cleanLCAgentStringList(check.Argv)
		check.CWD = strings.TrimSpace(check.CWD)
		check.Purpose = strings.TrimSpace(check.Purpose)
		check.Status = firstNonEmpty(strings.TrimSpace(check.Status), "unknown")
		check.Error = strings.TrimSpace(check.Error)
		if check.Command == "" && len(check.Argv) == 0 && check.Status == "unknown" {
			continue
		}
		out = append(out, check)
	}
	return out
}

func formatLCAgentVerificationCheck(check LCAgentVerificationCheck) string {
	label := firstNonEmpty(strings.TrimSpace(check.Command), strings.Join(check.Argv, " "), "verification check")
	if cwd := strings.TrimSpace(check.CWD); cwd != "" {
		label += " in " + cwd
	}
	status := firstNonEmpty(strings.TrimSpace(check.Status), "unknown")
	text := label + " " + status
	if check.ExitCode != 0 {
		text += fmt.Sprintf(" exit %d", check.ExitCode)
	}
	return strings.TrimSpace(text)
}

func correctedLCAgentVerificationStatus(status string, reported []string, actual []LCAgentVerificationCheck) string {
	status = strings.TrimSpace(status)
	if len(actual) == 0 {
		return status
	}
	checks := relevantLCAgentVerificationChecks(reported, actual)
	if len(checks) == 0 {
		return status
	}
	checks = latestLCAgentVerificationOutcomes(checks)
	for _, check := range checks {
		if !lcagentVerificationCheckPassed(check) {
			return firstNonEmpty(strings.TrimSpace(check.Status), status, "failed")
		}
	}
	return "verified"
}

func relevantLCAgentVerificationChecks(reported []string, actual []LCAgentVerificationCheck) []LCAgentVerificationCheck {
	if len(reported) == 0 {
		return actual
	}
	relevant := make([]LCAgentVerificationCheck, 0, len(reported))
	seen := map[int]bool{}
	for _, item := range reported {
		index := latestReportedLCAgentCheckIndex(item, actual)
		if index < 0 || seen[index] {
			continue
		}
		seen[index] = true
		relevant = append(relevant, actual[index])
	}
	return relevant
}

func latestReportedLCAgentCheckIndex(item string, actual []LCAgentVerificationCheck) int {
	for index := len(actual) - 1; index >= 0; index-- {
		if lcagentVerificationReportMatchesCheck(item, actual[index]) {
			return index
		}
	}
	return -1
}

func lcagentVerificationReportMatchesCheck(item string, check LCAgentVerificationCheck) bool {
	item = strings.ToLower(strings.TrimSpace(item))
	label := strings.ToLower(normalizedLCAgentVerificationCommand(lcagentVerificationCheckCommand(check)))
	return item != "" && label != "" && strings.Contains(item, label)
}

func latestLCAgentVerificationOutcomes(checks []LCAgentVerificationCheck) []LCAgentVerificationCheck {
	out := make([]LCAgentVerificationCheck, 0, len(checks))
	indexByLabel := map[string]int{}
	for _, check := range checks {
		label := normalizedLCAgentVerificationCommand(lcagentVerificationCheckCommand(check))
		if label == "" {
			label = "verification check"
		}
		if index, ok := indexByLabel[label]; ok {
			out[index] = check
			continue
		}
		indexByLabel[label] = len(out)
		out = append(out, check)
	}
	return out
}

func lcagentVerificationCheckPassed(check LCAgentVerificationCheck) bool {
	status := strings.ToLower(strings.TrimSpace(check.Status))
	return check.Success || status == "passed" || status == "verified"
}

func lcagentVerificationCheckCommand(check LCAgentVerificationCheck) string {
	if command := strings.TrimSpace(check.Command); command != "" {
		return command
	}
	return strings.Join(cleanLCAgentStringList(check.Argv), " ")
}

func normalizedLCAgentVerificationCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	segments := strings.Split(command, "&&")
	for index := len(segments) - 1; index >= 0; index-- {
		segment := strings.TrimSpace(segments[index])
		if segment == "" || isLCAgentShellBookkeepingCommand(segment) || lcagentShellLeadingCDCWD(segment) != "" {
			continue
		}
		return segment
	}
	return command
}

func isLCAgentShellBookkeepingCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return true
	}
	switch fields[0] {
	case "pwd":
		return len(fields) == 1
	case "ls":
		return len(fields) == 1 || (len(fields) == 2 && fields[1] == "package.json")
	default:
		return false
	}
}

func lcagentShellLeadingCDCWD(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 || fields[0] != "cd" {
		return ""
	}
	cwd := strings.Trim(fields[1], `"'`)
	if cwd == "" || cwd == "-" {
		return ""
	}
	return cwd
}

func lcagentVerificationCheckText(event map[string]json.RawMessage) string {
	check := lcagentVerificationCheckFromEvent(event)
	label := firstNonEmpty(check.Command, strings.Join(check.Argv, " "), "verification check")
	if check.CWD != "" {
		label += " in " + check.CWD
	}
	status := firstNonEmpty(check.Status, "unknown")
	text := "Verification " + status + ": " + label
	if check.ExitCode != 0 {
		text += fmt.Sprintf(" (exit %d)", check.ExitCode)
	}
	if check.Error != "" {
		text += ": " + check.Error
	}
	return text
}

func lcagentResumeContextText(event map[string]json.RawMessage) string {
	sourceID := firstNonEmpty(rawJSONString(event["source_session_id"]), rawJSONString(event["parent_session_id"]))
	sourcePath := rawJSONString(event["source_path"])
	text := "Loaded summarized LCAgent context"
	if rawJSONString(event["context_mode"]) == "exact" {
		text = "Loaded exact LCAgent context"
	}
	if sourceID != "" {
		text += " from " + sourceID
	}
	if sourcePath != "" {
		text += " (" + sourcePath + ")"
	}
	var details []string
	if depth := rawJSONInt(event["chain_depth"]); depth > 0 {
		details = append(details, fmt.Sprintf("depth %d", depth))
	}
	if source := rawJSONString(event["handoff_source"]); source != "" {
		details = append(details, "handoff "+source)
	}
	if count := rawJSONInt(event["exact_message_count"]); rawJSONString(event["context_mode"]) == "exact" && count > 0 {
		details = append(details, fmt.Sprintf("%d replay messages", count))
	}
	if status := rawJSONString(event["pending_status"]); status != "" {
		details = append(details, "pending verification "+status)
	}
	if len(details) > 0 {
		text += " [" + strings.Join(details, "; ") + "]"
	}
	return text
}

func lcagentContinuationText(event map[string]json.RawMessage) string {
	parentID := rawJSONString(event["parent_session_id"])
	parentPath := rawJSONString(event["parent_path"])
	text := "Continuing LCAgent"
	if parentID != "" {
		text += " from " + parentID
	}
	if parentPath != "" {
		text += " (" + parentPath + ")"
	}
	var details []string
	if depth := rawJSONInt(event["chain_depth"]); depth > 0 {
		details = append(details, fmt.Sprintf("depth %d", depth))
	}
	if root := rawJSONString(event["root_session_id"]); root != "" && root != parentID {
		details = append(details, "root "+root)
	}
	if reason := rawJSONString(event["continuation_reason"]); reason != "" {
		details = append(details, "reason "+reason)
	}
	if source := rawJSONString(event["handoff_source"]); source != "" {
		details = append(details, "handoff "+source)
	}
	if rawJSONString(event["context_mode"]) == "exact" {
		detail := "exact replay"
		if count := rawJSONInt(event["exact_message_count"]); count > 0 {
			detail = fmt.Sprintf("exact replay %d messages", count)
		}
		details = append(details, detail)
	} else if rawJSONString(event["context_mode"]) == "summary" {
		details = append(details, "summary fallback")
	}
	if status := rawJSONString(event["pending_status"]); status != "" {
		details = append(details, "pending verification "+status)
	}
	if pendingFiles := rawJSONStringList(event["pending_files"]); len(pendingFiles) > 0 {
		details = append(details, "pending files "+strings.Join(limitStrings(pendingFiles, 4), ", "))
	}
	if len(details) > 0 {
		text += " [" + strings.Join(details, "; ") + "]"
	}
	return text
}

func lcagentContextCompactedText(event map[string]json.RawMessage) string {
	reason := strings.TrimSpace(rawJSONString(event["reason"]))
	text := "LCAgent compacted context"
	if reason != "" {
		text += " [" + strings.ReplaceAll(reason, "_", " ") + "]"
	}
	stats := map[string]json.RawMessage{}
	_ = json.Unmarshal(event["stats"], &stats)
	originalChars := firstPositiveInt(rawJSONInt(stats["original_chars"]), rawJSONInt(stats["before_chars"]))
	compactedChars := firstPositiveInt(rawJSONInt(stats["compacted_chars"]), rawJSONInt(stats["after_chars"]))
	var details []string
	if originalChars > 0 && compactedChars > 0 {
		details = append(details, fmt.Sprintf("%d -> %d chars", originalChars, compactedChars))
	}
	if threshold := rawJSONInt(event["threshold"]); threshold > 0 {
		details = append(details, fmt.Sprintf("threshold %d", threshold))
	}
	if len(details) > 0 {
		text += ": " + strings.Join(details, "; ")
	}
	return text
}

func lcagentVerificationFeedbackText(event map[string]json.RawMessage) string {
	message := rawJSONString(event["message"])
	if message != "" {
		return message
	}
	status := firstNonEmpty(rawJSONString(event["status"]), "needs attention")
	command := rawJSONString(event["command"])
	if command != "" {
		return "Verification feedback: " + command + " is " + status
	}
	return "Verification feedback: " + status
}

func lcagentRepairFeedbackSuppressedText(event map[string]json.RawMessage) string {
	kind := firstNonEmpty(rawJSONString(event["kind"]), "repair")
	count := rawJSONInt(event["count"])
	message := rawJSONString(event["message"])
	if count > 0 && message != "" {
		return fmt.Sprintf("Suppressed duplicate %s feedback after %d repeats: %s", kind, count, message)
	}
	if message != "" {
		return "Suppressed duplicate " + kind + " feedback: " + message
	}
	return "Suppressed duplicate " + kind + " feedback"
}

func lcagentRepairGuidanceText(event map[string]json.RawMessage) string {
	message := rawJSONString(event["message"])
	if message != "" {
		return message
	}
	kind := firstNonEmpty(rawJSONString(event["kind"]), "repair")
	return "Repair guidance: " + kind
}

func lcagentModelRequestItemID(event map[string]json.RawMessage) string {
	sessionID := lcagentTranscriptIDPart(firstNonEmpty(rawJSONString(event["session_id"]), rawJSONString(event["id"])))
	phase := lcagentTranscriptIDPart(rawJSONString(event["phase"]))
	turn := rawJSONInt(event["turn"])
	if sessionID == "" && phase == "" && turn <= 0 {
		return ""
	}
	return fmt.Sprintf("lcagent-model-request-%s-%s-%d", sessionID, phase, turn)
}

func lcagentTranscriptIDPart(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	value = strings.Join(fields, "-")
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, value)
	return strings.Trim(value, "-")
}

func lcagentModelRequestText(event map[string]json.RawMessage) string {
	eventType := rawJSONString(event["type"])
	prefix := "LCAgent requested model response/tool call"
	if eventType == "model_request_progress" {
		prefix = "LCAgent still waiting for model response/tool call"
	}
	return lcagentModelLifecycleText(prefix, event, 0)
}

func lcagentModelResponseText(event map[string]json.RawMessage) string {
	prefix := "LCAgent model response/tool call received"
	if rawJSONBool(event["invalid"]) {
		prefix = "LCAgent model response was unusable"
	}
	return lcagentModelLifecycleText(prefix, event, rawJSONInt(event["tool_call_count"]))
}

func lcagentModelRequestFailureText(event map[string]json.RawMessage) string {
	text := lcagentModelLifecycleText("LCAgent model request failed", event, 0)
	if kind := rawJSONString(event["kind"]); kind != "" {
		text += "; " + kind
	}
	return text
}

func lcagentModelLifecycleText(prefix string, event map[string]json.RawMessage, toolCallCount int) string {
	parts := []string{}
	if turn := rawJSONInt(event["turn"]); turn > 0 {
		parts = append(parts, fmt.Sprintf("turn %d", turn))
	}
	if phase := strings.ReplaceAll(rawJSONString(event["phase"]), "_", " "); phase != "" {
		parts = append(parts, phase)
	}
	if provider := rawJSONString(event["provider"]); provider != "" {
		parts = append(parts, provider)
	}
	if model := rawJSONString(event["model"]); model != "" {
		parts = append(parts, model)
	}
	if attempt := rawJSONInt(event["attempt"]); attempt > 1 {
		parts = append(parts, fmt.Sprintf("attempt %d", attempt))
	}
	if elapsedMS := rawJSONInt(event["elapsed_ms"]); elapsedMS > 0 {
		parts = append(parts, "elapsed "+formatTurnStatusDuration(time.Duration(elapsedMS)*time.Millisecond))
	}
	if toolCallCount > 0 {
		label := "tool calls"
		if toolCallCount == 1 {
			label = "tool call"
		}
		parts = append(parts, fmt.Sprintf("%d %s", toolCallCount, label))
	} else if finish := rawJSONString(event["finish_reason"]); finish != "" {
		parts = append(parts, "finish "+finish)
	}
	if len(parts) == 0 {
		return strings.TrimSpace(prefix)
	}
	return strings.TrimSpace(prefix) + ": " + strings.Join(parts, "; ")
}

func lcagentProviderFailureText(event map[string]json.RawMessage) string {
	provider := firstNonEmpty(rawJSONString(event["provider"]), "provider")
	kind := firstNonEmpty(rawJSONString(event["kind"]), "failure")
	message := rawJSONString(event["message"])
	attempt := rawJSONInt(event["attempt"])
	retrying := rawJSONBool(event["retrying"])
	parts := []string{fmt.Sprintf("LCAgent %s failure: %s", provider, kind)}
	if attempt > 0 {
		parts = append(parts, fmt.Sprintf("attempt %d", attempt))
	}
	if retrying {
		parts = append(parts, "retrying")
	}
	if delayMS := rawJSONInt(event["retry_delay_ms"]); delayMS > 0 {
		parts = append(parts, fmt.Sprintf("retry delay %dms", delayMS))
	}
	if message != "" {
		parts = append(parts, message)
	}
	if action := lcagentProviderFailureAction(kind, retrying); action != "" {
		parts = append(parts, action)
	}
	return strings.Join(parts, "; ")
}

func lcagentProviderFailureAction(kind string, retrying bool) string {
	switch strings.TrimSpace(kind) {
	case "rate_limited":
		if retrying {
			return "waiting for the provider rate limit before retry"
		}
		return "try again later, lower concurrency, or switch route/provider"
	case "quota":
		return "check provider credits/quota or choose another configured route"
	case "auth":
		return "check the LCAgent env file and provider API key"
	case "timeout":
		if retrying {
			return "retrying after timeout; narrow the task or raise request-timeout if this repeats"
		}
		return "narrow the task or raise request-timeout before retrying"
	case "malformed_response", "provider_schema":
		return "provider response shape was not usable; retry with another model/route if it repeats"
	case "transient_http":
		if retrying {
			return "transient provider error; retry scheduled"
		}
		return "transient provider error; retry or switch route if it persists"
	default:
		if retrying {
			return "retry scheduled"
		}
		return ""
	}
}

func lcagentProviderRetryText(event map[string]json.RawMessage) string {
	provider := firstNonEmpty(rawJSONString(event["provider"]), "provider")
	attempt := rawJSONInt(event["attempt"])
	delayMS := rawJSONInt(event["delay_ms"])
	text := fmt.Sprintf("LCAgent retrying %s request", provider)
	if attempt > 0 {
		text += fmt.Sprintf(" (attempt %d)", attempt)
	}
	if delayMS > 0 {
		text += fmt.Sprintf(" after %dms", delayMS)
	}
	return text
}

func lcagentProviderRetrySucceededText(event map[string]json.RawMessage) string {
	provider := firstNonEmpty(rawJSONString(event["provider"]), "provider")
	attempt := rawJSONInt(event["attempt"])
	if attempt > 0 {
		return fmt.Sprintf("LCAgent %s request recovered on attempt %d", provider, attempt)
	}
	return "LCAgent " + provider + " request recovered after retry"
}

func lcagentPatchFeedbackText(event map[string]json.RawMessage) string {
	message := rawJSONString(event["message"])
	if message != "" {
		return message
	}
	path := rawJSONString(event["path"])
	stage := firstNonEmpty(rawJSONString(event["stage"]), "apply_patch")
	if path != "" {
		return "Patch feedback: " + path + " failed during " + stage
	}
	return "Patch feedback: " + stage
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	out := append([]string(nil), values[:limit]...)
	out = append(out, fmt.Sprintf("%d more", len(values)-limit))
	return out
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func pluralY(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}
