package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/attention"
	"lcroom/internal/events"
	"lcroom/internal/model"
)

const recentProjectParentPathLimit = 3

type CreateOrAttachProjectRequest struct {
	ParentPath    string
	Name          string
	CreateGitRepo bool
}

type CreateOrAttachProjectAction string

const (
	CreateOrAttachProjectCreated      CreateOrAttachProjectAction = "created"
	CreateOrAttachProjectAdded        CreateOrAttachProjectAction = "added_existing"
	CreateOrAttachProjectAlreadyKnown CreateOrAttachProjectAction = "already_tracked"
)

type CreateOrAttachProjectResult struct {
	Action              CreateOrAttachProjectAction
	ProjectPath         string
	ProjectName         string
	ParentPath          string
	GitRepoCreated      bool
	NameDerivedFromPath bool
	RecentParentPaths   []string
}

type normalizedCreateOrAttachProjectRequest struct {
	ParentPath          string
	ProjectName         string
	ProjectPath         string
	NameDerivedFromPath bool
}

func (s *Service) RecentProjectParentPaths(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.ListRecentProjectParentPaths(ctx, limit)
}

func (s *Service) CreateOrAttachProject(ctx context.Context, req CreateOrAttachProjectRequest) (CreateOrAttachProjectResult, error) {
	normalized, err := normalizeCreateOrAttachProjectRequest(req)
	if err != nil {
		return CreateOrAttachProjectResult{}, err
	}
	parentPath := normalized.ParentPath
	projectName := normalized.ProjectName
	projectPath := normalized.ProjectPath

	info, statErr := os.Stat(projectPath)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return CreateOrAttachProjectResult{}, fmt.Errorf("check project path: %w", statErr)
	}
	if exists && !info.IsDir() {
		return CreateOrAttachProjectResult{}, fmt.Errorf("path already exists and is not a directory: %s", projectPath)
	}

	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return CreateOrAttachProjectResult{}, fmt.Errorf("load projects: %w", err)
	}
	existing := projects[projectPath]

	result := CreateOrAttachProjectResult{
		ProjectPath:         projectPath,
		ProjectName:         projectName,
		ParentPath:          parentPath,
		NameDerivedFromPath: normalized.NameDerivedFromPath,
	}

	switch {
	case existing.Path != "" && !existing.Forgotten:
		result.Action = CreateOrAttachProjectAlreadyKnown
	case exists:
		result.Action = CreateOrAttachProjectAdded
	default:
		if err := os.MkdirAll(projectPath, 0o755); err != nil {
			return CreateOrAttachProjectResult{}, fmt.Errorf("create project directory: %w", err)
		}
		if req.CreateGitRepo && s.gitRepoInitializer != nil {
			if err := s.gitRepoInitializer(ctx, projectPath); err != nil {
				return CreateOrAttachProjectResult{}, fmt.Errorf("initialize git repo: %w", err)
			}
			result.GitRepoCreated = true
		}
		result.Action = CreateOrAttachProjectCreated
	}

	if err := s.trackProjectPath(ctx, existing, projectPath); err != nil {
		return CreateOrAttachProjectResult{}, err
	}
	if err := s.store.RememberRecentProjectParentPath(ctx, parentPath, recentProjectParentPathLimit); err != nil {
		return CreateOrAttachProjectResult{}, fmt.Errorf("remember recent parent path: %w", err)
	}
	recent, err := s.store.ListRecentProjectParentPaths(ctx, recentProjectParentPathLimit)
	if err != nil {
		return CreateOrAttachProjectResult{}, fmt.Errorf("load recent parent paths: %w", err)
	}
	result.RecentParentPaths = recent

	now := time.Now()
	actionPayload := map[string]string{
		"action": string(result.Action),
	}
	if result.GitRepoCreated {
		actionPayload["git_repo_created"] = "true"
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: projectPath,
			Payload:     actionPayload,
		})
	}
	payload := string(result.Action)
	if result.GitRepoCreated {
		payload += " git_repo_created=true"
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     payload,
	})

	return result, nil
}

func (s *Service) trackProjectPath(ctx context.Context, existing model.ProjectSummary, projectPath string) error {
	if existing.Path != "" {
		if existing.Forgotten {
			if err := s.store.SetForgotten(ctx, projectPath, false); err != nil {
				return fmt.Errorf("restore forgotten project: %w", err)
			}
			if err := s.RefreshProjectStatus(ctx, projectPath); err != nil {
				return err
			}
		}
		return nil
	}
	return s.upsertManualProjectState(ctx, existing, projectPath)
}

