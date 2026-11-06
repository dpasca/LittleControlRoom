package config

import (
	"lcroom/internal/modelcatalog"
	"strings"
)

const LCAgentSingleModel = "single-model"

func LCAgentUsesSingleModel(preset string) bool {
	return strings.EqualFold(strings.TrimSpace(preset), LCAgentSingleModel)
}

// Bundled routes own a particular provider/model. Single-model setup follows
// the operator's model selection and leaves permissions/profiles independent.
func LCAgentHasBundledRoute(preset string) bool {
	return strings.TrimSpace(preset) != "" && !LCAgentUsesSingleModel(preset)
}

// Retired Balanced configurations become ordinary explicit model selections.
// Keep custom helper routes and budgets; the old bundle owned main model and permissions.
func migrateLCAgentBalancedPreset(preset, provider, model, reasoning, auto *string) {
	if !strings.EqualFold(strings.TrimSpace(*preset), "balanced") {
		return
	}
	*preset = ""
	*provider = "deepseek"
	*model = modelcatalog.DeepSeekProModel
	if strings.TrimSpace(*reasoning) == "" {
		*reasoning = "high"
	}
	*auto = "low"
}
