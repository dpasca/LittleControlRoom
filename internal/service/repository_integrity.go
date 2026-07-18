package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/gitlock"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/worktreeprep"
)

const (
	repositoryWorkspaceExcursionEventType = "repository_workspace_excursion"
	repositoryRootRepairEventType         = "repository_root_repaired"

	repositoryExpectedBranchLinkedParent  = "linked_worktree_parent"
	repositoryExpectedBranchRemoteDefault = "origin_default"
	repositoryExpectedBranchWorktree      = "worktree_creation"
	repositoryExpectedBranchUser          = "user"
)

type RepositoryIntegrityRepairRequest struct {
	RootPath    string
	PrepProfile string
}

type RepositoryIntegrityRepairResult struct {
	RootPath           string
	WorktreePath       string
	MovedBranch        string
	RestoredBranch     string
	PrepProfile        string
	PreparedPaths      []string
	PreparationWarning string
}

type repositoryIntegrityFamily struct {
	rootPath string
	members  []model.ProjectSummary
}

func (s *Service) RepositoryIntegrityStates(ctx context.Context) (map[string]model.RepositoryIntegrityState, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("service unavailable")
	}
	projects, err := s.store.ListProjects(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("list projects for repository integrity: %w", err)
	}
	policies, err := s.store.ListRepositoryRootPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repository root policies: %w", err)
	}

	families := repositoryIntegrityFamilies(projects, policies)
	states := make(map[string]model.RepositoryIntegrityState, len(families))
	for _, family := range families {
		policy, ok := policies[family.rootPath]
		if !ok || strings.TrimSpace(policy.ExpectedBranch) == "" {
			expected, source := repositoryExpectedBranchFromMembers(family.members)
			if expected == "" && repositoryFamilyHasLinkedWorktree(family.members) {
				expected = repositoryRemoteDefaultBranch(ctx, family.rootPath)
				if expected != "" {
					source = repositoryExpectedBranchRemoteDefault
				}
			}
			if expected == "" {
				continue
			}
			policy = model.RepositoryRootPolicy{
				RootPath:             family.rootPath,
				ExpectedBranch:       expected,
				ExpectedBranchSource: source,
				Mode:                 model.RepositoryIntegrityModeWarn,
				UpdatedAt:            time.Now(),
			}
			if err := s.store.UpsertRepositoryRootPolicy(ctx, policy); err != nil {
				return nil, fmt.Errorf("record repository root policy for %s: %w", family.rootPath, err)
			}
		}
		state := repositoryIntegrityStateFromFamily(policy, family)
		state.RecentExcursions = s.recentRepositoryWorkspaceExcursions(ctx, family.rootPath, 5)
		if state.Displaced {
			s.assessRepositoryIntegrityRepair(ctx, &state)
			state.Acknowledged = strings.TrimSpace(policy.AcknowledgedFingerprint) == state.Fingerprint
		}
		states[family.rootPath] = state
	}
	return states, nil
}

func (s *Service) RepositoryIntegrityStateForProject(ctx context.Context, projectPath string) (model.RepositoryIntegrityState, error) {
	projectPath = cleanRepositoryIntegrityPath(projectPath)
	if projectPath == "" {
		return model.RepositoryIntegrityState{}, fmt.Errorf("project path is required")
	}
	states, err := s.RepositoryIntegrityStates(ctx)
	if err != nil {
		return model.RepositoryIntegrityState{}, err
	}
	for _, state := range states {
		if state.ContainsProject(projectPath) {
			return state, nil
		}
	}
	return model.RepositoryIntegrityState{}, sql.ErrNoRows
}

func (s *Service) EnsureRepositoryRootExpectedBranch(ctx context.Context, rootPath, expectedBranch, source string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	rootPath = cleanRepositoryIntegrityPath(rootPath)
	expectedBranch = strings.TrimSpace(expectedBranch)
	if rootPath == "" || expectedBranch == "" {
		return nil
	}
	policy, err := s.store.GetRepositoryRootPolicy(ctx, rootPath)
	switch {
	case err == nil && strings.TrimSpace(policy.ExpectedBranch) != "":
		return nil
	case err == nil:
		policy.ExpectedBranch = expectedBranch
		policy.ExpectedBranchSource = strings.TrimSpace(source)
		policy.AcknowledgedFingerprint = ""
		policy.UpdatedAt = time.Now()
	case errors.Is(err, sql.ErrNoRows):
		policy = model.RepositoryRootPolicy{
			RootPath:             rootPath,
			ExpectedBranch:       expectedBranch,
			ExpectedBranchSource: strings.TrimSpace(source),
			Mode:                 model.RepositoryIntegrityModeWarn,
			UpdatedAt:            time.Now(),
		}
	default:
		return err
	}
	return s.store.UpsertRepositoryRootPolicy(ctx, policy)
}

