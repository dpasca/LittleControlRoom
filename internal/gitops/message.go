package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/llm"
)

const (
	defaultCommitModel           = "gpt-5.4-mini"
	defaultCommitReasoningEffort = "low"
)

type CommitMessageInput struct {
	Intent                  string   `json:"intent"`
	ProjectName             string   `json:"project_name"`
	Branch                  string   `json:"branch,omitempty"`
	StageMode               string   `json:"stage_mode"`
	LatestSessionSummary    string   `json:"latest_session_summary,omitempty"`
	IncludedFiles           []string `json:"included_files"`
	SuggestedUntrackedFiles []string `json:"suggested_untracked_files,omitempty"`
	ExcludedFiles           []string `json:"excluded_files,omitempty"`
	DiffStat                string   `json:"diff_stat,omitempty"`
	Patch                   string   `json:"patch,omitempty"`
}

type CommitMessageSuggestion struct {
	Message string
	Model   string
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
	responses  *llm.ResponsesClient
}

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
		httpClient: &http.Client{Timeout: 45 * time.Second},
		responses:  llm.NewResponsesClient(apiKey, 45*time.Second, usage),
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
		return CommitMessageSuggestion{}, errors.New("openai commit message client not configured")
	}
	if strings.TrimSpace(input.ProjectName) == "" && strings.TrimSpace(input.Branch) != "" {
		input.ProjectName = filepath.Base(strings.TrimSpace(input.Branch))
	}

	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("marshal commit message input: %w", err)
	}

	response, err := c.runJSONSchemaPrompt(
		ctx,
		commitMessageInstructions,
		"Draft a git commit subject for this coding task snapshot:\n\n"+string(payload),
		"git_commit_message",
		map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "One concise git commit subject line.",
				},
			},
			"required": []string{"message"},
		},
	)
	if err != nil {
		return CommitMessageSuggestion{}, err
	}

	var decoded struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(response.OutputText), &decoded); err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("decode commit message result: %w", err)
	}
	message := strings.TrimSpace(decoded.Message)
	if message == "" {
		return CommitMessageSuggestion{}, errors.New("commit message suggestion was empty")
	}
	return CommitMessageSuggestion{
		Message: message,
		Model:   response.Model,
	}, nil
}

func (c *OpenAICommitMessageClient) runJSONSchemaPrompt(ctx context.Context, systemText, userText, schemaName string, schema map[string]any) (llm.JSONSchemaResponse, error) {
	response, err := c.responsesClient().RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           c.model,
		SystemText:      systemText,
		UserText:        userText,
		SchemaName:      schemaName,
		Schema:          schema,
		ReasoningEffort: defaultCommitReasoningEffort,
	})
	if err != nil {
		return llm.JSONSchemaResponse{}, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return llm.JSONSchemaResponse{}, fmt.Errorf("openai response missing assistant output (status=%s)", response.Status)
	}
	return response, nil
}

func (c *OpenAICommitMessageClient) responsesClient() *llm.ResponsesClient {
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

const commitMessageInstructions = `You write short git commit subject lines for coding work.

Use the provided task summary, file list, suggested untracked files, diff stat, and patch when present.
Treat suggested_untracked_files as part of the proposed commit even though they are not staged yet.
Prefer a single concise subject line under 72 characters.
Use imperative mood when it fits naturally.
Prefer the main user-facing or workflow outcome over an inventory of touched subsystems.
Avoid subjects that read like changelogs, such as lists joined with commas, "and", or semicolons.
Avoid mentioning tests or docs unless they are the primary change.
Do not add prefixes like feat:, fix:, chore:, or trailing punctuation unless the input strongly implies an existing convention.
Return only the JSON field requested by the schema.`
