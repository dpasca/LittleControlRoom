// Package transcriptactivity turns raw engineer tool traffic (provider tool
// calls, shell commands, and file-change items) into intent-level actions, so
// surfaces can show what an engineer accomplished instead of which commands it
// happened to run. It is UI-neutral: callers decide how to render a Summary.
package transcriptactivity

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"lcroom/internal/codexapp"
)

type Intent string

const (
	IntentEdit     Intent = "edit"
	IntentRead     Intent = "read"
	IntentSearch   Intent = "search"
	IntentTest     Intent = "test"
	IntentCheck    Intent = "check" // build, lint, typecheck, format
	IntentGit      Intent = "git"
	IntentWeb      Intent = "web"
	IntentDelegate Intent = "delegate"
	IntentMCP      Intent = "mcp"
	IntentScratch  Intent = "scratch" // throwaway work outside the project, e.g. under /tmp
	IntentRun      Intent = "run"     // any other command or tool
)

type Outcome string

const (
	OutcomeUnknown Outcome = ""
	OutcomeRunning Outcome = "running"
	OutcomeOK      Outcome = "ok"
	OutcomeFailed  Outcome = "failed"
)

// Action is one intent-level step derived from a transcript entry.
type Action struct {
	Intent  Intent
	Label   string   // short human label: `vitest idleLayers`, `".evaluate("`, `git commit`
	Paths   []string // files edited or read, as reported
	Count   int      // unnamed edits (e.g. "Applying 3 file change(s)"), otherwise 0
	Outcome Outcome
	Detail  string // result detail: "3 passed", "1 failed", "exit 1"
}

// ActivityKind reports whether a transcript kind carries tool traffic that
// Classify understands. Conversation, reasoning, and status entries do not.
func ActivityKind(kind codexapp.TranscriptKind) bool {
	switch kind {
	case codexapp.TranscriptTool, codexapp.TranscriptCommand, codexapp.TranscriptFileChange:
		return true
	default:
		return false
	}
}

// Classify derives the intent-level actions for one transcript entry. A shell
// command can yield several actions (`sed -i … && go test`); bookkeeping tools
// yield none.
func Classify(entry codexapp.TranscriptEntry) []Action {
	switch entry.Kind {
	case codexapp.TranscriptCommand:
		return classifyCommandEntry(entry)
	case codexapp.TranscriptFileChange:
		return classifyFileChangeEntry(entry)
	case codexapp.TranscriptTool:
		return classifyToolEntry(entry)
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Command entries

var (
	codexStatusLinePattern  = regexp.MustCompile(`^\[command ([A-Za-z_ ]+?)(?:, exit (-?\d+))?\]$`)
	claudeExitCodePattern   = regexp.MustCompile(`^Exit code (\d+)`)
	programNamePattern      = regexp.MustCompile(`^[\w.@+~\[-]+$`)
	heredocDelimiterPattern = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_]\w*)['"]?`)
)

func classifyCommandEntry(entry codexapp.TranscriptEntry) []Action {
	command, output := splitCommandEntry(entry)
	if command == "" {
		return nil
	}
	actions := ClassifyCommand(command)
	if len(actions) == 0 {
		return nil
	}
	outcome, detail := commandOutcome(entry, output)
	last := &actions[len(actions)-1]
	for i := range actions {
		if actions[i].Outcome == OutcomeUnknown {
			actions[i].Outcome = outcome
			if outcome == OutcomeFailed && &actions[i] != last {
				// A chained command fails as a whole; only the last step owns it.
				actions[i].Outcome = OutcomeOK
			}
		}
	}
	if last.Intent == IntentTest {
		if testDetail, failed := parseTestResult(output); testDetail != "" {
			last.Detail = testDetail
			if failed {
				last.Outcome = OutcomeFailed
			}
		}
	}
	if last.Detail == "" && last.Outcome == OutcomeFailed {
		last.Detail = detail
	}
	return actions
}

func splitCommandEntry(entry codexapp.TranscriptEntry) (string, []string) {
	lines := strings.Split(strings.TrimSpace(entry.Text), "\n")
	command := strings.TrimSpace(entry.CommandText)
	outputStart := 0
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "$ ") {
		if command == "" {
			command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "$ "))
		}
		outputStart = 1
		// Multi-line commands (heredocs) echo their body before the output.
		if extra := strings.Count(command, "\n"); extra > 0 && outputStart+extra <= len(lines) {
			outputStart += extra
		} else if match := heredocDelimiterPattern.FindStringSubmatch(lines[0]); match != nil && entry.CommandText == "" {
			for j := 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == match[1] {
					command = strings.TrimSpace(strings.TrimPrefix(strings.Join(lines[:j+1], "\n"), "$ "))
					outputStart = j + 1
					break
				}
			}
		}
	}
	output := make([]string, 0, len(lines)-outputStart)
	for _, line := range lines[outputStart:] {
		if strings.HasPrefix(strings.TrimSpace(line), "# cwd:") {
			continue
		}
		output = append(output, line)
	}
	return command, output
}

