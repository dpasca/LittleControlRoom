package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

// deleteWorktree is an explicit destructive action, not an archive operation.
// It never executes commands, hooks, or config from the directory being deleted.
func (s *Service) deleteWorktree(ctx context.Context, path string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) || path == string(filepath.Separator) {
		return fmt.Errorf("an absolute linked worktree path is required")
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	summary, err := s.store.GetTrackedProjectSummary(ctx, path)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			return nil
		}
	}
	root, kind := summary.WorktreeRootPath, summary.WorktreeKind
	if kind != model.WorktreeKindLinked || root == "" {
		root, kind, _, err = s.removeWorktreeTarget(ctx, path)
		if err != nil {
			return err
		}
	}
	if kind != model.WorktreeKindLinked || root == "" {
		return fmt.Errorf("only linked worktrees can be deleted")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	target := filepath.Join(parent, filepath.Base(path))
	if samePath(root, target) || removalPathWithin(root, target) {
		return fmt.Errorf("refusing to delete the primary checkout or its ancestor: %s", target)
	}
	unlockGit, err := s.lockGitWrite(ctx, root)
	if err != nil {
		return err
	}
	defer unlockGit()
	// Only inspect the trusted primary repository to locate its registrations.
	common, err := gitPath(ctx, root, "worktrees")
	if err != nil {
		return err
	}
	if removalPathWithin(common, target) || removalPathWithin(target, filepath.Dir(common)) {
		return fmt.Errorf("shared Git storage is inside the selected directory: %s", common)
	}
	registrations, err := deletionRegistrations(common, target)
	if err != nil {
		return err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer parentRoot.Close()
	name := filepath.Base(target)
	info, err := parentRoot.Lstat(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("selected worktree must be a directory, not a symlink: %s", target)
		}
		tree, err := parentRoot.OpenRoot(name)
		if err != nil {
			return err
		}
		defer tree.Close()
		opened, err := tree.Stat(".")
		if err != nil {
			return err
		}
		if !os.SameFile(info, opened) {
			return fmt.Errorf("selected worktree changed before deletion: %s", target)
		}
		dev, err := deletionFilesystem(tree)
		if err != nil {
			return err
		}
		parentDevice, err := deletionFilesystem(parentRoot)
		if err != nil {
			return err
		}
		if dev != parentDevice {
			return fmt.Errorf("selected worktree is a mounted filesystem: %s", target)
		}
		s.publishWorktreeRemovalProgress(path, "Checking deletion boundary...")
		// Collect only reciprocal nested linked-worktree registrations. Ordinary
		// .git directories and broken Git data are just contents to delete.
		var checked int64
		lastBoundary := time.Time{}
		err = walkDeletionTree(ctx, tree, dev, "", false, func(rel string, entry os.FileInfo) error {
			checked++
			if time.Since(lastBoundary) >= 500*time.Millisecond {
				s.publishWorktreeRemovalProgress(path, fmt.Sprintf("Checking deletion boundary · %d entries\n%s", checked, filepath.Join(target, rel)))
				lastBoundary = time.Now()
			}
			if filepath.Base(rel) == "gitdir" && filepath.Base(filepath.Dir(filepath.Dir(rel))) == "worktrees" && entry.Mode().IsRegular() {
				data, err := tree.ReadFile(rel)
				if err != nil {
					return err
				}
				consumer := filepath.Clean(strings.TrimSpace(string(data)))
				if filepath.IsAbs(consumer) && !removalPathWithin(consumer, target) {
					return fmt.Errorf("outside worktree depends on Git storage inside selected directory: %s", consumer)
				}
			}
			if filepath.Base(rel) != ".git" || !entry.Mode().IsRegular() || entry.Size() > 8192 {
				return nil
			}
			data, err := tree.ReadFile(rel)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(string(data), "gitdir: ") {
				return nil
			}
			admin := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
			if !filepath.IsAbs(admin) {
				admin = filepath.Join(target, filepath.Dir(rel), admin)
			}
			if removalPathWithin(admin, target) || !removalPathWithin(admin, filepath.Dir(common)) {
				return nil
			}
			registration, ok := ownedDeletionRegistration(admin, filepath.Join(target, rel))
			if ok {
				registrations = append(registrations, registration)
			}
			return nil
		})
		if err != nil {
			return err
		}
		var count int64
		last := time.Time{}
		err = walkDeletionTree(ctx, tree, dev, "", true, func(rel string, _ os.FileInfo) error {
			count++
			if time.Since(last) >= 500*time.Millisecond {
				s.publishWorktreeRemovalProgress(path, fmt.Sprintf("Deleting contents · %d entries\n%s", count, filepath.Join(target, rel)))
				last = time.Now()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("deletion incomplete at %s: %w", target, err)
		}
		current, err := parentRoot.Lstat(name)
		if err != nil {
			return err
		}
		if !os.SameFile(opened, current) {
			return fmt.Errorf("worktree path was replaced during deletion: %s", target)
		}
		if err := parentRoot.Remove(name); err != nil {
			return err
		}
	}
	s.publishWorktreeRemovalProgress(path, "Removing this worktree's Git registration...")
	for _, registration := range registrations {
		if err := unregisterDeletedWorktree(registration); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("worktree directory still exists or was recreated: %s", target)
	}
	worktrees, err := scanner.ListGitWorktrees(ctx, root)
	if err != nil {
		return fmt.Errorf("directory deleted; cannot verify Git registration: %w", err)
	}
	for _, worktree := range worktrees {
		if samePath(worktree.Path, target) {
			return fmt.Errorf("directory deleted but Git still registers %s", target)
		}
	}
	release := s.lockProjectStateMutation(path)
	defer release()
	if err := s.store.SetForgotten(ctx, path, true); err != nil {
		return err
	}
	if err := s.store.SetProjectPresence(ctx, path, false); err != nil {
		return err
	}
	if _, err := s.store.ClearTodoWorkForProjectPath(ctx, path); err != nil {
		return err
	}
	s.forgetProjectState(path)
	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: path, Payload: map[string]string{"action": "remove_worktree", "root_path": root}})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: path, Type: string(events.ActionApplied), Payload: "remove_worktree"})
	return nil
}

