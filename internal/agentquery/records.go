package agentquery

import (
	"sort"
	"strings"

	"lcroom/internal/bossrun"
	"lcroom/internal/model"
)

func projectReference(project model.ProjectSummary) map[string]any {
	return map[string]any{
		"name": project.Name,
		"path": project.Path,
		"kind": model.NormalizeProjectKind(project.Kind),
	}
}

func projectSummaryRecord(project model.ProjectSummary) map[string]any {
	record := map[string]any{
		"name":               project.Name,
		"path":               project.Path,
		"kind":               model.NormalizeProjectKind(project.Kind),
		"category":           project.CategoryName,
		"status":             project.Status,
		"attention_score":    project.AttentionScore,
		"last_activity":      formatTime(project.LastActivity),
		"present_on_disk":    project.PresentOnDisk,
		"in_scope":           project.InScope,
		"archived":           project.Archived,
		"pinned":             project.Pinned,
		"open_todo_count":    project.OpenTODOCount,
		"total_todo_count":   project.TotalTODOCount,
		"preferred_provider": project.PreferredSessionSource,
		"latest_summary":     clip(firstNonEmpty(project.LatestSessionSummary, project.LatestCompletedSessionSummary), 600),
		"worktree": map[string]any{
			"kind":           project.WorktreeKind,
			"root_path":      project.WorktreeRootPath,
			"parent_branch":  project.WorktreeParentBranch,
			"branch":         project.RepoBranch,
			"merge_status":   project.WorktreeMergeStatus,
			"origin_todo_id": project.WorktreeOriginTodoID,
		},
		"git": map[string]any{
			"branch":                   project.RepoBranch,
			"dirty":                    project.RepoDirty,
			"conflict":                 project.RepoConflict,
			"sync_status":              project.RepoSyncStatus,
			"ahead_count":              project.RepoAheadCount,
			"behind_count":             project.RepoBehindCount,
			"submodule_dirty_count":    project.RepoSubmoduleDirtyCount,
			"submodule_unpushed_count": project.RepoSubmoduleUnpushedCount,
		},
	}
	if project.SnoozedUntil != nil {
		record["snoozed_until"] = formatTime(*project.SnoozedUntil)
	}
	if strings.TrimSpace(project.LatestSessionID) != "" {
		record["latest_session"] = map[string]any{
			"provider":               project.LatestSessionSource,
			"session_id":             project.ExternalLatestSessionID(),
			"last_event_at":          formatTime(project.LatestSessionLastEventAt),
			"latest_turn_started_at": formatTime(project.LatestTurnStartedAt),
			"turn_state_known":       project.LatestTurnStateKnown,
			"turn_completed":         project.LatestTurnCompleted,
		}
	}
	if project.LatestSessionClassification != "" {
		record["latest_assessment"] = map[string]any{
			"status":     project.LatestSessionClassification,
			"stage":      project.LatestSessionClassificationStage,
			"category":   project.LatestSessionClassificationType,
			"summary":    clip(project.LatestSessionSummary, 800),
			"updated_at": formatTime(project.LatestSessionClassificationUpdatedAt),
		}
	}
	return record
}

func todoRecords(todos []model.TodoItem, includeCompleted bool) []map[string]any {
	records := make([]map[string]any, 0, len(todos))
	for _, todo := range todos {
		if todo.Done && !includeCompleted {
			continue
		}
		record := map[string]any{
			"id":                todo.ID,
			"project_path":      todo.ProjectPath,
			"text":              clip(todo.Text, 1200),
			"done":              todo.Done,
			"position":          todo.Position,
			"attachment_count":  len(todo.Attachments),
			"work_provider":     todo.WorkProvider,
			"work_project_path": todo.WorkProjectPath,
			"work_session_id":   todo.WorkSessionID,
			"work_state":        todo.WorkState,
			"created_at":        formatTime(todo.CreatedAt),
			"updated_at":        formatTime(todo.UpdatedAt),
			"completed_at":      formatTime(todo.CompletedAt),
		}
		records = append(records, record)
	}
	return records
}

