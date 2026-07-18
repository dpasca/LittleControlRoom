package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/slashcmd"
)

type Kind string

const (
	KindChat            Kind = "chat"
	KindAIStats         Kind = "ai-stats"
	KindPerf            Kind = "perf"
	KindErrors          Kind = "errors"
	KindRefresh         Kind = "refresh"
	KindRepairTerminal  Kind = "repair-terminal"
	KindUpdate          Kind = "update"
	KindSort            Kind = "sort"
	KindNonAIFolders    Kind = "non-ai-folders"
	KindTab             Kind = "tab"
	KindSetup           Kind = "setup"
	KindSettings        Kind = "settings"
	KindSkills          Kind = "skills"
	KindFilter          Kind = "filter"
	KindCategory        Kind = "category"
	KindNewProject      Kind = "new-project"
	KindNewTask         Kind = "new-task"
	KindTaskActions     Kind = "task-actions"
	KindOpen            Kind = "open"
	KindTerminal        Kind = "terminal"
	KindRun             Kind = "run"
	KindRestart         Kind = "restart"
	KindRunEdit         Kind = "run-edit"
	KindRuntime         Kind = "runtime"
	KindMobile          Kind = "mobile"
	KindCPU             Kind = "cpu"
	KindPorts           Kind = "ports"
	KindStop            Kind = "stop"
	KindDiff            Kind = "diff"
	KindCommit          Kind = "commit"
	KindPush            Kind = "push"
	KindPull            Kind = "pull"
	KindResolve         Kind = "resolve"
	KindIntegrity       Kind = "integrity"
	KindCodex           Kind = "codex"
	KindCodexNew        Kind = "codex-new"
	KindClaude          Kind = "claude"
	KindClaudeNew       Kind = "claude-new"
	KindOpenCode        Kind = "opencode"
	KindOpenCodeNew     Kind = "opencode-new"
	KindLCAgent         Kind = "lcagent"
	KindLCAgentNew      Kind = "lcagent-new"
	KindTodo            Kind = "todo"
	KindWorktreeUpdate  Kind = "worktree-update"
	KindWorktreeMerge   Kind = "worktree-merge"
	KindWorktreeRemove  Kind = "worktree-remove"
	KindWorktreePrune   Kind = "worktree-prune"
	KindPin             Kind = "pin"
	KindRead            Kind = "read"
	KindUnread          Kind = "unread"
	KindSnooze          Kind = "snooze"
	KindClearSnooze     Kind = "clear-snooze"
	KindSession         Kind = "session"
	KindSessions        Kind = "sessions"
	KindEvents          Kind = "events"
	KindIgnore          Kind = "ignore"
	KindIgnored         Kind = "ignored"
	KindArchive         Kind = "archive"
	KindUnarchive       Kind = "unarchive"
	KindRemove          Kind = "remove"
	KindFocus           Kind = "focus"
	KindPrivacy         Kind = "privacy"
	KindPrivacySettings Kind = "privacy-settings"
	KindQuit            Kind = "quit"
)

type SortMode string

const (
	SortAttention SortMode = "attention"
	SortRecent    SortMode = "recent"
)

type ProjectTab string

const (
	ProjectTabMain     ProjectTab = "main"
	ProjectTabActive   ProjectTab = ProjectTabMain
	ProjectTabCategory ProjectTab = "category"
	ProjectTabArchived ProjectTab = "archived"
	ProjectTabToggle   ProjectTab = "toggle"
)

type CategoryAction string

const (
	CategoryActionCreate CategoryAction = "create"
	CategoryActionRemove CategoryAction = "remove"
	CategoryActionMove   CategoryAction = "move"
	CategoryActionClear  CategoryAction = "clear"
)

type ToggleMode string

const (
	ToggleOn     ToggleMode = "on"
	ToggleOff    ToggleMode = "off"
	ToggleToggle ToggleMode = "toggle"
)

type FocusTarget string

const (
	FocusList    FocusTarget = "list"
	FocusDetail  FocusTarget = "detail"
	FocusRuntime FocusTarget = "runtime"
)

type Spec = slashcmd.Spec

