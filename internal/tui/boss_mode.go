package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const engineerNoticeArtifactLimit = 4

func (m Model) openBossMode() (tea.Model, tea.Cmd) {
	m.bossMode = true
	var initCmd tea.Cmd
	if !m.bossModelActive {
		m.bossModel = bossui.NewEmbeddedWithViewContext(m.ctx, m.svc, m.bossViewContext())
		m.bossModelActive = true
		initCmd = m.bossModel.Init()
	} else {
		m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
		initCmd = m.bossModel.ActivateCmd()
	}
	m.status = "Boss mode open. Esc hides it and keeps replies running."
	if m.width > 0 && m.height > 0 {
		updated, _ := m.bossModel.Update(m.bossModeWindowSizeMsg())
		m.bossModel = normalizeBossModel(updated)
	}
	m, noticeCmd := m.drainPendingBossHostNotices()
	return m, tea.Batch(initCmd, noticeCmd)
}

func (m *Model) closeBossMode(status string) {
	m.bossMode = false
	if status != "" {
		m.status = status
	}
	m.syncDetailViewport(false)
}

func (m Model) updateBossModeMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.bossModel.Update(msg)
	m.bossModel = normalizeBossModel(updated)
	m, noticeCmd := m.drainPendingBossHostNotices()
	return m, tea.Batch(cmd, noticeCmd)
}

type bossHostNotice struct {
	Content        string
	AnnounceInChat bool
	Handoff        *bossui.HandoffHighlight
}

func (m Model) updateBossHostNotice(content string) (Model, tea.Cmd) {
	return m.updateBossHostNoticeWithOptions(bossHostNotice{Content: content})
}

func (m Model) updateBossHostChatNotice(content string) (Model, tea.Cmd) {
	return m.updateBossHostNoticeWithOptions(bossHostNotice{Content: content, AnnounceInChat: true})
}

func (m Model) updateBossHostNoticeWithOptions(notice bossHostNotice) (Model, tea.Cmd) {
	content := strings.TrimSpace(notice.Content)
	if content == "" {
		return m, nil
	}
	if !m.bossMode || !m.bossModel.HostNoticesReady() {
		notice.Content = content
		m.pendingBossHostNotices = appendPendingBossHostNotice(m.pendingBossHostNotices, notice)
		return m, nil
	}
	notice.Content = content
	return m.applyBossHostNotice(notice)
}

func (m Model) applyBossHostNotice(notice bossHostNotice) (Model, tea.Cmd) {
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.bossModel.Update(bossui.HostNoticeMsg{Content: notice.Content, AnnounceInChat: notice.AnnounceInChat, Handoff: notice.Handoff})
	m.bossModel = normalizeBossModel(updated)
	return m, cmd
}

func (m Model) drainPendingBossHostNotices() (Model, tea.Cmd) {
	if !m.bossMode || len(m.pendingBossHostNotices) == 0 || !m.bossModel.HostNoticesReady() {
		return m, nil
	}
	notices := append([]bossHostNotice(nil), m.pendingBossHostNotices...)
	m.pendingBossHostNotices = nil
	cmds := make([]tea.Cmd, 0, len(notices))
	for _, notice := range notices {
		var cmd tea.Cmd
		m, cmd = m.applyBossHostNotice(notice)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func appendPendingBossHostNotice(notices []bossHostNotice, notice bossHostNotice) []bossHostNotice {
	notice.Content = strings.TrimSpace(notice.Content)
	if notice.Content == "" {
		return notices
	}
	if len(notices) > 0 && notices[len(notices)-1] == notice {
		return notices
	}
	notices = append(notices, notice)
	if len(notices) > 8 {
		notices = append([]bossHostNotice(nil), notices[len(notices)-8:]...)
	}
	return notices
}

func (m Model) updateBossModeWindowSize() (tea.Model, tea.Cmd) {
	m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.bossModel.Update(m.bossModeWindowSizeMsg())
	m.bossModel = normalizeBossModel(updated)
	return m, cmd
}

type agentTaskEngineerReturnedMsg struct {
	projectPath  string
	taskID       string
	label        string
	engineerName string
	summary      string
	notice       string
	handoff      *bossui.HandoffHighlight
	snapshot     codexapp.Snapshot
	task         model.AgentTask
	err          error
}

type bossEngineerReturnedMsg struct {
	projectPath  string
	label        string
	engineerName string
	summary      string
	notice       string
	handoff      *bossui.HandoffHighlight
	snapshot     codexapp.Snapshot
}

func (m Model) updateBossModeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.bossModel.SessionPickerActive() {
		if index, ok := bossModeAttentionJumpIndex(msg); ok {
			return m.openBossAttentionProject(index)
		}
	}
	return m.updateBossModeMessage(msg)
}

func (m Model) openBossAttentionProject(index int) (tea.Model, tea.Cmd) {
	item := m.bossModel.HotAttentionItem(index)
	switch item.Kind {
	case bossui.AttentionItemAgentTask:
		return m.openBossAttentionAgentTask(index, item.TaskID)
	case bossui.AttentionItemProject:
		return m.openBossAttentionProjectItem(index, item.ProjectPath)
	default:
		m.status = fmt.Sprintf("No attention item is mapped to Alt+%d", index+1)
		return m, nil
	}
}

func (m Model) openBossAttentionProjectItem(index int, projectPath string) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.status = fmt.Sprintf("No attention project is mapped to Alt+%d", index+1)
		return m, nil
	}
	project, ok := m.projectSummaryByPathAllProjects(projectPath)
	if !ok {
		m.status = fmt.Sprintf("Project for Alt+%d is no longer in the list", index+1)
		return m, nil
	}
	m.closeBossMode(fmt.Sprintf("Opening engineer session for %s", projectNameForPicker(project, project.Path)))
	focusCmd := m.focusProjectPath(project.Path)
	updated, launchCmd := m.launchEmbeddedForProject(project, m.preferredEmbeddedProviderForProject(project), false, "")
	m = normalizeUpdateModel(updated)
	return m, tea.Batch(focusCmd, launchCmd)
}

