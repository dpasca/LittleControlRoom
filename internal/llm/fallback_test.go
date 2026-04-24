package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModelFallbackRunnerSwitchesToHigherModelWithLowEffortOnCapacity(t *testing.T) {
	t.Parallel()

	baseRunner := &recordingJSONSchemaRunner{
		errs: []error{
			errors.New("model gpt-5.4-mini: Selected model is at capacity. Please try a different model."),
			nil,
		},
		response: JSONSchemaResponse{Model: "gpt-5.4", OutputText: `{"ok":true}`},
	}
	runner := NewCodexCapacityFallbackRunner(baseRunner)

	resp, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:           "gpt-5.4-mini",
		ReasoningEffort: "medium",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "demo",
		Schema:          map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if resp.Model != "gpt-5.4" {
		t.Fatalf("response model = %q, want gpt-5.4", resp.Model)
	}
	if len(baseRunner.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(baseRunner.requests))
	}
	if got := baseRunner.requests[0].Model; got != "gpt-5.4-mini" {
		t.Fatalf("first model = %q, want gpt-5.4-mini", got)
	}
	if got := baseRunner.requests[0].ReasoningEffort; got != "medium" {
		t.Fatalf("first reasoning effort = %q, want medium", got)
	}
	if got := baseRunner.requests[1].Model; got != "gpt-5.4" {
		t.Fatalf("fallback model = %q, want gpt-5.4", got)
	}
	if got := baseRunner.requests[1].ReasoningEffort; got != "low" {
		t.Fatalf("fallback reasoning effort = %q, want low", got)
	}
}

func TestModelFallbackRunnerKeepsExplicitNonMiniModel(t *testing.T) {
	t.Parallel()

	capacityErr := errors.New("model gpt-5.5: Selected model is at capacity. Please try a different model.")
	baseRunner := &recordingJSONSchemaRunner{
		errs: []error{capacityErr},
	}
	runner := NewCodexCapacityFallbackRunner(baseRunner)

	_, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:           "gpt-5.5",
		ReasoningEffort: "low",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "demo",
		Schema:          map[string]any{"type": "object"},
	})
	if !errors.Is(err, capacityErr) {
		t.Fatalf("RunJSONSchema() error = %v, want original capacity error", err)
	}
	if len(baseRunner.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(baseRunner.requests))
	}
	if got := baseRunner.requests[0].Model; got != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", got)
	}
}

func TestFallbackRunnerRetriesCapacityErrorsAcrossDiscoveredModels(t *testing.T) {
	t.Parallel()

	discovery := &OpenCodeDiscovery{
		models: []OpenCodeModelInfo{
			{
				ID:           "gpt-5.4-mini",
				ProviderID:   "openai",
				Status:       "active",
				Capabilities: OpenCodeModelCapabilities{TextInput: true},
			},
			{
				ID:           "gpt-5.4",
				ProviderID:   "openai",
				Status:       "active",
				InputCost:    1.0,
				OutputCost:   5.0,
				Capabilities: OpenCodeModelCapabilities{TextInput: true},
			},
		},
		providers:     []OpenCodeProviderInfo{{ID: "openai", Name: "OpenAI", Authenticated: true}},
		discoveredAt:  time.Now(),
		cacheDuration: time.Minute,
	}
	baseRunner := &recordingJSONSchemaRunner{
		errs: []error{
			errors.New("model gpt-5.4-mini: Selected model is at capacity. Please try a different model."),
			nil,
		},
		response: JSONSchemaResponse{Model: "openai/gpt-5.4", OutputText: `{"ok":true}`},
	}
	runner := NewFallbackRunner(discovery, baseRunner, DefaultModelSelectionConfig(), nil)

	resp, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		SystemText: "system",
		UserText:   "user",
		SchemaName: "demo",
		Schema:     map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if resp.Model != "openai/gpt-5.4" {
		t.Fatalf("response model = %q, want openai/gpt-5.4", resp.Model)
	}
	if len(baseRunner.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(baseRunner.requests))
	}
	if got := baseRunner.requests[0].Model; got != "openai/gpt-5.4-mini" {
		t.Fatalf("first request model = %q, want openai/gpt-5.4-mini", got)
	}
	if got := baseRunner.requests[1].Model; got != "openai/gpt-5.4" {
		t.Fatalf("fallback request model = %q, want openai/gpt-5.4", got)
	}
}

func TestIsModelCapacityErrorRecognizesCodexDiagnostic(t *testing.T) {
	t.Parallel()

	err := errors.New("model gpt-5.4-mini: Selected model is at capacity. Please try a different model.")
	if !IsModelCapacityError(err) {
		t.Fatalf("IsModelCapacityError() = false, want true")
	}
	if IsModelCapacityError(errors.New("model returned invalid JSON")) {
		t.Fatalf("IsModelCapacityError() = true for unrelated error")
	}
}
