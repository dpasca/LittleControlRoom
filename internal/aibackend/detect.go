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
const (
	minUsefulLocalContextWindow        int64 = 8192
	preferredSummaryLocalContextWindow int64 = 32768
)

type Status struct {
	Backend        config.AIBackend
	Label          string
	Installed      bool
	Authenticated  bool
	Ready          bool
	Detail         string
	LoginHint      string
	Endpoint       string
	Models         []string
	ActiveModel    string
	ContextWindow  int64
	ContextDetail  string
	ContextWarning string
}

type Snapshot struct {
	Selected   config.AIBackend
	OpenAIAPI  Status
	OpenRouter Status
	DeepSeek   Status
	Moonshot   Status
	Xiaomi     Status
	Codex      Status
	OpenCode   Status
	Claude     Status
	MLX        Status
	Ollama     Status
}

func Detect(ctx context.Context, cfg config.AppConfig) Snapshot {
	selected := cfg.EffectiveAIBackend()
	return Snapshot{
		Selected:   selected,
		OpenAIAPI:  detectOpenAIAPI(cfg),
		OpenRouter: detectOpenAICompatibleCloud(ctx, cfg, config.AIBackendOpenRouter),
		DeepSeek:   detectOpenAICompatibleCloud(ctx, cfg, config.AIBackendDeepSeek),
		Moonshot:   detectOpenAICompatibleCloud(ctx, cfg, config.AIBackendMoonshot),
		Xiaomi:     detectOpenAICompatibleCloud(ctx, cfg, config.AIBackendXiaomi),
		Codex:      detectCodex(ctx),
		OpenCode:   detectOpenCode(ctx),
		Claude:     detectClaudeCode(ctx),
		MLX:        detectOpenAICompatibleLocal(ctx, cfg, config.AIBackendMLX),
		Ollama:     detectOpenAICompatibleLocal(ctx, cfg, config.AIBackendOllama),
	}
}

func DetectStatus(ctx context.Context, cfg config.AppConfig, backend config.AIBackend) Status {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return detectOpenAIAPI(cfg)
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi:
		return detectOpenAICompatibleCloud(ctx, cfg, backend)
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
	case config.AIBackendOpenRouter:
		return s.OpenRouter
	case config.AIBackendDeepSeek:
		return s.DeepSeek
	case config.AIBackendMoonshot:
		return s.Moonshot
	case config.AIBackendXiaomi:
		return s.Xiaomi
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

func detectOpenAICompatibleCloud(ctx context.Context, cfg config.AppConfig, backend config.AIBackend) Status {
	label := backend.Label()
	apiKey := cfg.OpenAICompatibleAPIKey(backend)
	baseURL := cfg.OpenAICompatibleBaseURL(backend)
	status := Status{
		Backend:  backend,
		Label:    label,
		Endpoint: baseURL,
		Detail:   "No saved " + label + " API key.",
	}
	if strings.TrimSpace(apiKey) == "" {
		status.LoginHint = "Open /settings and save a " + label + " API key."
		return status
	}
	if baseURL == "" {
		status.LoginHint = "Open /settings and save a base URL for " + label + "."
		return status
	}
	if backend == config.AIBackendXiaomi &&
		config.LooksLikeXiaomiTokenPlanAPIKey(apiKey) &&
		config.LooksLikeRegularXiaomiBaseURL(baseURL) {
		status.Detail = "Token Plan Xiaomi key detected, but the base URL is the regular Xiaomi API endpoint."
		status.LoginHint = "Open /settings and set Xiaomi base URL. " + config.XiaomiTokenPlanBaseURLHint()
		return status
	}
	status.Installed = true
	status.Authenticated = true
	status.Ready = true
	status.ActiveModel = cfg.OpenAICompatibleModel(backend)
	status.Detail = "Saved " + label + " API key ready."

	model := cfg.OpenAICompatibleModel(backend)
	profile := llm.OpenAICompatibleProviderModelProfileForProviderModel(string(backend), model)
	discovery := llm.NewOpenAICompatibleModelDiscoveryWithAuthHeader(baseURL, apiKey, detectTimeout, profile.AuthHeader)
	if err := discovery.Discover(ctx); err != nil {
		return status
	}
	models := discovery.Models()
	status.Models = append([]string(nil), models...)
	configuredModel := strings.TrimSpace(model)
	if configuredModel == "" || len(models) == 0 {
		return status
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), configuredModel) {
			status.ActiveModel = model
			status.Detail = fmt.Sprintf("%s ready (using %s)", label, model)
			return status
		}
	}
	status.Detail = fmt.Sprintf("%s key ready; default model %s was not returned by /models.", label, configuredModel)
	status.LoginHint = "Set an explicit compatible model for this task, or choose another provider."
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

	model := cfg.OpenAICompatibleModel(backend)
	profile := llm.OpenAICompatibleProviderModelProfileForProviderModel(string(backend), model)
	discovery := llm.NewOpenAICompatibleModelDiscoveryWithAuthHeader(baseURL, cfg.OpenAICompatibleAPIKey(backend), detectTimeout, profile.AuthHeader)
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
				enrichLocalModelMetadata(ctx, cfg, backend, &status)
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
	enrichLocalModelMetadata(ctx, cfg, backend, &status)
	return status
}

func enrichLocalModelMetadata(ctx context.Context, cfg config.AppConfig, backend config.AIBackend, status *Status) {
	if status == nil || !status.Ready || strings.TrimSpace(status.ActiveModel) == "" {
		return
	}
	switch backend {
	case config.AIBackendOllama:
		meta, err := llm.FetchOllamaModelMetadata(ctx, cfg.OpenAICompatibleBaseURL(backend), status.ActiveModel, detectTimeout)
		if err != nil {
			status.ContextWarning = "Ollama model context metadata was not available."
			return
		}
		status.ContextWindow = meta.ContextWindow
		status.ContextDetail = ollamaContextDetail(meta)
		status.ContextWarning = localContextWarning(meta.ContextWindow)
	}
}

func ollamaContextDetail(meta llm.OllamaModelMetadata) string {
	parts := []string{}
	if meta.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("max context %s tokens", formatContextTokenCount(meta.ContextWindow)))
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

func localContextWarning(contextWindow int64) string {
	switch {
	case contextWindow <= 0:
		return "Model context window is unknown. For Ollama, `ollama show <model>` or /api/show should expose model_info context_length when available."
	case contextWindow < minUsefulLocalContextWindow:
		return fmt.Sprintf("Context is below LCR's useful floor of %s tokens. Use a larger-context model, or create an Ollama variant with a larger num_ctx if the model supports it.", formatContextTokenCount(minUsefulLocalContextWindow))
	case contextWindow < preferredSummaryLocalContextWindow:
		return fmt.Sprintf("Context is usable but below LCR's preferred %s-token window for richer summaries. Ollama can raise request context with num_ctx when the model and hardware allow it.", formatContextTokenCount(preferredSummaryLocalContextWindow))
	default:
		return ""
	}
}

func formatContextTokenCount(tokens int64) string {
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
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