func commandOutcome(entry codexapp.TranscriptEntry, output []string) (Outcome, string) {
	if entry.Failed {
		detail := ""
		if len(output) > 0 {
			if match := claudeExitCodePattern.FindStringSubmatch(strings.TrimSpace(output[0])); match != nil {
				detail = "exit " + match[1]
			}
		}
		return OutcomeFailed, detail
	}
	for i := len(output) - 1; i >= 0; i-- {
		line := strings.TrimSpace(output[i])
		if line == "" {
			continue
		}
		match := codexStatusLinePattern.FindStringSubmatch(line)
		if match == nil {
			break
		}
		status := strings.ToLower(strings.ReplaceAll(match[1], " ", ""))
		exit := match[2]
		switch {
		case exit != "" && exit != "0":
			return OutcomeFailed, "exit " + exit
		case status == "failed" || status == "declined" || status == "error":
			return OutcomeFailed, status
		case status == "inprogress" || status == "running" || status == "pending":
			return OutcomeRunning, ""
		default:
			return OutcomeOK, ""
		}
	}
	if len(output) > 0 {
		if match := claudeExitCodePattern.FindStringSubmatch(strings.TrimSpace(output[0])); match != nil && match[1] != "0" {
			return OutcomeFailed, "exit " + match[1]
		}
	}
	if entry.CommandText == "" {
		// Codex streams command output first and appends its status line on
		// completion; Claude command entries only exist once a result arrived.
		return OutcomeRunning, ""
	}
	return OutcomeOK, ""
}

// ---------------------------------------------------------------------------
// File-change entries

var (
	fileChangeCountPattern = regexp.MustCompile(`Applying (\d+) file change`)
	patchFileLinePattern   = regexp.MustCompile(`^(?:\*\*\* (?:Update|Add|Delete) File: |\+\+\+ b/|[MAD] )(\S.*)$`)
)

func classifyFileChangeEntry(entry codexapp.TranscriptEntry) []Action {
	text := strings.TrimSpace(entry.Text)
	action := Action{Intent: IntentEdit, Outcome: OutcomeOK}
	lines := strings.Split(text, "\n")
	first := strings.TrimSpace(lines[0])
	switch {
	case strings.HasPrefix(first, "Patch touched "):
		for _, part := range strings.Split(strings.TrimPrefix(first, "Patch touched "), ",") {
			action.Paths = appendUnique(action.Paths, strings.TrimSpace(part))
		}
	case strings.HasPrefix(first, "Files touched"):
		for _, line := range lines[1:] {
			action.Paths = appendUnique(action.Paths, strings.TrimSpace(line))
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if match := patchFileLinePattern.FindStringSubmatch(line); match != nil {
			action.Paths = appendUnique(action.Paths, strings.TrimSpace(match[1]))
		}
		if strings.HasPrefix(line, "[file changes ") {
			status := strings.TrimSuffix(strings.TrimPrefix(line, "[file changes "), "]")
			switch strings.ToLower(status) {
			case "failed", "declined":
				action.Outcome = OutcomeFailed
				action.Detail = status
			case "inprogress":
				action.Outcome = OutcomeRunning
			}
		}
	}
	if len(action.Paths) == 0 {
		action.Count = 1
		if match := fileChangeCountPattern.FindStringSubmatch(text); match != nil {
			if n, err := strconv.Atoi(match[1]); err == nil && n > 0 {
				action.Count = n
			}
		}
	}
	return []Action{scratchIfOutside(action)}
}

// ---------------------------------------------------------------------------
// Provider tool entries

type toolLine struct {
	name    string
	status  string
	summary string
	mcp     bool
	web     bool
}

func parseToolLine(entry codexapp.TranscriptEntry) toolLine {
	full := strings.TrimSpace(entry.Text)
	text := full
	if first, _, ok := strings.Cut(full, "\n"); ok {
		text = strings.TrimSpace(first)
	}
	switch {
	case strings.HasPrefix(text, "Web search: "):
		return toolLine{name: "web_search", summary: strings.TrimPrefix(text, "Web search: "), web: true}
	case strings.HasPrefix(text, "Viewed image: "):
		return toolLine{name: "view_image", summary: strings.TrimPrefix(text, "Viewed image: ")}
	case strings.HasPrefix(text, "MCP tool "):
		name, status := splitBracketStatus(strings.TrimPrefix(text, "MCP tool "))
		return toolLine{name: name, status: status, mcp: true}
	case strings.HasPrefix(text, "Tool "):
		rest := strings.TrimPrefix(text, "Tool ")
		if strings.HasPrefix(rest, "activity") {
			return toolLine{name: "activity", summary: strings.TrimPrefix(strings.TrimPrefix(rest, "activity"), ": ")}
		}
		head, summary, _ := strings.Cut(rest, ": ")
		if name, status := splitBracketStatus(head); status != "" {
			return toolLine{name: name, status: status, summary: summary}
		}
		name, status, _ := strings.Cut(head, " ")
		return toolLine{name: name, status: status, summary: summary}
	}
	// Claude Code: "<Name>: <summary>" with the structured tool name alongside.
	// Bash summaries keep their full (possibly multi-line) command.
	name := strings.TrimSpace(entry.ToolName)
	summary := text
	if name != "" {
		summary = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(full, name), ":"))
	} else if head, rest, ok := strings.Cut(text, ": "); ok && !strings.Contains(head, " ") {
		name, summary = head, rest
	}
	return toolLine{name: name, summary: summary}
}

