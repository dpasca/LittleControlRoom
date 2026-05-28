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
		mimo25ProRoutePreset("low"),
		mimo25ProRoutePreset("high"),
		mimo25ProRoutePreset("max"),
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
	case "mimo", "mimo-pro", "mimo25pro", "mimo-25-pro", "mimo-2.5-pro", "xiaomi", "xiaomi-mimo":
		normalized = "mimo-2.5-pro-low"
	}
	for _, preset := range lcagentRoutePresets() {
		if preset.Name == normalized {
			return preset, true
		}
	}
	return lcagentRoutePreset{}, false
}

func mimo25ProRoutePreset(reasoningEffort string) lcagentRoutePreset {
	reasoningEffort = strings.ToLower(strings.TrimSpace(reasoningEffort))
	if reasoningEffort == "" {
		reasoningEffort = "low"
	}
	requestEffort := reasoningEffort
	if reasoningEffort == "max" {
		requestEffort = "xhigh"
	}
	return lcagentRoutePreset{
		Name:            "mimo-2.5-pro-" + reasoningEffort,
		DisplayName:     "MiMo 2.5 Pro " + strings.ToUpper(reasoningEffort[:1]) + reasoningEffort[1:],
		Description:     "Xiaomi MiMo-V2.5-Pro " + reasoningEffort + "-reasoning benchmark lane through OpenRouter with Xiaomi provider pinning and larger retained context.",
		Provider:        "openrouter",
		Model:           "xiaomi/mimo-v2.5-pro",
		ReasoningEffort: requestEffort,
		Auto:            "low",
		ToolProfile:     "balanced",
		ContextProfile:  "large",
		RequestTimeout:  10 * time.Minute,
		ProviderOnly:    []string{"xiaomi"},
		Temperature:     "0.2",
	}
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
