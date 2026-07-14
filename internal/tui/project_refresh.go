package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// Full project scans reconcile global agent artifacts and many git repos. They
// run off the UI thread, so give large local workspaces enough room while
// keeping targeted project refreshes tighter.
const tuiProjectStatusRefreshTimeout = 30 * time.Second
const tuiProjectDetailLoadTimeout = 8 * time.Second
const tuiProjectSummaryLoadTimeout = 8 * time.Second
const tuiProjectsReloadTimeout = 12 * time.Second
const tuiProjectScanTimeout = 90 * time.Second
const tuiOpenAgentTaskLimit = 50
const embeddedSessionActivityRecordTimeout = 30 * time.Second

type projectsMsg struct {
	projects                []model.ProjectSummary
	archivedProjects        []model.ProjectSummary
	categories              []model.ProjectCategory
	openAgentTasks          []model.AgentTask
	orphanedWorktreesByRoot map[string][]model.ProjectSummary
	excludeProjectPatterns  []string
	err                     error
	filterErr               error
}

type detailMsg struct {
	path   string
	detail model.ProjectDetail
	err    error
}

const selectedDetailReloadDebounce = 50 * time.Millisecond

type projectSummaryMsg struct {
	path    string
	summary model.ProjectSummary
	found   bool
	err     error
}

type projectSessionSeenMsg struct {
	path    string
	refresh projectInvalidationIntent
	err     error
}

type projectStatusRefreshedMsg struct {
	projectPath string
	err         error
}

type embeddedSessionActivityRecordedMsg struct {
	key          string
	projectPath  string
	refreshAfter bool
	err          error
}

type scanMsg struct {
	report service.ScanReport
	err    error
}

type embeddedSessionActivityRecordRequest struct {
	activity     service.EmbeddedSessionActivity
	refreshAfter bool
}

type projectInvalidationKind uint8

const (
	projectInvalidationNone projectInvalidationKind = iota
	projectInvalidationProjectData
	projectInvalidationProjectStructure
	projectInvalidationProjectScan
)

type projectInvalidationIntent struct {
	kind                            projectInvalidationKind
	projectPath                     string
	detailPath                      string
	forceRetryFailedClassifications bool
}

type projectRefreshRequest struct {
	scan                            bool
	forceRetryFailedClassifications bool
	projects                        bool
	detailPath                      string
	summaryPaths                    []string
}

func (m *Model) ensureRefreshState() {
	if m.detailReloadInFlight == nil {
		m.detailReloadInFlight = make(map[string]bool)
	}
	if m.detailReloadQueued == nil {
		m.detailReloadQueued = make(map[string]bool)
	}
	if m.detailReloadErrors == nil {
		m.detailReloadErrors = make(map[string]string)
	}
	if m.summaryReloadInFlight == nil {
		m.summaryReloadInFlight = make(map[string]bool)
	}
	if m.summaryReloadQueued == nil {
		m.summaryReloadQueued = make(map[string]bool)
	}
}

func (m *Model) ensureEmbeddedActivityRecordState() {
	if m.embeddedActivityInFlight == nil {
		m.embeddedActivityInFlight = make(map[string]bool)
	}
	if m.embeddedActivityQueued == nil {
		m.embeddedActivityQueued = make(map[string]embeddedSessionActivityRecordRequest)
	}
}

func (m *Model) requestProjectsReloadCmd() tea.Cmd {
	if m.projectsReloadInFlight {
		m.projectsReloadQueued = true
		return nil
	}
	m.projectsReloadInFlight = true
	m.projectsReloadQueued = false
	return m.loadProjectsCmd()
}

func (m *Model) finishProjectsReloadCmd() tea.Cmd {
	m.projectsReloadInFlight = false
	if !m.projectsReloadQueued {
		return nil
	}
	m.projectsReloadQueued = false
	return m.requestProjectsReloadCmd()
}

func (m *Model) requestScanCmd(forceRetryFailedClassifications bool) tea.Cmd {
	if m.scanInFlight {
		m.scanQueued = true
		m.scanQueuedForceRetry = m.scanQueuedForceRetry || forceRetryFailedClassifications
		return nil
	}
	m.scanInFlight = true
	m.scanQueued = false
	m.scanQueuedForceRetry = false
	return m.scanCmd(forceRetryFailedClassifications)
}

