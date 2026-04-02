package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	aiStatsLatencyHistoryLimit        = 8
	aiStatsLatencyRecentDisplayLimit  = 5
	aiStatsLatencyInterestingSyncCost = 150 * time.Millisecond
	aiStatsLatencySlowCost            = time.Second
	aiStatsLatencySevereCost          = 5 * time.Second
)

type aiLatencyOp struct {
	ID          int64
	Name        string
	ProjectPath string
	Detail      string
	StartedAt   time.Time
}

type aiLatencySample struct {
	Name        string
	ProjectPath string
	Detail      string
	Result      string
	StartedAt   time.Time
	Duration    time.Duration
	Failed      bool
}

func (m *Model) beginAILatencyOp(name, projectPath, detail string) int64 {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0
	}
	if m.aiLatencyInFlight == nil {
		m.aiLatencyInFlight = make(map[int64]aiLatencyOp)
	}
	m.aiLatencyNextID++
	id := m.aiLatencyNextID
	m.aiLatencyInFlight[id] = aiLatencyOp{
		ID:          id,
		Name:        name,
		ProjectPath: strings.TrimSpace(projectPath),
		Detail:      strings.TrimSpace(detail),
		StartedAt:   m.currentTime(),
	}
	return id
}

func (m *Model) completeAILatencyOp(id int64, duration time.Duration, err error, result string) {
	if id == 0 {
		return
	}
	op, ok := m.aiLatencyInFlight[id]
	if ok {
		delete(m.aiLatencyInFlight, id)
	}
	if duration <= 0 {
		startedAt := op.StartedAt
		if startedAt.IsZero() {
			startedAt = m.currentTime()
		}
		duration = m.currentTime().Sub(startedAt)
		if duration < 0 {
			duration = 0
		}
	}
	failed := err != nil
	result = strings.TrimSpace(result)
	switch {
	case failed:
		result = strings.TrimSpace(err.Error())
	case result == "":
		result = "ok"
	}
	m.appendAILatencySample(aiLatencySample{
		Name:        firstNonEmptyTrimmed(op.Name, "operation"),
		ProjectPath: op.ProjectPath,
		Detail:      op.Detail,
		Result:      result,
		StartedAt:   op.StartedAt,
		Duration:    duration,
		Failed:      failed,
	})
}

func (m *Model) recordAISyncLatency(name, projectPath, detail string, duration time.Duration, result string) {
	if duration < aiStatsLatencyInterestingSyncCost {
		return
	}
	m.appendAILatencySample(aiLatencySample{
		Name:        strings.TrimSpace(name),
		ProjectPath: strings.TrimSpace(projectPath),
		Detail:      strings.TrimSpace(detail),
		Result:      firstNonEmptyTrimmed(result, "ok"),
		StartedAt:   m.currentTime().Add(-duration),
		Duration:    duration,
	})
}

func (m *Model) appendAILatencySample(sample aiLatencySample) {
	if strings.TrimSpace(sample.Name) == "" {
		return
	}
	if sample.Duration < 0 {
		sample.Duration = 0
	}
	m.aiLatencyRecent = append([]aiLatencySample{sample}, m.aiLatencyRecent...)
	if len(m.aiLatencyRecent) > aiStatsLatencyHistoryLimit {
		m.aiLatencyRecent = m.aiLatencyRecent[:aiStatsLatencyHistoryLimit]
	}
}

