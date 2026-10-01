package claudeartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const SubagentActivityWindow = 20 * time.Minute

const subagentProgressTailBytes = 1024 * 1024
const subagentProgressLimit = 16

// SubagentProgress is recorded evidence, not process ownership. Reading it
// never resumes a child or changes the parent's turn lifecycle.
type SubagentProgress struct {
	ID           string
	Description  string
	AgentType    string
	ToolUseID    string
	LatestAction string
	UpdatedAt    time.Time
	Completed    bool
	Unavailable  bool
}

func (p SubagentProgress) State(now time.Time) string {
	switch {
	case p.Unavailable:
		return "activity unavailable"
	case p.Completed:
		return "completed"
	case p.UpdatedAt.IsZero():
		return "activity unknown"
	case now.Sub(p.UpdatedAt) > SubagentActivityWindow:
		return "no recent activity"
	default:
		return "active"
	}
}

type cachedSubagentProgress struct {
	size     int64
	modified time.Time
	progress SubagentProgress
}

// SubagentProgressReader is owned by one background worker. It bounds reads
// and caches unchanged children independently of the quiet parent transcript.
type SubagentProgressReader struct {
	cache map[string]cachedSubagentProgress
}

func (r *SubagentProgressReader) Read(parentFile, parentID string, since time.Time) ([]SubagentProgress, error) {
	if !validSessionIDPart(parentID) || parentFile == "" || since.IsZero() {
		return nil, nil
	}
	dir := strings.TrimSuffix(parentFile, ".jsonl") + string(filepath.Separator) + "subagents"
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read subagent activity: %w", err)
	}
	type candidate struct {
		path string
		id   string
		info os.FileInfo
	}
	var candidates []candidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !validSessionIDPart(id) || info.ModTime().Before(since) {
			continue
		}
		candidates = append(candidates, candidate{filepath.Join(dir, name), id, info})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].info.ModTime().After(candidates[j].info.ModTime()) })
	if len(candidates) > subagentProgressLimit {
		candidates = candidates[:subagentProgressLimit]
	}
	nextCache := make(map[string]cachedSubagentProgress, len(candidates))
	var result []SubagentProgress
	for _, child := range candidates {
		cached, ok := r.cache[child.path]
		if !ok || cached.progress.Unavailable || cached.size != child.info.Size() || !cached.modified.Equal(child.info.ModTime()) {
			progress, err := readSubagentProgress(child.path, parentID, child.id)
			if err != nil {
				progress = cached.progress
				progress.ID = child.id
				progress.Unavailable = true
			}
			if progress.ID != "" && progress.LatestAction == "" {
				progress.LatestAction = cached.progress.LatestAction
			}
			cached = cachedSubagentProgress{child.info.Size(), child.info.ModTime(), progress}
		}
		// Metadata may materialize after the first transcript record, so do not
		// tie its refresh to transcript mtime. This read is small and bounded.
		progress := cached.progress
		readSubagentProgressMetadata(strings.TrimSuffix(child.path, ".jsonl")+".meta.json", &progress)
		if progress.ID != "" && (progress.Unavailable || !progress.UpdatedAt.Before(since)) {
			if progress.Description == "" {
				progress.Description = "Subagent " + progress.ID
			}
			result = append(result, progress)
		}
		nextCache[child.path] = cached
	}
	r.cache = nextCache
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Completed != result[j].Completed {
			return !result[i].Completed
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func readSubagentProgressMetadata(path string, progress *SubagentProgress) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	var meta struct {
		Description string `json:"description"`
		AgentType   string `json:"agentType"`
		ToolUseID   string `json:"toolUseId"`
	}
	if json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&meta) == nil {
		progress.Description = subagentProgressText(meta.Description)
		progress.AgentType = subagentProgressText(meta.AgentType)
		progress.ToolUseID = meta.ToolUseID
	}
}

func readSubagentProgress(path, parentID, agentID string) (SubagentProgress, error) {
	file, err := os.Open(path)
	if err != nil {
		return SubagentProgress{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return SubagentProgress{}, err
	}
	start := max(int64(0), stat.Size()-subagentProgressTailBytes)
	data, err := io.ReadAll(io.NewSectionReader(file, start, subagentProgressTailBytes))
	if err != nil {
		return SubagentProgress{}, err
	}
	if start > 0 {
		_, data, _ = bytes.Cut(data, []byte{'\n'})
	}
	var progress SubagentProgress
	var tracker TurnTracker
	sawOtherIdentity := false
	for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		if len(line) == 0 || line[len(line)-1] != '\n' {
			continue // A writer may not have finished the last record yet.
		}
		var record struct {
			Type        string    `json:"type"`
			Subtype     string    `json:"subtype"`
			SessionID   string    `json:"sessionId"`
			AgentID     string    `json:"agentId"`
			IsSidechain bool      `json:"isSidechain"`
			IsMeta      bool      `json:"isMeta"`
			Timestamp   time.Time `json:"timestamp"`
			Message     struct {
				StopReason string          `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if !record.IsSidechain || record.SessionID != parentID || record.AgentID != agentID {
			sawOtherIdentity = true
			continue
		}
		if record.IsMeta || record.Timestamp.IsZero() {
			continue
		}
		// Queue writes, attachments, and filesystem timestamps do not establish
		// worker progress. Only structured conversation/tool lifecycle does.
		if record.Type != "assistant" && record.Type != "user" && record.Type != "progress" && !(record.Type == "system" && record.Subtype == "turn_duration") {
			continue
		}
		progress.ID = agentID
		progress.UpdatedAt = record.Timestamp
		tracker.Observe(TurnObservation{Type: record.Type, Subtype: record.Subtype, At: record.Timestamp,
			ConversationalUser: true, AssistantStopReason: record.Message.StopReason,
			AsyncEvents: ParseAsyncTaskEvents(line)})
		if record.Type == "assistant" {
			var blocks []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					Description string `json:"description"`
					FilePath    string `json:"file_path"`
				} `json:"input"`
			}
			if json.Unmarshal(record.Message.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type != "tool_use" {
						continue
					}
					action := block.Name
					if block.Input.Description != "" {
						action += ": " + block.Input.Description
					} else if block.Input.FilePath != "" {
						action += ": " + block.Input.FilePath
					}
					progress.LatestAction = subagentProgressText(action)
				}
			}
		}
	}
	if progress.ID == "" && !sawOtherIdentity {
		return progress, fmt.Errorf("no complete activity records in bounded subagent tail")
	}
	progress.Completed = tracker.State().Completed
	return progress, nil
}

func subagentProgressText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > 200 {
		return string(runes[:199]) + "…"
	}
	return string(runes)
}
