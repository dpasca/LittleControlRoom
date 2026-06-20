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
	"lcroom/internal/lcagent/tools"
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
	provider        string
	reasoningEffort string
	client          *modeladapter.Client
}

type criticReviewPacket struct {
	SessionID       string                       `json:"session_id"`
	UserRequest     string                       `json:"user_request"`
	FinalOutcome    string                       `json:"final_outcome"`
	FinalSummary    string                       `json:"final_summary"`
	FilesChanged    []string                     `json:"files_changed,omitempty"`
	Verification    []string                     `json:"verification,omitempty"`
	ChangeReview    *script.ChangeReviewEvidence `json:"change_review,omitempty"`
	ContextMode     string                       `json:"context_mode"`
	MessagesOmitted int                          `json:"messages_omitted,omitempty"`
	Messages        []criticPacketMessage        `json:"messages"`
	EvidenceNotes   []string                     `json:"evidence_notes,omitempty"`
	Metadata        map[string]interface{}       `json:"metadata,omitempty"`
}

type criticConsultPacket struct {
	SessionID     string                     `json:"session_id"`
	UserRequest   string                     `json:"user_request,omitempty"`
	Kind          string                     `json:"kind"`
	Question      string                     `json:"question"`
	Context       string                     `json:"context,omitempty"`
	Candidate     string                     `json:"candidate,omitempty"`
	Checks        []string                   `json:"checks,omitempty"`
	Files         []script.CriticConsultFile `json:"files,omitempty"`
	EvidenceNotes []string                   `json:"evidence_notes,omitempty"`
}

type criticPacketMessage struct {
	Index            int                           `json:"index"`
	Role             string                        `json:"role"`
	Content          string                        `json:"content,omitempty"`
	ToolCallID       string                        `json:"tool_call_id,omitempty"`
	ToolCalls        []criticPacketToolCall        `json:"tool_calls,omitempty"`
	Truncated        bool                          `json:"truncated,omitempty"`
	ContentBytes     int                           `json:"content_bytes,omitempty"`
	EvidenceExcerpts []criticPacketEvidenceExcerpt `json:"evidence_excerpts,omitempty"`
}

type criticPacketToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type criticPacketEvidenceExcerpt struct {
	Source    string `json:"source,omitempty"`
	Match     string `json:"match,omitempty"`
	ByteStart int    `json:"byte_start,omitempty"`
	ByteEnd   int    `json:"byte_end,omitempty"`
	Text      string `json:"text,omitempty"`
}

