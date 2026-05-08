package modeladapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const DefaultOpenRouterModel = "deepseek/deepseek-v4-pro"

type OpenRouterConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	EnvFile    string
	MaxTurns   int
	HTTPClient *http.Client
}

type Client struct {
	apiKey     string
	baseURL    string
	model      string
	maxTurns   int
	httpClient *http.Client
}

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func NewOpenRouterClient(cfg OpenRouterConfig) (*Client, error) {
	if cfg.EnvFile != "" {
		if err := LoadEnvFile(cfg.EnvFile); err != nil {
			return nil, err
		}
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is required for provider=openrouter")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL")), "/")
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultOpenRouterModel
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 16
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		maxTurns:   maxTurns,
		httpClient: httpClient,
	}, nil
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Message, error) {
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"tools":       tools,
		"tool_choice": "auto",
		"temperature": 0.2,
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return Message{}, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "chat", "completions")
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://little-control-room.local")
	req.Header.Set("X-Title", "Little Control Room lcagent")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Message{}, err
	}
	var parsed ChatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Message{}, fmt.Errorf("openrouter response decode failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Message{}, fmt.Errorf("openrouter request failed: %s", parsed.Error.Message)
		}
		return Message{}, fmt.Errorf("openrouter request failed: HTTP %d", resp.StatusCode)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, fmt.Errorf("openrouter request failed: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, fmt.Errorf("openrouter response had no choices")
	}
	return parsed.Choices[0].Message, nil
}

func (c *Client) MaxTurns() int {
	if c == nil || c.maxTurns <= 0 {
		return 16
	}
	return c.maxTurns
}

func LoadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func Tools() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "run_command",
				Description: "Run a bounded shell command in the workspace. Use short commands and inspect results before editing.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"command":    map[string]any{"type": "string"},
						"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply an apply_patch-format patch. Files must stay inside the workspace.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"patch": map[string]any{"type": "string"},
					},
					"required": []string{"patch"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "update_plan",
				Description: "Publish the current short plan with statuses pending, in_progress, or completed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"step":   map[string]any{"type": "string"},
									"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
								},
								"required": []string{"step", "status"},
							},
						},
					},
					"required": []string{"items"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "final_response",
				Description: "Finish the session with a concise summary, files changed, and verification.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"summary":       map[string]any{"type": "string"},
						"files_changed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"verification":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required": []string{"summary", "files_changed", "verification"},
				},
			},
		},
	}
}

func SystemPrompt() string {
	return strings.Join([]string{
		"You are lcagent, a small local coding-agent harness controlled by Little Control Room.",
		"Use the provided tools for all workspace inspection, edits, plan updates, and final responses.",
		"Do not claim to have inspected files or run verification unless a tool result shows that happened.",
		"Use apply_patch for source edits. Keep changes focused on the user's prompt.",
		"When done, call final_response exactly once.",
	}, "\n")
}

func NormalizeArguments(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if raw[0] != '"' {
		return raw, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(encoded), nil
}
