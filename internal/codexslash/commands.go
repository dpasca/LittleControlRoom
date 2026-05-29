package codexslash

import (
	"fmt"
	"strconv"
	"strings"

	"lcroom/internal/slashcmd"
)

type Kind string

const (
	KindNew         Kind = "new"
	KindResume      Kind = "resume"
	KindStatus      Kind = "status"
	KindShowStatus  Kind = "show-status"
	KindModel       Kind = "model"
	KindReconnect   Kind = "reconnect"
	KindCompact     Kind = "compact"
	KindReview      Kind = "review"
	KindDevLCReview Kind = "dev-lcreview"
	KindPermissions Kind = "permissions"
	KindBoss        Kind = "boss"
	KindSkills      Kind = "skills"
	KindGoal        Kind = "goal"
	KindSettings    Kind = "settings"
)

type Spec = slashcmd.Spec

type Suggestion = slashcmd.Suggestion

type GoalAction string

const (
	GoalActionShow   GoalAction = "show"
	GoalActionSet    GoalAction = "set"
	GoalActionPause  GoalAction = "pause"
	GoalActionResume GoalAction = "resume"
	GoalActionClear  GoalAction = "clear"
)

type Invocation struct {
	Kind            Kind
	Prompt          string
	SessionID       string
	PermissionLevel string
	GoalAction      GoalAction
	GoalObjective   string
	GoalTokenBudget *int64
	Canonical       string
}

