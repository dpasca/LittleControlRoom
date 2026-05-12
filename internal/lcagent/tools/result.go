package tools

import "lcroom/internal/lcagent/policy"

func failureResult(err error) ToolResult {
	if err == nil {
		return ToolResult{Success: false}
	}
	result := ToolResult{Success: false, Error: err.Error()}
	if policy.IsDenied(err) {
		result.Denied = true
		result.DenialReason = policy.DenialReason(err)
	}
	return result
}