type Suggestion = slashcmd.Suggestion

type Invocation struct {
	Kind           Kind
	Sort           SortMode
	Tab            ProjectTab
	CategoryAction CategoryAction
	CategoryName   string
	Toggle         ToggleMode
	Focus          FocusTarget
	Duration       time.Duration
	Message        string
	Prompt         string
	Command        string
	Filter         string
	Assistant      string
	All            bool
	Clear          bool
	Canonical      string
}

var specs = []Spec{
	{Name: "chat", Usage: "/chat", Summary: "Open Chat"},
	{Name: "help", Usage: "/help", Summary: "Alias for /chat", Hidden: true},
	{Name: "ai", Usage: "/ai", Summary: "Open the internal AI stats dialog"},
	{Name: "perf", Usage: "/perf", Summary: "Open the internal responsiveness and wait tracker"},
	{Name: "errors", Usage: "/errors", Summary: "Open the recent error log"},
	{Name: "refresh", Usage: "/refresh", Summary: "Rescan projects and retry failed assessments"},
	{Name: "repair-terminal", Usage: "/repair-terminal", Summary: "Reinitialize terminal display and paste handling"},
	{Name: "update", Usage: "/update", Summary: "Check for and install a newer GitHub release"},
	{Name: "sort", Usage: "/sort attention|recent", Summary: "Set list ordering"},
	{Name: "non-ai-folders", Usage: "/non-ai-folders on|off", Summary: "Show or hide folders without AI activity"},
	{Name: "tab", Usage: "/tab [main|archived|toggle|category]", Summary: "Switch the Main, custom category, or Archived project-list tab"},
	{Name: "settings", Usage: "/settings", Summary: "Edit onboarding, AI, scope, browser, and advanced settings"},
	{Name: "skills", Usage: "/skills", Summary: "Review Codex skills and local duplicates that may be stale"},
	{Name: "setup", Usage: "/setup", Summary: "Open the friendly AI setup concierge"},
	{Name: "filter", Usage: "/filter [text|clear]", Summary: "Temporarily show only matching project names"},
	{Name: "category", Usage: "/category create|remove|move|clear [name]", Summary: "Create categories or move the selected item between category tabs"},
	{Name: "new-project", Usage: "/new-project [--assistant codex|opencode|claude|lcagent]", Summary: "Create a project folder, or paste an existing path to add it"},
	{Name: "new-task", Usage: "/new-task [--assistant codex|opencode|claude|lcagent] [request]", Summary: "Create a scratch task folder without stopping to name it"},
	{Name: "task-actions", Usage: "/task-actions", Summary: "Open archive/delete actions for the selected scratch task"},
	{Name: "open", Usage: "/open", Summary: "Open the selected project's folder in the system browser"},
	{Name: "terminal", Usage: "/terminal", Summary: "Open a system terminal in the selected project's folder"},
	{Name: "run", Usage: "/run [command]", Summary: "Start the selected project's managed runtime"},
	{Name: "start", Usage: "/start [command]", Summary: "Alias for /run"},
	{Name: "restart", Usage: "/restart", Summary: "Restart the selected project's managed runtime"},
	{Name: "run-edit", Usage: "/run-edit", Summary: "Edit the selected project's saved run command"},
	{Name: "runtime", Usage: "/runtime", Summary: "Focus the selected project's runtime pane"},
	{Name: "mobile", Usage: "/mobile", Summary: "Open mobile status, phone URL, pairing, and setup"},
	{Name: "cpu", Usage: "/cpu", Summary: "Inspect top CPU processes"},
	{Name: "ports", Usage: "/ports", Summary: "Inspect project-local TCP listeners"},
	{Name: "stop", Usage: "/stop", Summary: "Stop the selected project's managed runtime"},
	{Name: "diff", Usage: "/diff", Summary: "Open a full-screen diff for the selected project"},
	{Name: "commit", Usage: "/commit [message]", Summary: "Preview a commit; Alt+Enter also pushes when available"},
	{Name: "push", Usage: "/push", Summary: "Push the selected project when its branch is ahead"},
	{Name: "pull", Usage: "/pull", Summary: "Pull the selected project when its branch is behind"},
	{Name: "resolve", Usage: "/resolve", Summary: "Resolve merge conflicts in the background with project-row progress"},
	{Name: "integrity", Usage: "/integrity", Summary: "Inspect a displaced repository root and choose a safe response"},
	{Name: "codex", Usage: "/codex [prompt]", Summary: "Resume the selected project's latest Codex session, or start a new one"},
	{Name: "codex-new", Usage: "/codex-new [prompt]", Summary: "Start a fresh Codex session in the selected project"},
	{Name: "claude", Usage: "/claude [prompt]", Summary: "Resume the selected project's latest Claude Code session, or start a new one"},
	{Name: "claude-new", Usage: "/claude-new [prompt]", Summary: "Start a fresh Claude Code session in the selected project"},
	{Name: "opencode", Usage: "/opencode [prompt]", Summary: "Resume the selected project's latest OpenCode session, or start a new one"},
	{Name: "opencode-new", Usage: "/opencode-new [prompt]", Summary: "Start a fresh OpenCode session in the selected project"},
	{Name: "lcagent", Usage: "/lcagent [prompt]", Summary: "Resume the selected project's latest experimental LCAgent session, or start a new one"},
	{Name: "lcagent-new", Usage: "/lcagent-new [prompt]", Summary: "Start a fresh experimental LCAgent session in the selected project"},
	{Name: "todo", Usage: "/todo", Summary: "Open the selected project's TODO list"},
	{Name: "wt", Usage: "/wt update|merge|remove|prune", Summary: "Manage the selected worktree"},
	{Name: "pin", Usage: "/pin", Summary: "Toggle pin on the selected project"},
	{Name: "read", Usage: "/read [all]", Summary: "Mark the selected project, or all visible projects, as read"},
	{Name: "unread", Usage: "/unread", Summary: "Mark the selected project as unread"},
	{Name: "snooze", Usage: "/snooze [duration|off]", Summary: "Snooze the selected project or clear with /snooze off"},
	{Name: "clear-snooze", Usage: "/clear-snooze", Summary: "Clear snooze on the selected project"},
	{Name: "unsnooze", Usage: "/unsnooze", Summary: "Clear snooze on the selected project"},
	{Name: "session", Usage: "/session", Summary: "Open the legacy embedded session picker", Hidden: true},
	{Name: "sessions", Usage: "/sessions on|off|toggle", Summary: "Show or hide the Sessions section"},
	{Name: "events", Usage: "/events on|off|toggle", Summary: "Show or hide Recent events"},
	{Name: "ignore", Usage: "/ignore", Summary: "Hide the selected project's exact name"},
	{Name: "ignored", Usage: "/ignored", Summary: "Review ignored project names and restore them"},
	{Name: "archive", Usage: "/archive", Summary: "Archive one or more projects or the selected scratch task"},
	{Name: "unarchive", Usage: "/unarchive", Summary: "Move the selected project out of Archived when it is in scope"},
	{Name: "remove", Usage: "/remove", Summary: "Confirm, then make the selected item go away safely"},
	{Name: "focus", Usage: "/focus list|detail|runtime", Summary: "Move focus between panes"},
	{Name: "privacy", Usage: "/privacy on|off|toggle|settings", Summary: "Toggle demo privacy mode or open privacy settings"},
	{Name: "quit", Usage: "/quit", Summary: "Quit the TUI"},
}

