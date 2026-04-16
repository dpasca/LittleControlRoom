package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

type CreateTodoWorktreeRequest struct {
	ProjectPath    string
	TodoID         int64
	BranchName     string
	WorktreeSuffix string
}

type CreateTodoWorktreeResult struct {
	RootProjectPath string
	WorktreePath    string
	ParentBranch    string
	BranchName      string
	WorktreeSuffix  string
}

type MergeWorktreeBackResult struct {
	WorktreePath    string
	RootProjectPath string
	SourceBranch    string
	TargetBranch    string
	CommitHash      string
	AlreadyMerged   bool
	LinkedTodoID    int64
	LinkedTodoText  string
	LinkedTodoPath  string
}

func (s *Service) CreateTodoWorktree(ctx context.Context, req CreateTodoWorktreeRequest) (CreateTodoWorktreeResult, error) {
	if s == nil || s.store == nil {
		return CreateTodoWorktreeResult{}, fmt.Errorf("service unavailable")
	}
	projectPath := filepath.Clean(strings.TrimSpace(req.ProjectPath))
	if projectPath == "" {
		return CreateTodoWorktreeResult{}, fmt.Errorf("project path is required")
	}
	if req.TodoID <= 0 {
		return CreateTodoWorktreeResult{}, fmt.Errorf("todo id is required")
	}

	todo, err := s.store.GetTodo(ctx, req.TodoID)
	if err != nil {
		return CreateTodoWorktreeResult{}, err
	}
	if filepath.Clean(strings.TrimSpace(todo.ProjectPath)) != projectPath {
		return CreateTodoWorktreeResult{}, fmt.Errorf("todo %d belongs to %s, not %s", req.TodoID, todo.ProjectPath, projectPath)
	}

	suggestionBranch := ""
	suggestionSuffix := ""
	suggestion, err := s.store.GetTodoWorktreeSuggestion(ctx, req.TodoID)
	switch {
	case err == nil:
		if suggestion.ProjectPath != "" && filepath.Clean(strings.TrimSpace(suggestion.ProjectPath)) != projectPath {
			return CreateTodoWorktreeResult{}, fmt.Errorf("todo %d belongs to %s, not %s", req.TodoID, suggestion.ProjectPath, projectPath)
		}
		if suggestion.Status == model.TodoWorktreeSuggestionReady {
			suggestionBranch = suggestion.BranchName
			suggestionSuffix = suggestion.WorktreeSuffix
		}
	case !errors.Is(err, sql.ErrNoRows):
		return CreateTodoWorktreeResult{}, fmt.Errorf("load TODO worktree suggestion: %w", err)
	}

	fallbackBranch, fallbackSuffix := fallbackTodoWorktreeNames(todo.Text, req.TodoID)
	branchName := sanitizeWorktreeBranchName(firstNonEmptyTrimmed(req.BranchName, suggestionBranch, fallbackBranch))
	worktreeSuffix := sanitizeWorktreeSuffix(firstNonEmptyTrimmed(req.WorktreeSuffix, suggestionSuffix, fallbackSuffix))
	if worktreeSuffix == "" {
		worktreeSuffix = sanitizeWorktreeSuffix(strings.ReplaceAll(branchName, "/", "-"))
	}
	if branchName == "" || worktreeSuffix == "" {
		return CreateTodoWorktreeResult{}, fmt.Errorf("could not determine worktree branch or folder name")
	}

	worktreeRootPath, _ := s.readProjectWorktreeInfo(ctx, projectPath)
	if strings.TrimSpace(worktreeRootPath) == "" {
		worktreeRootPath = projectPath
	}
	parentBranch := ""
	if s.gitRepoStatusReader != nil {
		if status, statusErr := s.gitRepoStatusReader(ctx, worktreeRootPath); statusErr == nil {
			parentBranch = strings.TrimSpace(status.Branch)
		}
	}
	worktreePath, worktreeSuffix, branchName, err := uniqueWorktreeNames(worktreeRootPath, projectPath, worktreeSuffix, branchName)
	if err != nil {
		return CreateTodoWorktreeResult{}, err
	}
	result := CreateTodoWorktreeResult{
		RootProjectPath: worktreeRootPath,
		WorktreePath:    worktreePath,
		ParentBranch:    parentBranch,
		BranchName:      branchName,
		WorktreeSuffix:  worktreeSuffix,
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return CreateTodoWorktreeResult{}, fmt.Errorf("create worktree parent directory: %w", err)
	}
	if err := gitWorktreeAdd(ctx, worktreeRootPath, worktreePath, branchName); err != nil {
		return CreateTodoWorktreeResult{}, err
	}

	_, attachErr := s.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: filepath.Dir(worktreePath),
		Name:       filepath.Base(worktreePath),
	})
	if attachErr != nil {
		return result, fmt.Errorf("created worktree at %s but failed to track it in Little Control Room: %w", worktreePath, attachErr)
	}
	if strings.TrimSpace(parentBranch) != "" {
		if err := s.store.SetWorktreeParentBranch(ctx, worktreePath, parentBranch); err != nil {
			return result, fmt.Errorf("record parent branch for worktree %s: %w", worktreePath, err)
		}
		if err := s.RefreshProjectStatus(ctx, worktreePath); err != nil {
			return result, fmt.Errorf("refresh tracked worktree %s after recording its parent branch: %w", worktreePath, err)
		}
	}
	if err := s.store.SetWorktreeOriginTodoID(ctx, worktreePath, req.TodoID); err != nil {
		return result, fmt.Errorf("record origin todo for worktree %s: %w", worktreePath, err)
	}

	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: worktreePath,
			Payload: map[string]string{
				"action":      "create_worktree",
				"root_path":   worktreeRootPath,
				"branch_name": branchName,
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: worktreePath,
		Type:        string(events.ActionApplied),
		Payload:     fmt.Sprintf("create_worktree root=%s branch=%s", worktreeRootPath, branchName),
	})
	return result, nil
}

