package todoworktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

const (
	DefaultModel                  = "gpt-5.4-mini"
	localRunnerDefaultModel       = "gpt-5.4-mini"
	localRunnerClaudeDefaultModel = "haiku"
	suggestionHTTPTimeout         = 45 * time.Second
	suggestionPrimaryReasoning    = "low"
	defaultOpenSiblingTodoContext = 5
)

type Request struct {
	ProjectPath      string
	ProjectName      string
	TodoID           int64
	TodoText         string
	OpenSiblingTodos []string
}

type Result struct {
	BranchName     string  `json:"branch_name"`
	WorktreeSuffix string  `json:"worktree_suffix"`
	Kind           string  `json:"kind"`
	Reason         string  `json:"reason"`
	Confidence     float64 `json:"confidence"`
	Model          string
	Usage          model.LLMUsage
}

type Suggester interface {
	Suggest(ctx context.Context, req Request) (Result, error)
}

type OpenAIClient struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
	responses  llm.JSONSchemaRunner
}

func NewOpenAIClient(apiKey string) *OpenAIClient {
	return NewOpenAIClientWithUsageTracker(apiKey, nil)
}

func NewOpenAIClientWithUsageTracker(apiKey string, usage *llm.UsageTracker) *OpenAIClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	return &OpenAIClient{
		apiKey:     apiKey,
		model:      configuredModel(DefaultModel),
		httpClient: &http.Client{Timeout: suggestionHTTPTimeout},
		responses:  llm.NewResponsesClient(apiKey, suggestionHTTPTimeout, usage),
	}
}

func NewOpenAICompatibleClientWithUsageTracker(baseURL, apiKey, preferredModel string, usage *llm.UsageTracker) *OpenAIClient {
	model := strings.TrimSpace(preferredModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar))
	}
	return &OpenAIClient{
		model:     model,
		responses: llm.NewOpenAICompatibleResponsesRunner(baseURL, apiKey, model, suggestionHTTPTimeout, usage),
	}
}

func NewCodexClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAIClient {
	return &OpenAIClient{
		model:     configuredModel(localRunnerDefaultModel),
		responses: llm.NewPersistentCodexRunnerInDataDir(dataDir, suggestionHTTPTimeout, usage),
	}
}

func NewOpenCodeClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAIClient {
	return &OpenAIClient{
		model:     configuredModel(localRunnerDefaultModel),
		responses: llm.NewOpenCodeRunRunnerInDataDir(dataDir, suggestionHTTPTimeout, usage),
	}
}

func NewOpenCodeClientWithFallback(discovery *llm.OpenCodeDiscovery, tier config.ModelTier, usage *llm.UsageTracker) *OpenAIClient {
	baseRunner := llm.NewOpenCodeRunRunner(suggestionHTTPTimeout, usage)
	cfg := llm.DefaultModelSelectionConfig()
	cfg.Tier = llm.ModelTier(tier)
	fallbackRunner := llm.NewFallbackRunner(discovery, baseRunner, cfg, usage)
	return &OpenAIClient{
		model:     "",
		responses: fallbackRunner,
	}
}

func NewClaudeClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAIClient {
	return &OpenAIClient{
		model:     configuredModel(localRunnerClaudeDefaultModel),
		responses: llm.NewClaudePrintRunnerInDataDir(dataDir, suggestionHTTPTimeout, usage),
	}
}

func configuredModel(defaultModel string) string {
	model := strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar))
	if model != "" {
		return model
	}
	return strings.TrimSpace(defaultModel)
}

