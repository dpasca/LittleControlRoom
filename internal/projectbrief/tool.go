package projectbrief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

const (
	ToolName        = "record_project_brief"
	ToolDescription = "Directors and other sessions use this at the end of a turn that changes project state to replace this managed checkout's brief. Write in plain words. Skip small turns. LCR supplies the date, checkout and provider."
	maxText         = 320
	maxOptions      = 5
)

func ToolSchema() map[string]any {
	shortText := map[string]any{"type": "string", "minLength": 1, "maxLength": maxText}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"goal", "now", "needs", "next"},
		"properties": map[string]any{
			"goal": shortText, "now": shortText, "next": shortText,
			"needs": map[string]any{
				"type": []string{"object", "null"}, "additionalProperties": false,
				"description": "The owner's decision, or null. Use an empty deadline if none applies.",
				"required":    []string{"question", "options", "recommendation", "default", "deadline"},
				"properties": map[string]any{
					"question": shortText, "recommendation": shortText, "default": shortText,
					"options":  map[string]any{"type": "array", "minItems": 2, "maxItems": maxOptions, "items": shortText},
					"deadline": map[string]any{"type": "string", "description": "RFC 3339 timestamp or an empty string"},
					"check": map[string]any{
						"type": "object", "additionalProperties": false, "required": []string{"kind"},
						"description": "Only use when this exact Git fact resolves the entire owner decision. Checks refer to this checkout's current HEAD or branch.",
						"properties": map[string]any{
							"kind":          map[string]any{"type": "string", "enum": []string{"pushed", "merged", "branch_gone"}},
							"target_branch": map[string]any{"type": "string", "description": "Exact local destination branch, required only for merged."},
							"remote_branch": map[string]any{"type": "string", "description": "Exact remote-tracking destination, such as origin/feature, required only for pushed."},
						},
					},
				},
			},
		},
	}
}

type Writer struct {
	Store        *store.Store
	CheckoutPath string
	Provider     model.SessionSource
	SessionKey   string
}

func (w Writer) Record(ctx context.Context, input model.ProjectBriefText) (map[string]any, error) {
	reportedAt := time.Now().UTC()
	if w.Store == nil || w.SessionKey == "" || w.Provider == model.SessionSourceUnknown {
		return nil, errors.New("project briefs require a managed project session")
	}
	if err := validateText(&input); err != nil {
		return nil, err
	}
	project, err := w.Store.GetProjectSummary(ctx, w.CheckoutPath, true)
	if err != nil {
		return nil, fmt.Errorf("find the brief's checkout: %w", err)
	}
	sessionID, err := w.Store.ResolveEngineerSession(ctx, project.Path, w.Provider, w.SessionKey)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.New("LCR has not observed this managed session's identity yet")
	}

	family := familyPath(project)
	gitCtx, cancel := context.WithTimeout(ctx, gitCheckTimeout)
	defer cancel()
	baseline := readGitState(gitCtx, project.Path, family)
	var answered *bool
	if input.Needs != nil && input.Needs.Check != nil {
		check := input.Needs.Check
		if baseline == nil || baseline.Head == "(initial)" || baseline.Head == "" {
			return nil, errors.New("needs.check requires a checkout with a recorded Git HEAD")
		}
		if check.Kind == "branch_gone" && baseline.Branch == "(detached)" {
			return nil, errors.New("branch_gone requires a checked-out local branch")
		}
		if ref := decisionRef(*baseline, *check); ref != "" {
			if _, err := gitOutput(gitCtx, project.Path, "check-ref-format", ref); err != nil {
				return nil, errors.New("needs.check requires a valid Git ref")
			}
		}
		if answer, err := checkDecision(gitCtx, project.Path, *baseline, *check); err == nil {
			value := answer != ""
			answered = &value
		}
	}
	if _, replaced, err := w.Store.EngineerSessionSupersededByKey(ctx, project.Path, w.Provider, w.SessionKey); err != nil {
		return nil, err
	} else if replaced {
		return nil, errors.New("this session was replaced through /handoff")
	}

	payload, err := json.Marshal(savedBrief{Text: input, Provider: w.Provider, SessionID: sessionID, Family: family, Git: baseline, Answered: answered})
	if err != nil {
		return nil, err
	}
	saved, err := w.Store.UpsertProjectBrief(ctx, store.ProjectBriefRow{CheckoutPath: project.Path, RecordedAt: reportedAt, Payload: payload})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "saved": saved, "checkout_path": project.Path, "recorded_at": reportedAt}, nil
}

func validateText(input *model.ProjectBriefText) error {
	if err := normalizeShortText(&input.Goal, &input.Now, &input.Next); err != nil {
		return fmt.Errorf("goal, now and next: %w", err)
	}
	needs := input.Needs
	if needs == nil {
		return nil
	}
	if err := normalizeShortText(&needs.Question, &needs.Recommendation, &needs.Default); err != nil {
		return fmt.Errorf("needs question, recommendation and default: %w", err)
	}
	if len(needs.Options) < 2 || len(needs.Options) > maxOptions {
		return fmt.Errorf("needs.options must contain 2 to %d options", maxOptions)
	}
	for i := range needs.Options {
		if err := normalizeShortText(&needs.Options[i]); err != nil {
			return fmt.Errorf("needs.options: %w", err)
		}
	}
	if needs.Deadline != "" {
		if _, err := time.Parse(time.RFC3339, needs.Deadline); err != nil {
			return errors.New("needs.deadline must be an RFC 3339 timestamp or an empty string")
		}
	}
	if check := needs.Check; check != nil {
		switch check.Kind {
		case "pushed":
			remote, branch, found := strings.Cut(check.RemoteBranch, "/")
			if check.TargetBranch != "" || !found || remote == "" || branch == "" || utf8.RuneCountInString(check.RemoteBranch) > maxText {
				return errors.New("pushed requires only remote_branch, such as origin/feature")
			}
		case "merged":
			if check.RemoteBranch != "" || check.TargetBranch == "" || utf8.RuneCountInString(check.TargetBranch) > maxText {
				return errors.New("merged requires only target_branch")
			}
		case "branch_gone":
			if check.RemoteBranch != "" || check.TargetBranch != "" {
				return errors.New("branch_gone uses the checkout's branch")
			}
		default:
			return errors.New("needs.check.kind must be pushed, merged or branch_gone")
		}
	}
	return nil
}

func normalizeShortText(fields ...*string) error {
	for _, field := range fields {
		*field = strings.TrimSpace(*field)
		if *field == "" || utf8.RuneCountInString(*field) > maxText || strings.ContainsAny(*field, "\r\n") {
			return fmt.Errorf("each field must be one line of 1 to %d characters", maxText)
		}
	}
	return nil
}