func (s *Service) RegenerateTodoWorktreeSuggestion(ctx context.Context, projectPath string, todoID int64) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return fmt.Errorf("project path is required")
	}
	todo, err := s.store.GetTodo(ctx, todoID)
	if err != nil {
		return err
	}
	if filepath.Clean(strings.TrimSpace(todo.ProjectPath)) != projectPath {
		return fmt.Errorf("todo %d belongs to %s, not %s", todoID, todo.ProjectPath, projectPath)
	}
	changed, err := s.store.ForceQueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		return err
	}
	now := time.Now()
	if changed && s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: projectPath,
			Payload: map[string]string{
				"action":  "regenerate_worktree_suggestion",
				"todo_id": fmt.Sprintf("%d", todoID),
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     fmt.Sprintf("regenerate_worktree_suggestion todo_id=%d", todoID),
	})
	if changed && s.todoSuggester != nil {
		s.todoSuggester.Notify()
	}
	return nil
}

func (s *Service) EnsureTodoWorktreeSuggestion(ctx context.Context, projectPath string, todoID int64) (bool, error) {
	if s == nil || s.store == nil {
		return false, fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return false, fmt.Errorf("project path is required")
	}
	if todoID <= 0 {
		return false, fmt.Errorf("todo id is required")
	}
	todo, err := s.store.GetTodo(ctx, todoID)
	if err != nil {
		return false, err
	}
	if filepath.Clean(strings.TrimSpace(todo.ProjectPath)) != projectPath {
		return false, fmt.Errorf("todo %d belongs to %s, not %s", todoID, todo.ProjectPath, projectPath)
	}
	suggestion, err := s.store.GetTodoWorktreeSuggestion(ctx, todoID)
	switch {
	case err == nil:
		switch suggestion.Status {
		case model.TodoWorktreeSuggestionReady,
			model.TodoWorktreeSuggestionQueued,
			model.TodoWorktreeSuggestionRunning:
			return false, nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	changed, err := s.store.ForceQueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}

	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: projectPath,
			Payload: map[string]string{
				"action":  "ensure_worktree_suggestion",
				"todo_id": fmt.Sprintf("%d", todoID),
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     fmt.Sprintf("ensure_worktree_suggestion todo_id=%d", todoID),
	})
	if s.todoSuggester != nil {
		s.todoSuggester.Notify()
	}
	return true, nil
}

