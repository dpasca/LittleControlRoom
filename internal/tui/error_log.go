package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxErrorLogEntries = 200

type errorLogEntry struct {
	At          time.Time
	Status      string
	Message     string
	ProjectPath string
	ProjectName string
}

func (m Model) openErrorLog() (tea.Model, tea.Cmd) {
	m.errorLogVisible = true
	m.commandMode = false
	m.showHelp = false
	if len(m.errorLogEntries) == 0 {
		m.errorLogSelected = 0
		m.status = "Error log open. No errors recorded yet"
		return m, nil
	}
	if m.errorLogSelected < 0 || m.errorLogSelected >= len(m.errorLogEntries) {
		m.errorLogSelected = 0
	}
	if len(m.errorLogEntries) == 1 {
		m.status = "Error log open. 1 entry available"
	} else {
		m.status = fmt.Sprintf("Error log open. %d entries available", len(m.errorLogEntries))
	}
	return m, nil
}

func (m *Model) closeErrorLog(status string) {
	m.errorLogVisible = false
	if status != "" {
		m.status = status
	}
}

func (m *Model) reportError(status string, err error, projectPath string) {
	if err == nil {
		m.err = nil
		m.status = strings.TrimSpace(status)
		return
	}

	m.appendErrorLogEntry(status, err, projectPath)
	m.err = nil
	m.status = errorStatusWithHint(status)
}

func (m *Model) appendErrorLogEntry(status string, err error, projectPath string) {
	if err == nil {
		return
	}

	projectPath = strings.TrimSpace(projectPath)
	projectName := ""
	if projectPath != "" {
		projectName = projectNameForPicker(m.pickerProjectSummary(projectPath), projectPath)
	}

	if m.errorLogVisible && m.errorLogSelected >= 0 {
		m.errorLogSelected++
	}

	entry := errorLogEntry{
		At:          m.currentTime(),
		Status:      errorSummaryText(status),
		Message:     strings.TrimSpace(err.Error()),
		ProjectPath: projectPath,
		ProjectName: strings.TrimSpace(projectName),
	}

	m.errorLogEntries = append([]errorLogEntry{entry}, m.errorLogEntries...)
	if len(m.errorLogEntries) > maxErrorLogEntries {
		m.errorLogEntries = m.errorLogEntries[:maxErrorLogEntries]
	}
	if len(m.errorLogEntries) == 0 {
		m.errorLogSelected = 0
		return
	}
	if m.errorLogSelected < 0 {
		m.errorLogSelected = 0
	}
	if m.errorLogSelected >= len(m.errorLogEntries) {
		m.errorLogSelected = len(m.errorLogEntries) - 1
	}
}

func errorStatusWithHint(status string) string {
	status = errorSummaryText(status)
	if strings.Contains(status, "/errors") {
		return status
	}
	return status + " (use /errors)"
}

func errorSummaryText(status string) string {
	status = singleLineStatusText(status)
	if status == "" {
		return "Error"
	}
	return status
}

func (m Model) currentErrorLogEntries() []errorLogEntry {
	return append([]errorLogEntry(nil), m.errorLogEntries...)
}

func (m Model) currentErrorLogEntry() (errorLogEntry, bool) {
	entries := m.currentErrorLogEntries()
	if len(entries) == 0 {
		return errorLogEntry{}, false
	}
	index := m.errorLogSelected
	if index < 0 {
		index = 0
	}
	if index >= len(entries) {
		index = len(entries) - 1
	}
	return entries[index], true
}

func (m *Model) moveErrorLogSelection(delta int) {
	total := len(m.errorLogEntries)
	if total == 0 || delta == 0 {
		return
	}
	m.errorLogSelected += delta
	if m.errorLogSelected < 0 {
		m.errorLogSelected = 0
	}
	if m.errorLogSelected >= total {
		m.errorLogSelected = total - 1
	}
}

func (m *Model) copySelectedErrorLogEntry() tea.Cmd {
	entry, ok := m.currentErrorLogEntry()
	if !ok {
		m.status = "No error selected"
		return nil
	}
	if err := clipboardTextWriter(formatErrorLogCopyText(entry)); err != nil {
		m.reportError("Error copy failed", err, entry.ProjectPath)
		return nil
	}
	m.err = nil
	m.status = "Copied error details to clipboard"
	return nil
}

