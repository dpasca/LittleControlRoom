package lcagent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/lcagent/sessionmetrics"
)

type smokeReport struct {
	Passed    bool                   `json:"passed"`
	Workspace string                 `json:"workspace"`
	DataDir   string                 `json:"data_dir"`
	Artifact  string                 `json:"artifact,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Kept      bool                   `json:"kept"`
	Metrics   sessionmetrics.Summary `json:"metrics,omitempty"`
}

type browserSmokeReport struct {
	Passed                   bool                              `json:"passed"`
	Workspace                string                            `json:"workspace"`
	DataDir                  string                            `json:"data_dir"`
	Artifact                 string                            `json:"artifact,omitempty"`
	SessionID                string                            `json:"session_id,omitempty"`
	ManagedBrowserSessionKey string                            `json:"managed_browser_session_key"`
	BrowserProfileKey        string                            `json:"browser_profile_key"`
	URL                      string                            `json:"url"`
	ScreenshotPath           string                            `json:"screenshot_path,omitempty"`
	State                    browserctl.ManagedPlaywrightState `json:"state,omitempty"`
	WorkerStopped            bool                              `json:"worker_stopped"`
	BrowserProcessStopped    bool                              `json:"browser_process_stopped"`
	Kept                     bool                              `json:"kept"`
}

func runSmoke(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var provider, model, envFile, dataDir, outputRaw, autoRaw, reasoningEffort, browserLaunchModeRaw string
	var requestTimeout time.Duration
	var maxTurns int
	var keepTemp, browserSmoke bool
	fs.StringVar(&provider, "provider", "openrouter", "provider: openrouter, openai, deepseek, or moonshot")
	fs.StringVar(&model, "model", "", "optional model name; blank uses provider default")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root; blank uses the Little Control Room data dir")
	fs.StringVar(&outputRaw, "output", "text", "output: text or json")
	fs.StringVar(&autoRaw, "auto", "low", "autonomy: low or medium")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "optional provider reasoning effort")
	fs.StringVar(&browserLaunchModeRaw, "browser-launch-mode", string(browserctl.ManagedLaunchModeHeadless), "browser smoke launch mode: headless, headed, or background")
	fs.DurationVar(&requestTimeout, "request-timeout", 10*time.Minute, "provider HTTP request timeout")
	fs.IntVar(&maxTurns, "max-turns", 6, "maximum model turns")
	fs.BoolVar(&keepTemp, "keep-temp", false, "keep the temporary smoke workspace")
	fs.BoolVar(&browserSmoke, "browser", false, "run a scripted managed-browser smoke test against a local page")
	if err := fs.Parse(args); err != nil {
		return err
	}
	outputRaw = strings.ToLower(strings.TrimSpace(outputRaw))
	if outputRaw == "" {
		outputRaw = "text"
	}
	if outputRaw != "text" && outputRaw != "json" {
		return fmt.Errorf("unsupported smoke output mode: %s", outputRaw)
	}
	if browserSmoke {
		if dataDir == "" {
			dataDir = defaultDataDir()
		}
		return runBrowserSmoke(stdout, browserSmokeConfig{
			DataDir:    dataDir,
			Output:     outputRaw,
			KeepTemp:   keepTemp,
			LaunchMode: browserctl.ManagedLaunchMode(browserLaunchModeRaw).Normalize(),
		})
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "openrouter"
	}
	if provider == "scripted" {
		return fmt.Errorf("lcagent smoke requires a live provider")
	}
	if dataDir == "" {
		dataDir = defaultDataDir()
	}

	workspace, err := os.MkdirTemp("", "lcagent-live-smoke-*")
	if err != nil {
		return err
	}
	if !keepTemp {
		defer os.RemoveAll(workspace)
	}
	if err := writeSmokeWorkspace(workspace); err != nil {
		return err
	}

	execArgs := []string{
		"--cwd", workspace,
		"--data-dir", dataDir,
		"--auto", firstSmokeNonEmpty(autoRaw, "low"),
		"--output", "json",
		"--provider", provider,
		"--tool-profile", "balanced",
		"--context-profile", "balanced",
		"--request-timeout", requestTimeout.String(),
		"--max-turns", fmt.Sprintf("%d", maxTurns),
	}
	if model = strings.TrimSpace(model); model != "" {
		execArgs = append(execArgs, "--model", model)
	}
	if envFile = strings.TrimSpace(envFile); envFile != "" {
		execArgs = append(execArgs, "--env-file", envFile)
	}
	if reasoningEffort = strings.TrimSpace(reasoningEffort); reasoningEffort != "" {
		execArgs = append(execArgs, "--reasoning-effort", reasoningEffort)
	}
	execArgs = append(execArgs, smokePrompt())

	var execOut bytes.Buffer
	if err := runExec(execArgs, &execOut); err != nil {
		return err
	}
	var execResult struct {
		SessionID string `json:"session_id"`
		Artifact  string `json:"artifact"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(execOut.Bytes(), &execResult); err != nil {
		return fmt.Errorf("decode smoke exec result: %w", err)
	}
	report := smokeReport{
		Passed:    true,
		Workspace: workspace,
		DataDir:   dataDir,
		Artifact:  execResult.Artifact,
		SessionID: execResult.SessionID,
		Kept:      keepTemp,
	}
	if execResult.Artifact != "" {
		summary, err := sessionmetrics.AnalyzeFiles([]string{execResult.Artifact})
		if err != nil {
			return err
		}
		report.Metrics = summary
	}
	readme, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(readme), "LCAgent smoke: updated") {
		report.Passed = false
		return writeSmokeReport(stdout, report, outputRaw, fmt.Errorf("README.md did not contain the expected smoke line"))
	}
	if report.Metrics.VerificationStatuses["verified"] < 1 {
		report.Passed = false
		return writeSmokeReport(stdout, report, outputRaw, fmt.Errorf("smoke session did not record verified run_command checks"))
	}
	return writeSmokeReport(stdout, report, outputRaw, nil)
}

