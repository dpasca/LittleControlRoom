package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
)

type GitActionIntent string

const (
	GitActionCommit GitActionIntent = "commit"
	GitActionFinish GitActionIntent = "finish"

	defaultCommitAssistantTimeout = 20 * time.Second
)

type GitStageMode string

const (
	GitStageStagedOnly GitStageMode = "staged_only"
	GitStageAllChanges GitStageMode = "all_changes"
)

type CommitFile struct {
	Path    string
	Code    string
	Summary string
}

type TodoCompletion struct {
	ID   int64
	Text string
}

type CommitPreview struct {
	Intent             GitActionIntent
	ProjectPath        string
	ProjectName        string
	Branch             string
	StageMode          GitStageMode
	Included           []CommitFile
	Excluded           []CommitFile
	SelectedUntracked  []CommitFile
	Message            string
	DiffStat           string
	DiffSummary        string
	LatestSummary      string
	CanPush            bool
	Warnings           []string
	CommitMessageError string
	StateHash          string
	SuggestedTodos     []TodoCompletion
}

type CommitResult struct {
	ProjectPath string
	Branch      string
	CommitHash  string
	Pushed      bool
	Warning     string
}

type PushResult struct {
	ProjectPath string
	Branch      string
	Pushed      bool
	Summary     string
}

type NoChangesToCommitError struct {
	ProjectPath string
	ProjectName string
	Branch      string
	Ahead       int
	Behind      int
	CanPush     bool
	PushWarning string
}

type SubmoduleAttentionError struct {
	ProjectPath string
	ProjectName string
	Branch      string
	Submodules  []string
	PushWarning string
}

func (e NoChangesToCommitError) Error() string {
	base := "no changes to commit"
	switch {
	case e.Ahead > 0 && e.CanPush:
		return fmt.Sprintf("%s; branch is ahead of upstream by %d commit(s), use /push to send existing commits", base, e.Ahead)
	case e.Behind > 0:
		return fmt.Sprintf("%s; branch is behind upstream by %d commit(s)", base, e.Behind)
	case strings.TrimSpace(e.PushWarning) != "":
		return base + "; " + strings.TrimSpace(e.PushWarning)
	default:
		return base
	}
}

func (e SubmoduleAttentionError) Error() string {
	base := "submodule changes need attention before the parent repo can commit"
	if len(e.Submodules) == 0 {
		return base
	}
	return fmt.Sprintf("%s: %s", base, strings.Join(e.Submodules, ", "))
}

