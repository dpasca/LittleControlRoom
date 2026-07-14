package boss

import (
	"fmt"
	"sort"
	"strings"

	"lcroom/internal/control"
)

const bossToolControlReference = "control_reference"

func bossControlReferenceForDomain(rawDomain string) (bossToolResult, bool) {
	domain := normalizeBossPlannerDomain(rawDomain)
	capabilityNames, policy := bossControlReferenceSpec(domain)
	if len(capabilityNames) == 0 && len(policy) == 0 {
		return bossToolResult{}, false
	}

	lines := []string{
		"Scoped Chat control reference:",
		"- planner_domain: " + domain,
		"- scope: only the capabilities and goal kinds listed below may be proposed for this turn",
	}
	if len(capabilityNames) > 0 {
		lines = append(lines, "", "Available capabilities:")
		for _, name := range capabilityNames {
			capability, ok := control.CapabilityByName(name)
			if !ok {
				continue
			}
			line := fmt.Sprintf(
				"- %s: %s Risk=%s; confirmation=%s",
				capability.Name,
				strings.TrimSpace(capability.Description),
				capability.Risk,
				capability.Confirmation,
			)
			if len(capability.HostEffects) > 0 {
				line += "; host effects=" + strings.Join(capability.HostEffects, ", ")
			}
			if fields := capabilityInputFields(capability); len(fields) > 0 {
				line += "; input fields=" + strings.Join(fields, ", ")
			}
			lines = append(lines, line)
		}
	}
	if len(policy) > 0 {
		lines = append(lines, "", "Routing and payload policy:")
		for _, rule := range policy {
			if rule = strings.TrimSpace(rule); rule != "" {
				lines = append(lines, "- "+rule)
			}
		}
	}

	return bossToolResult{
		Name:     bossToolControlReference,
		Text:     strings.Join(lines, "\n"),
		Internal: true,
	}, true
}

