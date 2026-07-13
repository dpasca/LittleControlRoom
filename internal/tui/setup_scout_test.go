package tui

import (
	"strings"
	"testing"

	"lcroom/internal/config"
)

func TestSetupRepositoryScoutSummaryExplainsAutomaticChatInheritance(t *testing.T) {
	settings := config.EditableSettings{
		BossChatBackend:  config.AIBackendDeepSeek,
		BossUtilityModel: "deepseek-v4-flash",
	}
	summary := setupReviewRepositoryScoutSummary(settings)
	for _, want := range []string{"Automatic route order", "inherited Chat utility", "No separate LCAgent setup is required", "route, evidence, and trace", "do not imply repository content is absent"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}

func TestSetupRepositoryScoutSummaryExplainsExplicitOverrideAndFallback(t *testing.T) {
	settings := config.EditableSettings{
		BossChatBackend:    config.AIBackendDeepSeek,
		BossUtilityModel:   "deepseek-v4-flash",
		LCAgentRoutePreset: "quality",
	}
	summary := setupReviewRepositoryScoutSummary(settings)
	for _, want := range []string{"Explicit LCAgent", "first", "falls back to inherited Chat utility", "Duplicate provider/model routes are skipped", "failures list every attempted route"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}
