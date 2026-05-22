package script

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
	lcrmodel "lcroom/internal/model"
)

func TestRunnerExecutesScriptedMiniSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(root, ".agents", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: Demo skill\n---\n# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := skillcatalog.Discover(context.Background(), skillcatalog.Options{WorkspaceRoot: w.Root})
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "patch readme",
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Patch:     tools.PatchApplier{Workspace: w},
		Files:     tools.FileTools{Workspace: w},
		Skills:    catalog,
	}
	actions := []Action{
		{Type: "tool_call", Tool: "repo_overview", Args: raw(`{"path":".","max_files":10}`)},
		{Type: "tool_call", Tool: "list_files", Args: raw(`{"path":".","glob":"*.md","max_entries":10}`)},
		{Type: "tool_call", Tool: "read_file", Args: raw(`{"path":"README.md","limit":20}`)},
		{Type: "tool_call", Tool: "search", Args: raw(`{"query":"old","path":".","file_glob":"*.md","max_matches":10}`)},
		{Type: "tool_call", Tool: "file_outline", Args: raw(`{"path":"README.md"}`)},
		{Type: "tool_call", Tool: "module_outline", Args: raw(`{"path":".","file_glob":"*.md","max_files":10}`)},
		{Type: "tool_call", Tool: "load_skill", Args: raw(`{"name":"demo"}`)},
		{Type: "tool_call", Tool: "run_command", Args: raw(`{"argv":["cat","README.md"],"timeout_ms":1000}`)},
		{Type: "tool_call", Tool: "update_plan", Args: raw(`{"items":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"}]}`)},
		{Type: "tool_call", Tool: "apply_patch", Args: raw(`{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch\n"}`)},
		{Type: "final_response", Summary: "done", FilesChanged: []string{"README.md"}, Verification: []string{"script"}},
	}
	if err := runner.Run(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("README = %q", data)
	}
	text := stream.String()
	for _, eventType := range []string{"user_message", "tool_call", "tool_result", "skill_loaded", "plan_update", "files_touched", "patch_diff_summary", "verification_summary", "turn_complete"} {
		if !strings.Contains(text, `"type":"`+eventType+`"`) {
			t.Fatalf("stream missing %s:\n%s", eventType, text)
		}
	}
	if !strings.Contains(text, `"verification_status":"reported_only"`) || !strings.Contains(text, `"summary":"patch diff summary:`) {
		t.Fatalf("stream missing verification status or patch summary:\n%s", text)
	}
}

