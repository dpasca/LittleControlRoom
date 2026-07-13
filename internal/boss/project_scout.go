package boss

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"lcroom/internal/lcagent"
)

func (e *QueryExecutor) projectScoutReport(ctx context.Context, action bossAction, view ViewContext) (bossToolResult, error) {
	path, resolution, err := e.resolveProjectPath(ctx, action, view)
	if err != nil {
		return bossToolResult{}, err
	}
	project, err := e.store.GetProjectSummary(ctx, path, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return unavailableProjectScoutToolResult(path, nil, errors.New("repository Scout only reads tracked projects; refresh project discovery or choose a tracked project")), nil
		}
		return bossToolResult{}, err
	}
	if bossProjectHiddenByPrivacy(project, view) {
		return bossToolResult{}, errors.New("project is hidden while privacy mode is enabled")
	}
	question := strings.TrimSpace(action.Query)
	if question == "" {
		return bossToolResult{}, errors.New("project_scout needs the user's repository question in query")
	}
	if e.projectScout == nil {
		return unavailableProjectScoutToolResult(path, nil, errors.New("repository Scout is not connected")), nil
	}
	result, scoutErr := e.projectScout.Scout(ctx, lcagent.ScoutRequest{
		WorkspaceRoot: path,
		Question:      question,
		DataDir:       e.dataDir,
	})
	if ctx.Err() != nil {
		return bossToolResult{}, ctx.Err()
	}
	if scoutErr != nil {
		toolResult := unavailableProjectScoutToolResult(path, result.Attempts, scoutErr)
		toolResult.Usage = result.Usage
		return toolResult, nil
	}
	return successfulProjectScoutToolResult(path, resolution, result), nil
}

func successfulProjectScoutToolResult(projectPath, resolution string, result lcagent.ScoutResult) bossToolResult {
	routeLabel := scoutRouteLabel(result)
	lines := []string{
		"Fresh read-only repository Scout result:",
		"- target: " + filepath.Base(projectPath),
		"- resolution: " + strings.TrimSpace(resolution),
		"- inference route: " + routeLabel,
	}
	if failed := failedScoutAttempts(result.Attempts); len(failed) > 0 {
		lines = append(lines, "- fallback: "+formatScoutAttemptFailures(failed))
	}
	if result.ArtifactPath != "" {
		lines = append(lines, "- durable trace: "+markdownLocalPath("LCAgent Scout trace", result.ArtifactPath, 0))
	}
	lines = append(lines, "", "Scout findings:", clipText(strings.TrimSpace(result.Summary), 4000))
	if len(result.Evidence) > 0 {
		lines = append(lines, "", "Mechanically recorded read evidence:")
		const evidenceLimit = 12
		for i, evidence := range result.Evidence {
			if i >= evidenceLimit {
				lines = append(lines, fmt.Sprintf("- ... %d more read ranges are preserved in the durable trace", len(result.Evidence)-i))
				break
			}
			line := "- " + markdownScoutEvidence(evidence)
			if evidence.EndLine > evidence.StartLine {
				line += fmt.Sprintf(" (read through line %d)", evidence.EndLine)
			}
			lines = append(lines, line)
		}
	} else if len(result.InspectionTools) > 0 {
		lines = append(lines, "", "Inspection evidence: successful "+strings.Join(result.InspectionTools, ", ")+" calls are recorded in the durable trace; no read_file line ranges were produced.")
	}
	lines = append(lines, "", "Evidence policy: use these fresh repository findings for the answer. Do not claim a file, plan, or implementation is absent unless the Scout findings and inspection evidence actually support that negative claim. The host appends the route/evidence receipt to the user-facing answer; do not duplicate it.")
	toolResult := clippedToolResultWithReceipt(bossActionProjectScout, strings.Join(lines, "\n"), projectScoutUserReceipt(result))
	toolResult.Usage = result.Usage
	return toolResult
}

