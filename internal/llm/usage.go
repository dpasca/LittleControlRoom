package llm

import (
	"strings"
	"sync"
	"time"

	"lcroom/internal/model"
)

type UsageTracker struct {
	mu       sync.Mutex
	snapshot model.LLMSessionUsage
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{}
}

func (u *UsageTracker) Start(modelName string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	u.snapshot.Running++
	u.snapshot.Started++
	u.snapshot.LastStartedAt = time.Now()
}

func (u *UsageTracker) Complete(modelName string, usage model.LLMUsage) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	if u.snapshot.Running > 0 {
		u.snapshot.Running--
	}
	u.snapshot.Completed++
	u.snapshot.LastFinishedAt = time.Now()
	u.snapshot.Totals.InputTokens += usage.InputTokens
	u.snapshot.Totals.OutputTokens += usage.OutputTokens
	u.snapshot.Totals.TotalTokens += usage.TotalTokens
	u.snapshot.Totals.CachedInputTokens += usage.CachedInputTokens
	u.snapshot.Totals.ReasoningTokens += usage.ReasoningTokens
	u.snapshot.Totals.EstimatedCostUSD += usage.EstimatedCostUSD
}

func (u *UsageTracker) Fail(modelName string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if trimmed := strings.TrimSpace(modelName); trimmed != "" {
		u.snapshot.Model = trimmed
	}
	if u.snapshot.Running > 0 {
		u.snapshot.Running--
	}
	u.snapshot.Failed++
	u.snapshot.LastFinishedAt = time.Now()
}

func (u *UsageTracker) Snapshot(enabled bool) model.LLMSessionUsage {
	if u == nil {
		return model.LLMSessionUsage{Enabled: enabled}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	snapshot := u.snapshot
	snapshot.Enabled = enabled
	return snapshot
}

func (u *UsageTracker) Reset() {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.snapshot = model.LLMSessionUsage{}
}
