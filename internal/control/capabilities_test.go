package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		raw  string
		want Provider
	}{
		{raw: "", want: ProviderAuto},
		{raw: " auto ", want: ProviderAuto},
		{raw: "CODEX", want: ProviderCodex},
		{raw: "open-code", want: ProviderOpenCode},
		{raw: "open_code", want: ProviderOpenCode},
		{raw: "OpenCode", want: ProviderOpenCode},
		{raw: "claude", want: ProviderClaudeCode},
		{raw: "claude-code", want: ProviderClaudeCode},
		{raw: "claude_code", want: ProviderClaudeCode},
		{raw: "other", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeProvider(tt.raw); got != tt.want {
			t.Fatalf("NormalizeProvider(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeSessionMode(t *testing.T) {
	tests := []struct {
		raw  string
		want SessionMode
	}{
		{raw: "", want: SessionModeResumeOrNew},
		{raw: "resume", want: SessionModeResumeOrNew},
		{raw: "resume-or-new", want: SessionModeResumeOrNew},
		{raw: "resume_or_new", want: SessionModeResumeOrNew},
		{raw: "new", want: SessionModeNew},
		{raw: "fresh", want: SessionModeNew},
		{raw: "force-new", want: SessionModeNew},
		{raw: "bogus", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeSessionMode(tt.raw); got != tt.want {
			t.Fatalf("NormalizeSessionMode(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestEngineerSendPromptCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityEngineerSendPrompt)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityEngineerSendPrompt)
	}
	if capability.Name != CapabilityEngineerSendPrompt {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityEngineerSendPrompt)
	}
	if capability.Risk != RiskExternal {
		t.Fatalf("Risk = %q, want %q", capability.Risk, RiskExternal)
	}
	if capability.Confirmation != ConfirmationRequired {
		t.Fatalf("Confirmation = %q, want %q", capability.Confirmation, ConfirmationRequired)
	}
	if capability.RequiresHost {
		t.Fatalf("RequiresHost = true, want false")
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayRevealEngineerSession) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayRevealEngineerSession)
	}
	providers := map[Provider]ProviderCapability{}
	for _, provider := range capability.Providers {
		providers[provider.ID] = provider
	}
	if !providers[ProviderCodex].Available {
		t.Fatalf("Codex provider should be available in default metadata")
	}
	if !providers[ProviderOpenCode].Available {
		t.Fatalf("OpenCode provider should be available in default metadata")
	}
	if providers[ProviderClaudeCode].Available {
		t.Fatalf("Claude Code provider should be disabled in default metadata")
	}
	if !stringSliceContains(providers[ProviderCodex].Features, FeatureCompact) {
		t.Fatalf("Codex features = %#v, want compact support", providers[ProviderCodex].Features)
	}
	if stringSliceContains(providers[ProviderOpenCode].Features, FeatureCompact) {
		t.Fatalf("OpenCode features = %#v, did not expect compact support in default metadata", providers[ProviderOpenCode].Features)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("capability schemas should be object schemas")
	}
}

func TestAgentTaskCapabilitiesMetadata(t *testing.T) {
	for _, name := range []CapabilityName{CapabilityAgentTaskCreate, CapabilityAgentTaskContinue, CapabilityAgentTaskClose} {
		capability, ok := CapabilityByName(name)
		if !ok {
			t.Fatalf("CapabilityByName(%q) not found", name)
		}
		if capability.Name != name {
			t.Fatalf("Name = %q, want %q", capability.Name, name)
		}
		if capability.Confirmation != ConfirmationRequired {
			t.Fatalf("%s confirmation = %q, want required", name, capability.Confirmation)
		}
		if !capability.RequiresHost {
			t.Fatalf("%s RequiresHost = false, want true", name)
		}
		if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
			t.Fatalf("%s schemas should be object schemas", name)
		}
	}
}

func TestNormalizeEngineerSendPromptInput(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(EngineerSendPromptInput{
		RequestID:   " req-1 ",
		ProjectPath: " /tmp/demo/../demo ",
		Provider:    "",
		SessionMode: "",
		Prompt:      "  Please run the focused tests.  ",
	})
	if err != nil {
		t.Fatalf("NormalizeEngineerSendPromptInput() error = %v", err)
	}
	if input.RequestID != "req-1" {
		t.Fatalf("RequestID = %q, want req-1", input.RequestID)
	}
	if input.ProjectPath != "/tmp/demo" {
		t.Fatalf("ProjectPath = %q, want /tmp/demo", input.ProjectPath)
	}
	if input.Provider != ProviderAuto {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderAuto)
	}
	if input.SessionMode != SessionModeResumeOrNew {
		t.Fatalf("SessionMode = %q, want %q", input.SessionMode, SessionModeResumeOrNew)
	}
	if input.Prompt != "Please run the focused tests." {
		t.Fatalf("Prompt = %q, want trimmed prompt", input.Prompt)
	}
}

func TestNormalizeEngineerSendPromptInputAllowsProjectNameOnly(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(EngineerSendPromptInput{
		ProjectName: " Little Control Room ",
		Provider:    ProviderOpenCode,
		SessionMode: SessionModeNew,
		Prompt:      "Start a fresh investigation.",
	})
	if err != nil {
		t.Fatalf("NormalizeEngineerSendPromptInput() error = %v", err)
	}
	if input.ProjectName != "Little Control Room" {
		t.Fatalf("ProjectName = %q, want trimmed name", input.ProjectName)
	}
	if input.Provider != ProviderOpenCode {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderOpenCode)
	}
}

func TestNormalizeEngineerSendPromptInputRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input EngineerSendPromptInput
		want  string
	}{
		{
			name:  "missing project",
			input: EngineerSendPromptInput{Prompt: "Do the thing"},
			want:  "project_path or project_name is required",
		},
		{
			name:  "missing prompt",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo"},
			want:  "prompt is required",
		},
		{
			name:  "bad provider",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo", Provider: "other", Prompt: "Do the thing"},
			want:  "unsupported engineer provider",
		},
		{
			name:  "bad session mode",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo", SessionMode: "later", Prompt: "Do the thing"},
			want:  "unsupported engineer session mode",
		},
	}
	for _, tt := range tests {
		_, err := NormalizeEngineerSendPromptInput(tt.input)
		if err == nil {
			t.Fatalf("%s: error = nil, want %q", tt.name, tt.want)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: error = %q, want containing %q", tt.name, err.Error(), tt.want)
		}
	}
}

func TestValidateInvocationNormalizesEngineerSendPromptArgs(t *testing.T) {
	rawArgs := json.RawMessage(`{
		"project_path": " /tmp/demo ",
		"project_name": "",
		"provider": "opencode",
		"session_mode": "new",
		"prompt": "  Fix the failing test.  ",
		"reveal": true
	}`)
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-1 ",
		Capability: CapabilityEngineerSendPrompt,
		Args:       rawArgs,
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-1" {
		t.Fatalf("RequestID = %q, want boss-turn-1", inv.RequestID)
	}
	var input EngineerSendPromptInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-1" {
		t.Fatalf("args request_id = %q, want boss-turn-1", input.RequestID)
	}
	if input.Provider != ProviderOpenCode {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderOpenCode)
	}
	if input.SessionMode != SessionModeNew {
		t.Fatalf("SessionMode = %q, want %q", input.SessionMode, SessionModeNew)
	}
	if input.Prompt != "Fix the failing test." {
		t.Fatalf("Prompt = %q, want trimmed prompt", input.Prompt)
	}
	if !input.Reveal {
		t.Fatalf("Reveal = false, want true")
	}
}

func TestValidateInvocationNormalizesAgentTaskCreateArgs(t *testing.T) {
	rawArgs := json.RawMessage(`{
		"title": " Clean suspicious processes ",
		"kind": "",
		"parent_task_id": "",
		"prompt": "  Inspect and terminate stale PIDs.  ",
		"provider": "",
		"reveal": false,
		"capabilities": ["process.inspect", "process.terminate", "process.inspect"],
		"resources": [
			{"kind": "process", "id": "", "path": "", "project_path": "", "provider": "", "session_id": "", "todo_id": 0, "pid": 93624, "port": 0, "label": " hot python "},
			{"kind": "port", "id": "", "path": "", "project_path": "", "provider": "", "session_id": "", "todo_id": 0, "pid": 0, "port": 9229, "label": " debug "}
		]
	}`)
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-task ",
		Capability: CapabilityAgentTaskCreate,
		Args:       rawArgs,
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	var input AgentTaskCreateInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-task" || input.Title != "Clean suspicious processes" {
		t.Fatalf("normalized identity = %#v", input)
	}
	if input.Kind != AgentTaskKindAgent || input.Provider != ProviderAuto {
		t.Fatalf("kind/provider = %q/%q", input.Kind, input.Provider)
	}
	if len(input.Capabilities) != 2 {
		t.Fatalf("capabilities = %#v, want deduped list", input.Capabilities)
	}
	if len(input.Resources) != 2 || input.Resources[0].PID != 93624 || input.Resources[1].Port != 9229 {
		t.Fatalf("resources = %#v", input.Resources)
	}
}

func TestValidateInvocationRejectsUnknownCapabilityAndRequestIDMismatch(t *testing.T) {
	if _, err := ValidateInvocation(Invocation{Capability: "project.do_anything"}); err == nil {
		t.Fatalf("ValidateInvocation() error = nil, want unsupported capability error")
	}
	_, err := ValidateInvocation(Invocation{
		RequestID:  "outer",
		Capability: CapabilityEngineerSendPrompt,
		Args:       json.RawMessage(`{"request_id":"inner","project_path":"/tmp/demo","project_name":"","provider":"auto","session_mode":"resume_or_new","prompt":"Do it","reveal":false}`),
	})
	if err == nil {
		t.Fatalf("ValidateInvocation() error = nil, want request_id mismatch")
	}
	if !strings.Contains(err.Error(), "request_id mismatch") {
		t.Fatalf("error = %q, want request_id mismatch", err.Error())
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
