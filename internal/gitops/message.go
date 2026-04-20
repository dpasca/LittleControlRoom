package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/llm"
)

const (
	defaultCommitModel           = "gpt-5.4-mini"
	defaultClaudeCommitModel     = "haiku"
	defaultCommitReasoningEffort = "low"
	commitAssistantHTTPTimeout   = 45 * time.Second
	commitAssistantRetryBackoff  = 750 * time.Millisecond
)

var commitAssistantAttemptPlan = []commitAssistantAttemptConfig{
	{ReasoningEffort: defaultCommitReasoningEffort},
	{ReasoningEffort: defaultCommitReasoningEffort},
}

type commitAssistantAttemptConfig struct {
	ReasoningEffort string
}

type CommitMessageInput struct {
	Intent                  string          `json:"intent"`
	ProjectName             string          `json:"project_name"`
	Branch                  string          `json:"branch,omitempty"`
	StageMode               string          `json:"stage_mode"`
	LatestSessionSummary    string          `json:"latest_session_summary,omitempty"`
	IncludedFiles           []string        `json:"included_files"`
	SuggestedUntrackedFiles []string        `json:"suggested_untracked_files,omitempty"`
	ExcludedFiles           []string        `json:"excluded_files,omitempty"`
	DiffStat                string          `json:"diff_stat,omitempty"`
	Patch                   string          `json:"patch,omitempty"`
	OpenTodos               []CommitTodoRef `json:"open_todos,omitempty"`
}

type CommitTodoRef struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type CommitMessageSuggestion struct {
	Message          string
	Model            string
	CompletedTodoIDs []int64
}

type CommitMessageSuggester interface {
	Suggest(context.Context, CommitMessageInput) (CommitMessageSuggestion, error)
	ModelName() string
}

type OpenAICommitMessageClient struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
	responses  llm.JSONSchemaRunner
}

const errCommitAssistantNotConfigured = "commit assistant not configured for selected AI backend"

func NewOpenAICommitMessageClient(apiKey string) *OpenAICommitMessageClient {
	return NewOpenAICommitMessageClientWithUsageTracker(apiKey, nil)
}

func NewOpenAICommitMessageClientWithUsageTracker(apiKey string, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	model := strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	if model == "" {
		model = defaultCommitModel
	}

	return &OpenAICommitMessageClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: commitAssistantHTTPTimeout},
		responses:  llm.NewResponsesClient(apiKey, commitAssistantHTTPTimeout, usage),
	}
}

func NewOpenAICompatibleCommitMessageClientWithUsageTracker(baseURL, apiKey, preferredModel string, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	model := strings.TrimSpace(preferredModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	}
	return &OpenAICommitMessageClient{
		model: model,
		responses: llm.NewOpenAICompatibleResponsesRunnerWithOptions(baseURL, apiKey, model, commitAssistantHTTPTimeout, usage, llm.OpenAICompatibleResponsesRunnerOptions{
			PreferChatCompletions: true,
		}),
	}
}

func NewCodexCommitMessageClientWithUsageTracker(usage *llm.UsageTracker) *OpenAICommitMessageClient {
	return NewCodexCommitMessageClientWithUsageTrackerInDataDir("", usage)
}

func NewCodexCommitMessageClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	model := strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	if model == "" {
		model = defaultCommitModel
	}
	return &OpenAICommitMessageClient{
		model:     model,
		responses: llm.NewPersistentCodexRunnerInDataDir(dataDir, commitAssistantHTTPTimeout, usage),
	}
}

func NewOpenCodeCommitMessageClientWithUsageTracker(usage *llm.UsageTracker) *OpenAICommitMessageClient {
	return NewOpenCodeCommitMessageClientWithUsageTrackerInDataDir("", usage)
}

func NewOpenCodeCommitMessageClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	model := strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	if model == "" {
		model = defaultCommitModel
	}
	return &OpenAICommitMessageClient{
		model:     model,
		responses: llm.NewOpenCodeRunRunnerInDataDir(dataDir, commitAssistantHTTPTimeout, usage),
	}
}