func (s *Service) SetRepositoryRootExpectedBranch(ctx context.Context, rootPath, expectedBranch string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	rootPath = cleanRepositoryIntegrityPath(rootPath)
	expectedBranch = strings.TrimSpace(expectedBranch)
	if rootPath == "" || expectedBranch == "" {
		return fmt.Errorf("repository root path and expected branch are required")
	}
	policy, err := s.store.GetRepositoryRootPolicy(ctx, rootPath)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		policy = model.RepositoryRootPolicy{RootPath: rootPath, Mode: model.RepositoryIntegrityModeWarn}
	}
	policy.ExpectedBranch = expectedBranch
	policy.ExpectedBranchSource = repositoryExpectedBranchUser
	policy.AcknowledgedFingerprint = ""
	policy.UpdatedAt = time.Now()
	return s.store.UpsertRepositoryRootPolicy(ctx, policy)
}

func (s *Service) AcknowledgeRepositoryIntegrity(ctx context.Context, rootPath, fingerprint string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	return s.store.SetRepositoryRootAcknowledgedFingerprint(ctx, rootPath, fingerprint)
}

func (s *Service) RecordRepositoryWorkspaceExcursion(ctx context.Context, excursion model.RepositoryWorkspaceExcursion) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service unavailable")
	}
	excursion.RootPath = cleanRepositoryIntegrityPath(excursion.RootPath)
	excursion.ProjectPath = cleanRepositoryIntegrityPath(excursion.ProjectPath)
	excursion.CWD = cleanRepositoryIntegrityPath(excursion.CWD)
	if excursion.RootPath == "" || excursion.ProjectPath == "" || excursion.CWD == "" {
		return fmt.Errorf("repository excursion root, project, and cwd are required")
	}
	if excursion.At.IsZero() {
		excursion.At = time.Now()
	}
	payload, err := json.Marshal(excursion)
	if err != nil {
		return fmt.Errorf("encode repository workspace excursion: %w", err)
	}
	return s.store.AddEvent(ctx, model.StoredEvent{
		At:          excursion.At,
		ProjectPath: excursion.RootPath,
		Type:        repositoryWorkspaceExcursionEventType,
		Payload:     string(payload),
	})
}

