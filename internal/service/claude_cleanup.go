package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/store"
)

// The existing cleanup DTOs are shared with Codex so both providers use the same
// review, privacy, progress and cancellation UI. Mutation remains provider-specific.
func (s *Service) AuditSessionStorage(ctx context.Context, provider codexapp.Provider, options CodexCleanupAuditOptions) (CodexCleanupAudit, error) {
	switch provider.Normalized() {
	case codexapp.ProviderCodex:
		return s.AuditCodexSessionStorage(ctx, options)
	case codexapp.ProviderClaudeCode:
		return s.AuditClaudeSessionStorage(ctx, options)
	default:
		return CodexCleanupAudit{}, fmt.Errorf("session cleanup is unavailable for %q", provider)
	}
}

func (s *Service) DeleteSessionCleanupWorktree(ctx context.Context, provider codexapp.Provider, request DeleteCodexCleanupWorktreeRequest) (DeleteCodexCleanupWorktreeResult, error) {
	switch provider.Normalized() {
	case codexapp.ProviderCodex:
		return s.DeleteCodexCleanupWorktree(ctx, request)
	case codexapp.ProviderClaudeCode:
		return s.DeleteClaudeCleanupWorktree(ctx, request)
	default:
		return DeleteCodexCleanupWorktreeResult{}, fmt.Errorf("session cleanup is unavailable for %q", provider)
	}
}

