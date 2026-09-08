package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"lcroom/internal/control"
)

func (s *Store) SaveEngineerModelCatalog(ctx context.Context, catalog control.EngineerModelCatalog) error {
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO engineer_model_catalogs(provider, catalog_json) VALUES (?, ?) ON CONFLICT(provider) DO UPDATE SET catalog_json = excluded.catalog_json`, catalog.Provider, string(data))
	return err
}

func (s *Store) EngineerModelCatalog(ctx context.Context, provider control.Provider) (control.EngineerModelCatalog, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT catalog_json FROM engineer_model_catalogs WHERE provider = ?`, provider).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return control.EngineerModelCatalog{Provider: provider, Source: "unavailable", Models: []control.EngineerModel{}}, nil
	}
	if err != nil {
		return control.EngineerModelCatalog{}, err
	}
	var catalog control.EngineerModelCatalog
	err = json.Unmarshal([]byte(data), &catalog)
	return catalog, err
}

func modelSelectionJSON(selection control.EngineerModelSelection) string {
	data, _ := json.Marshal(selection)
	return string(data)
}

func (s *Store) ensureEngineerModelSelectionColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(engineer_messages)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "model_selection_json"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE engineer_messages ADD COLUMN model_selection_json TEXT NOT NULL DEFAULT '{}'`)
	return err
}
