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
	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

type OpenAIClient struct {
	apiKey     string
	model      string
	endpoint   string
	httpClient *http.Client
	responses  llm.JSONSchemaRunner
}

const (
	classifierHTTPTimeout             = 60 * time.Second
	classifierPrimaryReasoningEffort  = "medium"
	classifierFallbackReasoningEffort = "low"
	classifierRepairReasoningEffort   = "low"
	classifierDefaultRetryBackoff     = 750 * time.Millisecond
	localRunnerDefaultModel           = "gpt-5.4-mini"
	localRunnerClaudeDefaultModel     = "haiku"
	classifierErrorPreviewLimit       = 120
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

	return &OpenAIClient{
		apiKey:     apiKey,
		model:      configuredClassifierModel(DefaultModel),
		httpClient: &http.Client{Timeout: classifierHTTPTimeout},
		responses:  llm.NewResponsesClient(apiKey, classifierHTTPTimeout, usage),
	}
}

func NewOpenAICompatibleClientWithUsageTracker(baseURL, apiKey, preferredModel string, usage *llm.UsageTracker) *OpenAIClient {
	model := strings.TrimSpace(preferredModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv(brand.SessionClassifierModelEnvVar))
	}
	return &OpenAIClient{
		model: model,
		responses: llm.NewOpenAICompatibleResponsesRunnerWithOptions(baseURL, apiKey, model, classifierHTTPTimeout, usage, llm.OpenAICompatibleResponsesRunnerOptions{
			PreferChatCompletions: true,
		}),
	}
}

func NewCodexClientWithUsageTracker(usage *llm.UsageTracker) *OpenAIClient {
	return NewCodexClientWithUsageTrackerInDataDir("", usage)
}

func NewCodexClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAIClient {
	return &OpenAIClient{
		model:     configuredClassifierModel(localRunnerDefaultModel),
		responses: llm.NewPersistentCodexRunnerInDataDir(dataDir, classifierHTTPTimeout, usage),
	}
}

func NewOpenCodeClientWithUsageTracker(usage *llm.UsageTracker) *OpenAIClient {
	return NewOpenCodeClientWithUsageTrackerInDataDir("", usage)
}

func NewOpenCodeClientWithUsageTrackerInDataDir(dataDir string, usage *llm.UsageTracker) *OpenAIClient {
	return &OpenAIClient{
		model:     configuredClassifierModel(localRunnerDefaultModel),
		responses: llm.NewOpenCodeRunRunnerInDataDir(dataDir, classifierHTTPTimeout, usage),
	}
}

func NewOpenCodeClientWithFallback(discovery *llm.OpenCodeDiscovery, tier config.ModelTier, usage *llm.UsageTracker) *OpenAIClient {
	baseRunner := llm.NewOpenCodeRunRunner(classifierHTTPTimeout, usage)
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
		model:     configuredClassifierModel(localRunnerClaudeDefaultModel),
		responses: llm.NewClaudePrintRunnerInDataDir(dataDir, classifierHTTPTimeout, usage),
	}
}

func configuredClassifierModel(defaultModel string) string {
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
	decodeErr := decodeClassifierOutput(outputText, &result)
	validateErr := error(nil)
	if decodeErr == nil {
		validateErr = validateClassificationResult(&result)
	}
	if decodeErr == nil && validateErr == nil {
		result.Model = strings.TrimSpace(response.Model)
		result.Usage = response.Usage
		return result, nil
	}

	repairResult, repairResponse, repairErr := c.repairClassifierOutput(ctx, snapshotJSON, outputText, firstNonNilError(decodeErr, validateErr))
	if repairErr == nil {
		repairResult.Model = firstNonEmptyString(strings.TrimSpace(repairResponse.Model), strings.TrimSpace(response.Model))
		repairResult.Usage = addLLMUsage(response.Usage, repairResponse.Usage)
		return repairResult, nil
	}

	return Result{}, &retryableClassificationError{
		cause: errors.Join(firstNonNilError(decodeErr, validateErr), repairErr),
	}
}

func validateClassificationResult(result *Result) error {
	if result == nil {
		return errors.New("classifier result missing")
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Confidence < 0 || result.Confidence > 1 {
		result.Confidence = 0
	}
	switch result.Category {
	case model.SessionCategoryCompleted,
		model.SessionCategoryBlocked,
		model.SessionCategoryWaitingForUser,
		model.SessionCategoryNeedsFollowUp,
		model.SessionCategoryInProgress,
		model.SessionCategoryUnknown:
	default:
		if strings.TrimSpace(string(result.Category)) == "" {
			return errors.New("classifier result missing category")
		}
		return fmt.Errorf("classifier result has invalid category %q", result.Category)
	}
	if result.Summary == "" {
		return errors.New("classifier result missing summary")
	}
	return nil
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
		},
		"required": []string{"category", "summary"},
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

func (c *OpenAIClient) repairClassifierOutput(ctx context.Context, snapshotJSON []byte, outputText string, previousErr error) (Result, llm.JSONSchemaResponse, error) {
	response, err := c.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           c.model,
		SystemText:      sessionClassificationRepairInstructions,
		UserText:        buildClassifierRepairPrompt(snapshotJSON, outputText, previousErr),
		SchemaName:      "session_state_classification",
		Schema:          sessionClassificationSchema(),
		ReasoningEffort: classifierRepairReasoningEffort,
	})
	if err != nil {
		return Result{}, llm.JSONSchemaResponse{}, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return Result{}, response, missingAssistantOutputError(response)
	}

	var repaired Result
	if err := decodeClassifierOutput(response.OutputText, &repaired); err != nil {
		return Result{}, response, err
	}
	if err := validateClassificationResult(&repaired); err != nil {
		return Result{}, response, err
	}
	return repaired, response, nil
}