func (s *Service) AuditClaudeSessionStorage(ctx context.Context, options CodexCleanupAuditOptions) (CodexCleanupAudit, error) {
	audit := CodexCleanupAudit{}
	if s == nil || s.store == nil {
		return audit, fmt.Errorf("service unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	days := options.InactiveDays
	if days == 0 {
		days = 7
	}
	if (options.Category != CodexCleanupOrphaned && options.Category != CodexCleanupStale) ||
		(days != 7 && days != 14 && days != 30 && days != 90) || (options.Category == CodexCleanupOrphaned && days != 7) {
		return audit, fmt.Errorf("invalid session cleanup policy")
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	audit = CodexCleanupAudit{Category: options.Category, InactiveDays: days, AuditedAt: now, RecentCutoff: now.Add(-time.Duration(days) * 24 * time.Hour), WorktreeGraceCutoff: now.Add(-CodexCleanupDeletedWorktreeGrace)}
	home := s.Config().ClaudeCodeHome
	if !filepath.IsAbs(home) {
		return audit, fmt.Errorf("Claude Code home must be an absolute path")
	}
	root, err := os.OpenRoot(home)
	if errors.Is(err, fs.ErrNotExist) {
		return audit, nil
	}
	if err != nil {
		return audit, err
	}
	defer root.Close()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return audit, err
	}
	storageIdentity := filepath.Clean(home) + ":" + claudeCleanupFileIdentity(rootInfo)
	inv, err := inspectClaudeCleanup(ctx, root)
	if err != nil {
		return audit, fmt.Errorf("inspect Claude storage: %w", err)
	}
	audit.Storage = inv.storage
	audit.Storage.files = nil
	live, liveCWDs, err := claudeCleanupLiveSessions(root)
	if err != nil {
		return audit, err
	}
	for _, id := range options.LoadedThreadIDs {
		live[id] = struct{}{}
	}
	records, err := s.store.ListDeletedWorktreeRecords(ctx)
	if err != nil {
		return audit, err
	}
	byPath := map[string]store.DeletedWorktreeRecord{}
	for _, r := range records {
		byPath[normalizeCleanupPath(r.Path)] = r
	}
	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return audit, err
	}
	newest := map[string]*claudeCleanupTree{}
	counts := map[string]int{}
	for _, tree := range inv.trees {
		audit.ScannedThreads += 1 + tree.DescendantCount
		counts[tree.cwd] += 1 + tree.DescendantCount
		previous := newest[tree.cwd]
		if previous == nil || tree.LastActivity.After(previous.LastActivity) || (tree.LastActivity.Equal(previous.LastActivity) && tree.ID > previous.ID) {
			newest[tree.cwd] = tree
		}
	}
	groups := map[string]*CodexCleanupWorktreeGroup{}
	eligibleFiles := map[string]bool{}
	for _, tree := range inv.trees {
		if err := ctx.Err(); err != nil {
			return audit, err
		}
		record, recorded := byPath[tree.cwd]
		missing, pathErr := cleanupPathMissing(tree.cwd)
		if missing {
			audit.MissingCWDThreads += 1 + tree.DescendantCount
		}
		if options.Category == CodexCleanupStale {
			if missing || newest[tree.cwd] == tree {
				continue
			}
		} else {
			if !missing {
				continue
			}
			if !recorded {
				audit.Excluded.NoLCRRecord++
				continue
			}
		}
		uncertain := tree.uncertain || inv.unknownProjects[tree.project] || pathErr != nil
		if options.Category == CodexCleanupOrphaned {
			uncertain = uncertain || record.Archived || record.HasOpenTodo || record.MissingSince.IsZero() || !cleanupRootProjectCertain(record.RootPath, tree.cwd)
			if record.MissingSince.After(audit.WorktreeGraceCutoff) {
				audit.Excluded.Recent++
				continue
			}
		} else {
			info, err := os.Stat(tree.cwd)
			uncertain = uncertain || err != nil || !info.IsDir()
		}
		pinned := tree.pinned || record.Pinned || projects[tree.cwd].Pinned || projects[record.RootPath].Pinned
		loaded := false
		for cwd := range tree.cwds {
			// A child working in a different folder may be that folder's newest history.
			// Retain the entire parent tree until that relationship can be audited safely.
			uncertain = uncertain || cwd != tree.cwd
			pinned = pinned || projects[cwd].Pinned
			if _, ok := liveCWDs[cwd]; ok {
				loaded = true
			}
		}
		for _, id := range tree.MemberIDs {
			if _, ok := live[id]; ok {
				loaded = true
			}
		}
		switch {
		case uncertain:
			audit.Excluded.Uncertain++
			continue
		case pinned:
			audit.Excluded.Pinned++
			continue
		case loaded:
			audit.Excluded.Loaded++
			continue
		case pathOnExternalVolume(tree.cwd, home) || (options.Category == CodexCleanupOrphaned && pathOnExternalVolume(record.RootPath, home)):
			audit.Excluded.ExternalVolume++
			continue
		case tree.LastActivity.IsZero() || tree.LastActivity.After(audit.RecentCutoff):
			audit.Excluded.Recent++
			continue
		}
		group := groups[tree.cwd]
		if group == nil {
			group = &CodexCleanupWorktreeGroup{
				StorageIdentity:  storageIdentity,
				Provider:         codexapp.ProviderClaudeCode,
				Category:         options.Category,
				InactiveDays:     days,
				WorktreePath:     tree.cwd,
				WorktreeName:     filepath.Base(tree.cwd),
				RootProjectPath:  record.RootPath,
				MissingSince:     record.MissingSince,
				TotalThreadCount: counts[tree.cwd],
				Reason:           codexCleanupReason,
			}
			if options.Category == CodexCleanupStale {
				group.RootProjectPath = tree.cwd
				group.MissingSince = time.Time{}
				group.Reason = fmt.Sprintf("Inactive for at least %d days; newest session and protected trees are kept", days)
			}
			groups[tree.cwd] = group
		}
		group.Threads = append(group.Threads, tree.CodexCleanupThread)
		group.RootThreadCount++
		group.DescendantCount += tree.DescendantCount
		group.RecoverableBytes += tree.RecoverableBytes
		if tree.LastActivity.After(group.LastActivity) {
			group.LastActivity = tree.LastActivity
		}
		for _, f := range tree.RolloutFiles {
			eligibleFiles[f.Path] = true
		}
	}
	for _, g := range groups {
		sort.Slice(g.Threads, func(i, j int) bool { return g.Threads[i].ID < g.Threads[j].ID })
		g.Revision = cleanupGroupRevision(*g)
		audit.Groups = append(audit.Groups, *g)
		audit.EligibleRootThreads += g.RootThreadCount
		audit.EligibleDescendants += g.DescendantCount
		audit.RecoverableBytes += g.RecoverableBytes
	}
	sort.Slice(audit.Groups, func(i, j int) bool { return audit.Groups[i].WorktreePath < audit.Groups[j].WorktreePath })
	// Attribute only unambiguous folders; unknown storage gets a generic label so
	// directory encodings cannot accidentally reveal private project names.
	projectCWD := map[string]string{}
	ambiguous := map[string]bool{}
	for _, tree := range inv.trees {
		if old := projectCWD[tree.project]; old != "" && old != tree.cwd {
			ambiguous[tree.project] = true
		}
		projectCWD[tree.project] = tree.cwd
	}
	retained := map[string]*CodexCleanupRetainedGroup{}
	for path, size := range inv.storage.files {
		if eligibleFiles[path] {
			continue
		}
		cwd := ""
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) > 2 && parts[0] == "projects" && !ambiguous[parts[1]] {
			cwd = projectCWD[parts[1]]
		}
		g := retained[cwd]
		if g == nil {
			g = &CodexCleanupRetainedGroup{Name: "Other Claude storage", Reason: "Unattributed or non-session files; retained"}
			if cwd != "" {
				g.Name = filepath.Base(cwd)
				g.ProjectPath = cwd
				g.Path = cwd
				g.Reason = "Protected or ineligible sessions and associated files"
			}
			retained[cwd] = g
		}
		g.Bytes += size
		g.Files++
	}
	for _, g := range retained {
		audit.Retained = append(audit.Retained, *g)
	}
	sort.Slice(audit.Retained, func(i, j int) bool {
		if audit.Retained[i].Bytes == audit.Retained[j].Bytes {
			return audit.Retained[i].Name < audit.Retained[j].Name
		}
		return audit.Retained[i].Bytes > audit.Retained[j].Bytes
	})
	return audit, nil
}

