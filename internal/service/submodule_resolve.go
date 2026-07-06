package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"lcroom/internal/gitops"
	"lcroom/internal/scanner"
)

type resolvedSubmodule struct {
	Path       string
	Hash       string
	PushedOnly bool
	Branch     string
}

func (s *Service) ResolveSubmodulesAndPrepareCommit(ctx context.Context, projectPath string, intent GitActionIntent, messageOverride string) (CommitPreview, error) {
	if !projectPathExists(projectPath) {
		return CommitPreview{}, fmt.Errorf("project not found on disk: %s", projectPath)
	}

	parentStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return CommitPreview{}, err
	}
	_, _, attentionPaths := submoduleAttentionPaths(parentStatus)
	if len(attentionPaths) == 0 {
		return s.PrepareCommit(ctx, projectPath, intent, messageOverride)
	}

	hadStagedChanges := len(parentStatus.StagedChanges()) > 0
	resolved := make([]resolvedSubmodule, 0, len(attentionPaths))
	for _, relPath := range attentionPaths {
		childPath := filepath.Join(projectPath, relPath)
		childResolved, resolveErr := s.resolveSubmoduleRepoAndPush(ctx, childPath, relPath, parentStatus.Branch, map[string]struct{}{})
		if resolveErr != nil {
			return CommitPreview{}, fmt.Errorf("resolve submodule %s: %w", relPath, resolveErr)
		}
		resolved = append(resolved, childResolved...)
	}

	if hadStagedChanges {
		if err := gitops.StagePaths(ctx, projectPath, attentionPaths); err != nil {
			return CommitPreview{}, err
		}
	}

	preview, err := s.PrepareCommit(ctx, projectPath, intent, messageOverride)
	if err != nil {
		var noChangesErr NoChangesToCommitError
		if errors.As(err, &noChangesErr) && len(resolved) > 0 {
			return CommitPreview{}, SubmoduleResolvedNoParentChangesError{
				ProjectPath: projectPath,
				ProjectName: noChangesErr.ProjectName,
				Branch:      noChangesErr.Branch,
				Summary:     formatResolvedSubmoduleWarning(resolved),
			}
		}
		return CommitPreview{}, err
	}
	if note := formatResolvedSubmoduleWarning(resolved); note != "" {
		preview.Warnings = append([]string{note}, preview.Warnings...)
	}
	return preview, nil
}

type submodulePushPlan struct {
	SetUpstream bool
	Branch      string
}

func (s *Service) ensureMergeBackSubmodulesPublished(ctx context.Context, projectPath, parentBranch string, status scanner.GitRepoStatus) ([]resolvedSubmodule, error) {
	resolved := []resolvedSubmodule{}
	seen := map[string]struct{}{}
	for _, submodule := range status.Submodules {
		relPath := strings.TrimSpace(submodule.Path)
		if relPath == "" {
			continue
		}
		childPath := filepath.Join(projectPath, filepath.FromSlash(relPath))
		childResolved, err := s.resolveSubmoduleRepoAndPush(ctx, childPath, relPath, parentBranch, seen)
		if err != nil {
			return resolved, fmt.Errorf("publish merge-back submodule %s: %w", relPath, err)
		}
		resolved = append(resolved, childResolved...)
	}
	return resolved, nil
}

func (s *Service) resolveSubmoduleRepoAndPush(ctx context.Context, repoPath, displayPath, parentBranch string, seen map[string]struct{}) ([]resolvedSubmodule, error) {
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
		childResolved, resolveErr := s.resolveSubmoduleRepoAndPush(ctx, childPath, childDisplay, parentBranch, seen)
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
		needsDetachedPublish, publishErr := detachedSubmoduleHeadNeedsPublish(ctx, repoPath, status)
		if publishErr != nil {
			return nil, fmt.Errorf("check whether detached submodule %s needs publishing: %w", displayPath, publishErr)
		}
		if status.Ahead > 0 || needsSubmoduleUpstream(status) || needsDetachedPublish {
			pushPlan, pushErr := s.ensureSubmodulePushPlan(ctx, repoPath, displayPath, parentBranch, status)
			if pushErr != nil {
				return nil, fmt.Errorf("submodule %s cannot be auto-pushed: %w", displayPath, pushErr)
			}
			if err := pushSubmodule(ctx, repoPath, pushPlan); err != nil {
				return nil, fmt.Errorf("push submodule %s: %w", displayPath, err)
			}
			hash, _ := gitHeadShort(ctx, repoPath)
			resolved = append(resolved, resolvedSubmodule{Path: displayPath, Hash: hash, PushedOnly: true, Branch: pushPlan.Branch})
		}
		return resolved, nil
	}

	pushPlan, pushErr := s.ensureSubmodulePushPlan(ctx, repoPath, displayPath, parentBranch, status)
	if pushErr != nil {
		return nil, fmt.Errorf("submodule %s cannot be auto-pushed: %w", displayPath, pushErr)
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
	if err := pushSubmodule(ctx, repoPath, pushPlan); err != nil {
		return nil, fmt.Errorf("push submodule %s: %w", displayPath, err)
	}

	resolved = append(resolved, resolvedSubmodule{Path: displayPath, Hash: hash, Branch: pushPlan.Branch})
	return resolved, nil
}