type criticReviewPayload struct {
	Status              string                 `json:"status"`
	Confidence          float64                `json:"confidence"`
	Summary             string                 `json:"summary"`
	Findings            []criticReviewFinding  `json:"findings"`
	LeadInstruction     string                 `json:"lead_instruction,omitempty"`
	HumanPrompt         string                 `json:"human_prompt,omitempty"`
	ProposedUserMessage string                 `json:"proposed_user_message"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

type criticReviewFinding struct {
	Severity          string `json:"severity"`
	Materiality       string `json:"materiality,omitempty"`
	Basis             string `json:"basis,omitempty"`
	Claim             string `json:"claim"`
	EvidenceSource    string `json:"evidence_source"`
	Evidence          string `json:"evidence"`
	UserImpact        string `json:"user_impact,omitempty"`
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
	cfg.ReasoningEffort = openRouterReasoningEffortForProvider(provider, cfg.ReasoningEffort)
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
	message := "LCAgent critic enabled."
	if sameAsMain {
		message = "LCAgent critic uses the Main Model."
	}
	return criticProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		Message:  message,
		Reviewer: traceCritic{
			provider:        provider,
			reasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
			client:          client,
		},
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
	if runner.FileTouchEvents() == 0 {
		return nil
	}
	_, _, _, err := maybeRunCriticReview(ctx, writer, runner, profile, final, messages, compacted, "trace_only")
	return err
}

func maybeRunCriticReview(ctx context.Context, writer *session.Writer, runner script.Runner, profile criticProfile, final script.Action, messages []modeladapter.Message, compacted bool, mode string) (criticReviewPayload, string, bool, error) {
	if writer == nil || !profile.Enabled || profile.Reviewer.client == nil {
		return criticReviewPayload{}, "", false, nil
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "trace_only"
	}
	packet := buildCriticReviewPacketForRunner(runner, final, messages, compacted)
	packetHash := criticPacketHash(packet)
	if err := writer.WritePrivate(session.Event{
		"type":         "critic_review_packet",
		"session_id":   runner.SessionID,
		"packet_hash":  packetHash,
		"packet":       packet,
		"context_mode": packet.ContextMode,
		"mode":         mode,
	}); err != nil {
		return criticReviewPayload{}, packetHash, true, err
	}
	if err := writer.Write(session.Event{
		"type":        "critic_review_started",
		"session_id":  runner.SessionID,
		"provider":    profile.Provider,
		"model":       profile.Model,
		"packet_hash": packetHash,
		"mode":        mode,
	}); err != nil {
		return criticReviewPayload{}, packetHash, true, err
	}
	review, completion, err := runCriticReviewWithRetry(ctx, writer, profile, runner.SessionID, packet, packetHash, mode)
	if err != nil {
		event := session.Event{
			"type":        "critic_review_failed",
			"session_id":  runner.SessionID,
			"provider":    profile.Provider,
			"model":       profile.Model,
			"packet_hash": packetHash,
			"message":     err.Error(),
			"mode":        mode,
		}
		if strings.Contains(err.Error(), "invalid JSON") {
			event["failure_kind"] = "invalid_json"
		}
		return criticReviewPayload{}, packetHash, true, writer.Write(event)
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
		"mode":          mode,
	}); err != nil {
		return criticReviewPayload{}, packetHash, true, err
	}
	if err := writer.Write(session.Event{
		"type":                  "critic_review_result",
		"session_id":            runner.SessionID,
		"provider":              profile.Provider,
		"model":                 modelName,
		"mode":                  mode,
		"packet_hash":           packetHash,
		"status":                normalizeCriticStatus(review.Status),
		"confidence":            review.Confidence,
		"summary":               strings.TrimSpace(review.Summary),
		"findings":              cleanCriticFindings(review.Findings),
		"lead_instruction":      strings.TrimSpace(review.LeadInstruction),
		"human_prompt":          strings.TrimSpace(review.HumanPrompt),
		"proposed_user_message": strings.TrimSpace(review.ProposedUserMessage),
	}); err != nil {
		return criticReviewPayload{}, packetHash, true, err
	}
	return review, packetHash, true, nil
}

type criticConsultant struct {
	profile criticProfile
	writer  *session.Writer
}

func (c criticConsultant) ConsultCritic(ctx context.Context, request script.CriticConsultRequest) (script.CriticConsultResult, error) {
	if !c.profile.Enabled || c.profile.Reviewer.client == nil {
		return script.CriticConsultResult{}, fmt.Errorf("critic client is not configured")
	}
	packet := buildCriticConsultPacket(request)
	packetHash := criticPacketHash(packet)
	if c.writer != nil {
		if err := c.writer.WritePrivate(session.Event{
			"type":        "critic_consult_packet",
			"session_id":  packet.SessionID,
			"packet_hash": packetHash,
			"packet":      packet,
			"mode":        "consult",
		}); err != nil {
			return script.CriticConsultResult{}, err
		}
	}
	review, completion, err := runCriticConsultWithRetry(ctx, c.writer, c.profile, packet.SessionID, packet, packetHash)
	if err != nil {
		return script.CriticConsultResult{}, err
	}
	modelName := firstNonEmptyString(strings.TrimSpace(completion.Model), c.profile.Model)
	return script.CriticConsultResult{
		Status:              normalizeCriticStatus(review.Status),
		Summary:             strings.TrimSpace(review.Summary),
		Findings:            scriptCriticConsultFindings(cleanCriticFindings(review.Findings)),
		LeadInstruction:     strings.TrimSpace(review.LeadInstruction),
		HumanPrompt:         strings.TrimSpace(review.HumanPrompt),
		ProposedUserMessage: strings.TrimSpace(review.ProposedUserMessage),
		Provider:            c.profile.Provider,
		Model:               modelName,
		PacketHash:          packetHash,
		Usage:               json.RawMessage(completion.Usage),
		UsageSummary:        completion.UsageSummary,
	}, nil
}

func buildCriticConsultPacket(request script.CriticConsultRequest) criticConsultPacket {
	userRequest, _ := truncateCriticText(request.UserRequest, 4000)
	return criticConsultPacket{
		SessionID:   strings.TrimSpace(request.SessionID),
		UserRequest: userRequest,
		Kind:        normalizeCriticConsultPacketKind(request.Kind),
		Question:    strings.TrimSpace(request.Question),
		Context:     strings.TrimSpace(request.Context),
		Candidate:   strings.TrimSpace(request.Candidate),
		Checks:      cleanCriticStringList(request.Checks),
		Files:       cleanCriticConsultFiles(request.Files),
		EvidenceNotes: []string{
			"The lead model requested this consultation mid-turn.",
			"Review only this packet. File excerpts are bounded snapshots provided by the harness.",
			"Treat the result as advisory guidance for the lead, not as an automatic task outcome.",
		},
	}
}

func normalizeCriticConsultPacketKind(kind string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(kind), "-", "_")) {
	case "plan", "code", "patch", "debug", "final_claims", "other":
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(kind), "-", "_"))
	default:
		return "other"
	}
}

func cleanCriticConsultFiles(files []script.CriticConsultFile) []script.CriticConsultFile {
	out := make([]script.CriticConsultFile, 0, len(files))
	for _, file := range files {
		file.Path = strings.TrimSpace(file.Path)
		file.Role = strings.TrimSpace(file.Role)
		file.Excerpt = strings.TrimSpace(file.Excerpt)
		if file.Path == "" && file.Excerpt == "" {
			continue
		}
		out = append(out, file)
	}
	return out
}

func scriptCriticConsultFindings(findings []criticReviewFinding) []script.CriticConsultFinding {
	out := make([]script.CriticConsultFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, script.CriticConsultFinding{
			Severity:          strings.TrimSpace(finding.Severity),
			Materiality:       strings.TrimSpace(finding.Materiality),
			Basis:             strings.TrimSpace(finding.Basis),
			Claim:             strings.TrimSpace(finding.Claim),
			EvidenceSource:    strings.TrimSpace(finding.EvidenceSource),
			Evidence:          strings.TrimSpace(finding.Evidence),
			UserImpact:        strings.TrimSpace(finding.UserImpact),
			SuggestedFollowup: strings.TrimSpace(finding.SuggestedFollowup),
		})
	}
	return out
}

type criticReviewAttempt struct {
	Attempt     int
	Review      criticReviewPayload
	Completion  modeladapter.Completion
	InvalidJSON bool
	Err         error
}

func runCriticReviewWithRetry(ctx context.Context, writer *session.Writer, profile criticProfile, sessionID string, packet criticReviewPacket, packetHash string, mode string) (criticReviewPayload, modeladapter.Completion, error) {
	var previousInvalid string
	var lastCompletion modeladapter.Completion
	for attempt := 1; attempt <= 2; attempt++ {
		result := profile.Reviewer.ReviewAttempt(ctx, packet, attempt, previousInvalid)
		lastCompletion = result.Completion
		if result.Err == nil {
			return result.Review, result.Completion, nil
		}
		if !result.InvalidJSON {
			return criticReviewPayload{}, result.Completion, result.Err
		}
		if err := writeCriticInvalidModelResponse(writer, profile, sessionID, packetHash, mode, result); err != nil {
			return criticReviewPayload{}, result.Completion, err
		}
		previousInvalid = result.Completion.Message.Content
		if attempt < 2 {
			if writer != nil {
				if err := writer.Write(session.Event{
					"type":        "critic_review_retry",
					"session_id":  sessionID,
					"provider":    profile.Provider,
					"model":       firstNonEmptyString(strings.TrimSpace(result.Completion.Model), profile.Model),
					"packet_hash": packetHash,
					"mode":        mode,
					"attempt":     attempt + 1,
					"reason":      "invalid_json",
					"message":     "critic returned invalid structured output; retrying once with stricter JSON-only instructions",
				}); err != nil {
					return criticReviewPayload{}, result.Completion, err
				}
			}
			continue
		}
		return criticReviewPayload{}, result.Completion, fmt.Errorf("critic returned invalid JSON after retry")
	}
	return criticReviewPayload{}, lastCompletion, fmt.Errorf("critic review failed")
}

func runCriticConsultWithRetry(ctx context.Context, writer *session.Writer, profile criticProfile, sessionID string, packet criticConsultPacket, packetHash string) (criticReviewPayload, modeladapter.Completion, error) {
	const mode = "consult"
	var previousInvalid string
	var lastCompletion modeladapter.Completion
	for attempt := 1; attempt <= 2; attempt++ {
		result := profile.Reviewer.ConsultAttempt(ctx, packet, attempt, previousInvalid)
		lastCompletion = result.Completion
		if result.Err == nil {
			return result.Review, result.Completion, nil
		}
		if !result.InvalidJSON {
			return criticReviewPayload{}, result.Completion, result.Err
		}
		if err := writeCriticInvalidModelResponse(writer, profile, sessionID, packetHash, mode, result); err != nil {
			return criticReviewPayload{}, result.Completion, err
		}
		previousInvalid = result.Completion.Message.Content
		if attempt < 2 {
			if writer != nil {
				if err := writer.Write(session.Event{
					"type":        "critic_review_retry",
					"session_id":  sessionID,
					"provider":    profile.Provider,
					"model":       firstNonEmptyString(strings.TrimSpace(result.Completion.Model), profile.Model),
					"packet_hash": packetHash,
					"mode":        mode,
					"attempt":     attempt + 1,
					"reason":      "invalid_json",
					"message":     "critic consultation returned invalid structured output; retrying once with stricter JSON-only instructions",
				}); err != nil {
					return criticReviewPayload{}, result.Completion, err
				}
			}
			continue
		}
		return criticReviewPayload{}, result.Completion, fmt.Errorf("critic returned invalid JSON after retry")
	}
	return criticReviewPayload{}, lastCompletion, fmt.Errorf("critic consultation failed")
}

func (r traceCritic) ReviewAttempt(ctx context.Context, packet criticReviewPacket, attempt int, previousInvalid string) criticReviewAttempt {
	result := criticReviewAttempt{Attempt: attempt}
	if r.client == nil {
		result.Err = fmt.Errorf("critic client is not configured")
		return result
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		result.Err = err
		return result
	}
	messages := criticReviewMessages(string(body), previousInvalid)
	options := modeladapter.CompletionOptions{
		ReasoningEffort: strings.TrimSpace(r.reasoningEffort),
	}
	if !strings.EqualFold(r.provider, "openai") && strings.TrimSpace(options.ReasoningEffort) == "" {
		options.DisableThinking = true
	}
	completion, err := r.client.CompleteWithOptions(ctx, messages, nil, options)
	result.Completion = completion
	if err != nil {
		result.Err = err
		return result
	}
	review, err := parseCriticReviewPayload(completion.Message.Content)
	if err != nil {
		result.InvalidJSON = true
		result.Err = err
		return result
	}
	result.Review = normalizeCriticReviewForRouting(review)
	return result
}

func (r traceCritic) ConsultAttempt(ctx context.Context, packet criticConsultPacket, attempt int, previousInvalid string) criticReviewAttempt {
	result := criticReviewAttempt{Attempt: attempt}
	if r.client == nil {
		result.Err = fmt.Errorf("critic client is not configured")
		return result
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		result.Err = err
		return result
	}
	messages := criticConsultMessages(string(body), previousInvalid)
	options := modeladapter.CompletionOptions{
		ReasoningEffort: strings.TrimSpace(r.reasoningEffort),
	}
	if !strings.EqualFold(r.provider, "openai") && strings.TrimSpace(options.ReasoningEffort) == "" {
		options.DisableThinking = true
	}
	completion, err := r.client.CompleteWithOptions(ctx, messages, nil, options)
	result.Completion = completion
	if err != nil {
		result.Err = err
		return result
	}
	review, err := parseCriticReviewPayload(completion.Message.Content)
	if err != nil {
		result.InvalidJSON = true
		result.Err = err
		return result
	}
	result.Review = normalizeCriticReviewForRouting(review)
	return result
}

func criticReviewMessages(packetBody string, previousInvalid string) []modeladapter.Message {
	messages := []modeladapter.Message{
		{Role: "system", Content: criticSystemPrompt()},
		{Role: "user", Content: packetBody},
	}
	if strings.TrimSpace(previousInvalid) == "" {
		return messages
	}
	previous, _ := truncateCriticText(previousInvalid, 4000)
	messages = append(messages,
		modeladapter.Message{Role: "assistant", Content: previous},
		modeladapter.Message{Role: "user", Content: criticJSONRepairPrompt()},
	)
	return messages
}

func criticConsultMessages(packetBody string, previousInvalid string) []modeladapter.Message {
	messages := []modeladapter.Message{
		{Role: "system", Content: criticConsultSystemPrompt()},
		{Role: "user", Content: packetBody},
	}
	if strings.TrimSpace(previousInvalid) == "" {
		return messages
	}
	previous, _ := truncateCriticText(previousInvalid, 4000)
	messages = append(messages,
		modeladapter.Message{Role: "assistant", Content: previous},
		modeladapter.Message{Role: "user", Content: criticJSONRepairPrompt()},
	)
	return messages
}

func criticJSONRepairPrompt() string {
	return strings.Join([]string{
		"Your previous critic response was not valid JSON for the required schema.",
		"Return only one JSON object. Do not include markdown, prose, code fences, comments, or multiple JSON values.",
		"Use exactly this top-level shape:",
		`{"status":"clean|concerns|needs_followup","confidence":0.0,"summary":"short review summary","findings":[],"lead_instruction":"","human_prompt":"","proposed_user_message":""}`,
		"If there are findings, each item must include severity, materiality, basis, claim, evidence_source, evidence, user_impact, and suggested_followup.",
	}, "\n")
}

func writeCriticInvalidModelResponse(writer *session.Writer, profile criticProfile, sessionID, packetHash, mode string, result criticReviewAttempt) error {
	if writer == nil {
		return nil
	}
	content := result.Completion.Message.Content
	contentHash := criticContentHash(content)
	modelName := firstNonEmptyString(strings.TrimSpace(result.Completion.Model), profile.Model)
	message := "critic returned invalid structured output"
	if result.Err != nil {
		message = result.Err.Error()
	}
	public := session.Event{
		"type":           "critic_model_response_invalid",
		"session_id":     sessionID,
		"provider":       profile.Provider,
		"model":          modelName,
		"packet_hash":    packetHash,
		"attempt":        result.Attempt,
		"mode":           mode,
		"message":        message,
		"content_sha256": contentHash,
		"usage":          json.RawMessage(result.Completion.Usage),
		"usage_summary":  result.Completion.UsageSummary,
	}
	if result.Attempt < 2 {
		public["retrying"] = true
	}
	if err := writer.Write(public); err != nil {
		return err
	}
	return writer.WritePrivate(session.Event{
		"type":           "critic_model_response_invalid_raw",
		"session_id":     sessionID,
		"provider":       profile.Provider,
		"model":          modelName,
		"packet_hash":    packetHash,
		"attempt":        result.Attempt,
		"mode":           mode,
		"message":        message,
		"content_sha256": contentHash,
		"content":        content,
	})
}

func criticSystemPrompt() string {
	return strings.Join([]string{
		"You are a packet-bound critic for an AI coding agent turn.",
		"Review only the provided packet. You have no live tools and must not claim to inspect current files.",
		"Find concrete evidence that the lead turn may be wrong, incomplete, overclaimed, unsafe, insufficiently verified, or on a poor trajectory for the original user request.",
		"Be direct. If packet evidence shows something is wrong, missing, visibly broken, or not actually verified, say so plainly instead of softening it.",
		"When change_review is present, prioritize the produced changes: current file snapshots, patch summaries, quality plan, and verification evidence. Use the recent messages only to interpret the lead's final claim.",
		"Optimize for material user outcome, not transcript perfection. A true but low-impact wording nit should be a note, not a follow-up.",
		"Use evidence_source values only from: change_packet, file_snapshot, quality_plan, tool_trace, patch, verification, lead_final, missing_evidence.",
		"Each finding must include basis: confirmed, suspected, visual_or_product_gap, or missing_evidence. Use confirmed only when the packet directly proves the claim. Use suspected for plausible code diagnoses that are not mechanically proven. Use visual_or_product_gap for screenshot/product mismatches. Use missing_evidence when the packet lacks evidence for a lead claim.",
		"If a concern cannot cite packet evidence, do not include it as a finding.",
		"For code-level claims, quote the exact relevant lines or snapshot text. If exact packet evidence is ambiguous, mark the finding suspected and phrase it as a hypothesis.",
		"Use severity for factual risk and materiality for impact on the original user request.",
		"Set status=needs_followup only when the task should be reopened because the issue materially blocks or weakens the requested outcome.",
		"Use status=concerns for trace notes that are true but do not justify another lead turn.",
		"Prefer lead_instruction for routine corrections the lead can make without human judgment.",
		"Use human_prompt/proposed_user_message only when human input or an explicit task-reopening follow-up is genuinely warranted.",
		"Leave human_prompt and proposed_user_message blank for low-materiality findings, even when status=concerns.",
		"Message content can be truncated. Treat evidence_excerpts on a message as raw trace evidence from omitted portions of that same message.",
		"Do not claim that a string, file, or page content was absent from a truncated message solely because it is absent from the visible content; at most report missing_evidence when no excerpt or other packet evidence supports the lead claim.",
		"Return only JSON. Do not use markdown.",
		"The JSON shape is:",
		`{"status":"clean|concerns|needs_followup","confidence":0.0,"summary":"short review summary","findings":[{"severity":"low|medium|high","materiality":"low|medium|high","basis":"confirmed|suspected|visual_or_product_gap|missing_evidence","claim":"concrete issue","evidence_source":"change_packet|file_snapshot|quality_plan|tool_trace|patch|verification|lead_final|missing_evidence","evidence":"specific packet evidence","user_impact":"why this matters to the original user request","suggested_followup":"what should happen next, if anything"}],"lead_instruction":"private instruction for the lead when useful, blank otherwise","human_prompt":"draft only when human input is truly needed, blank otherwise","proposed_user_message":"same as human_prompt for backward compatibility, blank unless status=needs_followup"}`,
	}, "\n")
}

func criticConsultSystemPrompt() string {
	return strings.Join([]string{
		"You are an advisory critic for an AI coding agent that explicitly asked for a second opinion mid-turn.",
		"Review only the provided consultation packet. You have no live tools and must not claim to inspect current files beyond included file excerpts.",
		"Answer the lead's question directly. Focus on material correctness, missing evidence, risky assumptions, poor plan shape, insufficient verification, or user-impacting omissions.",
		"Be direct. If the candidate is wrong, missing a required piece, visually broken, or unverified from the packet, say so plainly.",
		"Do not rewrite the whole solution unless that is the simplest way to explain the concern.",
		"Use evidence_source values from: consult_question, provided_context, candidate, file_excerpt, missing_evidence.",
		"Each finding must include basis: confirmed, suspected, visual_or_product_gap, or missing_evidence. Use confirmed only when the packet directly proves the claim; use suspected for plausible but unproven code or debugging hypotheses.",
		"Set status=clean when the candidate or plan looks sound from the packet.",
		"Set status=concerns when there are risks or improvements the lead should consider.",
		"Set status=needs_followup only when the lead should gather more evidence, change course, or ask the user before proceeding.",
		"Use lead_instruction for the most useful private next step for the lead. Leave human_prompt and proposed_user_message blank unless user input is truly needed.",
		"Return only JSON. Do not use markdown.",
		"The JSON shape is:",
		`{"status":"clean|concerns|needs_followup","confidence":0.0,"summary":"short advisory answer","findings":[{"severity":"low|medium|high","materiality":"low|medium|high","basis":"confirmed|suspected|visual_or_product_gap|missing_evidence","claim":"concrete issue or reassurance","evidence_source":"consult_question|provided_context|candidate|file_excerpt|missing_evidence","evidence":"specific packet evidence","user_impact":"why this matters to the original user request","suggested_followup":"what the lead should do next, if anything"}],"lead_instruction":"private instruction for the lead when useful, blank otherwise","human_prompt":"draft only when human input is truly needed, blank otherwise","proposed_user_message":"same as human_prompt for backward compatibility, blank unless status=needs_followup"}`,
	}, "\n")
}

func buildCriticReviewPacketForRunner(runner script.Runner, final script.Action, messages []modeladapter.Message, compacted bool) criticReviewPacket {
	if runner.FileTouchEvents() == 0 {
		return buildCriticReviewPacket(runner.SessionID, runner.Prompt, final, messages, compacted)
	}
	packet := buildCriticReviewPacketWithMaxMessages(runner.SessionID, runner.Prompt, final, messages, compacted, 12)
	changeReview := runner.ChangeReviewEvidence()
	packet.ChangeReview = &changeReview
	packet.Metadata = map[string]interface{}{
		"review_focus":       "produced_change",
		"change_review_hash": criticPacketHash(changeReview),
	}
	packet.EvidenceNotes = append([]string{
		"The critic is reviewing a compact produced-change packet captured after the lead model requested final_response.",
		"Prioritize change_review.files snapshots, patch_summaries, quality_plan, and verification over transcript reconstruction.",
		"Current file snapshots are bounded; a missing detail in a truncated snapshot is missing_evidence unless another packet field proves it.",
	}, packet.EvidenceNotes...)
	return packet
}

func buildCriticReviewPacket(sessionID, userRequest string, final script.Action, messages []modeladapter.Message, compacted bool) criticReviewPacket {
	return buildCriticReviewPacketWithMaxMessages(sessionID, userRequest, final, messages, compacted, 80)
}

func buildCriticReviewPacketWithMaxMessages(sessionID, userRequest string, final script.Action, messages []modeladapter.Message, compacted bool, maxMessages int) criticReviewPacket {
	if maxMessages <= 0 {
		maxMessages = 80
	}
	start := 0
	if len(messages) > maxMessages {
		start = len(messages) - maxMessages
	}
	evidenceNeedles := criticEvidenceNeedles(userRequest, final)
	packetMessages := make([]criticPacketMessage, 0, len(messages)-start)
	for i := start; i < len(messages); i++ {
		packetMessages = append(packetMessages, criticPacketMessageFromModelMessage(i, messages[i], evidenceNeedles))
	}
	notes := []string{
		"The critic is reviewing a packet captured after the lead model finished the turn.",
		"Claims about pre-edit state must cite tool_trace evidence; claims about final state must cite final messages, patch evidence, or verification evidence present in the packet.",
		"Truncated message content is partial. Evidence excerpts, when present, are copied from raw omitted portions of the same tool result.",
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

func criticPacketMessageFromModelMessage(index int, msg modeladapter.Message, evidenceNeedles []string) criticPacketMessage {
	limit := 2200
	if msg.Role == "tool" {
		limit = 3200
	}
	trimmedContent := strings.TrimSpace(msg.Content)
	content, truncated := truncateCriticText(msg.Content, limit)
	out := criticPacketMessage{
		Index:        index,
		Role:         strings.TrimSpace(msg.Role),
		Content:      content,
		ToolCallID:   strings.TrimSpace(msg.ToolCallID),
		Truncated:    truncated,
		ContentBytes: len(trimmedContent),
	}
	if !truncated {
		out.ContentBytes = 0
	}
	if truncated && out.Role == "tool" {
		out.EvidenceExcerpts = criticToolEvidenceExcerpts(trimmedContent, evidenceNeedles, 6)
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

func criticEvidenceNeedles(userRequest string, final script.Action) []string {
	seen := map[string]struct{}{}
	var needles []string
	add := func(value string) {
		value = cleanCriticEvidenceNeedle(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		needles = append(needles, value)
	}
	for _, text := range []string{userRequest, final.Summary} {
		addCriticDelimitedEvidenceNeedles(text, '`', add)
		addCriticDelimitedEvidenceNeedles(text, '"', add)
	}
	for _, value := range final.FilesChanged {
		add(value)
	}
	for _, value := range final.Verification {
		add(value)
	}
	return needles
}

