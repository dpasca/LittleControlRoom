package lcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
)

const (
	planningPreflightPhaseLimit          = 6
	planningPreflightAcceptanceLimit     = 5
	planningPreflightTextLimit           = 8000
	planningPreflightReasonLimit         = 600
	planningPreflightPhaseTextLimit      = 160
	planningPreflightMaxCompletionTokens = 1200
)

type planningPreflightProfile struct {
	Enabled     bool
	Provider    string
	Model       string
	Message     string
	DisabledErr error
	client      *modeladapter.Client
}

type planningPreflightPayload struct {
	Scope                              string                   `json:"scope"`
	NeedsPreplan                       bool                     `json:"needs_preplan"`
	ArtifactType                       string                   `json:"artifact_type"`
	RequiresRuntimeVerification        bool                     `json:"requires_runtime_verification"`
	RequiresVisualVerification         bool                     `json:"requires_visual_verification"`
	RequiresTemporalVisualVerification bool                     `json:"requires_temporal_visual_verification"`
	Reason                             string                   `json:"reason"`
	SuggestedPhases                    []planningPreflightPhase `json:"suggested_phases,omitempty"`
}

type planningPreflightPhase struct {
	Name       string   `json:"name"`
	Acceptance []string `json:"acceptance,omitempty"`
}

func newPlanningPreflightProfile(enabled bool, provider string, cfg modeladapter.OpenRouterConfig, mainProvider string, mainModel string) planningPreflightProfile {
	if !enabled {
		return planningPreflightProfile{
			Enabled:  false,
			Provider: "off",
			Message:  "LCAgent planning preflight disabled.",
		}
	}
	provider, err := normalizeUtilityProvider(provider)
	if err != nil {
		return planningPreflightProfile{
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
		return planningPreflightProfile{
			Enabled:     false,
			Provider:    provider,
			Model:       cfg.Model,
			Message:     "LCAgent planning preflight unavailable: " + err.Error(),
			DisabledErr: err,
		}
	}
	message := "LCAgent planning preflight enabled."
	if sameAsMain {
		message = "LCAgent planning preflight uses the Main Model."
	}
	return planningPreflightProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		Message:  message,
		client:   client,
	}
}

func runPlanningPreflight(ctx context.Context, writer *session.Writer, sessionID string, profile planningPreflightProfile, prompt string, visionAvailable bool) (planningPreflightPayload, bool, error) {
	if !profile.Enabled || profile.client == nil {
		return planningPreflightPayload{}, false, nil
	}
	messages := planningPreflightMessages(prompt, visionAvailable)
	options := modeladapter.CompletionOptions{
		MaxCompletionTokens: planningPreflightMaxCompletionTokens,
	}
	completion, err := completeProviderWithRetriesValidated(ctx, writer, sessionID, profile.Provider, "planning_preflight", 0, profile.client.Model(), validatePlanningPreflightCompletion(profile.Provider), func() (modeladapter.Completion, error) {
		return profile.client.CompleteWithOptions(ctx, messages, nil, options)
	})
	if err != nil {
		if writeErr := writePlanningPreflightFailed(writer, sessionID, profile, err); writeErr != nil {
			return planningPreflightPayload{}, true, writeErr
		}
		return planningPreflightPayload{}, true, nil
	}
	if err := writer.Write(modelResponseEvent(sessionID, profile.Provider, "planning_preflight", 0, completion, len(completion.Message.ToolCalls))); err != nil {
		return planningPreflightPayload{}, true, err
	}
	content, _ := modeladapter.SanitizeAssistantContent(completion.Message.Content)
	payload, err := parsePlanningPreflightPayload(content)
	if err != nil {
		if writeErr := writePlanningPreflightFailed(writer, sessionID, profile, err); writeErr != nil {
			return planningPreflightPayload{}, true, writeErr
		}
		return planningPreflightPayload{}, true, nil
	}
	payload = normalizePlanningPreflightPayload(payload)
	if err := writePlanningPreflightResult(writer, sessionID, profile, payload, completion); err != nil {
		return planningPreflightPayload{}, true, err
	}
	return payload, true, nil
}

