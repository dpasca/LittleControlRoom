package helpmeta

import (
	"sort"
	"strings"

	"lcroom/internal/codexslash"
	"lcroom/internal/commands"
	"lcroom/internal/control"
	"lcroom/internal/slashcmd"
)

type TopicKind string

const (
	TopicKindCommand    TopicKind = "command"
	TopicKindCapability TopicKind = "capability"
	TopicKindWorkflow   TopicKind = "workflow"
	TopicKindKeybinding TopicKind = "keybinding"
	TopicKindSetting    TopicKind = "setting"
	TopicKindState      TopicKind = "state_signal"
)

type Surface string

const (
	SurfaceMainTUI          Surface = "main_tui"
	SurfaceEmbeddedEngineer Surface = "embedded_engineer"
	SurfaceChat             Surface = "chat"
	SurfaceControl          Surface = "control"
)

type Topic struct {
	ID          string                   `json:"id"`
	Kind        TopicKind                `json:"kind"`
	Surface     Surface                  `json:"surface"`
	Title       string                   `json:"title"`
	Summary     string                   `json:"summary"`
	Usage       []string                 `json:"usage,omitempty"`
	ManualSteps []string                 `json:"manual_steps,omitempty"`
	CanDoVia    []control.CapabilityName `json:"can_do_via,omitempty"`
	Related     []string                 `json:"related,omitempty"`
	SourceRefs  []string                 `json:"source_refs,omitempty"`
}

func Topics() []Topic {
	topics := make([]Topic, 0, len(commands.Specs())+len(codexslash.Specs())+len(control.Capabilities())+len(CuratedTopics()))
	topics = append(topics, MainCommandTopics()...)
	topics = append(topics, EmbeddedCommandTopics()...)
	topics = append(topics, ControlCapabilityTopics()...)
	topics = append(topics, CuratedTopics()...)
	sortTopics(topics)
	return cloneTopics(topics)
}

func TopicByID(id string) (Topic, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Topic{}, false
	}
	for _, topic := range Topics() {
		if topic.ID == id {
			return topic, true
		}
	}
	return Topic{}, false
}

func TopicsByKind(kind TopicKind) []Topic {
	kind = TopicKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		return nil
	}
	var out []Topic
	for _, topic := range Topics() {
		if topic.Kind == kind {
			out = append(out, topic)
		}
	}
	return out
}

func MainCommandTopics() []Topic {
	return commandTopics(SurfaceMainTUI, "commands.Specs", commands.Specs())
}

func EmbeddedCommandTopics() []Topic {
	return commandTopics(SurfaceEmbeddedEngineer, "codexslash.Specs", codexslash.Specs())
}

func ControlCapabilityTopics() []Topic {
	capabilities := control.Capabilities()
	topics := make([]Topic, 0, len(capabilities))
	for _, capability := range capabilities {
		name := strings.TrimSpace(string(capability.Name))
		if name == "" {
			continue
		}
		topic := Topic{
			ID:          CapabilityTopicID(capability.Name),
			Kind:        TopicKindCapability,
			Surface:     SurfaceControl,
			Title:       name,
			Summary:     strings.TrimSpace(capability.Description),
			Usage:       []string{name},
			ManualSteps: capabilityManualSteps(capability),
			CanDoVia:    []control.CapabilityName{capability.Name},
			Related:     capabilityRelatedTopics(capability),
			SourceRefs:  []string{"control.Capabilities"},
		}
		topics = append(topics, topic)
	}
	sortTopics(topics)
	return cloneTopics(topics)
}

