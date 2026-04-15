package aibackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/llm"

	"github.com/charmbracelet/x/ansi"
)

const detectTimeout = 4 * time.Second

type Status struct {
	Backend       config.AIBackend
	Label         string
	Installed     bool
	Authenticated bool
	Ready         bool
	Detail        string
	LoginHint     string
	Endpoint      string
	Models        []string
	ActiveModel   string
}

type Snapshot struct {
	Selected  config.AIBackend
	OpenAIAPI Status
	Codex     Status
	OpenCode  Status
	Claude    Status
	MLX       Status
	Ollama    Status
}

func Detect(ctx context.Context, cfg config.AppConfig) Snapshot {
	selected := cfg.EffectiveAIBackend()
	return Snapshot{
		Selected:  selected,
		OpenAIAPI: detectOpenAIAPI(cfg),
		Codex:     detectCodex(ctx),
		OpenCode:  detectOpenCode(ctx),
		Claude:    detectClaudeCode(ctx),
		MLX:       detectOpenAICompatibleLocal(ctx, cfg, config.AIBackendMLX),
		Ollama:    detectOpenAICompatibleLocal(ctx, cfg, config.AIBackendOllama),
	}
}

func DetectStatus(ctx context.Context, cfg config.AppConfig, backend config.AIBackend) Status {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return detectOpenAIAPI(cfg)
	case config.AIBackendCodex:
		return detectCodex(ctx)
	case config.AIBackendOpenCode:
		return detectOpenCode(ctx)
	case config.AIBackendClaude:
		return detectClaudeCode(ctx)
	case config.AIBackendMLX, config.AIBackendOllama:
		return detectOpenAICompatibleLocal(ctx, cfg, backend)
	case config.AIBackendDisabled:
		return Status{
			Backend: config.AIBackendDisabled,
			Label:   config.AIBackendDisabled.Label(),
			Ready:   true,
			Detail:  "AI features disabled by choice.",
		}
	default:
		return Status{
			Backend: config.AIBackendUnset,
			Label:   config.AIBackendUnset.Label(),
			Detail:  "Pick a backend in /setup to enable AI features.",
		}
	}
}

func (s Snapshot) StatusFor(backend config.AIBackend) Status {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return s.OpenAIAPI
	case config.AIBackendCodex:
		return s.Codex
	case config.AIBackendOpenCode:
		return s.OpenCode
	case config.AIBackendClaude:
		return s.Claude
	case config.AIBackendMLX:
		return s.MLX
	case config.AIBackendOllama:
		return s.Ollama
	case config.AIBackendDisabled:
		return DetectStatus(nil, config.AppConfig{}, config.AIBackendDisabled)
	default:
		return DetectStatus(nil, config.AppConfig{}, config.AIBackendUnset)
	}
}

func (s Snapshot) SelectedStatus() Status {
	return s.StatusFor(s.Selected)
}

func (s Snapshot) NeedsSetup() bool {
	return s.Selected == config.AIBackendUnset
}

func detectOpenAIAPI(cfg config.AppConfig) Status {
	status := Status{
		Backend: config.AIBackendOpenAIAPI,
		Label:   config.AIBackendOpenAIAPI.Label(),
		Detail:  "No saved OpenAI API key.",
	}
	if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
		status.LoginHint = "Open /settings and save an OpenAI API key."
		return status
	}
	status.Installed = true
	status.Authenticated = true
	status.Ready = true
	status.Detail = "Saved OpenAI API key ready."
	return status
}

func detectCodex(ctx context.Context) Status {
	status := Status{
		Backend:   config.AIBackendCodex,
		Label:     config.AIBackendCodex.Label(),
		Detail:    "Codex CLI is not installed.",
		LoginHint: "Install Codex, then run `codex login` and press r to refresh.",
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return status
	}
	status.Installed = true
	status.Detail = "Codex installed, but not logged in."
	status.LoginHint = "Run `codex login`, then press r to refresh."
	out, err := runCommand(ctx, "codex", "login", "status")
	if err != nil {
		trimmed := strings.TrimSpace(out)
		if trimmed != "" {
			status.Detail = trimmed
		}
		return status
	}
	trimmed := strings.TrimSpace(out)
	if strings.Contains(trimmed, "Logged in") {
		status.Authenticated = true
		status.Ready = true
		status.Detail = trimmed
		return status
	}
	if trimmed != "" {
		status.Detail = trimmed
	}
	return status
}

func detectOpenCode(ctx context.Context) Status {
	status := Status{
		Backend:   config.AIBackendOpenCode,
		Label:     config.AIBackendOpenCode.Label(),
		Detail:    "OpenCode CLI is not installed.",
		LoginHint: "Install OpenCode, then run `opencode auth login --provider openai` or configure another provider and press r to refresh.",
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		return status
	}
	status.Installed = true
	status.Detail = "OpenCode installed, but no authenticated provider is configured."
	status.LoginHint = "Run `opencode auth login --provider openai` or configure another provider, then press r to refresh."
	out, err := runCommand(ctx, "opencode", "providers", "list")
	trimmed := strings.TrimSpace(ansi.Strip(out))
	if err != nil && trimmed == "" {
		return status
	}
	if ready, detail := openCodeProviderStatus(out); ready {
		status.Authenticated = true
		status.Ready = true
		status.Detail = detail
		return status
	}
	if trimmed != "" {
		status.Detail = trimmed
	}
	return status
}