var specs = []Spec{
	{Name: "new", Usage: "/new [prompt]", Summary: "Start a fresh embedded session for this project"},
	{Name: "resume", Usage: "/resume [session-id]", Summary: "Resume a different embedded session for this project, or pick one when no session ID is given"},
	{Name: "sessions", Usage: "/sessions [session-id]", Summary: "Pick from this project's saved embedded sessions"},
	{Name: "session", Usage: "/session [session-id]", Summary: "Alias for /resume", Hidden: true},
	{Name: "model", Usage: "/model", Summary: "Pick the embedded model and reasoning effort for this and future embedded sessions of the same tool, even after restarting LCR"},
	{Name: "status", Usage: "/status", Summary: "Show embedded session config, limits, and token usage"},
	{Name: "show-status", Usage: "/show-status", Summary: "Show embedded session config, limits, and token usage", Hidden: true},
	{Name: "dev-show-status", Usage: "/dev-show-status", Summary: "Show embedded session config, limits, and token usage", Hidden: true},
	{Name: "reconnect", Usage: "/reconnect", Summary: "Restart the embedded provider helper and reconnect to the current session"},
	{Name: "compact", Usage: "/compact", Summary: "Compact conversation history to free up context"},
	{Name: "review", Usage: "/review", Summary: "Ask embedded Codex to review uncommitted changes"},
	{Name: "dev-lcreview", Usage: "/dev-lcreview", Summary: "Add an LCAgent trace-quality review TODO to the Little Control Room project", Hidden: true},
	{Name: "permissions", Usage: "/permissions [off|low|medium]", Summary: "Show or change LCAgent permission level for this session"},
	{Name: "boss", Usage: "/boss", Summary: "Open the high-level boss chat layer"},
	{Name: "skills", Usage: "/skills", Summary: "Open the local Codex skills inventory"},
	{Name: "goal", Usage: "/goal [status|pause|resume|clear|stop|objective] [--budget N]", Summary: "Show, set, pause, resume, or clear the embedded Codex goal"},
	{Name: "settings", Usage: "/settings", Summary: "Open app settings for this embedded provider"},
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
		return slashcmd.NameSuggestions(specs, "")
	}

	fields := strings.Fields(body)
	if len(fields) <= 1 && !strings.HasSuffix(trimmed, " ") {
		return slashcmd.NameSuggestions(specs, strings.ToLower(fields[0]))
	}

	switch strings.ToLower(fields[0]) {
	case "new":
		return []Suggestion{{
			Insert:  "/new",
			Display: "/new [prompt]",
			Summary: "Start a fresh embedded session; optional inline prompt opens the new session with that prompt",
		}}
	case "resume":
		return []Suggestion{resumeSuggestion("/resume")}
	case "sessions":
		return []Suggestion{resumeSuggestion("/sessions")}
	case "session":
		return []Suggestion{resumeSuggestion("/session")}
	case "model":
		return []Suggestion{{
			Insert:  "/model",
			Display: "/model",
			Summary: "Open a local picker for the embedded model and reasoning effort used by this and future embedded sessions of the same tool, even after restarting LCR",
		}}
	case "status":
		return []Suggestion{{
			Insert:  "/status",
			Display: "/status",
			Summary: "Show embedded session config, limits, and token usage",
		}}
	case "show-status", "dev-show-status":
		return []Suggestion{{
			Insert:  "/show-status",
			Display: "/show-status",
			Summary: "Show embedded session config, limits, and token usage",
		}}
	case "reconnect":
		return []Suggestion{{
			Insert:  "/reconnect",
			Display: "/reconnect",
			Summary: "Restart the embedded provider helper and reconnect to the current session",
		}}
	case "compact":
		return []Suggestion{{
			Insert:  "/compact",
			Display: "/compact",
			Summary: "Compact conversation history to free up context",
		}}
	case "review":
		return []Suggestion{{
			Insert:  "/review",
			Display: "/review",
			Summary: "Ask embedded Codex to review uncommitted changes",
		}}
	case "dev-lcreview":
		return []Suggestion{{
			Insert:  "/dev-lcreview",
			Display: "/dev-lcreview",
			Summary: "Add an LCAgent trace-quality review TODO to the Little Control Room project",
		}}
	case "permissions", "permission", "perms":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return permissionSuggestions(argPrefix)
	case "boss":
		return []Suggestion{{
			Insert:  "/boss",
			Display: "/boss",
			Summary: "Open the high-level boss chat layer",
		}}
	case "skills":
		return []Suggestion{{
			Insert:  "/skills",
			Display: "/skills",
			Summary: "Open the local Codex skills inventory",
		}}
	case "settings":
		return []Suggestion{{
			Insert:  "/settings",
			Display: "/settings",
			Summary: "Open app settings for this embedded provider",
		}}
	case "goal":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return goalSuggestions(argPrefix)
	default:
		return slashcmd.NameSuggestions(specs, strings.ToLower(fields[0]))
	}
}

func permissionSuggestions(argPrefix string) []Suggestion {
	choices := []struct {
		value   string
		insert  string
		display string
		summary string
	}{
		{value: "", insert: "/permissions", display: "/permissions", summary: "Show what Off, Low, and Medium allow in LCAgent"},
		{value: "off", insert: "/permissions off", display: "/permissions off", summary: "Deny file edits and allow only read-only commands"},
		{value: "low", insert: "/permissions low", display: "/permissions low", summary: "Allow project-local edits plus read-only and approved verification commands"},
		{value: "medium", insert: "/permissions medium", display: "/permissions medium", summary: "Allow workspace-contained commands without repeated approvals"},
	}
	out := make([]Suggestion, 0, len(choices))
	for _, choice := range choices {
		if argPrefix != "" && (choice.value == "" || !strings.HasPrefix(choice.value, argPrefix)) {
			continue
		}
		out = append(out, Suggestion{
			Insert:  choice.insert,
			Display: choice.display,
			Summary: choice.summary,
		})
	}
	return out
}

