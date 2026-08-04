package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexstate"
	"lcroom/internal/store"
)

const (
	CodexCleanupRecentWindow         = 30 * 24 * time.Hour
	CodexCleanupDeletedWorktreeGrace = 7 * 24 * time.Hour
	defaultCodexCleanupAuditInterval = 24 * time.Hour
)

const codexCleanupReason = "LCR removed this linked worktree and its saved working directory is still missing"

type CodexCleanupAuditOptions struct {
	Now             time.Time
	LoadedThreadIDs []string
}

type CodexCleanupExclusions struct {
	Pinned         int
	Loaded         int
	Recent         int
	ExternalVolume int
	NoLCRRecord    int
	Uncertain      int
}

func (e CodexCleanupExclusions) Total() int {
	return e.Pinned + e.Loaded + e.Recent + e.ExternalVolume + e.NoLCRRecord + e.Uncertain
}

type CodexCleanupAudit struct {
	AuditedAt           time.Time
	RecentCutoff        time.Time
	WorktreeGraceCutoff time.Time
	ScannedThreads      int
	MissingCWDThreads   int
	EligibleRootThreads int
	EligibleDescendants int
	RecoverableBytes    int64
	Groups              []CodexCleanupWorktreeGroup
	Excluded            CodexCleanupExclusions
}

type CodexCleanupAuditSnapshot struct {
	Audit       CodexCleanupAudit
	CompletedAt time.Time
	Error       string
}

type CodexCleanupWorktreeGroup struct {
	WorktreePath     string
	WorktreeName     string
	RootProjectPath  string
	Branch           string
	ParentBranch     string
	MissingSince     time.Time
	LastActivity     time.Time
	Reason           string
	RootThreadCount  int
	DescendantCount  int
	RecoverableBytes int64
	Revision         string
	Threads          []CodexCleanupThread
}

type CodexCleanupThread struct {
	ID               string
	Title            string
	GitSHA           string
	GitBranch        string
	GitOriginURL     string
	StartedAt        time.Time
	LastActivity     time.Time
	Archived         bool
	DescendantCount  int
	RecoverableBytes int64
	MemberIDs        []string
	RolloutFiles     []CodexCleanupRolloutFile
}

type CodexCleanupRolloutFile struct {
	ThreadID string
	Path     string
	Size     int64
	ModTime  time.Time
}

type DeleteCodexCleanupWorktreeRequest struct {
	WorktreePath    string
	RootThreadIDs   []string
	Revision        string
	LoadedThreadIDs []string
}

type DeleteCodexCleanupWorktreeResult struct {
	WorktreePath           string
	RequestedRootThreads   int
	DeletedRootThreads     int
	DeletedDescendants     int
	ExpectedBytes          int64
	VerifiedReclaimedBytes int64
	Verified               bool
	DeletedThreadIDs       []string
}

type codexCleanupThreadNode struct {
	thread     codexstate.Thread
	lineage    codexstate.ThreadLineage
	rootID     string
	file       CodexCleanupRolloutFile
	lineageErr error
	safetyErr  error
}