func openCodeProviderStatus(raw string) (bool, string) {
	providers, err := llm.ParseOpenCodeProvidersOutput(raw)
	if err != nil || len(providers) == 0 {
		return false, ""
	}
	seen := make(map[string]bool)
	labels := make([]string, 0, len(providers))
	for _, provider := range providers {
		label := strings.TrimSpace(provider.Name)
		switch strings.TrimSpace(provider.Type) {
		case "oauth":
			label = strings.TrimSpace(label + " OAuth")
		case "api_key":
			label = strings.TrimSpace(label + " API key")
		case "api":
			label = strings.TrimSpace(label + " API")
		}
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return false, ""
	}
	if len(labels) == 1 {
		return true, "OpenCode provider ready: " + labels[0] + "."
	}
	return true, "OpenCode providers ready: " + strings.Join(labels, ", ") + "."
}

func detectClaudeCode(ctx context.Context) Status {
	status := Status{
		Backend:   config.AIBackendClaude,
		Label:     config.AIBackendClaude.Label(),
		Detail:    "Claude Code CLI is not installed.",
		LoginHint: "Run `claude auth login`, then press r to refresh.",
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return status
	}
	status.Installed = true
	status.Detail = "Claude Code installed, but not logged in."

	out, err := runCommand(ctx, "claude", "auth", "status", "--json")
	trimmed := strings.TrimSpace(out)
	auth, ok := parseClaudeAuthStatus(trimmed)
	if !ok {
		if trimmed != "" {
			status.Detail = trimmed
		}
		return status
	}
	if auth.LoggedIn {
		status.Authenticated = true
		status.Ready = true
		status.Detail = claudeAuthDetail(auth)
		return status
	}
	if err == nil && trimmed != "" {
		status.Detail = trimmed
	}
	return status
}

func detectOpenAICompatibleLocal(ctx context.Context, cfg config.AppConfig, backend config.AIBackend) Status {
	baseURL := cfg.OpenAICompatibleBaseURL(backend)
	label := backend.Label()
	status := Status{
		Backend:  backend,
		Label:    label,
		Endpoint: baseURL,
		Detail:   label + " local server is not reachable.",
	}
	if baseURL == "" {
		status.LoginHint = "Open /settings and save a base URL for " + label + "."
		return status
	}
	status.Detail = fmt.Sprintf("%s local server is not reachable at %s.", label, baseURL)
	status.LoginHint = fmt.Sprintf("Start your %s OpenAI-compatible server, then press r to refresh. Use /settings if you need a different base URL or API key.", label)
	status.Installed = true

	discovery := llm.NewOpenAICompatibleModelDiscovery(baseURL, cfg.OpenAICompatibleAPIKey(backend), detectTimeout)
	if err := discovery.Discover(ctx); err != nil {
		return status
	}
	models := discovery.Models()
	status.Models = append([]string(nil), models...)
	if len(models) == 0 {
		status.Detail = fmt.Sprintf("%s server at %s returned no models.", label, baseURL)
		return status
	}

	status.Authenticated = true
	configuredModel := strings.TrimSpace(cfg.OpenAICompatibleModel(backend))
	if configuredModel != "" {
		for _, model := range models {
			if strings.EqualFold(strings.TrimSpace(model), configuredModel) {
				status.Ready = true
				status.ActiveModel = model
				status.Detail = fmt.Sprintf("%s ready at %s (using %s)", label, baseURL, model)
				return status
			}
		}
		status.Detail = fmt.Sprintf("%s ready at %s, but configured model %s was not returned by /v1/models.", label, baseURL, configuredModel)
		status.LoginHint = "Press m in /setup to pick a discovered model, or clear the saved override in /settings."
		return status
	}

	status.Ready = true
	status.ActiveModel = models[0]
	status.Detail = fmt.Sprintf("%s ready at %s (auto %s)", label, baseURL, models[0])
	if len(models) > 1 {
		status.Detail = fmt.Sprintf("%s ready at %s (auto %s +%d more)", label, baseURL, models[0], len(models)-1)
	}
	return status
}

type claudeAuthStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	SubscriptionType string `json:"subscriptionType"`
}

func parseClaudeAuthStatus(raw string) (claudeAuthStatus, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return claudeAuthStatus{}, false
	}
	var auth claudeAuthStatus
	if err := json.Unmarshal([]byte(raw), &auth); err == nil {
		return auth, true
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return claudeAuthStatus{}, false
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &auth); err != nil {
		return claudeAuthStatus{}, false
	}
	return auth, true
}

func claudeAuthDetail(auth claudeAuthStatus) string {
	parts := []string{"Claude Code ready"}
	if method := strings.TrimSpace(auth.AuthMethod); method != "" {
		parts = append(parts, "via "+method)
	}
	if subscription := strings.TrimSpace(auth.SubscriptionType); subscription != "" {
		parts = append(parts, "("+subscription+")")
	}
	return strings.Join(parts, " ")
}

func runCommand(parent context.Context, name string, args ...string) (string, error) {
	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, detectTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return combined.String(), ctx.Err()
	}
	return combined.String(), err
}
