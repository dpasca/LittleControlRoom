package lcagent

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/tools"
)

func TestParseCriticReviewPayloadFromFencedJSON(t *testing.T) {
	payload, err := parseCriticReviewPayload("```json\n{\"status\":\"needs-followup\",\"confidence\":0.82,\"summary\":\"check verification\",\"findings\":[{\"severity\":\"urgent\",\"claim\":\"tests were not run\",\"evidence_source\":\"lead_final\",\"evidence\":\"final says not run\",\"suggested_followup\":\"run tests\"}],\"proposed_user_message\":\"Please run the tests.\"}\n```")
	if err != nil {
		t.Fatalf("parseCriticReviewPayload() error = %v", err)
	}
	payload = normalizeCriticReviewForRouting(payload)
	if payload.Status != "needs_followup" {
		t.Fatalf("status = %q", payload.Status)
	}
	if len(payload.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(payload.Findings))
	}
	if payload.Findings[0].Severity != "medium" {
		t.Fatalf("severity = %q, want normalized medium", payload.Findings[0].Severity)
	}
	if payload.Findings[0].Materiality != "medium" {
		t.Fatalf("materiality = %q, want fallback medium", payload.Findings[0].Materiality)
	}
	if payload.ProposedUserMessage != "Please run the tests." {
		t.Fatalf("proposed message = %q", payload.ProposedUserMessage)
	}
}

func TestNormalizeCriticReviewForRoutingClearsConcernDraft(t *testing.T) {
	payload := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:              "concerns",
		Summary:             "wording is slightly imprecise",
		ProposedUserMessage: "Please clarify the wording.",
		Findings: []criticReviewFinding{{
			Severity:       "low",
			Materiality:    "low",
			Claim:          "minor wording issue",
			EvidenceSource: "lead_final",
			Evidence:       "the final wording was slightly broad",
		}},
	})

	if payload.Status != "concerns" {
		t.Fatalf("status = %q, want concerns", payload.Status)
	}
	if payload.ProposedUserMessage != "" || payload.HumanPrompt != "" {
		t.Fatalf("low concern should not draft follow-up: proposed=%q human=%q", payload.ProposedUserMessage, payload.HumanPrompt)
	}
	if len(payload.Findings) != 1 || payload.Findings[0].Materiality != "low" {
		t.Fatalf("findings = %#v", payload.Findings)
	}
}

func TestNormalizeCriticReviewForRoutingBlocksLowMaterialFollowupDraft(t *testing.T) {
	payload := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:      "needs_followup",
		HumanPrompt: "Please ask the lead to clarify a tiny wording issue.",
		Findings: []criticReviewFinding{{
			Severity:       "low",
			Materiality:    "low",
			Claim:          "minor wording issue",
			EvidenceSource: "lead_final",
			Evidence:       "the final wording was slightly broad",
		}},
	})

	if payload.Status != "needs_followup" {
		t.Fatalf("status = %q, want needs_followup", payload.Status)
	}
	if payload.ProposedUserMessage != "" || payload.HumanPrompt != "" {
		t.Fatalf("low-material follow-up should be suppressed: proposed=%q human=%q", payload.ProposedUserMessage, payload.HumanPrompt)
	}
}

func TestBuildCriticReviewPacketAddsEvidenceExcerptsForTruncatedToolOutput(t *testing.T) {
	sourceEvidence := `textbox "file content" [ref=e385]: "const tests = [{ input: {\"ops\":[{\"attributes\":{\"indent\":1,\"list\":\"bullet\"},\"insert\":\"\\n\"},{\"attributes\":{\"slackemoji\":true},\"insert\":\"party\"},{\"attributes\":{\"slackmention\":\"U123\"},\"insert\":\"Davide\"}]}}];"`
	output := "status: navigated\n" +
		strings.Repeat("github navigation chrome\n", 220) +
		sourceEvidence +
		strings.Repeat("\nfooter chrome", 220)
	body, err := json.Marshal(tools.ToolResult{Success: true, Output: output})
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	packet := buildCriticReviewPacket("sess-1", "Did you read the file?", script.Action{
		Outcome: "success",
		Summary: "I read `textbox \"file content\"` and saw \"indent\", \"slackemoji\", and \"slackmention\" in the source.",
	}, []modeladapter.Message{{
		Role:       "tool",
		Content:    string(body),
		ToolCallID: "call_1",
	}}, false)

	if len(packet.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(packet.Messages))
	}
	msg := packet.Messages[0]
	if !msg.Truncated {
		t.Fatalf("tool message should be truncated")
	}
	if len(msg.EvidenceExcerpts) == 0 {
		t.Fatalf("expected evidence excerpts for truncated tool output")
	}
	var joined strings.Builder
	for _, excerpt := range msg.EvidenceExcerpts {
		joined.WriteString(excerpt.Text)
		joined.WriteByte('\n')
		if excerpt.Source != "tool_result.output" {
			t.Fatalf("excerpt source = %q, want tool_result.output", excerpt.Source)
		}
	}
	evidence := joined.String()
	for _, want := range []string{`textbox "file content"`, "indent", "slackemoji", "slackmention"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("evidence excerpts missing %q:\n%s", want, evidence)
		}
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
