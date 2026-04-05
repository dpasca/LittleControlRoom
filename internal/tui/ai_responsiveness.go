package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	aiStatsLatencyHistoryLimit        = 8
	aiStatsLatencyRecentDisplayLimit  = 5
	aiStatsLatencyInterestingSyncCost = 150 * time.Millisecond
	aiStatsLatencySlowCost            = time.Second
	aiStatsLatencySevereCost          = 5 * time.Second
	uiStallTickThreshold              = 750 * time.Millisecond
	uiStallIgnoreGap                  = 10 * time.Minute
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

type pendingModelSettleOp struct {
	OpID      int64
	Model     string
	Reasoning string
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

func (m *Model) measureAISyncLatency(name, projectPath, detail string, fn func()) {
	startedAt := m.currentTime()
	fn()
	m.recordAISyncLatency(name, projectPath, detail, m.currentTime().Sub(startedAt), "")
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

func (m *Model) beginModelSettleLatency(projectPath, detail, model, reasoning string) {
	projectPath = strings.TrimSpace(projectPath)
	model = strings.TrimSpace(model)
	reasoning = strings.TrimSpace(reasoning)
	if projectPath == "" || model == "" {
		return
	}
	if m.modelSettlePending == nil {
		m.modelSettlePending = make(map[string]pendingModelSettleOp)
	}
	if pending, ok := m.modelSettlePending[projectPath]; ok {
		delete(m.modelSettlePending, projectPath)
		m.completeAILatencyOp(pending.OpID, 0, nil, "superseded")
	}
	opID := m.beginAILatencyOp("Model settle", projectPath, strings.TrimSpace(detail))
	if opID == 0 {
		return
	}
	m.modelSettlePending[projectPath] = pendingModelSettleOp{
		OpID:      opID,
		Model:     model,
		Reasoning: reasoning,
	}
}

func (m *Model) completeModelSettleLatency(projectPath string, snapshot codexapp.Snapshot) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || len(m.modelSettlePending) == 0 {
		return
	}
	pending, ok := m.modelSettlePending[projectPath]
	if !ok {
		return
	}
	if !snapshotReflectsModelSelection(snapshot, pending.Model, pending.Reasoning) {
		return
	}
	delete(m.modelSettlePending, projectPath)
	m.completeAILatencyOp(pending.OpID, 0, nil, "")
}

func (m *Model) cancelModelSettleLatency(projectPath, result string) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || len(m.modelSettlePending) == 0 {
		return
	}
	pending, ok := m.modelSettlePending[projectPath]
	if !ok {
		return
	}
	delete(m.modelSettlePending, projectPath)
	m.completeAILatencyOp(pending.OpID, 0, nil, firstNonEmptyTrimmed(result, "canceled"))
}

func snapshotReflectsModelSelection(snapshot codexapp.Snapshot, modelName, reasoning string) bool {
	modelName = strings.TrimSpace(modelName)
	reasoning = strings.TrimSpace(reasoning)
	if modelName == "" {
		return false
	}
	currentModel := firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
	currentReasoning := firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
	return strings.EqualFold(currentModel, modelName) && strings.EqualFold(currentReasoning, reasoning)
}

func (m *Model) recordUIStallFromSpinnerTick(now time.Time) {
	if now.IsZero() {
		now = m.currentTime()
	}
	last := m.lastSpinnerTickAt
	m.lastSpinnerTickAt = now
	if last.IsZero() {
		return
	}
	gap := now.Sub(last)
	if gap <= spinnerTickInterval+uiStallTickThreshold || gap > uiStallIgnoreGap {
		return
	}
	duration := gap - spinnerTickInterval
	if duration < 0 {
		duration = gap
	}
	projectPath := m.currentLatencyProjectPath()
	m.appendAILatencySample(aiLatencySample{
		Name:        "UI stall",
		ProjectPath: projectPath,
		Detail:      "spinner tick gap",
		Result:      "event loop blocked",
		StartedAt:   now.Add(-duration),
		Duration:    duration,
	})
}

func (m Model) currentLatencyProjectPath() string {
	if projectPath := strings.TrimSpace(m.codexVisibleProject); projectPath != "" {
		return projectPath
	}
	if projectPath := strings.TrimSpace(m.detail.Summary.Path); projectPath != "" {
		return projectPath
	}
	if project, ok := m.selectedProject(); ok {
		return strings.TrimSpace(project.Path)
	}
	return ""
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

func (m Model) uiStallCaptureSection(width int) []string {
	snapshot := m.uiStallDiagnosticsSnapshot()
	lines := []string{
		"",
		detailSectionStyle.Render("Stall Capture"),
	}
	if !snapshot.Enabled {
		lines = append(lines, detailField("Watchdog", detailMutedStyle.Render("off in this run")))
		return lines
	}
	lines = append(lines, detailField("Watchdog", detailValueStyle.Render("armed at "+snapshot.Threshold.String())))
	if root := strings.TrimSpace(snapshot.ArtifactRootDir); root != "" {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, max(12, width-2), "Artifacts: "+m.displayPathWithHomeTilde(root))...)
	}
	if snapshot.CaptureInFlight {
		lines = append(lines, detailField("State", detailWarningStyle.Render("capturing stall artifacts...")))
	}
	if !snapshot.HaveLastCapture {
		lines = append(lines, detailField("Last capture", detailMutedStyle.Render("none yet in this run")))
		return lines
	}
	record := snapshot.LastCapture
	stallText := strings.TrimSpace(record.StallDuration)
	if stallText == "" {
		stallText = "unknown duration"
	}
	lines = append(lines, detailField("Last capture", detailWarningStyle.Render(stallText+" at "+record.CapturedAt.Format(time.RFC3339))))
	if phase := strings.TrimSpace(record.TopActivePhase); phase != "" {
		lines = append(lines, detailField("Phase", detailValueStyle.Render(phase)))
	}
	if project := strings.TrimSpace(record.ActiveProject); project != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, max(12, width-2), "Project: "+m.displayPathWithHomeTilde(project))...)
	}
	if path := strings.TrimSpace(record.Directory); path != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, max(12, width-2), "Capture dir: "+m.displayPathWithHomeTilde(path))...)
	}
	if errText := strings.TrimSpace(record.Error); errText != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, max(12, width-2), "Capture issue: "+errText)...)
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
	m.status = "Performance open. Press c to copy or Esc to close"
	return nil
}

func (m *Model) closePerfDialog(status string) {
	m.showPerf = false
	if status != "" {
		m.status = status
	}
}

func (m *Model) copyPerfToClipboard() tea.Cmd {
	if err := clipboardTextWriter(m.formatPerfCopyText()); err != nil {
		m.reportError("Performance copy failed", err, "")
		return nil
	}
	m.err = nil
	m.status = "Copied performance details to clipboard"
	return nil
}

func (m Model) updatePerfMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "c":
		return m, m.copyPerfToClipboard()
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
	lines = append(lines, m.uiStallCaptureSection(width)...)
	lines = append(lines, "")
	lines = append(lines, commandPaletteHintStyle.Render("Open /perf after a freeze to see recent waits and any captured stall artifacts for this run."))
	lines = append(lines, "")
	lines = append(lines, renderHelpPanelActionRow(
		renderDialogAction("c", "copy", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
}

func (m Model) formatPerfCopyText() string {
	return strings.TrimSpace(ansi.Strip(m.renderPerfContent(96)))
}