func (s *Service) RepairRepositoryRoot(ctx context.Context, req RepositoryIntegrityRepairRequest) (RepositoryIntegrityRepairResult, error) {
	if s == nil || s.store == nil {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("service unavailable")
	}
	rootPath := cleanRepositoryIntegrityPath(req.RootPath)
	if rootPath == "" {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("repository root path is required")
	}

	unlockCreate, err := s.worktreeCreateLocks.LockContext(ctx, rootPath)
	if err != nil {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("wait for existing worktree creation in %s: %w", rootPath, err)
	}
	defer unlockCreate()
	unlockGitWrite, err := s.lockGitWrite(ctx, rootPath)
	if err != nil {
		return RepositoryIntegrityRepairResult{}, err
	}
	defer unlockGitWrite()

	state, err := s.RepositoryIntegrityStateForProject(ctx, rootPath)
	if err != nil {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("load repository integrity incident: %w", err)
	}
	if !state.Displaced {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("repository root %s is already on its expected branch", rootPath)
	}
	s.assessRepositoryIntegrityRepair(ctx, &state)
	if !state.CanRepair {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("repository root cannot be repaired safely: %s", state.RepairBlockReason)
	}

	rootSummary, err := s.store.GetProjectSummary(ctx, rootPath, true)
	if err != nil {
		return RepositoryIntegrityRepairResult{}, fmt.Errorf("load root project: %w", err)
	}
	sourceCategoryID, sourceCategoryKnown := s.projectCategoryForWorktreeSource(ctx, rootPath)
	sourceRunCommand := strings.TrimSpace(rootSummary.RunCommand)
	worktreePath := state.SuggestedWorktreePath
	if worktreePath == "" {
		worktreePath, err = availableRepositoryRepairWorktreePath(rootPath, state.ActualBranch)
		if err != nil {
			return RepositoryIntegrityRepairResult{}, err
		}
	}

	result := RepositoryIntegrityRepairResult{
		RootPath:       rootPath,
		WorktreePath:   worktreePath,
		MovedBranch:    state.ActualBranch,
		RestoredBranch: state.ExpectedBranch,
	}
	if err := gitSwitchRepositoryBranch(ctx, rootPath, state.ExpectedBranch); err != nil {
		return result, err
	}
	if err := gitWorktreeAdd(ctx, rootPath, worktreePath, state.ActualBranch); err != nil {
		rollbackErr := gitSwitchRepositoryBranch(ctx, rootPath, state.ActualBranch)
		if rollbackErr != nil {
			return result, fmt.Errorf("move branch %s into a linked worktree: %w; additionally failed to restore the root checkout: %v", state.ActualBranch, err, rollbackErr)
		}
		return result, fmt.Errorf("move branch %s into a linked worktree: %w; the root checkout was restored to %s", state.ActualBranch, err, state.ActualBranch)
	}

	prepResult, prepErr := worktreeprep.Prepare(ctx, rootPath, worktreePath, req.PrepProfile)
	result.PrepProfile = strings.TrimSpace(prepResult.Profile)
	for _, prepared := range prepResult.Prepared {
		result.PreparedPaths = append(result.PreparedPaths, prepared.Path)
	}
	if prepErr != nil {
		result.PreparationWarning = prepErr.Error()
	}

	_, attachErr := s.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:       filepath.Dir(worktreePath),
		Name:             filepath.Base(worktreePath),
		CategoryID:       sourceCategoryID,
		CategoryExplicit: sourceCategoryKnown,
	})
	if attachErr != nil {
		return result, fmt.Errorf("repaired the root checkout and created %s, but failed to track it in Little Control Room: %w", worktreePath, attachErr)
	}
	if err := s.store.SetWorktreeInitialBranch(ctx, worktreePath, state.ActualBranch); err != nil {
		return result, fmt.Errorf("repaired the root checkout but failed to record the initial branch for %s: %w", worktreePath, err)
	}
	if sourceRunCommand != "" {
		if err := s.store.SetRunCommand(ctx, worktreePath, sourceRunCommand); err != nil {
			return result, fmt.Errorf("repaired the root checkout but failed to inherit the run command for %s: %w", worktreePath, err)
		}
	}
	if err := s.store.SetWorktreeParentBranch(ctx, worktreePath, state.ExpectedBranch); err != nil {
		return result, fmt.Errorf("repaired the root checkout but failed to record the parent branch for %s: %w", worktreePath, err)
	}
	_ = s.store.SetRepositoryRootAcknowledgedFingerprint(ctx, rootPath, "")
	if err := s.RefreshProjectStatus(ctx, rootPath); err != nil {
		return result, fmt.Errorf("repaired repository root but failed to refresh %s: %w", rootPath, err)
	}
	if err := s.RefreshProjectStatus(ctx, worktreePath); err != nil {
		return result, fmt.Errorf("repaired repository root but failed to refresh %s: %w", worktreePath, err)
	}

	now := time.Now()
	payload, _ := json.Marshal(map[string]string{
		"root_path":       rootPath,
		"worktree_path":   worktreePath,
		"moved_branch":    state.ActualBranch,
		"restored_branch": state.ExpectedBranch,
	})
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: rootPath,
		Type:        repositoryRootRepairEventType,
		Payload:     string(payload),
	})
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ProjectChanged,
			At:          now,
			ProjectPath: rootPath,
			Payload: map[string]string{
				"action":        "repair_repository_root",
				"worktree_path": worktreePath,
			},
		})
	}
	return result, nil
}

