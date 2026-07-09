package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"lcroom/internal/browserctl"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func (s *appServerSession) applyThreadConfigLocked(approvalPolicy json.RawMessage, cwd, model, modelProvider, reasoningEffort, serviceTier string, sandboxPolicy json.RawMessage) {
	s.currentCWD = firstNonEmpty(strings.TrimSpace(cwd), s.projectPath)
	s.model = strings.TrimSpace(model)
	s.modelProvider = strings.TrimSpace(modelProvider)
	s.reasoningEffort = strings.TrimSpace(reasoningEffort)
	if strings.EqualFold(strings.TrimSpace(s.pendingModel), s.model) {
		s.pendingModel = ""
	}
	if strings.EqualFold(strings.TrimSpace(s.pendingReasoning), s.reasoningEffort) {
		s.pendingReasoning = ""
	}
	s.serviceTier = strings.TrimSpace(serviceTier)
	s.approvalPolicy = append(json.RawMessage(nil), approvalPolicy...)
	s.sandboxPolicy = append(json.RawMessage(nil), sandboxPolicy...)
}

func (s *appServerSession) readRateLimits() (*rateLimitSnapshot, map[string]rateLimitSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	result, err := s.call(ctx, "account/rateLimits/read", nil)
	if err != nil {
		return nil, nil, err
	}
	var response accountRateLimitsResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, nil, err
	}

	var limits *rateLimitSnapshot
	if hasRateLimitSnapshot(response.RateLimits) {
		limits = cloneRateLimitSnapshot(&response.RateLimits)
	}
	return limits, cloneRateLimitSnapshotMap(response.RateLimitsByID), nil
}

func cloneThreadTokenUsage(in *threadTokenUsage) *threadTokenUsage {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func exportedTokenUsageSnapshot(in *threadTokenUsage) *TokenUsageSnapshot {
	if in == nil {
		return nil
	}
	out := &TokenUsageSnapshot{
		Last:  exportedTokenUsageBreakdown(in.Last),
		Total: exportedTokenUsageBreakdown(in.Total),
	}
	if in.ModelContextWindow != nil && *in.ModelContextWindow > 0 {
		out.ModelContextWindow = *in.ModelContextWindow
	}
	return out
}

func exportedTokenUsageBreakdown(in tokenUsageBreakdown) TokenUsageBreakdown {
	return TokenUsageBreakdown{
		CachedInputTokens:     in.CachedInputTokens,
		InputTokens:           in.InputTokens,
		OutputTokens:          in.OutputTokens,
		ReasoningOutputTokens: in.ReasoningOutputTokens,
		TotalTokens:           in.TotalTokens,
	}
}

func exportedUsageWindowsSnapshot(primary *rateLimitSnapshot, byID map[string]rateLimitSnapshot) []UsageWindowSnapshot {
	embedded := collectEmbeddedStatusUsageWindows(primary, byID)
	if len(embedded) == 0 {
		return nil
	}
	out := make([]UsageWindowSnapshot, 0, len(embedded))
	for _, window := range embedded {
		snapshot := UsageWindowSnapshot{
			Limit:            window.Limit,
			Plan:             window.Plan,
			Window:           window.Window,
			LeftPercent:      window.LeftPercent,
			CreditBalance:    window.CreditBalance,
			HasCredits:       window.HasCredits,
			CreditsUnlimited: window.CreditsUnlimited,
		}
		if window.ResetsAt > 0 {
			snapshot.ResetsAt = time.Unix(window.ResetsAt, 0).Local()
		}
		out = append(out, snapshot)
	}
	return out
}

func exportedThreadGoal(in *threadGoal) *ThreadGoal {
	if in == nil {
		return nil
	}
	return &ThreadGoal{
		ThreadID:        strings.TrimSpace(in.ThreadID),
		Objective:       strings.TrimSpace(in.Objective),
		Status:          in.Status,
		TokenBudget:     cloneInt64Ptr(in.TokenBudget),
		TokensUsed:      in.TokensUsed,
		TimeUsedSeconds: in.TimeUsedSeconds,
		CreatedAt:       unixSecondsTime(in.CreatedAt),
		UpdatedAt:       unixSecondsTime(in.UpdatedAt),
	}
}

func cloneThreadGoal(in *ThreadGoal) *ThreadGoal {
	if in == nil {
		return nil
	}
	out := *in
	out.TokenBudget = cloneInt64Ptr(in.TokenBudget)
	return &out
}

func cloneInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func threadGoalSetResponseStale(goal *ThreadGoal, objective string, tokenBudget *int64) bool {
	if goal == nil {
		return false
	}
	if strings.TrimSpace(goal.Objective) != strings.TrimSpace(objective) {
		return true
	}
	if !int64PtrEqual(goal.TokenBudget, tokenBudget) {
		return true
	}
	switch strings.TrimSpace(string(goal.Status)) {
	case string(ThreadGoalStatusPaused), string(ThreadGoalStatusComplete), string(ThreadGoalStatusBudgetLimited), string(ThreadGoalStatusBlocked), "budget_limited", "usage_limited":
		return true
	default:
		return false
	}
}

func threadGoalShouldReactivateOnManualPrompt(goal *ThreadGoal) bool {
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		return false
	}
	switch strings.TrimSpace(string(goal.Status)) {
	case string(ThreadGoalStatusPaused), string(ThreadGoalStatusBlocked):
		return true
	default:
		return false
	}
}

