package browserctl

import (
	"testing"
	"time"
)

func managedObservation(projectPath, sessionID, loginURL string) Observation {
	return Observation{
		Ref: SessionRef{
			Provider:    "codex",
			ProjectPath: projectPath,
			SessionID:   sessionID,
		},
		Policy: Policy{
			ManagementMode:     ManagementModeManaged,
			DefaultBrowserMode: BrowserModeHeadless,
			LoginMode:          LoginModePromote,
			IsolationScope:     IsolationScopeTask,
		},
		Activity: SessionActivity{
			Policy:      Policy{ManagementMode: ManagementModeManaged, LoginMode: LoginModePromote},
			State:       SessionActivityStateWaitingForUser,
			ServerName:  "playwright",
			ToolName:    "browser_navigate",
			LastEventAt: time.Unix(10, 0),
		},
		LoginURL:  loginURL,
		UpdatedAt: time.Unix(10, 0),
	}
}

func TestControllerAcquireInteractiveBlocksSecondOwner(t *testing.T) {
	controller := NewController()
	controller.Observe(managedObservation("/tmp/a", "thread-a", "https://example.test/a"))
	controller.Observe(managedObservation("/tmp/b", "thread-b", "https://example.test/b"))

	first := controller.AcquireInteractive(SessionRef{Provider: "codex", ProjectPath: "/tmp/a", SessionID: "thread-a"})
	if !first.Granted {
		t.Fatalf("first acquire should succeed")
	}
	if first.Snapshot.Interactive == nil || first.Snapshot.Interactive.Ref.ProjectPath != "/tmp/a" {
		t.Fatalf("interactive owner = %#v, want /tmp/a", first.Snapshot.Interactive)
	}

	second := controller.AcquireInteractive(SessionRef{Provider: "codex", ProjectPath: "/tmp/b", SessionID: "thread-b"})
	if second.Granted {
		t.Fatalf("second acquire should be blocked while first is interactive")
	}
	if second.Owner == nil || second.Owner.Ref.ProjectPath != "/tmp/a" {
		t.Fatalf("blocking owner = %#v, want /tmp/a", second.Owner)
	}
}

func TestControllerObserveRemovesResolvedOwner(t *testing.T) {
	controller := NewController()
	obs := managedObservation("/tmp/a", "thread-a", "https://example.test/a")
	controller.Observe(obs)
	controller.AcquireInteractive(obs.Ref)

	resolved := obs
	resolved.Activity.State = SessionActivityStateIdle
	resolved.LoginURL = ""
	snapshot := controller.Observe(resolved)

	if snapshot.Interactive != nil {
		t.Fatalf("interactive owner should clear after resolution, got %#v", snapshot.Interactive)
	}
	if len(snapshot.Waiting) != 0 {
		t.Fatalf("waiting leases should clear after resolution, got %#v", snapshot.Waiting)
	}
}

func TestControllerReleaseInteractiveReturnsLeaseToWaiting(t *testing.T) {
	controller := NewController()
	obs := managedObservation("/tmp/a", "thread-a", "https://example.test/a")
	controller.Observe(obs)
	controller.AcquireInteractive(obs.Ref)

	snapshot := controller.ReleaseInteractive(obs.Ref)
	if snapshot.Interactive != nil {
		t.Fatalf("interactive owner should clear after release, got %#v", snapshot.Interactive)
	}
	if len(snapshot.Waiting) != 1 {
		t.Fatalf("waiting leases = %d, want 1", len(snapshot.Waiting))
	}
	if snapshot.Waiting[0].Ref.ProjectPath != "/tmp/a" {
		t.Fatalf("waiting lease project = %q, want /tmp/a", snapshot.Waiting[0].Ref.ProjectPath)
	}
}
