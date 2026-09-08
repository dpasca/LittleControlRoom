package service

import (
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/codexstate"
	"lcroom/internal/store"
)

// Attribute only files observed by the inventory. Unknown or conflicting index
// ownership stays explicit, and every file is counted once. This is display data,
// never deletion authority.
func buildCodexCleanupRetained(audit CodexCleanupAudit, threads []codexstate.Thread, records map[string]store.DeletedWorktreeRecord, home string) []CodexCleanupRetainedGroup {
	eligible := make(map[string]bool)
	for _, group := range audit.Groups {
		for _, thread := range group.Threads {
			for _, file := range thread.RolloutFiles {
				eligible[filepath.Clean(file.Path)] = true
			}
		}
	}
	owners := make(map[string]string)
	for _, thread := range threads {
		path := filepath.Clean(thread.RolloutPath)
		cwd := normalizeCleanupPath(thread.CWD)
		if !filepath.IsAbs(cwd) {
			cwd = ""
		}
		if previous, exists := owners[path]; exists && previous != cwd {
			owners[path] = ""
		} else if !exists {
			owners[path] = cwd
		}
	}
	groups := make(map[string]*CodexCleanupRetainedGroup)
	for path, size := range audit.Storage.files {
		if eligible[path] {
			continue
		}
		rel, _ := filepath.Rel(home, path)
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		group := CodexCleanupRetainedGroup{
			Name: top, Path: filepath.Join(home, top),
			Reason: "Other Codex files; outside session GC",
		}
		key := "other:" + top
		if top == "sessions" || top == "archived_sessions" {
			cwd := owners[path]
			key = "session:" + cwd
			group = CodexCleanupRetainedGroup{
				Name: filepath.Base(cwd), Path: cwd, ProjectPath: cwd,
				Reason: "No LCR deleted-worktree record",
			}
			if record, exists := records[cwd]; exists {
				group.ProjectPath = record.RootPath
				group.Reason = "Not eligible under worktree/tree safeguards"
			}
			if audit.Category == CodexCleanupStale {
				group.Reason = "Outside the selected inactivity policy or protected by session safeguards"
			}
			if cwd == "" {
				group.Name = "Unattributed session files"
				group.Reason = "No unique indexed working directory; ownership unknown"
			}
		}
		if groups[key] == nil {
			groups[key] = &group
		}
		groups[key].Bytes += size
		groups[key].Files++
	}
	result := make([]CodexCleanupRetainedGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bytes != result[j].Bytes {
			return result[i].Bytes > result[j].Bytes
		}
		return result[i].Path < result[j].Path
	})
	return result
}
