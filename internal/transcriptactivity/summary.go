package transcriptactivity

import (
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
)

// FileTouch is one edited file and how many separate edits touched it.
type FileTouch struct {
	Path  string
	Count int
}

// Run is a test or check invocation, keyed by label. Reruns fold into one Run
// that reports the latest outcome.
type Run struct {
	Label   string
	Outcome Outcome
	Detail  string
	Times   int
}

// Summary aggregates the actions of one step: the tool traffic between two
// pieces of engineer prose.
type Summary struct {
	Edited       []FileTouch
	UnnamedEdits int
	ReadPaths    []string
	Reads        int
	Searches     []string
	SearchCount  int
	Tests        []Run
	Checks       []Run
	Git          []string
	Web          []string
	WebCount     int
	Delegated    []string
	MCP          []string
	MCPCount     int
	Scratch      int
	Runs         []string
	RunCount     int
	// Failures lists failed commands outside tests and checks, which already
	// carry their own outcome.
	Failures []Action
	// Running is the in-flight action at the tail of a live step, if any. It is
	// excluded from the aggregates above.
	Running *Action
	Actions int
}

type Options struct {
	// LiveTail marks the step as the newest activity of a busy session, so a
	// trailing unfinished action is reported as Running.
	LiveTail bool
	// ProjectPath lets absolute and project-relative spellings of the same
	// file aggregate together.
	ProjectPath string
}

// Empty reports whether the step produced nothing worth showing.
func (s Summary) Empty() bool {
	return s.Actions == 0 && s.Running == nil
}

type actionSlot struct {
	actions []Action
	// pendingCommand is set for provider tool calls whose shell result arrives
	// as a later command entry (Claude Code's Bash), so the pair counts once.
	pendingCommand string
}

// Summarize aggregates the actions of consecutive activity entries.
func Summarize(entries []codexapp.TranscriptEntry, options Options) Summary {
	slots := make([]actionSlot, 0, len(entries))
	for _, entry := range entries {
		if !ActivityKind(entry.Kind) {
			continue
		}
		if entry.Kind == codexapp.TranscriptCommand {
			if command := strings.TrimSpace(entry.CommandText); command != "" {
				for i := range slots {
					pending := slots[i].pendingCommand
					if pending != "" && strings.HasPrefix(command, pending) {
						slots[i] = actionSlot{}
						break
					}
				}
			}
		}
		slot := actionSlot{actions: Classify(entry)}
		if entry.Kind == codexapp.TranscriptTool && isShellToolName(entry.ToolName) {
			tool := parseToolLine(entry)
			slot.pendingCommand = strings.TrimSpace(strings.TrimSuffix(tool.summary, "..."))
		}
		slots = append(slots, slot)
	}

	var actions []Action
	for _, slot := range slots {
		actions = append(actions, slot.actions...)
	}
	var summary Summary
	if options.LiveTail && len(actions) > 0 && actions[len(actions)-1].Outcome == OutcomeRunning {
		running := actions[len(actions)-1]
		summary.Running = &running
		actions = actions[:len(actions)-1]
	}
	for _, action := range actions {
		action.Paths = normalizePaths(action.Paths, options.ProjectPath)
		summary.add(action)
	}
	return summary
}

func normalizePaths(paths []string, projectPath string) []string {
	if len(paths) == 0 {
		return paths
	}
	projectPath = strings.TrimSpace(projectPath)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = filepath.Clean(strings.TrimSpace(p))
		if projectPath != "" && filepath.IsAbs(p) {
			if rel, err := filepath.Rel(projectPath, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				p = rel
			}
		}
		out = appendUnique(out, p)
	}
	return out
}

func isShellToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "shell":
		return true
	default:
		return false
	}
}

func (s *Summary) add(action Action) {
	s.Actions++
	failed := action.Outcome == OutcomeFailed
	switch action.Intent {
	case IntentEdit, IntentGit, IntentRun, IntentWeb, IntentDelegate, IntentMCP:
		if failed {
			// Reported once, as a failure, rather than also as done work.
			s.Failures = append(s.Failures, action)
			return
		}
	}
	switch action.Intent {
	case IntentEdit:
		if len(action.Paths) == 0 {
			s.UnnamedEdits += max(1, action.Count)
		}
		for _, p := range action.Paths {
			s.touch(p)
		}
	case IntentRead:
		s.Reads++
		for _, p := range action.Paths {
			s.ReadPaths = appendUnique(s.ReadPaths, p)
		}
		failed = false // a missing file or empty match is information, not failure
	case IntentSearch:
		s.SearchCount++
		s.Searches = appendUnique(s.Searches, action.Label)
		failed = false // grep exits 1 when nothing matches
	case IntentTest:
		s.Tests = addRun(s.Tests, action)
		failed = false
	case IntentCheck:
		s.Checks = addRun(s.Checks, action)
		failed = false
	case IntentGit:
		s.Git = appendUnique(s.Git, action.Label)
	case IntentWeb:
		s.WebCount++
		s.Web = appendUnique(s.Web, action.Label)
	case IntentDelegate:
		s.Delegated = appendUnique(s.Delegated, action.Label)
	case IntentMCP:
		s.MCPCount++
		s.MCP = appendUnique(s.MCP, action.Label)
	case IntentScratch:
		s.Scratch++
		failed = false // probes are expected to fail sometimes
	default:
		s.RunCount++
		s.Runs = appendUnique(s.Runs, action.Label)
	}
	if failed {
		s.Failures = append(s.Failures, action)
	}
}

func (s *Summary) touch(p string) {
	for i := range s.Edited {
		if s.Edited[i].Path == p {
			s.Edited[i].Count++
			return
		}
	}
	s.Edited = append(s.Edited, FileTouch{Path: p, Count: 1})
}

func addRun(runs []Run, action Action) []Run {
	for i := range runs {
		if runs[i].Label == action.Label {
			runs[i].Times++
			if action.Outcome != OutcomeUnknown && action.Outcome != OutcomeRunning {
				runs[i].Outcome = action.Outcome
				runs[i].Detail = action.Detail
			}
			return runs
		}
	}
	outcome := action.Outcome
	if outcome == OutcomeRunning {
		outcome = OutcomeUnknown
	}
	return append(runs, Run{Label: action.Label, Outcome: outcome, Detail: action.Detail, Times: 1})
}
