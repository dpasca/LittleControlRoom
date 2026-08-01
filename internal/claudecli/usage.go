package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultPlanUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"
	planUsageRequestTimeout  = 6 * time.Second
	claudeKeychainService    = "Claude Code-credentials"
)

type PlanUsageWindow struct {
	Utilization float64
	ResetsAt    time.Time
}

type PlanUsage struct {
	Available bool
	FiveHour  *PlanUsageWindow
	SevenDay  *PlanUsageWindow
}

type PlanUsageReader struct {
	HTTPClient      *http.Client
	Endpoint        string
	ReadAccessToken func(context.Context, string) (string, bool, error)
	LookupEnv       func(string) (string, bool)
}

func NewPlanUsageReader() *PlanUsageReader {
	return &PlanUsageReader{
		HTTPClient: &http.Client{Timeout: planUsageRequestTimeout},
		Endpoint:   defaultPlanUsageEndpoint,
		ReadAccessToken: func(ctx context.Context, configDir string) (string, bool, error) {
			return readClaudeOAuthAccessToken(ctx, configDir)
		},
		LookupEnv: os.LookupEnv,
	}
}

func (r *PlanUsageReader) Read(ctx context.Context, configDir string) (PlanUsage, error) {
	if r == nil {
		return PlanUsage{}, nil
	}
	lookupEnv := r.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if value, ok := lookupEnv("ANTHROPIC_API_KEY"); ok && strings.TrimSpace(value) != "" {
		// Claude Code gives an API key precedence over subscription credentials,
		// so claude.ai plan limits do not describe the active billing path.
		return PlanUsage{}, nil
	}
	if value, ok := lookupEnv("ANTHROPIC_AUTH_TOKEN"); ok && strings.TrimSpace(value) != "" {
		return PlanUsage{}, nil
	}

	token := ""
	if value, ok := lookupEnv("CLAUDE_CODE_OAUTH_TOKEN"); ok {
		token = strings.TrimSpace(value)
	}
	if token == "" {
		readAccessToken := r.ReadAccessToken
		if readAccessToken == nil {
			readAccessToken = readClaudeOAuthAccessToken
		}
		var available bool
		var err error
		token, available, err = readAccessToken(ctx, configDir)
		if err != nil {
			return PlanUsage{}, err
		}
		if !available || strings.TrimSpace(token) == "" {
			return PlanUsage{}, nil
		}
	}

	endpoint := strings.TrimSpace(r.Endpoint)
	if endpoint == "" {
		endpoint = defaultPlanUsageEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PlanUsage{}, fmt.Errorf("prepare Claude plan usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "LittleControlRoom")

	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: planUsageRequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return PlanUsage{}, fmt.Errorf("read Claude plan usage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return PlanUsage{}, fmt.Errorf("read Claude plan usage: unexpected HTTP status %d", resp.StatusCode)
	}

	var payload struct {
		FiveHour *planUsageWindowJSON `json:"five_hour"`
		SevenDay *planUsageWindowJSON `json:"seven_day"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024))
	if err := decoder.Decode(&payload); err != nil {
		return PlanUsage{}, fmt.Errorf("decode Claude plan usage: %w", err)
	}
	return PlanUsage{
		Available: true,
		FiveHour:  parsePlanUsageWindow(payload.FiveHour),
		SevenDay:  parsePlanUsageWindow(payload.SevenDay),
	}, nil
}

type planUsageWindowJSON struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func parsePlanUsageWindow(raw *planUsageWindowJSON) *PlanUsageWindow {
	if raw == nil || raw.Utilization == nil {
		return nil
	}
	utilization := *raw.Utilization
	if utilization < 0 {
		utilization = 0
	}
	if utilization > 100 {
		utilization = 100
	}
	window := &PlanUsageWindow{Utilization: utilization}
	if resetsAt := strings.TrimSpace(raw.ResetsAt); resetsAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, resetsAt); err == nil {
			window.ResetsAt = parsed
		}
	}
	return window
}

type storedClaudeCredentials struct {
	ClaudeAIOAuth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

func readClaudeOAuthAccessToken(ctx context.Context, configDir string) (string, bool, error) {
	if runtime.GOOS == "darwin" {
		output, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", claudeKeychainService, "-w").Output()
		if err == nil {
			if token, ok := parseStoredClaudeAccessToken(output); ok {
				return token, true, nil
			}
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", false, ctx.Err()
		}
	}

	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("resolve Claude credentials directory: %w", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read Claude credentials: %w", err)
	}
	if token, ok := parseStoredClaudeAccessToken(data); ok {
		return token, true, nil
	}
	return "", false, nil
}

func parseStoredClaudeAccessToken(data []byte) (string, bool) {
	var credentials storedClaudeCredentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		return "", false
	}
	token := strings.TrimSpace(credentials.ClaudeAIOAuth.AccessToken)
	return token, token != ""
}
