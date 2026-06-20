package lcagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
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

func TestNormalizeCriticReviewForRoutingUsesMaterialityForDrafts(t *testing.T) {
	payload := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:      "needs_followup",
		HumanPrompt: "Please ask the user to reopen this.",
		Findings: []criticReviewFinding{{
			Severity:       "medium",
			Materiality:    "low",
			Claim:          "technically true but irrelevant issue",
			EvidenceSource: "lead_final",
			Evidence:       "the final wording could be more exact",
		}},
	})

	if payload.ProposedUserMessage != "" || payload.HumanPrompt != "" {
		t.Fatalf("low-material medium-severity follow-up should be suppressed: proposed=%q human=%q", payload.ProposedUserMessage, payload.HumanPrompt)
	}
}

func TestCriticLeadFeedbackMessageRequiresMaterialFollowup(t *testing.T) {
	low := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:          "needs_followup",
		LeadInstruction: "Nitpick the phrasing.",
		Findings: []criticReviewFinding{{
			Severity:       "medium",
			Materiality:    "low",
			Claim:          "minor phrasing",
			EvidenceSource: "lead_final",
			Evidence:       "summary was broad",
		}},
	})
	if feedback := criticLeadFeedbackMessage(low); feedback != "" {
		t.Fatalf("low-material feedback = %q, want empty", feedback)
	}

	material := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:          "needs_followup",
		LeadInstruction: "Run the missing verification before final_response.",
		Findings: []criticReviewFinding{{
			Severity:          "medium",
			Materiality:       "high",
			Basis:             "confirmed",
			Claim:             "verification was not run",
			EvidenceSource:    "lead_final",
			Evidence:          "verification array was empty",
			UserImpact:        "the user asked for a code change",
			SuggestedFollowup: "run the relevant test",
		}},
	})
	feedback := criticLeadFeedbackMessage(material)
	if !strings.Contains(feedback, "Run the missing verification") || !strings.Contains(feedback, "Top material findings") || !strings.Contains(feedback, "Suggested fix: run the relevant test") {
		t.Fatalf("material feedback = %q", feedback)
	}

	concern := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:          "concerns",
		LeadInstruction: "Tighten the final answer to mention the verification gap.",
		Findings: []criticReviewFinding{{
			Severity:       "medium",
			Materiality:    "medium",
			Basis:          "confirmed",
			Claim:          "final answer overstates verification",
			EvidenceSource: "lead_final",
			Evidence:       "final answer says done but verification is thin",
		}},
	})
	feedback = criticLeadFeedbackMessage(concern)
	if !strings.Contains(feedback, "Tighten the final answer") || !strings.Contains(feedback, "Top material findings") {
		t.Fatalf("material concern feedback = %q", feedback)
	}
}

func TestCriticLeadFeedbackIncludesMultipleStructuredFindings(t *testing.T) {
	review := normalizeCriticReviewForRouting(criticReviewPayload{
		Status:          "needs_followup",
		LeadInstruction: "Fix the visible game gaps before final_response.",
		Findings: []criticReviewFinding{
			{
				Severity:          "high",
				Materiality:       "high",
				Basis:             "visual_or_product_gap",
				Claim:             "water is not visible",
				EvidenceSource:    "verification",
				Evidence:          "vision analysis says no water effects are visible",
				UserImpact:        "the user requested a boardwalk with water effects",
				SuggestedFollowup: "make water visible and rerun one focused screenshot check",
			},
			{
				Severity:          "medium",
				Materiality:       "high",
				Basis:             "suspected",
				Claim:             "HUD rendering may not be readable",
				EvidenceSource:    "file_snapshot",
				Evidence:          "HUD uses colored quads instead of readable text",
				UserImpact:        "the user requested controls and scoring on screen",
				SuggestedFollowup: "inspect HUD rendering and add readable text if needed",
			},
		},
	})

	feedback := criticLeadFeedbackMessage(review)
	for _, want := range []string{
		"Top material findings",
		"1. [high/high, visual_or_product_gap] Issue: water is not visible",
		"Suggested fix: make water visible and rerun one focused screenshot check",
		"2. [medium/high, suspected] Issue: HUD rendering may not be readable",
		"Treat suspected code findings as hypotheses",
	} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("feedback missing %q:\n%s", want, feedback)
		}
	}
}

