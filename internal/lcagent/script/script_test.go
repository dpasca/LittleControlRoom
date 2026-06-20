package script

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	for _, eventType := range []string{"user_message", "tool_call", "tool_result", "skill_loaded", "plan_update", "files_touched", "patch_diff_summary", "final_response_audit", "verification_summary", "turn_complete"} {
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

func TestRunnerEmitsFileEventsForDirectFileTools(t *testing.T) {
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
		Patch:     tools.PatchApplier{Workspace: w},
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "create_file",
		Args: raw(`{"path":"docs/new.txt","content":"hello\n"}`),
	})
	if err != nil {
		t.Fatalf("create_file failed: %v", err)
	}
	if !result.Success || strings.Join(result.FilesTouched, ",") != "docs/new.txt" {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("new file = %q", data)
	}
	text := stream.String()
	for _, want := range []string{`"tool":"create_file"`, `"type":"files_touched"`, `"docs/new.txt"`, `"type":"patch_diff_summary"`, `"operation":"add"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerDispatchesBrowserToolsThroughBrowserRunner(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	browser := &fakeBrowserRunner{}
	runner := Runner{
		Session:          writer,
		SessionID:        sessionID,
		Prompt:           "use browser",
		BrowserAvailable: true,
		Browser:          browser,
	}
	actions := []Action{
		{Type: "tool_call", Tool: "browser_navigate", Args: raw(`{"url":"https://example.test"}`)},
		{Type: "tool_call", Tool: "browser_snapshot", Args: raw(`{"max_chars":2000}`)},
		{Type: "final_response", Summary: "browser done", Verification: []string{"scripted browser fake"}},
	}
	if err := runner.Run(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(browser.calls, ","); got != "browser_navigate,browser_snapshot" {
		t.Fatalf("browser calls = %q", got)
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"browser_activity_started"`,
		`"type":"browser_page"`,
		`"type":"browser_activity_finished"`,
		`"tool":"browser_navigate"`,
		`"tool":"browser_snapshot"`,
		`"url":"https://example.test/"`,
		"title: Example",
		`button \"Continue\"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
}

func TestRunnerBrowserWaitForUserPausesUntilSteerMessage(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	steer := make(chan string, 1)
	steer <- "I'm logged in, continue"
	runner := Runner{
		Session:          writer,
		SessionID:        sessionID,
		Prompt:           "use browser",
		BrowserAvailable: true,
		Browser:          &fakeBrowserRunner{},
		SteerMessages:    steer,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "browser_wait_for_user",
		Args: raw(`{"message":"Finish login, then tell me to continue."}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v; result=%#v", err, result)
	}
	if !strings.Contains(result.Output, "I'm logged in") {
		t.Fatalf("result output = %q, want steer message", result.Output)
	}
	text := stream.String()
	for _, want := range []string{
		`"type":"browser_waiting_for_user"`,
		`"tool":"browser_wait_for_user"`,
		`"message":"Finish login, then tell me to continue."`,
		`"url":"https://example.test/"`,
		`"type":"user_message"`,
		`"message":"I'm logged in, continue"`,
		`"type":"browser_activity_finished"`,
		`"type":"tool_result"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
}

func TestRunnerFinalResponseAuditBlocksUnknownBrowserOutcomeBeforeUserWait(t *testing.T) {
	runner := Runner{
		BrowserAvailable: true,
		browserToolsUsed: true,
	}
	audit := runner.FinalResponseAudit(Action{
		Type:    "final_response",
		Summary: "Need login. Should I wait?",
		Outcome: "unknown",
	})
	if !audit.Blocking {
		t.Fatalf("audit.Blocking = false, want true; audit=%#v", audit)
	}
	if !strings.Contains(audit.Message, "browser_wait_for_user") {
		t.Fatalf("audit message missing browser_wait_for_user guidance: %q", audit.Message)
	}
	if audit.Code != "browser_wait_required" {
		t.Fatalf("audit.Code = %q, want browser_wait_required", audit.Code)
	}

	runner.browserWaitForUserUsed = true
	audit = runner.FinalResponseAudit(Action{
		Type:    "final_response",
		Summary: "Need login. Cannot continue.",
		Outcome: "unknown",
	})
	if audit.Blocking {
		t.Fatalf("audit.Blocking = true after wait was attempted; audit=%#v", audit)
	}
}

func TestRunnerRejectsBrowserToolsWhenUnavailable(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "use browser",
	}
	err = runner.Run(context.Background(), []Action{
		{Type: "tool_call", Tool: "browser_navigate", Args: raw(`{"url":"https://example.test"}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "browser_navigate failed") {
		t.Fatalf("Run error = %v, want browser tool failure", err)
	}
	if !strings.Contains(stream.String(), "browser tools are not available") {
		t.Fatalf("stream missing browser unavailable result:\n%s", stream.String())
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

func TestRunnerScoutsFilesWithUtilityModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package demo\n\nfunc updateCodexMode() {}\n"), 0o644); err != nil {
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
	scout := &fakeCodeScout{}
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Files:     tools.FileTools{Workspace: w},
		CodeScout: scout,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "scout_files",
		Args: raw(`{"question":"Where is embedded Enter handled?","path":".","file_glob":"*.go","max_files":5,"max_lines_per_file":80}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !strings.Contains(result.Output, "scout_files: true") || !strings.Contains(result.Output, "app.go:3") {
		t.Fatalf("scout output =\n%s", result.Output)
	}
	if scout.request.Question != "Where is embedded Enter handled?" || !strings.Contains(scout.request.FilePack, "func updateCodexMode") {
		t.Fatalf("scout request = %#v", scout.request)
	}
	text := stream.String()
	for _, want := range []string{`"type":"scout_files"`, `"phase":"scout_files"`, `"type":"scout_files_result"`, `"model":"fake-scout"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerConsultsCriticWithFileExcerpt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package demo\n\nfunc updateCodexMode() {}\n"), 0o644); err != nil {
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
	critic := &fakeCriticConsultant{}
	runner := Runner{
		Session:          writer,
		SessionID:        sessionID,
		Prompt:           "add Enter handling",
		Files:            tools.FileTools{Workspace: w},
		CriticConsultant: critic,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "consult_critic",
		Args: raw(`{"kind":"patch","question":"Does this change need another test?","candidate":"I will patch updateCodexMode.","checks":["correctness"],"files":[{"path":"app.go","start_line":1,"end_line":3,"role":"current code"}]}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "critic_consultation: concerns") || !strings.Contains(result.Output, "suggested_next_step") || !strings.Contains(result.Output, "[medium/medium, confirmed]") {
		t.Fatalf("consult result = %#v", result)
	}
	if critic.request.Kind != "patch" || critic.request.Question != "Does this change need another test?" {
		t.Fatalf("critic request = %#v", critic.request)
	}
	if len(critic.request.Files) != 1 || !strings.Contains(critic.request.Files[0].Excerpt, "func updateCodexMode") {
		t.Fatalf("critic file excerpts = %#v", critic.request.Files)
	}
	text := stream.String()
	for _, want := range []string{`"tool":"consult_critic"`, `"type":"critic_consult_started"`, `"type":"critic_consult_result"`, `"model":"fake-critic"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerAnalyzesImageWithConfiguredVisionModel(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	analyzer := &fakeImageAnalyzer{}
	runner := Runner{
		Session:       writer,
		SessionID:     sessionID,
		Prompt:        "make the skate game look like a boardwalk",
		ImageAnalyzer: analyzer,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "analyze_image",
		Args: raw(`{"path":"screenshot.png","question":"Is the boardwalk visible?","context":"Expected: wooden boardwalk, ocean, player.","checks":["missing boardwalk","floating props"]}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "boardwalk is missing") {
		t.Fatalf("analyze_image result = %#v", result)
	}
	if analyzer.request.Path != "screenshot.png" || analyzer.request.Question != "Is the boardwalk visible?" {
		t.Fatalf("image request = %#v", analyzer.request)
	}
	text := stream.String()
	for _, want := range []string{`"tool":"analyze_image"`, `"type":"image_analysis_started"`, `"type":"image_analysis_result"`, `"model":"fake-vision"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerAnalyzesImageWithComparisonPath(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	analyzer := &fakeImageAnalyzer{}
	runner := Runner{
		Session:       writer,
		SessionID:     sessionID,
		Prompt:        "verify a dynamic visual artifact",
		ImageAnalyzer: analyzer,
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "analyze_image",
		Args: raw(`{"path":"first.png","comparison_path":"second.png","question":"Does the visual state remain stable between frames?"}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("analyze_image result = %#v", result)
	}
	if analyzer.request.Path != "first.png" || analyzer.request.ComparisonPath != "second.png" {
		t.Fatalf("image request = %#v", analyzer.request)
	}
	if runner.imageAnalyses != 1 || runner.temporalImageAnalyses != 1 {
		t.Fatalf("analysis counters image=%d temporal=%d, want 1/1", runner.imageAnalyses, runner.temporalImageAnalyses)
	}
	text := stream.String()
	for _, want := range []string{`"comparison_path":"second.png"`, `"temporal":true`, `"type":"image_analysis_result"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerUpdatesQualityPlan(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "make a skate game",
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":false,"phases":[{"name":"core movement","status":"in_progress","acceptance":["player moves"]},{"name":"boardwalk environment","status":"planned","acceptance":["wooden boardwalk visible"]}]}`),
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, "quality_plan: game") {
		t.Fatalf("quality plan result = %#v", result)
	}
	if runner.qualityPlan == nil || runner.qualityPlan.ArtifactType != "game" || !runner.qualityPlan.RequiresVisualVerification {
		t.Fatalf("runner quality plan = %#v", runner.qualityPlan)
	}
	text := stream.String()
	for _, want := range []string{`"tool":"update_quality_plan"`, `"type":"quality_plan_update"`, `"artifact_type":"game"`, `"requires_visual_verification":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerCountsInspectionEvidenceTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n\nold behavior\n"), 0o644); err != nil {
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
	actions := []Action{
		{Type: "tool_call", Tool: "read_file", Args: raw(`{"path":"README.md","limit":20}`)},
		{Type: "tool_call", Tool: "search", Args: raw(`{"query":"old behavior","path":".","file_glob":"*.md","max_matches":10}`)},
		{Type: "tool_call", Tool: "file_outline", Args: raw(`{"path":"README.md"}`)},
	}
	for _, action := range actions {
		if result, err := runner.RunTool(context.Background(), action); err != nil || !result.Success {
			t.Fatalf("%s result = %#v err=%v", action.Tool, result, err)
		}
	}
	if got := runner.InspectionEvidenceEvents(); got != len(actions) {
		t.Fatalf("InspectionEvidenceEvents() = %d, want %d", got, len(actions))
	}
}

func TestRunnerRejectsQualityPlanPhaseJumps(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{
		Session:   writer,
		SessionID: sessionID,
		Prompt:    "make a skate game",
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":true,"phases":[{"name":"core movement","status":"in_progress"},{"name":"boardwalk environment","status":"planned"},{"name":"HUD","status":"planned"}]}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("initial update = %#v err=%v", result, err)
	}
	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":true,"phases":[{"name":"core movement","status":"verified","evidence":["screenshot shows movement"]},{"name":"boardwalk environment","status":"verified","evidence":["screenshot shows boardwalk"]},{"name":"HUD","status":"planned"}]}`),
	})
	if err == nil || result.Success || !strings.Contains(result.Error, "advanced 2 phases at once") {
		t.Fatalf("jump update = %#v err=%v, want phase-jump denial", result, err)
	}
	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":true,"phases":[{"name":"full game","status":"in_progress"}]}`),
	})
	if err == nil || result.Success || !strings.Contains(result.Error, "cannot shrink") {
		t.Fatalf("shrink update = %#v err=%v, want shrink denial", result, err)
	}
	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":false,"requires_visual_verification":true,"phases":[{"name":"core movement","status":"verified","evidence":["screenshot shows movement"]},{"name":"boardwalk environment","status":"in_progress"},{"name":"HUD","status":"planned"}]}`),
	})
	if err == nil || result.Success || !strings.Contains(result.Error, "cannot turn off runtime verification") {
		t.Fatalf("requirement downgrade = %#v err=%v, want runtime downgrade denial", result, err)
	}
	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":true,"phases":[{"name":"core movement","status":"verified","evidence":["screenshot shows movement"]},{"name":"boardwalk environment","status":"in_progress"},{"name":"HUD","status":"planned"}]}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("single-step update = %#v err=%v", result, err)
	}
}

func TestRunnerRejectsOutOfOrderQualityPlanUpdate(t *testing.T) {
	runner := Runner{}
	result := runner.validateQualityPlanProgression(QualityPlan{
		ArtifactType:                "game",
		RequiresRuntimeVerification: true,
		RequiresVisualVerification:  true,
		Phases: []QualityPlanPhase{
			{Name: "core movement", Status: "planned"},
			{Name: "boardwalk environment", Status: "in_progress"},
		},
	})
	if result.Success || !strings.Contains(result.Error, "phases must advance one at a time") {
		t.Fatalf("result = %#v, want out-of-order rejection", result)
	}
}

func TestRunnerAllowsTemporalQualityPlanDowngrade(t *testing.T) {
	runner := Runner{
		qualityPlan: &QualityPlan{
			ArtifactType:                       "ui",
			RequiresVisualVerification:         true,
			RequiresTemporalVisualVerification: true,
			Phases: []QualityPlanPhase{
				{Name: "dynamic visual state", Status: "in_progress"},
			},
		},
	}
	result := runner.validateQualityPlanProgression(QualityPlan{
		ArtifactType:               "ui",
		RequiresVisualVerification: true,
		Phases: []QualityPlanPhase{
			{Name: "dynamic visual state", Status: "in_progress"},
		},
	})
	if !result.Success {
		t.Fatalf("result = %#v, want temporal requirement downgrade allowed", result)
	}
}

func TestRunnerAllowsBroadWriteDuringActiveQualityPhase(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyMedium)
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
		Patch:     tools.PatchApplier{Workspace: w},
		qualityPlan: &QualityPlan{
			ArtifactType:                "game",
			RequiresRuntimeVerification: true,
			RequiresVisualVerification:  true,
			Phases: []QualityPlanPhase{
				{Name: "core movement", Status: "in_progress"},
				{Name: "environment", Status: "planned"},
			},
		},
	}
	content := strings.Repeat("int x = 0;\n", 700)
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "create_file",
		Args: raw(`{"path":"skate.cpp","content":` + strconv.Quote(content) + `}`),
	})
	if err != nil || !result.Success || result.Denied {
		t.Fatalf("result = %#v err=%v, want broad write allowed", result, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "skate.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("skate.cpp content length = %d, want %d", len(data), len(content))
	}
	text := stream.String()
	if strings.Contains(text, `"type":"permission_denied"`) {
		t.Fatalf("stream unexpectedly denied broad write:\n%s", text)
	}
	for _, want := range []string{`"tool":"create_file"`, `"type":"files_touched"`, `"skate.cpp"`, `"type":"patch_diff_summary"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerBlocksWriteUntilRequiredQualityPlanExists(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyMedium)
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
		Session:                      writer,
		SessionID:                    sessionID,
		Patch:                        tools.PatchApplier{Workspace: w},
		QualityPlanRequired:          true,
		QualityPlanRequirementScope:  "sizable",
		QualityPlanRequirementReason: "needs phased implementation",
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "create_file",
		Args: raw(`{"path":"skate.cpp","content":"int main() { return 0; }\n"}`),
	})
	if err == nil || result.Success || !strings.Contains(result.Error, "update_quality_plan must be called before write tools") {
		t.Fatalf("pre-plan write result = %#v err=%v, want planning block", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "skate.cpp")); !os.IsNotExist(err) {
		t.Fatalf("pre-plan write created file, stat err=%v", err)
	}

	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_quality_plan",
		Args: raw(`{"artifact_type":"game","requires_runtime_verification":true,"requires_visual_verification":true,"phases":[{"name":"core movement","status":"in_progress","acceptance":["player moves"]}]}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("quality plan result = %#v err=%v", result, err)
	}
	result, err = runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "create_file",
		Args: raw(`{"path":"skate.cpp","content":"int main() { return 0; }\n"}`),
	})
	if err != nil || !result.Success {
		t.Fatalf("post-plan write result = %#v err=%v, want success", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "skate.cpp")); err != nil {
		t.Fatalf("post-plan write did not create file: %v", err)
	}
}

