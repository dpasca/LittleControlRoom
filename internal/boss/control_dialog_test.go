package boss

import (
	"encoding/json"
	"lcroom/internal/control"
	"strings"
	"testing"
)

func TestCollaborationConfirmationShowsPromptOnStandardTerminal(t *testing.T) {
	args, _ := json.Marshal(control.EngineerSendPromptInput{ProjectPath: "/projects/crypto", Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "session", Prompt: "Fix the build incompatibility."})
	view, err := RenderCollaborationConfirmationDialog(control.Invocation{Capability: control.CapabilityEngineerSendPrompt, Args: args}, "/projects/crypto_desk", false, "", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"always allow this pair", "Fix the build incompatibility.", "crypto_desk", "/collab"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %s", want, view)
		}
	}
}
