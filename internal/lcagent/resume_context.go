package lcagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/lcagent/session"
)

const (
	resumeContextMaxText  = 1200
	resumeContextMaxItems = 12
)

type resumeContext struct {
	SourceSessionID    string
	SourcePath         string
	ProjectPath        string
	RootSessionID      string
	ChainDepth         int
	HandoffSource      string
	StartedAt          time.Time
	LastActivityAt     time.Time
	UserMessages       []string
	AssistantMessages  []string
	FinalSummary       string
	FilesChanged       []string
	Verification       []string
	VerificationStatus string
	PatchSummaries     []string
	PermissionDenials  []string
	LastError          string
}

func loadResumeContext(dataDir, raw, workspaceRoot string) (*resumeContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	path, err := resolveResumeContextPath(dataDir, raw)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("LCAgent resume session not found: %s", raw)
	}
	ctx, err := parseResumeContextFile(path)
	if err != nil {
		return nil, err
	}
	ctx.SourcePath = path
	if ctx.SourceSessionID == "" && !looksLikePath(raw) {
		ctx.SourceSessionID = raw
	}
	if workspaceRoot != "" && ctx.ProjectPath != "" && !sameCleanPath(workspaceRoot, ctx.ProjectPath) {
		return nil, fmt.Errorf("LCAgent resume session %s belongs to %s, not %s", firstResumeNonEmpty(ctx.SourceSessionID, raw), ctx.ProjectPath, workspaceRoot)
	}
	return ctx, nil
}

func resolveResumeContextPath(dataDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if looksLikePath(raw) {
		info, err := os.Stat(raw)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("LCAgent resume path is a directory: %s", raw)
			}
			return raw, nil
		}
		if filepath.IsAbs(raw) || strings.HasSuffix(raw, ".jsonl") {
			return "", err
		}
	}
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	want := raw + ".jsonl"
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

func looksLikePath(raw string) bool {
	return filepath.IsAbs(raw) || strings.ContainsRune(raw, os.PathSeparator) || strings.HasSuffix(strings.TrimSpace(raw), ".jsonl")
}

