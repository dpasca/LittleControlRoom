package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lcroom/internal/codexcli"
	"strconv"
	"strings"
	"time"
)

func (s *appServerSession) hydrateResumedThread(thread resumedThread) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hydrateResumedThreadLocked(thread)
}

func (s *appServerSession) hydrateResumedThreadLocked(thread resumedThread) {
	s.touchLocked()
	if thread.ID != "" {
		s.threadID = thread.ID
	}
	wasBusy := s.busy
	previousBusySince := s.busySince
	previousBusyActivityAt := s.lastBusyActivityAt
	previousTurnID := strings.TrimSpace(s.activeTurnID)

	activeTurnID := activeTurnIDFromThread(thread)
	busy := activeTurnID != ""
	busyExternal := busy && !s.compacting
	s.activeItems = nil
	s.pendingCompletion = nil
	currentBrowserPageURL := s.mergeResumedThreadItemsLocked(thread)

	busySince := time.Time{}
	lastBusyActivityAt := time.Time{}
	switch {
	case !busy:
		busySince = time.Time{}
	case !previousBusySince.IsZero() && wasBusy && previousTurnID == strings.TrimSpace(activeTurnID):
		busySince = previousBusySince
		lastBusyActivityAt = previousBusyActivityAt
	}

	s.busy = busy
	s.busyExternal = busyExternal
	s.busySince = busySince
	s.lastBusyActivityAt = lastBusyActivityAt
	s.activeTurnID = activeTurnID
	s.currentBrowserPageURL = strings.TrimSpace(currentBrowserPageURL)
	s.currentBrowserPageStale = s.currentBrowserPageURL != ""
}

func (s *appServerSession) mergeResumedThreadItemsLocked(thread resumedThread) string {
	if thread.ID != "" {
		s.threadID = thread.ID
	}
	currentBrowserPageURL := ""
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			if call, ok := s.browserToolCallForItem(item); ok {
				if pageURL := extractPlaywrightPageURL(item["result"]); pageURL != "" {
					currentBrowserPageURL = pageURL
				} else if strings.EqualFold(strings.TrimSpace(call.ToolName), "browser_close") {
					currentBrowserPageURL = ""
				}
			}
			itemID, kind, text, image := s.renderThreadItemForTurn(turn.Status, item)
			s.mergeRenderedHistoryItemLocked(itemID, kind, text, image)
		}
		if turn.Status == "failed" && turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "" {
			s.appendEntryLocked("", TranscriptError, turn.Error.Message)
		}
	}
	return currentBrowserPageURL
}

func readLines(r io.Reader, handle func([]byte)) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				handle(line)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func approvalPolicyForPreset(p codexcli.Preset) string {
	switch p {
	case codexcli.PresetFullAuto, codexcli.PresetSafe:
		return "on-request"
	default:
		return "never"
	}
}

func sandboxModeForPreset(p codexcli.Preset) string {
	switch p {
	case codexcli.PresetFullAuto:
		return "workspace-write"
	case codexcli.PresetSafe:
		return "read-only"
	default:
		return "danger-full-access"
	}
}

func decodeRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func decodeRawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return -1
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return -1
	}
	return value
}

func idKey(raw json.RawMessage) string {
	return strings.TrimSpace(string(raw))
}

func decodeRequestID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if value[0] == '"' {
		var raw string
		if err := json.Unmarshal([]byte(value), &raw); err == nil {
			return raw
		}
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n
	}
	return value
}

func normalizeRequestID(value any) string {
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
}

func cloneApprovalRequest(req *ApprovalRequest) *ApprovalRequest {
	if req == nil {
		return nil
	}
	clone := *req
	return &clone
}

func cloneToolInputRequest(req *ToolInputRequest) *ToolInputRequest {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.Questions) > 0 {
		clone.Questions = make([]ToolInputQuestion, len(req.Questions))
		copy(clone.Questions, req.Questions)
		for i := range req.Questions {
			if len(req.Questions[i].Options) > 0 {
				clone.Questions[i].Options = append([]ToolInputOption(nil), req.Questions[i].Options...)
			}
		}
	}
	return &clone
}

