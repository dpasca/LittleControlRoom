package worktreeprep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/gitlock"

	toml "github.com/pelletier/go-toml/v2"
)

const ConfigRelPath = ".lcroom/worktrees.toml"
const AutoSubmodulesProfile = "submodules-auto"
const RecursiveSubmodulesProfile = "recursive-submodules"

type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	Profiles       map[string]Profile `toml:"profiles"`
}

type Profile struct {
	Description string      `toml:"description"`
	Submodules  []Submodule `toml:"submodules"`
}

type Submodule struct {
	Path string `toml:"path"`
	Mode string `toml:"mode"`
}

type Result struct {
	ConfigPath string
	Profile    string
	Prepared   []PreparedSubmodule
	Skipped    bool
	SkipReason string
}

type PreparedSubmodule struct {
	Path   string
	Mode   string
	Commit string
}

func Prepare(ctx context.Context, rootPath, worktreePath, requestedProfile string) (Result, error) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	worktreePath = filepath.Clean(strings.TrimSpace(worktreePath))
	if rootPath == "" || rootPath == "." {
		return Result{}, fmt.Errorf("root path is required")
	}
	if worktreePath == "" || worktreePath == "." {
		return Result{}, fmt.Errorf("worktree path is required")
	}

	requestedProfile = strings.TrimSpace(requestedProfile)
	cfg, configPath, found, err := Load(rootPath)
	if err != nil {
		return Result{}, err
	}
	result := Result{ConfigPath: configPath}
	selectedProfile := requestedProfile
	if selectedProfile == "" && found {
		selectedProfile = strings.TrimSpace(cfg.DefaultProfile)
	}
	if selectedProfile == "" {
		selectedProfile = AutoSubmodulesProfile
	}
	if isSkipProfile(selectedProfile) {
		result.Skipped = true
		result.SkipReason = "worktree prep disabled"
		return result, nil
	}
	if isAutoSubmodulesProfile(selectedProfile) {
		return prepareAutoSubmodules(ctx, rootPath, worktreePath, result)
	}
	if isRecursiveSubmodulesProfile(selectedProfile) {
		return prepareRecursiveSubmodules(ctx, worktreePath, result)
	}
	if !found {
		return Result{}, fmt.Errorf("worktree prep profile %q is not defined because %s does not exist", selectedProfile, configPath)
	}

	profileName, profile, ok, err := cfg.ResolveProfile(selectedProfile)
	if err != nil {
		return Result{}, err
	}
	result.Profile = profileName
	if !ok {
		result.Skipped = true
		result.SkipReason = "no worktree prep profile selected"
		return result, nil
	}

	for _, submodule := range profile.Submodules {
		prepared, err := prepareSubmodule(ctx, rootPath, worktreePath, submodule)
		if err != nil {
			return result, err
		}
		result.Prepared = append(result.Prepared, prepared)
	}
	return result, nil
}

// SyncSubmodules updates a linked parent worktree's submodules without replacing
// the canonical checkout recorded by a shared nested-submodule repository.
//
// A plain `git submodule update` run from a parent linked worktree rewrites
// core.worktree when that submodule path was prepared with `git worktree add`.
// Because core.worktree lives in the shared submodule config, that leaves the
// canonical parent checkout unable to run commands such as `git status`.
func SyncSubmodules(ctx context.Context, rootPath, worktreePath string) error {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	worktreePath = filepath.Clean(strings.TrimSpace(worktreePath))
	if rootPath == "" || rootPath == "." {
		return fmt.Errorf("root path is required")
	}
	if worktreePath == "" || worktreePath == "." {
		return fmt.Errorf("worktree path is required")
	}

	paths, err := listConfiguredSubmodulePaths(ctx, worktreePath)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := syncSubmodule(ctx, rootPath, worktreePath, path); err != nil {
			return fmt.Errorf("sync submodule %s in %s: %w", path, worktreePath, err)
		}
	}
	return nil
}

