package service

import (
	"context"
	"encoding/json"
	"lcroom/internal/aibackend"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
	"lcroom/internal/todocapture"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type staticDetector struct {
	name       string
	activities map[string]*model.DetectorProjectActivity
}

type countingDetector struct {
	name       string
	calls      int
	scopes     []scanner.PathScope
	activities map[string]*model.DetectorProjectActivity
}

type blockingDetector struct {
	started chan struct{}
	release chan struct{}
}

func (d staticDetector) Name() string {
	if d.name != "" {
		return d.name
	}
	return "static"
}

func (d staticDetector) Detect(context.Context, scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	out := make(map[string]*model.DetectorProjectActivity, len(d.activities))
	for path, activity := range d.activities {
		out[path] = activity
	}
	return out, nil
}

func (d *countingDetector) Name() string {
	if d.name != "" {
		return d.name
	}
	return "counting"
}

func (d *countingDetector) Detect(_ context.Context, scope scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	d.calls++
	d.scopes = append(d.scopes, scope)
	out := make(map[string]*model.DetectorProjectActivity, len(d.activities))
	for path, activity := range d.activities {
		out[path] = activity
	}
	return out, nil
}

func (d blockingDetector) Name() string {
	return "blocking"
}

func (d blockingDetector) Detect(context.Context, scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	select {
	case d.started <- struct{}{}:
	default:
	}
	<-d.release
	return map[string]*model.DetectorProjectActivity{}, nil
}

type recordingClassifier struct {
	normalCalls int
	forcedCalls int
	notifyCalls int
	lastState   model.ProjectState
}

func (c *recordingClassifier) QueueProject(_ context.Context, state model.ProjectState) (bool, error) {
	if len(state.Sessions) == 0 {
		return false, nil
	}
	c.normalCalls++
	c.lastState = state
	return true, nil
}

func (c *recordingClassifier) QueueProjectRetry(_ context.Context, state model.ProjectState, _ time.Duration) (bool, error) {
	if len(state.Sessions) == 0 {
		return false, nil
	}
	c.forcedCalls++
	c.lastState = state
	return true, nil
}

func (c *recordingClassifier) Notify()               { c.notifyCalls++ }
func (c *recordingClassifier) Start(context.Context) {}

func TestApplyEditableSettingsSkipsAIClientRefreshForEmbeddedModelPreferences(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	detectCalls := 0
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(context.Context, config.AppConfig, config.AIBackend) aibackend.Status {
			detectCalls++
			return readyBackendStatus(config.AIBackendCodex)
		},
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.EmbeddedCodexModel = "gpt-5.4"
	settings.EmbeddedCodexReasoning = "high"

	svc.ApplyEditableSettings(settings)

	if detectCalls != 0 {
		t.Fatalf("backend detector calls = %d, want 0 for embedded model-only changes", detectCalls)
	}
	if got := svc.cfg.EmbeddedCodexModel; got != "gpt-5.4" {
		t.Fatalf("embedded codex model = %q, want gpt-5.4", got)
	}
	if got := svc.cfg.EmbeddedCodexReasoning; got != "high" {
		t.Fatalf("embedded codex reasoning = %q, want high", got)
	}
}

func TestApplyEditableSettingsUpdatesPrivacyMode(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.PrivacyMode = true
	settings.HideReasoningSections = false

	svc.ApplyEditableSettings(settings)

	got := svc.Config()
	if !got.PrivacyMode {
		t.Fatalf("privacy mode = false, want true")
	}
	if got.HideReasoningSections {
		t.Fatalf("hide reasoning sections = true, want false")
	}
}

func TestApplyEditableSettingsFailsCapturePolicyClosedButAppliesOtherSettings(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		cfg:               cfg,
		store:             st,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
	}
	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.EngineerTodoCaptureMode = todocapture.ModeOff
	settings.PrivacyMode = true
	err = svc.ApplyEditableSettings(settings)
	if err == nil {
		t.Fatal("ApplyEditableSettings() error = nil, want policy persistence error")
	}
	got := svc.Config()
	if got.EngineerTodoCaptureMode != todocapture.ModeOff {
		t.Fatalf("capture mode = %s, want fail-closed off", got.EngineerTodoCaptureMode)
	}
	if !got.PrivacyMode {
		t.Fatal("privacy mode was not applied alongside policy error")
	}
}