func (s *Service) upsertManualProjectState(ctx context.Context, existing model.ProjectSummary, projectPath string) error {
	now := time.Now()
	presentOnDisk := projectPathExists(projectPath)
	worktreeRootPath := ""
	worktreeKind := model.WorktreeKindNone
	worktreeParentBranch := strings.TrimSpace(existing.WorktreeParentBranch)
	worktreeMergeStatus := existing.WorktreeMergeStatus
	repoBranch := ""
	repoDirty := false
	repoConflict := false
	repoSyncStatus := model.RepoSyncStatus("")
	repoAheadCount := 0
	repoBehindCount := 0
	if presentOnDisk && s.gitRepoStatusReader != nil {
		worktreeRootPath, worktreeKind = s.readProjectWorktreeInfo(ctx, projectPath)
		if repoStatus, err := s.gitRepoStatusReader(ctx, projectPath); err == nil {
			repoBranch = strings.TrimSpace(repoStatus.Branch)
			repoDirty = repoStatus.Dirty
			repoConflict = repoConflictFromGit(repoStatus)
			repoSyncStatus = repoSyncStatusFromGit(repoStatus)
			repoAheadCount = repoStatus.Ahead
			repoBehindCount = repoStatus.Behind
		}
		worktreeMergeStatus = resolveWorktreeMergeStatus(ctx, worktreeRootPath, worktreeKind, repoBranch, worktreeParentBranch)
	}

	score := attention.Score(attention.Input{
		Path:            projectPath,
		Now:             now,
		CreatedAt:       now,
		RepoDirty:       repoDirty,
		Pinned:          existing.Pinned,
		Unread:          attention.AssessmentUnread(existing),
		SnoozedUntil:    existing.SnoozedUntil,
		HasActivity:     false,
		ActiveThreshold: s.cfg.ActiveThreshold,
		StuckThreshold:  s.cfg.StuckThreshold,
		OpenTodoCount:   existing.OpenTODOCount,
	})

	state := model.ProjectState{
		Path:                 projectPath,
		Name:                 filepath.Base(projectPath),
		Status:               score.Status,
		AttentionScore:       score.Score,
		PresentOnDisk:        presentOnDisk,
		WorktreeRootPath:     worktreeRootPath,
		WorktreeKind:         worktreeKind,
		WorktreeParentBranch: worktreeParentBranch,
		WorktreeMergeStatus:  worktreeMergeStatus,
		WorktreeOriginTodoID: existing.WorktreeOriginTodoID,
		RepoBranch:           repoBranch,
		RepoDirty:            repoDirty,
		RepoConflict:         repoConflict,
		RepoSyncStatus:       repoSyncStatus,
		RepoAheadCount:       repoAheadCount,
		RepoBehindCount:      repoBehindCount,
		ManuallyAdded:        true,
		InScope:              true,
		Pinned:               existing.Pinned,
		SnoozedUntil:         existing.SnoozedUntil,
		MovedFromPath:        existing.MovedFromPath,
		MovedAt:              existing.MovedAt,
		AttentionReason:      score.Reasons,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.store.UpsertProjectState(ctx, state); err != nil {
		return fmt.Errorf("persist project state: %w", err)
	}
	if projectStateChanged(existing, state) {
		s.publishProjectChanged(ctx, now, state)
	}
	return nil
}

func normalizeCreateOrAttachProjectRequest(req CreateOrAttachProjectRequest) (normalizedCreateOrAttachProjectRequest, error) {
	parentPath := normalizeCreateOrAttachProjectPath(req.ParentPath)
	if parentPath == "" {
		return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("project path is required")
	}

	projectName := strings.TrimSpace(req.Name)
	if projectName == "" {
		info, err := os.Stat(parentPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("project name is required unless the path already exists")
		case err != nil:
			return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("check project path: %w", err)
		case !info.IsDir():
			return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("path already exists and is not a directory: %s", parentPath)
		}

		projectName = filepath.Base(parentPath)
		if projectName == "." || projectName == string(os.PathSeparator) || !validProjectFolderName(projectName) {
			return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("project name is required unless the path ends with a folder name")
		}
		return normalizedCreateOrAttachProjectRequest{
			ParentPath:          filepath.Dir(parentPath),
			ProjectName:         projectName,
			ProjectPath:         parentPath,
			NameDerivedFromPath: true,
		}, nil
	}
	if !validProjectFolderName(projectName) {
		return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("project name must be a single folder name")
	}

	projectPath := filepath.Clean(filepath.Join(parentPath, projectName))
	if projectPath == parentPath {
		return normalizedCreateOrAttachProjectRequest{}, fmt.Errorf("project name must be a single folder name")
	}
	return normalizedCreateOrAttachProjectRequest{
		ParentPath:  parentPath,
		ProjectName: projectName,
		ProjectPath: projectPath,
	}, nil
}

func validProjectFolderName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/') && !strings.ContainsRune(name, '\\') && filepath.Base(name) == name
}

func normalizeCreateOrAttachProjectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = trimWrappingPathQuotes(path)
	path = expandUserHomePath(path)
	absPath, err := filepath.Abs(path)
	if err == nil {
		path = absPath
	}
	return filepath.Clean(path)
}

func trimWrappingPathQuotes(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 2 {
		if (path[0] == '\'' && path[len(path)-1] == '\'') || (path[0] == '"' && path[len(path)-1] == '"') {
			return strings.TrimSpace(path[1 : len(path)-1])
		}
	}
	return path
}

func expandUserHomePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func runGitInit(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "init", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
