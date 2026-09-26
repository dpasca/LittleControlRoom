package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	claudeModelCatalogTTL       = 15 * time.Minute
	claudeModelCatalogTimeout   = 20 * time.Second
	claudeModelCatalogRequestID = "lcr-model-catalog"
	claudeCLIDefaultModelValue  = "default"
)

var errClaudeModelCatalogUnavailable = errors.New("Claude Code model catalog is unavailable")

// claudeCLIModel is one entry of the model list Claude Code reports in its
// stream-json initialize response. Value is what --model accepts, and
// ResolvedModel is the concrete API model that value currently maps to.
type claudeCLIModel struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// newClaudeModelCatalogCommand builds the short-lived CLI process that answers
// the initialize handshake. It sends no prompt, so it makes no model request.
var newClaudeModelCatalogCommand = func(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--verbose", "--input-format=stream-json", "--output-format=stream-json")
	configureAppServerCommand(cmd)
	applyEmbeddedClaudeProcessEnvironment(cmd)
	cmd.Cancel = func() error { return terminateAppServerCommand(cmd) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

type claudeModelCatalogEntry struct {
	options   []ModelOption
	fetchedAt time.Time
}

var claudeModelCatalog struct {
	// mu guards entries and latest and is never held across a CLI query, so
	// render-path label lookups cannot block on it.
	mu      sync.Mutex
	entries map[string]claudeModelCatalogEntry
	latest  []ModelOption
	// fetchMu serializes CLI queries so concurrent pickers share one result.
	fetchMu sync.Mutex
}

// ClaudeCodeModelCatalog returns the model choices the installed Claude Code
// CLI offers outside any project. It can take a few seconds on a cold cache,
// so call it from a background command, never from the render path.
func (m *Manager) ClaudeCodeModelCatalog(ctx context.Context) ([]ModelOption, error) {
	if m == nil || m.claudeModelCatalog == nil {
		return nil, errClaudeModelCatalogUnavailable
	}
	home, _ := os.UserHomeDir()
	return m.claudeModelCatalog(ctx, home)
}

// loadClaudeModelCatalog returns the model choices Claude Code offers in dir,
// querying the CLI at most once per TTL. A failed refresh keeps stale choices.
func loadClaudeModelCatalog(ctx context.Context, dir string) ([]ModelOption, error) {
	dir = strings.TrimSpace(dir)
	if options, fresh := cachedClaudeModelCatalog(dir, time.Now()); fresh {
		return options, nil
	}
	claudeModelCatalog.fetchMu.Lock()
	defer claudeModelCatalog.fetchMu.Unlock()
	stale, fresh := cachedClaudeModelCatalog(dir, time.Now())
	if fresh {
		return stale, nil
	}
	models, err := queryClaudeModelCatalog(ctx, dir)
	options := claudeModelOptionsFromCLI(models)
	if err == nil && len(options) == 0 {
		err = errors.New("Claude Code reported no models")
	}
	if err != nil {
		if len(stale) > 0 {
			return stale, nil
		}
		return nil, err
	}
	claudeModelCatalog.mu.Lock()
	if claudeModelCatalog.entries == nil {
		claudeModelCatalog.entries = make(map[string]claudeModelCatalogEntry)
	}
	claudeModelCatalog.entries[dir] = claudeModelCatalogEntry{options: options, fetchedAt: time.Now()}
	claudeModelCatalog.latest = options
	claudeModelCatalog.mu.Unlock()
	return cloneModelOptions(options), nil
}

func cachedClaudeModelCatalog(dir string, now time.Time) ([]ModelOption, bool) {
	claudeModelCatalog.mu.Lock()
	defer claudeModelCatalog.mu.Unlock()
	entry, ok := claudeModelCatalog.entries[dir]
	if !ok {
		return nil, false
	}
	return cloneModelOptions(entry.options), now.Sub(entry.fetchedAt) < claudeModelCatalogTTL
}

func latestClaudeModelCatalog() []ModelOption {
	claudeModelCatalog.mu.Lock()
	defer claudeModelCatalog.mu.Unlock()
	return claudeModelCatalog.latest
}

func cloneModelOptions(options []ModelOption) []ModelOption {
	if options == nil {
		return nil
	}
	return append([]ModelOption(nil), options...)
}

func queryClaudeModelCatalog(ctx context.Context, dir string) ([]claudeCLIModel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, claudeModelCatalogTimeout)
	defer cancel()
	cmd := newClaudeModelCatalogCommand(ctx)
	if dir != "" {
		cmd.Dir = dir
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Claude Code: %w", err)
	}
	request := `{"type":"control_request","request_id":"` + claudeModelCatalogRequestID + `","request":{"subtype":"initialize"}}` + "\n"
	_, writeErr := io.WriteString(stdin, request)
	_ = stdin.Close()
	models, readErr := readClaudeModelCatalogResponse(stdout)
	// The CLI exits once stdin closes; stop it early when the answer is in.
	_ = terminateAppServerCommand(cmd)
	_, _ = io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	if readErr == nil {
		return models, nil
	}
	if writeErr != nil {
		readErr = fmt.Errorf("write Claude Code initialize request: %w", writeErr)
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return nil, fmt.Errorf("%w: %s", readErr, truncateClaudeCatalogDetail(detail))
	}
	return nil, readErr
}

func truncateClaudeCatalogDetail(detail string) string {
	const limit = 300
	if len(detail) <= limit {
		return detail
	}
	return detail[:limit] + "..."
}

func readClaudeModelCatalogResponse(r io.Reader) ([]claudeCLIModel, error) {
	// The initialize response also lists every slash command, so a single line
	// can exceed bufio.Scanner's default token size.
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if models, ok, parseErr := parseClaudeModelCatalogLine(line); ok {
			return models, parseErr
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("Claude Code exited without reporting its models")
			}
			return nil, err
		}
	}
}