func TestInitializeTodoCapturePolicyIsExplicitHostAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "initial-policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.EngineerTodoCaptureMode = todocapture.ModeExplicit
	svc := New(cfg, st, events.NewBus(), nil)
	if _, found, err := st.RuntimeSetting(ctx, todocapture.RuntimeModeSettingKey); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("service construction unexpectedly changed runtime authorization")
	}
	if err := svc.InitializeTodoCapturePolicy(ctx); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.RuntimeSetting(ctx, todocapture.RuntimeModeSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got != string(todocapture.ModeExplicit) {
		t.Fatalf("initialized runtime policy = %q, %v; want %q, true", got, found, todocapture.ModeExplicit)
	}
}

func TestApplyEditableSettingsRetriesPolicyPersistenceWhenConfigAlreadyMatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "policy-retry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetRuntimeSetting(ctx, todocapture.RuntimeModeSettingKey, string(todocapture.ModeExplicitAndClearDeferrals)); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.EngineerTodoCaptureMode = todocapture.ModeOff
	svc := &Service{
		cfg:               cfg,
		store:             st,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
	}
	settings := config.EditableSettingsFromAppConfig(cfg)
	if err := svc.ApplyEditableSettings(settings); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.RuntimeSetting(ctx, todocapture.RuntimeModeSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != string(todocapture.ModeOff) {
		t.Fatalf("persisted capture mode = %q, %v; want %q, true", got, ok, todocapture.ModeOff)
	}
}

func TestApplyEditableSettingsUpdatesMobilePreferences(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
	}
	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.MobileEnabled = false
	settings.MobileInputEnabled = true
	settings.MobileListenAddress = "0.0.0.0:8787"

	svc.ApplyEditableSettings(settings)

	got := svc.Config()
	if got.MobileEnabled {
		t.Fatal("mobile enabled = true, want false")
	}
	if !got.MobileInputEnabled {
		t.Fatal("mobile input enabled = false, want true")
	}
	if got.MobileListenAddress != "0.0.0.0:8787" {
		t.Fatalf("mobile listen address = %q, want 0.0.0.0:8787", got.MobileListenAddress)
	}
}

func TestApplyEditableSettingsUpdatesLCAgentVisionAndRecentModels(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
	}
	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.LCAgentVisionProvider = "openai"
	settings.LCAgentVisionModel = "openai/gpt-5-mini"
	settings.LCAgentMainVisionProvider = "openrouter"
	settings.LCAgentMainVisionModel = "openai/gpt-5.4"
	settings.RecentCodexModels = []string{"gpt-5.4"}
	settings.RecentLCAgentModels = []string{"openai/gpt-5-mini"}

	svc.ApplyEditableSettings(settings)
	settings.RecentCodexModels[0] = "mutated"
	settings.RecentLCAgentModels[0] = "mutated"

	got := svc.Config()
	if got.LCAgentVisionProvider != "openai" || got.LCAgentVisionModel != "gpt-5-mini" {
		t.Fatalf("LCAgent vision config = %q/%q, want openai/gpt-5-mini", got.LCAgentVisionProvider, got.LCAgentVisionModel)
	}
	if got.LCAgentMainVisionProvider != "openrouter" || got.LCAgentMainVisionModel != "openai/gpt-5.4" {
		t.Fatalf("LCAgent main vision config = %q/%q", got.LCAgentMainVisionProvider, got.LCAgentMainVisionModel)
	}
	if len(got.RecentCodexModels) != 1 || got.RecentCodexModels[0] != "gpt-5.4" {
		t.Fatalf("recent Codex models = %#v, want copied settings", got.RecentCodexModels)
	}
	if len(got.RecentLCAgentModels) != 1 || got.RecentLCAgentModels[0] != "openai/gpt-5-mini" {
		t.Fatalf("recent LCAgent models = %#v, want copied settings", got.RecentLCAgentModels)
	}
}

func TestApplyEditableSettingsRefreshesAIClientsWhenBackendConfigChanges(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	detectCalls := 0
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(_ context.Context, cfg config.AppConfig, backend config.AIBackend) aibackend.Status {
			detectCalls++
			return readyBackendStatus(firstNonZeroBackend(backend, cfg.EffectiveAIBackend()))
		},
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.AIBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	svc.ApplyEditableSettings(settings)

	if detectCalls != 1 {
		t.Fatalf("backend detector calls = %d, want 1 when backend config changes", detectCalls)
	}
	if svc.commitMessageSuggester == nil {
		t.Fatalf("commitMessageSuggester = nil, want OpenAI client after reconfigure")
	}
	if svc.classifier == nil {
		t.Fatalf("classifier = nil, want OpenAI client after reconfigure")
	}
	if svc.todoSuggester == nil {
		t.Fatalf("todoSuggester = nil, want OpenAI suggester after reconfigure")
	}
}