func (s *Service) PrepareCommit(ctx context.Context, projectPath string, intent GitActionIntent, messageOverride string) (CommitPreview, error) {
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		return CommitPreview{}, err
	}
	if !projectPathExists(projectPath) {
		return CommitPreview{}, fmt.Errorf("project not found on disk: %s", projectPath)
	}

	repoStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return CommitPreview{}, err
	}
	projectName := commitPreviewProjectName(projectPath, detail.Summary.Name)
	branchName := commitPreviewBranchName(repoStatus.Branch)
	canPush, pushWarning := pushAvailability(repoStatus)

	staged := repoStatus.StagedChanges()
	includedChanges := staged
	excludedChanges := append([]scanner.GitChange{}, repoStatus.UnstagedChanges()...)
	selectedUntracked := []scanner.GitChange{}
	stageMode := GitStageStagedOnly
	if len(staged) == 0 {
		includedChanges = filterParentCommitEligible(repoStatus.Changes)
		excludedChanges = filterParentCommitBlocked(repoStatus.Changes)
		stageMode = GitStageAllChanges
	} else {
		includedChanges = filterParentCommitEligible(staged)
	}
	if len(includedChanges) == 0 {
		blockedSubmodules := blockedSubmodulePaths(repoStatus.Changes)
		if len(blockedSubmodules) > 0 {
			return CommitPreview{}, SubmoduleAttentionError{
				ProjectPath: projectPath,
				ProjectName: projectName,
				Branch:      branchName,
				Submodules:  blockedSubmodules,
				PushWarning: pushWarning,
			}
		}
		return CommitPreview{}, NoChangesToCommitError{
			ProjectPath: projectPath,
			ProjectName: projectName,
			Branch:      branchName,
			Ahead:       repoStatus.Ahead,
			Behind:      repoStatus.Behind,
			CanPush:     canPush && repoStatus.Ahead > 0,
			PushWarning: pushWarning,
		}
	}

	// When the commit dialog opens, check whether the session classification
	// is stale relative to the current session content.  If so, trigger an
	// early refresh and wait for it so the commit message gets fresh context.
	// This only adds latency when the summary truly needs updating.
	if stageMode != GitStageStagedOnly {
		if s.refreshClassificationForCommit(ctx, projectPath, detail) {
			if freshDetail, err := s.store.GetProjectDetail(ctx, projectPath, 5); err == nil {
				detail = freshDetail
			}
		}
	}

	fullLatestSummary := strings.TrimSpace(detail.Summary.LatestSessionSummary)
	// When files are pre-staged, the session summary may describe work that
	// happened after staging, so exclude it from the commit message to keep
	// the message grounded in the actual diff.
	latestSummary := fullLatestSummary
	if stageMode == GitStageStagedOnly {
		latestSummary = ""
	}
	stateHash := commitPreviewStateHash(projectName, latestSummary, repoStatus)
	diffStat := ""
	patch := ""
	if stageMode == GitStageAllChanges {
		diffStat, err = gitops.ReadDiffStatAllStaged(ctx, projectPath)
		if err != nil {
			return CommitPreview{}, err
		}
		patches, err := gitops.ReadDiffPatchPerFile(ctx, projectPath, false, gitChangePaths(includedChanges), 10000)
		if err != nil {
			return CommitPreview{}, err
		}
		patch = gitops.MergeDiffPatches(patches)
	} else {
		diffStat, err = gitops.ReadDiffStat(ctx, projectPath, true)
		if err != nil {
			return CommitPreview{}, err
		}
		patches, err := gitops.ReadDiffPatchPerFile(ctx, projectPath, true, gitChangePaths(includedChanges), 10000)
		if err != nil {
			return CommitPreview{}, err
		}
		patch = gitops.MergeDiffPatches(patches)
	}

	warnings := []string{}
	if stageMode == GitStageStagedOnly {
		untracked := repoStatus.UntrackedChanges()
		reviewableUntracked, skippedUntracked := splitAutoReviewableUntracked(untracked)
		if skippedWarning := formatSkippedUntrackedAutoReviewWarning(skippedUntracked); skippedWarning != "" {
			warnings = append(warnings, skippedWarning)
		}
		switch {
		case len(reviewableUntracked) == 0:
		case s.untrackedFileRecommender == nil:
			warnings = append(warnings, "AI untracked review unavailable; untracked files will stay out unless you stage them manually.")
		default:
			input, buildWarnings, buildErr := buildUntrackedInclusionInput(projectPath, intent, projectName, branchName, fullLatestSummary, staged, diffStat, patch, reviewableUntracked)
			warnings = append(warnings, buildWarnings...)
			if buildErr != nil {
				warnings = append(warnings, "AI untracked review unavailable: "+strings.TrimSpace(buildErr.Error()))
			} else {
				assistantCtx, cancel := s.withCommitAssistantTimeout(ctx)
				suggestion, suggestErr := s.untrackedFileRecommender.RecommendUntracked(assistantCtx, input)
				cancel()
				if suggestErr != nil {
					warnings = append(warnings, "AI untracked review unavailable: "+formatCommitAssistantError(suggestErr, s.effectiveCommitAssistantTimeout()))
				} else {
					var selectedDecisions []gitops.UntrackedFileDecision
					selectedUntracked, selectedDecisions = selectRecommendedUntracked(reviewableUntracked, suggestion.Files)
					if len(selectedUntracked) > 0 {
						includedChanges = append(append([]scanner.GitChange{}, staged...), selectedUntracked...)
						excludedChanges = excludeChangesByPath(excludedChanges, gitChangePaths(selectedUntracked))
						diffStat, err = gitops.ReadDiffStatWithAddedPaths(ctx, projectPath, gitChangePaths(selectedUntracked))
						if err != nil {
							return CommitPreview{}, err
						}
						patches, err := gitops.ReadDiffPatchPerFile(ctx, projectPath, true, gitChangePaths(includedChanges), 10000)
						if err != nil {
							return CommitPreview{}, err
						}
						patch = gitops.MergeDiffPatches(patches)
						warnings = append(warnings, formatSelectedUntrackedWarning(len(selectedUntracked)))
						if reviewNote := formatSelectedUntrackedReview(selectedDecisions); reviewNote != "" {
							warnings = append(warnings, reviewNote)
						}
					}
				}
			}
		}
	}

	preview := CommitPreview{
		Intent:            intent,
		ProjectPath:       projectPath,
		ProjectName:       projectName,
		Branch:            branchName,
		StageMode:         stageMode,
		Included:          summarizeCommitFiles(includedChanges),
		Excluded:          summarizeCommitFiles(excludedChanges),
		SelectedUntracked: summarizeCommitFiles(selectedUntracked),
		DiffStat:          diffStat,
		DiffSummary:       diffStatSummary(diffStat),
		LatestSummary:     latestSummary,
		Warnings:          warnings,
		StateHash:         stateHash,
	}

	if stageMode == GitStageAllChanges {
		preview.Warnings = append(preview.Warnings, "All current changes will be staged before commit.")
	} else if len(preview.Excluded) > 0 {
		if len(preview.SelectedUntracked) > 0 {
			preview.Warnings = append(preview.Warnings, "Other local changes will stay in your worktree.")
		} else {
			preview.Warnings = append(preview.Warnings, "Only staged changes will be committed; other local changes stay in your worktree.")
		}
	}
	preview.Warnings = append(preview.Warnings, submodulePreviewWarnings(includedChanges, excludedChanges)...)

	// Collect open TODOs for the AI to evaluate.
	openTodos := openTodoRefs(detail.Todos)

	messageOverride = normalizeCommitMessage(messageOverride)
	if messageOverride != "" {
		preview.Message = messageOverride
	} else {
		input := gitops.CommitMessageInput{
			Intent:                  string(intent),
			ProjectName:             preview.ProjectName,
			Branch:                  preview.Branch,
			StageMode:               string(preview.StageMode),
			LatestSessionSummary:    preview.LatestSummary,
			IncludedFiles:           commitFilePaths(preview.Included),
			SuggestedUntrackedFiles: commitFilePaths(preview.SelectedUntracked),
			ExcludedFiles:           commitFilePaths(preview.Excluded),
			DiffStat:                preview.DiffStat,
			Patch:                   patch,
			OpenTodos:               openTodos,
		}
		if s.commitMessageSuggester != nil {
			assistantCtx, cancel := s.withCommitAssistantTimeout(ctx)
			suggestion, suggestErr := s.commitMessageSuggester.Suggest(assistantCtx, input)
			cancel()
			if suggestErr == nil {
				preview.Message = normalizeCommitMessage(suggestion.Message)
				preview.SuggestedTodos = matchSuggestedTodos(openTodos, detail.Todos, suggestion.CompletedTodoIDs)
			} else {
				errText := formatCommitAssistantError(suggestErr, s.effectiveCommitAssistantTimeout())
				preview.CommitMessageError = errText
				preview.Warnings = append(preview.Warnings, "AI commit message unavailable: "+errText)
			}
		}
		if preview.Message == "" {
			preview.Message = fallbackCommitMessage(preview.ProjectName, preview.Included)
		}
	}

	preview.CanPush = canPush
	if pushWarning != "" {
		preview.Warnings = append(preview.Warnings, pushWarning)
	}
	return preview, nil
}

