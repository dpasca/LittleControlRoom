package sessionmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const benchmarkSessionJSONL = `{"type":"session_meta","id":"lca_demo","cwd":"/repo","model":"deepseek/test"}
{"type":"tool_profile","profile":"generous"}
{"type":"context_profile","profile":"large"}
{"type":"model_response","model":"deepseek/test","usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":25},"completion_tokens":10,"total_tokens":110,"cost":0.001}}
{"type":"tool_call","tool":"read_file","args":{"path":"a.go"}}
{"type":"tool_result","tool":"read_file","result":{"success":true,"output":"file: a.go\ntotal_lines: 300\nhas_more: true\nnext_offset: 201\nlines: 1-200\n\n1 | package demo\n200 | func A() {}\n","truncated":true}}
{"type":"model_response","model":"deepseek/test","usage":{"prompt_tokens":150,"prompt_tokens_details":{"cached_tokens":50},"completion_tokens":20,"total_tokens":170,"cost":0.002}}
{"type":"tool_call","tool":"read_file","args":{"path":"a.go","offset":200}}
{"type":"tool_result","tool":"read_file","result":{"success":true,"output":"file: a.go\nlines: 200-300\n\n200 | func A() {}\n300 | func B() {}\n"}}
{"type":"tool_call","tool":"search","args":{"query":"func","path":"."}}
{"type":"tool_result","tool":"search","result":{"success":true,"output":"query: func\nmatches: 1\n\na.go:200: func A() {}\n"}}
{"type":"resume_context","source_session_id":"lca_previous","summary":"source lca_previous; summary: previous work"}
{"type":"permission_denied","tool":"apply_patch","reason":"apply_patch denied with --auto off"}
{"type":"patch_diff_summary","summary":"patch diff summary:\n- README.md: update +1 -1\ntotal: +1 -1"}
{"type":"patch_feedback","stage":"apply","path":"README.md","message":"Patch feedback: README.md failed during apply."}
{"type":"verification_check","command":"go test ./internal/lcagent/...","argv":["go","test","./internal/lcagent/..."],"purpose":"verify","status":"passed","success":true}
{"type":"verification_feedback","status":"failed","command":"go test ./internal/lcagent/...","message":"Verification feedback: go test ./internal/lcagent/... failed."}
{"type":"verification_summary","status":"verified","verification_checks":["go test ./internal/lcagent/..."]}
{"type":"turn_complete","summary":"done"}
`

func TestAnalyzeFilesSummarizesLCAgentSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(benchmarkSessionJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := AnalyzeFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sessions != 1 || len(summary.SessionIDs) != 1 || summary.SessionIDs[0] != "lca_demo" {
		t.Fatalf("session identity = %#v", summary)
	}
	if summary.ToolCalls["read_file"] != 2 || summary.ToolResults["search"] != 1 {
		t.Fatalf("tool counts = calls %#v results %#v", summary.ToolCalls, summary.ToolResults)
	}
	if summary.ToolProfiles["generous"] != 1 {
		t.Fatalf("tool profiles = %#v", summary.ToolProfiles)
	}
	if summary.ContextProfiles["large"] != 1 {
		t.Fatalf("context profiles = %#v", summary.ContextProfiles)
	}
	if summary.ResumeContexts != 1 || summary.PermissionDenials != 1 || summary.PatchDiffSummaries != 1 || summary.PatchFeedback != 1 || summary.VerificationChecks != 1 || summary.VerificationFeedback != 1 || summary.VerificationStatuses["verified"] != 1 || summary.VerificationCheckStatuses["passed"] != 1 {
		t.Fatalf("trust trace metrics = resumes %d denials %d patch summaries %d patch feedback %d verification checks %d feedback %d statuses %#v check statuses %#v", summary.ResumeContexts, summary.PermissionDenials, summary.PatchDiffSummaries, summary.PatchFeedback, summary.VerificationChecks, summary.VerificationFeedback, summary.VerificationStatuses, summary.VerificationCheckStatuses)
	}
	if summary.ReadFileCalls != 2 || summary.ReadFileLines != 301 {
		t.Fatalf("read stats = calls %d lines %d", summary.ReadFileCalls, summary.ReadFileLines)
	}
	if summary.ReadFileOverlappingCalls != 1 || summary.ReadFileOverlappingLines != 1 {
		t.Fatalf("overlap = calls %d lines %d", summary.ReadFileOverlappingCalls, summary.ReadFileOverlappingLines)
	}
	if summary.TokenUsage.InputTokens != 250 || summary.TokenUsage.CachedInputTokens != 75 || summary.TokenUsage.OutputTokens != 30 || summary.TokenUsage.TotalTokens != 280 {
		t.Fatalf("token usage = %+v", summary.TokenUsage)
	}
	if summary.MaxInputTokens != 150 || summary.MaxTotalTokens != 170 {
		t.Fatalf("max tokens = input %d total %d", summary.MaxInputTokens, summary.MaxTotalTokens)
	}
	if len(summary.LargestToolOutputs) == 0 || summary.LargestToolOutputs[0].Tool != "read_file" {
		t.Fatalf("largest outputs = %#v", summary.LargestToolOutputs)
	}
}

func BenchmarkAnalyzeSessionMetrics(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var summary Summary
		if err := Analyze(strings.NewReader(benchmarkSessionJSONL), "benchmark.jsonl", &summary); err != nil {
			b.Fatal(err)
		}
		summary.finish()
	}
}
