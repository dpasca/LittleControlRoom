package codexapp

import (
	"reflect"
	"strings"
	"testing"

	"lcroom/internal/browserctl"
)

func TestManagedTurnContextCarriesReportContractOnlyWhenRequested(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		session := &appServerSession{reportContract: enabled}
		entry, present := session.managedTurnContext()[reportContractContextSource]
		if present != enabled {
			t.Fatalf("report contract present=%v, enabled=%v", present, enabled)
		}
		if !enabled {
			continue
		}
		if entry.Kind != applicationContextKind || entry.Value != ReportContractInstructions() {
			t.Fatalf("report contract entry = %#v, want application-context instructions", entry)
		}
	}
}

func TestReportContractRidesClaudeSystemPromptWithoutMCPServers(t *testing.T) {
	options, err := buildClaudeMCPOptions(LaunchRequest{
		Provider:         ProviderClaudeCode,
		ProjectPath:      "/tmp/demo",
		ReportContract:   true,
		PlaywrightPolicy: browserctl.Policy{ManagementMode: browserctl.ManagementModeLegacy},
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	if options.Config != "" || options.ReportContract != ReportContractInstructions() {
		t.Fatalf("options = %#v, want contract without MCP config", options)
	}
	got := claudeTurnArgsWithMCP("", "", "", "acceptEdits", options, "")
	want := append(claudeTurnArgs("", "", "", "acceptEdits"), "--append-system-prompt", ReportContractInstructions())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithMCP() = %#v, want %#v", got, want)
	}
}

func TestReportContractJoinsMCPPromptInOneSystemPromptFlag(t *testing.T) {
	const config = `{"mcpServers":{"lcr_runtime":{"type":"stdio","command":"/tmp/lcroom"}}}`
	got := claudeTurnArgsWithMCP("", "", "", "acceptEdits", claudeMCPOptions{
		Config:         config,
		Prompt:         "mcp guidance",
		ReportContract: ReportContractInstructions(),
	}, "")
	want := append(claudeTurnArgs("", "", "", "acceptEdits"),
		"--mcp-config", config,
		"--append-system-prompt", "mcp guidance\n\n"+ReportContractInstructions(),
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeTurnArgsWithMCP() = %#v, want %#v", got, want)
	}
}

func TestReportContractIsOffUnlessRequested(t *testing.T) {
	options, err := buildClaudeMCPOptions(LaunchRequest{Provider: ProviderClaudeCode, ProjectPath: "/tmp/demo"})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	if options.ReportContract != "" {
		t.Fatalf("ReportContract = %q, want empty without LaunchRequest.ReportContract", options.ReportContract)
	}
}

func TestProviderCarriesReportContract(t *testing.T) {
	for provider, want := range map[Provider]bool{
		ProviderCodex:      true,
		ProviderClaudeCode: true,
		ProviderOpenCode:   false,
		ProviderLCAgent:    false,
		"":                 true, // launches default to Codex
	} {
		if got := ProviderCarriesReportContract(provider); got != want {
			t.Fatalf("ProviderCarriesReportContract(%q) = %v, want %v", provider, got, want)
		}
	}
}

func TestReportContractLinesReturnsCopy(t *testing.T) {
	lines := ReportContractLines()
	lines[0] = "mutated"
	if strings.Contains(ReportContractInstructions(), "mutated") || ReportContractLines()[0] == "mutated" {
		t.Fatal("ReportContractLines() exposed shared state")
	}
}
