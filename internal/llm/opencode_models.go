package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

type OpenCodeModelInfo struct {
	ID           string
	ProviderID   string
	Name         string
	Family       string
	Status       string
	InputCost    float64
	OutputCost   float64
	CacheRead    float64
	CacheWrite   float64
	ContextLimit int64
	OutputLimit  int64
	Capabilities OpenCodeModelCapabilities
}

type OpenCodeModelCapabilities struct {
	Temperature bool
	Reasoning   bool
	ToolCall    bool
	TextInput   bool
	ImageInput  bool
}

type OpenCodeProviderInfo struct {
	ID            string
	Name          string
	Type          string
	Authenticated bool
}

type OpenCodeDiscovery struct {
	mu            sync.RWMutex
	models        []OpenCodeModelInfo
	providers     []OpenCodeProviderInfo
	discoveredAt  time.Time
	cacheDuration time.Duration
	command       string
	discoverOnce  sync.Once
	discoverErr   error
}

const (
	defaultOpenCodeCacheDuration = 5 * time.Minute
	openCodeDiscoverTimeout      = 30 * time.Second
)

func NewOpenCodeDiscovery() *OpenCodeDiscovery {
	return &OpenCodeDiscovery{
		cacheDuration: defaultOpenCodeCacheDuration,
		command:       "opencode",
	}
}

func (d *OpenCodeDiscovery) Models() []OpenCodeModelInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.models
}

func (d *OpenCodeDiscovery) Providers() []OpenCodeProviderInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.providers
}

func (d *OpenCodeDiscovery) AuthenticatedProviders() []OpenCodeProviderInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var result []OpenCodeProviderInfo
	for _, p := range d.providers {
		if p.Authenticated {
			result = append(result, p)
		}
	}
	return result
}

func (d *OpenCodeDiscovery) Discover(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.discoveredAt.Add(d.cacheDuration).After(time.Now()) && len(d.models) > 0 {
		return nil
	}

	models, err := d.discoverModels(ctx)
	if err != nil {
		d.discoverErr = err
		return err
	}

	providers, err := d.discoverProviders(ctx)
	if err != nil {
		d.discoverErr = err
		return err
	}

	d.models = models
	d.providers = providers
	d.discoveredAt = time.Now()
	d.discoverErr = nil
	return nil
}

func (d *OpenCodeDiscovery) discoverModels(ctx context.Context) ([]OpenCodeModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, openCodeDiscoverTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, d.command, "models", "--verbose")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run opencode models: %w: %s", err, string(output))
	}

	return parseOpenCodeModelsOutput(string(output))
}

func (d *OpenCodeDiscovery) discoverProviders(ctx context.Context) ([]OpenCodeProviderInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, openCodeDiscoverTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, d.command, "providers", "list")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run opencode providers list: %w: %s", err, string(output))
	}

	return parseOpenCodeProvidersOutput(string(output))
}

