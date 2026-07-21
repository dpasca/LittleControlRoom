package service

import (
	"context"
	"database/sql"
	"encoding/json"
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
	commitTodoFocusedPatchMaxBytes       = 32000
	commitTodoFocusedFileLimit           = 16
	commitTodoCheckStaleAfter            = 3 * time.Minute
	commitTodoCompletionConfidenceCutoff = 0.75
)

var commitTodoCheckRetrySchedule = []time.Duration{
	time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

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
		s.failCommitTodoCheck(ctx, check, err.Error(), "")
		return nil
	}
	openTodos, todoItems := s.commitTodoRefsForDetail(ctx, detail, check.CreatedAt)
	if len(openTodos) == 0 {
		check.Status = model.CommitTodoCheckCompleted
		check.UpdatedAt = time.Now()
		if updated, err := s.store.CompleteCommitTodoCheck(ctx, check); err != nil || !updated {
			if err == nil {
				err = sql.ErrNoRows
			}
			s.failCommitTodoCheck(ctx, check, err.Error(), "")
		}
		return nil
	}

	input, err := s.buildCommitTodoCompletionInput(ctx, check, detail, openTodos)
	if err != nil {
		s.failCommitTodoCheck(ctx, check, err.Error(), "")
		return nil
	}
	check.EvidenceJSON = commitTodoEvidenceSummaryJSON(input)

	assistantCtx, cancel := s.withCommitAssistantTimeout(ctx)
	suggestion, err := checker.CheckCompletedTodos(assistantCtx, input)
	cancel()
	if err != nil {
		errText := formatCommitAssistantError(err, s.effectiveCommitAssistantTimeout())
		s.failCommitTodoCheck(ctx, check, errText, checker.ModelName())
		return nil
	}
	check.Model = firstNonEmptyTrimmed(suggestion.Model, checker.ModelName())
	if decisionJSON, err := json.Marshal(suggestion.CompletedTodos); err == nil {
		check.DecisionJSON = string(decisionJSON)
	}

	completed := matchCommitTodoCompletionDecisions(openTodos, todoItems, suggestion.CompletedTodos, commitTodoCompletionConfidenceCutoff)
	completedIDs := append([]int64(nil), check.CompletedTodoIDs...)
	completedIDSet := make(map[int64]struct{}, len(completedIDs)+len(completed))
	for _, todoID := range completedIDs {
		completedIDSet[todoID] = struct{}{}
	}
	for _, todo := range completed {
		if _, alreadyCompleted := completedIDSet[todo.ID]; alreadyCompleted {
			continue
		}
		if err := s.ToggleTodoDone(ctx, todo.ProjectPath, todo.ID, true); err != nil {
			s.failCommitTodoCheck(ctx, check, fmt.Sprintf("mark TODO %d complete: %v", todo.ID, err), check.Model)
			return nil
		}
		completedIDs = append(completedIDs, todo.ID)
		completedIDSet[todo.ID] = struct{}{}
		check.CompletedTodoIDs = append([]int64(nil), completedIDs...)
	}
	check.Status = model.CommitTodoCheckCompleted
	check.CompletedTodoIDs = completedIDs
	check.UpdatedAt = time.Now()
	updated, completeErr := s.store.CompleteCommitTodoCheck(ctx, check)
	if completeErr != nil || !updated {
		if completeErr == nil {
			completeErr = sql.ErrNoRows
		}
		s.failCommitTodoCheck(ctx, check, fmt.Sprintf("record completed commit TODO check: %v", completeErr), check.Model)
		return nil
	}

	if len(completedIDs) > 0 {
		s.publishCommitTodoCheckEvent(ctx, check.ProjectPath, "commit_todo_check_completed", check.HeadHash, len(completedIDs), check.Model, "")
	}
	return nil
}

func commitTodoCheckRetryDelay(attemptCount int) time.Duration {
	if len(commitTodoCheckRetrySchedule) == 0 {
		return 0
	}
	if attemptCount <= 0 {
		attemptCount = 1
	}
	index := attemptCount - 1
	if index >= len(commitTodoCheckRetrySchedule) {
		index = len(commitTodoCheckRetrySchedule) - 1
	}
	return commitTodoCheckRetrySchedule[index]
}