func TestApplyEditableSettingsResetsUsageWhenBackendChanges(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(_ context.Context, cfg config.AppConfig, backend config.AIBackend) aibackend.Status {
			return readyBackendStatus(firstNonZeroBackend(backend, cfg.EffectiveAIBackend()))
		},
	}

	svc.llmUsageTracker.Start("gpt-5-mini")
	svc.llmUsageTracker.Complete("gpt-5-mini", model.LLMUsage{
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
	})

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.AIBackend = config.AIBackendOpenCode

	svc.ApplyEditableSettings(settings)

	usage := svc.SessionUsage()
	if usage.Started != 0 || usage.Completed != 0 || usage.Failed != 0 || usage.Running != 0 {
		t.Fatalf("usage counters after backend switch = %+v, want all zero", usage)
	}
	if usage.Totals != (model.LLMUsage{}) {
		t.Fatalf("usage totals after backend switch = %+v, want zero totals", usage.Totals)
	}
}

func TestSessionUsageReturnsCachedSnapshotWhenServiceLockBusy(t *testing.T) {
	t.Parallel()

	svc := &Service{
		llmUsageTracker:        llm.NewUsageTracker(),
		commitMessageSuggester: fakeCommitMessageSuggester{},
	}

	svc.llmUsageTracker.Start("gpt-5-mini")
	svc.llmUsageTracker.Complete("gpt-5-mini", model.LLMUsage{
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
	})
	cached := svc.SessionUsage()
	if !cached.Enabled || cached.Completed != 1 || cached.Totals.TotalTokens != 150 {
		t.Fatalf("cached usage = %+v, want completed usage snapshot", cached)
	}

	svc.llmUsageTracker.Start("gpt-5-mini")
	svc.mu.Lock()
	defer svc.mu.Unlock()

	done := make(chan model.LLMSessionUsage, 1)
	go func() {
		done <- svc.SessionUsage()
	}()

	select {
	case got := <-done:
		if got.Running != cached.Running || got.Started != cached.Started || got.Totals.TotalTokens != cached.Totals.TotalTokens {
			t.Fatalf("SessionUsage() while service lock busy = %+v, want cached %+v", got, cached)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("SessionUsage() blocked while service lock was busy")
	}
}

func TestBossChatRunnerUsesBossChatBackendNotProjectAnalysisBackend(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenCode
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.BossChatModel = "gpt-5.4-mini"
	cfg.BossHelmModel = "gpt-5.5"
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossTextRunner()
	if runner == nil {
		t.Fatalf("NewBossTextRunner() runner = nil, want OpenAI API text runner")
	}
	if backend != config.AIBackendOpenAIAPI {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendOpenAIAPI)
	}
	if modelName != "gpt-5.5" {
		t.Fatalf("boss chat model = %q, want gpt-5.5", modelName)
	}
}

func TestBossChatRunnerKeepsBossChatModelAsLegacyHelmAlias(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenCode
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.BossChatModel = "gpt-5.4-mini"
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	_, modelName, backend := svc.NewBossTextRunner()
	if backend != config.AIBackendOpenAIAPI {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendOpenAIAPI)
	}
	if modelName != "gpt-5.4-mini" {
		t.Fatalf("legacy boss chat model = %q, want gpt-5.4-mini", modelName)
	}
}

func TestBossChatRunnerDefaultsToGPT56(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossTextRunner()
	if runner == nil {
		t.Fatalf("NewBossTextRunner() runner = nil, want OpenAI API text runner")
	}
	if backend != config.AIBackendOpenAIAPI {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendOpenAIAPI)
	}
	if modelName != config.DefaultBossHelmModel {
		t.Fatalf("boss chat model = %q, want %s", modelName, config.DefaultBossHelmModel)
	}
}

func TestBossUtilityRunnerDefaultsToLuna(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossUtilityJSONRunner()
	if runner == nil {
		t.Fatalf("NewBossUtilityJSONRunner() runner = nil, want OpenAI API structured runner")
	}
	if backend != config.AIBackendOpenAIAPI {
		t.Fatalf("boss utility backend = %s, want %s", backend, config.AIBackendOpenAIAPI)
	}
	if modelName != config.DefaultBossUtilityModel {
		t.Fatalf("boss utility model = %q, want %s", modelName, config.DefaultBossUtilityModel)
	}
}