func cloneElicitationRequest(req *ElicitationRequest) *ElicitationRequest {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.RequestedSchema) > 0 {
		clone.RequestedSchema = append(json.RawMessage(nil), req.RequestedSchema...)
	}
	return &clone
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type activeTurnMismatch struct {
	ExpectedTurnID string
	FoundTurnID    string
}

func isActiveTurnMismatchError(err error) bool {
	return parseActiveTurnMismatchError(err) != nil
}

func isRecoverableSteerError(err error) bool {
	return isActiveTurnMismatchError(err) || isNoActiveTurnToSteerError(err)
}

func isBusyTurnLikelyStuckError(err error) bool {
	return errors.Is(err, errBusyTurnLikelyStuck)
}

func parseActiveTurnMismatchError(err error) *activeTurnMismatch {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	const prefix = "expected active turn id "
	if !strings.HasPrefix(message, prefix) {
		return nil
	}
	rest := strings.TrimPrefix(message, prefix)
	expectedTurnID, remainder, ok := parseQuotedTurnID(rest)
	if !ok {
		return nil
	}
	const separator = " but found "
	if !strings.HasPrefix(remainder, separator) {
		return nil
	}
	foundTurnID, remainder, ok := parseQuotedTurnID(strings.TrimPrefix(remainder, separator))
	if !ok || strings.TrimSpace(remainder) != "" {
		return nil
	}
	return &activeTurnMismatch{
		ExpectedTurnID: expectedTurnID,
		FoundTurnID:    foundTurnID,
	}
}

func isNoActiveTurnToSteerError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	return strings.Contains(message, "no active turn to steer")
}

func parseQuotedTurnID(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	quote := value[0]
	if quote != '`' && quote != '"' && quote != '\'' {
		return "", "", false
	}
	end := strings.IndexByte(value[1:], quote)
	if end < 0 {
		return "", "", false
	}
	end++
	return value[1:end], value[end+1:], true
}

func activeTurnIDFromThread(thread resumedThread) string {
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		turn := thread.Turns[i]
		if strings.EqualFold(strings.TrimSpace(turn.Status), "inProgress") && strings.TrimSpace(turn.ID) != "" {
			return strings.TrimSpace(turn.ID)
		}
	}
	return ""
}

func effectiveThreadStatus(thread resumedThread) resumedThreadStatus {
	if activeTurnIDFromThread(thread) == "" {
		return resumedThreadStatus{Type: "idle"}
	}
	return thread.Status
}

func (s *appServerSession) waitForCompactionCompletion(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}

	for {
		thread, err := s.readThreadState(ctx, threadID)
		if err != nil {
			s.clearCompactionProgress()
			s.appendSystemError(err)
			return err
		}

		status := effectiveThreadStatus(thread)
		if strings.TrimSpace(thread.Status.Type) == "systemError" {
			status = thread.Status
		}

		s.mu.Lock()
		s.hydrateResumedThreadLocked(thread)
		s.syncThreadStatusLocked(threadID, status, true)
		s.mu.Unlock()
		s.notify()

		switch strings.TrimSpace(status.Type) {
		case "", "idle":
			return nil
		case "systemError":
			err := fmt.Errorf("codex reported a system error while compacting the conversation")
			s.clearCompactionProgress()
			s.appendSystemError(err)
			return err
		}

		select {
		case <-ctx.Done():
			err := ctx.Err()
			s.clearCompactionProgress()
			s.appendSystemError(err)
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *appServerSession) clearCompactionProgress() {
	s.mu.Lock()
	s.compacting = false
	s.contextCompactionActive = false
	s.mu.Unlock()
}

func threadHasRetainedHistory(thread resumedThread) bool {
	if activeTurnIDFromThread(thread) != "" {
		return true
	}
	for _, turn := range thread.Turns {
		if len(turn.Items) > 0 {
			return true
		}
	}
	return false
}

func isFreshThreadUnmaterializedError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not materialized yet") &&
		strings.Contains(message, "includeturns is unavailable before first user message")
}