func (s *Service) effectiveCommitAssistantTimeout() time.Duration {
	if s == nil || s.commitAssistantTimeout <= 0 {
		return defaultCommitAssistantTimeout
	}
	return s.commitAssistantTimeout
}

func (s *Service) withCommitAssistantTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.effectiveCommitAssistantTimeout())
}

func formatCommitAssistantError(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out after " + timeout.Round(time.Millisecond).String()
	}
	errText := strings.TrimSpace(err.Error())
	if errText == "" {
		return "unknown error"
	}
	return errText
}

func (s *Service) CommitPreviewStateHash(ctx context.Context, projectPath string) (string, error) {
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 1)
	if err != nil {
		return "", err
	}
	if !projectPathExists(projectPath) {
		return "", fmt.Errorf("project not found on disk: %s", projectPath)
	}

	repoStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return "", err
	}

	projectName := commitPreviewProjectName(projectPath, detail.Summary.Name)
	latestSummary := strings.TrimSpace(detail.Summary.LatestSessionSummary)
	return commitPreviewStateHash(projectName, latestSummary, repoStatus), nil
}

func (s *Service) ApplyCommit(ctx context.Context, preview CommitPreview, pushAfterCommit bool, completedTodoIDs []int64) (CommitResult, error) {
	if preview.ProjectPath == "" {
		return CommitResult{}, fmt.Errorf("project path required")
	}
	if preview.Message == "" {
		return CommitResult{}, fmt.Errorf("commit message required")
	}
	if pushAfterCommit && !preview.CanPush {
		return CommitResult{}, fmt.Errorf("commit & push unavailable for this repo")
	}

	if preview.StageMode == GitStageAllChanges {
		if err := gitops.StageAll(ctx, preview.ProjectPath); err != nil {
			return CommitResult{}, err
		}
	} else if err := gitops.StagePaths(ctx, preview.ProjectPath, commitFileStagePaths(preview.SelectedUntracked)); err != nil {
		return CommitResult{}, err
	}
	repoStatus, err := s.gitRepoStatusReader(ctx, preview.ProjectPath)
	if err != nil {
		return CommitResult{}, err
	}
	if len(filterParentCommitEligible(repoStatus.StagedChanges())) == 0 {
		blockedSubmodules := blockedSubmodulePaths(repoStatus.Changes)
		if len(blockedSubmodules) > 0 {
			return CommitResult{}, SubmoduleAttentionError{
				ProjectPath: preview.ProjectPath,
				ProjectName: preview.ProjectName,
				Branch:      preview.Branch,
				Submodules:  blockedSubmodules,
			}
		}
		canPush, pushWarning := pushAvailability(repoStatus)
		return CommitResult{}, NoChangesToCommitError{
			ProjectPath: preview.ProjectPath,
			ProjectName: preview.ProjectName,
			Branch:      strings.TrimSpace(repoStatus.Branch),
			Ahead:       repoStatus.Ahead,
			Behind:      repoStatus.Behind,
			CanPush:     canPush && repoStatus.Ahead > 0,
			PushWarning: pushWarning,
		}
	}

	hash, err := gitops.Commit(ctx, preview.ProjectPath, preview.Message)
	if err != nil {
		return CommitResult{}, err
	}

	now := time.Now()
	action := "git_commit"
	status := fmt.Sprintf("commit %s", hash)
	result := CommitResult{
		ProjectPath: preview.ProjectPath,
		Branch:      preview.Branch,
		CommitHash:  hash,
	}
	if pushAfterCommit {
		pushErr := gitops.Push(ctx, preview.ProjectPath)
		if pushErr != nil {
			result.Warning = fmt.Sprintf("Committed %s, but push failed: %s", hash, pushErr)
		} else {
			result.Pushed = true
			action = "git_finish"
			status = fmt.Sprintf("commit %s and push", hash)
		}
	}

	// Mark TODOs that the user confirmed as completed.
	for _, todoID := range completedTodoIDs {
		_ = s.ToggleTodoDone(ctx, preview.ProjectPath, todoID, true)
	}

	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: preview.ProjectPath, Payload: map[string]string{"action": action, "status": status}})
	_ = s.store.AddEvent(ctx, eventForGitAction(now, preview.ProjectPath, action, result))

	return result, nil
}

