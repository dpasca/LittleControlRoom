package control

import (
	"encoding/json"
	"testing"
)

func TestCurrentSessionAddressingValidationAndPermission(t *testing.T) {
	base := EngineerSendPromptInput{ProjectPath: "/target", Provider: ProviderCodex, SessionMode: SessionModeResumeOrNew, FollowCurrentSession: true, Prompt: "Project checkpoint"}
	for _, tc := range []struct {
		name  string
		edit  func(*EngineerSendPromptInput)
		valid bool
	}{
		{"current", func(*EngineerSendPromptInput) {}, true},
		{"exact and current", func(i *EngineerSendPromptInput) { i.TargetSessionID = "old-thread" }, false},
		{"new", func(i *EngineerSendPromptInput) { i.SessionMode = SessionModeNew }, false},
		{"auto provider", func(i *EngineerSendPromptInput) { i.Provider = ProviderAuto }, false},
		{"model", func(i *EngineerSendPromptInput) { i.Model = "changed" }, false},
		{"todo", func(i *EngineerSendPromptInput) { i.TodoID = 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.edit(&input)
			_, err := NormalizeEngineerSendPromptInput(input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	permission := func(input EngineerSendPromptInput) ControlPermission {
		t.Helper()
		args, _ := json.Marshal(input)
		p, ok := PermissionForOperation(Operation{ProjectPath: "/caller", Provider: "codex", SessionKey: "caller-key", Invocation: Invocation{Capability: CapabilityEngineerSendPrompt, Args: args}})
		if !ok {
			t.Fatal("message did not qualify for a scoped permission")
		}
		return p
	}
	current := permission(base)
	base.FollowCurrentSession = false
	base.TargetSessionID = "exact"
	exact := permission(base)
	if current.Constraints == exact.Constraints {
		t.Fatal("current-session addressing silently widened an existing exact-session permission")
	}
}
