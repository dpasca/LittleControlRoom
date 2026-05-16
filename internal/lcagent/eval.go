package lcagent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/lcagent/sessionmetrics"
)

type evalCase struct {
	Name        string
	Prompt      string
	Auto        string
	Files       map[string]string
	Script      string
	ExpectError bool
	Setup       func(cwd, dataDir string) ([]string, error)
	Check       func(sessionmetrics.Summary) error
}

type evalReport struct {
	Passed  bool                   `json:"passed"`
	Cases   []evalCaseResult       `json:"cases"`
	Summary sessionmetrics.Summary `json:"summary"`
	DataDir string                 `json:"data_dir,omitempty"`
}

type evalCaseResult struct {
	Name     string                 `json:"name"`
	Passed   bool                   `json:"passed"`
	Error    string                 `json:"error,omitempty"`
	Sessions int                    `json:"sessions"`
	Metrics  sessionmetrics.Summary `json:"metrics"`
}

func runEval(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var outputRaw, dataDir string
	var keepTemp bool
	fs.StringVar(&outputRaw, "output", "text", "output: text or json")
	fs.StringVar(&dataDir, "data-dir", "", "optional eval artifact data root")
	fs.BoolVar(&keepTemp, "keep-temp", false, "keep temporary eval workspaces")
	if err := fs.Parse(args); err != nil {
		return err
	}
	outputRaw = strings.ToLower(strings.TrimSpace(outputRaw))
	if outputRaw == "" {
		outputRaw = "text"
	}
	if outputRaw != "text" && outputRaw != "json" {
		return fmt.Errorf("unsupported eval output mode: %s", outputRaw)
	}

	root, err := os.MkdirTemp("", "lcagent-eval-*")
	if err != nil {
		return err
	}
	if !keepTemp {
		defer os.RemoveAll(root)
	}
	if dataDir == "" {
		dataDir = filepath.Join(root, "data")
	}
	cases := lcagentEvalCases()
	report := evalReport{Passed: true, DataDir: dataDir}
	var producedFiles []string
	for _, tc := range cases {
		result, files := runEvalCase(root, dataDir, tc)
		producedFiles = append(producedFiles, files...)
		if !result.Passed {
			report.Passed = false
		}
		report.Cases = append(report.Cases, result)
	}
	if len(producedFiles) > 0 {
		report.Summary, err = sessionmetrics.AnalyzeFiles(producedFiles)
		if err != nil {
			return err
		}
	}
	switch outputRaw {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	default:
		writeEvalTextReport(stdout, report)
	}
	if !report.Passed {
		return fmt.Errorf("lcagent eval failed")
	}
	return nil
}

func runEvalCase(root, dataDir string, tc evalCase) (evalCaseResult, []string) {
	result := evalCaseResult{Name: tc.Name}
	cwd := filepath.Join(root, tc.Name, "repo")
	scriptPath := filepath.Join(root, tc.Name, "script.jsonl")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		result.Error = err.Error()
		return result, nil
	}
	for path, body := range tc.Files {
		fullPath := filepath.Join(cwd, filepath.Clean(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			result.Error = err.Error()
			return result, nil
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			result.Error = err.Error()
			return result, nil
		}
	}
	if err := os.WriteFile(scriptPath, []byte(tc.Script), 0o600); err != nil {
		result.Error = err.Error()
		return result, nil
	}
	extraArgs := []string{}
	if tc.Setup != nil {
		var err error
		extraArgs, err = tc.Setup(cwd, dataDir)
		if err != nil {
			result.Error = err.Error()
			return result, nil
		}
	}
	before, _ := lcagentEvalSessionFileSet(dataDir)
	execArgs := []string{
		"--cwd", cwd,
		"--data-dir", dataDir,
		"--auto", firstEvalNonEmpty(tc.Auto, "low"),
		"--output", "stream-json",
		"--script", scriptPath,
	}
	execArgs = append(execArgs, extraArgs...)
	execArgs = append(execArgs, firstEvalNonEmpty(tc.Prompt, tc.Name))
	var stream bytes.Buffer
	runErr := runExec(execArgs, &stream)
	files, listErr := lcagentEvalNewSessionFiles(dataDir, before)
	if listErr != nil {
		result.Error = listErr.Error()
		return result, files
	}
	if tc.ExpectError {
		if runErr == nil {
			result.Error = "expected run error, got success"
			return result, files
		}
	} else if runErr != nil {
		result.Error = runErr.Error()
		return result, files
	}
	summary, err := sessionmetrics.AnalyzeFiles(files)
	if err != nil {
		result.Error = err.Error()
		return result, files
	}
	result.Sessions = summary.Sessions
	result.Metrics = summary
	if tc.Check != nil {
		if err := tc.Check(summary); err != nil {
			result.Error = err.Error()
			return result, files
		}
	}
	result.Passed = true
	return result, files
}