func sessionRecords(sessions []model.SessionEvidence) []map[string]any {
	sorted := append([]model.SessionEvidence(nil), sessions...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].LastEventAt.After(sorted[j].LastEventAt)
	})
	records := make([]map[string]any, 0, len(sorted))
	for _, session := range sorted {
		records = append(records, map[string]any{
			"provider":               session.Source,
			"session_id":             session.ExternalID(),
			"project_path":           session.ProjectPath,
			"format":                 session.Format,
			"started_at":             formatTime(session.StartedAt),
			"last_event_at":          formatTime(session.LastEventAt),
			"error_count":            session.ErrorCount,
			"latest_turn_started_at": formatTime(session.LatestTurnStartedAt),
			"turn_state_known":       session.LatestTurnStateKnown,
			"turn_completed":         session.LatestTurnCompleted,
		})
	}
	return records
}

func assessmentRecord(assessment model.SessionClassification) map[string]any {
	return map[string]any{
		"provider":          assessment.Source,
		"session_id":        assessment.ExternalID(),
		"project_path":      assessment.ProjectPath,
		"status":            assessment.Status,
		"stage":             assessment.Stage,
		"category":          assessment.Category,
		"summary":           clip(assessment.Summary, 1000),
		"confidence":        assessment.Confidence,
		"last_error":        clip(assessment.LastError, 600),
		"source_updated_at": formatTime(assessment.SourceUpdatedAt),
		"updated_at":        formatTime(assessment.UpdatedAt),
		"completed_at":      formatTime(assessment.CompletedAt),
	}
}

func agentTaskRecord(task model.AgentTask) map[string]any {
	resources := make([]map[string]any, 0, len(task.Resources))
	for _, resource := range task.Resources {
		resources = append(resources, map[string]any{
			"kind":         resource.Kind,
			"id":           resource.RefID,
			"project_path": resource.ProjectPath,
			"path":         resource.Path,
			"pid":          resource.PID,
			"port":         resource.Port,
			"provider":     resource.Provider,
			"session_id":   resource.SessionID,
			"label":        clip(resource.Label, 300),
		})
	}
	return map[string]any{
		"id":              task.ID,
		"parent_task_id":  task.ParentTaskID,
		"title":           clip(task.Title, 400),
		"kind":            model.NormalizeAgentTaskKind(task.Kind),
		"status":          model.NormalizeAgentTaskStatus(task.Status),
		"category":        task.CategoryName,
		"summary":         clip(task.Summary, 1200),
		"capabilities":    append([]string(nil), task.Capabilities...),
		"provider":        task.Provider,
		"session_id":      task.SessionID,
		"workspace_path":  task.WorkspacePath,
		"expires_at":      formatTime(task.ExpiresAt),
		"created_at":      formatTime(task.CreatedAt),
		"last_touched_at": formatTime(task.LastTouchedAt),
		"completed_at":    formatTime(task.CompletedAt),
		"archived_at":     formatTime(task.ArchivedAt),
		"updated_at":      formatTime(task.UpdatedAt),
		"resources":       resources,
	}
}

func goalRunRecord(record bossrun.GoalRecord, includeTrace bool, traceLimit int) map[string]any {
	run := record.Proposal.Run
	result := map[string]any{
		"id":               run.ID,
		"kind":             run.Kind,
		"title":            clip(run.Title, 400),
		"objective":        clip(run.Objective, 1200),
		"success_criteria": clip(run.SuccessCriteria, 1000),
		"status":           run.Status,
		"result_summary":   clip(record.Result.Summary, 1200),
		"verified":         record.Result.Verified,
		"error":            clip(record.Error, 800),
		"created_at":       formatTime(record.CreatedAt),
		"updated_at":       formatTime(record.UpdatedAt),
		"completed_at":     formatTime(record.CompletedAt),
		"trace_count":      len(record.Trace),
	}
	if !includeTrace {
		return result
	}
	trace := record.Trace
	if traceLimit > 0 && len(trace) > traceLimit {
		trace = trace[len(trace)-traceLimit:]
	}
	entries := make([]map[string]any, 0, len(trace))
	for _, entry := range trace {
		entries = append(entries, map[string]any{
			"step_id":     entry.StepID,
			"capability":  entry.Capability,
			"resource_id": entry.ResourceID,
			"status":      entry.Status,
			"summary":     clip(entry.Summary, 800),
			"at":          formatTime(entry.At),
		})
	}
	result["trace"] = entries
	result["trace_truncated"] = len(record.Trace) > len(entries)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