func addCriticDelimitedEvidenceNeedles(text string, delimiter byte, add func(string)) {
	for i := 0; i < len(text); i++ {
		if text[i] != delimiter || criticByteEscaped(text, i) {
			continue
		}
		start := i + 1
		for j := start; j < len(text); j++ {
			if text[j] == delimiter && !criticByteEscaped(text, j) {
				add(text[start:j])
				i = j
				break
			}
		}
	}
}

func criticByteEscaped(text string, index int) bool {
	backslashes := 0
	for i := index - 1; i >= 0 && text[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func cleanCriticEvidenceNeedle(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " \t\r\n.,;:()[]{}")
	if len(value) < 3 || len(value) > 160 {
		return ""
	}
	if strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func criticToolEvidenceExcerpts(content string, needles []string, maxExcerpts int) []criticPacketEvidenceExcerpt {
	if maxExcerpts <= 0 || len(needles) == 0 {
		return nil
	}
	sourceName, sourceText := criticToolEvidenceSource(content)
	sourceText = strings.TrimSpace(sourceText)
	if sourceText == "" {
		return nil
	}
	excerpts := make([]criticPacketEvidenceExcerpt, 0, criticMinInt(maxExcerpts, len(needles)))
	var ranges []criticByteRange
	for _, needle := range needles {
		match, index := criticFindEvidenceNeedle(sourceText, needle)
		if index < 0 {
			continue
		}
		start, end := criticExcerptRange(sourceText, index, len(match), 900)
		if criticRangeCovered(ranges, start, end) {
			continue
		}
		ranges = append(ranges, criticByteRange{Start: start, End: end})
		excerpts = append(excerpts, criticPacketEvidenceExcerpt{
			Source:    sourceName,
			Match:     needle,
			ByteStart: start,
			ByteEnd:   end,
			Text:      strings.TrimSpace(sourceText[start:end]),
		})
		if len(excerpts) >= maxExcerpts {
			break
		}
	}
	return excerpts
}

type criticByteRange struct {
	Start int
	End   int
}

func criticToolEvidenceSource(content string) (string, string) {
	var result tools.ToolResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		switch {
		case strings.TrimSpace(result.Output) != "":
			return "tool_result.output", result.Output
		case strings.TrimSpace(result.Error) != "":
			return "tool_result.error", result.Error
		case strings.TrimSpace(result.DiffSummary) != "":
			return "tool_result.diff_summary", result.DiffSummary
		}
	}
	return "message.content", content
}

func criticFindEvidenceNeedle(text, needle string) (string, int) {
	for _, candidate := range criticEvidenceNeedleVariants(needle) {
		if index := strings.Index(text, candidate); index >= 0 {
			return candidate, index
		}
	}
	return "", -1
}

func criticEvidenceNeedleVariants(needle string) []string {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return nil
	}
	var variants []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range variants {
			if existing == value {
				return
			}
		}
		variants = append(variants, value)
	}
	add(needle)
	if strings.Contains(needle, `"`) {
		add(strings.ReplaceAll(needle, `"`, `\"`))
	}
	if !strings.HasPrefix(needle, `"`) && !strings.HasSuffix(needle, `"`) {
		quoted := `"` + needle + `"`
		add(quoted)
		add(strings.ReplaceAll(quoted, `"`, `\"`))
	}
	return variants
}

