package boss

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/llm"
)

func TestBossReadOnlyRouterUserTextExcludesAmbientAppState(t *testing.T) {
	t.Parallel()

	req := AssistantRequest{
		StateBrief: "SECRET_PORTFOLIO_STATE",
		View:       ViewContext{Status: "SECRET_TUI_STATUS"},
		Messages:   []ChatMessage{{Role: "user", Content: "Please enable privacy mode."}},
	}
	text := bossReadOnlyRouterUserText(req)
	if !strings.Contains(text, "Please enable privacy mode.") {
		t.Fatalf("router text missing latest user turn:\n%s", text)
	}
	for _, unwanted := range []string{"SECRET_PORTFOLIO_STATE", "SECRET_TUI_STATUS", "Current app state brief", "Current TUI view"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("router text retained ambient state %q:\n%s", unwanted, text)
		}
	}
}

func TestAssistantUsesScopedSettingsReferencePromptAndSchema(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{
			Kind:          bossReadOnlyRoutePass,
			PlannerDomain: bossPlannerDomainSettings,
			Reason:        "This is an app settings change.",
		}),
	}}}
	planner := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: encodedBossAction(t, bossAction{
			Kind:              bossActionProposeControl,
			ControlCapability: string(control.CapabilitySettingsUpdate),
			SettingsChanges: []control.SettingsChange{{
				Field:     control.SettingsFieldPrivacyMode,
				Operation: control.SettingsUpdateSet,
				BoolValue: true,
			}},
		}),
	}}}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	resp, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat:   true,
		StateBrief: "SECRET_PORTFOLIO_STATE",
		View:       ViewContext{Status: "SECRET_TUI_STATUS"},
		Messages:   []ChatMessage{{Role: "user", Content: "Please enable privacy mode."}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if resp.ControlInvocation == nil || resp.ControlInvocation.Capability != control.CapabilitySettingsUpdate {
		t.Fatalf("control invocation = %#v, want settings.update", resp.ControlInvocation)
	}
	if len(planner.reqs) != 1 {
		t.Fatalf("planner calls = %d, want 1", len(planner.reqs))
	}
	req := planner.reqs[0]
	for _, want := range []string{
		"planner_domain=settings",
		"[control_reference]",
		"settings.update",
		"Please enable privacy mode.",
	} {
		if !strings.Contains(req.SystemText+"\n"+req.UserText, want) {
			t.Fatalf("scoped planner request missing %q:\nSYSTEM:\n%s\nUSER:\n%s", want, req.SystemText, req.UserText)
		}
	}
	for _, unwanted := range []string{
		"SECRET_PORTFOLIO_STATE",
		"SECRET_TUI_STATUS",
		"agent_task.create",
		"git.prepare_commit",
		"todo.create_worktree_and_start_engineer",
	} {
		if strings.Contains(req.SystemText+"\n"+req.UserText, unwanted) {
			t.Fatalf("scoped planner request retained unrelated context %q:\nSYSTEM:\n%s\nUSER:\n%s", unwanted, req.SystemText, req.UserText)
		}
	}
	properties := schemaProperties(t, req.Schema)
	for _, want := range []string{"kind", "answer", "control_capability", "settings_changes"} {
		if _, ok := properties[want]; !ok {
			t.Fatalf("settings schema missing %q: %#v", want, properties)
		}
	}
	for _, unwanted := range []string{"task_id", "commit_message", "goal_plan_steps", "todo_text"} {
		if _, ok := properties[unwanted]; ok {
			t.Fatalf("settings schema retained unrelated field %q", unwanted)
		}
	}
}

func TestScopedPlannerRejectsCapabilityOutsideModelSelectedDomain(t *testing.T) {
	t.Parallel()

	router := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: encodedReadOnlyRoute(t, bossReadOnlyRoute{
			Kind:          bossReadOnlyRoutePass,
			PlannerDomain: bossPlannerDomainSettings,
		}),
	}}}
	planner := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: encodedBossAction(t, bossAction{
			Kind:              bossActionProposeControl,
			ControlCapability: string(control.CapabilityAgentTaskCreate),
			TaskTitle:         "Wrong domain",
			TaskKind:          string(control.AgentTaskKindAgent),
		}),
	}}}
	assistant := &Assistant{
		planner:     planner,
		queryRouter: router,
		query:       newQueryExecutor(&fakeBossStore{}),
		model:       "gpt-test",
	}

	_, err := assistant.Reply(context.Background(), AssistantRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Enable privacy mode."}},
	})
	if err == nil || !strings.Contains(err.Error(), "planner_domain=settings does not allow control capability") {
		t.Fatalf("Reply() error = %v, want scoped-domain rejection", err)
	}
}

