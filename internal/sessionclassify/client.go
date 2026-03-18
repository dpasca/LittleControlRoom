package sessionclassify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

type OpenAIClient struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
	responses  *llm.ResponsesClient
}

const (
	classifierHTTPTimeout             = 60 * time.Second
	classifierPrimaryReasoningEffort  = "medium"
	classifierFallbackReasoningEffort = "minimal"
	classifierDefaultRetryBackoff     = 750 * time.Millisecond
)

var classifierAttemptPlan = []classifierAttemptConfig{
	{
		ReasoningEffort: classifierPrimaryReasoningEffort,
	},
	{
		ReasoningEffort: classifierFallbackReasoningEffort,
	},
}

type classifierAttemptConfig struct {
	ReasoningEffort string
}

type retryableClassificationError struct {
	cause error
	delay time.Duration
}

func (e *retryableClassificationError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *retryableClassificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func NewOpenAIClient(apiKey string) *OpenAIClient {
	return NewOpenAIClientWithUsageTracker(apiKey, nil)
}

func NewOpenAIClientWithUsageTracker(apiKey string, usage *llm.UsageTracker) *OpenAIClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	model := strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar))
	if model == "" {
		model = DefaultModel
	}

	return &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: classifierHTTPTimeout},
		responses:  llm.NewResponsesClient(apiKey, classifierHTTPTimeout, usage),
	}
}

func (c *OpenAIClient) ModelName() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *OpenAIClient) Classify(ctx context.Context, snapshot SessionSnapshot) (Result, error) {
	if c == nil || c.responsesClient() == nil {
		return Result{}, errors.New("openai client not configured")
	}

	snapshotJSON, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal session snapshot: %w", err)
	}

	for attemptIndex, attempt := range classifierAttemptPlan {
		result, err := c.classifyAttempt(ctx, snapshotJSON, attempt)
		if err == nil {
			return result, nil
		}
		if !isRetryableClassificationError(err) || attemptIndex == len(classifierAttemptPlan)-1 {
			return Result{}, err
		}
		if err := sleepContext(ctx, retryDelayForClassificationError(err)); err != nil {
			return Result{}, err
		}
	}

	return Result{}, errors.New("classifier attempt plan exhausted")
}

func (c *OpenAIClient) classifyAttempt(ctx context.Context, snapshotJSON []byte, attempt classifierAttemptConfig) (Result, error) {
	response, err := c.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           c.model,
		SystemText:      sessionClassificationInstructions,
		UserText:        "Classify this latest coding-session snapshot:\n\n" + string(snapshotJSON),
		SchemaName:      "session_state_classification",
		Schema:          sessionClassificationSchema(),
		ReasoningEffort: attempt.ReasoningEffort,
	})
	if err != nil {
		var httpErr *llm.HTTPStatusError
		if errors.As(err, &httpErr) && isRetryableHTTPStatus(httpErr.StatusCode) {
			return Result{}, &retryableClassificationError{
				cause: err,
				delay: retryDelayFromHeader(httpErr.RetryAfter),
			}
		}
		if retryable := retryableTransportClassificationError(ctx, err); retryable != nil {
			return Result{}, retryable
		}
		return Result{}, err
	}

	outputText := response.OutputText
	if outputText == "" {
		err := missingAssistantOutputError(response)
		if strings.EqualFold(strings.TrimSpace(response.Status), "incomplete") {
			return Result{}, &retryableClassificationError{cause: err}
		}
		return Result{}, err
	}

	var result Result
	if err := json.Unmarshal([]byte(outputText), &result); err != nil {
		return Result{}, fmt.Errorf("decode classifier result: %w", err)
	}
	if result.Category == "" {
		result.Category = model.SessionCategoryUnknown
	}
	result.Model = strings.TrimSpace(response.Model)
	result.Usage = response.Usage
	return result, nil
}

func (c *OpenAIClient) responsesClient() *llm.ResponsesClient {
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

func missingAssistantOutputError(response llm.JSONSchemaResponse) error {
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

func sessionClassificationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"category": map[string]any{
				"type": "string",
				"enum": []string{
					string(model.SessionCategoryCompleted),
					string(model.SessionCategoryBlocked),
					string(model.SessionCategoryWaitingForUser),
					string(model.SessionCategoryNeedsFollowUp),
					string(model.SessionCategoryInProgress),
					string(model.SessionCategoryUnknown),
				},
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "One concise dashboard-ready summary under 140 characters; brief fragments are fine, write from the implicit assistant point of view, and omit prefixes like 'Assistant is'.",
			},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
		},
		"required": []string{"category", "summary", "confidence"},
	}
}

func isRetryableHTTPStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func retryableTransportClassificationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if !isRetryableTransportError(err) {
		return nil
	}
	return &retryableClassificationError{
		cause: err,
		delay: classifierDefaultRetryBackoff,
	}
}

func isRetryableTransportError(err error) bool {
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
		errors.Is(err, syscall.EHOSTDOWN) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETDOWN) ||
		errors.Is(err, syscall.ENETRESET) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT)
}

func retryDelayForClassificationError(err error) time.Duration {
	var retryable *retryableClassificationError
	if errors.As(err, &retryable) && retryable.delay > 0 {
		return retryable.delay
	}
	return 0
}

func isRetryableClassificationError(err error) bool {
	var retryable *retryableClassificationError
	return errors.As(err, &retryable)
}

func retryDelayFromHeader(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return classifierDefaultRetryBackoff
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
	return classifierDefaultRetryBackoff
}

func sleepContext(ctx context.Context, d time.Duration) error {
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

const sessionClassificationInstructions = `You classify the latest state of a coding assistant session for a project dashboard.

Choose exactly one category:
- completed: the requested work appears complete for now; optional future ideas do not make it incomplete
- blocked: work stopped because of an unresolved blocker, failure, or dependency
- waiting_for_user: the assistant explicitly needs input, approval, credentials, or a decision from the user
- needs_follow_up: work is not blocked, but there is a concrete unfinished next step that should likely happen next
- in_progress: the session looks mid-flight with no clear handoff yet
- unknown: there is not enough evidence

Focus on the latest user and assistant messages, not the full project history.
Also consider the brief git_status snapshot as supporting context.
If latest_turn_state_known is true, treat latest_turn_completed as a strong workflow signal:
- true usually means the assistant finished that turn, even if the repo is still dirty
- false means the assistant may still be mid-turn unless the transcript clearly shows a handoff
Dirty or unsynced git state can be evidence of unfinished follow-up, but transcript evidence should remain primary.
Do not label a session in_progress only because the worktree is dirty after a completed turn.
Prefer completed when the assistant clearly wrapped up the asked task and any extra offer is optional.
Return a short factual dashboard summary under 140 characters.
Prefer brief direct phrasing over full sentences when natural.
Write from the implicit assistant point of view rather than naming the assistant as the subject.
Omit leading scaffolding like "Assistant is" or "The assistant is".
Do not force a stock opener; choose the most direct wording that fits the evidence.`
