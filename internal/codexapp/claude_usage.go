package codexapp

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"lcroom/internal/claudecli"
)

const (
	claudePlanUsageRefreshInterval = 5 * time.Minute
	claudePlanUsageRefreshTimeout  = 8 * time.Second
)

type claudePlanUsageReader interface {
	Read(context.Context, string) (claudecli.PlanUsage, error)
}

type claudeTokenUsageTracker struct {
	byMessageID      map[string]TokenUsageBreakdown
	anonymousMessage int64
	total            TokenUsageBreakdown
}

func (t *claudeTokenUsageTracker) observe(messageID string, current TokenUsageBreakdown, contextWindow int64) *TokenUsageSnapshot {
	if t.byMessageID == nil {
		t.byMessageID = make(map[string]TokenUsageBreakdown)
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		t.anonymousMessage++
		messageID = fmt.Sprintf("anonymous-%d", t.anonymousMessage)
	}
	if previous, ok := t.byMessageID[messageID]; ok {
		t.total = subtractClaudeTokenUsage(t.total, previous)
	}
	t.byMessageID[messageID] = current
	t.total = addClaudeTokenUsage(t.total, current)
	return &TokenUsageSnapshot{
		Last:               current,
		Total:              t.total,
		ModelContextWindow: contextWindow,
		ContextTokens:      max(current.InputTokens, 0),
	}
}

func addClaudeTokenUsage(left, right TokenUsageBreakdown) TokenUsageBreakdown {
	left.CachedInputTokens += right.CachedInputTokens
	left.InputTokens += right.InputTokens
	left.OutputTokens += right.OutputTokens
	left.ReasoningOutputTokens += right.ReasoningOutputTokens
	left.TotalTokens += right.TotalTokens
	return left
}

func subtractClaudeTokenUsage(left, right TokenUsageBreakdown) TokenUsageBreakdown {
	left.CachedInputTokens = max(left.CachedInputTokens-right.CachedInputTokens, 0)
	left.InputTokens = max(left.InputTokens-right.InputTokens, 0)
	left.OutputTokens = max(left.OutputTokens-right.OutputTokens, 0)
	left.ReasoningOutputTokens = max(left.ReasoningOutputTokens-right.ReasoningOutputTokens, 0)
	left.TotalTokens = max(left.TotalTokens-right.TotalTokens, 0)
	return left
}

type claudeRateLimitInfo struct {
	Status             string   `json:"status"`
	ResetsAt           *int64   `json:"resetsAt"`
	ResetsAtSnake      *int64   `json:"resets_at"`
	RateLimitType      string   `json:"rateLimitType"`
	RateLimitTypeSnake string   `json:"rate_limit_type"`
	Utilization        *float64 `json:"utilization"`
}

func (s *claudeCodeSession) scheduleClaudePlanUsageRefresh(force bool) {
	if s == nil {
		return
	}
	now := time.Now()
	if !s.mu.TryLock() {
		if force {
			go s.scheduleClaudePlanUsageRefreshAfterContention(now)
		}
		return
	}
	s.scheduleClaudePlanUsageRefreshLocked(force, now)
}

func (s *claudeCodeSession) scheduleClaudePlanUsageRefreshAfterContention(now time.Time) {
	s.mu.Lock()
	s.scheduleClaudePlanUsageRefreshLocked(true, now)
}

func (s *claudeCodeSession) scheduleClaudePlanUsageRefreshLocked(force bool, now time.Time) {
	if s.closed || s.planUsageReader == nil {
		s.mu.Unlock()
		return
	}
	if s.usageRefreshActive {
		if force {
			s.usageRefreshQueued = true
		}
		s.mu.Unlock()
		return
	}
	if !force && !s.usageRefreshAt.IsZero() && now.Sub(s.usageRefreshAt) < claudePlanUsageRefreshInterval {
		s.mu.Unlock()
		return
	}
	s.usageRefreshActive = true
	s.usageRefreshAt = now
	reader := s.planUsageReader
	configDir := s.claudeHome
	s.mu.Unlock()

	go s.refreshClaudePlanUsage(reader, configDir)
}

func (s *claudeCodeSession) refreshClaudePlanUsage(reader claudePlanUsageReader, configDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), claudePlanUsageRefreshTimeout)
	usage, err := reader.Read(ctx, configDir)
	cancel()

	windows := []UsageWindowSnapshot(nil)
	if err == nil && usage.Available {
		windows = claudePlanUsageWindows(usage)
	}

	s.mu.Lock()
	changed := false
	if err == nil {
		changed = !claudeUsageWindowsEqual(s.usageWindows, windows)
		s.usageWindows = cloneUsageWindowSnapshots(windows)
	}
	queued := s.usageRefreshQueued && !s.closed
	s.usageRefreshQueued = false
	s.usageRefreshActive = false
	s.mu.Unlock()

	if changed {
		s.notifyAsync()
	}
	if queued {
		s.scheduleClaudePlanUsageRefresh(true)
	}
}

