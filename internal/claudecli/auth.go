package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const authenticationStatusTimeout = 4 * time.Second

type AuthenticationStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	SubscriptionType string `json:"subscriptionType"`
	APIKeySource     string `json:"apiKeySource"`
}

func ReadAuthenticationStatus(parent context.Context) (AuthenticationStatus, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return AuthenticationStatus{}, fmt.Errorf("find Claude Code CLI: %w", err)
	}

	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, authenticationStatusTimeout)
		defer cancel()
	}

	output, commandErr := exec.CommandContext(ctx, "claude", "auth", "status", "--json").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return AuthenticationStatus{}, fmt.Errorf("read Claude Code authentication status: %w", ctx.Err())
	}
	raw := strings.TrimSpace(string(output))
	if status, ok := ParseAuthenticationStatus(raw); ok {
		// Claude exits non-zero when the structured status says loggedIn=false.
		// The JSON is still authoritative and useful to callers in that case.
		return status, nil
	}
	if commandErr != nil {
		if raw != "" {
			return AuthenticationStatus{}, fmt.Errorf("read Claude Code authentication status: %w: %s", commandErr, raw)
		}
		return AuthenticationStatus{}, fmt.Errorf("read Claude Code authentication status: %w", commandErr)
	}
	return AuthenticationStatus{}, fmt.Errorf("Claude Code returned an unreadable authentication status")
}

func ParseAuthenticationStatus(raw string) (AuthenticationStatus, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AuthenticationStatus{}, false
	}
	var status AuthenticationStatus
	if err := json.Unmarshal([]byte(raw), &status); err == nil {
		return status, true
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return AuthenticationStatus{}, false
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &status); err != nil {
		return AuthenticationStatus{}, false
	}
	return status, true
}

func AuthenticationDetail(status AuthenticationStatus) string {
	parts := []string{"Claude Code ready"}
	if method := strings.TrimSpace(status.AuthMethod); method != "" {
		parts = append(parts, "via "+method)
	}
	if subscription := strings.TrimSpace(status.SubscriptionType); subscription != "" {
		parts = append(parts, "("+subscription+")")
	}
	return strings.Join(parts, " ")
}