func (m *Model) finishScanCmd() tea.Cmd {
	m.scanInFlight = false
	if !m.scanQueued {
		return nil
	}
	forceRetry := m.scanQueuedForceRetry
	m.scanQueued = false
	m.scanQueuedForceRetry = false
	return m.requestScanCmd(forceRetry)
}

func (m Model) scanCmd(forceRetryFailedClassifications bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectScanTimeout)
		defer cancel()
		report, err := m.svc.ScanWithOptions(ctx, service.ScanOptions{
			ForceRetryFailedClassifications: forceRetryFailedClassifications,
		})
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", tuiProjectScanTimeout.Round(time.Millisecond), err)
		}
		return scanMsg{report: report, err: err}
	}
}

func (m *Model) requestDetailReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(path) {
		return nil
	}
	m.ensureRefreshState()
	delete(m.detailReloadErrors, path)
	if m.detailReloadInFlight[path] {
		m.detailReloadQueued[path] = true
		return nil
	}
	m.detailReloadInFlight[path] = true
	delete(m.detailReloadQueued, path)
	return m.loadDetailCmd(path)
}

func (m *Model) finishDetailReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	delete(m.detailReloadInFlight, path)
	if !m.detailReloadQueued[path] {
		delete(m.detailReloadQueued, path)
		return nil
	}
	delete(m.detailReloadQueued, path)
	return m.requestDetailReloadCmd(path)
}

func (m *Model) requestProjectSummaryReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(path) {
		return nil
	}
	m.ensureRefreshState()
	if m.summaryReloadInFlight[path] {
		m.summaryReloadQueued[path] = true
		return nil
	}
	m.summaryReloadInFlight[path] = true
	delete(m.summaryReloadQueued, path)
	return m.loadProjectSummaryCmd(path)
}

func (m *Model) finishProjectSummaryReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
	delete(m.summaryReloadInFlight, path)
	if !m.summaryReloadQueued[path] {
		delete(m.summaryReloadQueued, path)
		return nil
	}
	delete(m.summaryReloadQueued, path)
	return m.requestProjectSummaryReloadCmd(path)
}

func (m Model) refreshProjectStatusCmd(path string) tea.Cmd {
	return m.refreshProjectStatusCmdWithOptions(path, service.ScanOptions{})
}

func (m Model) refreshProjectStatusCmdWithOptions(path string, opts service.ScanOptions) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(path) || m.svc == nil {
		return nil
	}
	opts.SkipLinkedWorktreeStatusRefresh = true
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		refreshCtx, cancel := context.WithTimeout(ctx, tuiProjectStatusRefreshTimeout)
		defer cancel()
		err := m.svc.RefreshProjectStatusWithOptions(refreshCtx, path, opts)
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s", tuiProjectStatusRefreshTimeout.Round(time.Millisecond))
		}
		return projectStatusRefreshedMsg{projectPath: path, err: err}
	}
}

// recordEmbeddedSessionTransitionCmd persists a meaningful live-session state
// transition. Streaming activity itself remains in codexapp.Manager's in-memory
// snapshots and must not turn into periodic database work.
func (m *Model) recordEmbeddedSessionTransitionCmd(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	activity, ok := embeddedSessionActivityFromSnapshot(projectPath, snapshot)
	if !ok {
		return nil
	}
	return m.requestEmbeddedSessionActivityRecordCmd(activity, false)
}

func (m Model) recordEmbeddedSessionSettledCmd(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	activity, ok := embeddedSessionSettledActivityFromSnapshot(projectPath, snapshot)
	if !ok {
		return nil
	}
	return m.recordEmbeddedSessionStateCmd(activity)
}

func (m *Model) requestEmbeddedSessionActivityRecordCmd(activity service.EmbeddedSessionActivity, refreshAfter bool) tea.Cmd {
	if m.isAgentTaskProjectPath(activity.ProjectPath) {
		return nil
	}
	key := embeddedSessionActivityRecordKey(activity)
	if key == "" {
		return nil
	}
	m.ensureEmbeddedActivityRecordState()
	req := embeddedSessionActivityRecordRequest{activity: activity, refreshAfter: refreshAfter}
	if m.embeddedActivityInFlight[key] {
		m.embeddedActivityQueued[key] = mergeEmbeddedSessionActivityRecordRequest(m.embeddedActivityQueued[key], req)
		return nil
	}
	m.embeddedActivityInFlight[key] = true
	delete(m.embeddedActivityQueued, key)
	return m.recordEmbeddedSessionActivityRecordCmd(key, req)
}