func parseOpenCodeModelsOutput(raw string) ([]OpenCodeModelInfo, error) {
	var models []OpenCodeModelInfo
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 16*1024*1024)

	var currentID string
	var braceDepth int
	var currentJSON strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if braceDepth == 0 && !strings.HasPrefix(line, "{") {
			currentID = strings.TrimSpace(line)
			if strings.Contains(currentID, "/") {
				parts := strings.SplitN(currentID, "/", 2)
				if len(parts) == 2 {
					currentID = parts[1]
				}
			}
			continue
		}

		currentJSON.WriteString(line)
		currentJSON.WriteString("\n")

		for _, ch := range line {
			if ch == '{' {
				braceDepth++
			} else if ch == '}' {
				braceDepth--
			}
		}

		if braceDepth == 0 && currentJSON.Len() > 0 {
			var rawModel struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Name       string `json:"name"`
				Family     string `json:"family"`
				Status     string `json:"status"`
				Cost       struct {
					Input  float64 `json:"input"`
					Output float64 `json:"output"`
					Cache  struct {
						Read  float64 `json:"read"`
						Write float64 `json:"write"`
					} `json:"cache"`
				} `json:"cost"`
				Limit struct {
					Context int64 `json:"context"`
					Output  int64 `json:"output"`
				} `json:"limit"`
				Capabilities struct {
					Temperature bool `json:"temperature"`
					Reasoning   bool `json:"reasoning"`
					ToolCall    bool `json:"toolcall"`
					Input       struct {
						Text  bool `json:"text"`
						Image bool `json:"image"`
					} `json:"input"`
				} `json:"capabilities"`
			}

			if err := json.Unmarshal([]byte(currentJSON.String()), &rawModel); err == nil {
				models = append(models, OpenCodeModelInfo{
					ID:           rawModel.ID,
					ProviderID:   rawModel.ProviderID,
					Name:         rawModel.Name,
					Family:       rawModel.Family,
					Status:       rawModel.Status,
					InputCost:    rawModel.Cost.Input,
					OutputCost:   rawModel.Cost.Output,
					CacheRead:    rawModel.Cost.Cache.Read,
					CacheWrite:   rawModel.Cost.Cache.Write,
					ContextLimit: rawModel.Limit.Context,
					OutputLimit:  rawModel.Limit.Output,
					Capabilities: OpenCodeModelCapabilities{
						Temperature: rawModel.Capabilities.Temperature,
						Reasoning:   rawModel.Capabilities.Reasoning,
						ToolCall:    rawModel.Capabilities.ToolCall,
						TextInput:   rawModel.Capabilities.Input.Text,
						ImageInput:  rawModel.Capabilities.Input.Image,
					},
				})
			}

			currentJSON.Reset()
			currentID = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan opencode models output: %w", err)
	}

	return models, nil
}

func parseOpenCodeProvidersOutput(raw string) ([]OpenCodeProviderInfo, error) {
	var providers []OpenCodeProviderInfo

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = stripANSI(strings.TrimSpace(line))
		if line == "" {
			continue
		}

		if strings.Contains(line, "OpenAI") || strings.Contains(line, "MiniMax") || strings.Contains(line, "OpenCode Zen") {
			var name, providerType string

			if strings.Contains(line, "OpenAI oauth") {
				name = "OpenAI"
				providerType = "oauth"
			} else if strings.Contains(line, "OpenAI OPENAI_API_KEY") {
				name = "OpenAI"
				providerType = "api_key"
			} else if strings.Contains(line, "MiniMax") {
				name = "MiniMax"
				providerType = "api"
			} else if strings.Contains(line, "OpenCode Zen") {
				name = "OpenCode Zen"
				providerType = "api"
			} else {
				continue
			}

			providerID := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
			if strings.Contains(name, "(") {
				if idx := strings.Index(name, "("); idx > 0 {
					providerID = strings.ToLower(strings.TrimSpace(name[:idx]))
					name = strings.TrimSpace(name[:idx])
				}
			}

			providers = append(providers, OpenCodeProviderInfo{
				ID:            providerID,
				Name:          name,
				Type:          providerType,
				Authenticated: true,
			})
		}
	}

	return providers, nil
}

func ParseOpenCodeProvidersOutput(raw string) ([]OpenCodeProviderInfo, error) {
	return parseOpenCodeProvidersOutput(raw)
}

func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false
	for _, ch := range s {
		if ch == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if ch == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(ch)
	}
	return result.String()
}

type ModelTier string

const (
	ModelTierFree     ModelTier = "free"
	ModelTierCheap    ModelTier = "cheap"
	ModelTierBalanced ModelTier = "balanced"
)

type ModelSelectionConfig struct {
	Tier            ModelTier
	PreferReasoning bool
	MaxInputCost    float64
	MaxOutputCost   float64
}

func DefaultModelSelectionConfig() ModelSelectionConfig {
	return ModelSelectionConfig{
		Tier:            ModelTierFree,
		PreferReasoning: false,
		MaxInputCost:    1.0,
		MaxOutputCost:   5.0,
	}
}

