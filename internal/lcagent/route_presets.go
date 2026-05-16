package lcagent

import (
	"fmt"
	"io"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
)

type lcagentRoutePreset struct {
	Name            string
	DisplayName     string
	Description     string
	Provider        string
	Model           string
	FinalModel      string
	ReasoningEffort string
	Auto            string
	ToolProfile     string
	ContextProfile  string
	RequestTimeout  time.Duration
	ProviderOnly    []string
	Temperature     string
}

func lcagentRoutePresets() []lcagentRoutePreset {
	return []lcagentRoutePreset{
		{
			Name:            "balanced",
			DisplayName:     "Balanced Coding",
			Description:     "Default coding lane: DeepSeek V4 Pro through OpenRouter with high reasoning and conservative tool/context budgets.",
			Provider:        "openrouter",
			Model:           modeladapter.DefaultOpenRouterModel,
			ReasoningEffort: "high",
			Auto:            "low",
			ToolProfile:     "balanced",
			ContextProfile:  "balanced",
			RequestTimeout:  10 * time.Minute,
			Temperature:     "0.2",
		},
		{
			Name:            "quality",
			DisplayName:     "Quality Coding",
			Description:     "Higher-quality direct OpenAI lane for important coding tasks, with low reasoning and larger retained context.",
			Provider:        "openai",
			Model:           modeladapter.DefaultOpenAIModel,
			ReasoningEffort: "low",
			Auto:            "low",
			ToolProfile:     "balanced",
			ContextProfile:  "large",
			RequestTimeout:  10 * time.Minute,
			Temperature:     "omitted",
		},
		{
			Name:           "cheap-scout",
			DisplayName:    "Cheap Scout",
			Description:    "Low-cost read-first lane for bounded exploration, summaries, and small follow-up tasks.",
			Provider:       "openrouter",
			Model:          "deepseek/deepseek-v4-flash",
			Auto:           "off",
			ToolProfile:    "balanced",
			ContextProfile: "balanced",
			RequestTimeout: 10 * time.Minute,
			Temperature:    "0.2",
		},
	}
}

func lcagentRoutePresetByName(name string) (lcagentRoutePreset, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "":
		return lcagentRoutePreset{}, false
	case "scout", "cheap", "cheapscout":
		normalized = "cheap-scout"
	}
	for _, preset := range lcagentRoutePresets() {
		if preset.Name == normalized {
			return preset, true
		}
	}
	return lcagentRoutePreset{}, false
}

func lcagentRoutePresetNames() string {
	presets := lcagentRoutePresets()
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	return strings.Join(names, ", ")
}

func printLCAgentRoutePresets(stdout io.Writer) {
	for _, preset := range lcagentRoutePresets() {
		fmt.Fprintf(stdout, "%s\t%s\tprovider=%s model=%s auto=%s tool=%s context=%s reasoning=%s\n",
			preset.Name,
			preset.Description,
			preset.Provider,
			preset.Model,
			preset.Auto,
			preset.ToolProfile,
			preset.ContextProfile,
			lcagentRoutePresetDisplayDefault(preset.ReasoningEffort),
		)
	}
}

func lcagentRoutePresetDisplayDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(default)"
	}
	return value
}
