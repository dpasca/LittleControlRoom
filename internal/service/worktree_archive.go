package service

import (
	"context"
	"fmt"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/worktreeprep"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiveWorktree is the separate preservation operation. Normal deletion never
// enters this pipeline or requires an archive to verify successfully.
func (s *Service) ArchiveWorktree(ctx context.Context, projectPath string, force bool) error {
	return s.archiveWorktree(ctx, projectPath, force, false)
}

func (s *Service) archiveWorktree(ctx context.Context, projectPath string, force, cleanupRetained bool) (resultErr error) {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	unlockMutation, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlockMutation()

	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if !filepath.IsAbs(projectPath) || projectPath == string(filepath.Separator) {
		return fmt.Errorf("an absolute linked worktree path is required")
	}
	rootPath, kind, presentOnDisk, err := s.removeWorktreeTarget(ctx, projectPath)
	if err != nil {
		return err
	}
	if kind != model.WorktreeKindLinked {
		return fmt.Errorf("only linked worktrees can be removed from Little Control Room")
	}
	if strings.TrimSpace(rootPath) == "" {
		return fmt.Errorf("worktree root is unavailable for %s", projectPath)
	}
	unlockGitWrite, err := s.lockGitWrite(ctx, rootPath)
	if err != nil {
		return err
	}
	defer unlockGitWrite()
	removalStarted := false
	defer func() {
		if resultErr == nil {
			return
		}
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		registration, err := linkedWorktreeRegistrationWithReader(failureCtx, rootPath, kind, projectPath, s.gitWorktreeListReader)
		orphaned := err == nil && (registration == linkedWorktreeRegistrationAbsent || registration == linkedWorktreeRegistrationPrunable)
		if orphaned || removalStarted {
			if _, err := os.Lstat(projectPath); !os.IsNotExist(err) {
				resultErr = s.recordRetainedRemoval(failureCtx, projectPath, resultErr, orphaned)
			}
		}
	}()
	registration, registrationErr := linkedWorktreeRegistrationWithReader(ctx, rootPath, kind, projectPath, s.gitWorktreeListReader)
	if registrationErr != nil {
		registration = linkedWorktreeRegistrationUnknown
	}
	// Capture provenance before pruning or removing any administrative record.
	summary := s.residualWorktreeSummary(ctx, projectPath)
	expectedCommit := ""
	if registration == linkedWorktreeRegistrationLive {
		expectedCommit, err = gitCommitHash(ctx, projectPath, "HEAD")
		if err != nil {
			return err
		}
		branch, err := removalGitOutput(ctx, projectPath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return err
		}
		if branch == "HEAD" {
			branch = ""
		}
		summary.RepoBranch = branch
	} else {
		expectedCommit = s.retainedRemovalCommit(ctx, projectPath, rootPath, summary)
		if registration == linkedWorktreeRegistrationPrunable {
			if worktrees, err := scanner.ListGitWorktrees(ctx, rootPath); err == nil {
				for _, worktree := range worktrees {
					if samePath(worktree.Path, projectPath) && worktree.Head != "" {
						expectedCommit = worktree.Head
						summary.RepoBranch = worktree.Branch
					}
				}
			}
		}
	}
	if err := s.saveRemovalReceipt(ctx, worktreeRemovalPlan{RootPath: rootPath, Path: projectPath, Commit: expectedCommit, Branch: summary.RepoBranch}); err != nil {
		return fmt.Errorf("preserve worktree removal provenance: %w", err)
	}
	recovered := false
	if _, statErr := os.Lstat(filepath.Join(s.recoveryBase(), recoveryPathKey(projectPath))); statErr == nil {
		if presentOnDisk && !force && s.gitRepoStatusReader != nil && registration == linkedWorktreeRegistrationLive {
			status, statusErr := s.gitRepoStatusReader(ctx, projectPath)
			if statusErr != nil {
				return statusErr
			}
			if status.Dirty {
				return fmt.Errorf("worktree is dirty; commit or discard changes before retrying cleanup")
			}
		}
		recovered, err = s.recoverAndRemove(ctx, rootPath, projectPath)
		if err != nil {
			return err
		}
		presentOnDisk = false
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect worktree recovery before removal: %w", statErr)
	}
	missingCheckoutReconciled := false
	if !recovered && registration == linkedWorktreeRegistrationPrunable {
		// Git still remembers this path, but its checkout metadata is already
		// gone. Pruning removes only Git's stale administrative record; the
		// directory may now be an ancestor of an independently live nested
		// worktree, so leave its contents untouched.
		if err := checkPrunableWorktreeStores(ctx, rootPath); err != nil {
			return err
		}
		if presentOnDisk {
			if err := checkRemovalProcesses(ctx, projectPath); err != nil {
				return err
			}
		}
		if err := gitWorktreePrune(ctx, rootPath); err != nil {
			return fmt.Errorf("prune missing worktree registration for %s: %w", projectPath, err)
		}
		afterPrune, inspectErr := linkedWorktreeRegistrationWithReader(ctx, rootPath, kind, projectPath, s.gitWorktreeListReader)
		if inspectErr != nil {
			return fmt.Errorf("verify pruned worktree registration for %s: %w", projectPath, inspectErr)
		}
		if afterPrune != linkedWorktreeRegistrationAbsent {
			return fmt.Errorf("Git still registers the missing worktree %s after pruning", projectPath)
		}
		_, pathErr := os.Lstat(projectPath)
		presentOnDisk = !os.IsNotExist(pathErr)
		missingCheckoutReconciled = true
	}
	staleLinkedWorktree := registration == linkedWorktreeRegistrationAbsent || registration == linkedWorktreeRegistrationPrunable
	var removalPlan *worktreeRemovalPlan
	residualDirectoryRemoved := recovered
	if presentOnDisk && staleLinkedWorktree {
		summary := s.residualWorktreeSummary(ctx, projectPath)
		inspection, inspectErr := s.inspectResidualWorktreeDirectory(ctx, rootPath, projectPath, summary, expectedCommit)
		if inspectErr != nil {
			return fmt.Errorf("inspect orphaned worktree directory before cleanup: %w", inspectErr)
		}
		if !inspection.Safe {
			return fmt.Errorf("Git no longer tracks this worktree, but Little Control Room could not verify the remaining folder for safe cleanup: %s; Little Control Room left the folder untouched: %s", inspection.Reason, projectPath)
		}
		if inspection.Kind == ResidualWorktreeCleanupOwned {
			inspection.Plan.Branch = summary.RepoBranch
			removalPlan = &inspection.Plan
			if !cleanupRetained {
				return fmt.Errorf("owned nested worktrees or ignored output remain; use the explicit retained-folder cleanup after reviewing its size and contents")
			}
			if err := checkRemovalProcesses(ctx, projectPath); err != nil {
				return err
			}
			if err := s.saveRemovalReceipt(ctx, inspection.Plan); err != nil {
				return err
			}
			removalStarted = true
			if err := removeOwnedChildren(ctx, inspection.Plan); err != nil {
				return err
			}
		}
		if err := removeInspectedResidualWorktreeDirectory(ctx, inspection, projectPath); err != nil {
			return fmt.Errorf("remove verified worktree residue: %w", err)
		}
		presentOnDisk = false
		residualDirectoryRemoved = true
	}
	allowSubmoduleForceFallback := false
	if presentOnDisk && !residualDirectoryRemoved && !missingCheckoutReconciled && !force && s.gitRepoStatusReader != nil {
		status, err := s.gitRepoStatusReader(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("read git status before removing worktree: %w", err)
		}
		if status.Dirty {
			return fmt.Errorf("worktree is dirty; commit or discard changes before removing it")
		}
		allowSubmoduleForceFallback = true
	}
	if presentOnDisk && !residualDirectoryRemoved && !missingCheckoutReconciled {
		if err := checkRemovalProcesses(ctx, projectPath); err != nil {
			return err
		}
		recovered, err = s.recoverAndRemove(ctx, rootPath, projectPath)
		if err != nil {
			return err
		}
		if recovered {
			residualDirectoryRemoved = true
		}
		if !recovered {
			if _, err := worktreeprep.RepairRootSubmoduleWorktrees(ctx, rootPath); err != nil {
				return err
			}
			plan, err := inspectRemovalPlan(ctx, rootPath, projectPath, expectedCommit, false)
			if err != nil {
				return err
			}
			removalPlan = &plan
			plan.Branch = summary.RepoBranch
			if err := checkRemovalProcesses(ctx, projectPath); err != nil {
				return err
			}
			if err := s.saveRemovalReceipt(ctx, plan); err != nil {
				return err
			}
			removalStarted = true
			if err := removeOwnedChildren(ctx, plan); err != nil {
				return err
			}
			if err := validateRemovalPlanDirectory(plan); err != nil {
				return err
			}
			if err := validateRemovalClones(ctx, plan); err != nil {
				return err
			}
			// Git interprets absent gitlink directories as deleted files. Empty
			// placeholders represent uninitialized submodules and let the normal
			// clean-checkout removal retain Git's concurrent-change protection.
			for _, child := range plan.Children {
				if err := os.Mkdir(child.Path, 0o755); err != nil {
					return fmt.Errorf("restore empty submodule placeholder: %w", err)
				}
			}
		}
	}
	if !residualDirectoryRemoved && !missingCheckoutReconciled {
		removalStarted = true
		removeErr := gitWorktreeRemove(ctx, rootPath, projectPath, force)
		if removeErr != nil && allowSubmoduleForceFallback && isGitWorktreeSubmoduleRemoveError(removeErr) {
			removeErr = gitWorktreeRemove(ctx, rootPath, projectPath, true)
		}
		if removeErr != nil {
			if err := s.finishSafeWorktreeRemovalAfterGitError(ctx, rootPath, kind, projectPath, expectedCommit, removeErr); err != nil {
				return err
			}
		}
	}
	if !residualDirectoryRemoved && !missingCheckoutReconciled {
		if err := worktreeprep.PruneSubmoduleWorktrees(ctx, rootPath); err != nil {
			return fmt.Errorf("prune submodule worktrees after removing %s: %w", projectPath, err)
		}
	}
	if err := s.verifyWorktreeRemoval(ctx, rootPath, kind, projectPath, expectedCommit, missingCheckoutReconciled); err != nil {
		return err
	}
	if removalPlan != nil {
		tree, err := readResidualGitTree(ctx, rootPath, removalPlan.Commit)
		if err != nil {
			return err
		}
		if err := verifyRemovalChildRegistrations(ctx, *removalPlan, tree, true); err != nil {
			return err
		}
	}
	unlockProjectState := s.lockProjectStateMutation(projectPath)
	if err := s.store.SetForgotten(ctx, projectPath, true); err != nil {
		unlockProjectState()
		return fmt.Errorf("forget removed worktree: %w", err)
	}
	// Reconcile the persisted presence immediately so merged-and-removed worktrees
	// do not linger as orphaned checkouts until a later scan happens to revisit them.
	if err := s.store.SetProjectPresence(ctx, projectPath, false); err != nil {
		unlockProjectState()
		return fmt.Errorf("record removed worktree presence: %w", err)
	}
	if _, err := s.store.ClearTodoWorkForProjectPath(ctx, projectPath); err != nil {
		unlockProjectState()
		return fmt.Errorf("clear TODO work session for removed worktree: %w", err)
	}
	s.forgetProjectState(projectPath)
	unlockProjectState()

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
