package codexapp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type LCAgentTrace struct {
	SessionID             string
	ArtifactPath          string
	ProjectPath           string
	StartedAt             time.Time
	LastActivityAt        time.Time
	Completed             bool
	Aborted               bool
	Summary               string
	FilesChanged          []string
	Verification          []string
	ActualChecks          []LCAgentVerificationCheck
	VerificationStatus    string
	PermissionDenials     []LCAgentPermissionDenial
	PatchDiffSummaries    []string
	PatchFeedback         []string
	VerificationSummaries []string
	VerificationFeedback  []string
	Errors                []string
}

type LCAgentPermissionDenial struct {
	Tool   string
	Reason string
}

type LCAgentVerificationCheck struct {
	Command  string
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
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		eventType := rawJSONString(event["type"])
		if eventType == "" {
			continue
		}
		trace.observeEventTime(event)
		switch eventType {
		case "session_meta":
			trace.SessionID = rawJSONString(event["id"])
			trace.ProjectPath = rawJSONString(event["cwd"])
			trace.StartedAt = rawJSONTime(event["started_at"])
			if trace.LastActivityAt.IsZero() {
				trace.LastActivityAt = trace.StartedAt
			}
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
		case "verification_summary":
			status := rawJSONString(event["status"])
			if status != "" {
				trace.VerificationStatus = status
			}
			if len(trace.ActualChecks) == 0 {
				trace.ActualChecks = append(trace.ActualChecks, lcagentVerificationChecksFromRaw(event["actual_checks"])...)
			}
			if message := rawJSONString(event["message"]); message != "" {
				trace.VerificationSummaries = append(trace.VerificationSummaries, message)
			}
		case "assistant_message":
			if trace.Summary == "" {
				trace.Summary = rawJSONString(event["message"])
			}
		case "turn_complete":
			trace.Completed = true
			trace.Summary = firstNonEmpty(rawJSONString(event["summary"]), trace.Summary)
			trace.FilesChanged = rawJSONStringList(event["files_changed"])
			trace.Verification = rawJSONStringList(event["verification"])
			trace.VerificationStatus = firstNonEmpty(rawJSONString(event["verification_status"]), trace.VerificationStatus)
			if len(trace.ActualChecks) == 0 {
				trace.ActualChecks = append(trace.ActualChecks, lcagentVerificationChecksFromRaw(event["actual_checks"])...)
			}
		case "turn_aborted":
			trace.Aborted = true
			reason := firstNonEmpty(rawJSONString(event["reason"]), "LCAgent run aborted")
			trace.Errors = append(trace.Errors, reason)
		}
	}
	if err := scanner.Err(); err != nil {
		return LCAgentTrace{}, err
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
	if status := strings.TrimSpace(t.VerificationStatus); status != "" {
		parts = append(parts, "verification "+status)
	}
	if len(t.FilesChanged) > 0 {
		parts = append(parts, fmt.Sprintf("%d file%s changed", len(t.FilesChanged), pluralSuffix(len(t.FilesChanged))))
	}
	if len(t.ActualChecks) > 0 {
		parts = append(parts, fmt.Sprintf("%d verification check%s", len(t.ActualChecks), pluralSuffix(len(t.ActualChecks))))
	}
	if len(t.VerificationFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("%d verification feedback item%s", len(t.VerificationFeedback), pluralSuffix(len(t.VerificationFeedback))))
	}
	if len(t.PermissionDenials) > 0 {
		parts = append(parts, fmt.Sprintf("%d denial%s", len(t.PermissionDenials), pluralSuffix(len(t.PermissionDenials))))
	}
	if len(t.PatchFeedback) > 0 {
		parts = append(parts, fmt.Sprintf("%d patch feedback item%s", len(t.PatchFeedback), pluralSuffix(len(t.PatchFeedback))))
	}
	if summary := strings.TrimSpace(t.Summary); summary != "" {
		parts = append(parts, summary)
	}
	return strings.Join(parts, "; ")
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

func rawJSONStringList(raw json.RawMessage) []string {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
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
		parts = append(parts, fmt.Sprintf("actual %d", len(actualChecks)))
	}
	return strings.Join(parts, "; ")
}

func lcagentVerificationCheckFromEvent(event map[string]json.RawMessage) LCAgentVerificationCheck {
	return LCAgentVerificationCheck{
		Command:  rawJSONString(event["command"]),
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
		check.Status = firstNonEmpty(strings.TrimSpace(check.Status), "unknown")
		check.Error = strings.TrimSpace(check.Error)
		if check.Command == "" && check.Status == "unknown" {
			continue
		}
		out = append(out, check)
	}
	return out
}

func lcagentVerificationCheckText(event map[string]json.RawMessage) string {
	check := lcagentVerificationCheckFromEvent(event)
	label := firstNonEmpty(check.Command, "verification check")
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

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
