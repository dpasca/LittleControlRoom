package codexapp

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
)

type testSubagentProgressReader func(string, string, time.Time) ([]claudeartifact.SubagentProgress, error)

func (r testSubagentProgressReader) Read(file, id string, since time.Time) ([]claudeartifact.SubagentProgress, error) {
	return r(file, id, since)
}

func TestClaudeSubagentProgressRefreshDoesNotBlockSnapshotsOrChangeTurnOwnership(t *testing.T) {
	started, release, notified := make(chan struct{}), make(chan struct{}), make(chan struct{}, 4)
	var calls atomic.Int32
	now := time.Now()
	session := &claudeCodeSession{
		sessionFile: "/unused/parent.jsonl", sessionID: "parent", busy: true,
		latestTurnStartedAt: now.Add(-5 * time.Hour), latestTurnStateKnown: true,
		busySince: now.Add(-5 * time.Hour),
		notify:    func() { notified <- struct{}{} },
		subagentProgressReader: testSubagentProgressReader(func(string, string, time.Time) ([]claudeartifact.SubagentProgress, error) {
			calls.Add(1)
			close(started)
			<-release
			return []claudeartifact.SubagentProgress{{ID: "worker", Description: "Phone profiling", UpdatedAt: now, LatestAction: "Bash: Measure phone"}}, nil
		}),
	}
	t.Cleanup(func() { // Stops the re-armed poll for the unfinished worker.
		session.mu.Lock()
		session.closed = true
		session.mu.Unlock()
	})
	session.StateSnapshot()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no async refresh")
	}
	for range 3 {
		snapshot, ok := session.TryStateSnapshot()
		if !ok || !snapshot.Busy || len(snapshot.Subagents) != 0 {
			t.Fatalf("reader blocked snapshots: %#v, %v", snapshot, ok)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("refreshes were not coalesced")
	}
	close(release)
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("no refresh notification")
	}
	snapshot := session.StateSnapshot()
	if len(snapshot.Subagents) != 1 || !snapshot.Busy || snapshot.LatestTurnCompleted || !snapshot.LastActivityAt.IsZero() {
		t.Fatalf("child progress changed parent ownership/activity: %#v", snapshot)
	}
	snapshot.Subagents[0].Description = "mutated snapshot"
	if session.StateSnapshot().Subagents[0].Description != "Phone profiling" {
		t.Fatal("snapshot shares mutable child state")
	}
	if calls.Load() != 1 {
		t.Fatal("unchanged snapshot ignored refresh interval")
	}
	session.mu.Lock()
	session.latestTurnStartedAt = now // A queued follow-up must not hide current work.
	session.mu.Unlock()
	if len(session.StateSnapshot().Subagents) != 1 || calls.Load() != 1 {
		t.Fatal("queued follow-up discarded the current worker")
	}
}

func TestClaudeSubagentProgressPollsUnfinishedChildrenWithoutSnapshots(t *testing.T) {
	now := time.Now()
	var calls atomic.Int32
	notified := make(chan struct{}, 8)
	session := &claudeCodeSession{
		sessionFile: "/unused/parent.jsonl", sessionID: "parent", busy: true,
		latestTurnStartedAt: now.Add(-time.Hour), busySince: now.Add(-time.Hour),
		subagentProgressPollInterval: 10 * time.Millisecond,
		notify:                       func() { notified <- struct{}{} },
		subagentProgressReader: testSubagentProgressReader(func(string, string, time.Time) ([]claudeartifact.SubagentProgress, error) {
			// The quiet parent never asks for another snapshot; only the
			// unfinished child can bring this second read.
			return []claudeartifact.SubagentProgress{{ID: "worker", UpdatedAt: now, Completed: calls.Add(1) > 1}}, nil
		}),
	}
	session.StateSnapshot()
	for range 2 {
		select {
		case <-notified:
		case <-time.After(time.Second):
			t.Fatalf("child progress was not re-polled; reads=%d", calls.Load())
		}
	}
	session.mu.Lock()
	completed := len(session.subagentProgress) == 1 && session.subagentProgress[0].Completed
	session.mu.Unlock()
	if !completed {
		t.Fatal("completion from the child log was not published")
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("completed children kept polling: %d reads", calls.Load())
	}
}

func TestClaudeSubagentProgressRejectsPreviousTurnAndReportsReadFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	now := time.Now()
	session := &claudeCodeSession{sessionFile: "/unused/parent.jsonl", sessionID: "parent", busy: true, latestTurnStartedAt: now.Add(-time.Hour)}
	session.subagentProgressReader = testSubagentProgressReader(func(string, string, time.Time) ([]claudeartifact.SubagentProgress, error) {
		close(started)
		<-release
		return []claudeartifact.SubagentProgress{{ID: "old", UpdatedAt: now}}, nil
	})
	session.StateSnapshot()
	<-started
	session.mu.Lock()
	session.latestTurnStartedAt = now
	session.mu.Unlock()
	session.StateSnapshot() // New turn invalidates the old read while it is in flight.
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		session.mu.Lock()
		refreshing := session.subagentProgressRefreshing
		if !refreshing {
			if len(session.subagentProgress) != 0 {
				t.Fatal("previous turn was published")
			}
			session.subagentProgressReader = testSubagentProgressReader(func(string, string, time.Time) ([]claudeartifact.SubagentProgress, error) {
				return nil, errors.New("permission denied")
			})
			session.subagentProgressRefreshAt = time.Time{}
		}
		session.mu.Unlock()
		if !refreshing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	notified := make(chan struct{}, 1)
	session.notify = func() { notified <- struct{}{} }
	session.StateSnapshot()
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("no error notification")
	}
	if got := session.StateSnapshot(); !strings.Contains(got.SubagentProgressError, "permission denied") || !got.Busy {
		t.Fatalf("missing explicit read failure: %#v", got)
	}
}
