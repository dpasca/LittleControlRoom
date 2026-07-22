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

	"lcroom/internal/codexstate"
	"lcroom/internal/events"
	"lcroom/internal/gitlock"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/worktreeprep"
)

// RestorableWorktreeSession is a Codex conversation whose recorded working
// directory was an LCR linked worktree that no longer exists on disk.
type RestorableWorktreeSession struct {
	RootProjectPath   string
	WorktreePath      string
	WorktreeName      string
	SessionID         string
	SessionFile       string
	BranchName        string
	ParentBranch      string
	GitSHA            string
	Title             string
	Summary           string
	StartedAt         time.Time
	LastActivity      time.Time
	OriginTodoID      int64
	StoredMetadata    bool
	BranchExists      bool
	RecreateBranch    bool
	StaleRegistration bool
	Ready             bool
	BlockReason       string
}

type RestoreWorktreeSessionRequest struct {
	ProjectPath string
	SessionID   string
}

type RestoreWorktreeSessionResult struct {
	RootProjectPath string
	WorktreePath    string
	SessionID       string
	BranchName      string
	ParentBranch    string
	PrepProfile     string
	PreparedPaths   []string
	WorktreeCreated bool
	ProjectTracked  bool
}

func (s *Service) ListRestorableWorktreeSessions(ctx context.Context, projectPath string) ([]RestorableWorktreeSession, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("service unavailable")
	}
	root, rootSummary, err := s.worktreeRecoveryRoot(ctx, projectPath)
	if err != nil {
		return nil, err
	}

	summaries, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("load historical worktrees: %w", err)
	}
	evidence, err := s.store.GetProjectScanEvidenceMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("load historical worktree sessions: %w", err)
	}
	threads, err := codexstate.ListThreads(ctx, s.Config().CodexHome)
	if err != nil {
		return nil, fmt.Errorf("load Codex session index: %w", err)
	}
	indexedThreads := make(map[string]codexstate.Thread, len(threads))
	for _, thread := range threads {
		indexedThreads[thread.ID] = thread
	}

	bySessionID := make(map[string]RestorableWorktreeSession)
	sessionEvidence := make(map[string]model.SessionEvidence)
	for path, summary := range summaries {
		path = filepath.Clean(strings.TrimSpace(path))
		if summary.WorktreeKind != model.WorktreeKindLinked || summary.PresentOnDisk || !samePath(summary.WorktreeRootPath, root) {
			continue
		}
		for _, session := range evidence[path].Sessions {
			session = model.NormalizeSessionEvidenceIdentity(session)
			if session.Source != model.SessionSourceCodex {
				continue
			}
			sessionID := strings.TrimSpace(session.ExternalID())
			if sessionID == "" {
				continue
			}
			if indexed, ok := indexedThreads[sessionID]; ok {
				if strings.TrimSpace(indexed.AgentRole) != "" || !samePath(indexed.CWD, path) || projectPathExists(indexed.CWD) {
					continue
				}
			}
			if rolloutPath := strings.TrimSpace(session.SessionFile); rolloutPath != "" {
				if isRoot, rootErr := codexstate.RolloutIsRootThread(rolloutPath, sessionID); rootErr == nil && !isRoot {
					continue
				}
			}
			candidate := RestorableWorktreeSession{
				RootProjectPath: root,
				WorktreePath:    path,
				WorktreeName:    firstNonEmptyTrimmed(summary.Name, filepath.Base(path)),
				SessionID:       sessionID,
				SessionFile:     strings.TrimSpace(session.SessionFile),
				BranchName:      firstNonEmptyTrimmed(summary.RepoBranch, summary.WorktreeInitialBranch),
				ParentBranch:    strings.TrimSpace(summary.WorktreeParentBranch),
				StartedAt:       session.StartedAt,
				LastActivity:    session.LastEventAt,
				OriginTodoID:    summary.WorktreeOriginTodoID,
				StoredMetadata:  true,
			}
			if summary.ExternalLatestSessionID() == sessionID {
				candidate.Summary = strings.TrimSpace(summary.LatestSessionSummary)
			}
			bySessionID[sessionID] = mergeRestorableWorktreeSession(bySessionID[sessionID], candidate)
			sessionEvidence[sessionID] = session
		}
	}

	for _, thread := range threads {
		if strings.TrimSpace(thread.AgentRole) != "" {
			continue
		}
		historical := summaries[filepath.Clean(thread.CWD)]
		if !restorableWorktreeBelongsToRoot(thread.CWD, root, historical) {
			continue
		}
		if projectPathExists(thread.CWD) {
			continue
		}
		if isRoot, rootErr := codexstate.RolloutIsRootThread(thread.RolloutPath, thread.ID); rootErr == nil && !isRoot {
			continue
		}
		candidate := RestorableWorktreeSession{
			RootProjectPath: root,
			WorktreePath:    filepath.Clean(thread.CWD),
			WorktreeName:    firstNonEmptyTrimmed(historical.Name, filepath.Base(thread.CWD)),
			SessionID:       strings.TrimSpace(thread.ID),
			SessionFile:     strings.TrimSpace(thread.RolloutPath),
			BranchName:      firstNonEmptyTrimmed(historical.RepoBranch, historical.WorktreeInitialBranch, thread.GitBranch),
			ParentBranch:    strings.TrimSpace(historical.WorktreeParentBranch),
			GitSHA:          strings.TrimSpace(thread.GitSHA),
			Title:           strings.TrimSpace(thread.Title),
			StartedAt:       thread.StartedAt,
			LastActivity:    thread.LastActivity,
			OriginTodoID:    historical.WorktreeOriginTodoID,
			StoredMetadata:  historical.Path != "",
		}
		bySessionID[candidate.SessionID] = mergeRestorableWorktreeSession(bySessionID[candidate.SessionID], candidate)
	}

	parentBranch := strings.TrimSpace(rootSummary.RepoBranch)
	if policy, policyErr := s.store.GetRepositoryRootPolicy(ctx, root); policyErr == nil && strings.TrimSpace(policy.ExpectedBranch) != "" {
		parentBranch = strings.TrimSpace(policy.ExpectedBranch)
	} else if policyErr != nil && !errors.Is(policyErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("load repository root policy: %w", policyErr)
	}

	candidates := make([]RestorableWorktreeSession, 0, len(bySessionID))
	for sessionID, candidate := range bySessionID {
		if candidate.ParentBranch == "" {
			candidate.ParentBranch = parentBranch
		}
		if candidate.Title == "" {
			if session, ok := sessionEvidence[sessionID]; ok {
				if preview, previewErr := sessionclassify.ExtractPreview(ctx, session); previewErr == nil {
					candidate.Title = strings.TrimSpace(preview.Title)
					if candidate.Summary == "" {
						candidate.Summary = strings.TrimSpace(preview.Summary)
					}
				}
			}
		}
		if candidate.Title == "" {
			candidate.Title = "Codex session " + shortRecoveryID(candidate.SessionID)
		}
		candidates = append(candidates, candidate)
	}

	if err := s.populateWorktreeRecoveryAvailability(ctx, root, candidates); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].LastActivity.Equal(candidates[j].LastActivity) {
			if candidates[i].WorktreePath == candidates[j].WorktreePath {
				return candidates[i].SessionID < candidates[j].SessionID
			}
			return candidates[i].WorktreePath < candidates[j].WorktreePath
		}
		return candidates[i].LastActivity.After(candidates[j].LastActivity)
	})
	return candidates, nil
}