func TestRunnerFinalResponseAuditBlocksCompletedUntilQualityPlanEvidence(t *testing.T) {
	runner := Runner{
		qualityPlan: &QualityPlan{
			ArtifactType:                "game",
			RequiresRuntimeVerification: true,
			RequiresVisualVerification:  true,
			Phases: []QualityPlanPhase{
				{Name: "core movement", Status: "verified", Evidence: []string{"compile passed"}},
				{Name: "boardwalk environment", Status: "implemented", Evidence: []string{"created geometry"}},
			},
		},
		verificationChecks: []tools.VerificationCheck{{
			Command: "clang++ game.cpp",
			Status:  tools.VerificationStatusPassed,
			Success: true,
		}},
		imageAnalyses: 1,
	}
	audit := runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_phase_unverified" || !audit.Blocking {
		t.Fatalf("audit = %#v, want unverified phase block", audit)
	}

	runner.qualityPlan.Phases[1].Status = "verified"
	runner.qualityPlan.Phases[1].Evidence = nil
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_phase_evidence_missing" || !audit.Blocking {
		t.Fatalf("audit = %#v, want missing phase evidence block", audit)
	}

	runner.qualityPlan.Phases[1].Evidence = []string{"visual inspection passed"}
	runner.imageAnalyses = 0
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_visual_evidence_missing" || !audit.Blocking {
		t.Fatalf("audit = %#v, want visual evidence block", audit)
	}

	runner.imageAnalyses = 1
	runner.verificationChecks = nil
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_runtime_evidence_missing" || !audit.Blocking {
		t.Fatalf("audit = %#v, want runtime evidence block", audit)
	}

	runner.verificationChecks = []tools.VerificationCheck{{
		Command: "clang++ game.cpp",
		Status:  tools.VerificationStatusPassed,
		Success: true,
	}}
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Outcome != "pass" || audit.Blocking {
		t.Fatalf("audit = %#v, want pass after declared evidence exists", audit)
	}
}