func commitPreviewProjectName(projectPath, configuredName string) string {
	projectName := strings.TrimSpace(configuredName)
	if projectName == "" {
		projectName = filepath.Base(projectPath)
	}
	return projectName
}

func commitPreviewBranchName(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "(detached)"
	}
	return branch
}

func commitPreviewStateHash(projectName, latestSummary string, repoStatus scanner.GitRepoStatus) string {
	changes := append([]scanner.GitChange(nil), repoStatus.Changes...)
	sort.Slice(changes, func(i, j int) bool {
		return commitPreviewChangeSortKey(changes[i]) < commitPreviewChangeSortKey(changes[j])
	})

	hasher := sha256.New()
	writeCommitPreviewHashLine(hasher, "project", strings.TrimSpace(projectName))
	writeCommitPreviewHashLine(hasher, "summary", strings.TrimSpace(latestSummary))
	writeCommitPreviewHashLine(hasher, "branch", commitPreviewBranchName(repoStatus.Branch))
	writeCommitPreviewHashLine(hasher, "ahead", fmt.Sprintf("%d", repoStatus.Ahead))
	writeCommitPreviewHashLine(hasher, "behind", fmt.Sprintf("%d", repoStatus.Behind))
	writeCommitPreviewHashLine(hasher, "has_remote", fmt.Sprintf("%t", repoStatus.HasRemote))
	writeCommitPreviewHashLine(hasher, "has_upstream", fmt.Sprintf("%t", repoStatus.HasUpstream))
	for _, change := range changes {
		writeCommitPreviewHashLine(hasher, "change", commitPreviewChangeSortKey(change))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func commitPreviewChangeSortKey(change scanner.GitChange) string {
	return strings.Join([]string{
		strings.TrimSpace(change.Path),
		strings.TrimSpace(change.OriginalPath),
		change.Code,
		string(change.Kind),
		fmt.Sprintf("%t", change.Staged),
		fmt.Sprintf("%t", change.Unstaged),
		fmt.Sprintf("%t", change.Untracked),
		fmt.Sprintf("%t", change.IsSubmodule),
		fmt.Sprintf("%t", change.SubmoduleCommitChanged),
		fmt.Sprintf("%t", change.SubmoduleModified),
		fmt.Sprintf("%t", change.SubmoduleUntracked),
	}, "|")
}

func writeCommitPreviewHashLine(builder interface{ Write([]byte) (int, error) }, label, value string) {
	_, _ = builder.Write([]byte(label))
	_, _ = builder.Write([]byte("="))
	_, _ = builder.Write([]byte(value))
	_, _ = builder.Write([]byte{'\n'})
}

func (s *Service) PushProject(ctx context.Context, projectPath string) (PushResult, error) {
	if !projectPathExists(projectPath) {
		return PushResult{}, fmt.Errorf("project not found on disk: %s", projectPath)
	}

	repoStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return PushResult{}, err
	}
	branch := strings.TrimSpace(repoStatus.Branch)
	result := PushResult{
		ProjectPath: projectPath,
		Branch:      branch,
	}

	switch {
	case !repoStatus.HasRemote:
		return PushResult{}, fmt.Errorf("repo has no remote")
	case !repoStatus.HasUpstream:
		return PushResult{}, fmt.Errorf("repo has no upstream tracking branch")
	case repoStatus.Behind > 0 && repoStatus.Ahead > 0:
		return PushResult{}, fmt.Errorf("branch has diverged from upstream (+%d/-%d)", repoStatus.Ahead, repoStatus.Behind)
	case repoStatus.Behind > 0:
		return PushResult{}, fmt.Errorf("branch is behind upstream by %d commit(s)", repoStatus.Behind)
	case repoStatus.Ahead == 0:
		result.Summary = "Nothing to push; branch already synced"
		return result, nil
	}

	if err := gitops.Push(ctx, projectPath); err != nil {
		return PushResult{}, err
	}
	now := time.Now()
	result.Pushed = true
	result.Summary = "Pushed latest commits"
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: map[string]string{"action": "git_push"}})
	_ = s.store.AddEvent(ctx, eventForPush(now, projectPath, branch))
	return result, nil
}

