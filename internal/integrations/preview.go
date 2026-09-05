package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
)

func Preview(change Change) string {
	lines := []string{fmt.Sprintf("Manage %s integrations (%s scope)", change.Provider, change.Scope), ""}
	if change.Scope == "project" {
		lines = append(lines, "Project: "+change.ProjectPath)
	} else {
		lines = append(lines, "Applies to native agent configuration across projects, including sessions outside LCR.")
	}
	switch change.Action {
	case "install_skill":
		lines = append(lines, "Install a skill from: "+firstSource(change.SourcePath, change.GitURL))
		if change.GitRef != "" {
			lines = append(lines, "Git revision: "+change.GitRef)
		}
		if change.Subdirectory != "" {
			lines = append(lines, "Subdirectory: "+change.Subdirectory)
		}
		lines = append(lines, "Existing destination files are preserved. Copied skill scripts are not executed during installation.")
	case "add_mcp":
		if change.MCP != nil {
			lines = append(lines, "Add MCP server: "+change.MCP.Name)
			if change.MCP.URL != "" {
				lines = append(lines, "URL: "+change.MCP.URL)
			} else {
				command, _ := json.Marshal(change.MCP.Command)
				lines = append(lines, "Command arguments: "+string(command))
			}
			if len(change.MCP.EnvVars) > 0 {
				lines = append(lines, "Environment names: "+strings.Join(change.MCP.EnvVars, ", "))
			}
			if len(change.MCP.HeaderEnv) > 0 {
				refs, _ := json.Marshal(change.MCP.HeaderEnv)
				lines = append(lines, "Header environment references: "+string(refs))
			}
		}
	case "install_plugin":
		lines = append(lines, "Install native plugin: "+change.Plugin, "The native package manager may download code. Bundled connections can require separate sign-in.")
	case "set_enabled":
		if change.Enabled != nil {
			lines = append(lines, fmt.Sprintf("Set %s enabled=%t", change.EntryName, *change.Enabled))
		}
	case "remove":
		lines = append(lines, "Remove: "+change.EntryName, "Skill directories are moved to recovery storage; edited configurations are backed up. Native plugin removal may delete its cache; reinstall from the marketplace to restore it.", "A skill file can be shared by more than one agent.")
	case "check_mcp":
		lines = append(lines, "Check MCP server: "+change.EntryName, "Starts a separate connection and lists tools, without calling them. Local server startup executes its configured command.")
	}
	if change.EntryID != "" {
		lines = append(lines, "Inventory entry: "+change.EntryID)
	}
	lines = append(lines, "", ActivationNotice, "", "Enter confirms; Esc cancels.")
	return strings.Join(lines, "\n")
}
func firstSource(local, remote string) string {
	if local != "" {
		return local
	}
	return remote
}
