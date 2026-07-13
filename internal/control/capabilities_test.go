package control

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		raw  string
		want Provider
	}{
		{raw: "", want: ProviderAuto},
		{raw: " auto ", want: ProviderAuto},
		{raw: "CODEX", want: ProviderCodex},
		{raw: "open-code", want: ProviderOpenCode},
		{raw: "open_code", want: ProviderOpenCode},
		{raw: "OpenCode", want: ProviderOpenCode},
		{raw: "claude", want: ProviderClaudeCode},
		{raw: "claude-code", want: ProviderClaudeCode},
		{raw: "claude_code", want: ProviderClaudeCode},
		{raw: "lcagent", want: ProviderLCAgent},
		{raw: "lc-agent", want: ProviderLCAgent},
		{raw: "lc_agent", want: ProviderLCAgent},
		{raw: "other", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeProvider(tt.raw); got != tt.want {
			t.Fatalf("NormalizeProvider(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeSessionMode(t *testing.T) {
	tests := []struct {
		raw  string
		want SessionMode
	}{
		{raw: "", want: SessionModeResumeOrNew},
		{raw: "resume", want: SessionModeResumeOrNew},
		{raw: "resume-or-new", want: SessionModeResumeOrNew},
		{raw: "resume_or_new", want: SessionModeResumeOrNew},
		{raw: "new", want: SessionModeNew},
		{raw: "fresh", want: SessionModeNew},
		{raw: "force-new", want: SessionModeNew},
		{raw: "bogus", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeSessionMode(tt.raw); got != tt.want {
			t.Fatalf("NormalizeSessionMode(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeProjectArchiveAction(t *testing.T) {
	tests := []struct {
		raw  string
		want ProjectArchiveAction
	}{
		{raw: " archive ", want: ProjectArchiveActionArchive},
		{raw: "archived", want: ProjectArchiveActionArchive},
		{raw: "unarchive", want: ProjectArchiveActionUnarchive},
		{raw: "restore", want: ProjectArchiveActionUnarchive},
		{raw: "active", want: ProjectArchiveActionUnarchive},
		{raw: "delete", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeProjectArchiveAction(tt.raw); got != tt.want {
			t.Fatalf("NormalizeProjectArchiveAction(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeProjectArchiveInputAllowsProjectResources(t *testing.T) {
	input, err := NormalizeProjectArchiveInput(ProjectArchiveInput{
		Action: ProjectArchiveActionArchive,
		Resources: []ResourceRef{{
			Kind:        ResourceProject,
			ProjectPath: " /tmp/repos/quickgame_01 ",
			Label:       " quickgame_01 ",
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeProjectArchiveInput() error = %v", err)
	}
	if input.ProjectPath != "" || input.ProjectName != "" {
		t.Fatalf("single target fields = %q/%q, want empty batch target", input.ProjectPath, input.ProjectName)
	}
	if len(input.Resources) != 1 ||
		input.Resources[0].ProjectPath != "/tmp/repos/quickgame_01" ||
		input.Resources[0].Label != "quickgame_01" {
		t.Fatalf("resources = %#v, want normalized project resource", input.Resources)
	}
}

func TestEngineerSendPromptCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityEngineerSendPrompt)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityEngineerSendPrompt)
	}
	if capability.Name != CapabilityEngineerSendPrompt {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityEngineerSendPrompt)
	}
	if capability.Risk != RiskExternal {
		t.Fatalf("Risk = %q, want %q", capability.Risk, RiskExternal)
	}
	if capability.Confirmation != ConfirmationRequired {
		t.Fatalf("Confirmation = %q, want %q", capability.Confirmation, ConfirmationRequired)
	}
	if capability.RequiresHost {
		t.Fatalf("RequiresHost = true, want false")
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayRevealEngineerSession) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayRevealEngineerSession)
	}
	providers := map[Provider]ProviderCapability{}
	for _, provider := range capability.Providers {
		providers[provider.ID] = provider
	}
	if !providers[ProviderCodex].Available {
		t.Fatalf("Codex provider should be available in default metadata")
	}
	if !providers[ProviderOpenCode].Available {
		t.Fatalf("OpenCode provider should be available in default metadata")
	}
	if providers[ProviderClaudeCode].Available {
		t.Fatalf("Claude Code provider should be disabled in default metadata")
	}
	if !providers[ProviderLCAgent].Available {
		t.Fatalf("LCAgent provider should be available in default metadata")
	}
	if providers[ProviderLCAgent].Reason != "experimental" {
		t.Fatalf("LCAgent reason = %q, want experimental", providers[ProviderLCAgent].Reason)
	}
	if !stringSliceContains(providers[ProviderCodex].Features, FeatureCompact) {
		t.Fatalf("Codex features = %#v, want compact support", providers[ProviderCodex].Features)
	}
	if stringSliceContains(providers[ProviderOpenCode].Features, FeatureCompact) {
		t.Fatalf("OpenCode features = %#v, did not expect compact support in default metadata", providers[ProviderOpenCode].Features)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("capability schemas should be object schemas")
	}
}

func TestAgentTaskCapabilitiesMetadata(t *testing.T) {
	for _, name := range []CapabilityName{CapabilityAgentTaskCreate, CapabilityAgentTaskContinue, CapabilityAgentTaskClose} {
		capability, ok := CapabilityByName(name)
		if !ok {
			t.Fatalf("CapabilityByName(%q) not found", name)
		}
		if capability.Name != name {
			t.Fatalf("Name = %q, want %q", capability.Name, name)
		}
		if capability.Confirmation != ConfirmationRequired {
			t.Fatalf("%s confirmation = %q, want required", name, capability.Confirmation)
		}
		if !capability.RequiresHost {
			t.Fatalf("%s RequiresHost = false, want true", name)
		}
		if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
			t.Fatalf("%s schemas should be object schemas", name)
		}
	}
}

func TestScratchTaskArchiveCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityScratchTaskArchive)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityScratchTaskArchive)
	}
	if capability.Name != CapabilityScratchTaskArchive {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityScratchTaskArchive)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected scratch task capability metadata: %#v", capability)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("scratch task archive schemas should be object schemas")
	}
}

func TestProjectArchiveCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityProjectArchive)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityProjectArchive)
	}
	if capability.Name != CapabilityProjectArchive {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityProjectArchive)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected project archive capability metadata: %#v", capability)
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMaySetProjectArchive) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMaySetProjectArchive)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("project archive schemas should be object schemas")
	}
}

func TestTodoAddCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityTodoAdd)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityTodoAdd)
	}
	if capability.Name != CapabilityTodoAdd {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityTodoAdd)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected TODO add capability metadata: %#v", capability)
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayCreateProjectTodo) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayCreateProjectTodo)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("TODO add schemas should be object schemas")
	}
}

func TestTodoCreateWorktreeAndStartEngineerCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityTodoCreateWorktreeAndStartEngineer)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityTodoCreateWorktreeAndStartEngineer)
	}
	if capability.Name != CapabilityTodoCreateWorktreeAndStartEngineer {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityTodoCreateWorktreeAndStartEngineer)
	}
	if capability.Risk != RiskExternal || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected tracked worktree capability metadata: %#v", capability)
	}
	for _, effect := range []string{HostEffectMayCreateProjectTodo, HostEffectMayCreateProjectWorktree, HostEffectMayRevealEngineerSession} {
		if !stringSliceContains(capability.HostEffects, effect) {
			t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, effect)
		}
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("tracked worktree schemas should be object schemas")
	}
}

func TestProjectCreateAndStartEngineerCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityProjectCreateAndStartEngineer)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityProjectCreateAndStartEngineer)
	}
	if capability.Risk != RiskExternal || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected new-repository capability metadata: %#v", capability)
	}
	for _, effect := range []string{
		HostEffectMayCreateProjectDirectory,
		HostEffectMayInitializeGitRepository,
		HostEffectMayTrackProject,
		HostEffectMayCreateProjectTodo,
		HostEffectMayCreateProjectWorktree,
		HostEffectMayRevealEngineerSession,
	} {
		if !stringSliceContains(capability.HostEffects, effect) {
			t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, effect)
		}
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("new-repository capability schemas should be object schemas")
	}
}

func TestTodoCompleteCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityTodoComplete)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityTodoComplete)
	}
	if capability.Name != CapabilityTodoComplete {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityTodoComplete)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected TODO complete capability metadata: %#v", capability)
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayCompleteProjectTodo) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayCompleteProjectTodo)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("TODO complete schemas should be object schemas")
	}
}

func TestSettingsUpdateCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilitySettingsUpdate)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilitySettingsUpdate)
	}
	if capability.Name != CapabilitySettingsUpdate {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilitySettingsUpdate)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected settings update capability metadata: %#v", capability)
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayUpdateSettings) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayUpdateSettings)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("settings update schemas should be object schemas")
	}
}

func TestGitPrepareCommitCapabilityMetadata(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityGitPrepareCommit)
	if !ok {
		t.Fatalf("CapabilityByName(%q) not found", CapabilityGitPrepareCommit)
	}
	if capability.Name != CapabilityGitPrepareCommit {
		t.Fatalf("Name = %q, want %q", capability.Name, CapabilityGitPrepareCommit)
	}
	if capability.Risk != RiskWrite || capability.Confirmation != ConfirmationRequired || !capability.RequiresHost {
		t.Fatalf("unexpected git prepare commit capability metadata: %#v", capability)
	}
	if !stringSliceContains(capability.HostEffects, HostEffectMayPrepareGitCommit) {
		t.Fatalf("HostEffects = %#v, want %q", capability.HostEffects, HostEffectMayPrepareGitCommit)
	}
	if capability.InputSchema["type"] != "object" || capability.OutputSchema["type"] != "object" {
		t.Fatalf("git prepare commit schemas should be object schemas")
	}
}

func TestNormalizeEngineerSendPromptInput(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(EngineerSendPromptInput{
		RequestID:   " req-1 ",
		ProjectPath: " /tmp/demo/../demo ",
		Provider:    "",
		SessionMode: "",
		Prompt:      "  Please run the focused tests.  ",
	})
	if err != nil {
		t.Fatalf("NormalizeEngineerSendPromptInput() error = %v", err)
	}
	if input.RequestID != "req-1" {
		t.Fatalf("RequestID = %q, want req-1", input.RequestID)
	}
	if input.ProjectPath != "/tmp/demo" {
		t.Fatalf("ProjectPath = %q, want /tmp/demo", input.ProjectPath)
	}
	if input.Provider != ProviderAuto {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderAuto)
	}
	if input.SessionMode != SessionModeResumeOrNew {
		t.Fatalf("SessionMode = %q, want %q", input.SessionMode, SessionModeResumeOrNew)
	}
	if input.Prompt != "Please run the focused tests." {
		t.Fatalf("Prompt = %q, want trimmed prompt", input.Prompt)
	}
}

func TestNormalizeEngineerSendPromptInputAllowsProjectNameOnly(t *testing.T) {
	input, err := NormalizeEngineerSendPromptInput(EngineerSendPromptInput{
		ProjectName: " Little Control Room ",
		Provider:    ProviderOpenCode,
		SessionMode: SessionModeNew,
		Prompt:      "Start a fresh investigation.",
	})
	if err != nil {
		t.Fatalf("NormalizeEngineerSendPromptInput() error = %v", err)
	}
	if input.ProjectName != "Little Control Room" {
		t.Fatalf("ProjectName = %q, want trimmed name", input.ProjectName)
	}
	if input.Provider != ProviderOpenCode {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderOpenCode)
	}
}

func TestNormalizeEngineerSendPromptInputRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input EngineerSendPromptInput
		want  string
	}{
		{
			name:  "missing project",
			input: EngineerSendPromptInput{Prompt: "Do the thing"},
			want:  "project_path or project_name is required",
		},
		{
			name:  "missing prompt",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo"},
			want:  "prompt is required",
		},
		{
			name:  "bad provider",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo", Provider: "other", Prompt: "Do the thing"},
			want:  "unsupported engineer provider",
		},
		{
			name:  "bad session mode",
			input: EngineerSendPromptInput{ProjectPath: "/tmp/demo", SessionMode: "later", Prompt: "Do the thing"},
			want:  "unsupported engineer session mode",
		},
	}
	for _, tt := range tests {
		_, err := NormalizeEngineerSendPromptInput(tt.input)
		if err == nil {
			t.Fatalf("%s: error = nil, want %q", tt.name, tt.want)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: error = %q, want containing %q", tt.name, err.Error(), tt.want)
		}
	}
}

