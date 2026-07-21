package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
)

type CloneProjectRequest struct {
	Repository             string
	ParentPath             string
	PreferredSessionSource model.SessionSource
	CategoryID             string
	// CategoryExplicit assigns to CategoryID, or to Main when CategoryID is empty.
	CategoryExplicit bool
}

type CloneProjectPreview struct {
	Repository  string
	ParentPath  string
	BaseName    string
	ProjectName string
	ProjectPath string
	Collision   bool
}

type CloneProjectResult struct {
	ProjectPath       string
	ProjectName       string
	ParentPath        string
	Cloned            bool
	Registered        bool
	CollisionResolved bool
	RecentParentPaths []string
}

type normalizedCloneProjectRequest struct {
	repository string
	parentPath string
	baseName   string
}

// PreviewCloneProject resolves the destination that would be used right now.
// CloneProject repeats collision resolution while reserving the directory, so
// callers must treat this preview as informative rather than authoritative.
func PreviewCloneProject(repository, parentPath string) (CloneProjectPreview, error) {
	normalized, err := normalizeCloneProjectRequest(repository, parentPath)
	if err != nil {
		return CloneProjectPreview{}, err
	}
	name, path, collision, err := nextCloneProjectDestination(normalized.parentPath, normalized.baseName)
	if err != nil {
		return CloneProjectPreview{}, err
	}
	return CloneProjectPreview{
		Repository:  normalized.repository,
		ParentPath:  normalized.parentPath,
		BaseName:    normalized.baseName,
		ProjectName: name,
		ProjectPath: path,
		Collision:   collision,
	}, nil
}

func (s *Service) CloneProject(ctx context.Context, req CloneProjectRequest) (CloneProjectResult, error) {
	normalized, err := normalizeCloneProjectRequest(req.Repository, req.ParentPath)
	if err != nil {
		return CloneProjectResult{}, err
	}
	if s == nil || s.store == nil {
		return CloneProjectResult{}, fmt.Errorf("clone project: store unavailable")
	}
	if s.gitRepoCloner == nil {
		return CloneProjectResult{}, fmt.Errorf("clone project: Git clone is unavailable")
	}

	projectName, projectPath, collision, err := reserveCloneProjectDestination(normalized.parentPath, normalized.baseName)
	if err != nil {
		return CloneProjectResult{}, err
	}
	result := CloneProjectResult{
		ProjectPath:       projectPath,
		ProjectName:       projectName,
		ParentPath:        normalized.parentPath,
		CollisionResolved: collision,
	}

	if err := s.gitRepoCloner(ctx, normalized.repository, projectPath); err != nil {
		cleanupErr := os.RemoveAll(projectPath)
		if cleanupErr != nil {
			return result, fmt.Errorf("clone repository: %w; remove incomplete clone %s: %v", err, projectPath, cleanupErr)
		}
		return result, fmt.Errorf("clone repository: %w", err)
	}
	result.Cloned = true

	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return result, fmt.Errorf("repository cloned to %s but project registration could not load projects: %w", projectPath, err)
	}
	existing := projects[projectPath]
	if err := s.trackProjectPath(ctx, existing, projectPath, projectName, model.ProjectKindProject); err != nil {
		return result, fmt.Errorf("repository cloned to %s but project registration failed: %w", projectPath, err)
	}
	result.Registered = true
	if err := s.assignProjectCategoryIfRequested(ctx, projectPath, req.CategoryID, req.CategoryExplicit); err != nil {
		return result, fmt.Errorf("repository cloned and registered at %s but category assignment failed: %w", projectPath, err)
	}
	if err := s.persistProjectPreferredSessionSource(ctx, projectPath, req.PreferredSessionSource); err != nil {
		return result, fmt.Errorf("repository cloned and registered at %s but preferred engineer provider could not be saved: %w", projectPath, err)
	}
	if err := s.store.RememberRecentProjectParentPath(ctx, normalized.parentPath, recentProjectParentPathLimit); err != nil {
		return result, fmt.Errorf("repository cloned and registered at %s but its parent path could not be remembered: %w", projectPath, err)
	}
	recent, err := s.store.ListRecentProjectParentPaths(ctx, recentProjectParentPathLimit)
	if err != nil {
		return result, fmt.Errorf("repository cloned and registered at %s but recent parent paths could not be loaded: %w", projectPath, err)
	}
	result.RecentParentPaths = recent

	now := time.Now()
	actionPayload := map[string]string{"action": "cloned"}
	if collision {
		actionPayload["collision_resolved"] = "true"
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:        events.ActionApplied,
			At:          now,
			ProjectPath: projectPath,
			Payload:     actionPayload,
		})
	}
	payload := "cloned"
	if collision {
		payload += " collision_resolved=true"
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     payload,
	})

	return result, nil
}