func (s *Service) MergeWorktreeBack(ctx context.Context, projectPath string) (MergeWorktreeBackResult, error) {
	if s == nil || s.store == nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("project path is required")
	}

	detail, err := s.store.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		return MergeWorktreeBackResult{}, err
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		return MergeWorktreeBackResult{}, fmt.Errorf("only linked worktrees can be merged back")
	}

	rootPath := filepath.Clean(strings.TrimSpace(detail.Summary.WorktreeRootPath))
	if rootPath == "" {
		rootPath, _ = s.readProjectWorktreeInfo(ctx, projectPath)
	}
	if rootPath == "" || rootPath == "." {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree root is unavailable for %s", projectPath)
	}

	targetBranch := strings.TrimSpace(detail.Summary.WorktreeParentBranch)
	if targetBranch == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("this worktree has no recorded parent branch to merge back into")
	}
	if s.gitRepoStatusReader == nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("git status reader unavailable")
	}

	sourceStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("read worktree status before merge-back: %w", err)
	}
	if sourceStatus.Dirty {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree is dirty; commit or discard changes before merging back")
	}
	sourceBranch := strings.TrimSpace(sourceStatus.Branch)
	if sourceBranch == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree branch is unavailable for %s", projectPath)
	}
	if sourceBranch == targetBranch {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree branch %s already matches its parent branch", sourceBranch)
	}

	rootStatus, err := s.gitRepoStatusReader(ctx, rootPath)
	if err != nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("read root repo status before merge-back: %w", err)
	}
	rootBranch := strings.TrimSpace(rootStatus.Branch)
	if rootBranch != targetBranch {
		return MergeWorktreeBackResult{}, fmt.Errorf("root worktree %s is on %s, expected %s", rootPath, rootBranch, targetBranch)
	}
	if rootStatus.Dirty {
		return MergeWorktreeBackResult{}, fmt.Errorf("root worktree is dirty; commit or discard changes before merging back")
	}

	result := MergeWorktreeBackResult{
		WorktreePath:    projectPath,
		RootProjectPath: rootPath,
		SourceBranch:    sourceBranch,
		TargetBranch:    targetBranch,
	}
	if linkedTodo, ok := s.linkedOpenTodoForWorktree(ctx, detail.Summary); ok {
		result.LinkedTodoID = linkedTodo.ID
		result.LinkedTodoText = strings.TrimSpace(linkedTodo.Text)
		result.LinkedTodoPath = strings.TrimSpace(linkedTodo.ProjectPath)
	}
	alreadyMerged, err := gitBranchMergedIntoHEAD(ctx, rootPath, sourceBranch)
	if err != nil {
		return result, err
	}
	if alreadyMerged {
		result.AlreadyMerged = true
		return result, nil
	}
	if err := gitMergeBranch(ctx, rootPath, sourceBranch); err != nil {
		refreshErr := s.refreshWorktreeMergeStatus(ctx, rootPath, projectPath)
		if s.gitRepoStatusReader != nil {
			if failedRootStatus, statusErr := s.gitRepoStatusReader(ctx, rootPath); statusErr == nil {
				if conflictErr := worktreeMergeConflictError(rootPath, sourceBranch, targetBranch, failedRootStatus); conflictErr != nil {
					if refreshErr != nil {
						return result, fmt.Errorf("%w (status refresh also failed: %v)", conflictErr, refreshErr)
					}
					return result, conflictErr
				}
			}
		}
		if refreshErr != nil {
			return result, fmt.Errorf("merge %s back into %s at %s failed: %w (status refresh also failed: %v)", sourceBranch, targetBranch, rootPath, err, refreshErr)
		}
		return result, fmt.Errorf("merge %s back into %s at %s failed: %w", sourceBranch, targetBranch, rootPath, err)
	}
	if err := gitSubmoduleUpdateInitRecursive(ctx, rootPath); err != nil {
		refreshErr := s.refreshWorktreeMergeStatus(ctx, rootPath, projectPath)
		if refreshErr != nil {
			return result, fmt.Errorf("merged %s back into %s at %s but failed to sync submodules: %w (status refresh also failed: %v)", sourceBranch, targetBranch, rootPath, err, refreshErr)
		}
		return result, fmt.Errorf("merged %s back into %s at %s but failed to sync submodules: %w", sourceBranch, targetBranch, rootPath, err)
	}

	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: rootPath,
			Payload: map[string]string{
				"action":        "merge_worktree_back",
				"source_path":   projectPath,
				"source_branch": sourceBranch,
				"target_branch": targetBranch,
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: rootPath,
		Type:        string(events.ActionApplied),
		Payload:     fmt.Sprintf("merge_worktree_back source=%s source_branch=%s target_branch=%s", projectPath, sourceBranch, targetBranch),
	})
	if err := s.RefreshProjectStatus(ctx, rootPath); err != nil {
		return result, fmt.Errorf("refresh merged root project: %w", err)
	}
	if err := s.RefreshProjectStatus(ctx, projectPath); err != nil {
		return result, fmt.Errorf("refresh merged worktree project: %w", err)
	}
	return result, nil
}

