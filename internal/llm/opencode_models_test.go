package llm

import (
	"testing"
)

func TestParseOpenCodeModelsOutput(t *testing.T) {
	raw := `opencode/mimo-v2-omni-free
{
  "id": "mimo-v2-omni-free",
  "providerID": "opencode",
  "name": "MiMo V2 Omni Free",
  "family": "mimo-omni-free",
  "status": "active",
  "cost": {
    "input": 0,
    "output": 0,
    "cache": {
      "read": 0,
      "write": 0
    }
  },
  "limit": {
    "context": 262144,
    "output": 64000
  },
  "capabilities": {
    "temperature": true,
    "reasoning": true,
    "toolcall": true,
    "input": {
      "text": true,
      "image": true
    }
  }
}
`
	models, err := parseOpenCodeModelsOutput(raw)
	if err != nil {
		t.Fatalf("parseOpenCodeModelsOutput error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "mimo-v2-omni-free" {
		t.Errorf("expected ID 'mimo-v2-omni-free', got %q", models[0].ID)
	}
	if models[0].ProviderID != "opencode" {
		t.Errorf("expected ProviderID 'opencode', got %q", models[0].ProviderID)
	}
	if models[0].InputCost != 0 {
		t.Errorf("expected InputCost 0, got %f", models[0].InputCost)
	}
	if !models[0].Capabilities.TextInput {
		t.Errorf("expected TextInput true, got false")
	}
}

func TestParseOpenCodeProvidersOutput(t *testing.T) {
	raw := "\x1b[0m\n┌  Credentials \x1b[90m~/.local/share/opencode/auth.json\n│\n●  OpenAI \x1b[90moauth\n│\n●  MiniMax (minimax.io) \x1b[90mapi\n│\n●  OpenCode Zen \x1b[90mapi\n│\n└  3 credentials\n\n┌  Environment\n│\n●  OpenAI \x1b[90mOPENAI_API_KEY\n│\n└  1 environment variable\n"

	providers, err := parseOpenCodeProvidersOutput(raw)
	if err != nil {
		t.Fatalf("parseOpenCodeProvidersOutput error: %v", err)
	}
	if len(providers) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(providers))
	}

	expectedProviders := []struct {
		id   string
		name string
	}{
		{"openai", "OpenAI"},
		{"minimax", "MiniMax"},
		{"opencode-zen", "OpenCode Zen"},
		{"openai", "OpenAI"},
	}

	for i, expected := range expectedProviders {
		if providers[i].ID != expected.id {
			t.Errorf("provider %d: expected ID %q, got %q", i, expected.id, providers[i].ID)
		}
		if providers[i].Name != expected.name {
			t.Errorf("provider %d: expected Name %q, got %q", i, expected.name, providers[i].Name)
		}
	}
}