func normalizeCloneProjectRequest(repository, parentPath string) (normalizedCloneProjectRequest, error) {
	repository = trimWrappingPathQuotes(strings.TrimSpace(repository))
	if repository == "" {
		return normalizedCloneProjectRequest{}, fmt.Errorf("repository is required")
	}
	parentPath = normalizeCreateOrAttachProjectPath(parentPath)
	if parentPath == "" {
		return normalizedCloneProjectRequest{}, fmt.Errorf("clone destination is required")
	}
	info, err := os.Stat(parentPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return normalizedCloneProjectRequest{}, fmt.Errorf("clone destination does not exist: %s", parentPath)
	case err != nil:
		return normalizedCloneProjectRequest{}, fmt.Errorf("check clone destination: %w", err)
	case !info.IsDir():
		return normalizedCloneProjectRequest{}, fmt.Errorf("clone destination is not a directory: %s", parentPath)
	}
	baseName, err := cloneRepositoryBaseName(repository)
	if err != nil {
		return normalizedCloneProjectRequest{}, err
	}
	return normalizedCloneProjectRequest{
		repository: repository,
		parentPath: parentPath,
		baseName:   baseName,
	}, nil
}

func cloneRepositoryBaseName(repository string) (string, error) {
	source := strings.TrimSpace(repository)
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" && parsed.Path != "" {
		source = parsed.Path
	} else if !strings.Contains(source, "://") {
		colon := strings.IndexRune(source, ':')
		slash := strings.IndexAny(source, `/\\`)
		if colon >= 0 && (slash < 0 || colon < slash) {
			source = source[colon+1:]
		}
	}
	source = strings.TrimRight(source, `/\\`)
	source = strings.ReplaceAll(source, "\\", "/")
	if slash := strings.LastIndexByte(source, '/'); slash >= 0 {
		source = source[slash+1:]
	}
	if unescaped, err := url.PathUnescape(source); err == nil {
		source = unescaped
	}
	name := strings.TrimSuffix(source, ".git")
	if !validProjectFolderName(name) {
		return "", fmt.Errorf("could not determine a project name from repository %q", repository)
	}
	return name, nil
}

func nextCloneProjectDestination(parentPath, baseName string) (string, string, bool, error) {
	for suffix := 1; ; suffix++ {
		name := cloneProjectCandidateName(baseName, suffix)
		path := filepath.Join(parentPath, name)
		_, err := os.Lstat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return name, path, suffix > 1, nil
		case err != nil:
			return "", "", false, fmt.Errorf("check clone destination %s: %w", path, err)
		}
	}
}

func reserveCloneProjectDestination(parentPath, baseName string) (string, string, bool, error) {
	for suffix := 1; ; suffix++ {
		name := cloneProjectCandidateName(baseName, suffix)
		path := filepath.Join(parentPath, name)
		err := os.Mkdir(path, 0o755)
		switch {
		case err == nil:
			return name, path, suffix > 1, nil
		case errors.Is(err, os.ErrExist):
			continue
		default:
			return "", "", false, fmt.Errorf("reserve clone destination %s: %w", path, err)
		}
	}
}

func cloneProjectCandidateName(baseName string, suffix int) string {
	if suffix <= 1 {
		return baseName
	}
	return fmt.Sprintf("%s-%d", baseName, suffix)
}

func runGitClone(ctx context.Context, repository, projectPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", projectPath, "clone", "--", repository, ".")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