func (m Model) openBossAttentionAgentTask(index int, taskID string) (tea.Model, tea.Cmd) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || m.svc == nil {
		m.status = fmt.Sprintf("Agent task for Alt+%d is no longer available", index+1)
		return m, nil
	}
	task, err := m.svc.GetAgentTask(m.ctx, taskID)
	if err != nil {
		m.status = fmt.Sprintf("Agent task for Alt+%d is no longer available: %v", index+1, err)
		return m, nil
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	provider := codexProviderFromSessionSource(task.Provider)
	if provider == "" {
		provider = codexapp.ProviderCodex
	}
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = task.ID
	}
	m.closeBossMode(fmt.Sprintf("Opening engineer session for task %s", title))
	updated, launchCmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		reveal:   true,
		resumeID: taskSessionIDForProvider(task, provider),
	})
	m = normalizeUpdateModel(updated)
	return m, launchCmd
}

func (m Model) bossModeWindowSizeMsg() tea.WindowSizeMsg {
	layout := m.bodyLayout()
	return tea.WindowSizeMsg{Width: layout.width, Height: bossModeBodyHeight(layout.height)}
}

func (m Model) renderBossModeView() string {
	layout := m.bodyLayout()
	header := m.renderBossModeHeader(layout.width)
	bodyHeight := bossModeBodyHeight(layout.height)
	body := fitPaneContent(m.bossModel.View(), layout.width, bodyHeight)
	return strings.Join([]string{header, body, m.renderBossModeFooter(layout.width)}, "\n")
}

func (m Model) renderBossModeHeader(width int) string {
	parts := []string{
		bossModeTitleStyle.Render("Boss Mode"),
		renderFooterStatus(m.bossModel.StatusText()),
		renderFooterMeta("high-level project chat"),
	}
	if notice := processWarningFooterLabel(m.totalProcessWarningStats()); notice != "" {
		parts = append(parts, renderFooterAlert(notice))
	}
	line := strings.Join(parts, "  ")
	return renderLineWithRightSegment(line, m.renderTopCPUUsageSegment(), width)
}

func (m Model) renderBossModeFooter(width int) string {
	actions := []footerAction{
		footerPrimaryAction("Enter", "send"),
		footerNavAction("Alt+Enter", "newline"),
		footerNavAction("Alt+1..8", "jump"),
		footerLowAction("Alt+C", "copy menu"),
		footerNavAction("Ctrl+R", "refresh"),
		footerHideAction("Esc", "hide"),
	}
	if m.bossModel.SlashActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "run"),
			footerNavAction("Tab", "complete"),
			footerNavAction("Shift+Tab", "previous"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy menu"),
			footerHideAction("Esc", "hide"),
		}
	}
	if m.bossModel.ControlConfirmationActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "confirm"),
			footerExitAction("Esc", "cancel"),
		}
	}
	if m.bossModel.SessionPickerActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "open"),
			footerNavAction("Up/Down", "select"),
			footerExitAction("Esc", "close"),
		}
	}
	return fitStyledWidth(renderFooterLine(
		width,
		renderFooterActionList(actions...),
		renderFooterMeta("/boss off also hides"),
	), width)
}

var bossModeTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))

func bossModeBodyHeight(shellBodyHeight int) int {
	return max(1, shellBodyHeight)
}

func (m Model) bossViewContext() bossui.ViewContext {
	view := bossui.ViewContext{
		Active:              true,
		Embedded:            true,
		Loading:             m.loading,
		AllProjectCount:     len(m.allProjects),
		VisibleProjectCount: len(m.projects),
		FocusedPane:         string(m.focusedPane),
		SortMode:            string(m.sortMode),
		Visibility:          string(m.visibility),
		Filter:              strings.TrimSpace(m.projectFilter),
		Status:              strings.TrimSpace(m.status),
		PrivacyMode:         m.privacyMode,
		PrivacyPatterns:     append([]string(nil), m.privacyPatterns...),
		EngineerActivities:  m.bossEngineerActivities(),
		RuntimeContexts:     m.bossRuntimeContexts(),
	}
	if notice := processWarningSystemNoticeSummary(m.totalProcessWarningStats()); notice != "" {
		view.SystemNotices = append(view.SystemNotices, bossui.ViewSystemNotice{
			Code:     "process_suspicious",
			Severity: "warning",
			Summary:  notice,
			Count:    m.totalProcessWarningCount(),
		})
	}
	if notice := cpuSnapshotSystemNoticeSummary(m.cpuSnapshot, !m.privacyMode); notice != "" {
		view.SystemNotices = append(view.SystemNotices, bossui.ViewSystemNotice{
			Code:     "cpu_hot",
			Severity: "warning",
			Summary:  notice,
			Count:    cpuSnapshotHotProcessCount(m.cpuSnapshot),
		})
	}
	if m.browserAttention != nil {
		view.SystemNotices = append(view.SystemNotices, bossui.ViewSystemNotice{
			Code:     "browser_waiting",
			Severity: "warning",
			Summary:  bossBrowserAttentionNoticeSummary(*m.browserAttention),
			Count:    1,
		})
	}
	if m.questionNotify != nil {
		view.SystemNotices = append(view.SystemNotices, bossui.ViewSystemNotice{
			Code:     "engineer_input_waiting",
			Severity: "warning",
			Summary:  bossQuestionNoticeSummary(*m.questionNotify),
			Count:    1,
		})
	}
	return view
}

func (m Model) bossRuntimeContexts() []bossui.ViewRuntimeContext {
	const limit = 5
	contexts := make([]bossui.ViewRuntimeContext, 0, limit)
	seen := map[string]bool{}
	add := func(project model.ProjectSummary) {
		if len(contexts) >= limit {
			return
		}
		projectPath := filepath.Clean(strings.TrimSpace(project.Path))
		if projectPath == "" || projectPath == "." || seen[projectPath] {
			return
		}
		snapshot := m.projectRuntimeSnapshot(projectPath)
		if !runtimeDetailAvailable(project.RunCommand, snapshot) {
			return
		}
		contexts = append(contexts, bossRuntimeContextFromProject(project, snapshot))
		seen[projectPath] = true
	}
	if project, ok := m.selectedProject(); ok {
		add(project)
	}
	for _, project := range m.projects {
		add(project)
	}
	for _, project := range m.allProjects {
		add(project)
	}
	return contexts
}

func bossRuntimeContextFromProject(project model.ProjectSummary, snapshot projectrun.Snapshot) bossui.ViewRuntimeContext {
	projectPath := filepath.Clean(strings.TrimSpace(project.Path))
	if projectPath == "." {
		projectPath = ""
	}
	primaryURL := runtimePrimaryURL(snapshot)
	additionalURLs := append([]string(nil), snapshot.AnnouncedURLs...)
	if primaryURL != "" && len(additionalURLs) > 0 && strings.TrimSpace(additionalURLs[0]) == primaryURL {
		additionalURLs = additionalURLs[1:]
	}
	return bossui.ViewRuntimeContext{
		ProjectPath:    projectPath,
		ProjectName:    projectNameForPicker(project, projectPath),
		Command:        effectiveRuntimeCommand(project.RunCommand, snapshot),
		Status:         bossRuntimePlainStatus(snapshot),
		PrimaryURL:     primaryURL,
		AdditionalURLs: additionalURLs,
		Ports:          append([]int(nil), snapshot.Ports...),
		Running:        snapshot.Running,
	}
}

