package lcagent

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
)

func TestParseCriticReviewPayloadFromFencedJSON(t *testing.T) {
	payload, err := parseCriticReviewPayload("```json\n{\"status\":\"needs-followup\",\"confidence\":0.82,\"summary\":\"check verification\",\"findings\":[{\"severity\":\"urgent\",\"claim\":\"tests were not run\",\"evidence_source\":\"lead_final\",\"evidence\":\"final says not run\",\"suggested_followup\":\"run tests\"}],\"proposed_user_message\":\"Please run the tests.\"}\n```")
	if err != nil {
		t.Fatalf("parseCriticReviewPayload() error = %v", err)
	}
	payload.Status = normalizeCriticStatus(payload.Status)
	payload.Findings = cleanCriticFindings(payload.Findings)
	if payload.Status != "needs_followup" {
		t.Fatalf("status = %q", payload.Status)
	}
	if len(payload.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(payload.Findings))
	}
	if payload.Findings[0].Severity != "medium" {
		t.Fatalf("severity = %q, want normalized medium", payload.Findings[0].Severity)
	}
	if payload.ProposedUserMessage != "Please run the tests." {
		t.Fatalf("proposed message = %q", payload.ProposedUserMessage)
	}
}

func TestBuildCriticReviewPacketKeepsToolTraceAndOmissionCount(t *testing.T) {
	messages := make([]modeladapter.Message, 0, 82)
	for i := 0; i < 81; i++ {
		messages = append(messages, modeladapter.Message{Role: "assistant", Content: "message"})
	}
	args, _ := json.Marshal(map[string]string{"command": strings.Repeat("x", 1800)})
	messages = append(messages, modeladapter.Message{
		Role:    "assistant",
		Content: "I will run verification",
		ToolCalls: []modeladapter.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: modeladapter.FunctionCall{
				Name:      "run_command",
				Arguments: args,
			},
		}},
	})

	packet := buildCriticReviewPacket("sess-1", "fix it", script.Action{
		Outcome:      "success",
		Summary:      "done",
		FilesChanged: []string{"main.go"},
		Verification: []string{"go test ./..."},
	}, messages, true)
	if packet.MessagesOmitted != 2 {
		t.Fatalf("MessagesOmitted = %d, want 2", packet.MessagesOmitted)
	}
	if packet.ContextMode != "compacted" {
		t.Fatalf("ContextMode = %q, want compacted", packet.ContextMode)
	}
	last := packet.Messages[len(packet.Messages)-1]
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "run_command" {
		t.Fatalf("tool calls = %#v", last.ToolCalls)
	}
	if !last.ToolCalls[0].Truncated {
		t.Fatalf("tool call arguments should be truncated")
	}
	if packet.FilesChanged[0] != "main.go" || packet.Verification[0] != "go test ./..." {
		t.Fatalf("final evidence not copied: %#v", packet)
	}
}
