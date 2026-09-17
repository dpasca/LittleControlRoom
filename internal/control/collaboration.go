package control

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const ConfirmationProjectCollaboration = "project_collaboration"

// ProjectCollaboration grants bidirectional messaging between two exact LCR
// project paths. It does not grant other control capabilities or inherit to
// linked worktrees, which are separate projects in LCR.
type ProjectCollaboration struct {
	ProjectA string `json:"project_a"`
	ProjectB string `json:"project_b"`
}

func CollaborationPair(a, b string) (ProjectCollaboration, bool) {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return ProjectCollaboration{}, false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return ProjectCollaboration{}, false
	}
	if a > b {
		a, b = b, a
	}
	return ProjectCollaboration{ProjectA: a, ProjectB: b}, true
}

// CollaborationForOperation uses the host-bound caller path, never an origin
// supplied in tool arguments. Only messages to an inspected exact session are
// eligible; launches, model changes and TODO-based target redirects still ask.
func CollaborationForOperation(op Operation) (ProjectCollaboration, bool) {
	if op.Capability != CapabilityEngineerSendPrompt || op.SessionKey == "" || op.Provider == "" {
		return ProjectCollaboration{}, false
	}
	inv, err := ValidateInvocation(op.Invocation)
	if err != nil || inv.Capability != CapabilityEngineerSendPrompt {
		return ProjectCollaboration{}, false
	}
	var input EngineerSendPromptInput
	if json.Unmarshal(inv.Args, &input) != nil || input.SessionMode != SessionModeResumeOrNew ||
		input.TargetSessionID == "" || input.Provider == ProviderAuto || input.TodoID != 0 ||
		input.EngineerModelSelection != (EngineerModelSelection{}) {
		return ProjectCollaboration{}, false
	}
	return CollaborationPair(op.ProjectPath, input.ProjectPath)
}