func (s *Service) CommitAndMergeWorktreeBack(ctx context.Context, projectPath string) (MergeWorktreeBackResult, error) {
	if s == nil || s.store == nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("project path is required")
	}
	if s.gitRepoStatusReader == nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("git status reader unavailable")
	}

	detail, err := s.store.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		return MergeWorktreeBackResult{}, err
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		return MergeWorktreeBackResult{}, fmt.Errorf("only linked worktrees can be merged back")
	}

	rootPath := filepath.Clean(strings.TrimSpace(detail.Summary.WorktreeRootPath))
	if rootPath == "" {
		rootPath, _ = s.readProjectWorktreeInfo(ctx, projectPath)
	}
	if rootPath == "" || rootPath == "." {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree root is unavailable for %s", projectPath)
	}

	targetBranch := strings.TrimSpace(detail.Summary.WorktreeParentBranch)
	if targetBranch == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("this worktree has no recorded parent branch to merge back into")
	}

	sourceStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("read worktree status before merge-back: %w", err)
	}
	sourceBranch := strings.TrimSpace(sourceStatus.Branch)
	if sourceBranch == "" {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree branch is unavailable for %s", projectPath)
	}
	if sourceBranch == targetBranch {
		return MergeWorktreeBackResult{}, fmt.Errorf("worktree branch %s already matches its parent branch", sourceBranch)
	}

	rootStatus, err := s.gitRepoStatusReader(ctx, rootPath)
	if err != nil {
		return MergeWorktreeBackResult{}, fmt.Errorf("read root repo status before merge-back: %w", err)
	}
	rootBranch := strings.TrimSpace(rootStatus.Branch)
	if rootBranch != targetBranch {
		return MergeWorktreeBackResult{}, fmt.Errorf("root worktree %s is on %s, expected %s", rootPath, rootBranch, targetBranch)
	}
	if rootStatus.Dirty {
		return MergeWorktreeBackResult{}, fmt.Errorf("root worktree is dirty; commit or discard changes before merging back")
	}

	if !sourceStatus.Dirty {
		return s.MergeWorktreeBack(ctx, projectPath)
	}

	preview, err := s.ResolveSubmodulesAndPrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err != nil {
		return MergeWorktreeBackResult{}, err
	}
	commitResult, err := s.ApplyCommit(ctx, preview, false, nil)
	if err != nil {
		return MergeWorktreeBackResult{}, err
	}

	mergeResult, err := s.MergeWorktreeBack(ctx, projectPath)
	mergeResult.CommitHash = commitResult.CommitHash
	if err != nil {
		return mergeResult, err
	}
	return mergeResult, nil
}