func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

func Suggestions(input string) []Suggestion {
	return SuggestionsWithCategories(input, nil)
}

func SuggestionsWithCategories(input string, categoryNames []string) []Suggestion {
	trimmed := strings.TrimLeft(input, " \t\r\n")
	if trimmed == "" {
		trimmed = "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return slashcmd.NameSuggestions(specs, "")
	}

	hasTrailingSpace := strings.HasSuffix(trimmed, " ")
	fields := strings.Fields(body)
	namePrefix := strings.ToLower(fields[0])
	if len(fields) == 1 && !hasTrailingSpace {
		return slashcmd.NameSuggestions(specs, namePrefix)
	}

	switch namePrefix {
	case "sort":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/sort ", argPrefix,
			choice("attention", "Sort by attention score"),
			choice("recent", "Sort by recent activity"),
		)
	case "non-ai-folders":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/non-ai-folders ", argPrefix,
			choice("on", "Include folders without AI activity"),
			choice("off", "Hide folders without AI activity"),
		)
	case "tab", "tabs":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.TrimSpace(body[len(fields[0]):])
			if !hasTrailingSpace {
				argPrefix = strings.TrimSpace(argPrefix)
			}
		}
		choices := []slashcmd.Choice{
			choice("main", "Show the Main project-list tab"),
			choice("archived", "Show the Archived project-list tab"),
			choice("toggle", "Switch to the other project-list tab"),
		}
		for _, name := range categoryNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			choices = append(choices, choice(name, "Show the "+name+" category tab"))
		}
		return slashcmd.EnumSuggestions("/tab ", argPrefix, choices...)
	case "category":
		_, rawArgs := slashcmd.SplitCommandBody(body)
		action, rest := slashcmd.SplitCommandBody(rawArgs)
		action = strings.TrimSpace(action)
		if action == "" {
			return slashcmd.EnumSuggestions("/category ", action,
				choice("move", "Move the selected item to a category"),
				choice("clear", "Move the selected item back to Main"),
				choice("create", "Create a custom category tab"),
				choice("remove", "Remove a custom category tab"),
			)
		}
		switch strings.ToLower(action) {
		case "move", "set":
			return categoryNameSuggestions("/category move ", rest, categoryNames, "Move the selected item to this category")
		case "remove", "delete", "rm":
			return categoryNameSuggestions("/category remove ", rest, categoryNames, "Remove this category tab")
		default:
			if rest == "" && !hasTrailingSpace {
				return slashcmd.EnumSuggestions("/category ", action,
					choice("move", "Move the selected item to a category"),
					choice("clear", "Move the selected item back to Main"),
					choice("create", "Create a custom category tab"),
					choice("remove", "Remove a custom category tab"),
				)
			}
			return nil
		}
	case "filter":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/filter ", argPrefix,
			choice("clear", "Remove the active project-name filter"),
		)
	case "sessions":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/sessions ", argPrefix,
			choice("toggle", "Flip the Sessions section"),
			choice("on", "Show the Sessions section"),
			choice("off", "Hide the Sessions section"),
		)
	case "events":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/events ", argPrefix,
			choice("toggle", "Flip the Recent events section"),
			choice("on", "Show the Recent events section"),
			choice("off", "Hide the Recent events section"),
		)
	case "focus":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/focus ", argPrefix,
			choice("list", "Focus the project list"),
			choice("detail", "Focus the detail pane"),
			choice("runtime", "Focus the runtime pane"),
		)
	case "snooze":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/snooze ", argPrefix,
			choice("off", "Clear snooze"),
			choice("1h", "Snooze for 1 hour"),
			choice("4h", "Snooze for 4 hours"),
			choice("24h", "Snooze for 24 hours"),
		)
	case "read":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/read ", argPrefix,
			choice("all", "Mark all visible projects as read"),
		)
	case "wt", "worktree":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/wt ", argPrefix,
			choice("update", "Merge the recorded parent branch into the selected linked worktree"),
			choice("merge", "Merge the selected linked worktree back into its parent branch"),
			choice("remove", "Remove the selected linked worktree"),
			choice("prune", "Prune stale git worktree registrations for the current repo"),
		)
	case "privacy":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/privacy ", argPrefix,
			choice("toggle", "Flip demo privacy mode"),
			choice("on", "Enable demo privacy mode"),
			choice("off", "Disable demo privacy mode"),
			choice("settings", "Open privacy settings"),
		)
	default:
		return slashcmd.NameSuggestions(specs, namePrefix)
	}
}

