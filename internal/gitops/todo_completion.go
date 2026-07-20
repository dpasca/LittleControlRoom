package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"lcroom/internal/llm"
)

type CommitTodoCompletionInput struct {
	ProjectName       string                            `json:"project_name"`
	Branch            string                            `json:"branch,omitempty"`
	BaseHash          string                            `json:"base_hash,omitempty"`
	HeadHash          string                            `json:"head_hash"`
	CommitSubject     string                            `json:"commit_subject,omitempty"`
	ChangedFiles      []string                          `json:"changed_files,omitempty"`
	DiffStat          string                            `json:"diff_stat,omitempty"`
	Patch             string                            `json:"patch,omitempty"`
	EvidenceStrategy  string                            `json:"evidence_strategy,omitempty"`
	EvidenceModel     string                            `json:"evidence_model,omitempty"`
	EvidenceCommits   []CommitTodoEvidenceCommit        `json:"evidence_commits,omitempty"`
	EvidenceSelection []CommitTodoEvidenceSelectionItem `json:"evidence_selection,omitempty"`
	OpenTodos         []CommitTodoRef                   `json:"open_todos"`
}

type CommitTodoCompletionDecision struct {
	ID         int64   `json:"id"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

type CommitTodoCompletionSuggestion struct {
	CompletedTodos []CommitTodoCompletionDecision
	Model          string
}

type CommitTodoCompletionChecker interface {
	CheckCompletedTodos(context.Context, CommitTodoCompletionInput) (CommitTodoCompletionSuggestion, error)
	ModelName() string
}

type CommitTodoEvidenceCommit struct {
	Hash         string   `json:"hash"`
	Parents      []string `json:"parents,omitempty"`
	Subject      string   `json:"subject"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

type CommitTodoEvidenceSelectionInput struct {
	ProjectName string                     `json:"project_name"`
	Branch      string                     `json:"branch,omitempty"`
	BaseHash    string                     `json:"base_hash,omitempty"`
	HeadHash    string                     `json:"head_hash"`
	OpenTodos   []CommitTodoRef            `json:"open_todos"`
	Commits     []CommitTodoEvidenceCommit `json:"commits"`
}

type CommitTodoEvidenceSelectionItem struct {
	TodoIDs    []int64  `json:"todo_ids"`
	CommitHash string   `json:"commit_hash"`
	Files      []string `json:"files"`
	Reason     string   `json:"reason"`
}

type CommitTodoEvidenceSelection struct {
	Items []CommitTodoEvidenceSelectionItem `json:"selected_evidence"`
	Model string                            `json:"-"`
}

type CommitTodoEvidenceSelector interface {
	SelectCommitTodoEvidence(context.Context, CommitTodoEvidenceSelectionInput) (CommitTodoEvidenceSelection, error)
	ModelName() string
}

func (c *OpenAICommitMessageClient) SelectCommitTodoEvidence(ctx context.Context, input CommitTodoEvidenceSelectionInput) (CommitTodoEvidenceSelection, error) {
	if c == nil || c.responsesClient() == nil {
		return CommitTodoEvidenceSelection{}, errors.New(errCommitAssistantNotConfigured)
	}
	if len(input.OpenTodos) == 0 || len(input.Commits) == 0 {
		return CommitTodoEvidenceSelection{}, nil
	}
	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return CommitTodoEvidenceSelection{}, fmt.Errorf("marshal commit TODO evidence input: %w", err)
	}
	response, err := c.runJSONSchemaPrompt(
		ctx,
		commitTodoEvidenceSelectionInstructions,
		"Select commit files whose diffs should be inspected for these open TODOs:\n\n"+string(payload),
		"git_commit_todo_evidence_selection",
		commitTodoEvidenceSelectionSchema(input.Commits),
	)
	if err != nil {
		return CommitTodoEvidenceSelection{}, err
	}
	var out CommitTodoEvidenceSelection
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &out); err != nil {
		return CommitTodoEvidenceSelection{}, fmt.Errorf("decode commit TODO evidence selection: %w", err)
	}
	out.Model = strings.TrimSpace(response.Model)
	return out, nil
}