func NewOpenCodeCommitMessageClientWithFallback(discovery *llm.OpenCodeDiscovery, tier config.ModelTier, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	baseRunner := llm.NewOpenCodeRunRunner(commitAssistantHTTPTimeout, usage)
	cfg := llm.DefaultModelSelectionConfig()
	cfg.Tier = llm.ModelTier(tier)
	fallbackRunner := llm.NewFallbackRunner(discovery, baseRunner, cfg, usage)
	return &OpenAICommitMessageClient{
		model:     "",
		responses: fallbackRunner,
	}
}

func NewClaudeCommitMessageClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAICommitMessageClient {
	model := strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	if model == "" {
		model = defaultClaudeCommitModel
	}
	return &OpenAICommitMessageClient{
		model:     model,
		responses: llm.NewClaudePrintRunnerInDataDir(dataDir, commitAssistantHTTPTimeout, usage),
	}
}

func (c *OpenAICommitMessageClient) ModelName() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *OpenAICommitMessageClient) Suggest(ctx context.Context, input CommitMessageInput) (CommitMessageSuggestion, error) {
	if c == nil || c.responsesClient() == nil {
		return CommitMessageSuggestion{}, errors.New(errCommitAssistantNotConfigured)
	}
	if strings.TrimSpace(input.ProjectName) == "" && strings.TrimSpace(input.Branch) != "" {
		input.ProjectName = filepath.Base(strings.TrimSpace(input.Branch))
	}

	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("marshal commit message input: %w", err)
	}

	hasTodos := len(input.OpenTodos) > 0
	schema := commitMessageSchema(hasTodos)
	instructions := commitMessageInstructions
	if hasTodos {
		instructions += "\n" + commitMessageTodoInstructions
	}

	response, err := c.runJSONSchemaPrompt(
		ctx,
		instructions,
		"Draft a git commit subject for this coding task snapshot:\n\n"+string(payload),
		"git_commit_message",
		schema,
	)
	if err != nil {
		return CommitMessageSuggestion{}, err
	}

	var decoded struct {
		Message          string  `json:"message"`
		CompletedTodoIDs []int64 `json:"completed_todo_ids"`
	}
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &decoded); err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("decode commit message result: %w", err)
	}
	message := strings.TrimSpace(decoded.Message)
	if message == "" {
		return CommitMessageSuggestion{}, errors.New("commit message suggestion was empty")
	}
	return CommitMessageSuggestion{
		Message:          message,
		Model:            response.Model,
		CompletedTodoIDs: decoded.CompletedTodoIDs,
	}, nil
}

func (c *OpenAICommitMessageClient) runJSONSchemaPrompt(ctx context.Context, systemText, userText, schemaName string, schema map[string]any) (llm.JSONSchemaResponse, error) {
	for attemptIndex, attempt := range commitAssistantAttemptPlan {
		response, err := c.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
			Model:           c.model,
			SystemText:      systemText,
			UserText:        userText,
			SchemaName:      schemaName,
			Schema:          schema,
			ReasoningEffort: attempt.ReasoningEffort,
		})
		if err != nil {
			if retryable := retryableCommitAssistantErrorForRun(ctx, err); retryable != nil && attemptIndex < len(commitAssistantAttemptPlan)-1 {
				if err := sleepCommitAssistantRetry(ctx, retryDelayForCommitAssistantError(retryable)); err != nil {
					return llm.JSONSchemaResponse{}, err
				}
				continue
			}
			return llm.JSONSchemaResponse{}, err
		}
		if strings.TrimSpace(response.OutputText) == "" {
			err := missingCommitAssistantOutputError(response)
			if strings.EqualFold(strings.TrimSpace(response.Status), "incomplete") && attemptIndex < len(commitAssistantAttemptPlan)-1 {
				if err := sleepCommitAssistantRetry(ctx, commitAssistantRetryBackoff); err != nil {
					return llm.JSONSchemaResponse{}, err
				}
				continue
			}
			return llm.JSONSchemaResponse{}, err
		}
		return response, nil
	}
	return llm.JSONSchemaResponse{}, errors.New("commit assistant attempt plan exhausted")
}