func (s *Service) failCommitTodoCheck(ctx context.Context, check model.CommitTodoCheck, errText, modelName string) {
	errText = strings.TrimSpace(errText)
	updated, persistErr := s.store.FailCommitTodoCheck(ctx, check, errText, commitTodoCheckRetryDelay(check.AttemptCount))
	if persistErr != nil {
		errText = fmt.Sprintf("%s; record failure state: %v", errText, persistErr)
	} else if !updated {
		errText = fmt.Sprintf("%s; record failure state: %v", errText, sql.ErrNoRows)
	}
	s.publishCommitTodoCheckEvent(
		ctx,
		check.ProjectPath,
		"commit_todo_check_failed",
		check.HeadHash,
		0,
		firstNonEmptyTrimmed(modelName, check.Model),
		errText,
	)
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
	patch, patchTruncated, patchErr := gitops.ReadCommitRangePatchWithStatus(ctx, projectPath, baseHash, headHash, commitTodoCheckPatchMaxBytes)
	if (filesErr != nil || statErr != nil || patchErr != nil) && baseHash != "" {
		changedFiles, filesErr = gitops.ReadCommitRangeChangedFiles(ctx, projectPath, "", headHash)
		diffStat, statErr = gitops.ReadCommitRangeDiffStat(ctx, projectPath, "", headHash)
		patch, patchTruncated, patchErr = gitops.ReadCommitRangePatchWithStatus(ctx, projectPath, "", headHash, commitTodoCheckPatchMaxBytes)
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

	input := gitops.CommitTodoCompletionInput{
		ProjectName:      commitPreviewProjectName(projectPath, detail.Summary.Name),
		Branch:           commitPreviewBranchName(detail.Summary.RepoBranch),
		BaseHash:         baseHash,
		HeadHash:         headHash,
		CommitSubject:    subject,
		ChangedFiles:     changedFiles,
		DiffStat:         diffStat,
		Patch:            patch,
		OpenTodos:        openTodos,
		BypassModelCache: check.AttemptCount > 1,
	}
	if !patchTruncated {
		input.EvidenceStrategy = "complete_patch"
		return input, nil
	}
	input.EvidenceStrategy = "truncated_prefix"
	selector := s.currentCommitTodoEvidenceSelector()
	if selector == nil {
		return input, nil
	}

	commits, err := gitops.ReadCommitRangeEvidenceCommits(ctx, projectPath, baseHash, headHash)
	if err != nil {
		return gitops.CommitTodoCompletionInput{}, err
	}
	if len(commits) == 0 {
		return input, nil
	}
	selectionCtx, cancel := s.withCommitAssistantTimeout(ctx)
	selection, err := selector.SelectCommitTodoEvidence(selectionCtx, gitops.CommitTodoEvidenceSelectionInput{
		ProjectName:      input.ProjectName,
		Branch:           input.Branch,
		BaseHash:         input.BaseHash,
		HeadHash:         input.HeadHash,
		OpenTodos:        openTodos,
		Commits:          commits,
		BypassModelCache: input.BypassModelCache,
	})
	cancel()
	if err != nil {
		return gitops.CommitTodoCompletionInput{}, fmt.Errorf("select focused commit TODO evidence: %w", err)
	}
	focusedPatch, focusedCommits, normalizedSelection, err := buildFocusedCommitTodoEvidence(
		ctx,
		projectPath,
		commits,
		openTodos,
		selection.Items,
		commitTodoFocusedPatchMaxBytes,
		commitTodoFocusedFileLimit,
	)
	if err != nil {
		return gitops.CommitTodoCompletionInput{}, err
	}
	if strings.TrimSpace(focusedPatch) == "" {
		return input, nil
	}
	input.Patch = focusedPatch
	input.EvidenceStrategy = "focused_model_selection"
	input.EvidenceModel = firstNonEmptyTrimmed(selection.Model, selector.ModelName())
	input.EvidenceCommits = focusedCommits
	input.EvidenceSelection = normalizedSelection
	input.ChangedFiles = focusedChangedFiles(focusedCommits)
	return input, nil
}

func buildFocusedCommitTodoEvidence(
	ctx context.Context,
	projectPath string,
	commits []gitops.CommitTodoEvidenceCommit,
	openTodos []gitops.CommitTodoRef,
	selection []gitops.CommitTodoEvidenceSelectionItem,
	maxBytes int,
	maxFiles int,
) (string, []gitops.CommitTodoEvidenceCommit, []gitops.CommitTodoEvidenceSelectionItem, error) {
	commitByHash := make(map[string]gitops.CommitTodoEvidenceCommit, len(commits))
	for _, commit := range commits {
		commitByHash[strings.TrimSpace(commit.Hash)] = commit
	}
	openTodoIDs := make(map[int64]struct{}, len(openTodos))
	for _, todo := range openTodos {
		openTodoIDs[todo.ID] = struct{}{}
	}

	type focusedCommit struct {
		commit gitops.CommitTodoEvidenceCommit
		files  []string
	}
	var groups []*focusedCommit
	groupByHash := map[string]*focusedCommit{}
	var normalized []gitops.CommitTodoEvidenceSelectionItem
	totalFiles := 0
	for _, item := range selection {
		commit, ok := commitByHash[strings.TrimSpace(item.CommitHash)]
		if !ok {
			continue
		}
		validTodoIDs := make([]int64, 0, len(item.TodoIDs))
		for _, todoID := range item.TodoIDs {
			if _, ok := openTodoIDs[todoID]; ok {
				validTodoIDs = append(validTodoIDs, todoID)
			}
		}
		if len(validTodoIDs) == 0 {
			continue
		}
		allowedFiles := make(map[string]struct{}, len(commit.ChangedFiles))
		for _, file := range commit.ChangedFiles {
			allowedFiles[strings.TrimSpace(file)] = struct{}{}
		}
		group := groupByHash[commit.Hash]
		if group == nil {
			group = &focusedCommit{commit: commit}
			groupByHash[commit.Hash] = group
			groups = append(groups, group)
		}
		seenInGroup := make(map[string]struct{}, len(group.files))
		for _, file := range group.files {
			seenInGroup[file] = struct{}{}
		}
		includedFiles := make([]string, 0, len(item.Files))
		for _, file := range item.Files {
			file = strings.TrimSpace(file)
			if _, ok := allowedFiles[file]; !ok {
				continue
			}
			if _, ok := seenInGroup[file]; ok {
				includedFiles = append(includedFiles, file)
				continue
			}
			if totalFiles >= maxFiles {
				continue
			}
			seenInGroup[file] = struct{}{}
			group.files = append(group.files, file)
			totalFiles++
			includedFiles = append(includedFiles, file)
		}
		if len(includedFiles) > 0 {
			normalized = append(normalized, gitops.CommitTodoEvidenceSelectionItem{
				TodoIDs:    validTodoIDs,
				CommitHash: commit.Hash,
				Files:      includedFiles,
				Reason:     strings.TrimSpace(item.Reason),
			})
		}
	}
	if totalFiles == 0 {
		return "", nil, normalized, nil
	}
	if maxBytes <= 0 {
		maxBytes = commitTodoFocusedPatchMaxBytes
	}
	var patchBuilder strings.Builder
	var focusedCommits []gitops.CommitTodoEvidenceCommit
	remainingBytes := maxBytes
	remainingFiles := totalFiles
	for _, group := range groups {
		if len(group.files) == 0 {
			continue
		}
		focusedCommit := group.commit
		focusedCommit.ChangedFiles = append([]string(nil), group.files...)
		focusedCommits = append(focusedCommits, focusedCommit)
		for _, file := range group.files {
			header := fmt.Sprintf(
				"### commit %s: %s\n### file %s\n",
				group.commit.Hash,
				group.commit.Subject,
				file,
			)
			share := remainingBytes
			if remainingFiles > 0 {
				share /= remainingFiles
			}
			remainingFiles--
			patchBudget := share - len(header) - 1
			if patchBudget <= 0 {
				continue
			}
			patch, err := gitops.ReadCommitPatchForFiles(ctx, projectPath, group.commit, []string{file}, patchBudget)
			if err != nil {
				return "", nil, nil, err
			}
			if strings.TrimSpace(patch) == "" {
				continue
			}
			chunk := header + patch + "\n"
			patchBuilder.WriteString(chunk)
			remainingBytes -= len(chunk)
		}
	}
	return strings.TrimSpace(patchBuilder.String()), focusedCommits, normalized, nil
}

func focusedChangedFiles(commits []gitops.CommitTodoEvidenceCommit) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, commit := range commits {
		for _, file := range commit.ChangedFiles {
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			out = append(out, file)
		}
	}
	return out
}