func (s *Service) RestoreWorktreeSession(ctx context.Context, req RestoreWorktreeSessionRequest) (RestoreWorktreeSessionResult, error) {
	projectPath := filepath.Clean(strings.TrimSpace(req.ProjectPath))
	sessionID := strings.TrimSpace(req.SessionID)
	if projectPath == "" || projectPath == "." {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("project path is required")
	}
	if sessionID == "" {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("session id is required")
	}

	candidates, err := s.ListRestorableWorktreeSessions(ctx, projectPath)
	if err != nil {
		return RestoreWorktreeSessionResult{}, err
	}
	var candidate RestorableWorktreeSession
	for _, item := range candidates {
		if item.SessionID == sessionID {
			candidate = item
			break
		}
	}
	if candidate.SessionID == "" {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("deleted worktree session %s is no longer available", shortRecoveryID(sessionID))
	}
	if !candidate.Ready {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("cannot restore session %s: %s", shortRecoveryID(sessionID), firstNonEmptyTrimmed(candidate.BlockReason, "recovery preflight failed"))
	}

	root := filepath.Clean(candidate.RootProjectPath)
	unlockCreate, err := s.worktreeCreateLocks.LockContext(ctx, root)
	if err != nil {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("wait for existing worktree creation in %s: %w", root, err)
	}
	defer unlockCreate()
	unlockGitWrite, err := s.lockGitWrite(ctx, root)
	if err != nil {
		return RestoreWorktreeSessionResult{}, err
	}
	defer unlockGitWrite()

	refreshed := []RestorableWorktreeSession{candidate}
	if err := s.populateWorktreeRecoveryAvailability(ctx, root, refreshed); err != nil {
		return RestoreWorktreeSessionResult{}, err
	}
	candidate = refreshed[0]
	if !candidate.Ready {
		return RestoreWorktreeSessionResult{}, fmt.Errorf("cannot restore session %s: %s", shortRecoveryID(sessionID), firstNonEmptyTrimmed(candidate.BlockReason, "recovery preflight failed"))
	}

	result := RestoreWorktreeSessionResult{
		RootProjectPath: root,
		WorktreePath:    candidate.WorktreePath,
		SessionID:       candidate.SessionID,
		BranchName:      candidate.BranchName,
		ParentBranch:    candidate.ParentBranch,
	}
	if err := os.MkdirAll(filepath.Dir(candidate.WorktreePath), 0o755); err != nil {
		return result, fmt.Errorf("create restored worktree parent directory: %w", err)
	}
	if err := gitRestoreWorktree(ctx, root, candidate); err != nil {
		return result, err
	}
	result.WorktreeCreated = true

	summaries, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return result, fmt.Errorf("load project metadata after restoring worktree: %w", err)
	}
	rootSummary := summaries[root]
	existing := summaries[candidate.WorktreePath]
	_, err = s.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:             filepath.Dir(candidate.WorktreePath),
		Name:                   filepath.Base(candidate.WorktreePath),
		PreferredSessionSource: model.SessionSourceCodex,
		CategoryID:             rootSummary.CategoryID,
		CategoryExplicit:       existing.Path == "",
	})
	if err != nil {
		return result, fmt.Errorf("track restored worktree in Little Control Room: %w", err)
	}
	result.ProjectTracked = true
	if err := s.store.SetWorktreeInitialBranch(ctx, candidate.WorktreePath, candidate.BranchName); err != nil {
		return result, fmt.Errorf("record restored worktree branch: %w", err)
	}
	if candidate.ParentBranch != "" {
		if err := s.store.SetWorktreeParentBranch(ctx, candidate.WorktreePath, candidate.ParentBranch); err != nil {
			return result, fmt.Errorf("record restored worktree parent branch: %w", err)
		}
	}
	if candidate.OriginTodoID > 0 {
		if err := s.store.SetWorktreeOriginTodoID(ctx, candidate.WorktreePath, candidate.OriginTodoID); err != nil {
			return result, fmt.Errorf("restore worktree TODO link: %w", err)
		}
	}
	if existing.RunCommand == "" && rootSummary.RunCommand != "" {
		if err := s.store.SetRunCommand(ctx, candidate.WorktreePath, rootSummary.RunCommand); err != nil {
			return result, fmt.Errorf("inherit restored worktree run command: %w", err)
		}
	}
	if err := s.RefreshProjectStatus(ctx, candidate.WorktreePath); err != nil {
		return result, fmt.Errorf("refresh restored worktree: %w", err)
	}

	prepResult, err := worktreeprep.Prepare(ctx, root, candidate.WorktreePath, "")
	result.PrepProfile = strings.TrimSpace(prepResult.Profile)
	for _, prepared := range prepResult.Prepared {
		result.PreparedPaths = append(result.PreparedPaths, prepared.Path)
	}
	if err != nil {
		return result, fmt.Errorf("restored worktree at %s but failed to prepare it: %w", candidate.WorktreePath, err)
	}

	now := time.Now()
	if candidate.OriginTodoID > 0 {
		if todo, todoErr := s.store.GetTodo(ctx, candidate.OriginTodoID); todoErr == nil && !todo.Done {
			if err := s.MarkTodoWorkStarted(ctx, candidate.WorktreePath, candidate.OriginTodoID, model.SessionSourceCodex, candidate.SessionID, now); err != nil {
				return result, fmt.Errorf("restore TODO work session: %w", err)
			}
		} else if todoErr != nil && !errors.Is(todoErr, sql.ErrNoRows) {
			return result, fmt.Errorf("load restored worktree TODO: %w", todoErr)
		}
	}
	payload := map[string]string{
		"action":       "restore_worktree_session",
		"root_path":    root,
		"branch_name":  candidate.BranchName,
		"session_id":   candidate.SessionID,
		"prep_profile": result.PrepProfile,
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: candidate.WorktreePath, Payload: payload})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: candidate.WorktreePath,
		Type:        string(events.ActionApplied),
		Payload:     fmt.Sprintf("restore_worktree_session root=%s branch=%s session=%s", root, candidate.BranchName, candidate.SessionID),
	})
	return result, nil
}