func TestRunnerRecordsActualVerificationCheck(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "verify",
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
	}
	actions := []Action{
		{Type: "tool_call", Tool: "run_command", Args: raw(`{"argv":["cat","README.md"],"timeout_ms":1000,"purpose":"verify"}`)},
		{Type: "final_response", Summary: "verified", Verification: []string{"cat README.md"}},
	}
	if err := runner.Run(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	text := stream.String()
	for _, want := range []string{`"type":"verification_check"`, `"command":"cat README.md"`, `"status":"passed"`, `"verification_status":"verified"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerRefinesOversizedSearchWithIntent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("alpha target\nbeta target\ngamma target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	refiner := &fakeSearchRefiner{}
	runner := Runner{
		Session:              writer,
		SessionID:            sessionID,
		Files:                tools.FileTools{Workspace: w},
		SearchRefiner:        refiner,
		SearchRefineMinBytes: 1,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "search",
		Args: raw(`{"query":"target","path":".","max_matches":10,"intent":"find app entry points"}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !strings.Contains(result.Output, "search_refined: true") || !strings.Contains(result.Output, "app.go:1") {
		t.Fatalf("refined output =\n%s", result.Output)
	}
	if refiner.request.Intent != "find app entry points" || refiner.request.Query != "target" {
		t.Fatalf("refiner request = %#v", refiner.request)
	}
	if !strings.Contains(refiner.request.SearchOutput, "output_mode: compact") || strings.Contains(refiner.request.SearchOutput, "> 1 |") {
		t.Fatalf("refiner should receive compact search output:\n%s", refiner.request.SearchOutput)
	}
	text := stream.String()
	for _, want := range []string{`"type":"search_refine"`, `"phase":"search_refine"`, `"type":"search_refine_result"`, `"model":"fake-cheap"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

type fakeSearchRefiner struct {
	request SearchRefineRequest
}

func (f *fakeSearchRefiner) RefineSearch(_ context.Context, request SearchRefineRequest) (SearchRefineResult, error) {
	f.request = request
	return SearchRefineResult{
		Output:       "search_refined: true\nlikely_relevant:\n- app.go:1 confidence=high reason=entry point\n",
		Provider:     "fake",
		Model:        "fake-cheap",
		UsageSummary: lcrmodel.LLMUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

type fakeApprovalBroker struct {
	decisions []ApprovalDecision
	requests  []CommandApprovalRequest
	calls     int
}

func (f *fakeApprovalBroker) RequestCommandApproval(_ context.Context, request CommandApprovalRequest) (ApprovalDecision, error) {
	f.calls++
	f.requests = append(f.requests, request)
	if len(f.decisions) == 0 {
		return DecisionCancel, nil
	}
	decision := f.decisions[0]
	f.decisions = f.decisions[1:]
	return decision, nil
}

type fakeProcessBroker struct {
	requests []ProcessRequest
	result   tools.ToolResult
	err      error
}

func (f *fakeProcessBroker) RequestProcess(_ context.Context, request ProcessRequest) (tools.ToolResult, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return tools.ToolResult{}, f.err
	}
	if f.result.Output == "" {
		f.result.Output = "started"
	}
	f.result.Success = true
	return f.result, nil
}

func TestFinalVerificationStatusUsesLatestPassingOutcome(t *testing.T) {
	status, message := finalVerificationStatus(nil, []string{"go test ./... - PASS"}, []tools.VerificationCheck{
		{Command: "go test ./...", Status: tools.VerificationStatusFailed, ExitCode: 1},
		{Command: "go test ./...", Status: tools.VerificationStatusPassed, Success: true},
	})
	if status != "verified" {
		t.Fatalf("status = %q, want verified; message=%s", status, message)
	}
	if strings.Contains(message, "failed") || !strings.Contains(message, "go test ./...") {
		t.Fatalf("message = %q", message)
	}
}

func TestFinalVerificationStatusUsesLatestReportedCWDOutcome(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	status, message := finalVerificationStatus(nil, []string{"pnpm run lint passed"}, []tools.VerificationCheck{
		{Command: "pnpm run lint", CWD: root, Status: tools.VerificationStatusFailed, ExitCode: 1},
		{Command: "pnpm run lint", CWD: frontend, Status: tools.VerificationStatusPassed, Success: true},
	})
	if status != "verified" {
		t.Fatalf("status = %q, want verified; message=%s", status, message)
	}
	if !strings.Contains(message, "pnpm run lint") || !strings.Contains(message, frontend) || strings.Contains(message, "(failed)") {
		t.Fatalf("message = %q", message)
	}
}

func TestFinalVerificationStatusNormalizesShellCDCommand(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	status, message := finalVerificationStatus(nil, []string{"pnpm run build"}, []tools.VerificationCheck{
		{Command: "pnpm run build", CWD: root, Status: tools.VerificationStatusFailed, ExitCode: 1},
		{Command: "cd " + frontend + " && pwd && ls package.json && pnpm run build", Status: tools.VerificationStatusPassed, Success: true},
	})
	if status != "verified" {
		t.Fatalf("status = %q, want verified; message=%s", status, message)
	}
	if !strings.Contains(message, "pnpm run build") || !strings.Contains(message, frontend) || strings.Contains(message, "(failed)") {
		t.Fatalf("message = %q", message)
	}
}

func TestFinalVerificationStatusUsesReportedReplacementCommand(t *testing.T) {
	status, message := finalVerificationStatus(nil, []string{"python3 -m unittest: OK"}, []tools.VerificationCheck{
		{Command: "python -m unittest", Status: tools.VerificationStatusFailed, ExitCode: 127, Error: "executable not found"},
		{Command: "python3 -m unittest", Status: tools.VerificationStatusPassed, Success: true},
	})
	if status != "verified" {
		t.Fatalf("status = %q, want verified; message=%s", status, message)
	}
	if !strings.Contains(message, "python3 -m unittest") || strings.Contains(message, "python -m unittest") {
		t.Fatalf("message = %q", message)
	}
}

func TestFinalVerificationStatusKeepsLatestFailure(t *testing.T) {
	status, message := finalVerificationStatus(nil, []string{"go test ./..."}, []tools.VerificationCheck{
		{Command: "go test ./...", Status: tools.VerificationStatusPassed, Success: true},
		{Command: "go test ./...", Status: tools.VerificationStatusFailed, ExitCode: 1},
	})
	if status != "failed" {
		t.Fatalf("status = %q, want failed; message=%s", status, message)
	}
	if !strings.Contains(message, "go test ./... (failed)") {
		t.Fatalf("message = %q", message)
	}
}

func TestVerificationFeedbackForFailedCheck(t *testing.T) {
	result := tools.ToolResult{
		Success:  false,
		Command:  "go test ./...",
		Purpose:  tools.CommandPurposeVerify,
		ExitCode: 1,
		Error:    "exit status 1",
	}
	feedback, ok := VerificationFeedbackForResult(result)
	if !ok {
		t.Fatal("VerificationFeedbackForResult returned ok=false, want feedback")
	}
	if feedback.Status != tools.VerificationStatusFailed || !strings.Contains(feedback.Message, "go test ./...") || !strings.Contains(feedback.Message, "rerun a purpose=verify check") {
		t.Fatalf("feedback = %#v", feedback)
	}
}

func TestRunnerVerificationFeedbackSuggestsPackageSubdirCWD(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	if err := os.Mkdir(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Command: tools.CommandRunner{Workspace: w}}
	feedback, ok := runner.VerificationFeedbackForResult(tools.ToolResult{
		Success:  false,
		Command:  "pnpm run build",
		Argv:     []string{"pnpm", "run", "build"},
		CWD:      root,
		Purpose:  tools.CommandPurposeVerify,
		ExitCode: 1,
		Output:   "ERR_PNPM_NO_SCRIPT Missing script: build\n",
		Error:    "exit status 1",
	})
	if !ok {
		t.Fatal("VerificationFeedbackForResult returned ok=false, want feedback")
	}
	for _, want := range []string{`cwd set to "frontend"`, "frontend/package.json", `"build"`} {
		if !strings.Contains(feedback.Message, want) {
			t.Fatalf("feedback missing %q: %s", want, feedback.Message)
		}
	}
}

func TestPatchFeedbackForFailedPatch(t *testing.T) {
	result := tools.ToolResult{
		Success: false,
		Error:   "README.md: hunk context not found; re-read exact current lines",
		PatchFailure: &tools.PatchFailure{
			Stage:   "apply",
			Path:    "README.md",
			Message: "README.md: hunk context not found",
			Hint:    `call read_file {"path":"README.md","offset":1,"limit":2} to refresh exact current lines`,
			SuggestedReads: []tools.ReadSuggestion{{
				Path:   "README.md",
				Offset: 1,
				Limit:  2,
				Reason: "refresh current context for failed patch hunk 1",
			}},
		},
	}
	feedback, ok := PatchFeedbackForResult(result)
	if !ok {
		t.Fatal("PatchFeedbackForResult returned ok=false, want feedback")
	}
	if feedback.Stage != "apply" || feedback.Path != "README.md" || !strings.Contains(feedback.Message, `read_file {"path":"README.md","offset":1,"limit":2}`) || len(feedback.SuggestedReads) != 1 {
		t.Fatalf("feedback = %#v", feedback)
	}
}

func TestPatchRetryGuidanceEscalatesRepeatedPatchFeedback(t *testing.T) {
	feedback := PatchFeedback{
		Stage:   "apply",
		Path:    "README.md",
		Message: "Patch feedback: README.md failed during apply: hunk context not found",
		SuggestedReads: []tools.ReadSuggestion{{
			Path:   "README.md",
			Offset: 1,
			Limit:  40,
		}},
	}
	guidance := PatchRetryGuidance(feedback, 2)
	for _, want := range []string{
		"same patch feedback has repeated 2 times",
		`read_file {"path":"README.md","offset":1,"limit":40}`,
		"replace_text",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance missing %q: %s", want, guidance)
		}
	}
	if got := PatchRetryGuidance(feedback, 1); got != "" {
		t.Fatalf("guidance for first feedback = %q, want empty", got)
	}
}

func TestRunnerFinalVerificationFeedbackAfterChangedFiles(t *testing.T) {
	runner := Runner{}
	feedback, ok := runner.VerificationFeedbackForFinal(Action{
		Type:         "final_response",
		Summary:      "done",
		FilesChanged: []string{"README.md"},
		Verification: []string{"go test ./..."},
	})
	if !ok {
		t.Fatal("VerificationFeedbackForFinal returned ok=false, want feedback")
	}
	if feedback.Status != "reported_only" || !strings.Contains(feedback.Message, "no run_command check marked purpose=verify") {
		t.Fatalf("feedback = %#v", feedback)
	}
}

func TestRunnerEmitsPermissionDeniedEvent(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "try denied patch",
		Patch:     tools.PatchApplier{Workspace: w},
	}
	actions := []Action{
		{Type: "tool_call", Tool: "apply_patch", Args: raw(`{"patch":"*** Begin Patch\n*** Add File: denied.txt\n+nope\n*** End Patch\n"}`)},
	}
	if err := runner.Run(context.Background(), actions); err == nil {
		t.Fatal("Run succeeded, want denied tool failure")
	}
	text := stream.String()
	for _, want := range []string{`"type":"permission_denied"`, `"tool":"apply_patch"`, `"denied":true`, `"type":"turn_aborted"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerApprovedCommandRunsOnceAtMediumAutonomy(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAccept}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf approved","timeout_ms":1000}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !result.Success || result.Denied || !strings.Contains(result.Output, "approved") {
		t.Fatalf("result = %#v", result)
	}
	if approvals.calls != 1 || approvals.requests[0].Command != "printf approved" || approvals.requests[0].CWD != w.Root {
		t.Fatalf("approval requests = %#v calls=%d", approvals.requests, approvals.calls)
	}
	if strings.Contains(stream.String(), `"type":"permission_denied"`) {
		t.Fatalf("approved command should not emit permission_denied:\n%s", stream.String())
	}
}

func TestRunnerStartProcessRequiresApprovalAndUsesProcessBroker(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAccept}}
	processes := &fakeProcessBroker{result: tools.ToolResult{Output: "running; pid 42"}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
		Processes: processes,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "start_process",
		Args: raw(`{"command":"pnpm dev","cwd":"frontend","name":"frontend"}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !result.Success || result.Output != "running; pid 42" {
		t.Fatalf("result = %#v", result)
	}
	if approvals.calls != 1 || approvals.requests[0].Tool != "start_process" || approvals.requests[0].Command != "pnpm dev" {
		t.Fatalf("approval requests = %#v calls=%d", approvals.requests, approvals.calls)
	}
	if len(processes.requests) != 1 {
		t.Fatalf("process requests = %#v", processes.requests)
	}
	request := processes.requests[0]
	if request.Action != ProcessActionStart || request.Command != "pnpm dev" || request.CWD != "frontend" || request.Name != "frontend" || request.SessionID != sessionID {
		t.Fatalf("process request = %#v", request)
	}
}

func TestRunnerProcessApprovalGrantDoesNotReuseRunCommandGrant(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAcceptForSession}}
	processes := &fakeProcessBroker{result: tools.ToolResult{Output: "running"}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
		Processes: processes,
	}
	if _, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf ok","timeout_ms":1000}`),
	}); err != nil {
		t.Fatalf("run_command RunTool() error = %v", err)
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "start_process",
		Args: raw(`{"command":"printf ok"}`),
	})
	if err == nil {
		t.Fatalf("start_process RunTool() error = nil, want denial; result=%#v", result)
	}
	if !result.Denied || len(processes.requests) != 0 {
		t.Fatalf("result = %#v process requests=%#v", result, processes.requests)
	}
	if approvals.calls != 2 || approvals.requests[1].Tool != "start_process" {
		t.Fatalf("approval calls=%d requests=%#v", approvals.calls, approvals.requests)
	}
}