func splitBracketStatus(text string) (string, string) {
	idx := strings.Index(text, " [")
	if idx < 0 {
		return strings.TrimSpace(text), ""
	}
	status := text[idx+2:]
	if end := strings.IndexByte(status, ']'); end >= 0 {
		status = status[:end]
	}
	return strings.TrimSpace(text[:idx]), strings.TrimSpace(status)
}

func toolOutcome(status string) Outcome {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return OutcomeUnknown
	case "running", "pending", "inprogress", "in_progress", "started":
		return OutcomeRunning
	case "error", "failed", "declined", "call failed":
		return OutcomeFailed
	default:
		return OutcomeOK
	}
}

func classifyToolEntry(entry codexapp.TranscriptEntry) []Action {
	tool := parseToolLine(entry)
	outcome := toolOutcome(tool.status)
	lower := strings.ToLower(tool.name)
	toolPath := strings.TrimSpace(entry.ToolPath)
	if tool.mcp || strings.HasPrefix(lower, "mcp__") {
		return []Action{{Intent: IntentMCP, Label: mcpLabel(tool.name), Outcome: outcome}}
	}
	if tool.web {
		return []Action{{Intent: IntentWeb, Label: tool.summary, Outcome: outcome}}
	}
	withPath := func(intent Intent) []Action {
		p := firstNonEmpty(toolPath, tool.summary)
		action := Action{Intent: intent, Outcome: outcome}
		if p != "" {
			action.Paths = []string{p}
		}
		return []Action{scratchIfOutside(action)}
	}
	switch lower {
	case "todowrite", "todoread", "update_plan", "toolsearch", "exitplanmode", "enterplanmode",
		"askuserquestion", "skill", "activity", "wait", "sendmessage":
		return nil
	case "bash", "shell", "exec_command", "execute", "command":
		// Claude reports the call here and its result as a separate command
		// entry; the command entry is authoritative once it arrives.
		command := strings.TrimSuffix(tool.summary, "...")
		if looksLikeDescription(command) {
			// OpenCode reports the model's description instead of the command.
			return []Action{{Intent: IntentRun, Label: command, Outcome: outcome}}
		}
		actions := ClassifyCommand(command)
		for i := range actions {
			if actions[i].Outcome == OutcomeUnknown {
				actions[i].Outcome = firstOutcome(outcome, OutcomeRunning)
			}
		}
		return actions
	case "read", "read_file", "view", "view_image", "cat", "notebookread":
		return withPath(IntentRead)
	case "write", "edit", "multiedit", "notebookedit", "patch", "apply_patch", "apply_diff", "write_file", "edit_file", "str_replace_based_edit_tool":
		return withPath(IntentEdit)
	case "grep", "glob", "search", "find", "rg", "list_files", "list", "ls", "codesearch":
		return []Action{{Intent: IntentSearch, Label: quoteLabel(tool.summary), Outcome: outcome}}
	case "webfetch", "websearch", "web_search", "fetch":
		return []Action{{Intent: IntentWeb, Label: tool.summary, Outcome: outcome}}
	case "task", "agent", "subagent":
		return []Action{{Intent: IntentDelegate, Label: tool.summary, Outcome: outcome}}
	}
	label := tool.name
	if label == "" {
		label = "tool"
	}
	return []Action{{Intent: IntentRun, Label: label, Outcome: outcome}}
}

func looksLikeDescription(text string) bool {
	first, _, hasSpace := strings.Cut(strings.TrimSpace(text), " ")
	if !hasSpace || first == "" || strings.ContainsAny(first, "/=.$") {
		return false
	}
	return first[0] >= 'A' && first[0] <= 'Z'
}

func mcpLabel(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "mcp__")
	name = strings.Replace(name, "__", " ", 1)
	return strings.Replace(name, "/", " ", 1)
}

// ---------------------------------------------------------------------------
// Shell commands