func Parse(input string) (Invocation, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return Invocation{}, fmt.Errorf("command required")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return Invocation{}, fmt.Errorf("slash command must start with /")
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return Invocation{}, fmt.Errorf("command required")
	}

	name, rawArgs := slashcmd.SplitCommandBody(body)
	switch strings.ToLower(name) {
	case "chat", "help":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /chat")
		}
		return Invocation{Kind: KindChat, Canonical: "/chat"}, nil
	case "ai", "stats":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /ai")
		}
		return Invocation{Kind: KindAIStats, Canonical: "/ai"}, nil
	case "perf":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /perf")
		}
		return Invocation{Kind: KindPerf, Canonical: "/perf"}, nil
	case "error", "errors", "log":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /errors")
		}
		return Invocation{Kind: KindErrors, Canonical: "/errors"}, nil
	case "refresh":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /refresh")
		}
		return Invocation{Kind: KindRefresh, Canonical: "/refresh"}, nil
	case "repair-terminal":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /repair-terminal")
		}
		return Invocation{Kind: KindRepairTerminal, Canonical: "/repair-terminal"}, nil
	case "update":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /update")
		}
		return Invocation{Kind: KindUpdate, Canonical: "/update"}, nil
	case "sort":
		mode, err := parseSortMode(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindSort, Sort: mode, Canonical: "/sort " + string(mode)}, nil
	case "non-ai-folders":
		mode, err := parseNonAIFoldersMode(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindNonAIFolders, Toggle: mode, Canonical: "/non-ai-folders " + string(mode)}, nil
	case "tab", "tabs":
		tab, err := parseProjectTab(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		if tab == ProjectTabCategory {
			categoryName := strings.TrimSpace(rawArgs)
			return Invocation{Kind: KindTab, Tab: tab, CategoryName: categoryName, Canonical: slashcmd.CanonicalCommand("tab", categoryName)}, nil
		}
		return Invocation{Kind: KindTab, Tab: tab, Canonical: "/tab " + string(tab)}, nil
	case "setup":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /setup")
		}
		return Invocation{Kind: KindSetup, Canonical: "/setup"}, nil
	case "settings":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /settings")
		}
		return Invocation{Kind: KindSettings, Canonical: "/settings"}, nil
	case "skills":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /skills")
		}
		return Invocation{Kind: KindSkills, Canonical: "/skills"}, nil
	case "filter":
		switch strings.ToLower(strings.TrimSpace(rawArgs)) {
		case "":
			return Invocation{Kind: KindFilter, Canonical: "/filter"}, nil
		case "clear":
			return Invocation{Kind: KindFilter, Clear: true, Canonical: "/filter clear"}, nil
		default:
			return Invocation{
				Kind:      KindFilter,
				Filter:    strings.TrimSpace(rawArgs),
				Canonical: slashcmd.CanonicalCommand("filter", rawArgs),
			}, nil
		}
	case "category", "categories":
		return parseCategoryCommand(rawArgs)
	case "new-project":
		assistant, rest, err := parseAssistantArg(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		if rest != "" {
			return Invocation{}, fmt.Errorf("usage: /new-project [--assistant codex|opencode|claude|lcagent]")
		}
		canonical := "/new-project"
		if assistant != "" {
			canonical += " --assistant " + assistant
		}
		return Invocation{Kind: KindNewProject, Assistant: assistant, Canonical: canonical}, nil
	case "new-task":
		assistant, prompt, err := parseAssistantArg(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		canonical := slashcmd.CanonicalCommand("new-task", prompt)
		if assistant != "" {
			canonical = slashcmd.CanonicalCommand("new-task", "--assistant "+assistant+" "+prompt)
		}
		return Invocation{
			Kind:      KindNewTask,
			Prompt:    strings.TrimSpace(prompt),
			Assistant: assistant,
			Canonical: canonical,
		}, nil
	case "task-actions":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /task-actions")
		}
		return Invocation{Kind: KindTaskActions, Canonical: "/task-actions"}, nil
	case "open":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /open")
		}
		return Invocation{Kind: KindOpen, Canonical: "/open"}, nil
	case "terminal":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /terminal")
		}
		return Invocation{Kind: KindTerminal, Canonical: "/terminal"}, nil
	case "run", "start":
		return Invocation{
			Kind:      KindRun,
			Command:   strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("run", rawArgs),
		}, nil
	case "restart":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /restart")
		}
		return Invocation{Kind: KindRestart, Canonical: "/restart"}, nil
	case "run-edit":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /run-edit")
		}
		return Invocation{Kind: KindRunEdit, Canonical: "/run-edit"}, nil
	case "runtime":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /runtime")
		}
		return Invocation{Kind: KindRuntime, Canonical: "/runtime"}, nil
	case "mobile":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /mobile")
		}
		return Invocation{Kind: KindMobile, Canonical: "/mobile"}, nil
	case "cpu":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /cpu")
		}
		return Invocation{Kind: KindCPU, Canonical: "/cpu"}, nil
	case "ports":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /ports")
		}
		return Invocation{Kind: KindPorts, Canonical: "/ports"}, nil
	case "stop":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /stop")
		}
		return Invocation{Kind: KindStop, Canonical: "/stop"}, nil
	case "diff":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /diff")
		}
		return Invocation{Kind: KindDiff, Canonical: "/diff"}, nil
	case "todo":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /todo")
		}
		return Invocation{Kind: KindTodo, Canonical: "/todo"}, nil
	case "wt", "worktree":
		return parseWorktreeCommand(rawArgs)
	case "pin":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /pin")
		}
		return Invocation{Kind: KindPin, Canonical: "/pin"}, nil
	case "read":
		switch strings.ToLower(strings.TrimSpace(rawArgs)) {
		case "":
			return Invocation{Kind: KindRead, Canonical: "/read"}, nil
		case "all":
			return Invocation{Kind: KindRead, All: true, Canonical: "/read all"}, nil
		default:
			return Invocation{}, fmt.Errorf("usage: /read [all]")
		}
	case "unread":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /unread")
		}
		return Invocation{Kind: KindUnread, Canonical: "/unread"}, nil
	case "commit":
		return Invocation{
			Kind:      KindCommit,
			Message:   strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("commit", rawArgs),
		}, nil
	case "push":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /push")
		}
		return Invocation{Kind: KindPush, Canonical: "/push"}, nil
	case "pull":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /pull")
		}
		return Invocation{Kind: KindPull, Canonical: "/pull"}, nil
	case "resolve":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /resolve")
		}
		return Invocation{Kind: KindResolve, Canonical: "/resolve"}, nil
	case "integrity":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /integrity")
		}
		return Invocation{Kind: KindIntegrity, Canonical: "/integrity"}, nil
	case "codex":
		return Invocation{
			Kind:      KindCodex,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("codex", rawArgs),
		}, nil
	case "codex-new", "codex-start":
		return Invocation{
			Kind:      KindCodexNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("codex-new", rawArgs),
		}, nil
	case "claude":
		return Invocation{
			Kind:      KindClaude,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("claude", rawArgs),
		}, nil
	case "claude-new", "cc-start":
		return Invocation{
			Kind:      KindClaudeNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("claude-new", rawArgs),
		}, nil
	case "opencode":
		return Invocation{
			Kind:      KindOpenCode,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("opencode", rawArgs),
		}, nil
	case "opencode-new", "oc-start":
		return Invocation{
			Kind:      KindOpenCodeNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("opencode-new", rawArgs),
		}, nil
	case "lcagent":
		return Invocation{
			Kind:      KindLCAgent,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("lcagent", rawArgs),
		}, nil
	case "lcagent-new", "lca-start":
		return Invocation{
			Kind:      KindLCAgentNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("lcagent-new", rawArgs),
		}, nil
	case "snooze":
		switch strings.ToLower(strings.TrimSpace(rawArgs)) {
		case "", "now":
			duration, err := parseDurationArg(rawArgs)
			if err != nil {
				return Invocation{}, err
			}
			return Invocation{Kind: KindSnooze, Duration: duration, Canonical: "/snooze " + formatDurationArg(duration)}, nil
		case "off", "clear", "unsnooze":
			return Invocation{Kind: KindClearSnooze, Canonical: "/snooze off"}, nil
		default:
			duration, err := parseDurationArg(rawArgs)
			if err != nil {
				return Invocation{}, err
			}
			return Invocation{Kind: KindSnooze, Duration: duration, Canonical: "/snooze " + formatDurationArg(duration)}, nil
		}
	case "clear-snooze", "unsnooze":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /clear-snooze")
		}
		return Invocation{Kind: KindClearSnooze, Canonical: "/clear-snooze"}, nil
	case "session":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /session")
		}
		return Invocation{Kind: KindSession, Canonical: "/session"}, nil
	case "sessions":
		mode, err := parseToggleMode(rawArgs, "/sessions")
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindSessions, Toggle: mode, Canonical: "/sessions " + string(mode)}, nil
	case "events":
		mode, err := parseToggleMode(rawArgs, "/events")
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindEvents, Toggle: mode, Canonical: "/events " + string(mode)}, nil
	case "ignore":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /ignore")
		}
		return Invocation{Kind: KindIgnore, Canonical: "/ignore"}, nil
	case "ignored":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /ignored")
		}
		return Invocation{Kind: KindIgnored, Canonical: "/ignored"}, nil
	case "archive":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /archive")
		}
		return Invocation{Kind: KindArchive, Canonical: "/archive"}, nil
	case "unarchive", "restore":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /unarchive")
		}
		return Invocation{Kind: KindUnarchive, Canonical: "/unarchive"}, nil
	case "remove", "delete", "forget":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /remove")
		}
		return Invocation{Kind: KindRemove, Canonical: "/remove"}, nil
	case "focus":
		target, err := parseFocusTarget(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindFocus, Focus: target, Canonical: "/focus " + string(target)}, nil
	case "privacy":
		if strings.EqualFold(strings.TrimSpace(rawArgs), "settings") {
			return Invocation{Kind: KindPrivacySettings, Canonical: "/privacy settings"}, nil
		}
		mode, err := parseToggleMode(rawArgs, "/privacy")
		if err != nil {
			return Invocation{}, fmt.Errorf("usage: /privacy on|off|toggle|settings")
		}
		return Invocation{Kind: KindPrivacy, Toggle: mode, Canonical: "/privacy " + string(mode)}, nil
	case "quit":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /quit")
		}
		return Invocation{Kind: KindQuit, Canonical: "/quit"}, nil
	default:
		return Invocation{}, fmt.Errorf("unknown command: /%s", name)
	}
}