func goalSuggestions(argPrefix string) []Suggestion {
	choices := []struct {
		value   string
		insert  string
		display string
		summary string
	}{
		{
			value:   "",
			insert:  "/goal",
			display: "/goal",
			summary: "Show the current embedded Codex goal",
		},
		{
			value:   "status",
			insert:  "/goal status",
			display: "/goal status",
			summary: "Show the current embedded Codex goal",
		},
		{
			value:   "pause",
			insert:  "/goal pause",
			display: "/goal pause",
			summary: "Pause the current embedded Codex goal",
		},
		{
			value:   "resume",
			insert:  "/goal resume",
			display: "/goal resume",
			summary: "Resume the current paused embedded Codex goal",
		},
		{
			value:   "clear",
			insert:  "/goal clear",
			display: "/goal clear",
			summary: "Clear the current embedded Codex goal",
		},
		{
			value:   "stop",
			insert:  "/goal stop",
			display: "/goal stop",
			summary: "Interrupt the active goal turn and clear the embedded Codex goal",
		},
		{
			value:   "--budget",
			insert:  "/goal --budget ",
			display: "/goal --budget N objective",
			summary: "Set a goal with a token budget",
		},
	}
	out := make([]Suggestion, 0, len(choices))
	for _, choice := range choices {
		if argPrefix != "" && (choice.value == "" || !strings.HasPrefix(choice.value, argPrefix)) {
			continue
		}
		out = append(out, Suggestion{
			Insert:  choice.insert,
			Display: choice.display,
			Summary: choice.summary,
		})
	}
	return out
}

func Parse(input string) (Invocation, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return Invocation{}, fmt.Errorf("slash command required")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return Invocation{}, fmt.Errorf("slash command must start with /")
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return Invocation{}, fmt.Errorf("slash command required")
	}

	name, rawArgs := slashcmd.SplitCommandBody(body)
	switch strings.ToLower(name) {
	case "new":
		return Invocation{
			Kind:      KindNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: slashcmd.CanonicalCommand("new", rawArgs),
		}, nil
	case "resume", "sessions", "session":
		sessionID := strings.TrimSpace(rawArgs)
		if len(strings.Fields(sessionID)) > 1 {
			return Invocation{}, fmt.Errorf("usage: /%s [session-id]", strings.ToLower(name))
		}
		return Invocation{
			Kind:      KindResume,
			SessionID: sessionID,
			Canonical: slashcmd.CanonicalCommand("resume", rawArgs),
		}, nil
	case "model":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /model")
		}
		return Invocation{
			Kind:      KindModel,
			Canonical: "/model",
		}, nil
	case "status":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /status")
		}
		return Invocation{
			Kind:      KindStatus,
			Canonical: "/status",
		}, nil
	case "show-status", "dev-show-status":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /show-status")
		}
		return Invocation{
			Kind:      KindShowStatus,
			Canonical: "/show-status",
		}, nil
	case "reconnect":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /reconnect")
		}
		return Invocation{
			Kind:      KindReconnect,
			Canonical: "/reconnect",
		}, nil
	case "compact":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /compact")
		}
		return Invocation{
			Kind:      KindCompact,
			Canonical: "/compact",
		}, nil
	case "review":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /review")
		}
		return Invocation{
			Kind:      KindReview,
			Canonical: "/review",
		}, nil
	case "dev-lcreview":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /dev-lcreview")
		}
		return Invocation{
			Kind:      KindDevLCReview,
			Canonical: "/dev-lcreview",
		}, nil
	case "permissions", "permission", "perms":
		level := strings.ToLower(strings.TrimSpace(rawArgs))
		if len(strings.Fields(level)) > 1 {
			return Invocation{}, fmt.Errorf("usage: /permissions [off|low|medium]")
		}
		switch level {
		case "", "off", "low", "medium":
		default:
			return Invocation{}, fmt.Errorf("usage: /permissions [off|low|medium]")
		}
		canonical := "/permissions"
		if level != "" {
			canonical += " " + level
		}
		return Invocation{
			Kind:            KindPermissions,
			PermissionLevel: level,
			Canonical:       canonical,
		}, nil
	case "boss":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /boss")
		}
		return Invocation{
			Kind:      KindBoss,
			Canonical: "/boss",
		}, nil
	case "skills":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /skills")
		}
		return Invocation{
			Kind:      KindSkills,
			Canonical: "/skills",
		}, nil
	case "settings":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /settings")
		}
		return Invocation{
			Kind:      KindSettings,
			Canonical: "/settings",
		}, nil
	case "goal":
		return parseGoalInvocation(rawArgs)
	default:
		return Invocation{}, fmt.Errorf("unsupported embedded slash command: /%s", name)
	}
}

