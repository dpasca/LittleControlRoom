package codexapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
)

func TestLCAgentStagesSettingsWithoutInterruptingRun(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{
		{"deepseek", "deepseek-flash"},
		{"moonshot", "kimi-k3"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			root, dataDir := t.TempDir(), t.TempDir()
			gate := filepath.Join(t.TempDir(), "finish-turn")
			argsPath := filepath.Join(t.TempDir(), "args")
			exe := filepath.Join(t.TempDir(), "lcagent")
			t.Setenv("LCAGENT_STAGE_GATE", gate)
			t.Setenv("LCAGENT_STAGE_ARGS", argsPath)
			script := `#!/bin/sh
printf '%s\n' "$@" > "$LCAGENT_STAGE_ARGS"
printf '%s\n' '{"type":"session_meta","id":"lca_staging","thread_id":"lct_staging"}'
printf '%s\n' '{"type":"assistant_message","message":"Waiting for turn release"}'
while [ ! -f "$LCAGENT_STAGE_GATE" ]; do sleep 0.01; done
printf '%s\n' '{"type":"assistant_message","message":"Finished the original turn"}'
printf '%s\n' '{"type":"turn_complete"}'
`
			if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			req := LaunchRequest{
				Provider: ProviderLCAgent, ProjectPath: root, AppDataDir: dataDir,
				LCAgentPath: exe, LCAgentProvider: "deepseek", PendingModel: "deepseek-v4-pro",
				PendingReasoning: "high", LCAgentDeepSeekAPIKey: "old-key", Prompt: "Audit",
			}
			session, err := newLCAgentSession(req, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			before := waitForLCAgentTranscript(t, session, "Waiting for turn release")
			s := session.(*lcagentSession)
			s.mu.Lock()
			pid := s.cmd.Process.Pid
			s.mu.Unlock()

			next := req
			next.Prompt = ""
			next.PendingModel = tc.model
			next.LCAgentProvider = tc.provider
			next.LCAgentMoonshotAPIKey = "new-moonshot-key"
			next.PendingReasoning = "max"
			next.LCAgentRoutePreset = config.LCAgentSingleModel
			next.LCAgentDeepSeekAPIKey = "new-key"
			next.LCAgentWebSearchBackend = "exa"
			next.LCAgentWebSearchAPIKey = "new-search-key"
			if err := s.StageLaunchSettings(next); err != nil {
				t.Fatal(err)
			}
			after := s.Snapshot()
			if !after.Busy || after.Closed || after.ThreadID != before.ThreadID || after.Model != "deepseek-v4-pro" || after.PendingModel != tc.model || after.PendingModelProvider != tc.provider || after.PendingReasoning != "max" {
				t.Fatalf("staging changed the active turn: before=%+v after=%+v", before, after)
			}
			s.mu.Lock()
			unchanged := s.cmd != nil && s.cmd.Process.Pid == pid && s.deepSeekAPIKey == "old-key"
			s.mu.Unlock()
			if !unchanged {
				t.Fatal("staging replaced the process or changed its active configuration")
			}
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			finished := waitForLCAgentIdleSnapshot(t, s, nil)
			if finished.LastError != "" || !strings.Contains(finished.Transcript, "Finished the original turn") {
				t.Fatalf("original turn did not finish normally: %+v", finished)
			}
			if err := s.Submit("Continue audit"); err != nil {
				t.Fatal(err)
			}
			finished = waitForLCAgentIdleSnapshot(t, s, nil)
			if finished.LastError != "" || finished.ThreadID != before.ThreadID || !strings.Contains(finished.Transcript, "Audit") {
				t.Fatalf("next turn lost the thread or failed: %+v", finished)
			}
			args, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"--model\n" + tc.model + "\n", "--provider\n" + tc.provider + "\n", "--reasoning-effort\nmax\n", "--continue-from\nlct_staging\n", "--route-preset\nsingle-model\n", "--utility-model\n" + tc.model + "\n", "--web-search-backend\nexa\n"} {
				if !strings.Contains(string(args), want) {
					t.Errorf("next turn missing %q in %s", want, args)
				}
			}
			s.mu.Lock()
			updated := s.deepSeekAPIKey == "new-key" && s.moonshotAPIKey == "new-moonshot-key" && s.webSearchAPIKey == "new-search-key"
			s.mu.Unlock()
			if !updated {
				t.Fatal("next turn did not adopt the staged credentials")
			}
		})
	}
}

func TestLCAgentStagedLocalAutoDoesNotReuseCloudModel(t *testing.T) {
	req := LaunchRequest{
		Provider: ProviderLCAgent, ProjectPath: t.TempDir(), AppDataDir: t.TempDir(),
		LCAgentProvider: "deepseek", PendingModel: "deepseek-v4-pro",
	}
	session, err := newLCAgentSession(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := session.(*lcagentSession)
	t.Cleanup(func() { _ = s.Close() })
	req.LCAgentProvider, req.PendingModel = "ollama", ""
	if err := s.StageLaunchSettings(req); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.prepareRun("Continue", "Continue", lcagentRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.provider != "ollama" || prepared.model != "" {
		t.Fatalf("local Auto should resolve at launch, got %s/%s", prepared.provider, prepared.model)
	}
}
