package codexapp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
)

func TestLiveLCAgentOpenAIVisionCheck(t *testing.T) {
	model := strings.TrimSpace(os.Getenv("LCROOM_LIVE_OPENAI_VISION_MODEL"))
	if model == "" {
		t.Skip("set LCROOM_LIVE_OPENAI_VISION_MODEL to run the live OpenAI vision check")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		cfg, err := config.Parse("doctor", nil)
		if err != nil {
			t.Fatalf("load config for live OpenAI vision check: %v", err)
		}
		apiKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	}
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY/openai_api_key is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := CheckLCAgentVisionAccess(ctx, LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           t.TempDir(),
		LCAgentProvider:       "openai",
		LCAgentOpenAIAPIKey:   apiKey,
		LCAgentVisionProvider: "openai",
		LCAgentVisionModel:    model,
		LCAgentRequestTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("live OpenAI vision check for %s failed: %v", model, err)
	}
	response := strings.TrimSpace(result.Response)
	t.Logf("live OpenAI vision check result: provider=%s model=%s verified=%v top_left=%q bottom_right=%q response=%q", result.Provider, result.Model, result.Verified, result.ObservedTopLeft, result.ObservedBottomRight, response)
	if response == "" {
		t.Fatalf("live OpenAI vision check returned empty response")
	}
	if !result.Verified {
		t.Fatalf("live OpenAI vision check did not verify pixel inspection")
	}
}
