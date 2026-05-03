package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/model"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) agentTaskProjectSummaries() []model.ProjectSummary {
	if len(m.openAgentTasks) == 0 {
		return nil
	}
	out := make([]model.ProjectSummary, 0, len(m.openAgentTasks))
	for _, task := range m.openAgentTasks {
		if !agentTaskIsOpen(task) {
			continue
		}
		project, err := projectSummaryForAgentTask(task)
		if err != nil {
			continue
		}
		out = append(out, project)
	}
	return out
}

func (m Model) agentTaskForProjectPath(projectPath string) (model.AgentTask, bool) {
	projectPath = cleanAgentTaskPath(projectPath)
	if projectPath == "" {
		return model.AgentTask{}, false
	}
	for _, task := range m.openAgentTasks {
		if cleanAgentTaskPath(task.WorkspacePath) == projectPath {
			return task, true
		}
	}
	return model.AgentTask{}, false
}

func (m *Model) upsertOpenAgentTask(task model.AgentTask) {
	selectedPath := ""
	if selected, ok := m.selectedProject(); ok {
		selectedPath = selected.Path
	}
	if agentTaskIsOpen(task) {
		m.openAgentTasks = upsertAgentTask(m.openAgentTasks, task)
	} else {
		m.openAgentTasks = removeAgentTask(m.openAgentTasks, task.ID)
	}
	m.rebuildProjectList(selectedPath)
}

func upsertAgentTask(tasks []model.AgentTask, task model.AgentTask) []model.AgentTask {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return tasks
	}
	out := append([]model.AgentTask(nil), tasks...)
	for i := range out {
		if strings.TrimSpace(out[i].ID) == taskID {
			out[i] = task
			return out
		}
	}
	return append(out, task)
}

func removeAgentTask(tasks []model.AgentTask, taskID string) []model.AgentTask {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || len(tasks) == 0 {
		return tasks
	}
	out := make([]model.AgentTask, 0, len(tasks))
	for _, task := range tasks {
		if strings.TrimSpace(task.ID) == taskID {
			continue
		}
		out = append(out, task)
	}
	return out
}

func agentTaskIsOpen(task model.AgentTask) bool {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusActive, model.AgentTaskStatusWaiting:
		return true
	default:
		return false
	}
}

func agentTaskProjectStatus(task model.AgentTask) model.ProjectStatus {
	if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
		return model.StatusPossiblyStuck
	}
	return model.StatusActive
}

func agentTaskSessionFormat(source model.SessionSource) string {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceOpenCode:
		return "opencode_db"
	case model.SessionSourceClaudeCode:
		return "claude_code"
	case model.SessionSourceCodex:
		return "modern"
	default:
		return ""
	}
}

func agentTaskDisplaySource(task model.AgentTask) model.SessionSource {
	if source := model.NormalizeSessionSource(task.Provider); source != model.SessionSourceUnknown {
		return source
	}
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceEngineerSession {
			continue
		}
		if source := model.NormalizeSessionSource(resource.Provider); source != model.SessionSourceUnknown {
			return source
		}
	}
	return model.SessionSourceUnknown
}

func agentTaskAttentionScore(task model.AgentTask) int {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return 100
	default:
		provider := codexProviderFromSessionSource(agentTaskDisplaySource(task))
		if strings.TrimSpace(taskSessionIDForProvider(task, provider)) == "" {
			return 75
		}
		return 85
	}
}

func agentTaskClassificationType(task model.AgentTask) model.SessionCategory {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return model.SessionCategoryWaitingForUser
	case model.AgentTaskStatusCompleted:
		return model.SessionCategoryCompleted
	default:
		return model.SessionCategoryInProgress
	}
}

func agentTaskLastActivity(task model.AgentTask) time.Time {
	latest := task.CreatedAt
	for _, candidate := range []time.Time{task.UpdatedAt, task.LastTouchedAt, task.CompletedAt, task.ArchivedAt} {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	return latest
}

func agentTaskListStatus(task model.AgentTask) string {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return "review"
	case model.AgentTaskStatusCompleted:
		return "done"
	case model.AgentTaskStatusArchived:
		return "archived"
	default:
		return "agent"
	}
}

func agentTaskListSummary(task model.AgentTask) string {
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		return summary
	}
	parts := []string{fmt.Sprintf("%s task", agentTaskListStatus(task))}
	if name := bossui.EngineerNameForKey("agent_task", task.ID); name != "" {
		parts = append(parts, name)
	}
	if source := agentTaskDisplaySource(task); source != model.SessionSourceUnknown {
		if provider := codexProviderFromSessionSource(source); provider != "" {
			parts = append(parts, provider.Label())
		}
	}
	if sessionID := taskSessionIDForProvider(task, codexProviderFromSessionSource(agentTaskDisplaySource(task))); sessionID != "" {
		parts = append(parts, shortID(sessionID))
	}
	return strings.Join(parts, " - ")
}