const sessionClassificationInstructions = `You classify the latest state of a coding assistant session for a project dashboard.

Return exactly one JSON object that matches the response schema.
Do not wrap the JSON in markdown fences.
Do not include any prose before or after the JSON object.

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
Treat optional follow-up offers like “if you want, I can also ...” as optional unless the user actually asked for that extra step or the assistant says it still must happen.
If the latest assistant message asks the user to choose between options, confirm a proposed plan, approve a next step, or answer a direct implementation question, prefer waiting_for_user over completed.
Proposal handoffs count as waiting_for_user when the next meaningful action depends on the user's choice, even if the assistant includes a recommendation like “I’d go with 2”.
Use completed only when the assistant can stop without a reply from the user; if the assistant is clearly waiting for the user's answer before proceeding, do not mark completed.
Reasoning/tool transcript items can reflect earlier planning; when they conflict with a later user-visible assistant message, trust the latest user-visible assistant message.
If a transcript item contains "[...]", treat it as middle-content compaction for brevity, not evidence that the assistant stopped mid-sentence.
If the latest assistant message says requested repo actions already happened (for example committed, pushed, built, deployed, or published) and the git snapshot agrees, prefer completed over needs_follow_up.
Return a short factual dashboard summary under 140 characters.
Prefer brief direct phrasing over full sentences when natural.
Write from the implicit assistant point of view rather than naming the assistant as the subject.
Omit leading scaffolding like "Assistant is" or "The assistant is".
Do not force a stock opener; choose the most direct wording that fits the evidence.`

const sessionClassificationRepairInstructions = `You repair a previous classifier answer so it exactly matches the required response schema.

Return exactly one JSON object that matches the response schema.
Do not wrap the JSON in markdown fences.
Do not include any prose before or after the JSON object.
Preserve the meaning of the previous answer when possible.
If the previous answer omitted required fields or used the wrong property names, correct it.
If the previous answer is unusable, infer the best answer from the provided snapshot.`

func buildClassifierRepairPrompt(snapshotJSON []byte, outputText string, previousErr error) string {
	var b strings.Builder
	b.WriteString("Original snapshot:\n\n")
	b.Write(snapshotJSON)
	b.WriteString("\n\nPrevious invalid model output:\n\n")
	b.WriteString(strings.TrimSpace(outputText))
	if previousErr != nil {
		b.WriteString("\n\nWhy it was invalid:\n\n")
		b.WriteString(strings.TrimSpace(previousErr.Error()))
	}
	b.WriteString("\n\nReturn corrected JSON now.")
	return b.String()
}

func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	idx := strings.Index(s, "\n")
	if idx == -1 {
		return s
	}
	content := s[idx+1:]
	if strings.HasSuffix(content, "```") {
		content = strings.TrimSuffix(content, "```")
	}
	return strings.TrimSpace(content)
}

func stripThinkingBlocks(s string) string {
	s = strings.TrimSpace(s)
	for {
		if !strings.HasPrefix(s, "<think>") {
			return s
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			return s
		}
		s = strings.TrimSpace(s[end+len("</think>"):])
	}
}

func decodeClassifierOutput(outputText string, result *Result) error {
	sanitized := strings.TrimSpace(outputText)
	if sanitized == "" {
		return errors.New("decode classifier result: empty JSON output")
	}

	candidates := make([]string, 0, 4)
	candidates = append(candidates, sanitized)
	if thinkingStripped := stripThinkingBlocks(sanitized); thinkingStripped != "" && thinkingStripped != sanitized {
		candidates = append(candidates, thinkingStripped)
	}
	if fenced := stripMarkdownCodeBlock(sanitized); fenced != "" && fenced != sanitized {
		candidates = append(candidates, fenced)
	}
	for _, extracted := range extractJSONObjectCandidates(sanitized) {
		if extracted != sanitized {
			candidates = append(candidates, extracted)
		}
	}
	if len(candidates) > 1 {
		for _, extracted := range extractJSONObjectCandidates(candidates[1]) {
			if extracted != candidates[1] {
				candidates = append(candidates, extracted)
			}
		}
	}

	var firstErr error
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if err := json.Unmarshal([]byte(candidate), result); err == nil {
			return nil
		} else if firstErr == nil {
			firstErr = err
		}
	}

	preview := clippedSingleLinePreview(sanitized, classifierErrorPreviewLimit)
	if firstErr == nil {
		return fmt.Errorf("decode classifier result: failed to decode JSON output (preview=%q)", preview)
	}
	return fmt.Errorf("decode classifier result: failed to decode JSON output (preview=%q): %w", preview, firstErr)
}

func extractFirstJSONObject(text string) string {
	candidates := extractJSONObjectCandidates(text)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func extractJSONObjectCandidates(text string) []string {
	start := -1
	depth := 0
	inString := false
	escaped := false
	candidates := make([]string, 0, 2)

	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = inString
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				candidate := strings.TrimSpace(text[start : i+1])
				if candidate != "" {
					candidates = append(candidates, candidate)
				}
				start = -1
			}
		}
	}

	return candidates
}

func clippedSingleLinePreview(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit == 1 {
		return text[:1]
	}
	return strings.TrimSpace(text[:limit-1]) + "…"
}

func addLLMUsage(a, b model.LLMUsage) model.LLMUsage {
	return model.LLMUsage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		TotalTokens:       a.TotalTokens + b.TotalTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
		ReasoningTokens:   a.ReasoningTokens + b.ReasoningTokens,
		EstimatedCostUSD:  a.EstimatedCostUSD + b.EstimatedCostUSD,
	}
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
