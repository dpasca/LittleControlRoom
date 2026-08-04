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

func TestSetupRepositoryScoutSummaryKeepsConfiguredWorkerBehindChat(t *testing.T) {
	settings := config.EditableSettings{
		BossChatBackend:    config.AIBackendDeepSeek,
		BossUtilityModel:   "deepseek-v4-flash",
		LCAgentRoutePreset: "quality",
	}
	summary := setupReviewRepositoryScoutSummary(settings)
	for _, want := range []string{"inherited Chat utility", "configured LCAgent Quality Coding worker fallback", "Chat inference remains the first choice", "Duplicate provider/model routes are skipped", "failures list every attempted route"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}
