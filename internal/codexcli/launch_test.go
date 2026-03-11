package codexcli

import "testing"

func TestBuildLaunchPlanPrefersResumeWhenSessionExists(t *testing.T) {
	plan, err := BuildLaunchPlan("/tmp/demo", "019c-demo-session", "continue", false, PresetYolo)
	if err != nil {
		t.Fatalf("BuildLaunchPlan() error = %v", err)
	}
	if plan.Kind != LaunchResume {
		t.Fatalf("plan.Kind = %s, want %s", plan.Kind, LaunchResume)
	}
	if plan.Preset != PresetYolo {
		t.Fatalf("plan.Preset = %s, want %s", plan.Preset, PresetYolo)
	}
	if plan.SessionID != "019c-demo-session" {
		t.Fatalf("plan.SessionID = %q, want session id", plan.SessionID)
	}

	cmd := plan.Command()
	want := []string{"codex", "resume", "--dangerously-bypass-approvals-and-sandbox", "-C", "/tmp/demo", "019c-demo-session", "continue"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("cmd.Args len = %d, want %d (%v)", len(cmd.Args), len(want), cmd.Args)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("cmd.Args[%d] = %q, want %q (all=%v)", i, cmd.Args[i], want[i], cmd.Args)
		}
	}
	if cmd.Dir != "/tmp/demo" {
		t.Fatalf("cmd.Dir = %q, want /tmp/demo", cmd.Dir)
	}
}

func TestBuildLaunchPlanStartsNewWhenForced(t *testing.T) {
	plan, err := BuildLaunchPlan("/tmp/demo", "019c-demo-session", "summarize", true, PresetFullAuto)
	if err != nil {
		t.Fatalf("BuildLaunchPlan() error = %v", err)
	}
	if plan.Kind != LaunchNew {
		t.Fatalf("plan.Kind = %s, want %s", plan.Kind, LaunchNew)
	}
	if plan.Preset != PresetFullAuto {
		t.Fatalf("plan.Preset = %s, want %s", plan.Preset, PresetFullAuto)
	}
	if plan.SessionID != "" {
		t.Fatalf("plan.SessionID = %q, want empty", plan.SessionID)
	}

	cmd := plan.Command()
	want := []string{"codex", "--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-C", "/tmp/demo", "summarize"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("cmd.Args len = %d, want %d (%v)", len(cmd.Args), len(want), cmd.Args)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("cmd.Args[%d] = %q, want %q (all=%v)", i, cmd.Args[i], want[i], cmd.Args)
		}
	}
}

func TestBuildLaunchPlanRequiresProjectPath(t *testing.T) {
	if _, err := BuildLaunchPlan("", "", "", false, PresetYolo); err == nil {
		t.Fatalf("BuildLaunchPlan() expected error for empty project path")
	}
}

func TestBuildLaunchPlanDefaultsToYoloPreset(t *testing.T) {
	plan, err := BuildLaunchPlan("/tmp/demo", "", "", true, "")
	if err != nil {
		t.Fatalf("BuildLaunchPlan() error = %v", err)
	}
	if plan.Preset != PresetYolo {
		t.Fatalf("plan.Preset = %s, want %s", plan.Preset, PresetYolo)
	}
}

func TestParsePreset(t *testing.T) {
	tests := map[string]Preset{
		"yolo":      PresetYolo,
		"full-auto": PresetFullAuto,
		"full_auto": PresetFullAuto,
		"safe":      PresetSafe,
	}
	for raw, want := range tests {
		got, err := ParsePreset(raw)
		if err != nil {
			t.Fatalf("ParsePreset(%q) error = %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParsePreset(%q) = %s, want %s", raw, got, want)
		}
	}
}
