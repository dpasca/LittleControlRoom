package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/brand"
)

const defaultCommitModel = "gpt-5-mini"

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
}

func NewOpenAICommitMessageClient(apiKey string) *OpenAICommitMessageClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	model := strings.TrimSpace(os.Getenv(brand.CommitModelEnvVar))
	if model == "" {
		model = defaultCommitModel
	}

	endpoint := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/responses"

	return &OpenAICommitMessageClient{
		apiKey:   apiKey,
		model:    model,
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

func (c *OpenAICommitMessageClient) ModelName() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.model)
}

func (c *OpenAICommitMessageClient) Suggest(ctx context.Context, input CommitMessageInput) (CommitMessageSuggestion, error) {
	if c == nil || c.apiKey == "" {
		return CommitMessageSuggestion{}, errors.New("openai commit message client not configured")
	}
	if strings.TrimSpace(input.ProjectName) == "" && strings.TrimSpace(input.Branch) != "" {
		input.ProjectName = filepath.Base(strings.TrimSpace(input.Branch))
	}

	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("marshal commit message input: %w", err)
	}

	outputText, modelName, err := c.runJSONSchemaPrompt(
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
	if err := json.Unmarshal([]byte(outputText), &decoded); err != nil {
		return CommitMessageSuggestion{}, fmt.Errorf("decode commit message result: %w", err)
	}
	message := strings.TrimSpace(decoded.Message)
	if message == "" {
		return CommitMessageSuggestion{}, errors.New("commit message suggestion was empty")
	}
	return CommitMessageSuggestion{
		Message: message,
		Model:   modelName,
	}, nil
}

func (c *OpenAICommitMessageClient) runJSONSchemaPrompt(ctx context.Context, systemText, userText, schemaName string, schema map[string]any) (string, string, error) {
	reqBody := map[string]any{
		"model": c.model,
		"input": []any{
			map[string]any{
				"role": "system",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": systemText,
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": userText,
					},
				},
			},
		},
		"reasoning": map[string]any{
			"effort": "minimal",
		},
		"store": false,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   schemaName,
				"strict": true,
				"schema": schema,
			},
		},
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("send openai request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("openai responses api %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Status string `json:"status"`
		Model  string `json:"model"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", "", fmt.Errorf("decode openai response: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return "", "", errors.New(envelope.Error.Message)
	}

	var outputText string
	for _, item := range envelope.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				outputText = content.Text
				break
			}
		}
		if outputText != "" {
			break
		}
	}
	if outputText == "" {
		return "", "", fmt.Errorf("openai response missing assistant output (status=%s)", envelope.Status)
	}
	return outputText, strings.TrimSpace(envelope.Model), nil
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