// ClassifyCommand derives the actions a shell command performs. Chained
// statements yield one action each; pipelines are judged by their first stage.
func ClassifyCommand(command string) []Action {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	header, body := command, ""
	if idx := strings.Index(command, "<<"); idx >= 0 {
		if nl := strings.IndexByte(command[idx:], '\n'); nl >= 0 {
			header = command[:idx+nl]
			body = command[idx+nl+1:]
		}
	}
	header = strings.ReplaceAll(header, "\\\n", " ")
	var actions []Action
	cwd := ""
	for _, statement := range splitShell(header, statementSeparators) {
		statement, ok := stripShellKeywords(statement)
		if !ok {
			continue
		}
		stages := splitShell(statement, pipeSeparators)
		if len(stages) == 0 {
			continue
		}
		if dir, ok := cdTarget(stages[0]); ok {
			cwd = dir
			continue
		}
		action, ok := classifyStatement(stages, body)
		if !ok {
			continue
		}
		actions = append(actions, resolveInDir(action, cwd))
	}
	return mergeAdjacent(actions)
}

// stripShellKeywords removes control-flow words and grouping so the command
// inside `for …; do cmd; done` or `(cd x && cmd)` is classified. Statements
// that are pure control flow are dropped.
func stripShellKeywords(statement string) (string, bool) {
	statement = strings.TrimSpace(statement)
	for {
		trimmed := strings.TrimSpace(strings.TrimLeft(statement, "({"))
		trimmed = strings.TrimSpace(strings.TrimRight(trimmed, ")}"))
		word, rest, _ := strings.Cut(trimmed, " ")
		switch word {
		case "do", "then", "else", "elif", "if", "while", "until", "!":
			statement = rest
			continue
		case "for", "case", "select", "done", "fi", "esac", "function", "":
			return "", false
		}
		return trimmed, true
	}
}

func cdTarget(stageText string) (string, bool) {
	words := tokenize(stageText)
	if len(words) == 0 || (words[0] != "cd" && words[0] != "pushd") {
		return "", false
	}
	if len(words) < 2 {
		return "", true
	}
	return words[1], true
}

// resolveInDir anchors relative paths to an absolute `cd` target, so work in a
// scratch directory is recognized as scratch.
func resolveInDir(action Action, cwd string) Action {
	action.Paths = literalPaths(action.Paths)
	if strings.HasPrefix(cwd, "/") {
		for i, p := range action.Paths {
			if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~") {
				action.Paths[i] = path.Join(cwd, p)
			}
		}
		if IsScratchPath(cwd+"/") && action.Intent == IntentRun {
			action.Intent = IntentScratch
		}
	}
	return scratchIfOutside(action)
}

var (
	statementSeparators = []string{"&&", "||", ";", "\n"}
	pipeSeparators      = []string{"|"}
)

// splitShell splits text on unquoted separators, preferring longer matches so
// `||` is not read as a pipe.
func splitShell(text string, separators []string) []string {
	var parts []string
	var current strings.Builder
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			current.WriteByte(c)
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(text) {
				i++
				current.WriteByte(text[i])
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			current.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(text) {
			current.WriteByte(c)
			i++
			current.WriteByte(text[i])
			continue
		}
		if strings.HasPrefix(text[i:], "||") && !containsString(separators, "||") {
			current.WriteString("||")
			i++
			continue
		}
		if c == '|' && i > 0 && text[i-1] == '>' {
			current.WriteByte(c) // `>|` clobber redirect
			continue
		}
		matched := ""
		for _, sep := range separators {
			if strings.HasPrefix(text[i:], sep) && len(sep) > len(matched) {
				matched = sep
			}
		}
		if matched != "" {
			if part := strings.TrimSpace(current.String()); part != "" {
				parts = append(parts, part)
			}
			current.Reset()
			i += len(matched) - 1
			continue
		}
		current.WriteByte(c)
	}
	if part := strings.TrimSpace(current.String()); part != "" {
		parts = append(parts, part)
	}
	return parts
}