func CuratedTopics() []Topic {
	return cloneTopics([]Topic{
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindKeybinding, "chat"),
			Kind:    TopicKindKeybinding,
			Surface: SurfaceMainTUI,
			Title:   "Open Chat",
			Summary: "Use /chat or the backtick key to open the active Chat overlay.",
			Usage:   []string{"/chat", "`"},
			ManualSteps: []string{
				"Press ` from the main project list to open or hide Chat.",
				"Run /chat from the slash-command palette to open Chat.",
				"Press Esc or ` to hide Chat.",
			},
			Related:    []string{CommandTopicID(SurfaceMainTUI, "chat")},
			SourceRefs: []string{"tui.updateNormalMode", "tui.dispatchCommand", "tui.renderHelpChatOverlay"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindKeybinding, "project-todos"),
			Kind:    TopicKindKeybinding,
			Surface: SurfaceMainTUI,
			Title:   "Open a project's TODO list",
			Summary: "Press t on the selected project, or run /todo, to open that project's TODO dialog.",
			Usage:   []string{"t", "/todo"},
			ManualSteps: []string{
				"Select a project in the main list.",
				"Press t, or open the slash-command palette and run /todo.",
				"In the TODO dialog, use a to add, e to edit, space to mark done, c to start work, and Esc to close.",
			},
			CanDoVia: []control.CapabilityName{control.CapabilityTodoAdd, control.CapabilityTodoComplete},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "todo"),
				CapabilityTopicID(control.CapabilityTodoAdd),
				CapabilityTopicID(control.CapabilityTodoComplete),
			},
			SourceRefs: []string{"tui.updateNormalMode", "tui.todoDialogLegendLine", "commands.Specs"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "start-todo-work"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Start an engineer session from a TODO",
			Summary: "From the TODO dialog, start work on a TODO in the current project or in a dedicated TODO worktree, choosing Codex, OpenCode, Claude Code, or LCAgent.",
			Usage:   []string{"TODO dialog: c", "Start TODO dialog: Enter", "Start TODO dialog: w"},
			ManualSteps: []string{
				"Open the selected project's TODO list with t or /todo.",
				"Select a TODO and press c to open Start TODO.",
				"Choose the agent, optionally toggle a dedicated worktree with w, then press Enter to start the engineer session.",
				"If a pending TODO worktree row appears, press Enter to inspect its status or x to abort it.",
			},
			CanDoVia: []control.CapabilityName{control.CapabilityEngineerSendPrompt},
			Related: []string{
				TopicID(SurfaceMainTUI, TopicKindKeybinding, "project-todos"),
				CommandTopicID(SurfaceMainTUI, "todo"),
				CommandTopicID(SurfaceMainTUI, "codex"),
				CommandTopicID(SurfaceMainTUI, "opencode"),
				CommandTopicID(SurfaceMainTUI, "claude"),
				CommandTopicID(SurfaceMainTUI, "lcagent"),
				CapabilityTopicID(control.CapabilityEngineerSendPrompt),
			},
			SourceRefs: []string{"tui.todoDialogLegendLine", "tui.renderTodoCopyDialogOverlay", "tui.startTodoInProjectPath"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-lanes"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Understand linked worktree lanes",
			Summary: "Linked worktrees are always shown below their repo root with an indented arrow marker.",
			ManualSteps: []string{
				"Find the repo root row in the project list.",
				"Read its linked worktrees directly below it; lanes stay visible without an expand or collapse control.",
				"Use the linked worktree row actions when you need merge, remove, or status operations.",
			},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "wt"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-update-from-parent"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-merge-back"),
			},
			SourceRefs: []string{"tui.buildProjectRows", "tui.renderProjectList"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-update-from-parent"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Update a linked worktree from its parent",
			Summary: "Select a clean linked worktree and use /wt update to merge its recorded parent branch into it without modifying the canonical checkout.",
			Usage:   []string{"/wt update"},
			ManualSteps: []string{
				"Select the linked worktree row under its repo family.",
				"Commit or discard changes, stop its runtime, and let any active engineer turn finish.",
				"Run /wt update to merge the recorded parent branch into the linked worktree; this does not fetch or pull a remote.",
				"If conflicts occur, resolve them in the linked worktree with /resolve or abort the merge before retrying.",
			},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "wt"),
				CommandTopicID(SurfaceMainTUI, "resolve"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-merge-back"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "merge-conflict-recovery"),
			},
			SourceRefs: []string{"tui.updateWorktreeFromParentForSelection", "service.UpdateWorktreeFromParent", "commands.Specs"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-merge-back"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Merge a linked worktree back",
			Summary: "Select a linked worktree and use M or /wt merge to merge it back to its parent branch, with checks for dirty state, runtime activity, linked TODOs, and optional cleanup.",
			Usage:   []string{"M", "/wt merge"},
			ManualSteps: []string{
				"Select the linked worktree row under its repo family.",
				"Press M, or run /wt merge.",
				"Review the confirmation dialog; Little Control Room can stop the runtime, commit dirty worktree changes first, mark a linked TODO done, and remove the merged worktree after merge.",
				"If the merge is blocked by conflicts or dirty root state, resolve that Git state first, then refresh and retry.",
			},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "wt"),
				CommandTopicID(SurfaceMainTUI, "commit"),
				CommandTopicID(SurfaceMainTUI, "diff"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "merge-conflict-recovery"),
			},
			SourceRefs: []string{"tui.worktreeFooterActions", "tui.openWorktreeMergeConfirmForSelection", "tui.mergeBackRulesSummary", "commands.Specs"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-remove-prune"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Remove or prune linked worktrees",
			Summary: "Use x or /wt remove on a linked worktree to remove it, and /wt prune on a root family to clean stale Git worktree registrations.",
			Usage:   []string{"x", "/wt remove", "/wt prune"},
			ManualSteps: []string{
				"Select a linked worktree and press x, or run /wt remove, to open the remove confirmation.",
				"For a clean merged worktree linked to an open TODO, leave Mark linked TODO done enabled to complete the item before removing the checkout.",
				"If the selected row is a pending TODO worktree launch, x aborts the pending launch.",
				"Select a repo root or family and run /wt prune to clean stale Git worktree registrations.",
			},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "wt"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-lanes"),
			},
			SourceRefs: []string{"tui.updateNormalMode", "tui.worktreeFooterActions", "tui.openWorktreeRemoveConfirmForSelection", "commands.Specs"},
		},
		{
			ID:      TopicID(SurfaceMainTUI, TopicKindWorkflow, "merge-conflict-recovery"),
			Kind:    TopicKindWorkflow,
			Surface: SurfaceMainTUI,
			Title:   "Recover from merge conflicts",
			Summary: "When Little Control Room reports unresolved conflicts, use /resolve when available or resolve/abort the Git operation manually, then refresh before retrying commits or worktree merges.",
			Usage:   []string{"/resolve", "/diff", "/commit", "/refresh"},
			ManualSteps: []string{
				"Open /diff to inspect changed files and conflict markers when the project is dirty or conflicted.",
				"Use /resolve if the project exposes the resolve action; otherwise resolve or abort the Git merge/rebase/cherry-pick in the repository.",
				"Run /refresh after resolving the Git state so Little Control Room reloads repo status.",
				"Retry /commit, /wt update, or /wt merge only after the project no longer shows unresolved conflicts.",
			},
			Related: []string{
				CommandTopicID(SurfaceMainTUI, "resolve"),
				CommandTopicID(SurfaceMainTUI, "diff"),
				CommandTopicID(SurfaceMainTUI, "commit"),
				CommandTopicID(SurfaceMainTUI, "refresh"),
				TopicID(SurfaceMainTUI, TopicKindWorkflow, "worktree-merge-back"),
			},
			SourceRefs: []string{"tui.worktreeMergeReadiness", "tui.worktreeFooterActions", "commands.Specs"},
		},
	})
}