func threadGoalShouldPauseOnManualPrompt(goal *ThreadGoal) bool {
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		return false
	}
	switch strings.TrimSpace(string(goal.Status)) {
	case "", string(ThreadGoalStatusActive):
		return true
	default:
		return false
	}
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func threadGoalSummary(goal *ThreadGoal) string {
	if goal == nil {
		return "no goal"
	}
	parts := []string{}
	if goal.Objective != "" {
		parts = append(parts, fmt.Sprintf("objective %q", goal.Objective))
	}
	if goal.Status != "" {
		parts = append(parts, "status "+string(goal.Status))
	}
	if goal.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("token budget %d", *goal.TokenBudget))
	}
	if len(parts) == 0 {
		return "an empty goal"
	}
	return strings.Join(parts, ", ")
}

func unixSecondsTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

func cloneRateLimitSnapshot(in *rateLimitSnapshot) *rateLimitSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.Primary != nil {
		window := *in.Primary
		out.Primary = &window
	}
	if in.Secondary != nil {
		window := *in.Secondary
		out.Secondary = &window
	}
	if in.Credits != nil {
		credits := *in.Credits
		out.Credits = &credits
	}
	return &out
}

func cloneRateLimitSnapshotMap(in map[string]rateLimitSnapshot) map[string]rateLimitSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]rateLimitSnapshot, len(in))
	for key, value := range in {
		cloned := cloneRateLimitSnapshot(&value)
		if cloned != nil {
			out[key] = *cloned
		}
	}
	return out
}

func hasRateLimitSnapshot(snapshot rateLimitSnapshot) bool {
	return strings.TrimSpace(stringValue(snapshot.LimitID)) != "" ||
		strings.TrimSpace(stringValue(snapshot.LimitName)) != "" ||
		strings.TrimSpace(stringValue(snapshot.PlanType)) != "" ||
		snapshot.Primary != nil ||
		snapshot.Secondary != nil ||
		snapshot.Credits != nil
}

func (s *appServerSession) touchLocked() {
	s.lastActivityAt = time.Now()
}

func (s *appServerSession) touchBusyLocked() {
	now := time.Now()
	s.lastActivityAt = now
	s.lastBusyActivityAt = now
	s.stalled = false
	s.stallCount = 0
}

