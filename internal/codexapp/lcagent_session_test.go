package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLCAgentSessionLaunchesConfiguredCommandAndStreamsTranscript(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	envPath := filepath.Join(t.TempDir(), "openrouter.env")
	if err := os.WriteFile(envPath, []byte("OPENROUTER_API_KEY=test\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_fake_session","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"model_response","model":"deepseek/test-model","usage":{"prompt_tokens":120,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens":30,"total_tokens":150},"usage_summary":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"cached_input_tokens":40}}'
printf '%s\n' '{"type":"tool_call","tool":"run_command"}'
printf '%s\n' '{"type":"tool_result","tool":"run_command","result":{"success":true,"output":"command ok"}}'
printf '%s\n' '{"type":"plan_update","items":[{"step":"exercise fake agent","status":"completed"}]}'
printf '%s\n' '{"type":"assistant_message","message":"fake lcagent response"}'
printf '%s\n' '{"type":"files_touched","files":["README.md"]}'
printf '%s\n' '{"type":"turn_complete"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}

	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:              ProviderLCAgent,
		ProjectPath:           root,
		AppDataDir:            dataDir,
		LCAgentPath:           exe,
		LCAgentEnvFile:        envPath,
		LCAgentProvider:       "deepseek",
		LCAgentAuto:           "medium",
		LCAgentToolProfile:    "generous",
		LCAgentContextProfile: "large",
		LCAgentRequestTimeout: 10 * time.Minute,
		PendingModel:          "deepseek/test-model",
		PendingReasoning:      "low",
		Prompt:                "please run the fake agent",
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	snapshot = waitForLCAgentTranscript(t, session, "Files touched:\nREADME.md")
	if snapshot.Provider != ProviderLCAgent {
		t.Fatalf("Provider = %q, want %q", snapshot.Provider, ProviderLCAgent)
	}
	if snapshot.ThreadID != "lca_fake_session" {
		t.Fatalf("ThreadID = %q, want fake session id", snapshot.ThreadID)
	}
	if snapshot.Busy {
		t.Fatalf("Busy = true, want false")
	}
	if snapshot.Model != "deepseek/test-model" || snapshot.ModelProvider != "deepseek" || snapshot.ReasoningEffort != "low" {
		t.Fatalf("model = %q/%q reasoning=%q, want deepseek/test-model/deepseek low", snapshot.ModelProvider, snapshot.Model, snapshot.ReasoningEffort)
	}
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.Last.InputTokens != 120 || snapshot.TokenUsage.Last.OutputTokens != 30 || snapshot.TokenUsage.Last.CachedInputTokens != 40 || snapshot.TokenUsage.Total.TotalTokens != 150 {
		t.Fatalf("TokenUsage = %#v", snapshot.TokenUsage)
	}
	for _, want := range []string{"please run the fake agent", "Tool run_command running", "command ok", "completed: exercise fake agent", "fake lcagent response", "Files touched:\nREADME.md"} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{
		"exec",
		"--cwd", root,
		"--data-dir", dataDir,
		"--auto", "medium",
		"--output", "stream-json",
		"--provider", "deepseek",
		"--model", "deepseek/test-model",
		"--tool-profile", "generous",
		"--context-profile", "large",
		"--request-timeout", "10m0s",
		"--reasoning-effort", "low",
		"--env-file", envPath,
		"please run the fake agent",
	} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestLCAgentSessionReplaysRequestedArtifact(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	sessionID := "lca_replay_requested"
	started := time.Date(2026, 5, 9, 9, 30, 0, 0, time.UTC)
	last := started.Add(6 * time.Second)
	writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{
			"type":       "session_meta",
			"id":         sessionID,
			"cwd":        root,
			"started_at": started.Format(time.RFC3339Nano),
			"provider":   "openrouter",
			"model":      "deepseek/replay-model",
		},
		{
			"type":       "user_message",
			"session_id": sessionID,
			"timestamp":  started.Add(time.Second).Format(time.RFC3339Nano),
			"message":    "summarize this repo",
		},
		{
			"type":       "tool_call",
			"session_id": sessionID,
			"timestamp":  started.Add(2 * time.Second).Format(time.RFC3339Nano),
			"tool":       "read_file",
		},
		{
			"type":       "model_response",
			"session_id": sessionID,
			"timestamp":  started.Add(2500 * time.Millisecond).Format(time.RFC3339Nano),
			"model":      "deepseek/replay-model",
			"usage_summary": map[string]any{
				"input_tokens":        200,
				"output_tokens":       50,
				"total_tokens":        250,
				"cached_input_tokens": 75,
			},
		},
		{
			"type":       "tool_result",
			"session_id": sessionID,
			"timestamp":  started.Add(3 * time.Second).Format(time.RFC3339Nano),
			"tool":       "read_file",
			"result": map[string]any{
				"success": true,
				"output":  "file: README.md\nlines: 1-1\n\n1 | hello from README\n",
			},
		},
		{
			"type":       "plan_update",
			"session_id": sessionID,
			"timestamp":  started.Add(4 * time.Second).Format(time.RFC3339Nano),
			"items": []map[string]any{{
				"step":   "inspect repo",
				"status": "done",
			}},
		},
		{
			"type":       "assistant_message",
			"session_id": sessionID,
			"timestamp":  started.Add(5 * time.Second).Format(time.RFC3339Nano),
			"message":    "Replay answer",
		},
		{
			"type":       "files_touched",
			"session_id": sessionID,
			"timestamp":  last.Format(time.RFC3339Nano),
			"files":      []string{"README.md"},
		},
		{
			"type":       "turn_complete",
			"session_id": sessionID,
			"timestamp":  last.Format(time.RFC3339Nano),
		},
	})

	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		ResumeID:    sessionID,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.ThreadID != sessionID {
		t.Fatalf("ThreadID = %q, want replayed session id", snapshot.ThreadID)
	}
	if !snapshot.Started || snapshot.Busy {
		t.Fatalf("Started/Busy = %v/%v, want replayed idle session", snapshot.Started, snapshot.Busy)
	}
	if snapshot.Status != "Loaded LCAgent session "+sessionID+" from disk" {
		t.Fatalf("Status = %q", snapshot.Status)
	}
	if !snapshot.LastActivityAt.Equal(last) {
		t.Fatalf("LastActivityAt = %s, want %s", snapshot.LastActivityAt, last)
	}
	if snapshot.Model != "deepseek/replay-model" || snapshot.ModelProvider != "openrouter" {
		t.Fatalf("model = %q/%q, want openrouter/deepseek replay model", snapshot.ModelProvider, snapshot.Model)
	}
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.Last.InputTokens != 200 || snapshot.TokenUsage.Last.OutputTokens != 50 || snapshot.TokenUsage.Last.CachedInputTokens != 75 || snapshot.TokenUsage.Total.TotalTokens != 250 {
		t.Fatalf("TokenUsage = %#v", snapshot.TokenUsage)
	}
	for _, want := range []string{
		"Loaded LCAgent session " + sessionID + " from disk. Sending a prompt starts a continuing run with summarized context.",
		"summarize this repo",
		"Tool read_file running",
		"README.md lines 1-1",
		"done: inspect repo",
		"Replay answer",
		"Files touched:\nREADME.md",
	} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
	if strings.Contains(snapshot.Transcript, "1 | hello from README") {
		t.Fatalf("transcript should summarize read_file contents instead of embedding them:\n%s", snapshot.Transcript)
	}
}

