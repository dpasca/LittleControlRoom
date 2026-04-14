package service

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"lcroom/internal/gitops"
	"lcroom/internal/scanner"
)

const (
	maxUntrackedPreviewBytesPerFile = 900
	maxUntrackedPreviewBytesTotal   = 5400
	maxUntrackedDirectoryEntries    = 5
	maxSkippedUntrackedWarningPaths = 3
)

var autoExcludedUntrackedDirectoryNames = map[string]struct{}{
	"build":    {},
	"coverage": {},
	"dist":     {},
	"out":      {},
	"output":   {},
	"target":   {},
	"temp":     {},
	"tmp":      {},
}

func buildUntrackedInclusionInput(projectPath string, _ GitActionIntent, projectName, branch, latestSummary string, staged []scanner.GitChange, diffStat, patch string, candidates []scanner.GitChange) (gitops.UntrackedFileRecommendationInput, []string, error) {
	remainingBudget := maxUntrackedPreviewBytesTotal
	previewLimited := false
	reviewCandidates := make([]gitops.UntrackedFileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		reviewCandidate, usedPreviewBytes, usedBudgetLimit := describeUntrackedCandidate(projectPath, candidate.Path, remainingBudget)
		reviewCandidates = append(reviewCandidates, reviewCandidate)
		if usedPreviewBytes > 0 {
			remainingBudget -= usedPreviewBytes
			if remainingBudget < 0 {
				remainingBudget = 0
			}
		}
		if usedBudgetLimit {
			previewLimited = true
		}
	}

	warnings := []string{}
	if previewLimited {
		warnings = append(warnings, "Some untracked file previews were omitted or shortened to keep AI review compact.")
	}
	return gitops.UntrackedFileRecommendationInput{
		ProjectName:          projectName,
		Branch:               branch,
		LatestSessionSummary: latestSummary,
		StagedFiles:          commitFilePaths(summarizeCommitFiles(staged)),
		StagedDiffStat:       diffStat,
		StagedPatch:          patch,
		Candidates:           reviewCandidates,
	}, warnings, nil
}

func splitAutoReviewableUntracked(candidates []scanner.GitChange) ([]scanner.GitChange, []scanner.GitChange) {
	reviewable := make([]scanner.GitChange, 0, len(candidates))
	skipped := make([]scanner.GitChange, 0)
	for _, candidate := range candidates {
		if shouldAutoExcludeUntrackedDirectory(candidate.Path) {
			skipped = append(skipped, candidate)
			continue
		}
		reviewable = append(reviewable, candidate)
	}
	return reviewable, skipped
}

func shouldAutoExcludeUntrackedDirectory(relPath string) bool {
	trimmed := strings.TrimSpace(relPath)
	if !strings.HasSuffix(trimmed, "/") {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(path.Base(strings.TrimSuffix(trimmed, "/"))))
	if base == "" || base == "." {
		return false
	}
	_, ok := autoExcludedUntrackedDirectoryNames[base]
	return ok
}

func describeUntrackedCandidate(projectPath, relPath string, remainingBudget int) (gitops.UntrackedFileCandidate, int, bool) {
	candidate := gitops.UntrackedFileCandidate{
		Path: relPath,
		Kind: "file",
	}

	absPath := filepath.Join(projectPath, relPath)
	info, err := os.Stat(absPath)
	if err != nil {
		candidate.Kind = "unreadable"
		return candidate, 0, false
	}
	candidate.ByteSize = info.Size()
	if info.IsDir() {
		candidate.Kind = "directory"
		candidate.SampleEntries = readDirectorySample(absPath)
		return candidate, 0, false
	}

	if remainingBudget <= 0 {
		return candidate, 0, true
	}

	limit := maxUntrackedPreviewBytesPerFile
	if remainingBudget < limit {
		limit = remainingBudget
	}
	if limit <= 0 {
		return candidate, 0, true
	}

	file, err := os.Open(absPath)
	if err != nil {
		candidate.Kind = "unreadable"
		return candidate, 0, false
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil {
		candidate.Kind = "unreadable"
		return candidate, 0, false
	}
	if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
		candidate.Binary = true
		return candidate, 0, false
	}

	previewBytes := raw
	if len(previewBytes) > limit {
		previewBytes = previewBytes[:limit]
		candidate.PreviewTruncated = true
	}
	preview := strings.ReplaceAll(string(previewBytes), "\r\n", "\n")
	preview = strings.TrimSpace(preview)
	if preview != "" {
		candidate.Preview = preview
	}
	if len(raw) > limit {
		candidate.PreviewTruncated = true
	}
	return candidate, len(previewBytes), len(raw) > limit
}

