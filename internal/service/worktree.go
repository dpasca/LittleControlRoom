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
	BranchName      string
	WorktreeSuffix  string
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

	suggestion, err := s.store.GetTodoWorktreeSuggestion(ctx, req.TodoID)
	if err != nil {
		return CreateTodoWorktreeResult{}, fmt.Errorf("load TODO worktree suggestion: %w", err)
	}
	if suggestion.ProjectPath != "" && suggestion.ProjectPath != projectPath {
		return CreateTodoWorktreeResult{}, fmt.Errorf("todo %d belongs to %s, not %s", req.TodoID, suggestion.ProjectPath, projectPath)
	}
	switch suggestion.Status {
	case model.TodoWorktreeSuggestionReady:
	case model.TodoWorktreeSuggestionQueued, model.TodoWorktreeSuggestionRunning:
		return CreateTodoWorktreeResult{}, fmt.Errorf("TODO worktree suggestion is still preparing")
	case model.TodoWorktreeSuggestionFailed:
		return CreateTodoWorktreeResult{}, fmt.Errorf("TODO worktree suggestion is unavailable right now")
	default:
		return CreateTodoWorktreeResult{}, fmt.Errorf("TODO worktree suggestion is not ready yet")
	}

	branchName := sanitizeWorktreeBranchName(firstNonEmptyTrimmed(req.BranchName, suggestion.BranchName))
	worktreeSuffix := sanitizeWorktreeSuffix(firstNonEmptyTrimmed(req.WorktreeSuffix, suggestion.WorktreeSuffix))
	if worktreeSuffix == "" {
		worktreeSuffix = sanitizeWorktreeSuffix(strings.ReplaceAll(branchName, "/", "-"))
	}
	if branchName == "" || worktreeSuffix == "" {
		return CreateTodoWorktreeResult{}, fmt.Errorf("TODO worktree suggestion is missing branch or folder name")
	}

	worktreeRootPath, _ := s.readProjectWorktreeInfo(ctx, projectPath)
	if strings.TrimSpace(worktreeRootPath) == "" {
		worktreeRootPath = projectPath
	}
	worktreePath := suggestedTodoWorktreePath(worktreeRootPath, worktreeSuffix)
	result := CreateTodoWorktreeResult{
		RootProjectPath: worktreeRootPath,
		WorktreePath:    worktreePath,
		BranchName:      branchName,
		WorktreeSuffix:  worktreeSuffix,
	}

	if worktreePath == projectPath {
		return CreateTodoWorktreeResult{}, fmt.Errorf("suggested worktree path matches the current project path")
	}
	if info, statErr := os.Stat(worktreePath); statErr == nil {
		if info.IsDir() {
			return CreateTodoWorktreeResult{}, fmt.Errorf("worktree path already exists: %s", worktreePath)
		}
		return CreateTodoWorktreeResult{}, fmt.Errorf("worktree path already exists and is not a directory: %s", worktreePath)
	} else if !os.IsNotExist(statErr) {
		return CreateTodoWorktreeResult{}, fmt.Errorf("check worktree path: %w", statErr)
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

func (s *Service) RemoveWorktree(ctx context.Context, projectPath string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
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
	if s.gitRepoStatusReader != nil {
		status, err := s.gitRepoStatusReader(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("read git status before removing worktree: %w", err)
		}
		if status.Dirty {
			return fmt.Errorf("worktree is dirty; commit or discard changes before removing it")
		}
	}
	if err := gitWorktreeRemove(ctx, rootPath, projectPath); err != nil {
		return err
	}
	if err := s.store.SetForgotten(ctx, projectPath, true); err != nil {
		return fmt.Errorf("forget removed worktree: %w", err)
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
	if s == nil || s.gitWorktreeInfoReader == nil || !projectPathExists(projectPath) {
		return "", model.WorktreeKindNone
	}
	info, err := s.gitWorktreeInfoReader(ctx, projectPath)
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

func (s *Service) expandDiscoveredWorktreePaths(ctx context.Context, discovered []string, oldMap map[string]model.ProjectSummary, scope scanner.PathScope) ([]string, map[string]map[string]struct{}) {
	outSet := map[string]struct{}{}
	for _, path := range discovered {
		cleanPath := filepath.Clean(path)
		if cleanPath != "" && cleanPath != "." {
			outSet[cleanPath] = struct{}{}
		}
	}

	liveByRoot := map[string]map[string]struct{}{}
	if s == nil || s.gitWorktreeListReader == nil {
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
	}
	sort.Strings(seeds)

	listedRoots := map[string]struct{}{}
	for _, seed := range seeds {
		rootPath, _ := s.readProjectWorktreeInfo(ctx, seed)
		if rootPath == "" {
			rootPath = filepath.Clean(seed)
		}
		if _, seen := listedRoots[rootPath]; seen {
			continue
		}
		worktrees, err := s.gitWorktreeListReader(ctx, seed)
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

func gitWorktreeRemove(ctx context.Context, repoPath, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", worktreePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove git worktree %s: %w (%s)", worktreePath, err, strings.TrimSpace(string(out)))
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