func repositoryIntegrityFamilies(projects []model.ProjectSummary, policies map[string]model.RepositoryRootPolicy) []repositoryIntegrityFamily {
	byRoot := map[string]*repositoryIntegrityFamily{}
	for _, project := range projects {
		rootPath := cleanRepositoryIntegrityPath(project.Path)
		if project.WorktreeKind == model.WorktreeKindLinked {
			rootPath = cleanRepositoryIntegrityPath(project.WorktreeRootPath)
		}
		if rootPath == "" {
			continue
		}
		family := byRoot[rootPath]
		if family == nil {
			family = &repositoryIntegrityFamily{rootPath: rootPath}
			byRoot[rootPath] = family
		}
		family.members = append(family.members, project)
	}
	for rootPath := range policies {
		rootPath = cleanRepositoryIntegrityPath(rootPath)
		if rootPath == "" {
			continue
		}
		if byRoot[rootPath] == nil {
			byRoot[rootPath] = &repositoryIntegrityFamily{rootPath: rootPath}
		}
	}

	roots := make([]string, 0, len(byRoot))
	for rootPath, family := range byRoot {
		if _, hasPolicy := policies[rootPath]; !hasPolicy && !repositoryFamilyHasLinkedWorktree(family.members) {
			continue
		}
		roots = append(roots, rootPath)
	}
	sort.Strings(roots)
	out := make([]repositoryIntegrityFamily, 0, len(roots))
	for _, rootPath := range roots {
		family := byRoot[rootPath]
		sort.SliceStable(family.members, func(i, j int) bool {
			if family.members[i].Path == rootPath {
				return true
			}
			if family.members[j].Path == rootPath {
				return false
			}
			return family.members[i].Path < family.members[j].Path
		})
		out = append(out, *family)
	}
	return out
}

func repositoryExpectedBranchFromMembers(members []model.ProjectSummary) (string, string) {
	expected := ""
	for _, member := range members {
		if member.WorktreeKind != model.WorktreeKindLinked || !member.PresentOnDisk || member.Forgotten {
			continue
		}
		parent := strings.TrimSpace(member.WorktreeParentBranch)
		if parent == "" {
			continue
		}
		if expected == "" {
			expected = parent
			continue
		}
		if expected != parent {
			return "", ""
		}
	}
	if expected == "" {
		return "", ""
	}
	return expected, repositoryExpectedBranchLinkedParent
}

func repositoryFamilyHasLinkedWorktree(members []model.ProjectSummary) bool {
	for _, member := range members {
		if member.WorktreeKind == model.WorktreeKindLinked && member.PresentOnDisk && !member.Forgotten {
			return true
		}
	}
	return false
}

func repositoryIntegrityStateFromFamily(policy model.RepositoryRootPolicy, family repositoryIntegrityFamily) model.RepositoryIntegrityState {
	state := model.RepositoryIntegrityState{
		RootPath:             family.rootPath,
		RootName:             filepath.Base(family.rootPath),
		ExpectedBranch:       strings.TrimSpace(policy.ExpectedBranch),
		ExpectedBranchSource: strings.TrimSpace(policy.ExpectedBranchSource),
		Mode:                 model.NormalizeRepositoryIntegrityMode(policy.Mode),
	}
	for _, project := range family.members {
		member := model.RepositoryIntegrityMember{
			Path:         project.Path,
			Name:         project.Name,
			Branch:       strings.TrimSpace(project.RepoBranch),
			ParentBranch: strings.TrimSpace(project.WorktreeParentBranch),
			WorktreeKind: project.WorktreeKind,
			Dirty:        project.RepoDirty,
			Conflict:     project.RepoConflict,
			Present:      project.PresentOnDisk,
		}
		state.Members = append(state.Members, member)
		if cleanRepositoryIntegrityPath(project.Path) != family.rootPath {
			continue
		}
		state.RootName = project.Name
		state.ActualBranch = strings.TrimSpace(project.RepoBranch)
		state.RootDirty = project.RepoDirty
		state.RootConflict = project.RepoConflict
	}
	state.Displaced = state.ExpectedBranch != "" && state.ActualBranch != "" && state.ExpectedBranch != state.ActualBranch
	state.Fingerprint = repositoryIntegrityFingerprint(state)
	state.Acknowledged = state.Displaced && strings.TrimSpace(policy.AcknowledgedFingerprint) == state.Fingerprint
	return state
}

