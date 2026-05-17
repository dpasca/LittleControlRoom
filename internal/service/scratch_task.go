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
const scratchTaskArchiveDirName = "archive"

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
	cleanupTaskPath := false
	cleanupOnError := func(err error) (CreateScratchTaskResult, error) {
		if cleanupTaskPath {
			_ = os.RemoveAll(taskPath)
		}
		return CreateScratchTaskResult{}, err
	}
	if err := os.MkdirAll(taskPath, 0o755); err != nil {
		return CreateScratchTaskResult{}, fmt.Errorf("create task directory: %w", err)
	}
	cleanupTaskPath = true
	if err := os.WriteFile(filepath.Join(taskPath, scratchTaskMetadataFileName), []byte(renderScratchTaskMetadata(title, now)), 0o644); err != nil {
		return cleanupOnError(fmt.Errorf("write task metadata: %w", err))
	}

	if err := s.upsertManualProjectState(ctx, model.ProjectSummary{}, taskPath, title, model.ProjectKindScratchTask); err != nil {
		return cleanupOnError(err)
	}
	cleanupTaskPath = false

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

type discoveredScratchTask struct {
	Path      string
	Title     string
	CreatedAt time.Time
}

func discoverScratchTaskFolders(rootPath string) ([]discoveredScratchTask, error) {
	rootPath = normalizedScratchRootPath(rootPath)
	entries, err := os.ReadDir(rootPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read scratch root: %w", err)
	}

	tasks := []discoveredScratchTask{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == scratchTaskArchiveDirName {
			continue
		}
		taskPath := filepath.Join(rootPath, entry.Name())
		content, err := os.ReadFile(filepath.Join(taskPath, scratchTaskMetadataFileName))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read scratch task metadata: %w", err)
		}
		task, ok := parseScratchTaskMetadata(taskPath, string(content))
		if ok {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func shouldDiscoverScratchTaskFolders(cfg config.AppConfig, oldMap map[string]model.ProjectSummary) bool {
	rootPath := normalizedScratchRootPath(cfg.ScratchRoot)
	if rootPath == "" {
		return false
	}
	if rootPath != normalizedScratchRootPath(config.Default().ScratchRoot) {
		return true
	}
	prefix := rootPath + string(os.PathSeparator)
	for path, summary := range oldMap {
		if model.NormalizeProjectKind(summary.Kind) != model.ProjectKindScratchTask {
			continue
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if path == rootPath || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func normalizedScratchRootPath(rootPath string) string {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		rootPath = config.Default().ScratchRoot
	}
	if rootPath == "" {
		return ""
	}
	return filepath.Clean(rootPath)
}

func parseScratchTaskMetadata(taskPath, content string) (discoveredScratchTask, bool) {
	task := discoveredScratchTask{Path: filepath.Clean(taskPath)}
	kind := model.ProjectKind("")
	status := "active"
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(line, "# "):
			task.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case strings.HasPrefix(lower, "kind:"):
			kind = model.ProjectKind(strings.TrimSpace(line[len("Kind:"):]))
		case strings.HasPrefix(lower, "status:"):
			status = strings.ToLower(strings.TrimSpace(line[len("Status:"):]))
		case strings.HasPrefix(lower, "created:"):
			if createdAt, err := time.Parse("2006-01-02", strings.TrimSpace(line[len("Created:"):])); err == nil {
				task.CreatedAt = createdAt
			}
		}
	}
	if model.NormalizeProjectKind(kind) != model.ProjectKindScratchTask || status != "active" {
		return discoveredScratchTask{}, false
	}
	if strings.TrimSpace(task.Title) == "" {
		task.Title = filepath.Base(task.Path)
	}
	return task, true
}

func (s *Service) ArchiveScratchTask(ctx context.Context, projectPath string) (string, error) {
	project, err := s.lookupScratchTask(ctx, projectPath)
	if err != nil {
		return "", err
	}
	if !project.PresentOnDisk {
		return "", fmt.Errorf("scratch task is missing on disk: %s", projectPath)
	}

	rootPath := strings.TrimSpace(s.cfg.ScratchRoot)
	if rootPath == "" {
		rootPath = config.Default().ScratchRoot
	}
	rootPath = filepath.Clean(rootPath)
	archiveRoot := filepath.Join(rootPath, scratchTaskArchiveDirName)
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		return "", fmt.Errorf("create task archive root: %w", err)
	}

	archivedPath, err := nextScratchTaskArchivePath(archiveRoot, filepath.Base(filepath.Clean(project.Path)))
	if err != nil {
		return "", err
	}
	if err := os.Rename(project.Path, archivedPath); err != nil {
		return "", fmt.Errorf("archive scratch task: %w", err)
	}
	archivedAt := time.Now()
	if err := os.WriteFile(
		filepath.Join(archivedPath, scratchTaskMetadataFileName),
		[]byte(renderScratchTaskMetadataWithStatus(project.Name, firstNonZeroTime(project.CreatedAt, archivedAt), "archived", archivedAt)),
		0o644,
	); err != nil {
		return "", fmt.Errorf("update archived task metadata: %w", err)
	}
	unlockProjectState := s.lockProjectStateMutation(project.Path)
	defer unlockProjectState()
	if err := s.store.SetProjectPresence(ctx, project.Path, false); err != nil {
		return "", err
	}
	if err := s.store.SetForgotten(ctx, project.Path, true); err != nil {
		return "", err
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          archivedAt,
			ProjectPath: project.Path,
			Payload: map[string]string{
				"action":        "scratch_task_archived",
				"archived_path": archivedPath,
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          archivedAt,
		ProjectPath: project.Path,
		Type:        string(events.ActionApplied),
		Payload:     "scratch_task_archived",
	})
	return archivedPath, nil
}

func (s *Service) DeleteScratchTask(ctx context.Context, projectPath string) error {
	project, err := s.lookupScratchTask(ctx, projectPath)
	if err != nil {
		return err
	}
	if project.PresentOnDisk {
		if err := os.RemoveAll(project.Path); err != nil {
			return fmt.Errorf("delete scratch task: %w", err)
		}
	}
	unlockProjectState := s.lockProjectStateMutation(project.Path)
	defer unlockProjectState()
	if err := s.store.SetProjectPresence(ctx, project.Path, false); err != nil {
		return err
	}
	if err := s.store.SetForgotten(ctx, project.Path, true); err != nil {
		return err
	}
	now := time.Now()
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: project.Path,
			Payload: map[string]string{
				"action": "scratch_task_deleted",
			},
		})
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: project.Path,
		Type:        string(events.ActionApplied),
		Payload:     "scratch_task_deleted",
	})
	return nil
}

