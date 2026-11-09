package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/model"
)

type OrphanedWorktreeResolution string

const (
	OrphanedWorktreeResolutionNone                  OrphanedWorktreeResolution = ""
	OrphanedWorktreeResolutionClearResidue          OrphanedWorktreeResolution = "clear_residue"
	OrphanedWorktreeResolutionRemoveRegistered      OrphanedWorktreeResolution = "remove_registered_worktree"
	OrphanedWorktreeResolutionDeleteArchivedTaskNow OrphanedWorktreeResolution = "delete_archived_task_now"
)

type OrphanedWorktreeInspection struct {
	Registration      string
	RegistrationError string
	HaveCurrentStatus bool
	RetainedBytes     int64
	SizeError         string
	NestedWorktrees   []ownedRemovalChild
	ProjectPath       string
	RootPath          string
	BranchName        string
	TargetBranch      string
	MergeStatus       model.WorktreeMergeStatus
	Dirty             bool
	StatusError       string
	CleanupKind       ResidualWorktreeCleanupKind
	Resolution        OrphanedWorktreeResolution
	Reason            string
	AgentTask         model.AgentTask
	InspectedAt       time.Time
}

// InspectOrphanedWorktree inspects task ownership, checkout health and retained
// files, then determines which guarded action, if any, can resolve the workspace.
// It does not mutate Git, files, task records, or project state.
func (s *Service) InspectOrphanedWorktree(ctx context.Context, projectPath string) (OrphanedWorktreeInspection, error) {
	if s == nil || s.store == nil {
		return OrphanedWorktreeInspection{}, fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return OrphanedWorktreeInspection{}, fmt.Errorf("project path is required")
	}

	owners, err := s.agentTasksForWorkspace(ctx, projectPath)
	if err != nil {
		return OrphanedWorktreeInspection{}, fmt.Errorf("inspect task ownership: %w", err)
	}
	summary, err := s.store.GetTrackedProjectSummary(ctx, projectPath)
	if err != nil && (!errors.Is(err, sql.ErrNoRows) || len(owners) == 0) {
		return OrphanedWorktreeInspection{}, fmt.Errorf("load worktree record: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) && len(owners) == 1 {
		task := owners[0]
		summary.WorktreeRootPath = firstNonEmpty(task.OriginProjectPath, task.OriginWorktreePath)
		if task.Repository.Isolated && task.Repository.InitialBranch != "" {
			summary.WorktreeKind = model.WorktreeKindLinked
		}
		summary.Forgotten = true
	}
	rootPath := filepath.Clean(strings.TrimSpace(summary.WorktreeRootPath))
	if rootPath == "." {
		rootPath = ""
	}
	inspection := OrphanedWorktreeInspection{
		ProjectPath:  projectPath,
		RootPath:     rootPath,
		BranchName:   strings.TrimSpace(summary.RepoBranch),
		TargetBranch: strings.TrimSpace(summary.WorktreeParentBranch),
		MergeStatus:  summary.WorktreeMergeStatus,
		Dirty:        summary.RepoDirty,
		InspectedAt:  time.Now(),
	}
	inspection.RetainedBytes, err = retainedDirectoryBytes(ctx, projectPath)
	if err != nil {
		inspection.SizeError = err.Error()
	}
	registration := linkedWorktreeRegistrationUnknown
	var registrationErr error
	if rootPath != "" && summary.WorktreeKind == model.WorktreeKindLinked {
		registration, registrationErr = linkedWorktreeRegistrationWithReader(ctx, rootPath, summary.WorktreeKind, projectPath, s.gitWorktreeListReader)
		if registrationErr != nil {
			inspection.RegistrationError = registrationErr.Error()
		} else {
			switch registration {
			case linkedWorktreeRegistrationLive:
				inspection.Registration = "Registered Git worktree"
			case linkedWorktreeRegistrationAbsent:
				inspection.Registration = "Not registered in Git"
			case linkedWorktreeRegistrationPrunable:
				inspection.Registration = "Stale Git registration"
			}
		}
	}
	if len(owners) > 1 {
		inspection.Reason = fmt.Sprintf("This workspace is referenced by %d task records. Little Control Room will not remove it until that ownership is reconciled.", len(owners))
		return inspection, nil
	}
	if len(owners) == 1 {
		inspection.AgentTask = s.refreshAgentTaskWorkspace(ctx, owners[0])
		workspace := inspection.AgentTask.WorkspaceGit
		inspection.HaveCurrentStatus = workspace.State == model.AgentTaskWorkspaceHealthy
		inspection.BranchName, inspection.Dirty = workspace.Branch, workspace.Dirty
		inspection.StatusError = workspace.Error
		if model.NormalizeAgentTaskStatus(owners[0].Status) != model.AgentTaskStatusArchived {
			inspection.Reason = fmt.Sprintf("This workspace still belongs to the %s task %q, so checkout issues are reported on that task. No removal action is offered here.", owners[0].Status, agentTaskInspectionTitle(owners[0]))
			return inspection, nil
		}
		managedRoots := []string{appfs.InternalWorkspaceRoot(s.cfg.DataDir)}
		if !appfs.IsManagedInternalPath(projectPath, managedRoots) {
			inspection.Reason = "The archived task points outside Little Control Room's managed workspace directory, so automatic deletion is disabled."
			return inspection, nil
		}
		if owners[0].Repository.State == "held" {
			inspection.Reason = "Trash cleanup is blocked because this task still holds write ownership."
			return inspection, nil
		}
		inspection.Resolution = OrphanedWorktreeResolutionDeleteArchivedTaskNow
		inspection.Reason = fmt.Sprintf("This checkout belongs to the task %q in Trash and is being retained intentionally.", agentTaskInspectionTitle(owners[0]))
		return inspection, nil
	}

	if summary.WorktreeKind != model.WorktreeKindLinked || rootPath == "" {
		inspection.Reason = "The saved project record no longer identifies this path as a linked worktree."
		return inspection, nil
	}
	if !summary.Forgotten {
		inspection.Reason = "This worktree is tracked again and is no longer unclaimed. Refresh the project list before taking action."
		return inspection, nil
	}

	if registrationErr != nil {
		inspection.Reason = "Git registration inspection failed; no removal action is available."
		return inspection, nil
	}

	switch registration {
	case linkedWorktreeRegistrationLive:
		s.readOrphanedWorktreeStatus(ctx, &inspection)
		inspection.Resolution = OrphanedWorktreeResolutionRemoveRegistered
		inspection.Reason = "Git still registers this as a live linked worktree. It can be removed as a checkout; its branch will remain in the repository."
		return inspection, nil
	case linkedWorktreeRegistrationAbsent, linkedWorktreeRegistrationPrunable:
		residual, inspectErr := s.inspectResidualWorktreeDirectory(ctx, rootPath, projectPath, summary, "")
		if inspectErr != nil {
			return OrphanedWorktreeInspection{}, fmt.Errorf("inspect remaining worktree files: %w", inspectErr)
		}
		inspection.CleanupKind = residual.Kind
		inspection.NestedWorktrees = residual.Plan.Children
		if residual.Safe {
			inspection.Resolution = OrphanedWorktreeResolutionClearResidue
			inspection.Reason = "Git no longer has a live checkout at this path, and the remaining files passed the strict residue verification."
			if residual.Kind == ResidualWorktreeCleanupOwned {
				inspection.Reason = fmt.Sprintf("Retained folder: %s; %d owned clean nested worktrees; %d verified ignored clones. Clear residue permanently deletes verified tracked files and ignored output, including dependency clones whose Git objects are recoverable from upstream. Local work and unverifiable repositories block cleanup. Nested commits and clone refs are recorded before deletion; parent branches and shared submodule repositories are preserved.", retainedSizeLabel(inspection.RetainedBytes), len(residual.Plan.Children), len(residual.Plan.Clones))
			}
			return inspection, nil
		}
		inspection.Reason = strings.TrimSpace(residual.Reason)
		if inspection.Reason == "" {
			inspection.Reason = "The remaining folder could not be verified for automatic cleanup."
		}
		return inspection, nil
	default:
		inspection.Reason = "Little Control Room could not determine whether Git still owns this checkout. The folder was left untouched."
		return inspection, nil
	}
}

func (s *Service) agentTasksForWorkspace(ctx context.Context, workspacePath string) ([]model.AgentTask, error) {
	tasks, err := s.store.ListAgentTasks(ctx, model.AgentTaskFilter{IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	owners := make([]model.AgentTask, 0, 1)
	for _, task := range tasks {
		if samePath(task.WorkspacePath, workspacePath) {
			owners = append(owners, task)
		}
	}
	return owners, nil
}

func (s *Service) readOrphanedWorktreeStatus(ctx context.Context, inspection *OrphanedWorktreeInspection) {
	if inspection == nil || s.gitRepoStatusReader == nil {
		return
	}
	status, err := s.gitRepoStatusReader(ctx, inspection.ProjectPath)
	if err != nil {
		inspection.StatusError = err.Error()
		return
	}
	inspection.HaveCurrentStatus = true
	inspection.Dirty = status.Dirty
	if branch := strings.TrimSpace(status.Branch); branch != "" {
		inspection.BranchName = branch
	}
}

func agentTaskInspectionTitle(task model.AgentTask) string {
	if title := strings.TrimSpace(task.Title); title != "" {
		return title
	}
	if id := strings.TrimSpace(task.ID); id != "" {
		return id
	}
	return "agent task"
}

// DeleteArchivedAgentTaskNow permanently removes an archived task and its
// managed workspace. Linked Git worktrees go through the normal guarded
// removal path so the repository registration is cleaned up as well.
func (s *Service) DeleteArchivedAgentTaskNow(ctx context.Context, taskID, expectedWorkspacePath string, force bool) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("agent task id is required")
	}
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return err
	}
	if model.NormalizeAgentTaskStatus(task.Status) != model.AgentTaskStatusArchived {
		return fmt.Errorf("agent task %s is no longer in Trash", taskID)
	}
	if task.Repository.State == "held" {
		return fmt.Errorf("task still holds write ownership; its workspace was kept")
	}

	workspacePath := filepath.Clean(strings.TrimSpace(task.WorkspacePath))
	expectedWorkspacePath = filepath.Clean(strings.TrimSpace(expectedWorkspacePath))
	if expectedWorkspacePath == "" || expectedWorkspacePath == "." {
		return fmt.Errorf("expected archived task workspace path is required")
	}
	if !samePath(workspacePath, expectedWorkspacePath) {
		return fmt.Errorf("agent task %s now points to a different workspace; nothing was deleted", taskID)
	}
	if workspacePath != "" && workspacePath != "." {
		managedRoots := []string{appfs.InternalWorkspaceRoot(s.cfg.DataDir)}
		if !appfs.IsManagedInternalPath(workspacePath, managedRoots) {
			return fmt.Errorf("refusing to delete task workspace outside Little Control Room's managed directory: %s", workspacePath)
		}
		summary, summaryErr := s.store.GetTrackedProjectSummary(ctx, workspacePath)
		switch {
		case summaryErr == nil && summary.WorktreeKind == model.WorktreeKindLinked:
			if err := s.RemoveWorktree(ctx, workspacePath, force); err != nil {
				return fmt.Errorf("remove archived task worktree: %w", err)
			}
		case summaryErr == nil:
			fallthrough
		case errors.Is(summaryErr, sql.ErrNoRows):
			current := s.refreshAgentTaskWorkspace(ctx, task)
			if current.WorkspaceGit.Dirty && !force {
				return fmt.Errorf("workspace has uncommitted changes; confirm force removal to discard them")
			}
			if err := s.removeAgentTaskWorkspace(ctx, workspacePath, firstNonEmpty(task.OriginProjectPath, task.OriginWorktreePath)); err != nil {
				return fmt.Errorf("remove archived task workspace: %w", err)
			}
		default:
			return fmt.Errorf("inspect archived task workspace record: %w", summaryErr)
		}
	}

	current, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return err
	}
	if model.NormalizeAgentTaskStatus(current.Status) != model.AgentTaskStatusArchived || !samePath(current.WorkspacePath, task.WorkspacePath) {
		return fmt.Errorf("agent task %s changed while its archived workspace was being removed; its task record was kept", taskID)
	}
	if err := s.store.DeleteAgentTask(ctx, taskID); err != nil {
		return fmt.Errorf("delete archived agent task: %w", err)
	}
	s.agentTaskCleanupErrors.Delete(taskID)
	return nil
}
