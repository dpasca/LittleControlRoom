package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntegrationsCapabilityRequiresConfirmationAndStrictInputs(t *testing.T) {
	capability, ok := CapabilityByName(CapabilityIntegrationsManage)
	if !ok || capability.Confirmation != ConfirmationRequired || !capability.Async || capability.Scope != AuthorityScopePortfolio {
		t.Fatalf("unsafe capability: %#v", capability)
	}
	args := `{"provider":"opencode","scope":"user","action":"add_mcp","expected_revision":"` + strings.Repeat("a", 64) + `","mcp":{"name":"example","command":["example"]}}`
	inv, err := ValidateInvocation(Invocation{Capability: CapabilityIntegrationsManage, RequestID: "request-test", Args: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	var input IntegrationsManageInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		t.Fatal(err)
	}
	if input.RequestID != "request-test" || input.Provider != "opencode" {
		t.Fatalf("normalization lost fields: %#v", input)
	}
	for _, bad := range []string{strings.Replace(args, `"example"]`, `"example"],"token":"secret"`, 1), strings.Replace(args, `"provider"`, `"unrecognized":true,"provider"`, 1)} {
		if _, err := ValidateInvocation(Invocation{Capability: CapabilityIntegrationsManage, Args: json.RawMessage(bad)}); err == nil {
			t.Fatal("unknown or credential field accepted")
		}
	}
}
