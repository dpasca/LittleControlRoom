package lcagent

import "testing"

func TestParsePhaseWriteGatePayloadAllowsFencedJSON(t *testing.T) {
	payload, err := parsePhaseWriteGatePayload("```json\n{\"allow\":true,\"fits_active_phase\":true,\"reason\":\"ok\"}\n```")
	if err != nil {
		t.Fatalf("parsePhaseWriteGatePayload() error = %v", err)
	}
	if !payload.Allow || !payload.FitsActivePhase || payload.Reason != "ok" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestNormalizePhaseWriteGatePayloadMakesContradictionsBlocking(t *testing.T) {
	payload := normalizePhaseWriteGatePayload(phaseWriteGatePayload{
		Allow:                  true,
		FitsActivePhase:        true,
		ContainsLaterPhaseWork: true,
		Reason:                 "also includes HUD",
	})
	if payload.Allow {
		t.Fatalf("normalized payload allowed later-phase work: %#v", payload)
	}
	if payload.Reason != "also includes HUD" {
		t.Fatalf("reason = %q", payload.Reason)
	}
}