func (m Model) recordEmbeddedSessionActivityRecordCmd(key string, req embeddedSessionActivityRecordRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(embeddedSessionActivityRecordTimeout)
		defer cancel()
		err := m.svc.RecordEmbeddedSessionActivity(ctx, req.activity)
		err = timeoutActionError(err, embeddedSessionActivityRecordTimeout, "recording embedded session activity")
		return embeddedSessionActivityRecordedMsg{
			key:          key,
			projectPath:  req.activity.ProjectPath,
			refreshAfter: req.refreshAfter,
			err:          err,
		}
	}
}

func (m *Model) finishEmbeddedSessionActivityRecordCmd(msg embeddedSessionActivityRecordedMsg) tea.Cmd {
	if msg.key == "" {
		return nil
	}
	m.ensureEmbeddedActivityRecordState()
	delete(m.embeddedActivityInFlight, msg.key)
	if queued, ok := m.embeddedActivityQueued[msg.key]; ok {
		delete(m.embeddedActivityQueued, msg.key)
		return m.requestEmbeddedSessionActivityRecordCmd(queued.activity, queued.refreshAfter)
	}
	if msg.err == nil && msg.refreshAfter {
		return m.refreshProjectStatusCmd(msg.projectPath)
	}
	return nil
}

func (m Model) recordEmbeddedSessionStateCmd(activity service.EmbeddedSessionActivity) tea.Cmd {
	if m.isAgentTaskProjectPath(activity.ProjectPath) {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(embeddedSessionActivityRecordTimeout)
		defer cancel()
		err := m.svc.RecordEmbeddedSessionActivity(ctx, activity)
		err = timeoutActionError(err, embeddedSessionActivityRecordTimeout, "recording embedded session activity")
		return projectStatusRefreshedMsg{projectPath: activity.ProjectPath, err: err}
	}
}

func (m Model) recordEmbeddedSessionSettledAndRefreshCmd(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(projectPath) {
		return nil
	}
	activity, ok := embeddedSessionSettledActivityFromSnapshot(projectPath, snapshot)
	refreshCmd := m.refreshProjectStatusCmd(projectPath)
	if !ok {
		return refreshCmd
	}
	return func() tea.Msg {
		var errs []error
		ctx, cancel := m.actionContext(embeddedSessionActivityRecordTimeout)
		if err := m.svc.RecordEmbeddedSessionActivity(ctx, activity); err != nil {
			err = timeoutActionError(err, embeddedSessionActivityRecordTimeout, "recording embedded session activity")
			errs = append(errs, err)
		}
		cancel()
		if refreshCmd != nil {
			msg := refreshCmd()
			if refreshMsg, ok := msg.(projectStatusRefreshedMsg); ok {
				if refreshMsg.err != nil {
					errs = append(errs, refreshMsg.err)
				}
			} else if msg != nil {
				errs = append(errs, fmt.Errorf("refresh project status returned %T", msg))
			}
		}
		return projectStatusRefreshedMsg{projectPath: activity.ProjectPath, err: errors.Join(errs...)}
	}
}

func embeddedSessionActivityRecordKey(activity service.EmbeddedSessionActivity) string {
	projectPath := normalizeProjectPath(activity.ProjectPath)
	source := model.NormalizeSessionSource(activity.Source)
	sessionID := strings.TrimSpace(activity.SessionID)
	if projectPath == "" || sessionID == "" {
		return ""
	}
	if format := strings.TrimSpace(activity.Format); format != "" {
		normalizedSource, normalizedSessionID, _ := model.NormalizeSessionIdentity(source, format, sessionID, "")
		source = normalizedSource
		if normalizedSessionID != "" {
			sessionID = normalizedSessionID
		}
	}
	return projectPath + "\x00" + string(source) + "\x00" + sessionID
}

func mergeEmbeddedSessionActivityRecordRequest(existing, next embeddedSessionActivityRecordRequest) embeddedSessionActivityRecordRequest {
	if strings.TrimSpace(existing.activity.ProjectPath) == "" {
		return next
	}
	merged := existing
	nextAt := next.activity.LastActivityAt
	existingAt := existing.activity.LastActivityAt
	if nextAt.After(existingAt) || (nextAt.Equal(existingAt) && next.activity.LatestTurnCompleted && !existing.activity.LatestTurnCompleted) {
		merged.activity = next.activity
	}
	merged.refreshAfter = existing.refreshAfter || next.refreshAfter
	return merged
}

