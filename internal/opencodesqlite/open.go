package opencodesqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const busyTimeout = 5 * time.Second

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func dsn(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return fmt.Sprintf(
		"%s%s_pragma=busy_timeout(%d)&_pragma=query_only(ON)",
		path,
		sep,
		busyTimeout/time.Millisecond,
	)
}
