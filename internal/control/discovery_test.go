package control

import (
	"encoding/json"
	"testing"
)

func TestControlDiscoveryReportsUseRegistryAndAuthority(t *testing.T) {
	report, err := ListReport("settings", AuthorityScopePortfolio, true)
	if err != nil {
		t.Fatalf("ListReport() error = %v", err)
	}
	capabilities, ok := report["capabilities"].([]CapabilitySummary)
	if !ok {
		t.Fatalf("capabilities = %#v", report["capabilities"])
	}
	if len(capabilities) != 0 {
		t.Fatalf("portfolio settings capabilities = %#v, want none", capabilities)
	}

	hostReport, err := ListReport("settings", AuthorityScopeHost, true)
	if err != nil {
		t.Fatalf("host ListReport() error = %v", err)
	}
	hostCapabilities := hostReport["capabilities"].([]CapabilitySummary)
	if len(hostCapabilities) != 1 || hostCapabilities[0].Name != CapabilitySettingsUpdate {
		t.Fatalf("host settings capabilities = %#v", hostCapabilities)
	}
	if hostReport["confirmation_contract"] != ConfirmationContract {
		t.Fatalf("confirmation contract = %#v", hostReport["confirmation_contract"])
	}
}

func TestControlDescribeReportRejectsCapabilityOutsideAuthority(t *testing.T) {
	if _, err := DescribeReport(string(CapabilitySettingsUpdate), AuthorityScopePortfolio, "propose"); err == nil {
		t.Fatal("DescribeReport() error = nil, want authority rejection")
	}
	report, err := DescribeReport(string(CapabilityTodoAdd), AuthorityScopePortfolio, "propose")
	if err != nil {
		t.Fatalf("DescribeReport() error = %v", err)
	}
	capability, ok := report["capability"].(Capability)
	if !ok || capability.Name != CapabilityTodoAdd {
		t.Fatalf("capability = %#v", report["capability"])
	}
}

func TestBuildProposedInvocationInjectsTrustedRequestID(t *testing.T) {
	invocation, err := BuildProposedInvocation("host-request", CapabilityTodoAdd, json.RawMessage(`{
		"request_id":"model-request",
		"project_path":"/tmp/project",
		"project_name":"project",
		"text":"Track this"
	}`))
	if err != nil {
		t.Fatalf("BuildProposedInvocation() error = %v", err)
	}
	if invocation.RequestID != "host-request" {
		t.Fatalf("request id = %q", invocation.RequestID)
	}
	var input TodoAddInput
	if err := json.Unmarshal(invocation.Args, &input); err != nil {
		t.Fatalf("decode invocation: %v", err)
	}
	if input.RequestID != "host-request" || input.Text != "Track this" {
		t.Fatalf("input = %#v", input)
	}
}
