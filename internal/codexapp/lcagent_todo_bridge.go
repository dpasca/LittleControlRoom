package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"lcroom/internal/lcagent/tools"
	"lcroom/internal/todocapture"
)

const lcagentTodoRequestTimeout = 15 * time.Second

type lcagentTodoRequest struct {
	ID             string
	Action         todocapture.Action
	Text           string
	CaptureKind    todocapture.CaptureKind
	ReviewRevision string
}

type lcagentTodoBridge struct {
	handler     todocapture.Handler
	mode        todocapture.CaptureMode
	origin      todocapture.Origin
	stdin       io.Writer
	appendAsync func(TranscriptKind, string)
}

func (b lcagentTodoBridge) handle(request lcagentTodoRequest) {
	if b.stdin == nil {
		b.append(TranscriptError, "LCAgent project TODO response failed: session input channel is not available")
		return
	}
	result, response := b.run(request)
	payload, err := json.Marshal(map[string]any{
		"type":   "todo_response",
		"id":     request.ID,
		"result": result,
	})
	if err != nil {
		b.append(TranscriptError, "LCAgent project TODO response failed: "+err.Error())
		return
	}
	if _, err := fmt.Fprintln(b.stdin, string(payload)); err != nil {
		b.append(TranscriptError, "LCAgent project TODO response failed: "+err.Error())
		return
	}
	if !result.Success {
		b.append(TranscriptError, firstNonEmpty(result.Error, "LCAgent project TODO request failed"))
		return
	}
	if text := lcagentTodoResponseText(response); text != "" {
		b.append(TranscriptStatus, text)
	}
}

func (b lcagentTodoBridge) run(request lcagentTodoRequest) (tools.ToolResult, todocapture.Response) {
	mode := todocapture.NormalizeCaptureMode(b.mode)
	if b.handler == nil {
		return tools.ToolResult{Success: false, Error: "project TODO capture handler unavailable"}, todocapture.Response{}
	}
	if !mode.Enabled() {
		return tools.ToolResult{Success: false, Error: "project TODO capture is disabled"}, todocapture.Response{}
	}

	todoRequest := todocapture.Request{
		Action: request.Action,
		Origin: todocapture.Origin{
			ProjectPath: strings.TrimSpace(b.origin.ProjectPath),
			Provider:    strings.TrimSpace(b.origin.Provider),
			SessionKey:  strings.TrimSpace(b.origin.SessionKey),
		},
	}
	switch request.Action {
	case todocapture.ActionList:
	case todocapture.ActionAdd:
		kind, err := todocapture.ParseCaptureKind(string(request.CaptureKind))
		if err != nil {
			return tools.ToolResult{Success: false, Error: err.Error()}, todocapture.Response{}
		}
		if !mode.Allows(kind) {
			return tools.ToolResult{Success: false, Error: fmt.Sprintf("capture kind %q is not allowed in %s mode", kind, mode)}, todocapture.Response{}
		}
		text := strings.TrimSpace(request.Text)
		if text == "" {
			return tools.ToolResult{Success: false, Error: "todo text is required"}, todocapture.Response{}
		}
		revision := strings.TrimSpace(request.ReviewRevision)
		if revision == "" {
			return tools.ToolResult{Success: false, Error: "review revision is required; list project TODOs before adding"}, todocapture.Response{}
		}
		todoRequest.Add = todocapture.AddRequest{
			Text:           text,
			CaptureKind:    kind,
			ReviewRevision: revision,
		}
	default:
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("unsupported project TODO action: %s", request.Action)}, todocapture.Response{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), lcagentTodoRequestTimeout)
	defer cancel()
	response, err := b.handler.HandleTodoCapture(ctx, todoRequest)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}, todocapture.Response{}
	}
	output, err := json.Marshal(response)
	if err != nil {
		return tools.ToolResult{Success: false, Error: "encode project TODO response: " + err.Error()}, todocapture.Response{}
	}
	return tools.ToolResult{Success: true, Output: string(output)}, response
}

func (b lcagentTodoBridge) append(kind TranscriptKind, text string) {
	if b.appendAsync == nil || strings.TrimSpace(text) == "" {
		return
	}
	b.appendAsync(kind, text)
}

func lcagentTodoResponseText(response todocapture.Response) string {
	if response.List != nil {
		count := len(response.List.OpenTodos)
		label := lcagentTodoProjectLabel(response.List.Scope)
		if count == 1 {
			return "LCAgent reviewed 1 open LCR TODO for " + label + "."
		}
		return fmt.Sprintf("LCAgent reviewed %d open LCR TODOs for %s.", count, label)
	}
	if response.Add == nil {
		return "LCAgent project TODO request completed."
	}
	label := lcagentTodoProjectLabel(response.Add.Scope)
	switch strings.TrimSpace(response.Add.Disposition) {
	case todocapture.DispositionCreated:
		if response.Add.Todo != nil {
			return fmt.Sprintf("Added LCR TODO #%d to %s.", response.Add.Todo.ID, label)
		}
		return "Added an LCR TODO to " + label + "."
	case todocapture.DispositionExistingDuplicate:
		if response.Add.Todo != nil {
			return fmt.Sprintf("LCR TODO #%d already exists in %s.", response.Add.Todo.ID, label)
		}
		return "That TODO already exists in " + label + "."
	case todocapture.DispositionTodosChanged:
		return "Project TODOs changed; LCAgent must review the current list before adding."
	default:
		return "LCAgent project TODO request completed."
	}
}

func lcagentTodoProjectLabel(scope todocapture.ProjectScope) string {
	if name := strings.TrimSpace(scope.ProjectName); name != "" {
		return name
	}
	if path := strings.TrimSpace(scope.ProjectPath); path != "" {
		return path
	}
	return "this project"
}
