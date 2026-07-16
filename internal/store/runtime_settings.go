package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) SetRuntimeSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_settings(setting_key, setting_value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(setting_key) DO UPDATE SET
			setting_value = excluded.setting_value,
			updated_at = excluded.updated_at
	`, strings.TrimSpace(key), strings.TrimSpace(value), time.Now().Unix())
	return err
}

func (s *Store) RuntimeSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `
		SELECT setting_value
		FROM runtime_settings
		WHERE setting_key = ?
	`, strings.TrimSpace(key)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(value), true, nil
}
