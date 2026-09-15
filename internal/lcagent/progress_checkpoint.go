package lcagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
)

const progressCheckpointTool = "report_progress"
const progressCheckpointCalls = 12

// Work/time counters schedule reflection; the model evaluates meaning and
// chooses the next strategy. These counters are independent of compaction.
type progressCheckpointState struct {
	calls    int
	last     time.Time
	steered  bool
	failures int
	finish   bool
}

func (s progressCheckpointState) reason(now time.Time) string {
	if s.steered {
		return "user_correction"
	}
	if s.calls >= progressCheckpointCalls {
		return "work_budget"
	}
	if s.calls > 0 && now.Sub(s.last) >= time.Minute {
		return "elapsed_time"
	}
	return ""
}

type progressCheckpointReport struct {
	Objective   string   `json:"objective"`
	UserUpdate  string   `json:"user_update"`
	NewEvidence []string `json:"new_evidence"`
	Constraints []string `json:"constraints"`
	Blocker     string   `json:"blocker"`
	Decision    string   `json:"decision"`
	NextAction  string   `json:"next_action"`
}

func progressCheckpointTools() []modeladapter.ToolDefinition {
	textField := func(description string) map[string]any {
		return map[string]any{"type": "string", "maxLength": 1200, "description": description}
	}
	listField := func(description string) map[string]any {
		return map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 600}, "description": description}
	}
	return []modeladapter.ToolDefinition{{Type: "function", Function: modeladapter.FunctionSpec{
		Name:        progressCheckpointTool,
		Description: "Assess progress and user corrections before further execution. This reports your assessment; it does not run an action or verify completion.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"objective", "user_update", "new_evidence", "constraints", "blocker", "decision", "next_action"},
			"properties": map[string]any{
				"objective":    textField("Current overall objective, retaining the original task unless the user canceled or replaced it."),
				"user_update":  textField("Brief user-facing update: what is established, what remains, and how you will proceed. Address recent corrections directly."),
				"new_evidence": listField("Concrete new evidence since the last checkpoint; empty if none. Different tool output alone does not prove useful progress."),
				"constraints":  listField("Current user constraints, including corrections about the approach or its impact."),
				"blocker":      textField("Specific remaining blocker, or empty if none."),
				"decision":     map[string]any{"type": "string", "enum": []string{"continue", "change_strategy", "finish"}},
				"next_action":  textField("Concrete justified next step. For change_strategy, explain the different approach. For finish, name the result or human action needed."),
			},
		},
	}}}
}

func progressCheckpointPrompt(reason string) string {
	return "Harness progress checkpoint (" + reason + "). This is not a new user request. Before any further execution, call report_progress exactly once. Assess actual progress toward the overall task, incorporate the user's latest corrections and constraints, and choose whether to continue, change strategy, or finish honestly with a result or concrete human handoff. An unfinished task or open dialog is not itself a failed verification. Do not count changing queries, commands, timings, or snippets as useful progress without new task evidence. Do not repeat an approach the user has objected to. No execution tools are available in this request."
}

func decodeProgressCheckpoint(args json.RawMessage) (progressCheckpointReport, error) {
	var report progressCheckpointReport
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(&report); err != nil {
		return report, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return report, fmt.Errorf("expected one report object")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(args, &fields)
	for _, key := range []string{"objective", "user_update", "new_evidence", "constraints", "blocker", "decision", "next_action"} {
		if len(fields[key]) == 0 || bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return report, fmt.Errorf("%s is required", key)
		}
	}
	for _, value := range []string{report.Objective, report.UserUpdate, report.NextAction} {
		if strings.TrimSpace(value) == "" {
			return report, fmt.Errorf("objective, user_update, and next_action must be nonempty")
		}
	}
	for _, value := range []string{report.Objective, report.UserUpdate, report.NextAction, report.Blocker} {
		if len([]rune(value)) > 1200 {
			return report, fmt.Errorf("report text exceeds 1200 characters")
		}
	}
	for _, values := range [][]string{report.NewEvidence, report.Constraints} {
		if len(values) > 8 {
			return report, fmt.Errorf("report lists allow at most 8 items")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len([]rune(value)) > 600 {
				return report, fmt.Errorf("list items must contain 1–600 characters")
			}
		}
	}
	switch report.Decision {
	case "continue", "change_strategy", "finish":
		return report, nil
	default:
		return report, fmt.Errorf("decision must be continue, change_strategy, or finish")
	}
}

// Reject every execution call in a checkpoint response, including calls batched
// alongside a valid report. Always close tool calls in provider history.
func acceptProgressCheckpoint(writer *session.Writer, sessionID string, messages []modeladapter.Message, msg modeladapter.Message) ([]modeladapter.Message, *progressCheckpointReport, error) {
	messages = append(messages, msg)
	var report *progressCheckpointReport
	feedback := "Call report_progress exactly once before further execution. No requested actions were executed."
	if len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Function.Name == progressCheckpointTool {
		args, err := modeladapter.NormalizeArguments(msg.ToolCalls[0].Function.Arguments)
		if err == nil {
			var decoded progressCheckpointReport
			decoded, err = decodeProgressCheckpoint(args)
			if err == nil {
				report = &decoded
			}
		}
		if err != nil {
			feedback += " Invalid report: " + err.Error()
		}
	}
	for _, call := range msg.ToolCalls {
		result := tools.ToolResult{Success: report != nil}
		if report != nil {
			result.Output = "Progress assessment recorded. Follow the reported constraints and next action."
		} else {
			result.Error = feedback
		}
		if err := writeCheckpointToolResult(writer, sessionID, call, result); err != nil {
			return messages, nil, err
		}
		body, _ := json.Marshal(result)
		messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: string(body)})
	}
	if report == nil {
		messages = append(messages, modeladapter.Message{Role: "user", Content: feedback})
	}
	return messages, report, nil
}

func rejectFinalizationTools(writer *session.Writer, sessionID string, messages []modeladapter.Message, msg modeladapter.Message) ([]modeladapter.Message, error) {
	messages = append(messages, msg)
	for _, call := range msg.ToolCalls {
		result := tools.ToolResult{Success: false, Error: "Only final_response is available while finalizing. This tool was not executed. Preserve the answer and human handoff, and provide honest outcome/verification metadata."}
		if err := writeCheckpointToolResult(writer, sessionID, call, result); err != nil {
			return messages, err
		}
		body, _ := json.Marshal(result)
		messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: string(body)})
	}
	return messages, nil
}

func writeCheckpointToolResult(writer *session.Writer, sessionID string, call modeladapter.ToolCall, result tools.ToolResult) error {
	if !json.Valid(call.Function.Arguments) {
		return writeInvalidToolArgumentsResult(writer, sessionID, call, call.Function.Arguments, result)
	}
	if err := writer.Write(session.Event{"type": "tool_call", "session_id": sessionID, "tool": call.Function.Name, "args": call.Function.Arguments}); err != nil {
		return err
	}
	return writer.Write(session.Event{"type": "tool_result", "session_id": sessionID, "tool": call.Function.Name, "result": result})
}