func (m *Model) requestProjectRefreshCmd(req projectRefreshRequest) tea.Cmd {
	cmds := make([]tea.Cmd, 0, 2+len(req.summaryPaths))
	if req.scan {
		cmds = append(cmds, m.requestScanCmd(req.forceRetryFailedClassifications))
	}
	if req.projects {
		cmds = append(cmds, m.requestProjectsReloadCmd())
	}
	seen := make(map[string]struct{}, len(req.summaryPaths))
	for _, path := range req.summaryPaths {
		path = normalizeProjectPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		cmds = append(cmds, m.requestProjectSummaryReloadCmd(path))
	}
	cmds = append(cmds, m.requestDetailReloadCmd(req.detailPath))
	return batchCmds(cmds...)
}

func invalidateProjectData(projectPath string) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:        projectInvalidationProjectData,
		projectPath: projectPath,
	}
}

func invalidateProjectStructure(detailPath string) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:       projectInvalidationProjectStructure,
		detailPath: detailPath,
	}
}

func invalidateProjectScan(detailPath string, forceRetryFailedClassifications bool) projectInvalidationIntent {
	return projectInvalidationIntent{
		kind:                            projectInvalidationProjectScan,
		detailPath:                      detailPath,
		forceRetryFailedClassifications: forceRetryFailedClassifications,
	}
}

func (m Model) visibleDetailPathForProject(path string) string {
	path = normalizeProjectPath(path)
	if path == "" {
		return ""
	}
	if m.currentDetailTargetPath() != path {
		return ""
	}
	return path
}

func (m *Model) requestProjectInvalidationCmd(intent projectInvalidationIntent) tea.Cmd {
	switch intent.kind {
	case projectInvalidationProjectData:
		path := normalizeProjectPath(intent.projectPath)
		if path == "" {
			return nil
		}
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			detailPath:   m.visibleDetailPathForProject(path),
			summaryPaths: []string{path},
		})
	case projectInvalidationProjectStructure:
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			projects:   true,
			detailPath: normalizeProjectPath(intent.detailPath),
		})
	case projectInvalidationProjectScan:
		return m.requestProjectRefreshCmd(projectRefreshRequest{
			scan:                            true,
			forceRetryFailedClassifications: intent.forceRetryFailedClassifications,
			projects:                        true,
			detailPath:                      normalizeProjectPath(intent.detailPath),
		})
	default:
		return nil
	}
}

func actionChangesProjectStructure(action string) bool {
	switch strings.TrimSpace(action) {
	case "archive_project", "unarchive_project", "forget_project", "remove_worktree", "scratch_task_archived", "scratch_task_deleted", "scratch_task_renamed", "project_category_created", "project_category_deleted", "project_category_changed", "agent_task_category_changed":
		return true
	default:
		return false
	}
}

func structureActionDetailPath(event events.Event, currentSelectedPath string) string {
	currentSelectedPath = normalizeProjectPath(currentSelectedPath)
	switch strings.TrimSpace(event.Payload["action"]) {
	case "remove_worktree":
		if rootPath := normalizeProjectPath(event.Payload["root_path"]); rootPath != "" {
			return rootPath
		}
		if currentSelectedPath != "" && currentSelectedPath == normalizeProjectPath(event.ProjectPath) {
			return ""
		}
	}
	return currentSelectedPath
}

func (m *Model) requestProjectDetailViewCmd(path string) tea.Cmd {
	if _, ok := m.todoPendingLaunchForProjectPath(path); ok {
		return nil
	}
	if _, ok := m.agentTaskForProjectPath(path); ok {
		return nil
	}
	return m.requestDetailReloadCmd(normalizeProjectPath(path))
}

func (m *Model) requestSelectedProjectDetailViewCmd() tea.Cmd {
	path := m.currentSelectedProjectPath()
	if path == "" || m.isAgentTaskProjectPath(path) {
		return nil
	}
	m.selectedDetailRequestSeq++
	seq := m.selectedDetailRequestSeq
	return tea.Tick(selectedDetailReloadDebounce, func(time.Time) tea.Msg {
		return selectedDetailReloadMsg{path: path, seq: seq}
	})
}