func bossRuntimePlainStatus(snapshot projectrun.Snapshot) string {
	switch {
	case snapshot.Running:
		return "running"
	case strings.TrimSpace(snapshot.LastError) != "":
		return "failed: " + strings.TrimSpace(snapshot.LastError)
	case snapshot.ExitCodeKnown:
		if snapshot.ExitCode == 0 {
			return "exited"
		}
		return fmt.Sprintf("exit %d", snapshot.ExitCode)
	case !snapshot.ExitedAt.IsZero():
		return "stopped"
	default:
		return "configured"
	}
}

func (m Model) bossEngineerActivities() []bossui.ViewEngineerActivity {
	activities := make([]bossui.ViewEngineerActivity, 0, len(m.openAgentTasks)+len(m.codexSnapshots))
	seen := map[string]bool{}
	for _, task := range m.openAgentTasks {
		if !agentTaskIsOpen(task) {
			continue
		}
		snapshot, ok := m.cachedAgentTaskSnapshot(task)
		if !ok {
			continue
		}
		activity, ok := bossAgentTaskActivityFromSnapshot(task, snapshot)
		if ok {
			seen[bossEngineerActivityKey(activity)] = true
			activities = append(activities, activity)
		}
	}
	for _, snapshot := range m.liveCodexSnapshots() {
		activity, ok := m.bossProjectEngineerActivityFromSnapshot(snapshot)
		if !ok {
			continue
		}
		key := bossEngineerActivityKey(activity)
		if seen[key] {
			continue
		}
		seen[key] = true
		activities = append(activities, activity)
	}
	return activities
}

func (m Model) cachedAgentTaskSnapshot(task model.AgentTask) (codexapp.Snapshot, bool) {
	path := strings.TrimSpace(task.WorkspacePath)
	if path == "" {
		return codexapp.Snapshot{}, false
	}
	snapshot, ok := m.codexCachedSnapshot(path)
	if !ok || !snapshot.Started || snapshot.Closed {
		return codexapp.Snapshot{}, false
	}
	provider := codexProviderFromSessionSource(task.Provider)
	if provider != "" && embeddedProvider(snapshot) != provider {
		return codexapp.Snapshot{}, false
	}
	return snapshot, true
}

func bossAgentTaskActivityFromSnapshot(task model.AgentTask, snapshot codexapp.Snapshot) (bossui.ViewEngineerActivity, bool) {
	if !bossEngineerSnapshotActive(snapshot) {
		return bossui.ViewEngineerActivity{}, false
	}
	return bossui.ViewEngineerActivity{
		Kind:         "agent_task",
		TaskID:       strings.TrimSpace(task.ID),
		ProjectPath:  strings.TrimSpace(task.WorkspacePath),
		Title:        strings.TrimSpace(task.Title),
		EngineerName: bossEngineerNameForAgentTask(task),
		Provider:     modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)),
		SessionID:    strings.TrimSpace(snapshot.ThreadID),
		Status:       bossEngineerActivityStatus(snapshot),
		Active:       true,
		StartedAt:    bossEngineerActivityStartedAt(snapshot),
		LastEventAt:  embeddedSnapshotActivityAt(snapshot),
	}, true
}

func (m Model) bossProjectEngineerActivityFromSnapshot(snapshot codexapp.Snapshot) (bossui.ViewEngineerActivity, bool) {
	if !bossEngineerSnapshotActive(snapshot) {
		return bossui.ViewEngineerActivity{}, false
	}
	projectPath := strings.TrimSpace(snapshot.ProjectPath)
	if projectPath == "" {
		return bossui.ViewEngineerActivity{}, false
	}
	title := filepath.Base(projectPath)
	if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
		title = projectNameForPicker(project, projectPath)
	}
	return bossui.ViewEngineerActivity{
		Kind:         "project",
		ProjectPath:  projectPath,
		Title:        strings.TrimSpace(title),
		EngineerName: bossui.EngineerNameForKey("project", projectPath, snapshot.ThreadID),
		Provider:     modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)),
		SessionID:    strings.TrimSpace(snapshot.ThreadID),
		Status:       bossEngineerActivityStatus(snapshot),
		Active:       true,
		StartedAt:    bossEngineerActivityStartedAt(snapshot),
		LastEventAt:  embeddedSnapshotActivityAt(snapshot),
	}, true
}

