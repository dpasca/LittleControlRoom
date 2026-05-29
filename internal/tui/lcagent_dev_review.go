package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

const littleControlRoomProjectName = "LittleControlRoom"

func (m Model) addDevLCAgentReviewTodoCmd(snapshot codexapp.Snapshot) tea.Cmd {
	projectPath := normalizeProjectPath(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	if m.svc == nil {
		return func() tea.Msg {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	target, err := m.resolveLittleControlRoomProject()
	if err != nil {
		return func() tea.Msg {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
	}
	return func() tea.Msg {
		trace, traceErr := loadDevLCAgentReviewTrace(m.appDataDir(), snapshot, projectPath)
		text := devLCAgentReviewTodoText(snapshot, trace, traceErr)
		item, err := m.svc.AddTodo(m.ctx, target.Path, text)
		if err != nil {
			return codexActionMsg{projectPath: projectPath, err: err}
		}
		status := fmt.Sprintf("Added LCAgent review TODO #%d to %s", item.ID, projectNameForPicker(target, target.Path))
		if traceErr != nil {
			status += " with snapshot-only context"
		}
		return codexActionMsg{projectPath: projectPath, status: status}
	}
}

func (m Model) resolveLittleControlRoomProject() (model.ProjectSummary, error) {
	project, err := m.resolveControlProjectRef("", littleControlRoomProjectName)
	if err == nil {
		return project, nil
	}
	var (
		matched model.ProjectSummary
		found   bool
	)
	candidates := append(append([]model.ProjectSummary(nil), m.allProjects...), m.archivedProjects...)
	candidates = append(candidates, m.projects...)
	for _, project := range candidates {
		if filepath.Base(strings.TrimSpace(project.Path)) != littleControlRoomProjectName {
			continue
		}
		if found && normalizeProjectPath(matched.Path) != normalizeProjectPath(project.Path) {
			return model.ProjectSummary{}, fmt.Errorf("Little Control Room project is ambiguous; no TODO added")
		}
		matched = project
		found = true
	}
	if found {
		return matched, nil
	}
	return model.ProjectSummary{}, fmt.Errorf("Little Control Room project is not loaded; no TODO added")
}

func loadDevLCAgentReviewTrace(dataDir string, snapshot codexapp.Snapshot, projectPath string) (codexapp.LCAgentTrace, error) {
	sessionID := strings.TrimSpace(snapshot.ThreadID)
	if sessionID != "" {
		if trace, err := codexapp.LoadLCAgentTrace(dataDir, sessionID, projectPath); err == nil {
			return trace, nil
		}
	}
	return codexapp.LoadLCAgentTrace(dataDir, "", projectPath)
}

func devLCAgentReviewTodoText(snapshot codexapp.Snapshot, trace codexapp.LCAgentTrace, traceErr error) string {
	projectPath := strings.TrimSpace(firstNonEmptyString(trace.ProjectPath, snapshot.ProjectPath))
	sessionID := strings.TrimSpace(firstNonEmptyString(trace.SessionID, snapshot.ThreadID))
	quality := strings.TrimSpace(trace.TraceQualitySummary())
	if quality == "" {
		quality = strings.TrimSpace(snapshot.Status)
	}
	if quality == "" && traceErr != nil {
		quality = "trace artifact unavailable: " + traceErr.Error()
	}

	title := "Review LCAgent trace-quality issue"
	if trace.TraceQuality.Grade != "" {
		title += ": " + trace.TraceQuality.Grade
	}
	if projectPath != "" {
		title += " in " + filepath.Base(projectPath)
	}

	lines := []string{
		title,
		"",
		"Why this exists: an embedded LCAgent run produced a noisy or low-quality status that should become a compact user-facing state plus an actionable developer report.",
	}
	if projectPath != "" {
		lines = append(lines, "Source project: "+projectPath)
	}
	if sessionID != "" {
		lines = append(lines, "LCAgent session: "+sessionID)
	}
	if trace.ArtifactPath != "" {
		lines = append(lines, "Trace artifact: "+trace.ArtifactPath)
	}
	if quality != "" {
		lines = append(lines, "Trace quality: "+quality)
	}
	if len(trace.FilesChanged) > 0 {
		lines = append(lines, "Files changed: "+strings.Join(devLCAgentReviewLimit(trace.FilesChanged, 5), ", "))
	}
	if checks := trace.ActualCheckSummaries(); len(checks) > 0 {
		lines = append(lines, "Actual checks: "+strings.Join(devLCAgentReviewLimit(checks, 4), "; "))
	} else if len(trace.Verification) > 0 {
		lines = append(lines, "Reported verification: "+strings.Join(devLCAgentReviewLimit(trace.Verification, 4), "; "))
	}
	if traceErr != nil {
		lines = append(lines, "Trace load note: "+traceErr.Error())
	}
	lines = append(lines,
		"",
		"Review actions:",
		"- Decide whether this is agent behavior, verifier/harness behavior, project baseline noise, or UX presentation noise.",
		"- Convert any recurring pattern into structured trace/report fields instead of a long status string.",
		"- Keep the embedded status line short and put raw commands/evidence behind the report.",
	)
	return strings.Join(lines, "\n")
}

func devLCAgentReviewLimit(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	out := append([]string(nil), values[:limit]...)
	out = append(out, fmt.Sprintf("+%d more", len(values)-limit))
	return out
}
