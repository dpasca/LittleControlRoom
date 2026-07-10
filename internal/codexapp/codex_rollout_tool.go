package codexapp

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type codexReplayToolCall struct {
	name    string
	args    map[string]string
	summary string
}

func newCodexReplayToolCall(name, input, arguments string) codexReplayToolCall {
	name = strings.TrimSpace(name)
	if name == "exec" {
		inner := parseCodexCodeModeToolCalls(input)
		if len(inner) == 1 {
			return inner[0]
		}
		if len(inner) > 1 {
			parts := make([]string, 0, len(inner))
			for _, call := range inner {
				part := strings.TrimSpace(call.name)
				if summary := call.argumentSummary(); summary != "" {
					part += ": " + summary
				}
				parts = append(parts, part)
			}
			return codexReplayToolCall{name: name, summary: strings.Join(parts, " | ")}
		}
		return codexReplayToolCall{name: name, summary: firstCodeModeSourceLine(input)}
	}
	return codexReplayToolCall{
		name: name,
		args: decodeReplayToolJSONArguments(arguments),
	}
}

func (c codexReplayToolCall) transcriptEntry(itemID, status string) TranscriptEntry {
	status = normalizedReplayToolStatus(status)
	if c.name == "exec_command" {
		if command := strings.TrimSpace(c.args["cmd"]); command != "" {
			lines := []string{"$ " + command}
			if cwd := strings.TrimSpace(c.args["workdir"]); cwd != "" {
				lines = append(lines, "# cwd: "+cwd)
			}
			if summary := renderCommandStatusLine(status, nil); summary != "" {
				lines = append(lines, summary)
			}
			return TranscriptEntry{ItemID: itemID, Kind: TranscriptCommand, Text: strings.Join(lines, "\n")}
		}
	}

	name := strings.TrimSpace(c.name)
	if name == "view_image" {
		name = "view"
	}
	text := replayToolSummaryText(name, status, firstNonEmpty(c.summary, c.argumentSummary()))
	return TranscriptEntry{ItemID: itemID, Kind: TranscriptTool, Text: text}
}

func (c codexReplayToolCall) argumentSummary() string {
	switch c.name {
	case "wait":
		if cellID := strings.TrimSpace(c.args["cell_id"]); cellID != "" {
			return "cell " + cellID
		}
	case "view_image":
		if path := strings.TrimSpace(c.args["path"]); path != "" {
			return path
		}
	}
	for _, key := range []string{"cmd", "path", "query", "url", "server", "name"} {
		if value := strings.TrimSpace(c.args[key]); value != "" {
			return value
		}
	}
	if len(c.args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.args))
	for key := range c.args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(3, len(keys)))
	for _, key := range keys {
		value := strings.TrimSpace(c.args[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func replayToolSummaryText(name, status, summary string) string {
	text := "Tool " + strings.TrimSpace(name)
	if status := strings.TrimSpace(status); status != "" {
		text += " " + status
	}
	if summary := strings.TrimSpace(summary); summary != "" {
		text += ": " + summary
	}
	return text
}

func decodeReplayToolJSONArguments(arguments string) map[string]string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			out[key] = text
			continue
		}
		var scalar any
		if err := json.Unmarshal(value, &scalar); err != nil {
			continue
		}
		switch scalar := scalar.(type) {
		case float64:
			out[key] = strconv.FormatFloat(scalar, 'f', -1, 64)
		case bool:
			out[key] = strconv.FormatBool(scalar)
		}
	}
	return out
}

