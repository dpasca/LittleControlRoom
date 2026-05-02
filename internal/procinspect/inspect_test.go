package procinspect

import (
	"strings"
	"testing"
)

func TestParsePSOutput(t *testing.T) {
	processes := parsePSOutput(`
  101     1   101 R    98.5  0.2  02-14:25:41 /opt/homebrew/bin/node -r ts-node-dev src/index.ts
  202   101   101 S     0.0  0.1       01:03 helper process
`)

	if len(processes) != 2 {
		t.Fatalf("len(processes) = %d, want 2", len(processes))
	}
	first := processes[0]
	if first.PID != 101 || first.PPID != 1 || first.PGID != 101 {
		t.Fatalf("ids = %d/%d/%d, want 101/1/101", first.PID, first.PPID, first.PGID)
	}
	if first.CPU != 98.5 || first.Mem != 0.2 {
		t.Fatalf("usage = %.1f/%.1f, want 98.5/0.2", first.CPU, first.Mem)
	}
	if !strings.Contains(first.Command, "ts-node-dev") {
		t.Fatalf("command = %q, want ts-node-dev", first.Command)
	}
}

func TestParseLsofCWDOutput(t *testing.T) {
	cwds := parseLsofCWDOutput(`
p101
cnode
n/Users/davide/dev/repos/ChatNext3/server
p202
czsh
n/Users/davide/dev/repos/LittleControlRoom
`)

	if got := cwds[101]; got != "/Users/davide/dev/repos/ChatNext3/server" {
		t.Fatalf("cwd[101] = %q", got)
	}
	if got := cwds[202]; got != "/Users/davide/dev/repos/LittleControlRoom" {
		t.Fatalf("cwd[202] = %q", got)
	}
}

func TestClassifyProcessFindsOrphanedHighCPUProjectProcess(t *testing.T) {
	process := Process{
		PID:     101,
		PPID:    1,
		PGID:    101,
		CPU:     98.5,
		CWD:     "/Users/davide/dev/repos/ChatNext3/server",
		Command: "node -r ts-node-dev src/index.ts",
	}

	finding := classifyProcess(process, "/Users/davide/dev/repos/ChatNext3", map[int]int{101: 1}, 999, nil, nil, 50, 1)

	if len(finding.Reasons) != 2 {
		t.Fatalf("reasons = %#v, want orphaned and high CPU", finding.Reasons)
	}
	if finding.ManagedRuntime {
		t.Fatalf("managed runtime = true, want false")
	}
	if finding.OwnedByCurrentApp {
		t.Fatalf("owned by current app = true, want false")
	}
}

func TestClassifyProcessIgnoresManagedRuntime(t *testing.T) {
	process := Process{
		PID:  101,
		PPID: 1,
		PGID: 101,
		CPU:  98.5,
		CWD:  "/Users/davide/dev/repos/ChatNext3/server",
	}

	finding := classifyProcess(process, "/Users/davide/dev/repos/ChatNext3", map[int]int{101: 1}, 999, nil, map[int]struct{}{101: {}}, 50, 1)

	if !finding.ManagedRuntime {
		t.Fatalf("managed runtime = false, want true")
	}
	if len(finding.Reasons) != 0 {
		t.Fatalf("reasons = %#v, want none for managed runtime", finding.Reasons)
	}
}

func TestClassifyProcessIgnoresQuietOrphanWithoutPorts(t *testing.T) {
	process := Process{
		PID:  101,
		PPID: 1,
		PGID: 101,
		CPU:  0.0,
		CWD:  "/Users/davide/dev/repos/ChatNext3/server",
	}

	finding := classifyProcess(process, "/Users/davide/dev/repos/ChatNext3", map[int]int{101: 1}, 999, nil, nil, 50, 1)

	if len(finding.Reasons) != 0 {
		t.Fatalf("reasons = %#v, want none for quiet orphan without ports", finding.Reasons)
	}
}

func TestDeepestProjectForPath(t *testing.T) {
	projects := cleanProjectPaths([]string{
		"/Users/davide/dev/repos",
		"/Users/davide/dev/repos/ChatNext3",
	})

	got, ok := deepestProjectForPath("/Users/davide/dev/repos/ChatNext3/server", projects)
	if !ok {
		t.Fatalf("deepestProjectForPath did not match")
	}
	if got != "/Users/davide/dev/repos/ChatNext3" {
		t.Fatalf("project = %q, want ChatNext3", got)
	}
}

func TestProcessDescendsFrom(t *testing.T) {
	ppids := map[int]int{400: 300, 300: 200, 200: 100, 100: 1}
	if !processDescendsFrom(400, 200, ppids) {
		t.Fatalf("process should descend from ancestor")
	}
	if processDescendsFrom(400, 999, ppids) {
		t.Fatalf("process should not descend from unrelated pid")
	}
}
