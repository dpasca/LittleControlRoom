package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"lcroom/internal/model"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) ListProjectCategories(ctx context.Context) ([]model.ProjectCategory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, private, position, created_at, updated_at
		FROM project_categories
		ORDER BY position ASC, name COLLATE NOCASE ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list project categories: %w", err)
	}
	defer rows.Close()

	out := []model.ProjectCategory{}
	for rows.Next() {
		category, err := scanProjectCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, category)
	}
	return out, rows.Err()
}

func (s *Store) GetProjectCategoryByName(ctx context.Context, name string) (model.ProjectCategory, error) {
	name, err := normalizeProjectCategoryName(name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	category, err := scanProjectCategory(s.db.QueryRowContext(ctx, `
		SELECT id, name, private, position, created_at, updated_at
		FROM project_categories
		WHERE name_key = ?
	`, projectCategoryNameKey(name)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ProjectCategory{}, fmt.Errorf("category not found: %s", name)
		}
		return model.ProjectCategory{}, err
	}
	return category, nil
}

func (s *Store) CreateProjectCategory(ctx context.Context, name string) (model.ProjectCategory, error) {
	name, err := normalizeProjectCategoryName(name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	if projectCategoryNameReserved(name) {
		return model.ProjectCategory{}, fmt.Errorf("%q is a built-in category name", name)
	}
	now := time.Now().Unix()
	id, err := newProjectCategoryID()
	if err != nil {
		return model.ProjectCategory{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	defer tx.Rollback()

	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM project_categories`).Scan(&position); err != nil {
		return model.ProjectCategory{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_categories(id, name, name_key, private, position, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?, ?)
	`, id, name, projectCategoryNameKey(name), position, now, now); err != nil {
		return model.ProjectCategory{}, fmt.Errorf("create project category: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.ProjectCategory{}, err
	}
	return s.GetProjectCategoryByName(ctx, name)
}

func (s *Store) DeleteProjectCategory(ctx context.Context, name string) (model.ProjectCategory, error) {
	category, err := s.GetProjectCategoryByName(ctx, name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM project_categories WHERE id = ?`, category.ID); err != nil {
		return model.ProjectCategory{}, fmt.Errorf("delete project category: %w", err)
	}
	return category, nil
}

func (s *Store) SetProjectCategoryPrivate(ctx context.Context, name string, private bool) (model.ProjectCategory, error) {
	category, err := s.GetProjectCategoryByName(ctx, name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	privateInt := 0
	if private {
		privateInt = 1
	}
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE project_categories
		SET private = ?, updated_at = ?
		WHERE id = ?
	`, privateInt, now, category.ID); err != nil {
		return model.ProjectCategory{}, fmt.Errorf("update project category privacy: %w", err)
	}
	return s.GetProjectCategoryByName(ctx, category.Name)
}

func (s *Store) SetResourceCategory(ctx context.Context, kind model.CategoryResourceKind, resourceID, categoryID string) error {
	kind = model.NormalizeCategoryResourceKind(kind)
	if kind == "" {
		return errors.New("category resource kind is required")
	}
	resourceID = cleanCategoryResourceID(kind, resourceID)
	if resourceID == "" {
		return errors.New("category resource id is required")
	}
	categoryID = strings.TrimSpace(categoryID)
	now := time.Now().Unix()
	if categoryID == "" {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM category_assignments
			WHERE resource_kind = ? AND resource_id = ?
		`, string(kind), resourceID)
		return err
	}
	if _, err := scanProjectCategory(s.db.QueryRowContext(ctx, `
		SELECT id, name, private, position, created_at, updated_at
		FROM project_categories
		WHERE id = ?
	`, categoryID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("category not found: %s", categoryID)
		}
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO category_assignments(resource_kind, resource_id, category_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(resource_kind, resource_id) DO UPDATE SET
			category_id = excluded.category_id,
			updated_at = excluded.updated_at
	`, string(kind), resourceID, categoryID, now, now)
	return err
}

func scanProjectCategory(scanner interface {
	Scan(dest ...any) error
}) (model.ProjectCategory, error) {
	var category model.ProjectCategory
	var createdAt, updatedAt int64
	var private int
	if err := scanner.Scan(&category.ID, &category.Name, &private, &category.Position, &createdAt, &updatedAt); err != nil {
		return model.ProjectCategory{}, err
	}
	category.Private = private != 0
	category.CreatedAt = time.Unix(createdAt, 0)
	category.UpdatedAt = time.Unix(updatedAt, 0)
	return category, nil
}

func normalizeProjectCategoryName(raw string) (string, error) {
	name := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if name == "" {
		return "", errors.New("category name is required")
	}
	if len([]rune(name)) > 48 {
		return "", errors.New("category name must be 48 characters or fewer")
	}
	return name, nil
}

func projectCategoryNameKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
}

func projectCategoryNameReserved(name string) bool {
	switch projectCategoryNameKey(name) {
	case "main", "general", "active", "archive", "archived", "toggle", "next", "cycle":
		return true
	default:
		return false
	}
}

func cleanCategoryResourceID(kind model.CategoryResourceKind, resourceID string) string {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return ""
	}
	if kind == model.CategoryResourceProject {
		resourceID = filepath.Clean(resourceID)
		if resourceID == "." {
			return ""
		}
	}
	return resourceID
}

func newProjectCategoryID() (string, error) {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate project category id: %w", err)
	}
	return "cat_" + hex.EncodeToString(random), nil
}
