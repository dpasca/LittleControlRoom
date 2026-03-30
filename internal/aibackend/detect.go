package aibackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"lcroom/internal/config"

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
}

type Snapshot struct {
	Selected  config.AIBackend
	OpenAIAPI Status
	Codex     Status
	OpenCode  Status
	Claude    Status
}

func Detect(ctx context.Context, cfg config.AppConfig) Snapshot {
	selected := cfg.EffectiveAIBackend()
	return Snapshot{
		Selected:  selected,
		OpenAIAPI: detectOpenAIAPI(cfg),
		Codex:     detectCodex(ctx),
		OpenCode:  detectOpenCode(ctx),
		Claude:    detectClaudeCode(ctx),
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
		LoginHint: "Install OpenCode, then run `opencode auth login --provider openai` and press r to refresh.",
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		return status
	}
	status.Installed = true
	status.Detail = "OpenCode installed, but no OpenAI auth is configured."
	status.LoginHint = "Run `opencode auth login --provider openai`, then press r to refresh."
	out, err := runCommand(ctx, "opencode", "auth", "list")
	trimmed := strings.TrimSpace(ansi.Strip(out))
	if err != nil && trimmed == "" {
		return status
	}
	switch {
	case strings.Contains(trimmed, "OpenAI oauth"):
		status.Authenticated = true
		status.Ready = true
		status.Detail = "OpenCode OpenAI OAuth is ready."
	case strings.Contains(trimmed, "OpenAI OPENAI_API_KEY"):
		status.Authenticated = true
		status.Ready = true
		status.Detail = "OpenCode can use OPENAI_API_KEY from the environment."
	case trimmed != "":
		status.Detail = trimmed
	}
	return status
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