func (c *OpenAICommitMessageClient) CheckCompletedTodos(ctx context.Context, input CommitTodoCompletionInput) (CommitTodoCompletionSuggestion, error) {
	if c == nil || c.responsesClient() == nil {
		return CommitTodoCompletionSuggestion{}, errors.New(errCommitAssistantNotConfigured)
	}
	if len(input.OpenTodos) == 0 {
		return CommitTodoCompletionSuggestion{}, nil
	}
	if strings.TrimSpace(input.HeadHash) == "" {
		return CommitTodoCompletionSuggestion{}, errors.New("head hash is required")
	}

	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return CommitTodoCompletionSuggestion{}, fmt.Errorf("marshal commit TODO completion input: %w", err)
	}

	response, err := c.runJSONSchemaPrompt(
		ctx,
		commitTodoCompletionInstructions,
		"Determine whether this git commit or commit range completes any open TODOs:\n\n"+string(payload),
		"git_commit_todo_completion",
		commitTodoCompletionSchema(),
	)
	if err != nil {
		return CommitTodoCompletionSuggestion{}, err
	}

	var decoded struct {
		CompletedTodos []struct {
			ID         int64   `json:"id"`
			Reason     string  `json:"reason"`
			Confidence float64 `json:"confidence"`
		} `json:"completed_todos"`
	}
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &decoded); err != nil {
		return CommitTodoCompletionSuggestion{}, fmt.Errorf("decode commit TODO completion result: %w", err)
	}
	out := CommitTodoCompletionSuggestion{
		Model: strings.TrimSpace(response.Model),
	}
	for _, item := range decoded.CompletedTodos {
		out.CompletedTodos = append(out.CompletedTodos, CommitTodoCompletionDecision{
			ID:         item.ID,
			Reason:     strings.TrimSpace(item.Reason),
			Confidence: item.Confidence,
		})
	}
	return out, nil
}

func ReadCommitSubject(ctx context.Context, path, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "log", "-1", "--format=%s", ref)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read git commit subject for %s in %s: %w", ref, path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ReadCommitRangeDiffStat(ctx context.Context, path, baseHash, headHash string) (string, error) {
	args := commitRangeDiffArgs(path, baseHash, headHash, "--stat", "--find-renames")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read git commit range diff stat for %s..%s in %s: %w", baseHash, headHash, path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ReadCommitRangePatch(ctx context.Context, path, baseHash, headHash string, maxBytes int) (string, error) {
	patch, _, err := ReadCommitRangePatchWithStatus(ctx, path, baseHash, headHash, maxBytes)
	return patch, err
}

func ReadCommitRangePatchWithStatus(ctx context.Context, path, baseHash, headHash string, maxBytes int) (string, bool, error) {
	args := commitRangeDiffArgs(path, baseHash, headHash, "--unified=0", "--no-color", "--find-renames")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", false, fmt.Errorf("read git commit range patch for %s..%s in %s: %w", baseHash, headHash, path, err)
	}
	trimmed := strings.TrimSpace(string(out))
	truncated := false
	if maxBytes > 0 && len(trimmed) > maxBytes {
		trimmed = truncateCommitPatch(trimmed, maxBytes)
		truncated = true
	}
	return trimmed, truncated, nil
}

func ReadCommitRangeEvidenceCommits(ctx context.Context, path, baseHash, headHash string) ([]CommitTodoEvidenceCommit, error) {
	baseHash = strings.TrimSpace(baseHash)
	headHash = strings.TrimSpace(headHash)
	if headHash == "" {
		headHash = "HEAD"
	}
	args := []string{
		"-C", path,
		"log", "--reverse", "--topo-order",
		"--format=%x1e%H%x1f%P%x1f%s",
		"--name-only",
	}
	if baseHash == "" {
		args = append(args, "-1", headHash)
	} else {
		args = append(args, baseHash+".."+headHash)
	}
	args = append(args, "--", ".")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("read git commit range evidence for %s..%s in %s: %w", baseHash, headHash, path, err)
	}
	var commits []CommitTodoEvidenceCommit
	for _, record := range strings.Split(string(out), "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		header := strings.SplitN(strings.TrimSpace(lines[0]), "\x1f", 3)
		if len(header) != 3 {
			continue
		}
		commit := CommitTodoEvidenceCommit{
			Hash:    strings.TrimSpace(header[0]),
			Parents: strings.Fields(header[1]),
			Subject: strings.TrimSpace(header[2]),
		}
		seenFiles := map[string]struct{}{}
		for _, line := range lines[1:] {
			file := strings.TrimSpace(line)
			if file == "" {
				continue
			}
			if _, ok := seenFiles[file]; ok {
				continue
			}
			seenFiles[file] = struct{}{}
			commit.ChangedFiles = append(commit.ChangedFiles, file)
		}
		if commit.Hash != "" {
			commits = append(commits, commit)
		}
	}
	return commits, nil
}

