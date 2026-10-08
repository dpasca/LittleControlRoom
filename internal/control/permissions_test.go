package control

import (
	"encoding/json"
	"testing"
)

func TestPermissionDoesNotCoverDestructiveOrUnboundedActions(t *testing.T) {
	for _, test := range []struct {
		cap  CapabilityName
		args string
	}{
		{CapabilityWorktreeRemove, `{"worktree_path":"/target"}`},
		{CapabilityAgentTaskCreate, `{"title":"work","prompt":"work","provider":"codex"}`},
		{CapabilityEngineerSendPrompt, `{"project_path":"/target","prompt":"work","provider":"codex","session_mode":"new"}`},
		{CapabilityEngineerSendPrompt, `{"project_path":"/target","prompt":"work","provider":"codex","session_mode":"resume_or_new","target_session_id":"target","model":"changed"}`},
		{CapabilityAgentTaskClose, `{"task_id":"task","status":"archived"}`},
	} {
		t.Run(string(test.cap)+test.args, func(t *testing.T) {
			op := Operation{ProjectPath: "/caller", Provider: "codex", SessionKey: "key", Invocation: Invocation{RequestID: "op", Capability: test.cap, Args: json.RawMessage(test.args)}}
			if _, ok := PermissionForOperation(op); ok {
				t.Fatal("unexpected reusable permission")
			}
		})
	}
}
