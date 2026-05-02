package bossslash

import (
	"fmt"
	"strings"

	"lcroom/internal/slashcmd"
)

type Kind string

const (
	KindNew      Kind = "new"
	KindSessions Kind = "sessions"
	KindSkills   Kind = "skills"
	KindHelp     Kind = "help"
	KindClose    Kind = "close"
)

type Spec = slashcmd.Spec

type Suggestion = slashcmd.Suggestion

type Invocation struct {
	Kind      Kind
	Prompt    string
	SessionID string
	Canonical string
}

var specs = []Spec{
	{Name: "new", Usage: "/new [prompt]", Summary: "Start a fresh boss chat session"},
	{Name: "sessions", Usage: "/sessions [session-id]", Summary: "List recent boss chat sessions, or switch to one by ID"},
	{Name: "session", Usage: "/session [session-id]", Summary: "Alias for /sessions", Hidden: true},
	{Name: "resume", Usage: "/resume [session-id]", Summary: "Alias for /sessions", Hidden: true},
	{Name: "skills", Usage: "/skills", Summary: "Show Codex skills and local duplicates that may be stale"},
	{Name: "help", Usage: "/help", Summary: "Show boss chat slash commands"},
	{Name: "boss", Usage: "/boss off", Summary: "Close boss mode"},
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
			Summary: "Start a fresh boss chat session; optional inline prompt opens it with that prompt",
		}}
	case "sessions":
		return []Suggestion{sessionSuggestion("/sessions")}
	case "session":
		return []Suggestion{sessionSuggestion("/session")}
	case "resume":
		return []Suggestion{sessionSuggestion("/resume")}
	case "skills":
		return []Suggestion{{
			Insert:  "/skills",
			Display: "/skills",
			Summary: "Show Codex skills and local duplicates that may be stale",
		}}
	case "help":
		return []Suggestion{{
			Insert:  "/help",
			Display: "/help",
			Summary: "Show boss chat slash commands",
		}}
	case "boss":
		argPrefix := ""
		if len(fields) > 1 {
			argPrefix = strings.ToLower(fields[len(fields)-1])
		}
		return slashcmd.EnumSuggestions("/boss ", argPrefix,
			slashcmd.NewChoice("off", "Close boss mode"),
		)
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
	case "sessions", "session", "resume":
		sessionID := strings.TrimSpace(rawArgs)
		if len(strings.Fields(sessionID)) > 1 {
			return Invocation{}, fmt.Errorf("usage: /sessions [session-id]")
		}
		return Invocation{
			Kind:      KindSessions,
			SessionID: sessionID,
			Canonical: slashcmd.CanonicalCommand("sessions", sessionID),
		}, nil
	case "skills":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /skills")
		}
		return Invocation{
			Kind:      KindSkills,
			Canonical: "/skills",
		}, nil
	case "help":
		if strings.TrimSpace(rawArgs) != "" {
			return Invocation{}, fmt.Errorf("usage: /help")
		}
		return Invocation{
			Kind:      KindHelp,
			Canonical: "/help",
		}, nil
	case "boss":
		switch strings.ToLower(strings.TrimSpace(rawArgs)) {
		case "", "off", "close", "exit":
			return Invocation{
				Kind:      KindClose,
				Canonical: "/boss off",
			}, nil
		default:
			return Invocation{}, fmt.Errorf("usage: /boss off")
		}
	default:
		return Invocation{}, fmt.Errorf("unsupported boss slash command: /%s", name)
	}
}

func sessionSuggestion(insert string) Suggestion {
	return Suggestion{
		Insert:  insert,
		Display: insert + " [session-id]",
		Summary: "List recent boss chat sessions, or switch to one by ID",
	}
}
