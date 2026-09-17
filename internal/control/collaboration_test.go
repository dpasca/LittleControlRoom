package control

import (
	"encoding/json"
	"testing"
)

func TestCollaborationRequiresExactSessionAndHostOrigin(t *testing.T) {
	base := EngineerSendPromptInput{ProjectPath: "/repos/b", Provider: ProviderCodex, SessionMode: SessionModeResumeOrNew, TargetSessionID: "session-b", Prompt: "Do the authorized work"}
	for _, tc := range []struct {
		name   string
		edit   func(*EngineerSendPromptInput)
		origin string
		want   bool
	}{
		{"exact", func(*EngineerSendPromptInput) {}, "/repos/a", true},
		{"relative origin", func(*EngineerSendPromptInput) {}, "a", false},
		{"same project", func(*EngineerSendPromptInput) {}, "/repos/b", false},
		{"fresh launch", func(i *EngineerSendPromptInput) { i.SessionMode = SessionModeNew; i.TargetSessionID = "" }, "/repos/a", false},
		{"unbound session", func(i *EngineerSendPromptInput) { i.TargetSessionID = "" }, "/repos/a", false},
		{"auto provider", func(i *EngineerSendPromptInput) { i.Provider = ProviderAuto }, "/repos/a", false},
		{"todo redirect", func(i *EngineerSendPromptInput) { i.TodoID = 42 }, "/repos/a", false},
		{"model picker", func(i *EngineerSendPromptInput) { i.SelectModel = true }, "/repos/a", false},
		{"model change", func(i *EngineerSendPromptInput) { i.Model = "different-model" }, "/repos/a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.edit(&input)
			args, _ := json.Marshal(input)
			op := Operation{Capability: CapabilityEngineerSendPrompt, ProjectPath: tc.origin, Provider: "codex", SessionKey: "sender", Invocation: Invocation{Capability: CapabilityEngineerSendPrompt, Args: args}}
			_, ok := CollaborationForOperation(op)
			if ok != tc.want {
				t.Fatalf("eligible=%v, want=%v", ok, tc.want)
			}
			op.Capability = CapabilityTodoAdd
			if _, ok := CollaborationForOperation(op); ok {
				t.Fatal("other capability eligible")
			}
		})
	}
}