func parseResumeContextFile(path string) (*resumeContext, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	ctx := &resumeContext{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		eventType := resumeJSONString(event["type"])
		if eventType == "" {
			continue
		}
		at := resumeJSONTime(event["timestamp"])
		if at.IsZero() {
			at = resumeJSONTime(event["started_at"])
		}
		if !at.IsZero() && at.After(ctx.LastActivityAt) {
			ctx.LastActivityAt = at
		}
		switch eventType {
		case "session_meta":
			ctx.SourceSessionID = resumeJSONString(event["id"])
			ctx.ProjectPath = resumeJSONString(event["cwd"])
			ctx.StartedAt = resumeJSONTime(event["started_at"])
			ctx.RootSessionID = firstResumeNonEmpty(resumeJSONString(event["root_session_id"]), resumeJSONString(event["parent_session_id"]), ctx.RootSessionID)
			if depth := resumeJSONInt(event["continuation_depth"]); depth > ctx.ChainDepth {
				ctx.ChainDepth = depth
			}
			if ctx.LastActivityAt.IsZero() {
				ctx.LastActivityAt = ctx.StartedAt
			}
		case "continuation":
			ctx.RootSessionID = firstResumeNonEmpty(resumeJSONString(event["root_session_id"]), resumeJSONString(event["parent_session_id"]), ctx.RootSessionID)
			if depth := resumeJSONInt(event["chain_depth"]); depth > ctx.ChainDepth {
				ctx.ChainDepth = depth
			}
		case "user_message":
			ctx.UserMessages = appendResumeText(ctx.UserMessages, resumeJSONString(event["message"]))
		case "assistant_message":
			ctx.AssistantMessages = appendResumeText(ctx.AssistantMessages, resumeJSONString(event["message"]))
			if ctx.HandoffSource == "" && resumeJSONString(event["message"]) != "" {
				ctx.HandoffSource = "assistant_message"
			}
		case "final_handoff_compacted":
			ctx.HandoffSource = "final_handoff"
		case "files_touched":
			ctx.FilesChanged = appendResumeList(ctx.FilesChanged, resumeJSONStringList(event["files"])...)
		case "patch_diff_summary":
			ctx.PatchSummaries = appendResumeText(ctx.PatchSummaries, resumeJSONString(event["summary"]))
			ctx.FilesChanged = appendResumeList(ctx.FilesChanged, resumeJSONStringList(event["files"])...)
		case "verification_summary":
			ctx.VerificationStatus = resumeJSONString(event["status"])
			ctx.Verification = appendResumeList(ctx.Verification, resumeJSONStringList(event["verification_checks"])...)
			ctx.FilesChanged = appendResumeList(ctx.FilesChanged, resumeJSONStringList(event["files_changed"])...)
		case "turn_complete":
			ctx.FinalSummary = firstResumeNonEmpty(resumeJSONString(event["summary"]), ctx.FinalSummary)
			ctx.VerificationStatus = firstResumeNonEmpty(resumeJSONString(event["verification_status"]), ctx.VerificationStatus)
			ctx.Verification = appendResumeList(ctx.Verification, resumeJSONStringList(event["verification"])...)
			ctx.FilesChanged = appendResumeList(ctx.FilesChanged, resumeJSONStringList(event["files_changed"])...)
			if ctx.HandoffSource != "final_handoff" {
				ctx.HandoffSource = "turn_complete"
			}
		case "permission_denied":
			ctx.PermissionDenials = appendResumeText(ctx.PermissionDenials, resumeJSONString(event["reason"]))
		case "turn_aborted":
			ctx.LastError = firstResumeNonEmpty(resumeJSONString(event["reason"]), ctx.LastError)
			ctx.HandoffSource = "turn_aborted"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (c *resumeContext) event(currentSessionID, reason string) session.Event {
	if c == nil {
		return nil
	}
	return session.Event{
		"type":                 "resume_context",
		"session_id":           currentSessionID,
		"source_session_id":    c.SourceSessionID,
		"parent_session_id":    c.SourceSessionID,
		"root_session_id":      c.rootSessionID(),
		"chain_depth":          c.nextChainDepth(),
		"continuation_reason":  firstResumeNonEmpty(reason, "resume_context"),
		"handoff_source":       c.HandoffSource,
		"source_path":          c.SourcePath,
		"source_cwd":           c.ProjectPath,
		"summary":              c.summaryText(),
		"files_changed":        c.FilesChanged,
		"verification":         c.Verification,
		"verification_status":  c.VerificationStatus,
		"pending_files":        c.pendingFiles(),
		"pending_verification": c.pendingVerification(),
		"pending_status":       c.pendingVerificationStatus(),
		"patch_summaries":      c.PatchSummaries,
		"permission_denials":   c.PermissionDenials,
		"last_error":           c.LastError,
		"source_last_activity": resumeTimeString(c.LastActivityAt),
		"source_started_at":    resumeTimeString(c.StartedAt),
	}
}

func (c *resumeContext) continuationEvent(currentSessionID, reason string) session.Event {
	if c == nil {
		return nil
	}
	return session.Event{
		"type":                   "continuation",
		"session_id":             currentSessionID,
		"parent_session_id":      c.SourceSessionID,
		"root_session_id":        c.rootSessionID(),
		"chain_depth":            c.nextChainDepth(),
		"continuation_reason":    firstResumeNonEmpty(reason, "resume_context"),
		"handoff_source":         c.HandoffSource,
		"parent_path":            c.SourcePath,
		"parent_cwd":             c.ProjectPath,
		"parent_summary":         c.summaryText(),
		"pending_files":          c.pendingFiles(),
		"pending_verification":   c.pendingVerification(),
		"pending_status":         c.pendingVerificationStatus(),
		"parent_last_activity":   resumeTimeString(c.LastActivityAt),
		"parent_session_started": resumeTimeString(c.StartedAt),
	}
}

func (c *resumeContext) rootSessionID() string {
	if c == nil {
		return ""
	}
	return firstResumeNonEmpty(c.RootSessionID, c.SourceSessionID)
}

func (c *resumeContext) nextChainDepth() int {
	if c == nil {
		return 0
	}
	return c.ChainDepth + 1
}

func (c *resumeContext) pendingFiles() []string {
	if c == nil || c.VerificationStatus == "" || strings.EqualFold(c.VerificationStatus, "verified") {
		return nil
	}
	return compactResumeList(c.FilesChanged)
}

func (c *resumeContext) pendingVerification() []string {
	if c == nil || c.VerificationStatus == "" || strings.EqualFold(c.VerificationStatus, "verified") {
		return nil
	}
	return compactResumeList(c.Verification)
}

func (c *resumeContext) pendingVerificationStatus() string {
	if c == nil || c.VerificationStatus == "" || strings.EqualFold(c.VerificationStatus, "verified") {
		return ""
	}
	return c.VerificationStatus
}

func (c *resumeContext) systemPromptSection() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Previous LCAgent session context:\n")
	if c.SourceSessionID != "" {
		fmt.Fprintf(&b, "- Source session: %s\n", c.SourceSessionID)
	}
	if c.ProjectPath != "" {
		fmt.Fprintf(&b, "- Source workspace: %s\n", c.ProjectPath)
	}
	if root := c.rootSessionID(); root != "" && root != c.SourceSessionID {
		fmt.Fprintf(&b, "- Continuation root: %s\n", root)
	}
	if c.ChainDepth > 0 {
		fmt.Fprintf(&b, "- Previous chain depth: %d\n", c.ChainDepth)
	}
	if c.HandoffSource != "" {
		fmt.Fprintf(&b, "- Handoff source: %s\n", c.HandoffSource)
	}
	appendResumePromptList(&b, "Previous user request(s)", lastResumeItems(c.UserMessages, 3))
	if c.FinalSummary != "" {
		fmt.Fprintf(&b, "- Last final summary: %s\n", trimResumeText(c.FinalSummary))
	} else {
		appendResumePromptList(&b, "Assistant message(s)", lastResumeItems(c.AssistantMessages, 3))
	}
	appendResumePromptList(&b, "Files changed or touched", c.FilesChanged)
	appendResumePromptList(&b, "Patch diff summaries", c.PatchSummaries)
	appendResumePromptList(&b, "Verification", c.Verification)
	if c.VerificationStatus != "" {
		fmt.Fprintf(&b, "- Previous verification status: %s\n", c.VerificationStatus)
	}
	appendResumePromptList(&b, "Pending files to verify", c.pendingFiles())
	appendResumePromptList(&b, "Pending verification evidence", c.pendingVerification())
	appendResumePromptList(&b, "Permission denials", c.PermissionDenials)
	if c.LastError != "" {
		fmt.Fprintf(&b, "- Previous run error: %s\n", trimResumeText(c.LastError))
	}
	b.WriteString("- Treat this as background for continuity. The current user request below controls the turn, and exact file contents should be re-read before editing or asserting details.\n")
	return strings.TrimSpace(b.String())
}

func (c *resumeContext) summaryText() string {
	if c == nil {
		return ""
	}
	var parts []string
	if c.SourceSessionID != "" {
		parts = append(parts, "source "+c.SourceSessionID)
	}
	if c.ChainDepth > 0 {
		parts = append(parts, fmt.Sprintf("chain depth %d", c.ChainDepth))
	}
	if c.FinalSummary != "" {
		parts = append(parts, "summary: "+trimResumeText(c.FinalSummary))
	} else if len(c.AssistantMessages) > 0 {
		parts = append(parts, "assistant: "+trimResumeText(c.AssistantMessages[len(c.AssistantMessages)-1]))
	}
	if len(c.FilesChanged) > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) changed/touched", len(c.FilesChanged)))
	}
	if c.VerificationStatus != "" {
		parts = append(parts, "verification "+c.VerificationStatus)
	}
	if c.LastError != "" {
		parts = append(parts, "error: "+trimResumeText(c.LastError))
	}
	return strings.Join(parts, "; ")
}