func summarizeCommitFiles(changes []scanner.GitChange) []CommitFile {
	out := make([]CommitFile, 0, len(changes))
	for _, change := range changes {
		summary := change.Path
		if change.OriginalPath != "" {
			summary = change.OriginalPath + " -> " + change.Path
		}
		code := strings.TrimSpace(change.Code)
		if code == "" {
			code = strings.ToUpper(string(change.Kind))
		}
		out = append(out, CommitFile{
			Path:    change.Path,
			Code:    code,
			Summary: summary,
		})
	}
	return out
}

func commitFilePaths(files []CommitFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Summary)
	}
	return out
}

func normalizeCommitMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	message = strings.Join(strings.Fields(message), " ")
	return strings.TrimSpace(strings.TrimSuffix(message, "."))
}

func fallbackCommitMessage(projectName string, included []CommitFile) string {
	if len(included) == 1 {
		switch included[0].Code {
		case "A", "??":
			return "Add " + filepath.Base(included[0].Path)
		case "D":
			return "Remove " + filepath.Base(included[0].Path)
		default:
			return "Update " + filepath.Base(included[0].Path)
		}
	}
	if strings.TrimSpace(projectName) != "" {
		return "Update " + projectName
	}
	return fmt.Sprintf("Update %d files", len(included))
}

