package script

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
)

type Runner struct {
	Session      *session.Writer
	Command      tools.CommandRunner
	Patch        tools.PatchApplier
	Files        tools.FileTools
	Skills       skillcatalog.Catalog
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
	Command   string   `json:"command"`
	Argv      []string `json:"argv"`
	Shell     bool     `json:"shell"`
	TimeoutMS int      `json:"timeout_ms"`
}

type patchArgs struct {
	Patch string `json:"patch"`
}

type planArgs struct {
	Items []tools.PlanItem `json:"items"`
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type listFilesArgs struct {
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	MaxEntries int    `json:"max_entries"`
}

type searchArgs struct {
	Query         string `json:"query"`
	Path          string `json:"path"`
	FileGlob      string `json:"file_glob"`
	MaxMatches    int    `json:"max_matches"`
	ContextBefore *int   `json:"context_before"`
	ContextAfter  *int   `json:"context_after"`
}

type fileOutlineArgs struct {
	Path string `json:"path"`
}

type loadSkillArgs struct {
	Name string `json:"name"`
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
	case "read_file":
		var args readFileArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.Read(args.Path, args.Offset, args.Limit)
	case "list_files":
		var args listFilesArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.List(args.Path, args.Glob, args.MaxEntries)
	case "search":
		var args searchArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		contextBefore := 1
		contextAfter := 2
		if args.ContextBefore != nil {
			contextBefore = *args.ContextBefore
		}
		if args.ContextAfter != nil {
			contextAfter = *args.ContextAfter
		}
		result = r.Files.SearchContext(args.Query, args.Path, args.FileGlob, args.MaxMatches, contextBefore, contextAfter)
	case "file_outline":
		var args fileOutlineArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.Outline(args.Path)
	case "load_skill":
		var args loadSkillArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		loaded, err := r.Skills.Load(args.Name)
		if err != nil {
			result = tools.ToolResult{Success: false, Error: err.Error()}
			break
		}
		result = tools.ToolResult{Success: true, Output: formatLoadedSkill(loaded), Truncated: loaded.Truncated}
		if err := r.Session.Write(session.Event{
			"type":        "skill_loaded",
			"session_id":  r.SessionID,
			"name":        loaded.Skill.Name,
			"source":      loaded.Skill.Source,
			"path":        loaded.Skill.Path,
			"description": loaded.Skill.Description,
			"truncated":   loaded.Truncated,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	case "run_command":
		var args commandArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Command.RunSpec(ctx, tools.CommandSpec{
			Command:   args.Command,
			Argv:      args.Argv,
			Shell:     args.Shell || args.Command != "",
			TimeoutMS: args.TimeoutMS,
		})
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

func formatLoadedSkill(loaded skillcatalog.LoadedSkill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s\n", loaded.Skill.Name)
	fmt.Fprintf(&b, "source: %s\n", loaded.Skill.Source)
	fmt.Fprintf(&b, "path: %s\n", loaded.Skill.Path)
	if loaded.Skill.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", loaded.Skill.Description)
	}
	b.WriteString("\n")
	b.WriteString(loaded.Body)
	if loaded.Truncated {
		b.WriteString("\n--- skill body truncated ---\n")
	}
	return b.String()
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