func TestRunnerFinalResponseAuditBlocksCompletedUntilTemporalVisualEvidence(t *testing.T) {
	runner := Runner{
		qualityPlan: &QualityPlan{
			ArtifactType:                       "ui",
			RequiresVisualVerification:         true,
			RequiresTemporalVisualVerification: true,
			Phases: []QualityPlanPhase{
				{Name: "visual behavior", Status: "verified", Evidence: []string{"screenshots captured"}},
			},
		},
		imageAnalyses: 1,
	}
	audit := runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_temporal_visual_evidence_missing" || !audit.Blocking {
		t.Fatalf("audit = %#v, want temporal visual evidence block", audit)
	}

	runner.temporalImageAnalyses = 1
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Outcome != "pass" || audit.Blocking {
		t.Fatalf("audit = %#v, want pass after paired visual evidence exists", audit)
	}
}

func TestRunnerFinalResponseAuditBlocksCompletedWhenRequiredQualityPlanMissing(t *testing.T) {
	runner := Runner{
		QualityPlanRequired:          true,
		QualityPlanRequirementScope:  "sizable",
		QualityPlanRequirementReason: "substantial scratch game needs sequencing",
	}
	audit := runner.FinalResponseAudit(Action{Type: "final_response", Summary: "done", Outcome: "completed"})
	if audit.Code != "quality_plan_required_missing" || !audit.Blocking || !strings.Contains(audit.Message, "substantial scratch game") {
		t.Fatalf("audit = %#v, want missing required quality plan block", audit)
	}

	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "partial", Outcome: "partial"})
	if audit.Blocking {
		t.Fatalf("audit = %#v, partial outcome should be allowed as honest handoff", audit)
	}
}

