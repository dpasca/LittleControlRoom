package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	db *sql.DB
}

const (
	sqliteBusyTimeout  = 5 * time.Second
	sqliteMaxOpenConns = 4
)

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db}
	if err := s.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenExisting opens a database whose schema the LCR host has already
// initialized, as for the runtime helpers it launches. It skips the schema
// migrations, which need the write lock, so helper startup never waits behind
// a writer. A missing or empty database falls back to Open.
func OpenExisting(path string) (*Store, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0) {
		return Open(path)
	}
	if err != nil {
		return nil, fmt.Errorf("stat db: %w", err)
	}
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("lcroom-sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxOpenConns)
	return db, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func sqliteDSN(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}

	return fmt.Sprintf(
		"%s%s_txlock=immediate&_pragma=foreign_keys(ON)&_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		path,
		sep,
		sqliteBusyTimeout/time.Millisecond,
	)
}
