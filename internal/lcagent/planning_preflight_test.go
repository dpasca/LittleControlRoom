package lcagent

import (
	"strings"
	"testing"
)

func TestPlanningPreflightTemporalVisualRequirementImpliesVisual(t *testing.T) {
	payload, err := parsePlanningPreflightPayload(`{
		"scope": "sizable",
		"needs_preplan": true,
		"artifact_type": "ui",
		"requires_runtime_verification": true,
		"requires_visual_verification": false,
		"requires_temporal_visual_verification": true,
		"reason": "dynamic visual output needs staged verification",
		"suggested_phases": [{"name": "render loop", "acceptance": ["stable across observations"]}]
	}`)
	if err != nil {
		t.Fatalf("parsePlanningPreflightPayload() error = %v", err)
	}
	normalized := normalizePlanningPreflightPayload(payload)
	if !normalized.RequiresTemporalVisualVerification || !normalized.RequiresVisualVerification {
		t.Fatalf("normalized requirements visual=%v temporal=%v, want both true", normalized.RequiresVisualVerification, normalized.RequiresTemporalVisualVerification)
	}
	message := planningPreflightLeadMessage(payload)
	if !strings.Contains(message, "temporal visual verification") || !strings.Contains(message, "one paired observation") || !strings.Contains(message, "recognizable scene or interface") || !strings.Contains(message, "objects are grounded") {
		t.Fatalf("planning lead message missing temporal visual guidance:\n%s", message)
	}
}