func nextScratchTaskPath(rootPath, title string, now time.Time) (string, error) {
	base := BuildScratchTaskFolderName(title, now)
	return nextScratchTaskArchivePath(rootPath, base)
}

func nextScratchTaskArchivePath(rootPath, base string) (string, error) {
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
	return renderScratchTaskMetadataWithStatus(title, createdAt, "active", time.Time{})
}

func renderScratchTaskMetadataWithStatus(title string, createdAt time.Time, status string, archivedAt time.Time) string {
	lines := []string{
		fmt.Sprintf("# %s", title),
		"",
		fmt.Sprintf("Kind: %s", model.ProjectKindScratchTask),
		fmt.Sprintf("Status: %s", strings.TrimSpace(status)),
		fmt.Sprintf("Created: %s", createdAt.Format("2006-01-02")),
	}
	if !archivedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Archived: %s", archivedAt.Format("2006-01-02")))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (s *Service) lookupScratchTask(ctx context.Context, projectPath string) (model.ProjectSummary, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return model.ProjectSummary{}, fmt.Errorf("scratch task path is required")
	}
	summaries, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return model.ProjectSummary{}, err
	}
	project := summaries[projectPath]
	if project.Path == "" {
		return model.ProjectSummary{}, fmt.Errorf("scratch task not found: %s", projectPath)
	}
	if model.NormalizeProjectKind(project.Kind) != model.ProjectKindScratchTask {
		return model.ProjectSummary{}, fmt.Errorf("project is not a scratch task: %s", projectPath)
	}
	return project, nil
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