func selectRecommendedUntracked(candidates []scanner.GitChange, decisions []gitops.UntrackedFileDecision) ([]scanner.GitChange, []gitops.UntrackedFileDecision) {
	selectedSet := make(map[string]gitops.UntrackedFileDecision, len(decisions))
	for _, candidate := range candidates {
		selectedSet[candidate.Path] = gitops.UntrackedFileDecision{}
	}
	for _, decision := range decisions {
		if !decision.Include {
			continue
		}
		path := strings.TrimSpace(decision.Path)
		if path == "" {
			continue
		}
		if _, ok := selectedSet[path]; !ok {
			continue
		}
		selectedSet[path] = decision
	}

	selected := make([]scanner.GitChange, 0, len(decisions))
	selectedDecisions := make([]gitops.UntrackedFileDecision, 0, len(decisions))
	for _, candidate := range candidates {
		decision, ok := selectedSet[candidate.Path]
		if !ok || !decision.Include {
			continue
		}
		selected = append(selected, candidate)
		selectedDecisions = append(selectedDecisions, decision)
	}
	return selected, selectedDecisions
}

func excludeChangesByPath(changes []scanner.GitChange, removePaths []string) []scanner.GitChange {
	if len(removePaths) == 0 {
		return append([]scanner.GitChange{}, changes...)
	}
	remove := make(map[string]struct{}, len(removePaths))
	for _, path := range removePaths {
		remove[path] = struct{}{}
	}
	out := make([]scanner.GitChange, 0, len(changes))
	for _, change := range changes {
		if _, ok := remove[change.Path]; ok {
			continue
		}
		out = append(out, change)
	}
	return out
}

func gitChangePaths(changes []scanner.GitChange) []string {
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		out = append(out, change.Path)
	}
	return out
}

func commitFileStagePaths(files []CommitFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func formatSelectedUntrackedWarning(count int) string {
	switch count {
	case 0:
		return ""
	case 1:
		return "Will also stage 1 AI-recommended untracked file before commit."
	default:
		return fmt.Sprintf("Will also stage %d AI-recommended untracked files before commit.", count)
	}
}

func formatSelectedUntrackedReview(decisions []gitops.UntrackedFileDecision) string {
	if len(decisions) == 0 {
		return ""
	}
	if len(decisions) == 1 {
		reason := strings.TrimSpace(decisions[0].Reason)
		if reason == "" {
			return ""
		}
		return "AI review: " + reason
	}
	return fmt.Sprintf("AI review: selected %d untracked files as part of the current commit.", len(decisions))
}

func formatSkippedUntrackedAutoReviewWarning(skipped []scanner.GitChange) string {
	if len(skipped) == 0 {
		return ""
	}
	paths := make([]string, 0, min(len(skipped), maxSkippedUntrackedWarningPaths))
	for i := 0; i < len(skipped) && i < maxSkippedUntrackedWarningPaths; i++ {
		paths = append(paths, skipped[i].Path)
	}
	if len(skipped) == 1 {
		return fmt.Sprintf("Skipped automatic review for 1 untracked directory with a common generated/export name: %s. Stage it manually if it belongs in this commit.", paths[0])
	}
	pathText := strings.Join(paths, ", ")
	if len(skipped) > len(paths) {
		pathText += fmt.Sprintf(", and %d more", len(skipped)-len(paths))
	}
	return fmt.Sprintf("Skipped automatic review for %d untracked directories with common generated/export names: %s. Stage them manually if they belong in this commit.", len(skipped), pathText)
}

func readDirectorySample(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxUntrackedDirectoryEntries {
		names = names[:maxUntrackedDirectoryEntries]
	}
	return names
}