func (c *OpenAIClient) ModelName() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *OpenAIClient) Suggest(ctx context.Context, req Request) (Result, error) {
	if c == nil || c.responsesClient() == nil {
		return Result{}, errors.New("todo worktree suggester not configured")
	}
	payload, err := json.MarshalIndent(struct {
		ProjectPath      string   `json:"project_path"`
		ProjectName      string   `json:"project_name"`
		TodoID           int64    `json:"todo_id"`
		TodoText         string   `json:"todo_text"`
		OpenSiblingTodos []string `json:"open_sibling_todos,omitempty"`
	}{
		ProjectPath:      strings.TrimSpace(req.ProjectPath),
		ProjectName:      strings.TrimSpace(req.ProjectName),
		TodoID:           req.TodoID,
		TodoText:         strings.TrimSpace(req.TodoText),
		OpenSiblingTodos: compactSiblingTodos(req.OpenSiblingTodos),
	}, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal todo worktree request: %w", err)
	}

	response, err := c.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           c.model,
		SystemText:      suggestionInstructions,
		UserText:        "Suggest a branch and worktree naming pair for this TODO:\n\n" + string(payload),
		SchemaName:      "todo_worktree_suggestion",
		Schema:          suggestionSchema(),
		ReasoningEffort: suggestionPrimaryReasoning,
	})
	if err != nil {
		return Result{}, err
	}
	outputText := stripMarkdownCodeBlock(strings.TrimSpace(response.OutputText))
	if outputText == "" {
		return Result{}, errors.New("todo worktree suggester returned no assistant output")
	}

	var result Result
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return Result{}, fmt.Errorf("decode todo worktree suggestion: %w", err)
	}
	if err := validateResult(&result); err != nil {
		return Result{}, err
	}
	result.Model = strings.TrimSpace(response.Model)
	result.Usage = response.Usage
	return result, nil
}

func compactSiblingTodos(items []string) []string {
	out := make([]string, 0, min(len(items), defaultOpenSiblingTodoContext))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= defaultOpenSiblingTodoContext {
			break
		}
	}
	return out
}

func (c *OpenAIClient) responsesClient() llm.JSONSchemaRunner {
	if c == nil {
		return nil
	}
	if c.responses != nil {
		return c.responses
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return nil
	}
	return llm.NewResponsesClientWithHTTPClient(c.apiKey, c.endpoint, c.httpClient, nil)
}

func validateResult(result *Result) error {
	if result == nil {
		return errors.New("todo worktree suggestion missing")
	}
	result.BranchName = strings.TrimSpace(result.BranchName)
	result.WorktreeSuffix = strings.TrimSpace(result.WorktreeSuffix)
	result.Kind = strings.TrimSpace(result.Kind)
	result.Reason = strings.TrimSpace(result.Reason)
	if result.BranchName == "" {
		return errors.New("todo worktree suggestion missing branch_name")
	}
	if result.WorktreeSuffix == "" {
		return errors.New("todo worktree suggestion missing worktree_suffix")
	}
	if result.Reason == "" {
		return errors.New("todo worktree suggestion missing reason")
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return fmt.Errorf("todo worktree suggestion has invalid confidence %.4f", result.Confidence)
	}
	return nil
}

func suggestionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"branch_name": map[string]any{
				"type":        "string",
				"description": "A concise branch name for this TODO. Prefer lowercase slash-separated git branch style.",
			},
			"worktree_suffix": map[string]any{
				"type":        "string",
				"description": "A concise folder suffix for the worktree path. Prefer lowercase hyphen-separated text.",
			},
			"kind": map[string]any{
				"type":        "string",
				"description": "Short task kind such as feature, bugfix, docs, chore, or spike.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "One sentence explaining the suggestion.",
			},
			"confidence": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     1,
				"description": "Confidence between 0 and 1.",
			},
		},
		"required": []string{"branch_name", "worktree_suffix", "kind", "reason", "confidence"},
	}
}

const suggestionInstructions = `You suggest branch and worktree names for coding TODO items.

Rules:
- Decide intent from the TODO text and project context semantically. Do not rely on brittle literal keyword matching.
- Produce concise, readable names that are specific to the task.
- Avoid generic names such as fix/bugfix, feat/update, chore/cleanup, or worktree-task.
- Prefer branch names that would still make sense in git history one week later.
- Prefer lowercase text.
- Use slash-separated branch style for branch_name.
- Use hyphen-separated folder style for worktree_suffix.
- Keep the branch and worktree naming aligned with each other.
- If the TODO contains a typo or misspelling, infer the intended meaning rather than copying the typo literally.
- When there are sibling open TODOs, use them only to avoid ambiguity, not to combine tasks.

Return only valid JSON matching the schema.`

func stripMarkdownCodeBlock(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSpace(text)
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		firstLine := strings.TrimSpace(text[:newline])
		if !strings.HasPrefix(firstLine, "{") && !strings.HasPrefix(firstLine, "[") {
			text = text[newline+1:]
		}
	}
	if end := strings.LastIndex(text, "```"); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text)
}
