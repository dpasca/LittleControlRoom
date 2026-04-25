package boss

import (
	"context"
	"strings"
	"testing"

	"lcroom/internal/llm"
)

type fakeTextRunner struct {
	req  llm.TextRequest
	resp llm.TextResponse
	err  error
}

func (r *fakeTextRunner) RunText(_ context.Context, req llm.TextRequest) (llm.TextResponse, error) {
	r.req = req
	return r.resp, r.err
}

func TestAssistantReplyIncludesStateBriefAndRecentChat(t *testing.T) {
	t.Parallel()

	runner := &fakeTextRunner{
		resp: llm.TextResponse{Model: "gpt-test", OutputText: "Look at Alpha first."},
	}
	assistant := &Assistant{runner: runner, model: "gpt-test"}
	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		StateBrief: "Visible projects: 2.",
		Messages: []ChatMessage{
			{Role: "user", Content: "What should I do?"},
			{Role: "assistant", Content: "Let me check."},
			{Role: "user", Content: "Be brief."},
		},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.Content != "Look at Alpha first." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if runner.req.Model != "gpt-test" || runner.req.ReasoningEffort != bossAssistantReasoningEffort {
		t.Fatalf("unexpected request config: %+v", runner.req)
	}
	if len(runner.req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(runner.req.Messages))
	}
	if !strings.Contains(runner.req.Messages[0].Content, "Visible projects: 2.") {
		t.Fatalf("state brief missing from first message: %+v", runner.req.Messages[0])
	}
	if runner.req.Messages[2].Role != "assistant" {
		t.Fatalf("assistant history role = %q", runner.req.Messages[2].Role)
	}
	if !strings.Contains(runner.req.SystemText, "Little Control Room") {
		t.Fatalf("system prompt missing product context: %q", runner.req.SystemText)
	}
}

func TestAssistantReplyLimitsChatHistory(t *testing.T) {
	t.Parallel()

	runner := &fakeTextRunner{resp: llm.TextResponse{OutputText: "ok"}}
	assistant := &Assistant{runner: runner, model: "gpt-test"}
	var messages []ChatMessage
	for i := 0; i < 25; i++ {
		messages = append(messages, ChatMessage{Role: "user", Content: "message"})
	}
	if _, err := assistant.Reply(context.Background(), AssistantRequest{StateBrief: "state", Messages: messages}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(runner.req.Messages) != 17 {
		t.Fatalf("messages len = %d, want state brief plus 16 history messages", len(runner.req.Messages))
	}
}