func planningPreflightMessages(prompt string, visionAvailable bool) []modeladapter.Message {
	visualCapability := "unavailable"
	if visionAvailable {
		visualCapability = "available"
	}
	system := `You classify a coding-agent user request before implementation.
Return only one JSON object. Do not include Markdown or prose.

Schema:
{
  "scope": "simple" | "medium" | "sizable",
  "needs_preplan": boolean,
  "artifact_type": "none" | "code_edit" | "game" | "app" | "ui" | "document" | "analysis" | "other",
  "requires_runtime_verification": boolean,
  "requires_visual_verification": boolean,
  "requires_temporal_visual_verification": boolean,
  "reason": "one short reason",
  "suggested_phases": [
    {"name": "short phase name", "acceptance": ["observable acceptance check"]}
  ]
}

Use "sizable" when the request asks for a substantial new artifact, game, app, UI, generated document, multi-part implementation, or ambiguous work where implementation quality depends on sequencing.
Use "medium" for normal code changes that benefit from a brief plan but do not need a mandatory phased quality plan.
Use "simple" for direct answers, tiny inspections, or narrowly-scoped edits.
Set needs_preplan true when the lead should publish a phased update_quality_plan before claiming completion.
For games, apps, UI, and visual artifacts, set requires_visual_verification true when image analysis is available and the user-facing result should be judged visually.
Set requires_temporal_visual_verification true when image analysis is available and the visual artifact is interactive, animated, camera-driven, live-updating, stateful, or otherwise expected to change over time; false for static visual artifacts.
Keep suggested_phases concrete and ordered; omit them for simple tasks.
For games, UI, and visual artifacts, order phases so the recognizable user-facing scene appears early: first a stable render/movement foundation, then the main visible setting/composition, then controls/systems/HUD/NPCs/polish. Do not bury the requested visual identity in late phases behind invisible mechanics.
For 3D or spatial visual artifacts, include acceptance checks for coordinate/transform sanity, grounded objects, camera framing, plausible scale, and layering/occlusion when those risks are relevant.`
	user := "Vision image analysis is " + visualCapability + ".\n\nUser request:\n" + limitPlanningPreflightText(prompt)
	return []modeladapter.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

func validatePlanningPreflightCompletion(provider string) func(modeladapter.Completion) error {
	provider = strings.TrimSpace(provider)
	return func(completion modeladapter.Completion) error {
		if len(completion.Message.ToolCalls) > 0 {
			return &modeladapter.ProviderError{
				Provider:  provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   "planning preflight returned tool calls instead of JSON",
				Retryable: true,
			}
		}
		content, _ := modeladapter.SanitizeAssistantContent(completion.Message.Content)
		if strings.TrimSpace(content) == "" {
			return &modeladapter.ProviderError{
				Provider:  provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   "planning preflight returned empty content",
				Retryable: true,
			}
		}
		if _, err := parsePlanningPreflightPayload(content); err != nil {
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

func parsePlanningPreflightPayload(content string) (planningPreflightPayload, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	var payload planningPreflightPayload
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
	return planningPreflightPayload{}, fmt.Errorf("planning preflight returned invalid JSON")
}

func normalizePlanningPreflightPayload(payload planningPreflightPayload) planningPreflightPayload {
	payload.Scope = normalizePlanningPreflightScope(payload.Scope)
	if payload.Scope == "sizable" {
		payload.NeedsPreplan = true
	}
	payload.ArtifactType = normalizePlanningPreflightArtifactType(payload.ArtifactType)
	if payload.RequiresTemporalVisualVerification {
		payload.RequiresVisualVerification = true
	}
	payload.Reason = truncatePlanningPreflightText(payload.Reason, planningPreflightReasonLimit)
	payload.SuggestedPhases = normalizePlanningPreflightPhases(payload.SuggestedPhases)
	return payload
}

func normalizePlanningPreflightScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "simple", "medium", "sizable":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "medium"
	}
}

func normalizePlanningPreflightArtifactType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "none", "code_edit", "game", "app", "ui", "document", "analysis", "other":
		return value
	case "":
		return "other"
	default:
		return truncatePlanningPreflightText(value, 40)
	}
}

func normalizePlanningPreflightPhases(phases []planningPreflightPhase) []planningPreflightPhase {
	var out []planningPreflightPhase
	for _, phase := range phases {
		name := truncatePlanningPreflightText(phase.Name, planningPreflightPhaseTextLimit)
		if name == "" {
			continue
		}
		normalized := planningPreflightPhase{Name: name}
		for _, item := range phase.Acceptance {
			item = truncatePlanningPreflightText(item, planningPreflightPhaseTextLimit)
			if item == "" {
				continue
			}
			normalized.Acceptance = append(normalized.Acceptance, item)
			if len(normalized.Acceptance) >= planningPreflightAcceptanceLimit {
				break
			}
		}
		out = append(out, normalized)
		if len(out) >= planningPreflightPhaseLimit {
			break
		}
	}
	return out
}