func TestNormalizeProjectCreateAndStartEngineerInput(t *testing.T) {
	parentPath := filepath.Join(string(filepath.Separator), "tmp", "repos", "..", "repos")
	input, err := NormalizeProjectCreateAndStartEngineerInput(ProjectCreateAndStartEngineerInput{
		RequestID:   " request-1 ",
		ParentPath:  parentPath,
		ProjectName: " KeyMaster ",
		Prompt:      " Build the repository. ",
		Provider:    ProviderAuto,
	})
	if err != nil {
		t.Fatalf("NormalizeProjectCreateAndStartEngineerInput() error = %v", err)
	}
	if input.RequestID != "request-1" || input.ParentPath != filepath.Join(string(filepath.Separator), "tmp", "repos") {
		t.Fatalf("normalized request identity/path = %#v", input)
	}
	if input.ProjectName != "KeyMaster" || input.ProjectPath != filepath.Join(input.ParentPath, "KeyMaster") {
		t.Fatalf("normalized project target = %#v", input)
	}
	if input.TodoText != "Build the repository." || input.Prompt != "Build the repository." || input.Provider != ProviderAuto {
		t.Fatalf("normalized tracked work = %#v", input)
	}
}

func TestNormalizeProjectCreateAndStartEngineerInputRejectsInvalidTargets(t *testing.T) {
	tests := []struct {
		name  string
		input ProjectCreateAndStartEngineerInput
		want  string
	}{
		{
			name:  "relative parent",
			input: ProjectCreateAndStartEngineerInput{ParentPath: "repos", ProjectName: "KeyMaster", Prompt: "Build it."},
			want:  "parent_path must be absolute",
		},
		{
			name:  "nested name",
			input: ProjectCreateAndStartEngineerInput{ParentPath: "/tmp/repos", ProjectName: "team/KeyMaster", Prompt: "Build it."},
			want:  "project_name must be a single folder name",
		},
		{
			name:  "mismatched derived path",
			input: ProjectCreateAndStartEngineerInput{ParentPath: "/tmp/repos", ProjectName: "KeyMaster", ProjectPath: "/tmp/elsewhere", Prompt: "Build it."},
			want:  "project_path must match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeProjectCreateAndStartEngineerInput(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateInvocationNormalizesEngineerSendPromptArgs(t *testing.T) {
	rawArgs := json.RawMessage(`{
		"project_path": " /tmp/demo ",
		"project_name": "",
		"provider": "opencode",
		"session_mode": "new",
		"prompt": "  Fix the failing test.  ",
		"reveal": true
	}`)
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-1 ",
		Capability: CapabilityEngineerSendPrompt,
		Args:       rawArgs,
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-1" {
		t.Fatalf("RequestID = %q, want boss-turn-1", inv.RequestID)
	}
	var input EngineerSendPromptInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-1" {
		t.Fatalf("args request_id = %q, want boss-turn-1", input.RequestID)
	}
	if input.Provider != ProviderOpenCode {
		t.Fatalf("Provider = %q, want %q", input.Provider, ProviderOpenCode)
	}
	if input.SessionMode != SessionModeNew {
		t.Fatalf("SessionMode = %q, want %q", input.SessionMode, SessionModeNew)
	}
	if input.Prompt != "Fix the failing test." {
		t.Fatalf("Prompt = %q, want trimmed prompt", input.Prompt)
	}
	if !input.Reveal {
		t.Fatalf("Reveal = false, want true")
	}
}

