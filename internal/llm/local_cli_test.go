package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestCodexExecRunnerCachesIdenticalRequests(t *testing.T) {
	tmp := t.TempDir()
	countFile := filepath.Join(tmp, "codex-count.txt")
	scriptPath := filepath.Join(tmp, "codex")
	writeRunnerScript(t, scriptPath, strings.Join([]string{
		"#!/bin/sh",
		"count=0",
		"if [ -f " + shellQuote(countFile) + " ]; then count=$(cat " + shellQuote(countFile) + "); fi",
		"count=$((count+1))",
		"printf '%s' \"$count\" > " + shellQuote(countFile),
		"printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"message\\\":\\\"hello\\\"}\"}}'",
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3,\"total_tokens\":15}}'",
	}, "\n"))

	runner := NewCodexExecRunner(2*time.Second, nil)
	runner.command = scriptPath

	req := JSONSchemaRequest{
		Model:           "gpt-5.4-mini",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "demo",
		Schema:          map[string]any{"type": "object"},
		ReasoningEffort: "low",
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if first.OutputText != second.OutputText {
		t.Fatalf("cached output = %q, want %q", second.OutputText, first.OutputText)
	}
	countRaw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if strings.TrimSpace(string(countRaw)) != "1" {
		t.Fatalf("runner command count = %q, want 1 cached invocation", strings.TrimSpace(string(countRaw)))
	}
}

func TestOpenCodeRunRunnerCachesIdenticalRequests(t *testing.T) {
	tmp := t.TempDir()
	countFile := filepath.Join(tmp, "opencode-count.txt")
	scriptPath := filepath.Join(tmp, "opencode")
	writeRunnerScript(t, scriptPath, strings.Join([]string{
		"#!/bin/sh",
		"count=0",
		"if [ -f " + shellQuote(countFile) + " ]; then count=$(cat " + shellQuote(countFile) + "); fi",
		"count=$((count+1))",
		"printf '%s' \"$count\" > " + shellQuote(countFile),
		"printf '%s\\n' '{\"type\":\"text\",\"part\":{\"type\":\"text\",\"text\":\"{\\\"message\\\":\\\"hello\\\"}\"}}'",
		"printf '%s\\n' '{\"type\":\"step_finish\",\"part\":{\"type\":\"step-finish\",\"tokens\":{\"total\":15,\"input\":12,\"output\":3}}}'",
	}, "\n"))

	runner := NewOpenCodeRunRunner(2*time.Second, nil)
	runner.command = scriptPath

	req := JSONSchemaRequest{
		Model:           "gpt-5.4-mini",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "demo",
		Schema:          map[string]any{"type": "object"},
		ReasoningEffort: "low",
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if first.OutputText != second.OutputText {
		t.Fatalf("cached output = %q, want %q", second.OutputText, first.OutputText)
	}
	countRaw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if strings.TrimSpace(string(countRaw)) != "1" {
		t.Fatalf("runner command count = %q, want 1 cached invocation", strings.TrimSpace(string(countRaw)))
	}
}

func writeRunnerScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}