func bossEngineerActivityKey(activity bossui.ViewEngineerActivity) string {
	return strings.Join([]string{
		strings.TrimSpace(activity.ProjectPath),
		strings.TrimSpace(string(model.NormalizeSessionSource(activity.Provider))),
		strings.TrimSpace(activity.SessionID),
	}, "\x00")
}

func bossEngineerNameForAgentTask(task model.AgentTask) string {
	return bossui.EngineerNameForKey("agent_task", task.ID)
}

func bossEngineerActivityStartedAt(snapshot codexapp.Snapshot) time.Time {
	startedAt := snapshot.BusySince
	if startedAt.IsZero() {
		startedAt = snapshot.LastBusyActivityAt
	}
	if startedAt.IsZero() {
		startedAt = snapshot.LastActivityAt
	}
	return startedAt
}

func bossEngineerSnapshotActive(snapshot codexapp.Snapshot) bool {
	if !snapshot.Started || snapshot.Closed {
		return false
	}
	if snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return true
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return true
	}
	switch snapshot.Phase {
	case codexapp.SessionPhaseRunning,
		codexapp.SessionPhaseFinishing,
		codexapp.SessionPhaseReconciling,
		codexapp.SessionPhaseStalled,
		codexapp.SessionPhaseExternal:
		return true
	default:
		return false
	}
}

func bossEngineerActivityStatus(snapshot codexapp.Snapshot) string {
	switch snapshot.Phase {
	case codexapp.SessionPhaseStalled:
		return "stalled"
	case codexapp.SessionPhaseFinishing:
		return "finishing"
	case codexapp.SessionPhaseReconciling:
		return "rechecking"
	case codexapp.SessionPhaseExternal:
		return "working elsewhere"
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return "waiting"
	}
	if snapshot.BusyExternal {
		return "working elsewhere"
	}
	if snapshot.Busy || strings.TrimSpace(snapshot.ActiveTurnID) != "" {
		return "working"
	}
	if status := normalizedCodexStatus(snapshot.Status); status != "" {
		return status
	}
	return "working"
}

func (m Model) bossEngineerTurnCompletionHostNotice(projectPath string, hadPrev bool, prevSnapshot, snapshot codexapp.Snapshot) bossHostNotice {
	if !hadPrev || !bossEngineerSnapshotActive(prevSnapshot) || bossEngineerSnapshotActive(snapshot) {
		return bossHostNotice{}
	}
	label := m.bossEngineerCompletionLabel(projectPath)
	engineerName := m.bossEngineerCompletionName(projectPath, snapshot)
	output := latestEngineerTranscriptOutput(snapshot)
	if output != "" {
		return bossEngineerCompletionHostNotice(label, output, engineerName)
	}
	return bossEngineerCompletionHostNotice(label, "", engineerName)
}

func (m Model) handleBossEngineerTurnCompletion(projectPath string, hadPrev bool, prevSnapshot, snapshot codexapp.Snapshot) (Model, tea.Cmd) {
	if !hadPrev || !bossEngineerSnapshotActive(prevSnapshot) || bossEngineerSnapshotActive(snapshot) {
		return m, nil
	}
	session, _ := m.codexSession(projectPath)
	task, isAgentTask := m.agentTaskForProjectPath(projectPath)
	if isAgentTask && m.svc != nil {
		return m, m.markAgentTaskReadyForReviewCmd(projectPath, task, snapshot, session)
	}
	if latestEngineerTranscriptOutput(snapshot) == "" && session != nil {
		return m, m.bossEngineerCompletionNoticeCmd(projectPath, snapshot, session)
	}
	notice := m.bossEngineerTurnCompletionHostNotice(projectPath, hadPrev, prevSnapshot, snapshot)
	if strings.TrimSpace(notice.Content) == "" {
		return m, nil
	}
	return m.updateBossHostNoticeWithOptions(notice)
}

