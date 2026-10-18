package control

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EngineerCancelWakeupInput withdraws one scheduled wakeup that has not
// started delivery. WakeupID is the engineer message id returned when the
// wakeup was queued (delivery.message_id).
type EngineerCancelWakeupInput struct {
	RequestID string `json:"request_id,omitempty"`
	WakeupID  string `json:"wakeup_id"`
}

func EngineerCancelWakeupCapability() Capability {
	return Capability{
		Name:         CapabilityEngineerCancelWakeup,
		Description:  "Cancel a scheduled wakeup that has not started delivery, such as when the work it was meant to check is already done. Find ids with the engineer.wakeup_list query. An embedded session can cancel only wakeups it scheduled itself, and the cancellation is recorded immediately without operator confirmation; the operator can cancel any wakeup with /wakeups.",
		InputSchema:  engineerCancelWakeupInputSchema(),
		OutputSchema: map[string]any{"type": "object"},
		Risk:         RiskWrite,
		Confirmation: ConfirmationNone,
	}
}

func engineerCancelWakeupInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"request_id": map[string]any{"type": "string", "description": "Optional stable idempotency key for this request."},
			"wakeup_id":  map[string]any{"type": "string", "description": "Exact wakeup id (the engineer message id from the scheduling receipt or engineer.wakeup_list)."},
		},
		"required": []string{"wakeup_id"},
	}
}

func validateEngineerCancelWakeupInvocation(inv Invocation) (Invocation, error) {
	var input EngineerCancelWakeupInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, fmt.Errorf("decode %s args: %w", CapabilityEngineerCancelWakeup, err)
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch between invocation and %s args", CapabilityEngineerCancelWakeup)
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	input.WakeupID = strings.TrimSpace(input.WakeupID)
	if input.WakeupID == "" {
		return Invocation{}, fmt.Errorf("wakeup_id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Invocation{}, err
	}
	inv.RequestID = input.RequestID
	inv.Args = payload
	return inv, nil
}