func CommandTopicID(surface Surface, name string) string {
	return strings.TrimSpace(string(surface)) + ".command." + normalizeTopicIDPart(name)
}

func CapabilityTopicID(name control.CapabilityName) string {
	return string(SurfaceControl) + ".capability." + normalizeTopicIDPart(string(name))
}

func TopicID(surface Surface, kind TopicKind, name string) string {
	return strings.TrimSpace(string(surface)) + "." + strings.TrimSpace(string(kind)) + "." + normalizeTopicIDPart(name)
}

func commandTopics(surface Surface, sourceRef string, specs []slashcmd.Spec) []Topic {
	topics := make([]Topic, 0, len(specs))
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		name := strings.TrimSpace(spec.Name)
		usage := strings.TrimSpace(spec.Usage)
		if name == "" || usage == "" {
			continue
		}
		topics = append(topics, Topic{
			ID:          CommandTopicID(surface, name),
			Kind:        TopicKindCommand,
			Surface:     surface,
			Title:       usage,
			Summary:     strings.TrimSpace(spec.Summary),
			Usage:       []string{usage},
			ManualSteps: []string{"Run " + usage + "."},
			Related:     commandRelatedTopics(surface, name),
			SourceRefs:  []string{strings.TrimSpace(sourceRef)},
		})
	}
	sortTopics(topics)
	return cloneTopics(topics)
}

