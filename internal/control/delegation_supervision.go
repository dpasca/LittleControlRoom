package control

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// MaxSupervisedCorrections bounds the grant an operator can agree to in one
// confirmation. Raising it is a product decision, not a caller's to make.
const MaxSupervisedCorrections = 3

const ConfirmationDelegationSupervision = "delegation_supervision"

// DelegationCorrection is the exact shape of continuation a supervision grant
// may authorize: the same caller reopening the same task, in its existing
// session, with its saved model choice, to answer a review it already recorded.
type DelegationCorrection struct {
	TaskID      string `json:"task_id"`
	ProjectPath string `json:"project_path"`
	Provider    string `json:"provider"`
	SessionKey  string `json:"session_key"`
}

// DelegationCorrectionForOperation reports the correction an operation would
// perform, using host-bound caller identity rather than anything the agent
// supplied. It deliberately refuses anything that would widen scope: a fresh
// session, a provider or model change, or a redirect to another target. The
// task's own state is checked separately, against the database.
func DelegationCorrectionForOperation(op Operation) (DelegationCorrection, bool) {
	if op.Capability != CapabilityAgentTaskContinue || op.SessionKey == "" || op.Provider == "" {
		return DelegationCorrection{}, false
	}
	if !filepath.IsAbs(strings.TrimSpace(op.ProjectPath)) {
		return DelegationCorrection{}, false
	}
	inv, err := ValidateInvocation(op.Invocation)
	if err != nil || inv.Capability != CapabilityAgentTaskContinue {
		return DelegationCorrection{}, false
	}
	var input AgentTaskContinueInput
	if json.Unmarshal(inv.Args, &input) != nil {
		return DelegationCorrection{}, false
	}
	if input.TaskID == "" || input.SessionMode != SessionModeResumeOrNew {
		return DelegationCorrection{}, false
	}
	// Only the inherited provider and saved model choice are covered. Naming a
	// provider or model explicitly is a new decision for the operator, even
	// inside a granted correction round.
	if input.Provider != ProviderAuto || input.EngineerModelSelection != (EngineerModelSelection{}) {
		return DelegationCorrection{}, false
	}
	return DelegationCorrection{
		TaskID:      input.TaskID,
		ProjectPath: filepath.Clean(strings.TrimSpace(op.ProjectPath)),
		Provider:    op.Provider,
		SessionKey:  op.SessionKey,
	}, true
}
