package server

import (
	"context"

	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/projectbrief"
)

func (s *Server) visibleBriefProjects(ctx context.Context, cfg config.AppConfig) ([]model.ProjectSummary, error) {
	projects, err := s.svc.Store().ListProjects(ctx, true)
	if err != nil {
		return nil, err
	}
	projects = filterProjectSummariesByName(projects, cfg.ExcludeProjectPatterns)
	if cfg.PrivacyMode {
		projects = filterPrivateProjectSummaries(projects)
	}
	return projects, nil
}

func (s *Server) populateProjectBriefs(ctx context.Context, eligible, visible []model.ProjectSummary) {
	paths := make([]string, 0, len(visible))
	for _, project := range visible {
		paths = append(paths, project.Path)
	}
	briefs := projectbrief.Resolve(ctx, s.svc.Store(), eligible, paths)
	for i := range visible {
		visible[i].Brief = briefs[visible[i].Path]
	}
}
