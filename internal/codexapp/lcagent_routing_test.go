package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
)

func TestLCAgentSingleModelLaunchPassesSelectedProviderAndModel(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	exe := filepath.Join(t.TempDir(), "fake-lcagent")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LCAGENT_SINGLE_ARGS\"\nprintf '%s\\n' '{\"type\":\"turn_complete\"}'\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCAGENT_SINGLE_ARGS", argsPath)
	session, err := newLCAgentSession(LaunchRequest{
		Provider: ProviderLCAgent, ProjectPath: t.TempDir(), AppDataDir: t.TempDir(), LCAgentPath: exe,
		LCAgentRoutePreset: config.LCAgentSingleModel, LCAgentProvider: "deepseek", PendingModel: "deepseek-flash",
		LCAgentAuto: "medium", LCAgentUtilityProvider: "zai", LCAgentUtilityModel: "glm-5.3-flash",
		LCAgentVisionProvider: "zai", LCAgentVisionModel: "glm-5.3-flash", Prompt: "Proceed.",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitForLCAgentIdleSnapshot(t, session, nil)
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []string{
		"--route-preset\nsingle-model\n", "--provider\ndeepseek\n", "--model\ndeepseek-flash\n",
		"--utility-model\ndeepseek-flash\n", "--vision-provider\nmain\n", "--auto\nmedium\n",
	} {
		if !strings.Contains(string(args), pair) {
			t.Fatalf("missing launch arguments %q:\n%s", pair, args)
		}
	}
	if strings.Contains(string(args), "glm-") {
		t.Fatalf("stale secondary model in launch:\n%s", args)
	}
}

func TestLCAgentReopenKeepsSavedVisionRouteOverHistoricalGLM(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	id := "lca_old_vision_route"
	writeLCAgentReplayArtifact(t, dataDir, time.Now().Add(-time.Hour), id, []map[string]any{
		{"type": "session_meta", "id": id, "cwd": root},
		{"type": "model_request", "provider": "deepseek", "model": "deepseek-v4-pro"},
		{"type": "image_analysis_result", "provider": "zai", "model": "glm-5.3-flash", "summary": "Historical observation"},
		{"type": "turn_complete"},
	})
	resumed, err := newLCAgentSession(LaunchRequest{
		Provider: ProviderLCAgent, ProjectPath: root, AppDataDir: dataDir, ResumeID: id, RequireResumeID: true,
		LCAgentProvider: "deepseek", LCAgentVisionProvider: "deepseek", LCAgentVisionModel: "deepseek-flash",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := resumed.(*lcagentSession)
	// Check both the idle sidebar and what will actually launch next.
	snapshot := s.Snapshot()
	if snapshot.VisionModelProvider != "deepseek" || snapshot.VisionModel != "deepseek-flash" {
		t.Fatalf("reopened vision = %s/%s", snapshot.VisionModelProvider, snapshot.VisionModel)
	}
	if snapshot.ImageAnalyses != 1 || snapshot.ImageAnalysisLastSummary != "Historical observation" {
		t.Fatalf("historical observations lost: %#v", snapshot)
	}
	prepared, err := s.prepareRun("Continue", "Continue", lcagentRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.visionProvider != "deepseek" || prepared.visionModel != "deepseek-flash" {
		t.Fatalf("next run vision = %s/%s", prepared.visionProvider, prepared.visionModel)
	}
}

func TestLCAgentHistoricalImageResultDoesNotChangeRouting(t *testing.T) {
	s := &lcagentSession{visionProvider: "deepseek", visionModel: "deepseek-flash"}
	s.handleLCAgentImageAnalysisResult(map[string]json.RawMessage{
		"provider": json.RawMessage(`"zai"`), "model": json.RawMessage(`"glm-5.3-flash"`),
		"summary": json.RawMessage(`"Past pixels"`),
	})
	if s.visionProvider != "deepseek" || s.visionModel != "deepseek-flash" || s.imageAnalyses != 1 {
		t.Fatalf("image result changed operator routing: %s/%s", s.visionProvider, s.visionModel)
	}
}

func TestLCAgentSingleModelFollowsSelectionForEveryRole(t *testing.T) {
	for _, selection := range []struct{ provider, model string }{
		{"deepseek", "deepseek-flash"},
		{"xiaomi", "mimo-v2.5-pro"},
		{"ollama", "tomorrows-model"},
	} {
		t.Run(selection.provider, func(t *testing.T) {
			s := &lcagentSession{
				projectPath: t.TempDir(), routePreset: config.LCAgentSingleModel,
				provider: "deepseek", model: "deepseek-v4-pro",
				pendingModelProvider: selection.provider, pendingModel: selection.model,
				pendingReasoning: "low", mainVisionProvider: selection.provider, mainVisionModel: selection.model,
				utilityProvider: "zai", utilityModel: "glm-5.3-flash", utilityReasoning: "high",
				visionProvider: "zai", visionModel: "glm-5.3-flash",
			}
			prepared, err := s.prepareRun("Continue", "Continue", lcagentRunOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.routePreset != config.LCAgentSingleModel || prepared.modelProvider != selection.provider || prepared.model != selection.model {
				t.Fatalf("single-model selection lost: %#v", prepared)
			}
			if prepared.utilityProvider != "main" || prepared.utilityModel != selection.model || prepared.utilityReasoning != prepared.reasoningEffort {
				t.Fatalf("utility did not follow main: %s/%s (%s)", prepared.utilityProvider, prepared.utilityModel, prepared.utilityReasoning)
			}
			if prepared.visionProvider != "main" || prepared.visionModel != "" {
				t.Fatalf("vision did not follow main: %s/%s", prepared.visionProvider, prepared.visionModel)
			}
			if snapshot := s.Snapshot(); snapshot.VisionModelProvider != selection.provider || snapshot.VisionModel != selection.model {
				t.Fatalf("sidebar did not follow selection: %s/%s", snapshot.VisionModelProvider, snapshot.VisionModel)
			}
		})
	}
}

func TestLCAgentNativeVisionCanReturnToConfiguredFallback(t *testing.T) {
	s := &lcagentSession{visionProvider: "zai", visionModel: "glm-5.3-flash"}
	provider, _ := s.visionRouteLocked("deepseek", "deepseek-flash")
	if provider != "main" {
		t.Fatalf("native route = %s", provider)
	}
	provider, model := s.visionRouteLocked("deepseek", "deepseek-v4-pro")
	if provider != "zai" || model != "glm-5.3-flash" {
		t.Fatalf("fallback lost after model switch: %s/%s", provider, model)
	}
}
