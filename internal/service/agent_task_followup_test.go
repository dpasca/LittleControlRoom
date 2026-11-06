package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfirmedAcceptedFollowUpPreservesHistoryAndEdits(t *testing.T) {
	for _, mode := range []string{"explicit followup", "ordinary launch", "changed checkout"} {
		t.Run(mode, func(t *testing.T) {
			f := newCorrectionFixture(t)
			path := filepath.Join(f.task.Repository.Root, "accepted.txt")
			f.runWorker(t, 1, func() {
				if err := os.WriteFile(path, []byte("accepted work"), 0600); err != nil {
					t.Fatal(err)
				}
			}, ready("accepted"))
			f.review(t, "accept")
			expected := f.task
			before, err := f.svc.GetAgentTaskResult(t.Context(), expected.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed checkout" {
				if err := os.WriteFile(path, []byte("intervening edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			unlock, err := f.svc.BeginConfirmedTaskTurn(expected, "next-host-key", mode != "ordinary launch")
			if mode != "explicit followup" {
				if err == nil {
					unlock()
					t.Fatal("crossed an unauthorized result boundary")
				}
				if mode == "changed checkout" && !strings.Contains(err.Error(), "checkout changed since accepted revision") {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			current := f.reload(t)
			if current.Workflow.RunID != 2 || current.Workflow.Result != nil || current.Workflow.Review != nil || !current.Workflow.SupervisionRevoked || current.Repository.SessionKey != "next-host-key" {
				t.Fatalf("incorrect followup: %+v", current)
			}
			after, err := f.svc.GetAgentTaskResult(t.Context(), expected.ID, 1)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("accepted history changed: before %+v after %+v %v", before, after, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "accepted work" {
				t.Fatalf("accepted edits changed: %q %v", data, err)
			}
		})
	}
}

func TestExplicitStopInvalidatesQueuedTaskLaunch(t *testing.T) {
	f := newCorrectionFixture(t)
	queued := f.task
	stopped, err := f.svc.CancelAgentTaskRun(t.Context(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Workflow.Phase != "canceled" || stopped.Status != "waiting" {
		t.Fatalf("stop not durable: %+v", stopped)
	}
	if unlock, err := f.svc.BeginConfirmedTaskTurn(queued, "stale-launch", false); err == nil {
		unlock()
		t.Fatal("queued launch revived a stopped task")
	}
	current := f.reload(t)
	if current.Repository.State == "held" || current.Workflow.RunID != queued.Workflow.RunID {
		t.Fatalf("stale launch changed ownership: %+v", current)
	}
}
