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
	"lcroom/internal/lcagent/tools"
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

type processResponse struct {
	Type   string           `json:"type"`
	ID     string           `json:"id"`
	Result tools.ToolResult `json:"result"`
	Error  string           `json:"error"`
}

type stdioApprovalBroker struct {
	writer           *session.Writer
	sessionID        string
	cwd              string
	responses        <-chan approvalResponse
	processResponses <-chan processResponse
	steerMessages    <-chan string
	nextID           int64
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
	responses := make(chan approvalResponse, 8)
	processResponses := make(chan processResponse, 8)
	steerMessages := make(chan string, 8)
	go readStdioResponses(input, responses, processResponses, steerMessages)
	return &stdioApprovalBroker{
		writer:           writer,
		sessionID:        strings.TrimSpace(sessionID),
		cwd:              strings.TrimSpace(cwd),
		responses:        responses,
		processResponses: processResponses,
		steerMessages:    steerMessages,
	}
}

func readStdioResponses(input io.Reader, approvalResponses chan<- approvalResponse, processResponses chan<- processResponse, steerMessages chan<- string) {
	defer close(approvalResponses)
	defer close(processResponses)
	defer close(steerMessages)
	if input == nil {
		return
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "", "approval_response":
			var response approvalResponse
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				continue
			}
			response.ID = strings.TrimSpace(response.ID)
			if response.ID == "" {
				continue
			}
			response.Decision = script.NormalizeApprovalDecision(string(response.Decision))
			approvalResponses <- response
		case "process_response":
			var response processResponse
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				continue
			}
			response.ID = strings.TrimSpace(response.ID)
			if response.ID == "" {
				continue
			}
			processResponses <- response
		case "steer":
			var response struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				continue
			}
			if strings.TrimSpace(response.Message) != "" {
				steerMessages <- strings.TrimSpace(response.Message)
			}
		}
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
	request.Scope = strings.TrimSpace(request.Scope)
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
		"scope":      request.Scope,
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
			"cwd":        request.CWD,
			"scope":      request.Scope,
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

func (b *stdioApprovalBroker) RequestProcess(ctx context.Context, request script.ProcessRequest) (tools.ToolResult, error) {
	if b == nil || b.writer == nil {
		return tools.ToolResult{Success: false, Error: "managed background process broker unavailable"}, nil
	}
	request.ID = firstNonEmptyString(strings.TrimSpace(request.ID), b.nextProcessID())
	request.SessionID = firstNonEmptyString(strings.TrimSpace(request.SessionID), b.sessionID)
	request.Command = strings.TrimSpace(request.Command)
	request.ProjectPath = strings.TrimSpace(request.ProjectPath)
	request.CWD = strings.TrimSpace(request.CWD)
	if request.CWD == "" && request.ProjectPath == "" {
		request.CWD = b.cwd
	}
	event := session.Event{
		"type":       "process_request",
		"session_id": request.SessionID,
		"id":         request.ID,
		"action":     string(request.Action),
		"process_id": request.ProcessID,
		"name":       request.Name,
		"command":    request.Command,
		"cwd":        request.CWD,
	}
	if request.ProjectPath != "" {
		event["project_path"] = request.ProjectPath
	}
	if request.CreateNew {
		event["create_new"] = true
	}
	if request.ReplaceExisting {
		event["replace_existing"] = true
	}
	if err := b.writer.Write(event); err != nil {
		return tools.ToolResult{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return tools.ToolResult{Success: false, Error: ctx.Err().Error()}, ctx.Err()
		case response, ok := <-b.processResponses:
			if !ok {
				return tools.ToolResult{Success: false, Error: "managed background process response channel closed"}, nil
			}
			if response.ID != request.ID {
				continue
			}
			if strings.TrimSpace(response.Error) != "" && response.Result.Error == "" {
				response.Result.Success = false
				response.Result.Error = strings.TrimSpace(response.Error)
			}
			return response.Result, nil
		}
	}
}

func (b *stdioApprovalBroker) nextApprovalID() string {
	n := atomic.AddInt64(&b.nextID, 1)
	return fmt.Sprintf("lca_approval_%d", n)
}

func (b *stdioApprovalBroker) nextProcessID() string {
	n := atomic.AddInt64(&b.nextID, 1)
	return fmt.Sprintf("lca_process_%d", n)
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
