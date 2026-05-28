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
	Name                       string            `json:"name"`
	Category                   string            `json:"category"`
	Description                string            `json:"description"`
	Prompt                     string            `json:"prompt"`
	Files                      map[string]string `json:"-"`
	UncommittedFiles           map[string]string `json:"-"`
	ExpectedContains           map[string]string `json:"expected_contains,omitempty"`
	ExpectedVerificationStatus string            `json:"expected_verification_status,omitempty"`
	VerifyCommand              []string          `json:"verify_command,omitempty"`
	VerifyCommandExpectFailure bool              `json:"verify_command_expect_failure,omitempty"`
	ExpectFiles                []string          `json:"expect_files,omitempty"`
	ExpectNoEdits              bool              `json:"expect_no_edits,omitempty"`
}

type liveEvalReport struct {
	Passed          bool                   `json:"passed"`
	StartedAt       string                 `json:"started_at"`
	Provider        string                 `json:"provider,omitempty"`
	Model           string                 `json:"model,omitempty"`
	RoutePreset     string                 `json:"route_preset,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Autonomy        string                 `json:"autonomy,omitempty"`
	ToolProfile     string                 `json:"tool_profile,omitempty"`
	ContextProfile  string                 `json:"context_profile,omitempty"`
	DataDir         string                 `json:"data_dir,omitempty"`
	Root            string                 `json:"root,omitempty"`
	Kept            bool                   `json:"kept"`
	Cases           []liveEvalCaseResult   `json:"cases"`
	Summary         sessionmetrics.Summary `json:"summary"`
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
	VerificationStatus       string  `json:"verification_status,omitempty"`
	ExpectedVerificationSeen bool    `json:"expected_verification_seen"`
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
	var provider, model, envFile, dataDir, outputRaw, autoRaw, reasoningEffort, caseRaw, toolProfile, contextProfile, routePresetRaw string
	var requestTimeout time.Duration
	var maxTurns int
	var keepTemp, listOnly bool
	fs.StringVar(&routePresetRaw, "route-preset", "", "coding route preset: balanced, quality, mimo-2.5-pro-low, mimo-2.5-pro-high, mimo-2.5-pro-max, or cheap-scout; explicit flags override preset values")
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
	visitedFlags := visitedFlagNames(fs)
	var routePreset lcagentRoutePreset
	routePresetSet := false
	if strings.TrimSpace(routePresetRaw) != "" {
		var ok bool
		routePreset, ok = lcagentRoutePresetByName(routePresetRaw)
		if !ok {
			return fmt.Errorf("unknown route preset %q; available presets: %s", routePresetRaw, lcagentRoutePresetNames())
		}
		routePresetSet = true
		applyLiveEvalRoutePreset(routePreset, visitedFlags, &provider, &model, &reasoningEffort, &autoRaw, &toolProfile, &contextProfile, &requestTimeout)
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
		Passed:          true,
		StartedAt:       started.Format(time.RFC3339),
		Provider:        provider,
		Model:           strings.TrimSpace(model),
		RoutePreset:     routePresetNameForReport(routePreset, routePresetSet),
		ReasoningEffort: strings.TrimSpace(reasoningEffort),
		Autonomy:        firstLiveEvalNonEmpty(autoRaw, "low"),
		ToolProfile:     firstLiveEvalNonEmpty(toolProfile, "balanced"),
		ContextProfile:  firstLiveEvalNonEmpty(contextProfile, "balanced"),
		DataDir:         dataDir,
		Root:            root,
		Kept:            keepTemp,
	}
	var artifacts []string
	for _, task := range tasks {
		result := runLiveEvalTask(root, dataDir, task, liveEvalRunConfig{
			Provider:        provider,
			Model:           strings.TrimSpace(model),
			RoutePreset:     report.RoutePreset,
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

func applyLiveEvalRoutePreset(preset lcagentRoutePreset, visited map[string]bool, provider, model, reasoningEffort, autoRaw, toolProfileRaw, contextProfileRaw *string, requestTimeout *time.Duration) {
	if !visited["provider"] && strings.TrimSpace(preset.Provider) != "" {
		*provider = preset.Provider
	}
	if !visited["model"] && strings.TrimSpace(preset.Model) != "" {
		*model = preset.Model
	}
	if !visited["reasoning-effort"] && strings.TrimSpace(preset.ReasoningEffort) != "" {
		*reasoningEffort = preset.ReasoningEffort
	}
	if !visited["auto"] && strings.TrimSpace(preset.Auto) != "" {
		*autoRaw = preset.Auto
	}
	if !visited["tool-profile"] && strings.TrimSpace(preset.ToolProfile) != "" {
		*toolProfileRaw = preset.ToolProfile
	}
	if !visited["context-profile"] && strings.TrimSpace(preset.ContextProfile) != "" {
		*contextProfileRaw = preset.ContextProfile
	}
	if !visited["request-timeout"] && preset.RequestTimeout > 0 {
		*requestTimeout = preset.RequestTimeout
	}
}

func routePresetNameForReport(preset lcagentRoutePreset, ok bool) string {
	if !ok {
		return ""
	}
	return strings.TrimSpace(preset.Name)
}

type liveEvalRunConfig struct {
	Provider        string
	Model           string
	RoutePreset     string
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
	if err := writeLiveEvalWorkspace(workspace, task.Files, task.UncommittedFiles); err != nil {
		result.Error = err.Error()
		return result
	}
	initialDiff := ""
	if task.ExpectNoEdits {
		diff, err := liveEvalWorkspaceDiff(workspace)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		initialDiff = diff
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
	if cfg.RoutePreset != "" {
		execArgs = append(execArgs, "--route-preset", cfg.RoutePreset)
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
	if err := checkLiveEvalTask(workspace, task, initialDiff, &result); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Score.Correctness = true
	result.Passed = true
	return result
}

func liveEvalScoreFromMetrics(summary sessionmetrics.Summary) liveEvalScore {
	verificationStatus := liveEvalVerificationStatus(summary)
	score := liveEvalScore{
		Verified:                 summary.VerificationStatuses["verified"] > 0,
		VerificationStatus:       verificationStatus,
		ExpectedVerificationSeen: verificationStatus == "verified",
		ExpectedFilesTouched:     true,
		FailedToolResults:        failedLiveEvalToolResults(summary),
		PermissionDenials:        summary.PermissionDenials,
		PatchFeedback:            summary.PatchFeedback,
		VerificationFeedback:     summary.VerificationFeedback,
		ReadFileCalls:            summary.ReadFileCalls,
		ReadFileLines:            summary.ReadFileLines,
		OverlappingReadCalls:     summary.ReadFileOverlappingCalls,
		TraceQualityScore:        summary.TraceQuality.Score,
		TraceQualityGrade:        summary.TraceQuality.Grade,
		InputTokens:              summary.TokenUsage.InputTokens,
		CachedInputTokens:        summary.TokenUsage.CachedInputTokens,
		OutputTokens:             summary.TokenUsage.OutputTokens,
		TotalTokens:              summary.TokenUsage.TotalTokens,
		EstimatedCostUSD:         summary.TokenUsage.EstimatedCostUSD,
	}
	return score
}

func liveEvalVerificationStatus(summary sessionmetrics.Summary) string {
	for _, status := range []string{"verified", "failed", "denied", "timed_out", "reported_only", "missing_after_changes", "not_run"} {
		if summary.VerificationStatuses[status] > 0 {
			return status
		}
	}
	for status, count := range summary.VerificationStatuses {
		if count > 0 {
			return status
		}
	}
	return ""
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

func checkLiveEvalTask(workspace string, task liveEvalTask, initialDiff string, result *liveEvalCaseResult) error {
	if len(task.ExpectFiles) > 0 && result.Artifact != "" {
		changed, err := liveEvalFinalFilesChanged(result.Artifact)
		if err != nil {
			result.Score.ExpectedFilesTouched = false
			return err
		}
		for _, want := range task.ExpectFiles {
			if !changed[want] {
				result.Score.ExpectedFilesTouched = false
				return fmt.Errorf("final result did not list expected changed file %s", want)
			}
		}
	}
	if task.ExpectNoEdits && result.Artifact != "" {
		changed, err := liveEvalFinalFilesChanged(result.Artifact)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			result.Score.ExpectedFilesTouched = false
			return fmt.Errorf("final_response listed changed files during read-only task")
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
		if task.VerifyCommandExpectFailure {
			if err == nil {
				result.Score.Verified = false
				return fmt.Errorf("post-run verifier %q passed, want failure", strings.Join(task.VerifyCommand, " "))
			}
		} else if err != nil {
			result.Score.Verified = false
			return fmt.Errorf("post-run verifier %q failed: %w\n%s", strings.Join(task.VerifyCommand, " "), err, strings.TrimSpace(string(output)))
		}
	}
	expectedStatus := task.ExpectedVerificationStatus
	if expectedStatus == "" {
		expectedStatus = "verified"
	}
	if result.Metrics.VerificationStatuses[expectedStatus] < 1 {
		result.Score.Verified = false
		result.Score.ExpectedVerificationSeen = false
		return fmt.Errorf("session did not record expected verification status %q", expectedStatus)
	}
	result.Score.ExpectedVerificationSeen = true
	if task.ExpectNoEdits {
		finalDiff, err := liveEvalWorkspaceDiff(workspace)
		if err != nil {
			return err
		}
		if finalDiff != initialDiff {
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
		switch liveEvalRawString(event["type"]) {
		case "final_response", "assistant_message", "turn_complete":
		default:
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

func liveEvalWorkspaceDiff(workspace string) (string, error) {
	statusCmd := exec.Command("git", "status", "--porcelain=v1")
	statusCmd.Dir = workspace
	status, err := statusCmd.Output()
	if err != nil {
		return "", err
	}
	diffCmd := exec.Command("git", "diff", "--no-ext-diff", "--")
	diffCmd.Dir = workspace
	diff, err := diffCmd.Output()
	if err != nil {
		return "", err
	}
	return "status:\n" + string(status) + "diff:\n" + string(diff), nil
}

func writeLiveEvalWorkspace(workspace string, files, uncommittedFiles map[string]string) error {
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
	for path, body := range uncommittedFiles {
		fullPath := filepath.Join(workspace, filepath.Clean(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			return err
		}
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
			Name:        "js_package_script_fix",
			Category:    "js_ts",
			Description: "Fix a dependency-free JavaScript package test through an npm verification script.",
			Files: map[string]string{
				"package.json": `{
  "name": "lcagent-live-js",
  "private": true,
  "type": "module",
  "scripts": {
    "test": "node test.mjs"
  }
}
`,
				"slug.js": `export function slug(input) {
  return input;
}
`,
				"test.mjs": `import assert from "node:assert/strict";
import { slug } from "./slug.js";

assert.equal(slug("Hello, Little Control Room!"), "hello-little-control-room");
assert.equal(slug("  Already---Slugged  "), "already-slugged");
`,
			},
			Prompt: strings.TrimSpace(`The JavaScript package test is failing. Fix slug.js without adding dependencies. Then run npm test with run_command purpose set to verify, and finish with final_response listing slug.js and npm test.`),
			ExpectedContains: map[string]string{
				"slug.js": "toLowerCase",
			},
			VerifyCommand: []string{"npm", "test"},
			ExpectFiles:   []string{"slug.js"},
		},
		{
			Name:        "python_unittest_fix",
			Category:    "python",
			Description: "Fix a small Python function and verify with stdlib unittest.",
			Files: map[string]string{
				"textutil.py": `def initials(name: str) -> str:
    return name[:1].upper()
`,
				"test_textutil.py": `import unittest

from textutil import initials


class InitialsTest(unittest.TestCase):
    def test_multiple_words(self):
        self.assertEqual(initials("Little Control Room"), "LCR")

    def test_extra_spaces(self):
        self.assertEqual(initials("  agent   runtime  "), "AR")


if __name__ == "__main__":
    unittest.main()
`,
			},
			Prompt: strings.TrimSpace(`The Python unittest suite is failing. Fix textutil.py so initials returns the uppercase initials of each word. Then run python -m unittest with run_command purpose set to verify, and finish with final_response listing textutil.py and python -m unittest.`),
			ExpectedContains: map[string]string{
				"textutil.py": "split",
			},
			VerifyCommand: []string{"python3", "-m", "unittest"},
			ExpectFiles:   []string{"textutil.py"},
		},
		{
			Name:        "rust_cargo_fix",
			Category:    "rust",
			Description: "Fix a small Rust library bug and verify with cargo test.",
			Files: map[string]string{
				"Cargo.toml": `[package]
name = "lcagent-live-rust"
version = "0.1.0"
edition = "2021"

[dependencies]
`,
				"src/lib.rs": `pub fn is_even(value: i32) -> bool {
    value % 2 == 1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_even_numbers() {
        assert!(is_even(0));
        assert!(is_even(42));
        assert!(!is_even(7));
    }
}
`,
			},
			Prompt: strings.TrimSpace(`The Rust tests are failing. Fix src/lib.rs, then run cargo test with run_command purpose set to verify. Finish with final_response listing src/lib.rs and cargo test.`),
			ExpectedContains: map[string]string{
				"src/lib.rs": "% 2 == 0",
			},
			VerifyCommand: []string{"cargo", "test"},
			ExpectFiles:   []string{"src/lib.rs"},
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
		{
			Name:        "current_diff_review",
			Category:    "review",
			Description: "Read-only review of a seeded uncommitted regression with failing verification.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-review\n\ngo 1.22\n",
				"tasks.go": `package tasks

type Task struct {
	Title string
	Due   int
}

func DueNow(tasks []Task, cutoff int) []Task {
	var out []Task
	for _, task := range tasks {
		if task.Due <= cutoff {
			out = append(out, task)
		}
	}
	return out
}
`,
				"tasks_test.go": `package tasks

import (
	"reflect"
	"testing"
)

func TestDueNowIncludesBoundary(t *testing.T) {
	tasks := []Task{
		{Title: "done", Due: 5},
		{Title: "now", Due: 10},
		{Title: "later", Due: 11},
	}
	got := taskTitles(DueNow(tasks, 10))
	want := []string{"done", "now"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DueNow titles = %#v, want %#v", got, want)
	}
}

func taskTitles(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}
`,
			},
			UncommittedFiles: map[string]string{
				"tasks.go": `package tasks

type Task struct {
	Title string
	Due   int
}

func DueNow(tasks []Task, cutoff int) []Task {
	var out []Task
	for _, task := range tasks {
		if task.Due < cutoff {
			out = append(out, task)
		}
	}
	return out
}
`,
			},
			Prompt:                     strings.TrimSpace(`Review the current uncommitted diff for a bug. Do not edit files and do not fix the bug. Inspect the diff and relevant code, then run go test ./... with run_command purpose set to verify. Finish with final_response that leaves files_changed empty, reports the failed verification, and explains the regression you found.`),
			ExpectedVerificationStatus: "failed",
			VerifyCommand:              []string{"go", "test", "./..."},
			VerifyCommandExpectFailure: true,
			ExpectNoEdits:              true,
		},
		{
			Name:        "multi_file_price_refactor",
			Category:    "refactor",
			Description: "Refactor a public field across multiple implementation files and tests.",
			Files: map[string]string{
				"go.mod": "module lcagent-live-refactor\n\ngo 1.22\n",
				"item.go": `package catalog

type Item struct {
	SKU   string
	Name  string
	Price int
}

func NewItem(sku, name string, price int) Item {
	return Item{SKU: sku, Name: name, Price: price}
}
`,
				"cart.go": `package catalog

func Total(items []Item) int {
	total := 0
	for _, item := range items {
		total += item.Price
	}
	return total
}
`,
				"receipt.go": `package catalog

import "fmt"

func ReceiptLine(item Item) string {
	return fmt.Sprintf("%s: %d", item.Name, item.Price)
}
`,
				"catalog_test.go": `package catalog

import "testing"

func TestPricingUsesCents(t *testing.T) {
	item := NewItem("coffee", "Coffee", 1250)
	if item.PriceCents != 1250 {
		t.Fatalf("PriceCents = %d, want 1250", item.PriceCents)
	}
}

func TestTotalUsesCents(t *testing.T) {
	items := []Item{
		NewItem("coffee", "Coffee", 1250),
		NewItem("tea", "Tea", 375),
	}
	if got := Total(items); got != 1625 {
		t.Fatalf("Total() = %d, want 1625", got)
	}
}

func TestReceiptLineFormatsCents(t *testing.T) {
	if got := ReceiptLine(NewItem("coffee", "Coffee", 1250)); got != "Coffee: $12.50" {
		t.Fatalf("ReceiptLine() = %q", got)
	}
}
`,
			},
			Prompt: strings.TrimSpace(`Refactor the implementation from Item.Price to Item.PriceCents so the existing tests pass. Keep NewItem accepting the price in cents, update all implementation files that still use the old field, then run go test ./... with run_command purpose set to verify. Finish with final_response listing item.go, cart.go, and receipt.go in files_changed plus the verification command.`),
			ExpectedContains: map[string]string{
				"item.go":    "PriceCents int",
				"cart.go":    "item.PriceCents",
				"receipt.go": "item.PriceCents",
			},
			VerifyCommand: []string{"go", "test", "./..."},
			ExpectFiles:   []string{"item.go", "cart.go", "receipt.go"},
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
			fmt.Fprintf(stdout, "  score: correctness=%v verified=%v verification_status=%s expected_verification=%v trace_quality=%d/%s reads=%d/%d overlap=%d denials=%d feedback=%d tokens=%d cost=%.6f\n",
				result.Score.Correctness,
				result.Score.Verified,
				result.Score.VerificationStatus,
				result.Score.ExpectedVerificationSeen,
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