// tokenize splits a shell stage into words, removing quotes.
func tokenize(text string) []string {
	var words []string
	var current strings.Builder
	inWord := false
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(text) && strings.IndexByte("$`\"\\", text[i+1]) >= 0 {
				i++
				current.WriteByte(text[i])
			} else {
				current.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == '\\' && i+1 < len(text):
			i++
			current.WriteByte(text[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}

type stage struct {
	program   string
	args      []string
	redirects []string // output redirection targets
	heredoc   bool
}

func parseStage(text string) stage {
	words := tokenize(text)
	var s stage
	var args []string
	for i := 0; i < len(words); i++ {
		word := words[i]
		switch {
		case strings.HasPrefix(word, "<<"):
			s.heredoc = true
			if word == "<<" || word == "<<-" {
				i++ // skip the delimiter word
			}
			continue
		case word == ">" || word == ">>" || word == "1>" || word == ">|" || word == "&>":
			if i+1 < len(words) {
				s.redirects = append(s.redirects, words[i+1])
				i++
			}
			continue
		case word == "2>" || word == "<" || word == "2>>":
			i++
			continue
		case strings.HasPrefix(word, "2>") || strings.HasPrefix(word, "<"):
			continue
		case strings.HasPrefix(word, ">>") || (strings.HasPrefix(word, ">") && !strings.HasPrefix(word, ">&")):
			s.redirects = append(s.redirects, strings.TrimLeft(word, ">|"))
			continue
		case strings.HasPrefix(word, ">&") || strings.HasPrefix(word, "&>"):
			continue
		}
		args = append(args, word)
	}
	// Drop wrappers and environment assignments.
	for len(args) > 0 {
		first := args[0]
		switch {
		case isEnvAssignment(first):
			args = args[1:]
		case first == "sudo" || first == "time" || first == "nohup" || first == "command" || first == "exec" || first == "env":
			args = args[1:]
		case first == "timeout" || first == "gtimeout":
			args = args[1:]
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				args = args[1:]
			}
		default:
			s.program = path.Base(first)
			s.args = args[1:]
			s.redirects = filterRedirects(s.redirects)
			return s
		}
	}
	s.redirects = filterRedirects(s.redirects)
	return s
}

func isEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := strings.TrimSuffix(word[:eq], "+") // `args+=(…)`
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func filterRedirects(targets []string) []string {
	out := targets[:0]
	for _, target := range targets {
		if target == "" || target == "/dev/null" || strings.HasPrefix(target, "&") {
			continue
		}
		out = append(out, target)
	}
	return out
}

func classifyStatement(stageTexts []string, heredocBody string) (Action, bool) {
	first := parseStage(stageTexts[0])
	var redirects []string
	for _, text := range stageTexts {
		st := parseStage(text)
		redirects = append(redirects, st.redirects...)
		if st.program == "tee" {
			redirects = append(redirects, nonFlagArgs(st.args)...)
		}
	}
	if first.program != "" && !programNamePattern.MatchString(first.program) {
		return Action{}, false // variable, substitution, or parse fragment; nothing readable to report
	}
	if first.program == "" {
		if len(redirects) > 0 {
			return Action{Intent: IntentEdit, Paths: redirects}, true
		}
		return Action{}, false
	}
	program := first.program
	args := first.args
	if len(redirects) > 0 {
		switch program {
		case "cat", "echo", "printf", "tee", "jq", "sed", "awk", "sort", "head", "tail", "base64", "envsubst":
			return Action{Intent: IntentEdit, Paths: redirects}, true
		}
	}
	switch program {
	case "cd", "pushd", "popd", "export", "set", "unset", "source", ".", "true", "false", "sleep",
		"echo", "printf", "which", "type", "pwd", "clear", "date", "trap", "wait", "exit",
		"[", "[[", "test", "local", "declare", "read", "shift", "return", "mkdir":
		return Action{}, false
	case "git":
		return classifyGit(args), true
	case "gh":
		return Action{Intent: IntentGit, Label: "gh " + strings.Join(firstWords(nonFlagArgs(args), 2), " ")}, true
	case "grep", "egrep", "fgrep", "rg", "ag", "ack":
		return Action{Intent: IntentSearch, Label: quoteLabel(searchPattern(args))}, true
	case "find", "fd", "locate", "mdfind":
		return Action{Intent: IntentSearch, Label: findLabel(program, args)}, true
	case "sed":
		if hasInPlaceFlag(args) {
			return Action{Intent: IntentEdit, Paths: sedTargets(args)}, true
		}
		return Action{Intent: IntentRead, Paths: sedTargets(args)}, true
	case "perl":
		if hasInPlaceFlag(args) {
			return Action{Intent: IntentEdit, Paths: sedTargets(args)}, true
		}
		return classifyScript(program, args, heredocBody, first.heredoc)
	case "cat", "head", "tail", "less", "more", "nl", "wc", "bat", "stat", "file", "xxd", "hexdump", "od", "strings", "diff", "cmp", "readlink", "realpath", "du":
		return Action{Intent: IntentRead, Paths: nonFlagArgs(args)}, true
	case "ls", "tree", "exa", "eza":
		return Action{Intent: IntentRead, Paths: nonFlagArgs(args)}, true
	case "awk", "jq", "yq", "sort", "uniq", "cut", "tr", "column":
		return Action{Intent: IntentRead, Paths: nil}, true
	case "mv", "cp", "ln", "install", "rsync":
		// Only the destination changes.
		operands := nonFlagArgs(args)
		return Action{Intent: IntentEdit, Paths: operands[max(0, len(operands)-1):]}, true
	case "tee", "touch", "rm", "rmdir", "chmod", "chown", "unzip", "tar", "patch":
		return Action{Intent: IntentEdit, Paths: nonFlagArgs(args)}, true
	case "python", "python3", "node", "ruby", "bun", "deno", "tsx", "ts-node", "bash", "sh", "zsh", "osascript", "swift":
		if action, ok := classifyTestCommand(program, args); ok {
			return action, true
		}
		return classifyScript(program, args, heredocBody, first.heredoc)
	case "curl", "wget", "http", "xh":
		return Action{Intent: IntentWeb, Label: webLabel(args)}, true
	}
	if action, ok := classifyTestCommand(program, args); ok {
		return action, true
	}
	if action, ok := classifyCheckCommand(program, args); ok {
		return action, true
	}
	return Action{Intent: IntentRun, Label: runLabel(program, args)}, true
}

func classifyGit(args []string) Action {
	sub := ""
	rest := args
	for len(rest) > 0 {
		word := rest[0]
		rest = rest[1:]
		if word == "-C" || word == "-c" || word == "--git-dir" || word == "--work-tree" {
			if len(rest) > 0 {
				rest = rest[1:]
			}
			continue
		}
		if strings.HasPrefix(word, "-") {
			continue
		}
		sub = word
		break
	}
	label := "git"
	if sub != "" {
		label += " " + sub
	}
	return Action{Intent: IntentGit, Label: label}
}

var testRunners = map[string]bool{
	"vitest": true, "jest": true, "pytest": true, "mocha": true, "ava": true, "tap": true,
	"rspec": true, "phpunit": true, "ctest": true, "gotestsum": true, "karma": true,
}

func classifyTestCommand(program string, args []string) (Action, bool) {
	words := nonFlagArgs(args)
	switch program {
	case "go":
		if len(words) > 0 && words[0] == "test" {
			return Action{Intent: IntentTest, Label: "go test" + targetSuffix(words[1:])}, true
		}
	case "cargo", "swift", "dotnet", "mix", "deno", "bun", "zig":
		if len(words) > 0 && words[0] == "test" {
			return Action{Intent: IntentTest, Label: program + " test" + targetSuffix(words[1:])}, true
		}
	case "make", "just", "task":
		for _, word := range words {
			if word == "test" || strings.HasPrefix(word, "test-") || strings.HasPrefix(word, "test_") || word == "check" {
				intent := IntentTest
				if word == "check" {
					intent = IntentCheck
				}
				return Action{Intent: intent, Label: program + " " + word}, true
			}
		}
	case "npm", "pnpm", "yarn", "npx", "bunx", "pnpx":
		rest := words
		for len(rest) > 0 && (rest[0] == "exec" || rest[0] == "run" || rest[0] == "dlx" || rest[0] == "x") {
			rest = rest[1:]
		}
		if len(rest) > 0 {
			if rest[0] == "test" || rest[0] == "t" || strings.HasPrefix(rest[0], "test:") {
				return Action{Intent: IntentTest, Label: program + " " + rest[0] + targetSuffix(rest[1:])}, true
			}
			if testRunners[rest[0]] || rest[0] == "playwright" && len(rest) > 1 && rest[1] == "test" {
				return classifyTestCommand(rest[0], rest[1:])
			}
		}
	case "python", "python3":
		if len(args) >= 2 && args[0] == "-m" && (args[1] == "pytest" || args[1] == "unittest") {
			return Action{Intent: IntentTest, Label: args[1] + targetSuffix(nonFlagArgs(args[2:]))}, true
		}
	case "xcodebuild":
		if containsString(words, "test") {
			return Action{Intent: IntentTest, Label: "xcodebuild test"}, true
		}
	case "playwright":
		if len(words) > 0 && words[0] == "test" {
			return Action{Intent: IntentTest, Label: "playwright" + targetSuffix(words[1:])}, true
		}
	}
	if testRunners[program] {
		rest := words
		if program == "vitest" && len(rest) > 0 && (rest[0] == "run" || rest[0] == "watch") {
			rest = rest[1:]
		}
		return Action{Intent: IntentTest, Label: program + targetSuffix(rest)}, true
	}
	return Action{}, false
}

var checkPrograms = map[string]bool{
	"tsc": true, "eslint": true, "prettier": true, "gofmt": true, "goimports": true, "golangci-lint": true,
	"staticcheck": true, "ruff": true, "black": true, "mypy": true, "pyright": true, "flake8": true,
	"rustfmt": true, "clippy-driver": true, "swiftlint": true, "swiftformat": true, "shellcheck": true,
	"biome": true, "stylelint": true, "vue-tsc": true, "svelte-check": true,
}

func classifyCheckCommand(program string, args []string) (Action, bool) {
	words := nonFlagArgs(args)
	if checkPrograms[program] {
		return Action{Intent: IntentCheck, Label: program}, true
	}
	switch program {
	case "go":
		if len(words) > 0 && (words[0] == "build" || words[0] == "vet") {
			return Action{Intent: IntentCheck, Label: "go " + words[0] + targetSuffix(words[1:])}, true
		}
	case "cargo":
		if len(words) > 0 && (words[0] == "build" || words[0] == "check" || words[0] == "clippy" || words[0] == "fmt") {
			return Action{Intent: IntentCheck, Label: "cargo " + words[0]}, true
		}
	case "npm", "pnpm", "yarn", "npx", "bunx", "pnpx", "bun":
		rest := words
		for len(rest) > 0 && (rest[0] == "exec" || rest[0] == "run" || rest[0] == "dlx" || rest[0] == "x") {
			rest = rest[1:]
		}
		if len(rest) > 0 {
			if checkPrograms[rest[0]] {
				return Action{Intent: IntentCheck, Label: rest[0]}, true
			}
			switch {
			case rest[0] == "build" || rest[0] == "lint" || rest[0] == "typecheck" || rest[0] == "format" ||
				strings.HasPrefix(rest[0], "lint:") || strings.HasPrefix(rest[0], "build:") || rest[0] == "check":
				return Action{Intent: IntentCheck, Label: program + " " + rest[0]}, true
			}
		}
	case "make", "just":
		for _, word := range words {
			switch word {
			case "build", "lint", "vet", "fmt", "format", "typecheck", "scan", "doctor":
				return Action{Intent: IntentCheck, Label: program + " " + word}, true
			}
		}
	case "swift", "xcodebuild", "dotnet":
		if containsString(words, "build") {
			return Action{Intent: IntentCheck, Label: program + " build"}, true
		}
	}
	return Action{}, false
}

var (
	scriptWritePattern = regexp.MustCompile(`open\([^)]*,\s*['"][wa]|\.write_text\(|\.write_bytes\(|writeFileSync\(|writeFile\(|File\.write`)
	// Only slash-containing literals count as paths; bare `a.b` strings are
	// usually identifiers, not files.
	scriptTempLiteralPattern = regexp.MustCompile(`['"](?:/private)?/tmp/`)
	scriptPathPattern        = regexp.MustCompile(`['"]((?:[\w.@~-]+/)+[\w.@-]+\.[A-Za-z]\w{0,5})['"]`)
)

func classifyScript(program string, args []string, body string, heredoc bool) (Action, bool) {
	script := body
	if !heredoc {
		// Inline scripts: `python3 -c '…'`, `node -e '…'`.
		for i, arg := range args {
			if (arg == "-c" || arg == "-e") && i+1 < len(args) {
				script = args[i+1]
				break
			}
		}
		if script == "" {
			if files := nonFlagArgs(args); len(files) > 0 {
				return Action{Intent: IntentRun, Label: program + " " + path.Base(files[0])}, true
			}
		}
	}
	if script != "" && scriptWritePattern.MatchString(script) {
		var paths []string
		for _, match := range scriptPathPattern.FindAllStringSubmatch(script, -1) {
			paths = appendUnique(paths, match[1])
		}
		if len(paths) == 0 && scriptTempLiteralPattern.MatchString(script) {
			return Action{Intent: IntentScratch, Label: program + " script"}, true
		}
		return Action{Intent: IntentEdit, Paths: paths, Label: program + " script"}, true
	}
	return Action{Intent: IntentRun, Label: program + " script"}, true
}

// ---------------------------------------------------------------------------
// Test output

var (
	vitestSummaryPattern = regexp.MustCompile(`^\s*Tests\s+(.+?)\s*\(\d+\)\s*$`)
	jestSummaryPattern   = regexp.MustCompile(`^\s*Tests:\s+(.+?),\s*\d+ total`)
	pytestSummaryPattern = regexp.MustCompile(`^=+ (.+?) in [\d.]+s(?: \([^)]*\))? =+$`)
	cargoSummaryPattern  = regexp.MustCompile(`^test result: \w+\. (\d+) passed; (\d+) failed`)
	failedCountPattern   = regexp.MustCompile(`\b([1-9]\d*) (?:failed|failing|errors?)\b`)
)

// parseTestResult extracts a compact pass/fail summary from test output.
func parseTestResult(output []string) (string, bool) {
	goOK, goFail := 0, 0
	// Summaries sit at the end; bound the scan for very chatty test output.
	stop := max(0, len(output)-400)
	for i := len(output) - 1; i >= stop; i-- {
		line := strings.TrimRight(output[i], " \t")
		for _, pattern := range []*regexp.Regexp{vitestSummaryPattern, jestSummaryPattern, pytestSummaryPattern} {
			if match := pattern.FindStringSubmatch(line); match != nil {
				detail := strings.ReplaceAll(match[1], " | ", ", ")
				return detail, failedCountPattern.MatchString(detail)
			}
		}
		if match := cargoSummaryPattern.FindStringSubmatch(line); match != nil {
			if match[2] != "0" {
				return match[2] + " failed, " + match[1] + " passed", true
			}
			return match[1] + " passed", false
		}
		switch {
		case strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "ok \t") || strings.HasPrefix(line, "ok  "):
			goOK++
		case strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "--- FAIL"):
			goFail++
		}
	}
	switch {
	case goFail > 0:
		return plural(goFail, "failure", "failures"), true
	case goOK > 0:
		return plural(goOK, "package ok", "packages ok"), false
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Labels and helpers

func nonFlagArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func hasInPlaceFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-i" || strings.HasPrefix(arg, "-i") || arg == "--in-place" || strings.HasPrefix(arg, "-pi") || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "i") && len(arg) <= 4) {
			return true
		}
	}
	return false
}