func (m Model) updateErrorLogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.currentErrorLogEntries()
	if len(entries) == 0 {
		switch msg.String() {
		case "esc", "q":
			m.closeErrorLog("Error log closed")
		}
		return m, nil
	}

	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			m.errorLogSelected = 0
			return m, nil
		}
	}

	switch msg.String() {
	case "esc", "q":
		m.closeErrorLog("Error log closed")
		return m, nil
	case "up", "k":
		m.moveErrorLogSelection(-1)
		return m, nil
	case "down", "j":
		m.moveErrorLogSelection(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.moveErrorLogSelection(-5)
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveErrorLogSelection(5)
		return m, nil
	case "home":
		m.errorLogSelected = 0
		return m, nil
	case "end", "G":
		m.errorLogSelected = len(entries) - 1
		return m, nil
	case "g":
		m.pendingG = true
		return m, nil
	case "enter", "c":
		return m, m.copySelectedErrorLogEntry()
	}
	return m, nil
}

func (m Model) renderErrorLogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderErrorLogPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderErrorLogPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(72, bodyW-12), 108))
	panelInnerWidth := max(32, panelWidth-4)
	maxContentHeight := max(12, bodyH-2)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderErrorLogContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderErrorLogContent(width, maxHeight int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Error Log"),
		commandPaletteHintStyle.Render("↑↓ choose  Enter copy  c copy  Esc close"),
	}

	entries := m.currentErrorLogEntries()
	if len(entries) == 0 {
		lines = append(lines, "", detailMutedStyle.Render("No errors recorded yet."))
		return strings.Join(lines, "\n")
	}

	listLimit := min(len(entries), max(3, min(6, maxHeight/3)))
	if listLimit < 3 {
		listLimit = min(len(entries), 3)
	}
	start := 0
	if m.errorLogSelected >= listLimit {
		start = m.errorLogSelected - listLimit + 1
	}
	if start < 0 {
		start = 0
	}
	end := min(len(entries), start+listLimit)

	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("Entries"))
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d newer", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderErrorLogRow(entries[i], i == m.errorLogSelected, width))
	}
	if end < len(entries) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d older", len(entries)-end)))
	}

	selected, ok := m.currentErrorLogEntry()
	if !ok {
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "")
	lines = append(lines, detailSectionStyle.Render("Details"))
	lines = append(lines, detailField("Summary", detailValueStyle.Render(selected.Status)))
	lines = append(lines, detailField("When", detailMutedStyle.Render(selected.At.Format("2006-01-02 15:04:05 MST"))))
	if selected.ProjectName != "" {
		lines = append(lines, detailField("Project", detailValueStyle.Render(selected.ProjectName)))
	}
	if selected.ProjectPath != "" {
		lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(displayPathWithHomeTilde(selected.ProjectPath), max(20, width-6)))))
	}

	lines = append(lines, "")
	lines = append(lines, detailDangerStyle.Render("Error"))
	lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, selected.Message)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderErrorLogRow(entry errorLogEntry, selected bool, width int) string {
	timeLabel := entry.At.Format("01-02 15:04")
	summary := entry.Status
	if entry.ProjectName != "" {
		summary = summary + " - " + entry.ProjectName
	}
	row := truncateText(strings.TrimSpace(timeLabel+"  "+summary), width)
	if selected {
		return projectListSelectedRowStyle.Render(row)
	}
	return row
}

func formatErrorLogCopyText(entry errorLogEntry) string {
	lines := []string{
		"Summary: " + entry.Status,
		"When: " + entry.At.Format("2006-01-02 15:04:05 MST"),
	}
	if strings.TrimSpace(entry.ProjectName) != "" {
		lines = append(lines, "Project: "+entry.ProjectName)
	}
	if strings.TrimSpace(entry.ProjectPath) != "" {
		lines = append(lines, "Path: "+entry.ProjectPath)
	}
	lines = append(lines, "", "Error:", strings.TrimSpace(entry.Message))
	return strings.Join(lines, "\n")
}