func (m Model) markAgentTaskReadyForReviewCmd(projectPath string, task model.AgentTask, snapshot codexapp.Snapshot, session codexapp.Session) tea.Cmd {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" || m.svc == nil || m.svc.Store() == nil {
		return nil
	}
	label := bossAgentTaskCompletionLabel(task)
	engineerName := bossEngineerNameForAgentTask(task)
	svc := m.svc
	parent := m.ctx
	return func() tea.Msg {
		snapshot = freshEngineerCompletionSnapshot(projectPath, snapshot, session)
		summary := latestEngineerTranscriptReviewOutput(snapshot)
		notice := bossAgentTaskReviewNotice(label, summary, engineerName)
		ctx := parent
		if ctx == nil {
			ctx = context.Background()
		}
		completeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		status := model.AgentTaskStatusWaiting
		updated, err := svc.Store().UpdateAgentTask(completeCtx, model.UpdateAgentTaskInput{
			ID:      taskID,
			Status:  &status,
			Summary: &summary,
			Touch:   true,
		})
		return agentTaskEngineerReturnedMsg{
			projectPath:  strings.TrimSpace(projectPath),
			taskID:       taskID,
			label:        label,
			engineerName: engineerName,
			summary:      summary,
			notice:       notice,
			handoff:      bossEngineerCompletionHandoff(label, engineerName),
			snapshot:     snapshot,
			task:         updated,
			err:          err,
		}
	}
}

func (m Model) bossEngineerCompletionNoticeCmd(projectPath string, snapshot codexapp.Snapshot, session codexapp.Session) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || session == nil {
		return nil
	}
	label := m.bossEngineerCompletionLabel(projectPath)
	engineerName := m.bossEngineerCompletionName(projectPath, snapshot)
	return func() tea.Msg {
		snapshot = freshEngineerCompletionSnapshot(projectPath, snapshot, session)
		summary := latestEngineerTranscriptOutput(snapshot)
		return bossEngineerReturnedMsg{
			projectPath:  projectPath,
			label:        label,
			engineerName: engineerName,
			summary:      summary,
			notice:       bossEngineerCompletionNotice(label, summary, engineerName),
			handoff:      bossEngineerCompletionHandoff(label, engineerName),
			snapshot:     snapshot,
		}
	}
}

func freshEngineerCompletionSnapshot(projectPath string, snapshot codexapp.Snapshot, session codexapp.Session) codexapp.Snapshot {
	projectPath = strings.TrimSpace(projectPath)
	snapshot = snapshotWithCompletionProjectPath(projectPath, snapshot)
	if latestEngineerTranscriptOutput(snapshot) != "" || session == nil {
		return snapshot
	}
	fresh := snapshotWithCompletionProjectPath(projectPath, session.Snapshot())
	if latestEngineerTranscriptOutput(fresh) != "" {
		return fresh
	}
	if len(fresh.Entries) > len(snapshot.Entries) || fresh.TranscriptRevision > snapshot.TranscriptRevision {
		return fresh
	}
	return snapshot
}

func snapshotWithCompletionProjectPath(projectPath string, snapshot codexapp.Snapshot) codexapp.Snapshot {
	if strings.TrimSpace(snapshot.ProjectPath) == "" {
		snapshot.ProjectPath = strings.TrimSpace(projectPath)
	}
	return snapshot
}

func bossAgentTaskReviewNotice(label, output, engineerName string) string {
	if strings.TrimSpace(output) == "" {
		return bossEngineerCompletionNotice(label, "I don't have a useful summary yet.", engineerName)
	}
	return bossEngineerCompletionNotice(label, output, engineerName)
}

func bossAgentTaskCompletionLabel(task model.AgentTask) string {
	if title := strings.TrimSpace(task.Title); title != "" {
		return title
	}
	if id := strings.TrimSpace(task.ID); id != "" {
		return id
	}
	return "agent task"
}

func bossEngineerCompletionNotice(label, output, engineerName string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "engineer session"
	}
	engineerName = strings.TrimSpace(engineerName)
	if engineerName == "" {
		engineerName = "Engineer"
	}
	output = strings.TrimSpace(output)
	if output != "" {
		return engineerName + " is back from " + label + ": " + output
	}
	return engineerName + " is back from " + label + "."
}

func bossEngineerCompletionHostNotice(label, output, engineerName string) bossHostNotice {
	return bossHostNotice{
		Content:        bossEngineerCompletionNotice(label, output, engineerName),
		AnnounceInChat: true,
		Handoff:        bossEngineerCompletionHandoff(label, engineerName),
	}
}

