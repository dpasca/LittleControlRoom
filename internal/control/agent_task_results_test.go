package control

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/model"
)

func TestStructuredResultValidationRejectsUnboundedOrInconsistentClaims(t *testing.T) {
	zero, one := 0, 1
	for _, checks := range [][]model.AgentTaskCheck{
		{{Command: "test", CWD: "/repo", Outcome: "passed"}},
		{{Command: "test", CWD: "/repo", Outcome: "passed", ExitCode: &one}},
		{{Command: "test", CWD: "/repo", Outcome: "failed", ExitCode: &zero}},
		{{Command: "test", CWD: "/repo", Outcome: "not_run", ExitCode: &zero}},
		make([]model.AgentTaskCheck, 21),
	} {
		if err := ValidateTaskResult(model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "claim", Checks: checks}); err == nil {
			t.Fatalf("accepted invalid checks: %+v", checks)
		}
	}
	for _, outcome := range []string{"ready_for_review", "blocked", "failed"} {
		if err := ValidateTaskResult(model.AgentTaskResultPayload{Outcome: outcome, Summary: "claim"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateTaskResult(model.AgentTaskResultPayload{Outcome: "failed", Summary: strings.Repeat("x", 2001)}); err == nil {
		t.Fatal("unbounded summary")
	}
	for _, raw := range []string{`{"task_id":"agt_x","run_id":1,"outcome":"failed","summary":"claim","reviewer":"operator"}`, `{"task_id":"agt_x","run_id":1,"outcome":"failed","summary":"claim"} {}`} {
		if _, err := ValidateInvocation(Invocation{Capability: CapabilityAgentTaskSubmitResult, Args: json.RawMessage(raw)}); err == nil {
			t.Fatal("accepted extra input")
		}
	}
}