func (s *Service) AuditCodexSessionStorage(ctx context.Context, options CodexCleanupAuditOptions) (CodexCleanupAudit, error) {
	if s == nil || s.store == nil {
		return CodexCleanupAudit{}, fmt.Errorf("service unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	audit := CodexCleanupAudit{
		AuditedAt:           now,
		RecentCutoff:        now.Add(-CodexCleanupRecentWindow),
		WorktreeGraceCutoff: now.Add(-CodexCleanupDeletedWorktreeGrace),
	}

	records, err := s.store.ListDeletedWorktreeRecords(ctx)
	if err != nil {
		return audit, fmt.Errorf("load LCR deleted-worktree records: %w", err)
	}
	recordsByPath := make(map[string]store.DeletedWorktreeRecord, len(records))
	for _, record := range records {
		if path := normalizeCleanupPath(record.Path); path != "" {
			recordsByPath[path] = record
		}
	}
	if len(recordsByPath) == 0 {
		return audit, nil
	}

	codexHome := codexstate.ResolveHomeRoot(s.Config().CodexHome)
	threads, err := codexstate.ListThreadsIncludingUnknownCWD(ctx, codexHome)
	if err != nil {
		return audit, fmt.Errorf("load Codex thread index: %w", err)
	}
	audit.ScannedThreads = len(threads)
	loaded := stringSet(options.LoadedThreadIDs)

	candidateThreads := make([]codexstate.Thread, 0)
	candidateIDs := make(map[string]bool)
	threadsByID := make(map[string]codexstate.Thread, len(threads))
	relevantIDs := make(map[string]bool)
	descendantEvidenceByID := make(map[string]bool)
	for _, thread := range threads {
		threadID := strings.TrimSpace(thread.ID)
		threadsByID[threadID] = thread
		cwd := normalizeCleanupPath(thread.CWD)
		if _, ok := recordsByPath[cwd]; ok {
			candidateThreads = append(candidateThreads, thread)
			candidateIDs[threadID] = true
			relevantIDs[threadID] = true
		}
		if strings.TrimSpace(thread.AgentRole) != "" || codexstate.ThreadSourceParentID(thread.Source) != "" {
			descendantEvidenceByID[threadID] = true
			relevantIDs[threadID] = true
		}
	}
	if len(candidateThreads) == 0 {
		return audit, nil
	}

	nodes := make(map[string]*codexCleanupThreadNode, len(relevantIDs))
	unknownByCWD := make(map[string]bool)
	missingByID := make(map[string]bool, len(candidateThreads))
	type pathInspection struct {
		missing bool
		err     error
	}
	pathInspections := make(map[string]pathInspection)
	unresolvedDescendantLineage := false
	for _, thread := range threads {
		if err := ctx.Err(); err != nil {
			return audit, err
		}
		threadID := strings.TrimSpace(thread.ID)
		if !relevantIDs[threadID] {
			continue
		}
		cwd := normalizeCleanupPath(thread.CWD)
		inspection, inspected := pathInspections[cwd]
		if !inspected {
			inspection.missing, inspection.err = cleanupPathMissing(cwd)
			pathInspections[cwd] = inspection
		}
		missing, inspectErr := inspection.missing, inspection.err
		if inspectErr != nil {
			unknownByCWD[cwd] = true
		}
		if missing && candidateIDs[threadID] {
			missingByID[threadID] = true
			audit.MissingCWDThreads++
		}

		node := &codexCleanupThreadNode{thread: thread}
		if inspectErr != nil || threadID == "" || cwd == "" || !filepath.IsAbs(cwd) {
			node.safetyErr = firstNonNil(inspectErr, fmt.Errorf("saved working directory is not an absolute local path"))
			unknownByCWD[cwd] = true
		}
		if !thread.PinnedKnown {
			node.safetyErr = firstNonNil(node.safetyErr, fmt.Errorf("Codex pin state is unavailable"))
			unknownByCWD[cwd] = true
		}
		if !thread.AgentRoleKnown {
			node.safetyErr = firstNonNil(node.safetyErr, fmt.Errorf("Codex agent-role state is unavailable"))
			unknownByCWD[cwd] = true
		}
		file, fileErr := inspectCleanupRolloutFile(codexHome, threadID, thread.RolloutPath)
		if fileErr != nil {
			node.safetyErr = firstNonNil(node.safetyErr, fileErr)
			node.lineageErr = fileErr
			unknownByCWD[cwd] = true
		} else {
			node.file = file
			lineage, lineageErr := codexstate.ReadThreadLineage(file.Path, threadID)
			if lineageErr != nil || !lineage.Known {
				node.lineageErr = firstNonNil(lineageErr, fmt.Errorf("thread lineage is unavailable"))
				unknownByCWD[cwd] = true
			} else {
				node.lineage = lineage
			}
		}
		if node.lineageErr != nil && descendantEvidenceByID[threadID] {
			candidatePath, lineageComplete := indexedCleanupCandidatePath(threadID, threadsByID, recordsByPath, make(map[string]bool))
			if candidatePath != "" {
				unknownByCWD[candidatePath] = true
			} else if !lineageComplete {
				unresolvedDescendantLineage = true
			}
		}
		nodes[threadID] = node
	}

	for threadID, node := range nodes {
		rootID, resolveErr := resolveCleanupRootID(threadID, nodes, candidateIDs, make(map[string]bool))
		if resolveErr != nil {
			node.lineageErr = firstNonNil(node.lineageErr, resolveErr)
			unknownByCWD[normalizeCleanupPath(node.thread.CWD)] = true
			if descendantEvidenceByID[threadID] {
				candidatePath, lineageComplete := indexedCleanupCandidatePath(threadID, threadsByID, recordsByPath, make(map[string]bool))
				if candidatePath != "" {
					unknownByCWD[candidatePath] = true
				} else if !lineageComplete {
					unresolvedDescendantLineage = true
				}
			}
			continue
		}
		node.rootID = rootID
		if descendantEvidenceByID[threadID] {
			candidatePath, _ := indexedCleanupCandidatePath(threadID, threadsByID, recordsByPath, make(map[string]bool))
			if candidatePath != "" {
				root := nodes[rootID]
				if root == nil || normalizeCleanupPath(root.thread.CWD) != candidatePath {
					node.lineageErr = fmt.Errorf("indexed and rollout descendant lineage disagree")
					unknownByCWD[candidatePath] = true
				}
			}
		}
	}
	membersByRoot := make(map[string][]*codexCleanupThreadNode)
	for _, node := range nodes {
		if node.lineageErr != nil || node.rootID == "" {
			continue
		}
		membersByRoot[node.rootID] = append(membersByRoot[node.rootID], node)
	}

	groupsByPath := make(map[string]*CodexCleanupWorktreeGroup)
	for _, thread := range candidateThreads {
		if err := ctx.Err(); err != nil {
			return audit, err
		}
		threadID := strings.TrimSpace(thread.ID)
		if !missingByID[threadID] {
			continue
		}
		node := nodes[threadID]
		if node == nil || node.lineageErr != nil || node.safetyErr != nil {
			audit.Excluded.Uncertain++
			continue
		}
		if !node.lineage.IsRoot || node.rootID != threadID {
			continue
		}
		if strings.TrimSpace(thread.AgentRole) != "" {
			audit.Excluded.Uncertain++
			continue
		}
		cwd := normalizeCleanupPath(thread.CWD)
		record, ok := recordsByPath[cwd]
		if !ok {
			audit.Excluded.NoLCRRecord++
			continue
		}
		if record.Pinned || thread.Pinned {
			audit.Excluded.Pinned++
			continue
		}
		if record.Archived || record.HasOpenTodo {
			audit.Excluded.Uncertain++
			continue
		}
		if pathOnExternalVolume(cwd, codexHome) || pathOnExternalVolume(record.RootPath, codexHome) {
			audit.Excluded.ExternalVolume++
			continue
		}
		if !record.MissingSince.IsZero() && record.MissingSince.After(audit.WorktreeGraceCutoff) {
			audit.Excluded.Recent++
			continue
		}
		if !cleanupRootProjectCertain(record.RootPath, cwd) {
			audit.Excluded.Uncertain++
			continue
		}
		if unresolvedDescendantLineage || unknownByCWD[cwd] || !thread.HasUserEvent {
			audit.Excluded.Uncertain++
			continue
		}

		members := membersByRoot[threadID]
		candidate, exclusion := buildCleanupThreadCandidate(now, cwd, thread, members, loaded, codexHome)
		switch exclusion {
		case "pinned":
			audit.Excluded.Pinned++
			continue
		case "loaded":
			audit.Excluded.Loaded++
			continue
		case "recent":
			audit.Excluded.Recent++
			continue
		case "external":
			audit.Excluded.ExternalVolume++
			continue
		case "uncertain":
			audit.Excluded.Uncertain++
			continue
		}

		group := groupsByPath[cwd]
		if group == nil {
			group = &CodexCleanupWorktreeGroup{
				WorktreePath:    cwd,
				WorktreeName:    firstNonEmptyTrimmed(record.Name, filepath.Base(cwd)),
				RootProjectPath: normalizeCleanupPath(record.RootPath),
				Branch:          firstNonEmptyTrimmed(record.Branch, record.InitialBranch, thread.GitBranch),
				ParentBranch:    strings.TrimSpace(record.ParentBranch),
				MissingSince:    record.MissingSince,
				Reason:          codexCleanupReason,
			}
			groupsByPath[cwd] = group
		}
		group.Threads = append(group.Threads, candidate)
		group.RootThreadCount++
		group.DescendantCount += candidate.DescendantCount
		group.RecoverableBytes += candidate.RecoverableBytes
		if group.LastActivity.IsZero() || candidate.LastActivity.After(group.LastActivity) {
			group.LastActivity = candidate.LastActivity
		}
	}

	paths := make([]string, 0, len(groupsByPath))
	for path := range groupsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		group := groupsByPath[path]
		sort.SliceStable(group.Threads, func(i, j int) bool {
			if group.Threads[i].LastActivity.Equal(group.Threads[j].LastActivity) {
				return group.Threads[i].ID < group.Threads[j].ID
			}
			return group.Threads[i].LastActivity.Before(group.Threads[j].LastActivity)
		})
		group.Revision = cleanupGroupRevision(*group)
		audit.EligibleRootThreads += group.RootThreadCount
		audit.EligibleDescendants += group.DescendantCount
		audit.RecoverableBytes += group.RecoverableBytes
		audit.Groups = append(audit.Groups, *group)
	}
	return audit, nil
}