func commandRelatedTopics(surface Surface, name string) []string {
	switch surface {
	case SurfaceMainTUI:
		switch strings.TrimSpace(name) {
		case "codex", "codex-new", "opencode", "opencode-new", "claude", "claude-new", "lcagent", "lcagent-new":
			return []string{CommandTopicID(SurfaceEmbeddedEngineer, "new"), CommandTopicID(SurfaceEmbeddedEngineer, "sessions")}
		}
	case SurfaceEmbeddedEngineer:
		switch strings.TrimSpace(name) {
		case "help":
			return []string{CommandTopicID(SurfaceMainTUI, "help")}
		}
	}
	return nil
}

func capabilityManualSteps(capability control.Capability) []string {
	if capability.Confirmation == control.ConfirmationRequired {
		return []string{"Confirm the proposal before Little Control Room changes state or contacts an engineer session."}
	}
	return nil
}

func capabilityRelatedTopics(capability control.Capability) []string {
	switch capability.Name {
	case control.CapabilityEngineerSendPrompt:
		return []string{
			CommandTopicID(SurfaceMainTUI, "codex"),
			CommandTopicID(SurfaceMainTUI, "opencode"),
			CommandTopicID(SurfaceMainTUI, "claude"),
			CommandTopicID(SurfaceMainTUI, "lcagent"),
		}
	case control.CapabilityTodoAdd, control.CapabilityTodoComplete:
		return []string{CommandTopicID(SurfaceMainTUI, "todo")}
	case control.CapabilityGitPrepareCommit:
		return []string{CommandTopicID(SurfaceMainTUI, "commit")}
	case control.CapabilitySettingsUpdate:
		return []string{CommandTopicID(SurfaceMainTUI, "settings"), CommandTopicID(SurfaceMainTUI, "setup")}
	case control.CapabilityProjectArchive, control.CapabilityScratchTaskArchive:
		return []string{CommandTopicID(SurfaceMainTUI, "archive"), CommandTopicID(SurfaceMainTUI, "unarchive"), CommandTopicID(SurfaceMainTUI, "remove")}
	default:
		return nil
	}
}

func normalizeTopicIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func sortTopics(topics []Topic) {
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Surface != topics[j].Surface {
			return topics[i].Surface < topics[j].Surface
		}
		if topics[i].Kind != topics[j].Kind {
			return topics[i].Kind < topics[j].Kind
		}
		return topics[i].ID < topics[j].ID
	})
}

func cloneTopics(topics []Topic) []Topic {
	out := make([]Topic, len(topics))
	for i, topic := range topics {
		out[i] = cloneTopic(topic)
	}
	return out
}

func cloneTopic(topic Topic) Topic {
	topic.Usage = append([]string(nil), topic.Usage...)
	topic.ManualSteps = append([]string(nil), topic.ManualSteps...)
	topic.CanDoVia = append([]control.CapabilityName(nil), topic.CanDoVia...)
	topic.Related = append([]string(nil), topic.Related...)
	topic.SourceRefs = append([]string(nil), topic.SourceRefs...)
	return topic
}
