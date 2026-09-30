package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func TestClaudeMessageDeliveryRemainsVisibleWithActiveWorker(t *testing.T) {
	now := time.Now()
	s := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, Started: true,
		Phase: codexapp.SessionPhaseRunning, BackgroundInputSupported: true,
		ParentStatus: "Parent available · background work running", ParentActivityAt: now.Add(-time.Minute),
		BackgroundTasks:   []codexapp.BackgroundTaskSnapshot{{ID: "worker"}},
		MessageDeliveries: []codexapp.MessageDeliverySnapshot{{ID: "message", Preview: "Steer the phone test", State: "queued", SubmittedAt: now.Add(-3 * time.Minute)}},
	}
	m := Model{codexInput: newCodexTextarea()}
	text := ansi.Strip(strings.Join(m.codexLowerBlocks(s, 120), "\n"))
	for _, want := range []string{"Parent available", "last parent activity", "message waiting", "parent has not started", "Steer the phone test"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, width := range []int{35, 60} {
		for _, row := range m.renderCodexMessageDelivery(s, width) {
			if ansi.StringWidth(row) > width {
				t.Fatalf("overflow: %q", row)
			}
		}
	}
	if !codexSnapshotCanSubmitBusyInput(s) {
		t.Fatal("worker blocked composer")
	}
	if availability := codexapp.DescribeSessionInput(s); !availability.Available || availability.Mode != codexapp.SessionInputQueue {
		t.Fatalf("mobile input: %#v", availability)
	}
	cached := overlayCodexSnapshotState(codexapp.Snapshot{}, s)
	s.MessageDeliveries[0].State = "started"
	if cached.MessageDeliveries[0].State != "queued" || cached.ParentStatus != s.ParentStatus || !cached.BackgroundInputSupported {
		t.Fatal("cached receipt state lost or aliased")
	}
}
