package boss

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/agentcontext"
	"lcroom/internal/llm"
	"lcroom/internal/model"
)

const (
	bossContextNamespace            = "boss"
	bossPromptRecentTailLimit       = 12
	bossContextCompactionBatchSize  = 4
	bossContextCompactionMaxSummary = 2600
)

type BossPromptContext struct {
	Prepared            bool
	ContextMode         string
	Summary             string
	SummaryMessageCount int
	RecentMessages      []ChatMessage
	TotalMessages       int
	VisibleMessages     int
	ApproxChars         int
}

type bossContextNoToolCall struct{}

type bossContextCompactionResult struct {
	Summary string `json:"summary"`
}

func (a *Assistant) preparePromptContext(ctx context.Context, req AssistantRequest) (AssistantRequest, model.LLMUsage, string, error) {
	if req.PromptContext.Prepared {
		return req, model.LLMUsage{}, "", nil
	}
	chatMessages := conversationalChatMessages(req.Messages)
	prepared := bossPromptContextFromMessages(chatMessages, "", 0)
	if len(chatMessages) <= bossPromptChatHistoryLimit {
		req.PromptContext = prepared
		_ = a.saveBossPromptContext(prepared, req.SessionID)
		return req, model.LLMUsage{}, "", nil
	}

	state, ok := a.loadBossContextState(req.SessionID)
	if ok && strings.TrimSpace(state.Summary) != "" && state.SummaryCount > 0 && state.SummaryCount <= len(chatMessages) {
		prepared = bossPromptContextFromMessages(chatMessages, state.Summary, state.SummaryCount)
	}

	targetSummaryCount := len(chatMessages) - bossPromptRecentTailLimit
	covered := prepared.SummaryMessageCount
	needsCompaction := targetSummaryCount > covered && (covered == 0 || targetSummaryCount-covered >= bossContextCompactionBatchSize)
	if needsCompaction && a.queryRouter != nil {
		summary, usage, modelName, err := a.compactBossPromptContext(ctx, prepared.Summary, covered, chatMessages[covered:targetSummaryCount])
		if err != nil {
			if ctx.Err() != nil {
				return req, usage, modelName, err
			}
		} else if strings.TrimSpace(summary) != "" {
			prepared = bossPromptContextFromMessages(chatMessages, summary, targetSummaryCount)
			req.PromptContext = prepared
			_ = a.saveBossPromptContext(prepared, req.SessionID)
			return req, usage, modelName, nil
		}
	}

	if prepared.SummaryMessageCount == 0 {
		prepared = bossPromptContextFromMessages(chatMessages, "", 0)
	}
	req.PromptContext = prepared
	_ = a.saveBossPromptContext(prepared, req.SessionID)
	return req, model.LLMUsage{}, "", nil
}

func (a *Assistant) compactBossPromptContext(ctx context.Context, existingSummary string, existingCount int, messages []ChatMessage) (string, model.LLMUsage, string, error) {
	if a == nil || a.queryRouter == nil || len(messages) == 0 {
		return strings.TrimSpace(existingSummary), model.LLMUsage{}, "", nil
	}
	response, err := a.queryRouter.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           firstNonEmpty(strings.TrimSpace(a.utilityModel), strings.TrimSpace(a.model)),
		SystemText:      bossContextCompactionSystemPrompt(),
		UserText:        bossContextCompactionUserText(existingSummary, existingCount, messages),
		SchemaName:      "boss_chat_context_compaction",
		Schema:          bossContextCompactionSchema(),
		ReasoningEffort: bossReadOnlyRouterReasoningEffort,
	})
	modelName := strings.TrimSpace(response.Model)
	if err != nil {
		return strings.TrimSpace(existingSummary), response.Usage, modelName, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return strings.TrimSpace(existingSummary), response.Usage, modelName, fmt.Errorf("boss context compaction returned an empty summary")
	}
	var result bossContextCompactionResult
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &result); err != nil {
		return strings.TrimSpace(existingSummary), response.Usage, modelName, fmt.Errorf("decode boss context compaction: %w", err)
	}
	return clipText(strings.TrimSpace(result.Summary), bossContextCompactionMaxSummary), response.Usage, modelName, nil
}