func (s *Service) DeleteClaudeCleanupWorktree(ctx context.Context, request DeleteCodexCleanupWorktreeRequest) (result DeleteCodexCleanupWorktreeResult, returnErr error) {
	result.WorktreePath = normalizeCleanupPath(request.WorktreePath)
	if s == nil || s.store == nil {
		return result, fmt.Errorf("service unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := sortedUniqueStrings(request.RootThreadIDs)
	result.RequestedRootThreads = len(ids)
	if len(ids) == 0 || request.Revision == "" || !filepath.IsAbs(request.RootProjectPath) || !filepath.IsAbs(result.WorktreePath) {
		return result, fmt.Errorf("explicit session selection and preview identity are required")
	}
	progress := CodexCleanupProgress{TotalRoots: len(ids), Phase: "Rechecking cleanup safety"}
	publish := func() {
		if request.Progress != nil {
			request.Progress(progress)
		}
	}
	unlock, err := s.worktreeCreateLocks.LockContext(ctx, request.RootProjectPath)
	if err != nil {
		return result, err
	}
	progress.HoldsRepositoryLock = true
	defer func() { unlock(); progress.HoldsRepositoryLock = false; publish() }()
	publish()
	loaded := append([]string(nil), request.LoadedThreadIDs...)
	if request.CurrentLoadedThreadIDs != nil {
		loaded = append(loaded, request.CurrentLoadedThreadIDs()...)
	}
	audit, err := s.AuditClaudeSessionStorage(ctx, CodexCleanupAuditOptions{Category: request.Category, InactiveDays: request.InactiveDays, LoadedThreadIDs: loaded})
	if err != nil {
		return result, err
	}
	var group CodexCleanupWorktreeGroup
	for _, g := range audit.Groups {
		if g.WorktreePath == result.WorktreePath {
			group = g
			break
		}
	}
	currentIDs := []string{}
	for _, tree := range group.Threads {
		currentIDs = append(currentIDs, tree.ID)
	}
	if group.Revision != request.Revision || group.RootProjectPath != request.RootProjectPath || !equalStringSlices(ids, sortedUniqueStrings(currentIDs)) {
		return result, fmt.Errorf("cleanup preview changed; review the refreshed audit before deleting")
	}
	result.ExpectedBytes = group.RecoverableBytes
	root, err := os.OpenRoot(s.Config().ClaudeCodeHome)
	if err != nil {
		return result, err
	}
	defer root.Close()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return result, err
	}
	if filepath.Clean(s.Config().ClaudeCodeHome)+":"+claudeCleanupFileIdentity(rootInfo) != group.StorageIdentity {
		return result, fmt.Errorf("Claude storage root changed; refresh before deleting")
	}
	// Always verify completed removals, including on cancellation or partial failure.
	removed := map[string]bool{}
	defer func() {
		for _, tree := range group.Threads {
			complete := true
			for _, file := range tree.RolloutFiles {
				_, err := root.Lstat(file.Path)
				if removed[file.Path] && errors.Is(err, fs.ErrNotExist) {
					result.VerifiedReclaimedBytes += file.Size
				} else {
					complete = false
				}
			}
			if complete {
				result.DeletedRootThreads++
				result.DeletedDescendants += tree.DescendantCount
				result.DeletedThreadIDs = append(result.DeletedThreadIDs, tree.MemberIDs...)
			}
		}
		result.Verified = result.DeletedRootThreads == result.RequestedRootThreads && result.VerifiedReclaimedBytes == result.ExpectedBytes
		progress.ActiveRoots = 0
		progress.CompletedRoots = result.DeletedRootThreads
		progress.VerifiedReclaimedBytes = result.VerifiedReclaimedBytes
		progress.Phase = "Storage verification complete"
		publish()
		if !result.Verified && returnErr == nil {
			returnErr = fmt.Errorf("Claude cleanup incomplete; refresh the storage audit")
		}
	}()
	for _, tree := range group.Threads {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		progress.Phase = "Deleting Claude transcript files"
		progress.ActiveRoots = 1
		publish()
		projects, err := s.store.GetProjectSummaryMap(ctx)
		if err != nil {
			return result, err
		}
		if projects[group.WorktreePath].Pinned || projects[group.RootProjectPath].Pinned {
			return result, fmt.Errorf("project was pinned; refresh before deleting")
		}
		live, cwds, err := claudeCleanupLiveSessions(root)
		if err != nil {
			return result, err
		}
		if request.CurrentLoadedThreadIDs != nil {
			for _, id := range request.CurrentLoadedThreadIDs() {
				live[id] = struct{}{}
			}
		}
		if _, ok := cwds[group.WorktreePath]; ok {
			return result, fmt.Errorf("Claude session became active; refresh before deleting")
		}
		for _, id := range tree.MemberIDs {
			if _, ok := live[id]; ok {
				return result, fmt.Errorf("Claude session was loaded; refresh before deleting")
			}
		}
		if group.Category == CodexCleanupOrphaned {
			missing, err := cleanupPathMissing(group.WorktreePath)
			if err != nil || !missing {
				return result, fmt.Errorf("working directory reappeared; refresh before deleting")
			}
		}
		// Check the complete tree before removing any member, and each file again at
		// its deletion boundary. Never recursively remove a directory or follow links.
		if err := revalidateClaudeCleanupTree(ctx, root, tree); err != nil {
			return result, err
		}
		files := append([]CodexCleanupRolloutFile(nil), tree.RolloutFiles...)
		// Keep the parent transcript until its associated files have been removed.
		sort.SliceStable(files, func(i, j int) bool { return strings.Count(files[i].Path, "/") > strings.Count(files[j].Path, "/") })
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := removeClaudeCleanupFile(ctx, root, file); err != nil {
				return result, err
			}
			removed[file.Path] = true
			if _, err := root.Lstat(file.Path); !errors.Is(err, fs.ErrNotExist) {
				return result, fmt.Errorf("Claude artifact removal could not be verified")
			}
		}
		progress.CompletedRoots++
		progress.ActiveRoots = 0
		progress.VerifiedReclaimedBytes += tree.RecoverableBytes
		publish()
	}
	return result, nil
}

