package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	KindHelp        Kind = "help"
	KindRefresh     Kind = "refresh"
	KindSort        Kind = "sort"
	KindView        Kind = "view"
	KindSettings    Kind = "settings"
	KindCommit      Kind = "commit"
	KindPush        Kind = "push"
	KindFinish      Kind = "finish"
	KindCodex       Kind = "codex"
	KindCodexNew    Kind = "codex-new"
	KindPin         Kind = "pin"
	KindSnooze      Kind = "snooze"
	KindClearSnooze Kind = "clear-snooze"
	KindSessions    Kind = "sessions"
	KindEvents      Kind = "events"
	KindForget      Kind = "forget"
	KindFocus       Kind = "focus"
	KindQuit        Kind = "quit"
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
	FocusList   FocusTarget = "list"
	FocusDetail FocusTarget = "detail"
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
	Canonical string
}

var specs = []Spec{
	{Name: "help", Usage: "/help", Summary: "Open the help panel"},
	{Name: "refresh", Usage: "/refresh", Summary: "Rescan projects and retry failed assessments"},
	{Name: "sort", Usage: "/sort attention|recent", Summary: "Set list ordering"},
	{Name: "view", Usage: "/view ai|all", Summary: "Choose AI-linked or all folders"},
	{Name: "settings", Usage: "/settings", Summary: "Edit persisted scope, demo filters, and scan thresholds"},
	{Name: "commit", Usage: "/commit [message]", Summary: "Preview a commit for the selected project"},
	{Name: "push", Usage: "/push", Summary: "Push the selected project when its branch is ahead"},
	{Name: "finish", Usage: "/finish [message]", Summary: "Open the commit preview for the selected project"},
	{Name: "codex", Usage: "/codex [prompt]", Summary: "Resume the selected project's latest Codex session, or start a new one"},
	{Name: "codex-new", Usage: "/codex-new [prompt]", Summary: "Start a fresh Codex session in the selected project"},
	{Name: "pin", Usage: "/pin", Summary: "Toggle pin on the selected project"},
	{Name: "snooze", Usage: "/snooze [duration]", Summary: "Snooze the selected project"},
	{Name: "clear-snooze", Usage: "/clear-snooze", Summary: "Clear snooze on the selected project"},
	{Name: "sessions", Usage: "/sessions on|off|toggle", Summary: "Show or hide the Sessions section"},
	{Name: "events", Usage: "/events on|off|toggle", Summary: "Show or hide Recent events"},
	{Name: "forget", Usage: "/forget", Summary: "Forget a selected missing folder"},
	{Name: "focus", Usage: "/focus list|detail", Summary: "Move focus between panes"},
	{Name: "quit", Usage: "/quit", Summary: "Quit the TUI"},
}

func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

func Suggestions(input string) []Suggestion {
	trimmed := strings.TrimSpace(input)
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
		)
	case "snooze":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return enumSuggestions("/snooze ", argPrefix,
			choice("1h", "Snooze for 1 hour"),
			choice("4h", "Snooze for 4 hours"),
			choice("24h", "Snooze for 24 hours"),
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
	case "settings":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /settings")
		}
		return Invocation{Kind: KindSettings, Canonical: "/settings"}, nil
	case "pin":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /pin")
		}
		return Invocation{Kind: KindPin, Canonical: "/pin"}, nil
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
	case "finish":
		return Invocation{
			Kind:      KindFinish,
			Message:   strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("finish", rawArgs),
		}, nil
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
	case "snooze":
		duration, err := parseDurationArg(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindSnooze, Duration: duration, Canonical: "/snooze " + formatDurationArg(duration)}, nil
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
	case "forget":
		if rawArgs != "" {
			return Invocation{}, fmt.Errorf("usage: /forget")
		}
		return Invocation{Kind: KindForget, Canonical: "/forget"}, nil
	case "focus":
		target, err := parseFocusTarget(rawArgs)
		if err != nil {
			return Invocation{}, err
		}
		return Invocation{Kind: KindFocus, Focus: target, Canonical: "/focus " + string(target)}, nil
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
	default:
		return "", fmt.Errorf("usage: /focus list|detail")
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
			return 0, fmt.Errorf("usage: /snooze [duration]")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("usage: /snooze [duration]")
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