func gitBranchMergedIntoHEAD(ctx context.Context, repoPath, branch string) (bool, error) {
	return gitBranchMergedIntoBranch(ctx, repoPath, branch, "HEAD")
}

func gitBranchMergedIntoBranch(ctx context.Context, repoPath, branch, target string) (bool, error) {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	branch = strings.TrimSpace(branch)
	target = strings.TrimSpace(target)
	if repoPath == "" || branch == "" || target == "" {
		return false, fmt.Errorf("repo path, branch, and target are required")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "merge-base", "--is-ancestor", branch, target)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check whether %s is already merged into %s in %s: %w", branch, target, repoPath, err)
	}
	return true, nil
}

func resolveWorktreeMergeStatus(ctx context.Context, rootPath string, kind model.WorktreeKind, sourceBranch, targetBranch string) model.WorktreeMergeStatus {
	if kind != model.WorktreeKindLinked {
		return model.WorktreeMergeStatusUnknown
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	if rootPath == "" || rootPath == "." || sourceBranch == "" || targetBranch == "" {
		return model.WorktreeMergeStatusUnknown
	}
	if sourceBranch == targetBranch {
		return model.WorktreeMergeStatusMerged
	}
	merged, err := gitBranchMergedIntoBranch(ctx, rootPath, sourceBranch, targetBranch)
	if err != nil {
		return model.WorktreeMergeStatusUnknown
	}
	if merged {
		return model.WorktreeMergeStatusMerged
	}
	return model.WorktreeMergeStatusNotMerged
}

func (s *Service) refreshWorktreeMergeStatus(ctx context.Context, rootPath, projectPath string) error {
	var errs []string
	if err := s.RefreshProjectStatus(ctx, rootPath); err != nil {
		errs = append(errs, fmt.Sprintf("refresh merged root project: %v", err))
	}
	if err := s.RefreshProjectStatus(ctx, projectPath); err != nil {
		errs = append(errs, fmt.Sprintf("refresh merged worktree project: %v", err))
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

func worktreeMergeConflictError(rootPath, sourceBranch, targetBranch string, status scanner.GitRepoStatus) error {
	conflicted := conflictedPaths(status)
	if len(conflicted) == 0 {
		return nil
	}
	lines := []string{
		fmt.Sprintf("merge conflict while merging %s into %s at %s", sourceBranch, targetBranch, rootPath),
		"Resolve or abort the merge in the root checkout before retrying.",
		"Conflicted files:",
	}
	for _, file := range summarizeConflictedPaths(conflicted, 6) {
		lines = append(lines, "- "+file)
	}
	return errors.New(strings.Join(lines, "\n"))
}

func conflictedPaths(status scanner.GitRepoStatus) []string {
	out := make([]string, 0, len(status.Changes))
	seen := map[string]struct{}{}
	for _, change := range status.Changes {
		if change.Kind != scanner.GitChangeUnmerged {
			continue
		}
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func summarizeConflictedPaths(paths []string, limit int) []string {
	if len(paths) == 0 {
		return nil
	}
	if limit <= 0 || len(paths) <= limit {
		return append([]string(nil), paths...)
	}
	summary := append([]string(nil), paths[:limit]...)
	summary = append(summary, fmt.Sprintf("+%d more", len(paths)-limit))
	return summary
}

func (s *Service) RemoveWorktree(ctx context.Context, projectPath string, force bool) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return fmt.Errorf("project path is required")
	}
	rootPath, kind := s.readProjectWorktreeInfo(ctx, projectPath)
	if kind != model.WorktreeKindLinked {
		return fmt.Errorf("only linked worktrees can be removed from Little Control Room")
	}
	if strings.TrimSpace(rootPath) == "" {
		return fmt.Errorf("worktree root is unavailable for %s", projectPath)
	}
	allowSubmoduleForceFallback := false
	if !force && s.gitRepoStatusReader != nil {
		status, err := s.gitRepoStatusReader(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("read git status before removing worktree: %w", err)
		}
		if status.Dirty {
			return fmt.Errorf("worktree is dirty; commit or discard changes before removing it")
		}
		allowSubmoduleForceFallback = true
	}
	if err := gitWorktreeRemove(ctx, rootPath, projectPath, force); err != nil {
		if !(allowSubmoduleForceFallback && isGitWorktreeSubmoduleRemoveError(err)) {
			return err
		}
		if err := gitWorktreeRemove(ctx, rootPath, projectPath, true); err != nil {
			return err
		}
	}
	if err := s.store.SetForgotten(ctx, projectPath, true); err != nil {
		return fmt.Errorf("forget removed worktree: %w", err)
	}
	// Reconcile the persisted presence immediately so merged-and-removed worktrees
	// do not linger as orphaned checkouts until a later scan happens to revisit them.
	if err := s.store.SetProjectPresence(ctx, projectPath, projectPathExists(projectPath)); err != nil {
		return fmt.Errorf("record removed worktree presence: %w", err)
	}

	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: projectPath,
			Payload: map[string]string{
				"action":    "remove_worktree",
				"root_path": rootPath,
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     "remove_worktree",
	})
	return nil
}

func (s *Service) PruneWorktrees(ctx context.Context, projectPath string) error {
	if s == nil {
		return fmt.Errorf("service unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return fmt.Errorf("project path is required")
	}
	rootPath, _ := s.readProjectWorktreeInfo(ctx, projectPath)
	if strings.TrimSpace(rootPath) == "" {
		rootPath = projectPath
	}
	if err := gitWorktreePrune(ctx, rootPath); err != nil {
		return err
	}
	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: rootPath,
			Payload: map[string]string{
				"action": "prune_worktrees",
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: rootPath,
		Type:        string(events.ActionApplied),
		Payload:     "prune_worktrees",
	})
	return nil
}

func (s *Service) readProjectWorktreeInfo(ctx context.Context, projectPath string) (string, model.WorktreeKind) {
	return s.readProjectWorktreeInfoWithReader(ctx, projectPath, s.gitWorktreeInfoReader)
}

func (s *Service) readProjectWorktreeInfoWithReader(ctx context.Context, projectPath string, reader func(context.Context, string) (scanner.GitWorktreeInfo, error)) (string, model.WorktreeKind) {
	if s == nil || reader == nil || !projectPathExists(projectPath) {
		return "", model.WorktreeKindNone
	}
	info, err := reader(ctx, projectPath)
	if err != nil {
		return "", model.WorktreeKindNone
	}
	rootPath := filepath.Clean(strings.TrimSpace(info.RootPath))
	kind := modelWorktreeKindFromGit(info.Kind)
	if kind == model.WorktreeKindMain {
		rootPath = filepath.Clean(strings.TrimSpace(projectPath))
	}
	if rootPath != "" && s.store != nil {
		if summaries, err := s.store.GetProjectSummaryMap(ctx); err == nil {
			known := make([]string, 0, len(summaries))
			for path := range summaries {
				known = append(known, path)
			}
			rootPath = preferredKnownPathVariant(rootPath, known)
		}
	}
	return rootPath, kind
}

func (s *Service) linkedOpenTodoForWorktree(ctx context.Context, summary model.ProjectSummary) (model.TodoItem, bool) {
	if s == nil || s.store == nil || summary.WorktreeOriginTodoID <= 0 {
		return model.TodoItem{}, false
	}
	todo, err := s.store.GetTodo(ctx, summary.WorktreeOriginTodoID)
	if err != nil || todo.Done {
		return model.TodoItem{}, false
	}
	return todo, true
}

func (s *Service) expandDiscoveredWorktreePaths(ctx context.Context, discovered []string, oldMap map[string]model.ProjectSummary, scope scanner.PathScope, worktreeInfoReader func(context.Context, string) (scanner.GitWorktreeInfo, error), worktreeListReader func(context.Context, string) ([]scanner.GitWorktree, error)) ([]string, map[string]map[string]struct{}) {
	outSet := map[string]struct{}{}
	for _, path := range discovered {
		cleanPath := filepath.Clean(path)
		if cleanPath != "" && cleanPath != "." {
			outSet[cleanPath] = struct{}{}
		}
	}

	liveByRoot := map[string]map[string]struct{}{}
	if s == nil || worktreeListReader == nil {
		return sortedPathKeys(outSet), liveByRoot
	}

	seeds := make([]string, 0, len(outSet)+len(oldMap))
	for path := range outSet {
		if projectPathExists(path) {
			seeds = append(seeds, path)
		}
	}
	for path := range oldMap {
		if projectPathExists(path) {
			seeds = append(seeds, path)
		}
		if summary := oldMap[path]; summary.WorktreeKind == model.WorktreeKindLinked {
			rootPath := filepath.Clean(strings.TrimSpace(summary.WorktreeRootPath))
			if rootPath != "" && rootPath != "." && projectPathExists(rootPath) {
				seeds = append(seeds, rootPath)
			}
		}
	}
	sort.Strings(seeds)

	listedRoots := map[string]struct{}{}
	for _, seed := range seeds {
		rootPath, _ := s.readProjectWorktreeInfoWithReader(ctx, seed, worktreeInfoReader)
		if rootPath == "" {
			rootPath = filepath.Clean(seed)
		}
		if _, seen := listedRoots[rootPath]; seen {
			continue
		}
		worktrees, err := worktreeListReader(ctx, seed)
		if err != nil {
			continue
		}
		listedRoots[rootPath] = struct{}{}
		rootSet := liveByRoot[rootPath]
		if rootSet == nil {
			rootSet = map[string]struct{}{}
			liveByRoot[rootPath] = rootSet
		}
		for _, worktree := range worktrees {
			worktreePath := preferredKnownPathVariant(filepath.Clean(strings.TrimSpace(worktree.Path)), append(sortedPathKeys(outSet), mapKeys(oldMap)...))
			if worktreePath == "" || worktreePath == "." {
				continue
			}
			rootSet[worktreePath] = struct{}{}
			if scope.Allows(worktreePath) || oldMap[worktreePath].Path != "" || oldMap[rootPath].Path != "" {
				outSet[worktreePath] = struct{}{}
			}
		}
	}
	return sortedPathKeys(outSet), liveByRoot
}

func sortedPathKeys(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func mapKeys(values map[string]model.ProjectSummary) []string {
	out := make([]string, 0, len(values))
	for path := range values {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func modelWorktreeKindFromGit(kind scanner.GitWorktreeKind) model.WorktreeKind {
	switch kind {
	case scanner.GitWorktreeKindMain:
		return model.WorktreeKindMain
	case scanner.GitWorktreeKindLinked:
		return model.WorktreeKindLinked
	default:
		return model.WorktreeKindNone
	}
}

func suggestedTodoWorktreePath(projectPath, worktreeSuffix string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	worktreeSuffix = sanitizeWorktreeSuffix(worktreeSuffix)
	base := filepath.Base(projectPath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "worktree"
	}
	return filepath.Join(filepath.Dir(projectPath), base+"--"+worktreeSuffix)
}

func uniqueWorktreeNames(rootPath, projectPath, suffix, branch string) (worktreePath, finalSuffix, finalBranch string, _ error) {
	const maxAttempts = 100
	baseSuffix := suffix
	baseBranch := branch
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			tag := fmt.Sprintf("-%d", i+1)
			suffix = baseSuffix + tag
			branch = baseBranch + tag
		}
		worktreePath = suggestedTodoWorktreePath(rootPath, suffix)
		if worktreePath == projectPath {
			return "", "", "", fmt.Errorf("suggested worktree path matches the current project path")
		}
		info, statErr := os.Stat(worktreePath)
		if os.IsNotExist(statErr) {
			return worktreePath, suffix, branch, nil
		}
		if statErr != nil {
			return "", "", "", fmt.Errorf("check worktree path: %w", statErr)
		}
		if !info.IsDir() {
			return "", "", "", fmt.Errorf("worktree path already exists and is not a directory: %s", worktreePath)
		}
	}
	return "", "", "", fmt.Errorf("could not find a unique worktree path after %d attempts (last tried: %s)", maxAttempts, worktreePath)
}

func gitWorktreeAdd(ctx context.Context, repoPath, worktreePath, branchName string) error {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	worktreePath = filepath.Clean(strings.TrimSpace(worktreePath))
	branchName = strings.TrimSpace(branchName)
	if repoPath == "" || worktreePath == "" || branchName == "" {
		return fmt.Errorf("repo path, worktree path, and branch name are required")
	}

	args := []string{"-C", repoPath, "worktree", "add"}
	exists, err := gitLocalBranchExists(ctx, repoPath, branchName)
	if err != nil {
		return err
	}
	if !exists {
		args = append(args, "-b", branchName, worktreePath, "HEAD")
	} else {
		args = append(args, worktreePath, branchName)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create git worktree %s on %s: %w (%s)", worktreePath, branchName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitWorktreeRemove(ctx context.Context, repoPath, worktreePath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove git worktree %s: %w (%s)", worktreePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isGitWorktreeSubmoduleRemoveError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "working trees containing submodules cannot be moved or removed")
}

func gitMergeBranch(ctx context.Context, repoPath, branchName string) error {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	branchName = strings.TrimSpace(branchName)
	if repoPath == "" || branchName == "" {
		return fmt.Errorf("repo path and branch name are required")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "merge", "--no-edit", branchName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge branch %s into %s: %w (%s)", branchName, repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitSubmoduleUpdateInitRecursive(ctx context.Context, repoPath string) error {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	if repoPath == "" {
		return fmt.Errorf("repo path is required")
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "protocol.file.allow=always", "-C", repoPath, "submodule", "update", "--init", "--recursive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sync submodules for %s: %w (%s)", repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitWorktreePrune(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("prune git worktrees for %s: %w (%s)", repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitLocalBranchExists(ctx context.Context, repoPath, branchName string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check git branch %s: %w", branchName, err)
	}
	return true, nil
}

func sanitizeWorktreeBranchName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastSlash := false
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSlash = false
			lastDash = false
		case r == '/':
			if b.Len() == 0 || lastSlash {
				continue
			}
			b.WriteRune('/')
			lastSlash = true
			lastDash = false
		default:
			if b.Len() == 0 || lastDash || lastSlash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-/")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	for strings.Contains(out, "//") {
		out = strings.ReplaceAll(out, "//", "/")
	}
	return out
}

func sanitizeWorktreeSuffix(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() == 0 || lastDash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func fallbackTodoWorktreeNames(todoText string, todoID int64) (branchName, worktreeSuffix string) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(todoText)))
	if len(fields) > 8 {
		fields = fields[:8]
	}
	slug := sanitizeWorktreeSuffix(strings.Join(fields, " "))
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = fmt.Sprintf("todo-%d", todoID)
	}
	suffixStem := strings.TrimPrefix(slug, "todo-")
	if suffixStem == "" {
		suffixStem = slug
	}
	return "todo/" + slug, "todo-" + suffixStem
}

func preferredKnownPathVariant(candidate string, known []string) string {
	candidate = filepath.Clean(strings.TrimSpace(candidate))
	if candidate == "" || candidate == "." {
		return candidate
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return candidate
	}
	for _, path := range known {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		if resolvedPath == resolvedCandidate {
			return path
		}
	}
	return candidate
}
