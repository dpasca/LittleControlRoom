package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/worktreeprep"
)

func (s *Service) prepareAgentTaskCheckout(ctx context.Context, origin string, input *model.CreateAgentTaskInput) error {
	unlock, err := s.lockGitWrite(ctx, origin)
	if err != nil {
		return err
	}
	defer unlock()
	head, err := repositoryGit(ctx, origin, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("prepare isolated task checkout: %w", err)
	}
	branch := "agent-task/" + strings.TrimPrefix(filepath.Base(input.WorkspacePath), agentTaskWorkspacePrefix)
	if _, err := repositoryGit(ctx, origin, "worktree", "add", "-b", branch, "--", input.WorkspacePath, strings.TrimSpace(string(head))); err != nil {
		return fmt.Errorf("create isolated task worktree: %w", err)
	}
	input.Repository = model.AgentTaskRepository{
		Write: input.Repository.Write, Isolated: true, Root: canonicalRepositoryPath(input.WorkspacePath),
		InitialBranch: branch, State: "isolated",
	}
	if input.Repository.Write {
		input.Repository.State = "pending"
	}
	if _, err := worktreeprep.Prepare(ctx, origin, input.WorkspacePath, ""); err != nil {
		return fmt.Errorf("prepare task worktree %s: %w", input.WorkspacePath, err)
	}
	return nil
}

// Use the workspace's own administrative entry, even if the originating task
// was removed first. A linked checkout must be unregistered, not just unlinked.
func (s *Service) removeAgentTaskWorkspace(ctx context.Context, workspace, origin string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if info, err := os.Lstat(filepath.Join(workspace, ".git")); err == nil && info.Mode().IsRegular() {
		if _, err := repositoryGit(ctx, workspace, "worktree", "remove", "--force", "--force", "--", workspace); err != nil {
			return fmt.Errorf("remove task worktree %s: %w", workspace, err)
		}
		if origin != "" {
			return worktreeprep.PruneSubmoduleWorktrees(ctx, origin)
		}
		return nil
	}
	return os.RemoveAll(workspace)
}

func (s *Service) refreshAgentTaskWorkspace(ctx context.Context, task model.AgentTask) model.AgentTask {
	if _, err := os.Stat(task.WorkspacePath); err != nil {
		task.WorkspaceGit.Error = "Workspace unavailable: " + err.Error()
		return task
	}
	if _, err := os.Lstat(filepath.Join(task.WorkspacePath, ".git")); err != nil {
		if task.Repository.InitialBranch != "" {
			task.WorkspaceGit.Error = "Isolated workspace is missing its Git metadata"
		}
		return task
	}
	status, err := scanner.ReadGitRepoStatus(ctx, task.WorkspacePath)
	if err != nil {
		task.WorkspaceGit.Error = err.Error()
		return task
	}
	task.WorkspaceGit.Branch, task.WorkspaceGit.Dirty = status.Branch, status.Dirty
	for _, change := range status.Changes {
		if change.Kind == scanner.GitChangeUnmerged {
			task.WorkspaceGit.Conflict = true
		}
	}
	return task
}
