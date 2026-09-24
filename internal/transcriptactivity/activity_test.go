package transcriptactivity

import (
	"reflect"
	"testing"

	"lcroom/internal/codexapp"
)

func TestClassifyCommandIntents(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []Action
	}{
		{
			name:    "python heredoc edit",
			command: "python3 - <<'EOF'\nimport re\np='src/animation/actions/clips.ts'\ns=open(p).read()\ns=s.replace('a','b')\nopen(p,'w').write(s)\nEOF",
			want:    []Action{{Intent: IntentEdit, Label: "python3 script", Paths: []string{"src/animation/actions/clips.ts"}}},
		},
		{
			name:    "python heredoc probe without writes",
			command: "python3 - <<'EOF'\nimport json\nprint(json.load(open('package.json'))['name'])\nEOF",
			want:    []Action{{Intent: IntentRun, Label: "python3 script"}},
		},
		{
			name:    "cat heredoc writes file",
			command: "cat > src/animation/resolvePose.ts <<'EOF'\nimport type { ActiveAction } from '../episode/types';\nEOF",
			want:    []Action{{Intent: IntentEdit, Paths: []string{"src/animation/resolvePose.ts"}}},
		},
		{
			name:    "grep pipeline is a search",
			command: `grep -rn "\.evaluate(" src/app src/modules | grep -v "evaluateEpisode" | head -30`,
			want:    []Action{{Intent: IntentSearch, Label: `"\.evaluate("`}},
		},
		{
			name:    "sed -n is a read",
			command: "sed -n 1,30p tests/avatar/avatarInstance.test.ts",
			want:    []Action{{Intent: IntentRead, Paths: []string{"tests/avatar/avatarInstance.test.ts"}}},
		},
		{
			name:    "vitest through pnpm with filters",
			command: `pnpm exec vitest run tests/animation/idleLayers.test.ts 2>&1 | grep -E "✓|×|FAIL" | head -30`,
			want:    []Action{{Intent: IntentTest, Label: "vitest idleLayers"}},
		},
		{
			name:    "bsd sed in-place then test",
			command: `sed -i '' 's/const A = 0.05;/const A = 0.08;/' src/idle.ts && pnpm exec vitest run tests/idle.test.ts`,
			want: []Action{
				{Intent: IntentEdit, Paths: []string{"src/idle.ts"}},
				{Intent: IntentTest, Label: "vitest idle"},
			},
		},
		{
			name:    "scratch probe under tmp",
			command: "mkdir -p /tmp/castlife && cat > /tmp/castlife/probe.test.ts <<'EOF'\nimport { test } from 'vitest';\nEOF",
			want:    []Action{{Intent: IntentScratch, Paths: []string{"/tmp/castlife/probe.test.ts"}}},
		},
		{
			name:    "relative work after cd into tmp is scratch",
			command: "cd /tmp/castlife/renders && for t in 1.0 4.5; do ffmpeg -v error -ss $t -i in.mp4 cmp/b.png; done && cp cmp/b.png out.png",
			want:    []Action{{Intent: IntentScratch, Label: "ffmpeg", Paths: []string{"/tmp/castlife/renders/out.png"}}},
		},
		{
			name:    "multi-line inline node script stays one statement",
			command: "node -e '\nconst fs=require(\"fs\");\nfs.writeFileSync(\"src/out/data.json\", \"{}\");\n'",
			want:    []Action{{Intent: IntentEdit, Label: "node script", Paths: []string{"src/out/data.json"}}},
		},
		{
			name:    "subshell, test builtin, and array append",
			command: "(pnpm typecheck) && [ -f x ] && args+=(-i $f) && $CMD run",
			want:    []Action{{Intent: IntentCheck, Label: "pnpm typecheck"}},
		},
		{
			name:    "glob operands are dropped",
			command: "cp before-*/*.mp4 final/",
			want:    []Action{{Intent: IntentEdit, Paths: []string{"final/"}}},
		},
		{
			name:    "cd prefix and go test",
			command: "cd /repo && go test ./internal/tui/...",
			want:    []Action{{Intent: IntentTest, Label: "go test tui"}},
		},
		{
			name:    "git with -C",
			command: "git -C /repo status --short",
			want:    []Action{{Intent: IntentGit, Label: "git status"}},
		},
		{
			name:    "make targets",
			command: "make scan",
			want:    []Action{{Intent: IntentCheck, Label: "make scan"}},
		},
		{
			name:    "env assignment and other program",
			command: "FOO=1 docker compose up -d",
			want:    []Action{{Intent: IntentRun, Label: "docker compose"}},
		},
		{
			name:    "or-chain is two statements, not a pipe",
			command: "ls src || true",
			want:    []Action{{Intent: IntentRead, Paths: []string{"src"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyCommand(tt.command)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ClassifyCommand(%q)\n got %#v\nwant %#v", tt.command, got, tt.want)
			}
		})
	}
}

func TestClassifyCommandEntryOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		entry       codexapp.TranscriptEntry
		wantOutcome Outcome
		wantDetail  string
	}{
		{
			name: "vitest pass summary",
			entry: codexapp.TranscriptEntry{
				Kind:        codexapp.TranscriptCommand,
				CommandText: "pnpm exec vitest run tests/idle.test.ts",
				Text:        "$ pnpm exec vitest run tests/idle.test.ts\n ✓ tests/idle.test.ts (3 tests)\n Test Files  1 passed (1)\n      Tests  3 passed (3)",
			},
			wantOutcome: OutcomeOK,
			wantDetail:  "3 passed",
		},
		{
			name: "vitest failure summary",
			entry: codexapp.TranscriptEntry{
				Kind:        codexapp.TranscriptCommand,
				CommandText: "pnpm exec vitest run",
				Text:        "$ pnpm exec vitest run\n      Tests  1 failed | 2 passed (3)",
				Failed:      true,
			},
			wantOutcome: OutcomeFailed,
			wantDetail:  "1 failed, 2 passed",
		},
		{
			name: "claude failed command exit code",
			entry: codexapp.TranscriptEntry{
				Kind:        codexapp.TranscriptCommand,
				CommandText: "make scan",
				Text:        "$ make scan\nExit code 2\nboom",
				Failed:      true,
			},
			wantOutcome: OutcomeFailed,
			wantDetail:  "exit 2",
		},
		{
			name: "codex completed with exit status",
			entry: codexapp.TranscriptEntry{
				Kind: codexapp.TranscriptCommand,
				Text: "$ go test ./...\n# cwd: /repo\nok  \tlcroom/internal/a\t0.1s\nok  \tlcroom/internal/b\t0.2s\n[command completed, exit 0]",
			},
			wantOutcome: OutcomeOK,
			wantDetail:  "2 packages ok",
		},
		{
			name: "codex failed exit status",
			entry: codexapp.TranscriptEntry{
				Kind: codexapp.TranscriptCommand,
				Text: "$ make doctor\nerror\n[command failed, exit 1]",
			},
			wantOutcome: OutcomeFailed,
			wantDetail:  "exit 1",
		},
		{
			name: "codex command still streaming",
			entry: codexapp.TranscriptEntry{
				Kind: codexapp.TranscriptCommand,
				Text: "$ make doctor\npartial output",
			},
			wantOutcome: OutcomeRunning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := Classify(tt.entry)
			if len(actions) == 0 {
				t.Fatalf("Classify returned no actions")
			}
			last := actions[len(actions)-1]
			if last.Outcome != tt.wantOutcome || last.Detail != tt.wantDetail {
				t.Fatalf("outcome = %q/%q, want %q/%q", last.Outcome, last.Detail, tt.wantOutcome, tt.wantDetail)
			}
		})
	}
}

func TestClassifyCodexHeredocCommandWithoutCommandText(t *testing.T) {
	actions := Classify(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptCommand,
		Text: "$ python3 - <<'PY'\np='internal/a/b.go'\nopen(p,'w').write('x')\nPY\n[command completed, exit 0]",
	})
	want := []Action{{Intent: IntentEdit, Label: "python3 script", Paths: []string{"internal/a/b.go"}, Outcome: OutcomeOK}}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %#v, want %#v", actions, want)
	}
}

func TestClassifyToolEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry codexapp.TranscriptEntry
		want  []Action
	}{
		{
			name:  "claude write",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Write: /repo/src/idleLayers.ts", ToolName: "Write", ToolPath: "/repo/src/idleLayers.ts"},
			want:  []Action{{Intent: IntentEdit, Paths: []string{"/repo/src/idleLayers.ts"}}},
		},
		{
			name:  "claude grep",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Grep: evaluate\\(", ToolName: "Grep"},
			want:  []Action{{Intent: IntentSearch, Label: `"evaluate\("`}},
		},
		{
			name:  "claude todo bookkeeping is hidden",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "TodoWrite", ToolName: "TodoWrite"},
			want:  nil,
		},
		{
			name:  "claude mcp",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "mcp__playwright__browser_click", ToolName: "mcp__playwright__browser_click"},
			want:  []Action{{Intent: IntentMCP, Label: "playwright browser_click"}},
		},
		{
			name:  "claude bash call before its result",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Bash: go test ./internal/tui", ToolName: "Bash"},
			want:  []Action{{Intent: IntentTest, Label: "go test tui", Outcome: OutcomeRunning}},
		},
		{
			name:  "codex mcp tool",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "MCP tool lcr_runtime/list_processes [completed]"},
			want:  []Action{{Intent: IntentMCP, Label: "lcr_runtime list_processes", Outcome: OutcomeOK}},
		},
		{
			name:  "opencode bash with description",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: Run unit tests"},
			want:  []Action{{Intent: IntentRun, Label: "Run unit tests", Outcome: OutcomeOK}},
		},
		{
			name:  "opencode edit error",
			entry: codexapp.TranscriptEntry{Kind: codexapp.TranscriptTool, Text: "Tool edit error: app.go"},
			want:  []Action{{Intent: IntentEdit, Paths: []string{"app.go"}, Outcome: OutcomeFailed}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.entry)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Classify\n got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestClassifyFileChangeEntries(t *testing.T) {
	tests := []struct {
		text string
		want Action
	}{
		{"Patch touched a.go, b/c.go", Action{Intent: IntentEdit, Paths: []string{"a.go", "b/c.go"}, Outcome: OutcomeOK}},
		{"Files touched:\nx.go\ny.go", Action{Intent: IntentEdit, Paths: []string{"x.go", "y.go"}, Outcome: OutcomeOK}},
		{"Applying 3 file change(s)\n[file changes completed]", Action{Intent: IntentEdit, Count: 3, Outcome: OutcomeOK}},
		{"Success. Updated the following files:\nM internal/tui/app.go\nA internal/new.go", Action{Intent: IntentEdit, Paths: []string{"internal/tui/app.go", "internal/new.go"}, Outcome: OutcomeOK}},
	}
	for _, tt := range tests {
		got := Classify(codexapp.TranscriptEntry{Kind: codexapp.TranscriptFileChange, Text: tt.text})
		if !reflect.DeepEqual(got, []Action{tt.want}) {
			t.Fatalf("Classify(%q)\n got %#v\nwant %#v", tt.text, got, tt.want)
		}
	}
}

func TestSummarizePairsClaudeBashCallsWithResults(t *testing.T) {
	longCommand := "python3 - <<'EOF'\np='src/app/preview/officeSharedDesk.ts'\ns=open(p).read()\nopen(p,'w').write(s.replace('a','b'))\nEOF"
	entries := []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptTool, Text: "Write: /repo/src/idleLayers.ts", ToolName: "Write", ToolPath: "/repo/src/idleLayers.ts"},
		{Kind: codexapp.TranscriptTool, Text: "Bash: " + longCommand[:40] + "...", ToolName: "Bash"},
		{Kind: codexapp.TranscriptCommand, Text: "$ " + longCommand + "\n[command completed]", CommandText: longCommand},
		{Kind: codexapp.TranscriptTool, Text: "Bash: grep -rn foo src", ToolName: "Bash"},
		{Kind: codexapp.TranscriptCommand, Text: "$ grep -rn foo src\nExit code 1", CommandText: "grep -rn foo src", Failed: true},
		{Kind: codexapp.TranscriptTool, Text: "Bash: pnpm exec vitest run tests/idle.test.ts", ToolName: "Bash"},
		{Kind: codexapp.TranscriptCommand, Text: "$ pnpm exec vitest run tests/idle.test.ts\n      Tests  2 failed | 1 passed (3)", CommandText: "pnpm exec vitest run tests/idle.test.ts", Failed: true},
		{Kind: codexapp.TranscriptTool, Text: "Bash: make scan", ToolName: "Bash"},
		{Kind: codexapp.TranscriptCommand, Text: "$ make lint\nExit code 2", CommandText: "make lint", Failed: true},
		{Kind: codexapp.TranscriptTool, Text: "Bash: ./scripts/deploy.sh --dry-run", ToolName: "Bash"},
		{Kind: codexapp.TranscriptCommand, Text: "$ ./scripts/deploy.sh --dry-run\nExit code 3", CommandText: "./scripts/deploy.sh --dry-run", Failed: true},
		{Kind: codexapp.TranscriptTool, Text: "Bash: go test ./...", ToolName: "Bash"},
	}
	summary := Summarize(entries, Options{LiveTail: true})
	wantEdited := []FileTouch{{Path: "/repo/src/idleLayers.ts", Count: 1}, {Path: "src/app/preview/officeSharedDesk.ts", Count: 1}}
	if !reflect.DeepEqual(summary.Edited, wantEdited) {
		t.Fatalf("Edited = %#v, want %#v", summary.Edited, wantEdited)
	}
	if summary.SearchCount != 1 || len(summary.Failures) != 1 || summary.Failures[0].Label != "deploy.sh" || summary.Failures[0].Detail != "exit 3" {
		t.Fatalf("searches/failures = %d/%#v; grep no-match must not count as a failure", summary.SearchCount, summary.Failures)
	}
	wantTests := []Run{{Label: "vitest idle", Outcome: OutcomeFailed, Detail: "2 failed, 1 passed", Times: 1}}
	if !reflect.DeepEqual(summary.Tests, wantTests) {
		t.Fatalf("Tests = %#v, want %#v", summary.Tests, wantTests)
	}
	// "make scan" never got its own result, so it still counts once.
	if len(summary.Checks) != 2 {
		t.Fatalf("Checks = %#v, want unmatched make scan plus make lint", summary.Checks)
	}
	if summary.Running == nil || summary.Running.Label != "go test" {
		t.Fatalf("Running = %#v, want trailing go test", summary.Running)
	}

	idle := Summarize(entries, Options{})
	if idle.Running != nil || len(idle.Tests) != 2 {
		t.Fatalf("idle summary should fold the trailing call into aggregates: running=%#v tests=%#v", idle.Running, idle.Tests)
	}
}
