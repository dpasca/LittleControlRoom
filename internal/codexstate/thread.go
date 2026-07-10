package codexstate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// ThreadRolloutPath returns the canonical rollout JSONL path recorded for a
// Codex thread. An empty path with a nil error means the state database or
// thread row is not available.
func ThreadRolloutPath(codexHome, threadID string) (string, error) {
	codexHome = ResolveHomeRoot(codexHome)
	threadID = strings.TrimSpace(threadID)
	if codexHome == "" || codexHome == "." || threadID == "" {
		return "", nil
	}

	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat codex state db: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("open codex state db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var rolloutPath string
	err = db.QueryRow(`SELECT rollout_path FROM threads WHERE id = ?`, threadID).Scan(&rolloutPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query codex thread rollout path: %w", err)
	}
	return NormalizeRolloutPath(codexHome, rolloutPath), nil
}