func lcagentEvalCases() []evalCase {
	return []evalCase{
		{
			Name:   "scripted_patch_verify",
			Prompt: "patch README and report verification",
			Auto:   "low",
			Files: map[string]string{
				"README.md": "old\n",
			},
			Script: `{"type":"tool_call","tool":"apply_patch","args":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch\n"}}
{"type":"final_response","summary":"patched README","files_changed":["README.md"],"verification":["scripted go test placeholder"]}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.PatchDiffSummaries < 1 {
					return fmt.Errorf("patch diff summary count = %d, want >= 1", summary.PatchDiffSummaries)
				}
				if summary.VerificationStatuses["reported_only"] < 1 {
					return fmt.Errorf("verification reported_only count = %d, want >= 1", summary.VerificationStatuses["reported_only"])
				}
				return nil
			},
		},
		{
			Name:        "policy_denial_trace",
			Prompt:      "try a denied command",
			Auto:        "off",
			ExpectError: true,
			Script: `{"type":"tool_call","tool":"run_command","args":{"argv":["go","test","./..."],"shell":false,"purpose":"verify"}}
{"type":"final_response","summary":"should not reach final"}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.PermissionDenials < 1 {
					return fmt.Errorf("permission denial count = %d, want >= 1", summary.PermissionDenials)
				}
				if summary.VerificationCheckStatuses["denied"] < 1 {
					return fmt.Errorf("verification denied count = %d, want >= 1", summary.VerificationCheckStatuses["denied"])
				}
				if summary.VerificationFeedback < 1 {
					return fmt.Errorf("verification feedback count = %d, want >= 1", summary.VerificationFeedback)
				}
				return nil
			},
		},
		{
			Name:        "patch_failure_feedback",
			Prompt:      "try a stale patch",
			Auto:        "low",
			ExpectError: true,
			Files: map[string]string{
				"README.md": "current\nkeep\n",
			},
			Script: `{"type":"tool_call","tool":"apply_patch","args":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n keep\n*** End Patch\n"}}
{"type":"final_response","summary":"should not reach final"}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.PatchFeedback < 1 {
					return fmt.Errorf("patch feedback count = %d, want >= 1", summary.PatchFeedback)
				}
				return nil
			},
		},
		{
			Name:   "replace_text_fallback_trace",
			Prompt: "use exact replacement fallback",
			Auto:   "low",
			Files: map[string]string{
				"README.md": "before\nkeep\n",
			},
			Script: `{"type":"tool_call","tool":"replace_text","args":{"path":"README.md","old_text":"before\n","new_text":"after\n"}}
{"type":"final_response","summary":"replaced README text","files_changed":["README.md"],"verification":["not run: deterministic edit fixture"]}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.ToolCalls["replace_text"] < 1 || summary.ToolResults["replace_text"] < 1 {
					return fmt.Errorf("replace_text calls/results = %d/%d, want >= 1", summary.ToolCalls["replace_text"], summary.ToolResults["replace_text"])
				}
				if summary.PatchDiffSummaries < 1 {
					return fmt.Errorf("patch diff summary count = %d, want >= 1", summary.PatchDiffSummaries)
				}
				if summary.VerificationStatuses["reported_only"] < 1 {
					return fmt.Errorf("verification reported_only count = %d, want >= 1", summary.VerificationStatuses["reported_only"])
				}
				return nil
			},
		},
		{
			Name:   "low_autonomy_go_test_verification",
			Prompt: "run the narrow verification command",
			Auto:   "low",
			Files: map[string]string{
				"go.mod": "module lcagent-eval-low\n\ngo 1.22\n",
				"smoke_test.go": `package smoke

import "testing"

func TestSmoke(t *testing.T) {}
`,
			},
			Script: `{"type":"tool_call","tool":"run_command","args":{"argv":["go","test","./..."],"shell":false,"timeout_ms":120000,"purpose":"verify"}}
{"type":"final_response","summary":"go tests passed","files_changed":[],"verification":["go test ./..."]}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.ToolCalls["run_command"] < 1 || summary.ToolResults["run_command"] < 1 {
					return fmt.Errorf("run_command calls/results = %d/%d, want >= 1", summary.ToolCalls["run_command"], summary.ToolResults["run_command"])
				}
				if summary.VerificationChecks < 1 || summary.VerificationCheckStatuses["passed"] < 1 {
					return fmt.Errorf("verification checks = %d statuses %#v, want passed", summary.VerificationChecks, summary.VerificationCheckStatuses)
				}
				if summary.VerificationStatuses["verified"] < 1 {
					return fmt.Errorf("verification verified count = %d, want >= 1", summary.VerificationStatuses["verified"])
				}
				return nil
			},
		},
		{
			Name:        "failed_verification_trace",
			Prompt:      "run a failing verification command",
			Auto:        "low",
			ExpectError: true,
			Files: map[string]string{
				"go.mod": "module lcagent-eval-fail\n\ngo 1.22\n",
				"smoke_test.go": `package smoke

import "testing"

func TestSmoke(t *testing.T) { t.Fatal("intentional failure") }
`,
			},
			Script: `{"type":"tool_call","tool":"run_command","args":{"argv":["go","test","./..."],"shell":false,"timeout_ms":120000,"purpose":"verify"}}
{"type":"final_response","summary":"should not reach final","files_changed":[],"verification":["go test ./..."]}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.VerificationCheckStatuses["failed"] < 1 {
					return fmt.Errorf("verification failed count = %d, want >= 1", summary.VerificationCheckStatuses["failed"])
				}
				if summary.VerificationFeedback < 1 {
					return fmt.Errorf("verification feedback count = %d, want >= 1", summary.VerificationFeedback)
				}
				return nil
			},
		},
		{
			Name:   "missing_verification_contract",
			Prompt: "patch without verification to exercise the contract",
			Auto:   "low",
			Files: map[string]string{
				"README.md": "before\n",
			},
			Script: `{"type":"tool_call","tool":"apply_patch","args":{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-before\n+after\n*** End Patch\n"}}
{"type":"final_response","summary":"patched without verification","files_changed":["README.md"],"verification":[]}
`,
			Check: func(summary sessionmetrics.Summary) error {
				if summary.VerificationStatuses["missing_after_changes"] < 1 {
					return fmt.Errorf("missing_after_changes count = %d, want >= 1", summary.VerificationStatuses["missing_after_changes"])
				}
				return nil
			},
		},
		{
			Name:   "resume_context_trace",
			Prompt: "continue with summarized context",
			Auto:   "low",
			Script: `{"type":"final_response","summary":"continued from context","files_changed":[],"verification":[]}
`,
			Setup: func(cwd, dataDir string) ([]string, error) {
				sessionID := "lca_eval_resume_source"
				started := time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC)
				if err := writeLCAgentEvalResumeArtifact(dataDir, cwd, sessionID, started); err != nil {
					return nil, err
				}
				return []string{"--resume", sessionID}, nil
			},
			Check: func(summary sessionmetrics.Summary) error {
				if summary.ResumeContexts < 1 {
					return fmt.Errorf("resume context count = %d, want >= 1", summary.ResumeContexts)
				}
				return nil
			},
		},
	}
}

