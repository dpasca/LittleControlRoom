package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/gitops"
)

type resolvedSubmodule struct {
	Path string
	Hash string
}

func (s *Service) ResolveSubmodulesAndPrepareCommit(ctx context.Context, projectPath string, intent GitActionIntent, messageOverride string) (CommitPreview, error) {
	if !projectPathExists(projectPath) {
		return CommitPreview{}, fmt.Errorf("project not found on disk: %s", projectPath)
	}

	parentStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return CommitPreview{}, err
	}
	blocked := blockedSubmodulePaths(parentStatus.Changes)
	if len(blocked) == 0 {
		return s.PrepareCommit(ctx, projectPath, intent, messageOverride)
	}

	hadStagedChanges := len(parentStatus.StagedChanges()) > 0
	resolved := make([]resolvedSubmodule, 0, len(blocked))
	for _, relPath := range blocked {
		childPath := filepath.Join(projectPath, relPath)
		childResolved, resolveErr := s.resolveSubmoduleRepoAndPush(ctx, childPath, relPath, map[string]struct{}{})
		if resolveErr != nil {
			return CommitPreview{}, fmt.Errorf("resolve submodule %s: %w", relPath, resolveErr)
		}
		resolved = append(resolved, childResolved...)
	}

	if hadStagedChanges {
		if err := gitops.StagePaths(ctx, projectPath, blocked); err != nil {
			return CommitPreview{}, err
		}
	}

	preview, err := s.PrepareCommit(ctx, projectPath, intent, messageOverride)
	if err != nil {
		return CommitPreview{}, err
	}
	if note := formatResolvedSubmoduleWarning(resolved); note != "" {
		preview.Warnings = append([]string{note}, preview.Warnings...)
	}
	return preview, nil
}

func (s *Service) resolveSubmoduleRepoAndPush(ctx context.Context, repoPath, displayPath string, seen map[string]struct{}) ([]resolvedSubmodule, error) {
	cleanPath := filepath.Clean(repoPath)
	if _, ok := seen[cleanPath]; ok {
		return nil, fmt.Errorf("submodule recursion cycle detected at %s", displayPath)
	}
	seen[cleanPath] = struct{}{}
	defer delete(seen, cleanPath)

	status, err := s.gitRepoStatusReader(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	resolved := []resolvedSubmodule{}
	for _, relPath := range blockedSubmodulePaths(status.Changes) {
		childPath := filepath.Join(repoPath, relPath)
		childDisplay := filepath.ToSlash(filepath.Join(displayPath, relPath))
		childResolved, resolveErr := s.resolveSubmoduleRepoAndPush(ctx, childPath, childDisplay, seen)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved = append(resolved, childResolved...)
	}

	status, err = s.gitRepoStatusReader(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if blocked := blockedSubmodulePaths(status.Changes); len(blocked) > 0 {
		return nil, SubmoduleAttentionError{
			ProjectPath: repoPath,
			ProjectName: filepath.Base(repoPath),
			Branch:      status.Branch,
			Submodules:  blocked,
		}
	}

	included := filterParentCommitEligible(status.Changes)
	if len(included) == 0 {
		return resolved, nil
	}

	canPush, pushWarning := pushAvailability(status)
	if !canPush {
		return nil, fmt.Errorf("submodule %s cannot be auto-pushed: %s", displayPath, strings.TrimSpace(pushWarning))
	}

	if err := gitops.StageAll(ctx, repoPath); err != nil {
		return nil, err
	}

	status, err = s.gitRepoStatusReader(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	staged := filterParentCommitEligible(status.StagedChanges())
	if len(staged) == 0 {
		return nil, fmt.Errorf("submodule %s had no stageable changes after staging", displayPath)
	}

	message := fallbackCommitMessage(filepath.Base(displayPath), summarizeCommitFiles(staged))
	hash, err := gitops.Commit(ctx, repoPath, message)
	if err != nil {
		return nil, err
	}
	if err := gitops.Push(ctx, repoPath); err != nil {
		return nil, fmt.Errorf("push submodule %s: %w", displayPath, err)
	}

	resolved = append(resolved, resolvedSubmodule{Path: displayPath, Hash: hash})
	return resolved, nil
}

func formatResolvedSubmoduleWarning(resolved []resolvedSubmodule) string {
	if len(resolved) == 0 {
		return ""
	}
	if len(resolved) == 1 {
		return fmt.Sprintf("Resolved submodule %s at %s and pushed it before preparing this parent commit.", resolved[0].Path, resolved[0].Hash)
	}
	parts := make([]string, 0, len(resolved))
	for _, item := range resolved {
		parts = append(parts, fmt.Sprintf("%s %s", item.Path, item.Hash))
	}
	return fmt.Sprintf("Resolved and pushed %d submodules before preparing this parent commit: %s.", len(resolved), strings.Join(parts, ", "))
}
