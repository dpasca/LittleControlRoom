package modeladapter

import (
	"strings"
	"testing"

	"lcroom/internal/todocapture"
)

func TestProjectTodoToolsRequireExplicitEnablementAndHaveNoScopeOverride(t *testing.T) {
	for _, name := range []string{"list_project_todos", "add_project_todo"} {
		if toolNames(Tools())[name] {
			t.Fatalf("Tools() unexpectedly exposed %s", name)
		}
	}

	tools := ToolsWithOptions(ToolOptions{TodoCaptureMode: todocapture.ModeExplicit})
	list := toolSpec(t, tools, "list_project_todos")
	listProps := list.Parameters["properties"].(map[string]any)
	if len(listProps) != 0 {
		t.Fatalf("list_project_todos properties = %#v, want no model-controlled scope", listProps)
	}
	add := toolSpec(t, tools, "add_project_todo")
	addProps := add.Parameters["properties"].(map[string]any)
	if _, ok := addProps["project_path"]; ok {
		t.Fatalf("add_project_todo exposes project_path: %#v", addProps)
	}
	if _, ok := addProps["provider"]; ok {
		t.Fatalf("add_project_todo exposes provider: %#v", addProps)
	}
	kinds := addProps["capture_kind"].(map[string]any)["enum"].([]string)
	if len(kinds) != 1 || kinds[0] != string(todocapture.CaptureExplicitRequest) {
		t.Fatalf("explicit-only capture kinds = %#v", kinds)
	}
}

func TestProjectTodoToolsExposeDeferralsButNeverToReadOnlyScout(t *testing.T) {
	tools := ToolsWithOptions(ToolOptions{TodoCaptureMode: todocapture.ModeExplicitAndClearDeferrals})
	add := toolSpec(t, tools, "add_project_todo")
	props := add.Parameters["properties"].(map[string]any)
	kinds := props["capture_kind"].(map[string]any)["enum"].([]string)
	if len(kinds) != 2 || kinds[0] != string(todocapture.CaptureExplicitRequest) || kinds[1] != string(todocapture.CaptureClearDeferral) {
		t.Fatalf("deferral capture kinds = %#v", kinds)
	}

	readOnlyNames := toolNames(ToolsWithOptions(ToolOptions{
		TodoCaptureMode: todocapture.ModeExplicitAndClearDeferrals,
		ReadOnly:        true,
	}))
	for _, name := range []string{"list_project_todos", "add_project_todo"} {
		if readOnlyNames[name] {
			t.Fatalf("read-only tools unexpectedly exposed %s", name)
		}
	}
}

func TestProjectTodoPromptUsesSharedPolicyAndExcludesReadOnlyScout(t *testing.T) {
	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{TodoCaptureMode: todocapture.ModeExplicitAndClearDeferrals})
	if !strings.Contains(prompt, todocapture.AgentInstructions(todocapture.ModeExplicitAndClearDeferrals)) {
		t.Fatalf("system prompt missing shared TODO policy:\n%s", prompt)
	}
	readOnly := SystemPromptWithOptions("", "", SystemPromptOptions{
		TodoCaptureMode: todocapture.ModeExplicitAndClearDeferrals,
		ReadOnly:        true,
	})
	for _, forbidden := range []string{"list_project_todos", "add_project_todo", "project TODO capture is enabled"} {
		if strings.Contains(readOnly, forbidden) {
			t.Fatalf("read-only prompt contains %q:\n%s", forbidden, readOnly)
		}
	}
}

func TestLCRControlToolsAndPromptRequireExplicitEnablement(t *testing.T) {
	controlTools := []string{
		"list_control_capabilities",
		"describe_control_capability",
		"propose_control_operation",
		"get_control_operation",
	}
	defaults := toolNames(Tools())
	for _, name := range controlTools {
		if defaults[name] {
			t.Fatalf("Tools() unexpectedly exposed %s", name)
		}
	}

	enabled := toolNames(ToolsWithOptions(ToolOptions{LCRControlsEnabled: true}))
	for _, name := range controlTools {
		if !enabled[name] {
			t.Fatalf("LCR control profile missing %s", name)
		}
	}
	propose := toolSpec(t, LCRControlToolDefinitions(), "propose_control_operation")
	properties := propose.Parameters["properties"].(map[string]any)
	if _, ok := properties["arguments"]; !ok {
		t.Fatalf("proposal schema = %#v, want typed arguments object", propose.Parameters)
	}

	prompt := SystemPromptWithOptions("", "", SystemPromptOptions{LCRControlsEnabled: true})
	for _, want := range []string{
		"list_control_capabilities",
		"explicit operator confirmation",
		"propose engineer.send_prompt",
		"durable delivery waits for that recipient",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("control-enabled prompt missing %q:\n%s", want, prompt)
		}
	}

	readOnlyNames := toolNames(ToolsWithOptions(ToolOptions{LCRControlsEnabled: true, ReadOnly: true}))
	for _, name := range controlTools {
		if readOnlyNames[name] {
			t.Fatalf("read-only scout unexpectedly exposed %s", name)
		}
	}
}