func writeEvalTextReport(stdout io.Writer, report evalReport) {
	passed := 0
	for _, result := range report.Cases {
		if result.Passed {
			passed++
		}
	}
	fmt.Fprintf(stdout, "LCAgent eval: %d/%d passed\n", passed, len(report.Cases))
	for _, result := range report.Cases {
		status := "FAIL"
		if result.Passed {
			status = "PASS"
		}
		fmt.Fprintf(stdout, "- %s %s", status, result.Name)
		if result.Error != "" {
			fmt.Fprintf(stdout, ": %s", result.Error)
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "sessions=%d denials=%d patch_diff_summaries=%d patch_feedback=%d repair_feedback_suppressed=%d resume_contexts=%d verification_feedback=%d verification=%v\n",
		report.Summary.Sessions,
		report.Summary.PermissionDenials,
		report.Summary.PatchDiffSummaries,
		report.Summary.PatchFeedback,
		report.Summary.RepairFeedbackSuppressed,
		report.Summary.ResumeContexts,
		report.Summary.VerificationFeedback,
		report.Summary.VerificationStatuses,
	)
}

func lcagentEvalSessionFileSet(dataDir string) (map[string]struct{}, error) {
	files, err := lcagentEvalSessionFiles(dataDir)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(files))
	for _, file := range files {
		set[file] = struct{}{}
	}
	return set, nil
}

func lcagentEvalNewSessionFiles(dataDir string, before map[string]struct{}) ([]string, error) {
	files, err := lcagentEvalSessionFiles(dataDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, file := range files {
		if _, ok := before[file]; !ok {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out, nil
}

func lcagentEvalSessionFiles(dataDir string) ([]string, error) {
	root := filepath.Join(dataDir, "lcagent", "sessions")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func writeLCAgentEvalResumeArtifact(dataDir, cwd, sessionID string, started time.Time) error {
	path := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	events := []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": cwd, "started_at": started.Format(time.RFC3339Nano)},
		{"type": "user_message", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "message": "previous eval task"},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "summary": "previous eval summary", "verification_status": "reported_only", "verification": []string{"scripted"}},
	}
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func firstEvalNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