// sedTargets returns the file operands of a sed/perl invocation: the arguments
// after the script expression.
func sedTargets(args []string) []string {
	var files []string
	scriptSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-e" || arg == "-f":
			i++
			scriptSeen = true
		case arg == "-i" && i+1 < len(args) && args[i+1] == "":
			i++ // BSD sed: -i ''
		case strings.HasPrefix(arg, "-"):
		case !scriptSeen:
			scriptSeen = true
		default:
			files = append(files, arg)
		}
	}
	return files
}

func searchPattern(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-e", "--regexp":
			if i+1 < len(args) {
				return args[i+1]
			}
		case "-A", "-B", "-C", "-m", "--max-count", "-g", "--glob", "-t", "--type", "--include", "--exclude", "-f":
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

func findLabel(program string, args []string) string {
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-name", "-iname", "-path", "-g", "--glob":
			return quoteLabel(args[i+1])
		}
	}
	words := nonFlagArgs(args)
	if program == "fd" && len(words) > 0 {
		return quoteLabel(words[0])
	}
	return "files"
}

func webLabel(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			host := strings.TrimPrefix(strings.TrimPrefix(arg, "https://"), "http://")
			if idx := strings.IndexAny(host, "/?#"); idx >= 0 {
				host = host[:idx]
			}
			return host
		}
	}
	return ""
}

