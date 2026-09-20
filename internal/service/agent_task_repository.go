package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
)

// ConfigureAgentTaskRepositoryHost connects the UI-independent ownership service
// to live registries. Call once before the host accepts engineer input.
func (s *Service) ConfigureAgentTaskRepositoryHost(engineers func() []codexapp.Snapshot, processes func() []projectrun.Snapshot) {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	s.repositoryEngineers, s.repositoryProcesses = engineers, processes
}

func canonicalRepositoryPath(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	// Resolve the existing ancestor too: a not-yet-created child of a symlinked
	// checkout must not bypass ownership (including macOS /var -> /private/var).
	ancestor := absolute
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return absolute
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
}

func repositoryPathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = canonicalRepositoryPath(a), canonicalRepositoryPath(b)
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func taskRepositoryPath(task model.AgentTask) string {
	if task.Repository.Root != "" {
		return task.Repository.Root
	}
	return firstNonEmpty(task.OriginWorktreePath, task.OriginProjectPath)
}

// BeginRepositoryTurn serializes admission with lease acquisition. The caller
// must hold the returned unlock until the provider has marked the turn active.
// All engine turns are conservatively write-capable; opening a transcript is not.
func (s *Service) BeginRepositoryTurn(projectPath, sessionKey string) (func(), error) {
	s.repositoryMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	task, isTask, err := s.store.AgentTaskForWorkspace(ctx, projectPath)
	if err == nil {
		target := projectPath
		if isTask {
			target = taskRepositoryPath(task)
		}
		err = s.checkRepositoryOwner(ctx, target, task.ID, sessionKey)
		if err == nil && isTask && task.Repository.Write {
			err = s.acquireTaskRepository(ctx, task, sessionKey)
		}
		if err == nil && isTask && task.Workflow.Enabled {
			active := false
			if s.repositoryEngineers != nil {
				for _, snapshot := range s.repositoryEngineers() {
					if snapshot.ProjectPath == projectPath && snapshot.Busy && !snapshot.Closed {
						active = true
					}
				}
			}
			err = s.store.BeginStructuredTaskRun(ctx, task, sessionKey, active)
		}
	}
	if err != nil {
		if isTask && task.Repository.Write {
			s.recordRepositoryProblem(ctx, task, err)
		}
		s.repositoryMu.Unlock()
		return nil, err
	}
	return s.repositoryMu.Unlock, nil
}

func (s *Service) checkRepositoryOwner(ctx context.Context, target, taskID, sessionKey string) error {
	leases, err := s.store.AgentTaskRepositoryLeases(ctx)
	if err != nil {
		return err
	}
	for _, lease := range leases {
		if !repositoryPathsOverlap(lease.Root, target) {
			continue
		}
		if lease.TaskID == taskID && lease.SessionKey == sessionKey && sessionKey != "" {
			continue
		}
		return fmt.Errorf("repository %s is owned by task %s; stop its engineer and managed processes, then explicitly close the task to release ownership", lease.Root, lease.TaskID)
	}
	return nil
}