// StartCodexCleanupAuditor performs read-only background audits. It never
// deletes threads; /clean always performs another fresh audit with an explicit
// selection and confirmation before the mutation path becomes reachable.
func (s *Service) StartCodexCleanupAuditor(ctx context.Context, loadedThreadIDs func() []string) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		options := CodexCleanupAuditOptions{}
		if loadedThreadIDs != nil {
			options.LoadedThreadIDs = append([]string(nil), loadedThreadIDs()...)
		}
		audit, err := s.AuditCodexSessionStorage(ctx, options)
		snapshot := CodexCleanupAuditSnapshot{
			Audit:       cloneCodexCleanupAudit(audit),
			CompletedAt: time.Now(),
		}
		if err != nil {
			snapshot.Error = err.Error()
		}
		s.codexCleanupAuditMu.Lock()
		s.codexCleanupAuditLatest = snapshot
		s.codexCleanupAuditMu.Unlock()

		if ctx.Err() != nil {
			return
		}
		interval := s.codexCleanupAuditEvery
		if interval <= 0 {
			interval = defaultCodexCleanupAuditInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-timer.C:
		}
	}
}

func (s *Service) LastCodexCleanupAudit() CodexCleanupAuditSnapshot {
	if s == nil {
		return CodexCleanupAuditSnapshot{}
	}
	s.codexCleanupAuditMu.RLock()
	snapshot := s.codexCleanupAuditLatest
	s.codexCleanupAuditMu.RUnlock()
	snapshot.Audit = cloneCodexCleanupAudit(snapshot.Audit)
	return snapshot
}