func TestLCAgentSessionContinuesLoadedReplayWithResumeArg(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	sessionID := "lca_replay_continue"
	started := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, started, sessionID, []map[string]any{
		{"type": "session_meta", "id": sessionID, "cwd": root, "started_at": started.Format(time.RFC3339Nano), "model": "deepseek/replay-model"},
		{"type": "assistant_message", "session_id": sessionID, "timestamp": started.Add(time.Second).Format(time.RFC3339Nano), "message": "loaded answer"},
		{"type": "turn_complete", "session_id": sessionID, "timestamp": started.Add(2 * time.Second).Format(time.RFC3339Nano), "summary": "loaded answer"},
	})

	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := `#!/bin/sh
{
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$LCAGENT_ARGS_FILE"
printf '%s\n' '{"type":"session_meta","id":"lca_new_after_resume","cwd":"/tmp/demo"}'
printf '%s\n' '{"type":"resume_context","source_session_id":"lca_replay_continue","summary":"source lca_replay_continue; summary: loaded answer"}'
printf '%s\n' '{"type":"assistant_message","message":"continued answer"}'
printf '%s\n' '{"type":"turn_complete"}'
`
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake lcagent: %v", err)
	}
	t.Setenv("LCAGENT_ARGS_FILE", argsPath)

	notify := make(chan struct{}, 20)
	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
		LCAgentPath: exe,
		ResumeID:    sessionID,
	}, func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}
	if err := session.Submit("continue the replay"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	snapshot := waitForLCAgentIdleSnapshot(t, session, notify)
	snapshot = waitForLCAgentTranscript(t, session, "continued answer")
	if snapshot.ThreadID != "lca_new_after_resume" {
		t.Fatalf("ThreadID = %q, want new resumed run id", snapshot.ThreadID)
	}
	for _, want := range []string{
		"Starting a continuing LCAgent run with summarized context from " + sessionID,
		"Loaded resume context from " + sessionID,
		"continued answer",
	} {
		if !strings.Contains(snapshot.Transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, snapshot.Transcript)
		}
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	for _, want := range []string{"--resume", sessionID, "continue the replay"} {
		if !lcagentTestStringSliceContains(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestLCAgentSessionReplaysLatestProjectArtifact(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	dataDir := t.TempDir()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	writeLCAgentReplayArtifact(t, dataDir, base, "lca_old", []map[string]any{
		{"type": "session_meta", "id": "lca_old", "cwd": root, "started_at": base.Format(time.RFC3339Nano), "model": "old-model"},
		{"type": "assistant_message", "session_id": "lca_old", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano), "message": "old answer"},
	})
	writeLCAgentReplayArtifact(t, dataDir, base.Add(time.Minute), "lca_other", []map[string]any{
		{"type": "session_meta", "id": "lca_other", "cwd": otherRoot, "started_at": base.Add(time.Minute).Format(time.RFC3339Nano), "model": "other-model"},
		{"type": "assistant_message", "session_id": "lca_other", "timestamp": base.Add(time.Minute + time.Second).Format(time.RFC3339Nano), "message": "other answer"},
	})
	writeLCAgentReplayArtifact(t, dataDir, base.Add(2*time.Minute), "lca_new", []map[string]any{
		{"type": "session_meta", "id": "lca_new", "cwd": root, "started_at": base.Add(2 * time.Minute).Format(time.RFC3339Nano), "model": "new-model"},
		{"type": "assistant_message", "session_id": "lca_new", "timestamp": base.Add(2*time.Minute + time.Second).Format(time.RFC3339Nano), "message": "new answer"},
	})

	session, err := newLCAgentSession(LaunchRequest{
		Provider:    ProviderLCAgent,
		ProjectPath: root,
		AppDataDir:  dataDir,
	}, nil)
	if err != nil {
		t.Fatalf("newLCAgentSession() error = %v", err)
	}

	snapshot := session.Snapshot()
	if snapshot.ThreadID != "lca_new" {
		t.Fatalf("ThreadID = %q, want latest project session", snapshot.ThreadID)
	}
	if !strings.Contains(snapshot.Transcript, "new answer") {
		t.Fatalf("transcript missing latest answer:\n%s", snapshot.Transcript)
	}
	if strings.Contains(snapshot.Transcript, "old answer") || strings.Contains(snapshot.Transcript, "other answer") {
		t.Fatalf("transcript should only replay latest matching project session:\n%s", snapshot.Transcript)
	}
}

func waitForLCAgentIdleSnapshot(t *testing.T, session Session, notify <-chan struct{}) Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snapshot := session.Snapshot()
		if snapshot.Started && !snapshot.Busy {
			return snapshot
		}
		select {
		case <-notify:
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for lcagent session to finish; snapshot=%#v", snapshot)
		}
	}
}

func waitForLCAgentTranscript(t *testing.T, session Session, want string) Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snapshot := session.Snapshot()
		if strings.Contains(snapshot.Transcript, want) {
			return snapshot
		}
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for lcagent transcript %q; snapshot=%#v", want, snapshot)
		}
	}
}

func writeLCAgentReplayArtifact(t *testing.T, dataDir string, started time.Time, sessionID string, events []map[string]any) string {
	t.Helper()
	path := filepath.Join(dataDir, "lcagent", "sessions", started.Format("2006"), started.Format("01"), started.Format("02"), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir replay artifact dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create replay artifact: %v", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("encode replay event: %v", err)
		}
	}
	return path
}

func lcagentTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
