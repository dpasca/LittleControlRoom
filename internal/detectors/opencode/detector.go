package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/opencodesqlite"
	"lcroom/internal/scanner"
)

type Detector struct {
	opencodeHome string
}

func New(opencodeHome string) *Detector {
	return &Detector{opencodeHome: opencodeHome}
}

func (d *Detector) Name() string {
	return "opencode"
}

func (d *Detector) Detect(ctx context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	dbPath := filepath.Join(d.opencodeHome, "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]*model.DetectorProjectActivity{}, nil
		}
		return nil, err
	}

	db, err := opencodesqlite.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT
			s.id,
			s.directory,
			p.worktree,
			s.time_created,
			s.time_updated,
			s.time_archived,
			COALESCE((SELECT MAX(m.time_updated) FROM message m WHERE m.session_id = s.id), 0),
			COALESCE((SELECT MAX(pt.time_updated) FROM part pt WHERE pt.session_id = s.id), 0)
		FROM session s
		LEFT JOIN project p ON p.id = s.project_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := map[string]*model.DetectorProjectActivity{}
	for rows.Next() {
		var (
			sessionID    string
			directory    string
			worktree     sql.NullString
			timeCreated  int64
			timeUpdated  int64
			timeArchived sql.NullInt64
			maxMsgAt     int64
			maxPartAt    int64
		)
		if err := rows.Scan(&sessionID, &directory, &worktree, &timeCreated, &timeUpdated, &timeArchived, &maxMsgAt, &maxPartAt); err != nil {
			continue
		}
		if timeArchived.Valid && timeArchived.Int64 > 0 {
			continue
		}

		projectPath := directory
		if projectPath == "" && worktree.Valid {
			projectPath = worktree.String
		}
		if projectPath == "" {
			continue
		}
		projectPath = filepath.Clean(projectPath)
		if !scope.Allows(projectPath) {
			continue
		}

		entry, ok := results[projectPath]
		if !ok {
			entry = &model.DetectorProjectActivity{
				ProjectPath: projectPath,
				Source:      d.Name(),
			}
			results[projectPath] = entry
		}

		startedAt := unixToTimeFlexible(timeCreated)
		activityUpdatedAt := timeUpdated
		if maxMsgAt > 0 || maxPartAt > 0 {
			activityUpdatedAt = maxMsgAt
			if maxPartAt > activityUpdatedAt {
				activityUpdatedAt = maxPartAt
			}
		}
		updatedAt := unixToTimeFlexible(activityUpdatedAt)

		session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
			SessionID:           sessionID,
			ProjectPath:         projectPath,
			DetectedProjectPath: projectPath,
			SessionFile:         fmt.Sprintf("%s#session:%s", dbPath, sessionID),
			Format:              "opencode_db",
			StartedAt:           startedAt,
			LastEventAt:         updatedAt,
			ErrorCount:          0,
		})
		entry.Sessions = append(entry.Sessions, session)
		entry.Artifacts = append(entry.Artifacts, model.ArtifactEvidence{
			Path:      dbPath,
			Kind:      "opencode_sqlite",
			UpdatedAt: updatedAt,
			Note:      "OpenCode session metadata from opencode.db",
		})
		if updatedAt.After(entry.LastActivity) {
			entry.LastActivity = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, entry := range results {
		sort.Slice(entry.Sessions, func(i, j int) bool {
			return entry.Sessions[i].LastEventAt.After(entry.Sessions[j].LastEventAt)
		})
		dedupeArtifacts(entry)
	}

	return results, nil
}

func dedupeArtifacts(a *model.DetectorProjectActivity) {
	seen := map[string]struct{}{}
	out := make([]model.ArtifactEvidence, 0, len(a.Artifacts))
	for _, artifact := range a.Artifacts {
		key := artifact.Kind + "|" + artifact.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, artifact)
	}
	a.Artifacts = out
}

func unixToTimeFlexible(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	if v > 9_999_999_999 {
		sec := v / 1000
		ms := v % 1000
		return time.Unix(sec, ms*int64(time.Millisecond))
	}
	return time.Unix(v, 0)
}
