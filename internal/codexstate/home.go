package codexstate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const overlayHomePrefix = "lcroom-codex-home-"

type CleanupResult struct {
	UpdatedRows int
}

func ResolveHomeRoot(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return path
	}
	for _, name := range []string{"state_5.sqlite", "sessions", "archived_sessions", "config.toml"} {
		target, ok := symlinkParentTarget(filepath.Join(path, name))
		if ok {
			return filepath.Clean(target)
		}
	}
	return path
}

func NormalizeRolloutPath(codexHome, rolloutPath string) string {
	codexHome = ResolveHomeRoot(codexHome)
	rolloutPath = filepath.Clean(strings.TrimSpace(rolloutPath))
	if rolloutPath == "" || rolloutPath == "." {
		return ""
	}
	if codexHome == "" || codexHome == "." {
		return rolloutPath
	}

	parts := strings.Split(rolloutPath, string(filepath.Separator))
	for i, part := range parts {
		if !strings.HasPrefix(strings.TrimSpace(part), overlayHomePrefix) {
			continue
		}
		if len(parts) <= i+2 {
			return rolloutPath
		}
		kind := strings.TrimSpace(parts[i+1])
		if kind != "sessions" && kind != "archived_sessions" {
			return rolloutPath
		}
		suffix := filepath.Join(parts[i+2:]...)
		if suffix == "" || suffix == "." {
			return filepath.Join(codexHome, kind)
		}
		return filepath.Join(codexHome, kind, suffix)
	}
	return rolloutPath
}

func SanitizeStateRolloutPaths(codexHome string) (CleanupResult, error) {
	codexHome = ResolveHomeRoot(codexHome)
	if codexHome == "" || codexHome == "." {
		return CleanupResult{}, nil
	}
	dbPath := filepath.Join(filepath.Clean(codexHome), "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return CleanupResult{}, nil
		}
		return CleanupResult{}, fmt.Errorf("stat codex state db: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("open codex state db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.Query(`SELECT rowid, rollout_path FROM threads`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return CleanupResult{}, nil
		}
		return CleanupResult{}, fmt.Errorf("query threads rollout paths: %w", err)
	}
	defer rows.Close()

	type rolloutUpdate struct {
		rowID   int64
		newPath string
	}
	var updates []rolloutUpdate
	for rows.Next() {
		var (
			rowID   int64
			current string
		)
		if err := rows.Scan(&rowID, &current); err != nil {
			continue
		}
		normalized := NormalizeRolloutPath(codexHome, current)
		if normalized == "" || normalized == filepath.Clean(strings.TrimSpace(current)) {
			continue
		}
		updates = append(updates, rolloutUpdate{rowID: rowID, newPath: normalized})
	}
	if err := rows.Err(); err != nil {
		return CleanupResult{}, fmt.Errorf("scan threads rollout paths: %w", err)
	}
	if len(updates) == 0 {
		return CleanupResult{}, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return CleanupResult{}, fmt.Errorf("begin rollout path cleanup: %w", err)
	}
	stmt, err := tx.Prepare(`UPDATE threads SET rollout_path = ? WHERE rowid = ?`)
	if err != nil {
		_ = tx.Rollback()
		return CleanupResult{}, fmt.Errorf("prepare rollout path cleanup: %w", err)
	}
	defer stmt.Close()

	for _, update := range updates {
		if _, err := stmt.Exec(update.newPath, update.rowID); err != nil {
			_ = tx.Rollback()
			return CleanupResult{}, fmt.Errorf("update rollout path cleanup: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CleanupResult{}, fmt.Errorf("commit rollout path cleanup: %w", err)
	}
	return CleanupResult{UpdatedRows: len(updates)}, nil
}

func symlinkParentTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Dir(filepath.Clean(target)), true
}