func (s *Service) worktreeRecoveryRoot(ctx context.Context, projectPath string) (string, model.ProjectSummary, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return "", model.ProjectSummary{}, fmt.Errorf("project path is required")
	}
	selected, err := s.store.GetProjectSummary(ctx, projectPath, true)
	if err != nil {
		return "", model.ProjectSummary{}, err
	}
	root := projectPath
	if selected.WorktreeKind == model.WorktreeKindLinked && strings.TrimSpace(selected.WorktreeRootPath) != "" {
		root = filepath.Clean(selected.WorktreeRootPath)
	}
	rootSummary, err := s.store.GetProjectSummary(ctx, root, true)
	if err != nil {
		return "", model.ProjectSummary{}, fmt.Errorf("load repository root: %w", err)
	}
	if !projectPathExists(root) || !projectIsGitRepo(root) {
		return "", model.ProjectSummary{}, fmt.Errorf("worktree recovery requires the repository root on disk: %s", root)
	}
	return root, rootSummary, nil
}

func restorableWorktreeBelongsToRoot(candidatePath, rootPath string, historical model.ProjectSummary) bool {
	candidatePath = filepath.Clean(strings.TrimSpace(candidatePath))
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if candidatePath == "" || candidatePath == "." || rootPath == "" || rootPath == "." || samePath(candidatePath, rootPath) {
		return false
	}
	if historical.WorktreeKind == model.WorktreeKindLinked && samePath(historical.WorktreeRootPath, rootPath) {
		return true
	}
	return samePath(filepath.Dir(candidatePath), filepath.Dir(rootPath)) &&
		strings.HasPrefix(filepath.Base(candidatePath), filepath.Base(rootPath)+"--")
}

