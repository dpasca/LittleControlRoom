package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/integrations"
)

type IntegrationsManageInput struct {
	RequestID string `json:"request_id,omitempty"`
	integrations.Change
}

func IntegrationsManageCapability() Capability {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return Capability{
		Name: CapabilityIntegrationsManage, Domain: CapabilityDomainIntegrations, Scope: AuthorityScopePortfolio,
		Description: "Install skills or native plugins, add MCP connections, toggle or remove integrations, or check an MCP connection for an explicitly chosen agent and scope. First read integrations.list and use its revision. Native configuration changes can also affect agent sessions outside LCR. Never supply literal credentials; use environment-variable references.",
		Risk:        RiskExternal, Confirmation: ConfirmationRequired, RequiresHost: true, Async: true,
		HostEffects: []string{"may_update_agent_configuration", "may_install_agent_integration", "may_launch_mcp_connection_check"},
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"request_id":        str("Stable host request id."),
			"provider":          map[string]any{"type": "string", "enum": EngineerProviderStrings(false)},
			"scope":             map[string]any{"type": "string", "enum": []string{"user", "project"}, "description": "Explicit configuration scope. User changes may affect every project and native sessions outside LCR."},
			"project_path":      str("Exact loaded project path, required only for project scope."),
			"action":            map[string]any{"type": "string", "enum": []string{"install_skill", "add_mcp", "set_enabled", "remove", "install_plugin", "check_mcp"}},
			"expected_revision": str("Exact revision returned by integrations.list for this provider and scope."),
			"entry_id":          str("Exact inventory entry id for set_enabled, remove, or check_mcp. The entry's actions list must include the action."),
			"entry_name":        str("Exact name of that same inventory entry, required with entry_id and verified before execution."),
			"enabled":           map[string]any{"type": "boolean", "description": "Required for set_enabled."},
			"source_path":       str("Absolute self-contained skill directory containing SKILL.md, for install_skill."),
			"git_url":           str("HTTPS Git repository URL without credentials, alternative to source_path."),
			"git_ref":           str("Explicit revision to check out; required with git_url. The resolved commit is retained as provenance."),
			"subdirectory":      str("Relative skill directory within the source repository."),
			"plugin":            str("Exact name@marketplace selector for install_plugin; discovered with integrations.catalog or supplied explicitly."),
			"mcp": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"name":       str("Server name. playwright and lcr_runtime are reserved for LCR."),
				"command":    map[string]any{"type": "array", "items": str("Executable or argument, passed directly without a shell. Never include credentials."), "minItems": 1, "maxItems": 64},
				"url":        str("HTTPS server URL or loopback HTTP. No embedded credentials or query parameters."),
				"env_vars":   map[string]any{"type": "array", "items": str("Environment variable name, not its value.")},
				"header_env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "HTTP header names mapped to existing environment-variable names, never credential values."},
			}, "required": []string{"name"}},
		}, "required": []string{"provider", "scope", "action", "expected_revision"}},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"status": str("Outcome and any remaining activation step."), "integration_result": map[string]any{"type": "object", "description": "Changed and recovery paths, reconnect requirement, or a bounded connection-check result."}}},
	}
}

func validateIntegrationsManageInvocation(inv Invocation) (Invocation, error) {
	var input IntegrationsManageInput
	if err := decodeInvocationArgs(inv.Args, &input); err != nil {
		return Invocation{}, err
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if inv.RequestID != "" && input.RequestID != "" && inv.RequestID != input.RequestID {
		return Invocation{}, fmt.Errorf("request_id mismatch")
	}
	if input.RequestID == "" {
		input.RequestID = inv.RequestID
	}
	change, err := integrations.ValidateChange(input.Change)
	if err != nil {
		return Invocation{}, err
	}
	input.Change = change
	inv.RequestID = input.RequestID
	inv.Args, err = json.Marshal(input)
	return inv, err
}