func revalidateClaudeCleanupTree(ctx context.Context, root *os.Root, tree CodexCleanupThread) error {
	expected := map[string]bool{}
	parent := ""
	for _, f := range tree.RolloutFiles {
		expected[f.Path] = true
		parts := strings.Split(filepath.ToSlash(f.Path), "/")
		if len(parts) == 3 && parts[0] == "projects" && parts[2] == tree.ID+".jsonl" {
			parent = strings.TrimSuffix(f.Path, ".jsonl")
		}
		if err := validateClaudeCleanupPath(root, f.Path); err != nil {
			return err
		}
		fresh, err := claudeCleanupFile(ctx, root, f.Path)
		if err != nil || fresh != claudeCleanupFileWithoutThreadID(f) {
			return fmt.Errorf("Claude session changed during audit; refresh before deleting")
		}
	}
	if parent == "" {
		return fmt.Errorf("missing Claude parent transcript")
	}
	err := fs.WalkDir(root.FS(), parent, func(path string, e fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == parent {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 || (!e.IsDir() && !expected[path]) {
			return fmt.Errorf("Claude session tree changed during audit")
		}
		return nil
	})
	return err
}

func validateClaudeCleanupPath(root *os.Root, path string) error {
	if !filepath.IsLocal(path) {
		return fmt.Errorf("unsafe Claude artifact path")
	}
	base, err := root.Stat(".")
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	current := ""
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Sys().(*syscall.Stat_t).Dev != base.Sys().(*syscall.Stat_t).Dev {
			return fmt.Errorf("Claude artifact crosses a filesystem boundary")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Claude artifact path contains a symlink")
		}
	}
	return nil
}

func removeClaudeCleanupFile(ctx context.Context, root *os.Root, file CodexCleanupRolloutFile) error {
	if err := validateClaudeCleanupPath(root, file.Path); err != nil {
		return err
	}
	// Pin the containing directory with a descriptor before inspecting/removing the
	// basename. A concurrent parent rename cannot redirect deletion to a sibling.
	parentInfo, err := root.Lstat(filepath.Dir(file.Path))
	if err != nil {
		return err
	}
	parent, err := root.OpenRoot(filepath.Dir(file.Path))
	if err != nil {
		return err
	}
	defer parent.Close()
	openedParent, err := parent.Stat(".")
	if err != nil || !os.SameFile(parentInfo, openedParent) {
		return fmt.Errorf("Claude artifact directory changed before removal")
	}
	name := filepath.Base(file.Path)
	fresh, err := claudeCleanupFile(ctx, parent, name)
	fresh.Path = file.Path
	if err != nil || fresh != claudeCleanupFileWithoutThreadID(file) {
		return fmt.Errorf("Claude artifact changed before removal")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return parent.Remove(name)
}
