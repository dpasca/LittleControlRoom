package lcagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
)

// contextTokenCounter anchors a conversation to the last provider input count.
// Hashes identify content already included in that count; changed or added
// content is charged conservatively, without subtracting removed content whose
// exact token cost is unknown. Compaction explicitly discards the old anchor.
// Only main-conversation requests may update this counter, never utility calls.
type contextTokenCounter struct {
	Model          string   `json:"model,omitempty"`
	InputTokens    int64    `json:"input_tokens,omitempty"`
	MessageHashes  []string `json:"message_hashes,omitempty"`
	ToolsHash      string   `json:"tools_hash,omitempty"`
	ResponseHash   string   `json:"response_hash,omitempty"`
	ResponseTokens int64    `json:"response_tokens,omitempty"`
}

type contextTokenEstimate struct {
	Tokens int64
	Source string
}

func contextHash(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Unmeasured text is budgeted at one token per UTF-8 byte, including serialized
// protocol framing. Unlike chars/4 this does not assume English prose. Images
// retain an explicit conservative allowance until provider usage includes them;
// their exact tokenization is model-specific, so this remains an estimate.
func unmeasuredMessageTokens(message modeladapter.Message) int64 {
	images := len(message.Images)
	message.Images = nil
	message.Origin = ""
	body, _ := json.Marshal(message)
	return int64(len(body) + 16 + images*16384)
}

func unmeasuredToolsTokens(defs []modeladapter.ToolDefinition) int64 {
	if len(defs) == 0 {
		return 0
	}
	body, _ := json.Marshal(defs)
	return int64(len(body) + 16)
}

func (c *contextTokenCounter) Estimate(model string, messages []modeladapter.Message, defs []modeladapter.ToolDefinition) contextTokenEstimate {
	estimate := contextTokenEstimate{Tokens: 32, Source: "estimate"}
	known := map[string]int{}
	anchored := c != nil && c.Model == model && c.InputTokens > 0
	if anchored {
		estimate = contextTokenEstimate{Tokens: c.InputTokens, Source: "provider"}
		for _, hash := range c.MessageHashes {
			known[hash]++
		}
	}
	responseUsed := false
	for _, message := range messages {
		hash := contextHash(message)
		if known[hash] > 0 {
			known[hash]--
			continue
		}
		if anchored && !responseUsed && c.ResponseTokens > 0 && hash == c.ResponseHash {
			estimate.Tokens += c.ResponseTokens
			responseUsed = true
			continue
		}
		estimate.Tokens += unmeasuredMessageTokens(message)
		estimate.Source = "estimate"
	}
	if !anchored || c.ToolsHash != contextHash(defs) {
		estimate.Tokens += unmeasuredToolsTokens(defs)
		estimate.Source = "estimate"
	}
	for _, remaining := range known {
		if remaining > 0 {
			estimate.Source = "estimate"
		}
	}
	if anchored && estimate.Source == "estimate" {
		estimate.Source = "provider+estimate"
	}
	return estimate
}

func (c *contextTokenCounter) Observe(model string, messages []modeladapter.Message, defs []modeladapter.ToolDefinition, completion modeladapter.Completion) {
	usage := completion.UsageSummary
	if usage.InputTokens <= 0 {
		usage = modeladapter.UsageFromRaw(completion.Usage, model)
	}
	if usage.InputTokens <= 0 {
		return // Missing usage must not erase an earlier valid measurement.
	}
	*c = contextTokenCounter{
		Model: model, InputTokens: usage.InputTokens, ToolsHash: contextHash(defs),
		ResponseHash: contextHash(completion.Message), ResponseTokens: usage.OutputTokens,
	}
	for _, message := range messages {
		c.MessageHashes = append(c.MessageHashes, contextHash(message))
	}
}

func contextInputBudget(opts openRouterContextOptions) (budget, reserve int64) {
	budget = opts.LoopCompactionTokenBudget
	if budget <= 0 {
		budget = int64(opts.LoopCompactionCharThreshold / contextPackingApproxCharsPerToken)
	}
	// The 85%/70% policy already reserves this headroom for output and
	// estimation error. Keep it visible without imposing a new output cap.
	if opts.ModelContextWindowTokens > budget {
		reserve = opts.ModelContextWindowTokens - budget
	}
	return
}

func writeContextTokenUsage(writer *session.Writer, sessionID, model string, estimate contextTokenEstimate, opts openRouterContextOptions) error {
	if writer == nil {
		return nil
	}
	budget, reserve := contextInputBudget(opts)
	return writer.Write(session.Event{
		"type": "context_usage", "session_id": sessionID, "model": model,
		"context_tokens": estimate.Tokens, "context_source": estimate.Source,
		"compaction_token_budget": budget, "reserved_tokens": reserve,
	})
}

// prepareContextRequest checks the complete request, including transient notes
// and tool schemas. Packing is retried once and must actually fit afterwards.
func prepareContextRequest(counter *contextTokenCounter, model string, messages, tail []modeladapter.Message, defs []modeladapter.ToolDefinition, ledger *readLedger, opts openRouterContextOptions) ([]modeladapter.Message, finalHandoffCompactionStats, bool, error) {
	request := append(append([]modeladapter.Message(nil), messages...), tail...)
	budget, _ := contextInputBudget(opts)
	if counter.Estimate(model, request, defs).Tokens < budget {
		return request, finalHandoffCompactionStats{}, false, nil
	}
	packing := opts
	// An unmeasured packed transcript uses the conservative byte budget too.
	packing.LoopCompactionTranscriptChars = min(packing.LoopCompactionTranscriptChars, int(budget*contextPackingTranscriptTargetPercent/100))
	packed, stats, compacted := packOpenRouterLoopMessagesWithOptions(messages, ledger, packing)
	if !compacted {
		return nil, stats, false, fmt.Errorf("context exceeds %d-token working budget and cannot be reduced while preserving the active request", budget)
	}
	*counter = contextTokenCounter{}
	packed = append(packed, tail...)
	if estimate := counter.Estimate(model, packed, defs); estimate.Tokens >= budget {
		return nil, stats, false, fmt.Errorf("compacted context still exceeds working budget: %d estimated tokens, limit %d; reduce the request or tool payload", estimate.Tokens, budget)
	}
	return packed, stats, true, nil
}