func appendResumePromptList(b *strings.Builder, label string, values []string) {
	values = compactResumeList(values)
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s:\n", label)
	for _, value := range values {
		fmt.Fprintf(b, "  - %s\n", trimResumeText(value))
	}
}

func appendResumeText(values []string, value string) []string {
	value = trimResumeText(value)
	if value == "" {
		return values
	}
	return appendResumeList(values, value)
}

func appendResumeList(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]string, 0, len(values)+len(additions))
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
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= resumeContextMaxItems {
			break
		}
	}
	return out
}

func compactResumeList(values []string) []string {
	if len(values) <= resumeContextMaxItems {
		return values
	}
	return values[:resumeContextMaxItems]
}

func lastResumeItems(values []string, maxItems int) []string {
	if maxItems <= 0 || len(values) <= maxItems {
		return values
	}
	return values[len(values)-maxItems:]
}

func trimResumeText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= resumeContextMaxText {
		return value
	}
	return strings.TrimSpace(value[:resumeContextMaxText]) + "..."
}

func resumeJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func resumeJSONStringList(raw json.RawMessage) []string {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func resumeJSONTime(raw json.RawMessage) time.Time {
	value := resumeJSONString(raw)
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

func resumeJSONInt(raw json.RawMessage) int {
	var value int
	_ = json.Unmarshal(raw, &value)
	return value
}

func resumeTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func sameCleanPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	return canonicalResumePath(left) == canonicalResumePath(right)
}

func canonicalResumePath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

func firstResumeNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