func firstCodeModeSourceLine(input string) string {
	for _, line := range strings.Split(input, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

type codeModeTokenKind uint8

const (
	codeModeIdentifier codeModeTokenKind = iota + 1
	codeModeString
	codeModeNumber
	codeModePunctuation
)

type codeModeToken struct {
	kind  codeModeTokenKind
	value string
}

func parseCodexCodeModeToolCalls(input string) []codexReplayToolCall {
	tokens := lexCodexCodeMode(input)
	if len(tokens) < 6 {
		return nil
	}
	var calls []codexReplayToolCall
	for i := 0; i+5 < len(tokens); i++ {
		if tokens[i].kind != codeModeIdentifier || tokens[i].value != "tools" ||
			tokens[i+1].value != "." || tokens[i+2].kind != codeModeIdentifier ||
			tokens[i+3].value != "(" || tokens[i+4].value != "{" {
			continue
		}
		args, next, ok := parseCodeModeObject(tokens, i+4)
		if !ok {
			continue
		}
		calls = append(calls, codexReplayToolCall{name: tokens[i+2].value, args: args})
		if next > i {
			i = next - 1
		}
	}
	return calls
}

func parseCodeModeObject(tokens []codeModeToken, start int) (map[string]string, int, bool) {
	if start < 0 || start >= len(tokens) || tokens[start].value != "{" {
		return nil, start, false
	}
	args := map[string]string{}
	for i := start + 1; i < len(tokens); {
		if tokens[i].value == "}" {
			return args, i + 1, true
		}
		if tokens[i].value == "," {
			i++
			continue
		}
		if tokens[i].kind != codeModeIdentifier && tokens[i].kind != codeModeString {
			return nil, i, false
		}
		key := tokens[i].value
		i++
		if i >= len(tokens) || tokens[i].value != ":" {
			return nil, i, false
		}
		i++
		if i >= len(tokens) {
			return nil, i, false
		}
		if tokens[i].kind == codeModeString || tokens[i].kind == codeModeNumber || tokens[i].kind == codeModeIdentifier {
			args[key] = tokens[i].value
			i++
		} else {
			i = skipCodeModeValue(tokens, i)
		}
		for i < len(tokens) && tokens[i].value != "," && tokens[i].value != "}" {
			i++
		}
	}
	return nil, len(tokens), false
}

func skipCodeModeValue(tokens []codeModeToken, start int) int {
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].value {
		case "{", "[", "(":
			depth++
		case "}", "]", ")":
			if depth == 0 {
				return i
			}
			depth--
		case ",":
			if depth == 0 {
				return i
			}
		}
	}
	return len(tokens)
}

func lexCodexCodeMode(input string) []codeModeToken {
	tokens := make([]codeModeToken, 0, len(input)/4)
	for i := 0; i < len(input); {
		if isCodeModeSpace(input[i]) {
			i++
			continue
		}
		if input[i] == '/' && i+1 < len(input) {
			switch input[i+1] {
			case '/':
				i += 2
				for i < len(input) && input[i] != '\n' {
					i++
				}
				continue
			case '*':
				i += 2
				for i+1 < len(input) && (input[i] != '*' || input[i+1] != '/') {
					i++
				}
				if i+1 < len(input) {
					i += 2
				}
				continue
			}
		}
		if input[i] == '"' || input[i] == '\'' || input[i] == '`' {
			value, next := scanCodeModeString(input, i)
			tokens = append(tokens, codeModeToken{kind: codeModeString, value: value})
			i = next
			continue
		}
		if isCodeModeIdentifierStart(input[i]) {
			start := i
			i++
			for i < len(input) && isCodeModeIdentifierPart(input[i]) {
				i++
			}
			tokens = append(tokens, codeModeToken{kind: codeModeIdentifier, value: input[start:i]})
			continue
		}
		if input[i] >= '0' && input[i] <= '9' {
			start := i
			i++
			for i < len(input) && ((input[i] >= '0' && input[i] <= '9') || input[i] == '.') {
				i++
			}
			tokens = append(tokens, codeModeToken{kind: codeModeNumber, value: input[start:i]})
			continue
		}
		tokens = append(tokens, codeModeToken{kind: codeModePunctuation, value: input[i : i+1]})
		i++
	}
	return tokens
}

func scanCodeModeString(input string, start int) (string, int) {
	quote := input[start]
	i := start + 1
	for i < len(input) {
		if input[i] == '\\' {
			i += 2
			continue
		}
		if input[i] == quote {
			raw := input[start : i+1]
			if quote == '"' {
				if decoded, err := strconv.Unquote(raw); err == nil {
					return decoded, i + 1
				}
			}
			return decodeSimpleCodeModeString(raw), i + 1
		}
		i++
	}
	return strings.TrimSpace(input[start+1:]), len(input)
}

func decodeSimpleCodeModeString(raw string) string {
	if len(raw) < 2 {
		return raw
	}
	body := raw[1 : len(raw)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case '\n':
		case '\\', '\'', '"', '`':
			out.WriteByte(body[i])
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String()
}

func isCodeModeSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func isCodeModeIdentifierStart(ch byte) bool {
	return ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch >= utf8.RuneSelf
}

func isCodeModeIdentifierPart(ch byte) bool {
	return isCodeModeIdentifierStart(ch) || (ch >= '0' && ch <= '9')
}