type browserSmokeConfig struct {
	DataDir    string
	Output     string
	KeepTemp   bool
	LaunchMode browserctl.ManagedLaunchMode
}

func runBrowserSmoke(stdout io.Writer, cfg browserSmokeConfig) error {
	workspace, err := os.MkdirTemp("", "lcagent-browser-smoke-*")
	if err != nil {
		return err
	}
	if !cfg.KeepTemp {
		defer os.RemoveAll(workspace)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><title>LCAgent Browser Smoke</title></head>
<body>
<h1>Browser Smoke Ready</h1>
<p id="status">Managed browser fixture loaded.</p>
<button type="button">Continue</button>
</body>
</html>`)
	}))
	defer server.Close()

	sessionKey := browserctl.NewManagedSessionKey()
	policy := browserctl.DefaultPolicy()
	profileKey := browserctl.ManagedProfileKey(policy, "lcagent", workspace, "", sessionKey)
	scriptPath := filepath.Join(workspace, "browser-smoke.jsonl")
	script := strings.Join([]string{
		fmt.Sprintf(`{"type":"tool_call","tool":"browser_navigate","args":{"url":%q}}`, server.URL),
		`{"type":"tool_call","tool":"browser_snapshot","args":{"max_chars":4000}}`,
		`{"type":"tool_call","tool":"browser_screenshot","args":{}}`,
		`{"type":"tool_call","tool":"browser_current_page","args":{}}`,
		`{"type":"final_response","summary":"Browser smoke completed","files_changed":[],"verification":["browser_navigate, browser_snapshot, browser_screenshot, and browser_current_page succeeded against a local fixture"]}`,
		"",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return err
	}

	execArgs := []string{
		"--cwd", workspace,
		"--data-dir", cfg.DataDir,
		"--auto", "off",
		"--output", "json",
		"--provider", "scripted",
		"--script", scriptPath,
		"--browser-control", "managed",
		"--browser-session-key", sessionKey,
		"--browser-profile-key", profileKey,
		"--browser-launch-mode", string(cfg.LaunchMode),
		"browser smoke",
	}
	var execOut bytes.Buffer
	report := browserSmokeReport{
		Passed:                   true,
		Workspace:                workspace,
		DataDir:                  cfg.DataDir,
		ManagedBrowserSessionKey: sessionKey,
		BrowserProfileKey:        profileKey,
		URL:                      server.URL,
		Kept:                     cfg.KeepTemp,
	}
	if err := runExec(execArgs, &execOut); err != nil {
		report.Passed = false
		_ = writeBrowserSmokeReport(stdout, report, cfg.Output, err)
		return err
	}
	var execResult struct {
		SessionID string `json:"session_id"`
		Artifact  string `json:"artifact"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(execOut.Bytes(), &execResult); err != nil {
		report.Passed = false
		return writeBrowserSmokeReport(stdout, report, cfg.Output, fmt.Errorf("decode browser smoke exec result: %w", err))
	}
	report.SessionID = execResult.SessionID
	report.Artifact = execResult.Artifact

	evidence, err := readBrowserSmokeEvidence(execResult.Artifact, server.URL)
	if err != nil {
		report.Passed = false
		return writeBrowserSmokeReport(stdout, report, cfg.Output, err)
	}
	report.ScreenshotPath = evidence.ScreenshotPath
	state, stateErr := browserctl.ReadManagedPlaywrightState(cfg.DataDir, sessionKey)
	if stateErr != nil {
		report.Passed = false
		return writeBrowserSmokeReport(stdout, report, cfg.Output, fmt.Errorf("read managed browser state: %w", stateErr))
	}
	report.State = state
	report.WorkerStopped = state.MCPPID == 0
	report.BrowserProcessStopped = state.BrowserPID <= 0 || waitForProcessExit(state.BrowserPID, 2*time.Second)
	if err := validateBrowserSmokeReport(report, evidence); err != nil {
		report.Passed = false
		return writeBrowserSmokeReport(stdout, report, cfg.Output, err)
	}
	return writeBrowserSmokeReport(stdout, report, cfg.Output, nil)
}

type browserSmokeEvidence struct {
	CapabilityEnabled bool
	Navigated         bool
	Snapshotted       bool
	Screenshot        bool
	CurrentPage       bool
	PageURL           string
	SnapshotText      string
	ScreenshotPath    string
}

func readBrowserSmokeEvidence(artifactPath, wantURL string) (browserSmokeEvidence, error) {
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		return browserSmokeEvidence{}, err
	}
	var evidence browserSmokeEvidence
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch smokeRawString(event["type"]) {
		case "browser_capability":
			evidence.CapabilityEnabled = smokeRawBool(event["enabled"])
		case "tool_call":
			switch smokeRawString(event["tool"]) {
			case "browser_navigate":
				evidence.Navigated = true
			case "browser_snapshot":
				evidence.Snapshotted = true
			case "browser_screenshot":
				evidence.Screenshot = true
			case "browser_current_page":
				evidence.CurrentPage = true
			}
		case "browser_page":
			if url := strings.TrimSpace(smokeRawString(event["url"])); url != "" {
				evidence.PageURL = url
			}
		case "tool_result":
			tool := smokeRawString(event["tool"])
			var result struct {
				Output       string `json:"output"`
				ArtifactPath string `json:"artifact_path"`
			}
			_ = json.Unmarshal(event["result"], &result)
			if tool == "browser_snapshot" {
				evidence.SnapshotText = result.Output
			}
			if tool == "browser_screenshot" {
				evidence.ScreenshotPath = strings.TrimSpace(result.ArtifactPath)
			}
		}
	}
	if evidence.PageURL == "" {
		evidence.PageURL = wantURL
	}
	return evidence, nil
}

func smokeRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func smokeRawBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return false
}

func validateBrowserSmokeReport(report browserSmokeReport, evidence browserSmokeEvidence) error {
	if !evidence.CapabilityEnabled {
		return fmt.Errorf("browser capability was not enabled")
	}
	if !evidence.Navigated || !evidence.Snapshotted || !evidence.Screenshot || !evidence.CurrentPage {
		return fmt.Errorf("browser smoke missing expected browser tool calls")
	}
	if !strings.Contains(evidence.PageURL, report.URL) {
		return fmt.Errorf("browser page URL %q did not match fixture %q", evidence.PageURL, report.URL)
	}
	if !strings.Contains(evidence.SnapshotText, "Browser Smoke Ready") {
		return fmt.Errorf("browser snapshot did not contain fixture heading")
	}
	if strings.TrimSpace(report.ScreenshotPath) == "" {
		return fmt.Errorf("browser screenshot did not report an artifact path")
	}
	if info, err := os.Stat(report.ScreenshotPath); err != nil {
		return fmt.Errorf("browser screenshot artifact missing: %w", err)
	} else if info.Size() == 0 {
		return fmt.Errorf("browser screenshot artifact is empty")
	}
	if report.State.Provider != "lcagent" || report.State.SessionKey != report.ManagedBrowserSessionKey || report.State.ProfileKey != report.BrowserProfileKey {
		return fmt.Errorf("managed browser state does not match smoke session")
	}
	if !report.WorkerStopped {
		return fmt.Errorf("browser worker process was still recorded as running")
	}
	if !report.BrowserProcessStopped {
		return fmt.Errorf("browser process still appears to be running")
	}
	return nil
}

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func writeBrowserSmokeReport(stdout io.Writer, report browserSmokeReport, outputRaw string, runErr error) error {
	switch outputRaw {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	default:
		status := "PASS"
		if !report.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(stdout, "LCAgent managed browser smoke: %s\n", status)
		fmt.Fprintf(stdout, "session=%s\nartifact=%s\ndata_dir=%s\nbrowser_session=%s\n", report.SessionID, report.Artifact, report.DataDir, report.ManagedBrowserSessionKey)
		if report.ScreenshotPath != "" {
			fmt.Fprintf(stdout, "screenshot=%s\n", report.ScreenshotPath)
		}
		fmt.Fprintf(stdout, "worker_stopped=%t browser_process_stopped=%t\n", report.WorkerStopped, report.BrowserProcessStopped)
		if report.Kept {
			fmt.Fprintf(stdout, "workspace=%s\n", report.Workspace)
		}
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func writeSmokeWorkspace(workspace string) error {
	files := map[string]string{
		"go.mod": "module lcagent-live-smoke\n\ngo 1.22\n",
		"smoke_test.go": `package smoke

import "testing"

func TestSmoke(t *testing.T) {}
`,
		"README.md": "# LCAgent live smoke\n\nInitial smoke workspace.\n",
	}
	for path, body := range files {
		fullPath := filepath.Join(workspace, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func smokePrompt() string {
	return strings.TrimSpace(`This is a Little Control Room live LCAgent smoke test.

Please update README.md by adding exactly this line:
LCAgent smoke: updated

Then run go test ./... with run_command purpose set to verify, and finish with final_response that lists README.md in files_changed and go test ./... in verification.`)
}

func firstSmokeNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func writeSmokeReport(stdout io.Writer, report smokeReport, outputRaw string, runErr error) error {
	switch outputRaw {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	default:
		status := "PASS"
		if !report.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(stdout, "LCAgent live smoke: %s\n", status)
		fmt.Fprintf(stdout, "session=%s\nartifact=%s\ndata_dir=%s\n", report.SessionID, report.Artifact, report.DataDir)
		if report.Kept {
			fmt.Fprintf(stdout, "workspace=%s\n", report.Workspace)
		}
		if len(report.Metrics.VerificationStatuses) > 0 {
			fmt.Fprintf(stdout, "verification=%v denials=%d patch_diff_summaries=%d\n", report.Metrics.VerificationStatuses, report.Metrics.PermissionDenials, report.Metrics.PatchDiffSummaries)
		}
	}
	if runErr != nil {
		return runErr
	}
	return nil
}
