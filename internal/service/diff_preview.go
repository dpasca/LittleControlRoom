package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"lcroom/internal/gitops"
	"lcroom/internal/scanner"
)

const (
	diffPreviewPatchBytes = 24_000
	diffPreviewBlobBytes  = 8 << 20
)

type DiffPreview struct {
	ProjectPath string
	ProjectName string
	Branch      string
	Summary     string
	Files       []DiffFilePreview
}

type DiffFilePreview struct {
	Path         string
	OriginalPath string
	Code         string
	Summary      string
	Kind         scanner.GitChangeKind
	Staged       bool
	Unstaged     bool
	Untracked    bool
	Body         string
	IsImage      bool
	OldImage     []byte
	NewImage     []byte
}

type NoDiffChangesError struct {
	ProjectPath string
	ProjectName string
	Branch      string
}

func (e NoDiffChangesError) Error() string {
	base := "no changed files to diff"
	project := strings.TrimSpace(e.ProjectName)
	if project == "" && strings.TrimSpace(e.ProjectPath) != "" {
		project = filepath.Base(e.ProjectPath)
	}
	if project == "" {
		return base
	}
	return fmt.Sprintf("%s for %s", base, project)
}

func (s *Service) PrepareDiff(ctx context.Context, projectPath string) (DiffPreview, error) {
	detail, err := s.store.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		return DiffPreview{}, err
	}
	if !projectPathExists(projectPath) {
		return DiffPreview{}, fmt.Errorf("project not found on disk: %s", projectPath)
	}

	repoStatus, err := s.gitRepoStatusReader(ctx, projectPath)
	if err != nil {
		return DiffPreview{}, err
	}

	projectName := strings.TrimSpace(detail.Summary.Name)
	if projectName == "" {
		projectName = filepath.Base(projectPath)
	}
	branch := strings.TrimSpace(repoStatus.Branch)
	if branch == "" {
		branch = "(detached)"
	}
	if len(repoStatus.Changes) == 0 {
		return DiffPreview{}, NoDiffChangesError{
			ProjectPath: projectPath,
			ProjectName: projectName,
			Branch:      branch,
		}
	}

	changes := append([]scanner.GitChange(nil), repoStatus.Changes...)
	sort.SliceStable(changes, func(i, j int) bool {
		left, right := diffGroupRank(changes[i]), diffGroupRank(changes[j])
		if left != right {
			return left < right
		}
		leftPath := strings.ToLower(diffSummaryForChange(changes[i]))
		rightPath := strings.ToLower(diffSummaryForChange(changes[j]))
		return leftPath < rightPath
	})

	files := make([]DiffFilePreview, 0, len(changes))
	for _, change := range changes {
		files = append(files, buildDiffFilePreview(ctx, projectPath, change))
	}

	summary := fmt.Sprintf("%d changed file(s)", len(files))
	if diffStat, statErr := gitops.ReadDiffStatAllStaged(ctx, projectPath); statErr == nil {
		if statSummary := strings.TrimSpace(diffStatSummary(diffStat)); statSummary != "" {
			summary = statSummary
		}
	}

	return DiffPreview{
		ProjectPath: projectPath,
		ProjectName: projectName,
		Branch:      branch,
		Summary:     summary,
		Files:       files,
	}, nil
}

func buildDiffFilePreview(ctx context.Context, projectPath string, change scanner.GitChange) DiffFilePreview {
	preview := DiffFilePreview{
		Path:         change.Path,
		OriginalPath: change.OriginalPath,
		Code:         change.Code,
		Summary:      diffSummaryForChange(change),
		Kind:         change.Kind,
		Staged:       change.Staged,
		Unstaged:     change.Unstaged,
		Untracked:    change.Untracked,
	}

	if isPreviewImage(change.Path) || isPreviewImage(change.OriginalPath) {
		preview.IsImage = true
		preview.Body, preview.OldImage, preview.NewImage = buildImageDiffPreview(ctx, projectPath, change)
		return preview
	}

	preview.Body = buildTextDiffPreview(ctx, projectPath, change)
	return preview
}