func (d *OpenCodeDiscovery) SelectModel(cfg ModelSelectionConfig) (string, error) {
	d.mu.RLock()
	models := slices.Clone(d.models)
	providers := d.providers
	d.mu.RUnlock()

	if len(models) == 0 {
		return "", errors.New("no models discovered")
	}

	authenticatedProviderIDs := make(map[string]bool)
	for _, p := range providers {
		if p.Authenticated {
			authenticatedProviderIDs[p.ID] = true
			authenticatedProviderIDs[strings.ToLower(p.Name)] = true
		}
	}

	var candidates []OpenCodeModelInfo
	for _, m := range models {
		if m.Status != "active" {
			continue
		}
		if !m.Capabilities.TextInput {
			continue
		}
		if len(authenticatedProviderIDs) > 0 {
			providerMatch := false
			for pid := range authenticatedProviderIDs {
				if strings.EqualFold(m.ProviderID, pid) {
					providerMatch = true
					break
				}
			}
			if !providerMatch && m.ProviderID != "opencode" {
				continue
			}
		}
		candidates = append(candidates, m)
	}

	if len(candidates) == 0 {
		return "", errors.New("no suitable models available")
	}

	slices.SortFunc(candidates, func(a, b OpenCodeModelInfo) int {
		aScore := modelScoreForTier(a, cfg)
		bScore := modelScoreForTier(b, cfg)
		if aScore != bScore {
			return aScore - bScore
		}
		if a.InputCost != b.InputCost {
			if a.InputCost < b.InputCost {
				return -1
			}
			return 1
		}
		if a.OutputCost != b.OutputCost {
			if a.OutputCost < b.OutputCost {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})

	if len(candidates) > 0 {
		return candidates[0].ID, nil
	}

	return "", errors.New("no suitable model found")
}

func modelScoreForTier(m OpenCodeModelInfo, cfg ModelSelectionConfig) int {
	isFree := m.InputCost == 0 && m.OutputCost == 0
	isCheap := m.InputCost <= 0.5 && m.OutputCost <= 2.0

	switch cfg.Tier {
	case ModelTierFree:
		if isFree {
			return 0
		}
		if isCheap {
			return 10
		}
		return 20
	case ModelTierCheap:
		if isCheap {
			return 0
		}
		if isFree {
			return 5
		}
		return 10
	default:
		if isFree {
			return 0
		}
		if isCheap {
			return 5
		}
		return 10
	}
}

func (d *OpenCodeDiscovery) BuildFallbackChain(cfg ModelSelectionConfig) ([]string, error) {
	d.mu.RLock()
	models := slices.Clone(d.models)
	providers := d.providers
	d.mu.RUnlock()

	if len(models) == 0 {
		return nil, errors.New("no models discovered")
	}

	authenticatedProviderIDs := make(map[string]bool)
	for _, p := range providers {
		if p.Authenticated {
			authenticatedProviderIDs[p.ID] = true
			authenticatedProviderIDs[strings.ToLower(p.Name)] = true
		}
	}

	var candidates []OpenCodeModelInfo
	for _, m := range models {
		if m.Status != "active" {
			continue
		}
		if !m.Capabilities.TextInput {
			continue
		}
		if len(authenticatedProviderIDs) > 0 {
			providerMatch := false
			for pid := range authenticatedProviderIDs {
				if strings.EqualFold(m.ProviderID, pid) {
					providerMatch = true
					break
				}
			}
			if !providerMatch && m.ProviderID != "opencode" {
				continue
			}
		}
		candidates = append(candidates, m)
	}

	if len(candidates) == 0 {
		return nil, errors.New("no suitable models available")
	}

	slices.SortFunc(candidates, func(a, b OpenCodeModelInfo) int {
		aScore := modelScoreForTier(a, cfg)
		bScore := modelScoreForTier(b, cfg)
		if aScore != bScore {
			return aScore - bScore
		}
		if a.InputCost != b.InputCost {
			if a.InputCost < b.InputCost {
				return -1
			}
			return 1
		}
		if a.OutputCost != b.OutputCost {
			if a.OutputCost < b.OutputCost {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})

	seen := make(map[string]bool)
	var result []string
	for _, m := range candidates {
		modelRef := m.ProviderID + "/" + m.ID
		if !seen[modelRef] {
			seen[modelRef] = true
			result = append(result, modelRef)
		}
	}

	return result, nil
}

func (d *OpenCodeDiscovery) LastError() error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.discoverErr
}

func (d *OpenCodeDiscovery) IsDiscovered() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.models) > 0 && d.discoveredAt.Add(d.cacheDuration).After(time.Now())
}
