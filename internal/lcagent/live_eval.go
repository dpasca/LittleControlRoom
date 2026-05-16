package lcagent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/lcagent/sessionmetrics"
)

type liveEvalTask struct {
	Name             string            `json:"name"`
	Category         string            `json:"category"`
	Description      string            `json:"description"`
	Prompt           string            `json:"prompt"`
	Files            map[string]string `json:"-"`
	ExpectedContains map[string]string `json:"expected_contains,omitempty"`
	VerifyCommand    []string          `json:"verify_command,omitempty"`
	ExpectFiles      []string          `json:"expect_files,omitempty"`
	ExpectNoEdits    bool              `json:"expect_no_edits,omitempty"`
}

type liveEvalReport struct {
	Passed         bool                   `json:"passed"`
	StartedAt      string                 `json:"started_at"`
	Provider       string                 `json:"provider,omitempty"`
	Model          string                 `json:"model,omitempty"`
	Autonomy       string                 `json:"autonomy,omitempty"`
	ToolProfile    string                 `json:"tool_profile,omitempty"`
	ContextProfile string                 `json:"context_profile,omitempty"`
	DataDir        string                 `json:"data_dir,omitempty"`
	Root           string                 `json:"root,omitempty"`
	Kept           bool                   `json:"kept"`
	Cases          []liveEvalCaseResult   `json:"cases"`
	Summary        sessionmetrics.Summary `json:"summary"`
}

type liveEvalCaseResult struct {
	Name           string                 `json:"name"`
	Category       string                 `json:"category"`
	Passed         bool                   `json:"passed"`
	Error          string                 `json:"error,omitempty"`
	Workspace      string                 `json:"workspace,omitempty"`
	Artifact       string                 `json:"artifact,omitempty"`
	SessionID      string                 `json:"session_id,omitempty"`
	DurationMillis int64                  `json:"duration_ms"`
	Score          liveEvalScore          `json:"score"`
	Metrics        sessionmetrics.Summary `json:"metrics,omitempty"`
}