func mergeRestorableWorktreeSession(existing, candidate RestorableWorktreeSession) RestorableWorktreeSession {
	if existing.SessionID == "" {
		return candidate
	}
	merged := existing
	if merged.RootProjectPath == "" {
		merged.RootProjectPath = candidate.RootProjectPath
	}
	if merged.WorktreePath == "" {
		merged.WorktreePath = candidate.WorktreePath
	}
	if merged.WorktreeName == "" {
		merged.WorktreeName = candidate.WorktreeName
	}
	if merged.SessionFile == "" {
		merged.SessionFile = candidate.SessionFile
	}
	if merged.BranchName == "" {
		merged.BranchName = candidate.BranchName
	}
	if merged.ParentBranch == "" {
		merged.ParentBranch = candidate.ParentBranch
	}
	if merged.GitSHA == "" {
		merged.GitSHA = candidate.GitSHA
	}
	if merged.Title == "" {
		merged.Title = candidate.Title
	}
	if merged.Summary == "" {
		merged.Summary = candidate.Summary
	}
	if merged.StartedAt.IsZero() {
		merged.StartedAt = candidate.StartedAt
	}
	if candidate.LastActivity.After(merged.LastActivity) {
		merged.LastActivity = candidate.LastActivity
	}
	if merged.OriginTodoID == 0 {
		merged.OriginTodoID = candidate.OriginTodoID
	}
	merged.StoredMetadata = merged.StoredMetadata || candidate.StoredMetadata
	return merged
}

