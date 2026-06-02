package procinspect

import (
	"strings"
	"testing"
	"time"
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

func TestProjectInstanceFromProcessDetectsProjectLocalListener(t *testing.T) {
	process := Process{
		PID:     4017,
		PPID:    700,
		PGID:    700,
		CWD:     "/Users/davide/dev/repos/portfolio",
		Command: "vite --host 127.0.0.1",
		Ports:   []int{4017},
	}

	instance, ok := projectInstanceFromProcess(process, "/Users/davide/dev/repos/portfolio", map[int]int{4017: 700, 700: 42}, 42, nil, nil)

	if !ok {
		t.Fatalf("projectInstanceFromProcess() ok = false, want true")
	}
	if instance.ProjectPath != "/Users/davide/dev/repos/portfolio" {
		t.Fatalf("ProjectPath = %q, want portfolio path", instance.ProjectPath)
	}
	if !instance.OwnedByCurrentApp {
		t.Fatalf("OwnedByCurrentApp = false, want true for descendant process")
	}
	if len(instance.Ports) != 1 || instance.Ports[0] != 4017 {
		t.Fatalf("Ports = %v, want [4017]", instance.Ports)
	}
}

func TestProjectInstanceFromProcessSkipsManagedRuntime(t *testing.T) {
	process := Process{
		PID:   4017,
		PPID:  700,
		PGID:  700,
		CWD:   "/Users/davide/dev/repos/portfolio",
		Ports: []int{4017},
	}

	if _, ok := projectInstanceFromProcess(process, "/Users/davide/dev/repos/portfolio", map[int]int{4017: 700}, 999, nil, map[int]struct{}{700: {}}); ok {
		t.Fatalf("projectInstanceFromProcess() ok = true, want false for managed process group")
	}
}

func TestAppendExpectedPortFindingsFlagsExternalOwner(t *testing.T) {
	target := "/Users/davide/dev/repos/FraactalMech"
	owner := "/Users/davide/dev/repos/OtherApp"
	reports := map[string]*ProjectReport{
		target: {ProjectPath: target},
		owner:  {ProjectPath: owner},
	}
	process := Process{
		PID:     301,
		PPID:    44,
		PGID:    301,
		CPU:     0.2,
		Command: "node server.js",
		CWD:     owner,
		Ports:   []int{3001},
	}

	appendExpectedPortFindings(reports, map[int]Process{301: process}, cleanProjectPaths([]string{target, owner}), map[int]int{301: 44}, 999, ScanOptions{
		ExpectedPorts: []ExpectedPort{{ProjectPath: target, Port: 3001}},
	})

	findings := reports[target].Findings
	if len(findings) != 1 {
		t.Fatalf("findings len = %d, want 1: %#v", len(findings), findings)
	}
	finding := findings[0]
	if !finding.PortConflict {
		t.Fatalf("PortConflict = false, want true")
	}
	if finding.OwnerProjectPath != owner {
		t.Fatalf("OwnerProjectPath = %q, want %q", finding.OwnerProjectPath, owner)
	}
	if len(finding.ConflictPorts) != 1 || finding.ConflictPorts[0] != 3001 {
		t.Fatalf("ConflictPorts = %v, want [3001]", finding.ConflictPorts)
	}
	joined := strings.Join(finding.Reasons, ",")
	if !strings.Contains(joined, "port 3001 busy by another project") {
		t.Fatalf("reasons = %#v, want port conflict reason", finding.Reasons)
	}
}

func TestAppendExpectedPortFindingsIgnoresSameManagedProject(t *testing.T) {
	target := "/Users/davide/dev/repos/FraactalMech"
	reports := map[string]*ProjectReport{
		target: {ProjectPath: target},
	}
	process := Process{
		PID:   301,
		PPID:  44,
		PGID:  301,
		CWD:   target,
		Ports: []int{3001},
	}

	appendExpectedPortFindings(reports, map[int]Process{301: process}, cleanProjectPaths([]string{target}), map[int]int{301: 44}, 999, ScanOptions{
		ExpectedPorts:      []ExpectedPort{{ProjectPath: target, Port: 3001}},
		ManagedPIDs:        map[int]struct{}{301: {}},
		ManagedPIDProjects: map[int]string{301: target},
	})

	if len(reports[target].Findings) != 0 {
		t.Fatalf("findings = %#v, want none for same managed runtime", reports[target].Findings)
	}
}

func TestBuildCPUSnapshotSortsAndAnnotatesTopProcesses(t *testing.T) {
	now := time.Now()
	snapshot := buildCPUSnapshot([]Process{{
		PID:     100,
		PPID:    1,
		PGID:    100,
		CPU:     2.5,
		Mem:     0.1,
		Command: "quiet",
	}, {
		PID:     200,
		PPID:    999,
		PGID:    200,
		CPU:     87.2,
		Mem:     1.3,
		Command: "/usr/local/bin/node server.js",
	}, {
		PID:     300,
		PPID:    200,
		PGID:    200,
		CPU:     45.0,
		Mem:     0.4,
		Command: "worker",
	}}, map[int]string{
		200: "/Users/davide/dev/repos/ChatNext3/server",
	}, CPUScanOptions{
		ProjectPaths: []string{"/Users/davide/dev/repos/ChatNext3"},
		ManagedPGIDs: map[int]struct{}{200: {}},
		OwnPID:       999,
		Limit:        2,
		Now:          now,
	})

	if len(snapshot.Processes) != 2 {
		t.Fatalf("snapshot processes len = %d, want 2", len(snapshot.Processes))
	}
	if snapshot.TotalCPU != 134.7 {
		t.Fatalf("total CPU = %.1f, want 134.7", snapshot.TotalCPU)
	}
	top := snapshot.Processes[0]
	if top.PID != 200 || top.ProjectPath != "/Users/davide/dev/repos/ChatNext3" {
		t.Fatalf("top process = PID %d project %q, want PID 200 project path", top.PID, top.ProjectPath)
	}
	if !top.ManagedRuntime || !top.OwnedByCurrentApp {
		t.Fatalf("top annotations managed=%t owned=%t, want both true", top.ManagedRuntime, top.OwnedByCurrentApp)
	}
	joined := strings.Join(top.Reasons, ",")
	for _, want := range []string{"high CPU", "spawned by LCR", "managed runtime", "project-local"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons = %#v, want %q", top.Reasons, want)
		}
	}
	if !snapshot.ScannedAt.Equal(now) {
		t.Fatalf("scannedAt = %v, want %v", snapshot.ScannedAt, now)
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