func criticExcerptRange(text string, index, matchLen, targetLen int) (int, int) {
	if targetLen <= 0 || len(text) <= targetLen {
		return 0, len(text)
	}
	start := index - (targetLen-matchLen)/2
	if start < 0 {
		start = 0
	}
	end := start + targetLen
	if end > len(text) {
		end = len(text)
		start = criticMaxInt(0, end-targetLen)
	}
	if lineStart := strings.LastIndex(text[start:index], "\n"); lineStart >= 0 && index-(start+lineStart+1) <= 160 {
		start = start + lineStart + 1
	}
	matchEnd := index + matchLen
	if matchEnd < end {
		if lineEnd := strings.Index(text[matchEnd:end], "\n"); lineEnd >= 0 && lineEnd <= 160 {
			end = matchEnd + lineEnd
		}
	}
	if start > end {
		return 0, criticMinInt(len(text), targetLen)
	}
	return start, end
}

func criticRangeCovered(ranges []criticByteRange, start, end int) bool {
	for _, existing := range ranges {
		if start >= existing.Start && end <= existing.End {
			return true
		}
		overlapStart := criticMaxInt(start, existing.Start)
		overlapEnd := criticMinInt(end, existing.End)
		if overlapEnd <= overlapStart {
			continue
		}
		if overlapEnd-overlapStart >= (end-start)/2 {
			return true
		}
	}
	return false
}

func criticMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func criticMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
		finding.Materiality = normalizeCriticMateriality(finding.Materiality, finding.Severity)
		finding.Basis = normalizeCriticFindingBasis(finding.Basis)
		finding.EvidenceSource = normalizeCriticEvidenceSource(finding.EvidenceSource)
		finding.Claim = strings.TrimSpace(finding.Claim)
		finding.Evidence = strings.TrimSpace(finding.Evidence)
		finding.UserImpact = strings.TrimSpace(finding.UserImpact)
		finding.SuggestedFollowup = strings.TrimSpace(finding.SuggestedFollowup)
		if finding.Claim == "" && finding.Evidence == "" {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func normalizeCriticReviewForRouting(review criticReviewPayload) criticReviewPayload {
	review.Status = normalizeCriticStatus(review.Status)
	review.Summary = strings.TrimSpace(review.Summary)
	review.Findings = cleanCriticFindings(review.Findings)
	review.LeadInstruction = strings.TrimSpace(review.LeadInstruction)
	review.HumanPrompt = strings.TrimSpace(review.HumanPrompt)
	review.ProposedUserMessage = strings.TrimSpace(review.ProposedUserMessage)
	if review.HumanPrompt == "" {
		review.HumanPrompt = review.ProposedUserMessage
	}
	if !criticReviewShouldDraftHumanFollowup(review) {
		review.HumanPrompt = ""
		review.ProposedUserMessage = ""
		return review
	}
	draft := firstNonEmptyString(review.HumanPrompt, review.ProposedUserMessage)
	review.HumanPrompt = draft
	review.ProposedUserMessage = draft
	return review
}

func criticReviewShouldDraftHumanFollowup(review criticReviewPayload) bool {
	if review.Status != "needs_followup" {
		return false
	}
	if strings.TrimSpace(firstNonEmptyString(review.HumanPrompt, review.ProposedUserMessage)) == "" {
		return false
	}
	if len(review.Findings) == 0 {
		return false
	}
	return criticReviewHasMaterialFinding(review)
}

func criticReviewShouldBounceLead(review criticReviewPayload) bool {
	if !criticReviewHasMaterialFinding(review) {
		return false
	}
	switch review.Status {
	case "needs_followup":
		return true
	case "concerns":
		return strings.TrimSpace(review.LeadInstruction) != ""
	default:
		return false
	}
}

func criticReviewHasMaterialFinding(review criticReviewPayload) bool {
	for _, finding := range review.Findings {
		switch finding.Materiality {
		case "high", "medium":
			return true
		}
	}
	return false
}

func criticLeadFeedbackMessage(review criticReviewPayload) string {
	if !criticReviewShouldBounceLead(review) {
		return ""
	}
	instruction := strings.TrimSpace(review.LeadInstruction)
	if instruction == "" {
		for _, finding := range review.Findings {
			if finding.Materiality != "high" && finding.Materiality != "medium" {
				continue
			}
			instruction = strings.TrimSpace(finding.SuggestedFollowup)
			if instruction == "" {
				instruction = strings.TrimSpace(finding.Claim)
			}
			if instruction != "" {
				break
			}
		}
	}
	if instruction == "" {
		instruction = strings.TrimSpace(review.Summary)
	}
	if instruction == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("Critic feedback before final_response: ")
	b.WriteString(criticTrimFeedbackField(instruction, 700))
	materialFindings := criticMaterialFindings(review.Findings, 4)
	if len(materialFindings) > 0 {
		b.WriteString("\n\nTop material findings:")
	}
	for i, finding := range materialFindings {
		fmt.Fprintf(&b, "\n%d. [%s/%s", i+1, firstNonEmptyString(finding.Severity, "medium"), firstNonEmptyString(finding.Materiality, "medium"))
		if basis := strings.TrimSpace(finding.Basis); basis != "" {
			b.WriteString(", ")
			b.WriteString(basis)
		}
		b.WriteString("]")
		if claim := criticTrimFeedbackField(finding.Claim, 360); claim != "" {
			b.WriteString(" Issue: ")
			b.WriteString(claim)
		}
		if evidence := criticTrimFeedbackField(finding.Evidence, 520); evidence != "" {
			b.WriteString("\n   Evidence: ")
			b.WriteString(evidence)
		}
		if impact := criticTrimFeedbackField(finding.UserImpact, 360); impact != "" {
			b.WriteString("\n   Why it matters: ")
			b.WriteString(impact)
		}
		if followup := criticTrimFeedbackField(finding.SuggestedFollowup, 420); followup != "" {
			b.WriteString("\n   Suggested fix: ")
			b.WriteString(followup)
		}
	}
	if len(materialFindings) == 0 {
		for _, finding := range review.Findings {
			if finding.Materiality != "high" && finding.Materiality != "medium" {
				continue
			}
			if claim := criticTrimFeedbackField(finding.Claim, 320); claim != "" {
				b.WriteString("\n\nMaterial finding: ")
				b.WriteString(claim)
			}
			if evidence := criticTrimFeedbackField(finding.Evidence, 500); evidence != "" {
				b.WriteString("\nEvidence: ")
				b.WriteString(evidence)
			}
			if impact := criticTrimFeedbackField(finding.UserImpact, 320); impact != "" {
				b.WriteString("\nUser impact: ")
				b.WriteString(impact)
			}
			break
		}
	}
	b.WriteString("\n\nTreat this as private review guidance. Reopen the work only for material issues affecting the user's request. Treat suspected code findings as hypotheses to inspect, not as proven facts. If the critic is wrong, proceed and explain briefly in final_response.")
	return b.String()
}

func criticMaterialFindings(findings []criticReviewFinding, limit int) []criticReviewFinding {
	if limit <= 0 {
		return nil
	}
	out := make([]criticReviewFinding, 0, limit)
	for _, finding := range findings {
		if finding.Materiality != "high" && finding.Materiality != "medium" {
			continue
		}
		if strings.TrimSpace(finding.Claim) == "" && strings.TrimSpace(finding.Evidence) == "" {
			continue
		}
		out = append(out, finding)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func criticTrimFeedbackField(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 16 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-13]) + "...[truncated]"
}

func normalizeCriticSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(severity))
	default:
		return "medium"
	}
}

func normalizeCriticMateriality(materiality, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(materiality)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(materiality))
	}
	return normalizeCriticSeverity(fallback)
}

func normalizeCriticFindingBasis(basis string) string {
	value := strings.ToLower(strings.TrimSpace(basis))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "confirmed", "suspected", "visual_or_product_gap", "missing_evidence":
		return value
	case "visual", "product", "product_gap", "visual_gap", "visual_product_gap":
		return "visual_or_product_gap"
	case "missing", "unverified", "no_evidence":
		return "missing_evidence"
	default:
		return "unspecified"
	}
}

func normalizeCriticEvidenceSource(source string) string {
	value := strings.ToLower(strings.TrimSpace(source))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "change_packet", "file_snapshot", "quality_plan", "tool_trace", "patch", "verification", "lead_final", "consult_question", "provided_context", "candidate", "file_excerpt", "missing_evidence":
		return value
	case "image_analysis", "vision", "visual_analysis":
		return "verification"
	case "final_summary", "final_response", "lead_summary":
		return "lead_final"
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

func criticPacketHash(packet any) string {
	body, err := json.Marshal(packet)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func criticContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