func TestTraceCriticReviewAndConsultDoNotSetCompletionTokenCap(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		requestCount++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
			if _, ok := body[field]; ok {
				t.Fatalf("critic request sent %s cap in body: %#v", field, body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "deepseek/deepseek-v4-pro",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": `{"status":"clean","confidence":0.9,"summary":"ok","findings":[],"lead_instruction":"","human_prompt":"","proposed_user_message":""}`,
				},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()

	client, err := modeladapter.NewOpenRouterClient(modeladapter.OpenRouterConfig{
		APIKey:  "key",
		BaseURL: server.URL,
		Model:   "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := traceCritic{provider: "openrouter", client: client}

	review := reviewer.ReviewAttempt(context.Background(), criticReviewPacket{
		SessionID:    "sess-1",
		UserRequest:  "finish the task",
		FinalOutcome: "success",
		FinalSummary: "done",
		ContextMode:  "full",
	}, 1, "")
	if review.Err != nil {
		t.Fatalf("ReviewAttempt() error = %v", review.Err)
	}
	consult := reviewer.ConsultAttempt(context.Background(), criticConsultPacket{
		SessionID: "sess-1",
		Kind:      "plan",
		Question:  "Does this plan look sound?",
	}, 1, "")
	if consult.Err != nil {
		t.Fatalf("ConsultAttempt() error = %v", consult.Err)
	}
	if requestCount != 2 {
		t.Fatalf("requests = %d, want 2", requestCount)
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

func TestBuildCriticReviewPacketForRunnerUsesProducedChangeEvidence(t *testing.T) {
	root := t.TempDir()
	workspace, err := policy.NewWorkspace(root, policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := script.Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "create a small README",
		Patch:     tools.PatchApplier{Workspace: workspace},
		Files:     tools.FileTools{Workspace: workspace},
	}
	result, err := runner.RunTool(context.Background(), script.Action{
		Type: "tool_call",
		Tool: "create_file",
		Args: json.RawMessage(`{"path":"README.md","content":"# Demo\n\nhello world\n"}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("create_file result = %#v err=%v", result, err)
	}
	messages := make([]modeladapter.Message, 20)
	for i := range messages {
		messages[i] = modeladapter.Message{Role: "assistant", Content: "trace message"}
	}
	packet := buildCriticReviewPacketForRunner(runner, script.Action{
		Outcome:      "completed",
		Summary:      "Created README.md.",
		FilesChanged: []string{"README.md"},
	}, messages, false)

	if packet.ChangeReview == nil {
		t.Fatalf("ChangeReview is nil")
	}
	if len(packet.Messages) != 12 {
		t.Fatalf("messages = %d, want compact 12", len(packet.Messages))
	}
	if got := packet.Metadata["review_focus"]; got != "produced_change" {
		t.Fatalf("review_focus = %#v, want produced_change", got)
	}
	if len(packet.ChangeReview.Files) != 1 || packet.ChangeReview.Files[0].Path != "README.md" {
		t.Fatalf("change files = %#v", packet.ChangeReview.Files)
	}
	if snapshot := packet.ChangeReview.Files[0].Snapshot; !strings.Contains(snapshot, "hello world") || !strings.Contains(snapshot, "sha256:") {
		t.Fatalf("snapshot missing file evidence:\n%s", snapshot)
	}
	if len(packet.ChangeReview.PatchSummaries) != 1 || packet.ChangeReview.PatchSummaries[0].TotalAddedLines == 0 {
		t.Fatalf("patch summaries = %#v", packet.ChangeReview.PatchSummaries)
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