func bossEngineerCompletionHandoff(label, engineerName string) *bossui.HandoffHighlight {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "engineer session"
	}
	engineerName = strings.TrimSpace(engineerName)
	if engineerName == "" {
		engineerName = "Engineer"
	}
	return &bossui.HandoffHighlight{EngineerName: engineerName, ProjectLabel: label}
}

func (m Model) bossEngineerCompletionLabel(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if task, ok := m.agentTaskForProjectPath(projectPath); ok {
		if title := strings.TrimSpace(task.Title); title != "" {
			return title
		}
		if id := strings.TrimSpace(task.ID); id != "" {
			return id
		}
	}
	if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
		if name := projectNameForPicker(project, projectPath); strings.TrimSpace(name) != "" {
			return name
		}
	}
	if base := strings.TrimSpace(filepath.Base(projectPath)); base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	if projectPath != "" {
		return projectPath
	}
	return "engineer session"
}

func (m Model) bossEngineerCompletionName(projectPath string, snapshot codexapp.Snapshot) string {
	projectPath = strings.TrimSpace(projectPath)
	if task, ok := m.agentTaskForProjectPath(projectPath); ok {
		return bossEngineerNameForAgentTask(task)
	}
	return bossui.EngineerNameForKey("project", projectPath, snapshot.ThreadID)
}

func latestEngineerTranscriptOutput(snapshot codexapp.Snapshot) string {
	return latestEngineerTranscriptOutputWithSentences(snapshot, 1, 220)
}

func latestEngineerTranscriptReviewOutput(snapshot codexapp.Snapshot) string {
	return latestEngineerTranscriptOutputWithSentences(snapshot, 3, 360)
}

func latestEngineerTranscriptOutputWithSentences(snapshot codexapp.Snapshot, sentenceLimit, charLimit int) string {
	for i := len(snapshot.Entries) - 1; i >= 0; i-- {
		entry := snapshot.Entries[i]
		if entry.Kind != codexapp.TranscriptAgent && entry.Kind != codexapp.TranscriptPlan {
			continue
		}
		text := strings.TrimSpace(firstNonEmptyTrimmed(entry.DisplayText, entry.Text))
		if text == "" {
			continue
		}
		summary := cleanEngineerNoticeSummary(compactEngineerNoticeText(engineerNoticeSummaryText(text, sentenceLimit), charLimit))
		if !engineerNoticeHasUsefulDetail(summary) {
			return ""
		}
		return engineerNoticeWithEssentialArtifacts(summary, text, engineerNoticeArtifactLimit)
	}
	return ""
}

func engineerNoticeHasUsefulDetail(text string) bool {
	text = strings.TrimSpace(text)
	return len([]rune(text)) >= 16 && strings.Contains(text, " ")
}

func engineerNoticeSummaryText(text string, sentenceLimit int) string {
	sentenceLimit = max(1, sentenceLimit)
	if engineerNoticeContainsFence(text) {
		for _, paragraph := range engineerNoticeProseParagraphs(text) {
			for _, sentence := range engineerNoticeSentences(paragraph) {
				if engineerNoticeSentenceHasUsefulDetail(sentence) {
					return sentence
				}
			}
		}
		return strings.TrimSpace(strings.ReplaceAll(text, "`", ""))
	}

	var sentences []string
	for _, paragraph := range engineerNoticeProseParagraphs(text) {
		for _, sentence := range engineerNoticeSentences(paragraph) {
			if !engineerNoticeSentenceHasUsefulDetail(sentence) {
				continue
			}
			sentences = append(sentences, sentence)
			if len(sentences) >= sentenceLimit {
				return strings.Join(sentences, " ")
			}
		}
	}
	if len(sentences) > 0 {
		return strings.Join(sentences, " ")
	}
	return strings.TrimSpace(strings.ReplaceAll(text, "`", ""))
}

func engineerNoticeSentenceHasUsefulDetail(text string) bool {
	return engineerNoticeHasUsefulDetail(cleanEngineerNoticeSummary(text))
}

func engineerNoticeWithEssentialArtifacts(summary, text string, limit int) string {
	lines := engineerNoticeArtifactLines(text, limit)
	if len(lines) == 0 {
		return summary
	}
	return summary + "\n\nOutputs:\n" + strings.Join(lines, "\n")
}