func buildTextDiffPreview(ctx context.Context, projectPath string, change scanner.GitChange) string {
	sections := []string{}
	pathspecs := diffPathspecs(change)

	if change.Staged && !change.Untracked {
		patch, truncated, err := gitops.ReadDiffPatchForPaths(ctx, projectPath, true, pathspecs, diffPreviewPatchBytes)
		switch {
		case err != nil:
			sections = append(sections, "# Staged\n\nStaged diff unavailable: "+strings.TrimSpace(err.Error()))
		case strings.TrimSpace(patch) != "":
			if truncated {
				patch += "\n\n# staged diff truncated"
			}
			sections = append(sections, "# Staged\n\n"+patch)
		}
	}
	if change.Unstaged && !change.Untracked {
		patch, truncated, err := gitops.ReadDiffPatchForPaths(ctx, projectPath, false, pathspecs, diffPreviewPatchBytes)
		switch {
		case err != nil:
			sections = append(sections, "# Unstaged\n\nUnstaged diff unavailable: "+strings.TrimSpace(err.Error()))
		case strings.TrimSpace(patch) != "":
			if truncated {
				patch += "\n\n# unstaged diff truncated"
			}
			sections = append(sections, "# Unstaged\n\n"+patch)
		}
	}
	if change.Untracked {
		data, truncated, err := gitops.ReadWorkingTreeFile(projectPath, change.Path, diffPreviewPatchBytes)
		switch {
		case err != nil:
			sections = append(sections, "# Untracked\n\nFile preview unavailable: "+strings.TrimSpace(err.Error()))
		case isProbablyText(data):
			preview := formatUntrackedTextPreview(data)
			if truncated {
				preview += "\n# file preview truncated"
			}
			sections = append(sections, "# Untracked\n\n"+preview)
		default:
			sections = append(sections, "# Untracked\n\nBinary file preview unavailable.")
		}
	}
	if len(sections) == 0 {
		return "No textual diff available."
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildImageDiffPreview(ctx context.Context, projectPath string, change scanner.GitChange) (string, []byte, []byte) {
	lines := []string{
		"Binary image change rendered as ANSI preview.",
		fmt.Sprintf("State: %s", diffStateLabel(change)),
	}
	oldPath := change.Path
	if strings.TrimSpace(change.OriginalPath) != "" {
		oldPath = change.OriginalPath
	}

	var oldImage []byte
	if change.Kind != scanner.GitChangeAdded && !change.Untracked {
		data, truncated, err := gitops.ReadGitBlob(ctx, projectPath, oldPath, diffPreviewBlobBytes)
		switch {
		case err != nil:
			lines = append(lines, "HEAD image unavailable: "+strings.TrimSpace(err.Error()))
		case truncated:
			lines = append(lines, "HEAD image preview truncated before decode.")
		default:
			oldImage = data
		}
	}

	var newImage []byte
	if change.Kind != scanner.GitChangeDeleted {
		data, truncated, err := gitops.ReadWorkingTreeFile(projectPath, change.Path, diffPreviewBlobBytes)
		switch {
		case err != nil:
			lines = append(lines, "Working tree image unavailable: "+strings.TrimSpace(err.Error()))
		case truncated:
			lines = append(lines, "Working tree image preview truncated before decode.")
		default:
			newImage = data
		}
	}

	if change.Staged && change.Unstaged {
		lines = append(lines, "Selection includes staged and unstaged changes.")
	} else if change.Staged {
		lines = append(lines, "Selection includes staged changes.")
	} else if change.Unstaged {
		lines = append(lines, "Selection includes unstaged changes.")
	}
	if len(oldImage) == 0 && len(newImage) == 0 {
		lines = append(lines, "Image preview unavailable.")
	}
	return strings.Join(lines, "\n"), oldImage, newImage
}

func diffSummaryForChange(change scanner.GitChange) string {
	if strings.TrimSpace(change.OriginalPath) != "" && filepath.Clean(change.OriginalPath) != filepath.Clean(change.Path) {
		return change.OriginalPath + " -> " + change.Path
	}
	return change.Path
}

func diffPathspecs(change scanner.GitChange) []string {
	paths := []string{}
	if original := strings.TrimSpace(change.OriginalPath); original != "" {
		paths = append(paths, original)
	}
	if current := strings.TrimSpace(change.Path); current != "" {
		paths = append(paths, current)
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	out := paths[:0]
	for _, path := range paths {
		if len(out) == 0 || out[len(out)-1] != path {
			out = append(out, path)
		}
	}
	return out
}

func diffGroupRank(change scanner.GitChange) int {
	switch {
	case change.Untracked:
		return 1
	case change.Kind == scanner.GitChangeDeleted:
		return 2
	default:
		return 0
	}
}

func diffStateLabel(change scanner.GitChange) string {
	switch {
	case change.Untracked:
		return "untracked"
	case change.Kind == scanner.GitChangeDeleted:
		return "deleted"
	default:
		return "changed"
	}
}

func isPreviewImage(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".gif", ".jpeg", ".jpg", ".png":
		return true
	default:
		return false
	}
}

func isProbablyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	return !strings.ContainsRune(string(data), '\x00')
}

func formatUntrackedTextPreview(data []byte) string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "+"
	}
	for i, line := range lines {
		lines[i] = "+" + line
	}
	return strings.Join(lines, "\n")
}
