package control

import (
	"fmt"
	"strings"
	"time"
)

// EngineerModelSelection is an operation-local choice, never a settings update.
type EngineerModelSelection struct {
	Model           string `json:"model,omitempty"`
	ModelProvider   string `json:"model_provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	SelectModel     bool   `json:"select_model,omitempty"`
}

func (s *EngineerModelSelection) Normalize(provider Provider) error {
	s.Model = strings.TrimSpace(s.Model)
	s.ModelProvider = strings.TrimSpace(s.ModelProvider)
	s.ReasoningEffort = strings.TrimSpace(s.ReasoningEffort)
	if s.Model == "" && (s.ReasoningEffort != "" || s.ModelProvider != "") {
		return fmt.Errorf("model is required with reasoning_effort or model_provider; discover exact choices with engineer.models")
	}
	if (s.Model != "" || s.SelectModel) && provider.Normalized() == ProviderAuto {
		return fmt.Errorf("model selection requires an explicit engineer provider")
	}
	if s.ModelProvider != "" && provider.Normalized() != ProviderLCAgent {
		return fmt.Errorf("model_provider is only supported for lcagent")
	}
	if s.SelectModel && s.Model != "" {
		return fmt.Errorf("select_model cannot be combined with an explicit model")
	}
	return nil
}

type EngineerModel struct {
	Model                  string   `json:"model"`
	ModelProvider          string   `json:"model_provider,omitempty"`
	DisplayName            string   `json:"display_name"`
	ReasoningEfforts       []string `json:"reasoning_efforts"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	IsDefault              bool     `json:"is_default"`
}

type EngineerModelCatalog struct {
	Provider   Provider        `json:"provider"`
	Source     string          `json:"source"`
	ObservedAt time.Time       `json:"observed_at"`
	Models     []EngineerModel `json:"models"`
}

// Validate checks exact protocol identifiers, not natural-language aliases.
func (c EngineerModelCatalog) Validate(s EngineerModelSelection) error {
	if s.Model == "" {
		return nil
	}
	for _, option := range c.Models {
		if option.Model != s.Model || option.ModelProvider != s.ModelProvider {
			continue
		}
		if s.ReasoningEffort == "" {
			return nil
		}
		for _, effort := range option.ReasoningEfforts {
			if effort == s.ReasoningEffort {
				return nil
			}
		}
		return fmt.Errorf("reasoning effort %q is unsupported for %q; supported: %s", s.ReasoningEffort, s.Model, strings.Join(option.ReasoningEfforts, ", "))
	}
	return fmt.Errorf("model %q was not found in the %s catalog; query engineer.models or use select_model", s.Model, c.Provider)
}

func engineerModelProperty() map[string]any {
	return map[string]any{"type": "string", "description": "Optional exact model ID from the engineer.models query. Requires explicit provider. Omit to preserve defaults."}
}
func engineerModelProviderProperty() map[string]any {
	return map[string]any{"type": "string", "description": "For lcagent, exact model_provider returned by engineer.models."}
}
func engineerEffortProperty() map[string]any {
	return map[string]any{"type": "string", "description": "Optional supported reasoning effort from engineer.models; requires model."}
}
func engineerSelectModelProperty() map[string]any {
	return map[string]any{"type": "boolean", "description": "Open the host model/effort picker before any work starts. Requires explicit provider; mutually exclusive with model. reveal only shows the session and does not open a picker."}
}
