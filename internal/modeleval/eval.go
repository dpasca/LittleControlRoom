package modeleval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
)

const defaultEvalTimeout = 90 * time.Second
const defaultOpenAIModel = "gpt-5.4-mini"

type Options struct {
	Backend config.AIBackend
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type Report struct {
	Backend       string
	BaseURL       string
	Model         string
	ContextWindow int64
	ContextDetail string
	StartedAt     time.Time
	FinishedAt    time.Time
	Duration      time.Duration
	Cases         []CaseResult
}

type CaseResult struct {
	Name            string
	Passed          bool
	Model           string
	Duration        time.Duration
	OutputPreview   string
	Error           string
	Usage           model.LLMUsage
	TokensPerSecond float64
}

func (r Report) Passed() bool {
	if len(r.Cases) == 0 {
		return false
	}
	for _, c := range r.Cases {
		if !c.Passed {
			return false
		}
	}
	return true
}

func Run(ctx context.Context, opts Options) (Report, error) {
	opts = normalizeOptions(opts)
	textRunner, jsonRunner, err := runnersForOptions(opts)
	if err != nil {
		return Report{}, err
	}
	if textRunner == nil || jsonRunner == nil {
		return Report{}, errors.New("model eval requires text and JSON runners")
	}

	modelName, err := resolveEvalModel(ctx, opts)
	if err != nil {
		return Report{}, err
	}
	opts.Model = modelName

	report := Report{
		Backend:   string(opts.Backend),
		BaseURL:   strings.TrimSpace(opts.BaseURL),
		Model:     modelName,
		StartedAt: time.Now(),
	}
	if opts.Backend == config.AIBackendOllama {
		if meta, metaErr := llm.FetchOllamaModelMetadata(ctx, opts.BaseURL, modelName, minDuration(opts.Timeout, 10*time.Second)); metaErr == nil {
			report.ContextWindow = meta.ContextWindow
			report.ContextDetail = formatContextDetail(meta)
		}
	}

	report.Cases = append(report.Cases, runTextCase(ctx, opts, textRunner))
	report.Cases = append(report.Cases, runSummaryJSONCase(ctx, opts, jsonRunner))
	report.Cases = append(report.Cases, runAdviceFollowUpJSONCase(ctx, opts, jsonRunner))
	report.Cases = append(report.Cases, runCommitSubjectJSONCase(ctx, opts, jsonRunner))
	report.FinishedAt = time.Now()
	report.Duration = report.FinishedAt.Sub(report.StartedAt)
	return report, nil
}

func normalizeOptions(opts Options) Options {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultEvalTimeout
	}
	return opts
}

func runnersForOptions(opts Options) (llm.TextRunner, llm.JSONSchemaRunner, error) {
	usage := llm.NewUsageTracker()
	switch opts.Backend {
	case config.AIBackendOpenAIAPI:
		if strings.TrimSpace(opts.APIKey) == "" {
			return nil, nil, errors.New("OpenAI API model eval requires an API key")
		}
		return llm.NewResponsesTextClient(strings.TrimSpace(opts.APIKey), opts.Timeout, usage),
			llm.NewResponsesClient(strings.TrimSpace(opts.APIKey), opts.Timeout, usage),
			nil
	case config.AIBackendOllama:
		return llm.NewOllamaTextRunner(opts.BaseURL, opts.Model, opts.Timeout, usage),
			llm.NewOllamaJSONSchemaRunner(opts.BaseURL, opts.Model, opts.Timeout, usage),
			nil
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX:
		if strings.TrimSpace(opts.BaseURL) == "" {
			return nil, nil, fmt.Errorf("%s model eval requires a base URL", opts.Backend.Label())
		}
		if opts.Backend.UsesCloudAPIKey() && strings.TrimSpace(opts.APIKey) == "" {
			return nil, nil, fmt.Errorf("%s model eval requires an API key", opts.Backend.Label())
		}
		runnerOpts := llm.OpenAICompatibleResponsesRunnerOptionsForProviderModel(string(opts.Backend), opts.Model, llm.OpenAICompatibleResponsesRunnerOptions{
			PreferChatCompletions: opts.Backend.UsesCloudAPIKey(),
		})
		return llm.NewOpenAICompatibleTextRunnerWithOptions(opts.BaseURL, opts.APIKey, opts.Model, opts.Timeout, usage, runnerOpts),
			llm.NewOpenAICompatibleResponsesRunnerWithOptions(opts.BaseURL, opts.APIKey, opts.Model, opts.Timeout, usage, runnerOpts),
			nil
	default:
		return nil, nil, fmt.Errorf("model eval does not support backend %q yet; use openai_api, ollama, mlx, or an OpenAI-compatible cloud backend", opts.Backend)
	}
}

