package codexapp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func normalizeSubmission(input Submission) Submission {
	input.Text = strings.TrimSpace(input.Text)
	attachments := make([]Attachment, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		attachments = append(attachments, attachment)
	}
	input.Attachments = attachments
	return input
}

func launchRequestInitialInput(req LaunchRequest) Submission {
	input := normalizeSubmission(req.InitialInput)
	if input.Empty() {
		input = Submission{Text: strings.TrimSpace(req.Prompt)}
	}
	return normalizeSubmission(input)
}

func encodeSubmissionInput(input Submission) []userInput {
	input = normalizeSubmission(input)
	items := make([]userInput, 0, 1+len(input.Attachments))
	if input.Text != "" {
		items = append(items, userInput{
			Type: "text",
			Text: input.Text,
		})
	}
	for _, attachment := range input.Attachments {
		switch attachment.Kind {
		case AttachmentLocalImage:
			items = append(items, userInput{
				Type: "localImage",
				Path: attachment.Path,
			})
		}
	}
	return items
}

func formatTranscriptEntry(kind TranscriptKind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch kind {
	case TranscriptUser:
		return "You: " + text
	case TranscriptAgent:
		return "Codex: " + text
	case TranscriptStatus:
		return "[status] " + text
	case TranscriptSystem:
		return "[system] " + text
	case TranscriptError:
		return "[error] " + text
	case TranscriptPlan:
		return formatPlanTranscriptEntry(text)
	case TranscriptReasoning:
		return "Reasoning: " + text
	default:
		return text
	}
}

func formatPlanTranscriptEntry(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "Plan:\n" + text
}

func renderResumedThreadItem(item map[string]json.RawMessage) (string, TranscriptKind, string) {
	return renderResumedThreadItemForTurn("", item)
}

func renderResumedThreadItemForTurn(turnStatus string, item map[string]json.RawMessage) (string, TranscriptKind, string) {
	itemID := decodeRawString(item["id"])
	switch decodeRawString(item["type"]) {
	case "userMessage":
		text := renderResumedUserMessage(item["content"])
		if text == "" {
			return itemID, TranscriptUser, ""
		}
		return itemID, TranscriptUser, text
	case "agentMessage":
		text := strings.TrimSpace(decodeRawString(item["text"]))
		if text == "" {
			return itemID, TranscriptAgent, ""
		}
		return itemID, TranscriptAgent, text
	case "plan":
		text := strings.TrimSpace(decodeRawString(item["text"]))
		if text == "" {
			return itemID, TranscriptPlan, ""
		}
		return itemID, TranscriptPlan, text
	case "reasoning":
		content := decodeRawStringSlice(item["summary"])
		if len(content) == 0 {
			content = decodeRawStringSlice(item["content"])
		}
		if len(content) == 0 {
			return itemID, TranscriptReasoning, ""
		}
		return itemID, TranscriptReasoning, strings.Join(content, "\n")
	case "commandExecution":
		return itemID, TranscriptCommand, renderResumedCommandExecution(item)
	case "fileChange":
		return itemID, TranscriptFileChange, renderResumedFileChange(item)
	case "mcpToolCall":
		tool := strings.TrimSpace(decodeRawString(item["tool"]))
		server := strings.TrimSpace(decodeRawString(item["server"]))
		status := strings.TrimSpace(decodeRawString(item["status"]))
		if tool == "" && server == "" {
			return itemID, TranscriptTool, ""
		}
		label := "MCP tool"
		if server != "" {
			label += " " + server
		}
		if tool != "" {
			label += "/" + tool
		}
		if status != "" {
			label += " [" + status + "]"
		}
		return itemID, TranscriptTool, label
	case "dynamicToolCall":
		tool := strings.TrimSpace(decodeRawString(item["tool"]))
		status := strings.TrimSpace(decodeRawString(item["status"]))
		if tool == "" {
			return itemID, TranscriptTool, ""
		}
		label := "Tool " + tool
		if status != "" {
			label += " [" + status + "]"
		}
		return itemID, TranscriptTool, label
	case "webSearch":
		query := strings.TrimSpace(decodeRawString(item["query"]))
		if query == "" {
			return itemID, TranscriptTool, ""
		}
		return itemID, TranscriptTool, "Web search: " + query
	case "imageView":
		path := strings.TrimSpace(decodeRawString(item["path"]))
		if path == "" {
			return itemID, TranscriptTool, ""
		}
		return itemID, TranscriptTool, "Viewed image: " + path
	case "imageGeneration":
		if itemID == "" {
			itemID = generatedImageItemID(item)
		}
		status := strings.TrimSpace(decodeRawString(item["status"]))
		text := "Image generation"
		if status != "" {
			text += " [" + status + "]"
		}
		return itemID, TranscriptTool, text
	case "enteredReviewMode":
		review := strings.TrimSpace(decodeRawString(item["review"]))
		if review == "" {
			review = "Entered review mode"
		}
		return itemID, TranscriptSystem, review
	case "exitedReviewMode":
		review := strings.TrimSpace(decodeRawString(item["review"]))
		if review == "" {
			review = "Exited review mode"
		}
		return itemID, TranscriptSystem, review
	case "contextCompaction":
		if compactionStillRunning(item, turnStatus) {
			return itemID, TranscriptSystem, "Compacting conversation history..."
		}
		return itemID, TranscriptSystem, "Conversation history compacted"
	default:
		return itemID, TranscriptOther, ""
	}
}

