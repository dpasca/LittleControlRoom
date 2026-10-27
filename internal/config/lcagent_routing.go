package config

import "strings"

const LCAgentSingleModel = "single-model"

func LCAgentUsesSingleModel(preset string) bool {
	return strings.EqualFold(strings.TrimSpace(preset), LCAgentSingleModel)
}

// Bundled routes own a particular provider/model. Single-model setup follows
// the operator's model selection and leaves permissions/profiles independent.
func LCAgentHasBundledRoute(preset string) bool {
	return strings.TrimSpace(preset) != "" && !LCAgentUsesSingleModel(preset)
}
