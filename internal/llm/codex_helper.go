package llm

import (
	"context"
	"errors"
	"sync"
	"time"

	"lcroom/internal/codexapp"
)

const (
	persistentCodexHelperIdleTimeout = 30 * time.Minute
	persistentCodexHelperMaxRequests = 24
)

type codexPromptHelper interface {
	Run(context.Context, codexapp.PromptHelperRequest) (codexapp.PromptHelperResponse, error)
	Close() error
}

type PersistentCodexRunner struct {
	timeout time.Duration
	usage   *UsageTracker
	cache   *localRunnerResponseCache

	runMu sync.Mutex

	helperMu      sync.Mutex
	helper        codexPromptHelper
	helperFactory func() (codexPromptHelper, error)
	idleTimeout   time.Duration
	maxRequests   int
	requestCount  int
	idleTimer     *time.Timer
}

func NewPersistentCodexRunner(timeout time.Duration, usage *UsageTracker) *PersistentCodexRunner {
	return NewPersistentCodexRunnerInDataDir("", timeout, usage)
}

func NewPersistentCodexRunnerInDataDir(dataDir string, timeout time.Duration, usage *UsageTracker) *PersistentCodexRunner {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &PersistentCodexRunner{
		timeout: timeout,
		usage:   usage,
		cache:   newLocalRunnerResponseCache(64),
		helperFactory: func() (codexPromptHelper, error) {
			return codexapp.NewPromptHelperInDataDir(dataDir)
		},
		idleTimeout: persistentCodexHelperIdleTimeout,
		maxRequests: persistentCodexHelperMaxRequests,
	}
}

func (r *PersistentCodexRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil {
		return JSONSchemaResponse{}, errors.New("persistent codex runner not configured")
	}
	if req.Model == "" {
		return JSONSchemaResponse{}, errors.New("persistent codex runner requires a model")
	}

	cacheKey := cacheKeyForJSONSchemaRequest(req)
	if cached, ok := r.cache.Get(cacheKey); ok {
		return cached, nil
	}

	r.runMu.Lock()
	defer r.runMu.Unlock()

	if cached, ok := r.cache.Get(cacheKey); ok {
		return cached, nil
	}

	helper, err := r.acquireHelper()
	if err != nil {
		return JSONSchemaResponse{}, err
	}

	if r.usage != nil {
		r.usage.Start(req.Model)
	}

	runCtx, cancel := withRunnerTimeout(ctx, r.timeout)
	defer cancel()

	response, err := helper.Run(runCtx, codexapp.PromptHelperRequest{
		// codex app-server does not expose the same hard JSON-schema enforcement
		// that `codex exec --output-schema` gives us, so the prompt itself needs
		// to carry the exact schema contract.
		Prompt:          buildSchemaPrompt(req, false),
		Model:           req.Model,
		ReasoningEffort: req.ReasoningEffort,
	})
	if err != nil {
		if r.usage != nil {
			r.usage.Fail(req.Model)
		}
		r.discardHelper(helper)
		return JSONSchemaResponse{}, err
	}

	result := JSONSchemaResponse{
		Status:     "completed",
		Model:      response.Model,
		OutputText: response.OutputText,
		Usage:      response.Usage,
	}
	if result.Model == "" {
		result.Model = req.Model
	}

	if r.usage != nil {
		r.usage.Complete(result.Model, result.Usage)
	}
	r.cache.Store(cacheKey, result)
	r.releaseHelper(helper)
	return result, nil
}

func (r *PersistentCodexRunner) acquireHelper() (codexPromptHelper, error) {
	r.helperMu.Lock()

	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
	if r.helper != nil && r.requestCount >= r.maxRequests {
		helper := r.helper
		r.helper = nil
		r.requestCount = 0
		r.helperMu.Unlock()
		_ = helper.Close()
		r.helperMu.Lock()
	}
	if r.helper == nil {
		helper, err := r.helperFactory()
		if err != nil {
			r.helperMu.Unlock()
			return nil, err
		}
		r.helper = helper
		r.requestCount = 0
	}
	r.requestCount++
	helper := r.helper
	r.helperMu.Unlock()
	return helper, nil
}

func (r *PersistentCodexRunner) releaseHelper(helper codexPromptHelper) {
	r.helperMu.Lock()

	if r.helper != helper {
		r.helperMu.Unlock()
		return
	}
	if r.requestCount >= r.maxRequests {
		current := r.helper
		r.helper = nil
		r.requestCount = 0
		r.helperMu.Unlock()
		_ = current.Close()
		return
	}
	if r.idleTimeout <= 0 {
		r.helperMu.Unlock()
		return
	}
	current := helper
	r.idleTimer = time.AfterFunc(r.idleTimeout, func() {
		r.expireHelper(current)
	})
	r.helperMu.Unlock()
}

func (r *PersistentCodexRunner) discardHelper(helper codexPromptHelper) {
	r.helperMu.Lock()
	if r.helper != helper {
		r.helperMu.Unlock()
		_ = helper.Close()
		return
	}
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
	r.helper = nil
	r.requestCount = 0
	r.helperMu.Unlock()
	_ = helper.Close()
}

func (r *PersistentCodexRunner) expireHelper(helper codexPromptHelper) {
	r.helperMu.Lock()
	if r.helper != helper {
		r.helperMu.Unlock()
		return
	}
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
	r.helper = nil
	r.requestCount = 0
	r.helperMu.Unlock()
	_ = helper.Close()
}
