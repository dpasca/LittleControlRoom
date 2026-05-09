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
	ID      string `json:"id,omitempty"`
	Model   string `json:"model,omitempty"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type Completion struct {
	Message      Message
	ID           string
	Model        string
	FinishReason string
	Usage        json.RawMessage
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

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Completion, error) {
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"tools":       tools,
		"tool_choice": "auto",
		"temperature": 0.2,
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return Completion{}, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "chat", "completions")
	if err != nil {
		return Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return Completion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://little-control-room.local")
	req.Header.Set("X-Title", "Little Control Room lcagent")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Completion{}, err
	}
	var parsed ChatResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return Completion{}, fmt.Errorf("openrouter request failed: HTTP %d: %s", resp.StatusCode, body)
			}
			return Completion{}, fmt.Errorf("openrouter request failed: HTTP %d", resp.StatusCode)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Completion{}, fmt.Errorf("openrouter request failed: HTTP %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return Completion{}, fmt.Errorf("openrouter request failed: HTTP %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return Completion{}, fmt.Errorf("openrouter response decode failed: %w", decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Completion{}, fmt.Errorf("openrouter request failed: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Completion{}, fmt.Errorf("openrouter response had no choices")
	}
	choice := parsed.Choices[0]
	return Completion{
		Message:      choice.Message,
		ID:           strings.TrimSpace(parsed.ID),
		Model:        strings.TrimSpace(firstNonEmpty(parsed.Model, c.model)),
		FinishReason: strings.TrimSpace(choice.FinishReason),
		Usage:        append(json.RawMessage(nil), parsed.Usage...),
	}, nil
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
				Name:        "read_file",
				Description: "Read a bounded line range from a text file inside the workspace.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "Workspace-relative path. Absolute paths are denied."},
						"offset": map[string]any{"type": "integer", "minimum": 1, "description": "1-based starting line. Defaults to 1."},
						"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum lines to read. Defaults to 200."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_files",
				Description: "List files under a workspace path, optionally filtered by a simple glob.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":        map[string]any{"type": "string", "description": "Workspace-relative directory or file to list. Defaults to workspace root. Absolute paths are denied."},
						"glob":        map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "search",
				Description: "Search text files in the workspace for a literal query string.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":       map[string]any{"type": "string"},
						"path":        map[string]any{"type": "string", "description": "Workspace-relative directory or file to search. Defaults to workspace root. Absolute paths are denied."},
						"file_glob":   map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_matches": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "load_skill",
				Description: "Load the full body of an available skill by name. Use only after checking the skill metadata in the system prompt.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "run_command",
				Description: "Run a bounded command in the workspace. Prefer argv. Use shell command strings only when shell behavior is genuinely needed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"argv":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command argv, for example [\"go\",\"test\",\"./...\"]"},
						"command":    map[string]any{"type": "string", "description": "Legacy shell command string. Prefer argv unless shell syntax is required."},
						"shell":      map[string]any{"type": "boolean", "description": "Set true when using command as a shell string."},
						"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply a Codex apply_patch patch. The patch must use the exact envelope: *** Begin Patch, then *** Update File: path or *** Add File: path, hunks with @@ and +/- lines, then *** End Patch. Example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch",
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

func SystemPrompt(skillIndex, projectInstructions string) string {
	lines := []string{
		"You are lcagent, a small local coding-agent harness controlled by Little Control Room.",
		"Use the provided tools for all workspace inspection, edits, plan updates, and final responses.",
		"Do not claim to have inspected files or run verification unless a tool result shows that happened.",
		"Prefer read_file, list_files, and search for routine inspection before reaching for shell commands.",
		"Use workspace-relative paths in file tools; absolute paths are denied.",
		"File tools are workspace-only; use read-only run_command argv for paths outside the workspace.",
		"When using run_command, prefer argv over command strings; shell commands are for shell syntax only.",
		"Never write provider tool-call markup such as DSML in assistant text; call tools only through structured tool_calls.",
		"Skill descriptions in this prompt are metadata only; call load_skill before relying on any skill instructions.",
		"Use apply_patch for source edits. Patches must use this exact shape: *** Begin Patch, *** Update File: path, @@, -old line, +new line, *** End Patch.",
		"When done, call final_response exactly once.",
	}
	if strings.TrimSpace(skillIndex) != "" {
		lines = append(lines, "", strings.TrimSpace(skillIndex))
	}
	if strings.TrimSpace(projectInstructions) != "" {
		lines = append(lines, "", strings.TrimSpace(projectInstructions))
	}
	return strings.Join(lines, "\n")
}

func SanitizeAssistantContent(content string) (string, bool) {
	first := -1
	for _, marker := range providerToolMarkupMarkers {
		if idx := strings.Index(content, marker); idx >= 0 && (first == -1 || idx < first) {
			first = idx
		}
	}
	if first == -1 {
		return content, false
	}
	return strings.TrimSpace(content[:first]), true
}

func ContainsProviderToolMarkup(content string) bool {
	_, stripped := SanitizeAssistantContent(content)
	return stripped
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

var providerToolMarkupMarkers = []string{
	"<\uff5cDSML\uff5c",
	"<|tool_calls|>",
	"<|tool_call|>",
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func responseSnippet(data []byte) string {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 512 {
		return text[:512] + "..."
	}
	return text
}