func syncSubmodule(ctx context.Context, rootPath, worktreePath, submodulePath string) error {
	commit, err := resolveSubmoduleCommit(ctx, worktreePath, submodulePath)
	if err != nil {
		return err
	}
	rootSubmodulePath := filepath.Join(rootPath, filepath.FromSlash(submodulePath))
	worktreeSubmodulePath := filepath.Join(worktreePath, filepath.FromSlash(submodulePath))

	shared, err := reposShareGitCommonDir(ctx, rootSubmodulePath, worktreeSubmodulePath)
	if err != nil {
		return err
	}
	if !shared {
		return gitSubmoduleUpdate(ctx, worktreePath, submodulePath)
	}

	if err := ensureRootSubmoduleHasCommit(ctx, rootPath, submodulePath, commit); err != nil {
		return err
	}
	currentCommit, err := gitOutput(ctx, worktreeSubmodulePath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read linked submodule worktree %s HEAD: %w", submodulePath, err)
	}
	if strings.TrimSpace(currentCommit) != commit {
		if err := gitlock.CheckIndexLock(ctx, worktreeSubmodulePath); err != nil {
			return fmt.Errorf("preflight linked submodule worktree %s: %w", submodulePath, err)
		}
		if err := gitRun(ctx, worktreeSubmodulePath, "update linked submodule worktree "+submodulePath, "checkout", "--detach", commit); err != nil {
			return err
		}
	}

	return SyncSubmodules(ctx, rootSubmodulePath, worktreeSubmodulePath)
}

func reposShareGitCommonDir(ctx context.Context, firstPath, secondPath string) (bool, error) {
	if !isGitRepo(ctx, firstPath) || !isGitRepo(ctx, secondPath) {
		return false, nil
	}
	firstDir, err := gitCommonDir(ctx, firstPath)
	if err != nil {
		return false, err
	}
	secondDir, err := gitCommonDir(ctx, secondPath)
	if err != nil {
		return false, err
	}
	return sameCleanPath(firstDir, secondDir), nil
}

func gitCommonDir(ctx context.Context, repoPath string) (string, error) {
	value, err := gitOutput(ctx, repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("git common directory is empty for %s", repoPath)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(repoPath, value)
	}
	return filepath.Clean(value), nil
}

func prepareAutoSubmodules(ctx context.Context, rootPath, worktreePath string, result Result) (Result, error) {
	result.Profile = AutoSubmodulesProfile
	paths, err := listConfiguredSubmodulePaths(ctx, worktreePath)
	if err != nil {
		return result, err
	}
	for _, path := range paths {
		prepared, err := prepareSubmoduleAuto(ctx, rootPath, worktreePath, path)
		if err != nil {
			return result, err
		}
		result.Prepared = append(result.Prepared, prepared)
	}
	return result, nil
}

func prepareRecursiveSubmodules(ctx context.Context, worktreePath string, result Result) (Result, error) {
	result.Profile = RecursiveSubmodulesProfile
	paths, listErr := listConfiguredSubmodulePaths(ctx, worktreePath)
	if listErr != nil {
		return result, listErr
	}
	if len(paths) == 0 {
		return result, nil
	}
	if err := gitSubmoduleUpdateAll(ctx, worktreePath); err != nil {
		return result, err
	}
	for _, path := range paths {
		commit, _ := gitOutput(ctx, worktreePath, "rev-parse", "HEAD:"+path)
		result.Prepared = append(result.Prepared, PreparedSubmodule{
			Path:   path,
			Mode:   "checkout",
			Commit: strings.TrimSpace(commit),
		})
	}
	return result, nil
}

func Load(repoRoot string) (Config, string, bool, error) {
	repoRoot = filepath.Clean(strings.TrimSpace(repoRoot))
	if repoRoot == "" || repoRoot == "." {
		return Config{}, "", false, fmt.Errorf("repo root is required")
	}
	configPath := filepath.Join(repoRoot, filepath.FromSlash(ConfigRelPath))
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, configPath, false, nil
		}
		return Config{}, configPath, false, fmt.Errorf("read worktree prep config %s: %w", configPath, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, configPath, true, fmt.Errorf("parse worktree prep config %s: %w", configPath, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	for name, profile := range cfg.Profiles {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return Config{}, configPath, true, fmt.Errorf("worktree prep profile name cannot be blank")
		}
		for i, submodule := range profile.Submodules {
			path, err := cleanSubmodulePath(submodule.Path)
			if err != nil {
				return Config{}, configPath, true, fmt.Errorf("worktree prep profile %q submodule %d: %w", name, i+1, err)
			}
			mode, err := normalizeSubmoduleMode(submodule.Mode)
			if err != nil {
				return Config{}, configPath, true, fmt.Errorf("worktree prep profile %q submodule %s: %w", name, path, err)
			}
			profile.Submodules[i].Path = path
			profile.Submodules[i].Mode = mode
		}
		cfg.Profiles[name] = profile
	}
	cfg.DefaultProfile = strings.TrimSpace(cfg.DefaultProfile)
	return cfg, configPath, true, nil
}