func (m Model) waitBusCmd() tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-m.busCh
		if !ok {
			return nil
		}
		return busMsg(evt)
	}
}

func (m Model) loadProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectsReloadTimeout)
		defer cancel()
		allProjects, err := m.svc.Store().ListProjects(ctx, true)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timed out after %s", tuiProjectsReloadTimeout.Round(time.Millisecond))
			}
			return projectsMsg{err: err}
		}
		projects, archivedProjects := splitProjectArchiveSummaries(allProjects)
		categories, err := m.svc.ListProjectCategories(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timed out after %s", tuiProjectsReloadTimeout.Round(time.Millisecond))
			}
			return projectsMsg{err: err}
		}
		orphanedWorktrees, err := m.svc.Store().GetOrphanedWorktreeSummaryMap(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timed out after %s", tuiProjectsReloadTimeout.Round(time.Millisecond))
			}
			return projectsMsg{err: err}
		}
		openAgentTasks, agentTaskErr := m.svc.ListOpenAgentTasks(ctx, tuiOpenAgentTaskLimit)
		if errors.Is(agentTaskErr, context.DeadlineExceeded) {
			agentTaskErr = fmt.Errorf("timed out after %s", tuiProjectsReloadTimeout.Round(time.Millisecond))
		}
		patterns, filterErr := config.LoadExcludeProjectPatterns(m.currentConfigPath(), m.excludeProjectPatterns)
		if filterErr == nil && agentTaskErr != nil {
			filterErr = fmt.Errorf("agent tasks unavailable: %w", agentTaskErr)
		}
		return projectsMsg{
			projects:                projects,
			archivedProjects:        archivedProjects,
			categories:              categories,
			openAgentTasks:          openAgentTasks,
			orphanedWorktreesByRoot: buildOrphanedWorktreeMap(orphanedWorktrees),
			excludeProjectPatterns:  patterns,
			filterErr:               filterErr,
		}
	}
}

func splitProjectArchiveSummaries(projects []model.ProjectSummary) ([]model.ProjectSummary, []model.ProjectSummary) {
	if len(projects) == 0 {
		return nil, nil
	}
	active := make([]model.ProjectSummary, 0, len(projects))
	archived := make([]model.ProjectSummary, 0)
	for _, project := range projects {
		if projectSummaryArchived(project) {
			archived = append(archived, project)
		} else if projectSummaryActive(project) {
			active = append(active, project)
		}
	}
	return active, archived
}

func projectSummaryActive(project model.ProjectSummary) bool {
	return (project.InScope || project.ManuallyAdded) && !project.Archived
}

func projectSummaryArchived(project model.ProjectSummary) bool {
	return project.Archived
}

func (m Model) loadDetailCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		detailCtx, cancel := context.WithTimeout(ctx, tuiProjectDetailLoadTimeout)
		defer cancel()
		d, err := m.svc.Store().GetProjectDetail(detailCtx, path, 20)
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s", tuiProjectDetailLoadTimeout.Round(time.Millisecond))
		}
		return detailMsg{path: path, detail: d, err: err}
	}
}

func (m *Model) rememberDetailReloadResult(path string, err error) {
	path = normalizeProjectPath(path)
	if path == "" {
		return
	}
	m.ensureRefreshState()
	if err == nil {
		delete(m.detailReloadErrors, path)
		return
	}
	m.detailReloadErrors[path] = strings.TrimSpace(err.Error())
	if m.detailReloadErrors[path] == "" {
		m.detailReloadErrors[path] = "unknown error"
	}
}

func (m Model) detailReloadError(path string) string {
	path = normalizeProjectPath(path)
	if path == "" || len(m.detailReloadErrors) == 0 {
		return ""
	}
	return strings.TrimSpace(m.detailReloadErrors[path])
}

func (m Model) loadProjectSummaryCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectSummaryLoadTimeout)
		defer cancel()
		summary, err := m.svc.Store().GetProjectSummary(ctx, path, true)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return projectSummaryMsg{path: path}
			}
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timed out after %s", tuiProjectSummaryLoadTimeout.Round(time.Millisecond))
			}
			return projectSummaryMsg{path: path, err: err}
		}
		return projectSummaryMsg{path: path, summary: summary, found: true}
	}
}