func (s *appServerSession) clearActiveStateLocked() {
	s.clearBusyLocked("")
	s.compacting = false
	s.contextCompactionActive = false
	s.pendingApproval = nil
	s.pendingToolInput = nil
	s.pendingElicitation = nil
	s.browserToolCalls = nil
	s.browserActivity = browserctl.DefaultSessionActivity(s.playwrightPolicy)
}

func (s *appServerSession) browserToolCallForItem(item map[string]json.RawMessage) (browserToolCall, bool) {
	serverName := strings.TrimSpace(decodeRawString(item["server"]))
	toolName := strings.TrimSpace(decodeRawString(item["tool"]))
	if !browserctl.IsPlaywrightToolCall(serverName, toolName) {
		return browserToolCall{}, false
	}
	return browserToolCall{
		ServerName: serverName,
		ToolName:   toolName,
	}, true
}

func (s *appServerSession) updateBrowserPageURLLocked(call browserToolCall, item map[string]json.RawMessage) {
	if pageURL := extractPlaywrightPageURL(item["result"]); pageURL != "" {
		s.currentBrowserPageURL = pageURL
		s.currentBrowserPageStale = false
		return
	}
	if strings.EqualFold(strings.TrimSpace(call.ToolName), "browser_close") {
		s.currentBrowserPageURL = ""
		s.currentBrowserPageStale = false
	}
}

func (s *appServerSession) refreshBrowserActivityLocked(now time.Time) {
	activity := browserctl.DefaultSessionActivity(s.playwrightPolicy)
	previous := s.browserActivity.Normalize()
	activity.LastEventAt = previous.LastEventAt

	var current browserToolCall
	for _, call := range s.browserToolCalls {
		current = call
		break
	}
	if current.ServerName != "" || current.ToolName != "" {
		activity.State = browserctl.SessionActivityStateActive
		activity.ServerName = current.ServerName
		activity.ToolName = current.ToolName
		activity.LastEventAt = now
	}

	if request := s.pendingElicitation; request != nil {
		if browserctl.IsPlaywrightToolCall(request.ServerName, "") || current.ServerName != "" || current.ToolName != "" {
			activity.State = browserctl.SessionActivityStateWaitingForUser
			if strings.TrimSpace(request.ServerName) != "" {
				activity.ServerName = strings.TrimSpace(request.ServerName)
			}
			activity.LastEventAt = now
		}
	}

	s.browserActivity = activity.Normalize()
}

func (s *appServerSession) syncThreadStatusLocked(threadID string, status resumedThreadStatus, recovered bool) {
	threadID = strings.TrimSpace(threadID)
	currentThreadID := strings.TrimSpace(s.threadID)
	if currentThreadID != "" && threadID != "" && currentThreadID != threadID {
		return
	}

	switch strings.TrimSpace(status.Type) {
	case "idle":
		compacting := s.compacting
		hadPendingCompletion := s.pendingCompletion != nil
		hadActiveTurn := s.busy || strings.TrimSpace(s.activeTurnID) != "" || len(s.activeItems) > 0
		hadInteractiveState := s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil

		statusText := ""
		if compacting {
			statusText = "Conversation history compacted"
		} else if hadPendingCompletion {
			statusText = strings.TrimSpace(s.pendingCompletion.Status)
		} else if hadActiveTurn && recovered {
			statusText = "Recovered idle after status check"
		} else if hadActiveTurn {
			statusText = "Turn finished"
		} else if hadInteractiveState {
			statusText = "Codex session ready"
		}

		s.clearActiveStateLocked()
		if statusText != "" {
			s.status = statusText
			s.lastSystemNotice = statusText
		}
	case "active":
		s.reconciling = false
	case "systemError":
		hadState := s.compacting || s.busy || s.pendingCompletion != nil || strings.TrimSpace(s.activeTurnID) != "" || s.pendingApproval != nil || s.pendingToolInput != nil || s.pendingElicitation != nil
		s.clearActiveStateLocked()
		if hadState {
			s.status = "Codex thread reported a system error"
			s.lastSystemNotice = "Codex thread reported a system error"
		}
	}
}