func (s *Service) acquireTaskRepository(ctx context.Context, task model.AgentTask, sessionKey string) error {
	if task.Repository.State == "held" {
		if task.Repository.SessionKey != sessionKey || sessionKey == "" {
			return fmt.Errorf("task repository lease belongs to another session; inspect and close the stopped task before continuing")
		}
		return nil
	}
	if sessionKey == "" {
		return fmt.Errorf("repository write ownership requires a stable engineer control identity")
	}
	if task.Status == model.AgentTaskStatusArchived {
		return fmt.Errorf("trashed task cannot acquire repository ownership")
	}
	if err := s.repositoryWritersIdle(ctx, task.Repository.Root); err != nil {
		return err
	}
	for _, marker := range []string{"index.lock", "HEAD.lock", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"} {
		raw, err := repositoryGit(ctx, task.Repository.Root, "rev-parse", "--git-path", marker)
		if err != nil {
			return err
		}
		path := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(path) {
			path = filepath.Join(task.Repository.Root, path)
		}
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("repository has unfinished Git state %s; resolve it before delegation", marker)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	probe, err := os.CreateTemp(task.Repository.Root, ".lcr-write-check-*")
	if err != nil {
		return fmt.Errorf("repository is not writable: %w", err)
	}
	probe.Close()
	if err := os.Remove(probe.Name()); err != nil {
		return fmt.Errorf("remove write preflight probe: %w", err)
	}
	baseline, err := inspectTaskRepository(ctx, task.Repository.Root)
	if err != nil {
		return err
	}
	if baseline.changes != "" {
		return fmt.Errorf("repository write preflight requires a clean checkout; preserve and resolve existing edits before continuing")
	}
	repository := task.Repository
	repository.State, repository.SessionKey = "held", sessionKey
	repository.BaseHEAD, repository.BaseBranch = baseline.head, baseline.branch
	repository.HandoffFingerprint, repository.Changes, repository.Error = "", "", ""
	return s.store.AcquireAgentTaskRepository(ctx, task.ID, repository)
}

func (s *Service) repositoryWritersIdle(ctx context.Context, root string) error {
	if s.repositoryEngineers == nil || s.repositoryProcesses == nil {
		return fmt.Errorf("repository ownership host is unavailable; cannot verify active writers")
	}
	for _, snapshot := range s.repositoryEngineers() {
		if snapshot.Closed {
			continue
		}
		path := snapshot.ProjectPath
		task, ok, err := s.store.AgentTaskForWorkspace(ctx, path)
		if err != nil {
			return err
		}
		if ok {
			path = taskRepositoryPath(task)
		}
		if repositoryPathsOverlap(path, root) && (snapshot.Busy || snapshot.BusyExternal || snapshot.ActiveTurnID != "" || snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || (snapshot.Goal != nil && snapshot.Goal.Status == codexapp.ThreadGoalStatusActive)) {
			return fmt.Errorf("repository has an active engineer at %s; let it finish or stop it before transferring write ownership", snapshot.ProjectPath)
		}
	}
	for _, process := range s.repositoryProcesses() {
		if !process.Running {
			continue
		}
		task, ok, err := s.store.AgentTaskForWorkspace(ctx, process.ProjectPath)
		if err != nil {
			return err
		}
		path := process.ProjectPath
		if ok {
			path = taskRepositoryPath(task)
		}
		if repositoryPathsOverlap(path, root) || repositoryPathsOverlap(process.CWD, root) {
			return fmt.Errorf("stop managed process %s (pid %d) before transferring repository write ownership", process.ID, process.PID)
		}
	}
	return nil
}

// BeginRepositoryProcess uses the same admission lock as engine turns so a
// process cannot slip between the idle check and transfer of ownership.
func (s *Service) BeginRepositoryProcess(projectPath, cwd string) (func(), error) {
	s.repositoryMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task, ok, err := s.store.AgentTaskForWorkspace(ctx, projectPath)
	if err == nil {
		target, key := projectPath, ""
		if ok {
			target, key = taskRepositoryPath(task), task.Repository.SessionKey
		}
		err = s.checkRepositoryOwner(ctx, target, task.ID, key)
		if err == nil {
			err = s.checkRepositoryOwner(ctx, cwd, task.ID, key)
		}
		if err == nil && ok && task.Repository.Write && task.Repository.State != "held" {
			err = fmt.Errorf("task must acquire repository ownership before starting a managed process")
		}
	}
	if err != nil {
		s.repositoryMu.Unlock()
		return nil, err
	}
	return s.repositoryMu.Unlock, nil
}

// ReleaseTaskRepository is explicit on close or invoked at a verified idle
// handoff. Missing sessions never cause an implicit timeout/takeover of a lease.
func (s *Service) ReleaseTaskRepository(ctx context.Context, taskID string) error {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	return s.releaseTaskRepositoryLocked(ctx, taskID)
}
func (s *Service) releaseTaskRepositoryLocked(ctx context.Context, taskID string) error {
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !task.Repository.Write || task.Repository.State != "held" {
		return nil
	}
	if err := s.repositoryWritersIdle(ctx, task.Repository.Root); err != nil {
		s.recordRepositoryProblem(ctx, task, err)
		return err
	}
	after, err := inspectTaskRepository(ctx, task.Repository.Root)
	repository := task.Repository
	repository.State, repository.SessionKey = "released", ""
	if err != nil {
		// A stopped owner must remain releasable even if the checkout was moved or
		// its evidence is too large. Preserve the failure for caller inspection.
		repository.Error = "Repository evidence unavailable at handoff: " + err.Error()
		repository.HandoffFingerprint, repository.Changes = "", ""
		return s.store.ReleaseAgentTaskRepository(ctx, taskID, repository)
	}
	repository.Changes, repository.HandoffFingerprint, repository.Error = after.changes, after.fingerprint, ""
	if after.head != repository.BaseHEAD || after.branch != repository.BaseBranch {
		repository.Error = "Repository HEAD or branch changed during delegation; review this change before accepting the result."
	}
	return s.store.ReleaseAgentTaskRepository(ctx, taskID, repository)
}

func (s *Service) recordRepositoryProblem(ctx context.Context, task model.AgentTask, err error) {
	if current, loadErr := s.store.GetAgentTask(ctx, task.ID); loadErr == nil {
		task = current
	}
	task.Repository.Error = err.Error()
	if task.Repository.State != "held" {
		task.Repository.State = "blocked"
	}
	_, _ = s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Repository: &task.Repository})
}