func bossControlReferenceSpec(domain string) ([]control.CapabilityName, []string) {
	switch normalizeBossPlannerDomain(domain) {
	case bossPlannerDomainProjectWork:
		return []control.CapabilityName{
				control.CapabilityEngineerSendPrompt,
				control.CapabilityProjectCreateAndStartEngineer,
				control.CapabilityTodoCreateWorktreeAndStartEngineer,
				control.CapabilityTodoAdd,
				control.CapabilityTodoComplete,
			}, []string{
				"Use project.create_and_start_engineer only for a brand-new repository target that is not already loaded. Require an unambiguous absolute existing parent directory and one new folder name.",
				"For new implementation, change, fix, or investigation work in an existing loaded project, use todo.create_worktree_and_start_engineer. It creates the durable TODO and isolated worktree before launching the engineer.",
				"Use engineer.send_prompt for an explicit same-task follow-up, active-work steering, or work on an already tracked TODO. With a todo_id, use the TODO owner's project path; the host follows its recorded worktree.",
				"A clarification about a project Chat just created belongs to the same creation TODO/worktree. Continue that task instead of creating another TODO or sibling worktree.",
				"Use todo.add only when the user wants backlog work recorded without starting it now. Use todo.complete only with a known TODO and concise direct evidence.",
				"Do not replace an idle root-checkout engineer session with unrelated new work; idle between turns does not prove its task is finished.",
				"Set provider=auto unless the user explicitly names a provider. Set reveal=true only when the user asks to show or watch the engineer transcript.",
				"For prompt-bearing actions preserve the user's wording, named sources, metric, timeframe, negations, exclusions, and success condition.",
			}
	case bossPlannerDomainAgentTask:
		return []control.CapabilityName{
				control.CapabilityAgentTaskCreate,
				control.CapabilityAgentTaskContinue,
				control.CapabilityAgentTaskClose,
			}, []string{
				"Use agent_task.create for temporary delegated work with no natural loaded project, including host/process/browser/system investigation and fresh external research.",
				"Use agent_task.continue for a known open task that needs another attempt, progress, or a sharper question. Inspect its output first when a vague retry follows a just-created or just-continued task.",
				"Use agent_task.close status=completed when fresh evidence resolves a review/waiting task; use status=archived when the user wants one task removed from the active record.",
				"For cleanup, close_session=false unless the user explicitly asks to close the live engineer session too.",
				"For multiple task records to archive, propose an agent_task_cleanup goal instead of several independent confirmations.",
				"For multiple open tasks needing progress, propose one concrete next agent_task.continue and name what remains.",
				"Preserve the user's source, metric, timeframe, negations, exclusions, and success condition in any prompt sent to a task.",
			}
	case bossPlannerDomainProjectLifecycle:
		return []control.CapabilityName{
				control.CapabilityProjectCreateAndStartEngineer,
				control.CapabilityProjectArchive,
				control.CapabilityScratchTaskArchive,
			}, []string{
				"Use project.create_and_start_engineer only to create, register, and begin tracked work in a brand-new Git repository. Existing loaded projects use the project_work policy instead.",
				"Use project.set_archive_state only for regular loaded projects. A repository root archive/unarchive applies to its linked worktrees through the host.",
				"Use scratch_task.archive only when project metadata identifies kind=scratch_task.",
				"Resolve a specific target with project_detail; for every project matching a term use search_context with include_historical=true before proposing one batch project.set_archive_state action.",
				"Do not infer the target from the hidden TUI cursor and do not guess an ambiguous project, parent directory, or new repository name.",
			}
	case bossPlannerDomainSettings:
		return []control.CapabilityName{control.CapabilitySettingsUpdate}, []string{
			"Use settings.update for Little Control Room application settings, including project scope, privacy mode, reasoning visibility, and the Codex launch preset.",
			"Do not delegate app settings changes through the Little Control Room repository or an engineer session.",
			"Category privacy is managed from category operations, not settings.update.",
			"Put every requested change in settings_changes, using values for list settings, value for scalar settings, and bool_value for boolean settings.",
		}
	case bossPlannerDomainGit:
		return []control.CapabilityName{control.CapabilityGitPrepareCommit}, []string{
			"Use git.prepare_commit for a commit or commit-and-push request on one loaded project. It only opens the existing preview; the operator still confirms the actual commit or push in that dialog.",
			"Set push_after_commit=true only when the user explicitly asked to push.",
			"For multiple projects, propose the first clearly selected preview and name what remains, or ask which project to start with. Never silently drop targets.",
			"Do not use engineer.send_prompt merely to create a Git commit.",
		}
	case bossPlannerDomainGoal:
		return nil, []string{
			"Supported goal kinds are agent_task_cleanup and lcagent_task.",
			"Use agent_task_cleanup when multiple known delegated task records should be archived under one confirmation. Put selected tasks in goal_resources, exclusions in goal_keep_resources, and uncertain tasks in goal_review_resources.",
			"For agent_task_cleanup use allowed capability agent_task.close, max risk write, and forbid closing live engineer sessions or deleting files/workspaces.",
			"Use lcagent_task when the user explicitly wants one scoped, traceable LCAgent task. Use allowed capability agent_task.create, max risk external, and include the authorized project/file/process/session resources.",
		}
	default:
		return nil, nil
	}
}

func capabilityInputFields(capability control.Capability) []string {
	properties, _ := capability.InputSchema["properties"].(map[string]any)
	fields := make([]string, 0, len(properties))
	for field := range properties {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func appendBossControlReference(results []bossToolResult, domain string, emit func(AssistantStreamEvent)) []bossToolResult {
	result, ok := bossControlReferenceForDomain(domain)
	if !ok {
		return results
	}
	call := bossToolControlReference + " " + normalizeBossPlannerDomain(domain)
	emitToolCall(emit, call, "running")
	emitToolCall(emit, call, "done")
	return append(results, result)
}
