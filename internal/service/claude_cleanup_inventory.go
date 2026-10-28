package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/claudeartifact"
)

// Claude cleanup only owns transcripts and the exact per-session subagents and
// tool-results directories. Everything else is retained, including global
// history, plans, file-history, settings, indexes and caches.
type claudeCleanupTree struct {
	CodexCleanupThread
	cwd             string
	project         string
	uncertain       bool
	pinned          bool
	cwds            map[string]bool
	retentionReason string
}

type claudeCleanupInventory struct {
	trees           []*claudeCleanupTree
	storage         CodexCleanupStorage
	unknownProjects map[string]bool
}

func claudeCleanupFile(ctx context.Context, root *os.Root, path string) (CodexCleanupRolloutFile, error) {
	return readClaudeCleanupFile(ctx, root, path, nil)
}

// Parse and fingerprint the same open file in one pass. Deletion still verifies
// the fingerprint again before removing any file.
func readClaudeCleanupFile(ctx context.Context, root *os.Root, path string, inspect func(io.Reader)) (CodexCleanupRolloutFile, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return CodexCleanupRolloutFile{}, err
	}
	if !info.Mode().IsRegular() {
		return CodexCleanupRolloutFile{}, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := root.Open(path)
	if err != nil {
		return CodexCleanupRolloutFile{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return CodexCleanupRolloutFile{}, fmt.Errorf("file changed: %s", path)
	}
	hash := sha256.New()
	reader := io.TeeReader(contextReader{Context: ctx, Reader: f}, hash)
	if inspect != nil {
		inspect(reader)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return CodexCleanupRolloutFile{}, err
	}
	after, err := f.Stat()
	if err != nil || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return CodexCleanupRolloutFile{}, fmt.Errorf("file changed: %s", path)
	}
	current, err := root.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return CodexCleanupRolloutFile{}, fmt.Errorf("file replaced: %s", path)
	}
	return CodexCleanupRolloutFile{Identity: claudeCleanupFileIdentity(info), Path: path, Size: info.Size(), ModTime: info.ModTime(), Digest: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

func readClaudeCleanupTranscript(ctx context.Context, reader io.Reader, parentID, agentID string, tree *claudeCleanupTree) error {
	sc := bufio.NewScanner(reader)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	identity := false
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry struct {
			CWD         string          `json:"cwd"`
			SessionID   string          `json:"sessionId"`
			AgentID     string          `json:"agentId"`
			IsSidechain bool            `json:"isSidechain"`
			Timestamp   json.RawMessage `json:"timestamp"`
			Pinned      bool            `json:"pinned"`
			IsPinned    bool            `json:"isPinned"`
		}
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			return err
		}
		tree.pinned = tree.pinned || entry.Pinned || entry.IsPinned
		if len(entry.Timestamp) > 0 && string(entry.Timestamp) != "null" {
			var timestamp string
			if err := json.Unmarshal(entry.Timestamp, &timestamp); err != nil {
				return fmt.Errorf("unknown timestamp format")
			}
			at, err := time.Parse(time.RFC3339Nano, timestamp)
			if err != nil {
				return err
			}
			if at.After(tree.LastActivity) {
				tree.LastActivity = at
			}
		}
		if entry.SessionID != "" && entry.SessionID != parentID {
			return fmt.Errorf("ambiguous session identity")
		}
		if agentID == "" && (entry.IsSidechain || entry.AgentID != "") {
			return fmt.Errorf("uncertain root lineage")
		}
		if entry.CWD == "" {
			continue
		}
		cwd := normalizeCleanupPath(entry.CWD)
		if !filepath.IsAbs(cwd) || entry.SessionID != parentID {
			return fmt.Errorf("uncertain working directory")
		}
		if agentID != "" && (!entry.IsSidechain || entry.AgentID != agentID) {
			return fmt.Errorf("uncertain subagent lineage")
		}
		identity = true
		tree.cwds[cwd] = true
		if agentID == "" {
			if tree.cwd != "" && tree.cwd != cwd {
				tree.retentionReason = "Session records span multiple working directories"
				return fmt.Errorf("session moved between folders")
			}
			tree.cwd = cwd
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !identity {
		return fmt.Errorf("missing transcript identity")
	}
	return nil
}

func inspectClaudeCleanup(ctx context.Context, root *os.Root) (claudeCleanupInventory, error) {
	inv := claudeCleanupInventory{storage: CodexCleanupStorage{files: map[string]int64{}}, unknownProjects: map[string]bool{}}
	files := map[string]fs.FileInfo{}
	baseInfo, err := root.Stat(".")
	if err != nil {
		return inv, err
	}
	baseDev := baseInfo.Sys().(*syscall.Stat_t).Dev
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			if len(parts) >= 2 && parts[0] == "projects" {
				inv.storage.Partial = true
				inv.unknownProjects[parts[1]] = true
			}
			if path == "projects" {
				return fmt.Errorf("Claude projects directory is not a real directory")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Sys().(*syscall.Stat_t).Dev != baseDev {
			inv.storage.Partial = true
			if len(parts) >= 2 && parts[0] == "projects" {
				inv.unknownProjects[parts[1]] = true
			}
			if path == "projects" {
				return fmt.Errorf("Claude projects directory is on another filesystem")
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		inv.storage.TotalBytes += info.Size()
		inv.storage.files[path] = info.Size()
		if len(parts) >= 2 && parts[0] == "projects" {
			inv.storage.SessionBytes += info.Size()
			files[path] = info
		}
		return nil
	})
	if err != nil {
		return inv, err
	}
	roots := []string{}
	associated := map[string][]string{}
	for path := range files {
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) == 3 && strings.HasSuffix(parts[2], ".jsonl") {
			roots = append(roots, path)
		} else if len(parts) > 3 {
			parent := filepath.Join(parts[0], parts[1], parts[2]) + ".jsonl"
			associated[parent] = append(associated[parent], path)
		}
	}
	sort.Strings(roots)
	seen := map[string]*claudeCleanupTree{}
	for _, path := range roots {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		project := filepath.Base(filepath.Dir(path))
		tree := &claudeCleanupTree{CodexCleanupThread: CodexCleanupThread{ID: id, MemberIDs: []string{id}}, project: project, cwds: map[string]bool{}}
		if previous := seen[id]; previous != nil {
			previous.uncertain = true
			tree.uncertain = true
		}
		seen[id] = tree
		if claudeartifact.SubagentSessionID(id, "identity-check") == "" {
			tree.uncertain = true
		}
		paths := append([]string{path}, associated[path]...)
		prefix := strings.TrimSuffix(path, ".jsonl") + "/"
		sort.Strings(paths)
		for _, filePath := range paths {
			threadID := id
			isTranscript := filePath == path
			agentID := ""
			if !isTranscript {
				rel := strings.TrimPrefix(filePath, prefix)
				parts := strings.Split(rel, "/")
				switch {
				case len(parts) == 2 && parts[0] == "subagents" && strings.HasPrefix(parts[1], "agent-") && strings.HasSuffix(parts[1], ".jsonl"):
					agentID = strings.TrimSuffix(strings.TrimPrefix(parts[1], "agent-"), ".jsonl")
					threadID = claudeartifact.SubagentSessionID(id, agentID)
					if threadID == "" {
						tree.uncertain = true
					}
					tree.MemberIDs = append(tree.MemberIDs, threadID)
					tree.DescendantCount++
					isTranscript = true
				case len(parts) == 2 && parts[0] == "subagents" && strings.HasPrefix(parts[1], "agent-") && strings.HasSuffix(parts[1], ".meta.json"):
					companion := strings.TrimSuffix(filePath, ".meta.json") + ".jsonl"
					if files[companion] == nil {
						tree.uncertain = true
					}
				case len(parts) == 2 && parts[0] == "tool-results":
				default:
					tree.uncertain = true
				}
			}
			var inspect func(io.Reader)
			if isTranscript {
				inspect = func(reader io.Reader) {
					if readClaudeCleanupTranscript(ctx, reader, id, agentID, tree) != nil {
						tree.uncertain = true
						if tree.retentionReason == "" {
							tree.retentionReason = "Transcript identity or format could not be verified"
						}
					}
				}
			}
			file, err := readClaudeCleanupFile(ctx, root, filePath, inspect)
			if err := ctx.Err(); err != nil {
				return inv, err
			}
			if err != nil {
				tree.uncertain = true
				continue
			}
			file.ThreadID = threadID
			if file.ModTime.After(tree.LastActivity) {
				tree.LastActivity = file.ModTime
			}
			tree.RolloutFiles = append(tree.RolloutFiles, file)
			tree.RecoverableBytes += file.Size
		}
		if tree.cwd == "" || claudeartifact.ProjectDirectoryName(tree.cwd) != project {
			tree.uncertain = true
			inv.unknownProjects[project] = true
		}
		inv.trees = append(inv.trees, tree)
	}
	// Detached descendants and unknown JSONL layouts cannot establish ownership.
	for path := range files {
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) > 3 && strings.HasSuffix(path, ".jsonl") {
			parent := seen[parts[2]]
			if parent == nil || parent.project != parts[1] {
				inv.unknownProjects[parts[1]] = true
				if parent != nil {
					parent.uncertain = true
				}
			}
		}
	}
	return inv, ctx.Err()
}

