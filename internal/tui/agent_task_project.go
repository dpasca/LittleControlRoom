package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"

	"github.com/charmbracelet/lipgloss"
)

const (
	agentTaskDetailSummarySentenceLimit = 2
	agentTaskDetailSummaryCharLimit     = 240
	agentTaskDetailSummaryMaxLines      = 4
)

func (m Model) agentTaskProjectSummaries() []model.ProjectSummary {
	if len(m.openAgentTasks) == 0 {
		return nil
	}
	out := make([]model.ProjectSummary, 0, len(m.openAgentTasks))
	for _, task := range m.openAgentTasks {
		if !agentTaskIsVisible(task) {
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

func (m Model) isAgentTaskProjectPath(projectPath string) bool {
	_, ok := m.agentTaskForProjectPath(projectPath)
	return ok
}

func (m *Model) upsertOpenAgentTask(task model.AgentTask) {
	selectedPath := ""
	if selected, ok := m.selectedProject(); ok {
		selectedPath = selected.Path
	}
	if agentTaskIsVisible(task) {
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

func agentTaskIsVisible(task model.AgentTask) bool {
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusActive, model.AgentTaskStatusWaiting, model.AgentTaskStatusCompleted:
		return true
	default:
		return false
	}
}

func agentTaskProjectStatus(task model.AgentTask) model.ProjectStatus {
	if task.Workflow.Enabled && (task.Workflow.Phase == "submitted" || task.Workflow.Phase == "awaiting_review") {
		return model.StatusActive
	}
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return model.StatusPossiblyStuck
	case model.AgentTaskStatusCompleted:
		return model.StatusIdle
	default:
		return model.StatusActive
	}
}

func agentTaskSessionFormat(source model.SessionSource) string {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceOpenCode:
		return "opencode_db"
	case model.SessionSourceClaudeCode:
		return "claude_code"
	case model.SessionSourceLCAgent:
		return "lcagent_jsonl"
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
	case model.AgentTaskStatusCompleted:
		return 0
	default:
		provider := codexProviderFromSessionSource(agentTaskDisplaySource(task))
		if strings.TrimSpace(taskSessionIDForProvider(task, provider)) == "" {
			return 75
		}
		return 85
	}
}

func agentTaskClassificationType(task model.AgentTask) model.SessionCategory {
	if task.Workflow.Enabled && (task.Workflow.Phase == "submitted" || task.Workflow.Phase == "awaiting_review") {
		return model.SessionCategoryInProgress
	}
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
	for _, candidate := range []time.Time{task.UpdatedAt, task.LastTouchedAt, task.ResultReadyAt, task.ResultDeliveredAt, task.ResultConsumedAt, task.CompletedAt, task.ArchivedAt} {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	return latest
}

func agentTaskListStatus(task model.AgentTask) string {
	if task.Workflow.Enabled && task.Workflow.Phase != "" {
		return strings.ReplaceAll(task.Workflow.Phase, "_", " ")
	}
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
		if compact := liveEngineerCompactSummary(summary); compact != "" {
			return compact
		}
	}
	parts := []string{fmt.Sprintf("%s task", agentTaskListStatus(task))}
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

// Recovery tasks persist their entire engineer prompt as the summary so the
// blocker survives a restart, and finished workers can report several
// paragraphs. The detail pane only needs the opening statement, so condense to
// the first sentences and let the renderer cut anything still too tall.
func agentTaskDetailSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	condensed := engineerNoticeSummaryText(summary, agentTaskDetailSummarySentenceLimit)
	condensed = cleanEngineerNoticeSummary(compactEngineerNoticeText(condensed, agentTaskDetailSummaryCharLimit))
	if engineerNoticeHasUsefulDetail(condensed) {
		return condensed
	}
	return cleanEngineerNoticeSummary(compactEngineerNoticeText(summary, agentTaskDetailSummaryCharLimit))
}

func (m Model) renderAgentTaskDetailContent(task model.AgentTask, width int) string {
	summary := agentTaskDetailSummary(task.Summary)
	summaryStyle := detailValueStyle
	if summary == "" {
		summary = "No engineer summary yet"
		summaryStyle = detailMutedStyle
	}
	lines := []string{renderWrappedDetailFieldLimited("Summary", summaryStyle, width, summary, agentTaskDetailSummaryMaxLines)}
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
	if task.Workflow.Enabled {
		lines = append(lines, detailField("Workflow", detailValueStyle.Render(fmt.Sprintf("run %d · %s", task.Workflow.RunID, agentTaskListStatus(task)))))
		if result := task.Workflow.Result; result != nil {
			lines = append(lines, renderWrappedDetailField("Worker claims", detailValueStyle, width, fmt.Sprintf("%s · %d criteria · %d checks · %d files", result.Outcome, len(result.Criteria), len(result.Checks), len(result.ChangedFiles))))
		}
		if review := task.Workflow.Review; review != nil {
			lines = append(lines, renderWrappedDetailField("Caller review", detailValueStyle, width, review.Decision+": "+review.Summary))
		}
	}
	if task.Repository.Write {
		lines = append(lines, renderWrappedDetailField("Repository", detailValueStyle, width, task.Repository.Root))
		owner := task.Repository.State
		if owner == "held" {
			owner = "owned by this task"
		}
		lines = append(lines, renderWrappedDetailField("Write ownership", detailValueStyle, width, owner))
		if task.Repository.Error != "" {
			lines = append(lines, renderWrappedDetailField("Repository issue", detailValueStyle, width, task.Repository.Error))
		}
		if task.Repository.Changes != "" {
			lines = append(lines, renderWrappedDetailField("Changes to review", detailValueStyle, width, task.Repository.Changes))
		}
	}
	if label := taskModelLabel(task.ModelSelection); label != "" {
		lines = append(lines, renderWrappedDetailField("Requested model", detailValueStyle, width, label))
	}
	if label := taskModelLabel(task.ObservedModel); label != "" {
		lines = append(lines, renderWrappedDetailField("Reported model", detailValueStyle, width, label))
	}
	if last := agentTaskLastActivity(task); !last.IsZero() {
		lines = append(lines, detailField("Last touched", detailValueStyle.Render(last.Format(time.RFC3339))))
	}
	if affiliation := agentTaskAffiliationSummary(task); affiliation != "" {
		lines = append(lines, renderWrappedDetailField("Affiliation", detailValueStyle, width, affiliation))
	}
	if origin := agentTaskOriginSummary(task); origin != "" {
		lines = append(lines, renderWrappedDetailField("Requested by", detailValueStyle, width, origin))
	}
	if lifecycle := agentTaskResultLifecycleSummary(task); lifecycle != "" {
		lines = append(lines, renderWrappedDetailField("Result", detailValueStyle, width, lifecycle))
	}
	if len(task.Capabilities) > 0 {
		lines = append(lines, renderWrappedDetailField("Capabilities", detailValueStyle, width, strings.Join(task.Capabilities, ", ")))
	}
	if resources := agentTaskResourcesSummary(task.Resources); resources != "" {
		lines = append(lines, renderWrappedDetailField("Resources", detailValueStyle, width, resources))
	}
	if taskSessionIDForProvider(task, codexProviderFromSessionSource(agentTaskDisplaySource(task))) != "" {
		if agentTaskHasCapability(task, "worktree.merge.recover") && model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
			lines = append(lines, detailMutedStyle.Render("Engineer returned. Press Enter to inspect the result; then select the linked worktree and press M to retry merge-back."))
		} else {
			lines = append(lines, detailMutedStyle.Render("Press Enter to open the tracked engineer session."))
		}
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
	case model.AgentTaskResourceTodo:
		todo := strings.TrimSpace(resource.RefID)
		if todo != "" {
			todo = "TODO #" + strings.TrimPrefix(todo, "#")
		}
		if label == "" || todo == "" || strings.Contains(label, todo) {
			return firstNonEmptyTrimmed(label, todo)
		}
		return todo + " — " + label
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

func agentTaskAffiliationSummary(task model.AgentTask) string {
	parts := []string{}
	if projectPath := cleanAgentTaskPath(task.OriginProjectPath); projectPath != "" {
		parts = append(parts, filepath.Base(projectPath))
	}
	if worktreePath := cleanAgentTaskPath(task.OriginWorktreePath); worktreePath != "" && worktreePath != cleanAgentTaskPath(task.OriginProjectPath) {
		parts = append(parts, "worktree "+filepath.Base(worktreePath))
	}
	for _, resource := range task.Resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceTodo {
			continue
		}
		if todo := strings.TrimSpace(resource.RefID); todo != "" {
			parts = append(parts, "TODO #"+strings.TrimPrefix(todo, "#"))
		}
		break
	}
	return strings.Join(parts, " / ")
}

func agentTaskOriginSummary(task model.AgentTask) string {
	provider := codexProviderFromSessionSource(task.OriginProvider)
	parts := []string{}
	if provider != "" {
		parts = append(parts, provider.Label())
	}
	if sessionID := strings.TrimSpace(task.OriginSessionID); sessionID != "" {
		parts = append(parts, shortID(sessionID))
	}
	return strings.Join(parts, " ")
}

func agentTaskResultLifecycleSummary(task model.AgentTask) string {
	if task.Workflow.Enabled {
		if review := task.Workflow.Review; review != nil {
			return fmt.Sprintf("revision %d: %s by %s", review.Revision, review.Decision, review.Reviewer)
		}
		switch task.Workflow.Phase {
		case "canceled":
			return "stopped; any late claims are retained without automatic review"
		case "unclassified":
			return "worker stopped without a structured result; inspect its transcript"
		case "submitted":
			return "claims submitted; waiting for verified idle handoff"
		}
	}
	switch {
	case !task.ResultConsumedAt.IsZero():
		consumer := strings.TrimSpace(task.ResultConsumedBy)
		if consumer == "" {
			consumer = "operator"
		}
		return "consumed by " + consumer + " at " + task.ResultConsumedAt.Format(time.RFC3339)
	case strings.TrimSpace(task.ResultDeliveryError) != "":
		return "callback failed: " + strings.TrimSpace(task.ResultDeliveryError)
	case !task.ResultDeliveredAt.IsZero():
		return "delivered to the originating session; awaiting acceptance"
	case strings.TrimSpace(task.ResultMessageID) != "":
		if task.Workflow.Enabled {
			return "review queued; waiting for the original caller to be open and idle"
		}
		return "callback queued for the originating session"
	case !task.ResultReadyAt.IsZero():
		return "ready for review; no originating-session callback is available"
	default:
		return "worker result pending"
	}
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
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