func TestRunnerStopProcessDoesNotRequireApproval(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionCancel}}
	processes := &fakeProcessBroker{result: tools.ToolResult{Output: "stopped"}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
		Processes: processes,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "stop_process",
		Args: raw(`{"process_id":"rt_2"}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !result.Success || result.Output != "stopped" {
		t.Fatalf("result = %#v", result)
	}
	if approvals.calls != 0 {
		t.Fatalf("stop_process should not request approval, got %d calls", approvals.calls)
	}
	if len(processes.requests) != 1 || processes.requests[0].Action != ProcessActionStop || processes.requests[0].ProcessID != "rt_2" {
		t.Fatalf("process requests = %#v", processes.requests)
	}
}

func TestRunnerRejectsUnknownToolArgument(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Files:     tools.FileTools{Workspace: w},
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "read_file",
		Args: raw(`{"path":"README.md","surprise":true}`),
	})
	if err == nil {
		t.Fatal("RunTool succeeded, want invalid argument failure")
	}
	if result.Success || !strings.Contains(result.Error, `unknown field "surprise"`) {
		t.Fatalf("result = %#v", result)
	}
	if text := stream.String(); !strings.Contains(text, `"type":"tool_result"`) || !strings.Contains(text, "invalid read_file arguments") {
		t.Fatalf("stream missing invalid argument result:\n%s", text)
	}
}

func TestDecodeFinalResponseArgsRejectsUnknownField(t *testing.T) {
	_, err := DecodeFinalResponseArgs(raw(`{"summary":"done","files_changed":[],"verification":[],"extra":true}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "extra"`) {
		t.Fatalf("DecodeFinalResponseArgs() error = %v, want unknown field", err)
	}
}

func TestRunnerRunCommandHonorsCWDArgument(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	if err := os.Mkdir(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	wantCWD := filepath.Join(w.Root, "frontend")
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"argv":["pwd"],"cwd":"frontend","timeout_ms":1000}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !result.Success || result.CWD != wantCWD || !strings.Contains(result.Output, wantCWD) {
		t.Fatalf("result = %#v, want successful command in %s", result, wantCWD)
	}
	if !strings.Contains(stream.String(), `"cwd":"`+wantCWD+`"`) {
		t.Fatalf("stream did not record cwd %q:\n%s", wantCWD, stream.String())
	}
}

func TestRunnerApprovalRequestUsesRequestedCommandCWD(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(w.Root)
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAccept}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"argv":["pwd"],"cwd":"..","timeout_ms":1000}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !result.Success || result.CWD != parent || !strings.Contains(result.Output, parent) {
		t.Fatalf("result = %#v, want approved command in %s", result, parent)
	}
	if approvals.calls != 1 || approvals.requests[0].CWD != parent || !strings.Contains(approvals.requests[0].Scope, "this exact command") {
		t.Fatalf("approval requests = %#v calls=%d, want cwd %q and exact scope", approvals.requests, approvals.calls, parent)
	}
}

func TestRunnerApprovedCommandForSessionScopesToExactCommand(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAcceptForSession}}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: approvals,
	}
	if result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf scoped","timeout_ms":1000}`),
	}); err != nil || !result.Success {
		t.Fatalf("first RunTool() = %#v, %v", result, err)
	}
	if result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf scoped","timeout_ms":1000}`),
	}); err != nil || !result.Success {
		t.Fatalf("second RunTool() = %#v, %v", result, err)
	}
	if result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf other","timeout_ms":1000}`),
	}); err == nil || !result.Denied {
		t.Fatalf("third RunTool() = %#v, %v; want denied outside approved scope", result, err)
	}
	if approvals.calls != 2 {
		t.Fatalf("approval should be requested for first and third commands, got %d calls", approvals.calls)
	}
	if runner.Command.Workspace.Auto != policy.AutonomyLow {
		t.Fatalf("runner command autonomy = %q, want low", runner.Command.Workspace.Auto)
	}
	if approvals.requests[0].Scope == "" || !strings.Contains(approvals.requests[0].Scope, "this exact command") {
		t.Fatalf("approval scope = %#v, want exact command scope", approvals.requests)
	}
}

