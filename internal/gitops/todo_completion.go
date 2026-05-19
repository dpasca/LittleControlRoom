package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"lcroom/internal/llm"
)

type CommitTodoCompletionInput struct {
	ProjectName   string          `json:"project_name"`
	Branch        string          `json:"branch,omitempty"`
	BaseHash      string          `json:"base_hash,omitempty"`
	HeadHash      string          `json:"head_hash"`
	CommitSubject string          `json:"commit_subject,omitempty"`
	ChangedFiles  []string        `json:"changed_files,omitempty"`
	DiffStat      string          `json:"diff_stat,omitempty"`
	Patch         string          `json:"patch,omitempty"`
	OpenTodos     []CommitTodoRef `json:"open_todos"`
}

type CommitTodoCompletionDecision struct {
	ID         int64
	Reason     string
	Confidence float64
}

type CommitTodoCompletionSuggestion struct {
	CompletedTodos []CommitTodoCompletionDecision
	Model          string
}

type CommitTodoCompletionChecker interface {
	CheckCompletedTodos(context.Context, CommitTodoCompletionInput) (CommitTodoCompletionSuggestion, error)
	ModelName() string
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
	args := commitRangeDiffArgs(path, baseHash, headHash, "--unified=0", "--no-color", "--find-renames")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read git commit range patch for %s..%s in %s: %w", baseHash, headHash, path, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if maxBytes > 0 && len(trimmed) > maxBytes {
		trimmed = trimmed[:maxBytes]
	}
	return trimmed, nil
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

Use only the supplied commit subject, changed files, diff stat, patch, and open_todos.
Do not use keyword matching; reason from the actual change evidence.
A TODO is complete only when the commit directly and substantially satisfies it, with no obvious implementation follow-up remaining.
Do not mark broad, vague, or merely related TODOs complete.
Do not infer completion from the branch name or commit subject alone when the diff evidence is weak.
Return only TODOs whose completion confidence is at least 0.75.
Return an empty completed_todos array when no TODO is clearly completed.
Return only the JSON fields requested by the schema.`

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