func resolveEvalModel(ctx context.Context, opts Options) (string, error) {
	if modelName := strings.TrimSpace(opts.Model); modelName != "" {
		return modelName, nil
	}
	switch opts.Backend {
	case config.AIBackendOpenAIAPI:
		return defaultOpenAIModel, nil
	case config.AIBackendOllama, config.AIBackendMLX, config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi:
		discovery := llm.NewOpenAICompatibleModelDiscoveryWithAuthHeader(opts.BaseURL, opts.APIKey, minDuration(opts.Timeout, 15*time.Second), llm.OpenAICompatibleResponsesRunnerOptionsForProviderModel(string(opts.Backend), opts.Model, llm.OpenAICompatibleResponsesRunnerOptions{}).AuthHeader)
		return discovery.FirstModel(ctx)
	default:
		return "", errors.New("model eval could not resolve a model")
	}
}

func runTextCase(ctx context.Context, opts Options, runner llm.TextRunner) CaseResult {
	started := time.Now()
	resp, err := runner.RunText(ctx, llm.TextRequest{
		Model:      opts.Model,
		SystemText: "Reply with exactly one short sentence.",
		Messages: []llm.TextMessage{
			{Role: "user", Content: "Say that Little Control Room can use this model for local summaries."},
		},
	})
	return textCaseResult("plain_text_generation", started, resp.Model, resp.OutputText, resp.Usage, err)
}

func runSummaryJSONCase(ctx context.Context, opts Options, runner llm.JSONSchemaRunner) CaseResult {
	started := time.Now()
	client := sessionclassify.NewClientWithRunner(opts.Model, runner)
	result, err := client.Classify(ctx, sessionclassify.SessionSnapshot{
		ProjectPath:          "/tmp/little-control-room",
		SessionID:            "eval-gemma-summary",
		SessionFormat:        "modern",
		LastEventAt:          time.Now().UTC().Format(time.RFC3339),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		GitStatus: sessionclassify.GitStatusSnapshot{
			WorktreeDirty: true,
			RemoteStatus:  "no_upstream",
		},
		Transcript: []sessionclassify.TranscriptItem{
			{Role: "user", Text: "Can we test whether the local Gemma model can generate summaries for LCR?"},
			{Role: "assistant", Text: "I tested Ollama, found the OpenAI-compatible endpoint returned empty content, and switched generation to native Ollama generate with thinking disabled."},
			{Role: "assistant", Text: "I added a model-eval command and surfaced context window and token speed in /ai."},
		},
	})
	if err != nil {
		return textCaseResult("session_assessment_structured_json", started, "", "", model.LLMUsage{}, err)
	}
	output := fmt.Sprintf("%s: %s", result.Category, result.Summary)
	return textCaseResult("session_assessment_structured_json", started, result.Model, output, result.Usage, nil)
}

func runAdviceFollowUpJSONCase(ctx context.Context, opts Options, runner llm.JSONSchemaRunner) CaseResult {
	started := time.Now()
	client := sessionclassify.NewClientWithRunner(opts.Model, runner)
	result, err := client.Classify(ctx, sessionclassify.SessionSnapshot{
		ProjectPath:          "/tmp/romaexe-intros",
		SessionID:            "eval-advice-followup",
		SessionFormat:        "modern",
		LastEventAt:          time.Now().UTC().Format(time.RFC3339),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		GitStatus: sessionclassify.GitStatusSnapshot{
			WorktreeDirty: false,
			RemoteStatus:  "synced",
		},
		Transcript: []sessionclassify.TranscriptItem{
			{Role: "user", Text: "How can we improve this demo? I like the synth music but it is short. It would be nice if the scene reacted to the music somehow. I want to be realistic about what we can achieve."},
			{Role: "assistant", Text: "My vote is to improve it by making it a small choreographed intro rather than a richer object viewer. Concrete next milestone: reactive splats + 128-bar synth/director + one procedural club/data stage phase."},
		},
	})
	if err != nil {
		return textCaseResult("session_assessment_advice_followup_json", started, "", "", model.LLMUsage{}, err)
	}
	output := fmt.Sprintf("%s: %s", result.Category, result.Summary)
	if result.Category != model.SessionCategoryNeedsFollowUp {
		err = fmt.Errorf("advice milestone classified as %s, want %s", result.Category, model.SessionCategoryNeedsFollowUp)
	}
	return textCaseResult("session_assessment_advice_followup_json", started, result.Model, output, result.Usage, err)
}