func (a *Assistant) loadBossContextState(sessionID string) (agentcontext.State[ChatMessage, bossContextNoToolCall], bool) {
	if a == nil || strings.TrimSpace(a.dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return agentcontext.State[ChatMessage, bossContextNoToolCall]{}, false
	}
	state, ok, err := agentcontext.LoadState[ChatMessage, bossContextNoToolCall](a.dataDir, bossContextNamespace, sessionID, "", nil)
	if err != nil || !ok || state == nil {
		return agentcontext.State[ChatMessage, bossContextNoToolCall]{}, false
	}
	return *state, true
}

func (a *Assistant) saveBossPromptContext(prompt BossPromptContext, sessionID string) error {
	if a == nil || strings.TrimSpace(a.dataDir) == "" || strings.TrimSpace(sessionID) == "" || !prompt.Prepared {
		return nil
	}
	createdAt := time.Now()
	if existing, ok := a.loadBossContextState(sessionID); ok && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	state := agentcontext.State[ChatMessage, bossContextNoToolCall]{
		Version:      agentcontext.StateVersion,
		ThreadID:     strings.TrimSpace(sessionID),
		CreatedAt:    createdAt,
		UpdatedAt:    time.Now(),
		Status:       agentcontext.StatusStable,
		ContextMode:  prompt.ContextMode,
		Summary:      strings.TrimSpace(prompt.Summary),
		SummaryCount: prompt.SummaryMessageCount,
		Messages:     cloneChatMessages(prompt.RecentMessages),
		MessageCount: prompt.TotalMessages,
		ApproxChars:  prompt.ApproxChars,
	}
	return agentcontext.SaveState(a.dataDir, bossContextNamespace, state, bossChatMessagesApproxChars)
}

func bossPromptContextFromMessages(messages []ChatMessage, summary string, summaryCount int) BossPromptContext {
	total := len(messages)
	if summaryCount < 0 {
		summaryCount = 0
	}
	if summaryCount > total {
		summaryCount = total
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summaryCount = 0
	}

	mode := agentcontext.ContextModeExact
	start := 0
	if summaryCount > 0 {
		mode = agentcontext.ContextModeCompacted
		start = summaryCount
		if total-start > bossPromptRecentTailLimit {
			start = total - bossPromptRecentTailLimit
		}
	} else if total > bossPromptChatHistoryLimit {
		mode = agentcontext.ContextModeClipped
		start = total - bossPromptChatHistoryLimit
	}
	if start < 0 {
		start = 0
	}
	recent := cloneChatMessages(messages[start:])
	return BossPromptContext{
		Prepared:            true,
		ContextMode:         mode,
		Summary:             summary,
		SummaryMessageCount: summaryCount,
		RecentMessages:      recent,
		TotalMessages:       total,
		VisibleMessages:     len(recent),
		ApproxChars:         bossChatMessagesApproxChars(recent) + len(summary),
	}
}

func cloneChatMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Handoff != nil {
			handoff := *out[i].Handoff
			out[i].Handoff = &handoff
		}
	}
	return out
}

func bossContextCompactionSystemPrompt() string {
	return strings.Join([]string{
		"You compact older Chat turns for Little Control Room.",
		"Return a concise durable summary for future Chat context.",
		"Preserve user decisions, preferences, constraints, project/task names, unresolved questions, and promises or follow-ups.",
		"Omit raw system/flow notices, filler, and implementation telemetry unless it changes a decision.",
		"Do not invent facts. Return only the structured summary.",
	}, "\n")
}

func bossContextCompactionUserText(existingSummary string, existingCount int, messages []ChatMessage) string {
	var b strings.Builder
	if existingSummary = strings.TrimSpace(existingSummary); existingSummary != "" {
		b.WriteString("Existing summary covering the first ")
		b.WriteString(fmt.Sprint(existingCount))
		b.WriteString(" conversational turns:\n")
		b.WriteString(existingSummary)
		b.WriteString("\n\n")
	}
	b.WriteString("New older Chat turns to merge into the summary:\n")
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		b.WriteString(normalizeChatRole(message.Role))
		b.WriteString(": ")
		b.WriteString(clipText(content, 1600))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func bossContextCompactionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"summary"},
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Concise durable Chat summary preserving decisions, constraints, open questions, project/task references, and follow-ups.",
			},
		},
	}
}
