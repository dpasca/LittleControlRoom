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
