package lcagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/tools"
	"lcroom/internal/model"
)

const (
	scoutMaxTurns              = 4
	scoutDefaultRequestTimeout = 90 * time.Second
	scoutEventMaxBytes         = 16 * 1024 * 1024
)

// ScoutRoute describes only inference. The read-only behavior and tool budget
// belong to the Scout profile, so callers can inherit an existing inference
// setup without silently inheriting LCAgent's coding autonomy.
type ScoutRoute struct {
	Source          string
	Description     string
	Provider        string
	Model           string
	APIKey          string
	BaseURL         string
	EnvFile         string
	ReasoningEffort string
	RequestTimeout  time.Duration
}

type ScoutRequest struct {
	WorkspaceRoot string
	Question      string
	DataDir       string
}

type ScoutEvidence struct {
	Path       string
	StartLine  int
	EndLine    int
	TotalLines int
}

type ScoutAttempt struct {
	Source       string
	Description  string
	Provider     string
	Model        string
	Status       string
	Error        string
	SessionID    string
	ArtifactPath string
}

type ScoutResult struct {
	Summary          string
	Outcome          string
	Route            ScoutRoute
	ResolvedProvider string
	ResolvedModel    string
	SessionID        string
	ArtifactPath     string
	Evidence         []ScoutEvidence
	InspectionTools  []string
	Usage            model.LLMUsage
	Attempts         []ScoutAttempt
}

// ScoutUnavailableError retains safe route-attempt receipts so Chat can
// explain why repository inspection was unavailable without exposing secrets.
type ScoutUnavailableError struct {
	Attempts []ScoutAttempt
}

func (e *ScoutUnavailableError) Error() string {
	if e == nil || len(e.Attempts) == 0 {
		return "repository Scout has no compatible inference route"
	}
	last := e.Attempts[len(e.Attempts)-1]
	detail := strings.TrimSpace(last.Error)
	if detail == "" {
		detail = "route did not return grounded repository evidence"
	}
	return fmt.Sprintf("repository Scout could not complete after %d route attempt(s): %s", len(e.Attempts), detail)
}

// ScoutService tries routes in order. An explicit LCAgent route can therefore
// be first while inherited Chat/project-inference routes remain transparent
// fallbacks.
type ScoutService struct {
	Routes          []ScoutRoute
	OnAttemptStart  func(ScoutRoute)
	OnAttemptFinish func(ScoutRoute, model.LLMUsage, error)
}

func (s *ScoutService) Scout(ctx context.Context, req ScoutRequest) (ScoutResult, error) {
	if s == nil {
		return ScoutResult{}, &ScoutUnavailableError{}
	}
	routes := uniqueScoutRoutes(s.Routes)
	if len(routes) == 0 {
		return ScoutResult{}, &ScoutUnavailableError{}
	}
	var attempts []ScoutAttempt
	var totalUsage model.LLMUsage
	for _, route := range routes {
		if s.OnAttemptStart != nil {
			s.OnAttemptStart(route)
		}
		result, err := RunScoutWithRoute(ctx, req, route)
		if s.OnAttemptFinish != nil {
			s.OnAttemptFinish(route, result.Usage, err)
		}
		addScoutUsage(&totalUsage, result.Usage)
		attempt := ScoutAttempt{
			Source:       route.Source,
			Description:  route.Description,
			Provider:     route.Provider,
			Model:        route.Model,
			SessionID:    result.SessionID,
			ArtifactPath: result.ArtifactPath,
		}
		if err == nil {
			attempt.Status = "used"
			attempts = append(attempts, attempt)
			result.Usage = totalUsage
			result.Attempts = attempts
			return result, nil
		}
		if ctx.Err() != nil {
			return ScoutResult{Attempts: attempts}, ctx.Err()
		}
		attempt.Status = "failed"
		attempt.Error = compactScoutError(err)
		attempts = append(attempts, attempt)
	}
	return ScoutResult{Attempts: attempts, Usage: totalUsage}, &ScoutUnavailableError{Attempts: attempts}
}

