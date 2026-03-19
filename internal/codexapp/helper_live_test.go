package codexapp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPromptHelperLive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LCROOM_RUN_LIVE_CODEX_HELPER_TEST")) == "" {
		t.Skip("set LCROOM_RUN_LIVE_CODEX_HELPER_TEST=1 to run against a real local Codex install")
	}

	helper, err := NewPromptHelper()
	if err != nil {
		t.Fatalf("NewPromptHelper() error = %v", err)
	}
	defer func() {
		_ = helper.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	response, err := helper.Run(ctx, PromptHelperRequest{
		Prompt: "Reply with exactly this JSON and nothing else: {\"ok\":true}",
		Model:  "gpt-5.4-mini",
	})
	if err != nil {
		t.Fatalf("PromptHelper.Run() error = %v; snapshot = %#v", err, helper.session.Snapshot())
	}
	if strings.TrimSpace(response.OutputText) == "" {
		t.Fatalf("PromptHelper.Run() returned empty output; snapshot = %#v", helper.session.Snapshot())
	}
	t.Logf("helper output: %s", response.OutputText)
}
