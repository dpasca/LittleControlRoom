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
{"type":"context_compacted","turn":2,"threshold":120000,"stats":{"before_chars":140000,"after_chars":60000}}
{"type":"tool_call","tool":"read_file","args":{"path":"a.go","offset":200}}
{"type":"tool_result","tool":"read_file","result":{"success":true,"output":"file: a.go\nlines: 200-300\n\n200 | func A() {}\n300 | func B() {}\n"}}
{"type":"tool_call","tool":"search","args":{"query":"func","path":"."}}
{"type":"tool_result","tool":"search","result":{"success":true,"output":"query: func\nmatches: 1\n\na.go:200: func A() {}\n"}}
{"type":"continuation","parent_session_id":"lca_previous","root_session_id":"lca_previous","chain_depth":1,"continuation_reason":"continue_from"}
{"type":"resume_context","source_session_id":"lca_previous","summary":"source lca_previous; summary: previous work"}
{"type":"permission_denied","tool":"apply_patch","reason":"apply_patch denied with --auto off"}
{"type":"patch_diff_summary","summary":"patch diff summary:\n- README.md: update +1 -1\ntotal: +1 -1"}
{"type":"patch_feedback","stage":"apply","path":"README.md","message":"Patch feedback: README.md failed during apply."}
{"type":"repair_feedback_suppressed","kind":"patch","message":"Patch feedback: README.md failed during apply.","count":2}
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
	if summary.ToolSuccesses["read_file"] != 2 || summary.ToolSuccesses["search"] != 1 || summary.TraceQuality.ToolFailures != 0 {
		t.Fatalf("tool result quality = successes %#v failures %#v quality %#v", summary.ToolSuccesses, summary.ToolFailures, summary.TraceQuality)
	}
	if summary.ToolProfiles["generous"] != 1 {
		t.Fatalf("tool profiles = %#v", summary.ToolProfiles)
	}
	if summary.ContextProfiles["large"] != 1 {
		t.Fatalf("context profiles = %#v", summary.ContextProfiles)
	}
	if summary.Continuations != 1 || summary.ResumeContexts != 1 || summary.PermissionDenials != 1 || summary.PatchDiffSummaries != 1 || summary.PatchFeedback != 1 || summary.RepairFeedbackSuppressed != 1 || summary.VerificationChecks != 1 || summary.VerificationFeedback != 1 || summary.VerificationStatuses["verified"] != 1 || summary.VerificationCheckStatuses["passed"] != 1 {
		t.Fatalf("trust trace metrics = continuations %d resumes %d denials %d patch summaries %d patch feedback %d suppressed %d verification checks %d feedback %d statuses %#v check statuses %#v", summary.Continuations, summary.ResumeContexts, summary.PermissionDenials, summary.PatchDiffSummaries, summary.PatchFeedback, summary.RepairFeedbackSuppressed, summary.VerificationChecks, summary.VerificationFeedback, summary.VerificationStatuses, summary.VerificationCheckStatuses)
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
	if summary.UncachedInputTokens != 175 || summary.ContextCompactions != 1 {
		t.Fatalf("context waste = uncached %d compactions %d", summary.UncachedInputTokens, summary.ContextCompactions)
	}
	if summary.MaxInputTokens != 150 || summary.MaxTotalTokens != 170 {
		t.Fatalf("max tokens = input %d total %d", summary.MaxInputTokens, summary.MaxTotalTokens)
	}
	if summary.TraceQuality.Score != 83 || summary.TraceQuality.Grade != "good" || summary.TraceQuality.RepairEvents != 4 || summary.TraceQuality.VerificationRate != 1 {
		t.Fatalf("trace quality = %#v", summary.TraceQuality)
	}
	if len(summary.LargestToolOutputs) == 0 || summary.LargestToolOutputs[0].Tool != "read_file" {
		t.Fatalf("largest outputs = %#v", summary.LargestToolOutputs)
	}
	if summary.WasteReport.UncachedInputTokens != 175 || summary.WasteReport.ContextCompactions != 1 || len(summary.WasteReport.LargestToolOutputs) == 0 || summary.WasteReport.ReadFileOverlappingLines != 1 {
		t.Fatalf("waste report = %#v", summary.WasteReport)
	}
}

