package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

func (s *Store) GetRepositoryRootPolicy(ctx context.Context, rootPath string) (model.RepositoryRootPolicy, error) {
	rootPath = cleanRepositoryRootPath(rootPath)
	if rootPath == "" {
		return model.RepositoryRootPolicy{}, errors.New("repository root path is required")
	}
	var (
		policy    model.RepositoryRootPolicy
		mode      string
		updatedAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT root_path, expected_branch, expected_branch_source, mode,
		       acknowledged_fingerprint, updated_at
		FROM repository_root_policies
		WHERE root_path = ?
	`, rootPath).Scan(
		&policy.RootPath,
		&policy.ExpectedBranch,
		&policy.ExpectedBranchSource,
		&mode,
		&policy.AcknowledgedFingerprint,
		&updatedAt,
	)
	if err != nil {
		return model.RepositoryRootPolicy{}, err
	}
	policy.Mode = model.NormalizeRepositoryIntegrityMode(model.RepositoryIntegrityMode(mode))
	policy.UpdatedAt = time.Unix(updatedAt, 0)
	return policy, nil
}

func (s *Store) ListRepositoryRootPolicies(ctx context.Context) (map[string]model.RepositoryRootPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT root_path, expected_branch, expected_branch_source, mode,
		       acknowledged_fingerprint, updated_at
		FROM repository_root_policies
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]model.RepositoryRootPolicy{}
	for rows.Next() {
		var (
			policy    model.RepositoryRootPolicy
			mode      string
			updatedAt int64
		)
		if err := rows.Scan(
			&policy.RootPath,
			&policy.ExpectedBranch,
			&policy.ExpectedBranchSource,
			&mode,
			&policy.AcknowledgedFingerprint,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		policy.RootPath = cleanRepositoryRootPath(policy.RootPath)
		policy.Mode = model.NormalizeRepositoryIntegrityMode(model.RepositoryIntegrityMode(mode))
		policy.UpdatedAt = time.Unix(updatedAt, 0)
		out[policy.RootPath] = policy
	}
	return out, rows.Err()
}

func (s *Store) UpsertRepositoryRootPolicy(ctx context.Context, policy model.RepositoryRootPolicy) error {
	policy.RootPath = cleanRepositoryRootPath(policy.RootPath)
	if policy.RootPath == "" {
		return errors.New("repository root path is required")
	}
	policy.ExpectedBranch = strings.TrimSpace(policy.ExpectedBranch)
	policy.ExpectedBranchSource = strings.TrimSpace(policy.ExpectedBranchSource)
	policy.AcknowledgedFingerprint = strings.TrimSpace(policy.AcknowledgedFingerprint)
	policy.Mode = model.NormalizeRepositoryIntegrityMode(policy.Mode)
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repository_root_policies(
			root_path, expected_branch, expected_branch_source, mode,
			acknowledged_fingerprint, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(root_path) DO UPDATE SET
			expected_branch = excluded.expected_branch,
			expected_branch_source = excluded.expected_branch_source,
			mode = excluded.mode,
			acknowledged_fingerprint = excluded.acknowledged_fingerprint,
			updated_at = excluded.updated_at
	`,
		policy.RootPath,
		policy.ExpectedBranch,
		policy.ExpectedBranchSource,
		string(policy.Mode),
		policy.AcknowledgedFingerprint,
		policy.UpdatedAt.Unix(),
	)
	return err
}

func (s *Store) SetRepositoryRootAcknowledgedFingerprint(ctx context.Context, rootPath, fingerprint string) error {
	rootPath = cleanRepositoryRootPath(rootPath)
	if rootPath == "" {
		return errors.New("repository root path is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE repository_root_policies
		SET acknowledged_fingerprint = ?, updated_at = ?
		WHERE root_path = ?
	`, strings.TrimSpace(fingerprint), time.Now().Unix(), rootPath)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func cleanRepositoryRootPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if path == "." {
		return ""
	}
	return path
}