func (c *OpenAICommitMessageClient) responsesClient() llm.JSONSchemaRunner {
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

type retryableCommitAssistantError struct {
	cause error
	delay time.Duration
}

func (e *retryableCommitAssistantError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *retryableCommitAssistantError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func retryableCommitAssistantErrorForRun(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var httpErr *llm.HTTPStatusError
	if errors.As(err, &httpErr) && isRetryableCommitAssistantHTTPStatus(httpErr.StatusCode) {
		return &retryableCommitAssistantError{
			cause: err,
			delay: commitAssistantRetryDelayFromHeader(httpErr.RetryAfter),
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if !isRetryableCommitAssistantTransportError(err) {
		return nil
	}
	return &retryableCommitAssistantError{
		cause: err,
		delay: commitAssistantRetryBackoff,
	}
}

func isRetryableCommitAssistantHTTPStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func isRetryableCommitAssistantTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EADDRNOTAVAIL) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.EHOSTDOWN) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETDOWN) ||
		errors.Is(err, syscall.ENETRESET) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT)
}

func retryDelayForCommitAssistantError(err error) time.Duration {
	var retryable *retryableCommitAssistantError
	if errors.As(err, &retryable) && retryable.delay > 0 {
		return retryable.delay
	}
	return 0
}

func commitAssistantRetryDelayFromHeader(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return commitAssistantRetryBackoff
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		wait := time.Until(when)
		if wait < 0 {
			return 0
		}
		return wait
	}
	return commitAssistantRetryBackoff
}

func sleepCommitAssistantRetry(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func missingCommitAssistantOutputError(response llm.JSONSchemaResponse) error {
	status := strings.TrimSpace(response.Status)
	if strings.EqualFold(status, "incomplete") {
		details := []string{"status=incomplete"}
		if strings.TrimSpace(response.IncompleteReason) != "" {
			details = append(details, "reason="+strings.TrimSpace(response.IncompleteReason))
		}
		if response.MaxOutputTokens != nil && *response.MaxOutputTokens > 0 {
			details = append(details, fmt.Sprintf("max_output_tokens=%d", *response.MaxOutputTokens))
		}
		if response.Usage.OutputTokens > 0 {
			details = append(details, fmt.Sprintf("output_tokens=%d", response.Usage.OutputTokens))
		}
		if response.Usage.ReasoningTokens > 0 {
			details = append(details, fmt.Sprintf("reasoning_tokens=%d", response.Usage.ReasoningTokens))
		}
		return fmt.Errorf("openai response incomplete (%s)", strings.Join(details, ", "))
	}
	if status == "" {
		status = "unknown"
	}
	return fmt.Errorf("openai response missing assistant output (status=%s)", status)
}

const commitMessageInstructions = `You write short git commit subject lines for coding work.

Use the provided task summary, file list, suggested untracked files, diff stat, and patch when present.
Treat suggested_untracked_files as part of the proposed commit even though they are not staged yet.
Prefer a single concise subject line under 72 characters.
Use imperative mood when it fits naturally.
Prefer the main user-facing or workflow outcome over an inventory of touched subsystems.
Avoid subjects that read like changelogs, such as lists joined with commas, "and", or semicolons.
Avoid mentioning tests or docs unless they are the primary change.
Do not add prefixes like feat:, fix:, chore:, or trailing punctuation unless the input strongly implies an existing convention.
Return only the JSON fields requested by the schema.`

const commitMessageTodoInstructions = `The input includes open_todos: a list of the project's open TODO items with their IDs.
Examine the diff, file list, and session summary to determine whether this commit addresses or completes any of those TODOs.
Return the IDs of TODOs that this commit clearly addresses in completed_todo_ids.
Be conservative: only include a TODO if the changes directly and substantially address it.
Return an empty array if no TODOs are addressed.`

func commitMessageSchema(includeTodos bool) map[string]any {
	props := map[string]any{
		"message": map[string]any{
			"type":        "string",
			"description": "One concise git commit subject line.",
		},
	}
	required := []string{"message"}
	if includeTodos {
		props["completed_todo_ids"] = map[string]any{
			"type":        "array",
			"description": "IDs of open_todos that this commit addresses.",
			"items":       map[string]any{"type": "integer"},
		}
		required = append(required, "completed_todo_ids")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
		"required":             required,
	}
}