func TestAnalyzeFilesTraceQualityFlagsFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session_meta","id":"lca_failure","cwd":"/repo"}
{"type":"tool_call","tool":"read_file","args":{"path":"missing.go"}}
{"type":"tool_result","tool":"read_file","result":{"success":false,"error":"missing.go: no such file"}}
{"type":"verification_summary","status":"missing_after_changes"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := AnalyzeFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ToolFailures["read_file"] != 1 || summary.TraceQuality.ToolFailures != 1 {
		t.Fatalf("tool failures = %#v quality=%#v", summary.ToolFailures, summary.TraceQuality)
	}
	if summary.TraceQuality.Grade != "mixed" {
		t.Fatalf("trace quality grade = %s, want mixed: %#v", summary.TraceQuality.Grade, summary.TraceQuality)
	}
	if !traceQualityHasFinding(summary.TraceQuality, "tool_failures") || !traceQualityHasFinding(summary.TraceQuality, "no_verified_sessions") {
		t.Fatalf("trace quality findings = %#v", summary.TraceQuality.Findings)
	}
}

func TestAnalyzeFilesTraceQualityFlagsProviderFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session_meta","id":"lca_provider_failure","cwd":"/repo"}
{"type":"provider_failure","provider":"openrouter","kind":"rate_limited","message":"HTTP 429: slow down","retryable":true,"retrying":true}
{"type":"provider_retry","provider":"openrouter","attempt":2,"delay_ms":250}
{"type":"provider_retry_succeeded","provider":"openrouter","attempt":2}
{"type":"verification_summary","status":"verified"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := AnalyzeFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProviderFailures["rate_limited"] != 1 || summary.ProviderRetries != 1 || summary.ProviderRetrySuccesses != 1 {
		t.Fatalf("provider metrics = failures %#v retries %d successes %d", summary.ProviderFailures, summary.ProviderRetries, summary.ProviderRetrySuccesses)
	}
	if summary.TraceQuality.ProviderFailures != 1 || summary.TraceQuality.ProviderRetries != 1 || !traceQualityHasFinding(summary.TraceQuality, "provider_failures") {
		t.Fatalf("trace quality provider signals = %#v", summary.TraceQuality)
	}
}

func TestAnalyzeFilesSummarizesTiming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session_meta","id":"lca_timing","timestamp":"2026-05-20T00:00:00Z"}
{"type":"user_message","session_id":"lca_timing","timestamp":"2026-05-20T00:00:01Z","message":"inspect"}
{"type":"model_response","session_id":"lca_timing","timestamp":"2026-05-20T00:00:06Z","model":"deepseek/test"}
{"type":"tool_call","session_id":"lca_timing","timestamp":"2026-05-20T00:00:06.5Z","tool":"list_files","args":{"path":"."}}
{"type":"tool_result","session_id":"lca_timing","timestamp":"2026-05-20T00:00:07.75Z","tool":"list_files","result":{"success":true,"output":"path: .\nentries: 0\n"}}
{"type":"model_response","session_id":"lca_timing","timestamp":"2026-05-20T00:00:10.25Z","model":"deepseek/test"}
{"type":"turn_complete","session_id":"lca_timing","timestamp":"2026-05-20T00:00:11Z","summary":"done"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := AnalyzeFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ObservedElapsedSeconds != 11 {
		t.Fatalf("observed elapsed = %v, want 11", summary.ObservedElapsedSeconds)
	}
	if summary.ModelResponseWaitSeconds != 7.5 || summary.MaxModelResponseWaitSeconds != 5 {
		t.Fatalf("model waits = total %v max %v, want 7.5/5", summary.ModelResponseWaitSeconds, summary.MaxModelResponseWaitSeconds)
	}
	if summary.ToolSeconds["list_files"] != 1.25 {
		t.Fatalf("tool seconds = %#v, want list_files 1.25", summary.ToolSeconds)
	}
	if len(summary.SlowestEventGaps) == 0 || summary.SlowestEventGaps[0].From != "user_message" || summary.SlowestEventGaps[0].To != "model_response" || summary.SlowestEventGaps[0].Seconds != 5 {
		t.Fatalf("slowest event gaps = %#v", summary.SlowestEventGaps)
	}
	if len(summary.SlowestToolRuns) == 0 || summary.SlowestToolRuns[0].Tool != "list_files" || summary.SlowestToolRuns[0].Seconds != 1.25 {
		t.Fatalf("slowest tool runs = %#v", summary.SlowestToolRuns)
	}
}

func traceQualityHasFinding(quality TraceQuality, code string) bool {
	for _, finding := range quality.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
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
