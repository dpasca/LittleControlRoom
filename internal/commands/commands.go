package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	KindHelp           Kind = "help"
	KindAIStats        Kind = "ai-stats"
	KindPerf           Kind = "perf"
	KindErrors         Kind = "errors"
	KindBoss           Kind = "boss"
	KindRefresh        Kind = "refresh"
	KindSort           Kind = "sort"
	KindView           Kind = "view"
	KindSetup          Kind = "setup"
	KindSettings       Kind = "settings"
	KindFilter         Kind = "filter"
	KindNewProject     Kind = "new-project"
	KindNewTask        Kind = "new-task"
	KindTaskActions    Kind = "task-actions"
	KindOpen           Kind = "open"
	KindRun            Kind = "run"
	KindRestart        Kind = "restart"
	KindRunEdit        Kind = "run-edit"
	KindRuntime        Kind = "runtime"
	KindStop           Kind = "stop"
	KindDiff           Kind = "diff"
	KindCommit         Kind = "commit"
	KindPush           Kind = "push"
	KindCodex          Kind = "codex"
	KindCodexNew       Kind = "codex-new"
	KindClaude         Kind = "claude"
	KindClaudeNew      Kind = "claude-new"
	KindOpenCode       Kind = "opencode"
	KindOpenCodeNew    Kind = "opencode-new"
	KindTodo           Kind = "todo"
	KindWorktreeLanes  Kind = "worktree-lanes"
	KindWorktreeMerge  Kind = "worktree-merge"
	KindWorktreeRemove Kind = "worktree-remove"
	KindWorktreePrune  Kind = "worktree-prune"
	KindPin            Kind = "pin"
	KindRead           Kind = "read"
	KindUnread         Kind = "unread"
	KindSnooze         Kind = "snooze"
	KindClearSnooze    Kind = "clear-snooze"
	KindSessions       Kind = "sessions"
	KindEvents         Kind = "events"
	KindIgnore         Kind = "ignore"
	KindIgnored        Kind = "ignored"
	KindRemove         Kind = "remove"
	KindFocus          Kind = "focus"
	KindPrivacy        Kind = "privacy"
	KindQuit           Kind = "quit"
)

type SortMode string

const (
	SortAttention SortMode = "attention"
	SortRecent    SortMode = "recent"
)

type ViewMode string