type taskRepositorySnapshot struct{ head, branch, changes, fingerprint string }

type boundedRepositoryOutput struct{ bytes.Buffer }

func (b *boundedRepositoryOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, fmt.Errorf("repository evidence exceeds 8 MiB; inspect manually")
	}
	return b.Buffer.Write(p)
}

func repositoryGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var out boundedRepositoryOutput
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("repository preflight git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func inspectTaskRepository(ctx context.Context, root string) (taskRepositorySnapshot, error) {
	var result taskRepositorySnapshot
	top, err := repositoryGit(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	if canonicalRepositoryPath(strings.TrimSpace(string(top))) != canonicalRepositoryPath(root) {
		return result, fmt.Errorf("repository scope must be the exact checkout root")
	}
	head, err := repositoryGit(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	branch, err := repositoryGit(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return result, fmt.Errorf("repository preflight requires an attached branch: %w", err)
	}
	status, err := repositoryGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return result, err
	}
	diff, err := repositoryGit(ctx, root, "diff", "HEAD", "--binary", "--no-ext-diff", "--no-textconv", "--submodule=diff")
	if err != nil {
		return result, err
	}
	hash := sha256.New()
	for _, data := range [][]byte{head, branch, status, diff} {
		hash.Write(data)
		hash.Write([]byte{0})
	}
	untracked, err := repositoryGit(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return result, err
	}
	var total int64
	for _, name := range bytes.Split(untracked, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(root, string(name))
		info, err := os.Lstat(path)
		if err != nil {
			return result, err
		}
		hash.Write(name)
		hash.Write([]byte{0})
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return result, err
			}
			hash.Write([]byte(target))
		} else if info.Mode().IsRegular() {
			total += info.Size()
			if total > 32<<20 {
				return result, fmt.Errorf("untracked evidence exceeds 32 MiB; inspect before releasing ownership")
			}
			file, err := os.Open(path)
			if err != nil {
				return result, err
			}
			_, copyErr := io.Copy(hash, io.LimitReader(file, (32<<20)+1))
			file.Close()
			if copyErr != nil {
				return result, copyErr
			}
		} else {
			return result, fmt.Errorf("unsupported untracked file %q; inspect before handoff", string(name))
		}
		hash.Write([]byte{0})
	}
	result.head, result.branch = strings.TrimSpace(string(head)), strings.TrimSpace(string(branch))
	result.changes = strings.TrimSpace(string(status))
	if len(result.changes) > 8192 {
		result.changes = result.changes[:8192] + "\n[truncated]"
	}
	result.fingerprint = fmt.Sprintf("%x", hash.Sum(nil))
	return result, nil
}
