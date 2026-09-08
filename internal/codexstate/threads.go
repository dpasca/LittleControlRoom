package codexstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Thread is the recovery-oriented subset of Codex's global thread index.
// Rollout JSONL remains the conversation source of truth; the index adds Git
// identity that is needed to recreate a deleted worktree safely.
type Thread struct {
	ID             string
	RolloutPath    string
	CWD            string
	Title          string
	GitSHA         string
	GitBranch      string
	GitOriginURL   string
	Source         string
	AgentRole      string
	AgentRoleKnown bool
	Archived       bool
	Pinned         bool
	PinnedKnown    bool
	HasUserEvent   bool
	StartedAt      time.Time
	LastActivity   time.Time
}

// ListThreads reads Codex's global thread index without mutating it. Optional
// columns are selected defensively so older state_5.sqlite schemas still yield
// the session and cwd fields that they know about.
func ListThreads(ctx context.Context, codexHome string) ([]Thread, error) {
	return listThreads(ctx, codexHome, true)
}

// ListThreadsIncludingUnknownCWD includes indexed rows whose cwd is empty or
// malformed. Cleanup audits need those rows so an uncertain descendant cannot
// be omitted from a cascading thread/delete safety check.
func ListThreadsIncludingUnknownCWD(ctx context.Context, codexHome string) ([]Thread, error) {
	return listThreads(ctx, codexHome, false)
}

// ExistingThreadIDs checks only the selected index keys, without loading the
// complete session inventory. A missing/unreadable index fails verification.
func ExistingThreadIDs(ctx context.Context, codexHome string, ids []string) (map[string]bool, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(ResolveHomeRoot(codexHome), "state_5.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	found := make(map[string]bool)
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, "SELECT id FROM threads WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			found[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func listThreads(ctx context.Context, codexHome string, requireCWD bool) ([]Thread, error) {
	codexHome = ResolveHomeRoot(codexHome)
	if codexHome == "" || codexHome == "." {
		return nil, nil
	}
	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat codex state db: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open codex state db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	columns, err := codexThreadColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	if !columns["id"] || !columns["cwd"] {
		return nil, nil
	}

	textColumn := func(name string) string {
		if columns[name] {
			return "COALESCE(" + name + ", '')"
		}
		return "''"
	}
	intColumn := func(name string) string {
		if columns[name] {
			return "COALESCE(" + name + ", 0)"
		}
		return "0"
	}
	query := fmt.Sprintf(`
		SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
			%s, %s, %s, %s, %s, %s, %s, %s
		FROM threads
	`,
		textColumn("id"), textColumn("rollout_path"), textColumn("cwd"), textColumn("title"),
		textColumn("git_sha"), textColumn("git_branch"), textColumn("git_origin_url"), textColumn("source"),
		textColumn("agent_role"), intColumn("archived"), intColumn("has_user_event"),
		intColumn("created_at_ms"), intColumn("updated_at_ms"), intColumn("recency_at_ms"),
		intColumn("created_at"), intColumn("updated_at"), intColumn("recency_at"), intColumn("is_pinned"),
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query codex threads: %w", err)
	}
	defer rows.Close()

	threads := make([]Thread, 0)
	for rows.Next() {
		var (
			thread                                         Thread
			archived, hasUserEvent, pinned                 int
			createdMS, updatedMS, recencyMS                int64
			createdSeconds, updatedSeconds, recencySeconds int64
		)
		if err := rows.Scan(
			&thread.ID, &thread.RolloutPath, &thread.CWD, &thread.Title,
			&thread.GitSHA, &thread.GitBranch, &thread.GitOriginURL, &thread.Source,
			&thread.AgentRole, &archived, &hasUserEvent,
			&createdMS, &updatedMS, &recencyMS,
			&createdSeconds, &updatedSeconds, &recencySeconds, &pinned,
		); err != nil {
			return nil, fmt.Errorf("scan codex thread: %w", err)
		}
		thread.ID = strings.TrimSpace(thread.ID)
		thread.CWD = cleanThreadPath(thread.CWD)
		if thread.ID == "" || (requireCWD && thread.CWD == "") {
			continue
		}
		thread.RolloutPath = NormalizeRolloutPath(codexHome, thread.RolloutPath)
		thread.Title = strings.TrimSpace(thread.Title)
		thread.GitSHA = strings.TrimSpace(thread.GitSHA)
		thread.GitBranch = strings.TrimSpace(thread.GitBranch)
		thread.GitOriginURL = strings.TrimSpace(thread.GitOriginURL)
		thread.Source = strings.TrimSpace(thread.Source)
		thread.AgentRole = strings.TrimSpace(thread.AgentRole)
		thread.AgentRoleKnown = columns["agent_role"]
		thread.Archived = archived != 0
		thread.Pinned = pinned != 0
		thread.PinnedKnown = columns["is_pinned"]
		thread.HasUserEvent = hasUserEvent != 0
		thread.StartedAt = firstCodexThreadTime(createdMS, createdSeconds)
		thread.LastActivity = firstCodexThreadTime(recencyMS, recencySeconds, updatedMS, updatedSeconds, createdMS, createdSeconds)
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex threads: %w", err)
	}

	sort.SliceStable(threads, func(i, j int) bool {
		if threads[i].LastActivity.Equal(threads[j].LastActivity) {
			return threads[i].ID < threads[j].ID
		}
		return threads[i].LastActivity.After(threads[j].LastActivity)
	})
	return threads, nil
}

func codexThreadColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(threads)`)
	if err != nil {
		return nil, fmt.Errorf("inspect codex threads schema: %w", err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			kind       string
			notNull    int
			defaultV   any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultV, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan codex threads schema: %w", err)
		}
		columns[strings.TrimSpace(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate codex threads schema: %w", err)
	}
	return columns, nil
}

func cleanThreadPath(path string) string {
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

func firstCodexThreadTime(values ...int64) time.Time {
	for index := 0; index+1 < len(values); index += 2 {
		if milliseconds := values[index]; milliseconds > 0 {
			return time.UnixMilli(milliseconds)
		}
		if seconds := values[index+1]; seconds > 0 {
			return time.Unix(seconds, 0)
		}
	}
	return time.Time{}
}