func (s *appServerSession) handleTransportFailure(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return
	}

	var cmd *exec.Cmd
	var stdin io.WriteCloser

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.closed = true
	s.clearActiveStateLocked()
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	s.status = "Codex transport failed; session closed"
	cmd = s.cmd
	stdin = s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	}
	s.failPending(message)
	s.notify()
}

func (s *appServerSession) appendSystemNotice(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptSystem, message)
	s.lastSystemNotice = message
	if label := compactCodexStatusLabel(message); label != "" {
		s.status = label
	} else {
		s.status = message
	}
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) appendSystemError(err error) {
	if err == nil {
		return
	}
	message := err.Error()
	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptError, message)
	s.lastError = message
	s.lastSystemNotice = message
	if label := compactCodexStatusLabel(message); label != "" {
		s.status = label
	} else {
		s.status = "Codex error"
	}
	s.mu.Unlock()
	s.notify()
	s.maybeAppendAuth403Diagnosis(message)
}

func normalizeCodexStatusMessage(message string) string {
	return strings.ToLower(strings.TrimSpace(message))
}

func extractCodexHTTPStatusCode(normalized string) (int, bool) {
	for _, marker := range []string{"http error: ", "unexpected status "} {
		idx := strings.Index(normalized, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(normalized[idx+len(marker):])
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		code, err := strconv.Atoi(strings.Trim(fields[0], " ,:"))
		if err == nil {
			return code, true
		}
	}
	return 0, false
}

func isCodexResponsesTransportContext(normalized string) bool {
	return strings.Contains(normalized, "/backend-api/codex/responses") ||
		strings.Contains(normalized, "failed to connect to websocket")
}

func diagnoseCodexAuth403(message string) string {
	normalized := normalizeCodexStatusMessage(message)
	code, ok := extractCodexHTTPStatusCode(normalized)
	if !ok || code != 403 || !isCodexResponsesTransportContext(normalized) {
		return ""
	}
	return "Codex rejected the request with HTTP 403. This usually means ChatGPT authentication, session access, or Codex entitlement is unavailable, or ChatGPT account access is temporarily degraded. It is usually not a Little Control Room transport bug. Check `codex login status`; if needed, run `codex logout` and `codex login`, then use `/reconnect` in the embedded pane or reopen the embedded session once ChatGPT account access is healthy again."
}

func codexRateLimited429StatusLabel() string {
	return "Codex rate limited (HTTP 429)"
}

func isCodexServiceUnavailable503(message string) bool {
	normalized := normalizeCodexStatusMessage(message)
	code, ok := extractCodexHTTPStatusCode(normalized)
	return ok && code == 503 && isCodexResponsesTransportContext(normalized)
}

func codexServiceUnavailableStatusLabel(code int) string {
	switch code {
	case 500:
		return "Codex server error (HTTP 500)"
	case 502, 503, 504:
		return fmt.Sprintf("Codex service unavailable (HTTP %d)", code)
	default:
		if code >= 500 && code <= 599 {
			return fmt.Sprintf("Codex server error (HTTP %d)", code)
		}
		return ""
	}
}

func isCodexTimeoutMessage(normalized string) bool {
	if !isCodexResponsesTransportContext(normalized) {
		return false
	}
	switch {
	case strings.Contains(normalized, "context deadline exceeded"),
		strings.Contains(normalized, "i/o timeout"),
		strings.Contains(normalized, "timeout awaiting response headers"):
		return true
	default:
		return false
	}
}

func isCodexConnectionFailureMessage(normalized string) bool {
	if !isCodexResponsesTransportContext(normalized) {
		return false
	}
	switch {
	case strings.Contains(normalized, "failed to connect to websocket"),
		strings.Contains(normalized, "connection refused"),
		strings.Contains(normalized, "connection reset by peer"),
		strings.Contains(normalized, "broken pipe"),
		strings.Contains(normalized, "dial tcp"):
		return true
	default:
		return false
	}
}

func codexAuth403StatusLabel() string {
	return "Codex auth/session rejected (HTTP 403)"
}

func codexServiceUnavailable503StatusLabel() string {
	return "Codex service unavailable (HTTP 503)"
}

func codexTimeoutStatusLabel() string {
	return "Codex request timed out"
}

func codexConnectionFailedStatusLabel() string {
	return "Codex connection failed"
}

func codexCodeModeHostStatusLabel() string {
	return "Codex helper unavailable"
}

func isCodexCodeModeHostFailure(message string) bool {
	return strings.Contains(normalizeCodexStatusMessage(message), "failed to spawn code-mode host")
}

func codexGenericStderrStatusLabel(message string) string {
	normalized := normalizeCodexStatusMessage(message)
	switch {
	case strings.HasPrefix(normalized, "codex stderr stream error:"):
		return "Codex stderr stream failed"
	case strings.HasPrefix(normalized, "codex stderr:"):
		return "Codex reported stderr"
	default:
		return ""
	}
}

func compactCodexStatusLabel(message string) string {
	normalized := normalizeCodexStatusMessage(message)
	switch {
	case diagnoseCodexAuth403(message) != "":
		return codexAuth403StatusLabel()
	case func() bool {
		code, ok := extractCodexHTTPStatusCode(normalized)
		return ok && code == 429 && isCodexResponsesTransportContext(normalized)
	}():
		return codexRateLimited429StatusLabel()
	case func() string {
		code, ok := extractCodexHTTPStatusCode(normalized)
		if !ok || !isCodexResponsesTransportContext(normalized) {
			return ""
		}
		return codexServiceUnavailableStatusLabel(code)
	}() != "":
		code, _ := extractCodexHTTPStatusCode(normalized)
		return codexServiceUnavailableStatusLabel(code)
	case isCodexTimeoutMessage(normalized):
		return codexTimeoutStatusLabel()
	case isCodexConnectionFailureMessage(normalized):
		return codexConnectionFailedStatusLabel()
	case isCodexCodeModeHostFailure(message):
		return codexCodeModeHostStatusLabel()
	case codexGenericStderrStatusLabel(message) != "":
		return codexGenericStderrStatusLabel(message)
	default:
		return ""
	}
}

func (s *appServerSession) maybeAppendCodeModeHostDiagnosis(message string) {
	if !isCodexCodeModeHostFailure(message) {
		return
	}
	const diagnosis = "Codex's code-mode helper could not start. Reopen or /reconnect this embedded session so LCR can disable the unavailable helper automatically. If the problem returns, update or reinstall Codex."

	s.mu.Lock()
	if s.reportedCodeModeHostErr {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.reportedCodeModeHostErr = true
	s.appendEntryLocked("", TranscriptSystem, diagnosis)
	s.lastSystemNotice = diagnosis
	s.status = codexCodeModeHostStatusLabel()
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) maybeAppendAuth403Diagnosis(message string) {
	diagnosis := diagnoseCodexAuth403(message)
	if diagnosis == "" {
		return
	}

	s.mu.Lock()
	if s.reportedAuth403 {
		s.mu.Unlock()
		return
	}
	s.touchLocked()
	s.reportedAuth403 = true
	s.appendEntryLocked("", TranscriptSystem, diagnosis)
	s.lastSystemNotice = diagnosis
	status := strings.ToLower(strings.TrimSpace(s.status))
	if status == "" || status == "codex error" || strings.HasPrefix(status, "codex stderr:") || strings.Contains(status, "403 forbidden") {
		s.status = codexAuth403StatusLabel()
	}
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) appendEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID != "" {
		if index, ok := s.entryIndex[itemID]; ok {
			s.invalidateTranscriptCacheLocked()
			s.entries[index].Text += text
			return
		}
		s.entryIndex[itemID] = len(s.entries)
	}
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) appendEntryWithDisplayLocked(itemID string, kind TranscriptKind, text, displayText string) {
	if strings.TrimSpace(displayText) == strings.TrimSpace(text) {
		displayText = ""
	}
	if itemID != "" {
		if index, ok := s.entryIndex[itemID]; ok {
			s.invalidateTranscriptCacheLocked()
			s.entries[index].Text += text
			return
		}
		s.entryIndex[itemID] = len(s.entries)
	}
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text, DisplayText: displayText})
}