func cloneCodexCleanupAudit(audit CodexCleanupAudit) CodexCleanupAudit {
	cloned := audit
	cloned.Groups = make([]CodexCleanupWorktreeGroup, len(audit.Groups))
	for groupIndex, group := range audit.Groups {
		cloned.Groups[groupIndex] = group
		cloned.Groups[groupIndex].Threads = make([]CodexCleanupThread, len(group.Threads))
		for threadIndex, thread := range group.Threads {
			cloned.Groups[groupIndex].Threads[threadIndex] = thread
			cloned.Groups[groupIndex].Threads[threadIndex].MemberIDs = append([]string(nil), thread.MemberIDs...)
			cloned.Groups[groupIndex].Threads[threadIndex].RolloutFiles = append([]CodexCleanupRolloutFile(nil), thread.RolloutFiles...)
		}
	}
	return cloned
}

func buildCleanupThreadCandidate(now time.Time, cwd string, root codexstate.Thread, members []*codexCleanupThreadNode, loaded map[string]struct{}, codexHome string) (CodexCleanupThread, string) {
	if len(members) == 0 {
		return CodexCleanupThread{}, "uncertain"
	}
	candidate := CodexCleanupThread{
		ID:           strings.TrimSpace(root.ID),
		Title:        firstNonEmptyTrimmed(root.Title, "Codex thread "+shortRecoveryID(root.ID)),
		GitSHA:       strings.TrimSpace(root.GitSHA),
		GitBranch:    strings.TrimSpace(root.GitBranch),
		GitOriginURL: strings.TrimSpace(root.GitOriginURL),
		StartedAt:    root.StartedAt,
		LastActivity: root.LastActivity,
		Archived:     root.Archived,
	}
	seenMembers := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member == nil || member.lineageErr != nil || member.safetyErr != nil || member.file.Path == "" {
			return CodexCleanupThread{}, "uncertain"
		}
		memberID := strings.TrimSpace(member.thread.ID)
		if _, ok := seenMembers[memberID]; ok {
			return CodexCleanupThread{}, "uncertain"
		}
		seenMembers[memberID] = struct{}{}
		if !member.thread.PinnedKnown {
			return CodexCleanupThread{}, "uncertain"
		}
		if member.thread.Pinned {
			return CodexCleanupThread{}, "pinned"
		}
		if _, ok := loaded[memberID]; ok {
			return CodexCleanupThread{}, "loaded"
		}
		if member.thread.LastActivity.IsZero() || member.thread.LastActivity.After(now) || now.Sub(member.thread.LastActivity) < CodexCleanupRecentWindow {
			return CodexCleanupThread{}, "recent"
		}
		if member.file.ModTime.IsZero() || member.file.ModTime.After(now) || now.Sub(member.file.ModTime) < CodexCleanupRecentWindow {
			return CodexCleanupThread{}, "recent"
		}
		if candidate.LastActivity.IsZero() || member.thread.LastActivity.After(candidate.LastActivity) {
			candidate.LastActivity = member.thread.LastActivity
		}
		if member.file.ModTime.After(candidate.LastActivity) {
			candidate.LastActivity = member.file.ModTime
		}
		memberCWD := normalizeCleanupPath(member.thread.CWD)
		if memberCWD != cwd {
			return CodexCleanupThread{}, "uncertain"
		}
		missing, err := cleanupPathMissing(memberCWD)
		if err != nil || !missing {
			return CodexCleanupThread{}, "uncertain"
		}
		if pathOnExternalVolume(memberCWD, codexHome) {
			return CodexCleanupThread{}, "external"
		}
		if pathOnExternalVolume(member.file.Path, codexHome) {
			return CodexCleanupThread{}, "external"
		}
		candidate.MemberIDs = append(candidate.MemberIDs, memberID)
		candidate.RolloutFiles = append(candidate.RolloutFiles, member.file)
		candidate.RecoverableBytes += member.file.Size
	}
	sort.Strings(candidate.MemberIDs)
	sort.SliceStable(candidate.RolloutFiles, func(i, j int) bool {
		return candidate.RolloutFiles[i].ThreadID < candidate.RolloutFiles[j].ThreadID
	})
	candidate.DescendantCount = max(0, len(candidate.MemberIDs)-1)
	return candidate, ""
}

