package control

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUnattendedGrantAllows(t *testing.T) {
	grant, err := NormalizeUnattendedGrant(UnattendedGrant{
		CommandPrefixes: []string{"go test", " node scripts/check.mjs "},
		FileChanges:     true,
		Tools:           []string{"mcp__playwright__*", "web_search"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind, detail string
		want         bool
	}{
		{ApprovalKindCommand, "go test ./...", true},
		{ApprovalKindCommand, "go test", true},
		{ApprovalKindCommand, "go testing-tool", false},
		{ApprovalKindCommand, "go test ./... && rm -rf /", false},
		{ApprovalKindCommand, "go test $(curl evil)", false},
		{ApprovalKindCommand, "git push", false},
		{ApprovalKindFileChange, "", true},
		{ApprovalKindToolUse, "mcp__playwright__browser_click", true},
		{ApprovalKindToolUse, "web_search", true},
		{ApprovalKindToolUse, "web_search_extra", false},
		{ApprovalKindToolUse, "mcp__other__thing", false},
		{"unknown", "go test", false},
	}
	for _, tc := range cases {
		if got := grant.Allows(tc.kind, tc.detail); got != tc.want {
			t.Errorf("Allows(%q, %q) = %t, want %t", tc.kind, tc.detail, got, tc.want)
		}
	}
	if (UnattendedGrant{}).Allows(ApprovalKindFileChange, "") {
		t.Fatal("empty grant approved a file change")
	}
}

func TestNormalizeUnattendedGrantRejectsBroadAndInvalid(t *testing.T) {
	for name, grant := range map[string]UnattendedGrant{
		"star tool":       {Tools: []string{"*"}},
		"negative window": {ValidMinutes: -1},
		"huge window":     {ValidMinutes: MaxGrantValidMinutes + 1},
		"long notes":      {Notes: strings.Repeat("x", maxGrantNotesRunes+1)},
	} {
		if _, err := NormalizeUnattendedGrant(grant); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	grant, err := NormalizeUnattendedGrant(UnattendedGrant{Commands: true, CommandPrefixes: []string{"go test"}})
	if err != nil || len(grant.CommandPrefixes) != 0 {
		t.Fatalf("blanket commands should drop prefixes: %#v err=%v", grant, err)
	}
}

func scheduledInput() EngineerSendPromptInput {
	return EngineerSendPromptInput{
		ProjectPath: "/tmp/demo", Provider: ProviderCodex, SessionMode: SessionModeResumeOrNew,
		TargetSessionID: "session-1", Prompt: "Verify the repost.", DeliverAt: "2026-10-18T18:10:00+09:00",
	}
}

func TestNormalizeScheduledWakeup(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(scheduledInput())
	if err != nil {
		t.Fatal(err)
	}
	if input.DeliverAt != "2026-10-18T09:10:00Z" || input.MissedGraceMinutes != DefaultWakeupGraceMinutes || input.Unattended == nil || input.Unattended.ValidMinutes != DefaultGrantValidMinutes {
		t.Fatalf("normalized wakeup = %#v", input)
	}
	if !input.Scheduled() {
		t.Fatal("wakeup not reported as scheduled")
	}
	for name, edit := range map[string]func(*EngineerSendPromptInput){
		"bad time":           func(i *EngineerSendPromptInput) { i.DeliverAt = "tomorrow" },
		"no offset":          func(i *EngineerSendPromptInput) { i.DeliverAt = "2026-10-18T18:10:00" },
		"no exact target":    func(i *EngineerSendPromptInput) { i.TargetSessionID = "" },
		"todo redirect":      func(i *EngineerSendPromptInput) { i.TodoID = 3 },
		"negative grace":     func(i *EngineerSendPromptInput) { i.MissedGraceMinutes = -1 },
		"grant without time": func(i *EngineerSendPromptInput) { i.DeliverAt = ""; i.Unattended = &UnattendedGrant{Commands: true} },
	} {
		in := scheduledInput()
		edit(&in)
		if _, err := NormalizeEngineerSendPromptInput(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestScheduledWakeupIsNeverAutoApproved(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(scheduledInput())
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(input)
	op := Operation{Capability: CapabilityEngineerSendPrompt, ProjectPath: "/tmp/origin", Provider: "codex", SessionKey: "sender", Invocation: Invocation{Capability: CapabilityEngineerSendPrompt, Args: args}}
	if _, ok := CollaborationForOperation(op); ok {
		t.Fatal("scheduled wakeup qualified for project collaboration auto-approval")
	}
	if _, ok := PermissionForOperation(op); ok {
		t.Fatal("scheduled wakeup qualified for a reusable scoped permission")
	}
	immediate := scheduledInput()
	immediate.DeliverAt = ""
	args, _ = json.Marshal(immediate)
	op.Invocation.Args = args
	if _, ok := CollaborationForOperation(op); !ok {
		t.Fatal("ordinary exact-session message lost collaboration eligibility")
	}
}

func TestWakeupPromptPreambleStatesAuthority(t *testing.T) {
	grant, _ := NormalizeUnattendedGrant(UnattendedGrant{FileChanges: true, Notes: "publish the approved queue only"})
	text := WakeupPromptPreamble(time.Date(2026, 10, 18, 9, 10, 0, 0, time.UTC), grant)
	for _, want := range []string{"scheduled wakeup", "file changes", "publish the approved queue only", "stop"} {
		if !strings.Contains(text, want) {
			t.Errorf("preamble missing %q:\n%s", want, text)
		}
	}
}
