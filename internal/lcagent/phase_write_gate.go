package lcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
)

const phaseWriteGateMaxCompletionTokens = 700

type phaseWriteGateProfile struct {
	Enabled     bool
	Provider    string
	Model       string
	Message     string
	DisabledErr error
	client      *modeladapter.Client
}

type phaseWriteGate struct {
	profile phaseWriteGateProfile
	writer  *session.Writer
}

type phaseWriteGatePayload struct {
	Allow                  bool   `json:"allow"`
	FitsActivePhase        bool   `json:"fits_active_phase"`
	ContainsLaterPhaseWork bool   `json:"contains_later_phase_work"`
	TooMuchAtOnce          bool   `json:"too_much_at_once"`
	Reason                 string `json:"reason"`
	SuggestedSmallerSlice  string `json:"suggested_smaller_slice"`
}

func newPhaseWriteGateProfile(enabled bool, provider string, cfg modeladapter.OpenRouterConfig, mainProvider string, mainModel string) phaseWriteGateProfile {
	if !enabled {
		return phaseWriteGateProfile{
			Enabled:  false,
			Provider: "off",
			Message:  "LCAgent phase write gate disabled.",
		}
	}
	provider, err := normalizeUtilityProvider(provider)
	if err != nil {
		return phaseWriteGateProfile{
			Enabled:     false,
			Provider:    strings.TrimSpace(provider),
			Message:     err.Error(),
			DisabledErr: err,
		}
	}
	if provider == "off" {
		provider = "main"
	}
	sameAsMain := provider == "main"
	if sameAsMain {
		provider = normalizeMainProvider(mainProvider)
		cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), strings.TrimSpace(mainModel), defaultMainModelForProvider(provider))
	}
	cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), defaultMainModelForProvider(provider))
	cfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
	cfg.MaxTurns = 1
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		return phaseWriteGateProfile{
			Enabled:     false,
			Provider:    provider,
			Model:       cfg.Model,
			Message:     "LCAgent phase write gate unavailable: " + err.Error(),
			DisabledErr: err,
		}
	}
	message := "LCAgent phase write gate enabled."
	if sameAsMain {
		message = "LCAgent phase write gate uses the Main Model."
	}
	return phaseWriteGateProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		Message:  message,
		client:   client,
	}
}

func (g phaseWriteGate) EvaluatePhaseWrite(ctx context.Context, request script.PhaseWriteGateRequest) (script.PhaseWriteGateResult, error) {
	if !g.profile.Enabled || g.profile.client == nil {
		return script.PhaseWriteGateResult{}, fmt.Errorf("phase write gate client is not configured")
	}
	messages := phaseWriteGateMessages(request)
	options := modeladapter.CompletionOptions{
		MaxCompletionTokens: phaseWriteGateMaxCompletionTokens,
	}
	completion, err := completeProviderWithRetriesValidated(ctx, g.writer, request.SessionID, g.profile.Provider, "phase_write_gate", 0, g.profile.client.Model(), validatePhaseWriteGateCompletion(g.profile.Provider), func() (modeladapter.Completion, error) {
		return g.profile.client.CompleteWithOptions(ctx, messages, nil, options)
	})
	if err != nil {
		return script.PhaseWriteGateResult{}, err
	}
	if g.writer != nil {
		if err := g.writer.Write(modelResponseEvent(request.SessionID, g.profile.Provider, "phase_write_gate", 0, completion, len(completion.Message.ToolCalls))); err != nil {
			return script.PhaseWriteGateResult{}, err
		}
	}
	content, _ := modeladapter.SanitizeAssistantContent(completion.Message.Content)
	payload, err := parsePhaseWriteGatePayload(content)
	if err != nil {
		return script.PhaseWriteGateResult{}, err
	}
	payload = normalizePhaseWriteGatePayload(payload)
	return script.PhaseWriteGateResult{
		Allow:                  payload.Allow,
		FitsActivePhase:        payload.FitsActivePhase,
		ContainsLaterPhaseWork: payload.ContainsLaterPhaseWork,
		TooMuchAtOnce:          payload.TooMuchAtOnce,
		Reason:                 payload.Reason,
		SuggestedSmallerSlice:  payload.SuggestedSmallerSlice,
		Provider:               g.profile.Provider,
		Model:                  firstNonEmptyString(strings.TrimSpace(completion.Model), g.profile.Model),
		Usage:                  json.RawMessage(completion.Usage),
		UsageSummary:           completion.UsageSummary,
	}, nil
}

