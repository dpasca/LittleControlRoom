package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/llm"
)

const (
	scratchTaskTitleStateTemporary   = "temporary"
	scratchTaskTitleStateProvisional = "provisional"
	scratchTaskTitleStateAccepted    = "accepted"
	scratchTaskTitleStateManual      = "manual"

	scratchTaskTitleQualityNone   = "none"
	scratchTaskTitleQualityLow    = "low"
	scratchTaskTitleQualityMedium = "medium"
	scratchTaskTitleQualityHigh   = "high"

	scratchTaskAcceptedConfidence          = 0.75
	scratchTaskProvisionalConfidence       = 0.55
	scratchTaskGeneratedTitleLimit         = 80
	scratchTaskTitleLocalRunnerModel       = "gpt-5.4-mini"
	scratchTaskTitleClaudeLocalRunnerModel = "haiku"
	scratchTaskTitlePromptHistoryLimit     = 6
	scratchTaskTitlePromptRuneLimit        = 2000
	scratchTaskTitleAssessmentAttempts     = 2
	scratchTaskTitleAssessmentRetryDelay   = 250 * time.Millisecond
)

type ScratchTaskTitleAssessmentInput struct {
	ProjectPath       string   `json:"project_path"`
	CurrentTitle      string   `json:"current_title"`
	CurrentTitleState string   `json:"current_title_state"`
	UserPromptHistory []string `json:"user_prompt_history"`
	LatestUserPrompt  string   `json:"latest_user_prompt"`
}

type ScratchTaskTitleAssessment struct {
	CandidateTitle string  `json:"candidate_title"`
	Quality        string  `json:"quality"`
	Confidence     float64 `json:"confidence"`
	Adopt          bool    `json:"adopt"`
	KeepWatching   bool    `json:"keep_watching"`
	Reason         string  `json:"reason"`
	Model          string  `json:"-"`
}

type ScratchTaskTitleAssessor interface {
	AssessScratchTaskTitle(ctx context.Context, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error)
}

type scratchTaskTitleLLMAssessor struct {
	model           string
	runner          llm.JSONSchemaRunner
	reasoningEffort string
}

func newScratchTaskTitleLLMAssessor(model string, runner llm.JSONSchemaRunner, reasoningEffort string) ScratchTaskTitleAssessor {
	if runner == nil {
		return nil
	}
	return &scratchTaskTitleLLMAssessor{
		model:           strings.TrimSpace(model),
		runner:          runner,
		reasoningEffort: strings.TrimSpace(reasoningEffort),
	}
}

func (a *scratchTaskTitleLLMAssessor) AssessScratchTaskTitle(ctx context.Context, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error) {
	if a == nil || a.runner == nil {
		return ScratchTaskTitleAssessment{}, errors.New("scratch task title assessor not configured")
	}
	input.ProjectPath = strings.TrimSpace(input.ProjectPath)
	input.CurrentTitle = strings.TrimSpace(input.CurrentTitle)
	input.CurrentTitleState = normalizeScratchTaskTitleState(input.CurrentTitleState)
	input.UserPromptHistory = normalizeScratchTaskTitlePromptHistory(input.UserPromptHistory, input.LatestUserPrompt)
	if len(input.UserPromptHistory) > 0 {
		input.LatestUserPrompt = input.UserPromptHistory[len(input.UserPromptHistory)-1]
	} else {
		input.LatestUserPrompt = ""
	}
	if input.LatestUserPrompt == "" {
		return ScratchTaskTitleAssessment{}, errors.New("latest user prompt is required")
	}

	rawInput, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return ScratchTaskTitleAssessment{}, fmt.Errorf("marshal scratch task title input: %w", err)
	}
	response, err := a.runner.RunJSONSchema(ctx, llm.JSONSchemaRequest{
		Model:           a.model,
		SystemText:      scratchTaskTitleInstructions,
		UserText:        "Assess this scratch-task title opportunity:\n\n" + string(rawInput),
		SchemaName:      "scratch_task_title_assessment",
		Schema:          scratchTaskTitleAssessmentSchema(),
		ReasoningEffort: a.reasoningEffort,
	})
	if err != nil {
		return ScratchTaskTitleAssessment{}, err
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return ScratchTaskTitleAssessment{}, fmt.Errorf("scratch task title assessor returned no output")
	}
	var result ScratchTaskTitleAssessment
	if err := llm.DecodeJSONObjectOutput(response.OutputText, &result); err != nil {
		return ScratchTaskTitleAssessment{}, err
	}
	result.CandidateTitle = sanitizeScratchTaskCandidateTitle(result.CandidateTitle)
	result.Quality = normalizeScratchTaskTitleQuality(result.Quality)
	result.Reason = strings.TrimSpace(result.Reason)
	result.Model = strings.TrimSpace(response.Model)
	if result.Model == "" {
		result.Model = strings.TrimSpace(a.model)
	}
	if err := validateScratchTaskTitleAssessment(result); err != nil {
		return ScratchTaskTitleAssessment{}, err
	}
	return result, nil
}

