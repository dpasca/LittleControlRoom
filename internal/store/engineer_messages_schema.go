package store

import (
	"context"
	"fmt"
)

func (s *Store) ensureEngineerMessagesFollowCurrentColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(engineer_messages)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []string{"follow_current_session", "submission_uncertain"} {
		if columns[column] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE engineer_messages ADD COLUMN `+column+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add engineer_messages.%s column: %w", column, err)
		}
	}
	return nil
}

func (s *Store) ensureEngineerMessagesWakeupColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(engineer_messages)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"deliver_at", "INTEGER NOT NULL DEFAULT 0"},
		{"expires_at", "INTEGER NOT NULL DEFAULT 0"},
		{"unattended_json", "TEXT NOT NULL DEFAULT ''"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE engineer_messages ADD COLUMN `+column.name+` `+column.definition); err != nil {
			return fmt.Errorf("add engineer_messages.%s column: %w", column.name, err)
		}
	}
	return nil
}