func compactionStillRunning(item map[string]json.RawMessage, turnStatus string) bool {
	status := strings.ToLower(strings.TrimSpace(decodeRawString(item["status"])))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(turnStatus))
	}
	switch status {
	case "active", "inprogress", "in_progress", "pending", "running", "started":
		return true
	default:
		return false
	}
}

// normalizeProgressLine produces a skeleton of a progress message so that
// two lines differing only in spinner frame, percentage, counter, or other
// numeric value compare as equal.  This lets the progress handler replace
// transient status updates in-place instead of appending every tick.
//
// Examples:
//
//	"⠴ Running xcodebuild"  →  "Running xcodebuild"
//	"⠦ Running xcodebuild"  →  "Running xcodebuild"
//	"Compiling: 50%"         →  "Compiling: #%"
//	"Compiling: 75%"         →  "Compiling: #%"
//	"Processing 3/10 files"  →  "Processing #/# files"
func normalizeProgressLine(s string) string {
	// Strip leading braille spinner characters (U+2800–U+28FF).
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r >= 0x2800 && r <= 0x28FF {
			s = s[size:]
			continue
		}
		break
	}
	s = strings.TrimSpace(s)
	// Collapse digit runs into a single '#' placeholder.
	var b strings.Builder
	inDigits := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			if !inDigits {
				b.WriteByte('#')
				inDigits = true
			}
			continue
		}
		inDigits = false
		b.WriteRune(r)
	}
	return b.String()
}

func compactionTranscriptText(text string) bool {
	text = strings.TrimSpace(text)
	return text == "Compacting conversation history..." || text == "Conversation history compacted"
}

func renderResumedUserMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &content); err != nil {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch decodeRawString(item["type"]) {
		case "text", "input_text", "output_text":
			text := strings.TrimSpace(decodeRawString(item["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		case "localImage", "local_image":
			path := strings.TrimSpace(decodeRawString(item["path"]))
			if path != "" {
				label := Attachment{Kind: AttachmentLocalImage, Path: path}.DisplayLabel()
				parts = append(parts, "[attached image] "+label)
			} else {
				parts = append(parts, "[attached image]")
			}
		case "image", "input_image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func renderResumedCommandExecution(item map[string]json.RawMessage) string {
	command := strings.TrimSpace(decodeRawString(item["command"]))
	cwd := strings.TrimSpace(decodeRawString(item["cwd"]))
	status := strings.TrimSpace(decodeRawString(item["status"]))
	output := decodeRawNullableString(item["aggregatedOutput"])

	lines := make([]string, 0, 4)
	if command != "" {
		lines = append(lines, "$ "+command)
	}
	if cwd != "" {
		lines = append(lines, "# cwd: "+cwd)
	}
	if strings.TrimSpace(output) != "" {
		lines = append(lines, output)
	}
	if summary := renderCommandStatusLine(status, decodeRawNullableInt(item["exitCode"])); summary != "" {
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n")
}

func renderResumedFileChange(item map[string]json.RawMessage) string {
	status := strings.TrimSpace(decodeRawString(item["status"]))
	changeCount := decodeRawArrayLen(item["changes"])
	text := "Applying file changes"
	if changeCount > 0 {
		text = fmt.Sprintf("Applying %d file change(s)", changeCount)
	}
	if summary := renderFileChangeStatusLine(status); summary != "" {
		text += "\n" + summary
	}
	return text
}

func renderCommandStatusLine(status string, exitCode *int) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	summary := "[command " + status
	if exitCode != nil {
		summary += fmt.Sprintf(", exit %d", *exitCode)
	}
	summary += "]"
	return summary
}

func renderFileChangeStatusLine(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	return "[file changes " + status + "]"
}

func decodeRawNullableString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return decodeRawString(raw)
}

func decodeRawNullableInt(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	value := decodeRawInt(raw)
	if value < 0 {
		return nil
	}
	return &value
}

func decodeRawStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
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

func extractPlaywrightPageURL(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var result struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ""
	}
	for _, item := range result.Content {
		text := strings.TrimSpace(decodeRawString(item["text"]))
		if text == "" {
			continue
		}
		if pageURL := extractPageURLFromText(text); pageURL != "" {
			return pageURL
		}
	}
	return ""
}

func extractPageURLFromText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if !strings.HasPrefix(line, "Page URL:") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "Page URL:"))
	}
	return ""
}

func decodeRawArrayLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return 0
	}
	return len(values)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatApprovalPolicy(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var simple string
	if err := json.Unmarshal(raw, &simple); err == nil {
		return strings.TrimSpace(simple)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(compact)
}

type sandboxPolicySummary struct {
	Mode          string
	NetworkAccess string
	WritableRoots []string
}

func summarizeSandboxPolicy(raw json.RawMessage) sandboxPolicySummary {
	if len(raw) == 0 || string(raw) == "null" {
		return sandboxPolicySummary{}
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return sandboxPolicySummary{}
	}
	summary := sandboxPolicySummary{}
	switch strings.TrimSpace(decodeRawString(value["type"])) {
	case "dangerFullAccess":
		summary.Mode = "danger-full-access"
		summary.NetworkAccess = "full"
	case "readOnly":
		summary.Mode = "read-only"
		if decodeRawBool(value["networkAccess"]) {
			summary.NetworkAccess = "enabled"
		} else {
			summary.NetworkAccess = "disabled"
		}
	case "workspaceWrite":
		summary.Mode = "workspace-write"
		if decodeRawBool(value["networkAccess"]) {
			summary.NetworkAccess = "enabled"
		} else {
			summary.NetworkAccess = "disabled"
		}
		summary.WritableRoots = decodeRawStringSlice(value["writableRoots"])
	case "externalSandbox":
		summary.Mode = "external-sandbox"
		network := strings.TrimSpace(decodeRawString(value["networkAccess"]))
		if network == "" {
			network = "restricted"
		}
		summary.NetworkAccess = network
	}
	return summary
}

func formatThreadTokenUsage(usage *threadTokenUsage) string {
	if usage == nil {
		return ""
	}
	parts := []string{}
	if usage.Total.TotalTokens > 0 {
		total := fmt.Sprintf("total %d", usage.Total.TotalTokens)
		if usage.ModelContextWindow != nil && *usage.ModelContextWindow > 0 {
			percent := int(float64(usage.Total.TotalTokens) * 100 / float64(*usage.ModelContextWindow))
			total += fmt.Sprintf(" of %d tokens (%d%%)", *usage.ModelContextWindow, percent)
		} else {
			total += " tokens"
		}
		parts = append(parts, total)
	}
	if usage.Last.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("last turn %d tokens", usage.Last.TotalTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func appendThreadTokenUsageStatusLines(lines *[]string, tokenUsage *threadTokenUsage) {
	if lines == nil || tokenUsage == nil {
		return
	}
	tokenUsageSnapshot := exportedTokenUsageSnapshot(tokenUsage)
	if tokenUsageSnapshot != nil && tokenUsageSnapshot.ModelContextWindow > 0 {
		*lines = append(*lines, fmt.Sprintf("model context window: %d", tokenUsageSnapshot.ModelContextWindow))
		if contextTokens := tokenUsageSnapshot.EstimatedContextTokens(); contextTokens > 0 {
			*lines = append(*lines, fmt.Sprintf("context tokens: %d", contextTokens))
			*lines = append(*lines, fmt.Sprintf("context used percent: %d", 100-tokenUsageSnapshot.ContextLeftPercent()))
		}
	}
	if tokenUsage.Total.InputTokens > 0 {
		*lines = append(*lines, fmt.Sprintf("input tokens: %d", tokenUsage.Total.InputTokens))
	}
	if tokenUsage.Total.CachedInputTokens > 0 {
		*lines = append(*lines, fmt.Sprintf("cached input tokens: %d", tokenUsage.Total.CachedInputTokens))
	}
	if tokenUsage.Total.ReasoningOutputTokens > 0 {
		*lines = append(*lines, fmt.Sprintf("reasoning tokens: %d", tokenUsage.Total.ReasoningOutputTokens))
	}
	if tokenUsage.Total.OutputTokens > 0 {
		*lines = append(*lines, fmt.Sprintf("output tokens: %d", tokenUsage.Total.OutputTokens))
	}
}

func buildEmbeddedGoalStatusText(goal *ThreadGoal) string {
	if goal == nil {
		return "Embedded Codex goal\nstatus: none"
	}
	lines := []string{"Embedded Codex goal"}
	appendEmbeddedGoalLines(&lines, goal)
	return strings.Join(lines, "\n")
}

func buildEmbeddedGoalSetText(goal *ThreadGoal, fallbackObjective string) string {
	if goal == nil {
		goal = &ThreadGoal{
			Objective: strings.TrimSpace(fallbackObjective),
			Status:    ThreadGoalStatusActive,
		}
	}
	lines := []string{"Embedded Codex goal set"}
	appendEmbeddedGoalLines(&lines, goal)
	return strings.Join(lines, "\n")
}

func appendEmbeddedGoalLines(lines *[]string, goal *ThreadGoal) {
	if goal == nil {
		return
	}
	if goal.Objective != "" {
		*lines = append(*lines, "objective: "+goal.Objective)
	}
	if goal.Status != "" {
		*lines = append(*lines, "status: "+string(goal.Status))
	}
	if goal.TokenBudget != nil {
		*lines = append(*lines, fmt.Sprintf("token budget: %d", *goal.TokenBudget))
	}
	if goal.TokensUsed > 0 {
		*lines = append(*lines, fmt.Sprintf("tokens used: %d", goal.TokensUsed))
	}
	if goal.TimeUsedSeconds > 0 {
		*lines = append(*lines, "time used: "+formatTurnStatusDuration(time.Duration(goal.TimeUsedSeconds)*time.Second))
	}
}

func buildEmbeddedStatusText(threadID, projectPath, currentCWD, model, modelProvider, reasoningEffort, serviceTier string, approvalPolicy, sandboxPolicy json.RawMessage, tokenUsage *threadTokenUsage, rateLimits *rateLimitSnapshot, rateLimitsByID map[string]rateLimitSnapshot) string {
	lines := []string{"Embedded Codex status"}
	if threadID != "" {
		lines = append(lines, "thread: "+threadID)
	}
	if projectPath != "" {
		lines = append(lines, "project: "+projectPath)
	}
	if currentCWD != "" {
		lines = append(lines, "cwd: "+currentCWD)
	}
	if model != "" {
		lines = append(lines, "model: "+model)
	}
	if modelProvider != "" {
		lines = append(lines, "model provider: "+modelProvider)
	}
	if reasoningEffort != "" {
		lines = append(lines, "reasoning effort: "+reasoningEffort)
	}
	if serviceTier != "" {
		lines = append(lines, "service tier: "+serviceTier)
	}
	if approval := formatApprovalPolicy(approvalPolicy); approval != "" {
		lines = append(lines, "approval: "+approval)
	}
	sandboxSummary := summarizeSandboxPolicy(sandboxPolicy)
	if sandboxSummary.Mode != "" {
		lines = append(lines, "sandbox: "+sandboxSummary.Mode)
	}
	if sandboxSummary.NetworkAccess != "" {
		lines = append(lines, "network: "+sandboxSummary.NetworkAccess)
	}
	if len(sandboxSummary.WritableRoots) > 0 {
		lines = append(lines, "writable roots: "+strings.Join(sandboxSummary.WritableRoots, ", "))
	}
	if tokenUsage != nil {
		appendThreadTokenUsageStatusLines(&lines, tokenUsage)
		if tokenUsage.Last.TotalTokens > 0 {
			lines = append(lines, fmt.Sprintf("last turn tokens: %d", tokenUsage.Last.TotalTokens))
		}
	}
	for _, window := range collectEmbeddedStatusUsageWindows(rateLimits, rateLimitsByID) {
		parts := []string{
			"limit=" + window.Limit,
			"window=" + window.Window,
			fmt.Sprintf("left=%d", window.LeftPercent),
		}
		if window.Plan != "" {
			parts = append(parts, "plan="+window.Plan)
		}
		if window.ResetsAt > 0 {
			parts = append(parts, fmt.Sprintf("resetsAt=%d", window.ResetsAt))
		}
		lines = append(lines, "usage window: "+strings.Join(parts, "; "))
	}
	return strings.Join(lines, "\n")
}

type embeddedStatusUsageWindow struct {
	Limit       string
	Plan        string
	Window      string
	LeftPercent int
	ResetsAt    int64
}

func collectEmbeddedStatusUsageWindows(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) []embeddedStatusUsageWindow {
	windows := make([]embeddedStatusUsageWindow, 0, 4)
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToLower(keys[i])
		right := strings.ToLower(keys[j])
		if left == "codex" {
			return right != "codex"
		}
		if right == "codex" {
			return false
		}
		return left < right
	})
	for _, key := range keys {
		snapshot := byID[key]
		appendEmbeddedStatusUsageWindows(&windows, seen, &snapshot, key)
	}
	appendEmbeddedStatusUsageWindows(&windows, seen, primary, "")
	return windows
}

func appendEmbeddedStatusUsageWindows(windows *[]embeddedStatusUsageWindow, seen map[string]struct{}, snapshot *rateLimitSnapshot, fallbackLabel string) {
	if snapshot == nil {
		return
	}
	limitLabel := firstNonEmpty(
		strings.TrimSpace(stringValue(snapshot.LimitName)),
		strings.TrimSpace(stringValue(snapshot.LimitID)),
		strings.TrimSpace(fallbackLabel),
	)
	if limitLabel == "" {
		limitLabel = "usage"
	}
	plan := strings.TrimSpace(stringValue(snapshot.PlanType))
	appendEmbeddedStatusUsageWindow(windows, seen, limitLabel, plan, "primary", snapshot.Primary)
	appendEmbeddedStatusUsageWindow(windows, seen, limitLabel, plan, "secondary", snapshot.Secondary)
}

func appendEmbeddedStatusUsageWindow(windows *[]embeddedStatusUsageWindow, seen map[string]struct{}, limitLabel, plan, fallbackWindow string, window *rateLimitWindow) {
	if window == nil {
		return
	}
	windowLabel := rateLimitWindowDisplayLabel(fallbackWindow, window.WindowDurationMins)
	key := strings.ToLower(limitLabel) + "|" + strings.ToLower(windowLabel)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	resetsAt := int64(0)
	if window.ResetsAt != nil {
		resetsAt = *window.ResetsAt
	}
	*windows = append(*windows, embeddedStatusUsageWindow{
		Limit:       limitLabel,
		Plan:        plan,
		Window:      windowLabel,
		LeftPercent: clampPercent(100 - window.UsedPercent),
		ResetsAt:    resetsAt,
	})
}

func rateLimitWindowDisplayLabel(fallback string, durationMins *int64) string {
	if durationMins == nil || *durationMins <= 0 {
		return fallback
	}
	switch mins := *durationMins; {
	case mins == 7*24*60:
		return "weekly"
	case mins%(24*60) == 0:
		return fmt.Sprintf("%dd", mins/(24*60))
	case mins%60 == 0:
		return fmt.Sprintf("%dh", mins/60)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func clampPercent(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

func formatRateLimitStatus(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) string {
	snapshot := primary
	if snapshot == nil && len(byID) > 0 {
		if preferred, ok := byID["codex"]; ok {
			snapshot = cloneRateLimitSnapshot(&preferred)
		} else {
			keys := make([]string, 0, len(byID))
			for key := range byID {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			first := byID[keys[0]]
			snapshot = cloneRateLimitSnapshot(&first)
		}
	}
	if snapshot == nil {
		return ""
	}
	parts := []string{}
	if label := firstNonEmpty(strings.TrimSpace(stringValue(snapshot.LimitName)), strings.TrimSpace(stringValue(snapshot.LimitID))); label != "" {
		parts = append(parts, label)
	}
	if plan := strings.TrimSpace(stringValue(snapshot.PlanType)); plan != "" {
		parts = append(parts, "plan "+plan)
	}
	if window := formatRateLimitWindow("primary", snapshot.Primary); window != "" {
		parts = append(parts, window)
	}
	if window := formatRateLimitWindow("secondary", snapshot.Secondary); window != "" {
		parts = append(parts, window)
	}
	if snapshot.Credits != nil {
		switch {
		case snapshot.Credits.Unlimited:
			parts = append(parts, "credits unlimited")
		case snapshot.Credits.HasCredits && strings.TrimSpace(stringValue(snapshot.Credits.Balance)) != "":
			parts = append(parts, "credits "+strings.TrimSpace(stringValue(snapshot.Credits.Balance)))
		}
	}
	return strings.Join(parts, "; ")
}

func formatRateLimitWindow(label string, window *rateLimitWindow) string {
	if window == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("%s %d%%", label, window.UsedPercent)}
	if window.WindowDurationMins != nil && *window.WindowDurationMins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", *window.WindowDurationMins))
	}
	if window.ResetsAt != nil && *window.ResetsAt > 0 {
		parts = append(parts, "resets "+time.Unix(*window.ResetsAt, 0).Local().Format("15:04"))
	}
	return strings.Join(parts, " ")
}

func decodeRawBool(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}