func choice(value, summary string) slashcmd.Choice {
	return slashcmd.NewChoice(value, summary)
}

func categoryNameSuggestions(prefix, argPrefix string, names []string, summary string) []Suggestion {
	choices := make([]slashcmd.Choice, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		choices = append(choices, choice(name, summary))
	}
	return slashcmd.EnumSuggestions(prefix, strings.TrimSpace(argPrefix), choices...)
}

func parseSortMode(raw string) (SortMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "attention":
		return SortAttention, nil
	case "recent":
		return SortRecent, nil
	default:
		return "", fmt.Errorf("usage: /sort attention|recent")
	}
}

func parseNonAIFoldersMode(raw string) (ToggleMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on":
		return ToggleOn, nil
	case "off":
		return ToggleOff, nil
	default:
		return "", fmt.Errorf("usage: /non-ai-folders on|off")
	}
}

func parseProjectTab(raw string) (ProjectTab, error) {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "toggle", "next", "cycle":
		return ProjectTabToggle, nil
	case "main", "general", "active":
		return ProjectTabMain, nil
	case "archived", "archive":
		return ProjectTabArchived, nil
	default:
		return ProjectTabCategory, nil
	}
}

func parseCategoryCommand(raw string) (Invocation, error) {
	if strings.TrimSpace(raw) == "" {
		return Invocation{Kind: KindCategory, Canonical: "/category"}, nil
	}
	action, rest := slashcmd.SplitCommandBody(strings.TrimSpace(raw))
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "create", "new", "add":
		name := strings.TrimSpace(rest)
		if name == "" {
			return Invocation{}, fmt.Errorf("usage: /category create <name>")
		}
		return Invocation{Kind: KindCategory, CategoryAction: CategoryActionCreate, CategoryName: name, Canonical: slashcmd.CanonicalCommand("category", "create "+name)}, nil
	case "remove", "delete", "rm":
		name := strings.TrimSpace(rest)
		if name == "" {
			return Invocation{}, fmt.Errorf("usage: /category remove <name>")
		}
		return Invocation{Kind: KindCategory, CategoryAction: CategoryActionRemove, CategoryName: name, Canonical: slashcmd.CanonicalCommand("category", "remove "+name)}, nil
	case "move", "set":
		name := strings.TrimSpace(rest)
		if name == "" {
			return Invocation{}, fmt.Errorf("usage: /category move <name>")
		}
		return Invocation{Kind: KindCategory, CategoryAction: CategoryActionMove, CategoryName: name, Canonical: slashcmd.CanonicalCommand("category", "move "+name)}, nil
	case "clear", "main":
		if strings.TrimSpace(rest) != "" {
			return Invocation{}, fmt.Errorf("usage: /category clear")
		}
		return Invocation{Kind: KindCategory, CategoryAction: CategoryActionClear, Canonical: "/category clear"}, nil
	default:
		return Invocation{}, fmt.Errorf("usage: /category create|remove|move|clear [name]")
	}
}