func TestValidateInvocationNormalizesAgentTaskCreateArgs(t *testing.T) {
	rawArgs := json.RawMessage(`{
		"title": " Clean suspicious processes ",
		"kind": "",
		"parent_task_id": "",
		"prompt": "  Inspect and terminate stale PIDs.  ",
		"provider": "",
		"reveal": false,
		"capabilities": ["process.inspect", "process.terminate", "process.inspect"],
		"resources": [
			{"kind": "process", "id": "", "path": "", "project_path": "", "provider": "", "session_id": "", "todo_id": 0, "pid": 93624, "port": 0, "label": " hot python "},
			{"kind": "port", "id": "", "path": "", "project_path": "", "provider": "", "session_id": "", "todo_id": 0, "pid": 0, "port": 9229, "label": " debug "}
		]
	}`)
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-task ",
		Capability: CapabilityAgentTaskCreate,
		Args:       rawArgs,
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	var input AgentTaskCreateInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-task" || input.Title != "Clean suspicious processes" {
		t.Fatalf("normalized identity = %#v", input)
	}
	if input.Kind != AgentTaskKindAgent || input.Provider != ProviderAuto {
		t.Fatalf("kind/provider = %q/%q", input.Kind, input.Provider)
	}
	if len(input.Capabilities) != 2 {
		t.Fatalf("capabilities = %#v, want deduped list", input.Capabilities)
	}
	if len(input.Resources) != 2 || input.Resources[0].PID != 93624 || input.Resources[1].Port != 9229 {
		t.Fatalf("resources = %#v", input.Resources)
	}
}

func TestValidateInvocationNormalizesScratchTaskArchiveArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-scratch ",
		Capability: CapabilityScratchTaskArchive,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":""}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-scratch" {
		t.Fatalf("RequestID = %q, want boss-turn-scratch", inv.RequestID)
	}
	var input ScratchTaskArchiveInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-scratch" || input.ProjectPath != "/tmp/demo" {
		t.Fatalf("normalized scratch task archive input = %#v", input)
	}
}

func TestValidateInvocationNormalizesProjectArchiveArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-project-archive ",
		Capability: CapabilityProjectArchive,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":"","action":" restore "}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-project-archive" {
		t.Fatalf("RequestID = %q, want boss-turn-project-archive", inv.RequestID)
	}
	var input ProjectArchiveInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-project-archive" ||
		input.ProjectPath != "/tmp/demo" ||
		input.Action != ProjectArchiveActionUnarchive {
		t.Fatalf("normalized project archive input = %#v", input)
	}
}

func TestValidateInvocationNormalizesTodoAddArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-todo ",
		Capability: CapabilityTodoAdd,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":"","text":"  Follow up on Boss desk TODO visibility.  "}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-todo" {
		t.Fatalf("RequestID = %q, want boss-turn-todo", inv.RequestID)
	}
	var input TodoAddInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-todo" || input.ProjectPath != "/tmp/demo" || input.Text != "Follow up on Boss desk TODO visibility." {
		t.Fatalf("normalized todo add input = %#v", input)
	}
}

func TestValidateInvocationNormalizesTodoCreateWorktreeAndStartEngineerArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-worktree ",
		Capability: CapabilityTodoCreateWorktreeAndStartEngineer,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":" Demo ","todo_text":"  Fix Chat feedback.  ","prompt":"  Implement and verify the feedback.  ","provider":"auto","reveal":false}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	var input TodoCreateWorktreeAndStartEngineerInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if inv.RequestID != "boss-turn-worktree" ||
		input.RequestID != "boss-turn-worktree" ||
		input.ProjectPath != "/tmp/demo" ||
		input.ProjectName != "Demo" ||
		input.TodoText != "Fix Chat feedback." ||
		input.Prompt != "Implement and verify the feedback." ||
		input.Provider != ProviderAuto {
		t.Fatalf("normalized tracked worktree input = %#v", input)
	}
}

