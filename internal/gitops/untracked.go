package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/llm"
)

type UntrackedFileCandidate struct {
	Path             string   `json:"path"`
	Kind             string   `json:"kind"`
	ByteSize         int64    `json:"byte_size,omitempty"`
	Binary           bool     `json:"binary,omitempty"`
	Preview          string   `json:"preview,omitempty"`
	PreviewTruncated bool     `json:"preview_truncated,omitempty"`
	SampleEntries    []string `json:"sample_entries,omitempty"`
}

type UntrackedFileRecommendationInput struct {
	ProjectName          string                   `json:"project_name"`
	Branch               string                   `json:"branch,omitempty"`
	LatestSessionSummary string                   `json:"latest_session_summary,omitempty"`
	StagedFiles          []string                 `json:"staged_files"`
	StagedDiffStat       string                   `json:"staged_diff_stat,omitempty"`
	StagedPatch          string                   `json:"staged_patch,omitempty"`
	Candidates           []UntrackedFileCandidate `json:"candidates"`
}

type UntrackedFileDecision struct {
	Path       string  `json:"path"`
	Include    bool    `json:"include"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type UntrackedFileRecommendationResult struct {
	Files []UntrackedFileDecision
	Model string
}

type UntrackedFileRecommender interface {
	RecommendUntracked(context.Context, UntrackedFileRecommendationInput) (UntrackedFileRecommendationResult, error)
	ModelName() string
}

func (c *OpenAICommitMessageClient) RecommendUntracked(ctx context.Context, input UntrackedFileRecommendationInput) (UntrackedFileRecommendationResult, error) {
	if c == nil || c.responsesClient() == nil {
		return UntrackedFileRecommendationResult{}, errors.New(errCommitAssistantNotConfigured)
	}
	if strings.TrimSpace(input.ProjectName) == "" && strings.TrimSpace(input.Branch) != "" {
		input.ProjectName = filepath.Base(strings.TrimSpace(input.Branch))
	}

	payload, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return UntrackedFileRecommendationResult{}, fmt.Errorf("marshal untracked recommendation input: %w", err)
	}

	response, err := c.runJSONSchemaPrompt(
		ctx,
		untrackedRecommendationInstructions,
		"Review these untracked file candidates for a proposed git commit:\n\n"+string(payload),
		"git_untracked_review",
		map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"files": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"path": map[string]any{
								"type": "string",
							},
							"include": map[string]any{
								"type": "boolean",
							},
							"confidence": map[string]any{
								"type":    "number",
								"minimum": 0,
								"maximum": 1,
							},
							"reason": map[string]any{
								"type":        "string",
								"description": "One concise sentence explaining the decision.",
							},
						},
						"required": []string{"path", "include", "confidence", "reason"},
					},
				},
			},
			"required": []string{"files"},
		},
	)
	if err != nil {
		return UntrackedFileRecommendationResult{}, err
	}

	var decoded struct {
		Files []UntrackedFileDecision `json:"files"`
	}
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &decoded); err != nil {
		return UntrackedFileRecommendationResult{}, fmt.Errorf("decode untracked recommendation result: %w", err)
	}
	for i := range decoded.Files {
		decoded.Files[i].Path = strings.TrimSpace(decoded.Files[i].Path)
		decoded.Files[i].Reason = strings.TrimSpace(decoded.Files[i].Reason)
	}
	return UntrackedFileRecommendationResult{
		Files: decoded.Files,
		Model: response.Model,
	}, nil
}

const untrackedRecommendationInstructions = `You review untracked files for a proposed git commit.

Decide whether each untracked candidate clearly belongs in the same commit as the currently staged changes and latest session summary.
Be conservative and prefer exclusion when uncertain.
Only include files that are strongly supported by the staged changes, project context, or latest session summary.
Do not include files that appear to contain passwords, API keys, secrets, tokens, credentials, private dumps, or copied production data.
Avoid including scratch notes, exports, logs, screenshots, backups, generated build output, or unrelated experiments unless the context strongly shows they are part of the intended change.
Directory candidates may include sample entries instead of full file contents; only include them when those entries clearly belong with the staged work.
Return one decision for every candidate path.
Return only the JSON requested by the schema.`