func diffStatSummary(diffStat string) string {
	lines := strings.Split(diffStat, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func filterParentCommitEligible(changes []scanner.GitChange) []scanner.GitChange {
	out := make([]scanner.GitChange, 0, len(changes))
	for _, change := range changes {
		if change.ParentCommitEligible() {
			out = append(out, change)
		}
	}
	return out
}

func filterParentCommitBlocked(changes []scanner.GitChange) []scanner.GitChange {
	out := make([]scanner.GitChange, 0, len(changes))
	for _, change := range changes {
		if !change.ParentCommitEligible() {
			out = append(out, change)
		}
	}
	return out
}

func blockedSubmodulePaths(changes []scanner.GitChange) []string {
	return uniqueSubmodulePaths(changes, func(change scanner.GitChange) bool {
		return change.IsSubmodule && !change.ParentCommitEligible() && change.SubmoduleWorktreeDirty()
	})
}

func dirtyIncludedSubmodulePaths(changes []scanner.GitChange) []string {
	return uniqueSubmodulePaths(changes, func(change scanner.GitChange) bool {
		return change.ParentCommitEligible() && change.SubmoduleWorktreeDirty()
	})
}

func uniqueSubmodulePaths(changes []scanner.GitChange, keep func(scanner.GitChange) bool) []string {
	seen := make(map[string]struct{}, len(changes))
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		if !keep(change) {
			continue
		}
		if _, ok := seen[change.Path]; ok {
			continue
		}
		seen[change.Path] = struct{}{}
		out = append(out, change.Path)
	}
	return out
}

func submodulePreviewWarnings(includedChanges, excludedChanges []scanner.GitChange) []string {
	warnings := []string{}
	if blocked := blockedSubmodulePaths(excludedChanges); len(blocked) > 0 {
		warnings = append(warnings, formatBlockedSubmoduleWarning(blocked))
	}
	if dirtyIncluded := dirtyIncludedSubmodulePaths(includedChanges); len(dirtyIncluded) > 0 {
		warnings = append(warnings, formatDirtyIncludedSubmoduleWarning(dirtyIncluded))
	}
	return warnings
}

func formatBlockedSubmoduleWarning(paths []string) string {
	if len(paths) == 1 {
		return fmt.Sprintf("Submodule %s has local changes inside it. The parent commit will not include them; commit or discard them in that submodule first.", paths[0])
	}
	return fmt.Sprintf("Submodules %s have local changes inside them. The parent commit will not include those edits; commit or discard them in the submodules first.", strings.Join(paths, ", "))
}

func formatDirtyIncludedSubmoduleWarning(paths []string) string {
	if len(paths) == 1 {
		return fmt.Sprintf("Submodule %s still has additional local changes inside it. The parent commit can record the submodule pointer, but those submodule edits will stay in its worktree.", paths[0])
	}
	return fmt.Sprintf("Submodules %s still have additional local changes inside them. The parent commit can record their pointers, but those submodule edits will stay in each submodule worktree.", strings.Join(paths, ", "))
}

func pushAvailability(status scanner.GitRepoStatus) (bool, string) {
	switch {
	case !status.HasRemote:
		return false, "Commit & push unavailable: no remote is configured."
	case !status.HasUpstream:
		return false, "Commit & push unavailable: no upstream tracking branch is configured."
	case status.Behind > 0 && status.Ahead > 0:
		return false, fmt.Sprintf("Commit & push unavailable: branch diverged from upstream (+%d/-%d).", status.Ahead, status.Behind)
	case status.Behind > 0:
		return false, fmt.Sprintf("Commit & push unavailable: branch is behind upstream by %d commit(s).", status.Behind)
	default:
		return true, ""
	}
}

func eventForGitAction(now time.Time, projectPath, action string, result CommitResult) model.StoredEvent {
	payload := fmt.Sprintf("%s %s", action, result.CommitHash)
	if result.Pushed {
		payload += " pushed"
	}
	if result.Warning != "" {
		payload += " (" + result.Warning + ")"
	}
	return model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     payload,
	}
}