func unavailableProjectScoutToolResult(projectPath string, attempts []lcagent.ScoutAttempt, scoutErr error) bossToolResult {
	lines := []string{
		"Repository inspection unavailable for " + filepath.Base(projectPath) + ".",
		"Reason: " + strings.TrimSpace(scoutErr.Error()),
	}
	if len(attempts) > 0 {
		lines = append(lines, "Route attempts:")
		for _, attempt := range attempts {
			line := fmt.Sprintf("- %s (%s/%s): %s", firstNonEmpty(strings.TrimSpace(attempt.Description), strings.TrimSpace(attempt.Source), "unnamed route"), emptyLabel(attempt.Provider), emptyLabel(attempt.Model), firstNonEmpty(strings.TrimSpace(attempt.Error), strings.TrimSpace(attempt.Status), "failed"))
			if attempt.ArtifactPath != "" {
				line += "; trace " + markdownLocalPath("artifact", attempt.ArtifactPath, 0)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "Do not infer that repository content is absent. Tell the user that file inspection was unavailable and answer only from other explicit evidence.")
	receipt := "_Repository inspection unavailable"
	if len(attempts) > 0 {
		receipt += fmt.Sprintf(" after %d inference route attempt(s)", len(attempts))
	}
	receipt += "; no absence claim should be inferred._"
	return clippedToolResultWithReceipt(bossActionProjectScout, strings.Join(lines, "\n"), receipt)
}

func projectScoutUserReceipt(result lcagent.ScoutResult) string {
	parts := []string{"Repository inspection: " + scoutRouteLabel(result)}
	if failed := failedScoutAttempts(result.Attempts); len(failed) > 0 {
		parts[0] = "Repository inspection fallback: " + scoutRouteLabel(result) + " after " + formatScoutAttemptFailures(failed)
	}
	if links := scoutEvidenceReceiptLinks(result.Evidence, 3); len(links) > 0 {
		parts = append(parts, "evidence "+strings.Join(links, ", "))
	}
	if result.ArtifactPath != "" {
		parts = append(parts, markdownLocalPath("trace", result.ArtifactPath, 0))
	}
	return "_" + strings.Join(parts, "; ") + "._"
}

func scoutRouteLabel(result lcagent.ScoutResult) string {
	description := firstNonEmpty(strings.TrimSpace(result.Route.Description), strings.TrimSpace(result.Route.Source), "automatic inherited route")
	provider := firstNonEmpty(strings.TrimSpace(result.ResolvedProvider), strings.TrimSpace(result.Route.Provider), "unknown provider")
	modelName := firstNonEmpty(strings.TrimSpace(result.ResolvedModel), strings.TrimSpace(result.Route.Model), "automatic model")
	return fmt.Sprintf("%s (%s/%s)", description, provider, modelName)
}

func failedScoutAttempts(attempts []lcagent.ScoutAttempt) []lcagent.ScoutAttempt {
	var failed []lcagent.ScoutAttempt
	for _, attempt := range attempts {
		if strings.EqualFold(strings.TrimSpace(attempt.Status), "failed") {
			failed = append(failed, attempt)
		}
	}
	return failed
}

func formatScoutAttemptFailures(attempts []lcagent.ScoutAttempt) string {
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		label := firstNonEmpty(strings.TrimSpace(attempt.Description), strings.TrimSpace(attempt.Source), "route")
		detail := firstNonEmpty(strings.TrimSpace(attempt.Error), "failed")
		parts = append(parts, label+" failed: "+clipText(detail, 160))
	}
	return strings.Join(parts, "; ")
}

func markdownScoutEvidence(evidence lcagent.ScoutEvidence) string {
	label := filepath.Base(evidence.Path)
	if label == "." || label == string(filepath.Separator) || label == "" {
		label = "repository file"
	}
	return markdownLocalPath(label, evidence.Path, evidence.StartLine)
}

func scoutEvidenceReceiptLinks(evidence []lcagent.ScoutEvidence, limit int) []string {
	if limit <= 0 {
		return nil
	}
	byPath := map[string]lcagent.ScoutEvidence{}
	for _, item := range evidence {
		path := filepath.Clean(strings.TrimSpace(item.Path))
		if path == "." || path == "" {
			continue
		}
		if current, ok := byPath[path]; !ok || item.StartLine < current.StartLine {
			byPath[path] = item
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	links := make([]string, 0, len(paths))
	for _, path := range paths {
		links = append(links, markdownScoutEvidence(byPath[path]))
	}
	return links
}

func markdownLocalPath(label, path string, line int) string {
	label = strings.NewReplacer("[", "\\[", "]", "\\]").Replace(strings.TrimSpace(label))
	if label == "" {
		label = "local file"
	}
	target := filepath.Clean(strings.TrimSpace(path))
	if line > 0 {
		target += fmt.Sprintf(":%d", line)
	}
	if strings.ContainsAny(target, " ()") {
		target = "<" + target + ">"
	}
	return "[" + label + "](" + target + ")"
}

func clippedToolResultWithReceipt(name, text, receipt string) bossToolResult {
	result := clippedToolResult(name, text)
	result.UserReceipt = strings.TrimSpace(receipt)
	return result
}