func ReadCommitPatchForFiles(ctx context.Context, path string, commit CommitTodoEvidenceCommit, files []string, maxBytes int) (string, error) {
	commit.Hash = strings.TrimSpace(commit.Hash)
	if commit.Hash == "" {
		return "", errors.New("focused commit evidence requires a commit hash")
	}
	allowed := make(map[string]struct{}, len(commit.ChangedFiles))
	for _, file := range commit.ChangedFiles {
		allowed[strings.TrimSpace(file)] = struct{}{}
	}
	filtered := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if _, ok := allowed[file]; !ok {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		filtered = append(filtered, file)
	}
	if len(filtered) == 0 {
		return "", nil
	}
	var args []string
	if len(commit.Parents) == 0 {
		args = []string{"-C", path, "show", "--format=", "--no-ext-diff", "--unified=0", "--no-color", "--find-renames", commit.Hash, "--"}
	} else {
		args = []string{"-C", path, "diff", "--no-ext-diff", "--unified=0", "--no-color", "--find-renames", commit.Parents[0] + ".." + commit.Hash, "--"}
	}
	args = append(args, filtered...)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read focused git evidence for %s in %s: %w", commit.Hash, path, err)
	}
	return truncateCommitPatch(strings.TrimSpace(string(out)), maxBytes), nil
}

func truncateCommitPatch(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func ReadCommitRangeChangedFiles(ctx context.Context, path, baseHash, headHash string) ([]string, error) {
	args := commitRangeDiffArgs(path, baseHash, headHash, "--name-status", "--find-renames")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("read git commit range files for %s..%s in %s: %w", baseHash, headHash, path, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func commitRangeDiffArgs(path, baseHash, headHash string, flags ...string) []string {
	baseHash = strings.TrimSpace(baseHash)
	headHash = strings.TrimSpace(headHash)
	if headHash == "" {
		headHash = "HEAD"
	}
	args := []string{"-C", path}
	if baseHash == "" {
		args = append(args, "show", "--format=", "--no-ext-diff")
		args = append(args, flags...)
		args = append(args, headHash, "--", ".")
		return args
	}
	args = append(args, "diff", "--no-ext-diff")
	args = append(args, flags...)
	args = append(args, baseHash+".."+headHash, "--", ".")
	return args
}

const commitTodoCompletionInstructions = `You determine whether committed code completes open TODO items for a coding project.

Use only the supplied commit subject, changed files, diff stat, focused evidence commits, patch, and open_todos.
Do not use keyword matching; reason from the actual change evidence.
A TODO is complete only when the commit directly and substantially satisfies it, with no obvious implementation follow-up remaining.
Do not mark broad, vague, or merely related TODOs complete.
Do not infer completion from the branch name or commit subject alone when the diff evidence is weak.
Return only TODOs whose completion confidence is at least 0.75.
Return an empty completed_todos array when no TODO is clearly completed.
Return only the JSON fields requested by the schema.`

const commitTodoEvidenceSelectionInstructions = `You select commit evidence for a later TODO-completion assessment.

Use the semantic meaning of each open TODO, commit subject, and changed-file list. Do not use keyword or regex matching.
Select commit/file combinations whose actual diffs could directly show that a TODO was completed or that required validation/documentation was performed.
Be inclusive when evidence may be split between implementation, tests, and documentation.
Prefer the non-merge implementation commit over a merge commit when both are present.
Do not decide whether a TODO is complete in this step.
Return only commit hashes and file paths supplied in the input.
Return an empty selected_evidence array only when none of the commits could plausibly address any open TODO.
Return only the JSON fields requested by the schema.`

func commitTodoEvidenceSelectionSchema(commits []CommitTodoEvidenceCommit) map[string]any {
	hashes := make([]any, 0, len(commits))
	for _, commit := range commits {
		if hash := strings.TrimSpace(commit.Hash); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	hashSchema := map[string]any{"type": "string", "minLength": 1}
	if len(hashes) > 0 {
		hashSchema["enum"] = hashes
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"selected_evidence": map[string]any{
				"type":     "array",
				"maxItems": 12,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"todo_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 1},
						"commit_hash": hashSchema,
						"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}, "minItems": 1, "maxItems": 8},
						"reason":      map[string]any{"type": "string"},
					},
					"required": []string{"todo_ids", "commit_hash", "files", "reason"},
				},
			},
		},
		"required": []string{"selected_evidence"},
	}
}

func commitTodoCompletionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"completed_todos": map[string]any{
				"type":        "array",
				"description": "Open TODOs clearly completed by the commit evidence.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "integer",
							"description": "ID from open_todos.",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Brief evidence-based reason the TODO is complete.",
						},
						"confidence": map[string]any{
							"type":        "number",
							"description": "Completion confidence from 0 to 1.",
							"minimum":     0,
							"maximum":     1,
						},
					},
					"required": []string{"id", "reason", "confidence"},
				},
			},
		},
		"required": []string{"completed_todos"},
	}
}