type liveEvalScore struct {
	Correctness              bool    `json:"correctness"`
	Verified                 bool    `json:"verified"`
	ExpectedFilesTouched     bool    `json:"expected_files_touched"`
	UnexpectedWorkspaceDirty bool    `json:"unexpected_workspace_dirty,omitempty"`
	FailedToolResults        int     `json:"failed_tool_results"`
	PermissionDenials        int     `json:"permission_denials"`
	PatchFeedback            int     `json:"patch_feedback"`
	VerificationFeedback     int     `json:"verification_feedback"`
	ReadFileCalls            int     `json:"read_file_calls"`
	ReadFileLines            int     `json:"read_file_lines"`
	OverlappingReadCalls     int     `json:"overlapping_read_calls"`
	TraceQualityScore        int     `json:"trace_quality_score"`
	TraceQualityGrade        string  `json:"trace_quality_grade,omitempty"`
	InputTokens              int64   `json:"input_tokens"`
	CachedInputTokens        int64   `json:"cached_input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	TotalTokens              int64   `json:"total_tokens"`
	EstimatedCostUSD         float64 `json:"estimated_cost_usd"`
}

func runLiveEval(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("live-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var provider, model, envFile, dataDir, outputRaw, autoRaw, reasoningEffort, caseRaw, toolProfile, contextProfile string
	var requestTimeout time.Duration
	var maxTurns int
	var keepTemp, listOnly bool
	fs.StringVar(&provider, "provider", "openrouter", "provider: openrouter, openai, deepseek, or moonshot")
	fs.StringVar(&model, "model", "", "optional model name; blank uses provider default")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root; blank creates a timestamped live-eval dir under the Little Control Room data dir")
	fs.StringVar(&outputRaw, "output", "text", "output: text or json")
	fs.StringVar(&autoRaw, "auto", "low", "autonomy: low or medium")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "optional provider reasoning effort")
	fs.StringVar(&caseRaw, "case", "", "comma-separated case names; blank runs the full suite")
	fs.StringVar(&toolProfile, "tool-profile", "balanced", "file tool budget profile")
	fs.StringVar(&contextProfile, "context-profile", "balanced", "provider loop context profile")
	fs.DurationVar(&requestTimeout, "request-timeout", 12*time.Minute, "provider HTTP request timeout")
	fs.IntVar(&maxTurns, "max-turns", 8, "maximum model turns per case")
	fs.BoolVar(&keepTemp, "keep-temp", true, "keep temporary live-eval workspaces")
	fs.BoolVar(&listOnly, "list", false, "list live eval cases without running a provider")
	if err := fs.Parse(args); err != nil {
		return err
	}
	outputRaw = strings.ToLower(strings.TrimSpace(outputRaw))
	if outputRaw == "" {
		outputRaw = "text"
	}
	if outputRaw != "text" && outputRaw != "json" {
		return fmt.Errorf("unsupported live-eval output mode: %s", outputRaw)
	}
	tasks, err := selectLiveEvalTasks(caseRaw)
	if err != nil {
		return err
	}
	if listOnly {
		return writeLiveEvalTaskList(stdout, outputRaw, tasks)
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "openrouter"
	}
	if provider == "scripted" {
		return fmt.Errorf("lcagent live-eval requires a live provider")
	}
	started := time.Now()
	root, err := os.MkdirTemp("", "lcagent-live-eval-*")
	if err != nil {
		return err
	}
	if !keepTemp {
		defer os.RemoveAll(root)
	}
	if dataDir == "" {
		dataDir = filepath.Join(defaultDataDir(), "lcagent", "live-evals", started.Format("20060102-150405"))
	}
	report := liveEvalReport{
		Passed:         true,
		StartedAt:      started.Format(time.RFC3339),
		Provider:       provider,
		Model:          strings.TrimSpace(model),
		Autonomy:       firstLiveEvalNonEmpty(autoRaw, "low"),
		ToolProfile:    firstLiveEvalNonEmpty(toolProfile, "balanced"),
		ContextProfile: firstLiveEvalNonEmpty(contextProfile, "balanced"),
		DataDir:        dataDir,
		Root:           root,
		Kept:           keepTemp,
	}
	var artifacts []string
	for _, task := range tasks {
		result := runLiveEvalTask(root, dataDir, task, liveEvalRunConfig{
			Provider:        provider,
			Model:           strings.TrimSpace(model),
			EnvFile:         strings.TrimSpace(envFile),
			Autonomy:        report.Autonomy,
			ReasoningEffort: strings.TrimSpace(reasoningEffort),
			ToolProfile:     report.ToolProfile,
			ContextProfile:  report.ContextProfile,
			RequestTimeout:  requestTimeout,
			MaxTurns:        maxTurns,
		})
		if !result.Passed {
			report.Passed = false
		}
		if result.Artifact != "" {
			artifacts = append(artifacts, result.Artifact)
		}
		report.Cases = append(report.Cases, result)
	}
	if len(artifacts) > 0 {
		summary, err := sessionmetrics.AnalyzeFiles(artifacts)
		if err != nil {
			return err
		}
		report.Summary = summary
	}
	if err := writeLiveEvalReport(stdout, outputRaw, report); err != nil {
		return err
	}
	if !report.Passed {
		return fmt.Errorf("lcagent live-eval failed")
	}
	return nil
}

type liveEvalRunConfig struct {
	Provider        string
	Model           string
	EnvFile         string
	Autonomy        string
	ReasoningEffort string
	ToolProfile     string
	ContextProfile  string
	RequestTimeout  time.Duration
	MaxTurns        int
}

func runLiveEvalTask(root, dataDir string, task liveEvalTask, cfg liveEvalRunConfig) liveEvalCaseResult {
	started := time.Now()
	result := liveEvalCaseResult{Name: task.Name, Category: task.Category}
	workspace := filepath.Join(root, task.Name)
	result.Workspace = workspace
	if err := writeLiveEvalWorkspace(workspace, task.Files); err != nil {
		result.Error = err.Error()
		return result
	}
	before, _ := lcagentEvalSessionFileSet(dataDir)
	execArgs := []string{
		"--cwd", workspace,
		"--data-dir", dataDir,
		"--auto", cfg.Autonomy,
		"--output", "json",
		"--provider", cfg.Provider,
		"--tool-profile", cfg.ToolProfile,
		"--context-profile", cfg.ContextProfile,
		"--request-timeout", cfg.RequestTimeout.String(),
		"--max-turns", fmt.Sprintf("%d", cfg.MaxTurns),
	}
	if cfg.Model != "" {
		execArgs = append(execArgs, "--model", cfg.Model)
	}
	if cfg.EnvFile != "" {
		execArgs = append(execArgs, "--env-file", cfg.EnvFile)
	}
	if cfg.ReasoningEffort != "" {
		execArgs = append(execArgs, "--reasoning-effort", cfg.ReasoningEffort)
	}
	execArgs = append(execArgs, task.Prompt)
	var execOut bytes.Buffer
	runErr := runExec(execArgs, &execOut)
	result.DurationMillis = time.Since(started).Milliseconds()
	files, listErr := lcagentEvalNewSessionFiles(dataDir, before)
	if listErr != nil {
		result.Error = listErr.Error()
		return result
	}
	if len(files) > 0 {
		sort.Strings(files)
		result.Artifact = files[len(files)-1]
		summary, err := sessionmetrics.AnalyzeFiles([]string{result.Artifact})
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Metrics = summary
		if len(summary.SessionIDs) > 0 {
			result.SessionID = summary.SessionIDs[0]
		}
		result.Score = liveEvalScoreFromMetrics(summary)
		failedTools, err := liveEvalFailedToolResults(result.Artifact)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Score.FailedToolResults = failedTools
	}
	if runErr != nil {
		result.Error = runErr.Error()
		return result
	}
	var execResult struct {
		SessionID string `json:"session_id"`
		Artifact  string `json:"artifact"`
	}
	if err := json.Unmarshal(execOut.Bytes(), &execResult); err != nil {
		result.Error = fmt.Sprintf("decode exec result: %v", err)
		return result
	}
	if execResult.SessionID != "" {
		result.SessionID = execResult.SessionID
	}
	if execResult.Artifact != "" {
		result.Artifact = execResult.Artifact
	}
	if err := checkLiveEvalTask(workspace, task, &result); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Score.Correctness = true
	result.Passed = true
	return result
}

func liveEvalScoreFromMetrics(summary sessionmetrics.Summary) liveEvalScore {
	score := liveEvalScore{
		Verified:             summary.VerificationStatuses["verified"] > 0,
		ExpectedFilesTouched: true,
		FailedToolResults:    failedLiveEvalToolResults(summary),
		PermissionDenials:    summary.PermissionDenials,
		PatchFeedback:        summary.PatchFeedback,
		VerificationFeedback: summary.VerificationFeedback,
		ReadFileCalls:        summary.ReadFileCalls,
		ReadFileLines:        summary.ReadFileLines,
		OverlappingReadCalls: summary.ReadFileOverlappingCalls,
		TraceQualityScore:    summary.TraceQuality.Score,
		TraceQualityGrade:    summary.TraceQuality.Grade,
		InputTokens:          summary.TokenUsage.InputTokens,
		CachedInputTokens:    summary.TokenUsage.CachedInputTokens,
		OutputTokens:         summary.TokenUsage.OutputTokens,
		TotalTokens:          summary.TokenUsage.TotalTokens,
		EstimatedCostUSD:     summary.TokenUsage.EstimatedCostUSD,
	}
	return score
}

func failedLiveEvalToolResults(summary sessionmetrics.Summary) int {
	total := 0
	for tool, calls := range summary.ToolCalls {
		results := summary.ToolResults[tool]
		if calls > results {
			total += calls - results
		}
	}
	return total + summary.PermissionDenials + summary.PatchFeedback + summary.VerificationFeedback
}

func checkLiveEvalTask(workspace string, task liveEvalTask, result *liveEvalCaseResult) error {
	if len(task.ExpectFiles) > 0 && result.Artifact != "" {
		changed, err := liveEvalFinalFilesChanged(result.Artifact)
		if err != nil {
			result.Score.ExpectedFilesTouched = false
			return err
		}
		for _, want := range task.ExpectFiles {
			if !changed[want] {
				result.Score.ExpectedFilesTouched = false
				return fmt.Errorf("final_response did not list expected changed file %s", want)
			}
		}
	}
	for path, want := range task.ExpectedContains {
		body, err := os.ReadFile(filepath.Join(workspace, filepath.Clean(path)))
		if err != nil {
			result.Score.ExpectedFilesTouched = false
			return err
		}
		if !strings.Contains(string(body), want) {
			result.Score.ExpectedFilesTouched = false
			return fmt.Errorf("%s did not contain expected text", path)
		}
	}
	if len(task.VerifyCommand) > 0 {
		cmd := exec.Command(task.VerifyCommand[0], task.VerifyCommand[1:]...)
		cmd.Dir = workspace
		output, err := cmd.CombinedOutput()
		if err != nil {
			result.Score.Verified = false
			return fmt.Errorf("post-run verifier %q failed: %w\n%s", strings.Join(task.VerifyCommand, " "), err, strings.TrimSpace(string(output)))
		}
	}
	if result.Metrics.VerificationStatuses["verified"] < 1 {
		result.Score.Verified = false
		return fmt.Errorf("session did not record verified checks")
	}
	if task.ExpectNoEdits {
		dirty, err := liveEvalWorkspaceDirty(workspace)
		if err != nil {
			return err
		}
		if dirty {
			result.Score.UnexpectedWorkspaceDirty = true
			return fmt.Errorf("workspace changed during read-only task")
		}
	}
	return nil
}

func liveEvalFailedToolResults(path string) (int, error) {
	events, err := readLiveEvalArtifactEvents(path)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, event := range events {
		if liveEvalRawString(event["type"]) != "tool_result" {
			continue
		}
		var result struct {
			Success *bool `json:"success"`
		}
		if raw := event["result"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &result); err != nil {
				return 0, err
			}
			if result.Success != nil && !*result.Success {
				failed++
			}
		}
	}
	return failed, nil
}

func liveEvalFinalFilesChanged(path string) (map[string]bool, error) {
	events, err := readLiveEvalArtifactEvents(path)
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, event := range events {
		if liveEvalRawString(event["type"]) != "final_response" {
			continue
		}
		var files []string
		if raw := event["files_changed"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &files); err != nil {
				return nil, err
			}
		}
		for _, file := range files {
			changed[file] = true
		}
	}
	return changed, nil
}

func readLiveEvalArtifactEvents(path string) ([]map[string]json.RawMessage, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []map[string]json.RawMessage
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func liveEvalRawString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func liveEvalWorkspaceDirty(workspace string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = workspace
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func writeLiveEvalWorkspace(workspace string, files map[string]string) error {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}
	for path, body := range files {
		fullPath := filepath.Join(workspace, filepath.Clean(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := exec.Command("git", "-C", workspace, "init", "-q").Run(); err != nil {
		return err
	}
	if err := exec.Command("git", "-C", workspace, "add", ".").Run(); err != nil {
		return err
	}
	if err := exec.Command("git", "-C", workspace, "-c", "user.name=LCAgent Eval", "-c", "user.email=lcagent-eval@example.invalid", "commit", "-qm", "seed eval workspace").Run(); err != nil {
		return err
	}
	return nil
}

func selectLiveEvalTasks(raw string) ([]liveEvalTask, error) {
	all := liveEvalCases()
	if strings.TrimSpace(raw) == "" {
		return all, nil
	}
	byName := make(map[string]liveEvalTask, len(all))
	for _, task := range all {
		byName[task.Name] = task
	}
	var selected []liveEvalTask
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		task, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown live-eval case %q", name)
		}
		selected = append(selected, task)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no live-eval cases selected")
	}
	return selected, nil
}

func liveEvalCases() []liveEvalTask {
	return []liveEvalTask{
		{
			Name:        "readme_edit_verify",
			Category:    "smoke",
			Description: "Small documentation edit with explicit Go verification.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-readme\n\ngo 1.22\n",
				"smoke_test.go": `package smoke

import "testing"

func TestSmoke(t *testing.T) {}
`,
				"README.md": "# LCAgent live eval\n\nInitial README.\n",
			},
			Prompt: strings.TrimSpace(`Add exactly this line to README.md:
LCAgent live eval: README updated

Then run go test ./... with run_command purpose set to verify, and finish with final_response that lists README.md in files_changed and go test ./... in verification.`),
			ExpectedContains: map[string]string{"README.md": "LCAgent live eval: README updated"},
			VerifyCommand:    []string{"go", "test", "./..."},
			ExpectFiles:      []string{"README.md"},
		},
		{
			Name:        "go_bug_fix",
			Category:    "bug_fix",
			Description: "Fix a failing Go unit test by correcting implementation logic.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-bugfix\n\ngo 1.22\n",
				"calc.go": `package calc

func Add(a, b int) int {
	return a - b
}
`,
				"calc_test.go": `package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
`,
			},
			Prompt:        strings.TrimSpace(`The Go tests are failing. Find the bug, fix it, then run go test ./... with run_command purpose set to verify. Finish with final_response that lists the changed file and the verification command.`),
			VerifyCommand: []string{"go", "test", "./..."},
			ExpectFiles:   []string{"calc.go"},
		},
		{
			Name:        "feature_slice",
			Category:    "feature",
			Description: "Implement a small missing feature against existing tests.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-feature\n\ngo 1.22\n",
				"stringsx.go": `package stringsx

func Slug(input string) string {
	return ""
}
`,
				"stringsx_test.go": `package stringsx

import "testing"

func TestSlug(t *testing.T) {
	if got := Slug("Hello, Little Control Room!"); got != "hello-little-control-room" {
		t.Fatalf("Slug() = %q", got)
	}
}
`,
			},
			Prompt:        strings.TrimSpace(`Implement Slug so the existing tests pass. Keep the implementation small and dependency-free. Then run go test ./... with run_command purpose set to verify and finish with final_response listing changed files and verification.`),
			VerifyCommand: []string{"go", "test", "./..."},
			ExpectFiles:   []string{"stringsx.go"},
		},
		{
			Name:        "repo_orientation",
			Category:    "orientation",
			Description: "Read-only repository orientation with verification by inspection.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-orientation\n\ngo 1.22\n",
				"cmd/app/main.go": `package main

import (
	"fmt"

	"lcagent-live-orientation/internal/greet"
)

func main() {
	fmt.Println(greet.Message("LCAgent"))
}
`,
				"internal/greet/greet.go": `package greet

func Message(name string) string {
	return "hello, " + name
}
`,
				"README.md": "# Orientation fixture\n\nSmall CLI wired through internal/greet.\n",
			},
			Prompt:        strings.TrimSpace(`Inspect this repository and explain its main entry point, internal package, and how to verify it. Do not edit files. Run go test ./... with run_command purpose set to verify before final_response.`),
			VerifyCommand: []string{"go", "test", "./..."},
			ExpectNoEdits: true,
		},
	}
}

func writeLiveEvalTaskList(stdout io.Writer, outputRaw string, tasks []liveEvalTask) error {
	switch outputRaw {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			Cases []liveEvalTask `json:"cases"`
		}{Cases: tasks})
	default:
		fmt.Fprintf(stdout, "LCAgent live eval cases: %d\n", len(tasks))
		for _, task := range tasks {
			fmt.Fprintf(stdout, "- %s [%s]: %s\n", task.Name, task.Category, task.Description)
		}
		return nil
	}
}

func writeLiveEvalReport(stdout io.Writer, outputRaw string, report liveEvalReport) error {
	switch outputRaw {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	default:
		status := "PASS"
		if !report.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(stdout, "LCAgent live eval: %s\n", status)
		fmt.Fprintf(stdout, "provider=%s model=%s data_dir=%s root=%s\n", report.Provider, report.Model, report.DataDir, report.Root)
		for _, result := range report.Cases {
			caseStatus := "FAIL"
			if result.Passed {
				caseStatus = "PASS"
			}
			fmt.Fprintf(stdout, "- %s %s [%s] duration=%dms artifact=%s\n", caseStatus, result.Name, result.Category, result.DurationMillis, result.Artifact)
			if result.Error != "" {
				fmt.Fprintf(stdout, "  error: %s\n", result.Error)
			}
			fmt.Fprintf(stdout, "  score: correctness=%v verified=%v trace_quality=%d/%s reads=%d/%d overlap=%d denials=%d feedback=%d tokens=%d cost=%.6f\n",
				result.Score.Correctness,
				result.Score.Verified,
				result.Score.TraceQualityScore,
				result.Score.TraceQualityGrade,
				result.Score.ReadFileCalls,
				result.Score.ReadFileLines,
				result.Score.OverlappingReadCalls,
				result.Score.PermissionDenials,
				result.Score.PatchFeedback+result.Score.VerificationFeedback,
				result.Score.TotalTokens,
				result.Score.EstimatedCostUSD,
			)
		}
		return nil
	}
}

func firstLiveEvalNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