func (m Model) aiLatencyInFlightSnapshot() []aiLatencyOp {
	if len(m.aiLatencyInFlight) == 0 {
		return nil
	}
	items := make([]aiLatencyOp, 0, len(m.aiLatencyInFlight))
	for _, op := range m.aiLatencyInFlight {
		items = append(items, op)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items
}

func (m Model) aiStatsLatencySection(width int) []string {
	lines := []string{
		"",
		detailSectionStyle.Render("Latency"),
		commandPaletteHintStyle.Render("Recent waits from this Little Control Room run."),
	}
	inFlight := m.aiLatencyInFlightSnapshot()
	if len(inFlight) == 0 {
		lines = append(lines, detailField("In flight", detailMutedStyle.Render("none")))
	} else {
		lines = append(lines, detailField("In flight", detailWarningStyle.Render(fmt.Sprintf("%d operation(s)", len(inFlight)))))
		for _, op := range inFlight {
			elapsed := m.currentTime().Sub(op.StartedAt)
			if elapsed < 0 {
				elapsed = 0
			}
			text := fmt.Sprintf("- %s  %s", formatAILatencyDuration(elapsed), aiLatencyLabel(op.Name, op.ProjectPath, op.Detail))
			lines = append(lines, renderWrappedDialogTextLines(aiLatencySampleStyle(aiLatencySample{Duration: elapsed}), max(12, width-2), text)...)
		}
	}
	if len(m.aiLatencyRecent) == 0 {
		lines = append(lines, detailField("Recent", detailMutedStyle.Render("none yet in this run")))
		return lines
	}
	lines = append(lines, detailField("Recent", detailValueStyle.Render(fmt.Sprintf("%d captured", len(m.aiLatencyRecent)))))
	limit := min(aiStatsLatencyRecentDisplayLimit, len(m.aiLatencyRecent))
	for _, sample := range m.aiLatencyRecent[:limit] {
		text := fmt.Sprintf("- %s  %s", formatAILatencyDuration(sample.Duration), aiLatencyLabel(sample.Name, sample.ProjectPath, sample.Detail))
		if result := strings.TrimSpace(sample.Result); result != "" && !strings.EqualFold(result, "ok") {
			text += "  (" + result + ")"
		}
		lines = append(lines, renderWrappedDialogTextLines(aiLatencySampleStyle(sample), max(12, width-2), text)...)
	}
	return lines
}

func aiLatencyLabel(name, projectPath, detail string) string {
	parts := []string{strings.TrimSpace(name)}
	if project := aiLatencyProjectLabel(projectPath); project != "" {
		parts = append(parts, project)
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		parts = append(parts, detail)
	}
	return strings.Join(parts, "  •  ")
}

func aiLatencyProjectLabel(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(projectPath))
	switch strings.TrimSpace(base) {
	case "", ".", string(filepath.Separator):
		return projectPath
	default:
		return base
	}
}

func aiLatencySampleStyle(sample aiLatencySample) lipgloss.Style {
	switch {
	case sample.Failed || sample.Duration >= aiStatsLatencySevereCost:
		return detailDangerStyle
	case sample.Duration >= aiStatsLatencySlowCost:
		return detailWarningStyle
	default:
		return detailMutedStyle
	}
}

func formatAILatencyDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(100 * time.Microsecond).String()
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(10 * time.Millisecond).String()
}

func (m *Model) openPerfDialog() tea.Cmd {
	m.showPerf = true
	m.showAIStats = false
	m.showHelp = false
	m.err = nil
	m.status = "Performance open. Press Esc to close"
	return nil
}

func (m *Model) closePerfDialog(status string) {
	m.showPerf = false
	if status != "" {
		m.status = status
	}
}

func (m Model) updatePerfMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "?":
		m.closePerfDialog("Performance closed")
	}
	return m, nil
}

func (m Model) renderPerf(bodyW int) string {
	panelWidth := min(bodyW, min(max(60, bodyW-12), 96))
	panelInnerWidth := max(32, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderPerfContent(panelInnerWidth))
}

func (m Model) renderPerfOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderPerf(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderPerfContent(width int) string {
	lines := []string{
		renderDialogHeader("Performance", "Latency", "", width),
	}
	lines = append(lines, m.aiStatsLatencySection(width)...)
	lines = append(lines, "")
	lines = append(lines, commandPaletteHintStyle.Render("Open /perf after a freeze to see whether the wait was backend work or UI-thread rendering."))
	lines = append(lines, "")
	lines = append(lines, renderHelpPanelActionRow(
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
}
