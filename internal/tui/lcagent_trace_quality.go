package tui

import (
	"fmt"
	"sort"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

const lcagentProjectTraceQualitySessionLimit = 8

type lcagentTraceQualityMsg struct {
	projectPath string
	quality     projectLCAgentTraceQuality
	err         error
}

type projectLCAgentTraceQuality struct {
	ProjectPath       string
	AvailableSessions int
	ParsedSessions    int
	LatestSessionID   string
	LatestQuality     string
	LatestStatus      string
	LatestOutcome     string
	LatestChecks      []string
	ScoredSessions    int
	ScoreTotal        int
	VerifiedSessions  int
	ProviderFailures  int
	ProviderRetries   int
	ToolFailures      int
	RepairEvents      int
	PendingSessions   int
	ContinuationCount int
	ParseErrors       int
}

func (m Model) loadProjectLCAgentTraceQualityCmd(detail model.ProjectDetail) tea.Cmd {
	projectPath := normalizeProjectPath(detail.Summary.Path)
	if projectPath == "" {
		return nil
	}
	if !projectDetailHasLCAgentTraceArtifacts(detail) {
		return nil
	}
	sessions := append([]model.SessionEvidence(nil), detail.Sessions...)
	return func() tea.Msg {
		quality, err := buildProjectLCAgentTraceQuality(projectPath, sessions)
		return lcagentTraceQualityMsg{projectPath: projectPath, quality: quality, err: err}
	}
}

func projectDetailHasLCAgentTraceArtifacts(detail model.ProjectDetail) bool {
	for _, session := range detail.Sessions {
		if providerForSessionFormat(session.Format) == codexapp.ProviderLCAgent && strings.TrimSpace(session.SessionFile) != "" {
			return true
		}
	}
	return false
}

func buildProjectLCAgentTraceQuality(projectPath string, sessions []model.SessionEvidence) (projectLCAgentTraceQuality, error) {
	quality := projectLCAgentTraceQuality{ProjectPath: normalizeProjectPath(projectPath)}
	lcagentSessions := make([]model.SessionEvidence, 0, len(sessions))
	for _, session := range sessions {
		if providerForSessionFormat(session.Format) != codexapp.ProviderLCAgent {
			continue
		}
		if strings.TrimSpace(session.SessionFile) == "" {
			continue
		}
		lcagentSessions = append(lcagentSessions, session)
	}
	quality.AvailableSessions = len(lcagentSessions)
	if len(lcagentSessions) == 0 {
		return quality, nil
	}
	sort.SliceStable(lcagentSessions, func(i, j int) bool {
		left, right := lcagentSessions[i], lcagentSessions[j]
		if !left.LastEventAt.Equal(right.LastEventAt) {
			return left.LastEventAt.After(right.LastEventAt)
		}
		return strings.TrimSpace(left.SessionID) < strings.TrimSpace(right.SessionID)
	})
	if len(lcagentSessions) > lcagentProjectTraceQualitySessionLimit {
		lcagentSessions = lcagentSessions[:lcagentProjectTraceQualitySessionLimit]
	}

	var firstErr error
	for _, session := range lcagentSessions {
		trace, err := codexapp.ParseLCAgentTraceFile(strings.TrimSpace(session.SessionFile))
		if err != nil {
			quality.ParseErrors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		quality.observeTrace(session, trace)
	}
	return quality, firstErr
}

func (q *projectLCAgentTraceQuality) observeTrace(session model.SessionEvidence, trace codexapp.LCAgentTrace) {
	q.ParsedSessions++
	sessionID := strings.TrimSpace(trace.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(session.ExternalID())
	}
	if q.LatestSessionID == "" {
		q.LatestSessionID = sessionID
		q.LatestQuality = strings.TrimSpace(trace.TraceQualitySummaryLabel())
		q.LatestStatus = strings.TrimSpace(trace.VerificationStatus)
		q.LatestOutcome = strings.TrimSpace(trace.FinalOutcome)
		q.LatestChecks = append([]string(nil), trace.ActualCheckSummaries()...)
	}
	if trace.TraceQuality.Score > 0 {
		q.ScoredSessions++
		q.ScoreTotal += trace.TraceQuality.Score
	}
	if trace.Verified() {
		q.VerifiedSessions++
	}
	q.ProviderFailures += trace.TraceQuality.ProviderFailures
	q.ProviderRetries += trace.TraceQuality.ProviderRetries
	q.ToolFailures += trace.TraceQuality.ToolFailures
	q.RepairEvents += trace.TraceQuality.RepairEvents
	if strings.TrimSpace(trace.PendingStatus) != "" || len(trace.PendingFiles) > 0 || len(trace.PendingVerification) > 0 {
		q.PendingSessions++
	}
	if strings.TrimSpace(trace.ResumeSourceSessionID) != "" || trace.ContinuationChainDepth > 0 {
		q.ContinuationCount++
	}
}

func (m *Model) applyProjectLCAgentTraceQualityMsg(msg lcagentTraceQualityMsg) {
	projectPath := normalizeProjectPath(msg.projectPath)
	if projectPath == "" {
		return
	}
	if m.lcagentTraceQuality == nil {
		m.lcagentTraceQuality = make(map[string]projectLCAgentTraceQuality)
	}
	if msg.quality.ParsedSessions == 0 {
		delete(m.lcagentTraceQuality, projectPath)
		return
	}
	msg.quality.ProjectPath = projectPath
	m.lcagentTraceQuality[projectPath] = msg.quality
}

func (m *Model) clearProjectLCAgentTraceQuality(projectPath string) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || m.lcagentTraceQuality == nil {
		return
	}
	delete(m.lcagentTraceQuality, projectPath)
}

func (m Model) projectLCAgentTraceQualitySummary(projectPath string) string {
	if m.lcagentTraceQuality == nil {
		return ""
	}
	quality, ok := m.lcagentTraceQuality[normalizeProjectPath(projectPath)]
	if !ok {
		return ""
	}
	return quality.summary()
}

func (q projectLCAgentTraceQuality) summary() string {
	if q.ParsedSessions == 0 {
		return ""
	}
	parts := []string{}
	latest := []string{}
	if q.LatestQuality != "" {
		latest = append(latest, "latest "+q.LatestQuality)
	} else if q.LatestStatus != "" {
		latest = append(latest, "latest verification "+q.LatestStatus)
	} else if q.LatestSessionID != "" {
		latest = append(latest, "latest "+shortID(q.LatestSessionID))
	}
	if q.LatestOutcome != "" {
		latest = append(latest, "outcome "+q.LatestOutcome)
	}
	if len(q.LatestChecks) > 0 {
		latest = append(latest, "checks: "+strings.Join(lcagentTraceQualityLimitStrings(q.LatestChecks, 2), "; "))
	}
	if len(latest) > 0 {
		parts = append(parts, strings.Join(latest, "; "))
	}

	rollup := []string{fmt.Sprintf("last %d session%s", q.ParsedSessions, lcagentTraceQualityPlural(q.ParsedSessions))}
	if q.AvailableSessions > q.ParsedSessions {
		rollup[0] = fmt.Sprintf("last %d of %d sessions", q.ParsedSessions, q.AvailableSessions)
	}
	if q.ScoredSessions > 0 {
		rollup = append(rollup, fmt.Sprintf("avg trace quality %d", (q.ScoreTotal+q.ScoredSessions/2)/q.ScoredSessions))
	}
	rollup = append(rollup, fmt.Sprintf("verified %d/%d", q.VerifiedSessions, q.ParsedSessions))
	if q.ProviderFailures > 0 {
		rollup = append(rollup, fmt.Sprintf("provider failures %d", q.ProviderFailures))
	}
	if q.ProviderRetries > 0 {
		rollup = append(rollup, fmt.Sprintf("provider retries %d", q.ProviderRetries))
	}
	if q.ToolFailures > 0 {
		rollup = append(rollup, fmt.Sprintf("tool failures %d", q.ToolFailures))
	}
	if q.RepairEvents > 0 {
		rollup = append(rollup, fmt.Sprintf("repair events %d", q.RepairEvents))
	}
	if q.PendingSessions > 0 {
		rollup = append(rollup, fmt.Sprintf("pending %d", q.PendingSessions))
	}
	if q.ContinuationCount > 0 {
		rollup = append(rollup, fmt.Sprintf("continuations %d", q.ContinuationCount))
	}
	if q.ParseErrors > 0 {
		rollup = append(rollup, fmt.Sprintf("unreadable traces %d", q.ParseErrors))
	}
	parts = append(parts, strings.Join(rollup, ", "))
	return strings.Join(parts, "; ")
}

func lcagentTraceQualityLimitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	out := append([]string(nil), values[:limit]...)
	out = append(out, fmt.Sprintf("+%d more", len(values)-limit))
	return out
}

func lcagentTraceQualityPlural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