// RunScoutWithRoute runs the same LCAgent loop as `lcagent scout`, in-process,
// with workspace-only reads and credentials passed in memory.
func RunScoutWithRoute(ctx context.Context, req ScoutRequest, route ScoutRoute) (ScoutResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceRoot := strings.TrimSpace(req.WorkspaceRoot)
	question := strings.TrimSpace(req.Question)
	provider := normalizeScoutProvider(route.Provider)
	if workspaceRoot == "" {
		return ScoutResult{}, errors.New("repository Scout needs a workspace root")
	}
	if question == "" {
		return ScoutResult{}, errors.New("repository Scout needs a question")
	}
	if provider == "" {
		return ScoutResult{}, errors.New("repository Scout route has no provider")
	}
	timeout := route.RequestTimeout
	if timeout <= 0 {
		timeout = scoutDefaultRequestTimeout
	}

	args := []string{
		"--cwd", workspaceRoot,
		"--output", "stream-json",
		"--provider", provider,
		"--auto", "off",
		"--max-turns", fmt.Sprintf("%d", scoutMaxTurns),
		"--tool-profile", "balanced",
		"--context-profile", "balanced",
		"--utility-provider", "off",
		"--vision-provider", "off",
		"--web-search-backend", "off",
		"--browser-control", "off",
		"--request-timeout", timeout.String(),
	}
	if dataDir := strings.TrimSpace(req.DataDir); dataDir != "" {
		args = append(args, "--data-dir", dataDir)
	}
	if modelName := strings.TrimSpace(route.Model); modelName != "" {
		args = append(args, "--model", modelName)
	}
	if envFile := strings.TrimSpace(route.EnvFile); envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	if effort := strings.TrimSpace(route.ReasoningEffort); effort != "" {
		args = append(args, "--reasoning-effort", effort)
	}
	args = append(args, "--", question)

	var stream bytes.Buffer
	capture := &execRunCapture{}
	err := runExecWithOptions(args, &stream, execRunOptions{
		CommandName:           "scout",
		DefaultAuto:           "off",
		DefaultMaxTurns:       scoutMaxTurns,
		DelegationMode:        "repository_scout",
		DelegationDescription: "Read-only repository inspection requested by Little Control Room Chat.",
		PromptTransform:       repositoryScoutPrompt,
		Context:               ctx,
		ProviderConfig: &modeladapter.OpenRouterConfig{
			APIKey:  strings.TrimSpace(route.APIKey),
			BaseURL: strings.TrimSpace(route.BaseURL),
		},
		WorkspaceOnlyReads: true,
		ReadOnlyTools:      true,
		DisableSkills:      true,
		RouteSource:        strings.TrimSpace(route.Source),
		RouteDescription:   strings.TrimSpace(route.Description),
		Capture:            capture,
	})
	result, parseErr := parseScoutEvents(stream.Bytes(), workspaceRoot)
	result.Route = route
	result.SessionID = firstScoutValue(result.SessionID, capture.SessionID)
	result.ArtifactPath = firstScoutValue(result.ArtifactPath, capture.ArtifactPath)
	if err != nil {
		return result, err
	}
	if parseErr != nil {
		return result, parseErr
	}
	if strings.TrimSpace(result.Summary) == "" {
		return result, errors.New("repository Scout returned no final summary")
	}
	if len(result.Evidence) == 0 && len(result.InspectionTools) == 0 {
		return result, errors.New("repository Scout returned no successful file inspection evidence")
	}
	return result, nil
}

// ScoutRouteFromPreset exposes the inference half of an existing LCAgent route
// preset without carrying its coding autonomy into repository Scout.
func ScoutRouteFromPreset(name string) (ScoutRoute, bool) {
	preset, ok := lcagentRoutePresetByName(name)
	if !ok {
		return ScoutRoute{}, false
	}
	return ScoutRoute{
		Source:          "lcagent_override",
		Description:     "explicit LCAgent " + preset.DisplayName + " route",
		Provider:        preset.Provider,
		Model:           preset.Model,
		ReasoningEffort: preset.ReasoningEffort,
		RequestTimeout:  preset.RequestTimeout,
	}, true
}

