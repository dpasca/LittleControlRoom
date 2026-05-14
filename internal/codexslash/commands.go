package codexslash

import (
	"fmt"
	"strconv"
	"strings"

	"lcroom/internal/slashcmd"
)

type Kind string

const (
	KindNew       Kind = "new"
	KindResume    Kind = "resume"
	KindStatus    Kind = "status"
	KindModel     Kind = "model"
	KindReconnect Kind = "reconnect"
	KindCompact   Kind = "compact"
	KindReview    Kind = "review"
	KindBoss      Kind = "boss"
	KindSkills    Kind = "skills"
	KindGoal      Kind = "goal"
	KindSettings  Kind = "settings"
)

type Spec = slashcmd.Spec

type Suggestion = slashcmd.Suggestion

type GoalAction string

const (
	GoalActionShow  GoalAction = "show"
	GoalActionSet   GoalAction = "set"
	GoalActionClear GoalAction = "clear"
)

type Invocation struct {
	Kind            Kind
	Prompt          string
	SessionID       string
	GoalAction      GoalAction
	GoalObjective   string
	GoalTokenBudget *int64
	Canonical       string
}

var specs = []Spec{
	{Name: "new", Usage: "/new [prompt]", Summary: "Start a fresh embedded session for this project"},
	{Name: "resume", Usage: "/resume [session-id]", Summary: "Resume a different embedded session for this project, or pick one when no session ID is given"},
	{Name: "session", Usage: "/session [session-id]", Summary: "Alias for /resume", Hidden: true},
	{Name: "model", Usage: "/model", Summary: "Pick the embedded model and reasoning effort for this and future embedded sessions of the same tool, even after restarting LCR"},
	{Name: "status", Usage: "/status", Summary: "Show embedded session config, limits, and token usage"},
	{Name: "reconnect", Usage: "/reconnect", Summary: "Restart the embedded provider helper and reconnect to the current session"},
	{Name: "compact", Usage: "/compact", Summary: "Compact conversation history to free up context"},
	{Name: "review", Usage: "/review", Summary: "Ask embedded Codex to review uncommitted changes"},
	{Name: "boss", Usage: "/boss", Summary: "Open the high-level boss chat layer"},
	{Name: "skills", Usage: "/skills", Summary: "Open the local Codex skills inventory"},
	{Name: "goal", Usage: "/goal [status|clear|objective] [--budget N]", Summary: "Show, set, or clear the embedded Codex goal"},
	{Name: "settings", Usage: "/settings", Summary: "Open app settings for this embedded provider"},
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
		return []Suggestion{
			{
				Insert:  "/goal",
				Display: "/goal",
				Summary: "Show the current embedded Codex goal",
			},
			{
				Insert:  "/goal status",
				Display: "/goal status",
				Summary: "Show the current embedded Codex goal",
			},
			{
				Insert:  "/goal clear",
				Display: "/goal clear",
				Summary: "Clear the current embedded Codex goal",
			},
			{
				Insert:  "/goal --budget ",
				Display: "/goal --budget N objective",
				Summary: "Set a goal with a token budget",
			},
		}
	default:
		return slashcmd.NameSuggestions(specs, strings.ToLower(fields[0]))
	}
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
	case "resume", "session":
		sessionID := strings.TrimSpace(rawArgs)
		if len(strings.Fields(sessionID)) > 1 {
			return Invocation{}, fmt.Errorf("usage: /resume [session-id]")
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
	if strings.EqualFold(trimmed, "clear") || strings.EqualFold(trimmed, "reset") {
		return Invocation{
			Kind:       KindGoal,
			GoalAction: GoalActionClear,
			Canonical:  "/goal clear",
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
				return "", nil, fmt.Errorf("usage: /goal [status|clear|objective] [--budget N]")
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