func TestBossUtilityRunnerUsesConfiguredUtilityModel(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.BossUtilityModel = "gpt-5.4-nano"
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	_, modelName, backend := svc.NewBossUtilityJSONRunner()
	if backend != config.AIBackendOpenAIAPI {
		t.Fatalf("boss utility backend = %s, want %s", backend, config.AIBackendOpenAIAPI)
	}
	if modelName != "gpt-5.4-nano" {
		t.Fatalf("boss utility model = %q, want gpt-5.4-nano", modelName)
	}
}

func TestBossChatRunnerCanBeDisabledSeparately(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenCode
	cfg.BossChatBackend = config.AIBackendDisabled
	cfg.OpenAIAPIKey = "sk-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, _, backend := svc.NewBossTextRunner()
	if runner != nil {
		t.Fatalf("NewBossTextRunner() runner = %#v, want nil when boss chat is disabled", runner)
	}
	if backend != config.AIBackendDisabled {
		t.Fatalf("boss chat backend = %s, want disabled", backend)
	}
}

func TestBossChatRunnerSupportsLocalOpenAICompatibleBackend(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenCode
	cfg.BossChatBackend = config.AIBackendMLX
	cfg.MLXBaseURL = "http://127.0.0.1:8080/v1"
	cfg.MLXAPIKey = "mlx"
	cfg.MLXModel = "local-boss-model"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossTextRunner()
	if runner == nil {
		t.Fatalf("NewBossTextRunner() runner = nil, want local OpenAI-compatible text runner")
	}
	if backend != config.AIBackendMLX {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendMLX)
	}
	if modelName != "local-boss-model" {
		t.Fatalf("boss chat model = %q, want local-boss-model", modelName)
	}

	planner, plannerModel, plannerBackend := svc.NewBossJSONRunner()
	if planner == nil {
		t.Fatalf("NewBossJSONRunner() planner = nil, want local OpenAI-compatible structured runner")
	}
	if plannerBackend != config.AIBackendMLX {
		t.Fatalf("boss chat planner backend = %s, want %s", plannerBackend, config.AIBackendMLX)
	}
	if plannerModel != "local-boss-model" {
		t.Fatalf("boss chat planner model = %q, want local-boss-model", plannerModel)
	}
}

