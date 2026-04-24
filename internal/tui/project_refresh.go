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

// Interactive project refreshes also repair session snapshot hashes and queue
// classifier work after embedded turns, so they need a bit more budget than
// raw git metadata reads alone.
const tuiProjectStatusRefreshTimeout = 30 * time.Second

type projectsMsg struct {
	projects                []model.ProjectSummary
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

type scanMsg struct {
	report service.ScanReport
	err    error
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
	if m.summaryReloadInFlight == nil {
		m.summaryReloadInFlight = make(map[string]bool)
	}
	if m.summaryReloadQueued == nil {
		m.summaryReloadQueued = make(map[string]bool)
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
		report, err := m.svc.ScanWithOptions(m.ctx, service.ScanOptions{
			ForceRetryFailedClassifications: forceRetryFailedClassifications,
		})
		return scanMsg{report: report, err: err}
	}
}

func (m *Model) requestDetailReloadCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
	m.ensureRefreshState()
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
	if m.svc == nil {
		return nil
	}
	path = normalizeProjectPath(path)
	if path == "" {
		return nil
	}
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

func (m Model) recordEmbeddedSessionActivityCmd(projectPath string, snapshot codexapp.Snapshot) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	activity, ok := embeddedSessionActivityFromSnapshot(projectPath, snapshot)
	if !ok {
		return nil
	}
	return m.recordEmbeddedSessionStateCmd(activity)
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

func (m Model) recordEmbeddedSessionStateCmd(activity service.EmbeddedSessionActivity) tea.Cmd {
	return func() tea.Msg {
		err := m.svc.RecordEmbeddedSessionActivity(m.ctx, activity)
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
	activity, ok := embeddedSessionSettledActivityFromSnapshot(projectPath, snapshot)
	refreshCmd := m.refreshProjectStatusCmd(projectPath)
	if !ok {
		return refreshCmd
	}
	return func() tea.Msg {
		var errs []error
		if err := m.svc.RecordEmbeddedSessionActivity(m.ctx, activity); err != nil {
			errs = append(errs, err)
		}
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
	case "forget_project", "remove_worktree", "scratch_task_archived", "scratch_task_deleted":
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
	return m.requestDetailReloadCmd(normalizeProjectPath(path))
}

func (m *Model) requestSelectedProjectDetailViewCmd() tea.Cmd {
	return m.requestProjectDetailViewCmd(m.currentSelectedProjectPath())
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
		projects, err := m.svc.Store().ListProjects(m.ctx, false)
		if err != nil {
			return projectsMsg{err: err}
		}
		summaries, err := m.svc.Store().GetProjectSummaryMap(m.ctx)
		if err != nil {
			return projectsMsg{err: err}
		}
		patterns, filterErr := config.LoadExcludeProjectPatterns(m.currentConfigPath(), m.excludeProjectPatterns)
		return projectsMsg{
			projects:                projects,
			orphanedWorktreesByRoot: buildOrphanedWorktreeMap(summaries),
			excludeProjectPatterns:  patterns,
			filterErr:               filterErr,
		}
	}
}

func (m Model) loadDetailCmd(path string) tea.Cmd {
	path = normalizeProjectPath(path)
	return func() tea.Msg {
		d, err := m.svc.Store().GetProjectDetail(m.ctx, path, 20)
		return detailMsg{path: path, detail: d, err: err}
	}
}

func (m Model) loadProjectSummaryCmd(path string) tea.Cmd {
	return func() tea.Msg {
		summary, err := m.svc.Store().GetProjectSummary(m.ctx, path, false)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return projectSummaryMsg{path: path}
			}
			return projectSummaryMsg{path: path, err: err}
		}
		return projectSummaryMsg{path: path, summary: summary, found: true}
	}
}
