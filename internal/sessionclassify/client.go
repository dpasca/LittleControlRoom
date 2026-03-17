package sessionclassify

import (
	"bytes"
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
	"lcroom/internal/model"
)

type OpenAIClient struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
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

type openAIResponseEnvelope struct {
	Status            string `json:"status"`
	Model             string `json:"model"`
	MaxOutputTokens   *int64 `json:"max_output_tokens"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage *struct {
		InputTokens        int64 `json:"input_tokens"`
		InputTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokens        int64 `json:"output_tokens"`
		OutputTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
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

func NewOpenAIClientFromEnv() *OpenAIClient {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil
	}

	model := strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar))
	if model == "" {
		model = DefaultModel
	}

	endpoint := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/responses"

	return &OpenAIClient{
		apiKey:   apiKey,
		model:    model,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: classifierHTTPTimeout,
		},
	}
}

func (c *OpenAIClient) ModelName() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *OpenAIClient) Classify(ctx context.Context, snapshot SessionSnapshot) (Result, error) {
	if c == nil || c.apiKey == "" {
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
	reqBody := map[string]any{
		"model": c.model,
		"input": []any{
			map[string]any{
				"role": "system",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": sessionClassificationInstructions,
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": "Classify this latest coding-session snapshot:\n\n" + string(snapshotJSON),
					},
				},
			},
		},
		"reasoning": map[string]any{
			"effort": attempt.ReasoningEffort,
		},
		"store": false,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "session_state_classification",
				"strict": true,
				"schema": map[string]any{
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
				},
			},
		},
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, fmt.Errorf("marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("send openai request: %w", err)
		if retryable := retryableTransportClassificationError(ctx, err); retryable != nil {
			return Result{}, retryable
		}
		return Result{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("openai responses api %s: %s", resp.Status, strings.TrimSpace(string(body)))
		if isRetryableHTTPStatus(resp.StatusCode) {
			return Result{}, &retryableClassificationError{
				cause: err,
				delay: retryDelayFromHeader(resp.Header.Get("Retry-After")),
			}
		}
		return Result{}, err
	}

	var envelope openAIResponseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Result{}, fmt.Errorf("decode openai response: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return Result{}, errors.New(envelope.Error.Message)
	}

	outputText := responseAssistantOutputText(envelope)
	if outputText == "" {
		err := missingAssistantOutputError(envelope)
		if strings.EqualFold(strings.TrimSpace(envelope.Status), "incomplete") {
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
	result.Model = strings.TrimSpace(envelope.Model)
	if envelope.Usage != nil {
		result.Usage = model.LLMUsage{
			InputTokens:       envelope.Usage.InputTokens,
			OutputTokens:      envelope.Usage.OutputTokens,
			TotalTokens:       envelope.Usage.TotalTokens,
			CachedInputTokens: envelope.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens:   envelope.Usage.OutputTokensDetails.ReasoningTokens,
		}
	}
	return result, nil
}

func responseAssistantOutputText(envelope openAIResponseEnvelope) string {
	for _, item := range envelope.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				return content.Text
			}
		}
	}
	return ""
}

func missingAssistantOutputError(envelope openAIResponseEnvelope) error {
	status := strings.TrimSpace(envelope.Status)
	if strings.EqualFold(status, "incomplete") {
		details := []string{"status=incomplete"}
		if envelope.IncompleteDetails != nil && strings.TrimSpace(envelope.IncompleteDetails.Reason) != "" {
			details = append(details, "reason="+strings.TrimSpace(envelope.IncompleteDetails.Reason))
		}
		if envelope.MaxOutputTokens != nil && *envelope.MaxOutputTokens > 0 {
			details = append(details, fmt.Sprintf("max_output_tokens=%d", *envelope.MaxOutputTokens))
		}
		if envelope.Usage != nil {
			if envelope.Usage.OutputTokens > 0 {
				details = append(details, fmt.Sprintf("output_tokens=%d", envelope.Usage.OutputTokens))
			}
			if envelope.Usage.OutputTokensDetails.ReasoningTokens > 0 {
				details = append(details, fmt.Sprintf("reasoning_tokens=%d", envelope.Usage.OutputTokensDetails.ReasoningTokens))
			}
		}
		return fmt.Errorf("openai response incomplete (%s)", strings.Join(details, ", "))
	}
	if status == "" {
		status = "unknown"
	}
	return fmt.Errorf("openai response missing assistant output (status=%s)", status)
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