func (s *Service) populateWorktreeRecoveryAvailability(ctx context.Context, root string, candidates []RestorableWorktreeSession) error {
	worktrees, err := s.gitWorktreeListReader(ctx, root)
	if err != nil {
		return fmt.Errorf("inspect repository worktrees for recovery: %w", err)
	}
	branches, err := gitLocalBranchSet(ctx, root)
	if err != nil {
		return err
	}
	for index := range candidates {
		candidate := &candidates[index]
		candidate.Ready = false
		candidate.BlockReason = ""
		candidate.BranchExists = branches[candidate.BranchName]
		candidate.RecreateBranch = false
		candidate.StaleRegistration = false

		if projectPathExists(candidate.WorktreePath) {
			candidate.BlockReason = "the recorded worktree path already exists"
			continue
		}
		if candidate.BranchName == "" {
			candidate.BlockReason = "the session has no recorded Git branch"
			continue
		}
		if err := gitValidateBranchName(ctx, root, candidate.BranchName); err != nil {
			candidate.BlockReason = "the recorded Git branch name is invalid"
			continue
		}

		branchInUsePath := ""
		for _, worktree := range worktrees {
			if sameWorktreeRecoveryPath(worktree.Path, candidate.WorktreePath) {
				candidate.StaleRegistration = true
				if strings.TrimSpace(worktree.LockedReason) != "" {
					candidate.BlockReason = "the stale Git worktree registration is locked"
					break
				}
				if worktree.Branch != "" && worktree.Branch != candidate.BranchName {
					candidate.BlockReason = "the stale Git worktree registration points to branch " + worktree.Branch
					break
				}
				continue
			}
			if worktree.Branch == candidate.BranchName {
				branchInUsePath = worktree.Path
			}
		}
		if candidate.BlockReason != "" {
			continue
		}
		if branchInUsePath != "" {
			candidate.BlockReason = "branch " + candidate.BranchName + " is already checked out at " + branchInUsePath
			continue
		}
		if !candidate.BranchExists {
			if candidate.GitSHA == "" || !gitObjectID(candidate.GitSHA) {
				candidate.BlockReason = "the branch was deleted and the session has no usable Git commit fallback"
				continue
			}
			commitExists, commitErr := gitCommitExists(ctx, root, candidate.GitSHA)
			if commitErr != nil {
				return commitErr
			}
			if !commitExists {
				candidate.BlockReason = "the recorded Git commit is no longer available in this repository"
				continue
			}
			candidate.RecreateBranch = true
		}
		candidate.Ready = true
	}
	return nil
}

func gitLocalBranchSet(ctx context.Context, repoPath string) (map[string]bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, gitCommandError("list local branches for worktree recovery", err, out)
	}
	branches := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if branch := strings.TrimSpace(line); branch != "" {
			branches[branch] = true
		}
	}
	return branches, nil
}

func gitValidateBranchName(ctx context.Context, repoPath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "check-ref-format", "--branch", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return gitCommandError("validate restored worktree branch", err, out)
	}
	return nil
}

func gitObjectID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func gitCommitExists(ctx context.Context, repoPath, commit string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "--quiet", strings.TrimSpace(commit)+"^{commit}")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check restored worktree commit: %w", err)
	}
	return true, nil
}

func gitRestoreWorktree(ctx context.Context, root string, candidate RestorableWorktreeSession) error {
	if err := gitlock.CheckIndexLock(ctx, root); err != nil {
		return err
	}
	args := []string{"-C", root, "worktree", "add"}
	if candidate.StaleRegistration {
		args = append(args, "--force")
	}
	if candidate.RecreateBranch {
		args = append(args, "-b", candidate.BranchName, "--", candidate.WorktreePath, candidate.GitSHA)
	} else {
		args = append(args, "--", candidate.WorktreePath, candidate.BranchName)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitCommandError(fmt.Sprintf("restore git worktree %s on %s", candidate.WorktreePath, candidate.BranchName), err, out)
	}
	return nil
}

func sameWorktreeRecoveryPath(left, right string) bool {
	return comparableWorktreeRecoveryPath(left) == comparableWorktreeRecoveryPath(right)
}

func comparableWorktreeRecoveryPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(path)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(filepath.Clean(resolvedParent), filepath.Base(path))
	}
	return path
}

func shortRecoveryID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