func commitTodoEvidenceSummaryJSON(input gitops.CommitTodoCompletionInput) string {
	summary := struct {
		Strategy  string                                   `json:"strategy"`
		Model     string                                   `json:"model,omitempty"`
		Commits   []gitops.CommitTodoEvidenceCommit        `json:"commits,omitempty"`
		Selection []gitops.CommitTodoEvidenceSelectionItem `json:"selection,omitempty"`
	}{
		Strategy:  strings.TrimSpace(input.EvidenceStrategy),
		Model:     strings.TrimSpace(input.EvidenceModel),
		Commits:   input.EvidenceCommits,
		Selection: input.EvidenceSelection,
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(payload)
}

func (s *Service) commitTodoRefsForDetail(ctx context.Context, detail model.ProjectDetail, queuedAt time.Time) ([]gitops.CommitTodoRef, []model.TodoItem) {
	todos := make([]model.TodoItem, 0, len(detail.Todos)+1)
	for _, todo := range detail.Todos {
		if !queuedAt.IsZero() && !todo.CreatedAt.IsZero() && todo.CreatedAt.After(queuedAt) {
			continue
		}
		todos = append(todos, todo)
	}
	seen := make(map[int64]struct{}, len(todos)+1)
	for _, todo := range todos {
		seen[todo.ID] = struct{}{}
	}
	if detail.Summary.WorktreeOriginTodoID > 0 {
		if todo, err := s.store.GetTodo(ctx, detail.Summary.WorktreeOriginTodoID); err == nil {
			if !queuedAt.IsZero() && !todo.CreatedAt.IsZero() && todo.CreatedAt.After(queuedAt) {
				return openTodoRefs(todos), todos
			}
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