func runCommitSubjectJSONCase(ctx context.Context, opts Options, runner llm.JSONSchemaRunner) CaseResult {
	started := time.Now()
	resp, err := runner.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:      opts.Model,
		SystemText: "Write concise git commit subjects for coding changes. Return only the requested JSON.",
		UserText: `Draft a git commit subject for this coding task snapshot:
{
  "project_name": "Little Control Room",
  "stage_mode": "staged_only",
  "included_files": ["internal/tui/ai_stats_dialog.go", "internal/llm/ollama.go"],
  "diff_stat": "2 files changed, 140 insertions(+), 8 deletions(-)",
  "patch": "Adds Ollama native model evaluation support and displays local inference speed in /ai."
}`,
		SchemaName: "git_commit_message",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"message": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
			},
			"required": []string{"message"},
		},
		ReasoningEffort: "low",
	})
	if err != nil {
		return textCaseResult("commit_subject_structured_json", started, resp.Model, resp.OutputText, resp.Usage, err)
	}
	var decoded struct {
		Message       string `json:"message"`
		Subject       string `json:"subject"`
		CommitSubject string `json:"commit_subject"`
	}
	if decodeErr := llm.DecodeJSONObjectOutput(resp.OutputText, &decoded); decodeErr != nil {
		err = decodeErr
	} else if strings.TrimSpace(firstNonEmptyString(decoded.Message, decoded.Subject, decoded.CommitSubject)) == "" {
		err = errors.New("commit subject JSON omitted message")
	}
	return textCaseResult("commit_subject_structured_json", started, resp.Model, resp.OutputText, resp.Usage, err)
}

func textCaseResult(name string, started time.Time, modelName, output string, usage model.LLMUsage, err error) CaseResult {
	duration := time.Since(started)
	result := CaseResult{
		Name:          name,
		Passed:        err == nil && strings.TrimSpace(output) != "",
		Model:         strings.TrimSpace(modelName),
		Duration:      duration,
		OutputPreview: clippedSingleLine(output, 180),
		Usage:         usage,
	}
	if usage.OutputTokens > 0 {
		rateDuration := duration
		if usage.OutputEvalDuration > 0 {
			rateDuration = usage.OutputEvalDuration
		}
		if rateDuration > 0 {
			result.TokensPerSecond = float64(usage.OutputTokens) / rateDuration.Seconds()
		}
	}
	if err != nil {
		result.Error = strings.TrimSpace(err.Error())
		result.Passed = false
	} else if strings.TrimSpace(output) == "" {
		result.Error = "empty assistant output"
		result.Passed = false
	}
	return result
}

func formatContextDetail(meta llm.OllamaModelMetadata) string {
	parts := []string{}
	if meta.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", meta.ContextWindow))
	}
	if meta.ParameterSize != "" {
		parts = append(parts, meta.ParameterSize)
	}
	if meta.Quantization != "" {
		parts = append(parts, meta.Quantization)
	}
	if meta.Architecture != "" {
		parts = append(parts, meta.Architecture)
	}
	return strings.Join(parts, " | ")
}

func clippedSingleLine(text string, limit int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (r Report) MarshalJSON() ([]byte, error) {
	type jsonReport struct {
		Backend       string       `json:"backend"`
		BaseURL       string       `json:"base_url,omitempty"`
		Model         string       `json:"model"`
		ContextWindow int64        `json:"context_window,omitempty"`
		ContextDetail string       `json:"context_detail,omitempty"`
		Passed        bool         `json:"passed"`
		DurationMS    int64        `json:"duration_ms"`
		Cases         []CaseResult `json:"cases"`
	}
	return json.Marshal(jsonReport{
		Backend:       r.Backend,
		BaseURL:       r.BaseURL,
		Model:         r.Model,
		ContextWindow: r.ContextWindow,
		ContextDetail: r.ContextDetail,
		Passed:        r.Passed(),
		DurationMS:    r.Duration.Milliseconds(),
		Cases:         r.Cases,
	})
}