func (s *Service) assessRepositoryIntegrityRepair(ctx context.Context, state *model.RepositoryIntegrityState) {
	if state == nil {
		return
	}
	state.CanRepair = false
	state.RepairBlockReason = ""
	rootPath := cleanRepositoryIntegrityPath(state.RootPath)
	if rootPath == "" {
		state.RepairBlockReason = "the repository root path is unavailable"
		return
	}
	status, err := scanner.ReadGitRepoStatus(ctx, rootPath)
	if err != nil {
		state.RepairBlockReason = "LCR could not read the live root checkout: " + err.Error()
		return
	}
	state.ActualBranch = strings.TrimSpace(status.Branch)
	state.RootDirty = status.Dirty
	state.RootConflict = repositoryStatusHasConflict(status)
	state.Displaced = state.ExpectedBranch != "" && state.ActualBranch != "" && state.ExpectedBranch != state.ActualBranch
	state.Fingerprint = repositoryIntegrityFingerprint(*state)
	if !state.Displaced {
		state.RepairBlockReason = "the live root checkout is not displaced"
		return
	}
	if state.RootConflict {
		state.RepairBlockReason = "the root checkout has unresolved conflicts"
		return
	}
	if state.RootDirty {
		state.RepairBlockReason = "the root checkout has uncommitted changes"
		return
	}
	for _, branch := range []string{state.ExpectedBranch, state.ActualBranch} {
		exists, err := gitLocalBranchExists(ctx, rootPath, branch)
		if err != nil {
			state.RepairBlockReason = err.Error()
			return
		}
		if !exists {
			state.RepairBlockReason = fmt.Sprintf("local branch %s does not exist", branch)
			return
		}
	}
	worktrees, err := scanner.ListGitWorktrees(ctx, rootPath)
	if err != nil {
		state.RepairBlockReason = "LCR could not inspect linked worktrees: " + err.Error()
		return
	}
	for _, worktree := range worktrees {
		path := cleanRepositoryIntegrityPath(worktree.Path)
		branch := strings.TrimSpace(worktree.Branch)
		if path != "" && path != rootPath && branch == state.ExpectedBranch {
			state.RepairBlockReason = fmt.Sprintf("expected branch %s is already checked out at %s", state.ExpectedBranch, path)
			return
		}
	}
	if err := gitlock.CheckIndexAndModuleLocks(ctx, rootPath); err != nil {
		state.RepairBlockReason = err.Error()
		return
	}
	worktreePath, err := availableRepositoryRepairWorktreePath(rootPath, state.ActualBranch)
	if err != nil {
		state.RepairBlockReason = err.Error()
		return
	}
	state.SuggestedWorktreePath = worktreePath
	state.CanRepair = true
}

func availableRepositoryRepairWorktreePath(rootPath, branch string) (string, error) {
	rootPath = cleanRepositoryIntegrityPath(rootPath)
	suffix := sanitizeWorktreeSuffix(strings.ReplaceAll(strings.TrimSpace(branch), "/", "-"))
	if rootPath == "" || suffix == "" {
		return "", fmt.Errorf("could not derive a linked-worktree path for branch %s", branch)
	}
	basePath := suggestedTodoWorktreePath(rootPath, suffix)
	for attempt := 0; attempt < 100; attempt++ {
		candidate := basePath
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", basePath, attempt+1)
		}
		_, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check suggested repair worktree path %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("could not find an available linked-worktree path for branch %s", branch)
}

func repositoryStatusHasConflict(status scanner.GitRepoStatus) bool {
	for _, change := range status.Changes {
		if change.Kind == scanner.GitChangeUnmerged {
			return true
		}
	}
	return false
}

func repositoryIntegrityFingerprint(state model.RepositoryIntegrityState) string {
	raw := strings.Join([]string{
		cleanRepositoryIntegrityPath(state.RootPath),
		strings.TrimSpace(state.ExpectedBranch),
		strings.TrimSpace(state.ActualBranch),
		fmt.Sprintf("dirty=%t", state.RootDirty),
		fmt.Sprintf("conflict=%t", state.RootConflict),
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func repositoryRemoteDefaultBranch(ctx context.Context, rootPath string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", rootPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	branch = strings.TrimPrefix(branch, "origin/")
	return strings.TrimSpace(branch)
}

func gitSwitchRepositoryBranch(ctx context.Context, rootPath, branch string) error {
	if err := gitlock.CheckIndexAndModuleLocks(ctx, rootPath); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", rootPath, "switch", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gitCommandError(fmt.Sprintf("switch repository root %s to %s", rootPath, branch), err, out)
	}
	return nil
}

func (s *Service) recentRepositoryWorkspaceExcursions(ctx context.Context, rootPath string, limit int) []model.RepositoryWorkspaceExcursion {
	events, err := s.store.ListRecentEventsByTypeForProject(ctx, repositoryWorkspaceExcursionEventType, rootPath, limit)
	if err != nil {
		return nil
	}
	out := make([]model.RepositoryWorkspaceExcursion, 0, len(events))
	for _, event := range events {
		var excursion model.RepositoryWorkspaceExcursion
		if err := json.Unmarshal([]byte(event.Payload), &excursion); err != nil {
			continue
		}
		if excursion.At.IsZero() {
			excursion.At = event.At
		}
		out = append(out, excursion)
	}
	return out
}

func cleanRepositoryIntegrityPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if path == "." {
		return ""
	}
	return path
}