const scratchTaskTitleInstructions = `You name scratch tasks for Little Control Room.

Decide whether the user prompt history contains enough semantic intent to produce a durable, dashboard-friendly task title. The history is chronological and the latest prompt is also provided separately. If the latest prompt is a vague follow-up or acknowledgement, use earlier prompts to recover the concrete task intent.

A high-quality title is concise, specific, and names the concrete work or investigation. It should not be a greeting, acknowledgement, vague continuation, or generic phrase. If the entire history is only social setup, meta-chatter, or otherwise lacks a concrete task subject, do not adopt a title yet and keep watching future prompts.

Do not use keyword or regex rules. Judge the actual meaning of the prompt and return only the structured JSON.`

func scratchTaskTitleAssessmentSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"candidate_title": map[string]any{
				"type":        "string",
				"description": "A concise title under 8 words when the prompt has enough task intent; empty when no durable title should be adopted.",
			},
			"quality": map[string]any{
				"type": "string",
				"enum": []string{
					scratchTaskTitleQualityNone,
					scratchTaskTitleQualityLow,
					scratchTaskTitleQualityMedium,
					scratchTaskTitleQualityHigh,
				},
			},
			"confidence": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     1,
				"description": "Confidence from 0 to 1 that candidate_title is a useful durable title for this task.",
			},
			"adopt": map[string]any{
				"type":        "boolean",
				"description": "True only when candidate_title should replace a temporary or provisional title.",
			},
			"keep_watching": map[string]any{
				"type":        "boolean",
				"description": "True when future prompts should still be considered for a better title.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Short explanation for debugging title quality decisions.",
			},
		},
		"required": []string{"candidate_title", "quality", "confidence", "adopt", "keep_watching", "reason"},
	}
}

func validateScratchTaskTitleAssessment(result ScratchTaskTitleAssessment) error {
	if result.Quality == "" {
		return errors.New("scratch task title quality is required")
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return fmt.Errorf("scratch task title confidence %.3f out of range", result.Confidence)
	}
	if result.Adopt && strings.TrimSpace(result.CandidateTitle) == "" {
		return errors.New("scratch task title assessor set adopt with an empty candidate title")
	}
	return nil
}

func sanitizeScratchTaskCandidateTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return ""
	}
	return truncateRunes(title, scratchTaskGeneratedTitleLimit)
}

func normalizeScratchTaskTitlePromptHistory(history []string, latest string) []string {
	normalized := make([]string, 0, len(history)+1)
	appendPrompt := func(prompt string) {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return
		}
		prompt = truncateRunes(prompt, scratchTaskTitlePromptRuneLimit)
		if len(normalized) > 0 && normalized[len(normalized)-1] == prompt {
			return
		}
		normalized = append(normalized, prompt)
	}
	for _, prompt := range history {
		appendPrompt(prompt)
	}
	appendPrompt(latest)
	if len(normalized) <= scratchTaskTitlePromptHistoryLimit {
		return normalized
	}
	trimmed := make([]string, 0, scratchTaskTitlePromptHistoryLimit)
	trimmed = append(trimmed, normalized[0])
	trimmed = append(trimmed, normalized[len(normalized)-(scratchTaskTitlePromptHistoryLimit-1):]...)
	return trimmed
}

func assessScratchTaskTitleWithRetry(ctx context.Context, assessor ScratchTaskTitleAssessor, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error) {
	if assessor == nil {
		return ScratchTaskTitleAssessment{}, errors.New("scratch task title assessor not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for attempt := 1; attempt <= scratchTaskTitleAssessmentAttempts; attempt++ {
		assessment, err := assessor.AssessScratchTaskTitle(ctx, input)
		if err == nil {
			return assessment, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == scratchTaskTitleAssessmentAttempts {
			break
		}
		timer := time.NewTimer(scratchTaskTitleAssessmentRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ScratchTaskTitleAssessment{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ScratchTaskTitleAssessment{}, lastErr
}

func normalizeScratchTaskTitleState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case scratchTaskTitleStateTemporary:
		return scratchTaskTitleStateTemporary
	case scratchTaskTitleStateProvisional:
		return scratchTaskTitleStateProvisional
	case scratchTaskTitleStateAccepted:
		return scratchTaskTitleStateAccepted
	case scratchTaskTitleStateManual:
		return scratchTaskTitleStateManual
	default:
		return ""
	}
}

func normalizeScratchTaskTitleQuality(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case scratchTaskTitleQualityNone:
		return scratchTaskTitleQualityNone
	case scratchTaskTitleQualityLow:
		return scratchTaskTitleQualityLow
	case scratchTaskTitleQualityMedium:
		return scratchTaskTitleQualityMedium
	case scratchTaskTitleQualityHigh:
		return scratchTaskTitleQualityHigh
	default:
		return ""
	}
}