func resolveCleanupRootID(threadID string, nodes map[string]*codexCleanupThreadNode, candidateIDs map[string]bool, visiting map[string]bool) (string, error) {
	threadID = strings.TrimSpace(threadID)
	node := nodes[threadID]
	if node == nil || node.lineageErr != nil || !node.lineage.Known {
		return "", fmt.Errorf("thread lineage is incomplete")
	}
	if visiting[threadID] {
		return "", fmt.Errorf("thread lineage contains a cycle")
	}
	visiting[threadID] = true
	defer delete(visiting, threadID)

	if rootID := strings.TrimSpace(node.lineage.RootID); rootID != "" {
		if rootID == threadID {
			return rootID, nil
		}
		root := nodes[rootID]
		if root == nil {
			if candidateIDs[rootID] {
				return "", fmt.Errorf("thread root %s is unavailable", shortRecoveryID(rootID))
			}
			return rootID, nil
		}
		if root.lineageErr != nil || !root.lineage.IsRoot {
			return "", fmt.Errorf("thread root %s is unavailable", shortRecoveryID(rootID))
		}
		return rootID, nil
	}
	parentID := strings.TrimSpace(node.lineage.ParentID)
	if parentID == "" {
		return "", fmt.Errorf("thread parent is unavailable")
	}
	if nodes[parentID] == nil {
		if candidateIDs[parentID] {
			return parentID, nil
		}
		return "", fmt.Errorf("thread parent %s is unavailable", shortRecoveryID(parentID))
	}
	return resolveCleanupRootID(parentID, nodes, candidateIDs, visiting)
}

func indexedCleanupCandidatePath(threadID string, threads map[string]codexstate.Thread, records map[string]store.DeletedWorktreeRecord, visiting map[string]bool) (string, bool) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || visiting[threadID] {
		return "", false
	}
	thread, ok := threads[threadID]
	if !ok {
		return "", false
	}
	if cwd := normalizeCleanupPath(thread.CWD); cwd != "" {
		if _, recorded := records[cwd]; recorded {
			return cwd, true
		}
	}
	parentID := codexstate.ThreadSourceParentID(thread.Source)
	if parentID == "" {
		return "", strings.TrimSpace(thread.AgentRole) == ""
	}
	visiting[threadID] = true
	defer delete(visiting, threadID)
	return indexedCleanupCandidatePath(parentID, threads, records, visiting)
}

