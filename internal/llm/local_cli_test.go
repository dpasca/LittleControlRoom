package llm

import "testing"

func TestParseCodexExecJSONL(t *testing.T) {
	raw := "" +
		"{\"type\":\"thread.started\",\"thread_id\":\"thr_123\"}\n" +
		"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"message\\\":\\\"hello\\\"}\"}}\n" +
		"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1200,\"cached_input_tokens\":100,\"output_tokens\":30}}\n"

	got, err := parseCodexExecJSONL(raw, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("parseCodexExecJSONL() error = %v", err)
	}
	if got.OutputText != "{\"message\":\"hello\"}" {
		t.Fatalf("output text = %q, want hello json", got.OutputText)
	}
	if got.Usage.InputTokens != 1200 || got.Usage.OutputTokens != 30 {
		t.Fatalf("usage = %#v, want input/output tokens", got.Usage)
	}
	if got.Usage.TotalTokens != 1230 {
		t.Fatalf("total tokens = %d, want 1230", got.Usage.TotalTokens)
	}
	if got.Model != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want requested model", got.Model)
	}
}

func TestParseOpenCodeRunJSONL(t *testing.T) {
	raw := "" +
		"{\"type\":\"step_start\",\"part\":{\"type\":\"step-start\"}}\n" +
		"{\"type\":\"text\",\"part\":{\"type\":\"text\",\"text\":\"{\\\"message\\\":\\\"hello\\\"}\"}}\n" +
		"{\"type\":\"step_finish\",\"part\":{\"type\":\"step-finish\",\"tokens\":{\"total\":120,\"input\":90,\"output\":30,\"reasoning\":12,\"cache\":{\"read\":4}}}}\n"

	got, err := parseOpenCodeRunJSONL(raw, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("parseOpenCodeRunJSONL() error = %v", err)
	}
	if got.OutputText != "{\"message\":\"hello\"}" {
		t.Fatalf("output text = %q, want hello json", got.OutputText)
	}
	if got.Usage.TotalTokens != 120 || got.Usage.ReasoningTokens != 12 || got.Usage.CachedInputTokens != 4 {
		t.Fatalf("usage = %#v, want parsed step_finish tokens", got.Usage)
	}
	if got.Model != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want requested model", got.Model)
	}
}
