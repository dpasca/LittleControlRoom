package llm

import (
	"strings"
	"sync"
	"time"

	"lcroom/internal/model"
)

type UsageTracker struct {
	mu           sync.Mutex
	snapshot     model.LLMSessionUsage
	activeStarts []time.Time
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
	u.activeStarts = append(u.activeStarts, time.Now())
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
	finishedAt := time.Now()
	startedAt := u.popStartLocked()
	if u.snapshot.Running > 0 {
		u.snapshot.Running--
	}
	u.snapshot.Completed++
	u.snapshot.LastFinishedAt = finishedAt
	u.snapshot.Totals.InputTokens += usage.InputTokens
	u.snapshot.Totals.OutputTokens += usage.OutputTokens
	u.snapshot.Totals.TotalTokens += usage.TotalTokens
	u.snapshot.Totals.CachedInputTokens += usage.CachedInputTokens
	u.snapshot.Totals.ReasoningTokens += usage.ReasoningTokens
	u.snapshot.Totals.EstimatedCostUSD += usage.EstimatedCostUSD
	if !startedAt.IsZero() {
		duration := finishedAt.Sub(startedAt)
		if duration > 0 {
			u.snapshot.LastRequestDuration = duration
			u.snapshot.TotalRequestDuration += duration
			if usage.OutputTokens > 0 {
				u.snapshot.LastOutputTokensPerSecond = float64(usage.OutputTokens) / duration.Seconds()
			}
			if u.snapshot.Totals.OutputTokens > 0 && u.snapshot.TotalRequestDuration > 0 {
				u.snapshot.AverageOutputTokensPerSecond = float64(u.snapshot.Totals.OutputTokens) / u.snapshot.TotalRequestDuration.Seconds()
			}
		}
	}
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
	_ = u.popStartLocked()
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
	u.activeStarts = nil
}

func (u *UsageTracker) popStartLocked() time.Time {
	if u == nil || len(u.activeStarts) == 0 {
		return time.Time{}
	}
	startedAt := u.activeStarts[0]
	copy(u.activeStarts, u.activeStarts[1:])
	u.activeStarts = u.activeStarts[:len(u.activeStarts)-1]
	return startedAt
}