func claudePlanUsageWindows(usage claudecli.PlanUsage) []UsageWindowSnapshot {
	windows := make([]UsageWindowSnapshot, 0, 2)
	if usage.FiveHour != nil {
		windows = append(windows, claudePlanUsageWindow("5h", usage.FiveHour))
	}
	if usage.SevenDay != nil {
		windows = append(windows, claudePlanUsageWindow("weekly", usage.SevenDay))
	}
	return windows
}

func claudePlanUsageWindow(label string, window *claudecli.PlanUsageWindow) UsageWindowSnapshot {
	usedPercent := 0
	resetsAt := time.Time{}
	if window != nil {
		usedPercent = clampUsagePercent(int(math.Round(window.Utilization)))
		resetsAt = window.ResetsAt
	}
	return UsageWindowSnapshot{
		Limit:       "Claude",
		Window:      label,
		LeftPercent: 100 - usedPercent,
		ResetsAt:    resetsAt,
	}
}

func (s *claudeCodeSession) applyClaudeRateLimitInfoLocked(info claudeRateLimitInfo) {
	rateLimitType := firstNonEmptyTrimmed(info.RateLimitType, info.RateLimitTypeSnake)
	windowLabel, limitLabel, ok := claudeRateLimitLabels(rateLimitType)
	if !ok {
		return
	}
	usedPercent := 0
	switch {
	case info.Utilization != nil:
		// Agent SDK rate-limit events report utilization as a fraction from 0 to 1.
		usedPercent = clampUsagePercent(int(math.Round(*info.Utilization * 100)))
	case strings.EqualFold(strings.TrimSpace(info.Status), "rejected"):
		usedPercent = 100
	default:
		return
	}
	window := UsageWindowSnapshot{
		Limit:       limitLabel,
		Window:      windowLabel,
		LeftPercent: 100 - usedPercent,
	}
	resetsAt := info.ResetsAt
	if resetsAt == nil {
		resetsAt = info.ResetsAtSnake
	}
	if resetsAt != nil && *resetsAt > 0 {
		window.ResetsAt = time.Unix(*resetsAt, 0)
	}
	for i := range s.usageWindows {
		if strings.EqualFold(s.usageWindows[i].Limit, window.Limit) && strings.EqualFold(s.usageWindows[i].Window, window.Window) {
			s.usageWindows[i] = window
			sortClaudeUsageWindows(s.usageWindows)
			return
		}
	}
	s.usageWindows = append(s.usageWindows, window)
	sortClaudeUsageWindows(s.usageWindows)
}

func claudeRateLimitLabels(rateLimitType string) (window, limit string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(rateLimitType)) {
	case "five_hour":
		return "5h", "Claude", true
	case "seven_day":
		return "weekly", "Claude", true
	case "seven_day_opus":
		return "weekly", "Claude Opus", true
	case "seven_day_sonnet":
		return "weekly", "Claude Sonnet", true
	default:
		return "", "", false
	}
}

func sortClaudeUsageWindows(windows []UsageWindowSnapshot) {
	sort.SliceStable(windows, func(i, j int) bool {
		left := claudeUsageWindowOrder(windows[i])
		right := claudeUsageWindowOrder(windows[j])
		if left != right {
			return left < right
		}
		return windows[i].Limit < windows[j].Limit
	})
}

func claudeUsageWindowOrder(window UsageWindowSnapshot) int {
	switch strings.ToLower(strings.TrimSpace(window.Window)) {
	case "5h":
		return 0
	case "weekly":
		if strings.EqualFold(strings.TrimSpace(window.Limit), "Claude") {
			return 1
		}
		return 2
	default:
		return 3
	}
}

func clampUsagePercent(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func cloneUsageWindowSnapshots(windows []UsageWindowSnapshot) []UsageWindowSnapshot {
	if len(windows) == 0 {
		return nil
	}
	return append([]UsageWindowSnapshot(nil), windows...)
}

func claudeUsageWindowsEqual(left, right []UsageWindowSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Limit != right[i].Limit ||
			left[i].Plan != right[i].Plan ||
			left[i].Window != right[i].Window ||
			left[i].LeftPercent != right[i].LeftPercent ||
			left[i].CreditBalance != right[i].CreditBalance ||
			left[i].HasCredits != right[i].HasCredits ||
			left[i].CreditsUnlimited != right[i].CreditsUnlimited ||
			!left[i].ResetsAt.Equal(right[i].ResetsAt) {
			return false
		}
	}
	return true
}