func inspectCleanupRolloutFile(codexHome, threadID, rolloutPath string) (CodexCleanupRolloutFile, error) {
	codexHome = normalizeCleanupPath(codexstate.ResolveHomeRoot(codexHome))
	rolloutPath = normalizeCleanupPath(codexstate.NormalizeRolloutPath(codexHome, rolloutPath))
	if codexHome == "" || rolloutPath == "" || !filepath.IsAbs(rolloutPath) {
		return CodexCleanupRolloutFile{}, fmt.Errorf("thread rollout path is unavailable")
	}
	allowed := false
	for _, directory := range []string{"sessions", "archived_sessions"} {
		base := filepath.Join(codexHome, directory)
		relative, err := filepath.Rel(base, rolloutPath)
		if err == nil && relative != "." && relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			allowed = true
			break
		}
	}
	if !allowed {
		return CodexCleanupRolloutFile{}, fmt.Errorf("thread rollout is outside Codex session storage")
	}
	info, err := os.Lstat(rolloutPath)
	if err != nil {
		return CodexCleanupRolloutFile{}, fmt.Errorf("inspect thread rollout: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return CodexCleanupRolloutFile{}, fmt.Errorf("thread rollout is not a regular file")
	}
	resolvedRollout, err := filepath.EvalSymlinks(rolloutPath)
	if err != nil {
		return CodexCleanupRolloutFile{}, fmt.Errorf("resolve thread rollout path: %w", err)
	}
	resolvedAllowed := false
	for _, directory := range []string{"sessions", "archived_sessions"} {
		base, baseErr := filepath.EvalSymlinks(filepath.Join(codexHome, directory))
		if baseErr != nil {
			continue
		}
		relative, relErr := filepath.Rel(base, resolvedRollout)
		if relErr == nil && relative != "." && relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			resolvedAllowed = true
			break
		}
	}
	if !resolvedAllowed {
		return CodexCleanupRolloutFile{}, fmt.Errorf("resolved thread rollout is outside Codex session storage")
	}
	return CodexCleanupRolloutFile{
		ThreadID: strings.TrimSpace(threadID),
		Path:     rolloutPath,
		Size:     info.Size(),
		ModTime:  info.ModTime(),
	}, nil
}

func cleanupRootProjectCertain(rootPath, worktreePath string) bool {
	rootPath = normalizeCleanupPath(rootPath)
	worktreePath = normalizeCleanupPath(worktreePath)
	if rootPath == "" || worktreePath == "" || rootPath == worktreePath || !filepath.IsAbs(rootPath) {
		return false
	}
	info, err := os.Stat(rootPath)
	return err == nil && info.IsDir()
}

func cleanupPathMissing(path string) (bool, error) {
	path = normalizeCleanupPath(path)
	if path == "" {
		return false, fmt.Errorf("path is unavailable")
	}
	_, err := os.Lstat(path)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func pathOnExternalVolume(path, codexHome string) bool {
	path = normalizeCleanupPath(path)
	if path == "" {
		return true
	}
	for _, root := range []string{"/Volumes", "/media", "/mnt", "/run/media"} {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	pathVolume := strings.ToLower(filepath.VolumeName(path))
	homeVolume := strings.ToLower(filepath.VolumeName(normalizeCleanupPath(codexHome)))
	if pathVolume != "" && homeVolume != "" {
		return pathVolume != homeVolume
	}
	pathDevice, pathDeviceKnown := cleanupPathDevice(path)
	homeDevice, homeDeviceKnown := cleanupPathDevice(codexHome)
	return pathDeviceKnown && homeDeviceKnown && pathDevice != homeDevice
}

func cleanupPathDevice(path string) (uint64, bool) {
	path = normalizeCleanupPath(path)
	for path != "" {
		info, err := os.Stat(path)
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return 0, false
			}
			return uint64(stat.Dev), true
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, false
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, false
		}
		path = parent
	}
	return 0, false
}

func normalizeCleanupPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func cleanupGroupRevision(group CodexCleanupWorktreeGroup) string {
	hash := sha256.New()
	writeRevisionPart := func(value string) {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	writeRevisionPart(group.WorktreePath)
	writeRevisionPart(group.RootProjectPath)
	writeRevisionPart(group.MissingSince.UTC().Format(time.RFC3339Nano))
	for _, thread := range group.Threads {
		writeRevisionPart(thread.ID)
		writeRevisionPart(thread.LastActivity.UTC().Format(time.RFC3339Nano))
		for _, file := range thread.RolloutFiles {
			writeRevisionPart(file.ThreadID)
			writeRevisionPart(file.Path)
			writeRevisionPart(fmt.Sprintf("%d", file.Size))
			writeRevisionPart(file.ModTime.UTC().Format(time.RFC3339Nano))
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *Service) DeleteCodexCleanupWorktree(ctx context.Context, request DeleteCodexCleanupWorktreeRequest) (DeleteCodexCleanupWorktreeResult, error) {
	result := DeleteCodexCleanupWorktreeResult{WorktreePath: normalizeCleanupPath(request.WorktreePath)}
	if s == nil || s.store == nil {
		return result, fmt.Errorf("service unavailable")
	}
	if result.WorktreePath == "" || strings.TrimSpace(request.Revision) == "" {
		return result, fmt.Errorf("cleanup preview identity is required")
	}
	requestedIDs := sortedUniqueStrings(request.RootThreadIDs)
	result.RequestedRootThreads = len(requestedIDs)
	if len(requestedIDs) == 0 {
		return result, fmt.Errorf("explicit Codex thread selection is required")
	}

	audit, err := s.AuditCodexSessionStorage(ctx, CodexCleanupAuditOptions{LoadedThreadIDs: request.LoadedThreadIDs})
	if err != nil {
		return result, fmt.Errorf("repeat Codex cleanup safety audit: %w", err)
	}
	var group CodexCleanupWorktreeGroup
	for _, candidate := range audit.Groups {
		if normalizeCleanupPath(candidate.WorktreePath) == result.WorktreePath {
			group = candidate
			break
		}
	}
	if group.WorktreePath == "" {
		return result, fmt.Errorf("selected worktree is no longer eligible; no sessions were deleted")
	}
	currentIDs := make([]string, 0, len(group.Threads))
	for _, thread := range group.Threads {
		currentIDs = append(currentIDs, thread.ID)
	}
	currentIDs = sortedUniqueStrings(currentIDs)
	if !equalStringSlices(requestedIDs, currentIDs) || strings.TrimSpace(request.Revision) != group.Revision {
		return result, fmt.Errorf("cleanup preview changed; review the refreshed audit before deleting")
	}

	deleter := s.codexThreadDeleter
	if deleter == nil {
		deleter = codexapp.DeleteThreads
	}
	deletedIDs, deleteErr := deleter(ctx, s.Config().CodexHome, currentIDs)
	result.DeletedThreadIDs = append([]string(nil), deletedIDs...)
	result.ExpectedBytes = group.RecoverableBytes

	remaining, listErr := codexstate.ListThreadsIncludingUnknownCWD(ctx, s.Config().CodexHome)
	remainingSet := make(map[string]struct{}, len(remaining))
	for _, thread := range remaining {
		remainingSet[strings.TrimSpace(thread.ID)] = struct{}{}
	}
	verified := listErr == nil
	for _, thread := range group.Threads {
		treeVerified := listErr == nil
		for _, memberID := range thread.MemberIDs {
			if _, stillPresent := remainingSet[memberID]; stillPresent {
				treeVerified = false
			}
		}
		for _, file := range thread.RolloutFiles {
			info, statErr := os.Lstat(file.Path)
			switch {
			case errors.Is(statErr, os.ErrNotExist):
				result.VerifiedReclaimedBytes += file.Size
			case statErr != nil:
				treeVerified = false
			case info.Mode().IsRegular():
				treeVerified = false
			default:
				treeVerified = false
			}
		}
		if treeVerified {
			result.DeletedRootThreads++
			result.DeletedDescendants += thread.DescendantCount
		} else {
			verified = false
		}
	}
	result.Verified = verified &&
		result.DeletedRootThreads == result.RequestedRootThreads &&
		result.VerifiedReclaimedBytes == result.ExpectedBytes

	var verificationErr error
	if listErr != nil {
		verificationErr = fmt.Errorf("verify Codex thread index after deletion: %w", listErr)
	} else if !result.Verified {
		verificationErr = fmt.Errorf("reclaimed storage could not be fully verified after the Codex app-server deletion request")
	}
	return result, errors.Join(deleteErr, verificationErr)
}

func sortedUniqueStrings(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
