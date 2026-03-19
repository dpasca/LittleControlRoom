package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

type JSONSchemaRunner interface {
	RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error)
}

type CodexExecRunner struct {
	timeout   time.Duration
	usage     *UsageTracker
	command   string
	tempDirFn func() string
}

type OpenCodeRunRunner struct {
	timeout   time.Duration
	usage     *UsageTracker
	command   string
	tempDirFn func() string
}

func NewCodexExecRunner(timeout time.Duration, usage *UsageTracker) *CodexExecRunner {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &CodexExecRunner{
		timeout:   timeout,
		usage:     usage,
		command:   "codex",
		tempDirFn: os.TempDir,
	}
}

func NewOpenCodeRunRunner(timeout time.Duration, usage *UsageTracker) *OpenCodeRunRunner {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OpenCodeRunRunner{
		timeout:   timeout,
		usage:     usage,
		command:   "opencode",
		tempDirFn: os.TempDir,
	}
}

func (r *CodexExecRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil {
		return JSONSchemaResponse{}, errors.New("codex exec runner not configured")
	}
	if strings.TrimSpace(req.Model) == "" {
		return JSONSchemaResponse{}, errors.New("codex exec runner requires a model")
	}
	if r.usage != nil {
		r.usage.Start(req.Model)
	}
	response, err := r.run(ctx, req)
	if err != nil {
		if r.usage != nil {
			r.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, err
	}
	if r.usage != nil {
		r.usage.Complete(response.Model, response.Usage)
	}
	return response, nil
}

func (r *CodexExecRunner) run(parent context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	ctx, cancel := withRunnerTimeout(parent, r.timeout)
	defer cancel()

	workDir, cleanup, err := createRunnerWorkspace(r.tempDirFn)
	if err != nil {
		return JSONSchemaResponse{}, err
	}
	defer cleanup()

	schemaPath := filepath.Join(workDir, "schema.json")
	schemaRaw, err := json.Marshal(req.Schema)
	if err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("marshal schema for codex exec: %w", err)
	}
	if err := os.WriteFile(schemaPath, schemaRaw, 0o600); err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("write codex schema file: %w", err)
	}

	args := []string{
		"exec",
		"--skip-git-repo-check",
		"--ephemeral",
		"--json",
		"--cd", workDir,
		"--output-schema", schemaPath,
		"--model", req.Model,
		buildSchemaPrompt(req, true),
	}
	stdout, stderr, err := runCommandWithOutput(ctx, r.command, args...)
	if err != nil {
		return JSONSchemaResponse{}, formatRunnerCommandError("codex exec", err, stderr, stdout)
	}
	return parseCodexExecJSONL(stdout, req.Model)
}

func (r *OpenCodeRunRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil {
		return JSONSchemaResponse{}, errors.New("opencode runner not configured")
	}
	if strings.TrimSpace(req.Model) == "" {
		return JSONSchemaResponse{}, errors.New("opencode runner requires a model")
	}
	if r.usage != nil {
		r.usage.Start(req.Model)
	}
	response, err := r.run(ctx, req)
	if err != nil {
		if r.usage != nil {
			r.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, err
	}
	if r.usage != nil {
		r.usage.Complete(response.Model, response.Usage)
	}
	return response, nil
}

func (r *OpenCodeRunRunner) run(parent context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	ctx, cancel := withRunnerTimeout(parent, r.timeout)
	defer cancel()

	workDir, cleanup, err := createRunnerWorkspace(r.tempDirFn)
	if err != nil {
		return JSONSchemaResponse{}, err
	}
	defer cleanup()

	args := []string{
		"run",
		"--format", "json",
		"--dir", workDir,
		"--model", openCodeModelArg(req.Model),
		buildSchemaPrompt(req, false),
	}
	stdout, stderr, err := runCommandWithOutput(ctx, r.command, args...)
	if err != nil {
		return JSONSchemaResponse{}, formatRunnerCommandError("opencode run", err, stderr, stdout)
	}
	return parseOpenCodeRunJSONL(stdout, req.Model)
}

func buildSchemaPrompt(req JSONSchemaRequest, schemaEnforced bool) string {
	schemaRaw, _ := json.MarshalIndent(req.Schema, "", "  ")
	lines := []string{
		"Use only the information provided below.",
		"Do not inspect local files, run tools, or infer extra context outside this prompt.",
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		lines = append(lines, "Reasoning effort preference: "+strings.TrimSpace(req.ReasoningEffort)+".")
	}
	if system := strings.TrimSpace(req.SystemText); system != "" {
		lines = append(lines, "System instructions:\n"+system)
	}
	if user := strings.TrimSpace(req.UserText); user != "" {
		lines = append(lines, "Task input:\n"+user)
	}
	if schemaEnforced {
		lines = append(lines, "Return only the final JSON object that matches the supplied schema exactly.")
	} else {
		lines = append(lines, "Return only valid JSON that matches this schema exactly:\n"+string(schemaRaw))
	}
	return strings.Join(lines, "\n\n")
}

func openCodeModelArg(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}
	if strings.Contains(modelName, "/") {
		return modelName
	}
	return "openai/" + modelName
}

func withRunnerTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	if _, ok := parent.Deadline(); ok || timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func createRunnerWorkspace(tempDirFn func() string) (string, func(), error) {
	root := os.TempDir()
	if tempDirFn != nil {
		root = tempDirFn()
	}
	workDir, err := os.MkdirTemp(root, "lcroom-ai-*")
	if err != nil {
		return "", nil, fmt.Errorf("create runner workspace: %w", err)
	}
	return workDir, func() { _ = os.RemoveAll(workDir) }, nil
}

func runCommandWithOutput(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func formatRunnerCommandError(label string, err error, stderr, stdout string) error {
	parts := []string{}
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 {
		return fmt.Errorf("%s failed: %w", label, err)
	}
	return fmt.Errorf("%s failed: %w: %s", label, err, strings.Join(parts, " | "))
}

func parseCodexExecJSONL(raw, requestedModel string) (JSONSchemaResponse, error) {
	response := JSONSchemaResponse{
		Status: "completed",
		Model:  strings.TrimSpace(requestedModel),
	}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage struct {
				InputTokens       int64 `json:"input_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
				ReasoningTokens   int64 `json:"reasoning_tokens"`
				TotalTokens       int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return JSONSchemaResponse{}, fmt.Errorf("decode codex exec output: %w", err)
		}
		switch event.Type {
		case "item.completed":
			if event.Item.Type == "agent_message" && strings.TrimSpace(event.Item.Text) != "" {
				response.OutputText = strings.TrimSpace(event.Item.Text)
			}
		case "turn.completed":
			response.Usage = model.LLMUsage{
				InputTokens:       event.Usage.InputTokens,
				OutputTokens:      event.Usage.OutputTokens,
				TotalTokens:       event.Usage.TotalTokens,
				CachedInputTokens: event.Usage.CachedInputTokens,
				ReasoningTokens:   event.Usage.ReasoningTokens,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("scan codex exec output: %w", err)
	}
	if response.Usage.TotalTokens == 0 && (response.Usage.InputTokens > 0 || response.Usage.OutputTokens > 0) {
		response.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
	}
	if response.Model == "" {
		response.Model = strings.TrimSpace(requestedModel)
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(response.Model, response.Usage); ok {
		response.Usage.EstimatedCostUSD = estimatedCostUSD
	}
	if response.OutputText == "" {
		return JSONSchemaResponse{}, errors.New("codex exec returned no assistant output")
	}
	return response, nil
}

func parseOpenCodeRunJSONL(raw, requestedModel string) (JSONSchemaResponse, error) {
	response := JSONSchemaResponse{
		Status: "completed",
		Model:  strings.TrimSpace(requestedModel),
	}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Part struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Reason string `json:"reason"`
				Tokens struct {
					Total     int64 `json:"total"`
					Input     int64 `json:"input"`
					Output    int64 `json:"output"`
					Reasoning int64 `json:"reasoning"`
					Cache     struct {
						Read int64 `json:"read"`
					} `json:"cache"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return JSONSchemaResponse{}, fmt.Errorf("decode opencode output: %w", err)
		}
		switch event.Type {
		case "text":
			if strings.TrimSpace(event.Part.Text) != "" {
				response.OutputText = strings.TrimSpace(event.Part.Text)
			}
		case "step_finish":
			response.Usage = model.LLMUsage{
				InputTokens:       event.Part.Tokens.Input,
				OutputTokens:      event.Part.Tokens.Output,
				TotalTokens:       event.Part.Tokens.Total,
				CachedInputTokens: event.Part.Tokens.Cache.Read,
				ReasoningTokens:   event.Part.Tokens.Reasoning,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("scan opencode output: %w", err)
	}
	if response.Model == "" {
		response.Model = strings.TrimSpace(requestedModel)
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(response.Model, response.Usage); ok {
		response.Usage.EstimatedCostUSD = estimatedCostUSD
	}
	if response.OutputText == "" {
		return JSONSchemaResponse{}, errors.New("opencode run returned no assistant output")
	}
	return response, nil
}
