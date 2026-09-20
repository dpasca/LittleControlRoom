package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func TestEngineerSessionBindingIsImmutableAndScoped(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	if err := st.BindEngineerSession(ctx, "/caller/./tree", model.SessionSourceClaudeCode, "mcp-key", "claude-thread"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindEngineerSession(ctx, "/caller/tree", model.SessionSourceClaudeCode, "mcp-key", "claude-thread"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindEngineerSession(ctx, "/caller/tree", model.SessionSourceClaudeCode, "mcp-key", "new-thread"); err == nil {
		t.Fatal("accepted conflicting identity")
	}
	for _, tc := range []struct {
		path      string
		provider  model.SessionSource
		key, want string
	}{
		{"/caller/tree", model.SessionSourceClaudeCode, "mcp-key", "claude-thread"},
		{"/caller/tree", model.SessionSourceCodex, "mcp-key", ""},
		{"/caller/other", model.SessionSourceClaudeCode, "mcp-key", ""},
		{"/caller/tree", model.SessionSourceClaudeCode, "claude-thread", ""},
	} {
		got, err := st.ResolveEngineerSession(ctx, tc.path, tc.provider, tc.key)
		if err != nil || got != tc.want {
			t.Fatalf("lookup %#v = %q, %v", tc, got, err)
		}
	}
}

func TestAgentTaskLegacyCallerRequiresExactArtifactEvidence(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "ambiguous"
		if known {
			name = "recorded-provider-id"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "state.sqlite")
			st, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { st.Close() }()
			const project = "/caller/worktree"
			if err := st.UpsertProjectState(ctx, model.ProjectState{Path: project, Name: "caller", Status: model.StatusIdle, PresentOnDisk: true, UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			// Even an exact ID on a different provider is not evidence for this caller.
			source := "codex"
			if known {
				source = "claude_code"
			}
			if _, err := st.db.ExecContext(ctx, `INSERT INTO project_sessions(session_id, raw_session_id, source, project_path, session_file, format, last_event_at, error_count, updated_at)
    VALUES (?, 'old-key', ?, ?, '/recorded.jsonl', 'claude_code', 1, 0, 1)`, source+":old-key", source, project); err != nil {
				t.Fatal(err)
			}
			op, err := st.CreateControlOperation(ctx, control.Operation{ID: "lcrop_legacy", Provider: "claude_code", SessionKey: "old-key", ProjectPath: project,
				Invocation: control.Invocation{RequestID: "lcrop_legacy", Capability: control.CapabilityAgentTaskCreate, Args: json.RawMessage(`{"title":"Legacy worker","kind":"agent"}`)}, Status: control.OperationProposed})
			if err != nil {
				t.Fatal(err)
			}
			task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "Legacy worker", OriginOperationID: op.ID, OriginWorktreePath: project, OriginProvider: model.SessionSourceClaudeCode, OriginSessionID: "old-key"})
			if err != nil {
				t.Fatal(err)
			}
			ready := time.Now()
			if _, err := st.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, ResultReadyAt: &ready}); err != nil {
				t.Fatal(err)
			}
			msg, err := st.CreateEngineerMessage(ctx, control.EngineerMessage{AgentTaskID: task.ID, ProjectPath: project, Provider: control.ProviderClaudeCode, SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "old-key", Prompt: "Review result"})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := st.GetAgentTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.OriginSessionKey != "old-key" {
				t.Fatalf("lost legacy control key: %#v", recovered)
			}
			message, err := st.GetEngineerMessage(ctx, msg.ID)
			if err != nil {
				t.Fatal(err)
			}
			if known {
				if recovered.OriginSessionID != "old-key" || message.State != control.EngineerMessageQueued {
					t.Fatalf("valid legacy caller was blocked: %#v / %#v", recovered, message)
				}
			} else if recovered.OriginSessionID != "" || message.State != control.EngineerMessageFailed || recovered.ResultDeliveryError == "" {
				t.Fatalf("ambiguous callback could dispatch: %#v / %#v", recovered, message)
			}
			// Reopening twice must preserve the migration's outcome.
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			again, err := st.GetAgentTask(ctx, task.ID)
			if err != nil || again.OriginSessionID != recovered.OriginSessionID || again.ResultMessageID != recovered.ResultMessageID {
				t.Fatalf("migration is not idempotent: %#v, %v", again, err)
			}
		})
	}
}