func claudeCleanupFileWithoutThreadID(file CodexCleanupRolloutFile) CodexCleanupRolloutFile {
	file.ThreadID = ""
	return file
}

// Live CLI marker files protect external sessions too. Unreadable or malformed
// markers fail the audit closed rather than interpreting missing evidence as idle.
func claudeCleanupLiveSessions(root *os.Root) (map[string]struct{}, map[string]struct{}, error) {
	ids, cwds := map[string]struct{}{}, map[string]struct{}{}
	if info, err := root.Lstat("sessions"); err == nil && !info.IsDir() {
		return nil, nil, fmt.Errorf("unsafe Claude sessions directory")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	entries, err := fs.ReadDir(root.FS(), "sessions")
	if errors.Is(err, fs.ErrNotExist) {
		return ids, cwds, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if !e.Type().IsRegular() {
			return nil, nil, fmt.Errorf("unsafe live session marker")
		}
		data, err := root.ReadFile(filepath.Join("sessions", e.Name()))
		if err != nil {
			return nil, nil, err
		}
		var marker struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
		}
		if json.Unmarshal(data, &marker) != nil || marker.PID <= 0 {
			return nil, nil, fmt.Errorf("invalid Claude live session marker")
		}
		err = syscall.Kill(marker.PID, 0)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if marker.SessionID == "" && marker.CWD == "" {
			return nil, nil, fmt.Errorf("live Claude process without session identity")
		}
		ids[marker.SessionID] = struct{}{}
		if marker.CWD != "" {
			cwds[normalizeCleanupPath(marker.CWD)] = struct{}{}
		}
	}
	return ids, cwds, nil
}

func claudeCleanupFileIdentity(info fs.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}