func (c Config) ResolveProfile(requestedProfile string) (string, Profile, bool, error) {
	name := strings.TrimSpace(requestedProfile)
	if name == "" {
		name = strings.TrimSpace(c.DefaultProfile)
	}
	if name == "" || isSkipProfile(name) {
		return "", Profile{}, false, nil
	}
	profile, ok := c.Profiles[name]
	if !ok {
		names := make([]string, 0, len(c.Profiles))
		for profileName := range c.Profiles {
			names = append(names, profileName)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "", Profile{}, false, fmt.Errorf("worktree prep profile %q is not defined", name)
		}
		return "", Profile{}, false, fmt.Errorf("worktree prep profile %q is not defined; available profiles: %s", name, strings.Join(names, ", "))
	}
	return name, profile, true, nil
}

func normalizeBuiltInProfileName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func isRecursiveSubmodulesProfile(name string) bool {
	switch normalizeBuiltInProfileName(name) {
	case RecursiveSubmodulesProfile, "submodules", "all-submodules", "hydrate-submodules", "recursive":
		return true
	default:
		return false
	}
}

func isAutoSubmodulesProfile(name string) bool {
	switch normalizeBuiltInProfileName(name) {
	case AutoSubmodulesProfile, "auto-submodules", "auto":
		return true
	default:
		return false
	}
}

func isSkipProfile(name string) bool {
	switch normalizeBuiltInProfileName(name) {
	case "none", "off", "skip", "disabled", "false":
		return true
	default:
		return false
	}
}

func prepareSubmodule(ctx context.Context, rootPath, worktreePath string, submodule Submodule) (PreparedSubmodule, error) {
	path, err := cleanSubmodulePath(submodule.Path)
	if err != nil {
		return PreparedSubmodule{}, err
	}
	mode, err := normalizeSubmoduleMode(submodule.Mode)
	if err != nil {
		return PreparedSubmodule{}, err
	}
	switch mode {
	case "checkout":
		return prepareSubmoduleCheckout(ctx, worktreePath, path)
	case "worktree":
		return prepareSubmoduleWorktree(ctx, rootPath, worktreePath, path)
	default:
		return PreparedSubmodule{}, fmt.Errorf("unsupported submodule mode %q", mode)
	}
}

func prepareSubmoduleAuto(ctx context.Context, rootPath, worktreePath, submodulePath string) (PreparedSubmodule, error) {
	path, err := cleanSubmodulePath(submodulePath)
	if err != nil {
		return PreparedSubmodule{}, err
	}
	commit, err := resolveSubmoduleCommit(ctx, worktreePath, path)
	if err != nil {
		return PreparedSubmodule{}, err
	}
	canReuse, reuseErr := rootSubmoduleHasOrCanFetchCommit(ctx, rootPath, path, commit)
	if canReuse {
		return createSubmoduleWorktreeAtCommit(ctx, rootPath, worktreePath, path, commit)
	}
	prepared, checkoutErr := prepareSubmoduleCheckout(ctx, worktreePath, path)
	if checkoutErr != nil && reuseErr != nil {
		return PreparedSubmodule{}, fmt.Errorf("prepare submodule %s by checkout after linked worktree reuse failed: %w (reuse check also failed: %v)", path, checkoutErr, reuseErr)
	}
	return prepared, checkoutErr
}

func prepareSubmoduleCheckout(ctx context.Context, worktreePath, submodulePath string) (PreparedSubmodule, error) {
	if err := gitSubmoduleUpdate(ctx, worktreePath, submodulePath); err != nil {
		return PreparedSubmodule{}, err
	}
	commit, _ := gitOutput(ctx, worktreePath, "rev-parse", "HEAD:"+submodulePath)
	return PreparedSubmodule{Path: submodulePath, Mode: "checkout", Commit: strings.TrimSpace(commit)}, nil
}

func prepareSubmoduleWorktree(ctx context.Context, rootPath, worktreePath, submodulePath string) (PreparedSubmodule, error) {
	commit, err := resolveSubmoduleCommit(ctx, worktreePath, submodulePath)
	if err != nil {
		return PreparedSubmodule{}, err
	}
	if err := ensureRootSubmoduleHasCommit(ctx, rootPath, submodulePath, commit); err != nil {
		return PreparedSubmodule{}, err
	}
	return createSubmoduleWorktreeAtCommit(ctx, rootPath, worktreePath, submodulePath, commit)
}