func TestScopedPlannerSchemaIsSmallerThanLegacySchema(t *testing.T) {
	t.Parallel()

	legacy := schemaProperties(t, bossActionSchemaForRequest(AssistantRequest{}))
	settings := schemaProperties(t, bossActionSchemaForRequest(AssistantRequest{PlannerDomain: bossPlannerDomainSettings}))
	git := schemaProperties(t, bossActionSchemaForRequest(AssistantRequest{PlannerDomain: bossPlannerDomainGit}))
	if len(settings) >= len(legacy) || len(git) >= len(legacy) {
		t.Fatalf("schema sizes: legacy=%d settings=%d git=%d", len(legacy), len(settings), len(git))
	}
	if len(settings) > 15 || len(git) > 16 {
		t.Fatalf("scoped schemas unexpectedly broad: settings=%d git=%d", len(settings), len(git))
	}
}

func TestScopedPlannerMateriallyReducesPromptAndSchemaPayload(t *testing.T) {
	t.Parallel()

	legacyReq := AssistantRequest{HelpChat: true}
	scopedReq := AssistantRequest{HelpChat: true, PlannerDomain: bossPlannerDomainSettings}
	legacySchema, err := json.Marshal(bossActionSchemaForRequest(legacyReq))
	if err != nil {
		t.Fatalf("marshal legacy schema: %v", err)
	}
	scopedSchema, err := json.Marshal(bossActionSchemaForRequest(scopedReq))
	if err != nil {
		t.Fatalf("marshal scoped schema: %v", err)
	}
	reference, ok := bossControlReferenceForDomain(scopedReq.PlannerDomain)
	if !ok {
		t.Fatal("settings reference unavailable")
	}
	legacyBytes := len(bossActionPlannerSystemPromptForRequest(legacyReq)) + len(legacySchema)
	scopedBytes := len(bossActionPlannerSystemPromptForRequest(scopedReq)) + len(scopedSchema) + len(reference.Text)
	if scopedBytes*4 >= legacyBytes*3 {
		t.Fatalf("scoped payload reduction too small: legacy=%d scoped=%d", legacyBytes, scopedBytes)
	}
}

func TestControlReferenceComesFromCapabilityRegistry(t *testing.T) {
	t.Parallel()

	result, ok := bossControlReferenceForDomain(bossPlannerDomainGit)
	if !ok || !result.Internal {
		t.Fatalf("git control reference = %#v, %v", result, ok)
	}
	capability, _ := control.CapabilityByName(control.CapabilityGitPrepareCommit)
	for _, want := range []string{
		string(capability.Name),
		capability.Description,
		"Risk=" + string(capability.Risk),
		"confirmation=" + string(capability.Confirmation),
		"push_after_commit=true only when",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("control reference missing %q:\n%s", want, result.Text)
		}
	}
	if got := synthesizeToolLoopFallback([]bossToolResult{result}); strings.Contains(got, "Scoped Chat control reference") {
		t.Fatalf("internal reference leaked into fallback: %q", got)
	}
}

func TestStructuredRepairUsesScopedSchema(t *testing.T) {
	t.Parallel()

	planner := &fakeJSONSchemaRunner{resp: []llm.JSONSchemaResponse{{
		OutputText: encodedBossAction(t, bossAction{Kind: bossActionAnswer, Answer: "Repaired."}),
	}}}
	assistant := &Assistant{planner: planner, model: "gpt-test", backend: config.AIBackendOpenAIAPI}
	_, _, err := assistant.repairBossAction(context.Background(), AssistantRequest{
		PlannerDomain: bossPlannerDomainGit,
		Messages:      []ChatMessage{{Role: "user", Content: "Commit Alpha."}},
	}, nil, false, "not json")
	if err != nil {
		t.Fatalf("repairBossAction() error = %v", err)
	}
	properties := schemaProperties(t, planner.reqs[0].Schema)
	if _, ok := properties["commit_message"]; !ok {
		t.Fatalf("git repair schema missing commit_message")
	}
	if _, ok := properties["settings_changes"]; ok {
		t.Fatalf("git repair schema retained settings_changes")
	}
}

func schemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	return properties
}