// All traversal is anchored to directory descriptors. Symlinks are unlinked,
// never followed. Mounted filesystems are not part of the deletion boundary.
func walkDeletionTree(ctx context.Context, root *os.Root, device deletionFilesystemID, prefix string, remove bool, visit func(string, os.FileInfo) error) error {
	currentDevice, err := deletionFilesystem(root)
	if err != nil {
		return err
	}
	if currentDevice != device {
		return fmt.Errorf("mounted filesystem crosses deletion boundary: %s", root.Name())
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			info, err := root.Lstat(name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			rel := filepath.Join(prefix, name)
			if err := visit(rel, info); err != nil {
				return err
			}
			if info.IsDir() {
				child, err := root.OpenRoot(name)
				if err != nil {
					return err
				}
				opened, err := child.Stat(".")
				if err == nil && !os.SameFile(info, opened) {
					err = fmt.Errorf("directory changed during deletion: %s", rel)
				}
				if err == nil {
					err = walkDeletionTree(ctx, child, device, rel, remove, visit)
				}
				child.Close()
				if err != nil {
					return err
				}
			}
			if remove {
				if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

type deletionRegistration struct {
	path, backlink string
	identity       os.FileInfo
}

func ownedDeletionRegistration(admin, backlink string) (deletionRegistration, bool) {
	admin = filepath.Clean(admin)
	// A linked registration lives immediately under common/worktrees. Never
	// remove a submodule's common store, even if its config is broken.
	if filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return deletionRegistration{}, false
	}
	info, err := os.Lstat(admin)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return deletionRegistration{}, false
	}
	common, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil {
		return deletionRegistration{}, false
	}
	commonPath := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonPath) {
		commonPath = filepath.Join(admin, commonPath)
	}
	if !samePath(commonPath, filepath.Dir(filepath.Dir(admin))) {
		return deletionRegistration{}, false
	}
	data, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil || !samePath(strings.TrimSpace(string(data)), backlink) {
		return deletionRegistration{}, false
	}
	return deletionRegistration{admin, strings.TrimSpace(string(data)), info}, true
}

func deletionRegistrations(parent, target string) ([]deletionRegistration, error) {
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []deletionRegistration
	for _, entry := range entries {
		if registration, ok := ownedDeletionRegistration(filepath.Join(parent, entry.Name()), filepath.Join(target, ".git")); ok {
			result = append(result, registration)
		}
	}
	return result, nil
}

func unregisterDeletedWorktree(registration deletionRegistration) error {
	r, err := os.OpenRoot(registration.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer r.Close()
	identity, err := r.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(identity, registration.identity) {
		return fmt.Errorf("registration replaced: %s", registration.path)
	}
	data, err := r.ReadFile("gitdir")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != registration.backlink {
		return fmt.Errorf("worktree registration changed: %s", registration.path)
	}
	// Unlinking gitdir unregisters this checkout. Leave any shared stores here
	// (such as modules/) alone: they are outside the approved directory.
	if err := r.Remove("gitdir"); err != nil {
		return err
	}
	entries, err := os.ReadDir(registration.path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "logs" && entry.Name() != "refs" {
			return nil
		}
	}
	device, err := deletionFilesystem(r)
	if err != nil {
		return err
	}
	if err := walkDeletionTree(context.Background(), r, device, "", true, func(string, os.FileInfo) error { return nil }); err != nil {
		return err
	}
	return os.Remove(registration.path)
}

type deletionFilesystemID struct{ device, mount uint64 }

func deletionFilesystem(root *os.Root) (deletionFilesystemID, error) {
	dir, err := root.Open(".")
	if err != nil {
		return deletionFilesystemID{}, err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return deletionFilesystemID{}, err
	}
	mount, err := deletionMountID(dir)
	return deletionFilesystemID{uint64(info.Sys().(*syscall.Stat_t).Dev), mount}, err
}