func resolveSubmoduleCommit(ctx context.Context, worktreePath, submodulePath string) (string, error) {
	commit, err := gitOutput(ctx, worktreePath, "rev-parse", "HEAD:"+submodulePath)
	if err != nil {
		return "", fmt.Errorf("resolve submodule %s commit in %s: %w", submodulePath, worktreePath, err)
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", fmt.Errorf("resolve submodule %s commit in %s: empty commit", submodulePath, worktreePath)
	}
	return commit, nil
}

func createSubmoduleWorktreeAtCommit(ctx context.Context, rootPath, worktreePath, submodulePath, commit string) (PreparedSubmodule, error) {
	rootSubmodulePath := filepath.Join(rootPath, filepath.FromSlash(submodulePath))
	targetPath := filepath.Join(worktreePath, filepath.FromSlash(submodulePath))
	if err := gitlock.CheckIndexLock(ctx, rootSubmodulePath); err != nil {
		return PreparedSubmodule{}, fmt.Errorf("preflight submodule worktree %s: %w", submodulePath, err)
	}
	if err := ensureContained(worktreePath, targetPath); err != nil {
		return PreparedSubmodule{}, err
	}
	if err := removeEmptySubmodulePlaceholder(targetPath); err != nil {
		return PreparedSubmodule{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return PreparedSubmodule{}, fmt.Errorf("create submodule worktree parent for %s: %w", targetPath, err)
	}
	if err := gitRun(ctx, rootSubmodulePath, "create submodule worktree "+submodulePath, "worktree", "add", "--detach", targetPath, commit); err != nil {
		return PreparedSubmodule{}, err
	}
	return PreparedSubmodule{Path: submodulePath, Mode: "worktree", Commit: commit}, nil
}

func PruneSubmoduleWorktrees(ctx context.Context, rootPath string) error {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return fmt.Errorf("root path is required")
	}
	if _, err := RepairRootSubmoduleWorktrees(ctx, rootPath); err != nil {
		return err
	}
	paths, err := listConfiguredSubmodulePaths(ctx, rootPath)
	if err != nil {
		return err
	}
	var errs []string
	for _, path := range paths {
		submoduleRepoPath := filepath.Join(rootPath, filepath.FromSlash(path))
		if !isGitRepo(ctx, submoduleRepoPath) {
			continue
		}
		if err := gitRun(ctx, submoduleRepoPath, "prune submodule worktrees "+path, "worktree", "prune"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// RepairRootSubmoduleWorktrees restores the canonical root checkout recorded by
// initialized submodule repositories. Older linked-worktree sync paths could
// rewrite the shared core.worktree value to a nested checkout; after that
// checkout was removed, even `git status` in the parent repository failed.
//
// Only submodule gitdirs owned by each parent repository's modules directory are
// eligible. This keeps repair bounded to metadata that the root repo owns while
// still covering recursively initialized submodules.
func RepairRootSubmoduleWorktrees(ctx context.Context, rootPath string) ([]string, error) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return nil, fmt.Errorf("root path is required")
	}
	repaired := []string{}
	visited := map[string]struct{}{}
	if err := repairRootSubmoduleWorktrees(ctx, rootPath, "", visited, &repaired); err != nil {
		return repaired, err
	}
	return repaired, nil
}

func repairRootSubmoduleWorktrees(ctx context.Context, repoPath, pathPrefix string, visited map[string]struct{}, repaired *[]string) error {
	paths, err := listConfiguredSubmodulePaths(ctx, repoPath)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	repoGitDir, err := gitCommonDir(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("resolve Git directory for submodule repair in %s: %w", repoPath, err)
	}
	repoGitDir = filepath.Clean(repoGitDir)
	if _, ok := visited[repoGitDir]; ok {
		return nil
	}
	visited[repoGitDir] = struct{}{}
	modulesDir := filepath.Join(repoGitDir, "modules")

	for _, path := range paths {
		displayPath := filepath.ToSlash(filepath.Join(pathPrefix, filepath.FromSlash(path)))
		submodulePath := filepath.Join(repoPath, filepath.FromSlash(path))
		submoduleGitDir, initialized, err := directSubmoduleGitDir(submodulePath)
		if err != nil {
			return fmt.Errorf("inspect root submodule metadata for %s: %w", displayPath, err)
		}
		if !initialized || !pathContainedBy(modulesDir, submoduleGitDir) {
			continue
		}

		configuredWorktree, configured, err := gitCoreWorktree(ctx, submoduleGitDir, submodulePath)
		if err != nil {
			return fmt.Errorf("read root submodule worktree metadata for %s: %w", displayPath, err)
		}
		if configured {
			configuredPath := filepath.Clean(configuredWorktree)
			if !filepath.IsAbs(configuredPath) {
				configuredPath = filepath.Join(submoduleGitDir, configuredPath)
			}
			if !sameCleanPath(configuredPath, submodulePath) {
				expectedWorktree, err := filepath.Rel(submoduleGitDir, submodulePath)
				if err != nil {
					return fmt.Errorf("resolve canonical root worktree for submodule %s: %w", displayPath, err)
				}
				if err := setGitCoreWorktree(ctx, submoduleGitDir, submodulePath, expectedWorktree); err != nil {
					return fmt.Errorf("repair root submodule worktree metadata for %s: %w", displayPath, err)
				}
				*repaired = append(*repaired, displayPath)
			}
		}
		if err := repairRootSubmoduleWorktrees(ctx, submodulePath, displayPath, visited, repaired); err != nil {
			return err
		}
	}
	return nil
}

func directSubmoduleGitDir(submodulePath string) (string, bool, error) {
	gitPath := filepath.Join(submodulePath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stat %s: %w", gitPath, err)
	}
	if info.IsDir() {
		return filepath.Clean(gitPath), true, nil
	}
	raw, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", gitPath, err)
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	const prefix = "gitdir:"
	if !strings.HasPrefix(strings.ToLower(line), prefix) {
		return "", false, fmt.Errorf("%s does not contain a gitdir reference", gitPath)
	}
	value := strings.TrimSpace(line[len(prefix):])
	if value == "" {
		return "", false, fmt.Errorf("%s contains an empty gitdir reference", gitPath)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(submodulePath, value)
	}
	value = filepath.Clean(value)
	if info, err := os.Stat(value); err != nil {
		return "", false, fmt.Errorf("stat submodule gitdir %s: %w", value, err)
	} else if !info.IsDir() {
		return "", false, fmt.Errorf("submodule gitdir is not a directory: %s", value)
	}
	return value, true, nil
}

func gitCoreWorktree(ctx context.Context, gitDir, worktreePath string) (string, bool, error) {
	cmd := exec.CommandContext(
		ctx,
		"git",
		"--git-dir="+gitDir,
		"--work-tree="+worktreePath,
		"config",
		"--local",
		"--get",
		"core.worktree",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(string(out)) == "" {
		return "", false, nil
	}
	return "", false, fmt.Errorf("git config failed: %w: %s", err, strings.TrimSpace(string(out)))
}

func setGitCoreWorktree(ctx context.Context, gitDir, worktreePath, value string) error {
	cmd := exec.CommandContext(
		ctx,
		"git",
		"--git-dir="+gitDir,
		"--work-tree="+worktreePath,
		"config",
		"--local",
		"core.worktree",
		value,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pathContainedBy(rootPath, childPath string) bool {
	rootPath = filepath.Clean(rootPath)
	childPath = filepath.Clean(childPath)
	if resolved, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(childPath); err == nil {
		childPath = filepath.Clean(resolved)
	}
	rel, err := filepath.Rel(rootPath, childPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func listConfiguredSubmodulePaths(ctx context.Context, rootPath string) ([]string, error) {
	gitmodulesPath := filepath.Join(rootPath, ".gitmodules")
	if _, err := os.Stat(gitmodulesPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", gitmodulesPath, err)
	}
	out, err := gitOutput(ctx, rootPath, "config", "--file", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, fmt.Errorf("list submodule paths in %s: %w", rootPath, err)
	}
	seen := map[string]struct{}{}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		path, err := cleanSubmodulePath(fields[1])
		if err != nil {
			return nil, fmt.Errorf("submodule path from .gitmodules: %w", err)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func gitSubmoduleUpdate(ctx context.Context, repoPath, submodulePath string) error {
	if err := gitlock.CheckIndexLock(ctx, repoPath); err != nil {
		return err
	}
	return gitRun(ctx, repoPath, "update submodule "+submodulePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive", "--", submodulePath)
}

func gitSubmoduleUpdateAll(ctx context.Context, repoPath string) error {
	if err := gitlock.CheckIndexLock(ctx, repoPath); err != nil {
		return err
	}
	return gitRun(ctx, repoPath, "update submodules", "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
}

func ensureRootSubmoduleHasCommit(ctx context.Context, rootPath, submodulePath, commit string) error {
	rootSubmodulePath := filepath.Join(rootPath, filepath.FromSlash(submodulePath))
	if !isGitRepo(ctx, rootSubmodulePath) {
		if err := gitSubmoduleUpdate(ctx, rootPath, submodulePath); err != nil {
			return fmt.Errorf("initialize root submodule %s for linked worktree reuse: %w", submodulePath, err)
		}
	}
	if gitCommitExists(ctx, rootSubmodulePath, commit) {
		return nil
	}
	if err := gitRun(ctx, rootSubmodulePath, "fetch submodule commit "+submodulePath, "fetch", "--all", "--tags"); err != nil {
		return err
	}
	if !gitCommitExists(ctx, rootSubmodulePath, commit) {
		return fmt.Errorf("root submodule %s does not have required commit %s after fetch", submodulePath, commit)
	}
	return nil
}

func rootSubmoduleHasOrCanFetchCommit(ctx context.Context, rootPath, submodulePath, commit string) (bool, error) {
	rootSubmodulePath := filepath.Join(rootPath, filepath.FromSlash(submodulePath))
	if !isGitRepo(ctx, rootSubmodulePath) {
		return false, nil
	}
	if gitCommitExists(ctx, rootSubmodulePath, commit) {
		return true, nil
	}
	if err := gitRun(ctx, rootSubmodulePath, "fetch submodule commit "+submodulePath, "fetch", "--all", "--tags"); err != nil {
		return false, err
	}
	if !gitCommitExists(ctx, rootSubmodulePath, commit) {
		return false, nil
	}
	return true, nil
}

func gitCommitExists(ctx context.Context, repoPath, commit string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "cat-file", "-e", strings.TrimSpace(commit)+"^{commit}")
	return cmd.Run() == nil
}

func gitRun(ctx context.Context, repoPath, action string, args ...string) error {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	if repoPath == "" || repoPath == "." {
		return fmt.Errorf("repo path is required")
	}
	allArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return formatGitRunError(strings.TrimSpace(action), repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func formatGitRunError(action, repoPath string, err error, output string) error {
	base := fmt.Sprintf("%s in %s: %v", action, repoPath, err)
	if lockPath, ok := gitlock.LockPathFromOutput(output); ok {
		return fmt.Errorf("%s: %w", base, gitlock.IndexLockError{LockPath: lockPath})
	}
	if output == "" {
		return errors.New(base)
	}
	if isMissingSubmoduleCommitOutput(output) {
		return fmt.Errorf("%s: submodule checkout failed because the parent repo records a submodule commit that the submodule remote did not provide. Push the missing submodule commit first, or configure %s with mode = \"worktree\" for locally available submodule commits. Git output: %s", base, ConfigRelPath, output)
	}
	return fmt.Errorf("%s: %s", base, output)
}

func isMissingSubmoduleCommitOutput(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "not our ref") &&
		strings.Contains(text, "fetched in submodule path") &&
		strings.Contains(text, "direct fetching of that commit failed")
}

func gitOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	if repoPath == "" || repoPath == "." {
		return "", fmt.Errorf("repo path is required")
	}
	allArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), repoPath, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isGitRepo(ctx context.Context, path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return sameCleanPath(strings.TrimSpace(string(out)), path)
}

func sameCleanPath(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = filepath.Clean(resolved)
	}
	return a == b
}

func removeEmptySubmodulePlaceholder(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat submodule placeholder %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("submodule worktree path exists and is not a directory: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read submodule placeholder %s: %w", path, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("submodule worktree path is not empty: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove empty submodule placeholder %s: %w", path, err)
	}
	return nil
}

func cleanSubmodulePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("submodule path is required")
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("submodule path must be relative and stay inside the repo: %q", raw)
	}
	return value, nil
}

func normalizeSubmoduleMode(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "", "checkout", "update", "submodule":
		return "checkout", nil
	case "worktree", "linked-worktree", "linked":
		return "worktree", nil
	default:
		return "", fmt.Errorf("submodule mode must be checkout or worktree")
	}
}

func ensureContained(root, child string) error {
	root = filepath.Clean(root)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return fmt.Errorf("check path containment: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("path %s escapes worktree %s", child, root)
	}
	return nil
}
