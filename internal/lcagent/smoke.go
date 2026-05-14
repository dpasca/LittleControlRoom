package lcagent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func runSmoke(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var provider, model, envFile, dataDir, outputRaw, autoRaw, reasoningEffort string
	var requestTimeout time.Duration
	var maxTurns int
	var keepTemp bool
	fs.StringVar(&provider, "provider", "openrouter", "provider: openrouter, openai, deepseek, or moonshot")
	fs.StringVar(&model, "model", "", "optional model name; blank uses provider default")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root; blank uses the Little Control Room data dir")
	fs.StringVar(&outputRaw, "output", "text", "output: text or json")
	fs.StringVar(&autoRaw, "auto", "low", "autonomy: low or medium")
	fs.StringVar(&reasoningEffort, "reasoning-effort", "", "optional provider reasoning effort")
	fs.DurationVar(&requestTimeout, "request-timeout", 10*time.Minute, "provider HTTP request timeout")
	fs.IntVar(&maxTurns, "max-turns", 6, "maximum model turns")
	fs.BoolVar(&keepTemp, "keep-temp", false, "keep the temporary smoke workspace")
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