func engineerNoticeArtifactLines(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	prose := strings.Join(engineerNoticeProseParagraphs(text), "\n\n")
	targets := codexArtifactOpenTargetsFromMarkdown(prose)
	if len(targets) == 0 {
		return nil
	}
	lines := make([]string, 0, min(limit, len(targets)))
	for _, target := range targets {
		path := strings.TrimSpace(target.Path)
		if path == "" {
			continue
		}
		label := strings.TrimSpace(target.Label)
		if label == "" {
			label = filepath.Base(path)
		}
		if label == "" || label == "." || label == string(filepath.Separator) {
			label = path
		}
		lines = append(lines, "- "+markdownLinkText(label, path))
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func markdownLinkText(label, target string) string {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if label == "" {
		label = target
	}
	if target == "" {
		return label
	}
	return "[" + label + "](" + markdownLinkTarget(target) + ")"
}

func markdownLinkTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.ContainsAny(target, " \t\r\n()") {
		return "<" + target + ">"
	}
	return target
}

func engineerNoticeContainsFence(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "``") {
			return true
		}
	}
	return false
}

func engineerNoticeProseParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	outLines := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "``") {
			if !fenceMarkerClosesOnSameLine(trimmed) {
				inFence = !inFence
			}
			continue
		}
		if inFence {
			continue
		}
		outLines = append(outLines, strings.ReplaceAll(line, "`", ""))
	}
	paragraphs := strings.Split(strings.Join(outLines, "\n"), "\n\n")
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph != "" {
			out = append(out, paragraph)
		}
	}
	return out
}

func fenceMarkerClosesOnSameLine(line string) bool {
	if !(strings.HasPrefix(line, "```") || strings.HasPrefix(line, "``")) {
		return false
	}
	return len(line) > 3 && (strings.HasSuffix(line, "```") || strings.HasSuffix(line, "``"))
}

func engineerNoticeSentences(text string) []string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return nil
	}
	sentences := []string{}
	start := 0
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		end := i + len(string(r))
		if end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			if next != utf8.RuneError && !unicode.IsSpace(next) {
				continue
			}
		}
		sentence := strings.TrimSpace(text[start:end])
		if sentence != "" {
			sentences = append(sentences, sentence)
		}
		start = end
		for start < len(text) && text[start] == ' ' {
			start++
		}
	}
	if len(sentences) == 0 {
		return []string{text}
	}
	return sentences
}

func compactEngineerNoticeText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-1]) + "..."
}

func cleanEngineerNoticeSummary(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	text = strings.TrimRight(text, " \t\r\n:;,")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasSuffix(text, "...") || strings.HasSuffix(text, "…") {
		return text
	}
	last := text[len(text)-1]
	if last == '.' || last == '!' || last == '?' {
		return text
	}
	return text + "."
}

func bossBrowserAttentionNoticeSummary(notify browserAttentionNotification) string {
	projectName := strings.TrimSpace(notify.ProjectName)
	if projectName == "" {
		projectName = strings.TrimSpace(notify.ProjectPath)
	}
	source := notify.Activity.SourceLabel()
	if source == "" {
		source = "browser"
	}
	summary := strings.TrimSpace(notify.Activity.Summary())
	if summary == "" {
		summary = source + " is waiting for user input."
	}
	if projectName != "" {
		summary = projectName + ": " + summary
	}
	if notify.canOpenBrowser() {
		return summary + " The managed browser can be shown for this same engineer session."
	}
	return summary + " Open the engineer session to review it."
}

func bossQuestionNoticeSummary(notify questionNotification) string {
	projectName := strings.TrimSpace(notify.ProjectName)
	if projectName == "" {
		projectName = strings.TrimSpace(notify.ProjectPath)
	}
	summary := strings.TrimSpace(notify.Summary)
	if summary == "" {
		summary = notify.Provider.Label() + " is waiting for user input."
	}
	if projectName != "" {
		return projectName + ": " + summary
	}
	return summary
}

func normalizeBossModel(model tea.Model) bossui.Model {
	switch typed := model.(type) {
	case bossui.Model:
		return typed
	case *bossui.Model:
		if typed == nil {
			panic("boss mode update returned nil *boss.Model")
		}
		return *typed
	default:
		panic(fmt.Sprintf("boss mode update returned unsupported model type %T", model))
	}
}

func bossModeAttentionJumpIndex(msg tea.KeyMsg) (int, bool) {
	if !msg.Alt {
		return 0, false
	}
	key := strings.TrimPrefix(msg.String(), "alt+")
	if len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
		return int(key[0] - '1'), true
	}
	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '8' {
		return int(msg.Runes[0] - '1'), true
	}
	return 0, false
}