func parseClaudeModelCatalogLine(line []byte) ([]claudeCLIModel, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.Contains(line, []byte(claudeModelCatalogRequestID)) {
		return nil, false, nil
	}
	var envelope struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  struct {
				Models []claudeCLIModel `json:"models"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Type != "control_response" || envelope.Response.RequestID != claudeModelCatalogRequestID {
		return nil, false, nil
	}
	if envelope.Response.Subtype != "success" {
		return nil, true, fmt.Errorf("Claude Code initialize failed: %s", firstNonEmptyTrimmed(envelope.Response.Error, envelope.Response.Subtype))
	}
	return envelope.Response.Response.Models, true, nil
}

// claudeModelOptionsFromCLI keeps Claude Code's own order and effort support,
// then appends curated aliases it did not list so saved choices stay valid.
func claudeModelOptionsFromCLI(models []claudeCLIModel) []ModelOption {
	options := make([]ModelOption, 0, len(models)+len(claudeEmbeddedModelOptions()))
	seen := map[string]struct{}{}
	for _, model := range models {
		value := strings.TrimSpace(model.Value)
		if value == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(value)]; ok {
			continue
		}
		seen[strings.ToLower(value)] = struct{}{}
		resolved := concreteClaudeModel(model.ResolvedModel)
		option := ModelOption{
			ID:            value,
			Model:         value,
			ResolvedModel: resolved,
			DisplayName:   firstNonEmptyTrimmed(claudeModelDisplayName(firstNonEmptyTrimmed(resolved, value)), model.DisplayName, value),
			Description:   strings.TrimSpace(model.Description),
			IsDefault:     strings.EqualFold(value, claudeCLIDefaultModelValue),
		}
		for _, effort := range model.SupportedEffortLevels {
			if effort = strings.TrimSpace(effort); effort != "" {
				option.SupportedReasoningEfforts = append(option.SupportedReasoningEfforts, ReasoningEffortOption{ReasoningEffort: effort, Description: claudeReasoningEffortDescription(effort)})
			}
		}
		if len(option.SupportedReasoningEfforts) == 0 && model.SupportsEffort {
			option.SupportedReasoningEfforts = claudeReasoningEffortOptions()
		}
		if len(option.SupportedReasoningEfforts) > 0 {
			option.DefaultReasoningEffort = option.SupportedReasoningEfforts[0].ReasoningEffort
			for _, effort := range option.SupportedReasoningEfforts {
				if effort.ReasoningEffort == claudeDefaultReasoningEffort {
					option.DefaultReasoningEffort = claudeDefaultReasoningEffort
				}
			}
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return nil
	}
	for _, alias := range claudeEmbeddedModelOptions() {
		if _, ok := seen[strings.ToLower(alias.Model)]; ok {
			continue
		}
		alias.IsDefault = false
		// A context suffix changes the requested window, not effort support.
		// Prefer the CLI's metadata over the curated fallback's effort list.
		base, suffix := splitClaudeContextSuffix(alias.Model)
		for _, option := range options {
			if option.Model != base {
				continue
			}
			alias.SupportedReasoningEfforts = option.SupportedReasoningEfforts
			alias.DefaultReasoningEffort = option.DefaultReasoningEffort
			if resolvedBase, _ := splitClaudeContextSuffix(option.ResolvedModel); resolvedBase != "" {
				alias.ResolvedModel = resolvedBase + suffix
			}
			break
		}
		if resolved := claudeAliasResolvedModel(options, alias.Model); resolved != "" {
			if alias.ResolvedModel == "" {
				alias.ResolvedModel = resolved
			}
		}
		if alias.ResolvedModel != "" {
			alias.DisplayName = claudeModelDisplayName(alias.ResolvedModel)
		}
		options = append(options, alias)
	}
	return options
}

func claudeReasoningEffortDescription(effort string) string {
	for _, option := range claudeReasoningEffortOptions() {
		if strings.EqualFold(option.ReasoningEffort, effort) {
			return option.Description
		}
	}
	return ""
}

// claudeAliasResolvedModel finds the version a bare family alias such as
// "opus" maps to, using any listed choice of the same family. Context-window
// variants share a version, so the alias's own suffix is kept.
func claudeAliasResolvedModel(options []ModelOption, alias string) string {
	base, context := splitClaudeContextSuffix(concreteClaudeModel(alias))
	if base == "" || strings.Contains(strings.ToLower(base), "claude-") {
		return ""
	}
	for _, option := range options {
		resolvedBase, _ := splitClaudeContextSuffix(option.ResolvedModel)
		if family, _ := claudeModelNameParts(resolvedBase); family != "" && strings.EqualFold(family, base) {
			return resolvedBase + context
		}
	}
	return ""
}

// claudeKnownResolvedModel returns the concrete model a Claude name runs:
// concrete IDs are themselves, aliases need the latest CLI catalog.
func claudeKnownResolvedModel(model string) (string, bool) {
	model = concreteClaudeModel(model)
	if model == "" {
		return "", false
	}
	if strings.Contains(strings.ToLower(model), "claude-") {
		return model, true
	}
	options := latestClaudeModelCatalog()
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Model), model) && strings.TrimSpace(option.ResolvedModel) != "" {
			return strings.TrimSpace(option.ResolvedModel), true
		}
	}
	if resolved := claudeAliasResolvedModel(options, model); resolved != "" {
		return resolved, true
	}
	return "", false
}

// claudeCatalogModelDisplayName names a Claude model the way the latest CLI
// catalog does, so an alias such as "opus" reads as the version it runs.
func claudeCatalogModelDisplayName(model string) string {
	model = concreteClaudeModel(model)
	if model == "" {
		return ""
	}
	options := latestClaudeModelCatalog()
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Model), model) && strings.TrimSpace(option.DisplayName) != "" {
			return strings.TrimSpace(option.DisplayName)
		}
	}
	if resolved := claudeAliasResolvedModel(options, model); resolved != "" {
		return claudeModelDisplayName(resolved)
	}
	return claudeModelDisplayName(model)
}

var claudeContextSuffixPattern = regexp.MustCompile(`(?i)\[(\d+[km])\]$`)

func splitClaudeContextSuffix(model string) (string, string) {
	model = strings.TrimSpace(model)
	if loc := claudeContextSuffixPattern.FindStringIndex(model); loc != nil {
		return model[:loc[0]], model[loc[0]:]
	}
	return model, ""
}

// claudeModelDisplayName turns Claude model IDs and aliases into readable
// names, e.g. claude-opus-5-5[1m] becomes "Opus 5.5 (1M)". It parses the ID
// shape rather than a model table, so new versions need no code change.
func claudeModelDisplayName(model string) string {
	model = concreteClaudeModel(model)
	base, context := splitClaudeContextSuffix(model)
	family, version := claudeModelNameParts(base)
	if family == "" {
		return model
	}
	name := strings.ToUpper(family[:1]) + family[1:]
	if len(version) > 0 {
		name += " " + strings.Join(version, ".")
	}
	if context != "" {
		name += " (" + strings.ToUpper(strings.Trim(context, "[]")) + ")"
	}
	return name
}

// claudeModelNameParts splits claude-opus-4-1-20250805 style IDs (including
// the legacy claude-3-5-sonnet order and Bedrock/Vertex decorations) into a
// family and version numbers. A bare alias is its own family.
func claudeModelNameParts(model string) (string, []string) {
	lower := strings.ToLower(strings.TrimSpace(model))
	index := strings.Index(lower, "claude-")
	if index < 0 {
		if isLowerASCIIWord(lower) {
			return lower, nil
		}
		return "", nil
	}
	rest := lower[index+len("claude-"):]
	if cut := strings.IndexAny(rest, "@:"); cut >= 0 {
		rest = rest[:cut]
	}
	family := ""
	var version []string
	for _, token := range strings.Split(rest, "-") {
		switch {
		case isASCIIDigits(token) && len(token) <= 2:
			version = append(version, token)
		case family == "" && isLowerASCIIWord(token):
			family = token
		}
		// Dates such as 20251001 and revisions such as v1 carry no display meaning.
	}
	return family, version
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isLowerASCIIWord(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