func (s *appServerSession) ensureItemEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		changed := false
		if s.entries[index].Kind == "" {
			s.entries[index].Kind = kind
			changed = true
		}
		if s.entries[index].Text == "" {
			s.entries[index].Text = text
			changed = true
		}
		if changed {
			s.invalidateTranscriptCacheLocked()
		}
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) bindOptimisticEntryLocked(itemID string, kind TranscriptKind, text string) bool {
	if itemID == "" || strings.TrimSpace(text) == "" {
		return false
	}
	trimmed := strings.TrimSpace(text)
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := &s.entries[i]
		if entry.ItemID != "" || entry.Kind != kind {
			continue
		}
		if strings.TrimSpace(entry.Text) != trimmed {
			continue
		}
		displayText := entry.DisplayText // preserve display text from optimistic entry
		entry.ItemID = itemID
		entry.Kind = kind
		entry.Text = text
		entry.DisplayText = displayText
		s.entryIndex[itemID] = i
		s.invalidateTranscriptCacheLocked()
		return true
	}
	return false
}

func (s *appServerSession) upsertItemEntryLocked(itemID string, kind TranscriptKind, text string) {
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		if s.entries[index].Kind == kind && s.entries[index].Text == text && s.entries[index].GeneratedImage == nil {
			return
		}
		s.invalidateTranscriptCacheLocked()
		s.entries[index].Kind = kind
		s.entries[index].Text = text
		s.entries[index].GeneratedImage = nil
		return
	}
	if s.bindOptimisticEntryLocked(itemID, kind, text) {
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) upsertRenderedItemEntryLocked(itemID string, kind TranscriptKind, text string, image *GeneratedImageArtifact) {
	if image == nil {
		s.upsertItemEntryLocked(itemID, kind, text)
		return
	}
	if itemID == "" {
		s.invalidateTranscriptCacheLocked()
		s.entries = append(s.entries, transcriptEntry{Kind: kind, Text: text, GeneratedImage: cloneGeneratedImageArtifact(image)})
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		if s.entries[index].Kind == kind && s.entries[index].Text == text && generatedImageArtifactsEqual(s.entries[index].GeneratedImage, image) {
			return
		}
		s.invalidateTranscriptCacheLocked()
		s.entries[index].Kind = kind
		s.entries[index].Text = text
		s.entries[index].DisplayText = ""
		s.entries[index].GeneratedImage = cloneGeneratedImageArtifact(image)
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{
		ItemID:         itemID,
		Kind:           kind,
		Text:           text,
		GeneratedImage: cloneGeneratedImageArtifact(image),
	})
}

