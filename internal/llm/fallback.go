package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type FallbackRunner struct {
	mu          sync.Mutex
	discovery   *OpenCodeDiscovery
	baseRunner  JSONSchemaRunner
	config      ModelSelectionConfig
	fallback    []string
	fallbackIdx int
	lastError   error
	usage       *UsageTracker
}

type ModelFallback struct {
	FromModel       string
	Model           string
	ReasoningEffort string
}

type ModelFallbackRunner struct {
	baseRunner JSONSchemaRunner
	fallbacks  []ModelFallback
}

const (
	codexMiniCapacityFallbackSourceModel     = "gpt-5.4-mini"
	codexMiniCapacityFallbackTargetModel     = "gpt-5.4"
	codexMiniCapacityFallbackReasoningEffort = "low"
	selectedModelAtCapacityMessage           = "selected model is at capacity. please try a different model."
)

func NewFallbackRunner(discovery *OpenCodeDiscovery, baseRunner JSONSchemaRunner, config ModelSelectionConfig, usage *UsageTracker) *FallbackRunner {
	return &FallbackRunner{
		discovery:  discovery,
		baseRunner: baseRunner,
		config:     config,
		usage:      usage,
	}
}

func NewModelFallbackRunner(baseRunner JSONSchemaRunner, fallbacks []ModelFallback) *ModelFallbackRunner {
	return &ModelFallbackRunner{
		baseRunner: baseRunner,
		fallbacks:  slicesClone(fallbacks),
	}
}

func NewCodexCapacityFallbackRunner(baseRunner JSONSchemaRunner) JSONSchemaRunner {
	return NewModelFallbackRunner(baseRunner, []ModelFallback{
		{
			FromModel:       codexMiniCapacityFallbackSourceModel,
			Model:           codexMiniCapacityFallbackTargetModel,
			ReasoningEffort: codexMiniCapacityFallbackReasoningEffort,
		},
	})
}

func (r *ModelFallbackRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil || r.baseRunner == nil {
		return JSONSchemaResponse{}, errors.New("model fallback runner not configured")
	}

	resp, err := r.baseRunner.RunJSONSchema(ctx, req)
	if err == nil {
		return resp, nil
	}
	if !isModelFallbackError(err) {
		return JSONSchemaResponse{}, err
	}

	lastErr := err
	primaryModel := strings.TrimSpace(req.Model)
	tried := map[string]struct{}{}
	if primaryModel != "" {
		tried[strings.ToLower(primaryModel)] = struct{}{}
	}

	for _, fallback := range r.fallbacks {
		if !modelFallbackApplies(fallback, primaryModel) {
			continue
		}
		fallbackModel := strings.TrimSpace(fallback.Model)
		if fallbackModel == "" {
			continue
		}
		key := strings.ToLower(fallbackModel)
		if _, ok := tried[key]; ok {
			continue
		}
		tried[key] = struct{}{}

		attemptReq := req
		attemptReq.Model = fallbackModel
		if effort := strings.TrimSpace(fallback.ReasoningEffort); effort != "" {
			attemptReq.ReasoningEffort = effort
		}

		resp, err = r.baseRunner.RunJSONSchema(ctx, attemptReq)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isModelFallbackError(err) {
			return JSONSchemaResponse{}, err
		}
	}

	return JSONSchemaResponse{}, lastErr
}

func (r *FallbackRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil {
		return JSONSchemaResponse{}, errors.New("fallback runner not configured")
	}

	r.mu.Lock()
	if len(r.fallback) == 0 {
		if err := r.discovery.Discover(ctx); err != nil {
			r.mu.Unlock()
			return JSONSchemaResponse{}, fmt.Errorf("discover models: %w", err)
		}
		chain, err := r.discovery.BuildFallbackChain(r.config)
		if err != nil {
			r.mu.Unlock()
			return JSONSchemaResponse{}, fmt.Errorf("build fallback chain: %w", err)
		}
		r.fallback = chain
		r.fallbackIdx = 0
	}
	r.mu.Unlock()

	var lastErr error
	startIdx := r.fallbackIdx

	for i := 0; i < len(r.fallback); i++ {
		idx := (startIdx + i) % len(r.fallback)
		model := r.fallback[idx]

		modelToUse := model
		if strings.TrimSpace(req.Model) != "" {
			modelToUse = req.Model
		}

		attemptReq := req
		attemptReq.Model = modelToUse

		resp, err := r.baseRunner.RunJSONSchema(ctx, attemptReq)
		if err == nil {
			r.mu.Lock()
			r.fallbackIdx = idx
			r.lastError = nil
			r.mu.Unlock()
			return resp, nil
		}

		lastErr = err
		r.mu.Lock()
		r.lastError = err
		r.mu.Unlock()

		if !isModelFallbackError(err) {
			return JSONSchemaResponse{}, err
		}

		if i < len(r.fallback)-1 {
			select {
			case <-ctx.Done():
				return JSONSchemaResponse{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	if lastErr != nil {
		return JSONSchemaResponse{}, lastErr
	}
	return JSONSchemaResponse{}, errors.New("all models in fallback chain failed")
}

func modelFallbackApplies(fallback ModelFallback, currentModel string) bool {
	fromModel := strings.TrimSpace(fallback.FromModel)
	return fromModel == "" || strings.EqualFold(fromModel, strings.TrimSpace(currentModel))
}

func isModelFallbackError(err error) bool {
	return isRateLimitError(err) || IsModelCapacityError(err)
}

func IsModelCapacityError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), selectedModelAtCapacityMessage)
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	if strings.Contains(errStr, "rate limit") {
		return true
	}
	if strings.Contains(errStr, "too many requests") {
		return true
	}
	if strings.Contains(errStr, "429") {
		return true
	}
	if strings.Contains(errStr, "quota") {
		return true
	}
	if strings.Contains(errStr, "throttl") {
		return true
	}

	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 429
	}

	return false
}

func (r *FallbackRunner) CurrentModel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fallback) == 0 || r.fallbackIdx >= len(r.fallback) {
		return ""
	}
	return r.fallback[r.fallbackIdx]
}

func (r *FallbackRunner) FallbackChain() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slicesClone(r.fallback)
}

func (r *FallbackRunner) LastError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError
}

func (r *FallbackRunner) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.discovery.Discover(ctx); err != nil {
		return err
	}

	chain, err := r.discovery.BuildFallbackChain(r.config)
	if err != nil {
		return err
	}

	r.fallback = chain
	r.fallbackIdx = 0
	r.lastError = nil
	return nil
}

func slicesClone[T any](s []T) []T {
	if s == nil {
		return nil
	}
	result := make([]T, len(s))
	copy(result, s)
	return result
}