func TestCommandApprovalGrantMatchesPackageDependencyFamily(t *testing.T) {
	root := t.TempDir()
	grant := newCommandApprovalGrant(root, tools.CommandSpec{Argv: []string{"pnpm", "install"}, CWD: "frontend"}, "run_command")
	if !grant.Matches(root, tools.CommandSpec{Argv: []string{"pnpm", "add", "vite"}, CWD: "frontend"}, "run_command") {
		t.Fatalf("package dependency grant did not match same manager/cwd family: %#v", grant)
	}
	if grant.Matches(root, tools.CommandSpec{Argv: []string{"pnpm", "add", "vite"}, CWD: "backend"}, "run_command") {
		t.Fatalf("package dependency grant matched a different cwd")
	}
	if grant.Matches(root, tools.CommandSpec{Argv: []string{"npm", "install"}, CWD: "frontend"}, "run_command") {
		t.Fatalf("package dependency grant matched a different manager")
	}
	if grant.Matches(root, tools.CommandSpec{Argv: []string{"pnpm", "add", "vite"}, CWD: "frontend"}, "start_process") {
		t.Fatalf("package dependency grant matched a different tool")
	}
	if !strings.Contains(grant.ScopeText(), "pnpm dependency commands") {
		t.Fatalf("scope text = %q", grant.ScopeText())
	}
}

func TestRunnerDeclinedCommandApprovalKeepsDeniedResult(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Command:   tools.CommandRunner{Workspace: w, ArtifactDir: t.TempDir()},
		Approvals: &fakeApprovalBroker{
			decisions: []ApprovalDecision{DecisionDecline},
		},
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"printf denied","timeout_ms":1000}`),
	})
	if err == nil {
		t.Fatal("RunTool succeeded, want denied command error")
	}
	if !result.Denied || result.Success {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(stream.String(), `"type":"permission_denied"`) {
		t.Fatalf("stream missing permission_denied:\n%s", stream.String())
	}
}

func TestRunnerFinalMarksMissingVerificationAfterChanges(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{Session: writer, SessionID: sessionID}
	if err := runner.Final(Action{
		Type:         "final_response",
		Summary:      "changed without checks",
		FilesChanged: []string{"README.md"},
	}); err != nil {
		t.Fatal(err)
	}
	text := stream.String()
	for _, want := range []string{`"type":"verification_summary"`, `"status":"missing_after_changes"`, `"verification_status":"missing_after_changes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func raw(value string) json.RawMessage {
	return json.RawMessage(value)
}
