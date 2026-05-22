package lcagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
)

const (
	approvalModeDeny = "deny"
	approvalModeAsk  = "ask"
)

type approvalResponse struct {
	Type     string                  `json:"type"`
	ID       string                  `json:"id"`
	Decision script.ApprovalDecision `json:"decision"`
}

type stdioApprovalBroker struct {
	writer    *session.Writer
	sessionID string
	cwd       string
	responses <-chan approvalResponse
	nextID    int64
}

func normalizeApprovalMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", approvalModeDeny:
		return approvalModeDeny, nil
	case approvalModeAsk:
		return approvalModeAsk, nil
	default:
		return "", fmt.Errorf("unsupported approval mode: %s", raw)
	}
}

func newStdioApprovalBroker(writer *session.Writer, sessionID, cwd string, input io.Reader) *stdioApprovalBroker {
	responses := make(chan approvalResponse)
	go readApprovalResponses(input, responses)
	return &stdioApprovalBroker{
		writer:    writer,
		sessionID: strings.TrimSpace(sessionID),
		cwd:       strings.TrimSpace(cwd),
		responses: responses,
	}
}

func readApprovalResponses(input io.Reader, responses chan<- approvalResponse) {
	defer close(responses)
	if input == nil {
		return
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		var response approvalResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}
		if response.Type != "" && response.Type != "approval_response" {
			continue
		}
		response.ID = strings.TrimSpace(response.ID)
		if response.ID == "" {
			continue
		}
		response.Decision = script.NormalizeApprovalDecision(string(response.Decision))
		responses <- response
	}
}

func (b *stdioApprovalBroker) RequestCommandApproval(ctx context.Context, request script.CommandApprovalRequest) (script.ApprovalDecision, error) {
	if b == nil || b.writer == nil {
		return script.DecisionCancel, nil
	}
	request.ID = firstNonEmptyString(strings.TrimSpace(request.ID), b.nextApprovalID())
	request.SessionID = firstNonEmptyString(strings.TrimSpace(request.SessionID), b.sessionID)
	request.Tool = firstNonEmptyString(strings.TrimSpace(request.Tool), "run_command")
	request.CWD = firstNonEmptyString(strings.TrimSpace(request.CWD), b.cwd)
	decision := script.DecisionCancel
	if err := b.writer.Write(session.Event{
		"type":       "approval_request",
		"session_id": request.SessionID,
		"id":         request.ID,
		"kind":       "command",
		"tool":       request.Tool,
		"command":    request.Command,
		"cwd":        request.CWD,
		"reason":     request.Reason,
		"decisions":  []string{string(script.DecisionAccept), string(script.DecisionAcceptForSession), string(script.DecisionDecline), string(script.DecisionCancel)},
	}); err != nil {
		return decision, err
	}
	defer func() {
		_ = b.writer.Write(session.Event{
			"type":       "approval_resolved",
			"session_id": request.SessionID,
			"id":         request.ID,
			"kind":       "command",
			"tool":       request.Tool,
			"command":    request.Command,
			"decision":   string(decision),
			"status":     approvalDecisionStatus(decision),
		})
	}()
	for {
		select {
		case <-ctx.Done():
			return script.DecisionCancel, ctx.Err()
		case response, ok := <-b.responses:
			if !ok {
				return script.DecisionCancel, nil
			}
			if response.ID != request.ID {
				continue
			}
			decision = response.Decision
			return decision, nil
		}
	}
}

func (b *stdioApprovalBroker) nextApprovalID() string {
	n := atomic.AddInt64(&b.nextID, 1)
	return fmt.Sprintf("lca_approval_%d", n)
}

func approvalDecisionStatus(decision script.ApprovalDecision) string {
	switch decision {
	case script.DecisionAccept, script.DecisionAcceptForSession:
		return "approved"
	case script.DecisionDecline:
		return "declined"
	default:
		return "canceled"
	}
}