func (m Model) renderAgentTaskDetailContent(task model.AgentTask, width int) string {
	summary := strings.TrimSpace(task.Summary)
	summaryStyle := detailValueStyle
	if summary == "" {
		summary = "No engineer summary yet"
		summaryStyle = detailMutedStyle
	}
	lines := []string{renderWrappedDetailField("Summary", summaryStyle, width, summary)}
	lines = append(lines, detailField("Path", detailValueStyle.Render(task.WorkspacePath)))
	lines = appendDetailFields(lines, width,
		detailField("Kind", detailValueStyle.Render("agent task")),
		detailField("Status", agentTaskStatusStyle(task).Render(string(model.NormalizeAgentTaskStatus(task.Status)))),
		detailField("Task ID", detailValueStyle.Render(task.ID)),
		detailField("Attention", detailAttentionValueStyle.Render(fmt.Sprintf("%d", agentTaskAttentionScore(task)))),
	)
	if provider := codexProviderFromSessionSource(agentTaskDisplaySource(task)); provider != "" {
		sessionID := taskSessionIDForProvider(task, provider)
		sessionValue := detailMutedStyle.Render("not attached yet")
		if sessionID != "" {
			sessionValue = sourceStyleForTag(provider.SourceTag(), false).Render(provider.Label()) + " " + detailValueStyle.Render(shortID(sessionID))
		}
		lines = append(lines, detailField("Engineer", sessionValue))
	}
	if last := agentTaskLastActivity(task); !last.IsZero() {
		lines = append(lines, detailField("Last touched", detailValueStyle.Render(last.Format(time.RFC3339))))
	}
	if len(task.Capabilities) > 0 {
		lines = append(lines, renderWrappedDetailField("Capabilities", detailValueStyle, width, strings.Join(task.Capabilities, ", ")))
	}
	if resources := agentTaskResourcesSummary(task.Resources); resources != "" {
		lines = append(lines, renderWrappedDetailField("Resources", detailValueStyle, width, resources))
	}
	if taskSessionIDForProvider(task, codexProviderFromSessionSource(agentTaskDisplaySource(task))) != "" {
		lines = append(lines, detailMutedStyle.Render("Press Enter to open the tracked engineer session."))
	} else {
		lines = append(lines, detailMutedStyle.Render("Press Enter to start an engineer session for this task."))
	}
	return strings.Join(lines, "\n")
}

func agentTaskStatusStyle(task model.AgentTask) lipgloss.Style {
	if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
		return detailWarningStyle
	}
	return detailValueStyle
}

func agentTaskResourcesSummary(resources []model.AgentTaskResource) string {
	if len(resources) == 0 {
		return ""
	}
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		if summary := agentTaskResourceSummary(resource); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, "; ")
}

func agentTaskResourceSummary(resource model.AgentTaskResource) string {
	label := strings.TrimSpace(resource.Label)
	switch model.NormalizeAgentTaskResourceKind(resource.Kind) {
	case model.AgentTaskResourceProject:
		projectPath := cleanAgentTaskPath(resource.ProjectPath)
		projectName := ""
		if projectPath != "" {
			projectName = filepath.Base(projectPath)
		}
		return firstNonEmptyTrimmed(label, projectName, projectPath)
	case model.AgentTaskResourceProcess:
		if resource.PID > 0 {
			return strings.TrimSpace(fmt.Sprintf("pid %d %s", resource.PID, label))
		}
	case model.AgentTaskResourcePort:
		if resource.Port > 0 {
			return strings.TrimSpace(fmt.Sprintf("port %d %s", resource.Port, label))
		}
	case model.AgentTaskResourceFile:
		return firstNonEmptyTrimmed(label, resource.Path)
	case model.AgentTaskResourceAgentTask:
		return firstNonEmptyTrimmed(label, resource.RefID)
	case model.AgentTaskResourceEngineerSession:
		provider := codexProviderFromSessionSource(resource.Provider)
		sessionID := strings.TrimSpace(resource.SessionID)
		if provider != "" && sessionID != "" {
			return provider.Label() + " " + shortID(sessionID)
		}
		return firstNonEmptyTrimmed(label, sessionID)
	}
	return label
}

func cleanAgentTaskPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if path == "." {
		return ""
	}
	return path
}