func parseToggleMode(raw, usage string) (ToggleMode, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ToggleToggle, nil
	}
	switch trimmed {
	case "toggle":
		return ToggleToggle, nil
	case "on":
		return ToggleOn, nil
	case "off":
		return ToggleOff, nil
	default:
		return "", fmt.Errorf("usage: %s on|off|toggle", usage)
	}
}

func parseFocusTarget(raw string) (FocusTarget, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "list", "projects":
		return FocusList, nil
	case "detail", "details":
		return FocusDetail, nil
	case "runtime":
		return FocusRuntime, nil
	default:
		return "", fmt.Errorf("usage: /focus list|detail|runtime")
	}
}

func parseAssistantArg(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", nil
	}
	for _, flag := range []string{"--assistant", "--provider"} {
		if value, rest, ok := splitFlagValue(trimmed, flag); ok {
			assistant, err := normalizeAssistantArg(value)
			if err != nil {
				return "", "", err
			}
			return assistant, strings.TrimSpace(rest), nil
		}
	}
	return "", trimmed, nil
}

func splitFlagValue(raw, flag string) (string, string, bool) {
	if raw == flag {
		return "", "", true
	}
	if strings.HasPrefix(raw, flag+"=") {
		after := strings.TrimSpace(raw[len(flag)+1:])
		fields := strings.Fields(after)
		if len(fields) == 0 {
			return "", "", true
		}
		return fields[0], strings.TrimSpace(after[len(fields[0]):]), true
	}
	if strings.HasPrefix(raw, flag+" ") {
		after := strings.TrimSpace(raw[len(flag):])
		fields := strings.Fields(after)
		if len(fields) == 0 {
			return "", "", true
		}
		return fields[0], strings.TrimSpace(after[len(fields[0]):]), true
	}
	return "", "", false
}

