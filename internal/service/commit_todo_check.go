package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

const (
	commitTodoCheckPatchMaxBytes         = 16000
	commitTodoCheckStaleAfter            = 3 * time.Minute
	commitTodoCompletionConfidenceCutoff = 0.75
)

func (s *Service) commitTodoCheckWorker(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.commitTodoNotifyCh:
		case <-tick.C:
		}
		for {
			err := s.processOneCommitTodoCheck(ctx)
			if err == nil {
				continue
			}
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			break
		}
	}
}

func (s *Service) processOneCommitTodoCheck(ctx context.Context) error {
	checker := s.currentCommitTodoChecker()
	if checker == nil {
		return sql.ErrNoRows
	}
	check, err := s.store.ClaimNextQueuedCommitTodoCheck(ctx, commitTodoCheckStaleAfter)
	if err != nil {
		return err
	}

	detail, err := s.store.GetProjectDetail(ctx, check.ProjectPath, 0)
	if err != nil {
		_, _ = s.store.FailCommitTodoCheck(ctx, check, err.Error())
		return nil
	}
	openTodos, todoItems := s.commitTodoRefsForDetail(ctx, detail)
	if len(openTodos) == 0 {
		check.Status = model.CommitTodoCheckCompleted
		check.UpdatedAt = time.Now()
		_, _ = s.store.CompleteCommitTodoCheck(ctx, check)
		return nil
	}

	input, err := s.buildCommitTodoCompletionInput(ctx, check, detail, openTodos)
	if err != nil {
		_, _ = s.store.FailCommitTodoCheck(ctx, check, err.Error())
		s.publishCommitTodoCheckEvent(ctx, check.ProjectPath, "commit_todo_check_failed", check.HeadHash, 0, "", err.Error())
		return nil
	}

	assistantCtx, cancel := s.withCommitAssistantTimeout(ctx)
	suggestion, err := checker.CheckCompletedTodos(assistantCtx, input)
	cancel()
	if err != nil {
		errText := formatCommitAssistantError(err, s.effectiveCommitAssistantTimeout())
		_, _ = s.store.FailCommitTodoCheck(ctx, check, errText)
		s.publishCommitTodoCheckEvent(ctx, check.ProjectPath, "commit_todo_check_failed", check.HeadHash, 0, checker.ModelName(), errText)
		return nil
	}

	completed := matchCommitTodoCompletionDecisions(openTodos, todoItems, suggestion.CompletedTodos, commitTodoCompletionConfidenceCutoff)
	var completedIDs []int64
	for _, todo := range completed {
		if err := s.ToggleTodoDone(ctx, todo.ProjectPath, todo.ID, true); err == nil {
			completedIDs = append(completedIDs, todo.ID)
		}
	}
	check.Status = model.CommitTodoCheckCompleted
	check.Model = firstNonEmptyTrimmed(suggestion.Model, checker.ModelName())
	check.CompletedTodoIDs = completedIDs
	check.UpdatedAt = time.Now()
	_, _ = s.store.CompleteCommitTodoCheck(ctx, check)

	if len(completedIDs) > 0 {
		s.publishCommitTodoCheckEvent(ctx, check.ProjectPath, "commit_todo_check_completed", check.HeadHash, len(completedIDs), check.Model, "")
	}
	return nil
}

func (s *Service) queueCommitTodoCheckForFingerprintChange(ctx context.Context, projectPath string, summary model.ProjectSummary, cached model.ProjectGitFingerprint, current scanner.GitFingerprint) {
	if s == nil || s.store == nil {
		return
	}
	if !shouldQueueCommitTodoCheckForFingerprintChange(summary, cached, current) {
		return
	}
	queued, err := s.store.QueueCommitTodoCheck(ctx, model.CommitTodoCheck{
		ProjectPath: projectPath,
		BaseHash:    strings.TrimSpace(cached.HeadHash),
		HeadHash:    strings.TrimSpace(current.HeadHash),
		UpdatedAt:   time.Now(),
	})
	if err != nil || !queued {
		return
	}
	s.NotifyCommitTodoChecker()
}

func (s *Service) refreshStoredGitFingerprint(ctx context.Context, projectPath string) {
	if s == nil || s.store == nil {
		return
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return
	}
	reader := s.runtimeSnapshot().gitFingerprintReader
	if reader == nil {
		return
	}
	fingerprint, err := reader(ctx, projectPath)
	if err != nil {
		return
	}
	_ = s.store.UpsertProjectGitFingerprint(ctx, model.ProjectGitFingerprint{
		ProjectPath:  projectPath,
		HeadHash:     fingerprint.HeadHash,
		RecentHashes: append([]string(nil), fingerprint.RecentHashes...),
		UpdatedAt:    time.Now(),
	})
}

func shouldQueueCommitTodoCheckForFingerprintChange(summary model.ProjectSummary, cached model.ProjectGitFingerprint, current scanner.GitFingerprint) bool {
	if strings.TrimSpace(cached.HeadHash) == "" || strings.TrimSpace(current.HeadHash) == "" {
		return false
	}
	if strings.TrimSpace(cached.HeadHash) == strings.TrimSpace(current.HeadHash) {
		return false
	}
	return summary.OpenTODOCount > 0 || summary.WorktreeOriginTodoID > 0
}