func TestValidateInvocationNormalizesProjectCreateAndStartEngineerArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-new-project ",
		Capability: CapabilityProjectCreateAndStartEngineer,
		Args:       json.RawMessage(`{"parent_path":" /tmp/repos/../repos ","project_name":" KeyMaster ","todo_text":"  Build KeyMaster.  ","prompt":"  Create and verify the repository.  ","provider":"codex","reveal":false}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	var input ProjectCreateAndStartEngineerInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if inv.RequestID != "boss-turn-new-project" ||
		input.RequestID != "boss-turn-new-project" ||
		input.ParentPath != "/tmp/repos" ||
		input.ProjectName != "KeyMaster" ||
		input.ProjectPath != "/tmp/repos/KeyMaster" ||
		input.TodoText != "Build KeyMaster." ||
		input.Prompt != "Create and verify the repository." ||
		input.Provider != ProviderCodex {
		t.Fatalf("normalized new-repository input = %#v", input)
	}
}

func TestValidateInvocationNormalizesTodoCompleteArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-todo-complete ",
		Capability: CapabilityTodoComplete,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":" Demo ","todo_id":42,"todo_label":"  tracking  ","todo_text":"  Add tracking.  ","evidence":"  Engineer verified it.  "}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-todo-complete" {
		t.Fatalf("RequestID = %q, want boss-turn-todo-complete", inv.RequestID)
	}
	var input TodoCompleteInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-todo-complete" ||
		input.ProjectPath != "/tmp/demo" ||
		input.ProjectName != "Demo" ||
		input.TodoID != 42 ||
		input.TodoLabel != "tracking" ||
		input.TodoText != "Add tracking." ||
		input.Evidence != "Engineer verified it." {
		t.Fatalf("normalized todo complete input = %#v", input)
	}
}

func TestValidateInvocationNormalizesGitPrepareCommitArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-git ",
		Capability: CapabilityGitPrepareCommit,
		Args:       json.RawMessage(`{"project_path":" /tmp/demo/../demo ","project_name":" Demo ","message":"  Ship   talk cleanup  ","push_after_commit":true}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-git" {
		t.Fatalf("RequestID = %q, want boss-turn-git", inv.RequestID)
	}
	var input GitPrepareCommitInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-git" ||
		input.ProjectPath != "/tmp/demo" ||
		input.ProjectName != "Demo" ||
		input.Message != "Ship talk cleanup" ||
		!input.PushAfterCommit {
		t.Fatalf("normalized git prepare commit input = %#v", input)
	}
}

func TestValidateInvocationNormalizesSettingsUpdateArgs(t *testing.T) {
	inv, err := ValidateInvocation(Invocation{
		RequestID:  " boss-turn-settings ",
		Capability: CapabilitySettingsUpdate,
		Args: json.RawMessage(`{
			"changes": [
				{"field": "exclude_project_patterns", "operation": "add", "value": "", "values": [" tmp-* ", "tmp-*"], "bool_value": false}
			]
		}`),
	})
	if err != nil {
		t.Fatalf("ValidateInvocation() error = %v", err)
	}
	if inv.RequestID != "boss-turn-settings" {
		t.Fatalf("RequestID = %q, want boss-turn-settings", inv.RequestID)
	}
	var input SettingsUpdateInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatalf("decode normalized args: %v", err)
	}
	if input.RequestID != "boss-turn-settings" || len(input.Changes) != 1 {
		t.Fatalf("normalized settings input = %#v", input)
	}
	change := input.Changes[0]
	if change.Field != SettingsFieldExcludeProjectPatterns ||
		change.Operation != SettingsUpdateAppendUnique ||
		len(change.Values) != 1 ||
		change.Values[0] != "tmp-*" {
		t.Fatalf("normalized settings change = %#v", change)
	}
}

func TestValidateInvocationRejectsUnknownCapabilityAndRequestIDMismatch(t *testing.T) {
	if _, err := ValidateInvocation(Invocation{Capability: "project.do_anything"}); err == nil {
		t.Fatalf("ValidateInvocation() error = nil, want unsupported capability error")
	}
	_, err := ValidateInvocation(Invocation{
		RequestID:  "outer",
		Capability: CapabilityEngineerSendPrompt,
		Args:       json.RawMessage(`{"request_id":"inner","project_path":"/tmp/demo","project_name":"","provider":"auto","session_mode":"resume_or_new","prompt":"Do it","reveal":false}`),
	})
	if err == nil {
		t.Fatalf("ValidateInvocation() error = nil, want request_id mismatch")
	}
	if !strings.Contains(err.Error(), "request_id mismatch") {
		t.Fatalf("error = %q, want request_id mismatch", err.Error())
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
