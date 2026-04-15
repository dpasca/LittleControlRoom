package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
)

const scratchTaskMetadataFileName = "TASK.md"

type CreateScratchTaskRequest struct {
	Title string
}

type CreateScratchTaskResult struct {
	TaskPath string
	TaskName string
}

func BuildScratchTaskFolderName(title string, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	slug := scratchTaskSlug(title)
	if slug == "" {
		slug = "task"
	}
	return at.Format("2006-01-02") + "-" + slug
}

func (s *Service) CreateScratchTask(ctx context.Context, req CreateScratchTaskRequest) (CreateScratchTaskResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return CreateScratchTaskResult{}, fmt.Errorf("task title is required")
	}
	rootPath := strings.TrimSpace(s.cfg.ScratchRoot)
	if rootPath == "" {
		rootPath = config.Default().ScratchRoot
	}
	rootPath = filepath.Clean(rootPath)
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return CreateScratchTaskResult{}, fmt.Errorf("create scratch root: %w", err)
	}

	now := time.Now()
	taskPath, err := nextScratchTaskPath(rootPath, title, now)
	if err != nil {
		return CreateScratchTaskResult{}, err
	}
	if err := os.MkdirAll(taskPath, 0o755); err != nil {
		return CreateScratchTaskResult{}, fmt.Errorf("create task directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(taskPath, scratchTaskMetadataFileName), []byte(renderScratchTaskMetadata(title, now)), 0o644); err != nil {
		return CreateScratchTaskResult{}, fmt.Errorf("write task metadata: %w", err)
	}

	if err := s.upsertManualProjectState(ctx, model.ProjectSummary{}, taskPath, title, model.ProjectKindScratchTask); err != nil {
		return CreateScratchTaskResult{}, err
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: taskPath,
			Payload: map[string]string{
				"action": "scratch_task_created",
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: taskPath,
		Type:        string(events.ActionApplied),
		Payload:     "scratch_task_created",
	})

	return CreateScratchTaskResult{
		TaskPath: taskPath,
		TaskName: title,
	}, nil
}

func nextScratchTaskPath(rootPath, title string, now time.Time) (string, error) {
	base := BuildScratchTaskFolderName(title, now)
	for attempt := 1; attempt < 1000; attempt++ {
		name := base
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		path := filepath.Join(rootPath, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect task directory: %w", err)
		}
		if !info.IsDir() {
			continue
		}
	}
	return "", fmt.Errorf("unable to allocate a unique task directory under %s", rootPath)
}

func renderScratchTaskMetadata(title string, createdAt time.Time) string {
	return fmt.Sprintf("# %s\n\nKind: %s\nStatus: active\nCreated: %s\n",
		title,
		model.ProjectKindScratchTask,
		createdAt.Format("2006-01-02"),
	)
}

func scratchTaskSlug(title string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	if title == "" {
		return ""
	}
	var b strings.Builder
	lastHyphen := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case lastHyphen:
		default:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}