func (s *Service) buildCommitTodoCompletionInput(ctx context.Context, check model.CommitTodoCheck, detail model.ProjectDetail, openTodos []gitops.CommitTodoRef) (gitops.CommitTodoCompletionInput, error) {
	projectPath := filepath.Clean(strings.TrimSpace(check.ProjectPath))
	if projectPath == "" || projectPath == "." {
		return gitops.CommitTodoCompletionInput{}, fmt.Errorf("project path is required")
	}
	headHash := strings.TrimSpace(check.HeadHash)
	if headHash == "" {
		return gitops.CommitTodoCompletionInput{}, fmt.Errorf("head hash is required")
	}
	baseHash := strings.TrimSpace(check.BaseHash)

	subject, _ := gitops.ReadCommitSubject(ctx, projectPath, headHash)
	changedFiles, filesErr := gitops.ReadCommitRangeChangedFiles(ctx, projectPath, baseHash, headHash)
	diffStat, statErr := gitops.ReadCommitRangeDiffStat(ctx, projectPath, baseHash, headHash)
	patch, patchErr := gitops.ReadCommitRangePatch(ctx, projectPath, baseHash, headHash, commitTodoCheckPatchMaxBytes)
	if (filesErr != nil || statErr != nil || patchErr != nil) && baseHash != "" {
		changedFiles, filesErr = gitops.ReadCommitRangeChangedFiles(ctx, projectPath, "", headHash)
		diffStat, statErr = gitops.ReadCommitRangeDiffStat(ctx, projectPath, "", headHash)
		patch, patchErr = gitops.ReadCommitRangePatch(ctx, projectPath, "", headHash, commitTodoCheckPatchMaxBytes)
		baseHash = ""
	}
	if filesErr != nil {
		return gitops.CommitTodoCompletionInput{}, filesErr
	}
	if statErr != nil {
		return gitops.CommitTodoCompletionInput{}, statErr
	}
	if patchErr != nil {
		return gitops.CommitTodoCompletionInput{}, patchErr
	}

	return gitops.CommitTodoCompletionInput{
		ProjectName:   commitPreviewProjectName(projectPath, detail.Summary.Name),
		Branch:        commitPreviewBranchName(detail.Summary.RepoBranch),
		BaseHash:      baseHash,
		HeadHash:      headHash,
		CommitSubject: subject,
		ChangedFiles:  changedFiles,
		DiffStat:      diffStat,
		Patch:         patch,
		OpenTodos:     openTodos,
	}, nil
}

func (s *Service) commitTodoRefsForDetail(ctx context.Context, detail model.ProjectDetail) ([]gitops.CommitTodoRef, []model.TodoItem) {
	todos := append([]model.TodoItem(nil), detail.Todos...)
	seen := make(map[int64]struct{}, len(todos)+1)
	for _, todo := range todos {
		seen[todo.ID] = struct{}{}
	}
	if detail.Summary.WorktreeOriginTodoID > 0 {
		if todo, err := s.store.GetTodo(ctx, detail.Summary.WorktreeOriginTodoID); err == nil {
			if _, ok := seen[todo.ID]; !ok {
				todos = append(todos, todo)
			}
		}
	}
	return openTodoRefs(todos), todos
}

func matchCommitTodoCompletionDecisions(refs []gitops.CommitTodoRef, allTodos []model.TodoItem, decisions []gitops.CommitTodoCompletionDecision, confidenceCutoff float64) []TodoCompletion {
	if len(decisions) == 0 {
		return nil
	}
	openSet := make(map[int64]struct{}, len(refs))
	for _, ref := range refs {
		openSet[ref.ID] = struct{}{}
	}
	todoByID := make(map[int64]model.TodoItem, len(allTodos))
	for _, todo := range allTodos {
		todoByID[todo.ID] = todo
	}
	seen := map[int64]struct{}{}
	var out []TodoCompletion
	for _, decision := range decisions {
		if _, ok := openSet[decision.ID]; !ok {
			continue
		}
		if _, ok := seen[decision.ID]; ok {
			continue
		}
		if decision.Confidence < confidenceCutoff {
			continue
		}
		todo, ok := todoByID[decision.ID]
		if !ok {
			continue
		}
		seen[decision.ID] = struct{}{}
		out = append(out, TodoCompletion{
			ID:          decision.ID,
			Text:        todo.Text,
			ProjectPath: todo.ProjectPath,
			Reason:      strings.TrimSpace(decision.Reason),
			Confidence:  decision.Confidence,
		})
	}
	return out
}

func (s *Service) publishCommitTodoCheckEvent(ctx context.Context, projectPath, action, headHash string, completedCount int, modelName, lastError string) {
	now := time.Now()
	payload := map[string]string{
		"action": action,
	}
	if short := shortCommitHash(headHash); short != "" {
		payload["commit"] = short
	}
	if completedCount > 0 {
		payload["completed_todos"] = fmt.Sprintf("%d", completedCount)
	}
	if strings.TrimSpace(modelName) != "" {
		payload["model"] = strings.TrimSpace(modelName)
	}
	if strings.TrimSpace(lastError) != "" {
		payload["error"] = strings.TrimSpace(lastError)
	}
	if s.bus != nil {
		s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: payload})
	}

	message := action
	if short := shortCommitHash(headHash); short != "" {
		message += " commit=" + short
	}
	if completedCount > 0 {
		message += fmt.Sprintf(" completed_todos=%d", completedCount)
	}
	if strings.TrimSpace(lastError) != "" {
		message += " error=" + strings.TrimSpace(lastError)
	}
	_ = s.store.AddEvent(ctx, model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     message,
	})
}

func shortCommitHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