func runLabel(program string, args []string) string {
	// Only an immediate subcommand (`docker compose`, `pnpm render`) belongs in
	// the label; later words are usually flag values or operands.
	words := args[:min(1, len(args))]
	words = nonFlagArgs(words)
	label := program
	if len(words) > 0 && !strings.Contains(words[0], "/") && len(words[0]) <= 24 {
		label += " " + words[0]
	}
	return label
}

func targetSuffix(words []string) string {
	for _, word := range words {
		if word == "" {
			continue
		}
		name := path.Base(strings.TrimSuffix(word, "/..."))
		if name == "." || name == "..." || name == "/" {
			return ""
		}
		for _, suffix := range []string{".test.ts", ".test.tsx", ".test.js", ".spec.ts", ".spec.tsx", ".spec.js", "_test.go", "_test.py", ".py", ".ts", ".js"} {
			name = strings.TrimSuffix(name, suffix)
		}
		name = strings.TrimPrefix(name, "test_")
		if name == "" {
			return ""
		}
		return " " + name
	}
	return ""
}

func quoteLabel(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return `"` + text + `"`
}

func firstWords(words []string, n int) []string {
	if len(words) > n {
		return words[:n]
	}
	return words
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstOutcome(values ...Outcome) Outcome {
	for _, value := range values {
		if value != OutcomeUnknown {
			return value
		}
	}
	return OutcomeUnknown
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// scratchIfOutside reclassifies edits and runs that only touch temporary
// locations as scratch work.
func scratchIfOutside(action Action) Action {
	if action.Intent != IntentEdit || len(action.Paths) == 0 {
		return action
	}
	for _, p := range action.Paths {
		if !IsScratchPath(p) {
			return action
		}
	}
	action.Intent = IntentScratch
	return action
}

// literalPaths drops operands that only the shell can resolve (globs,
// variables, substitutions); they read as noise in a summary.
func literalPaths(paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := paths[:0]
	for _, p := range paths {
		if p == "" || strings.ContainsAny(p, "$*?{}`()") {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsScratchPath reports whether a path lives in a throwaway location.
func IsScratchPath(p string) bool {
	p = strings.TrimSpace(p)
	for _, prefix := range []string{"/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/", "/dev/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return p == "/tmp"
}

// mergeAdjacent folds consecutive same-intent edits (e.g. `mkdir x && cat > x/a`).
func mergeAdjacent(actions []Action) []Action {
	if len(actions) < 2 {
		return actions
	}
	out := actions[:1]
	for _, action := range actions[1:] {
		last := &out[len(out)-1]
		if last.Intent == action.Intent && (action.Intent == IntentEdit || action.Intent == IntentScratch || action.Intent == IntentRead) {
			for _, p := range action.Paths {
				last.Paths = appendUnique(last.Paths, p)
			}
			continue
		}
		out = append(out, action)
	}
	return out
}
