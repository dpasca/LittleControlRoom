package agentquery

import (
	"context"
	"fmt"

	"lcroom/internal/projectbrief"
)

func (e *Executor) addProjectBriefs(ctx context.Context, query Name, result map[string]any) {
	reader, ok := e.reader.(projectbrief.Reader)
	if !ok {
		return
	}
	var records []map[string]any
	switch query {
	case QueryProjectList, QueryProjectSearch:
		records, _ = result["projects"].([]map[string]any)
	case QueryPortfolioOverview:
		records, _ = result["attention_projects"].([]map[string]any)
	case QueryProjectDetail:
		if record, ok := result["project"].(map[string]any); ok {
			records = []map[string]any{record}
		}
	}
	if len(records) == 0 {
		return
	}

	eligible, err := e.visibleProjects(ctx, true)
	if err != nil {
		projectbrief.LogReadError(fmt.Errorf("list eligible checkouts: %w", err))
		return
	}
	paths := make([]string, 0, len(records))
	for _, record := range records {
		path, _ := record["path"].(string)
		paths = append(paths, path)
	}
	briefs := projectbrief.Resolve(ctx, reader, eligible, paths)
	for i, path := range paths {
		if brief := briefs[path]; brief != nil {
			records[i]["brief"] = brief
		}
	}
}