func parseGoalInvocation(rawArgs string) (Invocation, error) {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" || strings.EqualFold(trimmed, "status") || strings.EqualFold(trimmed, "show") {
		return Invocation{
			Kind:       KindGoal,
			GoalAction: GoalActionShow,
			Canonical:  "/goal",
		}, nil
	}
	if strings.EqualFold(trimmed, "clear") || strings.EqualFold(trimmed, "reset") || strings.EqualFold(trimmed, "stop") || strings.EqualFold(trimmed, "cancel") {
		return Invocation{
			Kind:       KindGoal,
			GoalAction: GoalActionClear,
			Canonical:  "/goal clear",
		}, nil
	}
	if strings.EqualFold(trimmed, "pause") || strings.EqualFold(trimmed, "suspend") {
		return Invocation{
			Kind:       KindGoal,
			GoalAction: GoalActionPause,
			Canonical:  "/goal pause",
		}, nil
	}
	if strings.EqualFold(trimmed, "resume") || strings.EqualFold(trimmed, "continue") {
		return Invocation{
			Kind:       KindGoal,
			GoalAction: GoalActionResume,
			Canonical:  "/goal resume",
		}, nil
	}

	objective, tokenBudget, err := parseGoalSetArgs(trimmed)
	if err != nil {
		return Invocation{}, err
	}
	canonical := "/goal " + objective
	if tokenBudget != nil {
		canonical += fmt.Sprintf(" --budget %d", *tokenBudget)
	}
	return Invocation{
		Kind:            KindGoal,
		GoalAction:      GoalActionSet,
		GoalObjective:   objective,
		GoalTokenBudget: tokenBudget,
		Canonical:       canonical,
	}, nil
}

func parseGoalSetArgs(rawArgs string) (string, *int64, error) {
	fields := strings.Fields(rawArgs)
	objectiveFields := make([]string, 0, len(fields))
	var tokenBudget *int64
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field == "--budget" || field == "--tokens":
			if i+1 >= len(fields) {
				return "", nil, fmt.Errorf("usage: /goal [status|clear|stop|objective] [--budget N]")
			}
			i++
			parsed, err := parseGoalTokenBudget(fields[i])
			if err != nil {
				return "", nil, err
			}
			tokenBudget = &parsed
		case strings.HasPrefix(field, "--budget="):
			parsed, err := parseGoalTokenBudget(strings.TrimPrefix(field, "--budget="))
			if err != nil {
				return "", nil, err
			}
			tokenBudget = &parsed
		case strings.HasPrefix(field, "--tokens="):
			parsed, err := parseGoalTokenBudget(strings.TrimPrefix(field, "--tokens="))
			if err != nil {
				return "", nil, err
			}
			tokenBudget = &parsed
		default:
			objectiveFields = append(objectiveFields, field)
		}
	}
	objective := strings.TrimSpace(strings.Join(objectiveFields, " "))
	if objective == "" {
		return "", nil, fmt.Errorf("goal objective required")
	}
	return objective, tokenBudget, nil
}

func parseGoalTokenBudget(raw string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("goal token budget must be a positive integer")
	}
	return parsed, nil
}

func resumeSuggestion(insert string) Suggestion {
	return Suggestion{
		Insert:  insert,
		Display: insert + " [session-id]",
		Summary: "Resume another embedded session for this project, or open a picker when no session ID is given",
	}
}