func TestRunnerFinalResponseAuditAllowsPartialWithOpenRequiredQualityPlan(t *testing.T) {
	runner := Runner{
		QualityPlanRequired: true,
		qualityPlan: &QualityPlan{
			ArtifactType: "game",
			Phases: []QualityPlanPhase{
				{Name: "foundation", Status: "verified", Evidence: []string{"compile passed"}},
				{Name: "boardwalk environment", Status: "planned"},
			},
		},
	}
	audit := runner.FinalResponseAudit(Action{Type: "final_response", Summary: "phase 1 done", Outcome: "partial"})
	if audit.Blocking {
		t.Fatalf("audit = %#v, want partial allowed while required phase remains", audit)
	}
	audit = runner.FinalResponseAudit(Action{Type: "final_response", Summary: "blocked by missing SDK", Outcome: "blocked"})
	if audit.Blocking {
		t.Fatalf("blocked outcome should be allowed for concrete stop conditions, audit=%#v", audit)
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

type fakeCodeScout struct {
	request ScoutFilesRequest
}

func (f *fakeCodeScout) ScoutFiles(_ context.Context, request ScoutFilesRequest) (ScoutFilesResult, error) {
	f.request = request
	return ScoutFilesResult{
		Output:       "scout_files: true\nlikely_relevant:\n- app.go:3 confidence=high reason=contains updateCodexMode\n",
		Provider:     "fake",
		Model:        "fake-scout",
		UsageSummary: lcrmodel.LLMUsage{InputTokens: 20, OutputTokens: 7, TotalTokens: 27},
	}, nil
}

type fakeCriticConsultant struct {
	request CriticConsultRequest
}

func (f *fakeCriticConsultant) ConsultCritic(_ context.Context, request CriticConsultRequest) (CriticConsultResult, error) {
	f.request = request
	return CriticConsultResult{
		Status:          "concerns",
		Summary:         "add a targeted test",
		LeadInstruction: "Add or inspect a focused test before final_response.",
		Provider:        "fake",
		Model:           "fake-critic",
		Findings: []CriticConsultFinding{{
			Severity:          "medium",
			Materiality:       "medium",
			Basis:             "confirmed",
			Claim:             "The candidate changes behavior without showing a test.",
			EvidenceSource:    "candidate",
			Evidence:          request.Candidate,
			SuggestedFollowup: "Add a focused test.",
		}},
		UsageSummary: lcrmodel.LLMUsage{InputTokens: 12, OutputTokens: 4, TotalTokens: 16},
	}, nil
}

type fakeImageAnalyzer struct {
	request ImageAnalysisRequest
}

func (f *fakeImageAnalyzer) AnalyzeImage(_ context.Context, request ImageAnalysisRequest) (ImageAnalysisResult, error) {
	f.request = request
	return ImageAnalysisResult{
		Output:       "The boardwalk is missing; props appear to float over a blue background.",
		Provider:     "fake",
		Model:        "fake-vision",
		UsageSummary: lcrmodel.LLMUsage{InputTokens: 30, OutputTokens: 9, TotalTokens: 39},
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

type fakeBrowserRunner struct {
	calls []string
	args  []json.RawMessage
}

func (f *fakeBrowserRunner) RunBrowserTool(_ context.Context, tool string, args json.RawMessage) tools.ToolResult {
	f.calls = append(f.calls, tool)
	f.args = append(f.args, append(json.RawMessage(nil), args...))
	switch tool {
	case "browser_navigate":
		return tools.ToolResult{Success: true, Output: "url: https://example.test/\ntitle: Example\nstatus: navigated\n"}
	case "browser_snapshot":
		return tools.ToolResult{Success: true, Output: "snapshot:\n- button \"Continue\" [ref=e1]\n"}
	default:
		return tools.ToolResult{Success: true, Output: "url: https://example.test/\nstatus: ok\n"}
	}
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
	audit := runner.FinalResponseAudit(Action{
		Type:         "final_response",
		Summary:      "done",
		FilesChanged: []string{"README.md"},
		Verification: []string{"go test ./..."},
	})
	if audit.Outcome != "block" || !audit.Blocking || audit.VerificationStatus != "reported_only" {
		t.Fatalf("audit = %#v, want blocking reported_only", audit)
	}
}

func TestRunnerFinalResponseAuditWarnsOnFailedVerification(t *testing.T) {
	runner := Runner{
		verificationChecks: []tools.VerificationCheck{{
			Command:  "go test ./...",
			Status:   tools.VerificationStatusTimedOut,
			TimedOut: true,
		}},
	}
	audit := runner.FinalResponseAudit(Action{
		Type:         "final_response",
		Summary:      "verification timed out",
		FilesChanged: nil,
		Verification: []string{"go test ./... timed out"},
	})
	if audit.Outcome != "warn" || audit.Blocking || audit.VerificationStatus != "failed" || !strings.Contains(audit.Message, "verification evidence did not pass") {
		t.Fatalf("audit = %#v, want non-blocking failed-verification warning", audit)
	}
}

func TestRunnerFinalResponseAuditBlocksCompletedAfterFailedVerification(t *testing.T) {
	runner := Runner{
		verificationChecks: []tools.VerificationCheck{{
			Command:  "go test ./...",
			Status:   tools.VerificationStatusFailed,
			ExitCode: 1,
		}},
	}
	audit := runner.FinalResponseAudit(Action{
		Type:         "final_response",
		Summary:      "done",
		Outcome:      "completed",
		FilesChanged: nil,
		Verification: []string{"go test ./..."},
	})
	if audit.Outcome != "block" || !audit.Blocking || audit.FinalOutcome != "completed" || audit.VerificationStatus != "failed" {
		t.Fatalf("audit = %#v, want blocking completed-after-failed-verification", audit)
	}
	feedback, ok := audit.VerificationFeedback()
	if !ok || !strings.Contains(feedback.Message, "outcome was completed") {
		t.Fatalf("feedback = %#v ok=%v, want outcome feedback", feedback, ok)
	}
}

func TestRunnerFinalResponseAuditAllowsCompletedAfterUnmatchedFailedProbeWhenReportedBuildPassed(t *testing.T) {
	runner := Runner{
		verificationChecks: []tools.VerificationCheck{
			{
				Command:  "clang++ -o /tmp/probe -x c++ -",
				Status:   tools.VerificationStatusFailed,
				ExitCode: 1,
			},
			{
				Command: "clang++ -O2 -o game game.cpp",
				Status:  tools.VerificationStatusPassed,
				Success: true,
			},
		},
	}
	audit := runner.FinalResponseAudit(Action{
		Type:         "final_response",
		Summary:      "done",
		Outcome:      "completed",
		FilesChanged: []string{"game.cpp"},
		Verification: []string{"clang++ compilation succeeded with zero errors"},
	})
	if audit.Outcome != "pass" || audit.Blocking || audit.VerificationStatus != "verified" {
		t.Fatalf("audit = %#v, want pass using later passed reported verification", audit)
	}
}

func TestRunnerFinalResponseAuditUsesLatestVerificationWhenFinalDoesNotListDetails(t *testing.T) {
	checks := []tools.VerificationCheck{
		{
			Command:  "g++ skate.cpp -o skate -lglfw -framework OpenGL",
			Status:   tools.VerificationStatusFailed,
			ExitCode: 1,
		},
		{
			Command: "g++ -I/opt/homebrew/include skate.cpp -o skate -L/opt/homebrew/lib -lglfw -framework OpenGL",
			Status:  tools.VerificationStatusPassed,
			Success: true,
		},
	}
	status, message := finalVerificationStatus([]string{"skate.cpp"}, nil, checks)
	if status != "verified" || !strings.Contains(message, "superseded") {
		t.Fatalf("status=%q message=%q, want latest passing verification with superseded failure note", status, message)
	}
}

func TestRunnerFinalResponseAuditBlocksCompletedOperationalActionWithoutLaterVerification(t *testing.T) {
	runner := Runner{
		verificationChecks: []tools.VerificationCheck{{
			Command: "go test ./...",
			Status:  tools.VerificationStatusPassed,
			Success: true,
		}},
		operationalActions: []OperationalAction{{
			Action:                   string(ProcessActionStart),
			Command:                  "pnpm dev",
			Success:                  true,
			VerificationChecksBefore: 1,
		}},
	}
	audit := runner.FinalResponseAudit(Action{
		Type:    "final_response",
		Summary: "server started",
		Outcome: "completed",
	})
	if audit.Outcome != "block" || !audit.Blocking || !strings.Contains(audit.Message, "no later run_command check marked purpose=verify") {
		t.Fatalf("audit = %#v, want blocking missing post-operation verification", audit)
	}
}

func TestRunnerFinalResponseAuditAllowsCompletedOperationalActionWithLaterVerification(t *testing.T) {
	runner := Runner{
		verificationChecks: []tools.VerificationCheck{
			{
				Command: "go test ./...",
				Status:  tools.VerificationStatusPassed,
				Success: true,
			},
			{
				Command: "curl http://127.0.0.1:3000/",
				Status:  tools.VerificationStatusPassed,
				Success: true,
			},
		},
		operationalActions: []OperationalAction{{
			Action:                   string(ProcessActionStart),
			Command:                  "pnpm dev",
			Success:                  true,
			VerificationChecksBefore: 1,
		}},
	}
	audit := runner.FinalResponseAudit(Action{
		Type:         "final_response",
		Summary:      "server started",
		Outcome:      "completed",
		Verification: []string{"curl http://127.0.0.1:3000/"},
	})
	if audit.Outcome != "pass" || audit.Blocking || audit.VerificationStatus != "verified" {
		t.Fatalf("audit = %#v, want pass after post-operation verification", audit)
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

func TestRunnerRunCommandDeniesWorkspaceWriteAtMedium(t *testing.T) {
	root := t.TempDir()
	w, err := policy.NewWorkspace(root, policy.AutonomyMedium)
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
	}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "run_command",
		Args: raw(`{"command":"cat > created.txt << 'EOF'\nhello\nEOF","timeout_ms":1000}`),
	})
	if err == nil {
		t.Fatalf("RunTool succeeded, want denial; result=%#v", result)
	}
	if !result.Denied || result.Success || !strings.Contains(result.DenialReason, tools.CommandWorkspaceWriteDenialReason) {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(w.Root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt stat error = %v, want not exist", err)
	}
	text := stream.String()
	for _, want := range []string{`"type":"permission_denied"`, `"tool":"run_command"`, `"type":"tool_result"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %s:\n%s", want, text)
		}
	}
}

func TestRunnerRunCommandSystemMutationDoesNotUseLowApproval(t *testing.T) {
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
		Args: raw(`{"argv":["defaults","write","com.example.demo","Enabled","1"],"timeout_ms":1000}`),
	})
	if err == nil {
		t.Fatalf("RunTool succeeded, want system mutation denial; result=%#v", result)
	}
	if approvals.calls != 0 {
		t.Fatalf("approval broker calls = %d, want 0", approvals.calls)
	}
	if !result.Denied || !result.SystemMutation || !strings.Contains(result.DenialReason, tools.CommandSystemMutationDenialReason) {
		t.Fatalf("result = %#v", result)
	}
	if text := stream.String(); !strings.Contains(text, `"system_mutation":true`) || !strings.Contains(text, `"type":"permission_denied"`) {
		t.Fatalf("stream missing system mutation denial evidence:\n%s", text)
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
	processes := &fakeProcessBroker{result: tools.ToolResult{
		Output: "running; pid 42",
		ManagedProcess: &tools.ManagedProcessEvidence{
			Action:    "start",
			ProcessID: "rt_1",
			Name:      "frontend",
			Command:   "pnpm dev",
			CWD:       "frontend",
			PID:       42,
			Running:   true,
		},
	}}
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
	text := stream.String()
	for _, want := range []string{
		`"type":"operational_action"`,
		`"action":"start"`,
		`"process_id":"rt_1"`,
		`"pid":42`,
		`"running":true`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
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
	approvals := &fakeApprovalBroker{decisions: []ApprovalDecision{DecisionAccept}}
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

func TestRunnerUpdatePlanAcceptsLegacyAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "todos", args: `{"todos":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"}]}`},
		{name: "plan", args: `{"plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"}]}`},
		{name: "stringified_todos", args: `{"todos":"[{\"step\":\"Inspect\",\"status\":\"completed\"},{\"step\":\"Patch\",\"status\":\"in_progress\"}]"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream bytes.Buffer
			writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			runner := Runner{Session: writer, SessionID: sessionID}
			result, err := runner.RunTool(context.Background(), Action{
				Type: "tool_call",
				Tool: "update_plan",
				Args: raw(tc.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatalf("result = %#v", result)
			}
			text := stream.String()
			for _, want := range []string{`"type":"plan_update"`, `"step":"Patch"`, `"status":"in_progress"`} {
				if !strings.Contains(text, want) {
					t.Fatalf("stream missing %s:\n%s", want, text)
				}
			}
		})
	}
}

func TestRunnerUpdatePlanInvalidArgsIncludeExpectedShape(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	runner := Runner{Session: writer, SessionID: sessionID}
	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "update_plan",
		Args: raw(`{"surprise":true}`),
	})
	if err == nil {
		t.Fatal("RunTool succeeded, want invalid argument failure")
	}
	for _, want := range []string{`unknown field "surprise"`, `expected {"items":[{"step":"Inspect"`, `legacy aliases "todos" and "plan" are also accepted`} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("error %q missing %q", result.Error, want)
		}
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

func TestRunnerApprovedCommandForSessionRaisesToMedium(t *testing.T) {
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
	}); err != nil || !result.Success {
		t.Fatalf("third RunTool() = %#v, %v; want medium session approval to allow another command", result, err)
	}
	if approvals.calls != 1 {
		t.Fatalf("approval should only be requested before raising the session to medium, got %d calls", approvals.calls)
	}
	if runner.Command.Workspace.Auto != policy.AutonomyMedium {
		t.Fatalf("runner command autonomy = %q, want medium", runner.Command.Workspace.Auto)
	}
	if approvals.requests[0].Scope == "" || !strings.Contains(approvals.requests[0].Scope, "this exact command") {
		t.Fatalf("approval scope = %#v, want exact command scope", approvals.requests)
	}
	if text := stream.String(); !strings.Contains(text, `"type":"permission_level_changed"`) || !strings.Contains(text, `"to":"medium"`) {
		t.Fatalf("stream missing permission level change:\n%s", text)
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