func repositoryScoutPrompt(userPrompt string) string {
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return ""
	}
	return fmt.Sprintf(`Repository Scout task from Little Control Room Chat.

Inspect the selected workspace only. This is read-only: never modify files or run commands. Follow the workspace's project instructions. Use targeted list/search/read tools, and read the relevant source documents before making claims. A claim that a plan, document, implementation, or feature does not exist requires a repository search broad enough to support that negative claim.

Return a compact evidence-grounded answer for the parent Chat assistant. Name the relevant workspace-relative files and distinguish confirmed findings from uncertainty. The host records the actual read ranges and inference route separately.

User question:
%s`, userPrompt)
}

func parseScoutEvents(data []byte, workspaceRoot string) (ScoutResult, error) {
	result := ScoutResult{}
	ledger := newReadLedger()
	inspectionSet := map[string]struct{}{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), scoutEventMaxBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal(line, &event); err != nil {
			return result, fmt.Errorf("decode repository Scout trace: %w", err)
		}
		var eventType string
		_ = json.Unmarshal(event["type"], &eventType)
		switch eventType {
		case "session_meta":
			_ = json.Unmarshal(event["id"], &result.SessionID)
			_ = json.Unmarshal(event["provider"], &result.ResolvedProvider)
			_ = json.Unmarshal(event["model"], &result.ResolvedModel)
		case "tool_result":
			var toolName string
			var toolResult tools.ToolResult
			_ = json.Unmarshal(event["tool"], &toolName)
			_ = json.Unmarshal(event["result"], &toolResult)
			if toolResult.Success && isScoutInspectionTool(toolName) {
				inspectionSet[strings.TrimSpace(toolName)] = struct{}{}
			}
			if strings.EqualFold(strings.TrimSpace(toolName), "read_file") {
				ledger.ObserveReadResult(toolResult)
			}
		case "model_response":
			var usage model.LLMUsage
			if err := json.Unmarshal(event["usage_summary"], &usage); err == nil {
				addScoutUsage(&result.Usage, usage)
			}
		case "turn_complete":
			_ = json.Unmarshal(event["summary"], &result.Summary)
			_ = json.Unmarshal(event["final_outcome"], &result.Outcome)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read repository Scout trace: %w", err)
	}
	result.Evidence = scoutEvidenceFromLedger(ledger, workspaceRoot)
	for toolName := range inspectionSet {
		result.InspectionTools = append(result.InspectionTools, toolName)
	}
	sort.Strings(result.InspectionTools)
	return result, nil
}

func scoutEvidenceFromLedger(ledger *readLedger, workspaceRoot string) []ScoutEvidence {
	if ledger == nil || len(ledger.files) == 0 {
		return nil
	}
	paths := make([]string, 0, len(ledger.files))
	for path := range ledger.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var evidence []ScoutEvidence
	for _, path := range paths {
		entry := ledger.files[path]
		absolute := path
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(workspaceRoot, filepath.FromSlash(path))
		}
		for _, span := range entry.Ranges {
			evidence = append(evidence, ScoutEvidence{
				Path:       filepath.Clean(absolute),
				StartLine:  span.Start,
				EndLine:    span.End,
				TotalLines: entry.TotalLines,
			})
		}
	}
	return evidence
}

func isScoutInspectionTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "file_outline", "module_outline", "repo_overview", "list_files", "search", "scout_files":
		return true
	default:
		return false
	}
}

func normalizeScoutProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "openai_api" {
		return "openai"
	}
	return provider
}

func uniqueScoutRoutes(routes []ScoutRoute) []ScoutRoute {
	seen := map[string]struct{}{}
	out := make([]ScoutRoute, 0, len(routes))
	for _, route := range routes {
		provider := normalizeScoutProvider(route.Provider)
		if provider == "" {
			continue
		}
		route.Provider = provider
		key := strings.Join([]string{provider, strings.TrimSpace(route.Model), strings.TrimRight(strings.TrimSpace(route.BaseURL), "/")}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, route)
	}
	return out
}

func compactScoutError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	const max = 320
	if len([]rune(text)) <= max {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func addScoutUsage(total *model.LLMUsage, usage model.LLMUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.TotalTokens += usage.TotalTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.EstimatedCostUSD += usage.EstimatedCostUSD
}

func firstScoutValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
