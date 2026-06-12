package lcagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
)

const (
	defaultCriticProvider = "off"
	defaultCriticModel    = ""
)

type criticProfile struct {
	Enabled     bool
	Provider    string
	Model       string
	Message     string
	Reviewer    traceCritic
	DisabledErr error
}

type traceCritic struct {
	provider string
	client   *modeladapter.Client
}

type criticReviewPacket struct {
	SessionID       string                 `json:"session_id"`
	UserRequest     string                 `json:"user_request"`
	FinalOutcome    string                 `json:"final_outcome"`
	FinalSummary    string                 `json:"final_summary"`
	FilesChanged    []string               `json:"files_changed,omitempty"`
	Verification    []string               `json:"verification,omitempty"`
	ContextMode     string                 `json:"context_mode"`
	MessagesOmitted int                    `json:"messages_omitted,omitempty"`
	Messages        []criticPacketMessage  `json:"messages"`
	EvidenceNotes   []string               `json:"evidence_notes,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

type criticPacketMessage struct {
	Index      int                    `json:"index"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	ToolCalls  []criticPacketToolCall `json:"tool_calls,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"`
}

type criticPacketToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type criticReviewPayload struct {
	Status              string                 `json:"status"`
	Confidence          float64                `json:"confidence"`
	Summary             string                 `json:"summary"`
	Findings            []criticReviewFinding  `json:"findings"`
	ProposedUserMessage string                 `json:"proposed_user_message"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

type criticReviewFinding struct {
	Severity          string `json:"severity"`
	Claim             string `json:"claim"`
	EvidenceSource    string `json:"evidence_source"`
	Evidence          string `json:"evidence"`
	SuggestedFollowup string `json:"suggested_followup"`
}

func newCriticProfile(provider string, cfg modeladapter.OpenRouterConfig, mainProvider string, mainModel string) criticProfile {
	provider, err := normalizeCriticProvider(provider)
	if err != nil {
		return criticProfile{
			Enabled:     false,
			Provider:    strings.TrimSpace(provider),
			Message:     err.Error(),
			DisabledErr: err,
		}
	}
	sameAsMain := provider == "main"
	if sameAsMain {
		provider = normalizeMainProvider(mainProvider)
		cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), strings.TrimSpace(mainModel), defaultMainModelForProvider(provider))
	}
	if provider == "off" {
		return criticProfile{
			Enabled:  false,
			Provider: provider,
			Message:  "LCAgent critic disabled.",
		}
	}
	cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), defaultMainModelForProvider(provider))
	cfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		return criticProfile{
			Enabled:     false,
			Provider:    provider,
			Model:       cfg.Model,
			Message:     "LCAgent critic unavailable: " + err.Error(),
			DisabledErr: err,
		}
	}
	message := "LCAgent trace-only critic enabled."
	if sameAsMain {
		message = "LCAgent trace-only critic uses the Main Model."
	}
	return criticProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		Message:  message,
		Reviewer: traceCritic{provider: provider, client: client},
	}
}

func normalizeCriticProvider(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	if value == "" {
		return defaultCriticProvider, nil
	}
	switch value {
	case "main", "same", "same-as-main":
		return "main", nil
	case "off", "openrouter", "openai", "deepseek", "moonshot", "xiaomi":
		return value, nil
	default:
		return "", fmt.Errorf("critic provider must be one of: main, off, openrouter, openai, deepseek, moonshot, xiaomi")
	}
}

func maybeRunTraceCritic(ctx context.Context, writer *session.Writer, runner script.Runner, profile criticProfile, final script.Action, messages []modeladapter.Message, compacted bool) error {
	if writer == nil || !profile.Enabled || profile.Reviewer.client == nil {
		return nil
	}
	packet := buildCriticReviewPacket(runner.SessionID, runner.Prompt, final, messages, compacted)
	packetHash := criticPacketHash(packet)
	if err := writer.WritePrivate(session.Event{
		"type":         "critic_review_packet",
		"session_id":   runner.SessionID,
		"packet_hash":  packetHash,
		"packet":       packet,
		"context_mode": packet.ContextMode,
	}); err != nil {
		return err
	}
	if err := writer.Write(session.Event{
		"type":        "critic_review_started",
		"session_id":  runner.SessionID,
		"provider":    profile.Provider,
		"model":       profile.Model,
		"packet_hash": packetHash,
		"mode":        "trace_only",
	}); err != nil {
		return err
	}
	review, completion, err := profile.Reviewer.Review(ctx, packet)
	if err != nil {
		return writer.Write(session.Event{
			"type":        "critic_review_failed",
			"session_id":  runner.SessionID,
			"provider":    profile.Provider,
			"model":       profile.Model,
			"packet_hash": packetHash,
			"message":     err.Error(),
		})
	}
	modelName := firstNonEmptyString(strings.TrimSpace(completion.Model), profile.Model)
	if err := writer.Write(session.Event{
		"type":          "critic_model_response",
		"session_id":    runner.SessionID,
		"provider":      profile.Provider,
		"model":         modelName,
		"packet_hash":   packetHash,
		"usage":         json.RawMessage(completion.Usage),
		"usage_summary": completion.UsageSummary,
	}); err != nil {
		return err
	}
	return writer.Write(session.Event{
		"type":                  "critic_review_result",
		"session_id":            runner.SessionID,
		"provider":              profile.Provider,
		"model":                 modelName,
		"mode":                  "trace_only",
		"packet_hash":           packetHash,
		"status":                normalizeCriticStatus(review.Status),
		"confidence":            review.Confidence,
		"summary":               strings.TrimSpace(review.Summary),
		"findings":              cleanCriticFindings(review.Findings),
		"proposed_user_message": strings.TrimSpace(review.ProposedUserMessage),
	})
}

func (r traceCritic) Review(ctx context.Context, packet criticReviewPacket) (criticReviewPayload, modeladapter.Completion, error) {
	if r.client == nil {
		return criticReviewPayload{}, modeladapter.Completion{}, fmt.Errorf("critic client is not configured")
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return criticReviewPayload{}, modeladapter.Completion{}, err
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: criticSystemPrompt()},
		{Role: "user", Content: string(body)},
	}
	options := modeladapter.CompletionOptions{MaxCompletionTokens: 1400}
	if !strings.EqualFold(r.provider, "openai") {
		options.DisableThinking = true
	}
	completion, err := r.client.CompleteWithOptions(ctx, messages, nil, options)
	if err != nil {
		return criticReviewPayload{}, completion, err
	}
	review, err := parseCriticReviewPayload(completion.Message.Content)
	if err != nil {
		return criticReviewPayload{}, completion, err
	}
	review.Status = normalizeCriticStatus(review.Status)
	review.Findings = cleanCriticFindings(review.Findings)
	review.ProposedUserMessage = strings.TrimSpace(review.ProposedUserMessage)
	if review.Status == "clean" {
		review.ProposedUserMessage = ""
	}
	return review, completion, nil
}

func criticSystemPrompt() string {
	return strings.Join([]string{
		"You are a trace-only critic for an AI coding agent turn.",
		"Review only the provided packet. You have no live tools and must not claim to inspect current files.",
		"Find concrete evidence that the lead turn may be wrong, incomplete, overclaimed, unsafe, or insufficiently verified.",
		"Use evidence_source values only from: tool_trace, patch, verification, lead_final, missing_evidence.",
		"If a concern cannot cite packet evidence, do not include it as a finding.",
		"Return only JSON. Do not use markdown.",
		"The JSON shape is:",
		`{"status":"clean|concerns|needs_followup","confidence":0.0,"summary":"short review summary","findings":[{"severity":"low|medium|high","claim":"concrete issue","evidence_source":"tool_trace|patch|verification|lead_final|missing_evidence","evidence":"specific packet evidence","suggested_followup":"what the next user message should ask"}],"proposed_user_message":"draft follow-up for the human to send, blank when status is clean"}`,
	}, "\n")
}

func buildCriticReviewPacket(sessionID, userRequest string, final script.Action, messages []modeladapter.Message, compacted bool) criticReviewPacket {
	const maxMessages = 80
	start := 0
	if len(messages) > maxMessages {
		start = len(messages) - maxMessages
	}
	packetMessages := make([]criticPacketMessage, 0, len(messages)-start)
	for i := start; i < len(messages); i++ {
		packetMessages = append(packetMessages, criticPacketMessageFromModelMessage(i, messages[i]))
	}
	notes := []string{
		"The critic is reviewing a packet captured after the lead model finished the turn.",
		"Claims about pre-edit state must cite tool_trace evidence; claims about final state must cite final messages, patch evidence, or verification evidence present in the packet.",
	}
	contextMode := threadContextModeForCompacted(compacted)
	if compacted {
		notes = append(notes, "The lead context was compacted before or during this turn; missing raw history should be reported as missing_evidence, not guessed.")
	}
	return criticReviewPacket{
		SessionID:       strings.TrimSpace(sessionID),
		UserRequest:     strings.TrimSpace(userRequest),
		FinalOutcome:    normalizeFinalOutcomeForCritic(final.Outcome),
		FinalSummary:    strings.TrimSpace(final.Summary),
		FilesChanged:    cleanCriticStringList(final.FilesChanged),
		Verification:    cleanCriticStringList(final.Verification),
		ContextMode:     contextMode,
		MessagesOmitted: start,
		Messages:        packetMessages,
		EvidenceNotes:   notes,
	}
}

func criticPacketMessageFromModelMessage(index int, msg modeladapter.Message) criticPacketMessage {
	limit := 2200
	if msg.Role == "tool" {
		limit = 3200
	}
	content, truncated := truncateCriticText(msg.Content, limit)
	out := criticPacketMessage{
		Index:      index,
		Role:       strings.TrimSpace(msg.Role),
		Content:    content,
		ToolCallID: strings.TrimSpace(msg.ToolCallID),
		Truncated:  truncated,
	}
	for _, call := range msg.ToolCalls {
		args, argsTruncated := truncateCriticText(string(call.Function.Arguments), 1600)
		out.ToolCalls = append(out.ToolCalls, criticPacketToolCall{
			ID:        strings.TrimSpace(call.ID),
			Name:      strings.TrimSpace(call.Function.Name),
			Arguments: args,
			Truncated: argsTruncated,
		})
	}
	return out
}

func truncateCriticText(text string, limit int) (string, bool) {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	if limit < 32 {
		return text[:limit], true
	}
	return strings.TrimSpace(text[:limit-24]) + "\n...[truncated]...", true
}

func parseCriticReviewPayload(content string) (criticReviewPayload, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	var payload criticReviewPayload
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
	return criticReviewPayload{}, fmt.Errorf("critic returned invalid JSON")
}

func normalizeCriticStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "clean":
		return "clean"
	case "needs_followup", "needs-followup", "followup", "needs_investigation":
		return "needs_followup"
	case "concern", "concerns", "warning", "warnings":
		return "concerns"
	default:
		return "concerns"
	}
}

func cleanCriticFindings(findings []criticReviewFinding) []criticReviewFinding {
	out := make([]criticReviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Severity = normalizeCriticSeverity(finding.Severity)
		finding.EvidenceSource = normalizeCriticEvidenceSource(finding.EvidenceSource)
		finding.Claim = strings.TrimSpace(finding.Claim)
		finding.Evidence = strings.TrimSpace(finding.Evidence)
		finding.SuggestedFollowup = strings.TrimSpace(finding.SuggestedFollowup)
		if finding.Claim == "" && finding.Evidence == "" {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func normalizeCriticSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(severity))
	default:
		return "medium"
	}
}

func normalizeCriticEvidenceSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "tool_trace", "patch", "verification", "lead_final", "missing_evidence":
		return strings.ToLower(strings.TrimSpace(source))
	default:
		return "missing_evidence"
	}
}

func cleanCriticStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeFinalOutcomeForCritic(outcome string) string {
	normalized := strings.ToLower(strings.TrimSpace(outcome))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func criticPacketHash(packet criticReviewPacket) string {
	body, err := json.Marshal(packet)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