func eventForPush(now time.Time, projectPath, branch string) model.StoredEvent {
	payload := "git_push"
	if strings.TrimSpace(branch) != "" {
		payload += " " + strings.TrimSpace(branch)
	}
	return model.StoredEvent{
		At:          now,
		ProjectPath: projectPath,
		Type:        string(events.ActionApplied),
		Payload:     payload,
	}
}

// refreshClassificationForCommit checks whether the latest session
// classification is fresh relative to the current session content.  If it is
// stale or still in progress, it triggers (or waits for) a refresh and returns
// true so the caller knows to re-read the project detail.
func (s *Service) refreshClassificationForCommit(ctx context.Context, projectPath string, detail model.ProjectDetail) bool {
	if s.classifier == nil || len(detail.Sessions) == 0 {
		return false
	}
	classification := detail.LatestSessionClassification
	if classification == nil {
		return false
	}

	latestSession := detail.Sessions[0]

	// If a classification is already in progress, just wait for it.
	if classification.Status == model.ClassificationPending || classification.Status == model.ClassificationRunning {
		return s.waitForClassification(ctx, latestSession.SessionID)
	}

	// Classification is completed (or failed).  Compute the current snapshot
	// hash and compare it with the classification's hash to detect staleness.
	gitStatus := sessionclassify.NewGitStatusSnapshot(
		detail.Summary.RepoDirty,
		detail.Summary.RepoSyncStatus,
		detail.Summary.RepoAheadCount,
		detail.Summary.RepoBehindCount,
	)
	currentHash, err := sessionclassify.ComputeSnapshotHash(ctx, projectPath, latestSession, gitStatus)
	if err != nil {
		return false
	}
	if currentHash == classification.SnapshotHash && classification.Status == model.ClassificationCompleted {
		return false // summary is fresh
	}

	// Stale — queue a new classification and wait for it.
	state := projectStateFromDetail(detail)
	if len(state.Sessions) > 0 {
		state.Sessions[0].SnapshotHash = currentHash
	}
	queued, _ := s.classifier.QueueProject(ctx, state)
	if queued {
		s.classifier.Notify()
	}
	return s.waitForClassification(ctx, latestSession.SessionID)
}

