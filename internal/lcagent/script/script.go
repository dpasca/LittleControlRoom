package script

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

type Runner struct {
	Session      *session.Writer
	Command      tools.CommandRunner
	Patch        tools.PatchApplier
	SessionID    string
	Prompt       string
	ArtifactsDir string
}

type Action struct {
	Type         string          `json:"type"`
	Tool         string          `json:"tool,omitempty"`
	Args         json.RawMessage `json:"args,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	FilesChanged []string        `json:"files_changed,omitempty"`
	Verification []string        `json:"verification,omitempty"`
}

type commandArgs struct {
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms"`
}

type patchArgs struct {
	Patch string `json:"patch"`
}

type planArgs struct {
	Items []tools.PlanItem `json:"items"`
}

func Load(path string) ([]Action, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var actions []Action
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var action Action
		if err := json.Unmarshal(line, &action); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return actions, nil
}

func (r Runner) Run(ctx context.Context, actions []Action) error {
	if err := r.Session.Write(session.Event{
		"type":       "user_message",
		"session_id": r.SessionID,
		"message":    r.Prompt,
	}); err != nil {
		return err
	}
	for _, action := range actions {
		switch action.Type {
		case "tool_call":
			if _, err := r.RunTool(ctx, action); err != nil {
				_ = r.Session.Write(session.Event{
					"type":       "turn_aborted",
					"session_id": r.SessionID,
					"reason":     err.Error(),
				})
				return err
			}
		case "final_response":
			return r.Final(action)
		default:
			err := fmt.Errorf("unsupported script action type: %s", action.Type)
			_ = r.Session.Write(session.Event{
				"type":       "turn_aborted",
				"session_id": r.SessionID,
				"reason":     err.Error(),
			})
			return err
		}
	}
	err := fmt.Errorf("script ended without final_response")
	_ = r.Session.Write(session.Event{
		"type":       "turn_aborted",
		"session_id": r.SessionID,
		"reason":     err.Error(),
	})
	return err
}

func (r Runner) RunTool(ctx context.Context, action Action) (tools.ToolResult, error) {
	if err := r.Session.Write(session.Event{
		"type":       "tool_call",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"args":       json.RawMessage(action.Args),
	}); err != nil {
		return tools.ToolResult{}, err
	}

	var result tools.ToolResult
	switch action.Tool {
	case "run_command":
		var args commandArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Command.Run(ctx, args.Command, time.Duration(args.TimeoutMS)*time.Millisecond)
	case "apply_patch":
		var args patchArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Patch.Apply(args.Patch)
		if len(result.FilesTouched) > 0 {
			if err := r.Session.Write(session.Event{
				"type":       "files_touched",
				"session_id": r.SessionID,
				"files":      result.FilesTouched,
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
	case "update_plan":
		var args planArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = tools.ToolResult{Success: true, Output: "plan updated"}
		if err := r.Session.Write(session.Event{
			"type":       "plan_update",
			"session_id": r.SessionID,
			"items":      args.Items,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	default:
		result = tools.ToolResult{Success: false, Error: "unsupported tool: " + action.Tool}
	}

	if err := r.Session.Write(session.Event{
		"type":       "tool_result",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"result":     result,
	}); err != nil {
		return tools.ToolResult{}, err
	}
	if !result.Success {
		return result, fmt.Errorf("%s failed: %s", action.Tool, result.Error)
	}
	return result, nil
}

func (r Runner) Final(action Action) error {
	if err := r.Session.Write(session.Event{
		"type":          "assistant_message",
		"session_id":    r.SessionID,
		"message":       action.Summary,
		"files_changed": action.FilesChanged,
		"verification":  action.Verification,
	}); err != nil {
		return err
	}
	return r.Session.Write(session.Event{
		"type":          "turn_complete",
		"session_id":    r.SessionID,
		"summary":       action.Summary,
		"files_changed": action.FilesChanged,
		"verification":  action.Verification,
	})
}