func formatResolvedSubmoduleWarning(resolved []resolvedSubmodule) string {
	if len(resolved) == 0 {
		return ""
	}
	if len(resolved) == 1 {
		if resolved[0].PushedOnly {
			return fmt.Sprintf("Pushed existing commits from submodule %s%s%s; no parent commit was needed for that submodule.", resolved[0].Path, resolvedHashSuffix(resolved[0].Hash), resolvedBranchSuffix(resolved[0].Branch))
		}
		return fmt.Sprintf("Resolved submodule %s%s%s and pushed it before preparing this parent commit.", resolved[0].Path, resolvedHashSuffix(resolved[0].Hash), resolvedBranchSuffix(resolved[0].Branch))
	}
	parts := make([]string, 0, len(resolved))
	for _, item := range resolved {
		action := "committed"
		if item.PushedOnly {
			action = "pushed"
		}
		parts = append(parts, fmt.Sprintf("%s %s%s%s", item.Path, action, resolvedHashSuffix(item.Hash), resolvedBranchSuffix(item.Branch)))
	}
	return fmt.Sprintf("Resolved and pushed %d submodules before preparing this parent commit: %s.", len(resolved), strings.Join(parts, ", "))
}

func resolvedHashSuffix(hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return ""
	}
	return " at " + hash
}

func resolvedBranchSuffix(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	return " on branch " + branch
}

func gitHeadShort(ctx context.Context, repoPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Service) ensureSubmodulePushPlan(ctx context.Context, repoPath, displayPath, parentBranch string, status scanner.GitRepoStatus) (submodulePushPlan, error) {
	if status.HasUpstream {
		canPush, pushWarning := pushAvailability(status)
		if !canPush {
			return submodulePushPlan{}, errors.New(strings.TrimSpace(pushWarning))
		}
		return submodulePushPlan{Branch: cleanResolvedBranchName(status.Branch)}, nil
	}
	if !status.HasRemote {
		return submodulePushPlan{}, errors.New("Commit & push unavailable: no remote is configured.")
	}

	branch := cleanResolvedBranchName(status.Branch)
	if branch == "" {
		head, err := gitHeadShort(ctx, repoPath)
		if err != nil {
			return submodulePushPlan{}, fmt.Errorf("read submodule HEAD before branch creation: %w", err)
		}
		branch = submoduleResolutionBranchName(parentBranch, displayPath, head)
		selected, err := switchSubmoduleToResolutionBranch(ctx, repoPath, branch)
		if err != nil {
			return submodulePushPlan{}, err
		}
		branch = selected
	}
	return submodulePushPlan{SetUpstream: true, Branch: branch}, nil
}

func pushSubmodule(ctx context.Context, repoPath string, plan submodulePushPlan) error {
	if plan.SetUpstream {
		return gitops.PushSetUpstream(ctx, repoPath, "origin")
	}
	return gitops.Push(ctx, repoPath)
}

func needsSubmoduleUpstream(status scanner.GitRepoStatus) bool {
	return status.HasRemote && !status.HasUpstream && cleanResolvedBranchName(status.Branch) != ""
}

func detachedSubmoduleHeadNeedsPublish(ctx context.Context, repoPath string, status scanner.GitRepoStatus) (bool, error) {
	if !status.HasRemote || status.HasUpstream || cleanResolvedBranchName(status.Branch) != "" {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "for-each-ref", "--contains", "HEAD", "--format=%(refname)", "refs/remotes", "refs/tags")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list refs containing detached HEAD in %s: %w: %s", repoPath, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) == "", nil
}

func cleanResolvedBranchName(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "(detached)" {
		return ""
	}
	return branch
}

func submoduleResolutionBranchName(parentBranch, displayPath, head string) string {
	parent := sanitizeBranchComponent(parentBranch)
	if parent == "" {
		parent = "detached-parent"
	}
	submodule := sanitizeBranchComponent(displayPath)
	if submodule == "" {
		submodule = "submodule"
	}
	head = sanitizeBranchComponent(head)
	if head != "" {
		submodule += "-" + head
	}
	return "lcroom/" + parent + "/" + submodule
}

func sanitizeBranchComponent(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		switch r {
		case '.', '_', '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-._")
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	return strings.Trim(out, "-._")
}

func switchSubmoduleToResolutionBranch(ctx context.Context, repoPath, baseBranch string) (string, error) {
	head, err := gitCommitHash(ctx, repoPath, "HEAD")
	if err != nil {
		return "", err
	}
	for i := 0; i < 5; i++ {
		branch := baseBranch
		if i > 0 {
			branch = fmt.Sprintf("%s-%d", baseBranch, i+1)
		}
		existingHead, exists, err := gitLocalBranchCommit(ctx, repoPath, branch)
		if err != nil {
			return "", err
		}
		if exists {
			if existingHead != head {
				continue
			}
			if err := gitSwitchBranch(ctx, repoPath, branch); err != nil {
				return "", err
			}
			return branch, nil
		}
		if err := gitCreateBranch(ctx, repoPath, branch); err != nil {
			return "", err
		}
		return branch, nil
	}
	return "", fmt.Errorf("could not find available LCR submodule branch for %s", baseBranch)
}

func gitLocalBranchCommit(ctx context.Context, repoPath, branch string) (string, bool, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("check local branch %s in %s: %w", branch, repoPath, err)
	}
	return strings.TrimSpace(string(out)), true, nil
}

func gitSwitchBranch(ctx context.Context, repoPath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "switch", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("switch submodule %s to branch %s: %w: %s", repoPath, branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitCreateBranch(ctx context.Context, repoPath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "switch", "-c", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create submodule branch %s in %s: %w: %s", branch, repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
