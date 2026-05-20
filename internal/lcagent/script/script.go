package script

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
)

type Runner struct {
	Session      *session.Writer
	Command      tools.CommandRunner
	Patch        tools.PatchApplier
	Files        tools.FileTools
	WebSearch    tools.WebSearchRunner
	WebSearchOn  bool
	Skills       skillcatalog.Catalog
	SessionID    string
	Prompt       string
	ArtifactsDir string

	verificationChecks []tools.VerificationCheck
	filesTouched       []string
}

type VerificationFeedback struct {
	Status  string `json:"status"`
	Command string `json:"command,omitempty"`
	Message string `json:"message"`
}

type PatchFeedback struct {
	Stage          string                 `json:"stage"`
	Path           string                 `json:"path,omitempty"`
	Message        string                 `json:"message"`
	SuggestedReads []tools.ReadSuggestion `json:"suggested_reads,omitempty"`
}

type Action struct {
	Type         string          `json:"type"`
	Tool         string          `json:"tool,omitempty"`
	Args         json.RawMessage `json:"args,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	FilesChanged []string        `json:"files_changed,omitempty"`
	Verification []string        `json:"verification,omitempty"`
}

type replaceTextArgs struct {
	Path                 string `json:"path"`
	OldText              string `json:"old_text"`
	NewText              string `json:"new_text"`
	ExpectedReplacements int    `json:"expected_replacements,omitempty"`
}

func VerificationFeedbackForResult(result tools.ToolResult) (VerificationFeedback, bool) {
	if !strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) || result.Success {
		return VerificationFeedback{}, false
	}
	check := tools.VerificationCheckFromResult(result)
	command := firstNonEmpty(check.Command, strings.Join(check.Argv, " "), "verification check")
	status := firstNonEmpty(check.Status, "failed")
	var next string
	switch status {
	case tools.VerificationStatusDenied:
		next = "Choose an approved argv-only verification command, or explain clearly why verification is blocked."
	case tools.VerificationStatusTimedOut:
		next = "Narrow the verification command, inspect the timeout, or explain clearly why verification is blocked."
	default:
		next = "Inspect the failure, fix the issue if it is caused by your changes, and rerun a purpose=verify check."
	}
	detail := firstNonEmpty(check.Error, result.Error)
	message := fmt.Sprintf("Verification feedback: %s %s. %s", command, verificationStatusPhrase(status), next)
	if detail != "" {
		message += " Error: " + detail
	}
	return VerificationFeedback{Status: status, Command: command, Message: message}, true
}

func verificationStatusPhrase(status string) string {
	switch status {
	case tools.VerificationStatusDenied:
		return "was denied"
	case tools.VerificationStatusTimedOut:
		return "timed out"
	case tools.VerificationStatusPassed:
		return "passed"
	case tools.VerificationStatusFailed:
		return "failed"
	default:
		return "finished with status " + status
	}
}

func (r *Runner) VerificationFeedbackForFinal(action Action) (VerificationFeedback, bool) {
	action.FilesChanged = cleanStringList(action.FilesChanged)
	action.Verification = cleanStringList(action.Verification)
	if len(action.FilesChanged) == 0 || len(r.verificationChecks) > 0 {
		return VerificationFeedback{}, false
	}
	status, _ := finalVerificationStatus(action.FilesChanged, action.Verification, r.verificationChecks)
	if status != "missing_after_changes" && status != "reported_only" {
		return VerificationFeedback{}, false
	}
	message := "Verification feedback: final_response listed changed files, but no run_command check marked purpose=verify has run. Run an appropriate verification command, or explain clearly why verification is blocked, then call final_response again."
	if status == "reported_only" {
		message = "Verification feedback: final_response reported verification, but no run_command check marked purpose=verify has run. Run an appropriate verification command, or explain clearly why verification is blocked, then call final_response again."
	}
	return VerificationFeedback{Status: status, Message: message}, true
}

func (f VerificationFeedback) ModelMessage() string {
	return strings.TrimSpace(f.Message)
}

func (r *Runner) WriteVerificationFeedback(feedback VerificationFeedback) error {
	return r.Session.Write(verificationFeedbackEvent(r.SessionID, feedback))
}

func PatchFeedbackForResult(result tools.ToolResult) (PatchFeedback, bool) {
	if result.Success || result.PatchFailure == nil {
		return PatchFeedback{}, false
	}
	failure := result.PatchFailure
	target := firstNonEmpty(failure.Path, "patch")
	message := "Patch feedback: " + target + " failed during " + firstNonEmpty(failure.Stage, "apply_patch") + ": " + firstNonEmpty(failure.Message, result.Error)
	if hint := strings.TrimSpace(failure.Hint); hint != "" {
		message += ". " + hint
	}
	return PatchFeedback{
		Stage:          failure.Stage,
		Path:           failure.Path,
		Message:        message,
		SuggestedReads: append([]tools.ReadSuggestion(nil), failure.SuggestedReads...),
	}, true
}

func (f PatchFeedback) ModelMessage() string {
	return strings.TrimSpace(f.Message)
}

func PatchRetryGuidance(feedback PatchFeedback, repeatCount int) string {
	message := strings.TrimSpace(feedback.Message)
	if message == "" || repeatCount < 2 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Patch retry guidance: the same patch feedback has repeated %d times. Stop retrying the same patch unchanged.", repeatCount)
	if len(feedback.SuggestedReads) > 0 {
		b.WriteString(" First call ")
		b.WriteString(formatReadSuggestionsForModel(feedback.SuggestedReads))
		b.WriteString(" to refresh exact current lines.")
	} else {
		b.WriteString(" Re-read the affected file before another patch attempt.")
	}
	b.WriteString(" Then retry with a smaller hunk that preserves current unchanged context, or use replace_text with exact old_text copied from the current file for a small literal edit.")
	return b.String()
}

func (r *Runner) WritePatchFeedback(feedback PatchFeedback) error {
	return r.Session.Write(patchFeedbackEvent(r.SessionID, feedback))
}

func formatReadSuggestionsForModel(suggestions []tools.ReadSuggestion) string {
	calls := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if strings.TrimSpace(suggestion.Path) == "" || suggestion.Offset <= 0 || suggestion.Limit <= 0 {
			continue
		}
		calls = append(calls, fmt.Sprintf(`read_file {"path":%q,"offset":%d,"limit":%d}`, suggestion.Path, suggestion.Offset, suggestion.Limit))
		if len(calls) >= 2 {
			break
		}
	}
	if len(calls) == 0 {
		return "read_file on the affected range"
	}
	return strings.Join(calls, " and ")
}

type commandArgs struct {
	Command   string   `json:"command"`
	Argv      []string `json:"argv"`
	Shell     bool     `json:"shell"`
	TimeoutMS int      `json:"timeout_ms"`
	Purpose   string   `json:"purpose"`
}

type patchArgs struct {
	Patch string `json:"patch"`
}

type planArgs struct {
	Items []tools.PlanItem `json:"items"`
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

type listFilesArgs struct {
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	MaxEntries    int    `json:"max_entries"`
	IncludeHidden bool   `json:"include_hidden"`
}

type repoOverviewArgs struct {
	Path          string `json:"path"`
	MaxFiles      int    `json:"max_files"`
	IncludeHidden bool   `json:"include_hidden"`
}

type searchArgs struct {
	Query         string `json:"query"`
	Path          string `json:"path"`
	FileGlob      string `json:"file_glob"`
	MaxMatches    int    `json:"max_matches"`
	ContextBefore *int   `json:"context_before"`
	ContextAfter  *int   `json:"context_after"`
	IncludeHidden bool   `json:"include_hidden"`
}

type webSearchArgs struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	Site        string `json:"site"`
	RecencyDays int    `json:"recency_days"`
}

type fileOutlineArgs struct {
	Path string `json:"path"`
}

type moduleOutlineArgs struct {
	Path          string `json:"path"`
	FileGlob      string `json:"file_glob"`
	MaxFiles      int    `json:"max_files"`
	IncludeHidden bool   `json:"include_hidden"`
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

func Load(path string) ([]Action, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var actions []Action
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var action Action
		if err := json.Unmarshal(line, &action); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return actions, nil
}

func (r *Runner) Run(ctx context.Context, actions []Action) error {
	if err := r.Session.Write(session.Event{
		"type":       "user_message",
		"session_id": r.SessionID,
		"message":    r.Prompt,
	}); err != nil {
		return err
	}
	for _, action := range actions {
		switch action.Type {
		case "tool_call":
			result, err := r.RunTool(ctx, action)
			if err != nil {
				if feedback, ok := PatchFeedbackForResult(result); ok {
					_ = r.WritePatchFeedback(feedback)
				}
				if feedback, ok := VerificationFeedbackForResult(result); ok {
					_ = r.WriteVerificationFeedback(feedback)
				}
				_ = r.Session.Write(session.Event{
					"type":       "turn_aborted",
					"session_id": r.SessionID,
					"reason":     err.Error(),
				})
				return err
			}
		case "final_response":
			return r.Final(action)
		default:
			err := fmt.Errorf("unsupported script action type: %s", action.Type)
			_ = r.Session.Write(session.Event{
				"type":       "turn_aborted",
				"session_id": r.SessionID,
				"reason":     err.Error(),
			})
			return err
		}
	}
	err := fmt.Errorf("script ended without final_response")
	_ = r.Session.Write(session.Event{
		"type":       "turn_aborted",
		"session_id": r.SessionID,
		"reason":     err.Error(),
	})
	return err
}

func (r *Runner) RunTool(ctx context.Context, action Action) (tools.ToolResult, error) {
	if err := r.Session.Write(session.Event{
		"type":       "tool_call",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"args":       json.RawMessage(action.Args),
	}); err != nil {
		return tools.ToolResult{}, err
	}

	var result tools.ToolResult
	switch action.Tool {
	case "read_file":
		var args readFileArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.Read(args.Path, args.Offset, args.Limit)
	case "list_files":
		var args listFilesArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.ListWithOptions(args.Path, args.Glob, args.MaxEntries, tools.ListOptions{IncludeHidden: args.IncludeHidden})
	case "repo_overview":
		var args repoOverviewArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.RepoOverview(args.Path, tools.RepoOverviewOptions{MaxFiles: args.MaxFiles, IncludeHidden: args.IncludeHidden})
	case "search":
		var args searchArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		contextBefore := 1
		contextAfter := 2
		if args.ContextBefore != nil {
			contextBefore = *args.ContextBefore
		}
		if args.ContextAfter != nil {
			contextAfter = *args.ContextAfter
		}
		result = r.Files.SearchContextWithOptions(args.Query, args.Path, args.FileGlob, args.MaxMatches, contextBefore, contextAfter, tools.SearchOptions{IncludeHidden: args.IncludeHidden})
	case "web_search":
		var args webSearchArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		if !r.WebSearchOn {
			result = tools.ToolResult{Success: false, Error: "web_search is not configured for this LCAgent run"}
			break
		}
		result = r.WebSearch.Search(ctx, args.Query, args.MaxResults, args.Site, args.RecencyDays)
	case "file_outline":
		var args fileOutlineArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.Outline(args.Path)
	case "module_outline":
		var args moduleOutlineArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Files.ModuleOutlineWithOptions(args.Path, args.FileGlob, args.MaxFiles, tools.ModuleOutlineOptions{IncludeHidden: args.IncludeHidden})
	case "load_skill":
		var args loadSkillArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		loaded, err := r.Skills.Load(args.Name)
		if err != nil {
			result = tools.ToolResult{Success: false, Error: err.Error()}
			break
		}
		result = tools.ToolResult{Success: true, Output: formatLoadedSkill(loaded), Truncated: loaded.Truncated}
		if err := r.Session.Write(session.Event{
			"type":        "skill_loaded",
			"session_id":  r.SessionID,
			"name":        loaded.Skill.Name,
			"source":      loaded.Skill.Source,
			"path":        loaded.Skill.Path,
			"description": loaded.Skill.Description,
			"truncated":   loaded.Truncated,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	case "run_command":
		var args commandArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Command.RunSpec(ctx, tools.CommandSpec{
			Command:   args.Command,
			Argv:      args.Argv,
			Shell:     args.Shell || args.Command != "",
			TimeoutMS: args.TimeoutMS,
			Purpose:   args.Purpose,
		})
		if strings.EqualFold(result.Purpose, tools.CommandPurposeVerify) {
			check := tools.VerificationCheckFromResult(result)
			r.verificationChecks = append(r.verificationChecks, check)
			if err := r.Session.Write(verificationCheckEvent(r.SessionID, check)); err != nil {
				return tools.ToolResult{}, err
			}
		}
	case "apply_patch":
		var args patchArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = r.Patch.Apply(args.Patch)
	case "replace_text":
		var args replaceTextArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = tools.TextEditor{Workspace: r.Patch.Workspace}.ReplaceText(tools.ReplaceTextSpec{
			Path:                 args.Path,
			OldText:              args.OldText,
			NewText:              args.NewText,
			ExpectedReplacements: args.ExpectedReplacements,
		})
	case "update_plan":
		var args planArgs
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return tools.ToolResult{}, err
		}
		result = tools.ToolResult{Success: true, Output: "plan updated"}
		if err := r.Session.Write(session.Event{
			"type":       "plan_update",
			"session_id": r.SessionID,
			"items":      args.Items,
		}); err != nil {
			return tools.ToolResult{}, err
		}
	default:
		result = tools.ToolResult{Success: false, Error: "unsupported tool: " + action.Tool}
	}

	if action.Tool == "apply_patch" || action.Tool == "replace_text" {
		if len(result.FilesTouched) > 0 {
			r.filesTouched = appendCleanUniqueStrings(r.filesTouched, result.FilesTouched...)
			if err := r.Session.Write(session.Event{
				"type":       "files_touched",
				"session_id": r.SessionID,
				"files":      result.FilesTouched,
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
		if result.PatchSummary != nil || strings.TrimSpace(result.DiffSummary) != "" {
			if err := r.Session.Write(session.Event{
				"type":          "patch_diff_summary",
				"session_id":    r.SessionID,
				"files":         result.FilesTouched,
				"summary":       result.DiffSummary,
				"patch_summary": result.PatchSummary,
			}); err != nil {
				return tools.ToolResult{}, err
			}
		}
	}

	if result.Denied {
		if err := r.Session.Write(session.Event{
			"type":       "permission_denied",
			"session_id": r.SessionID,
			"tool":       action.Tool,
			"reason":     firstNonEmpty(result.DenialReason, result.Error),
		}); err != nil {
			return tools.ToolResult{}, err
		}
	}

	if err := r.Session.Write(session.Event{
		"type":       "tool_result",
		"session_id": r.SessionID,
		"tool":       action.Tool,
		"result":     result,
	}); err != nil {
		return tools.ToolResult{}, err
	}
	if !result.Success {
		return result, fmt.Errorf("%s failed: %s", action.Tool, result.Error)
	}
	return result, nil
}

func (r *Runner) FilesTouched() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.filesTouched...)
}

func (r *Runner) VerificationDetails() []string {
	if r == nil || len(r.verificationChecks) == 0 {
		return nil
	}
	return formatVerificationChecks(r.verificationChecks, len(r.verificationChecks))
}

func formatLoadedSkill(loaded skillcatalog.LoadedSkill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s\n", loaded.Skill.Name)
	fmt.Fprintf(&b, "source: %s\n", loaded.Skill.Source)
	fmt.Fprintf(&b, "path: %s\n", loaded.Skill.Path)
	if loaded.Skill.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", loaded.Skill.Description)
	}
	b.WriteString("\n")
	b.WriteString(loaded.Body)
	if loaded.Truncated {
		b.WriteString("\n--- skill body truncated ---\n")
	}
	return b.String()
}

func (r *Runner) Final(action Action) error {
	action.FilesChanged = cleanStringList(action.FilesChanged)
	action.Verification = cleanStringList(action.Verification)
	verificationStatus, verificationMessage := finalVerificationStatus(action.FilesChanged, action.Verification, r.verificationChecks)
	if err := r.Session.Write(session.Event{
		"type":                "verification_summary",
		"session_id":          r.SessionID,
		"status":              verificationStatus,
		"message":             verificationMessage,
		"files_changed":       action.FilesChanged,
		"verification_checks": action.Verification,
		"actual_checks":       append([]tools.VerificationCheck(nil), r.verificationChecks...),
	}); err != nil {
		return err
	}
	if err := r.Session.Write(session.Event{
		"type":          "assistant_message",
		"session_id":    r.SessionID,
		"message":       action.Summary,
		"files_changed": action.FilesChanged,
		"verification":  action.Verification,
	}); err != nil {
		return err
	}
	return r.Session.Write(session.Event{
		"type":                "turn_complete",
		"session_id":          r.SessionID,
		"summary":             action.Summary,
		"files_changed":       action.FilesChanged,
		"verification":        action.Verification,
		"verification_status": verificationStatus,
		"actual_checks":       append([]tools.VerificationCheck(nil), r.verificationChecks...),
	})
}

func verificationCheckEvent(sessionID string, check tools.VerificationCheck) session.Event {
	return session.Event{
		"type":       "verification_check",
		"session_id": sessionID,
		"command":    check.Command,
		"argv":       check.Argv,
		"purpose":    check.Purpose,
		"status":     check.Status,
		"success":    check.Success,
		"exit_code":  check.ExitCode,
		"duration":   check.Duration,
		"timed_out":  check.TimedOut,
		"denied":     check.Denied,
		"error":      check.Error,
	}
}

func verificationFeedbackEvent(sessionID string, feedback VerificationFeedback) session.Event {
	return session.Event{
		"type":       "verification_feedback",
		"session_id": sessionID,
		"status":     feedback.Status,
		"command":    feedback.Command,
		"message":    feedback.Message,
	}
}

func patchFeedbackEvent(sessionID string, feedback PatchFeedback) session.Event {
	event := session.Event{
		"type":       "patch_feedback",
		"session_id": sessionID,
		"stage":      feedback.Stage,
		"path":       feedback.Path,
		"message":    feedback.Message,
	}
	if len(feedback.SuggestedReads) > 0 {
		event["suggested_reads"] = feedback.SuggestedReads
	}
	return event
}

func finalVerificationStatus(filesChanged, verification []string, actualChecks []tools.VerificationCheck) (string, string) {
	if len(actualChecks) > 0 {
		finalChecks := latestVerificationOutcomes(relevantVerificationChecks(verification, actualChecks))
		failed := failedVerificationChecks(finalChecks)
		if len(failed) > 0 {
			return "failed", "Verification checks failed: " + strings.Join(formatVerificationChecks(failed, 3), "; ")
		}
		message := "Verification checks passed: " + strings.Join(formatVerificationChecks(finalChecks, 3), "; ")
		if len(verification) == 0 {
			message += ". final_response did not list verification details."
		}
		return "verified", message
	}
	if len(verification) > 0 {
		return "reported_only", "Verification was reported in final_response, but no run_command check was marked with purpose=verify."
	}
	if len(filesChanged) > 0 {
		return "missing_after_changes", "No verification check was run or reported for changed files."
	}
	return "not_run", "No verification check was run."
}

func relevantVerificationChecks(reported []string, actual []tools.VerificationCheck) []tools.VerificationCheck {
	if len(reported) == 0 {
		return actual
	}
	relevant := make([]tools.VerificationCheck, 0, len(actual))
	for _, check := range actual {
		if verificationReportsCheck(reported, check) {
			relevant = append(relevant, check)
		}
	}
	if len(relevant) == 0 {
		return actual
	}
	return relevant
}

func verificationReportsCheck(reported []string, check tools.VerificationCheck) bool {
	label := strings.ToLower(verificationCheckLabel(check))
	if label == "" {
		return false
	}
	for _, item := range reported {
		if strings.Contains(strings.ToLower(item), label) {
			return true
		}
	}
	return false
}

func latestVerificationOutcomes(checks []tools.VerificationCheck) []tools.VerificationCheck {
	out := make([]tools.VerificationCheck, 0, len(checks))
	indexByLabel := map[string]int{}
	for _, check := range checks {
		label := verificationCheckLabel(check)
		if label == "" {
			label = "verification check"
		}
		if index, ok := indexByLabel[label]; ok {
			out[index] = check
			continue
		}
		indexByLabel[label] = len(out)
		out = append(out, check)
	}
	return out
}

func failedVerificationChecks(checks []tools.VerificationCheck) []tools.VerificationCheck {
	var failed []tools.VerificationCheck
	for _, check := range checks {
		if check.Status != tools.VerificationStatusPassed {
			failed = append(failed, check)
		}
	}
	return failed
}

func formatVerificationChecks(checks []tools.VerificationCheck, limit int) []string {
	if limit <= 0 || limit > len(checks) {
		limit = len(checks)
	}
	out := make([]string, 0, limit+1)
	for _, check := range checks[:limit] {
		label := firstNonEmpty(verificationCheckLabel(check), "verification check")
		if check.Status != "" && check.Status != tools.VerificationStatusPassed {
			label += " (" + check.Status + ")"
		}
		out = append(out, label)
	}
	if len(checks) > limit {
		out = append(out, fmt.Sprintf("%d more", len(checks)-limit))
	}
	return out
}

func verificationCheckLabel(check tools.VerificationCheck) string {
	return firstNonEmpty(check.Command, strings.Join(check.Argv, " "))
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func appendCleanUniqueStrings(existing []string, values ...string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range cleanStringList(values) {
		if seen[value] {
			continue
		}
		existing = append(existing, value)
		seen[value] = true
	}
	return existing
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