func normalizeAssistantArg(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "codex":
		return "codex", nil
	case "opencode", "open-code", "open_code":
		return "opencode", nil
	case "claude", "claude-code", "claude_code":
		return "claude_code", nil
	case "lcagent", "lc-agent", "lc_agent":
		return "lcagent", nil
	default:
		return "", fmt.Errorf("assistant must be one of: codex, opencode, claude, lcagent")
	}
}

func parseWorktreeCommand(raw string) (Invocation, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "update":
		return Invocation{Kind: KindWorktreeUpdate, Canonical: "/wt update"}, nil
	case "merge":
		return Invocation{Kind: KindWorktreeMerge, Canonical: "/wt merge"}, nil
	case "remove":
		return Invocation{Kind: KindWorktreeRemove, Canonical: "/wt remove"}, nil
	case "prune":
		return Invocation{Kind: KindWorktreePrune, Canonical: "/wt prune"}, nil
	default:
		return Invocation{}, fmt.Errorf("usage: /wt update|merge|remove|prune")
	}
}

func parseDurationArg(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Hour, nil
	}
	if strings.HasSuffix(trimmed, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(trimmed, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("usage: /snooze [duration|off]")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("usage: /snooze [duration|off]")
	}
	return d, nil
}

func formatDurationArg(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return d.String()
}