func (s *appServerSession) appendDeltaToItemLocked(itemID string, kind TranscriptKind, text string) {
	if text == "" {
		return
	}
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		s.invalidateTranscriptCacheLocked()
		if s.entries[index].Kind == "" || s.entries[index].Kind == TranscriptOther {
			s.entries[index].Kind = kind
		}
		s.entries[index].Text += text
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) mergeRenderedHistoryItemLocked(itemID string, kind TranscriptKind, text string, image *GeneratedImageArtifact) {
	if image == nil {
		s.mergeHistoryItemLocked(itemID, kind, text)
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	s.upsertRenderedItemEntryLocked(itemID, kind, text, image)
}

func (s *appServerSession) mergeHistoryItemLocked(itemID string, kind TranscriptKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if itemID == "" {
		s.appendEntryLocked("", kind, text)
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		changed := false
		if s.entries[index].Kind == "" {
			s.entries[index].Kind = kind
			changed = true
		}
		current := s.entries[index].Text
		switch {
		case current == "":
			s.entries[index].Text = text
			changed = true
		case strings.HasPrefix(text, current):
			s.entries[index].Text = text
			changed = true
		case strings.HasPrefix(current, text):
			return
		case compactionTranscriptText(current) && compactionTranscriptText(text):
			s.entries[index].Text = text
			changed = true
		}
		if changed {
			s.invalidateTranscriptCacheLocked()
		}
		return
	}
	s.entryIndex[itemID] = len(s.entries)
	s.invalidateTranscriptCacheLocked()
	s.entries = append(s.entries, transcriptEntry{ItemID: itemID, Kind: kind, Text: text})
}

func (s *appServerSession) finalizeCommandItemLocked(itemID string, item map[string]json.RawMessage) {
	text := strings.TrimSpace(renderResumedCommandExecution(item))
	if itemID == "" {
		if text != "" {
			s.appendEntryLocked("", TranscriptCommand, text)
		}
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		changed := false
		s.entries[index].Kind = TranscriptCommand
		changed = true
		aggregated := strings.TrimSpace(decodeRawNullableString(item["aggregatedOutput"]))
		switch {
		case text == "":
			if changed {
				s.invalidateTranscriptCacheLocked()
			}
			return
		case aggregated != "", strings.TrimSpace(s.entries[index].Text) == "":
			s.entries[index].Text = text
			changed = true
		default:
			statusLine := renderCommandStatusLine(decodeRawString(item["status"]), decodeRawNullableInt(item["exitCode"]))
			if statusLine == "" {
				s.entries[index].Text = text
				changed = true
			} else {
				nextText := upsertTrailingSummaryLine(s.entries[index].Text, statusLine, "[command ")
				if nextText != s.entries[index].Text {
					s.entries[index].Text = nextText
					changed = true
				}
			}
		}
		if changed {
			s.invalidateTranscriptCacheLocked()
		}
		return
	}
	s.upsertItemEntryLocked(itemID, TranscriptCommand, text)
}

func (s *appServerSession) finalizeFileChangeItemLocked(itemID string, item map[string]json.RawMessage) {
	text := strings.TrimSpace(renderResumedFileChange(item))
	if itemID == "" {
		if text != "" {
			s.appendEntryLocked("", TranscriptFileChange, text)
		}
		return
	}
	if index, ok := s.entryIndex[itemID]; ok {
		changed := false
		s.entries[index].Kind = TranscriptFileChange
		changed = true
		switch {
		case strings.TrimSpace(s.entries[index].Text) == "":
			s.entries[index].Text = text
			changed = true
		default:
			statusLine := renderFileChangeStatusLine(decodeRawString(item["status"]))
			if statusLine == "" {
				if text != "" {
					s.entries[index].Text = text
					changed = true
				}
			} else {
				nextText := upsertTrailingSummaryLine(s.entries[index].Text, statusLine, "[file changes ")
				if nextText != s.entries[index].Text {
					s.entries[index].Text = nextText
					changed = true
				}
			}
		}
		if changed {
			s.invalidateTranscriptCacheLocked()
		}
		return
	}
	s.upsertItemEntryLocked(itemID, TranscriptFileChange, text)
}

func upsertTrailingSummaryLine(text, summary, prefix string) string {
	text = strings.TrimRight(text, "\n")
	summary = strings.TrimSpace(summary)
	if text == "" {
		return summary
	}
	if summary == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			lines[i] = summary
			return strings.Join(lines, "\n")
		}
	}
	lines = append(lines, summary)
	return strings.Join(lines, "\n")
}
