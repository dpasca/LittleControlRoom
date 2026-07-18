package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestManagedTurnContextIncludesWorkspaceContract(t *testing.T) {
	session := &appServerSession{
		workspaceContract: WorkspaceContract{
			AssignedPath:       "/repos/demo--feature",
			RepositoryRootPath: "/repos/demo",
			ExpectedRootBranch: "master",
		},
	}
	entries := session.managedTurnContext()
	entry, ok := entries[managedWorkspaceContextSource]
	if !ok {
		t.Fatalf("managedTurnContext() = %#v, want workspace entry", entries)
	}
	for _, required := range []string{"/repos/demo--feature", "/repos/demo", "master", "warning, not enforcing"} {
		if !strings.Contains(entry.Value, required) {
			t.Fatalf("workspace context missing %q: %q", required, entry.Value)
		}
	}
}

func TestCommandFromCanonicalRootWarnsAndReportsExcursionOnce(t *testing.T) {
	reported := make(chan WorkspaceExcursion, 2)
	session := &appServerSession{
		projectPath: "/repos/demo--feature",
		threadID:    "thread-1",
		entryIndex:  make(map[string]int),
		notify:      func() {},
		workspaceContract: WorkspaceContract{
			AssignedPath:       "/repos/demo--feature",
			RepositoryRootPath: "/repos/demo",
			ExpectedRootBranch: "master",
		},
		workspaceExcursionHandler: func(excursion WorkspaceExcursion) {
			reported <- excursion
		},
		workspaceExcursionItems: make(map[string]struct{}),
	}
	started := json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":"turn-1",
		"item":{"id":"cmd-1","type":"commandExecution","command":"git status","cwd":"/repos/demo","status":"inProgress"}
	}`)
	session.handleItemStarted(started)
	session.handleItemCompleted(json.RawMessage(`{
		"threadId":"thread-1",
		"turnId":"turn-1",
		"item":{"id":"cmd-1","type":"commandExecution","command":"git status","cwd":"/repos/demo","status":"completed","exitCode":0}
	}`))

	select {
	case excursion := <-reported:
		if excursion.ProjectPath != "/repos/demo--feature" || excursion.RepositoryRootPath != "/repos/demo" || excursion.CWD != "/repos/demo" {
			t.Fatalf("excursion = %#v", excursion)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace excursion was not reported")
	}
	select {
	case duplicate := <-reported:
		t.Fatalf("duplicate excursion reported: %#v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}

	snapshot := session.Snapshot()
	if snapshot.Status != "Workspace boundary warning" {
		t.Fatalf("status = %q, want workspace warning", snapshot.Status)
	}
	foundNotice := false
	for _, entry := range snapshot.Entries {
		if entry.Kind == TranscriptSystem && strings.Contains(entry.Text, "Workspace boundary warning") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("transcript = %#v, want workspace boundary system notice", snapshot.Entries)
	}
}

func TestCommandInsideAssignedWorktreeDoesNotReportExcursion(t *testing.T) {
	reported := make(chan WorkspaceExcursion, 1)
	session := &appServerSession{
		threadID:   "thread-1",
		entryIndex: make(map[string]int),
		notify:     func() {},
		workspaceContract: WorkspaceContract{
			AssignedPath:       "/repos/demo--feature",
			RepositoryRootPath: "/repos/demo",
		},
		workspaceExcursionHandler: func(excursion WorkspaceExcursion) { reported <- excursion },
	}
	session.handleItemStarted(json.RawMessage(`{
		"threadId":"thread-1",
		"item":{"id":"cmd-2","type":"commandExecution","command":"git status","cwd":"/repos/demo--feature/subdir"}
	}`))
	select {
	case excursion := <-reported:
		t.Fatalf("unexpected excursion: %#v", excursion)
	case <-time.After(50 * time.Millisecond):
	}
}