const (
	ViewAI  ViewMode = "ai"
	ViewAll ViewMode = "all"
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

type Spec struct {
	Name    string
	Usage   string
	Summary string
}

type Suggestion struct {
	Insert  string
	Display string
	Summary string
}

type Invocation struct {
	Kind      Kind
	Sort      SortMode
	View      ViewMode
	Toggle    ToggleMode
	Focus     FocusTarget
	Duration  time.Duration
	Message   string
	Prompt    string
	Command   string
	Filter    string
	All       bool
	Clear     bool
	Canonical string
}

var specs = []Spec{
	{Name: "help", Usage: "/help", Summary: "Open the help panel"},
	{Name: "ai", Usage: "/ai", Summary: "Open the internal AI stats dialog"},
	{Name: "perf", Usage: "/perf", Summary: "Open the internal responsiveness and wait tracker"},
	{Name: "errors", Usage: "/errors", Summary: "Open the recent error log"},
	{Name: "boss", Usage: "/boss [on|off|toggle]", Summary: "Open or close the chat-first boss layer"},
	{Name: "refresh", Usage: "/refresh", Summary: "Rescan projects and retry failed assessments"},
	{Name: "sort", Usage: "/sort attention|recent", Summary: "Set list ordering"},
	{Name: "view", Usage: "/view ai|all", Summary: "Choose AI-linked or all folders"},
	{Name: "settings", Usage: "/settings", Summary: "Edit the saved OpenAI key, scope, filters, and scan thresholds"},
	{Name: "setup", Usage: "/setup", Summary: "Choose AI roles for project reports and boss chat"},
	{Name: "filter", Usage: "/filter [text|clear]", Summary: "Temporarily show only matching project names"},
	{Name: "new-project", Usage: "/new-project", Summary: "Create a project folder, or paste an existing path to add it"},
	{Name: "new-task", Usage: "/new-task", Summary: "Create a scratch task folder under the default task root"},
	{Name: "task-actions", Usage: "/task-actions", Summary: "Open archive/delete actions for the selected scratch task"},
	{Name: "open", Usage: "/open", Summary: "Open the selected project's folder in the system browser"},
	{Name: "run", Usage: "/run [command]", Summary: "Start the selected project's managed runtime"},
	{Name: "start", Usage: "/start [command]", Summary: "Alias for /run"},
	{Name: "restart", Usage: "/restart", Summary: "Restart the selected project's managed runtime"},
	{Name: "run-edit", Usage: "/run-edit", Summary: "Edit the selected project's saved run command"},
	{Name: "runtime", Usage: "/runtime", Summary: "Focus the selected project's runtime pane"},
	{Name: "stop", Usage: "/stop", Summary: "Stop the selected project's managed runtime"},
	{Name: "diff", Usage: "/diff", Summary: "Open a full-screen diff for the selected project"},
	{Name: "commit", Usage: "/commit [message]", Summary: "Preview a commit; Alt+Enter also pushes when available"},
	{Name: "push", Usage: "/push", Summary: "Push the selected project when its branch is ahead"},
	{Name: "codex", Usage: "/codex [prompt]", Summary: "Resume the selected project's latest Codex session, or start a new one"},
	{Name: "codex-new", Usage: "/codex-new [prompt]", Summary: "Start a fresh Codex session in the selected project"},
	{Name: "claude", Usage: "/claude [prompt]", Summary: "Resume the selected project's latest Claude Code session, or start a new one"},
	{Name: "claude-new", Usage: "/claude-new [prompt]", Summary: "Start a fresh Claude Code session in the selected project"},
	{Name: "opencode", Usage: "/opencode [prompt]", Summary: "Resume the selected project's latest OpenCode session, or start a new one"},
	{Name: "opencode-new", Usage: "/opencode-new [prompt]", Summary: "Start a fresh OpenCode session in the selected project"},
	{Name: "todo", Usage: "/todo", Summary: "Open the selected project's TODO list"},
	{Name: "wt", Usage: "/wt lanes|merge|remove|prune", Summary: "Toggle worktree lanes or manage the selected worktree"},
	{Name: "pin", Usage: "/pin", Summary: "Toggle pin on the selected project"},
	{Name: "read", Usage: "/read [all]", Summary: "Mark the selected project, or all visible projects, as read"},
	{Name: "unread", Usage: "/unread", Summary: "Mark the selected project as unread"},
	{Name: "snooze", Usage: "/snooze [duration|off]", Summary: "Snooze the selected project or clear with /snooze off"},
	{Name: "clear-snooze", Usage: "/clear-snooze", Summary: "Clear snooze on the selected project"},
	{Name: "unsnooze", Usage: "/unsnooze", Summary: "Clear snooze on the selected project"},
	{Name: "sessions", Usage: "/sessions on|off|toggle", Summary: "Show or hide the Sessions section"},
	{Name: "events", Usage: "/events on|off|toggle", Summary: "Show or hide Recent events"},
	{Name: "ignore", Usage: "/ignore", Summary: "Hide the selected project's exact name"},
	{Name: "ignored", Usage: "/ignored", Summary: "Review ignored project names and restore them"},
	{Name: "remove", Usage: "/remove", Summary: "Make the selected item go away safely"},
	{Name: "focus", Usage: "/focus list|detail|runtime", Summary: "Move focus between panes"},
	{Name: "privacy", Usage: "/privacy on|off|toggle", Summary: "Toggle demo privacy mode that hides project name patterns"},
	{Name: "quit", Usage: "/quit", Summary: "Quit the TUI"},
}

func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

func Suggestions(input string) []Suggestion {
	trimmed := strings.TrimLeft(input, " \t\r\n")
	if trimmed == "" {
		trimmed = "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return commandNameSuggestions("")
	}

	hasTrailingSpace := strings.HasSuffix(trimmed, " ")
	fields := strings.Fields(body)
	namePrefix := strings.ToLower(fields[0])
	if len(fields) == 1 && !hasTrailingSpace {
		return commandNameSuggestions(namePrefix)
	}

	switch namePrefix {
	case "sort":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/sort ", argPrefix,
			choice("attention", "Sort by attention score"),
			choice("recent", "Sort by recent activity"),
		)
	case "view":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/view ", argPrefix,
			choice("ai", "Show AI-linked folders only"),
			choice("all", "Show all discovered folders"),
		)
	case "filter":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/filter ", argPrefix,
			choice("clear", "Remove the active project-name filter"),
		)
	case "sessions":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/sessions ", argPrefix,
			choice("toggle", "Flip the Sessions section"),
			choice("on", "Show the Sessions section"),
			choice("off", "Hide the Sessions section"),
		)
	case "events":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/events ", argPrefix,
			choice("toggle", "Flip the Recent events section"),
			choice("on", "Show the Recent events section"),
			choice("off", "Hide the Recent events section"),
		)
	case "focus":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/focus ", argPrefix,
			choice("list", "Focus the project list"),
			choice("detail", "Focus the detail pane"),
			choice("runtime", "Focus the runtime pane"),
		)
	case "snooze":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/snooze ", argPrefix,
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
		return enumSuggestions("/read ", argPrefix,
			choice("all", "Mark all visible projects as read"),
		)
	case "wt", "worktree":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/wt ", argPrefix,
			choice("lanes", "Expand or collapse the current worktree family"),
			choice("merge", "Merge the selected linked worktree back into its parent branch"),
			choice("remove", "Remove the selected linked worktree"),
			choice("prune", "Prune stale git worktree registrations for the current repo"),
		)
	case "privacy":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/privacy ", argPrefix,
			choice("toggle", "Flip demo privacy mode"),
			choice("on", "Enable demo privacy mode"),
			choice("off", "Disable demo privacy mode"),
		)
	case "boss":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/boss ", argPrefix,
			choice("on", "Open the boss chat layer"),
			choice("off", "Close the boss chat layer"),
			choice("toggle", "Toggle the boss chat layer"),
		)
	default:
		return commandNameSuggestions(namePrefix)
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

	name, rawArgs := splitCommandBody(body)
	switch strings.ToLower(name) {
	case "help":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /help")
		}
		return Invocation{Kind: KindHelp, Canonical: "/help"}, nil
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
	case "boss":
		mode, err := parseToggleMode(rawArgs, "/boss")
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindBoss, Toggle: mode, Canonical: "/boss " + string(mode)}, nil
	case "refresh":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /refresh")
		}
		return Invocation{Kind: KindRefresh, Canonical: "/refresh"}, nil
	case "sort":
		mode, err := parseSortMode(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindSort, Sort: mode, Canonical: "/sort " + string(mode)}, nil
	case "view":
		mode, err := parseViewMode(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindView, View: mode, Canonical: "/view " + string(mode)}, nil
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
				Canonical: canonicalCommand("filter", rawArgs),
			}, nil
		}
	case "new-project":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /new-project")
		}
		return Invocation{Kind: KindNewProject, Canonical: "/new-project"}, nil
	case "new-task":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /new-task")
		}
		return Invocation{Kind: KindNewTask, Canonical: "/new-task"}, nil
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
	case "run", "start":
		return Invocation{
			Kind:      KindRun,
			Command:   strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("run", rawArgs),
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
			Canonical: canonicalCommand("commit", rawArgs),
		}, nil
	case "push":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /push")
		}
		return Invocation{Kind: KindPush, Canonical: "/push"}, nil
	case "codex":
		return Invocation{
			Kind:      KindCodex,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("codex", rawArgs),
		}, nil
	case "codex-new", "codex-start":
		return Invocation{
			Kind:      KindCodexNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("codex-new", rawArgs),
		}, nil
	case "claude":
		return Invocation{
			Kind:      KindClaude,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("claude", rawArgs),
		}, nil
	case "claude-new", "cc-start":
		return Invocation{
			Kind:      KindClaudeNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("claude-new", rawArgs),
		}, nil
	case "opencode":
		return Invocation{
			Kind:      KindOpenCode,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("opencode", rawArgs),
		}, nil
	case "opencode-new", "oc-start":
		return Invocation{
			Kind:      KindOpenCodeNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("opencode-new", rawArgs),
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
		mode, err := parseToggleMode(rawArgs, "/privacy")
		if err != nil {
			return Invocation{}, err
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

func splitCommandBody(body string) (string, string) {
	for i, r := range body {
		if r == ' ' || r == '\t' {
			return body[:i], strings.TrimSpace(body[i+1:])
		}
	}
	return body, ""
}

func commandNameSuggestions(prefix string) []Suggestion {
	out := make([]Suggestion, 0, len(specs))
	for _, spec := range specs {
		if prefix != "" && !strings.HasPrefix(spec.Name, prefix) {
			continue
		}
		out = append(out, Suggestion{
			Insert:  "/" + spec.Name,
			Display: spec.Usage,
			Summary: spec.Summary,
		})
	}
	return out
}

type enumChoice struct {
	Value   string
	Summary string
}

func choice(value, summary string) enumChoice {
	return enumChoice{Value: value, Summary: summary}
}

func enumSuggestions(prefix, argPrefix string, choices ...enumChoice) []Suggestion {
	out := make([]Suggestion, 0, len(choices))
	for _, ch := range choices {
		if argPrefix != "" && !strings.HasPrefix(ch.Value, argPrefix) {
			continue
		}
		insert := prefix + ch.Value
		out = append(out, Suggestion{
			Insert:  insert,
			Display: insert,
			Summary: ch.Summary,
		})
	}
	return out
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

func parseViewMode(raw string) (ViewMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ai":
		return ViewAI, nil
	case "all":
		return ViewAll, nil
	default:
		return "", fmt.Errorf("usage: /view ai|all")
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

func parseWorktreeCommand(raw string) (Invocation, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "lanes":
		return Invocation{Kind: KindWorktreeLanes, Canonical: "/wt lanes"}, nil
	case "merge":
		return Invocation{Kind: KindWorktreeMerge, Canonical: "/wt merge"}, nil
	case "remove":
		return Invocation{Kind: KindWorktreeRemove, Canonical: "/wt remove"}, nil
	case "prune":
		return Invocation{Kind: KindWorktreePrune, Canonical: "/wt prune"}, nil
	default:
		return Invocation{}, fmt.Errorf("usage: /wt lanes|merge|remove|prune")
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

func canonicalCommand(name, rawArgs string) string {
	args := strings.TrimSpace(rawArgs)
	if args == "" {
		return "/" + name
	}
	return "/" + name + " " + args
}