func planningPreflightRequiresQualityPlan(payload planningPreflightPayload) bool {
	payload = normalizePlanningPreflightPayload(payload)
	return payload.NeedsPreplan || payload.Scope == "sizable"
}

func planningPreflightLeadMessage(payload planningPreflightPayload) string {
	payload = normalizePlanningPreflightPayload(payload)
	var b strings.Builder
	b.WriteString("LCAgent planning preflight classified this request as ")
	b.WriteString(payload.Scope)
	if payload.ArtifactType != "" {
		b.WriteString(" (artifact_type=")
		b.WriteString(payload.ArtifactType)
		b.WriteString(")")
	}
	b.WriteString(" and requires a phased quality plan before write tools or completed final_response.\n\n")
	b.WriteString("Call update_quality_plan before implementation writes or completion. Use phases as execution gates: establish the core behavior first, then the main user-facing shape of the artifact, then layer details, then verify. A full draft is acceptable when that is the natural write shape, but it is still a draft until the phases have concrete evidence.\n")
	if payload.ArtifactType == "game" || payload.ArtifactType == "app" || payload.ArtifactType == "ui" {
		b.WriteString("For visual artifact work, make the recognizable scene or interface an early phase after the technical foundation; do not defer the requested visual identity behind invisible mechanics.\n")
		b.WriteString("For 3D or spatial visual work, include checks that objects are grounded or intentionally airborne, important surfaces are not covered by decorative layers, scale/camera framing is plausible, and coordinate transforms are understandable.\n")
	}
	if payload.RequiresRuntimeVerification {
		b.WriteString("\nThe plan should require runtime verification.")
	}
	if payload.RequiresVisualVerification {
		b.WriteString("\nThe plan should require visual verification.")
	}
	if payload.RequiresTemporalVisualVerification {
		b.WriteString("\nThe plan should require temporal visual verification with paired observations.")
	}
	if len(payload.SuggestedPhases) > 0 {
		b.WriteString("\n\nSuggested phase outline:")
		for _, phase := range payload.SuggestedPhases {
			b.WriteString("\n- ")
			b.WriteString(phase.Name)
			if len(phase.Acceptance) > 0 {
				b.WriteString(": ")
				b.WriteString(strings.Join(phase.Acceptance, "; "))
			}
		}
	}
	if payload.Reason != "" {
		b.WriteString("\n\nPreflight reason: ")
		b.WriteString(payload.Reason)
	}
	return b.String()
}

func writePlanningPreflightResult(writer *session.Writer, sessionID string, profile planningPreflightProfile, payload planningPreflightPayload, completion modeladapter.Completion) error {
	if writer == nil {
		return nil
	}
	return writer.Write(session.Event{
		"type":                                  "planning_preflight_result",
		"session_id":                            sessionID,
		"provider":                              strings.TrimSpace(profile.Provider),
		"model":                                 strings.TrimSpace(profile.Model),
		"scope":                                 strings.TrimSpace(payload.Scope),
		"needs_preplan":                         payload.NeedsPreplan,
		"requires_quality_plan":                 planningPreflightRequiresQualityPlan(payload),
		"artifact_type":                         strings.TrimSpace(payload.ArtifactType),
		"requires_runtime_verification":         payload.RequiresRuntimeVerification,
		"requires_visual_verification":          payload.RequiresVisualVerification,
		"requires_temporal_visual_verification": payload.RequiresTemporalVisualVerification,
		"reason":                                strings.TrimSpace(payload.Reason),
		"suggested_phases":                      payload.SuggestedPhases,
		"usage":                                 json.RawMessage(completion.Usage),
		"usage_summary":                         completion.UsageSummary,
	})
}

func writePlanningPreflightFailed(writer *session.Writer, sessionID string, profile planningPreflightProfile, err error) error {
	if writer == nil {
		return nil
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return writer.Write(session.Event{
		"type":       "planning_preflight_failed",
		"session_id": sessionID,
		"provider":   strings.TrimSpace(profile.Provider),
		"model":      strings.TrimSpace(profile.Model),
		"message":    strings.TrimSpace(message),
		"fallback":   "proceed_without_required_quality_plan",
	})
}

func limitPlanningPreflightText(value string) string {
	return truncatePlanningPreflightText(value, planningPreflightTextLimit)
}

func truncatePlanningPreflightText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= len("...") {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-len("...")]) + "..."
}