func phaseWriteGateMessages(request script.PhaseWriteGateRequest) []modeladapter.Message {
	system := `You are a phase write gate for a coding agent.
Return only one JSON object. Do not include Markdown or prose.

Schema:
{
  "allow": boolean,
  "fits_active_phase": boolean,
  "contains_later_phase_work": boolean,
  "too_much_at_once": boolean,
  "reason": "one concise reason",
  "suggested_smaller_slice": "concrete smaller write to attempt when blocked"
}

Goal: positively prevent the lead model from biting off too much.
Allow writes that implement the current active phase or small support scaffolding needed for that phase to compile/run.
Deny writes that substantially implement later phases, produce a broad all-in-one artifact when only an early slice is active, or skip the active phase.
Judge semantically from the active phase and acceptance criteria, not by line count alone.`
	user := phaseWriteGateUserPrompt(request)
	return []modeladapter.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

func phaseWriteGateUserPrompt(request script.PhaseWriteGateRequest) string {
	var b strings.Builder
	if userRequest := strings.TrimSpace(request.UserRequest); userRequest != "" {
		b.WriteString("Original user request:\n")
		b.WriteString(userRequest)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Tool: %s\n", strings.TrimSpace(request.Tool))
	fmt.Fprintf(&b, "Active phase index: %d\n", request.ActivePhaseIndex+1)
	fmt.Fprintf(&b, "Active phase name: %s\n", strings.TrimSpace(request.ActivePhase.Name))
	fmt.Fprintf(&b, "Active phase status: %s\n", strings.TrimSpace(request.ActivePhase.Status))
	if len(request.ActivePhase.Acceptance) > 0 {
		b.WriteString("Active phase acceptance:\n")
		for _, item := range request.ActivePhase.Acceptance {
			if item = strings.TrimSpace(item); item != "" {
				fmt.Fprintf(&b, "- %s\n", item)
			}
		}
	}
	if len(request.CompletedPhaseNames) > 0 {
		fmt.Fprintf(&b, "\nCompleted/skipped phases: %s\n", strings.Join(request.CompletedPhaseNames, "; "))
	}
	if len(request.RemainingPhaseNames) > 0 {
		fmt.Fprintf(&b, "Remaining phases from active onward: %s\n", strings.Join(request.RemainingPhaseNames, "; "))
	}
	b.WriteString("\nFull quality plan phase statuses:\n")
	for i, phase := range request.QualityPlan.Phases {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, strings.TrimSpace(phase.Status), strings.TrimSpace(phase.Name))
		if len(phase.Acceptance) > 0 {
			fmt.Fprintf(&b, "   acceptance: %s\n", strings.Join(phase.Acceptance, "; "))
		}
	}
	b.WriteString("\nProposed write tool arguments")
	if request.ToolArgsTruncated {
		b.WriteString(" (truncated)")
	}
	b.WriteString(":\n")
	b.WriteString(request.ToolArgs)
	return b.String()
}

func validatePhaseWriteGateCompletion(provider string) func(modeladapter.Completion) error {
	provider = strings.TrimSpace(provider)
	return func(completion modeladapter.Completion) error {
		if len(completion.Message.ToolCalls) > 0 {
			return &modeladapter.ProviderError{
				Provider:  provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   "phase write gate returned tool calls instead of JSON",
				Retryable: true,
			}
		}
		content, _ := modeladapter.SanitizeAssistantContent(completion.Message.Content)
		if strings.TrimSpace(content) == "" {
			return &modeladapter.ProviderError{
				Provider:  provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   "phase write gate returned empty content",
				Retryable: true,
			}
		}
		if _, err := parsePhaseWriteGatePayload(content); err != nil {
			return &modeladapter.ProviderError{
				Provider:  provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   err.Error(),
				Retryable: true,
			}
		}
		return nil
	}
}

func parsePhaseWriteGatePayload(content string) (phaseWriteGatePayload, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	var payload phaseWriteGatePayload
	if err := json.Unmarshal([]byte(content), &payload); err == nil {
		return payload, nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(content[start:end+1]), &payload); err == nil {
			return payload, nil
		}
	}
	return phaseWriteGatePayload{}, fmt.Errorf("phase write gate returned invalid JSON")
}

func normalizePhaseWriteGatePayload(payload phaseWriteGatePayload) phaseWriteGatePayload {
	payload.Reason = truncatePlanningPreflightText(payload.Reason, 500)
	payload.SuggestedSmallerSlice = truncatePlanningPreflightText(payload.SuggestedSmallerSlice, 500)
	if !payload.FitsActivePhase || payload.ContainsLaterPhaseWork || payload.TooMuchAtOnce {
		payload.Allow = false
	}
	if strings.TrimSpace(payload.Reason) == "" {
		if payload.Allow {
			payload.Reason = "write fits the current active phase"
		} else {
			payload.Reason = "write does not fit the current active phase"
		}
	}
	return payload
}