const classificationWaitTimeout = 15 * time.Second

// waitForClassification polls until the classification for sessionID reaches a
// terminal state (completed or failed) or the timeout expires.  Returns true
// when the classification completed successfully.
func (s *Service) waitForClassification(ctx context.Context, sessionID string) bool {
	ctx, cancel := context.WithTimeout(ctx, classificationWaitTimeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			c, err := s.store.GetSessionClassification(ctx, sessionID)
			if err != nil {
				return false
			}
			if c.Status == model.ClassificationCompleted {
				return true
			}
			if c.Status == model.ClassificationFailed {
				return false
			}
		}
	}
}

func projectStateFromDetail(detail model.ProjectDetail) model.ProjectState {
	return model.ProjectState{
		Path:                 detail.Summary.Path,
		Name:                 detail.Summary.Name,
		LastActivity:         detail.Summary.LastActivity,
		Status:               detail.Summary.Status,
		AttentionScore:       detail.Summary.AttentionScore,
		PresentOnDisk:        detail.Summary.PresentOnDisk,
		WorktreeRootPath:     detail.Summary.WorktreeRootPath,
		WorktreeKind:         detail.Summary.WorktreeKind,
		WorktreeParentBranch: detail.Summary.WorktreeParentBranch,
		WorktreeMergeStatus:  detail.Summary.WorktreeMergeStatus,
		WorktreeOriginTodoID: detail.Summary.WorktreeOriginTodoID,
		RepoBranch:           detail.Summary.RepoBranch,
		RepoDirty:            detail.Summary.RepoDirty,
		RepoConflict:         detail.Summary.RepoConflict,
		RepoSyncStatus:       detail.Summary.RepoSyncStatus,
		RepoAheadCount:       detail.Summary.RepoAheadCount,
		RepoBehindCount:      detail.Summary.RepoBehindCount,
		Forgotten:            detail.Summary.Forgotten,
		ManuallyAdded:        detail.Summary.ManuallyAdded,
		InScope:              detail.Summary.InScope,
		Pinned:               detail.Summary.Pinned,
		SnoozedUntil:         detail.Summary.SnoozedUntil,
		RunCommand:           detail.Summary.RunCommand,
		MovedFromPath:        detail.Summary.MovedFromPath,
		MovedAt:              detail.Summary.MovedAt,
		Sessions:             detail.Sessions,
		Artifacts:            detail.Artifacts,
	}
}

func openTodoRefs(todos []model.TodoItem) []gitops.CommitTodoRef {
	var refs []gitops.CommitTodoRef
	for _, t := range todos {
		if !t.Done {
			refs = append(refs, gitops.CommitTodoRef{ID: t.ID, Text: t.Text})
		}
	}
	return refs
}

func matchSuggestedTodos(refs []gitops.CommitTodoRef, allTodos []model.TodoItem, completedIDs []int64) []TodoCompletion {
	if len(completedIDs) == 0 {
		return nil
	}
	// Build a set of open TODO IDs for validation.
	openSet := make(map[int64]struct{}, len(refs))
	for _, r := range refs {
		openSet[r.ID] = struct{}{}
	}
	// Build ID→text lookup from all todos.
	textByID := make(map[int64]string, len(allTodos))
	for _, t := range allTodos {
		textByID[t.ID] = t.Text
	}

	var result []TodoCompletion
	for _, id := range completedIDs {
		if _, ok := openSet[id]; !ok {
			continue // AI hallucinated or referenced a done/nonexistent TODO
		}
		result = append(result, TodoCompletion{
			ID:   id,
			Text: textByID[id],
		})
	}
	return result
}
