package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const ConfirmationContract = "Controls execute after operator confirmation or under an existing scoped permission, project collaboration grant, or task correction grant. Exact-worker result submission and exact-caller review record bounded metadata immediately. Set resume_on_success only for unfinished work when you will end this turn immediately. Follow end_turn first: end the turn without polling; the host may resume the exact waiting workflow after success. New input, stop, replacement, failure, or host restart suppresses continuation. Otherwise automatic_delivery permits continued authorized work; requires_new_user_turn requires stopping. Saved permissions never expand task scope."

// ListReport returns the transport-neutral progressive control catalog. MCP and
// in-process agent hosts should expose this result rather than duplicating the
// registry's discovery contract.
func ListReport(domainRaw string, authority AuthorityScope, proposalsAvailable bool) (map[string]any, error) {
	domain := NormalizeCapabilityDomain(domainRaw)
	if strings.TrimSpace(domainRaw) != "" && domain == "" {
		return nil, errors.New("unsupported control capability domain")
	}
	return map[string]any{
		"success":               true,
		"authority_scope":       NormalizeAuthorityScope(string(authority)),
		"proposals_available":   proposalsAvailable,
		"domains":               DomainSummaries(),
		"capabilities":          CapabilitySummaries(domain, authority),
		"next_step":             "Call describe_control_capability with one exact capability name before proposing it.",
		"confirmation_contract": ConfirmationContract,
	}, nil
}

// DescribeReport returns one exact registered capability and the shape of the
// progressive proposal call that consumes it.
func DescribeReport(nameRaw string, authority AuthorityScope, proposalTool string) (map[string]any, error) {
	name := CapabilityName(strings.TrimSpace(nameRaw))
	capability, ok := CapabilityByName(name)
	if !ok {
		return nil, errors.New("unknown control capability")
	}
	if !AuthorityAllows(authority, capability.Scope) {
		return nil, fmt.Errorf("control capability %q requires %s scope; this agent has %s scope", capability.Name, capability.Scope, NormalizeAuthorityScope(string(authority)))
	}
	report := map[string]any{
		"success":               true,
		"capability":            capability,
		"confirmation_contract": ConfirmationContract,
		"proposal": map[string]any{
			"tool": strings.TrimSpace(proposalTool),
			"arguments": map[string]any{
				"capability":        capability.Name,
				"arguments":         "Use an object matching capability.input_schema.",
				"request_id":        "Optional stable idempotency key for an exact retry.",
				"resume_on_success": "Optional boolean: request continuation of unfinished work and end this turn immediately. Omit for a final handoff or notification.",
			},
		},
	}
	if NormalizeAuthorityScope(string(authority)) == AuthorityScopeHost {
		delete(report["proposal"].(map[string]any)["arguments"].(map[string]any), "resume_on_success")
		report["confirmation_contract"] = "Help Chat proposals terminate its turn and use the host confirmation dialog; this adapter does not support automatic caller continuation."
	}
	return report, nil
}

// BuildProposedInvocation injects the host-owned request id and validates an
// agent's arguments against the selected typed capability. It does not execute
// or queue the invocation.
func BuildProposedInvocation(requestID string, capabilityName CapabilityName, arguments json.RawMessage) (Invocation, error) {
	if len(strings.TrimSpace(string(arguments))) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return Invocation{}, errors.New("arguments must be a JSON object matching the described input schema")
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	requestID = strings.TrimSpace(requestID)
	encodedRequestID, err := json.Marshal(requestID)
	if err != nil {
		return Invocation{}, err
	}
	payload["request_id"] = encodedRequestID
	normalizedArguments, err := json.Marshal(payload)
	if err != nil {
		return Invocation{}, err
	}
	return ValidateInvocation(Invocation{
		RequestID:  requestID,
		Capability: CapabilityName(strings.TrimSpace(string(capabilityName))),
		Args:       normalizedArguments,
	})
}
