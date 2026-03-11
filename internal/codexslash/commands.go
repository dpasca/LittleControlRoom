package codexslash

import (
	"fmt"
	"strings"
)

type Kind string

const (
	KindNew    Kind = "new"
	KindStatus Kind = "status"
	KindModel  Kind = "model"
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
	Prompt    string
	Canonical string
}

var specs = []Spec{
	{Name: "new", Usage: "/new [prompt]", Summary: "Start a fresh embedded Codex session for this project"},
	{Name: "model", Usage: "/model", Summary: "Pick the embedded Codex model and reasoning effort for the next prompt"},
	{Name: "status", Usage: "/status", Summary: "Show embedded Codex session config, limits, and token usage"},
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
			Summary: "Start a fresh embedded Codex session; optional inline prompt opens the new session with that prompt",
		}}
	case "model":
		return []Suggestion{{
			Insert:  "/model",
			Display: "/model",
			Summary: "Open a local picker for the embedded model and reasoning effort",
		}}
	case "status":
		return []Suggestion{{
			Insert:  "/status",
			Display: "/status",
			Summary: "Show embedded Codex session config, limits, and token usage",
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

func canonicalCommand(name, rawArgs string) string {
	args := strings.TrimSpace(rawArgs)
	if args == "" {
		return "/" + name
	}
	return "/" + name + " " + args
}
