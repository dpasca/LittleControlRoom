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

func TestWorkerDelegationScopeAndExclusions(t *testing.T) {
	for _, test := range []struct {
		name string
		cap  CapabilityName
		args string
		want bool
	}{
		{"default launch", CapabilityAgentTaskCreate, `{"title":"Work","prompt":"Implement"}`, true},
		{"explicit model", CapabilityAgentTaskCreate, `{"title":"Work","provider":"codex","model":"approved-model","repository_write":true,"structured_results":true}`, true},
		{"model picker", CapabilityAgentTaskCreate, `{"title":"Work","select_model":true}`, false},
		{"follow-up", CapabilityAgentTaskContinue, `{"task_id":"worker","prompt":"Continue","session_mode":"resume_or_new"}`, true},
		{"new worker session", CapabilityAgentTaskContinue, `{"task_id":"worker","prompt":"Continue","session_mode":"new"}`, false},
		{"complete", CapabilityAgentTaskClose, `{"task_id":"worker","status":"completed"}`, true},
		{"waiting", CapabilityAgentTaskClose, `{"task_id":"worker","status":"waiting"}`, true},
		{"trash", CapabilityAgentTaskClose, `{"task_id":"worker","status":"archived"}`, false},
		{"close session", CapabilityAgentTaskClose, `{"task_id":"worker","status":"completed","close_session":true}`, false},
		{"message to project", CapabilityEngineerSendPrompt, `{"project_path":"/other","prompt":"Work"}`, false},
		{"worktree launch", CapabilityTodoCreateWorktreeAndStartEngineer, `{"project_path":"/other","todo_text":"Work"}`, false},
		{"delete worktree", CapabilityWorktreeRemove, `{"worktree_path":"/other"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := Operation{ProjectPath: "/caller", Provider: "claude_code", SessionKey: "organizer", Invocation: Invocation{Capability: test.cap, Args: json.RawMessage(test.args)}}
			p, ok := WorkerDelegationForOperation(op)
			if ok != test.want {
				t.Fatalf("eligible = %v, want %v", ok, test.want)
			}
			if ok && (p.Scope != PermissionScopeSessionWorkers || p.Origin != op.ProjectPath || p.Provider != op.Provider || p.SessionKey != op.SessionKey || p.Limit != 0) {
				t.Fatalf("wrong scope: %+v", p)
			}
			for _, anonymous := range []Operation{
				{ProjectPath: op.ProjectPath, Provider: op.Provider, Invocation: op.Invocation},
				{ProjectPath: op.ProjectPath, SessionKey: op.SessionKey, Invocation: op.Invocation},
				{Provider: op.Provider, SessionKey: op.SessionKey, Invocation: op.Invocation},
			} {
				if _, ok := WorkerDelegationForOperation(anonymous); ok {
					t.Fatal("anonymous caller eligible")
				}
			}
		})
	}
}
