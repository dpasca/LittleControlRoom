package codexslash

import (
	"fmt"
	"strings"
)

type Kind string

const (
	KindNew       Kind = "new"
	KindResume    Kind = "resume"
	KindStatus    Kind = "status"
	KindModel     Kind = "model"
	KindReconnect Kind = "reconnect"
)

type Spec struct {
	Name    string
	Usage   string
	Summary string
	Hidden  bool
}

type Suggestion struct {
	Insert  string
	Display string
	Summary string
}

type Invocation struct {
	Kind      Kind
	Prompt    string
	SessionID string
	Canonical string
}

var specs = []Spec{
	{Name: "new", Usage: "/new [prompt]", Summary: "Start a fresh embedded session for this project"},
	{Name: "resume", Usage: "/resume [session-id]", Summary: "Resume a different embedded session for this project, or pick one when no session ID is given"},
	{Name: "session", Usage: "/session [session-id]", Summary: "Alias for /resume", Hidden: true},
	{Name: "model", Usage: "/model", Summary: "Pick the embedded model and reasoning effort for this and future embedded sessions of the same tool, even after restarting LCR"},
	{Name: "status", Usage: "/status", Summary: "Show embedded session config, limits, and token usage"},
	{Name: "reconnect", Usage: "/reconnect", Summary: "Restart the embedded provider helper and reconnect to the current session"},
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
		return nameSuggestions("")
	}

	fields := strings.Fields(body)
	if len(fields) <= 1 && !strings.HasSuffix(trimmed, " ") {
		return nameSuggestions(strings.ToLower(fields[0]))
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
	default:
		return nameSuggestions(strings.ToLower(fields[0]))
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

	name, rawArgs := splitCommandBody(body)
	switch strings.ToLower(name) {
	case "new":
		return Invocation{
			Kind:      KindNew,
			Prompt:    strings.TrimSpace(rawArgs),
			Canonical: canonicalCommand("new", rawArgs),
		}, nil
	case "resume", "session":
		sessionID := strings.TrimSpace(rawArgs)
		if len(strings.Fields(sessionID)) > 1 {
			return Invocation{}, fmt.Errorf("usage: /resume [session-id]")
		}
		return Invocation{
			Kind:      KindResume,
			SessionID: sessionID,
			Canonical: canonicalCommand("resume", rawArgs),
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
	default:
		return Invocation{}, fmt.Errorf("unsupported embedded slash command: /%s", name)
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

func nameSuggestions(prefix string) []Suggestion {
	out := make([]Suggestion, 0, len(specs))
	for _, spec := range specs {
		if prefix == "" && spec.Hidden {
			continue
		}
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

func resumeSuggestion(insert string) Suggestion {
	return Suggestion{
		Insert:  insert,
		Display: insert + " [session-id]",
		Summary: "Resume another embedded session for this project, or open a picker when no session ID is given",
	}
}

func canonicalCommand(name, rawArgs string) string {
	args := strings.TrimSpace(rawArgs)
	if args == "" {
		return "/" + name
	}
	return "/" + name + " " + args
}