func TestBossChatOllamaThinkingDefaultsToNativeThinkOnlyForTextRunner(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	textRequests := make([]map[string]any, 0, 1)
	jsonRequests := make([]map[string]any, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/generate":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if _, hasFormat := req["format"]; hasFormat {
				jsonRequests = append(jsonRequests, req)
			} else {
				textRequests = append(textRequests, req)
			}
			_, _ = w.Write([]byte(`{
				"model":"gemma4:12b-mlx",
				"response":"done",
				"done":true,
				"done_reason":"stop",
				"prompt_eval_count":4,
				"eval_count":1
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendOllama
	cfg.OllamaBaseURL = server.URL + "/v1"
	cfg.OllamaModel = "gemma4:12b-mlx"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	textRunner, textModel, backend := svc.NewBossTextRunner()
	if backend != config.AIBackendOllama || textModel != "gemma4:12b-mlx" {
		t.Fatalf("text runner backend/model = %s/%q, want ollama/gemma4:12b-mlx", backend, textModel)
	}
	if _, err := textRunner.RunText(context.Background(), llm.TextRequest{
		Model: textModel,
		Messages: []llm.TextMessage{
			{Role: "user", Content: "answer"},
		},
	}); err != nil {
		t.Fatalf("RunText() error = %v", err)
	}

	jsonRunner, jsonModel, jsonBackend := svc.NewBossJSONRunner()
	if jsonBackend != config.AIBackendOllama || jsonModel != "gemma4:12b-mlx" {
		t.Fatalf("json runner backend/model = %s/%q, want ollama/gemma4:12b-mlx", jsonBackend, jsonModel)
	}
	if _, err := jsonRunner.RunJSONSchema(context.Background(), llm.JSONSchemaRequest{
		Model:      jsonModel,
		SystemText: "Return JSON.",
		UserText:   "Return status.",
		SchemaName: "status",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"status": map[string]any{"type": "string"}},
			"required":   []string{"status"},
		},
	}); err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}

	if len(textRequests) != 1 || textRequests[0]["think"] != true {
		t.Fatalf("text requests = %#v, want one request with think=true", textRequests)
	}
	if len(jsonRequests) != 1 || jsonRequests[0]["think"] != false {
		t.Fatalf("json requests = %#v, want one request with think=false", jsonRequests)
	}
}

func TestBossChatRunnerSupportsDirectDeepSeekBackend(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.BossChatBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "ds-test-example"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossTextRunner()
	if runner == nil {
		t.Fatalf("NewBossTextRunner() runner = nil, want DeepSeek text runner")
	}
	if backend != config.AIBackendDeepSeek {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendDeepSeek)
	}
	if modelName != config.DefaultDeepSeekProModel {
		t.Fatalf("boss chat model = %q, want %q", modelName, config.DefaultDeepSeekProModel)
	}

	planner, plannerModel, plannerBackend := svc.NewBossJSONRunner()
	if planner == nil {
		t.Fatalf("NewBossJSONRunner() planner = nil, want DeepSeek structured runner")
	}
	if plannerBackend != config.AIBackendDeepSeek {
		t.Fatalf("boss chat planner backend = %s, want %s", plannerBackend, config.AIBackendDeepSeek)
	}
	if plannerModel != config.DefaultDeepSeekProModel {
		t.Fatalf("boss chat planner model = %q, want %q", plannerModel, config.DefaultDeepSeekProModel)
	}

	utility, utilityModel, utilityBackend := svc.NewBossUtilityJSONRunner()
	if utility == nil {
		t.Fatalf("NewBossUtilityJSONRunner() runner = nil, want DeepSeek utility runner")
	}
	if utilityBackend != config.AIBackendDeepSeek {
		t.Fatalf("boss utility backend = %s, want %s", utilityBackend, config.AIBackendDeepSeek)
	}
	if utilityModel != config.DefaultDeepSeekModel {
		t.Fatalf("boss utility model = %q, want %q", utilityModel, config.DefaultDeepSeekModel)
	}
}

func TestBossChatRunnerUsesXiaomiProAndUtilityDefaults(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendXiaomi
	cfg.XiaomiAPIKey = "tc-test-example"
	cfg.XiaomiBaseURL = "https://token-plan-sgp.xiaomimimo.com/v1"
	svc := &Service{
		cfg:                  cfg,
		bossChatUsageTracker: llm.NewUsageTracker(),
	}

	runner, modelName, backend := svc.NewBossTextRunner()
	if runner == nil {
		t.Fatalf("NewBossTextRunner() runner = nil, want Xiaomi text runner")
	}
	if backend != config.AIBackendXiaomi {
		t.Fatalf("boss chat backend = %s, want %s", backend, config.AIBackendXiaomi)
	}
	if modelName != config.DefaultXiaomiProModel {
		t.Fatalf("boss chat model = %q, want %q", modelName, config.DefaultXiaomiProModel)
	}

	utility, utilityModel, utilityBackend := svc.NewBossUtilityJSONRunner()
	if utility == nil {
		t.Fatalf("NewBossUtilityJSONRunner() runner = nil, want Xiaomi utility runner")
	}
	if utilityBackend != config.AIBackendXiaomi {
		t.Fatalf("boss utility backend = %s, want %s", utilityBackend, config.AIBackendXiaomi)
	}
	if utilityModel != config.DefaultXiaomiModel {
		t.Fatalf("boss utility model = %q, want %q", utilityModel, config.DefaultXiaomiModel)
	}
}

func TestProjectReportsSupportDirectDeepSeekBackend(t *testing.T) {
	cfg := config.Default()
	cfg.AIBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "ds-test-example"
	svc := &Service{
		cfg:             cfg,
		llmUsageTracker: llm.NewUsageTracker(),
		backendDetector: func(_ context.Context, _ config.AppConfig, backend config.AIBackend) aibackend.Status {
			return aibackend.Status{Backend: backend, Ready: true, Installed: true, Authenticated: true}
		},
	}

	svc.configureAIClientsLocked()
	if svc.classifier == nil {
		t.Fatalf("classifier = nil, want DeepSeek-backed classifier manager")
	}
	if svc.commitMessageSuggester == nil {
		t.Fatalf("commitMessageSuggester = nil, want DeepSeek-backed suggester")
	}
	if svc.todoSuggester == nil {
		t.Fatalf("todoSuggester = nil, want DeepSeek-backed todo suggester")
	}
}
